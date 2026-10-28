package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ReadSnapshot is a managed, read-only source view. Manifest is relative to FS;
// Revision belongs to the same serialized view. Filesystem methods and open
// handles are valid only during the ReadView callback. Returned byte copies may
// be retained, but callers must buffer their response until ReadView succeeds.
type ReadSnapshot struct {
	FS       fs.FS
	Manifest string
	Revision string
}

// ReadView holds the operation gate while supplying lazily loaded, hash-checked
// source bytes. Unlike View it creates no writable snapshot or temporary files.
// The callback must not re-enter this Service. A final uncached scan rejects
// external source or storage changes before the caller publishes its response.
func (s *Service) ReadView(ctx context.Context, visit func(context.Context, ReadSnapshot) error) error {
	if visit == nil {
		return ErrInvalid
	}
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return err
	}
	defer done()
	rootInfo, err := os.Lstat(s.cfg.Root)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ErrInvalid
	}
	project, tree, err := s.project(ctx)
	if err != nil {
		return err
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return err
	}
	identity := storageIdentity(status)
	view, err := newReadViewFS(ctx, s.cfg.Root, rootInfo, tree)
	if err != nil {
		return err
	}
	defer view.close()
	err = visit(ctx, ReadSnapshot{FS: view, Manifest: "docs.json", Revision: project.Revision})
	view.close()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := readViewRootUnchanged(s.cfg.Root, rootInfo); err != nil {
		return err
	}
	verified, err := scanTree(ctx, s.cfg.Root, identity, "")
	if err != nil {
		return err
	}
	current, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return err
	}
	if storageIdentity(current) != identity || verified.Revision != project.Revision {
		return ErrConflict
	}
	if err := readViewRootUnchanged(s.cfg.Root, rootInfo); err != nil {
		return err
	}
	s.cached = verified
	return ctx.Err()
}

type readViewInfo struct {
	name      string
	size      int64
	modified  time.Time
	directory bool
}

func (info readViewInfo) Name() string       { return info.name }
func (info readViewInfo) Size() int64        { return info.size }
func (info readViewInfo) ModTime() time.Time { return info.modified }
func (info readViewInfo) IsDir() bool        { return info.directory }
func (info readViewInfo) Sys() any           { return nil }
func (info readViewInfo) Mode() fs.FileMode {
	if info.directory {
		return fs.ModeDir | 0o555
	}
	return 0o444
}

type readViewFS struct {
	mu        sync.RWMutex
	ctx       context.Context
	directory string
	rootInfo  fs.FileInfo
	files     map[string]fileState
	nodes     map[string]readViewInfo
	children  map[string][]fs.DirEntry
	closed    bool
}

func newReadViewFS(ctx context.Context, directory string, rootInfo fs.FileInfo, tree sourceTree) (*readViewFS, error) {
	if len(tree.Files)+len(tree.Dirs) > MaxFiles {
		return nil, ErrLimit
	}
	view := &readViewFS{ctx: ctx, directory: directory, rootInfo: rootInfo, files: tree.Files, nodes: make(map[string]readViewInfo), children: make(map[string][]fs.DirEntry)}
	view.nodes["."] = readViewInfo{name: ".", directory: true, modified: rootInfo.ModTime()}
	for _, name := range tree.Dirs {
		view.nodes[name] = readViewInfo{name: path.Base(name), directory: true}
	}
	for name, state := range tree.Files {
		info := readViewInfo{name: path.Base(name), size: state.Size}
		if state.Info != nil {
			info.modified = state.Info.ModTime()
		}
		view.nodes[name] = info
	}
	for name, info := range view.nodes {
		if name != "." {
			parent := path.Dir(name)
			view.children[parent] = append(view.children[parent], fs.FileInfoToDirEntry(info))
		}
	}
	for _, entries := range view.children {
		slices.SortFunc(entries, func(left, right fs.DirEntry) int { return strings.Compare(left.Name(), right.Name()) })
	}
	return view, nil
}

func readViewRootUnchanged(directory string, before fs.FileInfo) error {
	current, err := os.Lstat(directory)
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, current) {
		return ErrConflict
	}
	return nil
}

func readViewError(operation, name string, err error) error {
	var fileError *fs.PathError
	if errors.As(err, &fileError) {
		err = fileError.Err
	}
	return &fs.PathError{Op: operation, Path: name, Err: err}
}

// check is called with view.mu held. Inventory, not the live directory, decides
// which paths exist; this never grants access to hidden or unmanaged files.
func (view *readViewFS) check(name string) error {
	if view.closed {
		return fs.ErrClosed
	}
	if err := view.ctx.Err(); err != nil {
		return err
	}
	if name != "." && !validPath(name) {
		return fs.ErrInvalid
	}
	if _, exists := view.nodes[name]; !exists {
		return fs.ErrNotExist
	}
	return nil
}

