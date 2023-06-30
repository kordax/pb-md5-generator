package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/engine/md"
	arrayutils "gitlab.com/kordax/basic-utils/array-utils"
	mathutils "gitlab.com/kordax/basic-utils/math-utils"
)

type Config struct {
	EmphasisSyntax  md.EmphasisSyntax
	HeaderSyntax    md.HeaderSyntax
	RuleSyntax      md.RuleSyntax
	CodeblockSyntax md.CodeblockSyntax
	ListSyntax      md.ListSyntax
}

func DefaultRenderConfig() *Config {
	return &Config{
		EmphasisSyntax:  md.EmphasisSyntaxAsterisks,
		HeaderSyntax:    md.HeaderSyntaxNumberSigns,
		RuleSyntax:      md.RuleSyntaxAsterisks,
		CodeblockSyntax: md.CodeblockSyntaxBackticks,
		ListSyntax:      md.ListSyntaxAsterisk,
	}
}

type Renderer interface {
	Render(doc *md.Document) (string, error)
}

type MarkdownRenderer struct {
	builder strings.Builder
	config  Config
}

func NewMarkdownRenderer(config *Config) *MarkdownRenderer {
	return &MarkdownRenderer{
		config: *config,
	}
}

func (g *MarkdownRenderer) Render(doc *md.Document) (string, error) {
	sections := doc.GetSections()
	sort.Slice(sections, func(i, j int) bool {
		return sections[i].GetIndex() < sections[j].GetIndex()
	})
	for _, section := range sections {
		if err := g.renderSection(section); err != nil {
			return "", err
		}
	}

	return g.builder.String(), nil
}

func (g *MarkdownRenderer) renderSection(section ...md.Section) error {
	for _, s := range section {
		elements := s.GetElements()
		sort.Slice(elements, func(i, j int) bool {
			return elements[i].GetIndex() < elements[j].GetIndex()
		})
		for i, e := range elements {
			if err, _ := g.renderElement(e); err != nil {
				return err
			}

			if i+1 < len(elements) {
				switch elements[i+1].GetType() {
				case md.ElementTypeTable:
					fallthrough
				case md.ElementTypeRule:
					fallthrough
				case md.ElementTypeList:
					fallthrough
				case md.ElementTypeHeader:
					fallthrough
				case md.ElementTypeBlockquote:
					fallthrough
				case md.ElementTypeImage:
					fallthrough
				case md.ElementTypeCodeblock:
					fallthrough
				case md.ElementTypeParagraph:
					switch e.GetType() {
					case md.ElementTypeText:
						fallthrough
					case md.ElementTypeImage:
						fallthrough
					case md.ElementTypeLink:
						g.newline()
						fallthrough
					default:
						g.newline()
					}
				case md.ElementTypeText:
					fallthrough
				case md.ElementTypeLink:
					if elements[i+1].GetType() == md.ElementTypeText || elements[i+1].GetType() == md.ElementTypeLink {
						g.newline()
					}
				default:
					if e.GetType() == md.ElementTypeList {
						g.newline()
					}
				}
			} else {
				switch e.GetType() {
				case md.ElementTypeText:
					fallthrough
				case md.ElementTypeImage:
					fallthrough
				case md.ElementTypeLink:
					g.newline()
				}
			}
		}
	}
	return nil
}

// parent should always be a dereferenced pointer
func (g *MarkdownRenderer) renderElement(element md.Element) (error, int) {
	if element == nil {
		return nil, 0
	}

	switch element.GetType() {
	case md.ElementTypeHeader:
		return g.renderHeader(element.(*md.Header))
	case md.ElementTypeParagraph:
		return g.renderParagraph(element.(*md.Paragraph))
	case md.ElementTypeText:
		w := g.renderText(element.(*md.Text))
		return nil, w
	case md.ElementTypeBlockquote:
		return g.renderBlockquote(element.(*md.Blockquote))
	case md.ElementTypeList:
		return g.renderList(element.(*md.List))
	case md.ElementTypeCodeblock:
		return g.renderCodeblock(element.(*md.Codeblock))
	case md.ElementTypeImage:
		return g.renderImage(element.(*md.Image))
	case md.ElementTypeRule:
		return g.renderRule(element.(*md.Rule))
	case md.ElementTypeLink:
		return g.renderLink(element.(*md.Link))
	case md.ElementTypeTable:
		return g.renderTable(element.(*md.Table)), 0
	case md.ElementTypeHtmlRef:
		return g.renderHtmlRef(element.(*md.HtmlRef))
	}

	return fmt.Errorf("unsupported element type received"), 0
}

