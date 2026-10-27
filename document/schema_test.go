package document_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/importer"
	"github.com/kordax/pb-md5-generator/internal/site"
	jsonschema "github.com/pb33f/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type denySchemaLoader struct {
	calls *atomic.Int32
}

func (loader denySchemaLoader) Load(url string) (any, error) {
	loader.calls.Add(1)
	return nil, fmt.Errorf("external schema loading is forbidden: %s", url)
}

func decodeSchemaData(t *testing.T, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	require.NoError(t, decoder.Decode(&value))
	return value
}

func compileDocumentationSchema(t *testing.T, kind string) *jsonschema.Schema {
	t.Helper()
	data, err := document.JSONSchema(kind)
	require.NoError(t, err)
	value := decodeSchemaData(t, data)
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	var loads atomic.Int32
	compiler.UseLoader(denySchemaLoader{calls: &loads})
	meta, err := compiler.Compile("https://json-schema.org/draft/2020-12/schema")
	require.NoError(t, err)
	require.NoError(t, meta.Validate(value), "schema must conform to the Draft 2020-12 meta-schema")
	id := "urn:pb-docs:" + kind + ":v1"
	require.NoError(t, compiler.AddResource(id, value))
	schema, err := compiler.Compile(id)
	require.NoError(t, err)
	assert.Equal(t, 2020, schema.DraftVersion)
	assert.Zero(t, loads.Load(), "all references must resolve without a URL loader")
	return schema
}

func modelSchemaData(t *testing.T, model any) any {
	t.Helper()
	data, err := json.Marshal(model)
	require.NoError(t, err)
	return decodeSchemaData(t, data)
}

func schemaPage() document.Page {
	return document.Page{
		Schema: "urn:pb-docs:page:v1", Format: "pb-docs/page", Version: document.FormatVersion,
		ID: "guide", Title: "Guide", Path: "guide.html", Kind: "markdown",
		Source: document.Location{File: "guide.md", Line: 1, Column: 1},
		Blocks: []document.Block{},
	}
}

func schemaProject(page document.Page) document.Project {
	return document.Project{
		Schema: "urn:pb-docs:project:v1", Format: "pb-docs/project", Version: document.FormatVersion,
		Title: "Documentation", Description: "A portable project", Root: "../",
		Source: document.Location{File: "docs.json"},
		Pages:  []document.PageReference{{ID: page.ID, Title: page.Title, Path: page.Path, Source: page.Source, Kind: page.Kind, Data: "pages/guide.json"}},
	}
}

func TestSchemasAreStandaloneDraft202012(t *testing.T) {
	for _, kind := range document.SchemaNames() {
		t.Run(kind, func(t *testing.T) {
			compileDocumentationSchema(t, kind)
			data, err := document.JSONSchema(kind)
			require.NoError(t, err)
			root := decodeSchemaData(t, data).(map[string]any)
			assert.Equal(t, "https://json-schema.org/draft/2020-12/schema", root["$schema"])
			assert.Equal(t, "urn:pb-docs:"+kind+":v1", root["$id"])
			var inspect func(any)
			inspect = func(value any) {
				switch node := value.(type) {
				case map[string]any:
					for key, child := range node {
						if key == "$ref" {
							assert.True(t, strings.HasPrefix(child.(string), "#/"), "reference must be self-contained: %v", child)
						}
						inspect(child)
					}
				case []any:
					for _, child := range node {
						inspect(child)
					}
				}
			}
			inspect(root)
		})
	}
}

func TestSchemaAccessOwnsReturnedData(t *testing.T) {
	assert.Equal(t, []string{"manifest", "page", "project"}, document.SchemaNames())
	names := document.SchemaNames()
	names[0] = "changed"
	assert.Equal(t, []string{"manifest", "page", "project"}, document.SchemaNames())
	for _, kind := range document.SchemaNames() {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			first, err := document.JSONSchema(kind)
			require.NoError(t, err)
			original := bytes.Clone(first)
			first[0] = '!'
			second, err := document.JSONSchema(kind)
			require.NoError(t, err)
			assert.Equal(t, original, second)
		})
	}
	for _, name := range []string{"", "Page", "page-v2", "../page", "schema/page-v1.json"} {
		data, err := document.JSONSchema(name)
		assert.Error(t, err)
		assert.Nil(t, data)
	}
}