// readFile is called with view.mu held. No payload is cached in the filesystem;
// every reader owns its bytes and the compiler keeps its existing page budgets.
func (view *readViewFS) readFile(name string) ([]byte, error) {
	state, exists := view.files[name]
	if !exists {
		return nil, fs.ErrInvalid
	}
	if err := readViewRootUnchanged(view.directory, view.rootInfo); err != nil {
		return nil, err
	}
	limit := int64(MaxTextBytes)
	if binaryFile(name) {
		limit = MaxBinaryBytes
	}
	if name == "docs.json" {
		limit = 1 << 20
	}
	data, err := readSource(view.directory, name, limit)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != state.Size || hex.EncodeToString(digest[:]) != state.Hash {
		return nil, ErrConflict
	}
	if !binaryFile(name) && !utf8.Valid(data) {
		return nil, ErrInvalid
	}
	if err := view.ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func (view *readViewFS) Open(name string) (fs.File, error) {
	view.mu.RLock()
	defer view.mu.RUnlock()
	if err := view.check(name); err != nil {
		return nil, readViewError("open", name, err)
	}
	info := view.nodes[name]
	file := &readViewFile{view: view, name: name, info: info}
	if info.directory {
		file.entries = append([]fs.DirEntry(nil), view.children[name]...)
	} else {
		data, err := view.readFile(name)
		if err != nil {
			return nil, readViewError("open", name, err)
		}
		file.reader.Reset(data)
	}
	return file, nil
}

func (view *readViewFS) ReadFile(name string) ([]byte, error) {
	view.mu.RLock()
	defer view.mu.RUnlock()
	if err := view.check(name); err != nil {
		return nil, readViewError("read", name, err)
	}
	data, err := view.readFile(name)
	if err != nil {
		return nil, readViewError("read", name, err)
	}
	return data, nil
}

func (view *readViewFS) Stat(name string) (fs.FileInfo, error) {
	view.mu.RLock()
	defer view.mu.RUnlock()
	if err := view.check(name); err != nil {
		return nil, readViewError("stat", name, err)
	}
	return view.nodes[name], nil
}

func (view *readViewFS) ReadDir(name string) ([]fs.DirEntry, error) {
	view.mu.RLock()
	defer view.mu.RUnlock()
	if err := view.check(name); err != nil {
		return nil, readViewError("readdir", name, err)
	}
	if !view.nodes[name].directory {
		return nil, readViewError("readdir", name, fs.ErrInvalid)
	}
	return append([]fs.DirEntry{}, view.children[name]...), nil
}

func (view *readViewFS) close() {
	view.mu.Lock()
	defer view.mu.Unlock()
	view.closed = true
	view.ctx, view.rootInfo, view.files, view.nodes, view.children = nil, nil, nil, nil, nil
	view.directory = ""
}

type readViewFile struct {
	mu      sync.Mutex
	view    *readViewFS
	name    string
	info    readViewInfo
	reader  bytes.Reader
	entries []fs.DirEntry
	offset  int
	closed  bool
}

func (file *readViewFile) check() error {
	if file.closed || file.view.closed {
		return fs.ErrClosed
	}
	return file.view.ctx.Err()
}

func (file *readViewFile) Read(data []byte) (int, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	file.view.mu.RLock()
	defer file.view.mu.RUnlock()
	if err := file.check(); err != nil {
		return 0, readViewError("read", file.name, err)
	}
	if file.info.directory {
		return 0, readViewError("read", file.name, fs.ErrInvalid)
	}
	return file.reader.Read(data)
}

func (file *readViewFile) Stat() (fs.FileInfo, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	file.view.mu.RLock()
	defer file.view.mu.RUnlock()
	if err := file.check(); err != nil {
		return nil, readViewError("stat", file.name, err)
	}
	return file.info, nil
}

func (file *readViewFile) ReadDir(count int) ([]fs.DirEntry, error) {
	file.mu.Lock()
	defer file.mu.Unlock()
	file.view.mu.RLock()
	defer file.view.mu.RUnlock()
	if err := file.check(); err != nil {
		return nil, readViewError("readdir", file.name, err)
	}
	if !file.info.directory {
		return nil, readViewError("readdir", file.name, fs.ErrInvalid)
	}
	if count > 0 && file.offset == len(file.entries) {
		return nil, io.EOF
	}
	end := len(file.entries)
	if count > 0 && count < end-file.offset {
		end = file.offset + count
	}
	entries := append([]fs.DirEntry{}, file.entries[file.offset:end]...)
	file.offset = end
	return entries, nil
}

func (file *readViewFile) Close() error {
	file.mu.Lock()
	defer file.mu.Unlock()
	file.closed = true
	file.reader.Reset(nil)
	file.entries = nil
	return nil
}
