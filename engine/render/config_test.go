package render

import (
	"testing"

	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unsupportedElement struct {
	md.OrderedSafeElement
}

func (unsupportedElement) GetType() md.ElementType {
	return md.ElementType(999)
}

func TestDelimiterConfigs(t *testing.T) {
	cfg := Config{
		EmphasisSyntax:  md.EmphasisSyntaxUnderscores,
		HeaderSyntax:    md.HeaderSyntaxUnderlined,
		RuleSyntax:      md.RuleSyntaxDashes,
		CodeblockSyntax: md.CodeblockSyntaxTildas,
		ListSyntax:      md.ListSyntaxPlus,
	}

	header := md.NewHeaderBuilder().Level(md.HeaderLevelOne).Text("Title").Build()
	headerDelimiter, _ := getHeaderDelimiter(cfg, header)
	assert.Equal(t, md.HeaderDelimiterEquals, headerDelimiter)

	header.SetLevel(md.HeaderLevelThree)
	headerDelimiter, _ = getHeaderDelimiter(cfg, header)
	assert.Equal(t, md.HeaderDelimiterDashes, headerDelimiter)

	ruleDelimiter, _ := getRuleDelimiter(cfg)
	assert.Equal(t, md.RuleDashesDelimiter, ruleDelimiter)
	codeDelimiter, _ := getCodeblockDelimiter(cfg)
	assert.Equal(t, md.CodeblockTildasDelimiter, codeDelimiter)
	boldDelimiter, _ := getEmphasisBoldDelimiter(cfg)
	assert.Equal(t, md.EmphasisBoldUnderscoresDelimiter, boldDelimiter)
	italicDelimiter, _ := getEmphasisItalicDelimiter(cfg)
	assert.Equal(t, md.EmphasisBoldUnderscoresDelimiter, italicDelimiter)
	boldItalicDelimiter, _ := getEmphasisBoldItalicDelimiter(cfg)
	assert.Equal(t, md.EmphasisBoldItalicAsteriskDelimiter, boldItalicDelimiter)
	listDelimiter, _ := getUnorderedListDelimiter(cfg)
	assert.Equal(t, md.ListPlusDelimiter, listDelimiter)

	cfg.ListSyntax = md.ListSyntaxDash
	listDelimiter, _ = getUnorderedListDelimiter(cfg)
	assert.Equal(t, md.ListDashDelimiter, listDelimiter)
}

func TestRenderedElementLengthAndUnsupportedElement(t *testing.T) {
	renderer := NewMarkdownRenderer(DefaultConfig())
	code := md.NewCodeblockBuilder().Text("```inside```").Build()
	assert.Greater(t, renderer.renderedElementLength(code), 0)
	assert.Zero(t, renderer.renderedElementLength(md.NewImageBuilder().Url("x").Build()))

	_, err := renderer.renderElement(&unsupportedElement{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported element type")
}