func TestManifestSchemaStructure(t *testing.T) {
	schema := compileDocumentationSchema(t, "manifest")
	fixture := []byte(`{"$schema":"schema/manifest-v1.json","version":1,"title":"Docs","description":"Author guide","pages":[{"id":"guide","title":"Guide","source":"guide.md","path":"guide.html"},{"title":"RPC","source":"service.proto","path":"reference/rpc.html","kind":"protobuf"},{"title":"HTTP","source":"api.yaml","path":"reference/http.html","kind":"openapi"}],"assets":["logo.png","api.yaml"]}`)
	require.NoError(t, schema.Validate(decodeSchemaData(t, fixture)))
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown version", func(value map[string]any) { value["version"] = 2 }},
		{"version type", func(value map[string]any) { value["version"] = "1" }},
		{"missing title", func(value map[string]any) { delete(value, "title") }},
		{"unknown field", func(value map[string]any) { value["future"] = true }},
		{"null pages", func(value map[string]any) { value["pages"] = nil }},
		{"empty pages", func(value map[string]any) { value["pages"] = []any{} }},
		{"null assets", func(value map[string]any) { value["assets"] = nil }},
		{"missing page source", func(value map[string]any) { delete(value["pages"].([]any)[0].(map[string]any), "source") }},
		{"unknown page kind", func(value map[string]any) { value["pages"].([]any)[0].(map[string]any)["kind"] = "html" }},
		{"unknown page field", func(value map[string]any) { value["pages"].([]any)[0].(map[string]any)["url"] = "x" }},
		{"invalid explicit ID", func(value map[string]any) { value["pages"].([]any)[0].(map[string]any)["id"] = "Guide/Unsafe" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := decodeSchemaData(t, fixture).(map[string]any)
			test.mutate(value)
			assert.Error(t, schema.Validate(value))
		})
	}
	// JSON Schema counts Unicode characters. Compiler UTF-8 byte budgets are
	// deliberately separate, as are path cleanliness and page ownership.
	value := decodeSchemaData(t, fixture).(map[string]any)
	value["title"] = strings.Repeat("é", 256)
	value["pages"].([]any)[0].(map[string]any)["path"] = "../duplicate.html"
	require.NoError(t, schema.Validate(value))
}

func TestProjectAndPageSchemaStructure(t *testing.T) {
	page := schemaPage()
	for _, test := range []struct {
		kind  string
		model any
	}{
		{"project", schemaProject(page)},
		{"page", page},
	} {
		t.Run(test.kind, func(t *testing.T) {
			schema := compileDocumentationSchema(t, test.kind)
			require.NoError(t, schema.Validate(modelSchemaData(t, test.model)))
			for _, field := range []string{"format", "version", "source", "title"} {
				t.Run("missing "+field, func(t *testing.T) {
					value := modelSchemaData(t, test.model).(map[string]any)
					delete(value, field)
					assert.Error(t, schema.Validate(value))
				})
			}
			for _, mutation := range []map[string]any{{"format": "pb-docs/future"}, {"version": 2}, {"future": true}, {"source": map[string]any{"file": "x", "line": 0}}, {"source": map[string]any{"file": "x", "offset": 4}}} {
				value := modelSchemaData(t, test.model).(map[string]any)
				for key, replacement := range mutation {
					value[key] = replacement
				}
				assert.Error(t, schema.Validate(value))
			}
			value := modelSchemaData(t, test.model).(map[string]any)
			if test.kind == "project" {
				value["pages"] = nil
			} else {
				value["blocks"] = nil
			}
			assert.Error(t, schema.Validate(value), "required collections are arrays, not null")
		})
	}
	projectSchema := compileDocumentationSchema(t, "project")
	value := modelSchemaData(t, schemaProject(page)).(map[string]any)
	value["root"] = "./"
	assert.Error(t, projectSchema.Validate(value))
	value = modelSchemaData(t, schemaProject(page)).(map[string]any)
	delete(value["pages"].([]any)[0].(map[string]any), "data")
	assert.Error(t, projectSchema.Validate(value))
}

