package site

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func navigationPages(count int) []Page {
	pages := make([]Page, 0, count)
	for i := range count {
		page := Page{ID: fmt.Sprintf("page-%d", i), Title: fmt.Sprintf("Page %d", i), Source: fmt.Sprintf("page-%d.md", i), Path: fmt.Sprintf("page-%d.html", i), Kind: "markdown"}
		if i == 0 {
			page.ID, page.Source, page.Path = "home", "index.md", "index.html"
		}
		pages = append(pages, page)
	}
	return pages
}

func navigationTreeFixture() *document.NavigationTree {
	collapsed := true
	return &document.NavigationTree{Items: []document.NavigationNode{
		{Kind: "group", ID: "guides", Title: "Guides", Description: "Learn the project", Collapsed: &collapsed, Children: []document.NavigationNode{
			{Kind: "page", PageID: "page-1", Title: "Start here"},
			{Kind: "group", ID: "reference", Title: "Reference", Children: []document.NavigationNode{{Kind: "page", PageID: "page-2"}}},
		}},
		{Kind: "page", PageID: "home"},
	}}
}

func navigationIDs(items []NavigationItem) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item.Kind == "page" {
			result = append(result, item.PageID)
		} else {
			result = append(result, item.ID)
		}
	}
	return result
}

func TestNavigationResolvesStableReferencesDepthFirstSequenceAndAncestry(t *testing.T) {
	manifest := Manifest{Version: 1, Title: "Docs", Pages: navigationPages(4), Navigation: &document.NavigationSettings{Tree: navigationTreeFixture()}}
	original, err := json.Marshal(manifest.Navigation.Tree)
	require.NoError(t, err)
	view, err := ResolveNavigation(manifest, "page-2")
	require.NoError(t, err)
	assert.Equal(t, []string{"page-1", "page-2", "home", "page-3"}, navigationIDs(view.Pages))
	assert.Equal(t, []string{"guides", "home", "page-3"}, navigationIDs(view.Items))
	assert.Equal(t, []string{"guides", "reference", "page-2"}, navigationIDs(view.Breadcrumbs))
	assert.Equal(t, "Start here", view.Pages[0].Title)
	assert.Equal(t, "page-1.html", view.Pages[0].Path)
	assert.True(t, view.Items[0].Collapsed)
	assert.True(t, view.Items[0].Active)
	assert.True(t, view.Items[0].Children[1].Active)
	assert.True(t, view.Items[0].Children[1].Children[0].Current)
	assert.Empty(t, view.Items[0].Path)
	for _, crumb := range view.Breadcrumbs {
		assert.Nil(t, crumb.Children)
	}
	manifest.Pages[2].Path = "moved/reference.html"
	moved, err := ResolveNavigation(manifest, "page-2")
	require.NoError(t, err)
	assert.Equal(t, "moved/reference.html", moved.Pages[1].Path)
	after, err := json.Marshal(manifest.Navigation.Tree)
	require.NoError(t, err)
	assert.Equal(t, original, after, "resolving a moved page must not rewrite authored navigation identities")
}

func TestNavigationOmitAffectsSequenceWithoutRemovingPagesOrCurrentBreadcrumb(t *testing.T) {
	manifest := Manifest{Version: 1, Title: "Docs", Pages: navigationPages(4), Navigation: &document.NavigationSettings{Tree: navigationTreeFixture()}}
	manifest.Navigation.Tree.UnlistedPages = "omit"
	view, err := ResolveNavigation(manifest, "page-3")
	require.NoError(t, err)
	assert.Equal(t, []string{"page-1", "page-2", "home"}, navigationIDs(view.Pages))
	assert.Equal(t, []string{"page-3"}, navigationIDs(view.Breadcrumbs))
	assert.False(t, view.Items[0].Active)
	assert.Len(t, manifest.Pages, 4, "navigation omission does not filter page access or search catalog")
	view, err = ResolveNavigation(manifest, "missing")
	require.NoError(t, err)
	assert.Empty(t, view.Breadcrumbs)
}

