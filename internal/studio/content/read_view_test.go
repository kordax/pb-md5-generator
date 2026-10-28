package content

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadViewProvidesManagedReadOnlyFilesystemAndPrivateByteCopies(t *testing.T) {
	service, config := managedFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(config.Root, "examples", "empty"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, "examples", "code.go"), []byte("package fixture\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, ".env"), []byte("private-value"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, "credentials.json"), []byte("private-value"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, "ignored.exe"), []byte("unmanaged-value"), 0o600))
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	before, err := os.Stat(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	type principalKey struct{}
	ctx := context.WithValue(context.Background(), principalKey{}, "current-principal")
	var retained fs.FS
	var lateFile fs.File
	var lateDirectory fs.ReadDirFile
	err = service.ReadView(ctx, func(viewContext context.Context, snapshot ReadSnapshot) error {
		assert.Equal(t, "current-principal", viewContext.Value(principalKey{}))
		assert.Equal(t, "docs.json", snapshot.Manifest)
		assert.Equal(t, project.Revision, snapshot.Revision)
		require.NoError(t, fstest.TestFS(snapshot.FS, "docs.json", "index.md", "examples/code.go"))
		for _, name := range []string{".env", "credentials.json", "ignored.exe", "missing.md"} {
			_, readErr := fs.ReadFile(snapshot.FS, name)
			assert.Error(t, readErr, name)
		}
		info, statErr := fs.Stat(snapshot.FS, "index.md")
		require.NoError(t, statErr)
		assert.Equal(t, "index.md", info.Name())
		assert.Equal(t, before.Size(), info.Size())
		assert.Equal(t, before.ModTime(), info.ModTime())
		assert.Zero(t, info.Mode()&0o222)
		assert.Nil(t, info.Sys())
		data, readErr := fs.ReadFile(snapshot.FS, "index.md")
		require.NoError(t, readErr)
		data[0] = '!'
		again, readErr := fs.ReadFile(snapshot.FS, "index.md")
		require.NoError(t, readErr)
		assert.Equal(t, "# Documentation\n", string(again))
		file, openErr := snapshot.FS.Open("index.md")
		require.NoError(t, openErr)
		_, writable := file.(io.Writer)
		assert.False(t, writable)
		_, physical := file.(*os.File)
		assert.False(t, physical)
		_, descriptor := file.(interface{ Fd() uintptr })
		assert.False(t, descriptor)
		_, named := file.(interface{ Name() string })
		assert.False(t, named)
		body, readErr := io.ReadAll(file)
		require.NoError(t, readErr)
		assert.Equal(t, "# Documentation\n", string(body))
		require.NoError(t, file.Close())
		_, readErr = file.Read(make([]byte, 1))
		assert.ErrorIs(t, readErr, fs.ErrClosed)
		_, statErr = file.Stat()
		assert.ErrorIs(t, statErr, fs.ErrClosed)
		retained = snapshot.FS
		lateFile, openErr = snapshot.FS.Open("index.md")
		require.NoError(t, openErr)
		directory, openErr := snapshot.FS.Open("examples")
		require.NoError(t, openErr)
		lateDirectory = directory.(fs.ReadDirFile)
		state, stateErr := os.ReadDir(config.StateDir)
		require.NoError(t, stateErr)
		for _, entry := range state {
			assert.False(t, strings.HasPrefix(entry.Name(), ".view-"))
		}
		return nil
	})
	require.NoError(t, err)
	_, err = fs.ReadFile(retained, "index.md")
	assert.ErrorIs(t, err, fs.ErrClosed)
	_, err = fs.Stat(retained, "index.md")
	assert.ErrorIs(t, err, fs.ErrClosed)
	_, err = fs.ReadDir(retained, ".")
	assert.ErrorIs(t, err, fs.ErrClosed)
	_, err = retained.Open("index.md")
	assert.ErrorIs(t, err, fs.ErrClosed)
	_, err = lateFile.Read(make([]byte, 1))
	assert.ErrorIs(t, err, fs.ErrClosed)
	_, err = lateFile.Stat()
	assert.ErrorIs(t, err, fs.ErrClosed)
	_, err = lateDirectory.ReadDir(1)
	assert.ErrorIs(t, err, fs.ErrClosed)
	require.NoError(t, lateFile.Close())
	require.NoError(t, lateDirectory.Close())
	after, err := os.Stat(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after))
	assert.Equal(t, before.ModTime(), after.ModTime())
	data, err := os.ReadFile(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Documentation\n", string(data))
}

func TestReadViewDirectoryMetadataAndInvalidPathsAreConsistent(t *testing.T) {
	service, config := managedFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(config.Root, "examples", "empty"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, "examples", "z.txt"), []byte("last"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, "examples", "a.txt"), []byte("first"), 0o600))
	err := service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
		for _, name := range []string{"", "/index.md", "../index.md", "examples/../index.md", "examples//a.txt", "index.md/", "examples\\a.txt"} {
			_, openErr := snapshot.FS.Open(name)
			assert.ErrorIs(t, openErr, fs.ErrInvalid, name)
		}
		entries, readErr := fs.ReadDir(snapshot.FS, "examples")
		require.NoError(t, readErr)
		require.Len(t, entries, 3)
		assert.Equal(t, []string{"a.txt", "empty", "z.txt"}, []string{entries[0].Name(), entries[1].Name(), entries[2].Name()})
		assert.True(t, entries[1].IsDir())
		assert.Equal(t, fs.ModeDir, entries[1].Type())
		info, statErr := entries[1].Info()
		require.NoError(t, statErr)
		assert.True(t, info.IsDir())
		assert.Zero(t, info.Size())
		assert.Zero(t, info.Mode()&0o222)
		entries[0] = entries[2]
		again, readErr := fs.ReadDir(snapshot.FS, "examples")
		require.NoError(t, readErr)
		assert.Equal(t, "a.txt", again[0].Name())
		empty, readErr := fs.ReadDir(snapshot.FS, "examples/empty")
		require.NoError(t, readErr)
		assert.Empty(t, empty)
		_, readErr = fs.ReadDir(snapshot.FS, "index.md")
		assert.ErrorIs(t, readErr, fs.ErrInvalid)
		_, readErr = fs.ReadFile(snapshot.FS, "examples")
		assert.ErrorIs(t, readErr, fs.ErrInvalid)
		file, openErr := snapshot.FS.Open("index.md")
		require.NoError(t, openErr)
		_, readErr = file.(fs.ReadDirFile).ReadDir(1)
		assert.ErrorIs(t, readErr, fs.ErrInvalid)
		require.NoError(t, file.Close())
		directory, openErr := snapshot.FS.Open("examples")
		require.NoError(t, openErr)
		_, readErr = directory.Read(make([]byte, 1))
		assert.ErrorIs(t, readErr, fs.ErrInvalid)
		listing := directory.(fs.ReadDirFile)
		first, readErr := listing.ReadDir(1)
		require.NoError(t, readErr)
		require.Len(t, first, 1)
		assert.Equal(t, "a.txt", first[0].Name())
		rest, readErr := listing.ReadDir(-1)
		require.NoError(t, readErr)
		assert.Len(t, rest, 2)
		_, readErr = listing.ReadDir(1)
		assert.ErrorIs(t, readErr, io.EOF)
		require.NoError(t, directory.Close())
		return nil
	})
	require.NoError(t, err)
}

