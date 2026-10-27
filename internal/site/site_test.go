package site

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func projectFixture(t *testing.T) (string, string, Manifest) {
	t.Helper()
	project := t.TempDir()
	output := filepath.Join(t.TempDir(), "site")
	manifest := Manifest{Version: 1, Title: "Example <docs>", Description: "Guides and API reference", Pages: []Page{
		{Title: "Welcome", Source: "index.md", Path: "index.html"},
		{Title: "Setup", Source: "guides/setup.md", Path: "guide/setup.html"},
	}, Assets: []string{"media/readme.txt"}}
	writeTestFile(t, project, "index.md", "# Welcome\n\n[Setup](guides/setup.md#привет)\n\n[Download](media/readme.txt)\n\n<script>alert(1)</script>\n")
	writeTestFile(t, project, "guides/setup.md", "# Setup\n\n## Привет\n\n[Home](../index.md)\n\n[API type](#demo.Type)\n\n<a name=\"demo.Type\"></a>\n\n## Привет\n\n```go\nfmt.Println(\"hello\")\n```\n")
	writeTestFile(t, project, "media/readme.txt", "Downloadable text")
	writeManifest(t, project, manifest)
	return project, output, manifest
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
}

func writeManifest(t *testing.T, root string, manifest Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	writeTestFile(t, root, "docs.json", string(data))
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	require.NoError(t, filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(data)
		return nil
	}))
	return files
}

func TestBuildPortableSite(t *testing.T) {
	project, output, _ := projectFixture(t)
	config := filepath.Join(project, "docs.json")
	require.NoError(t, Build(config, output, Options{}))
	files := snapshot(t, output)
	home := files["index.html"]
	guide := files["guide/setup.html"]
	assert.Contains(t, home, "Example &lt;docs&gt;")
	assert.Contains(t, home, `href="guide/setup.html#%D0%BF%D1%80%D0%B8%D0%B2%D0%B5%D1%82"`)
	assert.Contains(t, home, `href="assets/media/readme.txt"`)
	assert.NotContains(t, home, "<script>alert")
	assert.Contains(t, home, "Content-Security-Policy")
	assert.Contains(t, guide, `href="../_docs/site.css"`)
	assert.Contains(t, guide, `href="../index.html"`)
	assert.Contains(t, guide, `id="привет"`)
	assert.Contains(t, guide, `id="привет-1"`)
	assert.Contains(t, guide, `id="demo.Type"`)
	assert.Contains(t, guide, `class="language-go"`)
	assert.Contains(t, guide, `aria-current="page"`)
	assert.Contains(t, guide, `aria-label="On this page"`)
	assert.Equal(t, "Downloadable text", files["assets/media/readme.txt"])
	var index []searchEntry
	require.NoError(t, json.Unmarshal([]byte(files["_docs/search.json"]), &index))
	require.Len(t, index, 2)
	assert.Contains(t, index[1].Text, "Привет")
	assert.Equal(t, "guide/setup.html", index[1].URL)
	require.NoError(t, Build(config, output, Options{Check: true}))
	assert.Equal(t, files, snapshot(t, output))
	require.NoError(t, Build(config, output, Options{}))
	assert.Equal(t, files, snapshot(t, output), "repeated builds must be byte-identical")
}

func TestBuildErrorsPreserveOutput(t *testing.T) {
	for _, content := range []string{
		"# Welcome\n\n[Missing](missing.md)",
		"# Welcome\n\n[Bad fragment](guides/setup.md#missing)",
		"# Welcome\n\n[Escape](../outside.md)",
		"# Welcome\n\n![Missing image](missing.png)",
		"# Welcome\n\n[Unsafe](javascript:alert%281%29)",
		"# Welcome {#_docs-main}",
		"# One {#same}\n\n# Two {#same}",
		string([]byte{0xff}),
	} {
		t.Run(content, func(t *testing.T) {
			project, output, _ := projectFixture(t)
			config := filepath.Join(project, "docs.json")
			require.NoError(t, Build(config, output, Options{}))
			before := snapshot(t, output)
			writeTestFile(t, project, "index.md", content)
			require.Error(t, Build(config, output, Options{}))
			assert.Equal(t, before, snapshot(t, output))
			entries, err := os.ReadDir(filepath.Dir(output))
			require.NoError(t, err)
			assert.Len(t, entries, 1, "failed builds must remove staging directories")
		})
	}
}

