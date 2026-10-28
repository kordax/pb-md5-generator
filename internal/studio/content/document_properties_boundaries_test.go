package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateDocumentPropertiesUsesSharedUTF8AndOutputBudgets(t *testing.T) {
	longOutput := strings.Repeat("a/", 2045) + "a.html"
	require.Len(t, longOutput, 4096)
	for _, kind := range []string{"markdown", "html", "protobuf", "openapi"} {
		assert.NoError(t, ValidateDocumentProperties(strings.Repeat("я", 128), kind, longOutput))
	}
	cases := []struct{ title, kind, output string }{
		{"", "markdown", "guide.html"},
		{" \t ", "markdown", "guide.html"},
		{"New\nline", "markdown", "guide.html"},
		{strings.Repeat("я", 129), "markdown", "guide.html"},
		{string([]byte{0xff}), "markdown", "guide.html"},
		{"Title", "", "guide.html"},
		{"Title", "script", "guide.html"},
		{"Title", "markdown", ""},
		{"Title", "markdown", longOutput + "x"},
		{"Title", "markdown", "../guide.html"},
		{"Title", "markdown", "Upper.html"},
		{"Title", "markdown", "guide/é.html"},
		{"Title", "markdown", "assets/guide.html"},
		{"Title", "markdown", "_docs/guide.html"},
	}
	for _, input := range cases {
		assert.ErrorIs(t, ValidateDocumentProperties(input.title, input.kind, input.output), ErrInvalid)
	}
}

func TestDocumentPropertiesRejectedInputsPreserveSourceAndExistingDraft(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DocumentPropertiesInput)
		want   error
	}{
		{"missing properties revision", func(input *DocumentPropertiesInput) { input.PropertiesRevision = "" }, ErrInvalid},
		{"stale properties revision", func(input *DocumentPropertiesInput) { input.PropertiesRevision = strings.Repeat("0", 64) }, ErrConflict},
		{"new base differs from reviewed project", func(input *DocumentPropertiesInput) { input.Revision = strings.Repeat("0", 64) }, ErrConflict},
		{"invalid id", func(input *DocumentPropertiesInput) { input.DocumentID = "Bad ID" }, ErrInvalid},
		{"missing document", func(input *DocumentPropertiesInput) { input.DocumentID = "absent-page" }, ErrNotFound},
		{"incomplete draft pair", func(input *DocumentPropertiesInput) { input.DraftID = strings.Repeat("a", 32) }, ErrInvalid},
		{"unknown draft", func(input *DocumentPropertiesInput) {
			input.DraftID, input.DraftRevision = strings.Repeat("a", 32), strings.Repeat("b", 64)
		}, ErrNotFound},
		{"invalid title", func(input *DocumentPropertiesInput) { input.Title = "\t" }, ErrInvalid},
		{"invalid kind", func(input *DocumentPropertiesInput) { input.Kind = "" }, ErrInvalid},
		{"invalid output", func(input *DocumentPropertiesInput) { input.OutputPath = "assets/active.html" }, ErrInvalid},
		{"published output collision", func(input *DocumentPropertiesInput) { input.OutputPath = "index.html" }, ErrConflict},
		{"published output ancestor", func(input *DocumentPropertiesInput) { input.OutputPath = "index.html/child.html" }, ErrConflict},
		{"NUL text", func(input *DocumentPropertiesInput) { input.Content = "new\x00text" }, ErrInvalid},
		{"invalid UTF8 text", func(input *DocumentPropertiesInput) { input.Content = string([]byte{0xff}) }, ErrInvalid},
		{"oversized text", func(input *DocumentPropertiesInput) { input.Content = strings.Repeat("x", MaxTextBytes+1) }, ErrLimit},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, config, original := documentPropertiesFixture(t)
			retained := saveText(t, service, "unrelated.md", "# Retained text\n")
			input := documentPropertiesInput(t, service, "guide", "# Current unsaved buffer\n")
			test.mutate(&input)
			_, err := service.SaveDocumentProperties(context.Background(), input, editor)
			assert.ErrorIs(t, err, test.want)
			after, err := service.ReadDraft(context.Background(), retained.ID)
			require.NoError(t, err)
			assert.Equal(t, retained, after)
			drafts, err := service.Drafts(context.Background())
			require.NoError(t, err)
			assert.Len(t, drafts, 1)
			body, err := os.ReadFile(filepath.Join(config.Root, "guide.md"))
			require.NoError(t, err)
			assert.Equal(t, original, string(body))
		})
	}
}

