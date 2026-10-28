package site

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func apiViewFixture() (*document.API, Manifest) {
	location := document.Location{File: "contracts/api.yaml"}
	schemaID := "contracts/api.yaml#/components/schemas/Pet"
	schema := document.Schema{ID: schemaID, Anchor: document.APIAnchor("schema", schemaID), Name: "Pet", Source: location, Definition: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"next":{"$ref":"#/components/schemas/Pet"}},"required":["id"]}`)}
	operation := document.Operation{ID: "findPet", Anchor: document.APIAnchor("operation", "findPet"), Method: "GET", Path: "/pets/{id}", Interaction: "request", Tags: []string{"Pets"}, Summary: "Find a pet", Source: location, Servers: []document.Server{{URL: "https://api.example.test/v1"}}, Security: []document.SecurityRequirement{}, Parameters: []document.Parameter{{Name: "id", In: "path", Required: true, Schema: schemaID, ExampleJSON: "18446744073709551615"}}, Responses: []document.Response{{Status: "200", Description: "A pet", Headers: []document.Parameter{{Name: "X-Trace", In: "header", Schema: schemaID}}, Content: []document.MediaType{{Name: "application/json", Schema: schemaID, ExampleJSON: `{"id":18446744073709551615}`}}}}}
	api := &document.API{ID: "fixture-api", Protocol: "http", Format: "openapi", Version: "3.2.0", APIVersion: "v1", Title: "Pet reference", Source: location, Servers: operation.Servers, Operations: []document.Operation{operation}, Schemas: []document.Schema{schema}}
	manifest := Manifest{Version: 1, Title: "Fixture", Pages: []Page{{ID: "api", Title: "API", Source: "contracts/api.yaml", Path: "reference/api.html", Kind: "openapi"}}}
	return api, manifest
}

func renderAPIViewFixture(t *testing.T, api *document.API, manifest Manifest) pageContent {
	t.Helper()
	content, err := renderAPI(context.Background(), api, 0, manifest, "_docs/pages/fixture.json")
	require.NoError(t, err)
	return content
}

func TestAPIViewHTTPReference(t *testing.T) {
	api, manifest := apiViewFixture()
	api.Operations[0].Deprecated = true
	api.Operations[0].Parameters[0].Style = "matrix"
	api.Operations[0].Parameters[0].Explode = new(true)
	api.Operations[0].Parameters[0].AllowReserved = true
	api.Operations[0].RequestBody = &document.RequestBody{Required: true, Description: "Optional operation metadata can still describe a body.", Content: []document.MediaType{{Name: "application/json", Schema: api.Schemas[0].ID, ItemSchema: api.Schemas[0].ID, Encoding: json.RawMessage(`{"payload":{"contentType":"application/json"}}`), Examples: []document.Example{{Name: "pet", Summary: "Large ID", ValueJSON: `{"id":18446744073709551615}`, ExternalValue: "https://examples.example.test/pet.json"}}}}}
	content := renderAPIViewFixture(t, api, manifest)
	assert.True(t, content.Interactive)
	assert.Equal(t, "../_docs/pages/fixture.json", content.APIData)
	for _, value := range []string{"<h1", "Pets", "Find a pet", "api-method", "api-path", "Deprecated", "Style: matrix", "explode: true", "allowReserved: true", "X-Trace", "Request body", "Responses", "application/json", "Media encoding", "Stream item schema", "18446744073709551615", "External example (not fetched)", "contracts/api.yaml"} {
		assert.Contains(t, content.HTML, value)
	}
	visibleText := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(content.HTML, ""))
	assert.Contains(t, visibleText, "GET /pets/{id}")
	assert.Contains(t, content.HTML, `data-api-operation="findPet" data-api-url="../_docs/pages/fixture.json"`)
	assert.Contains(t, content.HTML, `data-api-client hidden`)
	assert.NotContains(t, content.HTML, `href="contracts/api.yaml`)
	assert.True(t, content.Anchors[document.APIAnchor("api-body", "findPet")])
	assert.Len(t, content.Blocks, 0)
	assertAPIReferenceIntegrity(t, content)
}

func TestAPIViewOnlyOutgoingHTTPHasTry(t *testing.T) {
	api, manifest := apiViewFixture()
	for _, interaction := range []string{"webhook", "callback", "rpc"} {
		operation := api.Operations[0]
		operation.ID = interaction
		operation.Anchor = document.APIAnchor("operation", interaction)
		operation.Interaction = interaction
		api.Operations = append(api.Operations, operation)
	}
	content := renderAPIViewFixture(t, api, manifest)
	assert.Equal(t, 1, strings.Count(content.HTML, "data-api-operation="))
	assert.Contains(t, content.HTML, "Incoming interaction")
	api.Operations = api.Operations[1:]
	content = renderAPIViewFixture(t, api, manifest)
	assert.False(t, content.Interactive)
	assert.NotContains(t, content.HTML, "data-api-operation=")
}

