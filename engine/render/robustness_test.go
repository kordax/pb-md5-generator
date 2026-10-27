package render

import (
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderDocument(element md.Element) *md.Document {
	return md.NewDocumentBuilder().Sections(*md.NewSectionBuilder().Elements(element).Build()).Build()
}

func TestRendererReuseDoesNotAccumulatePreviousDocuments(t *testing.T) {
	renderer := NewMarkdownRenderer(DefaultConfig())
	first := renderDocument(md.NewTextBuilder().Text("first document").Build())
	second := renderDocument(md.NewTextBuilder().Text("second document").Build())
	_, err := renderer.Render(first)
	require.NoError(t, err)
	for range 20 {
		output, err := renderer.Render(second)
		require.NoError(t, err)
		assert.Equal(t, "second document\n", output)
	}
}

func TestCodeblockLanguageAndEmbeddedFences(t *testing.T) {
	for _, test := range []struct {
		name      string
		syntax    md.CodeblockSyntax
		delimiter string
	}{
		{name: "backticks", syntax: md.CodeblockSyntaxBackticks, delimiter: "`"},
		{name: "tildes", syntax: md.CodeblockSyntaxTildas, delimiter: "~"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			config.CodeblockSyntax = test.syntax
			payload := "before\n" + strings.Repeat(test.delimiter, 4000) + "\nafter"
			block := md.NewCodeblockBuilder().Language("json").Text(payload).Build()
			output, err := NewMarkdownRenderer(config).Render(renderDocument(block))
			require.NoError(t, err)
			fence := strings.Repeat(test.delimiter, 4001)
			assert.Equal(t, fence+"json\n"+payload+"\n"+fence+"\n", output)
		})
	}
}

func TestCodeblockLanguageRejectsFenceInjection(t *testing.T) {
	block := md.NewCodeblockBuilder().Language("json\n```\n<script>").Text("{}").Build()
	_, err := NewMarkdownRenderer(DefaultConfig()).Render(renderDocument(block))
	require.ErrorContains(t, err, "invalid code block language")
}

func TestEmptyBoldTextDoesNotPanic(t *testing.T) {
	for _, value := range []string{"", " ", "\n  \n"} {
		t.Run(value, func(t *testing.T) {
			text := md.NewTextBuilder().Text(value).Emphasis(md.TextEmphasisBold).Build()
			require.NotPanics(t, func() {
				output, err := NewMarkdownRenderer(DefaultConfig()).Render(renderDocument(text))
				require.NoError(t, err)
				assert.Empty(t, strings.TrimSpace(output))
			})
		})
	}
}
