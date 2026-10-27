//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package content

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func confinedLockFixtureFile(t *testing.T, directory, name, content string, mode os.FileMode) {
	t.Helper()
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer func() { require.NoError(t, root.Close()) }()
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	require.NoError(t, err)
	_, err = file.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

func TestConfinedUnixLockRetainsLifetimeAfterDirectoryClose(t *testing.T) {
	directory := t.TempDir()
	name := filepath.Join(directory, ".studio-lock")
	first, err := lockRootFile(name)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	info, err := first.Stat()
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	second, err := lockRootFile(name)
	assert.Nil(t, second)
	require.ErrorIs(t, err, ErrConflict)
	require.NoError(t, first.Close())
	third, err := lockRootFile(name)
	require.NoError(t, err)
	require.NoError(t, third.Close())
}

func TestConfinedUnixLockRejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	directory := t.TempDir()
	confinedLockFixtureFile(t, directory, "target", "preserve this file", 0o600)
	require.NoError(t, os.Symlink(filepath.Join(directory, "target"), filepath.Join(directory, ".studio-lock")))
	file, err := lockRootFile(filepath.Join(directory, ".studio-lock"))
	assert.Nil(t, file)
	require.Error(t, err)
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer func() { require.NoError(t, root.Close()) }()
	data, err := root.ReadFile("target")
	require.NoError(t, err)
	assert.Equal(t, "preserve this file", string(data))
}

func TestConfinedUnixLockRejectsNonPrivateFilesAndPipes(t *testing.T) {
	t.Run("permissions", func(t *testing.T) {
		directory := t.TempDir()
		confinedLockFixtureFile(t, directory, ".studio-lock", "unchanged", 0o644)
		file, err := lockRootFile(filepath.Join(directory, ".studio-lock"))
		assert.Nil(t, file)
		assert.ErrorIs(t, err, ErrOwnership)
	})
	t.Run("FIFO", func(t *testing.T) {
		directory := t.TempDir()
		name := filepath.Join(directory, ".studio-lock")
		require.NoError(t, syscall.Mkfifo(name, 0o600))
		file, err := lockRootFile(name)
		assert.Nil(t, file)
		assert.ErrorIs(t, err, ErrOwnership)
	})
	t.Run("missing parent", func(t *testing.T) {
		file, err := lockRootFile(filepath.Join(t.TempDir(), "missing", ".studio-lock"))
		assert.Nil(t, file)
		assert.Error(t, err)
	})
}

func TestDevelopmentCheckoutGuardUsesBoundedConfinedModuleRead(t *testing.T) {
	t.Run("module checkout", func(t *testing.T) {
		directory := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(directory, "nested"), 0o700))
		confinedLockFixtureFile(t, directory, "go.mod", "module github.com/kordax/pb-md5-generator\n", 0o600)
		t.Chdir(filepath.Join(directory, "nested"))
		assert.ErrorIs(t, rejectDevelopmentCheckout(filepath.Join(directory, "managed")), ErrOwnership)
		assert.NoError(t, rejectDevelopmentCheckout(t.TempDir()))
	})
	t.Run("module symlink", func(t *testing.T) {
		directory, outside := t.TempDir(), t.TempDir()
		confinedLockFixtureFile(t, outside, "module", "module github.com/kordax/pb-md5-generator\n", 0o600)
		require.NoError(t, os.Symlink(filepath.Join(outside, "module"), filepath.Join(directory, "go.mod")))
		t.Chdir(directory)
		assert.ErrorIs(t, rejectDevelopmentCheckout(t.TempDir()), ErrOwnership)
	})
	t.Run("oversized module", func(t *testing.T) {
		directory := t.TempDir()
		confinedLockFixtureFile(t, directory, "go.mod", strings.Repeat("x", (1<<20)+1), 0o600)
		t.Chdir(directory)
		assert.ErrorIs(t, rejectDevelopmentCheckout(t.TempDir()), ErrOwnership)
	})
}
