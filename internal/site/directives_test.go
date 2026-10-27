package site

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

func TestDirectiveParserNestedContainers(t *testing.T) {
	source := []byte(":::tabs\n:::tab title=\"Go\"\n~~~go\nrun()\n~~~\n:::\n:::tab title=\"cURL\"\nText\n:::\n:::\n")
	state := newDirectiveState(context.Background(), "guide.md", nil, nil)
	directives := &directiveParser{state: state, sourceName: "guide.md"}
	markdown := goldmark.New(goldmark.WithParserOptions(parser.WithBlockParsers(util.Prioritized(directives, 50))))
	doc := markdown.Parser().Parse(text.NewReader(source))
	require.NoError(t, directives.err)
	tabs, ok := doc.FirstChild().(*directiveNode)
	require.True(t, ok)
	for child := tabs.FirstChild(); child != nil; child = child.NextSibling() {
		_, ok := child.(*directiveNode)
		require.True(t, ok, "unexpected %s child", child.Kind())
	}
	require.Equal(t, 2, tabs.ChildCount())
}

func directiveTestManifest() Manifest {
	return Manifest{Version: 1, Title: "Documentation", Pages: []Page{
		{Title: "Guide", Source: "docs/guide.md", Path: "index.html", Kind: "markdown"},
		{Title: "Other", Source: "other/page.md", Path: "other.html", Kind: "markdown"},
	}, Assets: []string{"examples/image.svg"}}
}

func compileDirectiveTest(source string, files map[string]string) (pageContent, error) {
	return compileMarkdownSource(context.Background(), []byte(source), 0, directiveTestManifest(), func(name string) ([]byte, error) {
		value, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("missing fixture %s", name)
		}
		return []byte(value), nil
	})
}

func TestDirectiveCalloutsAndMarkdownContainers(t *testing.T) {
	source := ":::note title=\"Before \\\"you\\\" begin\"\r\n**Safe** <script>alert(1)</script>\r\n:::tip\r\nNested tip.\r\n:::\r\n:::\r\n\n> :::warning\n> A quoted warning.\n> :::\n\n- :::info\n  A list note.\n  :::\n\n:::danger\n\n    literal code\n:::\n"
	content, err := compileDirectiveTest(source, nil)
	require.NoError(t, err)
	assert.Contains(t, content.HTML, "Before &#34;you&#34; begin")
	assert.Contains(t, content.HTML, "<strong>Safe</strong>")
	assert.NotContains(t, content.HTML, "<script>")
	assert.Contains(t, content.HTML, "docs-callout-tip")
	assert.Contains(t, content.HTML, "docs-callout-warning")
	assert.Contains(t, content.HTML, "docs-callout-info")
	assert.Contains(t, content.HTML, "<pre><code>literal code")
	assert.Contains(t, content.Text, "Nested tip.")
	assert.Equal(t, document.BlockCallout, content.Blocks[0].Kind)
	assert.Equal(t, "docs/guide.md", content.Blocks[0].Source.File)
	assert.Equal(t, 1, content.Blocks[0].Source.Line)
}

func TestDirectiveSyntaxRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"unknown", ":::api\n:::\n", "unknown directive"},
		{"spaced opener", "::: note\n:::\n", "unknown directive"},
		{"Unicode whitespace", ":::\u00a0\n", "unknown directive"},
		{"extra colon", "::::note\n:::\n", "unknown directive"},
		{"unmatched", ":::\n", "unmatched"},
		{"unclosed", ":::note\nBody\n", "unclosed"},
		{"unknown attr", ":::note style=\"x\"\n:::\n", "unknown attribute"},
		{"duplicate attr", ":::note title=\"one\" title=\"two\"\n:::\n", "duplicate"},
		{"single quotes", ":::note title='one'\n:::\n", "double-quoted"},
		{"unquoted", ":::note title=one\n:::\n", "double-quoted"},
		{"unterminated", ":::note title=\"one\n:::\n", "unterminated"},
		{"bad JSON escape", ":::note title=\"\\x41\"\n:::\n", "invalid JSON"},
		{"missing separator", ":::note title=\"x\"title=\"y\"\n:::\n", "whitespace"},
		{"missing equals", ":::note title \"one\"\n:::\n", "expected ="},
		{"empty title", ":::note title=\" \"\n:::\n", "title must"},
		{"long title", ":::note title=\"" + strings.Repeat("x", 257) + "\"\n:::\n", "title must"},
		{"control title", ":::note title=\"bad\\nvalue\"\n:::\n", "control character"},
		{"tab outside tabs", ":::tab title=\"Go\"\nText\n:::\n", "direct child"},
		{"tab missing title", ":::tab\nText\n:::\n", "requires a nonempty title"},
		{"empty tabs", ":::tabs\n:::\n", "requires 1"},
		{"stray tabs prose", ":::tabs\nStray prose\n:::\n", "only direct"},
		{"include missing source", ":::include\n:::\n", "requires a source"},
		{"include body", ":::include source=\"x.md\"\nBody\n:::\n", "empty body"},
		{"bad language", ":::include source=\"x\" language=\"bad name\"\n:::\n", "include language"},
		{"empty language", ":::include source=\"x\" language=\"\"\n:::\n", "include language"},
		{"long header", ":::note title=\"" + strings.Repeat("x", 4096) + "\"\n:::\n", "header exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := compileDirectiveTest(test.source, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "docs/guide.md:1:1:")
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestDirectiveFencesAndInlineCodeRemainLiteral(t *testing.T) {
	source := "~~~markdown\n:::unknown\n:::\n~~~\n\n    :::unknown\n\nText `:::unknown` stays literal.\n\n:::note\n~~~markdown\n:::\n:::unknown\n~~~\nAfter the fence.\n:::\n"
	content, err := compileDirectiveTest(source, nil)
	require.NoError(t, err)
	assert.Contains(t, content.HTML, "<code>:::unknown</code>")
	assert.Contains(t, content.HTML, "After the fence.")
	assert.Equal(t, 1, strings.Count(content.HTML, "docs-callout-note"))
}

func TestDirectiveIncludesPreserveOriginsAndResolveLinksAndImages(t *testing.T) {
	files := map[string]string{
		"docs/includes/fragment.md":      "# Included\n\n[Other](../../other/page.md#section)\n\n![Diagram](../../examples/image.svg)\n\n:::include source=\"nested/second.md\"\n:::\n",
		"docs/includes/nested/second.md": ":::tip\nNested source content.\n:::\n",
	}
	source := "# Primary\n\n:::include source=\"includes/fragment.md\"\n:::\n"
	content, err := compileDirectiveTest(source, files)
	require.NoError(t, err)
	assert.Contains(t, content.HTML, `href="other.html#section"`)
	assert.Contains(t, content.HTML, `src="assets/examples/image.svg"`)
	assert.Contains(t, content.Text, "Nested source content.")
	require.Len(t, content.Headings, 2)
	assert.Equal(t, "included", content.Headings[1].ID)
	require.Len(t, content.Blocks, 2)
	include := content.Blocks[1]
	assert.Equal(t, document.BlockInclude, include.Kind)
	assert.Equal(t, "docs/guide.md", include.Source.File)
	assert.Equal(t, 3, include.Source.Line)
	assert.Equal(t, "docs/includes/fragment.md", include.IncludeSource)
	require.NotEmpty(t, include.Children)
	assert.Equal(t, "docs/includes/fragment.md", include.Children[0].Source.File)
	assert.Equal(t, 1, include.Children[0].Source.Line)
}

func TestDirectiveCodeIncludesAreLiteralAndMayQuoteOwnSource(t *testing.T) {
	literal := ":::unknown\n<script>unsafe</script>\n"
	content, err := compileDirectiveTest(":::include source=\"guide.md\" language=\"markdown\"\n:::\n", map[string]string{"docs/guide.md": literal})
	require.NoError(t, err)
	assert.Contains(t, content.HTML, ":::unknown")
	assert.Contains(t, content.HTML, "&lt;script&gt;unsafe&lt;/script&gt;")
	assert.NotContains(t, content.HTML, "<script>")
	require.Len(t, content.Blocks, 1)
	require.Len(t, content.Blocks[0].Children, 1)
	assert.Equal(t, literal, content.Blocks[0].Children[0].Text)
	assert.Equal(t, "markdown", content.Blocks[0].Children[0].Language)
	assert.Equal(t, "docs/guide.md", content.Blocks[0].Children[0].Source.File)
}

func TestDirectiveRepeatedIncludesKeepUniqueHeadingAndTabIDs(t *testing.T) {
	fragment := "# Repeat\n\n[This section](#repeat)\n\n:::tabs\n:::tab title=\"One\"\nFirst\n:::\n:::tab title=\"Two\"\nSecond\n:::\n:::\n"
	source := ":::include source=\"fragment.md\"\n:::\n\n:::include source=\"fragment.md\"\n:::\n"
	content, err := compileDirectiveTest(source, map[string]string{"docs/fragment.md": fragment})
	require.NoError(t, err)
	require.Len(t, content.Headings, 2)
	assert.Equal(t, "repeat", content.Headings[0].ID)
	assert.Equal(t, "repeat-1", content.Headings[1].ID)
	assert.NotEqual(t, content.Blocks[0].Children[2].ID, content.Blocks[1].Children[2].ID)
	assert.Contains(t, content.HTML, `href="#repeat-1"`)
	repeated, err := compileDirectiveTest(source, map[string]string{"docs/fragment.md": fragment})
	require.NoError(t, err)
	assert.Equal(t, content.HTML, repeated.HTML)
}

func TestDirectiveIncludeFailuresAndBounds(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		_, err := compileDirectiveTest(":::include source=\"a.md\"\n:::\n", map[string]string{"docs/a.md": ":::include source=\"guide.md\"\n:::\n"})
		require.ErrorContains(t, err, "include cycle")
		assert.Contains(t, err.Error(), "docs/a.md:1:1:")
	})
	for _, source := range []string{"../../outside.md", "/etc/passwd", "https://example.test/data", "x.md?q=1", "x.md#anchor", `..\outside.md`} {
		t.Run(source, func(t *testing.T) {
			_, err := compileDirectiveTest(fmt.Sprintf(":::include source=%q\n:::\n", source), nil)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "missing fixture")
		})
	}
	t.Run("missing", func(t *testing.T) {
		_, err := compileDirectiveTest(":::include source=\"missing.md\"\n:::\n", nil)
		require.ErrorContains(t, err, "missing fixture docs/missing.md")
	})
	t.Run("invalid UTF-8", func(t *testing.T) {
		_, err := compileDirectiveTest(":::include source=\"bad.md\"\n:::\n", map[string]string{"docs/bad.md": string([]byte{0xff})})
		require.ErrorContains(t, err, "not valid UTF-8")
	})
	t.Run("include depth", func(t *testing.T) {
		files := map[string]string{}
		for i := 0; i < 9; i++ {
			files[fmt.Sprintf("docs/%d.md", i)] = fmt.Sprintf(":::include source=\"%d.md\"\n:::\n", i+1)
		}
		_, err := compileDirectiveTest(":::include source=\"0.md\"\n:::\n", files)
		require.ErrorContains(t, err, "include depth exceeds 8")
	})
	t.Run("directive depth", func(t *testing.T) {
		_, err := compileDirectiveTest(strings.Repeat(":::note\n", 17)+"body\n"+strings.Repeat(":::\n", 17), nil)
		require.ErrorContains(t, err, "directives exceed 16 nesting levels")
	})
	t.Run("directive count", func(t *testing.T) {
		_, err := compileDirectiveTest(strings.Repeat(":::note\nbody\n:::\n", 1025), nil)
		require.ErrorContains(t, err, "page exceeds 1024 directives")
	})
	t.Run("tabs count", func(t *testing.T) {
		_, err := compileDirectiveTest(":::tabs\n"+strings.Repeat(":::tab title=\"Example\"\nbody\n:::\n", 33)+":::\n", nil)
		require.ErrorContains(t, err, "requires 1–32 tabs")
	})
	t.Run("cumulative headings", func(t *testing.T) {
		fragment := strings.Repeat("# Heading\n\n", 3000)
		_, err := compileDirectiveTest(strings.Repeat(":::include source=\"headings.md\"\n:::\n", 3), map[string]string{"docs/headings.md": fragment})
		require.ErrorContains(t, err, "page exceeds 8192 headings")
	})
	t.Run("cumulative AST", func(t *testing.T) {
		fragment := strings.Repeat("Paragraph\n\n", 9000)
		_, err := compileDirectiveTest(strings.Repeat(":::include source=\"paragraphs.md\"\n:::\n", 3), map[string]string{"docs/paragraphs.md": fragment})
		require.ErrorContains(t, err, "markdown AST exceeds 50000 nodes")
	})
	t.Run("cumulative source bytes", func(t *testing.T) {
		fragment := strings.Repeat("x", 1<<20)
		_, err := compileDirectiveTest(strings.Repeat(":::include source=\"data.txt\" language=\"text\"\n:::\n", 17), map[string]string{"docs/data.txt": fragment})
		require.ErrorContains(t, err, "expanded includes exceed 16777216 bytes")
	})
	t.Run("no loader", func(t *testing.T) {
		_, err := compileMarkdown([]byte(":::include source=\"data.md\"\n:::\n"), 0, directiveTestManifest())
		require.ErrorContains(t, err, "includes are unavailable")
	})
}

func FuzzDirectiveHeader(f *testing.F) {
	for _, seed := range []string{"", ":::note", ":::\u00a0", ":::tab title=\"Example\"", ":::include source=\"x.md\"", ":::note title=\"\\\"\"", "::::", ":::note title=\"x\\"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, header string) {
		_, _, _ = parseDirectiveHeader(header)
	})
}
