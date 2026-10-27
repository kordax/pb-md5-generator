package studio

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeChecksConsumersWhenReferencedSchemaChanges(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Home\n")
	root := filepath.Join(config.DataDir, "content")
	manifestPath := filepath.Join(root, "docs.json")
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest site.Manifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	manifest.Pages = append(manifest.Pages, site.Page{ID: "api", Title: "Public API", Source: "public/api.yaml", Path: "public/api.html", Kind: "openapi"})
	data, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, data, 0o600))
	apiSource := "openapi: 3.1.0\ninfo: {title: Public API, version: 1.0.0}\npaths:\n  /ping:\n    get:\n      responses:\n        '200':\n          description: OK\n          content:\n            application/json:\n              schema: {$ref: './components.yaml'}\n"
	components := "type: object\nproperties:\n  value: {type: string}\n"
	for name, source := range map[string]string{
		"public/api.yaml":        apiSource,
		"public/components.yaml": components,
		"private.schema.yaml":    "type: object\nproperties:\n  hidden: {const: private-schema-content}\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(source), 0o600))
	}
	startRuntimeForTest(t, config)
	owner := newRuntimeClient(t, config)
	owner.login(t, "owner", ownerTestPassword)
	status, _, _ := owner.request(t, http.MethodPost, "/_docs/api/v1/users", map[string]any{
		"username": "editor", "displayName": "Scoped editor", "password": "Scoped-editor-cedar-library-2026", "role": "editor", "grants": map[string]any{"read": []string{"public"}, "write": []string{"public"}},
	})
	require.Equal(t, http.StatusCreated, status)
	editor := newRuntimeClient(t, config)
	editor.login(t, "editor", "Scoped-editor-cedar-library-2026")
	status, _, data = editor.request(t, http.MethodGet, "/_docs/api/v1/documents/api?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	var page apiweb.Document
	require.NoError(t, json.Unmarshal(data, &page))
	draftID, revision := uploadRuntimeText(t, editor, "public/components.yaml", page.Revision, "$ref: '../private.schema.yaml'\n")
	status, _, body := editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+draftID+"/publish", map[string]string{"revision": revision})
	assert.Equal(t, http.StatusForbidden, status)
	assert.NotContains(t, string(body), "private-schema-content")
	published, err := os.ReadFile(filepath.Join(root, "public", "components.yaml"))
	require.NoError(t, err)
	assert.Equal(t, components, string(published))
	status, _, body = editor.request(t, http.MethodGet, "/public/api.html", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.NotContains(t, string(body), "private-schema-content")
}
