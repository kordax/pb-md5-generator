package parser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestParseSyntax(t *testing.T) {
	syntax, length, err := parseSyntax("@code[xml]:")
	require.NoError(t, err)
	assert.Equal(t, SyntaxXml, syntax)
	assert.Equal(t, len("[xml]:"), length)

	syntax, length, err = parseSyntax("@code[json]")
	require.NoError(t, err)
	assert.Equal(t, SyntaxJson, syntax)
	assert.Equal(t, len("[json]"), length)

	_, _, err = parseSyntax("@code")
	assert.Error(t, err)
}

func TestParseAutocodeChar(t *testing.T) {
	params := []string{
		"max=10",
		"min=1.5",
		"len=32",
		"val=sample",
		"type=email",
	}

	maxValue, err := parseAutocodeChar(AutocodeMaxMarker, params)
	require.NoError(t, err)
	assert.Equal(t, 10.0, maxValue)

	minValue, err := parseAutocodeChar(AutocodeMinMarker, params)
	require.NoError(t, err)
	assert.Equal(t, 1.5, minValue)

	length, err := parseAutocodeChar(AutocodeMaxLengthMarker, params)
	require.NoError(t, err)
	assert.Equal(t, 32, length)

	value, err := parseAutocodeChar(AutocodeValueMarker, params)
	require.NoError(t, err)
	assert.Equal(t, "sample", value)

	customType, err := parseAutocodeChar(AutocodeTypeMarker, params)
	require.NoError(t, err)
	assert.Equal(t, "email", customType)

	missing, err := parseAutocodeChar("missing", params)
	require.NoError(t, err)
	assert.Nil(t, missing)

	_, err = parseAutocodeChar(AutocodeMaxMarker, []string{"max=bad"})
	assert.Error(t, err)
}

func TestProtoToFieldValueType(t *testing.T) {
	tests := []struct {
		name     string
		typ      descriptorpb.FieldDescriptorProto_Type
		expected ValueType
	}{
		{name: "id", typ: descriptorpb.FieldDescriptorProto_TYPE_INT64, expected: ValueTypeInt},
		{name: "amount", typ: descriptorpb.FieldDescriptorProto_TYPE_DOUBLE, expected: ValueTypeFloat},
		{name: "enabled", typ: descriptorpb.FieldDescriptorProto_TYPE_BOOL, expected: ValueTypeBool},
		{name: "user_uuid", typ: descriptorpb.FieldDescriptorProto_TYPE_STRING, expected: ValueTypeUUID},
		{name: "email", typ: descriptorpb.FieldDescriptorProto_TYPE_STRING, expected: ValueTypeEmail},
		{name: "phone", typ: descriptorpb.FieldDescriptorProto_TYPE_STRING, expected: ValueTypePhone},
		{name: "password", typ: descriptorpb.FieldDescriptorProto_TYPE_STRING, expected: ValueTypePassword},
		{name: "name", typ: descriptorpb.FieldDescriptorProto_TYPE_STRING, expected: ValueTypeString},
		{name: "raw", typ: descriptorpb.FieldDescriptorProto_TYPE_BYTES, expected: ValueTypeString},
		{name: "kind", typ: descriptorpb.FieldDescriptorProto_TYPE_ENUM, expected: ValueTypeEnum},
		{name: "nested", typ: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, expected: ValueTypeStruct},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := fieldDescriptor(tt.name, tt.typ, nil)
			assert.Equal(t, tt.expected, protoToFieldValueType(field))
		})
	}
}

func TestSmallGenericHelpers(t *testing.T) {
	assert.Equal(t, 5, ParsedFile{index: 5}.Index())

	assert.Equal(t, []int{2, 4}, mapSlice([]int{1, 2}, func(v int) int { return v * 2 }))
	assert.Equal(t, []int{2}, filter([]int{1, 2, 3}, func(v int) bool { return v%2 == 0 }))
	assert.Equal(t, 1, contains("b", []string{"a", "b"}))
	assert.Equal(t, -1, contains("c", []string{"a", "b"}))

	index, value := containsPredicate([]int{1, 2, 3}, func(v int) bool { return v > 1 })
	require.NotNil(t, value)
	assert.Equal(t, 1, index)
	assert.Equal(t, 2, *value)

	index, value = containsPredicate([]int{1}, func(v int) bool { return v > 1 })
	assert.Equal(t, -1, index)
	assert.Nil(t, value)
}

func TestSourceErrorFormattingAndLookup(t *testing.T) {
	baseErr := errors.New("bad marker")
	err := SourceError{File: "api.proto", Line: 12, Entity: "field name", Err: baseErr}
	assert.Equal(t, "api.proto:12: field name: bad marker", err.Error())
	assert.ErrorIs(t, err, baseErr)

	err = SourceError{File: "api.proto", Err: baseErr}
	assert.Equal(t, "api.proto: bad marker", err.Error())

	markerErr := markerError{marker: "@code", err: baseErr}
	assert.Equal(t, "bad marker", markerErr.Error())
	assert.ErrorIs(t, markerErr, baseErr)

	parser := &DescriptorParser{
		payload: map[string]string{
			"api.proto": "syntax = \"proto3\";\nmessage Request {\n  // @type=email\n  string email = 1;\n}\n",
		},
	}

	assert.Equal(t, 2, parser.lineAt("api.proto", parser.findDeclaration("api.proto", "message", "Request")))
	assert.Equal(t, 4, parser.lineAt("api.proto", parser.findFieldDeclaration("api.proto", "email")))
	assert.Equal(t, 3, parser.lineAt("api.proto", parser.findMarker("api.proto", "@type")))
	assert.Equal(t, -1, parser.findFieldDeclaration("api.proto", "missing"))
	assert.Zero(t, parser.lineAt("api.proto", -1))
	assert.Equal(t, 6, parser.lineAt("api.proto", 10_000))

	sourceErr := parser.sourceError("api.proto", "field email", parser.findFieldDeclaration("api.proto", "email"), baseErr)
	assert.Equal(t, "api.proto:4: field email: bad marker", sourceErr.Error())
}

func TestPayloadForFileReadsAndCaches(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "api.proto")
	require.NoError(t, os.WriteFile(filePath, []byte("line1\nline2\n"), 0o600))
	file, err := os.Open(filePath)
	require.NoError(t, err)
	defer file.Close()

	parser := &DescriptorParser{
		matchedFiles: map[string]*os.File{"api.proto": file},
		payload:      map[string]string{},
	}

	assert.Equal(t, "line1\nline2\n", parser.payloadForFile("api.proto"))
	require.NoError(t, os.WriteFile(filePath, []byte("changed"), 0o600))
	assert.Equal(t, "line1\nline2\n", parser.payloadForFile("api.proto"))
	assert.Empty(t, parser.payloadForFile("missing.proto"))
}

func fieldDescriptor(name string, typ descriptorpb.FieldDescriptorProto_Type, message *protokit.Descriptor) *protokit.FieldDescriptor {
	return &protokit.FieldDescriptor{
		FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{
			Name: &name,
			Type: &typ,
		},
		Message: message,
	}
}
