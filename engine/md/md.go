package md

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type ElementType int
type HeaderLevel int
type EmphasisSyntax int
type HeaderSyntax int
type RuleSyntax int
type CodeblockSyntax int
type ListSyntax int
type TextEmphasis int
type ColumnAlignment int

const (
	ElementTypeHeader ElementType = iota
	ElementTypeParagraph
	ElementTypeText
	ElementTypeBlockquote
	ElementTypeList
	ElementTypeListEntry
	ElementTypeCodeblock
	ElementTypeImage
	ElementTypeRule
	ElementTypeLink
	ElementTypeTable
	ElementTypeRow
	ElementTypeHtmlRef
)

const (
	HeaderLevelOne HeaderLevel = iota + 1
	HeaderLevelTwo
	HeaderLevelThree
	HeaderLevelFour
	HeaderLevelFive
	HeaderLevelSix
)

const (
	EmphasisSyntaxAsterisks EmphasisSyntax = iota
	EmphasisSyntaxUnderscores
)

const (
	HeaderSyntaxNumberSigns HeaderSyntax = iota
	HeaderSyntaxUnderlined
)

const (
	RuleSyntaxAsterisks RuleSyntax = iota
	RuleSyntaxUnderscores
	RuleSyntaxDashes
)

const (
	ListSyntaxAsterisk ListSyntax = iota
	ListSyntaxDash
	ListSyntaxPlus
)

const (
	CodeblockSyntaxBackticks CodeblockSyntax = iota
	CodeblockSyntaxTildas
)

const (
	TextEmphasisNormal TextEmphasis = iota
	TextEmphasisBold
	TextEmphasisItalic
	TextEmphasisBoldItalic
)

//goland:noinspection GoUnusedConst
const (
	ColumnAlignmentLeft ColumnAlignment = iota
	ColumnAlignmentCenter
	ColumnAlignmentRight
)

const EmphasisBoldAsterisksDelimiter = "**"
const EmphasisBoldUnderscoresDelimiter = "__"
const EmphasisItalicAsteriskDelimiter = "*"
const EmphasisBoldItalicAsteriskDelimiter = "***"
const EmphasisBoldItalicUnderscoresDelimiter = "___"

const CodeblockBackticksDelimiter = "```"
const CodeblockTildasDelimiter = "~~~"

const RuleAsterisksDelimiter = "***"
const RuleUnderscoresDelimiter = "___"
const RuleDashesDelimiter = "---"

const HeaderDelimiterBasic = "#"
const HeaderDelimiterEquals = "="
const HeaderDelimiterDashes = "-"

const ListAsteriskDelimiter = "*"
const ListDashDelimiter = "-"
const ListPlusDelimiter = "+"

// ColumnAlignmentLeftDelimiter TODO: Implement alignment
//goland:noinspection GoUnusedConst
const ColumnAlignmentLeftDelimiter = ":---"

//goland:noinspection GoUnusedConst
const ColumnAlignmentCenterDelimiter = ":----:"

//goland:noinspection GoUnusedConst
const ColumnAlignmentRightDelimiter = "---:"

type Element interface {
	GetType() ElementType
	GetIndex() int
	SetIndex(index int)
}

type OrderedSafeElement struct {
	index   int
	blocked bool
}

func (o *OrderedSafeElement) GetIndex() int {
	return o.index
}

func (o *OrderedSafeElement) SetIndex(index int) {
	o.index = index
}

/*
Block blocks the element... was element is blocked implementations are responsible for blocking any mutable operations on the element.
*/
func (o *OrderedSafeElement) Block() {
	o.blocked = true
}

type Document struct {
	sections []Section // ordered slice of sections
}

func (d *Document) GetSections() []Section {
	return d.sections
}

func (d *Document) AddSection(section *Section) {
	section.SetIndex(len(d.sections))
	d.sections = append(d.sections, *section)
}

