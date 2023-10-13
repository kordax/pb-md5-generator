package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	plugingo "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/pseudomuto/protokit"
	"github.com/rs/zerolog/log"
	arrayutils "gitlab.com/kordax/basic-utils/array-utils"
	"gitlab.com/kordax/basic-utils/opt"
	"google.golang.org/protobuf/types/descriptorpb"
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

type FieldFlags struct {
	maxLength  opt.Opt[int]
	min, max   opt.Opt[float64]
	value      opt.Opt[string]
	customType opt.Opt[ValueType]
	other      []string
}

func (a FieldFlags) GetMaxLength() opt.Opt[int] {
	return a.maxLength
}

func (a FieldFlags) GetMin() opt.Opt[float64] {
	return a.min
}

func (a FieldFlags) GetMax() opt.Opt[float64] {
	return a.max
}

func (a FieldFlags) GetValue() opt.Opt[string] {
	return a.value
}

func (a FieldFlags) GetCustomType() opt.Opt[ValueType] {
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

type Text struct {
	title string
	text  string
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
	autocode opt.Opt[AutocodeOpt]
	code     opt.Opt[arrayutils.Pair[Syntax, string]]

	header      string
	description string

	m       *protokit.Descriptor
	fields  []MessageField
	entries []Entry
	flags   []string
}

type MessageField struct {
	valueType   ValueType
	flags       opt.Opt[FieldFlags]
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
		flags:       opt.OfNullable(flags),
	}
}

func (m *MessageField) Descriptor() *protokit.FieldDescriptor {
	return m.d
}

func (m *MessageField) ValueType() ValueType {
	return m.valueType
}

type DescriptorParser struct {
	descriptors  []*protokit.FileDescriptor
	matchedFiles map[string]*os.File
	payload      map[string]string
	root         string

	document md.Document

	readOffsets map[string]int
}

func NewDescriptorParser(request *plugingo.CodeGeneratorRequest) *DescriptorParser {
	cmdLine := request.GetParameter()
	params := strings.Split(cmdLine, ";")
	matchedFiles := make(map[string]*os.File)
	for _, f := range request.GetFileToGenerate() {
		pathParams := arrayutils.Filter(params, func(v *string) bool {
			match, _ := regexp.MatchString("M.*proto=.+", *v)
			return match
		})
		paths := arrayutils.Map(pathParams, func(v *string) arrayutils.Pair[string, string] {
			return *arrayutils.NewPair(strings.Split(*v, "=")[0], strings.Split(*v, "=")[1])
		})
		if _, rawPath := arrayutils.ContainsPredicate(paths, func(v *arrayutils.Pair[string, string]) bool {
			return strings.Trim((*v).Left, "M ") == path.Base(f)
		}); rawPath == nil {
			panic(fmt.Errorf("no path provided for file: %s", f))
		} else {
			fullPath := path.Join(rawPath.Right, f)
			lstat, err := os.Stat(fullPath)
			if err != nil {
				panic(fmt.Errorf("no file found, even though matched path was provided, path: %s, err: %s", fullPath, err.Error()))
			}
			file, err := os.OpenFile(fullPath, os.O_RDONLY, lstat.Mode())
			if err != nil {
				panic(fmt.Errorf("failed to open file: %s, path: %s, err: %s", f, fullPath, err.Error()))
			}
			matchedFiles[f] = file
		}
	}

	return &DescriptorParser{
		descriptors:  protokit.ParseCodeGenRequest(request),
		matchedFiles: matchedFiles,
		readOffsets:  make(map[string]int),
		payload:      make(map[string]string),
	}
}

