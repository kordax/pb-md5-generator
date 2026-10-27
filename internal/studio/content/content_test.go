package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var editor = Actor{ID: "alice", Name: "Alice Example", Email: "alice@example.test"}

func managedFixture(t *testing.T) (*Service, Config) {
	t.Helper()
	base := t.TempDir()
	config := Config{Root: filepath.Join(base, "content"), StateDir: filepath.Join(base, "state"), OutputDir: filepath.Join(base, "site"), Initialize: true, Compile: func(ctx context.Context, manifest, output string) error {
		return site.BuildContext(ctx, manifest, output, site.Options{})
	}}
	service, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	return service, config
}

func saveText(t *testing.T, service *Service, name, text string) Draft {
	t.Helper()
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	draft, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: name, Content: &text}}, editor)
	require.NoError(t, err)
	return draft
}

func publishDraft(t *testing.T, service *Service, draft Draft) PublishResult {
	t.Helper()
	result, err := service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.NoError(t, err)
	require.True(t, result.Published)
	return result
}

func TestDraftRestartInvalidPublishAndRestore(t *testing.T) {
	service, config := managedFixture(t)
	draft := saveText(t, service, "index.md", "# First published\n")
	first := publishDraft(t, service, draft)
	require.Equal(t, "local", first.Sync.Backend)
	published, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	draft = saveText(t, service, "index.md", "# Invalid draft\n\n[Missing](absent.md)\n")
	file, err := service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# First published\n", file.Content)
	metadata, err := service.Drafts(context.Background())
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	assert.Nil(t, metadata[0].Change.Content, "list must not retain all draft payloads")
	require.NoError(t, service.Close())
	config.Initialize = false
	service, err = New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	restored, err := service.ReadDraft(context.Background(), draft.ID)
	require.NoError(t, err)
	require.NotNil(t, restored.Change.Content)
	assert.Equal(t, "# Invalid draft\n\n[Missing](absent.md)\n", *restored.Change.Content)
	_, err = service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.ErrorIs(t, err, ErrInvalid)
	after, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, published, after)
	file, err = service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# First published\n", file.Content)
	_, err = service.ReadDraft(context.Background(), draft.ID)
	require.NoError(t, err, "failed publication retains draft")
	require.NoError(t, service.DiscardDraft(context.Background(), draft.ID, draft.Revision, editor))
	draft = saveText(t, service, "index.md", "# New version\n")
	publishDraft(t, service, draft)
	history, err := service.History(context.Background(), "index.md")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(history), 4)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	rollback, err := service.Restore(context.Background(), "index.md", history[len(history)-1].ID, project.Revision, editor)
	require.NoError(t, err)
	publishDraft(t, service, rollback)
	file, err = service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Documentation\n", file.Content)
}

func TestDocumentsFilesMovesFoldersAndHomepage(t *testing.T) {
	service, _ := managedFixture(t)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	text := "# A guide\n"
	draft, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Changes: []Change{
		{Operation: "mkdir", Path: "guides"},
		{Operation: "write", Path: "guides/start.md", Title: "Get started", Kind: "markdown", Content: &text},
		{Operation: "write", Path: "images/logo.png", Data: []byte{137, 80, 78, 71}},
	}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	require.Len(t, project.Documents, 2)
	id := project.Documents[1].ID
	file, err := service.Read(context.Background(), "images/logo.png")
	require.NoError(t, err)
	assert.True(t, file.Binary)
	assert.Equal(t, []byte{137, 80, 78, 71}, file.Data)
	draft, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "move", Path: "guides", To: "handbook"}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "handbook/start.md", project.Documents[1].Path)
	assert.Equal(t, id, project.Documents[1].ID)
	assert.Equal(t, "guides/start.html", project.Documents[1].OutputPath)
	draft, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "delete", Path: "index.md"}}, editor)
	require.NoError(t, err)
	_, err = service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	file, err = service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.NotEmpty(t, file.Content)
	require.NoError(t, service.DiscardDraft(context.Background(), draft.ID, draft.Revision, editor))
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	draft, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Changes: []Change{{Operation: "delete", Path: "handbook/start.md"}, {Operation: "rmdir", Path: "handbook"}}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	require.Len(t, project.Documents, 1)
}

