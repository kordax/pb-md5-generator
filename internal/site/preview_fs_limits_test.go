package site

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type previewFileInfo struct {
	size int64
	mode fs.FileMode
}

func (i previewFileInfo) Name() string       { return "source.md" }
func (i previewFileInfo) Size() int64        { return i.size }
func (i previewFileInfo) Mode() fs.FileMode  { return i.mode }
func (i previewFileInfo) ModTime() time.Time { return time.Time{} }
func (i previewFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i previewFileInfo) Sys() any           { return nil }

type previewBoundFS struct {
	before, after                          previewFileInfo
	data                                   []byte
	statErr, openErr, fileStatErr, readErr error
	onStat, onRead                         func()
	stats, opens, closes, readBytes        int
}

func (s *previewBoundFS) Stat(string) (fs.FileInfo, error) {
	s.stats++
	if s.onStat != nil {
		s.onStat()
	}
	return s.before, s.statErr
}

func (s *previewBoundFS) Open(string) (fs.File, error) {
	s.opens++
	if s.openErr != nil {
		return nil, s.openErr
	}
	return &previewBoundFile{Reader: bytes.NewReader(s.data), owner: s}, nil
}

type previewBoundFile struct {
	*bytes.Reader
	owner *previewBoundFS
}

func (f *previewBoundFile) Stat() (fs.FileInfo, error) { return f.owner.after, f.owner.fileStatErr }
func (f *previewBoundFile) Close() error               { f.owner.closes++; return nil }
func (f *previewBoundFile) Read(data []byte) (int, error) {
	if f.owner.readErr != nil {
		return 0, f.owner.readErr
	}
	n, err := f.Reader.Read(data)
	f.owner.readBytes += n
	if f.owner.onRead != nil {
		f.owner.onRead()
	}
	return n, err
}

func TestPreviewFSBoundedReadRejectsSpecialOversizedAndChangingFiles(t *testing.T) {
	sentinel := errors.New("filesystem failure")
	for _, item := range []struct {
		name                          string
		configure                     func(*previewBoundFS)
		expectedOpens, expectedCloses int
	}{
		{name: "directory", configure: func(s *previewBoundFS) { s.before.mode = fs.ModeDir }},
		{name: "pipe", configure: func(s *previewBoundFS) { s.before.mode = fs.ModeNamedPipe }},
		{name: "symlink", configure: func(s *previewBoundFS) { s.before.mode = fs.ModeSymlink }},
		{name: "size before open", configure: func(s *previewBoundFS) { s.before.size = 5 }},
		{name: "negative size", configure: func(s *previewBoundFS) { s.before.size = -1 }},
		{name: "stat failed", configure: func(s *previewBoundFS) { s.statErr = sentinel }},
		{name: "open failed", configure: func(s *previewBoundFS) { s.openErr = sentinel }, expectedOpens: 1},
		{name: "file stat failed", configure: func(s *previewBoundFS) { s.fileStatErr = sentinel }, expectedOpens: 1, expectedCloses: 1},
		{name: "type changed", configure: func(s *previewBoundFS) { s.after.mode = fs.ModeDir }, expectedOpens: 1, expectedCloses: 1},
		{name: "size changed", configure: func(s *previewBoundFS) { s.after.size = 5 }, expectedOpens: 1, expectedCloses: 1},
		{name: "negative file size", configure: func(s *previewBoundFS) { s.after.size = -1 }, expectedOpens: 1, expectedCloses: 1},
		{name: "read failed", configure: func(s *previewBoundFS) { s.readErr = sentinel }, expectedOpens: 1, expectedCloses: 1},
	} {
		t.Run(item.name, func(t *testing.T) {
			source := &previewBoundFS{before: previewFileInfo{size: 2}, after: previewFileInfo{size: 2}, data: []byte("ok")}
			item.configure(source)
			_, err := readPreviewFS(t.Context(), source, "source.md", 4)
			require.Error(t, err)
			if source.statErr != nil || source.openErr != nil || source.fileStatErr != nil || source.readErr != nil {
				assert.ErrorIs(t, err, sentinel)
			}
			assert.Equal(t, item.expectedOpens, source.opens)
			assert.Equal(t, item.expectedCloses, source.closes)
			assert.Zero(t, source.readBytes)
		})
	}
	source := &previewBoundFS{before: previewFileInfo{size: 2}, after: previewFileInfo{size: 2}, data: bytes.Repeat([]byte("x"), 100)}
	_, err := readPreviewFS(t.Context(), source, "source.md", 4)
	require.ErrorContains(t, err, "exceeds 4 bytes")
	assert.Equal(t, 5, source.readBytes)
	assert.Equal(t, 1, source.closes)
}