func (g *MarkdownRenderer) renderHeader(header ...*md.Header) (error, int) {
	chars := 0

	for _, h := range header {
		switch g.config.HeaderSyntax {
		case md.HeaderSyntaxNumberSigns:
			n := 1
			switch h.GetLevel() {
			case md.HeaderLevelOne:
				n = 1
			case md.HeaderLevelTwo:
				n = 2
			case md.HeaderLevelThree:
				n = 3
			case md.HeaderLevelFour:
				n = 4
			case md.HeaderLevelFive:
				n = 5
			case md.HeaderLevelSix:
				n = 6
			}
			g.builder.WriteString(strings.Repeat(md.HeaderDelimiterBasic, n) + " ")
			chars += n + 1
			chars += g.renderString(h.GetText())
		default:
			del, _ := getHeaderDelimiter(g.config, h)
			chars += g.renderString(h.GetText())
			if h.GetText() != "" {
				textChars := utf8.RuneCountInString(h.GetText())
				g.builder.WriteString("\n" + strings.Repeat(del, textChars))
				chars += textChars
			} else {
				g.builder.WriteString("\n" + strings.Repeat(del, 3))
				chars += utf8.RuneCountInString(del) * 3
			}
		}

		g.newline()
	}

	return nil, chars
}

func (g *MarkdownRenderer) renderParagraph(paragraph ...*md.Paragraph) (error, int) {
	chars := 0
	for _, p := range paragraph {
		for _, e := range p.GetElements() {
			err, written := g.renderElement(e)
			if err != nil {
				return err, 0
			}
			chars += written
		}
		g.newline()
	}

	return nil, chars
}

func (g *MarkdownRenderer) renderBlockquote(blockquote ...*md.Blockquote) (error, int) {
	chars := 0
	for _, q := range blockquote {
		for _, e := range q.GetElements() {
			nonEmptyParagraph := false
			if e.GetType() == md.ElementTypeParagraph {
				c := (e).(*md.Paragraph)
				if len(c.GetElements()) > 0 {
					chars += 2
					g.builder.WriteString("> ")
					g.newline()
					nonEmptyParagraph = true
				}
			}
			g.builder.WriteString("> ")
			chars += 2
			err, written := g.renderElement(e)
			if err != nil {
				return err, 0
			}
			chars += written

			if nonEmptyParagraph {
				g.builder.WriteString("> ")
				g.newline()
				chars += 2
			}

			switch e.GetType() {
			case md.ElementTypeText:
				fallthrough
			case md.ElementTypeImage:
				fallthrough
			case md.ElementTypeLink:
				g.newline()
			}
		}
		g.newline()
	}

	return nil, chars
}

func (g *MarkdownRenderer) renderList(list ...*md.List) (error, int) {
	chars := 0
	for _, l := range list {
		for _, e := range l.GetEntries() {
			chars += g.renderListEntry(&e, l.IsOrdered(), l.GetLevel())
		}
	}

	return nil, chars
}