func TestPageSchemaRecursiveContentAndKinds(t *testing.T) {
	page := schemaPage()
	location := page.Source
	inlines := []document.Inline{{Kind: document.InlineLink, Destination: "guide.html#topic", Reference: &document.Reference{Page: page.ID, Anchor: "topic"}, Children: []document.Inline{{Kind: document.InlineStrong, Children: []document.Inline{{Kind: document.InlineText, Text: "Read the guide"}}}}}}
	paragraph := document.Block{Kind: document.BlockParagraph, Source: location, Inlines: inlines}
	item := document.Block{Kind: document.BlockListItem, Source: location, Children: []document.Block{paragraph}}
	list := document.Block{Kind: document.BlockList, Source: location, Ordered: true, Start: 2, Children: []document.Block{item}}
	page.Blocks = []document.Block{{Kind: document.BlockQuote, Source: location, Children: []document.Block{list}}}
	for _, kind := range []document.BlockKind{document.BlockParagraph, document.BlockHeading, document.BlockCode, document.BlockQuote, document.BlockList, document.BlockListItem, document.BlockTable, document.BlockTableRow, document.BlockTableHeader, document.BlockTableCell, document.BlockDefinitionList, document.BlockDefinitionTerm, document.BlockDefinition, document.BlockRule, document.BlockAnchor, document.BlockHTML} {
		page.Blocks = append(page.Blocks, document.Block{Kind: kind, Source: location})
	}
	checked := false
	for _, kind := range []document.InlineKind{document.InlineText, document.InlineCode, document.InlineEmphasis, document.InlineStrong, document.InlineStrike, document.InlineLink, document.InlineImage, document.InlineSoftBreak, document.InlineHardBreak, document.InlineCheckbox, document.InlineHTML, document.InlineAnchor} {
		page.Blocks[1].Inlines = append(page.Blocks[1].Inlines, document.Inline{Kind: kind, Checked: &checked})
	}
	schema := compileDocumentationSchema(t, "page")
	require.NoError(t, schema.Validate(modelSchemaData(t, page)))
	value := modelSchemaData(t, page).(map[string]any)
	block := value["blocks"].([]any)[0].(map[string]any)
	for range 3 {
		block = block["children"].([]any)[0].(map[string]any)
	}
	inline := block["inlines"].([]any)[0].(map[string]any)
	inline["reference"].(map[string]any)["anchor"] = 42
	assert.Error(t, schema.Validate(value))
	inline["reference"].(map[string]any)["anchor"] = "topic"
	require.NoError(t, schema.Validate(value))
	inline["children"].([]any)[0].(map[string]any)["kind"] = "futureInline"
	assert.Error(t, schema.Validate(value))
	inline["children"].([]any)[0].(map[string]any)["kind"] = "strong"
	require.NoError(t, schema.Validate(value))
	block["kind"] = "futureBlock"
	assert.Error(t, schema.Validate(value))
	block["kind"] = "paragraph"
	delete(block, "source")
	assert.Error(t, schema.Validate(value))
}

func TestPageSchemaValidatesRealImportedAPIs(t *testing.T) {
	schema := compileDocumentationSchema(t, "page")
	for _, name := range []string{"modern.yaml", "catalog.yaml", "legacy.json", "catalog.proto"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "testdata", "api", name))
			require.NoError(t, err)
			var api *document.API
			page := schemaPage()
			if strings.HasSuffix(name, ".proto") {
				page.Kind = "protobuf"
				api, err = importer.Protobuf(context.Background(), name, data)
			} else {
				page.Kind = "openapi"
				api, err = importer.OpenAPI(context.Background(), name, data, func(name string) ([]byte, error) {
					return os.ReadFile(filepath.Join("..", "testdata", "api", filepath.FromSlash(name)))
				})
			}
			require.NoError(t, err)
			require.NotEmpty(t, api.Operations)
			require.NotEmpty(t, api.Schemas)
			page.API = api
			page.Source = api.Source
			require.NoError(t, schema.Validate(modelSchemaData(t, page)))
		})
	}
}

const precisionOpenAPI = `{"openapi":"3.1.2","info":{"title":"Exact numbers","version":"1"},"paths":{"/value":{"get":{"responses":{"200":{"description":"Exact integer","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Exact"},"example":18446744073709551615}}}}}}},"components":{"schemas":{"Exact":{"type":"integer","minimum":9007199254740993,"default":18446744073709551615},"Any":true}}}`

func TestPageSchemaPreservesExactRawJSONAndOpenContainers(t *testing.T) {
	api, err := importer.OpenAPI(context.Background(), "exact.json", []byte(precisionOpenAPI), nil)
	require.NoError(t, err)
	page := schemaPage()
	page.Kind, page.API = "openapi", api
	schema := compileDocumentationSchema(t, "page")
	value := modelSchemaData(t, page).(map[string]any)
	require.NoError(t, schema.Validate(value))
	apiValue := value["api"].(map[string]any)
	operation := apiValue["operations"].([]any)[0].(map[string]any)
	media := operation["responses"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, json.Number("18446744073709551615"), media["example"])
	assert.Equal(t, "18446744073709551615", media["exampleJSON"])
	foundExact := false
	for _, rawSchema := range apiValue["schemas"].([]any) {
		node := rawSchema.(map[string]any)
		if node["name"] == "Exact" {
			foundExact = true
			definition := node["definition"].(map[string]any)
			assert.Equal(t, json.Number("9007199254740993"), definition["minimum"])
			assert.Equal(t, json.Number("18446744073709551615"), definition["default"])
			assert.Equal(t, "18446744073709551615", node["defaultJSON"])
		}
	}
	require.True(t, foundExact, "the real imported schema must carry its exact constraints")
	// Protocol extensions and instance $refs remain data; schema validation does
	// not dereference them or claim to validate their protocol-specific meaning.
	operation["raw"] = map[string]any{"x-extension": []any{nil, true, json.Number("123456789012345678901234567890")}, "$ref": "https://example.invalid/unfetched.json"}
	require.NoError(t, schema.Validate(value))
	operation["futureTypedField"] = true
	assert.Error(t, schema.Validate(value))
	delete(operation, "futureTypedField")
	operation["security"] = []any{map[string]any{"oauth": nil}}
	assert.Error(t, schema.Validate(value), "security scopes must be arrays")
	operation["security"] = []any{}
	operation["servers"] = nil
	assert.Error(t, schema.Validate(value), "effective servers must be arrays")
}

