package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/pseudomuto/protokit"
	"google.golang.org/protobuf/types/descriptorpb"
)

type Generator[R any] interface {
	Generate(messages []Message) (*R, error)
}

type MDGenerator struct {
	codegen *Codegenerator
	style   GeneratorStyle
}

func NewMDGenerator(codegen *Codegenerator) *MDGenerator {
	return NewMDGeneratorWithStyle(codegen, DefaultGeneratorStyle())
}

func NewMDGeneratorWithStyle(codegen *Codegenerator, style GeneratorStyle) *MDGenerator {
	style = style.WithDefaults()
	return &MDGenerator{codegen: codegen, style: style}
}

func (g *MDGenerator) Generate(parsedFiles []ParsedFile) (*md.Document, error) {
	tocSection := md.NewSectionBuilder().Build()
	result := &md.Document{}

	sortedFiles := make([]ParsedFile, len(parsedFiles))
	copy(sortedFiles, parsedFiles)
	sort.Slice(sortedFiles, func(i, j int) bool {
		return sortedFiles[i].index < sortedFiles[j].index
	})

	collectedEntries := make([]Entry, 0)
	for _, parsedFile := range sortedFiles {
		collectedEntries = append(collectedEntries, parsedFile.entries...)
	}
	sort.SliceStable(collectedEntries, func(i, j int) bool {
		return collectedEntries[i].index < collectedEntries[j].index
	})
	allEntries := filter(collectedEntries, func(v Entry) bool {
		return v.t == EntryTypeMessage || v.t == EntryTypeEnum
	})
	enums := filter(allEntries, func(v Entry) bool {
		return v.t == EntryTypeEnum
	})
	sort.Slice(enums, func(i, j int) bool {
		return enums[i].index < enums[j].index
	})

	g.tableOfContents(allEntries, enums, tocSection)
	result.AddSection(tocSection)

	for _, parsedFile := range sortedFiles {
		entries := parsedFile.entries
		section := md.NewSectionBuilder().Build()
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i].index < entries[j].index
		})

		if parsedFile.Title() != "" {
			g.header(parsedFile.Title(), 1, section)
		} else {
			g.header(parsedFile.Filename(), 1, section)
		}
		g.header("API Description", 2, section)

		header := ""
		for _, entry := range entries {
			switch entry.t {
			case EntryTypeMessage:
				if entry.msg.header != "" && entry.msg.header != header {
					g.header(entry.msg.header, 3, section)
				}
				if entry.msg.header != "" {
					header = entry.msg.header
				}
				if entry.msg.m != nil {
					err := g.message(parsedFiles, entry.msg, section)
					if err != nil {
						return nil, err
					}
				}
			}
		}

		result.AddSection(section)
	}

	enumSection := md.NewSectionBuilder().Build()
	g.header("Enums", 2, enumSection)
	for _, enum := range enums {
		if enum.enum.e != nil {
			err := g.enum(enum.enum, enumSection)
			if err != nil {
				return nil, err
			}
		}
	}
	result.AddSection(enumSection)

	return result, nil
}

func (g *MDGenerator) tableOfContents(entries []Entry, enums []Entry, section *md.Section) {
	messages := filter(entries, func(v Entry) bool {
		return v.t == EntryTypeMessage
	})
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].index < messages[j].index
	})

	toc := MkList(false, nil)
	entry := MkListTextEntry(toc, "Table Of Contents")
	result := g.list(messages, toc, false, 0)
	entry.AddSublist(result)
	toc.AddEntry(entry)
	section.AddElement(toc)

	tocEnums := MkList(false, nil)
	entry = MkListTextEntry(tocEnums, "Enums")
	result = g.list(enums, tocEnums, false, 0)
	entry.AddSublist(result)
	tocEnums.AddEntry(entry)
	section.AddElement(tocEnums)
}

func (g *MDGenerator) header(header string, level md.HeaderLevel, section *md.Section) {
	section.AddElement(md.NewHeaderBuilder().Text(header).Level(level).Build())
}

func codeSpan(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "\\`") + "`"
}

type IdentifierStyle string

const (
	IdentifierStylePlain    IdentifierStyle = "plain"
	IdentifierStyleCode     IdentifierStyle = "code"
	IdentifierStyleBold     IdentifierStyle = "bold"
	IdentifierStyleBoldCode IdentifierStyle = "bold-code"
)

type GeneratorStyle struct {
	TableIdentifiers   IdentifierStyle
	HeadingIdentifiers IdentifierStyle
}

func DefaultGeneratorStyle() GeneratorStyle {
	return GeneratorStyle{
		TableIdentifiers:   IdentifierStyleBoldCode,
		HeadingIdentifiers: IdentifierStyleCode,
	}
}

