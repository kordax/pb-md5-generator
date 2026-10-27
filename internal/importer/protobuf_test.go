package importer

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	protosource "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestProtobufPreservesRPCSemantics(t *testing.T) {
	source := fixture(t, "catalog.proto")
	api, err := Protobuf(context.Background(), "catalog.proto", source)
	require.NoError(t, err)
	assert.Equal(t, "grpc", api.Protocol)
	assert.Equal(t, "protobuf", api.Format)
	assert.Equal(t, "proto3", api.Version)
	assert.Equal(t, "catalog.v1", api.Title)
	require.Len(t, api.Operations, 2)
	get := &api.Operations[0]
	assert.Equal(t, "rpc", get.Interaction)
	assert.Empty(t, get.Method)
	assert.Equal(t, "/catalog.v1.Catalog/Get", get.Path)
	assert.Equal(t, "catalog.v1.Catalog", get.Service)
	assert.Equal(t, "Get", get.RPCMethod)
	assert.Equal(t, "Read one node.", get.Description)
	assert.False(t, get.ClientStreaming)
	assert.False(t, get.ServerStreaming)
	assert.True(t, api.Operations[1].ClientStreaming)
	assert.True(t, api.Operations[1].ServerStreaming)
	assert.Equal(t, "catalog.proto#/messageType/0", get.InputSchema)
	assert.Equal(t, get.InputSchema, get.OutputSchema)
	assert.Equal(t, "catalog.proto", get.Source.File)
	assert.Positive(t, get.Source.Line)
	assert.NotNil(t, get.Servers)
	assert.NotNil(t, get.Security)
	require.Len(t, api.Schemas, 2)
	node := findSchema(t, api, get.InputSchema)
	var definition map[string]any
	require.NoError(t, json.Unmarshal(node.Definition, &definition))
	properties := definition["properties"].(map[string]any)
	assert.Equal(t, "int64", properties["id"].(map[string]any)["format"])
	assert.Equal(t, "string", properties["id"].(map[string]any)["x-protobuf-json-type"])
	assert.Contains(t, properties, "displayName")
	assert.Equal(t, "array", properties["children"].(map[string]any)["type"])
	assert.Equal(t, "object", properties["related"].(map[string]any)["type"])
	assert.Equal(t, "base64", properties["digest"].(map[string]any)["contentEncoding"])
	assert.Equal(t, true, properties["active"].(map[string]any)["x-protobuf-optional"])
	assert.NotEmpty(t, definition["x-protobuf-oneofs"])
	require.Len(t, node.References, 3)
	assert.Equal(t, node.ID, node.References[0].Target)
	enum := findSchema(t, api, "catalog.proto#/messageType/0/enumType/0")
	assert.Contains(t, string(enum.Definition), `"STATUS_READY"`)
	assert.Contains(t, string(enum.Definition), `"number":1`)
	require.Len(t, api.Documents, 1)
	assert.Contains(t, string(api.Documents[0].Content), `"sourceCodeInfo"`)
	assert.Contains(t, string(api.Documents[0].Content), `"clientStreaming":true`)
	request, err := protosource.RequestFromSource("catalog.proto", source)
	require.NoError(t, err)
	reused, err := ProtobufRequest(context.Background(), "catalog.proto", request)
	require.NoError(t, err)
	assert.Equal(t, api, reused)
}

