package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/pseudomuto/protokit"
	arrayutils "gitlab.com/kordax/basic-utils/array-utils"
	"google.golang.org/protobuf/types/descriptorpb"
)

type Generator[R any] interface {
	Generate(messages []Message) (*R, error)
}

type MDGenerator struct {
	codegen *Codegenerator
}

func NewMDGenerator(codegen *Codegenerator) *MDGenerator {
	return &MDGenerator{codegen: codegen}
}

func (g *MDGenerator) Generate(parsedFiles []ParsedFile) (*md.Document, error) {
	tocSection := md.NewSectionBuilder().Build()
	result := &md.Document{}

	collectedEntries := arrayutils.MapAggr(parsedFiles, func(v *ParsedFile) []Entry {
		return v.entries
	})
	sort.SliceStable(collectedEntries, func(i, j int) bool {
		return collectedEntries[i].index < collectedEntries[j].index
	})
	allEntries := arrayutils.Filter(collectedEntries, func(v *Entry) bool {
		return v.t == EntryTypeMessage || v.t == EntryTypeEnum
	})
	enums := arrayutils.Filter(allEntries, func(v *Entry) bool {
		return v.t == EntryTypeEnum
	})
	sort.Slice(enums, func(i, j int) bool {
		return enums[i].index < enums[j].index
	})

	g.tableOfContents(allEntries, enums, tocSection)
	result.AddSection(tocSection)

	sortedFiles := parsedFiles
	sort.Slice(sortedFiles, func(i, j int) bool {
		return sortedFiles[i].index < sortedFiles[j].index
	})

	for _, parsedFile := range parsedFiles {
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
				if entry.msg.header != header {
					g.header(entry.msg.header, 3, section)
				}
				header = entry.msg.header
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
	messages := arrayutils.Filter(entries, func(v *Entry) bool {
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
		text = name + " message description:"
		g.header(text, 4, section)
		section.AddElement(md.NewTextBuilder().Text(message.description).Build())
	} else {
		text = name + " message:"
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
	for _, field := range message.fields {
		fRow := MkRow()
		fRow.AddText(MkText(field.d.GetName(), md.TextEmphasisBold))
		colField.AddRow(fRow)

		tRow := MkRow()
		tRow.AddLink(MkFieldTypeLink(&field))
		colType.AddRow(tRow)

		lRow := MkRow()
		lRow.AddText(MkText(pbLabel(field.d), md.TextEmphasisNormal))
		colLabel.AddRow(lRow)

		dRow := MkRow()
		dRow.AddText(MkText(field.description, md.TextEmphasisNormal))
		colDesc.AddRow(dRow)

		if field.flags.Present() {
			flags := field.flags.Get()
			flags.min.IfPresent(func(min float64) {
				minFound = true
				row := MkRow()
				row.AddText(MkText(strconv.FormatFloat(min, 'f', -1, 64), md.TextEmphasisNormal))
				colMin.AddRow(row)
			})
			flags.max.IfPresent(func(max float64) {
				maxFound = true
				row := MkRow()
				row.AddText(MkText(strconv.FormatFloat(max, 'f', -1, 64), md.TextEmphasisNormal))
				colMax.AddRow(row)
			})
			flags.maxLength.IfPresent(func(max int) {
				lenFound = true
				row := MkRow()
				row.AddText(MkText(strconv.Itoa(max), md.TextEmphasisNormal))
				colLen.AddRow(row)
			})
		} else {
			colMin.AddRow(MkRow())
			colMax.AddRow(MkRow())
			colLen.AddRow(MkRow())
		}
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

	message.code.IfPresent(func(code arrayutils.Pair[Syntax, string]) {
		g.header(fmt.Sprintf("'%s' code example:", message.m.GetName()), 4, section)
		g.code(code.Right, section)
	})
	message.autocode.IfPresent(func(ac AutocodeOpt) {
		g.header(fmt.Sprintf("'%s' code example:", message.m.GetName()), 4, section)
		generated, err := g.codegen.Generate(files, message)
		if err != nil {
			return
		}
		section.AddElement(generated)
	})

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
		text = name + "description:"
		g.header(text, 4, section)
		section.AddElement(md.NewTextBuilder().Text(enum.description).Build())
	} else {
		text = name + ":"
		g.header(text, 4, section)
	}
	table := md.NewTableBuilder().Rows(len(enum.e.GetValues())).Build()

	colField := md.NewColumnBuilder().Name("Value").Build()
	colDesc := md.NewColumnBuilder().Name("Description").Build()

	for _, value := range enum.values {
		fRow := MkRow()
		fRow.AddText(MkText(value.d.GetName(), md.TextEmphasisBold))
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
		return "uint62"
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
