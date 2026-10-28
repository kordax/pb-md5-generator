package site

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/kordax/pb-md5-generator/internal/importer"
	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type previewSpyFS struct {
	fs.FS
	mutex sync.Mutex
	opens map[string]int
	stats map[string]int
}

func newPreviewSpyFS(source fs.FS) *previewSpyFS {
	return &previewSpyFS{FS: source, opens: make(map[string]int), stats: make(map[string]int)}
}

func (s *previewSpyFS) Open(name string) (fs.File, error) {
	s.mutex.Lock()
	s.opens[name]++
	s.mutex.Unlock()
	return s.FS.Open(name)
}

func (s *previewSpyFS) Stat(name string) (fs.FileInfo, error) {
	s.mutex.Lock()
	s.stats[name]++
	s.mutex.Unlock()
	return fs.Stat(s.FS, name)
}

func previewFSFixture(t *testing.T, kind, extension string) (fstest.MapFS, Page) {
	t.Helper()
	page := Page{ID: "guide", Title: "Guide", Source: "guide." + extension, Path: "guide.html", Kind: kind}
	manifest := map[string]any{
		"version": 1, "title": "Lazy preview",
		"pages":  []Page{{ID: "home", Title: "Home", Source: "index.md", Path: "index.html", Kind: "markdown"}, page, {ID: "private", Title: "Private page title", Source: "private.md", Path: "private.html", Kind: "markdown"}},
		"assets": []string{"public.png", "private.png"}, "logo": "private.png", "favicon": "private.png",
		"navigation": map[string]any{"showTOC": true, "showPreviousNext": true, "stickyHeader": true, "tree": map[string]any{"unlistedPages": "omit", "items": []any{
			map[string]any{"kind": "group", "id": "visible", "title": "Visible section", "children": []any{map[string]any{"kind": "page", "pageId": "home"}, map[string]any{"kind": "page", "pageId": "guide"}}},
			map[string]any{"kind": "group", "id": "private", "title": "Private section title", "children": []any{map[string]any{"kind": "page", "pageId": "private"}}},
		}}},
	}
	encoded, err := json.Marshal(manifest)
	require.NoError(t, err)
	return fstest.MapFS{
		"docs.json": {Data: encoded}, "index.md": {Data: []byte("# Published home\n")}, page.Source: {Data: []byte("Published source must not be read\n")},
		"private.md": {Data: []byte("Private body")}, "public.png": {Data: []byte("image bytes must not be opened")}, "private.png": {Data: []byte("private image")},
		"unrelated.bin": {Data: []byte("Unrelated bytes")},
	}, page
}

func previewFSOptions() PreviewOptions {
	return PreviewOptions{
		CanRead: func(name string) bool { return name != "private.md" && name != "private.png" },
		Compiler: Options{ImportPage: func(ctx context.Context, kind, name string, source []byte, load func(string) ([]byte, error)) (ImportResult, error) {
			if kind == "openapi" {
				api, err := importer.OpenAPI(ctx, name, source, importer.Load(load))
				return ImportResult{API: api}, err
			}
			if err := importer.CheckProtobufSource(ctx, name, source); err != nil {
				return ImportResult{}, err
			}
			request, err := internalproto.RequestFromSource(name, source)
			if err != nil {
				return ImportResult{}, err
			}
			api, err := importer.ProtobufRequest(ctx, name, request)
			return ImportResult{API: api}, err
		}},
	}
}

func writePreviewFSFixture(t *testing.T, files fstest.MapFS) string {
	t.Helper()
	root := t.TempDir()
	for name, file := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(name))), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), file.Data, 0o600))
	}
	return filepath.Join(root, "docs.json")
}