func (s GeneratorStyle) WithDefaults() GeneratorStyle {
	defaults := DefaultGeneratorStyle()
	if s.TableIdentifiers == "" {
		s.TableIdentifiers = defaults.TableIdentifiers
	}
	if s.HeadingIdentifiers == "" {
		s.HeadingIdentifiers = defaults.HeadingIdentifiers
	}
	return s
}

func ParseIdentifierStyle(value string) (IdentifierStyle, error) {
	switch IdentifierStyle(value) {
	case IdentifierStylePlain, IdentifierStyleCode, IdentifierStyleBold, IdentifierStyleBoldCode:
		return IdentifierStyle(value), nil
	default:
		return "", fmt.Errorf("unknown identifier style %q, expected one of: plain, code, bold, bold-code", value)
	}
}

func styledIdentifier(value string, style IdentifierStyle) (string, md.TextEmphasis) {
	switch style {
	case IdentifierStyleCode:
		return codeSpan(value), md.TextEmphasisNormal
	case IdentifierStyleBold:
		return value, md.TextEmphasisBold
	case IdentifierStyleBoldCode:
		return codeSpan(value), md.TextEmphasisBold
	default:
		return value, md.TextEmphasisNormal
	}
}

func styledInlineIdentifier(value string, style IdentifierStyle) string {
	text, emphasis := styledIdentifier(value, style)
	switch emphasis {
	case md.TextEmphasisBold:
		return "**" + text + "**"
	case md.TextEmphasisItalic:
		return "*" + text + "*"
	case md.TextEmphasisBoldItalic:
		return "***" + text + "***"
	default:
		return text
	}
}

func (g *MDGenerator) list(entries []Entry, parent *md.List, ordered bool, levels int) *md.List {
	return listRecursive(entries, ordered, parent, 0, levels)
}

func (g *MDGenerator) message(files []ParsedFile, message *Message, section *md.Section) error {
	section.AddElement(MkMessageRef(message))
	name := message.m.GetFullName()
	if name == "" {
		return fmt.Errorf("empty message name received for entry: %+v", message)
	}
	var text string
	if message.description != "" {
		text = fmt.Sprintf("%s message description:", styledInlineIdentifier(name, g.style.HeadingIdentifiers))
		g.header(text, 4, section)
		section.AddElement(md.NewTextBuilder().Text(message.description).Build())
	} else {
		text = fmt.Sprintf("%s message:", styledInlineIdentifier(name, g.style.HeadingIdentifiers))
		g.header(text, 4, section)
	}

	colField := md.NewColumnBuilder().Name("Field").Build()
	colType := md.NewColumnBuilder().Name("Type").Build()
	colLabel := md.NewColumnBuilder().Name("Label").Build()
	colDesc := md.NewColumnBuilder().Name("Description").Build()
	colMin := md.NewColumnBuilder().Name("Min value").Build()
	colMax := md.NewColumnBuilder().Name("Max value").Build()
	colLen := md.NewColumnBuilder().Name("Max length/size").Build()

	minFound := false
	maxFound := false
	lenFound := false
	for i := range message.fields {
		field := &message.fields[i]
		fRow := MkRow()
		fieldText, fieldEmphasis := styledIdentifier(field.d.GetName(), g.style.TableIdentifiers)
		fRow.AddText(MkText(fieldText, fieldEmphasis))
		colField.AddRow(fRow)

		tRow := MkRow()
		tRow.AddLink(MkFieldTypeLink(field))
		colType.AddRow(tRow)

		lRow := MkRow()
		lRow.AddText(MkText(pbLabel(field.d), md.TextEmphasisNormal))
		colLabel.AddRow(lRow)

		dRow := MkRow()
		dRow.AddText(MkText(field.description, md.TextEmphasisNormal))
		colDesc.AddRow(dRow)

		minRow := MkRow()
		maxRow := MkRow()
		lenRow := MkRow()
		if field.flags.Present() {
			flags := field.flags.Get()
			flags.min.IfPresent(func(min float64) {
				minFound = true
				minRow.AddText(MkText(strconv.FormatFloat(min, 'f', -1, 64), md.TextEmphasisNormal))
			})
			flags.max.IfPresent(func(max float64) {
				maxFound = true
				maxRow.AddText(MkText(strconv.FormatFloat(max, 'f', -1, 64), md.TextEmphasisNormal))
			})
			flags.maxLength.IfPresent(func(max int) {
				lenFound = true
				lenRow.AddText(MkText(strconv.Itoa(max), md.TextEmphasisNormal))
			})
		}
		colMin.AddRow(minRow)
		colMax.AddRow(maxRow)
		colLen.AddRow(lenRow)
	}

	table := md.NewTableBuilder().Rows(len(message.fields)).Build()

	table.AddColumn(colField)
	table.AddColumn(colType)
	table.AddColumn(colLabel)
	table.AddColumn(colDesc)

	if minFound {
		table.AddColumn(colMin)
	}
	if maxFound {
		table.AddColumn(colMax)
	}
	if lenFound {
		table.AddColumn(colLen)
	}

	section.AddElement(table)

	message.code.IfPresent(func(code Pair[Syntax, string]) {
		g.header(fmt.Sprintf("%s code example:", styledInlineIdentifier(message.m.GetName(), g.style.HeadingIdentifiers)), 4, section)
		g.code(code.Right, section)
	})
	message.autocode.IfPresent(func(ac AutocodeOpt) {
		g.header(fmt.Sprintf("%s code example:", styledInlineIdentifier(message.m.GetName(), g.style.HeadingIdentifiers)), 4, section)
		generated, err := g.codegen.Generate(files, message)
		if err != nil {
			return
		}
		section.AddElement(generated)
	})

	entries := message.entries
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].index < entries[j].index
	})
	for _, entry := range entries {
		if entry.t != EntryTypeMessage || entry.msg == nil || entry.msg.m == nil {
			continue
		}
		if err := g.message(files, entry.msg, section); err != nil {
			return err
		}
	}

	return nil
}

