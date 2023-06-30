package engine

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func TestNewConfigurableMDGenerator_renderHeader(t *testing.T) {
	logger := log.With().Str("test", "TestNewConfigurableMDGenerator_renderHeader").Logger()
	t.Run("level five, basic syntax", func(t *testing.T) {
		generator := NewMarkdownRenderer(&Config{})
		header := &md.Header{}
		header.SetLevel(md.HeaderLevelFive)
		headerText := "test header #5 of mine"
		header.SetText(headerText)

		err, _ := generator.renderHeader(header)
		assert.NoError(t, err)
		result := generator.builder.String()
		assert.NotEmpty(t, result)
		assert.Equal(t, "##### "+headerText+"\n", result)
		logger.Info().Msg("result:")
		logger.Info().Msg(result)
	})
	t.Run("level one, underlined syntax", func(t *testing.T) {
		generator := NewMarkdownRenderer(&Config{HeaderSyntax: md.HeaderSyntaxUnderlined})
		header := &md.Header{}
		header.SetLevel(md.HeaderLevelOne)
		headerText := "test header #1 of mine"
		header.SetText(headerText)

		err, _ := generator.renderHeader(header)
		assert.NoError(t, err)
		result := generator.builder.String()
		assert.NotEmpty(t, result)
		assert.Equal(t, headerText+"\n"+strings.Repeat(md.HeaderDelimiterEquals, len(headerText))+"\n", result)
		logger.Info().Msg("result:")
		logger.Info().Msg(result)
	})
	t.Run("level two, underlined syntax", func(t *testing.T) {
		generator := NewMarkdownRenderer(&Config{HeaderSyntax: md.HeaderSyntaxUnderlined})
		header := &md.Header{}
		header.SetLevel(md.HeaderLevelTwo)
		headerText := "test header #2 of mine, length of 42 chars"

		header.SetText(headerText)

		err, _ := generator.renderHeader(header)
		assert.NoError(t, err)
		result := generator.builder.String()
		assert.NotEmpty(t, result)
		assert.Equal(t, headerText+"\n"+strings.Repeat(md.HeaderDelimiterDashes, len(headerText))+"\n", result)
		logger.Info().Msg("result:")
		logger.Info().Msg(result)
	})
}

func TestNewConfigurableMDGenerator_renderTable(t *testing.T) {
	generator := NewMarkdownRenderer(&Config{})
	table := md.NewTableBuilder().Rows(1).Columns(
		*md.NewColumnBuilder().
			Index(0).
			Name("Column n2").
			Rows(*md.NewRowBuilder().
				Elements(
					md.NewTextBuilder().Text("Column n1 row n1").Build(),
				).Build(),
			).Name("Column n1").Build(),
		*md.NewColumnBuilder().
			Index(1).
			Name("Column n2").
			Rows(*md.NewRowBuilder().
				Elements(
					md.NewTextBuilder().Text("Column n2 row n1").Build(),
				).Build(),
			).Build(),
		*md.NewColumnBuilder().
			Index(2).
			Rows(*md.NewRowBuilder().
				Elements(
					md.NewTextBuilder().Text("Column n2 row n1").Build(),
				).Build(),
			).Name("Column n3").Build(),
	).Build()

	section := md.NewSectionBuilder().Build()
	section.AddElement(table)
	document := *md.NewDocumentBuilder().Sections(
		*section,
	).Build()

	result, err := generator.Render(&document)
	assert.NoError(t, err)
	expected :=
		`| Column n1        | Column n2        | Column n3        |
|------------------|------------------|------------------|
| Column n1 row n1 | Column n2 row n1 | Column n2 row n1 |
`
	assert.NotEmpty(t, result)
	assert.Equal(t, expected, result)
}

