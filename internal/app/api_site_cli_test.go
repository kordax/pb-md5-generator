package app

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSiteCommandGeneratesOpaqueIgnoredTypeExamples(t *testing.T) {
	project, output := t.TempDir(), filepath.Join(t.TempDir(), "site")
	config := filepath.Join(project, "docs.json")
	require.NoError(t, os.WriteFile(config, []byte(`{"version":1,"title":"Opaque examples","pages":[{"title":"API","source":"api.proto","path":"index.html","kind":"protobuf"}]}`), 0o600))
	source := `syntax = "proto3";
package opaque;
// @autocode[json]
message Visible {
  string public_field = 1;
  HiddenType public_hidden = 2;
}
// @ignore
message HiddenType { string hidden_example_shape_marker = 1; }
`
	require.NoError(t, os.WriteFile(filepath.Join(project, "api.proto"), []byte(source), 0o600))
	require.Zero(t, Run([]string{"build", "-config", config, "-o", output}))
	page, err := os.ReadFile(filepath.Join(output, "index.html"))
	require.NoError(t, err)
	assert.Contains(t, string(page), `class="language-json"`)
	assert.Contains(t, string(page), "public_field")
	assert.NotContains(t, string(page), "hidden_example_shape_marker")
	assert.Less(t, len(page), 1<<20)
	err = filepath.WalkDir(filepath.Join(output, "_docs", "pages"), func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		assert.NotContains(t, string(data), "hidden_example_shape_marker")
		return nil
	})
	require.NoError(t, err)
}

