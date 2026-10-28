package site

import (
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinResolutionAndCustomTypographyAffectTheReader(t *testing.T) {
	manifest := Manifest{Title: "Docs", ThemeID: "paper", Pages: []Page{{ID: "home", Title: "Home", Source: "index.md", Path: "index.html"}, {ID: "guide", Title: "Guide", Source: "guide.md", Path: "guide.html"}}}
	appearance := manifest.Appearance()
	assert.Equal(t, "paper", appearance.Theme.ID)
	assert.Equal(t, DefaultTheme().Font, appearance.Theme.Font)
	assert.Equal(t, DefaultTheme().Width, appearance.Theme.Width)
	assert.NotEqual(t, DefaultTheme().Light.Background, appearance.Theme.Light.Background)
	require.NoError(t, manifest.ValidateAppearance())
	page, err := renderPage(manifest, 0, pageContent{HTML: "<p>Home</p>"})
	require.NoError(t, err)
	assert.Contains(t, string(page), `data-theme="light"`)
	assert.Contains(t, string(page), `rel="next"`)

	theme := DefaultTheme()
	theme.ID, theme.Name, theme.Font, theme.Radius = "terminal", "Terminal", "mono", 0
	manifest.ThemeID, manifest.Theme = theme.ID, &theme
	manifest.Navigation = &document.NavigationSettings{ShowTOC: true, StickyHeader: true}
	require.NoError(t, manifest.ValidateAppearance())
	css := appearanceCSS(manifest)
	assert.Contains(t, css, "ui-monospace,SFMono-Regular")
	assert.Contains(t, css, "--docs-radius:0px")
	page, err = renderPage(manifest, 0, pageContent{HTML: "<p>Home</p>"})
	require.NoError(t, err)
	assert.NotContains(t, string(page), `aria-label="Adjacent pages"`)
	assert.NotContains(t, string(page), `data-theme="auto"`)
	assert.Contains(t, string(page), `class="toc"`)
}

func TestThemeIdentifiersAndColorsStayWithinThePortableVocabulary(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("a", 65), "with.dot", "with/slash", "日本語"} {
		theme := DefaultTheme()
		theme.ID = id
		require.Error(t, ValidateTheme(theme))
	}
	for _, accent := range []string{"##12345", "#abcdex", "#ABCDEX", "#12345g"} {
		theme := DefaultTheme()
		theme.Accent = accent
		require.Error(t, ValidateTheme(theme))
	}
	manifest := Manifest{Title: "Docs", ThemeID: "with.dot"}
	require.Error(t, manifest.ValidateAppearance())
	theme := DefaultTheme()
	theme.Accent = "#AbC123"
	require.NoError(t, ValidateTheme(theme))
}
