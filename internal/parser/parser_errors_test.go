package parser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestParserErrorHelpers(t *testing.T) {
	request, err := testRequest()
	require.NoError(t, err)
	p := NewDescriptorParser(request)

	parsed, err := p.Parse()
	require.NoError(t, err)
	require.NotEmpty(t, parsed)

	var msgDesc *protokit.Descriptor
	var fieldDesc *protokit.FieldDescriptor
	var enumDesc *Enum
	for _, file := range parsed {
		for _, entry := range file.Entries() {
			if entry.Message() != nil && entry.Message().Descriptor() != nil {
				msgDesc = entry.Message().Descriptor()
				if len(entry.Message().Descriptor().GetMessageFields()) > 0 {
					fieldDesc = entry.Message().Descriptor().GetMessageFields()[0]
				}
			}
			if enumDesc == nil && entry.Enum() != nil {
				enumDesc = entry.Enum()
			}
		}
		if msgDesc != nil && enumDesc != nil {
			break
		}
	}
	require.NotNil(t, msgDesc)
	require.NotNil(t, fieldDesc)
	require.NotNil(t, enumDesc)
	require.NotEmpty(t, enumDesc.Values())

	msgErr := p.messageError(msgDesc, errors.New("boom"))
	assert.Contains(t, msgErr.Error(), "message")

	fieldErr := p.fieldError(fieldDesc, errors.New("boom"))
	assert.Contains(t, fieldErr.Error(), "field")

	enumErr := p.enumValueError(enumDesc.Values()[0].Descriptor(), errors.New("boom"))
	assert.Contains(t, enumErr.Error(), "enum value")
	assert.Contains(t, enumErr.Error(), "enum")

	assert.Equal(t, p.findFieldDeclaration("api.proto", "unknown"), p.findEnumValueDeclaration("api.proto", "unknown"))
}

func TestMapStringToValueTypeNormalizesCaseAndValidates(t *testing.T) {
	for _, tt := range []struct {
		in       string
		expected ValueType
	}{
		{in: "INT", expected: ValueTypeInt},
		{in: "UInt", expected: ValueTypeUInt},
		{in: "Float", expected: ValueTypeFloat},
		{in: "Bool", expected: ValueTypeBool},
		{in: "String", expected: ValueTypeString},
		{in: "ENUM", expected: ValueTypeEnum},
		{in: "JWT", expected: ValueTypeJWT},
		{in: "UUID", expected: ValueTypeUUID},
		{in: "Struct", expected: ValueTypeStruct},
		{in: "Email", expected: ValueTypeEmail},
		{in: "PHONE", expected: ValueTypePhone},
		{in: "PASSWORD", expected: ValueTypePassword},
	} {
		actual, err := MapStringToValueType(tt.in)
		require.NoError(t, err)
		assert.Equal(t, tt.expected, actual)
	}

	_, err := MapStringToValueType("not_real")
	assert.Error(t, err)
}

func TestParserParseInvalidFieldTypeError(t *testing.T) {
	request := requestFromProtoContent(t, `
syntax = "proto3";

package fixture;

option go_package = "./fixture";

message BadField {
  // @type=not_real
  string name = 1;
}
`)

	p := NewDescriptorParser(request)
	_, err := p.Parse()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown custom type provided")
}

func requestFromProtoContent(t *testing.T, content string) *pluginpb.CodeGeneratorRequest {
	t.Helper()

	root := t.TempDir()
	protoFile := filepath.Join(root, "broken.proto")
	require.NoError(t, os.WriteFile(protoFile, []byte(content), 0o600))

	p := internalproto.SourceParser{ProtoDir: root}
	request, err := p.RequestFromFiles([]string{protoFile})
	require.NoError(t, err)
	return request
}

func TestParseSyntaxInHelpers(t *testing.T) {
	request, err := testRequest()
	require.NoError(t, err)
	p := NewDescriptorParser(request)

	fileName := request.FileToGenerate[0]
	payload := p.payloadForFile(fileName)
	assert.Contains(t, payload, "syntax = 'proto3';")
	assert.Equal(t, 0, p.lineAt(fileName, -1))
	assert.Greater(t, p.lineAt(fileName, len(payload)), 0)
}
