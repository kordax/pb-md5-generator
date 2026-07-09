package proto

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestCheckDependenciesReportsMissingTools(t *testing.T) {
	originalPath := os.Getenv("PATH")
	t.Cleanup(func() {
		require.NoError(t, os.Setenv("PATH", originalPath))
	})

	emptyDir := t.TempDir()
	require.NoError(t, os.Setenv("PATH", emptyDir))
	err := CheckDependencies()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protoc binary is required")

	protoc := filepath.Join(emptyDir, "protoc")
	require.NoError(t, os.WriteFile(protoc, []byte("#!/bin/sh\n"), 0o700))
	err = CheckDependencies()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protoc-gen-go binary is required")
}

func TestRequestFromFilesRejectsInvalidFiles(t *testing.T) {
	compiler := Compiler{ProtoDir: t.TempDir(), OutputDir: t.TempDir()}

	_, err := compiler.RequestFromFiles([]string{filepath.Join(compiler.ProtoDir, "missing.proto")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot process file")
}

func TestCompileSimpleProto(t *testing.T) {
	protoDir := t.TempDir()
	outputDir := t.TempDir()
	protoFile := filepath.Join(protoDir, "sample.proto")
	require.NoError(t, os.WriteFile(protoFile, []byte(`syntax = "proto3";
package sample;
option go_package = "./sample";
message Request {
  string name = 1;
}
`), 0o600))

	compiler := Compiler{
		ProtoDir:  protoDir,
		OutputDir: outputDir,
		runProtoc: func(args []string) ([]byte, string, error) {
			assert.Contains(t, args, "--proto_path="+protoDir)
			assert.Contains(t, args, "--descriptor_set_out="+filepath.Join(outputDir, descriptorFilename))
			assert.Contains(t, args, "--include_source_info")
			assert.Contains(t, args, "sample.proto")
			assert.Contains(t, args, "--go_out="+outputDir)

			payload, err := goproto.Marshal(&descriptorpb.FileDescriptorSet{
				File: []*descriptorpb.FileDescriptorProto{{Name: goproto.String("sample.proto")}},
			})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(outputDir, descriptorFilename), payload, 0o600))
			return []byte("ok"), "", nil
		},
	}
	descriptors, parameters, err := compiler.Compile([]string{protoFile})
	require.NoError(t, err)
	require.Len(t, descriptors, 1)
	assert.Equal(t, "sample.proto", descriptors[0].GetName())
	assert.Equal(t, []string{"Msample.proto=" + protoDir}, parameters)

	request, err := compiler.RequestFromFiles([]string{protoFile})
	require.NoError(t, err)
	assert.Equal(t, []string{"sample.proto"}, request.GetFileToGenerate())
	assert.Contains(t, request.GetParameter(), "Msample.proto="+protoDir)
}

func TestCompileReturnsProtocAndDescriptorErrors(t *testing.T) {
	compiler := Compiler{
		ProtoDir:  t.TempDir(),
		OutputDir: t.TempDir(),
		runProtoc: func([]string) ([]byte, string, error) {
			return []byte("out"), "stderr", errors.New("protoc failed")
		},
	}

	_, _, err := compiler.Compile([]string{filepath.Join(compiler.ProtoDir, "sample.proto")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "protoc failed")

	compiler.runProtoc = func([]string) ([]byte, string, error) {
		return nil, "", nil
	}
	_, _, err = compiler.Compile([]string{filepath.Join(compiler.ProtoDir, "sample.proto")})
	require.Error(t, err)

	compiler.runProtoc = func([]string) ([]byte, string, error) {
		return nil, "", os.WriteFile(filepath.Join(compiler.OutputDir, descriptorFilename), []byte("invalid"), 0o600)
	}
	_, _, err = compiler.Compile([]string{filepath.Join(compiler.ProtoDir, "sample.proto")})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "cannot parse") || strings.Contains(err.Error(), "cannot unmarshal"))
}
