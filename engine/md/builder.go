package md

import (
	"fmt"
	"strings"
)

// ColumnBuilder Column builder pattern code
type ColumnBuilder struct {
	column *Column
}

func NewColumnBuilder() *ColumnBuilder {
	column := &Column{}
	b := &ColumnBuilder{column: column}
	return b
}

func (b *ColumnBuilder) Index(index int) *ColumnBuilder {
	b.column.index = index
	return b
}

func (b *ColumnBuilder) Name(name string) *ColumnBuilder {
	b.column.name = name
	return b
}

func (b *ColumnBuilder) Alignment(alignment ColumnAlignment) *ColumnBuilder {
	b.column.alignment = alignment
	return b
}

func (b *ColumnBuilder) Rows(rows ...Row) *ColumnBuilder {
	b.column.rows = rows
	return b
}

func (b *ColumnBuilder) Build() *Column {
	return b.column
}

// TableBuilder Table builder pattern code
type TableBuilder struct {
	table *Table
}

func NewTableBuilder() *TableBuilder {
	table := &Table{}
	b := &TableBuilder{table: table}
	return b
}

func (b *TableBuilder) Rows(rows int) *TableBuilder {
	b.table.rows = rows
	return b
}

func (b *TableBuilder) Index(index int) *TableBuilder {
	b.table.index = index
	return b
}

func (b *TableBuilder) Columns(columns ...Column) *TableBuilder {
	b.table.columns = columns
	return b
}

func (b *TableBuilder) Build() *Table {
	return b.table
}

// RowBuilder Row builder pattern code
type RowBuilder struct {
	row *Row
}

func NewRowBuilder() *RowBuilder {
	row := &Row{}
	b := &RowBuilder{row: row}
	return b
}

func (b *RowBuilder) Index(index int) *RowBuilder {
	b.row.index = index
	return b
}

func (b *RowBuilder) Elements(elements ...Element) *RowBuilder {
	b.row.elements = elements
	return b
}

func (b *RowBuilder) Build() *Row {
	return b.row
}

// RuleBuilder Rule builder pattern code
type RuleBuilder struct {
	rule *Rule
}

func NewRuleBuilder() *RuleBuilder {
	rule := &Rule{}
	b := &RuleBuilder{rule: rule}
	return b
}

func (b *RuleBuilder) Index(index int) *RuleBuilder {
	b.rule.index = index
	return b
}

func (b *RuleBuilder) Build() *Rule {
	return b.rule
}

// ImageBuilder Image builder pattern code
type ImageBuilder struct {
	image *Image
}

func NewImageBuilder() *ImageBuilder {
	image := &Image{}
	b := &ImageBuilder{image: image}
	return b
}

func (b *ImageBuilder) Index(index int) *ImageBuilder {
	b.image.index = index
	return b
}

func (b *ImageBuilder) Url(url string) *ImageBuilder {
	b.image.url = url
	return b
}

func (b *ImageBuilder) Text(text string) *ImageBuilder {
	b.image.text = text
	return b
}

func (b *ImageBuilder) Title(title string) *ImageBuilder {
	b.image.title = title
	return b
}

func (b *ImageBuilder) Build() *Image {
	return b.image
}

// CodeblockBuilder Codeblock builder pattern code
type CodeblockBuilder struct {
	codeblock *Codeblock
}

func NewCodeblockBuilder() *CodeblockBuilder {
	codeblock := &Codeblock{}
	b := &CodeblockBuilder{codeblock: codeblock}
	return b
}

func (b *CodeblockBuilder) Index(index int) *CodeblockBuilder {
	b.codeblock.index = index
	return b
}

func (b *CodeblockBuilder) Text(text string) *CodeblockBuilder {
	b.codeblock.text = text
	return b
}

func (b *CodeblockBuilder) Build() *Codeblock {
	return b.codeblock
}

// ListBuilder List builder pattern code
type ListBuilder struct {
	list *List
}

func NewListBuilder() *ListBuilder {
	list := &List{}
	b := &ListBuilder{list: list}
	return b
}

func (b *ListBuilder) Index(index int) *ListBuilder {
	b.list.index = index
	return b
}

func (b *ListBuilder) Ordered(ordered bool) *ListBuilder {
	b.list.ordered = ordered
	return b
}

func (b *ListBuilder) Entries(entries ...ListEntry) *ListBuilder {
	b.list.entries = entries
	return b
}

func (b *ListBuilder) Build() *List {
	return b.list
}

// ListEntryBuilder ListEntry builder pattern code
type ListEntryBuilder struct {
	listEntry *ListEntry
}

func NewListEntryBuilder(list *List) *ListEntryBuilder {
	listEntry := &ListEntry{list: list}
	b := &ListEntryBuilder{listEntry: listEntry}
	return b
}

func (b *ListEntryBuilder) Element(element Element) *ListEntryBuilder {
	b.listEntry.element = element
	return b
}

func (b *ListEntryBuilder) Index(index int) *ListEntryBuilder {
	b.listEntry.index = index
	return b
}

func (b *ListEntryBuilder) Elements(elements ...Element) *ListEntryBuilder {
	b.listEntry.elements = elements
	return b
}