func TestNewConfigurableMDGenerator_renderDocument(t *testing.T) {
	code := `#!/bin/bash

docker run --rm \
  -v $(pwd)/docs:/out \
  -v $(pwd)./../../protobufs/cryp-advise/:/protos \
  pseudomuto/protoc-gen-doc --doc_opt=markdown,docs_template.md
`

	generator := NewMarkdownRenderer(&Config{})
	section := md.NewSectionBuilder().Build()
	section.AddElement(mkTestHeader("Document main section", 1))
	section.AddElement(mkTestHeader("Docker script example", 2))
	section.AddElement(MkCode(code))
	section.AddElement(mkTestHeader("Then my table example", 2))
	section.AddElement(mkTestTable("my_table1", 3, 5))
	section.AddElement(mkTestHeader("Summary", 1))

	sumParagraph := MkParagraph()
	TextToParagraph(sumParagraph,
		`Our summary bold text...
New line of our summary and special symbols!
`,
		md.TextEmphasisBold)
	TextToParagraph(sumParagraph,
		`Small details that we have.
Normal text.
`,
		md.TextEmphasisNormal)
	TextToParagraph(sumParagraph, `Italic text`, md.TextEmphasisItalic)
	TextToParagraph(sumParagraph, `Bold Italic text`, md.TextEmphasisBoldItalic)
	section.AddElement(sumParagraph)

	section.AddElement(mkTestHeader("Summary subsection", 2))

	subParagraph := MkParagraph()
	TextToParagraph(subParagraph, "subsection text", md.TextEmphasisItalic)
	section.AddElement(subParagraph)

	section.AddElement(mkTestHeader("Summary #3", 3))

	subParagraph3 := MkParagraph()
	TextToParagraph(subParagraph3, "subsection text 3", md.TextEmphasisBoldItalic)
	section.AddElement(subParagraph3)

	section.AddElement(mkTestHeader("Summary #4", 4))
	section.AddElement(mkTestHeader("Summary #5", 5))
	section.AddElement(mkTestHeader("Summary #6", 6))

	linkParagraph := MkParagraph()
	TextToParagraph(linkParagraph, "here i will use my link: ", md.TextEmphasisNormal)
	LinkToParagraph(linkParagraph, "http://localhost:3000", "Click Here On My Link")
	section.AddElement(linkParagraph)

	section.AddElement(MkText("my text before the rule", md.TextEmphasisNormal))
	section.AddElement(MkRule())
	section.AddElement(MkText("text after the rule", md.TextEmphasisNormal))

	image := MkImage("https://www.libtechsource.com/wp-content/uploads/2018/03/code-monkey-logo.png", "My picture is here", "It's my picture's title")
	section.AddElement(image)

	document := md.NewDocumentBuilder().Sections(
		*section,
	).Build()

	result, err := generator.Render(document)
	assert.NoError(t, err)
	expected := `# Document main section, header_level n1

## Docker script example, header_level n2

` + "```\n" + code + "```" + `

## Then my table example, header_level n2

| my_table1_column n0       | my_table1_column n1       | my_table1_column n2       |
|---------------------------|---------------------------|---------------------------|
| my_table1_column_0_row n0 | my_table1_column_1_row n0 | my_table1_column_2_row n0 |
| my_table1_column_0_row n1 | my_table1_column_1_row n1 | my_table1_column_2_row n1 |
| my_table1_column_0_row n2 | my_table1_column_1_row n2 | my_table1_column_2_row n2 |
| my_table1_column_0_row n3 | my_table1_column_1_row n3 | my_table1_column_2_row n3 |
| my_table1_column_0_row n4 | my_table1_column_1_row n4 | my_table1_column_2_row n4 |
` + `
# Summary, header_level n1

**Our summary bold text...
New line of our summary and special symbols!** Small details that we have.
Normal text.
*Italic text* ___Bold Italic text___ 

## Summary subsection, header_level n2

*subsection text* 

### Summary #3, header_level n3

___subsection text 3___ 

#### Summary #4, header_level n4

##### Summary #5, header_level n5

###### Summary #6, header_level n6

here i will use my link: [Click Here On My Link](http://localhost:3000)

my text before the rule

***

text after the rule

![My picture is here](https://www.libtechsource.com/wp-content/uploads/2018/03/code-monkey-logo.png "It's my picture's title")
`
	assert.NotEmpty(t, result)
	assert.Equal(t, expected, result)

	log.Info().Msgf("result:\n%s", result)
}

