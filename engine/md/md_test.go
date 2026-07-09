package md

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requirePanic(t *testing.T, fn func()) {
	t.Helper()
	require.Panics(t, fn)
}

func TestBuildersAndAccessors(t *testing.T) {
	text := NewTextBuilder().Index(2).Text("plain").Emphasis(TextEmphasisItalic).Build()
	link := NewLinkBuilder().Index(3).Url("https://example.com").Text("example").Build()
	code := NewCodeblockBuilder().Index(4).Text("code").Build()
	row := NewRowBuilder().Index(5).Elements(text, link, code).Build()
	column := NewColumnBuilder().Index(6).Name("Name").Alignment(ColumnAlignmentCenter).Rows(*row).Build()
	table := NewTableBuilder().Index(7).Rows(1).Columns(*column).Build()
	image := NewImageBuilder().Index(8).Url("image.png").Text("alt").Title("title").Build()
	rule := NewRuleBuilder().Index(9).Build()
	header := NewHeaderBuilder().Index(10).Level(HeaderLevelTwo).Text("Header").Build()
	paragraph := NewParagraphBuilder().Index(11).Elements(text, link).Build()
	blockquote := NewBlockquoteBuilder().Index(12).Elements(text).Build()
	paragraphWithElements := NewParagraphBuilder().Elements(text).Build()
	blockquoteWithElements := NewBlockquoteBuilder().Elements(link).Build()
	list := NewListBuilder().Index(13).Ordered(true).Build()
	presetEntry := ListEntry{}
	listWithEntries := NewListBuilder().Entries(presetEntry).Build()
	entry := NewListEntryBuilder(list).Index(14).Element(text).Elements(link).Build()
	htmlRef := NewHtmlRefBuilder().Name("anchor").Build()
	section := NewSectionBuilder().Index(15).Elements(header, paragraph).Build()
	document := NewDocumentBuilder().Sections(*section).Build()

	assert.Equal(t, 2, text.GetIndex())
	assert.Equal(t, "plain", text.GetText())
	assert.Equal(t, 5, text.GetLen())
	assert.Equal(t, TextEmphasisItalic, text.GetEmphasis())
	assert.Equal(t, "https://example.com", link.GetUrl())
	assert.Equal(t, "example", link.GetText())
	assert.Equal(t, "code", code.GetText())
	assert.Len(t, row.GetElements(), 3)
	assert.Equal(t, "Name", column.GetName())
	assert.Equal(t, ColumnAlignmentCenter, column.GetAlignment())
	assert.Len(t, column.GetRows(), 1)
	assert.Equal(t, 1, table.GetRows())
	assert.Len(t, table.GetColumns(), 1)
	assert.Equal(t, "image.png", image.GetUrl())
	assert.Equal(t, "alt", image.GetText())
	assert.Equal(t, "title", image.GetTitle())
	assert.Equal(t, HeaderLevelTwo, header.GetLevel())
	assert.Equal(t, "Header", header.GetText())
	assert.Len(t, paragraph.GetElements(), 2)
	assert.Len(t, paragraphWithElements.GetElements(), 1)
	assert.Len(t, blockquote.GetElements(), 1)
	assert.Len(t, blockquoteWithElements.GetElements(), 1)
	assert.True(t, list.IsOrdered())
	assert.Len(t, listWithEntries.GetEntries(), 1)
	assert.Same(t, text, entry.GetElement())
	assert.Len(t, entry.GetElements(), 1)
	assert.Equal(t, "anchor", htmlRef.GetName())
	assert.Len(t, document.GetSections(), 1)
	assert.Equal(t, ElementTypeRule, rule.GetType())
}

func TestMutatorsAssignIndexes(t *testing.T) {
	section := &Section{}
	header := &Header{}
	section.AddElement(header)
	assert.Equal(t, 0, header.GetIndex())

	document := &Document{}
	document.AddSection(section)
	assert.Equal(t, 0, section.GetIndex())

	paragraph := &Paragraph{}
	text := &Text{}
	paragraph.AddElement(text)
	assert.Equal(t, 0, text.GetIndex())

	blockquote := &Blockquote{}
	link := &Link{}
	blockquote.AddElement(link)
	assert.Equal(t, 0, link.GetIndex())

	list := &List{}
	entry := NewListEntryBuilder(list).Element(&Text{}).Build()
	list.AddEntry(entry)
	assert.Equal(t, 0, entry.GetIndex())
	list.SetOrdered(true)
	list.SetLevel(2)
	assert.True(t, list.IsOrdered())
	assert.Equal(t, 2, list.GetLevel())

	sublist := &List{}
	entry.AddSublist(sublist)
	assert.Equal(t, 0, sublist.GetIndex())
	assert.Equal(t, 3, sublist.GetLevel())

	row := &Row{}
	rowText := &Text{}
	rowLink := &Link{}
	rowCode := Codeblock{}
	row.AddText(rowText)
	row.AddLink(rowLink)
	row.AddCodeblock(rowCode)
	assert.Len(t, row.GetElements(), 3)
	assert.Equal(t, 2, row.GetElements()[2].GetIndex())

	column := &Column{}
	column.SetName("Column")
	column.SetAlignment(ColumnAlignmentRight)
	column.AddRow(row)
	assert.Equal(t, "Column", column.GetName())
	assert.Equal(t, ColumnAlignmentRight, column.GetAlignment())
	assert.Equal(t, 0, row.GetIndex())

	table := &Table{}
	table.AddColumn(column)
	assert.Equal(t, 0, column.GetIndex())
}

