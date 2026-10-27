//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package auth

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ownerTestInfo struct {
	os.FileInfo
	metadata any
}

func (info ownerTestInfo) Sys() any { return info.metadata }

func TestPrivateStateOwnershipMatchesEffectiveUser(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	require.NoError(t, err)
	assert.True(t, ownedByCurrentUser(info))
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	other := *stat
	other.Uid ^= 1
	assert.False(t, ownedByCurrentUser(ownerTestInfo{FileInfo: info, metadata: &other}))
	assert.False(t, ownedByCurrentUser(ownerTestInfo{FileInfo: info, metadata: nil}))
	assert.False(t, ownedByCurrentUser(ownerTestInfo{FileInfo: info, metadata: (*syscall.Stat_t)(nil)}))
}

func TestUnixAuthenticationLockCreatesPrivateFileAndReleasesExclusivity(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private auth state")
	require.NoError(t, os.Mkdir(directory, 0o700))
	name := filepath.Join(directory, ".lock")
	first, err := acquireLock(name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	info, err := first.Stat()
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.True(t, ownedByCurrentUser(info))
	second, err := acquireLock(name)
	require.ErrorContains(t, err, "already owned by another process")
	assert.Nil(t, second)
	require.NoError(t, first.Close())
	third, err := acquireLock(name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = third.Close() })
	current, err := third.Stat()
	require.NoError(t, err)
	assert.True(t, os.SameFile(info, current), "reopening retains the same lock inode")
}

func TestUnixAuthenticationLockRejectsSymlinksAndNonregularFiles(t *testing.T) {
	for _, kind := range []string{"directory", "fifo", "internal symlink", "external symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			name := filepath.Join(directory, ".lock")
			switch kind {
			case "directory":
				require.NoError(t, os.Mkdir(name, 0o700))
			case "fifo":
				require.NoError(t, syscall.Mkfifo(name, 0o600))
			case "internal symlink":
				require.NoError(t, os.WriteFile(filepath.Join(directory, "target"), []byte("retained"), 0o600))
				require.NoError(t, os.Symlink("target", name))
			case "external symlink":
				target := filepath.Join(t.TempDir(), "target")
				require.NoError(t, os.WriteFile(target, []byte("retained"), 0o600))
				require.NoError(t, os.Symlink(target, name))
			case "dangling symlink":
				require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "absent"), name))
			}
			file, err := acquireLock(name)
			require.ErrorContains(t, err, "must be a regular file")
			assert.Nil(t, file)
		})
	}
}

func TestUnixAuthenticationLockRejectsPublicModeAndUnavailableParent(t *testing.T) {
	name := filepath.Join(t.TempDir(), ".lock")
	require.NoError(t, os.WriteFile(name, []byte("retained lock data"), 0o600))
	require.NoError(t, os.Chmod(name, 0o644))
	file, err := acquireLock(name)
	require.ErrorContains(t, err, "must be private")
	assert.Nil(t, file)
	require.NoError(t, os.Chmod(name, 0o600))
	file, err = acquireLock(name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	data, err := os.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, "retained lock data", string(data))
	for _, unavailable := range []string{filepath.Join(t.TempDir(), "missing", ".lock"), filepath.Join(name, ".lock")} {
		file, err := acquireLock(unavailable)
		require.ErrorContains(t, err, "open authentication lock directory")
		assert.Nil(t, file)
	}
}