func TestNavigationFilteringRemovesAllPrivateGroupMetadataAndOwnsCopies(t *testing.T) {
	tree := navigationTreeFixture()
	tree.UnlistedPages = "omit"
	tree.Items[0].Description = "PRIVATE-GROUP-DESCRIPTION"
	tree.Items[0].Children[1].Title = "PRIVATE-NESTED-LABEL"
	before, err := json.Marshal(tree)
	require.NoError(t, err)
	filtered, err := FilterNavigation(tree, map[string]bool{"home": true})
	require.NoError(t, err)
	require.NotNil(t, filtered)
	assert.Equal(t, "omit", filtered.UnlistedPages)
	require.Len(t, filtered.Items, 1)
	assert.Equal(t, "home", filtered.Items[0].PageID)
	encoded, err := json.Marshal(filtered)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "PRIVATE")
	assert.NotContains(t, string(encoded), "guides")
	assert.NotContains(t, string(encoded), "reference")
	empty, err := PruneNavigation(tree, nil)
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.NotNil(t, empty.Items)
	assert.Empty(t, empty.Items)
	encoded, err = json.Marshal(empty)
	require.NoError(t, err)
	assert.JSONEq(t, `{"unlistedPages":"omit","items":[]}`, string(encoded))
	clone, err := CloneNavigation(tree)
	require.NoError(t, err)
	clone.Items[0].Children[0].Title = "Changed clone"
	*clone.Items[0].Collapsed = false
	after, err := json.Marshal(tree)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.NotSame(t, clone.Items[0].Collapsed, tree.Items[0].Collapsed)
}

func TestNavigationLegacyAndExplicitEmptyTreesKeepDifferentSourceMeaning(t *testing.T) {
	for _, tree := range []*document.NavigationTree{nil, {Items: []document.NavigationNode{}}} {
		clone, err := CloneNavigation(tree)
		require.NoError(t, err)
		filtered, err := FilterNavigation(tree, nil)
		require.NoError(t, err)
		if tree == nil {
			assert.Nil(t, clone)
			assert.Nil(t, filtered)
		} else {
			require.NotNil(t, clone)
			require.NotNil(t, clone.Items)
			require.NotNil(t, filtered)
			require.NotNil(t, filtered.Items)
		}
		manifest := Manifest{Pages: navigationPages(3), Navigation: &document.NavigationSettings{Tree: tree}}
		view, err := ResolveNavigation(manifest, "home")
		require.NoError(t, err)
		assert.Equal(t, []string{"home", "page-1", "page-2"}, navigationIDs(view.Pages))
		assert.Equal(t, []string{"home"}, navigationIDs(view.Breadcrumbs))
	}
	view, err := ResolveNavigation(Manifest{}, "")
	require.NoError(t, err)
	assert.NotNil(t, view.Items)
	assert.NotNil(t, view.Pages)
	assert.NotNil(t, view.Breadcrumbs)
}

func TestNavigationRejectsAmbiguousOrUnsafeSourceNodesWithoutDisclosingMetadata(t *testing.T) {
	falseValue := false
	for _, scenario := range []struct {
		name string
		tree *document.NavigationTree
	}{
		{"missing items", &document.NavigationTree{}},
		{"policy", &document.NavigationTree{UnlistedPages: "redirect", Items: []document.NavigationNode{}}},
		{"unknown kind", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "external", PageID: "home"}}}},
		{"unsafe page", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "../private"}}}},
		{"duplicate page", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "home"}, {Kind: "page", PageID: "home"}}}},
		{"page group ID", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "home", ID: "bad"}}}},
		{"page description", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "home", Description: "bad"}}}},
		{"page collapsed false", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "home", Collapsed: &falseValue}}}},
		{"page children", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "home", Children: []document.NavigationNode{}}}}},
		{"missing group label", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "group", ID: "group", Children: []document.NavigationNode{{Kind: "page", PageID: "home"}}}}}},
		{"unsafe group ID", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "group", ID: "Group.Bad", Title: "Group", Children: []document.NavigationNode{{Kind: "page", PageID: "home"}}}}}},
		{"empty group", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "group", ID: "group", Title: "Group", Children: []document.NavigationNode{}}}}},
		{"group page ID", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "group", ID: "group", Title: "Group", PageID: "home", Children: []document.NavigationNode{{Kind: "page", PageID: "home"}}}}}},
		{"duplicate group ID", &document.NavigationTree{Items: []document.NavigationNode{{Kind: "group", ID: "group", Title: "One", Children: []document.NavigationNode{{Kind: "page", PageID: "home"}}}, {Kind: "group", ID: "group", Title: "Two", Children: []document.NavigationNode{{Kind: "page", PageID: "page-1"}}}}}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			assert.Error(t, ValidateNavigation(scenario.tree, navigationPages(2)))
			_, err := CloneNavigation(scenario.tree)
			assert.Error(t, err)
			_, err = FilterNavigation(scenario.tree, nil)
			assert.Error(t, err)
			_, err = ResolveNavigation(Manifest{Pages: navigationPages(2), Navigation: &document.NavigationSettings{Tree: scenario.tree}}, "home")
			assert.Error(t, err)
		})
	}
	missing := &document.NavigationTree{Items: []document.NavigationNode{{Kind: "group", ID: "private-group", Title: "PRIVATE LABEL", Children: []document.NavigationNode{{Kind: "page", PageID: "private-missing-page"}}}}}
	err := ValidateNavigation(missing, navigationPages(1))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "PRIVATE LABEL")
	assert.NotContains(t, err.Error(), "private-missing-page")
	assert.NotContains(t, err.Error(), "private-group")
}

