package site

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeModelFixture(t *testing.T, root, name, content string) string {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o750))
	require.NoError(t, os.WriteFile(filename, []byte(content), 0o600))
	return filename
}

func readModelFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	require.NoError(t, err)
	return data
}

func TestBuildRejectsResolvedManifestInsideOutput(t *testing.T) {
	root := t.TempDir()
	config := writeModelFixture(t, root, "docs.json", `{"version":1,"title":"Guide","pages":[{"title":"Home","source":"page.md","path":"index.html"}],"assets":["download.json"]}`)
	writeModelFixture(t, root, "page.md", "# Home\n")
	writeModelFixture(t, root, "download.json", `{"version":1,"title":"Guide","pages":[{"title":"Home","source":"page.md","path":"index.html"}]}`)
	output := filepath.Join(root, "out")
	require.NoError(t, Build(config, output, Options{}))
	saved := readModelFixture(t, filepath.Join(output, "assets", "download.json"))
	require.NoError(t, os.Remove(config))
	if err := os.Symlink(filepath.Join("out", "assets", "download.json"), config); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, check := range []bool{false, true} {
		err := Build(config, output, Options{Check: check})
		require.ErrorContains(t, err, "output contains project manifest")
		assert.Equal(t, saved, readModelFixture(t, config))
	}
}

func TestBuildContextCancellationPreservesPublishedSite(t *testing.T) {
	root := t.TempDir()
	config := writeModelFixture(t, root, "docs.json", `{"version":1,"title":"Guide","pages":[{"title":"Home","source":"page.proto","path":"index.html","kind":"protobuf"}]}`)
	writeModelFixture(t, root, "page.proto", "syntax = \"proto3\";\n")
	output := filepath.Join(root, "out")
	require.NoError(t, Build(config, output, Options{ImportProto: func(string, []byte) ([]byte, error) { return []byte("# Published\n"), nil }}))
	saved := readModelFixture(t, filepath.Join(output, "index.html"))
	ctx, cancel := context.WithCancel(context.Background())
	err := BuildContext(ctx, config, output, Options{ImportPage: func(context.Context, string, string, []byte, func(string) ([]byte, error)) (ImportResult, error) {
		cancel()
		return ImportResult{Markdown: []byte("# Canceled\n")}, nil
	}})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, saved, readModelFixture(t, filepath.Join(output, "index.html")))
	stages, err := filepath.Glob(filepath.Join(root, ".docs-build-*"))
	require.NoError(t, err)
	assert.Empty(t, stages)
	var missingContext context.Context
	require.ErrorContains(t, BuildContext(missingContext, config, output, Options{}), "context")
	require.ErrorIs(t, BuildContext(ctx, config, output, Options{}), context.Canceled)
}

func TestImportedSourcesAreConfinedAndBounded(t *testing.T) {
	for _, bad := range []string{"../outside.yaml", "/outside.yaml", "https://example.com/api.yaml", "out/assets/dependency.yaml"} {
		t.Run(bad, func(t *testing.T) {
			root := t.TempDir()
			config := writeModelFixture(t, root, "docs.json", `{"version":1,"title":"Seed","pages":[{"title":"Home","source":"page.md","path":"index.html"}],"assets":["dependency.yaml"]}`)
			writeModelFixture(t, root, "page.md", "# Home\n")
			writeModelFixture(t, root, "dependency.yaml", "must not be consumed\n")
			output := filepath.Join(root, "out")
			require.NoError(t, Build(config, output, Options{}))
			writeModelFixture(t, root, "docs.json", `{"version":1,"title":"API","pages":[{"title":"API","source":"api.yaml","path":"index.html","kind":"openapi"}]}`)
			writeModelFixture(t, root, "api.yaml", "openapi: 3.1.0\n")
			called := false
			err := Build(config, output, Options{Check: true, ImportPage: func(_ context.Context, _, _ string, _ []byte, load func(string) ([]byte, error)) (ImportResult, error) {
				called = true
				_, err := load(bad)
				return ImportResult{}, err
			}})
			require.Error(t, err)
			assert.True(t, called, "the source loader, rather than output ownership, must reject this input")
		})
	}
	root := t.TempDir()
	config := writeModelFixture(t, root, "docs.json", `{"version":1,"title":"API","pages":[{"title":"API","source":"api.yaml","path":"index.html","kind":"openapi"}]}`)
	writeModelFixture(t, root, "api.yaml", "openapi: 3.1.0\n")
	for i := range maxImportedDocuments {
		writeModelFixture(t, root, fmt.Sprintf("ref-%d.yaml", i), "{}")
	}
	err := Build(config, filepath.Join(root, "out"), Options{ImportPage: func(_ context.Context, _, _ string, _ []byte, load func(string) ([]byte, error)) (ImportResult, error) {
		for i := range maxImportedDocuments {
			if _, err := load(fmt.Sprintf("ref-%d.yaml", i)); err != nil {
				return ImportResult{}, err
			}
		}
		return ImportResult{}, errors.New("source budget did not include root")
	}})
	require.ErrorContains(t, err, "64")
}

