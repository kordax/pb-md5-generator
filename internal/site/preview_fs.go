package site

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"sync"
)

// CompilePreviewFSContext compiles from a read-only, immutable project filesystem.
// The filesystem is rooted at the manifest directory and must confine all reads,
// reject symlinks and special files, and remain valid for the entire call. CanRead
// is checked before opening dependencies. Callers must authorize the primary
// source before reading and passing its bytes, just as with physical previews.
func CompilePreviewFSContext(ctx context.Context, sourceFS fs.FS, manifestName string, page Page, source []byte, options PreviewOptions) (PreviewResult, error) {
	if ctx == nil {
		return PreviewResult{}, fmt.Errorf("preview context is required")
	}
	if err := ctx.Err(); err != nil {
		return PreviewResult{}, err
	}
	canRead := options.CanRead
	if canRead == nil {
		canRead = func(string) bool { return true }
	}
	if !localPath(page.Source) || !canRead(page.Source) {
		return PreviewResult{}, ErrPreviewAccess
	}
	manifest, err := ReadManifestFS(ctx, sourceFS, manifestName)
	if err != nil {
		return PreviewResult{}, err
	}
	return compilePreviewLoaded(ctx, manifest, page, source, options, canRead, previewFSSourceLoader(ctx, sourceFS, page.Source, source))
}

// ReadManifestFS decodes the same bounded authoring manifest as ReadManifest.
// The filesystem must provide contained, read-only regular files. Implementing
// fs.StatFS keeps the pre-open regular-file and size check metadata-only.
func ReadManifestFS(ctx context.Context, sourceFS fs.FS, name string) (Manifest, error) {
	data, err := readPreviewFS(ctx, sourceFS, name, maxManifestBytes)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	return DecodeManifest(data)
}

func readPreviewFS(ctx context.Context, sourceFS fs.FS, name string, limit int64) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("preview context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sourceFS == nil {
		return nil, fmt.Errorf("preview source filesystem is required")
	}
	if !localPath(name) || !fs.ValidPath(name) || limit < 0 {
		return nil, fmt.Errorf("preview source must be a clean project-relative path")
	}
	info, err := fs.Stat(sourceFS, name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular file of at most %d bytes", name, limit)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := sourceFS.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular file of at most %d bytes", name, limit)
	}
	data, err := io.ReadAll(io.LimitReader(previewContextReader{ctx: ctx, reader: file}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	return data, nil
}

type previewContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r previewContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(data)
	if canceled := r.ctx.Err(); canceled != nil {
		return 0, canceled
	}
	return n, err
}

func previewFSSourceLoader(ctx context.Context, sourceFS fs.FS, primary string, source []byte) func(string) ([]byte, error) {
	loaded := map[string][]byte{primary: source}
	total := len(source)
	var mutex sync.Mutex
	return func(name string) ([]byte, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !localPath(name) {
			return nil, fmt.Errorf("import source must be a clean project-relative path")
		}
		if data, exists := loaded[name]; exists {
			return data, nil
		}
		if len(loaded) >= maxImportedDocuments {
			return nil, fmt.Errorf("import exceeds %d documents", maxImportedDocuments)
		}
		data, err := readPreviewFS(ctx, sourceFS, name, min(int64(maxSourceBytes), int64(maxImportedBytes-total)))
		if err != nil {
			return nil, fmt.Errorf("import source %s: %w", name, err)
		}
		total += len(data)
		loaded[name] = data
		return data, nil
	}
}