func TestAPIViewGRPCStreamsAndOpaqueTypes(t *testing.T) {
	api, manifest := apiViewFixture()
	api.Protocol = "grpc"
	api.Format = "protobuf"
	api.Operations = []document.Operation{
		{ID: "unary", Anchor: document.APIAnchor("operation", "unary"), Service: "pets.Pets", RPCMethod: "Get", Path: "/pets.Pets/Get", InputSchema: api.Schemas[0].ID, OutputSchema: "HiddenReply", Source: api.Source},
		{ID: "watch", Anchor: document.APIAnchor("operation", "watch"), Service: "pets.Pets", RPCMethod: "Watch", Path: "/pets.Pets/Watch", InputSchema: api.Schemas[0].ID, OutputSchema: api.Schemas[0].ID, ClientStreaming: true, ServerStreaming: true, Source: api.Source},
	}
	content := renderAPIViewFixture(t, api, manifest)
	assert.False(t, content.Interactive)
	assert.NotContains(t, content.HTML, "Try request")
	assert.NotContains(t, content.HTML, "data-api-client")
	for _, value := range []string{"pets.Pets.Get", "pets.Pets.Watch", "RPC", "Input schema", "Output schema", "Client streaming", "Server streaming", ">true<", ">false<", "HiddenReply"} {
		assert.Contains(t, content.HTML, value)
	}
	assert.NotContains(t, content.HTML, `href="#HiddenReply`)
	assertAPIReferenceIntegrity(t, content)
}

func TestAPIViewSecurityAlternativesPreserveANDAndAnonymous(t *testing.T) {
	api, manifest := apiViewFixture()
	api.SecuritySchemes = []document.SecurityScheme{
		{ID: "key", Type: "apiKey", Name: "X-API-Key", In: "header", Description: "API key"},
		{ID: "oauth", Type: "oauth2", Flows: json.RawMessage(`{"authorizationCode":{"authorizationUrl":"https://auth.example.test/authorize","tokenUrl":"https://auth.example.test/token","scopes":{"read:pets":"Read pets"}}}`)},
		{ID: "oidc", Type: "openIdConnect", OpenIDConnectURL: "https://auth.example.test/.well-known/openid-configuration"},
	}
	api.Security = []document.SecurityRequirement{{"key": {}, "oauth": {"read:pets"}}, {}}
	api.Operations[0].Security = []document.SecurityRequirement{{"oidc": {}}}
	content := renderAPIViewFixture(t, api, manifest)
	for _, value := range []string{"Declared authentication", "Effective authentication", "alternative (OR)", "required (AND)", "Anonymous alternative", "read:pets", "X-API-Key", "OAuth flows", "OpenID Connect discovery"} {
		assert.Contains(t, content.HTML, value)
	}
	assertAPIReferenceIntegrity(t, content)
}

func TestAPIViewSchemaPrecisionCompositionAndRecursion(t *testing.T) {
	api, manifest := apiViewFixture()
	api.Schemas[0].Definition = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://schemas.example.test/Pet","type":["object","null"],"allOf":[{"$ref":"#/components/schemas/Pet"}],"oneOf":[{"const":1},{"const":2}],"properties":{"next":{"$ref":"#/components/schemas/Pet"}},"example":{"id":18446744073709551615},"default":9007199254740993}`)
	api.Schemas[0].ExampleJSON = `{"id":18446744073709551615}`
	api.Schemas[0].DefaultJSON = "9007199254740993"
	api.Schemas[0].References = []document.SchemaReference{{Pointer: "/allOf/0/$ref", Reference: "#/components/schemas/Pet", Target: api.Schemas[0].ID}, {Pointer: "/$dynamicRef", Reference: "#node", Target: api.Schemas[0].ID, Dynamic: true}}
	content := renderAPIViewFixture(t, api, manifest)
	for _, value := range []string{"2020-12", "allOf", "oneOf", "18446744073709551615", "9007199254740993", "static fallback", "without expanding referenced trees", "/allOf/0/$ref"} {
		assert.Contains(t, content.HTML, value)
	}
	assert.Equal(t, 1, strings.Count(content.HTML, `class="api-schema"`))
	assert.Less(t, len(content.HTML), 30<<10)
	assertAPIReferenceIntegrity(t, content)
}

func TestAPIViewFragmentScopesAndCanonicalSchemaLinks(t *testing.T) {
	api, manifest := apiViewFixture()
	description := "## Examples\n\n[Local](#examples) and [Pet](#/components/schemas/Pet)."
	api.Description = description
	api.Operations[0].Description = description
	api.Operations[0].Responses[0].Description = description
	api.Schemas[0].Definition = json.RawMessage(`{"type":"object","description":"## Examples\n\n[Local](#examples) and [Pet](#/components/schemas/Pet)."}`)
	content := renderAPIViewFixture(t, api, manifest)
	ids := make(map[string]bool)
	for _, heading := range content.Headings {
		if heading.Title == "Examples" {
			assert.False(t, ids[heading.ID])
			ids[heading.ID] = true
			assert.Contains(t, content.HTML, `href="#`+heading.ID+`"`)
		}
	}
	assert.Len(t, ids, 4)
	assert.Contains(t, content.HTML, `href="#`+api.Schemas[0].Anchor+`"`)
	assert.NotContains(t, content.HTML, `href="#/components`)
	assertAPIReferenceIntegrity(t, content)
}

