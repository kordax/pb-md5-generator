package site

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func compilerEdgeOptions() Options {
	return Options{ImportPage: func(ctx context.Context, _ string, name string, source []byte, load func(string) ([]byte, error)) (ImportResult, error) {
		api, err := importer.OpenAPI(ctx, name, source, importer.Load(load))
		return ImportResult{API: api}, err
	}}
}

func compilerEdgeProject(t *testing.T) (string, string, string, Manifest, Options) {
	t.Helper()
	root := t.TempDir()
	manifest := Manifest{Version: 1, Title: "Compiler boundary project", Pages: []Page{{ID: "api", Title: "API", Source: "api.yaml", Path: "index.html", Kind: "openapi"}}, Assets: []string{"notes.txt"}}
	writeModelFixture(t, root, "api.yaml", "openapi: 3.1.0\ninfo: {title: Boundary API, version: '1'}\npaths:\n  /ping:\n    get:\n      operationId: ping\n      responses:\n        '200': {description: Success}\n")
	writeModelFixture(t, root, "notes.txt", "An owned project attachment.\n")
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	config := writeModelFixture(t, root, "docs.json", string(data))
	output := filepath.Join(t.TempDir(), "site")
	options := compilerEdgeOptions()
	require.NoError(t, BuildContext(t.Context(), config, output, options))
	return root, config, output, manifest, options
}

func compilerEdgeSnapshot(t *testing.T, directory string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	require.NoError(t, filepath.WalkDir(directory, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("snapshot contains a nonregular file")
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(data)
		return nil
	}))
	return files
}

func TestCompilerCheckFindsDriftInEveryPublishedArtifact(t *testing.T) {
	_, config, output, manifest, options := compilerEdgeProject(t)
	before := compilerEdgeSnapshot(t, output)
	artifacts := []string{manifest.Pages[0].Path, pageDataPath(manifest.Pages[0]), "_docs/search.json", "_docs/project.json", "_docs/schema/manifest-v1.json", "_docs/schema/project-v1.json", "_docs/schema/page-v1.json", "_docs/api.css", "_docs/api-client.js", "_docs/appearance.css", "_docs/site.css", "_docs/site.js", "assets/notes.txt", inventoryPath}
	for _, artifact := range artifacts {
		t.Run(artifact, func(t *testing.T) {
			original, exists := before[artifact]
			require.True(t, exists)
			filename := filepath.Join(output, filepath.FromSlash(artifact))
			changed := original + "\n"
			require.NoError(t, os.WriteFile(filename, []byte(changed), 0o600))
			t.Cleanup(func() { require.NoError(t, os.WriteFile(filename, []byte(original), 0o600)) })
			check := options
			check.Check = true
			err := BuildContext(t.Context(), config, output, check)
			require.ErrorContains(t, err, "out of date")
			after := compilerEdgeSnapshot(t, output)
			assert.Equal(t, changed, after[artifact], "check mode cannot repair or replace an artifact")
			delete(after, artifact)
			unchanged := make(map[string]string, len(before)-1)
			for name, data := range before {
				if name != artifact {
					unchanged[name] = data
				}
			}
			assert.Equal(t, unchanged, after)
		})
	}
}