func (p *DescriptorParser) Parse() ([]ParsedFile, error) {
	result := make([]ParsedFile, 0)
	sort.Slice(p.descriptors, func(i, j int) bool {
		return p.descriptors[i].GetName()[0] < p.descriptors[j].GetName()[0]
	})
	msgInd := 0
	enumInd := 0
	for i, descriptor := range p.descriptors {
		entries := make([]Entry, 0)
		log.Info().Msgf("parsing file '%s' to a document", descriptor.GetName())
		log.Info().Msgf("%d messages", len(descriptor.GetMessages()))
		_, ignore, err := p.getMarker(descriptor, IgnoreFileMarker)
		if ignore != -1 {
			log.Warn().Msgf("ignoring file '%s'", descriptor.GetName())
			continue
		}
		title, _, err := p.getMarker(descriptor, TitleMarker)
		log.Info().Msgf("title: %s", title)
		if err != nil {
			return nil, err
		}
		var header string
		var headerIndex int

		for _, message := range descriptor.GetMessages() {
			h, hi, err := p.nextMarker(descriptor, HeaderMarker)
			if err != nil {
				return nil, err
			}
			sourceIndex := p.getMessageSourceIndex(descriptor, message)
			hdrValue := ""
			if h != "" {
				header = h
				headerIndex = hi
			}
			if headerIndex < sourceIndex {
				hdrValue = header
			}
			msg, err := p.parseMessage(message, hdrValue)
			if err != nil {
				return nil, err
			}
			if arrayutils.Contains(IgnoreMarker, msg.flags) != -1 {
				log.Warn().Msgf("ignoring message '%s'", message.GetName())
				continue
			}
			entries = append(entries, Entry{
				index: msgInd,
				t:     EntryTypeMessage,
				msg:   msg,
			})
			msgInd++
		}

		for _, enum := range descriptor.GetEnums() {
			en, err := p.parseEnum(enum)
			if err != nil {
				return nil, err
			}
			if arrayutils.Contains(IgnoreMarker, en.flags) != -1 {
				log.Warn().Msgf("ignoring enum '%s'", enum.GetName())
				continue
			}

			entries = append(entries, Entry{
				index: enumInd,
				t:     EntryTypeEnum,
				enum:  en,
			})
			enumInd++
		}

		parsedFile := ParsedFile{
			index:    i,
			filename: descriptor.GetName(),
			title:    title,
			entries:  entries,
		}
		result = append(result, parsedFile)
	}

	return result, nil
}

func (p *DescriptorParser) parseMessage(descriptor *protokit.Descriptor, header string) (*Message, error) {
	log.Debug().Msgf("parsing message: %s", descriptor.GetName())
	result := &Message{
		m:      descriptor,
		header: header,
	}
	result.description = p.parseMessageDescription(descriptor)
	result.flags = p.parseMessageFlags(descriptor)
	autocode, err := p.parseAutocode(descriptor)
	if err != nil {
		return nil, wrapMsgErr(descriptor, err)
	}
	result.autocode = opt.OfNullable(autocode)
	if autocode == nil {
		code, err := p.parseCode(descriptor)
		if err != nil {
			return nil, wrapMsgErr(descriptor, err)
		}
		result.code = opt.OfNullable(code)
	}

	for _, f := range descriptor.GetMessageFields() {
		field, err := p.parseField(f, descriptor)
		if err != nil {
			return nil, wrapMsgErr(descriptor, err)
		}
		if flags := field.flags.Get(); flags != nil {
			if arrayutils.Contains(IgnoreMarker, flags.other) != -1 {
				log.Warn().Msgf("ignoring field '%s'", f.GetName())
				continue
			}
		}

		result.fields = append(result.fields, *field)
	}

	for i, d := range descriptor.GetMessages() {
		nestedMsg, err := p.parseMessage(d, header)
		if err != nil {
			return nil, wrapMsgErr(d, err)
		}

		result.entries = append(result.entries, Entry{
			index: i,
			t:     EntryTypeMessage,
			msg:   nestedMsg,
		})
	}

	return result, nil
}

func (p *DescriptorParser) parseEnum(descriptor *protokit.EnumDescriptor) (*Enum, error) {
	log.Debug().Msgf("parsing enum: %s", descriptor.GetName())
	result := &Enum{
		e: descriptor,
	}
	result.description = p.parseEnumDescription(descriptor)
	result.flags = p.parseEnumFlags(descriptor)
	for _, e := range result.e.GetValues() {
		value, err := p.parseEnumValue(e, descriptor)
		if err != nil {
			return nil, err
		}

		result.values = append(result.values, *value)
	}

	return result, nil
}

func (p *DescriptorParser) parseField(descriptor *protokit.FieldDescriptor, m *protokit.Descriptor) (*MessageField, error) {
	log.Debug().Msgf("parsing message field: %s", descriptor.GetFullName())
	vt := protoToFieldValueType(descriptor)
	description := p.parseFieldDescription(descriptor)
	flags, err := p.parseFieldFlags(descriptor)
	if err != nil {
		return nil, wrapMsgErr(m, err)
	}

	return NewMessageField(descriptor, m, description, vt, flags), nil
}

func (p *DescriptorParser) parseEnumValue(descriptor *protokit.EnumValueDescriptor, e *protokit.EnumDescriptor) (*EnumField, error) {
	log.Debug().Msgf("parsing message field: %s", descriptor.GetFullName())
	description := p.parseEnumValueDescription(descriptor)
	flags, err := p.parseEnumValueFlags(descriptor)
	if err != nil {
		return nil, wrapEnumErr(e, err)
	}

	return &EnumField{
		description: description,
		d:           descriptor,
		flags:       flags,
	}, nil
}