func navigationAtDepth(depth int) *document.NavigationTree {
	node := document.NavigationNode{Kind: "page", PageID: "home"}
	for i := 1; i < depth; i++ {
		node = document.NavigationNode{Kind: "group", ID: fmt.Sprintf("group-%d", i), Title: "Group", Children: []document.NavigationNode{node}}
	}
	return &document.NavigationTree{Items: []document.NavigationNode{node}}
}

func TestNavigationBudgetsBoundGoCyclesDepthNodeCountAndEscapedBytes(t *testing.T) {
	cycle := make([]document.NavigationNode, 1)
	cycle[0] = document.NavigationNode{Kind: "group", ID: "cycle", Title: "Cycle", Children: cycle}
	cyclic := &document.NavigationTree{Items: cycle}
	assert.ErrorContains(t, ValidateNavigation(cyclic, navigationPages(1)), "cycles")
	_, err := CloneNavigation(cyclic)
	assert.Error(t, err)
	require.NoError(t, ValidateNavigation(navigationAtDepth(MaxNavigationDepth), navigationPages(1)))
	assert.ErrorContains(t, ValidateNavigation(navigationAtDepth(MaxNavigationDepth+1), navigationPages(1)), "depth")
	pages := navigationPages(2048)
	large := &document.NavigationTree{Items: make([]document.NavigationNode, 0, 2048)}
	for i, page := range pages {
		large.Items = append(large.Items, document.NavigationNode{Kind: "group", ID: fmt.Sprintf("g-%d", i), Title: "G", Children: []document.NavigationNode{{Kind: "page", PageID: page.ID}}})
	}
	require.NoError(t, ValidateNavigation(large, pages), "4096 total nodes are supported")
	large.Items = append(large.Items, document.NavigationNode{Kind: "page", PageID: "additional-page"})
	_, err = CloneNavigation(large)
	assert.ErrorContains(t, err, "node limit")
	overBytes := &document.NavigationTree{Items: []document.NavigationNode{}}
	for i, page := range navigationPages(60) {
		overBytes.Items = append(overBytes.Items, document.NavigationNode{Kind: "group", ID: fmt.Sprintf("group-%d", i), Title: strings.Repeat("<", 256), Description: strings.Repeat("<", 512), Children: []document.NavigationNode{{Kind: "page", PageID: page.ID}}})
	}
	assert.ErrorContains(t, ValidateNavigation(overBytes, navigationPages(60)), "encoded byte limit")
	_, err = FilterNavigation(overBytes, nil)
	assert.ErrorContains(t, err, "encoded byte limit", "filtering must validate the original resource budget first")
	tooMany := &document.NavigationTree{Items: make([]document.NavigationNode, MaxNavigationNodes+1)}
	_, err = CloneNavigation(tooMany)
	assert.ErrorContains(t, err, "node limit")
}

func TestNavigationLabelsUseUTF8ByteBudgetsAndPlainTextControlRules(t *testing.T) {
	for _, title := range []string{strings.Repeat("界", 86), "line\nlabel", "tab\tlabel", "   ", string([]byte{0xff})} {
		tree := navigationAtDepth(2)
		tree.Items[0].Title = title
		assert.Error(t, ValidateNavigation(tree, navigationPages(1)))
	}
	tree := navigationAtDepth(2)
	tree.Items[0].Title = strings.Repeat("界", 85)
	tree.Items[0].Description = "Line one\nLine two\tadditional text"
	require.NoError(t, ValidateNavigation(tree, navigationPages(1)))
	tree.Items[0].Description = "bad\rdescription"
	assert.Error(t, ValidateNavigation(tree, navigationPages(1)))
	tree.Items[0].Description = strings.Repeat("d", MaxNavigationDescriptionBytes+1)
	assert.Error(t, ValidateNavigation(tree, navigationPages(1)))
	pages := navigationPages(1)
	pages[0].ID = ""
	pages[0].Title = "Legacy\npage title"
	view, err := ResolveNavigation(Manifest{Pages: pages}, document.PageID("index.html"))
	require.NoError(t, err)
	assert.Equal(t, document.PageID("index.html"), view.Pages[0].PageID)
	assert.True(t, view.Pages[0].Current)
}

