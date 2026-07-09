package parser

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/pseudomuto/protokit"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestDescriptorParser_ParseHeader(t *testing.T) {
	request, err := testRequest()
	assert.NoError(t, err)
	parser := NewDescriptorParser(request)
	header, _, err := parser.nextMarker(protokit.ParseCodeGenRequest(request)[0], HeaderMarker)
	assert.NoError(t, err)
	assert.NotEmpty(t, header)
}

func TestDescriptorParser_DescriptorToDocument(t *testing.T) {
	request, err := testRequest()
	assert.NoError(t, err)
	parser := NewDescriptorParser(request)
	entries, err := parser.Parse()
	assert.NoError(t, err)
	assert.NotEmpty(t, entries)
	assert.Len(t, entries, 1)
	assert.Equal(t, "test_proto", entries[0].Filename())
	assert.Empty(t, entries[0].Title())
}

func testRequest() (*pluginpb.CodeGeneratorRequest, error) {
	_, currentFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(filepath.Dir(currentFile)))
	descPath := filepath.Join(root, "testdata", "test-proto", "test.pb.desc")
	blob, err := os.ReadFile(descPath)
	if err != nil {
		return nil, err
	}
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(blob, fds); err != nil {
		return nil, err
	}
	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test_proto"},
		Parameter:      proto.String("Mtest_proto=" + filepath.Join(root, "testdata", "test-proto")),
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			fds.File[0],
		},
	}, nil
}
