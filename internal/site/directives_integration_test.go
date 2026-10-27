package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const directiveGuide = `# Manual

:::warning title="Sources first"
Never edit published output.
:::

:::tabs title="Build choices"
:::tab title="CLI"
The command-line pathway is available without JavaScript.
:::
:::tab title="Container"
### Container workflow {#container-workflow}

The container pathway is also available without JavaScript.
:::
:::

:::include source="snippets/part.md"
:::

:::include source="snippets/request.json" language="json"
:::
`

const directiveSnippet = `## Included summary {#included-summary}

The reusable-snippet-needle belongs to this included file.

[Deployment](../deploy.md#docker)

![Diagram](diagram.png)
`

func writeDirectiveProject(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	writeDirectiveSource(t, root, "docs.json", `{"version":1,"title":"Manual","pages":[{"title":"Manual","source":"docs/guide.md","path":"index.html"},{"title":"Deployment","source":"docs/deploy.md","path":"ops/deploy.html"}],"assets":["docs/snippets/diagram.png"]}`)
	writeDirectiveSource(t, root, "docs/guide.md", directiveGuide)
	writeDirectiveSource(t, root, "docs/deploy.md", "# Docker {#docker}\n\nDeployment instructions.\n")
	writeDirectiveSource(t, root, "docs/snippets/part.md", directiveSnippet)
	writeDirectiveSource(t, root, "docs/snippets/request.json", "{\"literal\":\"<script>bad</script>\",\"directive\":\":::warning\"}\n")
	writeDirectiveSource(t, root, "docs/snippets/diagram.png", "fixture image bytes")
	return root, filepath.Join(root, "published")
}

func writeDirectiveSource(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
}

func exportedDirectiveBlocks(t *testing.T, output string) []document.Block {
	t.Helper()
	directory := filepath.Join(output, "_docs", "pages")
	files, err := os.ReadDir(directory)
	require.NoError(t, err)
	for _, file := range files {
		data, readErr := os.ReadFile(filepath.Join(directory, file.Name()))
		require.NoError(t, readErr)
		var page struct {
			Path   string           `json:"path"`
			Blocks []document.Block `json:"blocks"`
		}
		require.NoError(t, json.Unmarshal(data, &page))
		if page.Path == "index.html" {
			return page.Blocks
		}
	}
	t.Fatal("the primary page model was not exported")
	return nil
}

func TestDirectiveProjectPublishesReusableContent(t *testing.T) {
	root, output := writeDirectiveProject(t)
	config := filepath.Join(root, "docs.json")
	require.NoError(t, Build(config, output, Options{}))
	data, err := os.ReadFile(filepath.Join(output, "index.html"))
	require.NoError(t, err)
	page := string(data)
	assert.Contains(t, page, "Never edit published output.")
	assert.Contains(t, page, "The command-line pathway is available without JavaScript.")
	assert.Contains(t, page, "The container pathway is also available without JavaScript.")
	assert.Contains(t, page, `href="#container-workflow"`)
	assert.Contains(t, page, `href="#included-summary"`)
	assert.Contains(t, page, `href="ops/deploy.html#docker"`)
	assert.Contains(t, page, `src="assets/docs/snippets/diagram.png"`)
	assert.Contains(t, page, "&lt;script&gt;bad&lt;/script&gt;")
	assert.NotContains(t, page, "<script>bad</script>")
	assert.NotContains(t, page, ":::include source=")
	search, err := os.ReadFile(filepath.Join(output, "_docs", "search.json"))
	require.NoError(t, err)
	assert.Contains(t, string(search), "reusable-snippet-needle")

	blocks := exportedDirectiveBlocks(t, output)
	var markdownInclude, codeInclude *document.Block
	for i := range blocks {
		if blocks[i].Kind != document.BlockInclude {
			continue
		}
		switch blocks[i].IncludeSource {
		case "docs/snippets/part.md":
			markdownInclude = &blocks[i]
		case "docs/snippets/request.json":
			codeInclude = &blocks[i]
		}
	}
	require.NotNil(t, markdownInclude)
	require.NotNil(t, codeInclude)
	assert.Equal(t, "docs/guide.md", markdownInclude.Source.File)
	assert.Positive(t, markdownInclude.Source.Line)
	require.NotEmpty(t, markdownInclude.Children)
	assert.Equal(t, "docs/snippets/part.md", markdownInclude.Children[0].Source.File)
	assert.Equal(t, 1, markdownInclude.Children[0].Source.Line)
	assert.False(t, markdownInclude.Children[0].Source.Generated)
	require.Len(t, codeInclude.Children, 1)
	assert.Equal(t, document.BlockCode, codeInclude.Children[0].Kind)
	assert.Equal(t, "docs/snippets/request.json", codeInclude.Children[0].Source.File)
	assert.Equal(t, "json", codeInclude.Children[0].Language)
	assert.Contains(t, codeInclude.Children[0].Text, ":::warning")
	before, err := os.Stat(filepath.Join(output, "index.html"))
	require.NoError(t, err)
	require.NoError(t, Build(config, output, Options{Check: true}))
	after, err := os.Stat(filepath.Join(output, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())
}

func TestDirectiveFailureKeepsPublishedProject(t *testing.T) {
	root, output := writeDirectiveProject(t)
	config := filepath.Join(root, "docs.json")
	require.NoError(t, Build(config, output, Options{}))
	before, err := os.ReadFile(filepath.Join(output, "index.html"))
	require.NoError(t, err)
	cases := []struct {
		name    string
		file    string
		content string
	}{
		{name: "escaped source", file: "docs/guide.md", content: "# Manual\n\n:::include source=\"../../outside.md\"\n:::\n"},
		{name: "cycle", file: "docs/snippets/part.md", content: ":::include source=\"part.md\"\n:::\n"},
		{name: "unknown directive in include", file: "docs/snippets/part.md", content: "First paragraph.\n\n:::unknown\n:::\n"},
		{name: "unclosed callout", file: "docs/snippets/part.md", content: ":::note\nAn incomplete note.\n"},
		{name: "tab outside group", file: "docs/guide.md", content: "# Manual\n\n:::tab title=\"Misplaced\"\nText.\n:::\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			writeDirectiveSource(t, root, test.file, test.content)
			err := Build(config, output, Options{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.file)
			assert.Regexp(t, `:[0-9]+:[0-9]+`, err.Error())
			after, readErr := os.ReadFile(filepath.Join(output, "index.html"))
			require.NoError(t, readErr)
			assert.Equal(t, before, after)
			writeDirectiveSource(t, root, "docs/guide.md", directiveGuide)
			writeDirectiveSource(t, root, "docs/snippets/part.md", directiveSnippet)
		})
	}
	require.NoError(t, Build(config, output, Options{Check: true}))
}
