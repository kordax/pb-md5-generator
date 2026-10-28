package content

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func contentNavigationFixture(t *testing.T) (*Service, Config) {
	t.Helper()
	service, config := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	guide, private := "# Guide\n", "# Private document\n"
	draft, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Changes: []Change{
		{Operation: "write", Path: "guide.md", Title: "Guide", DocumentID: "guide", OutputPath: "guide.html", Content: &guide},
		{Operation: "write", Path: "private.md", Title: "Private", DocumentID: "private", OutputPath: "private.html", Content: &private},
	}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	return service, config
}

func installContentNavigation(t *testing.T, service *Service, config Config, tree *document.NavigationTree) site.Manifest {
	t.Helper()
	manifest, err := loadManifest(config.Root)
	require.NoError(t, err)
	manifest.Navigation = &document.NavigationSettings{ShowTOC: false, ShowPreviousNext: true, StickyHeader: false, Tree: tree}
	manifest.ThemeID, manifest.Footer = "paper", "Retain the footer"
	manifest.Editor = &document.EditorSettings{DefaultFormat: "html", PreviewOnSave: true}
	manifest.API = &document.APISettings{Enabled: true, ShowCurl: true, RequestTimeoutMs: 1000, MaxResponseBytes: 1024}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	publishDraft(t, service, saveText(t, service, "docs.json", string(data)))
	manifest, err = loadManifest(config.Root)
	require.NoError(t, err)
	return manifest
}

func TestDocumentDeletionPrunesNavigationAndEmptyGroupsInTheSamePublication(t *testing.T) {
	service, config := contentNavigationFixture(t)
	collapsed := false
	original := installContentNavigation(t, service, config, &document.NavigationTree{UnlistedPages: "omit", Items: []document.NavigationNode{
		{Kind: "group", ID: "guides", Title: "Guides", Collapsed: &collapsed, Children: []document.NavigationNode{{Kind: "page", PageID: "guide", Title: "Read the guide"}}},
		{Kind: "group", ID: "private-group", Title: "PRIVATE-GROUP-TITLE", Description: "PRIVATE-GROUP-DESCRIPTION", Children: []document.NavigationNode{
			{Kind: "group", ID: "nested", Title: "PRIVATE-NESTED-TITLE", Children: []document.NavigationNode{{Kind: "page", PageID: "private"}}},
		}},
	}})
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	draft, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "delete", Path: "private.md"}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	manifest, err := loadManifest(config.Root)
	require.NoError(t, err)
	require.NotNil(t, manifest.Navigation)
	assert.Equal(t, original.Navigation.ShowTOC, manifest.Navigation.ShowTOC)
	assert.Equal(t, original.Navigation.ShowPreviousNext, manifest.Navigation.ShowPreviousNext)
	assert.Equal(t, original.Navigation.StickyHeader, manifest.Navigation.StickyHeader)
	expected, err := site.PruneNavigation(original.Navigation.Tree, map[string]bool{"home": true, "guide": true})
	require.NoError(t, err)
	assert.Equal(t, expected, manifest.Navigation.Tree, "pruning must match the permission-aware automatic-deletion contract")
	assert.Equal(t, original.ThemeID, manifest.ThemeID)
	assert.Equal(t, original.Footer, manifest.Footer)
	assert.Equal(t, original.Editor, manifest.Editor)
	assert.Equal(t, original.API, manifest.API)
	data, err := readSource(config.Root, "docs.json", 1<<20)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "PRIVATE-GROUP")
	assert.NotContains(t, string(data), "PRIVATE-NESTED")
	_, err = service.Read(t.Context(), "private.md")
	assert.ErrorIs(t, err, ErrNotFound)
	view, err := site.ResolveNavigation(manifest, "guide")
	require.NoError(t, err)
	require.Len(t, view.Pages, 1)
	assert.Equal(t, "guide", view.Pages[0].PageID)
}

func TestFailedDeletionRetainsNavigationAndDraftThenRetriesToExplicitEmptyTree(t *testing.T) {
	service, config := contentNavigationFixture(t)
	original := installContentNavigation(t, service, config, &document.NavigationTree{UnlistedPages: "omit", Items: []document.NavigationNode{
		{Kind: "group", ID: "private-only", Title: "Private group", Children: []document.NavigationNode{{Kind: "page", PageID: "private"}}},
	}})
	manifestBefore, err := readSource(config.Root, "docs.json", 1<<20)
	require.NoError(t, err)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	draft, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "delete", Path: "private.md"}}, editor)
	require.NoError(t, err)
	compile := service.cfg.Compile
	service.cfg.Compile = func(context.Context, string, string) error { return fmt.Errorf("compiler temporarily unavailable") }
	_, err = service.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	manifestAfter, err := readSource(config.Root, "docs.json", 1<<20)
	require.NoError(t, err)
	assert.Equal(t, manifestBefore, manifestAfter)
	retained, err := service.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, retained)
	_, err = service.Read(t.Context(), "private.md")
	require.NoError(t, err)
	service.cfg.Compile = compile
	publishDraft(t, service, draft)
	manifest, err := loadManifest(config.Root)
	require.NoError(t, err)
	require.NotNil(t, manifest.Navigation)
	require.NotNil(t, manifest.Navigation.Tree)
	require.NotNil(t, manifest.Navigation.Tree.Items)
	assert.Empty(t, manifest.Navigation.Tree.Items)
	assert.Equal(t, "omit", manifest.Navigation.Tree.UnlistedPages)
	assert.Equal(t, original.Navigation.ShowPreviousNext, manifest.Navigation.ShowPreviousNext)
	data, err := json.Marshal(manifest.Navigation.Tree)
	require.NoError(t, err)
	assert.JSONEq(t, `{"unlistedPages":"omit","items":[]}`, string(data))
	view, err := site.ResolveNavigation(manifest, "home")
	require.NoError(t, err)
	assert.Empty(t, view.Items)
	assert.Empty(t, view.Pages)
	require.Len(t, view.Breadcrumbs, 1)
	assert.Equal(t, "home", view.Breadcrumbs[0].PageID)
}

func TestIDPreservingDocumentMovesKeepAuthoredNavigationAndResolveTheNewPath(t *testing.T) {
	service, config := contentNavigationFixture(t)
	original := installContentNavigation(t, service, config, &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "guide", Title: "Stable guide label"}}})
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	draft, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "move", Path: "guide.md", To: "handbook.md", Title: "Moved guide", OutputPath: "handbook/start.html"}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	manifest, err := loadManifest(config.Root)
	require.NoError(t, err)
	assert.Equal(t, original.Navigation, manifest.Navigation)
	view, err := site.ResolveNavigation(manifest, "guide")
	require.NoError(t, err)
	require.NotEmpty(t, view.Pages)
	assert.Equal(t, "guide", view.Pages[0].PageID)
	assert.Equal(t, "handbook/start.html", view.Pages[0].Path)
	assert.Equal(t, "Stable guide label", view.Pages[0].Title)
	assert.True(t, view.Pages[0].Current)
}
