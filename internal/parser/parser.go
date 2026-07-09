package parser

import (
	"os"

	"github.com/kordax/basic-utils/v3/uopt"
	"github.com/kordax/basic-utils/v3/upair"
	"github.com/pseudomuto/protokit"
)

type Option[T any] = uopt.Opt[T]

func Some[T any](value T) Option[T] {
	return uopt.Of(value)
}

func OptionFromPtr[T any](value *T) Option[T] {
	return uopt.OfNullable(value)
}

type Pair[L, R any] = upair.Pair[L, R]

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

const MarkerDelimiter = "@"
const IgnoreFileMarker = "ignore-file"
const IgnoreMarker = "ignore"
const TitleMarker = "title"
const HeaderMarker = "header"
const CodeMarker = "code"
const AutocodeMarker = "autocode"
const AutocodeMaxMarker = "max"
const AutocodeMinMarker = "min"
const AutocodeMaxLengthMarker = "len"
const AutocodeValueMarker = "val"
const AutocodeTypeMarker = "type"

const CodeSyntaxPattern = "(" + CodeMarker + "(\\[[a-zA-Z]+\\])" + "|" + AutocodeMarker + ")"

type EntryType int

const (
	EntryTypeMessage EntryType = iota
	EntryTypeEnum    EntryType = iota
)

type AutocodeOpt struct {
	syntax Syntax
}

func (o AutocodeOpt) Syntax() Syntax {
	return o.syntax
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

func (a FieldFlags) Other() []string {
	return a.other
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

func (p ParsedFile) Entries() []Entry {
	return p.entries
}

type Entry struct {
	index int
	t     EntryType

	enum *Enum
	msg  *Message
}

func (e Entry) Index() int {
	return e.index
}

func (e Entry) Type() EntryType {
	return e.t
}

func (e Entry) Message() *Message {
	return e.msg
}

func (e Entry) Enum() *Enum {
	return e.enum
}

type Enum struct {
	description string
	e           *protokit.EnumDescriptor
	values      []EnumField
	flags       []string
}

func (e Enum) Description() string {
	return e.description
}

func (e Enum) Descriptor() *protokit.EnumDescriptor {
	return e.e
}

func (e Enum) Values() []EnumField {
	return e.values
}

func (e Enum) Flags() []string {
	return e.flags
}

type EnumField struct {
	description string
	flags       []string

	d *protokit.EnumValueDescriptor
}

func (e EnumField) Description() string {
	return e.description
}

func (e EnumField) Flags() []string {
	return e.flags
}

func (e EnumField) Descriptor() *protokit.EnumValueDescriptor {
	return e.d
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

func (m Message) Autocode() Option[AutocodeOpt] {
	return m.autocode
}

func (m Message) Code() Option[Pair[Syntax, string]] {
	return m.code
}

func (m Message) Header() string {
	return m.header
}

func (m Message) Description() string {
	return m.description
}

func (m Message) Descriptor() *protokit.Descriptor {
	return m.m
}

func (m Message) Fields() []MessageField {
	return m.fields
}

func (m Message) Entries() []Entry {
	return m.entries
}

func (m Message) Flags() []string {
	return m.flags
}

type MessageField struct {
	valueType   ValueType
	flags       Option[FieldFlags]
	description string

	d     *protokit.FieldDescriptor
	m     *protokit.Descriptor
	isMsg *Message
}

func (f MessageField) ValueType() ValueType {
	return f.valueType
}

func (f MessageField) Flags() Option[FieldFlags] {
	return f.flags
}

func (f MessageField) Description() string {
	return f.description
}

func (f MessageField) Parent() *protokit.Descriptor {
	return f.m
}

func (f MessageField) Message() *Message {
	return f.isMsg
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

func (f *MessageField) Descriptor() *protokit.FieldDescriptor {
	return f.d
}

type DescriptorParser struct {
	descriptors  []*protokit.FileDescriptor
	matchedFiles map[string]*os.File
	payload      map[string]string

	readOffsets map[string]int
}
