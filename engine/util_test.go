package engine

import (
	"testing"

	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/kordax/pb-md5-generator/internal/parser"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestUtilityWrappersCreateElements(t *testing.T) {
	table := MkTable(2)
	assert.NotNil(t, table)
	assert.Equal(t, 2, table.GetRows())

	code := MkCode("payload")
	assert.Equal(t, "payload", code.GetText())

	header := MkHeader("Section", md.HeaderLevelTwo)
	assert.Equal(t, "Section", header.GetText())
	assert.Equal(t, md.HeaderLevelTwo, header.GetLevel())

	paragraph := MkParagraph()
	assert.NotNil(t, paragraph)
	assert.Len(t, paragraph.GetElements(), 0)

	image := MkImage("https://example.com/icon.png", "icon", "demo")
	assert.Equal(t, "https://example.com/icon.png", image.GetUrl())
	assert.Equal(t, "icon", image.GetText())
	assert.Equal(t, "demo", image.GetTitle())

	rule := MkRule()
	assert.NotNil(t, rule)

	text := MkText("hello", md.TextEmphasisBold)
	assert.Equal(t, md.TextEmphasisBold, text.GetEmphasis())
	assert.Equal(t, "hello", text.GetText())
	assert.Equal(t, "hello", text.GetText())

	list := MkList(false, nil)
	assert.False(t, list.IsOrdered())
	assert.Equal(t, 0, list.GetLevel())

	parent := MkList(true, nil)
	parent.SetLevel(2)
	nested := MkList(false, parent)
	assert.Equal(t, 3, nested.GetLevel())

	listTextEntry := MkListTextEntry(list, "first")
	assert.Equal(t, "first", listTextEntry.GetElement().(*md.Text).GetText())

	link := md.NewLinkBuilder().Text("docs").Url("https://example.com").Build()
	listEntry := MkListEntry(list, link)
	assert.Equal(t, "docs", listEntry.GetElement().(*md.Link).GetText())

	blockquote := MkBlockquote()
	assert.NotNil(t, blockquote)

	row := MkRow()
	assert.NotNil(t, row)
}

func TestParagraphHelpers(t *testing.T) {
	paragraph := MkParagraph()
	TextToParagraph(paragraph, "fixed", md.TextEmphasisItalic)
	LinkToParagraph(paragraph, "https://example.com", "docs")

	elements := paragraph.GetElements()
	require.Len(t, elements, 2)
	assert.Equal(t, "fixed", elements[0].(*md.Text).GetText())
	assert.Equal(t, md.TextEmphasisItalic, elements[0].(*md.Text).GetEmphasis())
	assert.Equal(t, "docs", elements[1].(*md.Link).GetText())
	assert.Equal(t, "https://example.com", elements[1].(*md.Link).GetUrl())
}

func TestModelLinkHelpers(t *testing.T) {
	field := &MessageField{
		d: &protokit.FieldDescriptor{
			FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{
				Type:  descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum(),
				Name:  strToPtr("id"),
				Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			},
		},
	}
	fieldLink := MkFieldTypeLink(field)
	assert.Equal(t, "int64", fieldLink.GetText())
	assert.Equal(t, "#int64", fieldLink.GetUrl())

	generalLink := MkLink("CreateUser", "api.User.create")
	assert.Equal(t, "CreateUser", generalLink.GetText())
	assert.Equal(t, "#api.User.create", generalLink.GetUrl())

	parsed, err := parser.NewDescriptorParser(testDescriptorRequest(t)).Parse()
	require.NoError(t, err)
	require.NotEmpty(t, parsed)

	var enum *Enum
	var message *Message
	for _, file := range parsed {
		for _, entry := range file.Entries() {
			if enumMsg := entry.Enum(); enumMsg != nil && enum == nil {
				converted := convertEnum(*enumMsg)
				enum = &converted
			}
			if msg := entry.Message(); msg != nil && message == nil {
				converted := convertMessage(*msg)
				message = &converted
			}
		}
		if enum != nil && message != nil {
			break
		}
	}
	require.NotNil(t, enum)
	require.NotNil(t, message)

	assert.Equal(t, "doc_generator_test.LoginStatus", MkEnumRef(enum).GetName())
	assert.Equal(t, "doc_generator_test.ClientRequest", MkMessageRef(message).GetName())
}

func strToPtr(value string) *string {
	return &value
}