type Section struct {
	OrderedSafeElement
	elements []Element
}

func (s *Section) GetElements() []Element {
	return s.elements
}

func (s *Section) AddElement(element Element) {
	if s.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", s))
	}
	element.SetIndex(len(s.elements))
	s.elements = append(s.elements, element)
}

type Header struct {
	OrderedSafeElement
	level HeaderLevel
	text  string
}

func (h *Header) GetLevel() HeaderLevel {
	return h.level
}

func (h *Header) GetText() string {
	return h.text
}

func (h *Header) SetLevel(level HeaderLevel) {
	if h.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", h))
	}
	h.level = level
}

/*
SetText sets text and replaces all the newline/carriage-return characters to prevent misformed header text.
*/
func (h *Header) SetText(text string) {
	if h.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", h))
	}
	h.text = text
}

type Paragraph struct {
	OrderedSafeElement
	elements []Element
}

func (p *Paragraph) GetElements() []Element {
	return p.elements
}

func (p *Paragraph) AddElement(element Element) {
	if p.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", p))
	}
	element.SetIndex(len(p.elements))
	p.elements = append(p.elements, element)
}

type Text struct {
	OrderedSafeElement
	builder  strings.Builder
	emphasis TextEmphasis
}

func (t *Text) GetLen() int {
	return utf8.RuneCountInString(t.builder.String())
}

func (t *Text) GetText() string {
	return t.builder.String()
}

func (t *Text) GetEmphasis() TextEmphasis {
	return t.emphasis
}

func (t *Text) SetEmphasis(emphasis TextEmphasis) {
	t.emphasis = emphasis
}

func (t *Text) Add(text string) {
	if t.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", t))
	}
	t.builder.WriteString(text)
}

type Blockquote struct {
	OrderedSafeElement
	elements []Element
}

func (b *Blockquote) GetElements() []Element {
	return b.elements
}

func (b *Blockquote) AddElement(element Element) {
	if b.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", b))
	}
	element.SetIndex(len(b.elements))
	b.elements = append(b.elements, element)
}

type Link struct {
	OrderedSafeElement
	url  string
	text string
}

func (l *Link) GetUrl() string {
	return l.url
}

func (l *Link) GetText() string {
	return l.text
}

func (l *Link) SetUrl(url string) {
	if l.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", l))
	}
	l.url = url
}

func (l *Link) SetText(text string) {
	if l.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", l))
	}
	l.text = text
}

type ListEntry struct {
	OrderedSafeElement
	element  Element
	elements []Element
	list     *List
}

func (l *ListEntry) GetElement() Element {
	return l.element
}

func (l *ListEntry) GetElements() []Element {
	return l.elements
}

func (l *ListEntry) SetElement(element Element) {
	if l.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", l))
	}
	l.element = element
}

func (l *ListEntry) AddElement(element Element) {
	if l.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", l))
	}
	element.SetIndex(len(l.elements))
	l.elements = append(l.elements, element)
}

func (l *ListEntry) AddSublist(list *List) {
	if l.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", l))
	}
	list.SetIndex(len(l.elements))
	list.SetLevel(l.list.level + 1)
	l.elements = append(l.elements, list)
}

type List struct {
	OrderedSafeElement
	entries []ListEntry
	ordered bool
	level   int
}

func (l *List) GetEntries() []ListEntry {
	return l.entries
}

func (l *List) IsOrdered() bool {
	return l.ordered
}

func (l *List) SetOrdered(ordered bool) {
	l.ordered = ordered
}

func (l *List) GetLevel() int {
	return l.level
}

func (l *List) SetLevel(level int) {
	l.level = level
}

func (l *List) AddEntry(entry *ListEntry) {
	if l.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", l))
	}
	entry.SetIndex(len(l.entries))
	l.entries = append(l.entries, *entry)
}

type Codeblock struct {
	OrderedSafeElement
	text string
}