func (p *DescriptorParser) getMarker(descriptor *protokit.FileDescriptor, marker string) (string, int, error) {
	marker = MarkerDelimiter + marker
	if from := p.nextIndex(descriptor, marker); from != -1 {
		payload, err := p.getPayload(descriptor)
		if err != nil {
			return "", -1, err
		}
		fromStr := payload[from+len(marker):]
		to := strings.Index(fromStr, "\n")

		return strings.Trim(fromStr[:to], ":\n*/ "), from, nil
	}

	return "", -1, nil
}

func (p *DescriptorParser) nextMarker(descriptor *protokit.FileDescriptor, marker string) (string, int, error) {
	marker = MarkerDelimiter + marker
	if from := p.nextIndex(descriptor, marker); from != -1 {
		payload, err := p.getPayload(descriptor)
		if err != nil {
			return "", -1, err
		}
		offset := p.readOffsets[descriptor.GetName()]
		buf := payload[offset:]
		fromStr := buf[from+len(marker):]
		to := strings.Index(fromStr, "\n")
		p.readOffsets[descriptor.GetName()] += from + len(marker)

		return strings.Trim(fromStr[:to], ":\n*/ "), from + offset, nil
	}

	return "", -1, nil
}

func (p *DescriptorParser) getPayload(descriptor *protokit.FileDescriptor) (string, error) {
	if payload, ok := p.payload[descriptor.GetName()]; ok {
		return payload, nil
	}

	readFile, err := io.ReadAll(p.matchedFiles[descriptor.GetName()])
	if err != nil {
		return "", err
	}
	p.payload[descriptor.GetName()] = string(readFile)
	return string(readFile), err
}

func (p *DescriptorParser) getMessageSourceIndex(fileDescriptor *protokit.FileDescriptor, descriptor *protokit.Descriptor) int {
	return p.indexOf(fileDescriptor, "message "+descriptor.GetName())
}

func (p *DescriptorParser) indexOf(fileDescriptor *protokit.FileDescriptor, substr string) int {
	payload, _ := p.getPayload(fileDescriptor)
	return strings.Index(payload, substr)
}

func (p *DescriptorParser) nextIndex(fileDescriptor *protokit.FileDescriptor, substr string) int {
	payload, err := p.getPayload(fileDescriptor)
	if err != nil {
		panic(err)
	}
	offset := p.readOffsets[*fileDescriptor.Name]
	buf := payload[offset:]

	return strings.Index(buf, substr)
}

func (p *DescriptorParser) parseMessageDescription(descriptor *protokit.Descriptor) string {
	comments := descriptor.GetComments()
	str := comments.String()
	desc := ""
	if spl := strings.Split(str, MarkerDelimiter); len(spl) > 0 {
		desc = spl[0]
	}

	return strings.Trim(strings.ReplaceAll(desc, "\n", " "), "*\n ")
}

func (p *DescriptorParser) parseMessageFlags(descriptor *protokit.Descriptor) []string {
	comments := descriptor.GetComments()
	str := comments.String()
	var params []string
	if spl := strings.Split(str, MarkerDelimiter); len(spl) > 1 {
		spl = arrayutils.Map(spl, func(v *string) string {
			return strings.TrimSpace(*v)
		})
		params = spl[1:]
		return params
	}

	return nil
}

func (p *DescriptorParser) parseEnumDescription(descriptor *protokit.EnumDescriptor) string {
	comments := descriptor.GetComments()
	str := comments.String()
	desc := ""
	if spl := strings.Split(str, MarkerDelimiter); len(spl) > 0 {
		desc = spl[0]
	}

	return strings.Trim(strings.ReplaceAll(desc, "\n", " "), "*\n ")
}

func (p *DescriptorParser) parseEnumFlags(descriptor *protokit.EnumDescriptor) []string {
	comments := descriptor.GetComments()
	str := comments.String()
	var params []string
	if spl := strings.Split(str, MarkerDelimiter); len(spl) > 1 {
		spl = arrayutils.Map(spl, func(v *string) string {
			return strings.TrimSpace(*v)
		})
		params = spl[1:]
		return params
	}

	return nil
}