func TestReadViewRequiresNoStateDirectoryOrPhysicalSnapshot(t *testing.T) {
	service, config := managedFixture(t)
	require.NoError(t, os.Rename(config.StateDir, config.StateDir+".offline"))
	t.Cleanup(func() { require.NoError(t, os.Rename(config.StateDir+".offline", config.StateDir)) })
	err := service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
		data, readErr := fs.ReadFile(snapshot.FS, "index.md")
		require.NoError(t, readErr)
		assert.Equal(t, "# Documentation\n", string(data))
		return nil
	})
	require.NoError(t, err)
	_, err = os.Stat(config.StateDir)
	assert.True(t, os.IsNotExist(err), "reads must not create private snapshot directories")
}

func TestReadViewSerializesQueuedWriterAndPreservesContextCancellation(t *testing.T) {
	service, _ := managedFixture(t)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
			close(entered)
			<-release
			_, readErr := fs.ReadFile(snapshot.FS, "index.md")
			return readErr
		})
	}()
	<-entered
	text := "# Queued owner text\n"
	waiting, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	_, err = service.SaveDraft(waiting, DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "index.md", Content: &text}}, editor)
	cancel()
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	require.NoError(t, <-result)
	drafts, err := service.Drafts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, drafts)
	_, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "index.md", Content: &text}}, editor)
	require.NoError(t, err)

	ctx, stop := context.WithCancel(context.Background())
	err = service.ReadView(ctx, func(_ context.Context, snapshot ReadSnapshot) error {
		file, openErr := snapshot.FS.Open("index.md")
		require.NoError(t, openErr)
		defer func() { require.NoError(t, file.Close()) }()
		stop()
		_, readErr := fs.ReadFile(snapshot.FS, "index.md")
		assert.ErrorIs(t, readErr, context.Canceled)
		_, readErr = file.Read(make([]byte, 1))
		assert.ErrorIs(t, readErr, context.Canceled)
		return nil
	})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestReadViewClosesFilesystemAfterCallbackErrorOrPanic(t *testing.T) {
	service, _ := managedFixture(t)
	failure := errors.New("rendering refused")
	var retained fs.FS
	err := service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
		retained = snapshot.FS
		return failure
	})
	assert.ErrorIs(t, err, failure)
	_, err = fs.ReadFile(retained, "index.md")
	assert.ErrorIs(t, err, fs.ErrClosed)
	require.Panics(t, func() {
		_ = service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
			retained = snapshot.FS
			panic("callback stopped")
		})
	})
	_, err = retained.Open("index.md")
	assert.ErrorIs(t, err, fs.ErrClosed)
	_, err = service.Project(context.Background())
	require.NoError(t, err, "panic must release the content gate")
	assert.ErrorIs(t, service.ReadView(context.Background(), nil), ErrInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err = service.ReadView(ctx, func(context.Context, ReadSnapshot) error { called = true; return nil })
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, called)
}