func (g *MarkdownRenderer) renderListEntry(entry *md.ListEntry, ordered bool, level int) int {
	chars := 0
	if level > 0 {
		tab := strings.Repeat(" ", level*5)
		chars += utf8.RuneCountInString(tab)
		g.builder.WriteString(tab)
	}

	var del string
	if ordered {
		del = strconv.Itoa(entry.GetIndex()+1) + ". "
	} else {
		del, _ = getUnorderedListDelimiter(g.config)
		del += " "
	}
	chars += utf8.RuneCountInString(del)
	g.builder.WriteString(del)
	err, written := g.renderElement(entry.GetElement())
	if err != nil {
		return 0
	}
	chars += written
	g.newline()
	for _, e := range entry.GetElements() {
		err, written := g.renderElement(e)
		if err != nil {
			return 0
		}
		chars += written
	}

	return chars
}

func (g *MarkdownRenderer) renderCodeblock(codeblock ...*md.Codeblock) (error, int) {
	chars := 0
	del, delChars := getCodeblockDelimiter(g.config)

	for _, b := range codeblock {
		g.builder.WriteString(del)
		g.newline()
		chars += delChars
		if b.GetText() != "" {
			chars += g.renderString(b.GetText())
		} else {
			return fmt.Errorf("empty code blocks are not supported"), 0
		}
		if !strings.HasSuffix(b.GetText(), "\n") {
			g.newline()
		}
		g.builder.WriteString(del)
		chars += delChars
		g.newline()
	}

	return nil, chars
}

func (g *MarkdownRenderer) renderImage(image ...*md.Image) (error, int) {
	chars := 0
	for _, l := range image {
		imgStr := fmt.Sprintf("![%s](%s \"%s\")", l.GetText(), l.GetUrl(), l.GetTitle())
		chars += utf8.RuneCountInString(imgStr)
		g.builder.WriteString(imgStr)
	}

	return nil, chars
}

func (g *MarkdownRenderer) renderRule(rule ...*md.Rule) (error, int) {
	chars := 0
	del, delChars := getRuleDelimiter(g.config)
	for range rule {
		g.builder.WriteString(del)
		chars += delChars
		g.newline()
	}

	return nil, chars
}

func (g *MarkdownRenderer) renderLink(link ...*md.Link) (error, int) {
	chars := 0
	for _, l := range link {
		urlStr := fmt.Sprintf("[%s](%s)", l.GetText(), l.GetUrl())
		chars += utf8.RuneCountInString(urlStr)
		g.builder.WriteString(urlStr)
	}

	return nil, chars
}

func (g *MarkdownRenderer) renderTable(table ...*md.Table) error {
	for _, t := range table {
		if err := g.renderColumns(t.GetRows(), t.GetColumns()...); err != nil {
			return err
		}
		g.newline()
	}

	return nil
}

