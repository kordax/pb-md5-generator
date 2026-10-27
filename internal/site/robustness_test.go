package site

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeadingIDsRemainUniqueAndLinear(t *testing.T) {
	ids := &headingIDs{used: make(map[string]bool)}
	ids.Put([]byte("repeated-1"))
	assert.Equal(t, "repeated", string(ids.Generate([]byte("Repeated"), 0)))
	assert.Equal(t, "repeated-2", string(ids.Generate([]byte("Repeated"), 0)))
	assert.Equal(t, "repeated-1-1", string(ids.Generate([]byte("Repeated-1"), 0)))
	for i := 3; i < 10000; i++ {
		require.Equal(t, fmt.Sprintf("repeated-%d", i), string(ids.Generate([]byte("Repeated"), 0)))
	}
	assert.Equal(t, "section", string(ids.Generate([]byte("!!!"), 0)))
}

func TestAmbiguousManifestAndLimits(t *testing.T) {
	for _, input := range []string{
		`{"version":1,"version":2}`,
		`{"pages":[{"title":"first","title":"second"}]}`,
		strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18),
		`{"pages": [}`, `{"title":`, `{"title": [}`, `[]`, `null`,
		string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}),
	} {
		t.Run(input, func(t *testing.T) {
			project := t.TempDir()
			writeTestFile(t, project, "docs.json", input)
			root, err := os.OpenRoot(project)
			require.NoError(t, err)
			defer root.Close()
			_, err = loadManifest(root, "docs.json")
			require.Error(t, err)
		})
	}
	_, _, valid := projectFixture(t)
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.Description = strings.Repeat("x", 4097) },
		func(m *Manifest) { m.Title = strings.Repeat("x", 257) },
		func(m *Manifest) { m.Pages = make([]Page, maxPages+1) },
		func(m *Manifest) { m.Assets = make([]string, maxAssets+1) },
	} {
		copy := valid
		mutate(&copy)
		require.Error(t, copy.validate())
	}
}

func TestSpecialAndUnmanagedOutput(t *testing.T) {
	project, output, _ := projectFixture(t)
	config := filepath.Join(project, "docs.json")
	writeTestFile(t, filepath.Dir(output), filepath.Base(output), "ordinary file")
	require.ErrorContains(t, Build(config, output, Options{}), "directory")
	require.NoError(t, os.Remove(output))
	require.NoError(t, os.Mkdir(output, 0o755))
	require.NoError(t, Build(config, output, Options{}))
	writeTestFile(t, output, inventoryPath, "not JSON")
	require.ErrorContains(t, Build(config, output, Options{}), "invalid build inventory")
	writeTestFile(t, output, inventoryPath, `{"format":"pb-docs-site/v1","files":["../escape"]}`)
	require.ErrorContains(t, Build(config, output, Options{}), "invalid path")
	require.NoError(t, os.RemoveAll(output))
	require.NoError(t, Build(config, output, Options{}))
	require.NoError(t, os.Mkdir(filepath.Join(output, "unexpected"), 0o755))
	require.ErrorContains(t, Build(config, output, Options{}), "unmanaged directory")
	require.NoError(t, os.Remove(filepath.Join(output, "unexpected")))
	if err := os.Symlink(filepath.Join(output, "index.html"), filepath.Join(output, "unexpected")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.ErrorContains(t, Build(config, output, Options{}), "symlink")
	require.NoError(t, os.Remove(filepath.Join(output, "unexpected")))
	alias := filepath.Join(filepath.Dir(output), "alias")
	require.NoError(t, os.Symlink(output, alias))
	require.ErrorContains(t, Build(config, alias, Options{}), "symlink")
}

func TestReadBoundsAndMarkdownVariants(t *testing.T) {
	project := t.TempDir()
	root, err := os.OpenRoot(project)
	require.NoError(t, err)
	defer root.Close()
	_, err = readLimited(root, "missing", 1)
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, root.WriteFile("large", []byte("123"), 0o600))
	_, err = readLimited(root, "large", 2)
	require.Error(t, err)
	_, err = readLimited(root, "large", 3)
	require.NoError(t, err)
	manifest := Manifest{Pages: []Page{{Title: "Page", Source: "page.md", Path: "index.html"}}, Assets: []string{"a.png"}}
	source := []byte("## Heading {#stable}\n\nA soft\nline with a hard  \nbreak and https://example.com.\n\n    code block\n\n![image](a.png)\n")
	content, err := compileMarkdown(source, 0, manifest)
	require.NoError(t, err)
	assert.Contains(t, content.HTML, `id="stable"`)
	assert.Contains(t, content.HTML, `src="assets/a.png"`)
	assert.Contains(t, content.Text, "soft line with a hard break")
	assert.Contains(t, content.Text, "code block")
	assert.Contains(t, content.Text, "https://example.com")
	long := []byte(strings.Repeat("д", 33000))
	content, err = compileMarkdown(long, 0, manifest)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(content.Text), 32<<10)
	html, err := renderPage(manifest, 0, content)
	require.NoError(t, err)
	assert.Contains(t, string(html), "<h1>Page</h1>")
}