func (g *MDGenerator) enum(enum *Enum, section *md.Section) error {
	section.AddElement(MkEnumRef(enum))
	name := enum.e.GetFullName()
	if name == "" {
		return fmt.Errorf("empty enum name received for entry: %+v", enum)
	}
	var text string
	if enum.description != "" {
		text = fmt.Sprintf("%s enum description:", styledInlineIdentifier(name, g.style.HeadingIdentifiers))
		g.header(text, 4, section)
		section.AddElement(md.NewTextBuilder().Text(enum.description).Build())
	} else {
		text = fmt.Sprintf("%s enum:", styledInlineIdentifier(name, g.style.HeadingIdentifiers))
		g.header(text, 4, section)
	}
	table := md.NewTableBuilder().Rows(len(enum.e.GetValues())).Build()

	colField := md.NewColumnBuilder().Name("Value").Build()
	colDesc := md.NewColumnBuilder().Name("Description").Build()

	for _, value := range enum.values {
		fRow := MkRow()
		valueText, valueEmphasis := styledIdentifier(value.d.GetName(), g.style.TableIdentifiers)
		fRow.AddText(MkText(valueText, valueEmphasis))
		colField.AddRow(fRow)

		dRow := MkRow()
		dRow.AddText(MkText(value.description, md.TextEmphasisNormal))
		colDesc.AddRow(dRow)
	}

	table.AddColumn(colField)
	table.AddColumn(colDesc)

	section.AddElement(table)

	return nil
}

func (g *MDGenerator) code(code string, section *md.Section) {
	section.AddElement(md.NewCodeblockBuilder().Text(code).Build())
}

func pbTypeToString(d *protokit.FieldDescriptor) string {
	switch d.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_INT64:
		return "int64"
	case descriptorpb.FieldDescriptorProto_TYPE_INT32:
		return "int32"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64:
		return "uint64"
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32:
		return "uint32"
	case descriptorpb.FieldDescriptorProto_TYPE_SINT64:
		return "int64"
	case descriptorpb.FieldDescriptorProto_TYPE_SINT32:
		return "int32"
	case descriptorpb.FieldDescriptorProto_TYPE_FIXED64:
		return "float64"
	case descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		return "float32"
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return "float64"
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		return "float32"
	case descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		return "float64"
	case descriptorpb.FieldDescriptorProto_TYPE_SFIXED32:
		return "float32"
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return "bool"
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		return "string"
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		return "[]byte"
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_GROUP:
		fallthrough
	default:
		tn := d.GetTypeName()
		if strings.HasPrefix(tn, ".") {
			return tn[1:]
		}
		return tn
	}
}

func pbLabel(d *protokit.FieldDescriptor) string {
	label := d.GetLabel()
	if label == descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL {
		return ""
	}

	return label.String()
}

func listRecursive(entries []Entry, ordered bool, parent *md.List, level, levels int) *md.List {
	list := MkList(ordered, parent)

	for _, entry := range entries {
		listEntry := md.NewListEntryBuilder(list).Build()

		switch entry.t {
		case EntryTypeMessage:
			listEntry.SetElement(MkLink(entry.msg.m.GetName(), entry.msg.m.GetFullName()))
			if level < levels {
				if len(entry.msg.entries) > 0 {
					subList := MkList(ordered, list)
					result := listRecursive(entry.msg.entries, ordered, subList, level+1, levels)
					listEntry.AddSublist(result)
				}
			}
		case EntryTypeEnum:
			listEntry.SetElement(MkLink(entry.enum.e.GetName(), entry.enum.e.GetFullName()))
		}
		list.AddEntry(listEntry)
	}

	return list
}
