package document_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func directiveSchemaPage(t *testing.T, block string) any {
	t.Helper()
	page := modelSchemaData(t, schemaPage()).(map[string]any)
	node := decodeSchemaData(t, []byte(block)).(map[string]any)
	node["source"] = map[string]any{"file": "guide.md"}
	page["blocks"] = []any{node}
	return page
}

func TestPageSchemaDirectiveStructure(t *testing.T) {
	schema := compileDocumentationSchema(t, "page")
	validTab := `{"kind":"tab","title":"Go","source":{"file":"guide.md"}}`
	paragraph := `{"kind":"paragraph","source":{"file":"snippets/intro.md","line":1,"column":1},"inlines":[{"kind":"text","text":"A reusable paragraph"}]}`
	code := `{"kind":"code","source":{"file":"snippets/request.json","line":1,"column":1},"language":"json","text":"{}"}`
	for name, fixture := range map[string]string{
		"callout default title":  `{"kind":"callout","variant":"note","title":"Note"}`,
		"callout children":       `{"kind":"callout","variant":"warning","title":"Before deploy","children":[` + paragraph + `]}`,
		"labeled tab":            `{"kind":"tabs","id":"tabs-1","children":[` + validTab + `]}`,
		"nested tabs":            `{"kind":"tabs","children":[{"kind":"tab","title":"Go","source":{"file":"guide.md"},"children":[{"kind":"tabs","children":[` + validTab + `],"source":{"file":"guide.md"}}]}]}`,
		"empty Markdown include": `{"kind":"include","includeSource":"snippets/empty.md"}`,
		"Markdown include":       `{"kind":"include","includeSource":"snippets/intro.md","children":[` + paragraph + `]}`,
		"code include":           `{"kind":"include","includeSource":"snippets/request.json","language":"json","children":[` + code + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, schema.Validate(directiveSchemaPage(t, fixture)))
		})
	}
	for name, fixture := range map[string]string{
		"callout missing variant":     `{"kind":"callout","title":"Note"}`,
		"callout missing title":       `{"kind":"callout","variant":"note"}`,
		"unknown variant":             `{"kind":"callout","variant":"notice","title":"Notice"}`,
		"blank callout title":         `{"kind":"callout","variant":"note","title":" \t "}`,
		"long callout title":          `{"kind":"callout","variant":"note","title":"` + strings.Repeat("x", 257) + `"}`,
		"callout raw text":            `{"kind":"callout","variant":"note","title":"Note","text":"not a typed child"}`,
		"callout inline content":      `{"kind":"callout","variant":"note","title":"Note","inlines":[]}`,
		"callout language":            `{"kind":"callout","variant":"note","title":"Note","language":"go"}`,
		"tabs missing children":       `{"kind":"tabs"}`,
		"empty tabs":                  `{"kind":"tabs","children":[]}`,
		"tabs ordinary block":         `{"kind":"tabs","children":[` + paragraph + `]}`,
		"standalone tab":              validTab,
		"tab under quote":             `{"kind":"quote","children":[` + validTab + `]}`,
		"unlabeled tab":               `{"kind":"tabs","children":[{"kind":"tab","source":{"file":"guide.md"}}]}`,
		"blank tab title":             `{"kind":"tabs","children":[{"kind":"tab","title":"\n\t","source":{"file":"guide.md"}}]}`,
		"long tab title":              `{"kind":"tabs","children":[{"kind":"tab","title":"` + strings.Repeat("x", 257) + `","source":{"file":"guide.md"}}]}`,
		"tab unknown field":           `{"kind":"tabs","children":[{"kind":"tab","title":"Go","variant":"note","source":{"file":"guide.md"}}]}`,
		"tabs raw content":            `{"kind":"tabs","text":"hidden content","children":[` + validTab + `]}`,
		"too many tabs":               `{"kind":"tabs","children":[` + strings.TrimSuffix(strings.Repeat(validTab+",", 33), ",") + `]}`,
		"include missing source":      `{"kind":"include"}`,
		"include empty source":        `{"kind":"include","includeSource":""}`,
		"include source type":         `{"kind":"include","includeSource":5}`,
		"include unknown field":       `{"kind":"include","includeSource":"x.md","execute":true}`,
		"code include no child":       `{"kind":"include","includeSource":"x.txt","language":"text"}`,
		"code include empty child":    `{"kind":"include","includeSource":"x.txt","language":"text","children":[]}`,
		"code include prose child":    `{"kind":"include","includeSource":"x.txt","language":"text","children":[` + paragraph + `]}`,
		"code include two children":   `{"kind":"include","includeSource":"x.txt","language":"text","children":[` + code + `,` + code + `]}`,
		"include empty language":      `{"kind":"include","includeSource":"x.txt","language":"","children":[` + code + `]}`,
		"include long language":       `{"kind":"include","includeSource":"x.txt","language":"` + strings.Repeat("x", 33) + `","children":[` + code + `]}`,
		"include direct text":         `{"kind":"include","includeSource":"x.txt","text":"text belongs in children"}`,
		"include title":               `{"kind":"include","includeSource":"x.txt","title":"Ignored title"}`,
		"paragraph directive variant": `{"kind":"paragraph","variant":"note"}`,
		"paragraph directive title":   `{"kind":"paragraph","title":"Note"}`,
		"paragraph include source":    `{"kind":"paragraph","includeSource":"x.md"}`,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, schema.Validate(directiveSchemaPage(t, fixture)))
		})
	}
}

