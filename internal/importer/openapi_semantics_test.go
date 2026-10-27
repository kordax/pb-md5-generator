package importer

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

func TestOpenAPITraversesContractPositionsOnly(t *testing.T) {
	source := contract("3.2.1", `{"securitySchemes":{"oauth":{"type":"oauth2","flows":{"implicit":{"authorizationUrl":"https://auth.example.test","scopes":{"schema":"Scope named schema","$ref":"Scope named ref"}}}}},"examples":{"schema":{"value":{"$ref":"https://example.test/payload","schema":false}}},"schemas":{"Payload":{"type":"object","default":{"$ref":"https://example.test/default"},"examples":[{"$ref":"https://example.test/example"}],"x-document":{"$ref":"https://example.test/extension"}}},"headers":{"Trace":{"schema":{"type":"string"}}},"links":{"Next":{"operationId":"read","parameters":{"schema":"$response.body#/schema"}}},"responses":{"Result":{"description":"ok","headers":{"Trace":{"$ref":"#/components/headers/Trace"}},"links":{"next":{"$ref":"#/components/links/Next"}},"content":{"multipart/form-data":{"schema":{"type":"array"},"prefixEncoding":[{"headers":{"Trace":{"$ref":"#/components/headers/Trace"}}}],"itemEncoding":{"headers":{"Trace":{"$ref":"#/components/headers/Trace"}}}}}}}}`, `{"x-metadata":{"$ref":"https://example.test/extension"},"/read":{"get":{"operationId":"read","responses":{"200":{"$ref":"#/components/responses/Result"},"x-metadata":{"$ref":"https://example.test/extension"}}}}}`)
	loads := 0
	api, err := OpenAPI(context.Background(), "api.json", []byte(source), func(string) ([]byte, error) { loads++; return nil, fmt.Errorf("must not load data") })
	require.NoError(t, err)
	assert.Zero(t, loads)
	assert.Contains(t, string(findSchema(t, api, "api.json#/components/schemas/Payload").Definition), `https://example.test/default`)
	assert.Len(t, api.Operations, 1)
}

