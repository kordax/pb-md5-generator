package site

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func previewProject(t *testing.T) (string, Page) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"docs.json":  `{"version":1,"title":"Preview project","pages":[{"id":"home","title":"Home","source":"index.md","path":"index.html","kind":"markdown"},{"id":"guide","title":"Guide","source":"guide.md","path":"guide.html","kind":"markdown"},{"id":"secret","title":"Secret navigation title","source":"private.md","path":"private.html","kind":"markdown"}],"assets":["public.png","private.png"]}`,
		"index.md":   "# Published home\n",
		"guide.md":   "# Published guide\n",
		"private.md": "Top secret source content\n",
		"public.md":  "Included public paragraph\n",
	}
	for name, source := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(source), 0o600))
	}
	return filepath.Join(root, "docs.json"), Page{ID: "guide", Title: "Guide", Source: "guide.md", Path: "guide.html", Kind: "markdown"}
}

func TestPreviewUsesUnsavedSourceWithoutPublishing(t *testing.T) {
	manifest, page := previewProject(t)
	before, err := os.ReadDir(filepath.Dir(manifest))
	require.NoError(t, err)
	result, err := PreviewContext(t.Context(), manifest, page, []byte("# Unsaved heading\n\n:::include source=\"public.md\"\n:::\n"), PreviewOptions{
		CanRead: func(name string) bool { return name != "private.md" && name != "private.png" && name != "index.md" },
	})
	require.NoError(t, err)
	assert.Contains(t, string(result), "Unsaved heading")
	assert.Contains(t, string(result), "Included public paragraph")
	assert.NotContains(t, string(result), "Published guide")
	assert.NotContains(t, string(result), "Secret navigation title")
	assert.NotContains(t, string(result), "private.html")
	assert.NotContains(t, string(result), "index.html")
	after, err := os.ReadDir(filepath.Dir(manifest))
	require.NoError(t, err)
	assert.Equal(t, before, after)
	published, err := os.ReadFile(filepath.Join(filepath.Dir(manifest), page.Source))
	require.NoError(t, err)
	assert.Equal(t, "# Published guide\n", string(published))
}

func TestPreviewAllowsNewAndMovedDocuments(t *testing.T) {
	for _, moved := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "moved"}[moved], func(t *testing.T) {
			manifest, page := previewProject(t)
			if !moved {
				page.ID = "new-page"
			}
			page.Source, page.Path = "unsaved.md", "unsaved.html"
			html, err := PreviewContext(t.Context(), manifest, page, []byte("# New source\n"), PreviewOptions{})
			require.NoError(t, err)
			assert.Contains(t, string(html), "New source")
			_, err = os.Stat(filepath.Join(filepath.Dir(manifest), page.Source))
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestPreviewChecksEveryTransitiveSource(t *testing.T) {
	manifest, page := previewProject(t)
	canRead := func(name string) bool { return name != "private.md" && name != "missing-private.md" }
	for _, name := range []string{"private.md", "missing-private.md"} {
		_, err := PreviewContext(t.Context(), manifest, page, []byte(":::include source=\""+name+"\"\n:::\n"), PreviewOptions{CanRead: canRead})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPreviewAccess)
		assert.NotContains(t, err.Error(), "Top secret")
		assert.NotContains(t, err.Error(), "no such file")
	}
	page.Kind = "protobuf"
	_, err := PreviewContext(t.Context(), manifest, page, []byte("syntax = \"proto3\";"), PreviewOptions{
		CanRead: canRead,
		Compiler: Options{ImportPage: func(_ context.Context, _, _ string, _ []byte, load func(string) ([]byte, error)) (ImportResult, error) {
			_, err := load("private.md")
			return ImportResult{}, err
		}},
	})
	assert.ErrorIs(t, err, ErrPreviewAccess)
	page.Source = "private.md"
	_, err = PreviewContext(t.Context(), filepath.Join(t.TempDir(), "missing.json"), page, nil, PreviewOptions{CanRead: canRead})
	assert.ErrorIs(t, err, ErrPreviewAccess)
}

func TestPreviewRejectsInvalidAndCancelledInput(t *testing.T) {
	manifest, page := previewProject(t)
	for _, source := range [][]byte{{0xff}, []byte(strings.Repeat("x", maxSourceBytes+1))} {
		_, err := PreviewContext(t.Context(), manifest, page, source, PreviewOptions{})
		require.Error(t, err)
	}
	page.Source = "../escape.md"
	_, err := PreviewContext(t.Context(), manifest, page, nil, PreviewOptions{})
	assert.ErrorIs(t, err, ErrPreviewAccess)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = PreviewContext(ctx, manifest, page, nil, PreviewOptions{})
	assert.ErrorIs(t, err, context.Canceled)
	var missing context.Context
	_, err = PreviewContext(missing, manifest, page, nil, PreviewOptions{})
	assert.Error(t, err)
}
