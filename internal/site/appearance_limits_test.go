package site

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestRejectsOversizedPathsBeforeNavigationAmplification(t *testing.T) {
	manifest := Manifest{Version: 1, Title: "Docs", Pages: []Page{{ID: "home", Title: "Home", Source: "index.md", Path: "index.html"}}}
	for index := 1; index < maxPages; index++ {
		manifest.Pages = append(manifest.Pages, Page{ID: fmt.Sprintf("page%d", index), Title: "Page", Source: fmt.Sprintf("p/%d.md", index), Path: fmt.Sprintf("p/%d.html", index)})
	}
	// This remains below the 1 MiB manifest budget. Repeating its relative
	// navigation prefix for every page must not reach the renderer.
	manifest.Pages[1].Path = strings.Repeat("a/", 384<<10) + "page.html"
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.Less(t, len(data), maxManifestBytes)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs.json"), data, 0o600))
	confined, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer confined.Close()
	_, err = ReadManifest(confined, "docs.json")
	require.ErrorContains(t, err, "path must be a lowercase relative .html path")

	boundary := strings.Repeat("a/", 2043) + "guide.html"
	require.Len(t, boundary, 4096)
	assert.True(t, localPath(boundary))
	assert.False(t, localPath(strings.TrimSuffix(boundary, ".html")+"x.html"))
	assert.False(t, localPath(strings.Repeat("ю", 2049)+".md"), "the limit counts UTF-8 bytes")
	manifest.Pages = manifest.Pages[:2]
	manifest.Pages[1].Path = boundary
	require.NoError(t, ValidateManifest(&manifest))
	manifest.Pages[1].Source = strings.Repeat("a/", 384<<10) + "page.md"
	require.ErrorContains(t, ValidateManifest(&manifest), "source must be a clean relative path")
}

func TestPublicModelEncodersPreflightEscapesAndRespectCancellation(t *testing.T) {
	page := document.Page{Format: "pb-docs/page", Version: 1, ID: "home", Title: "Home", Source: document.Location{File: "index.md"}, Path: "index.html", Kind: "markdown"}
	data, err := EncodePageModel(t.Context(), page)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, []any{}, decoded["blocks"])
	project := ProjectModel(Manifest{Title: "Docs", Pages: []Page{{ID: "home", Title: "Home", Source: "index.md", Path: "index.html", Kind: "markdown"}}})
	data, err = EncodeProjectModel(t.Context(), project)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"format":"pb-docs/project"`)

	page.Kind = "html"
	page.Blocks = []document.Block{{Kind: document.BlockHTML, HTML: strings.Repeat("<", 3<<20)}}
	// The input is 3 MiB, but JSON escaping would require over 18 MiB. Reject
	// it before encoding/json allocates that output or its growing buffer.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	data, err = EncodePageModel(t.Context(), page)
	runtime.ReadMemStats(&after)
	require.ErrorContains(t, err, "encoded bytes")
	assert.Nil(t, data)
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20), "rejection must precede the expanded JSON allocation")
	project.Title = strings.Repeat("<", 2<<20)
	data, err = EncodeProjectModel(t.Context(), project)
	require.ErrorContains(t, err, "encoded bytes")
	assert.Nil(t, data)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = EncodePageModel(canceled, page)
	require.ErrorIs(t, err, context.Canceled)
	var missing context.Context
	_, err = EncodeProjectModel(missing, project)
	require.ErrorContains(t, err, "context is required")
}
