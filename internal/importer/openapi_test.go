package importer

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS("../../testdata/api"), name)
	require.NoError(t, err)
	return data
}

func fixtureLoad(name string) ([]byte, error) {
	return fs.ReadFile(os.DirFS("../../testdata/api"), name)
}

func findOperation(t *testing.T, api *document.API, id string) *document.Operation {
	t.Helper()
	for index := range api.Operations {
		if api.Operations[index].ID == id {
			return &api.Operations[index]
		}
	}
	t.Fatalf("operation %q missing", id)
	return nil
}

func findSchema(t *testing.T, api *document.API, id string) *document.Schema {
	t.Helper()
	for index := range api.Schemas {
		if api.Schemas[index].ID == id {
			return &api.Schemas[index]
		}
	}
	t.Fatalf("schema %q missing", id)
	return nil
}

func TestOpenAPIRealLocalContract(t *testing.T) {
	api, err := OpenAPI(context.Background(), "catalog.yaml", fixture(t, "catalog.yaml"), fixtureLoad)
	require.NoError(t, err)
	assert.Equal(t, "http", api.Protocol)
	assert.Equal(t, "3.1.2", api.Version)
	assert.Equal(t, "2026-10", api.APIVersion)
	assert.Equal(t, "Local catalog", api.Title)
	require.Len(t, api.Documents, 3)
	require.Len(t, api.Operations, 4)
	get := findOperation(t, api, "getItem")
	assert.Equal(t, "GET", get.Method)
	assert.Equal(t, "request", get.Interaction)
	assert.Empty(t, get.Servers)
	assert.NotNil(t, get.Servers)
	assert.Empty(t, get.Security)
	assert.NotNil(t, get.Security)
	assert.Equal(t, "catalog.yaml", get.Source.File)
	assert.Positive(t, get.Source.Line)
	require.Len(t, get.Parameters, 2)
	assert.Equal(t, "id", get.Parameters[0].Name)
	assert.True(t, get.Parameters[0].Required)
	assert.Equal(t, "q", get.Parameters[1].Name)
	assert.Equal(t, "9007199254740993", get.Parameters[1].ExampleJSON)
	require.Len(t, get.Responses, 1)
	assert.Equal(t, "A catalog item", get.Responses[0].Description)
	assert.Equal(t, "X-Request-ID", get.Responses[0].Headers[0].Name)
	assert.Equal(t, "{\"id\":9007199254740993}", get.Responses[0].Content[0].Examples[0].ValueJSON)
	post := findOperation(t, api, "updateItem")
	require.Len(t, post.Servers, 1)
	assert.Equal(t, "https://{region}.example.test/v1", post.Servers[0].URL)
	require.Len(t, post.Security, 2)
	assert.Equal(t, []string{"catalog:read"}, post.Security[0]["oauth"])
	assert.Contains(t, post.Security[0], "token")
	assert.Empty(t, post.Security[1])
	require.NotNil(t, post.RequestBody)
	assert.True(t, post.RequestBody.Required)
	assert.Equal(t, "{\"id\":9007199254740993}", post.RequestBody.Content[0].ExampleJSON)
	assert.Equal(t, "{\"id\":9007199254740993}", post.RequestBody.Content[0].Examples[0].ValueJSON)
	node := findSchema(t, api, "schemas/node.yaml#")
	assert.Contains(t, string(node.Definition), "unevaluatedProperties")
	require.Len(t, node.References, 1)
	assert.Equal(t, "schemas/node.yaml#", node.References[0].Target)
	assert.Equal(t, "/properties/children/items/$ref", node.References[0].Pointer)
	assert.Equal(t, "#node", node.References[0].Reference)
	assert.Equal(t, "schemas/node.yaml", node.Source.File)
	assert.Positive(t, node.Source.Line)
	interactions := map[string]int{}
	for _, operation := range api.Operations {
		interactions[operation.Interaction]++
	}
	assert.Equal(t, map[string]int{"request": 2, "callback": 1, "webhook": 1}, interactions)
	encoded, err := json.Marshal(api)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"servers":[]`)
	assert.Contains(t, string(encoded), `9007199254740993`)
	again, err := OpenAPI(context.Background(), "catalog.yaml", fixture(t, "catalog.yaml"), fixtureLoad)
	require.NoError(t, err)
	againEncoded, err := json.Marshal(again)
	require.NoError(t, err)
	assert.Equal(t, string(encoded), string(againEncoded))
}

func TestOpenAPIFeatureVersions(t *testing.T) {
	legacy, err := OpenAPI(context.Background(), "legacy.json", fixture(t, "legacy.json"), nil)
	require.NoError(t, err)
	assert.Equal(t, "3.0.4", legacy.Version)
	require.Len(t, legacy.Operations, 1)
	assert.Equal(t, []document.Server{{URL: "/"}}, legacy.Operations[0].Servers)
	assert.Contains(t, string(legacy.Schemas[0].Definition), `"nullable":true`)
	modern, err := OpenAPI(context.Background(), "modern.yaml", fixture(t, "modern.yaml"), nil)
	require.NoError(t, err)
	assert.Equal(t, "primary", modern.Servers[0].Name)
	assert.Equal(t, "Parent", modern.Tags[1].Parent)
	query := findOperation(t, modern, "search")
	assert.Equal(t, "QUERY", query.Method)
	assert.Equal(t, "querystring", query.Parameters[0].In)
	require.Len(t, query.Responses[0].Content, 1)
	media := query.Responses[0].Content[0]
	assert.NotEmpty(t, media.ItemSchema)
	assert.Equal(t, "{\"id\":9007199254740993}", media.Examples[0].ValueJSON)
	require.NotNil(t, media.Examples[0].SerializedValue)
	assert.Equal(t, "{\"id\":9007199254740993}", *media.Examples[0].SerializedValue)
	assert.Equal(t, "REPORT", findOperation(t, modern, "report").Method)
	assert.Equal(t, "https://auth.example.test/.well-known/openid-configuration", modern.SecuritySchemes[0].OpenIDConnectURL)
}

func contract(version, components, paths string) string {
	if components == "" {
		components = `{}`
	}
	if paths == "" {
		paths = `{}`
	}
	return `{"openapi":"` + version + `","info":{"title":"Test","version":"1"},"paths":` + paths + `,"components":` + components + `}`
}

func TestOpenAPIRejectsInvalidContracts(t *testing.T) {
	cases := []struct{ name, source string }{
		{"version missing", `{}`},
		{"swagger", `{"swagger":"2.0"}`},
		{"future version", contract("3.3.0", "", "")},
		{"bad patch", contract("3.1.x", "", "")},
		{"duplicate keys", "openapi: 3.1.0\nopenapi: 3.1.1\n"},
		{"multiple documents", "openapi: 3.1.0\n---\nopenapi: 3.1.0\n"},
		{"invalid info", `{"openapi":"3.1.2","info":{},"paths":{}}`},
		{"legacy boolean schema", contract("3.0.4", `{"schemas":{"No":false}}`, "")},
		{"legacy union type", contract("3.0.4", `{"schemas":{"No":{"type":["string","null"]}}}`, "")},
		{"invalid schema keyword", contract("3.1.2", `{"schemas":{"No":{"type":"object","required":"id"}}}`, "")},
		{"unsupported dialect", contract("3.1.2", `{"schemas":{"No":{"$schema":"https://example.test/dialect","type":"object"}}}`, "")},
		{"remote ref", contract("3.1.2", `{"schemas":{"No":{"$ref":"https://example.test/schema.json"}}}`, "")},
		{"escaping ref", contract("3.1.2", `{"schemas":{"No":{"$ref":"../outside.json"}}}`, "")},
		{"absolute file ref", contract("3.1.2", `{"schemas":{"No":{"$ref":"/tmp/schema.json"}}}`, "")},
		{"bad pointer", contract("3.1.2", `{"schemas":{"No":{"$ref":"#/components/~2"}}}`, "")},
		{"missing target", contract("3.1.2", `{"schemas":{"No":{"$ref":"#/missing"}}}`, "")},
		{"duplicate operation ID", contract("3.1.2", "", `{"/a":{"get":{"operationId":"same","responses":{"200":{"description":"ok"}}}},"/b":{"get":{"operationId":"same","responses":{"200":{"description":"ok"}}}}}`)},
		{"missing path parameter", contract("3.1.2", "", `{"/a/{id}":{"get":{"responses":{"200":{"description":"ok"}}}}}`)},
		{"unbound path parameter", contract("3.1.2", "", `{"/a":{"get":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}}}`)},
		{"unresolved security", strings.Replace(contract("3.1.2", "", ""), `"paths":{}`, `"paths":{},"security":[{"missing":[]}]`, 1)},
		{"non-schema reference cycle", contract("3.1.2", `{"parameters":{"A":{"$ref":"#/components/parameters/B"},"B":{"$ref":"#/components/parameters/A"}}}`, "")},
		{"ambiguous path item ref siblings", contract("3.1.2", `{"pathItems":{"A":{"get":{"responses":{"200":{"description":"ok"}}}}}}`, `{"/a":{"$ref":"#/components/pathItems/A","post":{"responses":{"200":{"description":"ok"}}}}}`)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			loads := 0
			api, err := OpenAPI(context.Background(), "api.json", []byte(test.source), func(string) ([]byte, error) { loads++; return nil, errors.New("unexpected load") })
			require.Error(t, err)
			assert.Nil(t, api)
			assert.Zero(t, loads)
			var diagnostic *Error
			assert.ErrorAs(t, err, &diagnostic)
		})
	}
}

func TestOpenAPILocalIDsAnchorsAndDynamicReferences(t *testing.T) {
	source := contract("3.1.2", `{"schemas":{"Tree":{"$id":"https://schemas.example.test/tree","$dynamicAnchor":"node","type":"object","properties":{"children":{"type":"array","items":{"$dynamicRef":"#node"}}}},"Use":{"$ref":"https://schemas.example.test/tree#node"},"Any":true,"Exact":{"type":"integer","example":9007199254740993,"default":18446744073709551615}}}`, "")
	api, err := OpenAPI(context.Background(), "api.json", []byte(source), nil)
	require.NoError(t, err)
	tree := findSchema(t, api, "api.json#/components/schemas/Tree")
	require.Len(t, tree.References, 1)
	assert.True(t, tree.References[0].Dynamic)
	assert.Equal(t, tree.ID, tree.References[0].Target)
	assert.Equal(t, tree.ID, findSchema(t, api, "api.json#/components/schemas/Use").References[0].Target)
	exact := findSchema(t, api, "api.json#/components/schemas/Exact")
	assert.Equal(t, "9007199254740993", exact.ExampleJSON)
	assert.Equal(t, "18446744073709551615", exact.DefaultJSON)
	assert.Equal(t, "true", string(findSchema(t, api, "api.json#/components/schemas/Any").Definition))
}

func TestOpenAPILoaderBoundsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OpenAPI(ctx, "api.json", []byte(contract("3.1.2", "", "")), nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = OpenAPI(context.Background(), "../api.json", []byte(contract("3.1.2", "", "")), nil)
	require.Error(t, err)
	_, err = OpenAPI(context.Background(), "api.json", make([]byte, MaxDocumentBytes+1), nil)
	require.Error(t, err)
	calls := []string{}
	source := contract("3.1.2", `{"schemas":{"External":{"$ref":"schemas/external.json"}}}`, "")
	_, err = OpenAPI(context.Background(), "api.json", []byte(source), func(name string) ([]byte, error) {
		calls = append(calls, name)
		return make([]byte, MaxDocumentBytes+1), nil
	})
	require.Error(t, err)
	assert.Equal(t, []string{"schemas/external.json"}, calls)
	_, err = OpenAPI(context.Background(), "api.json", []byte(source), func(string) ([]byte, error) { return nil, fs.ErrNotExist })
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}
