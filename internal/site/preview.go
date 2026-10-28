package site

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/kordax/pb-md5-generator/document"
)

// ErrPreviewAccess deliberately does not identify an inaccessible source.
var ErrPreviewAccess = errors.New("preview source is unavailable")

// PreviewOptions applies the same compiler as publication with a source-level
// access check. A nil CanRead is suitable only for trusted local callers.
type PreviewOptions struct {
	Compiler Options
	CanRead  func(string) bool

	// ReaderStudioURL is a runtime-only link supplied by the live CMS reader.
	ReaderStudioURL string
}

// PageDataPath returns the portable model URL for a validated page identity.
func PageDataPath(page Page) string { return pageDataPath(page) }

// PreviewResult contains the same page model and HTML used by publication.
type PreviewResult struct {
	HTML     []byte
	Page     document.Page
	DataPath string
	Sources  []string
}

// PreviewContext renders an unsaved source without writing to the project or
// publication. Include and import reads are checked before touching the file.
// The manifest navigation and assets contain only sources visible to CanRead.
func PreviewContext(ctx context.Context, manifestPath string, page Page, source []byte, options PreviewOptions) ([]byte, error) {
	result, err := CompilePreviewContext(ctx, manifestPath, page, source, options)
	return result.HTML, err
}

// CompilePreviewContext also returns the portable page model for CMS readers.
func CompilePreviewContext(ctx context.Context, manifestPath string, page Page, source []byte, options PreviewOptions) (PreviewResult, error) {
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
	absolute, err := filepath.Abs(manifestPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("resolve project manifest: %w", err)
	}
	root, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return PreviewResult{}, fmt.Errorf("open preview project: %w", err)
	}
	defer root.Close()
	manifest, err := ReadManifest(root, filepath.Base(absolute))
	if err != nil {
		return PreviewResult{}, err
	}
	return compilePreviewLoaded(ctx, manifest, page, source, options, canRead, confinedSourceLoader(ctx, root, "", page.Source, source))
}

func compilePreviewLoaded(ctx context.Context, manifest Manifest, page Page, source []byte, options PreviewOptions, canRead func(string) bool, loadSource func(string) ([]byte, error)) (PreviewResult, error) {
	var err error
	index := -1
	for i, existing := range manifest.Pages {
		if (page.ID != "" && page.ID == existing.ID) || page.Source == existing.Source {
			if index >= 0 && index != i {
				return PreviewResult{}, fmt.Errorf("preview document identity conflicts with the project")
			}
			index = i
		}
	}
	if index < 0 {
		manifest.Pages = append(manifest.Pages, page)
		index = len(manifest.Pages) - 1
	} else {
		if page.ID == "" {
			page.ID = manifest.Pages[index].ID
		}
		manifest.Pages[index] = page
	}
	if err := ValidateManifest(&manifest); err != nil {
		return PreviewResult{}, err
	}
	page = manifest.Pages[index]
	manifest.Pages = slices.DeleteFunc(manifest.Pages, func(candidate Page) bool { return !canRead(candidate.Source) })
	if manifest.Navigation != nil && manifest.Navigation.Tree != nil {
		retained := make(map[string]bool, len(manifest.Pages))
		for _, candidate := range manifest.Pages {
			retained[candidate.ID] = true
		}
		navigation := *manifest.Navigation
		navigation.Tree, err = FilterNavigation(navigation.Tree, retained)
		if err != nil {
			return PreviewResult{}, err
		}
		manifest.Navigation = &navigation
	}
	manifest.Assets = slices.DeleteFunc(manifest.Assets, func(name string) bool { return !canRead(name) })
	if manifest.Logo != "" && !canRead(manifest.Logo) {
		manifest.Logo = ""
	}
	if manifest.Favicon != "" && !canRead(manifest.Favicon) {
		manifest.Favicon = ""
	}
	current := slices.IndexFunc(manifest.Pages, func(candidate Page) bool { return candidate.Source == page.Source })
	if current < 0 {
		return PreviewResult{}, ErrPreviewAccess
	}
	var denied atomic.Bool
	var sourceMutex sync.Mutex
	sources := map[string]bool{page.Source: true}
	load := func(name string) ([]byte, error) {
		if !localPath(name) || !canRead(name) {
			denied.Store(true)
			return nil, ErrPreviewAccess
		}
		data, err := loadSource(name)
		if err == nil {
			sourceMutex.Lock()
			sources[name] = true
			sourceMutex.Unlock()
		}
		return data, err
	}
	content, api, err := compileLoadedPage(ctx, source, current, manifest, options.Compiler, load)
	if denied.Load() {
		return PreviewResult{}, ErrPreviewAccess
	}
	if err != nil {
		return PreviewResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return PreviewResult{}, err
	}
	manifest.ReaderStudioURL = options.ReaderStudioURL
	rendered, err := renderPage(manifest, current, content)
	if err != nil {
		return PreviewResult{}, err
	}
	if content.Blocks == nil {
		content.Blocks = []document.Block{}
	}
	model := document.Page{Schema: "../schema/page-v1.json", Format: "pb-docs/page", Version: document.FormatVersion, ID: page.ID, Title: page.Title, Path: page.Path, Source: document.Location{File: page.Source}, Kind: page.Kind, Blocks: content.Blocks, API: api}
	sourceMutex.Lock()
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sourceMutex.Unlock()
	slices.Sort(names)
	return PreviewResult{HTML: rendered, Page: model, DataPath: pageDataPath(page), Sources: names}, nil
}