func TestSettersAndBlockedElements(t *testing.T) {
	header := &Header{}
	header.SetLevel(HeaderLevelSix)
	header.SetText("H")
	assert.Equal(t, HeaderLevelSix, header.GetLevel())

	text := &Text{}
	text.SetEmphasis(TextEmphasisBold)
	text.Add("hello")
	assert.Equal(t, "hello", text.GetText())

	link := &Link{}
	link.SetUrl("u")
	link.SetText("t")
	assert.Equal(t, "u", link.GetUrl())
	assert.Equal(t, "t", link.GetText())

	image := &Image{}
	image.SetUrl("u")
	image.SetText("t")
	image.SetTitle("title")
	assert.Equal(t, "title", image.GetTitle())

	htmlRef := &HtmlRef{}
	htmlRef.SetName("ref")
	assert.Equal(t, "ref", htmlRef.GetName())

	requirePanic(t, func() { NewTextBuilder().Text("bad#text").Build() })

	header.Block()
	requirePanic(t, func() { header.SetLevel(HeaderLevelOne) })
	requirePanic(t, func() { header.SetText("blocked") })
	text.Block()
	requirePanic(t, func() { text.Add("blocked") })
	section := &Section{}
	section.Block()
	requirePanic(t, func() { section.AddElement(&Text{}) })
	paragraph := &Paragraph{}
	paragraph.Block()
	requirePanic(t, func() { paragraph.AddElement(&Text{}) })
	blockquote := &Blockquote{}
	blockquote.Block()
	requirePanic(t, func() { blockquote.AddElement(&Text{}) })
	entry := &ListEntry{}
	entry.Block()
	requirePanic(t, func() { entry.SetElement(&Text{}) })
	requirePanic(t, func() { entry.AddElement(&Text{}) })
	list := &List{}
	list.Block()
	requirePanic(t, func() { list.AddEntry(&ListEntry{}) })
	code := &Codeblock{}
	code.Block()
	requirePanic(t, func() { code.AddText("x") })
	link.Block()
	requirePanic(t, func() { link.SetUrl("blocked") })
	requirePanic(t, func() { link.SetText("blocked") })
	image.Block()
	requirePanic(t, func() { image.SetUrl("blocked") })
	requirePanic(t, func() { image.SetText("blocked") })
	requirePanic(t, func() { image.SetTitle("blocked") })
	table := &Table{}
	table.Block()
	requirePanic(t, func() { table.AddColumn(&Column{}) })
	column := &Column{}
	column.Block()
	requirePanic(t, func() { column.SetName("blocked") })
	requirePanic(t, func() { column.SetAlignment(ColumnAlignmentLeft) })
	requirePanic(t, func() { column.AddRow(&Row{}) })
	row := &Row{}
	row.Block()
	requirePanic(t, func() { row.AddText(&Text{}) })
	requirePanic(t, func() { row.AddLink(&Link{}) })
	requirePanic(t, func() { row.AddCodeblock(Codeblock{}) })
	htmlRef.Block()
	requirePanic(t, func() { htmlRef.SetName("blocked") })
}

func TestElementTypes(t *testing.T) {
	elements := []Element{
		&Header{}, &Text{}, &Blockquote{}, &Link{}, &List{}, &Codeblock{}, &Image{}, &Rule{},
		&Table{}, &Paragraph{}, &ListEntry{}, &Row{}, &HtmlRef{},
	}
	types := []ElementType{
		ElementTypeHeader, ElementTypeText, ElementTypeBlockquote, ElementTypeLink,
		ElementTypeList, ElementTypeCodeblock, ElementTypeImage, ElementTypeRule,
		ElementTypeTable, ElementTypeParagraph, ElementTypeListEntry, ElementTypeRow,
		ElementTypeHtmlRef,
	}
	for i, element := range elements {
		assert.Equal(t, types[i], element.GetType())
	}
}
