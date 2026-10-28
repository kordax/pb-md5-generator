package content

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func documentPropertiesFixture(t *testing.T) (*Service, Config, string) {
	t.Helper()
	text := "\ufeff# Original guide\r\n\r\nOriginal source bytes.\r\n"
	service, config, input := registrationFixture(t, "guide.md", text)
	input.DocumentID, input.Title, input.OutputPath = "guide", "Original guide", "guide.html"
	draft, err := service.RegisterSource(context.Background(), input, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	return service, config, text
}

func documentPropertiesInput(t *testing.T, service *Service, id, text string) DocumentPropertiesInput {
	t.Helper()
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	for _, entry := range project.Documents {
		if entry.ID == id {
			return DocumentPropertiesInput{DocumentID: id, Revision: project.Revision, PropertiesRevision: project.Revision, Content: text, Title: entry.Title, Kind: entry.Kind, OutputPath: entry.OutputPath}
		}
	}
	t.Fatalf("fixture document %q is missing", id)
	return DocumentPropertiesInput{}
}

func TestDocumentPropertiesPublicationKeepsIdentitySourceBytesAndNavigation(t *testing.T) {
	service, config, text := documentPropertiesFixture(t)
	manifest, err := loadManifest(config.Root)
	require.NoError(t, err)
	filename := filepath.Join(config.Root, "docs.json")
	data, err := os.ReadFile(filename)
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &envelope))
	envelope["navigation"], err = json.Marshal(map[string]any{
		"showTOC": true, "showPreviousNext": true, "stickyHeader": true,
		"tree": map[string]any{"unlistedPages": "append", "items": []any{
			map[string]any{"kind": "group", "id": "manuals", "title": "Manuals", "children": []any{
				map[string]any{"kind": "page", "pageId": manifest.Pages[0].ID},
				map[string]any{"kind": "page", "pageId": "guide", "title": "Authored navigation label"},
			}},
		}},
	})
	require.NoError(t, err)
	data, err = json.Marshal(envelope)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filename, data, 0o600))
	manifest, err = loadManifest(config.Root)
	require.NoError(t, err)
	beforeNavigation := manifest.Navigation
	before, err := os.Stat(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	beforeHistory, err := service.History(context.Background(), "guide.md")
	require.NoError(t, err)
	input := documentPropertiesInput(t, service, "guide", text)
	input.Title, input.OutputPath = "Renamed guide", "manual/renamed.html"
	draft, err := service.SaveDocumentProperties(context.Background(), input, editor)
	require.NoError(t, err)
	assert.Equal(t, "guide", draft.Change.DocumentID)
	assert.Equal(t, "guide.md", draft.Change.Path)
	assert.Equal(t, input.Revision, draft.BaseRevision)
	require.NotNil(t, draft.Change.Content)
	assert.Equal(t, text, *draft.Change.Content)
	staged, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.Equal(t, data, staged, "properties must not change published sources before Publish")
	stored, err := service.ReadDraft(context.Background(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, stored)
	publishDraft(t, service, draft)
	manifest, err = loadManifest(config.Root)
	require.NoError(t, err)
	assert.Equal(t, beforeNavigation, manifest.Navigation, "stable IDs need no navigation pruning")
	require.Len(t, manifest.Pages, 2)
	assert.Equal(t, "guide", manifest.Pages[1].ID)
	assert.Equal(t, "guide.md", manifest.Pages[1].Source)
	assert.Equal(t, input.Title, manifest.Pages[1].Title)
	assert.Equal(t, input.OutputPath, manifest.Pages[1].Path)
	body, err := os.ReadFile(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	assert.Equal(t, []byte(text), body)
	after, err := os.Stat(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())
	assert.True(t, os.SameFile(before, after))
	_, err = os.Stat(filepath.Join(config.OutputDir, "manual", "renamed.html"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(config.OutputDir, "guide.html"))
	assert.True(t, os.IsNotExist(err))
	afterHistory, err := service.History(context.Background(), "guide.md")
	require.NoError(t, err)
	assert.Equal(t, beforeHistory, afterHistory)
	manifestHistory, err := service.History(context.Background(), "docs.json")
	require.NoError(t, err)
	assert.NotEmpty(t, manifestHistory)
}

func TestDocumentPropertiesSavesExactUnsavedTextAndExplicitKind(t *testing.T) {
	service, config, original := documentPropertiesFixture(t)
	text := "\ufeff<h1>Current HTML buffer</h1>\r\n<p>Unsaved text.</p>\r\n"
	input := documentPropertiesInput(t, service, "guide", text)
	input.Title, input.Kind = "HTML guide", "html"
	draft, err := service.SaveDocumentProperties(context.Background(), input, editor)
	require.NoError(t, err)
	assert.Equal(t, "html", draft.Change.Kind)
	require.NotNil(t, draft.Change.Content)
	assert.Equal(t, text, *draft.Change.Content)
	body, err := os.ReadFile(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	assert.Equal(t, original, string(body))
	publishDraft(t, service, draft)
	body, err = os.ReadFile(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	assert.Equal(t, text, string(body))
	page, err := os.ReadFile(filepath.Join(config.OutputDir, "guide.html"))
	require.NoError(t, err)
	assert.Contains(t, string(page), "Current HTML buffer")
}

func TestDocumentPropertiesPreservesOldBaseUntilExplicitReviewedRebase(t *testing.T) {
	service, config, original := documentPropertiesFixture(t)
	input := documentPropertiesInput(t, service, "guide", "# Initial local draft\n")
	old, err := service.SaveSource(context.Background(), SourceDraftInput{Path: "guide.md", Revision: input.Revision, Data: []byte(input.Content)}, editor)
	require.NoError(t, err)
	publishDraft(t, service, saveText(t, service, "unrelated.txt", "Other published content\n"))
	current, err := service.Project(context.Background())
	require.NoError(t, err)
	require.NotEqual(t, old.BaseRevision, current.Revision)
	input.Revision, input.PropertiesRevision = old.BaseRevision, current.Revision
	input.DraftID, input.DraftRevision = old.ID, old.Revision
	input.Content, input.Title, input.OutputPath = "\ufeff# Current owner text\r\n", "Current owner properties", "manual/merged.html"
	saved, err := service.SaveDocumentProperties(context.Background(), input, editor)
	require.NoError(t, err)
	assert.Equal(t, old.ID, saved.ID)
	assert.Equal(t, old.BaseRevision, saved.BaseRevision)
	assert.NotEqual(t, old.Revision, saved.Revision)
	assert.Equal(t, "guide", saved.Change.DocumentID, "owned raw write becomes the same registered page")
	_, err = service.Publish(context.Background(), PublishInput{ID: saved.ID, Revision: saved.Revision}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	body, err := os.ReadFile(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	assert.Equal(t, original, string(body))
	_, err = service.SaveDocumentProperties(context.Background(), input, editor)
	assert.ErrorIs(t, err, ErrConflict, "stale paired CAS must not overwrite newer owner text")
	retained, err := service.ReadDraft(context.Background(), saved.ID)
	require.NoError(t, err)
	assert.Equal(t, saved, retained)
	input.Rebase, input.Revision, input.PropertiesRevision = true, current.Revision, current.Revision
	input.DraftRevision = saved.Revision
	input.Content = "\ufeff# Explicitly merged owner buffer\r\n"
	rebased, err := service.SaveDocumentProperties(context.Background(), input, editor)
	require.NoError(t, err)
	assert.Equal(t, saved.ID, rebased.ID)
	assert.Equal(t, current.Revision, rebased.BaseRevision)
	assert.Equal(t, input.Title, rebased.Change.Title)
	assert.Equal(t, input.OutputPath, rebased.Change.OutputPath)
	publishDraft(t, service, rebased)
	body, err = os.ReadFile(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	assert.Equal(t, input.Content, string(body))
	body, err = os.ReadFile(filepath.Join(config.Root, "unrelated.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Other published content\n", string(body))
}

func TestDocumentPropertiesRepairsOwnedUnpublishedCreationAndRegistration(t *testing.T) {
	for _, registration := range []bool{false, true} {
		name := "creation"
		if registration {
			name = "registration"
		}
		t.Run(name, func(t *testing.T) {
			var service *Service
			var config Config
			var old Draft
			var err error
			text := "# Needs a different renderer\n"
			if registration {
				var input RegisterSourceInput
				service, config, input = registrationFixture(t, "new.md", text)
				input.DocumentID, input.Title, input.Kind, input.OutputPath = "new-page", "Wrong renderer", "openapi", "new.html"
				old, err = service.RegisterSource(context.Background(), input, editor)
			} else {
				service, config = managedFixture(t)
				service.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
				project, projectErr := service.Project(context.Background())
				require.NoError(t, projectErr)
				old, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "new.md", DocumentID: "new-page", Title: "Wrong renderer", Kind: "openapi", OutputPath: "new.html", Content: &text}}, editor)
			}
			require.NoError(t, err)
			_, err = service.Publish(context.Background(), PublishInput{ID: old.ID, Revision: old.Revision}, editor)
			assert.ErrorIs(t, err, ErrInvalid)
			project, err := service.Project(context.Background())
			require.NoError(t, err)
			input := DocumentPropertiesInput{DocumentID: "new-page", Revision: old.BaseRevision, PropertiesRevision: project.Revision, DraftID: old.ID, DraftRevision: old.Revision, Content: "\ufeff# Corrected buffer\r\n", Title: "Corrected title", Kind: "markdown", OutputPath: "repaired/new.html"}
			saved, err := service.SaveDocumentProperties(context.Background(), input, editor)
			require.NoError(t, err)
			assert.Equal(t, old.ID, saved.ID)
			assert.Equal(t, "new-page", saved.Change.DocumentID)
			assert.Equal(t, "new.md", saved.Change.Path)
			body, readErr := os.ReadFile(filepath.Join(config.Root, "new.md"))
			if registration {
				require.NoError(t, readErr)
				assert.Equal(t, text, string(body))
			} else {
				assert.True(t, os.IsNotExist(readErr))
			}
			publishDraft(t, service, saved)
			manifest, err := loadManifest(config.Root)
			require.NoError(t, err)
			require.Len(t, manifest.Pages, 2)
			assert.Equal(t, "new-page", manifest.Pages[1].ID)
			assert.Equal(t, "new.md", manifest.Pages[1].Source)
			assert.Equal(t, input.OutputPath, manifest.Pages[1].Path)
			body, err = os.ReadFile(filepath.Join(config.Root, "new.md"))
			require.NoError(t, err)
			assert.Equal(t, input.Content, string(body))
		})
	}
}

func TestDocumentPropertiesBadPublicationKeepsBufferAndMetadata(t *testing.T) {
	service, config, original := documentPropertiesFixture(t)
	input := documentPropertiesInput(t, service, "guide", "# Not an OpenAPI document\n")
	input.Kind, input.Title, input.OutputPath = "openapi", "Retained properties", "reference/retained.html"
	draft, err := service.SaveDocumentProperties(context.Background(), input, editor)
	require.NoError(t, err)
	_, err = service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	stored, err := service.ReadDraft(context.Background(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, stored)
	body, err := os.ReadFile(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	assert.Equal(t, original, string(body))
	manifest, err := loadManifest(config.Root)
	require.NoError(t, err)
	assert.Equal(t, "Original guide", manifest.Pages[1].Title)
	assert.Equal(t, "markdown", manifest.Pages[1].Kind)
	assert.Equal(t, "guide.html", manifest.Pages[1].Path)
}