func TestBuildCheckDoesNotCreateOrWrite(t *testing.T) {
	project, output, _ := projectFixture(t)
	config := filepath.Join(project, "docs.json")
	missing := filepath.Join(output, "missing", "site")
	require.Error(t, Build(config, missing, Options{Check: true}))
	_, err := os.Stat(output)
	assert.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, Build(config, output, Options{}))
	writeTestFile(t, output, "index.html", "stale")
	before := snapshot(t, output)
	require.ErrorContains(t, Build(config, output, Options{Check: true}), "out of date")
	assert.Equal(t, before, snapshot(t, output))
}

func TestManagedOutputAndStalePages(t *testing.T) {
	project, output, manifest := projectFixture(t)
	config := filepath.Join(project, "docs.json")
	writeTestFile(t, output, "user.txt", "keep me")
	require.ErrorContains(t, Build(config, output, Options{}), "unmanaged")
	assert.Equal(t, "keep me", snapshot(t, output)["user.txt"])
	require.NoError(t, os.RemoveAll(output))
	require.NoError(t, Build(config, output, Options{}))
	manifest.Pages = manifest.Pages[:1]
	manifest.Pages[0].Source = "home.md"
	writeTestFile(t, project, "home.md", "# Home")
	writeManifest(t, project, manifest)
	require.Error(t, Build(config, output, Options{Check: true}))
	require.NoError(t, Build(config, output, Options{}))
	_, err := os.Stat(filepath.Join(output, "guide/setup.html"))
	assert.ErrorIs(t, err, fs.ErrNotExist)
	writeTestFile(t, output, "extra.txt", "unmanaged")
	require.ErrorContains(t, Build(config, output, Options{}), "unmanaged")
	require.ErrorContains(t, Build(config, output, Options{Check: true}), "unmanaged")
}

