package parser

import (
	"testing"

	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestDescriptorParserSortsFilesByFullName(t *testing.T) {
	parser := &DescriptorParser{
		descriptors: []*protokit.FileDescriptor{
			{FileDescriptorProto: &descriptorpb.FileDescriptorProto{Name: proto.String("z/service.proto")}},
			{FileDescriptorProto: &descriptorpb.FileDescriptorProto{Name: proto.String("a/service.proto")}},
		},
		payload: map[string]string{
			"z/service.proto": "",
			"a/service.proto": "",
		},
		readOffsets: make(map[string]int),
	}

	files, err := parser.Parse()
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "a/service.proto", files[0].Filename())
	assert.Equal(t, "z/service.proto", files[1].Filename())
}
