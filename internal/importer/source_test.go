package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSourcesPreserveExactYAMLAndLocations(t *testing.T) {
	sources := newSources(context.Background(), nil)
	source, err := sources.read("api.yaml", []byte("a: 18446744073709551615\nb: 0xFF\nc: +.5\nd: 1e20\ne: null\nf: true\ng: 2026-10-27\nh: &name [one, two]\ni: *name\n"))
	require.NoError(t, err)
	assert.Equal(t, json.Number("18446744073709551615"), object(source.value)["a"])
	assert.Equal(t, json.Number("255"), object(source.value)["b"])
	assert.Equal(t, json.Number("0.5"), object(source.value)["c"])
	assert.Equal(t, json.Number("1e20"), object(source.value)["d"])
	assert.Nil(t, object(source.value)["e"])
	assert.Equal(t, true, object(source.value)["f"])
	assert.Equal(t, "2026-10-27", object(source.value)["g"])
	assert.Equal(t, []any{"one", "two"}, object(source.value)["i"])
	assert.Equal(t, document.Location{File: "api.yaml", Line: 2, Column: 4}, sourceLocation(source, "/b"))
	assert.Equal(t, document.Location{File: "api.yaml"}, sourceLocation(source, "/absent"))
	cached, err := sources.read("api.yaml", nil)
	require.NoError(t, err)
	assert.Same(t, source, cached)
	for _, test := range []string{"a: .nan", "a: .inf", "a: !!binary SGVsbG8=", "a: &a [*a]", "a: {<<: {x: 1}}", "? [a, b]\n: value", "a: 1\na: 2", "a: [", "", "null"} {
		parsed, parseErr := newSources(context.Background(), nil).read("bad.yaml", []byte(test))
		if test == "null" {
			require.NoError(t, parseErr)
			assert.Nil(t, parsed.value)
			continue
		}
		require.Error(t, parseErr, test)
	}
	for _, name := range []string{"../a", "/a", "a\\b", "https://example.test/a", "a%2fb", "a?b", "a#b", "a\n"} {
		assert.False(t, validSourceName(name), name)
	}
}

func TestSourceGraphBudgets(t *testing.T) {
	ctx := context.Background()
	sources := newSources(ctx, func(string) ([]byte, error) { return []byte("{}"), nil })
	for index := range MaxDocuments {
		_, err := sources.read(fmt.Sprintf("%d.yaml", index), nil)
		require.NoError(t, err)
	}
	_, err := sources.read("overflow.yaml", nil)
	require.ErrorContains(t, err, "limit")
	large := []byte(`{"text":"` + strings.Repeat("x", MaxDocumentBytes-20) + `"}`)
	sources = newSources(ctx, func(string) ([]byte, error) { return large, nil })
	for index := range 4 {
		_, err = sources.read(fmt.Sprintf("%d.yaml", index), nil)
		require.NoError(t, err)
	}
	_, err = sources.read("overflow.yaml", nil)
	require.ErrorContains(t, err, "limit")
	_, err = newSources(ctx, nil).read("deep.json", []byte(strings.Repeat("[", MaxDepth+2)+"0"+strings.Repeat("]", MaxDepth+2)))
	require.ErrorContains(t, err, "depth")
	_, err = newSources(ctx, nil).read("nodes.json", []byte("["+strings.Repeat("0,", MaxNodes)+"0]"))
	require.ErrorContains(t, err, "node")
	_, err = newSources(ctx, nil).read("structure.yaml", []byte(strings.Repeat("#\n", MaxNodes+1)))
	require.ErrorContains(t, err, "complex")
	_, err = newSources(ctx, nil).read("invalid.yaml", []byte{0xff})
	require.Error(t, err)
	alias := "a: &a [" + strings.Repeat("0,", 1000) + "0]\nb: [" + strings.Repeat("*a,", 1000) + "*a]\n"
	_, err = newSources(ctx, nil).read("aliases.yaml", []byte(alias))
	require.ErrorContains(t, err, "node")
	model := &document.API{Title: strings.Repeat("x", MaxModelBytes/6+1)}
	assert.Error(t, checkModelSize(model))
	assert.Error(t, checkModelSize(&document.API{Schemas: make([]document.Schema, MaxNodes)}))
	assert.NoError(t, checkModelSize(&document.API{Security: []document.SecurityRequirement{{"key": {"read"}}}}))
	_, err = (deniedLoader{}).Load("https://example.test/never")
	assert.Error(t, err)
}

func TestNumericAndURIValidation(t *testing.T) {
	for _, test := range []struct {
		input, want string
		integer     bool
	}{
		{"1_000", "1000", true}, {"0o17", "15", true}, {"+7", "7", true}, {"-.5", "-0.5", false}, {"1.", "1.0", false},
	} {
		value, err := exactYAMLNumber(test.input, test.integer)
		require.NoError(t, err)
		assert.Equal(t, test.want, string(value))
	}
	for _, test := range []struct {
		value   string
		integer bool
	}{{"bad", true}, {"nan", false}, {"1e10001", false}, {strings.Repeat("1", 1025), true}} {
		_, err := exactYAMLNumber(test.value, test.integer)
		require.Error(t, err)
	}
	for _, uri := range []string{"%", "https://user:secret@example.test/a", "a?query"} {
		_, err := referenceURL(uri)
		require.Error(t, err)
	}
	assert.Equal(t, "a~1b~0c", escapePointer("a/b~c"))
	cause := errors.New("private input that must not appear")
	diagnostic := &Error{Code: "invalid", Source: document.Location{File: "api.yaml", Line: 3, Column: 4}, Pointer: "/paths", Message: "invalid object", Cause: cause}
	assert.ErrorIs(t, diagnostic, cause)
	assert.NotContains(t, diagnostic.Error(), cause.Error())
	assert.Contains(t, diagnostic.Error(), "api.yaml:3:4#/paths")
}

func TestConversionAndReflectionLimits(t *testing.T) {
	sources := newSources(context.Background(), nil)
	source := &parsedSource{name: "api.yaml", locations: map[string]document.Location{}}
	_, err := sources.convert(source, &yaml.Node{Kind: yaml.DocumentNode}, "", 0, map[*yaml.Node]bool{})
	require.Error(t, err)
	_, err = sources.convert(source, &yaml.Node{Kind: yaml.Kind(255)}, "", 0, map[*yaml.Node]bool{})
	require.Error(t, err)
	assert.NoError(t, boundDescriptor(context.Background(), reflect.ValueOf([1]string{"a"}), map[uintptr]bool{}, 0, new(int), new(int)))
}
