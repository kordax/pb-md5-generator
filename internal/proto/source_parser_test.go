package proto

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestRequestFromFilesRejectsInvalidFiles(t *testing.T) {
	parser := SourceParser{ProtoDir: t.TempDir()}
	_, err := parser.RequestFromFiles([]string{filepath.Join(parser.ProtoDir, "missing.proto")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot process file")
}

func TestSourceParserReadsProtoDirectly(t *testing.T) {
	protoDir := t.TempDir()
	protoFile := writeProto(t, protoDir, "sample.proto", `syntax = "proto3";
package sample;
// Request documentation.
message Request {
  string name = 1;
}
`)
	request, err := (SourceParser{ProtoDir: protoDir}).RequestFromFiles([]string{protoFile})
	require.NoError(t, err)
	require.Len(t, request.GetProtoFile(), 1)
	assert.Equal(t, "sample.proto", request.GetProtoFile()[0].GetName())
	assert.NotEmpty(t, request.GetProtoFile()[0].GetSourceCodeInfo().GetLocation())
	assert.Equal(t, []string{"sample.proto"}, request.GetFileToGenerate())
	assert.Equal(t, "Msample.proto="+protoDir, request.GetParameter())
}

func TestSourceParserDoesNotResolveImports(t *testing.T) {
	protoDir := t.TempDir()
	protoFile := writeProto(t, protoDir, "example/service.proto", `syntax = "proto3";
package example;
import "not/available.proto";
message Request { not.available.Identifier id = 1; }
`)

	request, err := (SourceParser{ProtoDir: protoDir}).RequestFromFiles([]string{protoFile})
	require.NoError(t, err)
	field := request.GetProtoFile()[0].GetMessageType()[0].GetField()[0]
	assert.Equal(t, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, field.GetType())
	assert.Equal(t, ".not.available.Identifier", field.GetTypeName())
}

func TestSourceParserPreservesNestedPathsAndAdditionalRoots(t *testing.T) {
	protoDir := t.TempDir()
	includeDir := t.TempDir()
	shared := writeProto(t, includeDir, "shared/types.proto", `syntax = "proto3";
package shared;
enum State { STATE_UNSPECIFIED = 0; }
`)
	service := writeProto(t, protoDir, "example/v1/service.proto", `syntax = "proto3";
package example.v1;
message Request { shared.State state = 1; }
`)

	request, err := (SourceParser{
		ProtoDir:   protoDir,
		ProtoPaths: []string{includeDir},
	}).RequestFromFiles([]string{service, shared})
	require.NoError(t, err)
	assert.Equal(t, []string{"example/v1/service.proto", "shared/types.proto"}, request.GetFileToGenerate())
	assert.Contains(t, request.GetParameter(), "Mexample/v1/service.proto="+protoDir)
	assert.Contains(t, request.GetParameter(), "Mshared/types.proto="+includeDir)
	field := request.GetProtoFile()[0].GetMessageType()[0].GetField()[0]
	assert.Equal(t, descriptorpb.FieldDescriptorProto_TYPE_ENUM, field.GetType())
	assert.Equal(t, ".shared.State", field.GetTypeName())
}

func TestSourceParserResolvesTypesRelativeToParentNamespace(t *testing.T) {
	protoDir := t.TempDir()
	common := writeProto(t, protoDir, "common/types.proto", `syntax = "proto3";
package example.common;
message Shared {}
`)
	service := writeProto(t, protoDir, "v1/service.proto", `syntax = "proto3";
package example.v1;
message Request { common.Shared shared = 1; }
`)

	request, err := (SourceParser{ProtoDir: protoDir}).RequestFromFiles([]string{service, common})
	require.NoError(t, err)
	field := request.GetProtoFile()[0].GetMessageType()[0].GetField()[0]
	assert.Equal(t, ".example.common.Shared", field.GetTypeName())
}

func TestSourceParserReportsSyntaxErrors(t *testing.T) {
	protoDir := t.TempDir()
	invalid := writeProto(t, protoDir, "invalid.proto", "this is not protobuf\n")
	_, err := (SourceParser{ProtoDir: protoDir}).RequestFromFiles([]string{invalid})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse protobuf source")
}

func writeProto(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