func TestPreviewFSMatchesPhysicalForEveryPageKind(t *testing.T) {
	cases := []struct {
		kind, extension, source string
		dependencies            map[string]string
	}{
		{kind: "markdown", extension: "md", source: "# Unsaved heading\n\n:::include source=\"snippets/first.md\"\n:::\n\n:::include source=\"snippets/first.md\"\n:::\n", dependencies: map[string]string{"snippets/first.md": "Included paragraph\n\n:::include source=\"nested.md\"\n:::\n", "snippets/nested.md": "Nested included paragraph\n"}},
		{kind: "html", extension: "html", source: `<h1 id="intro">Safe heading</h1><script>neverRun()</script><p><a href="index.md">Home</a></p><img src="public.png" onerror="neverRun()">`},
		{kind: "protobuf", extension: "proto", source: "syntax = \"proto3\";\npackage sample;\nmessage Request { string id = 1; }\nmessage Response { string value = 1; }\nservice Docs { rpc Fetch (Request) returns (Response); }\n"},
		{kind: "openapi", extension: "yaml", source: "openapi: 3.0.3\ninfo:\n  title: Reference\n  version: 1.0.0\npaths:\n  /items:\n    get:\n      responses:\n        '200':\n          description: OK\n          content:\n            application/json:\n              schema:\n                $ref: './schemas/response.yaml'\n", dependencies: map[string]string{"schemas/response.yaml": "type: object\nproperties:\n  id:\n    type: string\n"}},
	}
	for _, item := range cases {
		t.Run(item.kind, func(t *testing.T) {
			files, page := previewFSFixture(t, item.kind, item.extension)
			expectedSources := []string{page.Source}
			for name, source := range item.dependencies {
				files[name] = &fstest.MapFile{Data: []byte(source)}
				expectedSources = append(expectedSources, name)
			}
			slices.Sort(expectedSources)
			spy := newPreviewSpyFS(files)
			physical, err := CompilePreviewContext(t.Context(), writePreviewFSFixture(t, files), page, []byte(item.source), previewFSOptions())
			require.NoError(t, err)
			lazy, err := CompilePreviewFSContext(t.Context(), spy, "docs.json", page, []byte(item.source), previewFSOptions())
			require.NoError(t, err)
			assert.Equal(t, physical, lazy)
			assert.Equal(t, expectedSources, lazy.Sources)
			assert.NotContains(t, string(lazy.HTML), "Private page title")
			assert.NotContains(t, string(lazy.HTML), "Private section title")
			assert.NotContains(t, string(lazy.HTML), "private.html")
			assert.NotContains(t, string(lazy.HTML), "neverRun()")
			assert.Equal(t, 1, spy.opens["docs.json"])
			for name := range item.dependencies {
				assert.Equal(t, 1, spy.opens[name], name)
			}
			assert.Len(t, spy.opens, len(item.dependencies)+1)
			assert.NotContains(t, spy.stats, page.Source)
			assert.NotContains(t, spy.stats, "public.png")
			assert.NotContains(t, spy.stats, "private.md")
			assert.NotContains(t, spy.stats, "unrelated.bin")
			assert.Equal(t, "Published source must not be read\n", string(files[page.Source].Data))
		})
	}
}

func TestPreviewFSAllowsNewAndMovedUnsavedSources(t *testing.T) {
	for _, moved := range []bool{false, true} {
		t.Run(fmt.Sprintf("moved=%v", moved), func(t *testing.T) {
			files, page := previewFSFixture(t, "markdown", "md")
			if !moved {
				page.ID = "new-page"
			}
			page.Source, page.Path = "new/unsaved.md", "new/unsaved.html"
			spy := newPreviewSpyFS(files)
			physical, err := CompilePreviewContext(t.Context(), writePreviewFSFixture(t, files), page, []byte("# Unsaved new source\n"), previewFSOptions())
			require.NoError(t, err)
			result, err := CompilePreviewFSContext(t.Context(), spy, "docs.json", page, []byte("# Unsaved new source\n"), previewFSOptions())
			require.NoError(t, err)
			assert.Equal(t, physical, result)
			assert.Equal(t, []string{page.Source}, result.Sources)
			assert.Equal(t, map[string]int{"docs.json": 1}, spy.opens)
			assert.NotContains(t, files, page.Source)
		})
	}
}

func TestPreviewFSChecksAccessBeforeAnySourceOperation(t *testing.T) {
	files, page := previewFSFixture(t, "markdown", "md")
	spy := newPreviewSpyFS(files)
	_, err := CompilePreviewFSContext(t.Context(), spy, "docs.json", page, nil, PreviewOptions{CanRead: func(string) bool { return false }})
	require.ErrorIs(t, err, ErrPreviewAccess)
	assert.Empty(t, spy.opens)
	assert.Empty(t, spy.stats)
	for _, name := range []string{"private.md", "missing-private.md"} {
		spy := newPreviewSpyFS(files)
		_, err := CompilePreviewFSContext(t.Context(), spy, "docs.json", page, []byte(":::include source=\""+name+"\"\n:::\n"), PreviewOptions{CanRead: func(source string) bool { return source != name }})
		require.ErrorIs(t, err, ErrPreviewAccess)
		assert.Equal(t, ErrPreviewAccess.Error(), err.Error())
		assert.NotContains(t, spy.opens, name)
		assert.NotContains(t, spy.stats, name)
	}
	spy = newPreviewSpyFS(files)
	_, err = CompilePreviewFSContext(t.Context(), spy, "docs.json", page, []byte(":::include source=\"../escape.md\"\n:::\n"), previewFSOptions())
	require.ErrorContains(t, err, "escapes")
	assert.NotContains(t, spy.opens, "../escape.md")
	assert.NotContains(t, spy.stats, "../escape.md")
	page.Kind = "protobuf"
	spy = newPreviewSpyFS(files)
	_, err = CompilePreviewFSContext(t.Context(), spy, "docs.json", page, []byte("syntax = \"proto3\";"), PreviewOptions{CanRead: func(name string) bool { return name != "private.md" }, Compiler: Options{ImportPage: func(_ context.Context, _, _ string, _ []byte, load func(string) ([]byte, error)) (ImportResult, error) {
		_, _ = load("private.md")
		return ImportResult{Markdown: []byte("# Importer swallowed an access error")}, nil
	}}})
	require.ErrorIs(t, err, ErrPreviewAccess)
	assert.NotContains(t, spy.opens, "private.md")
	assert.NotContains(t, spy.stats, "private.md")
}