func (c *Codeblock) GetText() string {
	return c.text
}

func (c *Codeblock) AddText(text string) {
	if c.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", c))
	}
	c.text = text
}

type Image struct {
	OrderedSafeElement
	url, text, title string
}

func (i *Image) GetUrl() string {
	return i.url
}

func (i *Image) GetText() string {
	return i.text
}

func (i *Image) GetTitle() string {
	return i.title
}

func (i *Image) SetUrl(url string) {
	if i.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", i))
	}
	i.url = url
}

func (i *Image) SetText(text string) {
	if i.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", i))
	}
	i.text = text
}

func (i *Image) SetTitle(title string) {
	if i.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", i))
	}
	i.title = title
}

type Rule struct {
	OrderedSafeElement
}

type Table struct {
	OrderedSafeElement
	columns []Column
	rows    int
}

func (t *Table) GetRows() int {
	return t.rows
}

func (t *Table) GetColumns() []Column {
	return t.columns
}

func (t *Table) AddColumn(column *Column) {
	if t.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", t))
	}
	column.SetIndex(len(t.columns))
	t.columns = append(t.columns, *column)
}

type Column struct {
	OrderedSafeElement
	name      string
	alignment ColumnAlignment
	rows      []Row
}

func (c *Column) GetName() string {
	return c.name
}

func (c *Column) SetName(name string) {
	if c.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", c))
	}
	c.name = name
}

func (c *Column) GetAlignment() ColumnAlignment {
	return c.alignment
}

func (c *Column) SetAlignment(alignment ColumnAlignment) {
	if c.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", c))
	}
	c.alignment = alignment
}

func (c *Column) GetRows() []Row {
	return c.rows
}

func (c *Column) AddRow(row *Row) {
	if c.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", c))
	}
	row.SetIndex(len(c.rows))
	c.rows = append(c.rows, *row)
}

type Row struct {
	OrderedSafeElement
	elements []Element
}

func (r *Row) GetElements() []Element {
	return r.elements
}

func (r *Row) AddText(text *Text) {
	if r.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", r))
	}
	text.SetIndex(len(r.elements))
	r.elements = append(r.elements, text)
}

func (r *Row) AddLink(link *Link) {
	if r.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", r))
	}
	link.SetIndex(len(r.elements))
	r.elements = append(r.elements, link)
}

func (r *Row) AddCodeblock(codeblock Codeblock) {
	if r.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", r))
	}
	codeblock.SetIndex(len(r.elements))
	r.elements = append(r.elements, &codeblock)
}

type HtmlRef struct {
	OrderedSafeElement
	name string
}

func (r *HtmlRef) GetName() string {
	return r.name
}

func (r *HtmlRef) SetName(name string) {
	if r.blocked {
		panic(fmt.Errorf("operation on blocked element: %+v", r))
	}
	r.name = name
}

//goland:noinspection GoMixedReceiverTypes
func (h Header) GetType() ElementType {
	return ElementTypeHeader
}

func (t Text) GetType() ElementType {
	return ElementTypeText
}

func (b Blockquote) GetType() ElementType {
	return ElementTypeBlockquote
}

func (l Link) GetType() ElementType {
	return ElementTypeLink
}

func (l List) GetType() ElementType {
	return ElementTypeList
}

func (c Codeblock) GetType() ElementType {
	return ElementTypeCodeblock
}

func (i Image) GetType() ElementType {
	return ElementTypeImage
}

func (r Rule) GetType() ElementType {
	return ElementTypeRule
}

func (t Table) GetType() ElementType {
	return ElementTypeTable
}

func (p Paragraph) GetType() ElementType {
	return ElementTypeParagraph
}

func (l ListEntry) GetType() ElementType {
	return ElementTypeListEntry
}

func (r Row) GetType() ElementType {
	return ElementTypeRow
}

func (r HtmlRef) GetType() ElementType {
	return ElementTypeHtmlRef
}