func (g *MarkdownRenderer) renderColumns(tableRows int, columns ...md.Column) error {
	columnMaxLengths := map[int]int{}
	written := 0

	// render column headers
	for _, column := range columns {
		if column.GetIndex() == 0 {
			g.builder.WriteByte('|')
			written++
		}
		g.builder.WriteString(" " + column.GetName())
		written += utf8.RuneCountInString(column.GetName()) + 1
		maxLength := mathutils.MaxInt(arrayutils.Map(column.GetRows(), func(v *md.Row) int {
			return mathutils.SumInt(arrayutils.Map(v.GetElements(), func(e *md.Element) int {
				switch (*e).GetType() {
				case md.ElementTypeText:
					c := (*e).(*md.Text)
					_, dellen := getEmphasisDelimiter(g.config, c.GetEmphasis())
					return c.GetLen() + dellen*2
				case md.ElementTypeLink:
					c := (*e).(*md.Link)
					return utf8.RuneCountInString(c.GetUrl()) + utf8.RuneCountInString(c.GetText()) + 4
				case md.ElementTypeCodeblock:
					c := (*e).(*md.Codeblock)
					del, dellen := getCodeblockDelimiter(g.config)
					return utf8.RuneCountInString(c.GetText()) + strings.Count(c.GetText(), del)*dellen
				default:
					return 0
				}
			}))
		}))
		nameLen := utf8.RuneCountInString(column.GetName())
		if maxLength < nameLen {
			maxLength = nameLen
		}
		if maxLength < 3 {
			maxLength = 3
		}
		columnMaxLengths[column.GetIndex()-1] = maxLength
		toFill := maxLength - utf8.RuneCountInString(column.GetName())
		if toFill > 0 {
			g.builder.WriteString(strings.Repeat(" ", toFill))
			g.builder.WriteString(" |")
			written += toFill + 2
		} else {
			g.builder.WriteString(" |")
			written++
		}
	}

	if len(columns) > 0 {
		g.newline()
	}

	// render header underline
	for c := 0; c < len(columns); c++ {
		if c == 0 {
			g.builder.WriteByte('|')
		}
		maxLength := columnMaxLengths[c-1]
		toFill := maxLength + 2
		if toFill > 0 {
			g.builder.WriteString(strings.Repeat("-", toFill))
			g.builder.WriteString("|")
			written += toFill + 3
		} else {
			g.builder.WriteString("|")
			written += 2
		}

		if c == len(columns)-1 {
			g.newline()
		}
	}

	// render rows sequentially where 'r' is row number and c is a column
	for r := 0; r < tableRows; r++ {
		for c := 0; c < len(columns); c++ {
			column := columns[c]
			rows := column.GetRows()
			if column.GetIndex() == 0 {
				g.builder.WriteByte('|')
			}
			rowWritten := 0
			g.builder.WriteByte(' ')
			if r <= len(rows)-1 {
				row := rows[r]
				for _, element := range row.GetElements() {
					err, w := g.renderElement(element)
					if err != nil {
						return err
					}
					written += w
					rowWritten += w
				}
			}
			maxLength := columnMaxLengths[column.GetIndex()-1]
			toFill := maxLength - rowWritten + 1
			if toFill > 0 {
				g.builder.WriteString(strings.Repeat(" ", toFill))
				written += toFill
			}
			g.builder.WriteString("|")
			written++

			if c == len(columns)-1 && r != tableRows-1 {
				g.newline()
			}
		}
	}

	return nil
}

func (g *MarkdownRenderer) renderText(text ...*md.Text) int {
	chars := 0
	for _, t := range text {
		str := t.GetText()
		switch t.GetEmphasis() {
		case md.TextEmphasisNormal:
			g.builder.WriteString(str)
			chars += utf8.RuneCountInString(str)
		case md.TextEmphasisBold:
			del, delChars := getEmphasisBoldDelimiter(g.config)
			g.builder.WriteString(del)
			for str[len(str)-1] == '\n' || str[len(str)-1] == ' ' {
				str = str[:len(str)-1]
			}
			g.builder.WriteString(str)
			g.builder.WriteString(del + " ")
			chars += utf8.RuneCountInString(str) + delChars*2 + 1
		case md.TextEmphasisItalic:
			del, delChars := getEmphasisItalicDelimiter(g.config)
			g.builder.WriteString(del)
			g.builder.WriteString(str)
			g.builder.WriteString(del + " ")
			chars += utf8.RuneCountInString(str) + delChars*2 + 1
		case md.TextEmphasisBoldItalic:
			del, delChars := getEmphasisBoldItalicDelimiter(g.config)
			g.builder.WriteString(del)
			g.builder.WriteString(str)
			g.builder.WriteString(del + " ")
			chars += utf8.RuneCountInString(str) + delChars*2 + 1
		}
	}

	return chars
}

func (g *MarkdownRenderer) renderString(text string) int {
	g.builder.WriteString(text)
	return utf8.RuneCountInString(text)
}

func (g *MarkdownRenderer) renderHtmlRef(ref ...*md.HtmlRef) (error, int) {
	g.newline()
	chars := 0
	for _, r := range ref {
		refStr := fmt.Sprintf("<a name=\"%s\"></a>", r.GetName())
		chars += utf8.RuneCountInString(refStr)
		g.builder.WriteString(refStr)
	}
	g.newline()

	return nil, chars
}

func (g *MarkdownRenderer) newline() {
	g.builder.WriteByte('\n')
}