func TestPreviewFSWidePrivateNavigationDoesNotReadOrExposePrivateFiles(t *testing.T) {
	page := Page{ID: "guide", Title: "Allowed guide", Source: "guide.md", Path: "guide.html", Kind: "markdown"}
	manifest := func(marker string) []byte {
		pages := []Page{{ID: "home", Title: marker, Source: "index.md", Path: "index.html", Kind: "markdown"}, page}
		private := []any{map[string]any{"kind": "page", "pageId": "home"}}
		for i := range 512 {
			id := fmt.Sprintf("private-%03d", i)
			pages = append(pages, Page{ID: id, Title: marker + id, Source: "private/" + id + ".md", Path: "private/" + id + ".html", Kind: "markdown"})
			private = append(private, map[string]any{"kind": "page", "pageId": id})
		}
		value := map[string]any{"version": 1, "title": "Wide preview", "pages": pages, "assets": []string{"private.png"}, "logo": "private.png", "favicon": "private.png", "navigation": map[string]any{"tree": map[string]any{"unlistedPages": "omit", "items": []any{map[string]any{"kind": "page", "pageId": "guide"}, map[string]any{"kind": "group", "id": "private", "title": marker, "children": private}}}}}
		data, err := json.Marshal(value)
		require.NoError(t, err)
		return data
	}
	files := fstest.MapFS{"docs.json": {Data: manifest("FIRST PRIVATE MARKER")}, "private/private-000.md": {Data: []byte("First private body")}, "private.png": {Data: []byte("First image")}, "unrelated.bin": {Data: make([]byte, 1<<20)}}
	spy := newPreviewSpyFS(files)
	options := PreviewOptions{CanRead: func(name string) bool { return name == page.Source }}
	first, err := CompilePreviewFSContext(t.Context(), spy, "docs.json", page, []byte("# Allowed heading\n"), options)
	require.NoError(t, err)
	files["docs.json"].Data = manifest("SECOND PRIVATE MARKER")
	files["private/private-000.md"].Data = []byte("Second private body")
	files["private.png"].Data = []byte("Second image")
	second, err := CompilePreviewFSContext(t.Context(), spy, "docs.json", page, []byte("# Allowed heading\n"), options)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.NotContains(t, string(second.HTML), "PRIVATE MARKER")
	assert.NotContains(t, string(second.HTML), "private.png")
	assert.NotContains(t, string(second.HTML), "index.html")
	assert.Equal(t, []string{page.Source}, second.Sources)
	assert.Equal(t, map[string]int{"docs.json": 2}, spy.opens)
	assert.Equal(t, map[string]int{"docs.json": 2}, spy.stats)
}

func TestPreviewFSRejectsInvalidCancelledAndConflictingInputs(t *testing.T) {
	files, page := previewFSFixture(t, "markdown", "md")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	spy := newPreviewSpyFS(files)
	_, err := CompilePreviewFSContext(ctx, spy, "docs.json", page, nil, PreviewOptions{})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, spy.opens)
	var missingContext context.Context
	_, err = CompilePreviewFSContext(missingContext, spy, "docs.json", page, nil, PreviewOptions{})
	assert.Error(t, err)
	_, err = CompilePreviewFSContext(t.Context(), nil, "docs.json", page, nil, PreviewOptions{})
	assert.Error(t, err)
	for _, name := range []string{"../docs.json", "/docs.json", "missing.json"} {
		_, err = ReadManifestFS(t.Context(), spy, name)
		assert.Error(t, err, name)
	}
	for _, source := range [][]byte{{0xff}, []byte(strings.Repeat("x", maxSourceBytes+1))} {
		_, err = CompilePreviewFSContext(t.Context(), spy, "docs.json", page, source, previewFSOptions())
		assert.Error(t, err)
	}
	page.ID = "home"
	_, err = CompilePreviewFSContext(t.Context(), spy, "docs.json", page, []byte("# Conflicting identity"), previewFSOptions())
	assert.ErrorContains(t, err, "identity conflicts")
	_, err = ReadManifestFS(t.Context(), fstest.MapFS{"docs.json": {Data: []byte(`{"version":999}`)}}, "docs.json")
	assert.Error(t, err)
}
