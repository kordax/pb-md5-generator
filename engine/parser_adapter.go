package engine

import (
	"github.com/kordax/pb-md5-generator/internal/parser"
	"google.golang.org/protobuf/types/pluginpb"
)

type DescriptorParser struct {
	delegate *parser.DescriptorParser
}

func NewDescriptorParser(request *pluginpb.CodeGeneratorRequest) *DescriptorParser {
	return &DescriptorParser{
		delegate: parser.NewDescriptorParser(request),
	}
}

func (p *DescriptorParser) Parse() ([]ParsedFile, error) {
	result, err := p.delegate.Parse()
	if err != nil {
		return nil, err
	}
	return convertParsedFiles(result), nil
}

func mapStringToValueType(customType string) (ValueType, error) {
	parsed, err := parser.MapStringToValueType(customType)
	if err != nil {
		var zero ValueType
		return zero, err
	}
	return ValueType(parsed), nil
}

func filter[T any](values []T, predicate func(T) bool) []T {
	return parser.Filter(values, predicate)
}

func convertParsedFiles(files []parser.ParsedFile) []ParsedFile {
	result := make([]ParsedFile, 0, len(files))
	for _, file := range files {
		result = append(result, convertParsedFile(file))
	}
	return result
}

func convertParsedFile(file parser.ParsedFile) ParsedFile {
	entries := make([]Entry, 0, len(file.Entries()))
	for _, entry := range file.Entries() {
		entries = append(entries, convertEntry(entry))
	}

	return ParsedFile{
		index:    file.Index(),
		filename: file.Filename(),
		title:    file.Title(),
		entries:  entries,
	}
}

func convertEntry(entry parser.Entry) Entry {
	result := Entry{
		index: entry.Index(),
		t:     convertEntryType(entry.Type()),
	}

	if msg := entry.Message(); msg != nil {
		converted := convertMessage(*msg)
		result.msg = &converted
	}
	if en := entry.Enum(); en != nil {
		converted := convertEnum(*en)
		result.enum = &converted
	}

	return result
}

func convertEnum(value parser.Enum) Enum {
	values := make([]EnumField, 0, len(value.Values()))
	for _, item := range value.Values() {
		values = append(values, convertEnumField(item))
	}
	return Enum{
		description: value.Description(),
		e:           value.Descriptor(),
		values:      values,
		flags:       value.Flags(),
	}
}

func convertEnumField(value parser.EnumField) EnumField {
	return EnumField{
		description: value.Description(),
		flags:       value.Flags(),
		d:           value.Descriptor(),
	}
}

func convertMessage(value parser.Message) Message {
	fields := make([]MessageField, 0, len(value.Fields()))
	for _, field := range value.Fields() {
		fields = append(fields, convertMessageField(field))
	}
	entries := make([]Entry, 0, len(value.Entries()))
	for _, entry := range value.Entries() {
		entries = append(entries, convertEntry(entry))
	}

	return Message{
		autocode:    convertAutocode(value.Autocode()),
		code:        convertCode(value.Code()),
		header:      value.Header(),
		description: value.Description(),
		m:           value.Descriptor(),
		fields:      fields,
		entries:     entries,
		flags:       value.Flags(),
	}
}

func convertMessageField(value parser.MessageField) MessageField {
	var nested *Message
	if inner := value.Message(); inner != nil {
		converted := convertMessage(*inner)
		nested = &converted
	}
	return MessageField{
		valueType:   ValueType(value.ValueType()),
		flags:       convertFieldFlags(value.Flags()),
		description: value.Description(),
		d:           value.Descriptor(),
		m:           value.Parent(),
		isMsg:       nested,
	}
}

func convertFieldFlags(flags parser.Option[parser.FieldFlags]) Option[FieldFlags] {
	if !flags.Present() {
		return Option[FieldFlags]{}
	}
	src := flags.Get()
	var customType Option[ValueType]
	if ct := src.GetCustomType(); ct.Present() {
		cast := ValueType(*ct.Get())
		customType = OptionFromPtr(&cast)
	} else {
		customType = Option[ValueType]{}
	}
	return OptionFromPtr(&FieldFlags{
		maxLength:  src.GetMaxLength(),
		min:        src.GetMin(),
		max:        src.GetMax(),
		value:      src.GetValue(),
		customType: customType,
		other:      src.Other(),
	})
}

func convertAutocode(value parser.Option[parser.AutocodeOpt]) Option[AutocodeOpt] {
	if !value.Present() {
		return Option[AutocodeOpt]{}
	}
	source := value.Get()
	return Some(AutocodeOpt{syntax: Syntax(source.Syntax())})
}

func convertEntryType(value parser.EntryType) EntryType {
	switch value {
	case parser.EntryTypeEnum:
		return EntryTypeEnum
	default:
		return EntryTypeMessage
	}
}

func convertCode(value parser.Option[parser.Pair[parser.Syntax, string]]) Option[Pair[Syntax, string]] {
	if !value.Present() {
		return Option[Pair[Syntax, string]]{}
	}
	source := value.Get()
	return OptionFromPtr(&Pair[Syntax, string]{
		Left:  Syntax(source.Left),
		Right: source.Right,
	})
}