func TestNavigationSourceJSONHonorsAuthoredAndRawDepthAndFieldPresence(t *testing.T) {
	manifest := Manifest{Version: 1, Title: "Docs", Pages: navigationPages(1), Navigation: &document.NavigationSettings{Tree: navigationAtDepth(8)}}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	decoded, err := DecodeManifest(data)
	require.NoError(t, err)
	require.NotNil(t, decoded.Navigation.Tree)
	manifest.Navigation.Tree = navigationAtDepth(9)
	data, err = json.Marshal(manifest)
	require.NoError(t, err)
	_, err = DecodeManifest(data)
	assert.ErrorContains(t, err, "depth")
	raw := `{"version":1,"title":"Docs","pages":[{"id":"home","title":"Home","source":"index.md","path":"index.html"}],"extra":` + strings.Repeat("[", maxManifestJSONDepth+1) + "0" + strings.Repeat("]", maxManifestJSONDepth+1) + "}"
	_, err = DecodeManifest([]byte(raw))
	assert.ErrorContains(t, err, "JSON nesting")
	for _, node := range []string{
		`{"kind":"page","pageId":"home","id":""}`,
		`{"kind":"page","pageId":"home","description":""}`,
		`{"kind":"page","pageId":"home","collapsed":false}`,
		`{"kind":"page","pageId":"home","children":[]}`,
		`{"kind":"group","id":"group","title":"Group","pageId":"","children":[{"kind":"page","pageId":"home"}]}`,
		`{"kind":"page","pageId":"home","title":null}`,
		`{"kind":"page","pageId":"home","url":"https://example.test"}`,
	} {
		raw := `{"version":1,"title":"Docs","pages":[{"id":"home","title":"Home","source":"index.md","path":"index.html"}],"navigation":{"tree":{"items":[` + node + `]}}}`
		_, err := DecodeManifest([]byte(raw))
		assert.Error(t, err, node)
	}
}

func TestNavigationResolversRejectAmbiguousPageCatalogsAndUnsafeProjectExports(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		mutate func([]Page) []Page
	}{
		{"unsafe identity", func(pages []Page) []Page { pages[0].ID = "../unsafe"; return pages }},
		{"external destination", func(pages []Page) []Page { pages[0].Path = "https://example.invalid/doc.html"; return pages }},
		{"empty title", func(pages []Page) []Page { pages[0].Title = ""; return pages }},
		{"invalid UTF8 title", func(pages []Page) []Page { pages[0].Title = string([]byte{0xff}); return pages }},
		{"duplicate output", func(pages []Page) []Page { pages[1].Path = pages[0].Path; return pages }},
		{"duplicate identity", func(pages []Page) []Page { pages[1].ID = pages[0].ID; return pages }},
		{"catalog limit", func([]Page) []Page { return navigationPages(maxPages + 1) }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			pages := scenario.mutate(navigationPages(2))
			_, err := ResolveNavigation(Manifest{Pages: pages}, "home")
			assert.Error(t, err)
			tree := &document.NavigationTree{Items: []document.NavigationNode{}}
			assert.Error(t, ValidateNavigation(tree, pages))
			model := ProjectModel(Manifest{Pages: pages, Navigation: &document.NavigationSettings{Tree: tree}})
			_, err = EncodeProjectModel(context.Background(), model)
			assert.Error(t, err)
		})
	}
	cycle := make([]document.NavigationNode, 1)
	cycle[0] = document.NavigationNode{Kind: "group", ID: "cycle", Title: "Cycle", Children: cycle}
	model := ProjectModel(Manifest{Pages: navigationPages(1), Navigation: &document.NavigationSettings{Tree: &document.NavigationTree{Items: cycle}}})
	_, err := EncodeProjectModel(context.Background(), model)
	assert.ErrorContains(t, err, "cycles")
	model.Navigation.Tree = &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "missing-page"}}}
	_, err = EncodeProjectModel(context.Background(), model)
	assert.ErrorContains(t, err, "not in the project")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		_, err = EncodeProjectModel(ctx, model)
		assert.Error(t, err)
	}
}

func TestNavigationProjectModelExportsTheAuthoredTree(t *testing.T) {
	manifest := Manifest{Version: 1, Title: "Docs", Pages: navigationPages(4), Navigation: &document.NavigationSettings{ShowTOC: true, Tree: navigationTreeFixture()}}
	require.NoError(t, ValidateManifest(&manifest))
	project := ProjectModel(manifest)
	encoded, err := EncodeProjectModel(context.Background(), project)
	require.NoError(t, err)
	var decoded document.Project
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.NotNil(t, decoded.Navigation)
	assert.Equal(t, manifest.Navigation.Tree, decoded.Navigation.Tree)
	assert.Contains(t, string(encoded), `"pageId":"page-2"`)
	assert.NotContains(t, string(encoded), `"url"`)
}