func TestProtobufDescriptorImportsAndScalarTypes(t *testing.T) {
	root := &descriptorpb.FileDescriptorProto{Name: proto.String("root.proto"), Package: proto.String("example"), MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Input")}}, Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("Service"), Method: []*descriptorpb.MethodDescriptorProto{{Name: proto.String("Read"), InputType: proto.String(".example.Input"), OutputType: proto.String(".other.Output")}}}}}
	dependency := &descriptorpb.FileDescriptorProto{Name: proto.String("deps/output.proto"), Package: proto.String("other"), MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Output")}}}
	request := &pluginpb.CodeGeneratorRequest{FileToGenerate: []string{"root.proto"}, ProtoFile: []*descriptorpb.FileDescriptorProto{root, dependency}}
	api, err := ProtobufRequest(context.Background(), "root.proto", request)
	require.NoError(t, err)
	assert.Equal(t, "proto2", api.Version)
	assert.Equal(t, "deps/output.proto#/messageType/0", api.Operations[0].OutputSchema)
	assert.Len(t, api.Documents, 2)
	_, err = ProtobufRequest(context.Background(), "root.proto", &pluginpb.CodeGeneratorRequest{ProtoFile: []*descriptorpb.FileDescriptorProto{root}})
	require.ErrorContains(t, err, "unresolved-protobuf-type")
	for number := descriptorpb.FieldDescriptorProto_TYPE_DOUBLE; number <= descriptorpb.FieldDescriptorProto_TYPE_SINT64; number++ {
		if number == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE || number == descriptorpb.FieldDescriptorProto_TYPE_GROUP || number == descriptorpb.FieldDescriptorProto_TYPE_ENUM {
			continue
		}
		field := &descriptorpb.FieldDescriptorProto{Name: proto.String(strings.ToLower(number.String())), Number: proto.Int32(int32(number)), Type: &number, Label: descriptorpb.FieldDescriptorProto_LABEL_REQUIRED.Enum(), DefaultValue: proto.String("1"), Options: &descriptorpb.FieldOptions{Deprecated: proto.Bool(true)}}
		root.MessageType[0].Field = append(root.MessageType[0].Field, field)
	}
	api, err = ProtobufRequest(context.Background(), "root.proto", request)
	require.NoError(t, err)
	assert.Contains(t, string(findSchema(t, api, "root.proto#/messageType/0").Definition), `"required"`)
}

func TestProtobufRejectsInvalidAndBoundedInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Protobuf(ctx, "api.proto", []byte("syntax = \"proto3\";"))
	require.ErrorIs(t, err, context.Canceled)
	_, err = ProtobufRequest(ctx, "api.proto", nil)
	require.ErrorIs(t, err, context.Canceled)
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"../api.proto", []byte("message A {}")},
		{"api.proto", make([]byte, MaxDocumentBytes+1)},
		{"api.proto", []byte{0xff}},
		{"api.proto", []byte("message {")},
		{"api.proto", []byte("syntax = \"proto3\"; message A { Missing missing = 1; }")},
	} {
		api, importErr := Protobuf(context.Background(), test.name, test.data)
		require.Error(t, importErr)
		assert.Nil(t, api)
	}
	_, err = ProtobufRequest(context.Background(), "api.proto", nil)
	require.Error(t, err)
	_, err = ProtobufRequest(context.Background(), "../api.proto", &pluginpb.CodeGeneratorRequest{})
	require.Error(t, err)
	file := &descriptorpb.FileDescriptorProto{Name: proto.String("api.proto")}
	for _, request := range []*pluginpb.CodeGeneratorRequest{
		{ProtoFile: []*descriptorpb.FileDescriptorProto{nil}},
		{ProtoFile: []*descriptorpb.FileDescriptorProto{file, file}},
		{ProtoFile: []*descriptorpb.FileDescriptorProto{{Name: proto.String("../escape.proto")}}},
		{ProtoFile: []*descriptorpb.FileDescriptorProto{{Name: proto.String("other.proto")}}},
		{ProtoFile: make([]*descriptorpb.FileDescriptorProto, MaxDocuments+1)},
	} {
		_, err = ProtobufRequest(context.Background(), "api.proto", request)
		require.Error(t, err)
	}
	cyclic := &descriptorpb.DescriptorProto{Name: proto.String("Cycle")}
	cyclic.NestedType = []*descriptorpb.DescriptorProto{cyclic}
	file.MessageType = []*descriptorpb.DescriptorProto{cyclic}
	_, err = ProtobufRequest(context.Background(), "api.proto", &pluginpb.CodeGeneratorRequest{ProtoFile: []*descriptorpb.FileDescriptorProto{file}})
	require.Error(t, err)
	var diagnostic *Error
	require.ErrorAs(t, err, &diagnostic)
	assert.Equal(t, "source-limit", diagnostic.Code)
}

func TestDescriptorBudgetHelper(t *testing.T) {
	for _, value := range []any{nil, []any{1, "a"}, []byte{1, 2}, [2]int{1, 2}} {
		assert.NoError(t, boundDescriptor(context.Background(), reflect.ValueOf(value), map[uintptr]bool{}, 0, new(int), new(int)))
	}
	assert.Error(t, boundDescriptor(context.Background(), reflect.ValueOf("a"), map[uintptr]bool{}, MaxDepth+1, new(int), new(int)))
	assert.Error(t, boundDescriptor(context.Background(), reflect.ValueOf(make([]int, MaxNodes+1)), map[uintptr]bool{}, 0, new(int), new(int)))
	assert.Equal(t, "/messageType/0/nestedType/1/field/2", protoPointer([]int32{4, 0, 3, 1, 2, 2}))
	assert.Equal(t, "/enumType/0/value/1", protoPointer([]int32{5, 0, 2, 1}))
	assert.Equal(t, "displayName", protoJSONName("display_name"))
}