func TestDirectiveSchemaTitleAndLanguageGrammar(t *testing.T) {
	schema := compileDocumentationSchema(t, "page")
	for _, test := range []struct {
		name  string
		title string
	}{
		{"C0 control", "Title\x01"},
		{"trailing newline", "Title\n"},
		{"DEL control", "Title\x7f"},
		{"C1 control", "Title\u0085"},
		{"Unicode whitespace", "\u00a0\u1680\u2003\u2028\u202f\u205f\u3000"},
	} {
		t.Run(test.name, func(t *testing.T) {
			title, err := json.Marshal(test.title)
			require.NoError(t, err)
			for _, fixture := range []string{
				`{"kind":"callout","variant":"note","title":` + string(title) + `}`,
				`{"kind":"tabs","children":[{"kind":"tab","source":{"file":"guide.md"},"title":` + string(title) + `}]}`,
			} {
				assert.Error(t, schema.Validate(directiveSchemaPage(t, fixture)))
			}
		})
	}
	for _, language := range []string{"go", "c++", "protobuf.v3", strings.Repeat("x", 32)} {
		fixture := `{"kind":"include","includeSource":"snippet.txt","language":"` + language + `","children":[{"kind":"code","source":{"file":"snippet.txt"}}]}`
		require.NoError(t, schema.Validate(directiveSchemaPage(t, fixture)))
	}
	for _, language := range []string{"-sh", "_go", "go/html", "go html", "go#", "go\n", "é", strings.Repeat("x", 33)} {
		value, err := json.Marshal(language)
		require.NoError(t, err)
		fixture := `{"kind":"include","includeSource":"snippet.txt","language":` + string(value) + `,"children":[{"kind":"code","source":{"file":"snippet.txt"}}]}`
		assert.Error(t, schema.Validate(directiveSchemaPage(t, fixture)), "invalid language %q", language)
	}
	for _, control := range []rune{0, 0xa, 0x1f, 0x7f, 0x85, 0x9f} {
		name, err := json.Marshal("snippet" + string(control) + ".md")
		require.NoError(t, err)
		fixture := `{"kind":"include","includeSource":` + string(name) + `}`
		assert.Error(t, schema.Validate(directiveSchemaPage(t, fixture)))
	}
}

