package site

import (
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"
)

func groupedRenderFixture() Manifest {
	collapsed := true
	return Manifest{Version: 1, Title: "Grouped docs", Pages: []Page{
		{ID: "home", Title: "Home", Source: "index.md", Path: "index.html", Kind: "markdown"},
		{ID: "start", Title: "Getting started", Source: "start.md", Path: "guides/start.html", Kind: "markdown"},
		{ID: "api", Title: "API reference", Source: "api.md", Path: "reference/api.html", Kind: "markdown"},
		{ID: "other", Title: "Additional page", Source: "other.md", Path: "other.html", Kind: "markdown"},
	}, Navigation: &document.NavigationSettings{ShowTOC: true, ShowPreviousNext: true, StickyHeader: true, Tree: &document.NavigationTree{UnlistedPages: "append", Items: []document.NavigationNode{
		{Kind: "group", ID: "guides", Title: "Guides", Description: "<img src=x onerror=attack()>", Collapsed: &collapsed, Children: []document.NavigationNode{
			{Kind: "page", PageID: "api"},
			{Kind: "group", ID: "advanced", Title: "Advanced", Collapsed: &collapsed, Children: []document.NavigationNode{{Kind: "page", PageID: "start", Title: "Start here"}}},
		}},
		{Kind: "group", ID: "inactive", Title: "Overview", Collapsed: &collapsed, Children: []document.NavigationNode{{Kind: "page", PageID: "home"}}},
	}}}}
}

func navAttribute(node *html.Node, key string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}

func navHasAttribute(node *html.Node, key string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return true
		}
	}
	return false
}

func navElements(root *html.Node, matches func(*html.Node) bool) []*html.Node {
	var result []*html.Node
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && matches(node) {
			result = append(result, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return result
}

func TestGroupedNavigationRendersNativeSectionsAndDFSAdjacentPages(t *testing.T) {
	manifest := groupedRenderFixture()
	require.NoError(t, ValidateManifest(&manifest))
	data, err := renderPage(manifest, 1, pageContent{HTML: "<p>Guide body</p>"})
	require.NoError(t, err)
	root, err := html.Parse(strings.NewReader(string(data)))
	require.NoError(t, err)
	navigation := navElements(root, func(node *html.Node) bool {
		return node.Data == "nav" && navAttribute(node, "aria-label") == "Documentation"
	})
	require.Len(t, navigation, 1)
	links := navElements(navigation[0], func(node *html.Node) bool { return node.Data == "a" })
	var urls []string
	for _, link := range links {
		urls = append(urls, navAttribute(link, "href"))
	}
	assert.Equal(t, []string{"../reference/api.html", "start.html", "../index.html", "../other.html"}, urls)
	current := navElements(navigation[0], func(node *html.Node) bool { return navAttribute(node, "aria-current") == "page" })
	require.Len(t, current, 1)
	assert.Equal(t, "start.html", navAttribute(current[0], "href"))
	for _, id := range []string{"guides", "advanced", "inactive"} {
		groups := navElements(navigation[0], func(node *html.Node) bool {
			return node.Data == "details" && navAttribute(node, "id") == "_docs-nav-"+id
		})
		require.Len(t, groups, 1)
		assert.Equal(t, id != "inactive", navHasAttribute(groups[0], "open"), id)
		assert.Equal(t, "summary", groups[0].FirstChild.Data)
	}
	assert.Empty(t, navElements(navigation[0], func(node *html.Node) bool { return node.Data == "img" || node.Data == "script" }))
	assert.Contains(t, string(data), "&lt;img src=x onerror=attack()&gt;")
	adjacent := navElements(root, func(node *html.Node) bool {
		return node.Data == "a" && (navAttribute(node, "rel") == "prev" || navAttribute(node, "rel") == "next")
	})
	require.Len(t, adjacent, 2)
	assert.Equal(t, "../reference/api.html", navAttribute(adjacent[0], "href"))
	assert.Equal(t, "../index.html", navAttribute(adjacent[1], "href"))
	breadcrumbs := navElements(root, func(node *html.Node) bool {
		return node.Data == "nav" && navAttribute(node, "aria-label") == "Breadcrumb"
	})
	require.Len(t, breadcrumbs, 1)
	items := navElements(breadcrumbs[0], func(node *html.Node) bool { return node.Data == "li" })
	var labels []string
	for _, item := range items {
		labels = append(labels, item.FirstChild.Data)
	}
	assert.Equal(t, []string{"Grouped docs", "Guides", "Advanced", "Start here"}, labels)
	assert.Contains(t, siteCSS, ".mobile-navigation>summary{display:none}")
	assert.NotContains(t, siteCSS, ".mobile-navigation summary{display:none}")
	assert.Contains(t, siteCSS, ".navigation .nav-group>summary{display:list-item;")
}

func TestGroupedNavigationExplicitEmptyAndOmittedPageKeepCustomSemantics(t *testing.T) {
	manifest := groupedRenderFixture()
	manifest.Navigation.Tree = &document.NavigationTree{UnlistedPages: "omit", Items: []document.NavigationNode{}}
	data, err := renderPage(manifest, 1, pageContent{HTML: "<p>Still directly readable</p>"})
	require.NoError(t, err)
	root, err := html.Parse(strings.NewReader(string(data)))
	require.NoError(t, err)
	navigation := navElements(root, func(node *html.Node) bool {
		return node.Data == "nav" && navAttribute(node, "aria-label") == "Documentation"
	})
	require.Len(t, navigation, 1)
	assert.Empty(t, navElements(navigation[0], func(node *html.Node) bool { return node.Data == "a" || node.Data == "details" }))
	assert.NotContains(t, string(data), `aria-label="Adjacent pages"`)
	assert.Contains(t, string(data), `aria-label="Breadcrumb"`)
	assert.Contains(t, string(data), "Still directly readable")
	assert.NotContains(t, string(data), "_docs-nav-guides")

	manifest.Navigation.Tree.UnlistedPages = "append"
	data, err = renderPage(manifest, 1, pageContent{})
	require.NoError(t, err)
	assert.Contains(t, string(data), `rel="prev" href="../index.html"`)
	assert.Contains(t, string(data), `rel="next" href="../reference/api.html"`)
	manifest.Navigation.ShowPreviousNext = false
	data, err = renderPage(manifest, 1, pageContent{})
	require.NoError(t, err)
	assert.NotContains(t, string(data), `aria-label="Adjacent pages"`)
}

func TestGroupedNavigationFlatFallbackAndInvalidTreesDoNotReturnPartialHTML(t *testing.T) {
	manifest := groupedRenderFixture()
	manifest.Navigation.Tree = nil
	data, err := renderPage(manifest, 1, pageContent{HTML: "<p>Legacy body</p>"})
	require.NoError(t, err)
	assert.Contains(t, string(data), `<div class="breadcrumb">Grouped docs <span aria-hidden="true">/</span> Getting started</div>`)
	assert.NotContains(t, string(data), `class="nav-group"`)
	assert.Contains(t, string(data), `rel="prev" href="../index.html"`)
	assert.Contains(t, string(data), `rel="next" href="../reference/api.html"`)

	manifest.Navigation.Tree = &document.NavigationTree{Items: []document.NavigationNode{{Kind: "group", ID: "empty", Title: "Empty"}}}
	data, err = renderPage(manifest, 1, pageContent{})
	require.Error(t, err)
	assert.Nil(t, data)
	manifest.Navigation.Tree = &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "home"}}}
	data, err = renderPage(manifest, 0, pageContent{HTML: strings.Repeat("x", maxRenderedBytes)})
	require.Error(t, err)
	assert.Nil(t, data)
}
