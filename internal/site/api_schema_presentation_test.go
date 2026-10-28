package site

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIViewInlineSchemasKeepReadableLinksAndOriginalAnchors(t *testing.T) {
	api, manifest := apiViewFixture()
	inline := []document.Schema{
		{ID: "contracts/api.yaml#/response", Name: "schema", Definition: json.RawMessage(`{"$ref":"#/components/schemas/Pet"}`)},
		{ID: "contracts/api.yaml#/parameter", Name: "schema", Definition: json.RawMessage(`{"type":"integer","format":"int64","description":"Exact identifier","default":9007199254740993}`)},
		{ID: "contracts/api.yaml#/titled", Definition: json.RawMessage(`{"title":"Pet <identity>","type":"string"}`)},
		{ID: "contracts/api.yaml#/boolean", Name: "schema", Definition: json.RawMessage(`true`)},
		{ID: "contracts/api.yaml#/composed", Name: "schema", Definition: json.RawMessage(`{"oneOf":[{"type":"string"},{"type":"integer"}]}`)},
	}
	for index := range inline {
		inline[index].Anchor = document.APIAnchor("schema", inline[index].ID)
		inline[index].Source = api.Source
	}
	inline[0].References = []document.SchemaReference{{Pointer: "/$ref", Reference: "#/components/schemas/Pet", Target: api.Schemas[0].ID}}
	api.Schemas = append(api.Schemas, inline...)
	api.Operations[0].Responses[0].Content[0].Schema = inline[0].ID
	api.Operations[0].Parameters[0].Schema = inline[1].ID
	content := renderAPIViewFixture(t, api, manifest)

	assert.Contains(t, content.HTML, `Schema: <a href="#`+inline[0].Anchor+`"><code>Pet</code></a>`)
	assert.Contains(t, content.HTML, `class="api-parameter-type">integer · int64</span>`)
	assert.Contains(t, content.HTML, `Inline schema definitions`)
	assert.Contains(t, content.HTML, `Pet &lt;identity&gt;`)
	assert.NotContains(t, content.HTML, `>schema</h3>`)
	assert.Equal(t, len(inline), strings.Count(content.HTML, `class="api-schema api-inline-schema"><summary`))
	assert.NotContains(t, content.HTML, `class="api-schema api-inline-schema" open`)
	for _, schema := range api.Schemas {
		assert.True(t, content.Anchors[schema.Anchor], schema.ID)
		assert.Equal(t, 1, strings.Count(content.HTML, `id="`+schema.Anchor+`"`), schema.ID)
	}
	for _, heading := range content.Headings {
		for _, schema := range inline {
			assert.NotEqual(t, schema.Anchor, heading.ID)
		}
	}
	for _, retained := range []string{"9007199254740993", "Exact identifier", "Raw schema definition", "oneOf", "true"} {
		assert.Contains(t, content.HTML, retained)
	}
	assertAPIReferenceIntegrity(t, content)
}

func TestSchemaPresentationResolvesLocalAliasesWithoutExpandingUnsafeOrRecursiveTrees(t *testing.T) {
	api, _ := apiViewFixture()
	renderer := &apiRenderer{api: api, schemas: make(map[string]document.Schema)}
	add := func(name, display, definition string, source string) string {
		id := "contracts/api.yaml#/" + name
		renderer.schemas[id] = document.Schema{ID: id, Name: display, Source: document.Location{File: source}, Definition: json.RawMessage(definition)}
		return id
	}
	pet := add("pet", "pets.v1.Pet", `{"type":"object"}`, "contracts/api.yaml")
	alias := add("alias", "schema", `{"$ref":"#/pet"}`, "")
	cross := add("cross", "schema", `{"$ref":"types.json#/Pet"}`, "contracts/api.yaml")
	renderer.schemas["contracts/types.json#/Pet"] = document.Schema{Name: "Pet", Definition: json.RawMessage(`{"type":"object"}`)}
	cycle := add("cycle", "schema", `{"$ref":"#/cycle2"}`, "contracts/api.yaml")
	add("cycle2", "schema", `{"$ref":"#/cycle"}`, "contracts/api.yaml")
	chain := make([]string, 18)
	for index := range chain {
		definition := fmt.Sprintf(`{"$ref":"#/chain%d"}`, index+1)
		if index == len(chain)-1 {
			definition = `{"type":"string"}`
		}
		chain[index] = add(fmt.Sprintf("chain%d", index), "schema", definition, "contracts/api.yaml")
	}
	cases := []struct {
		name, id, label, kind string
	}{
		{"named", pet, "pets.v1.Pet", "object"},
		{"local alias with source fallback", alias, "pets.v1.Pet", "object"},
		{"relative file alias", cross, "Pet", "object"},
		{"cycle", cycle, "Schema definition", "schema"},
		{"depth ceiling", chain[0], "Schema definition", "schema"},
		{"last permitted depth", chain[2], "string", "string"},
		{"unknown", "missing", "Schema definition", "schema"},
		{"empty identifier", "", "Schema definition", "schema"},
		{"union", add("union", "schema", `{"type":["string","null"]}`, ""), "string | null", "string | null"},
		{"formatted scalar", add("integer", "schema", `{"type":"integer","format":"int64"}`, ""), "integer · int64", "integer · int64"},
		{"title", add("title", "schema", `{"title":"Readable identity"}`, ""), "Readable identity", "schema"},
		{"opaque named", add("opaque", "Opaque", `{}`, ""), "Opaque", "Opaque"},
		{"empty name", add("empty", "", `{}`, ""), "Schema definition", "schema"},
		{"invalid JSON", add("invalid", "schema", `{"type":`, ""), "Schema definition", "schema"},
		{"boolean schema", add("boolean", "schema", `false`, ""), "Schema definition", "schema"},
		{"invalid type", add("invalid-type", "schema", `{"type":7}`, ""), "Schema definition", "schema"},
		{"empty union", add("empty-union", "schema", `{"type":[]}`, ""), "Schema definition", "schema"},
		{"external ref", add("external", "schema", `{"$ref":"https://outside.invalid/a#/Pet"}`, ""), "Schema definition", "schema"},
		{"network ref", add("network", "schema", `{"$ref":"//outside.invalid/a#/Pet"}`, ""), "Schema definition", "schema"},
		{"query ref", add("query", "schema", `{"$ref":"api.yaml?version=2#/pet"}`, ""), "Schema definition", "schema"},
		{"bad URL", add("bad-url", "schema", `{"$ref":"%zz#/pet"}`, ""), "Schema definition", "schema"},
		{"unanchored ref", add("unanchored", "schema", `{"$ref":"types.json"}`, ""), "Schema definition", "schema"},
		{"over budget", add("large", "schema", strings.Repeat(" ", maxAPISchemaSummary+1), ""), "Schema definition", "schema"},
		{"large named definition", add("large-named", "Large payload", strings.Repeat(" ", maxAPISchemaSummary+1), ""), "Large payload", "schema"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.label, renderer.schemaLabel(test.id))
			assert.Equal(t, test.kind, renderer.parameterType(test.id))
		})
	}
}

