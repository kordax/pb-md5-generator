package document_test

import (
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppearanceSchemasCarryPortableTokensWithoutExecutableFields(t *testing.T) {
	theme := site.BuiltinThemes()[1]
	manifest := site.Manifest{Version: 1, Title: "Docs", ThemeID: theme.ID, Theme: &theme, Logo: "logo.png", Footer: "Team handbook", Assets: []string{"logo.png"}, Pages: []site.Page{{ID: "home", Title: "Home", Source: "index.html", Path: "index.html", Kind: "html"}}, Navigation: &document.NavigationSettings{ShowTOC: true}, Editor: &document.EditorSettings{DefaultFormat: "html"}, API: &document.APISettings{Enabled: true, ShowCurl: true, RequestTimeoutMs: 30000, MaxResponseBytes: 1048576}, Publishing: &document.PublishingSettings{AutoPublish: true}}
	for _, kind := range []string{"manifest", "project"} {
		t.Run(kind, func(t *testing.T) {
			schema := compileDocumentationSchema(t, kind)
			var model any = manifest
			if kind == "project" {
				model = site.ProjectModel(manifest)
			}
			require.NoError(t, schema.Validate(modelSchemaData(t, model)))
			for _, mutate := range []func(map[string]any){
				func(value map[string]any) { value["theme"].(map[string]any)["css"] = "body{display:none}" },
				func(value map[string]any) {
					value["theme"].(map[string]any)["accent"] = "url(https://external.invalid/)"
				},
				func(value map[string]any) { value["theme"].(map[string]any)["font"] = "remote-font" },
				func(value map[string]any) { value["theme"].(map[string]any)["radius"] = 25 },
				func(value map[string]any) {
					value["theme"].(map[string]any)["light"].(map[string]any)["background"] = "red"
				},
				func(value map[string]any) { value["api"].(map[string]any)["requestTimeoutMs"] = 30001 },
				func(value map[string]any) { value["api"].(map[string]any)["maxResponseBytes"] = 1048577 },
				func(value map[string]any) { value["editor"].(map[string]any)["defaultFormat"] = "script" },
			} {
				value := modelSchemaData(t, model).(map[string]any)
				mutate(value)
				assert.Error(t, schema.Validate(value))
			}
		})
	}
}
