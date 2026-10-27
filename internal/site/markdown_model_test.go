package site

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarkdownModelPreservesContentSemantics(t *testing.T) {
	source := []byte("# Guide\n\nA *small* **strong** ~~old~~ paragraph.\n\nA&nbsp;B &amp; C \\*literal\\*.\n\nCode ` a\nb ` and `&amp;`.\n\n> Quoted text.\n\n3. Third item\n4. Fourth item\n\n- [x] Done\n- [ ] Pending\n\n| Left | Right |\n| :--- | ---: |\n| one | two |\n\nTerm\n: Definition text.\n\n---\n\n```go\nfmt.Println(1)\n```\n\n<div>Raw HTML</div>\n")
	original := bytes.Clone(source)
	manifest := Manifest{Version: 1, Title: "Guide", Pages: []Page{{ID: "guide", Title: "Guide", Source: "guide.md", Path: "index.html", Kind: "markdown"}}}
	content, err := compileMarkdown(source, 0, manifest)
	require.NoError(t, err)
	assert.Equal(t, original, source)
	blocks, inlines := flattenModel(content.Blocks)
	byKind := make(map[document.BlockKind][]document.Block)
	for _, block := range blocks {
		byKind[block.Kind] = append(byKind[block.Kind], block)
	}
	for _, kind := range []document.BlockKind{"paragraph", "heading", "code", "quote", "list", "listItem", "table", "tableRow", "tableHeader", "tableCell", "definitionList", "definitionTerm", "definition", "rule", "html"} {
		assert.NotEmpty(t, byKind[kind], "content kind %s must survive export", kind)
	}
	require.Len(t, byKind["heading"], 1)
	assert.Equal(t, "guide", byKind["heading"][0].ID)
	assert.Equal(t, 1, byKind["heading"][0].Level)
	assert.Equal(t, 1, byKind["heading"][0].Source.Line)
	require.Len(t, byKind["code"], 1)
	assert.Equal(t, "go", byKind["code"][0].Language)
	assert.Equal(t, "fmt.Println(1)\n", byKind["code"][0].Text)
	require.NotEmpty(t, byKind["html"])
	assert.True(t, byKind["html"][0].Omitted)
	var ordered bool
	for _, list := range byKind["list"] {
		if list.Ordered {
			ordered = true
			assert.Equal(t, 3, list.Start)
		}
	}
	assert.True(t, ordered)
	var text strings.Builder
	var code []string
	var checked []bool
	inlineKinds := make(map[document.InlineKind]bool)
	for _, inline := range inlines {
		inlineKinds[inline.Kind] = true
		if inline.Kind == "text" {
			text.WriteString(inline.Text)
		}
		if inline.Kind == "code" {
			code = append(code, inline.Text)
		}
		if inline.Checked != nil {
			checked = append(checked, *inline.Checked)
		}
	}
	for _, kind := range []document.InlineKind{"emphasis", "strong", "strikethrough", "checkbox"} {
		assert.True(t, inlineKinds[kind], "%s must survive export", kind)
	}
	assert.Contains(t, text.String(), "A\u00a0B & C *literal*.")
	assert.Contains(t, code, "a b")
	assert.Contains(t, code, "&amp;")
	assert.Equal(t, []bool{true, false}, checked)
}

func TestGeneratedMarkdownModelDoesNotInventAuthoredCoordinates(t *testing.T) {
	manifest := Manifest{Version: 1, Title: "API", Pages: []Page{{ID: "api", Title: "API", Source: "api.proto", Path: "index.html", Kind: "protobuf"}}}
	content, err := compileMarkdown([]byte("# Generated\n\nAn *example*.\n\n```json\n{}\n```\n"), 0, manifest)
	require.NoError(t, err)
	blocks, _ := flattenModel(content.Blocks)
	require.NotEmpty(t, blocks)
	for _, block := range blocks {
		assert.Equal(t, "api.proto", block.Source.File)
		assert.True(t, block.Source.Generated)
		assert.Zero(t, block.Source.Line)
		assert.Zero(t, block.Source.Column)
	}
}

func flattenModel(nodes []document.Block) ([]document.Block, []document.Inline) {
	var blocks []document.Block
	var inlines []document.Inline
	var collectInlines func([]document.Inline)
	collectInlines = func(nodes []document.Inline) {
		for _, node := range nodes {
			inlines = append(inlines, node)
			collectInlines(node.Children)
		}
	}
	var collectBlocks func([]document.Block)
	collectBlocks = func(nodes []document.Block) {
		for _, node := range nodes {
			blocks = append(blocks, node)
			collectInlines(node.Inlines)
			collectBlocks(node.Children)
		}
	}
	collectBlocks(nodes)
	return blocks, inlines
}
