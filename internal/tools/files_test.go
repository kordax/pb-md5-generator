package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtoFilesRecursively(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.proto"), []byte("syntax = \"proto3\";"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "skip.txt"), []byte("x"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "nested"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "b.proto"), []byte("syntax = \"proto3\";"), 0o600))

	files, err := ProtoFilesRecursively(root)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(root, "a.proto"),
		filepath.Join(root, "nested", "b.proto"),
	}, files)

	_, err = ProtoFilesRecursively(filepath.Join(root, "missing"))
	assert.Error(t, err)
}

func TestRequireRegularFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.proto")
	require.NoError(t, os.WriteFile(file, []byte("syntax = \"proto3\";"), 0o600))

	assert.NoError(t, RequireRegularFile(file))
	assert.Error(t, RequireRegularFile(root))
	assert.Error(t, RequireRegularFile(filepath.Join(root, "missing.proto")))
}

func TestEnsureDirectoryAbsent(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))

	assert.NoError(t, EnsureDirectoryAbsent(filepath.Join(root, "missing")))
	assert.NoError(t, EnsureDirectoryAbsent(file))
	assert.Error(t, EnsureDirectoryAbsent(root))
}