func TestSchemaPropertiesCoverExportedModelFields(t *testing.T) {
	pageBytes, err := document.JSONSchema("page")
	require.NoError(t, err)
	pageSchema := decodeSchemaData(t, pageBytes).(map[string]any)
	defs := pageSchema["$defs"].(map[string]any)
	models := map[string]any{
		"location": document.Location{}, "reference": document.Reference{}, "block": document.Block{}, "inline": document.Inline{},
		"api": document.API{}, "apiDocument": document.APIDocument{}, "tag": document.Tag{}, "server": document.Server{},
		"serverVariable": document.ServerVariable{}, "securityScheme": document.SecurityScheme{}, "operation": document.Operation{},
		"parameter": document.Parameter{}, "requestBody": document.RequestBody{}, "response": document.Response{}, "mediaType": document.MediaType{},
		"example": document.Example{}, "apiSchema": document.Schema{}, "schemaReference": document.SchemaReference{},
	}
	for name, model := range models {
		t.Run(name, func(t *testing.T) {
			properties := defs[name].(map[string]any)["properties"].(map[string]any)
			typeOf := reflect.TypeOf(model)
			for index := range typeOf.NumField() {
				field := typeOf.Field(index)
				key := strings.Split(field.Tag.Get("json"), ",")[0]
				assert.Contains(t, properties, key, "exported JSON field %s.%s is missing from the schema", typeOf.Name(), field.Name)
			}
		})
	}
}

func TestSchemasValidateActualCompiledArtifacts(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	require.NoError(t, os.Mkdir(input, 0o700))
	manifest := `{"version":1,"title":"Real project","pages":[{"id":"guide","title":"Guide","source":"guide.md","path":"index.html"},{"id":"http","title":"HTTP API","source":"exact.json","path":"api/http.html","kind":"openapi"},{"id":"rpc","title":"RPC API","source":"service.proto","path":"api/rpc.html","kind":"protobuf"}]}`
	files := map[string]string{
		"docs.json":     manifest,
		"guide.md":      "# Guide\n\nRead **nested [HTTP](exact.json)** or [RPC](service.proto).\n\n- A list\n  - Nested item\n\n| A | B |\n| :- | -: |\n| 1 | 2 |\n",
		"exact.json":    precisionOpenAPI,
		"service.proto": "syntax = \"proto3\"; package example; message Node { Node next = 1; } service Catalog { rpc Watch(stream Node) returns(stream Node); }",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(input, name), []byte(content), 0o600))
	}
	output := filepath.Join(root, "output")
	err := site.Build(filepath.Join(input, "docs.json"), output, site.Options{ImportPage: func(ctx context.Context, kind, name string, source []byte, load func(string) ([]byte, error)) (site.ImportResult, error) {
		var api *document.API
		var err error
		if kind == "protobuf" {
			api, err = importer.Protobuf(ctx, name, source)
		} else {
			api, err = importer.OpenAPI(ctx, name, source, importer.Load(load))
		}
		return site.ImportResult{API: api}, err
	}})
	require.NoError(t, err)
	manifestSchema := compileDocumentationSchema(t, "manifest")
	require.NoError(t, manifestSchema.Validate(decodeSchemaData(t, []byte(manifest))))
	projectData, err := os.ReadFile(filepath.Join(output, "_docs", "project.json"))
	require.NoError(t, err)
	projectSchema := compileDocumentationSchema(t, "project")
	require.NoError(t, projectSchema.Validate(decodeSchemaData(t, projectData)))
	var project document.Project
	require.NoError(t, json.Unmarshal(projectData, &project))
	require.Len(t, project.Pages, 3)
	pageSchema := compileDocumentationSchema(t, "page")
	for _, page := range project.Pages {
		data, err := os.ReadFile(filepath.Join(output, "_docs", filepath.FromSlash(page.Data)))
		require.NoError(t, err)
		require.NoError(t, pageSchema.Validate(decodeSchemaData(t, data)), "compiled page %s", page.ID)
	}
}
