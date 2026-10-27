package importer

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestCompositionAndEmptyReferencesRetainTheirScope(t *testing.T) {
	source := contract("3.1.2", `{"schemas":{"Shared":{"type":"object"},"Composed":{"$defs":{"Local":{"type":"string"}},"allOf":[{"$ref":"#/components/schemas/Shared"}],"anyOf":[true,false],"oneOf":[{"type":"integer"},{"type":"string"}],"prefixItems":[{"type":"string"}],"not":{"type":"null"},"if":{"type":"object"},"then":{"required":["id"]},"else":false,"items":true,"contains":{"type":"integer"},"propertyNames":{"type":"string"},"contentSchema":{"type":"object"},"unevaluatedItems":false,"additionalProperties":true},"External":{"$ref":"refs/tree.json"}}}`, "")
	api, err := OpenAPI(context.Background(), "api.json", []byte(source), func(string) ([]byte, error) {
		return []byte(`{"$id":"local","$ref":"","$defs":{"Child":{"$id":"child","$anchor":"leaf","properties":{"self":{"$ref":"#leaf"}}}},"allOf":[{"$ref":"#/$defs/Child"}]}`), nil
	})
	require.NoError(t, err)
	child := findSchema(t, api, "refs/tree.json#/$defs/Child")
	require.Len(t, child.References, 1)
	assert.Equal(t, child.ID, child.References[0].Target)
	root := findSchema(t, api, "refs/tree.json#")
	require.Len(t, root.References, 3)
	assert.Equal(t, root.ID, root.References[0].Target)
	assert.Equal(t, "", root.References[0].Reference)
	assert.Contains(t, string(findSchema(t, api, "api.json#/components/schemas/Composed").Definition), `"prefixItems"`)
}

func TestOpenAPI32MediaAndSecurityReferences(t *testing.T) {
	source := contract("3.2.1", `{"mediaTypes":{"JSON":{"schema":{"type":"integer"},"example":9007199254740993}}}`, `{"/read":{"get":{"parameters":[{"name":"Authorization","in":"header","schema":{"type":"string"}}],"responses":{"200":{"description":"ok","headers":{"Content-Type":{"schema":{"type":"string"}}},"content":{"application/json":{"$ref":"#/components/mediaTypes/JSON"}}}}}}}`)
	source = strings.Replace(source, `"paths":`, `"security":[{"auth.json":[]}],"paths":`, 1)
	api, err := OpenAPI(context.Background(), "api.json", []byte(source), func(name string) ([]byte, error) {
		assert.Equal(t, "auth.json", name)
		return []byte(`{"type":"http","scheme":"basic"}`), nil
	})
	require.NoError(t, err)
	assert.Equal(t, "auth.json", api.SecuritySchemes[0].ID)
	assert.Equal(t, "basic", api.SecuritySchemes[0].Scheme)
	assert.Empty(t, api.Operations[0].Parameters)
	assert.Empty(t, api.Operations[0].Responses[0].Headers)
	media := api.Operations[0].Responses[0].Content[0]
	assert.Equal(t, "api.json#/components/mediaTypes/JSON/schema", media.Schema)
	assert.Equal(t, "9007199254740993", media.ExampleJSON)
}

func TestReferencedObjectShapesAreValidatedEvenWhenUnused(t *testing.T) {
	for _, kind := range []string{"parameters", "headers", "requestBodies", "responses", "securitySchemes", "examples", "links", "callbacks", "pathItems", "mediaTypes"} {
		t.Run(kind, func(t *testing.T) {
			components, err := json.Marshal(map[string]any{kind: map[string]any{"Invalid": map[string]any{"$ref": "invalid.json"}}})
			require.NoError(t, err)
			_, err = OpenAPI(context.Background(), "api.json", []byte(contract("3.2.1", string(components), "")), func(string) ([]byte, error) { return []byte(`{"invalid":123}`), nil })
			require.Error(t, err)
		})
	}
}

func TestIgnoredMapDoesNotRetainSyntheticPrivateDescriptor(t *testing.T) {
	source := []byte("syntax = \"proto3\";\nmessage Maps {\n// @ignore\nmap<string,string> forbidden_map_content = 1;\nmap<string,string> visible = 2;\n}\n")
	api, err := Protobuf(context.Background(), "maps.proto", source)
	require.NoError(t, err)
	encoded, err := json.Marshal(api)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(encoded)), "forbidden")
	assert.Contains(t, string(encoded), "VisibleEntry")
	assert.Contains(t, string(api.Schemas[0].Definition), `"visible"`)
}

func TestMessageBudgetIncludesMapValues(t *testing.T) {
	message, err := structpb.NewStruct(map[string]any{"nested": map[string]any{"value": "text"}})
	require.NoError(t, err)
	assert.NoError(t, boundDescriptor(context.Background(), reflect.ValueOf(message), map[uintptr]bool{}, 0, new(int), new(int)))
	message, err = structpb.NewStruct(map[string]any{"oversize": strings.Repeat("x", MaxSourceBytes+1)})
	require.NoError(t, err)
	assert.Error(t, boundDescriptor(context.Background(), reflect.ValueOf(message), map[uintptr]bool{}, 0, new(int), new(int)))
}