func (p *DescriptorParser) parseCode(descriptor *protokit.Descriptor) (*arrayutils.Pair[Syntax, string], error) {
	marker := MarkerDelimiter + CodeMarker

	comments := descriptor.GetComments()
	str := comments.String()

	if ind := strings.Index(str, marker); ind != -1 {
		block := str[ind+len(marker):]
		str = str[ind:]
		str = strings.Split(str, "\n")[0]
		syntax := SyntaxJson
		matched, err := regexp.MatchString(CodeSyntaxPattern, str)
		if err != nil {
			return nil, err
		}
		if matched {
			var l int
			syntax, l, err = parseSyntax(str)
			if err != nil {
				return nil, wrapMsgErr(descriptor, fmt.Errorf("failed to parse @code tag syntax: %s", err))
			}
			block = strings.Trim(block[l:], " \n*")
		}
		block = strings.Trim(block, ":\n*/")
		if syntax == SyntaxJson {
			var indent bytes.Buffer
			err := json.Indent(&indent, []byte(block), "", "\t")
			if err != nil {
				return nil, fmt.Errorf("failed to marshal and validate json code: %s, code:\n%s", err.Error(), block)
			}
			block = indent.String()
		}
		return &arrayutils.Pair[Syntax, string]{
			Left:  syntax,
			Right: block,
		}, nil
	}

	return nil, nil
}

func (p *DescriptorParser) parseAutocode(descriptor *protokit.Descriptor) (*AutocodeOpt, error) {
	marker := MarkerDelimiter + AutocodeMarker

	comments := descriptor.GetComments()
	str := comments.String()

	if ind := strings.Index(str, marker); ind != -1 {
		str = str[ind:]
		str = strings.Split(str, "\n")[0]
		matched, _ := regexp.MatchString(CodeSyntaxPattern, str)
		if !matched {
			return nil, fmt.Errorf("invalid autocode tag provided, failed to parse syntax: %s", str)
		}
		syntax, _, err := parseSyntax(str)
		if err != nil {
			return nil, wrapMsgErr(descriptor, fmt.Errorf("failed to parse @autocode tag syntax: %s", err))
		}
		return &AutocodeOpt{syntax: syntax}, nil
	}

	return nil, nil
}

func parseSyntax(markerStr string) (Syntax, int, error) {
	from := strings.Index(markerStr, "[")
	to := strings.Index(markerStr, "]")
	if from == -1 || to == -1 {
		return SyntaxXml, -1, fmt.Errorf("syntax tags are missing")
	}
	codeStr := markerStr[from : to+1]
	code := codeStr[1 : len(codeStr)-1]
	if len(markerStr) > to+1 && markerStr[to+1] == ':' {
		codeStr += ":"
	}
	switch strings.ToLower(code) {
	case "xml":
		return SyntaxXml, len(codeStr), nil
	default:
		return SyntaxJson, len(codeStr), nil
	}
}

func (p *DescriptorParser) parseFieldDescription(descriptor *protokit.FieldDescriptor) string {
	comments := descriptor.GetComments()
	str := comments.String()
	desc := ""
	if spl := strings.Split(str, MarkerDelimiter); len(spl) > 0 {
		desc = spl[0]
	}

	return strings.Trim(strings.ReplaceAll(desc, "\n", " "), "*\n ")
}

func (p *DescriptorParser) parseEnumValueDescription(descriptor *protokit.EnumValueDescriptor) string {
	comments := descriptor.GetComments()
	str := comments.String()
	desc := ""
	if spl := strings.Split(str, MarkerDelimiter); len(spl) > 0 {
		desc = spl[0]
	}

	return strings.Trim(strings.ReplaceAll(desc, "\n", " "), "*\n ")
}

func (p *DescriptorParser) parseFieldFlags(descriptor *protokit.FieldDescriptor) (*FieldFlags, error) {
	comments := descriptor.GetComments()
	str := comments.String()
	var params []string
	if spl := strings.Split(str, MarkerDelimiter); len(spl) > 1 {
		spl = arrayutils.Map(spl, func(v *string) string {
			return strings.TrimSpace(*v)
		})
		params = spl[1:]

		maxVal, err := parseAutocodeChar(AutocodeMaxMarker, params)
		if err != nil {
			return nil, err
		}
		minVal, err := parseAutocodeChar(AutocodeMinMarker, params)
		if err != nil {
			return nil, err
		}
		length, err := parseAutocodeChar(AutocodeMaxLengthMarker, params)
		if err != nil {
			return nil, err
		}
		value, err := parseAutocodeChar(AutocodeValueMarker, params)
		if err != nil {
			return nil, err
		}
		customType, err := parseAutocodeChar(AutocodeTypeMarker, params)
		if err != nil {
			return nil, err
		}

		result := &FieldFlags{
			maxLength: opt.Opt[int]{},
			min:       opt.Opt[float64]{},
			max:       opt.Opt[float64]{},
			value:     opt.Opt[string]{},
		}
		for _, param := range params {
			if param != AutocodeMaxMarker &&
				param != AutocodeMinMarker &&
				param != AutocodeMaxLengthMarker &&
				param != AutocodeValueMarker {
				result.other = append(result.other, param)
			}
		}
		if maxVal != nil {
			result.max = opt.Of(maxVal.(float64))
		}
		if minVal != nil {
			result.min = opt.Of(minVal.(float64))
		}
		if length != nil {
			result.maxLength = opt.Of(int(length.(uint64)))
		}
		if value != nil {
			result.value = opt.Of(value.(string))
		}
		if customType != nil {
			t, maperr := mapStringToValueType(customType.(string))
			if maperr != nil {
				return nil, maperr
			}
			result.customType = opt.Of(t)
		}

		return result, nil
	}

	return nil, nil
}

