package importer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	protosource "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestProtobufIgnoreAppliesToEveryExportedRepresentation(t *testing.T) {
	source := fixture(t, "visibility.proto")
	request, err := protosource.RequestFromSource("visibility.proto", source)
	require.NoError(t, err)
	before := proto.Clone(request)
	api, err := ProtobufRequest(context.Background(), "visibility.proto", request)
	require.NoError(t, err)
	assert.True(t, proto.Equal(before, request), "import must not mutate caller descriptors")
	require.Len(t, api.Operations, 1)
	operation := api.Operations[0]
	assert.Equal(t, "/privacy.PublicService/Read", operation.Path)
	assert.Equal(t, "privacy.SecretMessage", operation.InputSchema)
	assert.Equal(t, "visibility.proto#/messageType/0", operation.OutputSchema)
	assert.Equal(t, 59, operation.Source.Line)
	require.Len(t, api.Schemas, 2)
	visible := findSchema(t, api, "visibility.proto#/messageType/0")
	assert.Equal(t, "privacy.Visible", visible.Name)
	assert.Equal(t, 15, visible.Source.Line)
	definition := string(visible.Definition)
	for _, name := range []string{"retainedInline", "retainedFenced", "retainedFixed", "retainedIndented", "retainedName"} {
		assert.Contains(t, definition, name)
	}
	assert.Contains(t, definition, `"x-protobuf-opaque":true`)
	assert.Contains(t, definition, `"x-protobuf-full-name":"privacy.SecretMessage"`)
	require.Len(t, visible.References, 1)
	assert.Equal(t, "visibility.proto#/messageType/0/enumType/0", visible.References[0].Target)
	encoded, err := json.Marshal(api)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(encoded)), "forbidden")
	require.Len(t, api.Documents, 1)
	var retained map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(api.Documents[0].Content, &retained))
	assert.NotContains(t, retained, "x-protobuf-source")
	var wireText string
	require.NoError(t, json.Unmarshal(retained["x-protobuf-descriptor-wire"], &wireText))
	wire, err := base64.StdEncoding.DecodeString(wireText)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(wire)), "forbidden")
	var sanitized descriptorpb.FileDescriptorProto
	require.NoError(t, proto.Unmarshal(wire, &sanitized))
	require.Len(t, sanitized.MessageType, 1)
	assert.Equal(t, "Visible", sanitized.MessageType[0].GetName())
	assert.Len(t, sanitized.MessageType[0].GetOneofDecl(), 1)
	assert.Len(t, sanitized.GetService(), 1)
	assert.Len(t, sanitized.GetService()[0].GetMethod(), 1)
	again, err := Protobuf(context.Background(), "visibility.proto", source)
	require.NoError(t, err)
	assert.Equal(t, api, again)
}

func TestProtobufFileVisibilityAndIndependentCommentChunks(t *testing.T) {
	file := &descriptorpb.FileDescriptorProto{Name: proto.String("api.proto"), Package: proto.String("private"), MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Secret")}}, EnumType: []*descriptorpb.EnumDescriptorProto{{Name: proto.String("SecretEnum")}}, SourceCodeInfo: &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{{Path: nil, LeadingComments: proto.String("@ignore")}}}}
	request := &pluginpb.CodeGeneratorRequest{ProtoFile: []*descriptorpb.FileDescriptorProto{file}}
	api, err := ProtobufRequest(context.Background(), "api.proto", request)
	require.NoError(t, err)
	assert.Empty(t, api.Schemas)
	assert.NotContains(t, string(api.Documents[0].Content), "Secret")
	file.SourceCodeInfo.Location = []*descriptorpb.SourceCodeInfo_Location{{Path: []int32{4, 0}, LeadingDetachedComments: []string{"```\n@ignore"}, LeadingComments: proto.String("@ignore")}}
	api, err = ProtobufRequest(context.Background(), "api.proto", request)
	require.NoError(t, err)
	assert.Len(t, api.Schemas, 1)
	assert.Equal(t, "private.SecretEnum", api.Schemas[0].Name)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err = visibleProtobufRequest(ctx, request)
	require.ErrorIs(t, err, context.Canceled)
}

func TestProtobufPreflightAndUnknownWireAreBounded(t *testing.T) {
	assert.NoError(t, CheckProtobufSource(context.Background(), "api.proto", []byte("// {{{{{\n/* [[[[ */ message A { string a = 1 [default = \"}}}\\\"\"]; }")))
	for _, source := range []string{strings.Repeat("{", MaxDepth+1), strings.Repeat(";", MaxNodes+1), "}"} {
		require.Error(t, CheckProtobufSource(context.Background(), "api.proto", []byte(source)))
	}
	file := &descriptorpb.FileDescriptorProto{Name: proto.String("api.proto")}
	// An unknown length-delimited option remains available to machine readers.
	file.Options = &descriptorpb.FileOptions{}
	file.Options.ProtoReflect().SetUnknown([]byte{0xa2, 0x06, 0x03, 'x', 'y', 'z'})
	api, err := ProtobufRequest(context.Background(), "api.proto", &pluginpb.CodeGeneratorRequest{ProtoFile: []*descriptorpb.FileDescriptorProto{file}})
	require.NoError(t, err)
	assert.Contains(t, string(api.Documents[0].Content), "x-protobuf-descriptor-wire")
	file.Options.ProtoReflect().SetUnknown(make([]byte, MaxSourceBytes+1))
	_, err = ProtobufRequest(context.Background(), "api.proto", &pluginpb.CodeGeneratorRequest{ProtoFile: []*descriptorpb.FileDescriptorProto{file}})
	require.ErrorContains(t, err, "source-limit")
	assert.NoError(t, boundDescriptor(context.Background(), reflect.ValueOf(map[string]any{"a": "b"}), map[uintptr]bool{}, 0, new(int), new(int)))
}

func TestProtobufWellKnownJSONMappingIsExplicit(t *testing.T) {
	file := protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto)
	api, err := ProtobufRequest(context.Background(), file.GetName(), &pluginpb.CodeGeneratorRequest{ProtoFile: []*descriptorpb.FileDescriptorProto{file}})
	require.NoError(t, err)
	assert.Contains(t, string(api.Schemas[0].Definition), `"x-protobuf-json-format":"RFC3339 string"`)
	assert.Contains(t, string(api.Schemas[0].Definition), `"seconds"`)
	for _, name := range []string{"Duration", "FieldMask", "Any", "Struct", "ListValue", "Value", "DoubleValue", "Int64Value", "UInt32Value", "BoolValue", "StringValue", "BytesValue"} {
		assert.NotEmpty(t, wellKnownJSONForm("google.protobuf."+name))
	}
	assert.Empty(t, wellKnownJSONForm("example.User"))
}