func TestNewConfigurableMDGenerator_renderLists(t *testing.T) {
	generator := NewMarkdownRenderer(&Config{})
	section := md.NewSectionBuilder().Build()
	section.AddElement(mkTestHeader("List test", 1))
	section.AddElement(mkTestHeader("My test list example", 2))

	orderedList := MkList(true, nil)

	orderedListEntry1 := MkListTextEntry(orderedList, "ordered list entry 1")
	orderedListEntry2 := MkListTextEntry(orderedList, "ordered list entry 2, with nested list")
	orderedListEntry3 := MkListTextEntry(orderedList, "ordered list entry 3")
	orderedListEntry4 := MkListTextEntry(orderedList, "ordered list entry 4")

	unorderedSublist := MkList(false, orderedList)
	unorderedSublist.AddEntry(MkListTextEntry(unorderedSublist, "unordered sublist entry 1"))
	unorderedSublist.AddEntry(MkListTextEntry(unorderedSublist, "unordered sublist entry 2"))
	orderedListEntry2.AddSublist(unorderedSublist)

	orderedList.AddEntry(orderedListEntry1)
	orderedList.AddEntry(orderedListEntry2)
	orderedList.AddEntry(orderedListEntry3)
	orderedList.AddEntry(orderedListEntry4)

	unorderedList := MkList(false, nil)

	unorderedListEntry1 := MkListTextEntry(unorderedList, "unordered list entry 1")
	unorderedListEntry2 := MkListTextEntry(unorderedList, "unordered list entry 2, with nested list")
	unorderedListEntry3 := MkListTextEntry(unorderedList, "unordered list entry 3")
	unorderedListEntry4 := MkListTextEntry(unorderedList, "unordered list entry 4")

	orderedSublist := MkList(true, unorderedList)
	orderedSublist.AddEntry(MkListTextEntry(orderedSublist, "ordered sublist entry 1"))
	orderedSublistSecondEntry := MkListTextEntry(orderedSublist, "ordered sublist entry 2")
	unorderedSublistThirdLevel := MkList(false, orderedSublist)
	unorderedSublistThirdLevel.AddEntry(MkListTextEntry(unorderedSublistThirdLevel, "unordered sublist third level 1"))
	unorderedSublistThirdLevel.AddEntry(MkListTextEntry(unorderedSublistThirdLevel, "unordered sublist third level 2"))
	orderedSublistSecondEntry.AddSublist(unorderedSublistThirdLevel)
	orderedSublist.AddEntry(orderedSublistSecondEntry)
	unorderedListEntry2.AddSublist(orderedSublist)

	unorderedList.AddEntry(unorderedListEntry1)
	unorderedList.AddEntry(unorderedListEntry2)
	unorderedList.AddEntry(unorderedListEntry3)
	unorderedList.AddEntry(unorderedListEntry4)

	orderedLinkList := MkList(true, nil)
	linkEntry1 := MkListEntry(orderedLinkList, md.NewLinkBuilder().Url("https://my-url1.com").Text("My test url #1").Build())
	linkEntry2 := MkListEntry(orderedLinkList, md.NewLinkBuilder().Url("https://my-url2.com").Text("My test url #2").Build())
	linkEntry3 := MkListEntry(orderedLinkList, md.NewLinkBuilder().Url("https://my-url3.com").Text("My test url #3").Build())
	orderedLinkList.AddEntry(linkEntry1)
	orderedLinkList.AddEntry(linkEntry2)
	orderedLinkList.AddEntry(linkEntry3)

	section.AddElement(orderedList)
	section.AddElement(unorderedList)
	section.AddElement(orderedLinkList)
	document := md.NewDocumentBuilder().Sections(
		*section,
	).Build()

	result, err := generator.Render(document)
	assert.NoError(t, err)
	assert.NotEmpty(t, result)

	expected := `# List test, header_level n1

## My test list example, header_level n2

1. ordered list entry 1
2. ordered list entry 2, with nested list
     * unordered sublist entry 1
     * unordered sublist entry 2
3. ordered list entry 3
4. ordered list entry 4

* unordered list entry 1
* unordered list entry 2, with nested list
     1. ordered sublist entry 1
     2. ordered sublist entry 2
          * unordered sublist third level 1
          * unordered sublist third level 2
* unordered list entry 3
* unordered list entry 4

1. [My test url #1](https://my-url1.com)
2. [My test url #2](https://my-url2.com)
3. [My test url #3](https://my-url3.com)
`
	assert.Equal(t, expected, result)

	log.Info().Msgf("result:\n%s", result)
}

func TestNewConfigurableMDGenerator_renderBlockquote(t *testing.T) {
	generator := NewMarkdownRenderer(&Config{})
	section := md.NewSectionBuilder().Build()
	blockquote := MkBlockquote()
	blockquote.AddElement(MkText("block quote test first line", md.TextEmphasisBold))
	blockquote.AddElement(MkText("block quote test italic line", md.TextEmphasisItalic))
	blockquote.AddElement(MkText("block quote test normal line", md.TextEmphasisNormal))
	blockquote.AddElement(MkText("block quote test bold italic line", md.TextEmphasisBoldItalic))
	blockquote.AddElement(MkParagraph())
	blockquote.AddElement(MkText("block quote after empty paragraph", md.TextEmphasisNormal))
	paragraph := MkParagraph()
	paragraph.AddElement(MkText("paragraph text", md.TextEmphasisNormal))
	blockquote.AddElement(paragraph)
	blockquote.AddElement(MkText("block quote after filled paragraph", md.TextEmphasisNormal))
	section.AddElement(blockquote)

	document := md.NewDocumentBuilder().Sections(
		*section,
	).Build()

	result, err := generator.Render(document)
	assert.NoError(t, err)
	assert.NotEmpty(t, result)

	log.Info().Msgf("result:\n%s", result)
}

func mkTestTable(prefix string, columns, rows int) *md.Table {
	table := md.NewTableBuilder().Rows(rows).Build()
	for c := 0; c < columns; c++ {
		column := md.NewColumnBuilder().Index(c).Name(fmt.Sprintf("%s_column n%d", prefix, c)).Build()
		for r := 0; r < rows; r++ {
			column.AddRow(
				md.NewRowBuilder().
					Elements(
						md.NewTextBuilder().Text(fmt.Sprintf("%s_column_%d_row n%d", prefix, c, r)).Build(),
					).Build(),
			)
		}
		table.AddColumn(column)
	}

	return table
}

func mkTestHeader(text string, level md.HeaderLevel) *md.Header {
	return md.NewHeaderBuilder().Text(text + ", header_level n" + strconv.Itoa(int(level))).Level(level).Build()
}
