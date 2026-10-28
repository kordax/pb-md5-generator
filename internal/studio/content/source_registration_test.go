package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registrationFixture(t *testing.T, name, text string) (*Service, Config, RegisterSourceInput) {
	t.Helper()
	service, config := managedFixture(t)
	config.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
	service.cfg.AuthorizeSource = config.AuthorizeSource
	filename := filepath.Join(config.Root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o700))
	require.NoError(t, os.WriteFile(filename, []byte(text), 0o600))
	input := registrationInput(t, service, name, []byte(text))
	return service, config, input
}

func registrationInput(t *testing.T, service *Service, name string, data []byte) RegisterSourceInput {
	t.Helper()
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	return RegisterSourceInput{Path: name, Revision: project.Revision, SourceRevision: hex.EncodeToString(digest[:]), Title: "Registered source", Kind: "markdown"}
}

func TestRegisterSourcePublicationPreservesBytesTimeAndExistingAssets(t *testing.T) {
	service, config := managedFixture(t)
	config.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
	service.cfg.AuthorizeSource = config.AuthorizeSource
	text := "\ufeff# Exact source\r\n\r\nReviewed source bytes.\r\n"
	publishDraft(t, service, saveText(t, service, "examples/notes.txt", text))
	input := registrationInput(t, service, "examples/notes.txt", []byte(text))
	filename := filepath.Join(config.Root, "examples", "notes.txt")
	beforeInfo, err := os.Stat(filename)
	require.NoError(t, err)
	beforeManifest, err := os.ReadFile(filepath.Join(config.Root, "docs.json"))
	require.NoError(t, err)
	manifest, err := loadManifest(config.Root)
	require.NoError(t, err)
	beforeAssets := append([]string(nil), manifest.Assets...)
	beforeHistory, err := service.History(context.Background(), input.Path)
	require.NoError(t, err)

	draft, err := service.RegisterSource(context.Background(), input, editor)
	require.NoError(t, err)
	assert.Equal(t, input.Revision, draft.BaseRevision)
	assert.True(t, validDocumentID(draft.Change.DocumentID))
	assert.True(t, strings.HasPrefix(draft.Change.DocumentID, "page-"))
	assert.Equal(t, "examples/notes.html", draft.Change.OutputPath)
	require.NotNil(t, draft.Change.Content)
	assert.Equal(t, text, *draft.Change.Content)
	afterStage, err := os.ReadFile(filepath.Join(config.Root, "docs.json"))
	require.NoError(t, err)
	assert.Equal(t, beforeManifest, afterStage, "registration must remain a draft")

	require.NoError(t, service.Close())
	config.Initialize = false
	reopened, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	stored, err := reopened.ReadDraft(context.Background(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft.Change.DocumentID, stored.Change.DocumentID)
	assert.Equal(t, draft.Revision, stored.Revision)
	publishDraft(t, reopened, stored)
	after, err := os.ReadFile(filename)
	require.NoError(t, err)
	afterInfo, err := os.Stat(filename)
	require.NoError(t, err)
	assert.Equal(t, []byte(text), after)
	assert.Equal(t, beforeInfo.ModTime(), afterInfo.ModTime())
	assert.True(t, os.SameFile(beforeInfo, afterInfo), "unchanged source must not be replaced")
	manifest, err = loadManifest(config.Root)
	require.NoError(t, err)
	assert.Equal(t, beforeAssets, manifest.Assets)
	require.Len(t, manifest.Pages, 2)
	assert.Equal(t, draft.Change.DocumentID, manifest.Pages[1].ID)
	assert.Equal(t, input.Title, manifest.Pages[1].Title)
	assert.Equal(t, input.Kind, manifest.Pages[1].Kind)
	assert.Equal(t, draft.Change.OutputPath, manifest.Pages[1].Path)
	afterHistory, err := reopened.History(context.Background(), input.Path)
	require.NoError(t, err)
	assert.Equal(t, beforeHistory, afterHistory, "unchanged bytes do not invent a source revision")
	page, err := os.ReadFile(filepath.Join(config.OutputDir, "examples", "notes.html"))
	require.NoError(t, err)
	assert.Contains(t, string(page), "Reviewed source bytes.")
}

func TestRegisterSourceExplicitMetadataDoesNotInferKindFromSuffix(t *testing.T) {
	for _, kind := range []string{"markdown", "html", "protobuf", "openapi"} {
		t.Run(kind, func(t *testing.T) {
			service, _, input := registrationFixture(t, "examples/literal.txt", "Literal source\n")
			input.Kind, input.DocumentID, input.OutputPath = kind, "chosen-id", "reference/chosen.html"
			draft, err := service.RegisterSource(context.Background(), input, editor)
			require.NoError(t, err)
			assert.Equal(t, kind, draft.Change.Kind)
			assert.Equal(t, "chosen-id", draft.Change.DocumentID)
			assert.Equal(t, "reference/chosen.html", draft.Change.OutputPath)
			require.NotNil(t, draft.Change.Content)
			assert.Equal(t, "Literal source\n", *draft.Change.Content)
		})
	}
}

func TestRegisterSourceRejectsPublishedMetadataAndSourceAliases(t *testing.T) {
	for _, collision := range []string{"source", "source-alias", "id", "output", "output ancestor"} {
		t.Run(collision, func(t *testing.T) {
			service, config, input := registrationFixture(t, "source.md", "# Unregistered\n")
			project, err := service.Project(context.Background())
			require.NoError(t, err)
			switch collision {
			case "source":
				input.Path = "index.md"
				data, readErr := os.ReadFile(filepath.Join(config.Root, "index.md"))
				require.NoError(t, readErr)
				digest := sha256.Sum256(data)
				input.SourceRevision = hex.EncodeToString(digest[:])
			case "source-alias":
				input.Path = "SOURCE.md"
			case "id":
				input.DocumentID = project.Documents[0].ID
			case "output":
				input.OutputPath = project.Documents[0].OutputPath
			case "output ancestor":
				input.OutputPath = project.Documents[0].OutputPath + "/child.html"
			}
			_, err = service.RegisterSource(context.Background(), input, editor)
			assert.ErrorIs(t, err, ErrConflict)
			drafts, err := service.Drafts(context.Background())
			require.NoError(t, err)
			assert.Empty(t, drafts)
			data, err := os.ReadFile(filepath.Join(config.Root, "source.md"))
			require.NoError(t, err)
			assert.Equal(t, "# Unregistered\n", string(data))
		})
	}
}

func TestRegisterSourceReservesPendingPathsManifestAndPageMetadata(t *testing.T) {
	text := "# Pending content\n"
	cases := []struct {
		name    string
		pending Change
		id      string
		output  string
	}{
		{name: "same source", pending: Change{Operation: "write", Path: "guide/source.md", Content: &text}},
		{name: "source alias", pending: Change{Operation: "write", Path: "GUIDE/Source.md", Content: &text}},
		{name: "source ancestor", pending: Change{Operation: "mkdir", Path: "guide"}},
		{name: "move destination", pending: Change{Operation: "move", Path: "other.md", To: "guide/source.md"}},
		{name: "manifest", pending: Change{Operation: "write", Path: "docs.json", Content: &text}},
		{name: "manifest alias", pending: Change{Operation: "write", Path: "DOCS.JSON", Content: &text}},
		{name: "explicit id", pending: Change{Operation: "write", Path: "other.md", Title: "Other", DocumentID: "reserved", Content: &text}, id: "reserved"},
		{name: "implicit id", pending: Change{Operation: "write", Path: "other.md", Title: "Other", Content: &text}, id: document.PageID("other.html")},
		{name: "explicit output", pending: Change{Operation: "write", Path: "other.md", Title: "Other", OutputPath: "reserved.html", Content: &text}, output: "reserved.html"},
		{name: "implicit output", pending: Change{Operation: "write", Path: "other.md", Title: "Other", Content: &text}, output: "other.html"},
		{name: "move output", pending: Change{Operation: "move", Path: "index.md", To: "other.md", OutputPath: "reserved.html"}, output: "reserved.html"},
		{name: "output ancestor", pending: Change{Operation: "write", Path: "other.md", Title: "Other", Content: &text}, output: "other.html/child.html"},
		{name: "output descendant", pending: Change{Operation: "write", Path: "other.md", Title: "Other", OutputPath: "reserved.html/child.html", Content: &text}, output: "reserved.html"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, config, input := registrationFixture(t, "guide/source.md", "# Reviewed\n")
			if test.id != "" {
				input.DocumentID = test.id
			}
			if test.output != "" {
				input.OutputPath = test.output
			}
			owner := Actor{ID: "bob", Name: "Bob", Email: "bob@example.test"}
			pending, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: input.Revision, Change: test.pending}, owner)
			require.NoError(t, err)
			_, err = service.RegisterSource(context.Background(), input, editor)
			assert.ErrorIs(t, err, ErrConflict)
			after, err := service.ReadDraft(context.Background(), pending.ID)
			require.NoError(t, err)
			assert.Equal(t, pending, after, "conflict must not consume or rewrite competing drafts")
			drafts, err := service.Drafts(context.Background())
			require.NoError(t, err)
			assert.Len(t, drafts, 1)
			data, err := os.ReadFile(filepath.Join(config.Root, "guide", "source.md"))
			require.NoError(t, err)
			assert.Equal(t, "# Reviewed\n", string(data))
		})
	}
}

func TestRegisterSourceFailedPublicationRetainsSourceAndDraft(t *testing.T) {
	text := "# Broken links\n\n[Missing](absent.md)\n"
	service, config, input := registrationFixture(t, "restored.md", text)
	before, err := os.ReadFile(filepath.Join(config.Root, "docs.json"))
	require.NoError(t, err)
	draft, err := service.RegisterSource(context.Background(), input, editor)
	require.NoError(t, err)
	_, err = service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	after, err := os.ReadFile(filepath.Join(config.Root, "docs.json"))
	require.NoError(t, err)
	assert.Equal(t, before, after)
	data, err := os.ReadFile(filepath.Join(config.Root, input.Path))
	require.NoError(t, err)
	assert.Equal(t, text, string(data))
	retained, err := service.ReadDraft(context.Background(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, retained)
}