func TestReferencedContractsKeepDeclaredFeatureSet(t *testing.T) {
	root := contract("3.1.2", `{"schemas":{"External":{"$ref":"common.json#/components/schemas/External"}}}`, "")
	for _, test := range []struct {
		version string
		valid   bool
	}{{"3.1.0", true}, {"3.0.4", false}, {"3.2.1", false}, {"3.1.x", false}} {
		t.Run(test.version, func(t *testing.T) {
			api, err := OpenAPI(context.Background(), "api.json", []byte(root), func(name string) ([]byte, error) {
				assert.Equal(t, "common.json", name)
				return []byte(contract(test.version, `{"schemas":{"External":{"type":"string"}}}`, "")), nil
			})
			if test.valid {
				require.NoError(t, err)
				assert.Len(t, api.Documents, 2)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestReferenceURIConfinementAndPointerRules(t *testing.T) {
	for _, reference := range []string{"%2e%2e/escape.json", "%252e%252e/escape.json", "file:///etc/passwd", "//example.test/schema", "schema.json?query", "schema.json%5c..%5cescape", "#/components/schemas/Missing", "#/components/schemas/A~2B"} {
		t.Run(reference, func(t *testing.T) {
			ref, err := json.Marshal(reference)
			require.NoError(t, err)
			loads := 0
			source := contract("3.1.2", `{"schemas":{"Use":{"$ref":`+string(ref)+`}}}`, "")
			_, err = OpenAPI(context.Background(), "api.json", []byte(source), func(string) ([]byte, error) { loads++; return nil, fmt.Errorf("unexpected load") })
			require.Error(t, err)
			assert.Zero(t, loads)
		})
	}
	for _, source := range []string{
		contract("3.1.2", `{"schemas":{"A":{"$id":"same"},"B":{"$id":"same"}}}`, ""),
		contract("3.1.2", `{"schemas":{"A":{"$id":"bad#fragment"}}}`, ""),
		contract("3.1.2", `{"schemas":{"A":{"properties":{"child":{"$schema":"https://example.test/unknown"}}}}}`, ""),
		contract("3.1.2", `{"schemas":{"A":{"$vocabulary":{"https://example.test/required":true}}}}`, ""),
	} {
		_, err := OpenAPI(context.Background(), "api.json", []byte(source), nil)
		require.Error(t, err)
	}
	root := contract("3.1.2", `{"schemas":{"Use":{"$ref":"schemas/root.json#node"}}}`, "")
	api, err := OpenAPI(context.Background(), "api.json", []byte(root), func(string) ([]byte, error) {
		return []byte(`{"$id":"https://schemas.example.test/tree","$anchor":"node","$defs":{"Child":{"$id":"child","type":"string"}},"properties":{"self":{"$ref":"#node"},"child":{"$ref":"child"}}}`), nil
	})
	require.NoError(t, err)
	assert.Len(t, findSchema(t, api, "schemas/root.json#").References, 2)
}

func TestEffectiveParameterAndSecurityOverrides(t *testing.T) {
	source := contract("3.1.2", `{"parameters":{"Query":{"name":"q","in":"query","style":"form","explode":false,"allowReserved":true,"schema":{"type":"array","items":{"type":"string"}},"examples":{"local":{"$ref":"#/components/examples/Local"}}}},"examples":{"Local":{"value":["a","b"]}},"securitySchemes":{"key":{"type":"http","scheme":"bearer"}}}`, `{"/items/{id}":{"servers":[{"url":"https://path.example.test"}],"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}},{"name":"X-Mode","in":"header","schema":{"type":"string"}}],"get":{"security":[{}, {"key":[]}],"parameters":[{"$ref":"#/components/parameters/Query"},{"name":"x-mode","in":"header","schema":{"type":"integer"}}],"responses":{"200":{"description":"ok"}}}}}`)
	api, err := OpenAPI(context.Background(), "api.json", []byte(source), nil)
	require.NoError(t, err)
	operation := api.Operations[0]
	assert.Equal(t, []document.Server{{URL: "https://path.example.test"}}, operation.Servers)
	require.Len(t, operation.Parameters, 3)
	assert.Equal(t, "x-mode", operation.Parameters[1].Name)
	query := operation.Parameters[2]
	assert.Equal(t, "form", query.Style)
	require.NotNil(t, query.Explode)
	assert.False(t, *query.Explode)
	assert.True(t, query.AllowReserved)
	assert.Equal(t, `["a","b"]`, query.Examples[0].ValueJSON)
	assert.Len(t, operation.Security, 2)
	conflict := contract("3.2.1", "", `{"/search":{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}}],"query":{"parameters":[{"name":"search","in":"querystring","content":{"application/x-www-form-urlencoded":{"schema":{"type":"object"}}}}],"responses":{"200":{"description":"ok"}}}}}`)
	_, err = OpenAPI(context.Background(), "api.json", []byte(conflict), nil)
	require.ErrorContains(t, err, "query and querystring")
}

func TestOpenAPIExplicitModelBudgets(t *testing.T) {
	makeNormalizer := func() *openAPI {
		sources := newSources(context.Background(), nil)
		root, err := sources.read("api.json", []byte(contract("3.1.2", "", "")))
		require.NoError(t, err)
		validator, err := newContractValidator("3.1.2")
		require.NoError(t, err)
		return &openAPI{sources: sources, root: root, version: "3.1.2", api: &document.API{Servers: []document.Server{}, Security: []document.SecurityRequirement{}}, validator: validator, schemas: map[string]document.Schema{}, visited: map[string]bool{}, resources: map[string]referenceTarget{}, bases: map[string]string{}, indexed: map[string]bool{}, edges: map[string][]document.SchemaReference{}}
	}
	n := makeNormalizer()
	n.sources.refs = MaxReferences
	_, _, err := n.resolve("api.json", "", "#/info", "api.json", true)
	require.ErrorContains(t, err, "budget")
	n = makeNormalizer()
	for index := range MaxSchemas {
		n.schemas[fmt.Sprint(index)] = document.Schema{}
	}
	_, err = n.addSchema("api.json", "/new", map[string]any{}, "")
	require.ErrorContains(t, err, "count")
	n = makeNormalizer()
	n.api.Operations = make([]document.Operation, MaxOperations)
	err = n.pathItem("api.json", "/paths/~1new", "/new", map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "ok"}}}}, "request", map[string]bool{}, map[string]bool{}, 0)
	require.ErrorContains(t, err, "count")
	n = makeNormalizer()
	_, err = n.addSchema("api.json", "/scalar", "bad", "")
	require.Error(t, err)
	err = n.indexSchema("api.json", "", map[string]any{}, "api.json", MaxDepth+1)
	require.ErrorContains(t, err, "depth")
	err = n.schemaReferences("api.json", "", map[string]any{}, MaxDepth+1, "api.json#")
	require.ErrorContains(t, err, "nesting")
	err = n.scanReferences("api.json", "", map[string]any{}, map[referenceTarget]bool{}, MaxDepth+1)
	require.ErrorContains(t, err, "depth")
	err = n.pathItem("api.json", "", "", nil, "callback", map[string]bool{}, map[string]bool{}, MaxDepth+1)
	require.ErrorContains(t, err, "depth")
	n.rawBytes = MaxModelBytes
	_, err = n.addSchema("api.json", "/oversize", map[string]any{}, "")
	require.ErrorContains(t, err, "budget")
	assert.True(t, knownVocabulary("https://json-schema.org/draft/2020-12/vocab/validation"))
	assert.True(t, knownVocabulary("https://spec.openapis.org/oas/3.1/vocab/base"))
	assert.False(t, knownVocabulary("https://example.test/custom"))
	_, err = n.at(referenceTarget{file: "api.json", pointer: "/info/title/invalid"})
	require.Error(t, err)
	_, err = n.at(referenceTarget{file: "api.json", pointer: "/info/~2"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "Test"))
}
