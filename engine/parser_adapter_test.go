package engine

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/kordax/pb-md5-generator/internal/parser"
)

func TestNewDescriptorParserAndParse(t *testing.T) {
	request := testDescriptorRequest(t)

	adapter := NewDescriptorParser(request)
	parsed, err := adapter.Parse()
	require.NoError(t, err)

	require.Len(t, parsed, 1)
	assert.Equal(t, "test_proto", parsed[0].filename)
	assert.Equal(t, "", parsed[0].title)
	assert.NotEmpty(t, parsed[0].entries)
}

func TestConvertEntryType(t *testing.T) {
	assert.Equal(t, EntryTypeMessage, convertEntryType(parser.EntryTypeMessage))
	assert.Equal(t, EntryTypeEnum, convertEntryType(parser.EntryTypeEnum))
}

func TestConvertParsedStructures(t *testing.T) {
	request := testDescriptorRequest(t)
	result, err := parser.NewDescriptorParser(request).Parse()
	require.NoError(t, err)

	parsedFiles := convertParsedFiles(result)
	require.Len(t, parsedFiles, 1)

	var parsedMessage parser.Message
	var parsedEnum parser.Enum
	for _, entry := range result[0].Entries() {
		if e := entry.Message(); e != nil {
			parsedMessage = *e
			break
		}
	}
	for _, entry := range result[0].Entries() {
		if e := entry.Enum(); e != nil {
			parsedEnum = *e
			break
		}
	}
	require.NotNil(t, &parsedMessage)
	require.NotNil(t, &parsedEnum)

	convertedFile := convertParsedFile(result[0])
	assert.Equal(t, result[0].Index(), convertedFile.Index())
	assert.Len(t, convertedFile.entries, len(result[0].Entries()))

	convertedEntries := make([]Entry, 0, len(result[0].Entries()))
	for _, entry := range result[0].Entries() {
		convertedEntries = append(convertedEntries, convertEntry(entry))
	}
	assert.Len(t, convertedEntries, len(result[0].Entries()))

	convertedMessage := convertMessage(parsedMessage)
	assert.NotEmpty(t, convertedMessage.fields)
	assert.NotNil(t, convertedMessage.m)

	convertedFlags := convertFieldFlags(parsedMessage.Fields()[0].Flags())
	if convertedFlags.Present() {
		_ = convertedFlags.Get()
	}

	var messageWithAutocode *parser.Message
	var messageWithCode *parser.Message
	for _, entry := range result[0].Entries() {
		msg := entry.Message()
		if msg == nil {
			continue
		}
		if msg.Autocode().Present() {
			messageWithAutocode = msg
			break
		}
		if msg.Code().Present() {
			messageWithCode = msg
		}
	}
	require.NotNil(t, messageWithCode)
	convertedCode := convertCode(messageWithCode.Code())
	assert.True(t, convertedCode.Present())
	require.NotNil(t, messageWithAutocode)
	convertedAutocode := convertAutocode(messageWithAutocode.Autocode())
	assert.True(t, convertedAutocode.Present())

	convertedEnum := convertEnum(parsedEnum)
	assert.NotNil(t, convertedEnum.e)
	assert.NotNil(t, convertedEnum.values)

	convertedEnumField := convertEnumField(parsedEnum.Values()[0])
	assert.Equal(t, parsedEnum.Values()[0].Description(), convertedEnumField.description)
}

func TestConvertCodeFromAdapter(t *testing.T) {
	request := testDescriptorRequest(t)
	p := parser.NewDescriptorParser(request)
	parsed, err := p.Parse()
	require.NoError(t, err)

	files := convertParsedFiles(parsed)
	require.Len(t, files, 1)

	var message *Message
	for _, entry := range files[0].entries {
		if entry.msg != nil {
			if entry.msg.code.Present() || entry.msg.autocode.Present() {
				message = entry.msg
				break
			}
		}
	}
	require.NotNil(t, message)

	codegen := NewCodegenerator()
	block, err := codegen.Generate(files, message)
	require.NoError(t, err)
	require.NotNil(t, block)
	assert.NotEmpty(t, block.GetText())

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(block.GetText()), &payload))
	assert.NotEmpty(t, payload)
}

func TestMapStringToValueTypeAdapter(t *testing.T) {
	typ, err := mapStringToValueType("int")
	require.NoError(t, err)
	assert.Equal(t, ValueTypeInt, typ)

	_, err = mapStringToValueType("unknown")
	require.Error(t, err)
}