func TestAPIViewParameterGroupsRetainSerializationAndMetadata(t *testing.T) {
	api, manifest := apiViewFixture()
	api.Operations[0].Parameters = nil
	for _, location := range []string{"path", "query", "header", "cookie", "querystring", "extension"} {
		api.Operations[0].Parameters = append(api.Operations[0].Parameters, document.Parameter{Name: location + "Value", In: location, Schema: api.Schemas[0].ID, Description: "Description for " + location, Required: location == "path", Deprecated: location == "query", Style: "simple", Explode: new(false), AllowReserved: true, AllowEmptyValue: true, Raw: json.RawMessage(`{"x-retained":"metadata"}`)})
	}
	content := renderAPIViewFixture(t, api, manifest)
	for _, retained := range []string{"Path parameters", "Query parameters", "Header parameters", "Cookie parameters", "Query string parameters", ">Parameters</h4>", "Description for extension", "Deprecated", "Optional", "Required", "Schema, examples &amp; serialization", "Style: simple", "explode: false", "allowReserved: true", "allowEmptyValue: true", "x-retained", "metadata"} {
		assert.Contains(t, content.HTML, retained)
	}
	assert.NotContains(t, content.HTML, "Serialization / description</th>")
	assertAPIReferenceIntegrity(t, content)
}

func TestAPIViewResponseSamplesKeepExactWireExamplesAndBoundThePreview(t *testing.T) {
	api, manifest := apiViewFixture()
	wire := `{"wire":"<payload>","id":9007199254740993}`
	api.Operations[0].Responses[0].Content = []document.MediaType{
		{Name: "application/companion", ExampleJSON: `{"companion":1}`},
		{Name: "application/raw", Example: json.RawMessage(`{"raw":2}`)},
		{Name: "application/wire", Examples: []document.Example{{SerializedValue: &wire}}},
		{Name: "application/value-json", Examples: []document.Example{{ValueJSON: `{"valueJSON":3}`}}},
		{Name: "application/data", Examples: []document.Example{{DataValue: json.RawMessage(`{"data":4}`)}}},
		{Name: "application/value", Examples: []document.Example{{Value: json.RawMessage(`{"value":5}`)}}},
		{Name: "application/undeclared"},
		{Name: "text/plain", ExampleJSON: strings.Repeat("Я", maxAPIRawPreview)},
	}
	content := renderAPIViewFixture(t, api, manifest)
	require.True(t, utf8.ValidString(content.HTML))
	for _, retained := range []string{`{"companion":1}`, `{"raw":2}`, wire, `{"valueJSON":3}`, `{"data":4}`, `{"value":5}`} {
		assert.Contains(t, content.HTML, html.EscapeString(retained))
	}
	assert.Contains(t, content.HTML, "No example body is declared for this media type.")
	assert.Contains(t, content.HTML, "Example preview truncated. Complete examples remain in the contract.")
	assertAPIReferenceIntegrity(t, content)

	api.Operations[0].Responses = nil
	content = renderAPIViewFixture(t, api, manifest)
	assert.Contains(t, content.HTML, "No response examples are declared.")
}