func (b *ListEntryBuilder) Build() *ListEntry {
	return b.listEntry
}

// LinkBuilder Link builder pattern code
type LinkBuilder struct {
	link *Link
}

func NewLinkBuilder() *LinkBuilder {
	link := &Link{}
	b := &LinkBuilder{link: link}
	return b
}

func (b *LinkBuilder) Index(index int) *LinkBuilder {
	b.link.index = index
	return b
}

func (b *LinkBuilder) Url(url string) *LinkBuilder {
	b.link.url = url
	return b
}

func (b *LinkBuilder) Text(text string) *LinkBuilder {
	b.link.text = text
	return b
}

func (b *LinkBuilder) Build() *Link {
	return b.link
}

// BlockquoteBuilder Blockquote builder pattern code
type BlockquoteBuilder struct {
	blockquote *Blockquote
}

func NewBlockquoteBuilder() *BlockquoteBuilder {
	blockquote := &Blockquote{}
	b := &BlockquoteBuilder{blockquote: blockquote}
	return b
}

func (b *BlockquoteBuilder) Index(index int) *BlockquoteBuilder {
	b.blockquote.index = index
	return b
}

func (b *BlockquoteBuilder) Elements(elements ...Element) *BlockquoteBuilder {
	b.blockquote.elements = elements
	return b
}

func (b *BlockquoteBuilder) Build() *Blockquote {
	return b.blockquote
}

// TextBuilder Text builder pattern code
type TextBuilder struct {
	text *Text
}

func NewTextBuilder() *TextBuilder {
	text := &Text{}
	b := &TextBuilder{text: text}
	return b
}

func (b *TextBuilder) Index(index int) *TextBuilder {
	b.text.index = index
	return b
}

func (b *TextBuilder) Text(text string) *TextBuilder {
	b.text.builder.WriteString(text)
	return b
}

func (b *TextBuilder) Emphasis(emphasis TextEmphasis) *TextBuilder {
	b.text.emphasis = emphasis
	return b
}

func (b *TextBuilder) Build() *Text {
	if strings.ContainsAny(b.text.GetText(), "#^&*") {
		panic(fmt.Errorf("text cannot contain special characters: %s, string: '%s'", "#^&*", b.text.GetText()))
	}

	return b.text
}

// ParagraphBuilder Paragraph builder pattern code
type ParagraphBuilder struct {
	paragraph *Paragraph
}

func NewParagraphBuilder() *ParagraphBuilder {
	paragraph := &Paragraph{}
	b := &ParagraphBuilder{paragraph: paragraph}
	return b
}

func (b *ParagraphBuilder) Index(index int) *ParagraphBuilder {
	b.paragraph.index = index
	return b
}

func (b *ParagraphBuilder) Elements(elements ...Element) *ParagraphBuilder {
	b.paragraph.elements = elements
	return b
}

func (b *ParagraphBuilder) Build() *Paragraph {
	return b.paragraph
}

// HeaderBuilder Header builder pattern code
type HeaderBuilder struct {
	header *Header
}

func NewHeaderBuilder() *HeaderBuilder {
	header := &Header{}
	b := &HeaderBuilder{header: header}
	return b
}

func (b *HeaderBuilder) Index(index int) *HeaderBuilder {
	b.header.index = index
	return b
}

func (b *HeaderBuilder) Level(level HeaderLevel) *HeaderBuilder {
	b.header.level = level
	return b
}

func (b *HeaderBuilder) Text(text string) *HeaderBuilder {
	b.header.text = text
	return b
}

func (b *HeaderBuilder) Build() *Header {
	return b.header
}

// SectionBuilder Section builder pattern code
type SectionBuilder struct {
	section *Section
}

func NewSectionBuilder() *SectionBuilder {
	section := &Section{}
	b := &SectionBuilder{section: section}
	return b
}

func (b *SectionBuilder) Index(index int) *SectionBuilder {
	b.section.index = index
	return b
}

func (b *SectionBuilder) Elements(elements ...Element) *SectionBuilder {
	b.section.elements = elements
	return b
}

func (b *SectionBuilder) Build() *Section {
	return b.section
}

// DocumentBuilder Document builder pattern code
type DocumentBuilder struct {
	document *Document
}

func NewDocumentBuilder() *DocumentBuilder {
	document := &Document{}
	b := &DocumentBuilder{document: document}
	return b
}

func (b *DocumentBuilder) Sections(sections ...Section) *DocumentBuilder {
	b.document.sections = sections
	return b
}

func (b *DocumentBuilder) Build() *Document {
	return b.document
}

// HtmlRefBuilder HtmlRef builder pattern code
type HtmlRefBuilder struct {
	htmlRef *HtmlRef
}

func NewHtmlRefBuilder() *HtmlRefBuilder {
	htmlRef := &HtmlRef{}
	b := &HtmlRefBuilder{htmlRef: htmlRef}
	return b
}

func (b *HtmlRefBuilder) Name(name string) *HtmlRefBuilder {
	b.htmlRef.name = name
	return b
}

func (b *HtmlRefBuilder) Build() *HtmlRef {
	return b.htmlRef
}
