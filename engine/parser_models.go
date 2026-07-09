package engine

import (
	"github.com/pseudomuto/protokit"
)

type Syntax int

const (
	SyntaxJson Syntax = iota
	SyntaxXml
)

type ValueType int

const (
	ValueTypeInt ValueType = iota
	ValueTypeUInt
	ValueTypeFloat
	ValueTypeBool
	ValueTypeString
	ValueTypeEnum
	ValueTypeJWT
	ValueTypeUUID
	ValueTypeStruct
	ValueTypeEmail
	ValueTypePhone
	ValueTypePassword
)

type EntryType int

const (
	EntryTypeMessage EntryType = iota
	EntryTypeEnum
)

type AutocodeOpt struct {
	syntax Syntax
}

type FieldFlags struct {
	maxLength  Option[int]
	min, max   Option[float64]
	value      Option[string]
	customType Option[ValueType]
	other      []string
}

func (a FieldFlags) GetMaxLength() Option[int] {
	return a.maxLength
}

func (a FieldFlags) GetMin() Option[float64] {
	return a.min
}

func (a FieldFlags) GetMax() Option[float64] {
	return a.max
}

func (a FieldFlags) GetValue() Option[string] {
	return a.value
}

func (a FieldFlags) GetCustomType() Option[ValueType] {
	return a.customType
}

type ParsedFile struct {
	index    int
	filename string
	title    string
	entries  []Entry
}

func (p ParsedFile) Index() int {
	return p.index
}

func (p ParsedFile) Filename() string {
	return p.filename
}

func (p ParsedFile) Title() string {
	return p.title
}

type Entry struct {
	index int
	t     EntryType

	enum *Enum
	msg  *Message
}

type Enum struct {
	description string
	e           *protokit.EnumDescriptor
	values      []EnumField
	flags       []string
}

type EnumField struct {
	description string
	flags       []string

	d *protokit.EnumValueDescriptor
}

type Message struct {
	autocode Option[AutocodeOpt]
	code     Option[Pair[Syntax, string]]

	header      string
	description string

	m       *protokit.Descriptor
	fields  []MessageField
	entries []Entry
	flags   []string
}

type MessageField struct {
	valueType   ValueType
	flags       Option[FieldFlags]
	description string

	d     *protokit.FieldDescriptor
	m     *protokit.Descriptor
	isMsg *Message
}

func NewMessageField(d *protokit.FieldDescriptor, m *protokit.Descriptor, description string, valueType ValueType, flags *FieldFlags) *MessageField {
	return &MessageField{
		d:           d,
		m:           m,
		description: description,
		valueType:   valueType,
		flags:       OptionFromPtr(flags),
	}
}

func (m *MessageField) Descriptor() *protokit.FieldDescriptor {
	return m.d
}

func (m *MessageField) ValueType() ValueType {
	return m.valueType
}