func (p *DescriptorParser) parseEnumValueFlags(descriptor *protokit.EnumValueDescriptor) ([]string, error) {
	comments := descriptor.GetComments()
	str := comments.String()
	if spl := strings.Split(str, MarkerDelimiter); len(spl) > 1 {
		spl = arrayutils.Map(spl, func(v *string) string {
			return strings.TrimSpace(*v)
		})
		return spl[1:], nil
	}

	return nil, nil
}

func parseAutocodeChar(marker string, parameters []string) (any, error) {
	if ind, _ := arrayutils.ContainsPredicate(parameters, func(v *string) bool {
		return strings.Contains(*v, marker+"=")
	}); ind != -1 {
		spl := strings.Split(parameters[ind], marker+"=")
		strVal := strings.Split(spl[1], " ")[0]
		if len(spl) > 1 {
			switch marker {
			case AutocodeValueMarker:
				return strVal, nil
			case AutocodeMaxLengthMarker:
				return strconv.ParseUint(strVal, 10, 64)
			case AutocodeMinMarker:
				return strconv.ParseFloat(strVal, 64)
			case AutocodeMaxMarker:
				return strconv.ParseFloat(strVal, 64)
			case AutocodeTypeMarker:
				return strVal, nil
			default:
				return strconv.ParseFloat(strVal, 64)
			}
		} else {
			return nil, fmt.Errorf("failed to read parameter '%s', invalid format: %s", marker, parameters[ind])
		}
	}

	return nil, nil
}

func wrapMsgErr(descriptor *protokit.Descriptor, err error) error {
	return fmt.Errorf("failed to parse/process message %s\n%s", descriptor.GetName(), err.Error())
}

func wrapEnumErr(descriptor *protokit.EnumDescriptor, err error) error {
	return fmt.Errorf("failed to parse/process enum %s:%s", descriptor.GetName(), err.Error())
}

func protoToFieldValueType(d *protokit.FieldDescriptor) ValueType {
	switch d.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_INT64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_INT32:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_SINT64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_SINT32:
		return ValueTypeInt
	case descriptorpb.FieldDescriptorProto_TYPE_FIXED64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_SFIXED32:
		return ValueTypeFloat
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return ValueTypeBool
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		if strings.Contains(strings.ToLower(d.GetName()), "uuid") {
			return ValueTypeUUID
		}
		if strings.Contains(strings.ToLower(d.GetName()), "email") {
			return ValueTypeEmail
		}
		if strings.Contains(strings.ToLower(d.GetName()), "phone") {
			return ValueTypePhone
		}
		if strings.Contains(strings.ToLower(d.GetName()), "password") {
			return ValueTypePassword
		}
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		return ValueTypeString
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		return ValueTypeEnum
	case descriptorpb.FieldDescriptorProto_TYPE_GROUP:
		fallthrough
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		return ValueTypeStruct
	}

	return ValueTypeString
}

func mapStringToValueType(customType string) (ValueType, error) {
	switch strings.ToLower(customType) {
	case "int":
		return ValueTypeInt, nil
	case "uint":
		return ValueTypeUInt, nil
	case "float":
		return ValueTypeFloat, nil
	case "bool":
		return ValueTypeBool, nil
	case "string":
		return ValueTypeString, nil
	case "enum":
		return ValueTypeEnum, nil
	case "uuid":
		return ValueTypeUUID, nil
	case "struct":
		return ValueTypeStruct, nil
	case "email":
		return ValueTypeEmail, nil
	case "phone":
		return ValueTypePhone, nil
	case "password":
		return ValueTypePassword, nil
	default:
		return 0, fmt.Errorf("unknown custom type provided: %s", customType)
	}
}
