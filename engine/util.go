package engine

import (
	"github.com/kordax/pb-md5-generator/engine/md"
)

//goland:noinspection GoUnusedExportedFunction
func MkTable(rows int) *md.Table {
	return md.NewTableBuilder().Rows(rows).Build()
}

func MkCode(code string) *md.Codeblock {
	return md.NewCodeblockBuilder().Text(code).Build()
}

//goland:noinspection GoUnusedExportedFunction
func MkHeader(text string, level md.HeaderLevel) *md.Header {
	return md.NewHeaderBuilder().Text(text).Level(level).Build()
}

func MkParagraph() *md.Paragraph {
	return md.NewParagraphBuilder().Build()
}

func MkImage(url, text, title string) *md.Image {
	return md.NewImageBuilder().Url(url).Text(text).Title(title).Build()
}

func MkRule() *md.Rule {
	return md.NewRuleBuilder().Build()
}

func MkText(text string, emphasis md.TextEmphasis) *md.Text {
	return md.NewTextBuilder().Text(text).Emphasis(emphasis).Build()
}

func MkList(ordered bool, parent *md.List) *md.List {
	l := md.NewListBuilder().Ordered(ordered).Build()
	if parent != nil {
		l.SetLevel(parent.GetLevel() + 1)
	}

	return l
}

func MkListTextEntry(list *md.List, text string) *md.ListEntry {
	return md.NewListEntryBuilder(list).Element(md.NewTextBuilder().Text(text).Build()).Build()
}

func MkListEntry(list *md.List, element md.Element) *md.ListEntry {
	return md.NewListEntryBuilder(list).Element(element).Build()
}

func MkBlockquote() *md.Blockquote {
	return md.NewBlockquoteBuilder().Build()
}

func TextToParagraph(p *md.Paragraph, text string, emphasis md.TextEmphasis) {
	p.AddElement(md.NewTextBuilder().Emphasis(emphasis).Text(text).Build())
}

func LinkToParagraph(p *md.Paragraph, url, label string) {
	p.AddElement(md.NewLinkBuilder().Url(url).Text(label).Build())
}

func MkRow() *md.Row {
	return md.NewRowBuilder().Build()
}

func MkFieldTypeLink(field *MessageField) *md.Link {
	tStr := pbTypeToString(field.d)
	link := "#" + tStr
	return md.NewLinkBuilder().Text(tStr).Url(link).Build()
}

func MkLink(name, fullName string) *md.Link {
	link := "#" + fullName
	return md.NewLinkBuilder().Text(name).Url(link).Build()
}

func MkEnumRef(enum *Enum) *md.HtmlRef {
	link := MkLink(enum.e.GetName(), enum.e.GetFullName())
	return md.NewHtmlRefBuilder().Name(link.GetUrl()[1:]).Build()
}

func MkMessageRef(msg *Message) *md.HtmlRef {
	link := MkLink(msg.m.GetName(), msg.m.GetFullName())
	return md.NewHtmlRefBuilder().Name(link.GetUrl()[1:]).Build()
}
