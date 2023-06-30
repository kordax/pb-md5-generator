package engine

import (
	plugingo "github.com/golang/protobuf/protoc-gen-go/plugin"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func testRequest() (*plugingo.CodeGeneratorRequest, error) {
	fds := &descriptorpb.FileDescriptorSet{}
	err := proto.Unmarshal(TestProtoDescriptor, fds)
	if err != nil {
		return nil, err
	}
	request := &plugingo.CodeGeneratorRequest{
		FileToGenerate: []string{"test_proto"},
		Parameter:      proto.String("Mtest_proto=./test-proto"),
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			fds.File[0],
		},
		CompilerVersion: nil,
	}
	return request, nil
}