func getHeaderDelimiter(config Config, h *md.Header) (string, int) {
	switch config.HeaderSyntax {
	case md.HeaderSyntaxUnderlined:
		switch h.GetLevel() {
		case md.HeaderLevelOne:
			return md.HeaderDelimiterEquals, utf8.RuneCountInString(md.HeaderDelimiterEquals)
		default:
			return md.HeaderDelimiterDashes, utf8.RuneCountInString(md.HeaderDelimiterDashes)
		}
	default:
		return md.HeaderDelimiterBasic, utf8.RuneCountInString(md.HeaderDelimiterBasic)
	}
}

func getRuleDelimiter(config Config) (string, int) {
	switch config.RuleSyntax {
	case md.RuleSyntaxUnderscores:
		return md.RuleUnderscoresDelimiter, utf8.RuneCountInString(md.RuleUnderscoresDelimiter)
	case md.RuleSyntaxDashes:
		return md.RuleDashesDelimiter, utf8.RuneCountInString(md.RuleDashesDelimiter)
	default:
		return md.RuleAsterisksDelimiter, utf8.RuneCountInString(md.RuleAsterisksDelimiter)
	}
}

func getCodeblockDelimiter(config Config) (string, int) {
	switch config.CodeblockSyntax {
	case md.CodeblockSyntaxTildas:
		return md.CodeblockTildasDelimiter, utf8.RuneCountInString(md.CodeblockTildasDelimiter)
	default:
		return md.CodeblockBackticksDelimiter, utf8.RuneCountInString(md.CodeblockBackticksDelimiter)
	}
}

func getEmphasisBoldDelimiter(config Config) (string, int) {
	switch config.EmphasisSyntax {
	case md.EmphasisSyntaxUnderscores:
		return md.EmphasisBoldUnderscoresDelimiter, utf8.RuneCountInString(md.EmphasisBoldUnderscoresDelimiter)
	default:
		return md.EmphasisBoldAsterisksDelimiter, utf8.RuneCountInString(md.EmphasisBoldAsterisksDelimiter)
	}
}

func getEmphasisItalicDelimiter(config Config) (string, int) {
	switch config.EmphasisSyntax {
	case md.EmphasisSyntaxUnderscores:
		return md.EmphasisBoldUnderscoresDelimiter, utf8.RuneCountInString(md.EmphasisBoldUnderscoresDelimiter)
	default:
		return md.EmphasisItalicAsteriskDelimiter, utf8.RuneCountInString(md.EmphasisItalicAsteriskDelimiter)
	}
}

func getEmphasisBoldItalicDelimiter(config Config) (string, int) {
	switch config.EmphasisSyntax {
	case md.EmphasisSyntaxUnderscores:
		return md.EmphasisBoldItalicAsteriskDelimiter, utf8.RuneCountInString(md.EmphasisBoldItalicAsteriskDelimiter)
	default:
		return md.EmphasisBoldItalicUnderscoresDelimiter, utf8.RuneCountInString(md.EmphasisBoldItalicUnderscoresDelimiter)
	}
}

func getUnorderedListDelimiter(config Config) (string, int) {
	switch config.ListSyntax {
	case md.ListSyntaxPlus:
		return md.ListPlusDelimiter, utf8.RuneCountInString(md.ListPlusDelimiter)
	case md.ListSyntaxDash:
		return md.ListDashDelimiter, utf8.RuneCountInString(md.ListDashDelimiter)
	default:
		return md.ListAsteriskDelimiter, utf8.RuneCountInString(md.ListAsteriskDelimiter)
	}
}

func getEmphasisDelimiter(config Config, emphasis md.TextEmphasis) (string, int) {
	switch emphasis {
	case md.TextEmphasisItalic:
		return getEmphasisItalicDelimiter(config)
	case md.TextEmphasisBold:
		return getEmphasisBoldDelimiter(config)
	case md.TextEmphasisBoldItalic:
		return getEmphasisBoldItalicDelimiter(config)
	default:
		return "", 0
	}
}