func TestPageSchemaValidatesCompiledDirectivesAndSourceProvenance(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	require.NoError(t, os.MkdirAll(filepath.Join(input, "snippets", "deep"), 0o700))
	files := map[string]string{
		"docs.json":             `{"version":1,"title":"Author guide","pages":[{"id":"guide","title":"Guide","source":"guide.md","path":"index.html"}]}`,
		"guide.md":              "# Guide\n\n:::warning title=\"Before deploy\"\nWait for health.\n:::\n\n:::tabs\n:::tab title=\"Go\"\nRun `client.Create()`.\n:::\n:::tab title=\"Shell\"\n:::include source=\"snippets/run.sh\" language=\"sh\"\n:::\n:::\n:::\n\n:::include source=\"snippets/intro.md\"\n:::\n",
		"snippets/run.sh":       "#!/bin/sh\necho ready\n",
		"snippets/intro.md":     "## Included guide\n\nA reusable paragraph.\n\n:::include source=\"deep/more.md\"\n:::\n",
		"snippets/deep/more.md": "Nested source.\n",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(input, filepath.FromSlash(name)), []byte(content), 0o600))
	}
	output := filepath.Join(root, "output")
	require.NoError(t, site.Build(filepath.Join(input, "docs.json"), output, site.Options{}))
	index, err := os.ReadFile(filepath.Join(output, "_docs", "project.json"))
	require.NoError(t, err)
	var project document.Project
	require.NoError(t, json.Unmarshal(index, &project))
	require.Len(t, project.Pages, 1)
	data, err := os.ReadFile(filepath.Join(output, "_docs", filepath.FromSlash(project.Pages[0].Data)))
	require.NoError(t, err)
	schema := compileDocumentationSchema(t, "page")
	require.NoError(t, schema.Validate(decodeSchemaData(t, data)))
	var page document.Page
	require.NoError(t, json.Unmarshal(data, &page))
	require.Len(t, page.Blocks, 4)
	callout, tabs, include := page.Blocks[1], page.Blocks[2], page.Blocks[3]
	assert.Equal(t, document.BlockCallout, callout.Kind)
	assert.Equal(t, "warning", callout.Variant)
	assert.Equal(t, "Before deploy", callout.Title)
	assert.Equal(t, document.Location{File: "guide.md", Line: 3, Column: 1}, callout.Source)
	require.Len(t, callout.Children, 1)
	assert.Equal(t, document.Location{File: "guide.md", Line: 4, Column: 1}, callout.Children[0].Source)
	assert.Equal(t, document.BlockTabs, tabs.Kind)
	assert.NotEmpty(t, tabs.ID)
	require.Len(t, tabs.Children, 2)
	assert.Equal(t, document.BlockTab, tabs.Children[1].Kind)
	assert.Equal(t, "Shell", tabs.Children[1].Title)
	assert.Equal(t, document.Location{File: "guide.md", Line: 11, Column: 1}, tabs.Children[1].Source)
	require.Len(t, tabs.Children[1].Children, 1)
	codeInclude := tabs.Children[1].Children[0]
	assert.Equal(t, document.BlockInclude, codeInclude.Kind)
	assert.Equal(t, "snippets/run.sh", codeInclude.IncludeSource)
	assert.Equal(t, document.Location{File: "guide.md", Line: 12, Column: 1}, codeInclude.Source)
	require.Len(t, codeInclude.Children, 1)
	assert.Equal(t, document.BlockCode, codeInclude.Children[0].Kind)
	assert.Equal(t, "sh", codeInclude.Children[0].Language)
	assert.Equal(t, "#!/bin/sh\necho ready\n", codeInclude.Children[0].Text)
	assert.Equal(t, document.Location{File: "snippets/run.sh", Line: 1, Column: 1}, codeInclude.Children[0].Source)
	assert.Equal(t, document.BlockInclude, include.Kind)
	assert.Equal(t, "snippets/intro.md", include.IncludeSource)
	assert.Equal(t, document.Location{File: "guide.md", Line: 17, Column: 1}, include.Source)
	require.Len(t, include.Children, 3)
	assert.Equal(t, document.BlockHeading, include.Children[0].Kind)
	assert.Equal(t, document.Location{File: "snippets/intro.md", Line: 1, Column: 4}, include.Children[0].Source)
	nested := include.Children[2]
	assert.Equal(t, document.BlockInclude, nested.Kind)
	assert.Equal(t, "snippets/deep/more.md", nested.IncludeSource)
	assert.Equal(t, document.Location{File: "snippets/intro.md", Line: 5, Column: 1}, nested.Source)
	require.Len(t, nested.Children, 1)
	assert.Equal(t, document.Location{File: "snippets/deep/more.md", Line: 1, Column: 1}, nested.Children[0].Source)
}