func TestBuildExportsStablePageIdentityAndProvenance(t *testing.T) {
	root := t.TempDir()
	config := writeModelFixture(t, root, "docs.json", `{"version":1,"title":"Guide","pages":[{"id":"home","title":"Home","source":"page.md","path":"index.html"},{"id":"chapter","title":"Chapter","source":"chapter.md","path":"guide/chapter.html"}],"assets":["manual.pdf"]}`)
	writeModelFixture(t, root, "page.md", "# Home\n\nRead [Chapter](chapter.md#next) or [Manual](manual.pdf#page=3).\n\nRaw <span>HTML</span>.\n")
	writeModelFixture(t, root, "chapter.md", "# Next\n")
	writeModelFixture(t, root, "manual.pdf", "%PDF-test")
	output := filepath.Join(root, "out")
	require.NoError(t, Build(config, output, Options{}))
	var project document.Project
	require.NoError(t, json.Unmarshal(readModelFixture(t, filepath.Join(output, "_docs", "project.json")), &project))
	require.Len(t, project.Pages, 2)
	assert.Equal(t, "../", project.Root)
	assert.Equal(t, "home", project.Pages[0].ID)
	assert.Equal(t, "chapter", project.Pages[1].ID)
	dataFile := filepath.Join(output, "_docs", filepath.FromSlash(project.Pages[0].Data))
	var page document.Page
	require.NoError(t, json.Unmarshal(readModelFixture(t, dataFile), &page))
	assert.Equal(t, "home", page.ID)
	assert.Equal(t, "page.md", page.Source.File)
	require.NotEmpty(t, page.Blocks)
	assert.Equal(t, 1, page.Blocks[0].Source.Line)
	assert.False(t, page.Blocks[0].Source.Generated)
	var references []document.Reference
	var omitted bool
	var inlines func([]document.Inline)
	inlines = func(nodes []document.Inline) {
		for _, inline := range nodes {
			if inline.Reference != nil {
				references = append(references, *inline.Reference)
			}
			omitted = omitted || inline.Omitted
			inlines(inline.Children)
		}
	}
	var blocks func([]document.Block)
	blocks = func(nodes []document.Block) {
		for _, block := range nodes {
			omitted = omitted || block.Omitted
			inlines(block.Inlines)
			blocks(block.Children)
		}
	}
	blocks(page.Blocks)
	assert.Contains(t, references, document.Reference{Page: "chapter", Anchor: "next"})
	assert.Contains(t, references, document.Reference{Asset: "manual.pdf", Anchor: "page=3"})
	assert.True(t, omitted)
	require.NoError(t, Build(config, output, Options{Check: true}))
	manifest := string(readModelFixture(t, config))
	writeModelFixture(t, root, "docs.json", strings.Replace(manifest, "guide/chapter.html", "renamed.html", 1))
	require.NoError(t, Build(config, output, Options{}))
	var updated document.Project
	require.NoError(t, json.Unmarshal(readModelFixture(t, filepath.Join(output, "_docs", "project.json")), &updated))
	assert.Equal(t, project.Pages[1].Data, updated.Pages[1].Data)
	assert.Equal(t, "chapter", updated.Pages[1].ID)
	_, err := os.Stat(filepath.Join(output, "guide", "chapter.html"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