func TestAPIViewEscapesAuthoredMarkupAndRejectsUnsafeLinks(t *testing.T) {
	api, manifest := apiViewFixture()
	api.Title = `<img src=x onerror=alert(1)>`
	api.Operations[0].Summary = `<script>alert('summary')</script>`
	api.Operations[0].Description = "<script>alert('description')</script>\n\nSafe **description**."
	api.Schemas[0].Definition = json.RawMessage(`{"const":"</code><script>alert(1)</script>","description":"Safe schema"}`)
	api.Operations[0].Parameters[0].Name = `x" autofocus onfocus="alert(1)`
	api.Operations[0].RequestBody = &document.RequestBody{Content: []document.MediaType{{Name: "text/plain", Examples: []document.Example{{Name: "unsafe", ExternalValue: "javascript:alert(1)"}}}}}
	content := renderAPIViewFixture(t, api, manifest)
	assert.NotContains(t, content.HTML, "<script>")
	assert.NotContains(t, content.HTML, "<img")
	assert.NotContains(t, content.HTML, `href="javascript:`)
	assert.Contains(t, content.HTML, "&lt;script&gt;")
	assert.Contains(t, content.HTML, "<strong>description</strong>")
	api.Operations[0].Description = "[Unsafe](javascript:alert(1))"
	_, err := renderAPI(context.Background(), api, 0, manifest, "_docs/pages/fixture.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link")
}

func TestAPIViewSchemaAndTextPreviewLimits(t *testing.T) {
	api, manifest := apiViewFixture()
	properties := make(map[string]any)
	for index := range 80 {
		properties[fmt.Sprintf("field%03d", index)] = map[string]string{"type": "string"}
	}
	definition, err := json.Marshal(map[string]any{"properties": properties, "description": "Property reference", "x-large": strings.Repeat("x", maxAPIRawPreview+1000)})
	require.NoError(t, err)
	api.Schemas[0].Definition = definition
	api.Operations[0].Summary = strings.Repeat("Ж", 20000)
	content := renderAPIViewFixture(t, api, manifest)
	assert.Contains(t, content.HTML, "first 32 properties in name order out of 80")
	assert.Contains(t, content.HTML, "preview truncated at 64 KiB")
	assert.Contains(t, content.HTML, "complete value is retained")
	assert.LessOrEqual(t, len(content.Text), maxAPIText)
	assert.True(t, utf8.ValidString(content.Text))
	assert.Less(t, len(content.HTML), 200<<10)
	api.Schemas[0].Definition = json.RawMessage(`{"description":"` + strings.Repeat("x", maxAPISchemaSummary) + `"}`)
	content = renderAPIViewFixture(t, api, manifest)
	assert.Contains(t, content.HTML, "Structured summary omitted")
}

type apiViewCancelContext struct {
	context.Context
	checks int
}

func (c *apiViewCancelContext) Err() error {
	c.checks++
	if c.checks >= 20 {
		return context.Canceled
	}
	return nil
}

func TestAPIViewBudgetsAndCancellation(t *testing.T) {
	api, manifest := apiViewFixture()
	api.Operations[0].Summary = strings.Repeat("<", maxRenderedBytes/2)
	_, err := renderAPI(context.Background(), api, 0, manifest, "_docs/pages/fixture.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rendered output exceeds")
	api, manifest = apiViewFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = renderAPI(ctx, api, 0, manifest, "_docs/pages/fixture.json")
	assert.ErrorIs(t, err, context.Canceled)
	_, err = renderAPI(&apiViewCancelContext{Context: context.Background()}, api, 0, manifest, "_docs/pages/fixture.json")
	assert.ErrorIs(t, err, context.Canceled)
	for _, input := range []struct {
		api      *document.API
		current  int
		dataPath string
	}{{nil, 0, "model.json"}, {api, -1, "model.json"}, {api, 0, ""}} {
		_, err := renderAPI(context.Background(), input.api, input.current, manifest, input.dataPath)
		assert.Error(t, err)
	}
}

func assertAPIReferenceIntegrity(t *testing.T, content pageContent) {
	t.Helper()
	ids := regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(content.HTML, -1)
	seen := make(map[string]bool)
	for _, id := range ids {
		value := html.UnescapeString(id[1])
		assert.False(t, seen[value], "duplicate rendered ID %s", value)
		assert.True(t, content.Anchors[value], "unregistered rendered ID %s", value)
		seen[value] = true
	}
	for _, ref := range content.References {
		if ref.page == 0 {
			assert.True(t, seen[ref.fragment], "reference points to absent anchor %s", ref.fragment)
		}
	}
}
