package engine

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func testRequest() (*pluginpb.CodeGeneratorRequest, error) {
	fds := &descriptorpb.FileDescriptorSet{}
	err := proto.Unmarshal(TestProtoDescriptor, fds)
	if err != nil {
		return nil, err
	}
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test_proto"},
		Parameter:      proto.String("Mtest_proto=./test-proto"),
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			fds.File[0],
		},
		CompilerVersion: nil,
	}
	return request, nil
}