func TestMarkdownExpansionBudgets(t *testing.T) {
	manifest := Manifest{Pages: []Page{{Title: "Page", Source: "page.md", Path: "index.html"}}}
	_, err := compileMarkdown([]byte(strings.Repeat("# Heading\n\n", 200000)), 0, manifest)
	require.ErrorContains(t, err, "complexity")
	_, err = compileMarkdown([]byte(strings.Repeat("# Heading\n\n", maxPageHeadings+1)), 0, manifest)
	require.ErrorContains(t, err, "headings")
	_, err = compileMarkdown([]byte(strings.Repeat("- a\n", 20000)), 0, manifest)
	require.ErrorContains(t, err, "AST")
	_, err = compileMarkdown([]byte("![external](http://example.test/image.png)"), 0, manifest)
	require.ErrorContains(t, err, "require HTTPS")
	buffer := boundedBuffer{limit: 3}
	_, err = buffer.WriteString("12")
	require.NoError(t, err)
	_, err = buffer.Write([]byte("3"))
	require.NoError(t, err)
	_, err = buffer.WriteString("4")
	require.ErrorContains(t, err, "exceeds")
	_, err = buffer.Write([]byte("4"))
	require.ErrorContains(t, err, "exceeds")
	assert.Equal(t, "123", buffer.String())
}

func TestProjectMetadataBudget(t *testing.T) {
	project, output, _ := projectFixture(t)
	manifest := Manifest{Version: 1, Title: "Corpus"}
	var body strings.Builder
	for i := range maxPageHeadings {
		fmt.Fprintf(&body, "# Heading %d\n\n", i)
	}
	for i := range 13 {
		source, destination := fmt.Sprintf("page-%d.md", i), fmt.Sprintf("page-%d.html", i)
		if i == 0 {
			destination = "index.html"
		}
		manifest.Pages = append(manifest.Pages, Page{Title: "Page", Source: source, Path: destination})
		writeTestFile(t, project, source, body.String())
	}
	writeManifest(t, project, manifest)
	require.ErrorContains(t, Build(filepath.Join(project, "docs.json"), output, Options{}), "link metadata")
	_, err := os.Stat(output)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestPageMetadataDoesNotRetainLargeBackingStrings(t *testing.T) {
	for _, kind := range []string{"search text", "short search text", "link fragment"} {
		t.Run(kind, func(t *testing.T) {
			project, output, _ := projectFixture(t)
			manifest := Manifest{Version: 1, Title: "Memory corpus"}
			for i := range 16 {
				name, destination := fmt.Sprintf("page-%d.proto", i), fmt.Sprintf("page-%d.html", i)
				if i == 0 {
					destination = "index.html"
				}
				manifest.Pages = append(manifest.Pages, Page{Title: "Page", Source: name, Path: destination, Kind: "protobuf"})
				writeTestFile(t, project, name, "source snapshot")
			}
			writeManifest(t, project, manifest)
			body := strings.Repeat("x", 2<<20)
			if kind == "short search text" {
				body = "    " + strings.Repeat(" ", 2<<20) + "x\n"
			}
			if kind == "link fragment" {
				body = "# Target\n\n[link](?q=" + body + "#target)"
			}
			markdown := []byte(body)
			runtime.GC()
			var baseline, during runtime.MemStats
			runtime.ReadMemStats(&baseline)
			calls := 0
			err := Build(filepath.Join(project, "docs.json"), output, Options{ImportProto: func(string, []byte) ([]byte, error) {
				calls++
				if calls == len(manifest.Pages) {
					runtime.GC()
					runtime.ReadMemStats(&during)
				}
				return markdown, nil
			}})
			require.NoError(t, err)
			assert.Less(t, int64(during.HeapAlloc)-int64(baseline.HeapAlloc), int64(12<<20), "bounded metadata must not retain 2 MiB backing strings per page")
		})
	}
}

func TestRepeatedBuildResources(t *testing.T) {
	project, output, _ := projectFixture(t)
	config := filepath.Join(project, "docs.json")
	require.NoError(t, Build(config, output, Options{}))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	goroutines := runtime.NumGoroutine()
	fdsBefore, fdErr := os.ReadDir("/proc/self/fd")
	for range 100 {
		require.NoError(t, Build(config, output, Options{}))
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	assert.Less(t, retained, int64(8<<20), "retained heap must plateau across repeated builds")
	assert.LessOrEqual(t, runtime.NumGoroutine(), goroutines+2)
	if fdErr == nil {
		fdsAfter, err := os.ReadDir("/proc/self/fd")
		require.NoError(t, err)
		assert.LessOrEqual(t, len(fdsAfter), len(fdsBefore))
	}
	t.Logf("100 builds: retained heap delta %d bytes; goroutines %d -> %d", retained, goroutines, runtime.NumGoroutine())
}

func BenchmarkRepeatedHeadings(b *testing.B) {
	for _, count := range []int{1000, 8000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			source := []byte(strings.Repeat("# Repeated\n\n", count))
			manifest := Manifest{Pages: []Page{{Title: "Page", Source: "index.md", Path: "index.html"}}}
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				if _, err := compileMarkdown(source, 0, manifest); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkBuild1000Pages(b *testing.B) {
	project, output := b.TempDir(), filepath.Join(b.TempDir(), "site")
	manifest := Manifest{Version: 1, Title: "Corpus"}
	body := "# Documentation\n\n" + strings.Repeat("A guide, runbook or reference page. ", 120)
	for i := range 1000 {
		name := fmt.Sprintf("page-%d.md", i)
		out := fmt.Sprintf("page-%d.html", i)
		if i == 0 {
			out = "index.html"
		}
		manifest.Pages = append(manifest.Pages, Page{Title: fmt.Sprintf("Page %d", i), Source: name, Path: out})
		if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		b.Fatal(err)
	}
	config := filepath.Join(project, "docs.json")
	if err := os.WriteFile(config, data, 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(body) * len(manifest.Pages)))
	for b.Loop() {
		if err := Build(config, output, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