func TestSiteCommandExportsMixedAPIModelsAndAssets(t *testing.T) {
	project, output := t.TempDir(), filepath.Join(t.TempDir(), "site")
	config := filepath.Join(project, "docs.json")
	files := map[string]string{
		"docs.json": `{"version":1,"title":"Mixed reference","pages":[{"id":"guide","title":"Guide","source":"index.md","path":"index.html"},{"id":"rpc","title":"RPC","source":"api.proto","path":"rpc/index.html","kind":"protobuf"},{"id":"http","title":"HTTP","source":"openapi.yaml","path":"http/index.html","kind":"openapi"}]}`,
		"index.md":  "# Guide\n\n[RPC](api.proto) and [HTTP](openapi.yaml).",
		"api.proto": `syntax = "proto3";
package mixed;
message Visible {
  string public_field = 1;
  // @ignore
  string hidden_field_marker = 2;
  HiddenType public_hidden = 3;
}
// @ignore
message HiddenType { string hidden_shape_marker = 1; }
service PublicService {
  rpc Get(Visible) returns (Visible);
  // @ignore
  rpc SecretRPCMarker(Visible) returns (Visible);
}
// @ignore
service SecretServiceMarker { rpc Hidden(Visible) returns (Visible); }
`,
		"openapi.yaml": `openapi: 3.1.1
info:
  title: Local HTTP API
  version: v1
servers:
  - url: https://api.example.test/v1
paths:
  /pets/{id}:
    get:
      operationId: getPet
      summary: Read a pet
      security:
        - apiKey: []
      parameters:
        - name: id
          in: path
          required: true
          schema: {type: integer, format: int64}
          example: 9007199254740993
      responses:
        '200':
          description: A pet
          content:
            application/json:
              schema: {$ref: './models.json#/Pet'}
              example: {id: 18446744073709551615}
components:
  securitySchemes:
    apiKey: {type: apiKey, name: X-API-Key, in: header}
`,
		"models.json": `{"Pet":{"type":"object","properties":{"id":{"type":"integer"},"next":{"$ref":"#/Pet"}}}}`,
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(project, name), []byte(content), 0o600))
	}
	require.Zero(t, Run([]string{"build", "-config", config, "-o", output}))
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(name)))
		require.NoError(t, err, "%s", name)
		return data
	}
	guide, rpc, http := string(read("index.html")), string(read("rpc/index.html")), string(read("http/index.html"))
	assert.Contains(t, guide, `href="rpc/index.html"`)
	assert.Contains(t, guide, `href="http/index.html"`)
	assert.NotContains(t, guide, "api-client.js")
	assert.Contains(t, rpc, "PublicService.Get")
	assert.Contains(t, rpc, "public_field")
	assert.Contains(t, rpc, "api.css")
	assert.NotContains(t, rpc, "api-client.js")
	assert.NotContains(t, rpc, "Try request")
	assert.Contains(t, http, "Try request")
	assert.Contains(t, http, "data-api-operation=")
	assert.Contains(t, http, "api-client.js")
	assert.Contains(t, http, "api.css")
	assert.Contains(t, http, "X-API-Key")
	assert.Contains(t, http, "18446744073709551615")
	assert.NotContains(t, http, `href="../models.json`)
	for _, marker := range []string{"hidden_field_marker", "hidden_shape_marker", "SecretRPCMarker", "SecretServiceMarker"} {
		assert.NotContains(t, rpc, marker)
	}
	for _, asset := range []string{"_docs/api-client.js", "_docs/api.css"} {
		assert.NotEmpty(t, read(asset), asset)
	}
	var projectModel map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(read("_docs/project.json"), &projectModel))
	assert.NotEmpty(t, projectModel["pages"])

	type exportedPage struct {
		Version int               `json:"version"`
		ID      string            `json:"id"`
		Path    string            `json:"path"`
		Kind    string            `json:"kind"`
		Blocks  []json.RawMessage `json:"blocks"`
		API     *document.API     `json:"api"`
	}
	models := make(map[string]exportedPage)
	modelFiles := make(map[string]string)
	err := filepath.WalkDir(filepath.Join(output, "_docs", "pages"), func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(name) != ".json" {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		var page exportedPage
		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}
		assert.Equal(t, 1, page.Version)
		assert.NotNil(t, page.Blocks)
		models[page.ID], modelFiles[page.ID] = page, name
		if page.Kind == "protobuf" {
			for _, marker := range []string{"hidden_field_marker", "hidden_shape_marker", "SecretRPCMarker", "SecretServiceMarker"} {
				assert.NotContains(t, string(data), marker, "ignored material leaked into exported protobuf page model")
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Len(t, models, 3)
	require.NotNil(t, models["rpc"].API)
	assert.Equal(t, "grpc", models["rpc"].API.Protocol)
	require.Len(t, models["rpc"].API.Operations, 1)
	assert.Equal(t, "Get", models["rpc"].API.Operations[0].RPCMethod)
	for _, schema := range models["rpc"].API.Schemas {
		assert.NotEqual(t, "HiddenType", schema.Name)
	}
	require.NotNil(t, models["http"].API)
	assert.Equal(t, "http", models["http"].API.Protocol)
	require.Len(t, models["http"].API.Operations, 1)
	operation := models["http"].API.Operations[0]
	assert.Equal(t, "GET", operation.Method)
	assert.Equal(t, "9007199254740993", operation.Parameters[0].ExampleJSON)
	assert.Equal(t, `{"id":18446744073709551615}`, operation.Responses[0].Content[0].ExampleJSON)
	assert.Len(t, models["http"].API.SecuritySchemes, 1)
	assert.Len(t, models["http"].API.Documents, 2)
	assert.NotEmpty(t, models["guide"].Blocks)
	assert.Nil(t, models["guide"].API)
	for id, modelFile := range modelFiles {
		if id == "guide" {
			continue
		}
		relative, err := filepath.Rel(filepath.Dir(filepath.Join(output, filepath.FromSlash(models[id].Path))), modelFile)
		require.NoError(t, err)
		pageHTML := rpc
		if id == "http" {
			pageHTML = http
		}
		assert.Contains(t, pageHTML, filepath.ToSlash(relative), "API page should link its complete exported model")
	}
	require.Zero(t, Run([]string{"build", "-config", config, "-o", output, "-check"}))
	require.NoError(t, os.WriteFile(filepath.Join(project, "openapi.yaml"), []byte(strings.Replace(files["openapi.yaml"], "Read a pet", "Read the changed pet", 1)), 0o600))
	assert.Equal(t, 1, Run([]string{"build", "-config", config, "-o", output, "-check"}))
	assert.Equal(t, http, string(read("http/index.html")), "check should preserve the previous site")
}
