package site

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPortableAppearanceChangesPublishedReaderAndProject(t *testing.T) {
	root := t.TempDir()
	theme := BuiltinThemes()[1]
	manifest := Manifest{Version: 1, Title: "Handbook <team>", Description: "A portable reader", Footer: "Maintained <locally>", Logo: "brand.png", Favicon: "brand.png", ThemeID: theme.ID, Theme: &theme, Assets: []string{"brand.png"}, Pages: []Page{{ID: "home", Title: "Home", Source: "index.md", Path: "index.html"}, {ID: "guide", Title: "Guide", Source: "guide.md", Path: "guide/start.html"}}, Navigation: &document.NavigationSettings{ShowTOC: false, ShowPreviousNext: true, StickyHeader: false}, Editor: &document.EditorSettings{DefaultFormat: "html", PreviewOnSave: true}, API: &document.APISettings{Enabled: true, ShowCurl: false, RequestTimeoutMs: 1500, MaxResponseBytes: 4096}, Publishing: &document.PublishingSettings{AutoPublish: true}}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs.json"), data, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "index.md"), []byte("# Home\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "guide.md"), []byte("# Guide\n\n## Installation\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "brand.png"), []byte("raster fixture"), 0o600))
	output := filepath.Join(t.TempDir(), "site")
	require.NoError(t, BuildContext(t.Context(), filepath.Join(root, "docs.json"), output, Options{}))
	html, err := os.ReadFile(filepath.Join(output, "guide", "start.html"))
	require.NoError(t, err)
	assert.Contains(t, string(html), `data-theme="light"`)
	assert.Contains(t, string(html), `href="../_docs/appearance.css"`)
	assert.Contains(t, string(html), `src="../assets/brand.png"`)
	assert.Contains(t, string(html), `rel="icon" href="../assets/brand.png"`)
	assert.Contains(t, string(html), `rel="prev" href="../index.html"`)
	assert.NotContains(t, string(html), `class="toc"`)
	assert.Contains(t, string(html), "Maintained &lt;locally&gt;")
	assert.NotContains(t, string(html), "unsafe-inline")
	css, err := os.ReadFile(filepath.Join(output, "_docs", "appearance.css"))
	require.NoError(t, err)
	assert.Contains(t, string(css), "Georgia")
	assert.Contains(t, string(css), "#fffbf5")
	assert.Contains(t, string(css), ".topbar{position:static}")
	data, err = os.ReadFile(filepath.Join(output, "_docs", "project.json"))
	require.NoError(t, err)
	var project document.Project
	require.NoError(t, json.Unmarshal(data, &project))
	assert.Equal(t, "paper", project.ThemeID)
	assert.Equal(t, manifest.Navigation, project.Navigation)
	assert.Equal(t, manifest.Editor, project.Editor)
	assert.Equal(t, manifest.API, project.API)
	assert.Equal(t, manifest.Publishing, project.Publishing)
	require.NoError(t, BuildContext(t.Context(), filepath.Join(root, "docs.json"), output, Options{Check: true}))
}

func TestThemeTokensAndBrandingRejectExecutableOrUnboundedValues(t *testing.T) {
	for _, mutate := range []func(*document.Theme){
		func(theme *document.Theme) { theme.ID = "../outside" },
		func(theme *document.Theme) { theme.ID = "_hidden" },
		func(theme *document.Theme) { theme.Name = strings.Repeat("x", 121) },
		func(theme *document.Theme) { theme.Description = "bad\x00text" },
		func(theme *document.Theme) { theme.Mode = "javascript" },
		func(theme *document.Theme) { theme.Accent = "red; background:url(https://external.invalid/)" },
		func(theme *document.Theme) { theme.Font = "url(https://external.invalid/font)" },
		func(theme *document.Theme) { theme.Density = "arbitrary-css" },
		func(theme *document.Theme) { theme.Width = "100vw" },
		func(theme *document.Theme) { theme.Radius = 25 },
		func(theme *document.Theme) { theme.Light = &document.ThemePalette{Text: "var(--injected)"} },
	} {
		theme := DefaultTheme()
		mutate(&theme)
		require.Error(t, ValidateTheme(theme))
	}
	for _, theme := range BuiltinThemes() {
		require.NoError(t, ValidateTheme(theme))
	}
	original := BuiltinThemes()[1].Light.Background
	BuiltinThemes()[1].Light.Background = "#000000"
	assert.Equal(t, original, BuiltinThemes()[1].Light.Background)
	for _, image := range []string{"../secret.png", "https://external.invalid/image.png", "logo.svg", "undeclared.png", "javascript:alert(1)"} {
		manifest := Manifest{Title: "Docs", Logo: image, Assets: []string{"logo.svg"}}
		require.Error(t, manifest.ValidateAppearance(), image)
	}
	manifest := Manifest{Title: "Docs", ThemeID: "missing"}
	require.Error(t, manifest.ValidateAppearance())
	theme := DefaultTheme()
	manifest.ThemeID, manifest.Theme = "other", &theme
	require.Error(t, manifest.ValidateAppearance())
	require.Error(t, ValidateManifest(nil))
}

func TestAPISettingsDisableInteractiveControlsAndConfigureClient(t *testing.T) {
	api := &document.API{Protocol: "http", Operations: []document.Operation{{ID: "ping", Anchor: "api-ping", Method: "GET", Path: "/ping"}}}
	manifest := Manifest{Version: 1, Title: "Docs", Pages: []Page{{ID: "home", Title: "API", Source: "api.yaml", Path: "index.html", Kind: "openapi"}}, API: &document.APISettings{Enabled: false, ShowCurl: false, RequestTimeoutMs: 1500, MaxResponseBytes: 4096}}
	content, err := renderAPI(context.Background(), api, 0, manifest, "_docs/pages/api.json")
	require.NoError(t, err)
	assert.False(t, content.Interactive)
	assert.NotContains(t, content.HTML, "data-api-operation")
	disabledHTML, err := renderPage(manifest, 0, content)
	require.NoError(t, err)
	assert.NotContains(t, string(disabledHTML), "connect-src 'self' http: https:")
	manifest.API.Enabled = true
	content, err = renderAPI(context.Background(), api, 0, manifest, "_docs/pages/api.json")
	require.NoError(t, err)
	assert.True(t, content.Interactive)
	html, err := renderPage(manifest, 0, content)
	require.NoError(t, err)
	assert.Contains(t, string(html), "connect-src 'self' http: https:;")
	assert.Contains(t, string(html), `data-api-show-curl="false"`)
	assert.Contains(t, string(html), `data-api-timeout="1500"`)
	assert.Contains(t, string(html), `data-api-response-bytes="4096"`)
}