func TestPreviewFSBoundedReadCancellationAndPathChecks(t *testing.T) {
	for _, phase := range []string{"before", "stat", "read"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source := &previewBoundFS{before: previewFileInfo{size: 2}, after: previewFileInfo{size: 2}, data: []byte("ok")}
			switch phase {
			case "before":
				cancel()
			case "stat":
				source.onStat = cancel
			case "read":
				source.onRead = cancel
			}
			_, err := readPreviewFS(ctx, source, "source.md", 4)
			require.ErrorIs(t, err, context.Canceled)
			if phase == "read" {
				assert.Equal(t, 1, source.closes)
			} else {
				assert.Zero(t, source.opens)
			}
		})
	}
	source := &previewBoundFS{}
	for _, name := range []string{"../escape.md", "/escape.md", ".", "dir//source.md", "dir\\source.md"} {
		_, err := readPreviewFS(t.Context(), source, name, 4)
		assert.Error(t, err, name)
	}
	_, err := readPreviewFS(t.Context(), source, "source.md", -1)
	assert.Error(t, err)
	var missingContext context.Context
	_, err = readPreviewFS(missingContext, source, "source.md", 4)
	assert.Error(t, err)
	assert.Zero(t, source.stats)
}

func TestPreviewFSImportCacheIsConcurrentAndDoesNotOpenThePrimarySource(t *testing.T) {
	spy := newPreviewSpyFS(fstest.MapFS{"dependency.md": {Data: []byte("Shared dependency")}})
	load := previewFSSourceLoader(t.Context(), spy, "primary.md", []byte("Unsaved primary"))
	primary, err := load("primary.md")
	require.NoError(t, err)
	assert.Equal(t, "Unsaved primary", string(primary))
	assert.Empty(t, spy.opens)
	var group sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		group.Go(func() {
			data, err := load("dependency.md")
			if err == nil && string(data) != "Shared dependency" {
				err = fmt.Errorf("unexpected dependency bytes")
			}
			results <- err
		})
	}
	group.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	assert.Equal(t, map[string]int{"dependency.md": 1}, spy.opens)
	assert.Equal(t, map[string]int{"dependency.md": 1}, spy.stats)
}

func TestPreviewFSImportBudgetsCountUniqueReadsAndThePrimaryBytes(t *testing.T) {
	files := fstest.MapFS{}
	for i := range maxImportedDocuments {
		files[fmt.Sprintf("source-%d.md", i)] = &fstest.MapFile{Data: []byte("x")}
	}
	spy := newPreviewSpyFS(files)
	load := previewFSSourceLoader(t.Context(), spy, "primary.md", []byte("x"))
	for i := range maxImportedDocuments - 1 {
		_, err := load(fmt.Sprintf("source-%d.md", i))
		require.NoError(t, err)
	}
	_, err := load(fmt.Sprintf("source-%d.md", maxImportedDocuments-1))
	require.ErrorContains(t, err, "documents")
	assert.Len(t, spy.opens, maxImportedDocuments-1)
	_, err = load("../escape.md")
	assert.ErrorContains(t, err, "clean project-relative")

	large := bytes.Repeat([]byte("x"), maxSourceBytes)
	files = fstest.MapFS{"overflow.md": {Data: []byte("x")}}
	remaining := maxImportedBytes - len(large)
	for i := 0; remaining > 0; i++ {
		size := min(len(large), remaining)
		files[fmt.Sprintf("large-%d.md", i)] = &fstest.MapFile{Data: large[:size]}
		remaining -= size
	}
	spy = newPreviewSpyFS(files)
	load = previewFSSourceLoader(t.Context(), spy, "primary.md", large)
	for i := 0; i < len(files)-1; i++ {
		_, err := load(fmt.Sprintf("large-%d.md", i))
		require.NoError(t, err)
	}
	_, err = load("overflow.md")
	require.ErrorContains(t, err, "at most 0 bytes")
	assert.NotContains(t, spy.opens, "overflow.md")
	cached, err := load("primary.md")
	require.NoError(t, err)
	assert.Equal(t, len(large), len(cached))

	ctx, cancel := context.WithCancel(t.Context())
	load = previewFSSourceLoader(ctx, spy, "primary.md", nil)
	cancel()
	_, err = load("primary.md")
	assert.ErrorIs(t, err, context.Canceled)
}