func TestDocumentPropertiesNeverMovesHomepageOrAdoptsAnUnpairedOwnDraft(t *testing.T) {
	service, _, original := documentPropertiesFixture(t)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	home := project.Documents[0]
	input := documentPropertiesInput(t, service, home.ID, "# Home buffer\n")
	input.OutputPath = "manual/home.html"
	_, err = service.SaveDocumentProperties(context.Background(), input, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	retained := saveText(t, service, "guide.md", "# Newer own draft\n")
	input = documentPropertiesInput(t, service, "guide", original)
	_, err = service.SaveDocumentProperties(context.Background(), input, editor)
	assert.ErrorIs(t, err, ErrConflict)
	after, err := service.ReadDraft(context.Background(), retained.ID)
	require.NoError(t, err)
	assert.Equal(t, retained, after)
}

func TestDocumentPropertiesPendingClaimsDoNotExposeOrOverwriteCompetingDrafts(t *testing.T) {
	text := "# Pending private buffer\n"
	cases := []struct {
		name    string
		pending Change
		output  string
	}{
		{"different identity at same path", Change{Operation: "write", Path: "guide.md", DocumentID: "different-page", Title: "Other page", Content: &text}, "guide.html"},
		{"path alias", Change{Operation: "write", Path: "GUIDE.md", Content: &text}, "guide.html"},
		{"move destination", Change{Operation: "move", Path: "other.md", To: "guide.md"}, "guide.html"},
		{"manifest", Change{Operation: "write", Path: "docs.json", Content: &text}, "guide.html"},
		{"explicit id", Change{Operation: "write", Path: "other.md", Title: "Other", DocumentID: "guide", Content: &text}, "guide.html"},
		{"implicit output", Change{Operation: "write", Path: "reserved.md", Title: "Other", Content: &text}, "reserved.html"},
		{"explicit output", Change{Operation: "write", Path: "other.md", Title: "Other", OutputPath: "reserved.html", Content: &text}, "reserved.html"},
		{"move output", Change{Operation: "move", Path: "index.md", To: "other.md", OutputPath: "reserved.html"}, "reserved.html"},
		{"output prefix", Change{Operation: "write", Path: "other.md", Title: "Other", OutputPath: "reserved.html/child.html", Content: &text}, "reserved.html"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, _, original := documentPropertiesFixture(t)
			input := documentPropertiesInput(t, service, "guide", original)
			input.OutputPath = test.output
			owner := Actor{ID: "bob", Name: "Bob", Email: "bob@example.test"}
			pending, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: input.Revision, Change: test.pending}, owner)
			require.NoError(t, err)
			_, err = service.SaveDocumentProperties(context.Background(), input, editor)
			assert.ErrorIs(t, err, ErrConflict)
			assert.NotContains(t, err.Error(), owner.ID)
			assert.NotContains(t, err.Error(), text)
			after, err := service.ReadDraft(context.Background(), pending.ID)
			require.NoError(t, err)
			assert.Equal(t, pending, after)
		})
	}
}

func TestDocumentPropertiesChecksFreshSourceAuthorizationBeforePersistence(t *testing.T) {
	for _, denyAt := range []int{1, 2} {
		t.Run(string(rune('0'+denyAt)), func(t *testing.T) {
			service, config, original := documentPropertiesFixture(t)
			input := documentPropertiesInput(t, service, "guide", "# Owner buffer to retain\n")
			old, err := service.SaveSource(context.Background(), SourceDraftInput{Path: "guide.md", Revision: input.Revision, Data: []byte("# Previously saved owner buffer\n")}, editor)
			require.NoError(t, err)
			input.DraftID, input.DraftRevision = old.ID, old.Revision
			calls := 0
			service.cfg.AuthorizeSource = func(_ context.Context, actor Actor, name string, access SourceAccess) error {
				assert.Equal(t, editor.ID, actor.ID)
				assert.Equal(t, "guide.md", name)
				assert.Equal(t, SourceReadWrite, access)
				calls++
				if calls == denyAt {
					return ErrOwnership
				}
				return nil
			}
			_, err = service.SaveDocumentProperties(context.Background(), input, editor)
			assert.ErrorIs(t, err, ErrOwnership)
			assert.Equal(t, denyAt, calls)
			after, err := service.ReadDraft(context.Background(), old.ID)
			require.NoError(t, err)
			assert.Equal(t, old, after)
			body, err := os.ReadFile(filepath.Join(config.Root, "guide.md"))
			require.NoError(t, err)
			assert.Equal(t, original, string(body))
		})
	}
}

func TestDocumentPropertiesRechecksPrincipalAfterWaitingForGate(t *testing.T) {
	service, _, original := documentPropertiesFixture(t)
	input := documentPropertiesInput(t, service, "guide", original)
	var revoked atomic.Bool
	type principalKey struct{}
	service.cfg.AuthorizeSource = func(ctx context.Context, actor Actor, _ string, _ SourceAccess) error {
		assert.Equal(t, editor.ID, ctx.Value(principalKey{}))
		assert.Equal(t, editor.ID, actor.ID)
		if revoked.Load() {
			return ErrOwnership
		}
		return nil
	}
	_, release, err := service.enter(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), principalKey{}, editor.ID), 5*time.Second)
	defer cancel()
	started, result := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, saveErr := service.SaveDocumentProperties(ctx, input, editor)
		result <- saveErr
	}()
	<-started
	revoked.Store(true)
	release()
	assert.ErrorIs(t, <-result, ErrOwnership)
	drafts, err := service.Drafts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, drafts)
}

