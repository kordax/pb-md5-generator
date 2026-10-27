package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func snapshotRequest(root string) *pluginpb.CodeGeneratorRequest {
	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"api.proto"},
		Parameter:      proto.String("Mapi.proto=" + root),
		ProtoFile: []*descriptorpb.FileDescriptorProto{{
			Name:        proto.String("api.proto"),
			Syntax:      proto.String("proto3"),
			MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Example")}},
		}},
	}
}

func TestSourceSnapshotsDoNotReadFilesystemOrRetainMutableInput(t *testing.T) {
	contents := []byte("// @title: Snapshot title\nmessage Example {}")
	parser := NewDescriptorParserWithOptions(snapshotRequest("/does-not-exist"), ParserOptions{
		Sources: map[string][]byte{"api.proto": contents},
	})
	copy(contents, strings.Repeat("x", len(contents)))
	files, err := parser.Parse()
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "Snapshot title", files[0].Title())
	assert.Empty(t, parser.matchedFiles)
}

func TestMissingSnapshotDoesNotFallBackToFilesystem(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "api.proto"), []byte("message Example {}"), 0o600))
	parser := NewDescriptorParserWithOptions(snapshotRequest(root), ParserOptions{Sources: map[string][]byte{}})
	_, err := parser.Parse()
	require.ErrorContains(t, err, "no source snapshot provided for file: api.proto")
}

func TestDefaultParserSnapshotsSourcesWithoutRetainingOpenFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "api.proto")
	require.NoError(t, os.WriteFile(path, []byte("// @title: Original\nmessage Example {}"), 0o600))
	parser := NewDescriptorParser(snapshotRequest(root))
	assert.Empty(t, parser.matchedFiles, "constructor must not retain file descriptors across parses")
	require.NoError(t, os.Remove(path), "source must be closed immediately, including on Windows")
	files, err := parser.Parse()
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "Original", files[0].Title())
}

func TestParseSyntaxMalformedBracketsDoNotPanic(t *testing.T) {
	for _, value := range []string{"@autocode ] [json]", "[", "]", "][", "@code[]", "@autocode[[json]"} {
		t.Run(value, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, _, err := parseSyntax(value)
				require.Error(t, err)
			})
		})
	}
}

func FuzzParseSyntax(f *testing.F) {
	for _, seed := range []string{"@code[json]", "@autocode[xml]", "@autocode ] [json]", "", "]["} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		_, _, _ = parseSyntax(value)
	})
}