func TestBuildBoundsAndConfinement(t *testing.T) {
	t.Run("source size", func(t *testing.T) {
		project, output, _ := projectFixture(t)
		writeTestFile(t, project, "index.md", strings.Repeat("x", maxSourceBytes+1))
		require.ErrorContains(t, Build(filepath.Join(project, "docs.json"), output, Options{}), "at most")
	})
	t.Run("outside source symlink", func(t *testing.T) {
		project, output, _ := projectFixture(t)
		outside := filepath.Join(t.TempDir(), "outside.md")
		require.NoError(t, os.WriteFile(outside, []byte("# Secret"), 0o600))
		require.NoError(t, os.Remove(filepath.Join(project, "index.md")))
		if err := os.Symlink(outside, filepath.Join(project, "index.md")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		require.ErrorContains(t, Build(filepath.Join(project, "docs.json"), output, Options{}), "escapes")
	})
	t.Run("project output", func(t *testing.T) {
		project, _, _ := projectFixture(t)
		require.ErrorContains(t, Build(filepath.Join(project, "docs.json"), project, Options{}), "cannot replace")
		require.ErrorContains(t, Build(filepath.Join(project, "docs.json"), filepath.Dir(project), Options{}), "cannot replace")
	})
	t.Run("output contains source", func(t *testing.T) {
		project, _, _ := projectFixture(t)
		require.ErrorContains(t, Build(filepath.Join(project, "docs.json"), filepath.Join(project, "guides"), Options{}), "contains source")
	})
	t.Run("directory source", func(t *testing.T) {
		project, output, manifest := projectFixture(t)
		manifest.Pages[0].Source = "guides"
		writeManifest(t, project, manifest)
		require.ErrorContains(t, Build(filepath.Join(project, "docs.json"), output, Options{}), "regular file")
	})
	t.Run("empty output", func(t *testing.T) {
		project, _, _ := projectFixture(t)
		require.ErrorContains(t, Build(filepath.Join(project, "docs.json"), "", Options{}), "required")
	})
}

func TestProtobufImporter(t *testing.T) {
	project, output, manifest := projectFixture(t)
	manifest.Pages = []Page{{Title: "API", Source: "api.proto", Path: "index.html", Kind: "protobuf"}}
	writeManifest(t, project, manifest)
	writeTestFile(t, project, "api.proto", `syntax = "proto3"; message Example { string value = 1; }`)
	config := filepath.Join(project, "docs.json")
	require.ErrorContains(t, Build(config, output, Options{}), "importer is unavailable")
	require.ErrorContains(t, Build(config, output, Options{ImportProto: func(string, []byte) ([]byte, error) {
		return nil, assert.AnError
	}}), "import api.proto")
	require.NoError(t, Build(config, output, Options{ImportProto: func(name string, source []byte) ([]byte, error) {
		assert.Equal(t, "api.proto", name)
		assert.Contains(t, string(source), "message Example")
		return []byte("# API\n\nGenerated documentation"), nil
	}}))
	assert.Contains(t, snapshot(t, output)["index.html"], "Generated documentation")
}

func TestMarkdownLinkContract(t *testing.T) {
	manifest := Manifest{Pages: []Page{{Title: "Page", Source: "guide/page.md", Path: "guide/page.html"}}, Assets: []string{"image.png"}}
	for input, expected := range map[string]string{
		"https://example.com/docs?q=1#intro": "https://example.com/docs?q=1#intro",
		"mailto:help@example.com":            "mailto:help@example.com",
		"#heading":                           "#heading",
		"../image.png":                       "../assets/image.png",
		"page.md?q=1#heading":                "page.html?q=1#heading",
	} {
		actual, _, err := resolveLink(input, 0, manifest, false)
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	}
	for _, input := range []string{"%zz", "/absolute.md", `..\outside.md`, "file:///etc/passwd", "//example.com", "missing.md"} {
		_, _, err := resolveLink(input, 0, manifest, false)
		require.Error(t, err, input)
	}
	_, _, err := resolveLink("mailto:x@example.com", 0, manifest, true)
	require.Error(t, err)
	assert.Equal(t, "../../_docs/site.css", relativeURL("deep/guide/page.html", "_docs/site.css"))
	assert.Equal(t, "other.html", relativeURL("deep/guide/page.html", "deep/guide/other.html"))
}

func TestManifestValidation(t *testing.T) {
	for _, input := range []string{
		`{`, `{} {}`, `{"version":1,"unknown":true}`, `{"version":2,"title":"Docs","pages":[]}`,
		`{"version":1,"title":"","pages":[]}`,
		`{"version":1,"title":"Docs","pages":[]}`,
		`{"version":1,"title":"Docs","pages":[{"title":"Home","source":"home.md","path":"home.html"}]}`,
		`{"version":1,"title":"Docs","pages":[{"title":"","source":"home.md","path":"index.html"}]}`,
		`{"version":1,"title":"Docs","pages":[{"title":"Home","source":"../secret.md","path":"index.html"}]}`,
		`{"version":1,"title":"Docs","pages":[{"title":"Home","source":"home.md","path":"Index.html"}]}`,
		`{"version":1,"title":"Docs","pages":[{"title":"Home","source":"home.md","path":"index.html","kind":"unknown"}]}`,
		`{"version":1,"title":"Docs","pages":[{"title":"Home","source":"home.md","path":"index.html"},{"title":"Other","source":"home.md","path":"other.html"}]}`,
		`{"version":1,"title":"Docs","pages":[{"title":"Home","source":"home.md","path":"index.html"}],"assets":["script.js"]}`,
		`{"version":1,"title":"Docs","pages":[{"title":"Home","source":"home.md","path":"index.html"}],"assets":["a.png","A.png"]}`,
	} {
		t.Run(input, func(t *testing.T) {
			root, err := os.OpenRoot(t.TempDir())
			require.NoError(t, err)
			defer root.Close()
			require.NoError(t, root.WriteFile("docs.json", []byte(input), 0o600))
			_, err = loadManifest(root, "docs.json")
			require.Error(t, err)
		})
	}
	for _, name := range []string{"../outside", "/absolute", `a\b`, "a:b", "NUL.txt", "dir/COM1", "trailing.", "trailing ", ".", "a\x00b", "a%20b"} {
		assert.False(t, localPath(name), name)
	}
	assert.False(t, outputPath("_docs/index.html"))
	assert.False(t, outputPath("assets/index.html"))
}

func BenchmarkCompileMarkdown(b *testing.B) {
	source := []byte("# Documentation\n\n" + strings.Repeat("## Section\n\nUseful documentation with **emphasis**, `code` and tables.\n\n", 100))
	manifest := Manifest{Pages: []Page{{Title: "Documentation", Source: "index.md", Path: "index.html"}}}
	b.ReportAllocs()
	b.SetBytes(int64(len(source)))
	for b.Loop() {
		if _, err := compileMarkdown(source, 0, manifest); err != nil {
			b.Fatal(err)
		}
	}
}