func TestFilterAdapter(t *testing.T) {
	assert.Equal(t, []int{2, 4}, filter([]int{1, 2, 3, 4}, func(v int) bool { return v%2 == 0 }))
}

func TestConvertMessageFieldNested(t *testing.T) {
	request := testDescriptorRequest(t)
	result, err := parser.NewDescriptorParser(request).Parse()
	require.NoError(t, err)

	var sourceMessage parser.Message
	for _, file := range result {
		for _, entry := range file.Entries() {
			if msg := entry.Message(); msg != nil {
				sourceMessage = *msg
				break
			}
		}
	}
	convertMessage(sourceMessage)

	convertedMessage := convertMessage(sourceMessage)
	assert.Equal(t, sourceMessage.Header(), convertedMessage.header)
}

func TestConvertFieldFlagsVariants(t *testing.T) {
	if _, err := exec.LookPath("protoc"); err != nil {
		t.Skip("protoc not installed in test environment")
	}

	request := requestFromProtoContent(t, `
syntax = "proto3";

package fixture;

option go_package = "./fixture";

message Flagged {
  // @type=jwt
  // @val=token
  string token = 1;

  // @len=12
  string user_code = 2;
}
`)

	parsedRequest, err := parser.NewDescriptorParser(request).Parse()
	require.NoError(t, err)

	var withType parser.MessageField
	var withoutType parser.MessageField
	for _, file := range parsedRequest {
		for _, entry := range file.Entries() {
			msg := entry.Message()
			if msg == nil {
				continue
			}
			for _, f := range msg.Fields() {
				switch f.Descriptor().GetName() {
				case "token":
					withType = f
				case "user_code":
					withoutType = f
				}
			}
		}
	}

	convertedWithType := convertFieldFlags(withType.Flags())
	assert.True(t, convertedWithType.Present())
	assert.Equal(t, ValueTypeJWT, *convertedWithType.Get().GetCustomType().Get())
	assert.Equal(t, "token", *convertedWithType.Get().GetValue().Get())

	convertedWithoutType := convertFieldFlags(withoutType.Flags())
	assert.True(t, convertedWithoutType.Present())
	assert.False(t, convertedWithoutType.Get().GetCustomType().Present())

	empty := convertFieldFlags(parser.Option[parser.FieldFlags]{})
	assert.False(t, empty.Present())
}

func TestConvertMessageFieldWithNestedMessage(t *testing.T) {
	fieldDescriptor := &protokit.FieldDescriptor{
		FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{
			Name: strToPtr("username"),
			Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		},
	}
	parent := &protokit.Descriptor{
		DescriptorProto: &descriptorpb.DescriptorProto{
			Name: strToPtr("RegisterRequest"),
		},
	}

	field := parser.NewMessageField(fieldDescriptor, parent, "desc", parser.ValueTypeString, nil)
	converted := convertMessageField(*field)

	assert.Equal(t, ValueTypeString, converted.ValueType())
	assert.Equal(t, fieldDescriptor, converted.Descriptor())
	assert.Equal(t, "desc", converted.description)
}

func TestConvertMessageFieldDescriptorAccess(t *testing.T) {
	field := NewMessageField(&protokit.FieldDescriptor{
		FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{
			Name: strToPtr("email"),
		},
	}, &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strToPtr("Req")}},
		"email field",
		ValueTypeEmail,
		nil,
	)

	assert.Equal(t, "email", field.Descriptor().GetName())
	assert.Equal(t, ValueTypeEmail, field.ValueType())
}

func requestFromProtoContent(t *testing.T, content string) *pluginpb.CodeGeneratorRequest {
	t.Helper()

	root := t.TempDir()
	protoFile := filepath.Join(root, "flagged.proto")
	require.NoError(t, os.WriteFile(protoFile, []byte(content), 0o600))

	compiler := internalproto.Compiler{
		ProtoDir:  root,
		OutputDir: filepath.Join(root, "tmp"),
	}
	request, err := compiler.RequestFromFiles([]string{protoFile})
	require.NoError(t, err)
	return request
}

func testDescriptorRequest(t *testing.T) *pluginpb.CodeGeneratorRequest {
	t.Helper()
	_, currentFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(currentFile))
	path := filepath.Join(root, "testdata", "test-proto", "test.pb.desc")

	blob, err := os.ReadFile(path)
	require.NoError(t, err)

	fds := &descriptorpb.FileDescriptorSet{}
	require.NoError(t, proto.Unmarshal(blob, fds))

	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test_proto"},
		Parameter:      proto.String("Mtest_proto=" + filepath.Join(root, "testdata", "test-proto")),
		ProtoFile:      fds.File,
	}
}
