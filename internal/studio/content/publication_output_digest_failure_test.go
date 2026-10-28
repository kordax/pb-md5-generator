package content

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func publicationDigestFailureFixture(t *testing.T) (string, string) {
	t.Helper()
	service, config := managedFixture(t)
	var bitmap bytes.Buffer
	require.NoError(t, png.Encode(&bitmap, image.NewGray(image.Rect(0, 0, 1, 1))))
	const assetName = "digest-image.png"
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, assetName), bitmap.Bytes(), 0o600))
	manifestPath := filepath.Join(config.Root, "docs.json")
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(data, &manifest))
	manifest["assets"] = []string{assetName}
	data, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, data, 0o600))
	publishDraft(t, service, saveText(t, service, "index.md", "# Output digest I/O fixture\n"))
	asset := filepath.Join(config.OutputDir, "assets", assetName)
	require.FileExists(t, asset)
	return config.OutputDir, asset
}

func TestPublicationOutputDigestFailurePermissions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires enforced permissions for an unprivileged file owner")
	}
	for _, test := range []struct {
		name      string
		directory bool
		mode      os.FileMode
	}{
		{"unreadable PNG", false, 0},
		{"unreadable nested directory", true, 0},
		{"nested directory without search", true, 0o400},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, asset := publicationDigestFailureFixture(t)
			original, err := publicationOutputDigest(t.Context(), output, false)
			require.NoError(t, err)
			target := asset
			if test.directory {
				target = filepath.Dir(asset)
			}
			info, err := os.Lstat(target)
			require.NoError(t, err)
			mode := info.Mode().Perm()
			t.Cleanup(func() { assert.NoError(t, os.Chmod(target, mode)) })
			require.NoError(t, os.Chmod(target, test.mode))
			probe, permissionErr := os.Open(asset)
			if probe != nil {
				require.NoError(t, probe.Close())
			}
			require.ErrorIs(t, permissionErr, os.ErrPermission)
			actual, digestErr := publicationOutputDigest(t.Context(), output, false)
			require.NoError(t, os.Chmod(target, mode))
			assert.ErrorIs(t, digestErr, os.ErrPermission)
			assert.Empty(t, actual, "an incomplete traversal must not produce an authority digest")
			restored, err := publicationOutputDigest(t.Context(), output, false)
			require.NoError(t, err)
			assert.Equal(t, original, restored, "permission-only repair must preserve every output byte")
			current, err := os.Lstat(target)
			require.NoError(t, err)
			assert.True(t, os.SameFile(info, current))
			assert.Equal(t, mode, current.Mode().Perm())
		})
	}
}

func TestPublicationOutputDigestFailureInvalidRoots(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing-output")
	digest, err := publicationOutputDigest(t.Context(), missing, true)
	assert.ErrorIs(t, err, ErrOwnership)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Empty(t, digest)
	_, err = os.Lstat(missing)
	assert.True(t, os.IsNotExist(err), "a missing root must not be initialized by a digest read")
	file := filepath.Join(root, "file-instead-of-output")
	marker := []byte("independent non-directory output evidence\n")
	require.NoError(t, os.WriteFile(file, marker, 0o600))
	info, err := os.Lstat(file)
	require.NoError(t, err)
	digest, err = publicationOutputDigest(t.Context(), file, true)
	assert.ErrorIs(t, err, ErrOwnership)
	assert.Empty(t, digest)
	current, err := os.Lstat(file)
	require.NoError(t, err)
	assert.True(t, os.SameFile(info, current))
	assert.Equal(t, info.ModTime(), current.ModTime())
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(marker, data))
}

func TestPublicationOutputDigestFailureEntryBudget(t *testing.T) {
	output, _ := publicationDigestFailureFixture(t)
	original, err := publicationOutputDigest(t.Context(), output, false)
	require.NoError(t, err)
	directory := filepath.Join(output, "digest-entry-budget")
	require.NoError(t, os.Mkdir(directory, 0o700))
	count := 4*MaxFiles + 1
	for index := 0; index < count; index++ {
		name := filepath.Join(directory, fmt.Sprintf("entry-%05d", index))
		require.NoError(t, os.WriteFile(name, nil, 0o600))
	}
	digest, err := publicationOutputDigest(t.Context(), output, false)
	assert.ErrorIs(t, err, ErrLimit)
	assert.Empty(t, digest)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	assert.Len(t, entries, count, "the bounded read must not delete excess namespace entries")
	for index := 0; index < count; index++ {
		name := filepath.Join(directory, fmt.Sprintf("entry-%05d", index))
		require.NoError(t, os.Remove(name))
	}
	require.NoError(t, os.Remove(directory))
	restored, err := publicationOutputDigest(t.Context(), output, false)
	require.NoError(t, err)
	assert.Equal(t, original, restored, "removing only the injected empty files must restore the original site proof")
}

func TestPublicationOutputDigestFailureCanceledReaderDoesNotConsumeInput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	data := []byte("underlying reader remains available\n")
	underlying := bytes.NewReader(data)
	reader := publicationReader{ctx: ctx, r: underlying}
	buffer := []byte("unchanged")
	read, err := reader.Read(buffer)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, read)
	assert.Equal(t, len(data), underlying.Len())
	assert.Equal(t, "unchanged", string(buffer))
}