func TestCompilerRejectsInvalidImportResultsWithoutReplacingPublication(t *testing.T) {
	root, config, output, manifest, baseline := compilerEdgeProject(t)
	before := compilerEdgeSnapshot(t, output)
	pluginFailure := errors.New("import plugin failed")
	tests := []struct {
		name string
		kind string
		make func(context.CancelFunc) Options
		want string
		is   error
	}{
		{name: "missing typed HTTP contract", kind: "openapi", make: func(context.CancelFunc) Options {
			return Options{ImportPage: func(context.Context, string, string, []byte, func(string) ([]byte, error)) (ImportResult, error) {
				return ImportResult{}, nil
			}}
		}, want: "no typed API"},
		{name: "invalid typed contract text", kind: "openapi", make: func(context.CancelFunc) Options {
			return Options{ImportPage: func(context.Context, string, string, []byte, func(string) ([]byte, error)) (ImportResult, error) {
				return ImportResult{API: &document.API{Protocol: "http", Title: string([]byte{0xff})}}, nil
			}}
		}, want: "invalid UTF-8"},
		{name: "bad generated local link", kind: "openapi", make: func(context.CancelFunc) Options {
			return Options{ImportPage: func(ctx context.Context, kind, name string, data []byte, load func(string) ([]byte, error)) (ImportResult, error) {
				result, err := baseline.ImportPage(ctx, kind, name, data, load)
				result.Markdown = []byte("[Unavailable page](missing.md)\n")
				return result, err
			}}
		}, want: "missing.md"},
		{name: "API and generated heading identity collide", kind: "openapi", make: func(context.CancelFunc) Options {
			return Options{ImportPage: func(ctx context.Context, kind, name string, data []byte, load func(string) ([]byte, error)) (ImportResult, error) {
				result, err := baseline.ImportPage(ctx, kind, name, data, load)
				result.Markdown = []byte("# Collision {#" + document.APIAnchor("api", "api") + "}\n")
				return result, err
			}}
		}, want: "duplicate anchor"},
		{name: "oversized generated Markdown", kind: "openapi", make: func(context.CancelFunc) Options {
			return Options{ImportPage: func(ctx context.Context, kind, name string, data []byte, load func(string) ([]byte, error)) (ImportResult, error) {
				result, err := baseline.ImportPage(ctx, kind, name, data, load)
				result.Markdown = bytes.Repeat([]byte("x"), maxSourceBytes+1)
				return result, err
			}}
		}, want: "generated page exceeds"},
		{name: "cancel after successful import", kind: "openapi", make: func(cancel context.CancelFunc) Options {
			return Options{ImportPage: func(ctx context.Context, kind, name string, data []byte, load func(string) ([]byte, error)) (ImportResult, error) {
				result, err := baseline.ImportPage(ctx, kind, name, data, load)
				cancel()
				return result, err
			}}
		}, is: context.Canceled},
		{name: "legacy import failure", kind: "protobuf", make: func(context.CancelFunc) Options {
			return Options{ImportProto: func(string, []byte) ([]byte, error) { return nil, pluginFailure }}
		}, is: pluginFailure},
		{name: "generated documents cannot request filesystem includes", kind: "protobuf", make: func(context.CancelFunc) Options {
			return Options{ImportProto: func(string, []byte) ([]byte, error) { return []byte(":::include source=\"notes.txt\"\n"), nil }}
		}, want: "include"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := manifest
			current.Pages = append([]Page(nil), manifest.Pages...)
			current.Pages[0].Kind = test.kind
			data, err := json.Marshal(current)
			require.NoError(t, err)
			writeModelFixture(t, root, "docs.json", string(data))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err = BuildContext(ctx, config, output, test.make(cancel))
			if test.is != nil {
				require.ErrorIs(t, err, test.is)
			} else {
				require.ErrorContains(t, err, test.want)
			}
			assert.Equal(t, before, compilerEdgeSnapshot(t, output))
		})
	}
}

func TestCompilerComposedAPIBudgetsPreservePublication(t *testing.T) {
	_, config, output, _, baseline := compilerEdgeProject(t)
	before := compilerEdgeSnapshot(t, output)
	tests := []struct {
		name        string
		description string
		markdown    string
		want        string
	}{
		{name: "combined heading count", markdown: strings.Repeat("# Heading\n", maxPageHeadings), want: "exceeds 8192 headings"},
		{name: "combined rendered bytes", description: strings.Repeat("d", maxSourceBytes), markdown: strings.Repeat("m", maxSourceBytes), want: "API reference exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := Options{ImportPage: func(ctx context.Context, kind, name string, data []byte, load func(string) ([]byte, error)) (ImportResult, error) {
				result, err := baseline.ImportPage(ctx, kind, name, data, load)
				if result.API != nil {
					result.API.Description = test.description
					if test.description != "" {
						result.API.Operations[0].Description = test.description
						result.API.Operations[0].Responses[0].Description = test.description
					}
				}
				result.Markdown = []byte(test.markdown)
				return result, err
			}}
			err := BuildContext(t.Context(), config, output, options)
			require.ErrorContains(t, err, test.want)
			assert.Equal(t, before, compilerEdgeSnapshot(t, output))
		})
	}
}

func TestCompilerRejectsUnavailableProjectAndDestinationPaths(t *testing.T) {
	root, config, output, _, baseline := compilerEdgeProject(t)
	before := compilerEdgeSnapshot(t, output)
	err := BuildContext(t.Context(), filepath.Join(root, "missing", "docs.json"), output, baseline)
	require.ErrorContains(t, err, "open project")
	err = BuildContext(t.Context(), config, filepath.Join(t.TempDir(), "invalid\x00destination", "site"), baseline)
	require.Error(t, err)
	assert.Equal(t, before, compilerEdgeSnapshot(t, output))
}