func TestDocumentPropertiesOwnerCASSerializesConcurrentEditorBuffers(t *testing.T) {
	service, _, original := documentPropertiesFixture(t)
	input := documentPropertiesInput(t, service, "guide", original)
	old, err := service.SaveDocumentProperties(context.Background(), input, editor)
	require.NoError(t, err)
	input.DraftID, input.DraftRevision = old.ID, old.Revision
	type outcome struct {
		draft Draft
		err   error
	}
	start, outcomes := make(chan struct{}), make(chan outcome, 2)
	var saves sync.WaitGroup
	for _, text := range []string{"# Editor A\r\n", "# Editor B\r\n"} {
		request := input
		request.Content = text
		saves.Go(func() {
			<-start
			draft, saveErr := service.SaveDocumentProperties(context.Background(), request, editor)
			outcomes <- outcome{draft, saveErr}
		})
	}
	close(start)
	saves.Wait()
	close(outcomes)
	var winner Draft
	conflicts := 0
	for result := range outcomes {
		if result.err == nil {
			assert.Empty(t, winner.ID)
			winner = result.draft
		} else {
			assert.ErrorIs(t, result.err, ErrConflict)
			conflicts++
		}
	}
	require.NotEmpty(t, winner.ID)
	assert.Equal(t, 1, conflicts)
	stored, err := service.ReadDraft(context.Background(), old.ID)
	require.NoError(t, err)
	assert.Equal(t, winner, stored)
}

func TestDocumentPropertiesFreshGlobalCASCannotReuseStaleFileHashes(t *testing.T) {
	service, config, original := documentPropertiesFixture(t)
	filename := filepath.Join(config.Root, "unrelated.txt")
	require.NoError(t, os.WriteFile(filename, []byte("old payload"), 0o600))
	input := documentPropertiesInput(t, service, "guide", original)
	require.NoError(t, os.WriteFile(filename, []byte("new payload"), 0o600))
	observed, err := os.Stat(filename)
	require.NoError(t, err)
	state := service.cached.Files["unrelated.txt"]
	state.Info = observed
	service.cached.Files["unrelated.txt"] = state
	_, err = service.SaveDocumentProperties(context.Background(), input, editor)
	assert.ErrorIs(t, err, ErrConflict)
	drafts, err := service.Drafts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, drafts)
	body, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.Equal(t, "new payload", string(body))
}

func TestDocumentPropertiesNeverRebindsNamedBranchDraftsAndIgnoresOtherBranches(t *testing.T) {
	service, config, _ := gitManagedFixture(t)
	service.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	home := project.Documents[0]
	input := documentPropertiesInput(t, service, home.ID, "# Main draft\n")
	mainDraft, err := service.SaveDocumentProperties(context.Background(), input, editor)
	require.NoError(t, err)
	require.NoError(t, config.Storage.CreateBranch(context.Background(), "other"))
	require.NoError(t, config.Storage.Checkout(context.Background(), "other"))
	otherInput := documentPropertiesInput(t, service, home.ID, "# Other branch buffer\n")
	blocked := otherInput
	blocked.Revision, blocked.DraftID, blocked.DraftRevision = mainDraft.BaseRevision, mainDraft.ID, mainDraft.Revision
	_, err = service.SaveDocumentProperties(context.Background(), blocked, editor)
	assert.ErrorIs(t, err, ErrConflict)
	after, err := service.ReadDraft(context.Background(), mainDraft.ID)
	require.NoError(t, err)
	assert.Equal(t, mainDraft, after)
	otherDraft, err := service.SaveDocumentProperties(context.Background(), otherInput, editor)
	require.NoError(t, err)
	assert.Equal(t, "other", otherDraft.Branch)
	require.NoError(t, config.Storage.Checkout(context.Background(), "main"))
	input = documentPropertiesInput(t, service, home.ID, "# Updated main owner buffer\n")
	input.DraftID, input.DraftRevision, input.Revision = mainDraft.ID, mainDraft.Revision, mainDraft.BaseRevision
	saved, err := service.SaveDocumentProperties(context.Background(), input, editor)
	require.NoError(t, err)
	assert.Equal(t, mainDraft.ID, saved.ID)
	assert.Equal(t, "main", saved.Branch)
	after, err = service.ReadDraft(context.Background(), otherDraft.ID)
	require.NoError(t, err)
	assert.Equal(t, otherDraft, after)
}