func TestConcurrentOptimisticRevisionsAndDraftOwnership(t *testing.T) {
	service, _ := managedFixture(t)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	one, two := "# One\n", "# Two\n"
	first, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "index.md", Content: &one}}, editor)
	require.NoError(t, err)
	second, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "index.md", Content: &two}}, editor)
	require.NoError(t, err)
	other := Actor{ID: "mallory", Name: "Mallory", Email: "mallory@example.test"}
	_, err = service.Publish(context.Background(), PublishInput{ID: first.ID, Revision: first.Revision}, other)
	assert.ErrorIs(t, err, ErrConflict)
	assert.ErrorIs(t, service.DiscardDraft(context.Background(), first.ID, first.Revision, other), ErrConflict)
	_, err = service.SaveDraft(context.Background(), DraftInput{ID: first.ID, Revision: first.Revision, BaseRevision: first.BaseRevision, Change: first.Change}, other)
	assert.ErrorIs(t, err, ErrConflict)
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, draft := range []Draft{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	var succeeded, conflicted int
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, conflicted)
}

func TestPathConfinementLimitsAndPrivateInclude(t *testing.T) {
	service, config := managedFixture(t)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	for _, name := range []string{"../outside.md", "/absolute.md", ".git/config", "private/.env", "private/credentials.json", "secret-values.yaml", "index.md/../other.md", "folder\\other.md", "CON.txt", "_docs/index.md"} {
		text := "text"
		_, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: name, Content: &text}}, editor)
		assert.ErrorIs(t, err, ErrInvalid, name)
		_, err = service.Read(context.Background(), name)
		assert.Error(t, err, name)
	}
	large := strings.Repeat("x", MaxTextBytes+1)
	_, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "big.md", Content: &large}}, editor)
	assert.ErrorIs(t, err, ErrLimit)
	_, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "big.pdf", Data: make([]byte, MaxBinaryBytes+1)}}, editor)
	assert.ErrorIs(t, err, ErrLimit)
	outside := filepath.Join(t.TempDir(), "outside.md")
	require.NoError(t, os.WriteFile(outside, []byte("never read"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(config.Root, "escape.md")))
	_, err = service.Project(context.Background())
	assert.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, os.Remove(filepath.Join(config.Root, "escape.md")))
	require.NoError(t, os.Mkdir(filepath.Join(config.Root, ".git"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, ".git", "config"), []byte("PRIVATE-CREDENTIAL-MARKER"), 0o600))
	draft := saveText(t, service, "index.md", "# Leak attempt\n\n:::include source=\".git/config\" language=\"text\"\n:::\n")
	_, err = service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrInvalid, "sanitized compilation snapshot cannot contain Git internals")
	file, err := service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.NotContains(t, file.Content, "PRIVATE-CREDENTIAL-MARKER")
}

func TestOwnershipContextAndManagedOutputRollback(t *testing.T) {
	service, config := managedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Project(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	wrong := config
	wrong.OutputDir = filepath.Join(t.TempDir(), "other")
	_, err = New(wrong)
	assert.ErrorIs(t, err, ErrOwnership)
	wrong = config
	wrong.StateDir = filepath.Join(config.Root, "state")
	_, err = New(wrong)
	assert.ErrorIs(t, err, ErrInvalid)
	publishDraft(t, service, saveText(t, service, "index.md", "# Good\n"))
	prior, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(config.OutputDir, "operator-owned.txt"), []byte("keep"), 0o600))
	draft := saveText(t, service, "index.md", "# Rejected publish\n")
	_, err = service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrOwnership)
	file, err := service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Good\n", file.Content)
	after, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, prior, after)
	marker, err := os.ReadFile(filepath.Join(config.OutputDir, "operator-owned.txt"))
	require.NoError(t, err)
	assert.Equal(t, "keep", string(marker))
}