func TestCompilerCancellationWinsBeforeSourceIO(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	defer root.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	manifest := Manifest{Pages: []Page{{ID: "page", Title: "Page", Source: "missing.md", Path: "index.html", Kind: "markdown"}}}
	_, _, err = compileSourcePage(ctx, root, "", 0, manifest, Options{})
	require.ErrorIs(t, err, context.Canceled, "an already canceled compile must not inspect its source")
	_, _, err = compileLoadedPage(ctx, []byte("# Source\n"), 0, manifest, Options{}, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestCompilerOutputPathConflictsPreservePublishedSite(t *testing.T) {
	root, config, output, _, _ := compilerEdgeProject(t)
	before := compilerEdgeSnapshot(t, output)
	manifest := Manifest{Version: 1, Title: "Conflicting paths", Pages: []Page{{Title: "Home", Source: "home.md", Path: "index.html"}, {Title: "Group", Source: "group.md", Path: "group.html"}, {Title: "Nested", Source: "nested.md", Path: "group.html/nested.html"}}}
	for _, page := range manifest.Pages {
		writeModelFixture(t, root, page.Source, "# "+page.Title+"\n")
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	writeModelFixture(t, root, "docs.json", string(data))
	err = BuildContext(t.Context(), config, output, Options{})
	require.Error(t, err, "a public file cannot also be an output directory")
	assert.Equal(t, before, compilerEdgeSnapshot(t, output))
}

func TestCompilerRechecksOutputOwnershipImmediatelyBeforePublish(t *testing.T) {
	_, config, output, _, baseline := compilerEdgeProject(t)
	before := compilerEdgeSnapshot(t, output)
	options := Options{ImportPage: func(ctx context.Context, kind, name string, data []byte, load func(string) ([]byte, error)) (ImportResult, error) {
		result, err := baseline.ImportPage(ctx, kind, name, data, load)
		if err == nil {
			err = os.WriteFile(filepath.Join(output, "user-notes.txt"), []byte("Created while compilation was running.\n"), 0o600)
		}
		return result, err
	}}
	err := BuildContext(t.Context(), config, output, options)
	require.ErrorContains(t, err, "unmanaged")
	after := compilerEdgeSnapshot(t, output)
	assert.Equal(t, "Created while compilation was running.\n", after["user-notes.txt"])
	delete(after, "user-notes.txt")
	assert.Equal(t, before, after)
}

func TestCompilerAtomicPublishRestoresOutputWhenStageDisappears(t *testing.T) {
	_, _, output, _, _ := compilerEdgeProject(t)
	before := compilerEdgeSnapshot(t, output)
	err := publishStage(filepath.Join(t.TempDir(), "missing-stage"), output)
	require.Error(t, err)
	assert.Equal(t, before, compilerEdgeSnapshot(t, output))
	entries, err := os.ReadDir(filepath.Dir(output))
	require.NoError(t, err)
	require.Len(t, entries, 1, "rollback must not leave an orphaned previous publication")
	assert.Equal(t, filepath.Base(output), entries[0].Name())
}

func TestCompilerFailsIfAttachmentVanishesDuringImport(t *testing.T) {
	root, config, output, _, baseline := compilerEdgeProject(t)
	before := compilerEdgeSnapshot(t, output)
	options := Options{ImportPage: func(ctx context.Context, kind, name string, data []byte, load func(string) ([]byte, error)) (ImportResult, error) {
		result, err := baseline.ImportPage(ctx, kind, name, data, load)
		if err == nil {
			err = os.Remove(filepath.Join(root, "notes.txt"))
		}
		return result, err
	}}
	err := BuildContext(t.Context(), config, output, options)
	require.ErrorContains(t, err, "asset notes.txt")
	assert.Equal(t, before, compilerEdgeSnapshot(t, output))
}

func TestCompilerSourceLoaderLimitsAndCancellation(t *testing.T) {
	directory := t.TempDir()
	for index := 0; index < maxImportedDocuments; index++ {
		writeModelFixture(t, directory, fmt.Sprintf("fragment-%d.json", index), "{}")
	}
	require.NoError(t, os.Mkdir(filepath.Join(directory, "directory.json"), 0o750))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	load := confinedSourceLoader(t.Context(), root, "", "primary.yaml", []byte("primary snapshot"))
	for index := 0; index < maxImportedDocuments-1; index++ {
		data, err := load(fmt.Sprintf("fragment-%d.json", index))
		require.NoError(t, err)
		assert.Equal(t, "{}", string(data))
	}
	_, err = load(fmt.Sprintf("fragment-%d.json", maxImportedDocuments-1))
	require.ErrorContains(t, err, "exceeds 64 documents")
	readDirectory := confinedSourceLoader(t.Context(), root, "", "primary.yaml", nil)
	_, err = readDirectory("directory.json")
	require.ErrorContains(t, err, "regular file")
	ctx, cancel := context.WithCancel(t.Context())
	canceled := confinedSourceLoader(ctx, root, "", "primary.yaml", nil)
	cancel()
	_, err = canceled("fragment-0.json")
	require.ErrorIs(t, err, context.Canceled)
}

func TestCompilerPreviewRejectsConflictingIdentityAndMissingInputs(t *testing.T) {
	root := t.TempDir()
	manifest := Manifest{Version: 1, Title: "Preview identities", Pages: []Page{{ID: "home", Title: "Home", Source: "index.md", Path: "index.html"}, {ID: "guide", Title: "Guide", Source: "guide.md", Path: "guide.html"}}}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	config := writeModelFixture(t, root, "docs.json", string(data))
	writeModelFixture(t, root, "index.md", "# Home\n")
	writeModelFixture(t, root, "guide.md", "# Guide\n")
	page := Page{ID: "home", Title: "Ambiguous", Source: "guide.md", Path: "guide.html"}
	_, err = PreviewContext(t.Context(), config, page, []byte("# Draft\n"), PreviewOptions{})
	require.ErrorContains(t, err, "identity conflicts")
	page.ID, page.Source, page.Path = "new", "new.md", "index.html"
	_, err = PreviewContext(t.Context(), config, page, []byte("# Draft\n"), PreviewOptions{})
	require.ErrorContains(t, err, "duplicate path")
	page.Path = "new.html"
	_, err = PreviewContext(t.Context(), filepath.Join(root, "missing.json"), page, nil, PreviewOptions{})
	require.Error(t, err)
	_, err = PreviewContext(t.Context(), filepath.Join(root, "missing-directory", "docs.json"), page, nil, PreviewOptions{})
	require.ErrorContains(t, err, "open preview project")
	assert.Equal(t, "# Home\n", string(readModelFixture(t, filepath.Join(root, "index.md"))))
	assert.Equal(t, "# Guide\n", string(readModelFixture(t, filepath.Join(root, "guide.md"))))
}

func TestCompilerSearchTextRemainsUTF8WhenMergingAtItsByteLimit(t *testing.T) {
	content := pageContent{Text: strings.Repeat("x", (32<<10)-2), Anchors: map[string]bool{}}
	err := mergePageContent(&content, pageContent{Text: "世界"})
	require.NoError(t, err)
	assert.True(t, utf8.ValidString(content.Text))
	assert.Equal(t, strings.Repeat("x", (32<<10)-2)+" ", content.Text)
	assert.LessOrEqual(t, len(content.Text), 32<<10)
}

func TestCompilerModelEncodingRejectsEveryTruncatedBudget(t *testing.T) {
	type emptyMetadata struct{}
	values := []any{
		[]any{"a", true, nil}, [2]int{1, 2}, map[string]any{"a": "value"}, map[string]string(nil),
		json.RawMessage("{\"description\":\"line\u2028separator\u2029\"}"),
		struct {
			Count int
			Data  emptyMetadata `json:"data,omitempty"`
			hide  chan int
		}{Count: 7, hide: make(chan int)},
	}
	for index, value := range values {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			expected, err := json.Marshal(value)
			require.NoError(t, err)
			encoded, err := marshalModel(value, len(expected)+1)
			require.NoError(t, err)
			assert.Equal(t, string(expected)+"\n", string(encoded))
			for limit := 1; limit <= len(expected); limit++ {
				encoded, err := marshalModel(value, limit)
				require.Error(t, err, "encoded budget %d cannot hold %d bytes plus newline", limit, len(expected))
				assert.Nil(t, encoded, "preflight failure must not expose a partial model")
			}
		})
	}
}
