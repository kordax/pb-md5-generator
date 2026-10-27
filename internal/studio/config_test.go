package studio

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCMSDefaultsKeepDataSeparateAndDoNotCreateIt(t *testing.T) {
	data := filepath.Join(t.TempDir(), "cms")
	config, err := prepareConfig(DefaultConfig(data))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:8090", config.Address)
	assert.Equal(t, "http://localhost:8090", config.PublicURL)
	assert.Equal(t, "local", config.Backend)
	assert.Equal(t, "origin", config.GitRemote)
	assert.True(t, config.Initialize)
	assert.Equal(t, filepath.Join(data, "content"), config.contentDir)
	assert.Equal(t, filepath.Join(data, "state"), config.stateDir)
	assert.Equal(t, filepath.Join(data, "site"), config.outputDir)
	_, err = os.Stat(data)
	assert.ErrorIs(t, err, os.ErrNotExist)
	defaultPath, err := prepareConfig(DefaultConfig(""))
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(defaultPath.DataDir))
	assert.Equal(t, "pb-docs", filepath.Base(defaultPath.DataDir))
}

func TestCMSOriginAndHostingPrefix(t *testing.T) {
	config, err := prepareConfig(Config{DataDir: t.TempDir(), Address: "0.0.0.0:8080", PublicURL: "HTTPS://DOCS.EXAMPLE.TEST/team/product/", Backend: "git"})
	require.NoError(t, err)
	assert.Equal(t, "https://docs.example.test/team/product", config.PublicURL)
	assert.Equal(t, "/team/product", config.basePath)
	assert.Equal(t, "git", config.Backend)
	local, err := prepareConfig(Config{DataDir: t.TempDir(), PublicURL: "http://LOCALHOST:8090/"})
	require.NoError(t, err)
	assert.Empty(t, local.basePath)
	assert.Equal(t, "http://localhost:8090", local.PublicURL)
	for _, value := range []string{
		"http://127.0.0.1:8090", "http://[::1]:8090", "http://docs.example.test", "ftp://docs.example.test", "https:///missing-host", "https://docs.example.test:0", "https://docs.example.test:65536", "https://docs.example.test:bad", "https://docs.example.test/?", "https://docs.example.test/?query=value", "https://docs.example.test/#fragment", "https://docs.example.test/a/../b", "https://docs.example.test/a%2fb", "https://docs.example.test/path%20space", "https://docs.example.test/文档", "https://operator@example.test/", ":bad",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := prepareConfig(Config{DataDir: t.TempDir(), PublicURL: value})
			require.ErrorIs(t, err, ErrInvalidConfig)
		})
	}
	for _, address := range []string{"no-port", ":0", "127.0.0.1:99999", "127.0.0.1:wrong"} {
		_, err := prepareConfig(Config{DataDir: t.TempDir(), Address: address})
		require.ErrorIs(t, err, ErrInvalidConfig)
	}
	_, err = prepareConfig(Config{DataDir: t.TempDir(), Backend: "unrecognized"})
	require.ErrorIs(t, err, ErrInvalidConfig)
	_, err = prepareConfig(Config{DataDir: string(filepath.Separator)})
	require.ErrorIs(t, err, ErrInvalidConfig)
	_, err = prepareConfig(Config{DataDir: "bad\x00path"})
	require.ErrorIs(t, err, ErrInvalidConfig)
}

func TestCMSCustomListenerNeedsItsPublicURL(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, listener.Close()) })
	_, err = prepareConfig(Config{DataDir: t.TempDir(), Listener: listener})
	require.ErrorIs(t, err, ErrInvalidConfig)
	config, err := prepareConfig(Config{DataDir: t.TempDir(), Listener: listener, PublicURL: "https://docs.example.test"})
	require.NoError(t, err)
	assert.Equal(t, listener.Addr().String(), config.Address)
}

func TestCMSPrivateDirectoryNeverChangesExistingPermissions(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	require.NoError(t, ensurePrivateDirectory(private))
	require.NoError(t, ensurePrivateDirectory(private))
	info, err := os.Stat(private)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
		public := filepath.Join(root, "shared")
		require.NoError(t, os.Mkdir(public, 0o755))
		require.NoError(t, os.Chmod(public, 0o755))
		require.ErrorIs(t, ensurePrivateDirectory(public), ErrInvalidConfig)
		after, statErr := os.Stat(public)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o755), after.Mode().Perm())
		link := filepath.Join(root, "link")
		require.NoError(t, os.Symlink(private, link))
		require.ErrorIs(t, ensurePrivateDirectory(link), ErrInvalidConfig)
	}
	file := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(file, []byte("not a directory"), 0o600))
	require.Error(t, ensurePrivateDirectory(file))
	require.Error(t, ensurePrivateDirectory(filepath.Join(file, "child")))
}
