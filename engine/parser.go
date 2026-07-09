package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kordax/basic-utils/v3/uarray"
	"github.com/pseudomuto/protokit"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
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

type DescriptorParser struct {
	descriptors  []*protokit.FileDescriptor
	matchedFiles map[string]*os.File
	payload      map[string]string

	readOffsets map[string]int
}

type SourceError struct {
	File   string
	Line   int
	Entity string
	Err    error
}

func (e SourceError) Error() string {
	location := e.File
	if e.Line > 0 {
		location = fmt.Sprintf("%s:%d", e.File, e.Line)
	}
	if e.Entity != "" {
		return fmt.Sprintf("%s: %s: %s", location, e.Entity, e.Err.Error())
	}
	return fmt.Sprintf("%s: %s", location, e.Err.Error())
}

func (e SourceError) Unwrap() error {
	return e.Err
}

type markerError struct {
	marker string
	err    error
}

func (e markerError) Error() string {
	return e.err.Error()
}

func (e markerError) Unwrap() error {
	return e.err
}

func NewDescriptorParser(request *pluginpb.CodeGeneratorRequest) *DescriptorParser {
	cmdLine := request.GetParameter()
	params := strings.Split(cmdLine, ";")
	matchedFiles := make(map[string]*os.File)
	for _, f := range request.GetFileToGenerate() {
		pathParams := filter(params, func(v string) bool {
			match, _ := regexp.MatchString("M.*proto=.+", v)
			return match
		})
		paths := mapSlice(pathParams, func(v string) Pair[string, string] {
			split := strings.Split(v, "=")
			return Pair[string, string]{Left: split[0], Right: split[1]}
		})
		if _, rawPath := containsPredicate(paths, func(v Pair[string, string]) bool {
			return strings.Trim(v.Left, "M ") == path.Base(f)
		}); rawPath == nil {
			panic(fmt.Errorf("no path provided for file: %s", f))
		} else {
			fullPath := path.Join(rawPath.Right, f)
			lstat, err := os.Stat(fullPath)
			if err != nil {
				panic(fmt.Errorf("no file found, even though matched path was provided, path: %s, err: %s", fullPath, err.Error()))
			}
			file, err := os.OpenFile(fullPath, os.O_RDONLY, lstat.Mode()) // #nosec G304 -- proto source path comes from the compiler request.
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
		_, ignore, _ := p.getMarker(descriptor, IgnoreFileMarker)
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
			if contains(IgnoreMarker, msg.flags) != -1 {
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
			if contains(IgnoreMarker, en.flags) != -1 {
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
		return nil, p.messageError(descriptor, err)
	}
	result.autocode = OptionFromPtr(autocode)
	if autocode == nil {
		code, err := p.parseCode(descriptor)
		if err != nil {
			return nil, p.messageError(descriptor, err)
		}
		result.code = OptionFromPtr(code)
	}

	for _, f := range descriptor.GetMessageFields() {
		field, err := p.parseField(f, descriptor)
		if err != nil {
			return nil, err
		}
		if flags := field.flags.Get(); flags != nil {
			if contains(IgnoreMarker, flags.other) != -1 {
				log.Warn().Msgf("ignoring field '%s'", f.GetName())
				continue
			}
		}

		result.fields = append(result.fields, *field)
	}

	for i, d := range descriptor.GetMessages() {
		nestedMsg, err := p.parseMessage(d, header)
		if err != nil {
			return nil, err
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
		return nil, p.fieldError(descriptor, err)
	}

	return NewMessageField(descriptor, m, description, vt, flags), nil
}

func (p *DescriptorParser) parseEnumValue(descriptor *protokit.EnumValueDescriptor, e *protokit.EnumDescriptor) (*EnumField, error) {
	log.Debug().Msgf("parsing message field: %s", descriptor.GetFullName())
	description := p.parseEnumValueDescription(descriptor)
	flags, err := p.parseEnumValueFlags(descriptor)
	if err != nil {
		return nil, p.enumValueError(descriptor, err)
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
		spl = mapSlice(spl, func(v string) string {
			return strings.TrimSpace(v)
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
		spl = mapSlice(spl, func(v string) string {
			return strings.TrimSpace(v)
		})
		params = spl[1:]
		return params
	}

	return nil
}

func (p *DescriptorParser) parseCode(descriptor *protokit.Descriptor) (*Pair[Syntax, string], error) {
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
				return nil, markerError{marker: MarkerDelimiter + CodeMarker, err: fmt.Errorf("failed to parse @code tag syntax: %w", err)}
			}
			block = strings.Trim(block[l:], " \n*")
		}
		block = strings.Trim(block, ":\n*/")
		if syntax == SyntaxJson {
			var indent bytes.Buffer
			err := json.Indent(&indent, []byte(block), "", "\t")
			if err != nil {
				return nil, markerError{marker: MarkerDelimiter + CodeMarker, err: fmt.Errorf("failed to marshal and validate json code: %s, code:\n%s", err.Error(), block)}
			}
			block = indent.String()
		}
		return &Pair[Syntax, string]{
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
			return nil, markerError{marker: MarkerDelimiter + AutocodeMarker, err: fmt.Errorf("invalid autocode tag provided, failed to parse syntax: %s", str)}
		}
		syntax, _, err := parseSyntax(str)
		if err != nil {
			return nil, markerError{marker: MarkerDelimiter + AutocodeMarker, err: fmt.Errorf("failed to parse @autocode tag syntax: %w", err)}
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
		spl = mapSlice(spl, func(v string) string {
			return strings.TrimSpace(v)
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
			maxLength: Option[int]{},
			min:       Option[float64]{},
			max:       Option[float64]{},
			value:     Option[string]{},
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
			result.max = Some(maxVal.(float64))
		}
		if minVal != nil {
			result.min = Some(minVal.(float64))
		}
		if length != nil {
			result.maxLength = Some(length.(int))
		}
		if value != nil {
			result.value = Some(value.(string))
		}
		if customType != nil {
			t, maperr := mapStringToValueType(customType.(string))
			if maperr != nil {
				return nil, maperr
			}
			result.customType = Some(t)
		}

		return result, nil
	}

	return nil, nil
}

func (p *DescriptorParser) parseEnumValueFlags(descriptor *protokit.EnumValueDescriptor) ([]string, error) {
	comments := descriptor.GetComments()
	str := comments.String()
	if spl := strings.Split(str, MarkerDelimiter); len(spl) > 1 {
		spl = mapSlice(spl, func(v string) string {
			return strings.TrimSpace(v)
		})
		return spl[1:], nil
	}

	return nil, nil
}

func parseAutocodeChar(marker string, parameters []string) (any, error) {
	if ind, _ := containsPredicate(parameters, func(v string) bool {
		return strings.Contains(v, marker+"=")
	}); ind != -1 {
		spl := strings.Split(parameters[ind], marker+"=")
		strVal := strings.Split(spl[1], " ")[0]
		if len(spl) > 1 {
			switch marker {
			case AutocodeValueMarker:
				return strVal, nil
			case AutocodeMaxLengthMarker:
				return strconv.Atoi(strVal)
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

func (p *DescriptorParser) messageError(descriptor *protokit.Descriptor, err error) error {
	fileName := descriptor.GetFile().GetName()
	var markerErr markerError
	if errors.As(err, &markerErr) {
		return p.sourceError(fileName, fmt.Sprintf("message %s marker %s", descriptor.GetName(), markerErr.marker), p.findMarker(fileName, markerErr.marker), err)
	}
	return p.sourceError(fileName, fmt.Sprintf("message %s", descriptor.GetName()), p.findDeclaration(fileName, "message", descriptor.GetName()), err)
}

func (p *DescriptorParser) fieldError(descriptor *protokit.FieldDescriptor, err error) error {
	fileName := descriptor.GetFile().GetName()
	return p.sourceError(fileName, fmt.Sprintf("field %s", descriptor.GetName()), p.findFieldDeclaration(fileName, descriptor.GetName()), err)
}

func (p *DescriptorParser) enumValueError(descriptor *protokit.EnumValueDescriptor, err error) error {
	fileName := descriptor.GetFile().GetName()
	return p.sourceError(fileName, fmt.Sprintf("enum value %s", descriptor.GetName()), p.findEnumValueDeclaration(fileName, descriptor.GetName()), err)
}

func (p *DescriptorParser) sourceError(fileName, entity string, index int, err error) error {
	return SourceError{
		File:   fileName,
		Line:   p.lineAt(fileName, index),
		Entity: entity,
		Err:    err,
	}
}

func (p *DescriptorParser) findDeclaration(fileName, kind, name string) int {
	payload := p.payloadForFile(fileName)
	return strings.Index(payload, kind+" "+name)
}

func (p *DescriptorParser) findFieldDeclaration(fileName, name string) int {
	payload := p.payloadForFile(fileName)
	re := regexp.MustCompile(`(?m)\b` + regexp.QuoteMeta(name) + `\b\s*=`)
	loc := re.FindStringIndex(payload)
	if loc == nil {
		return -1
	}
	return loc[0]
}

func (p *DescriptorParser) findMarker(fileName, marker string) int {
	return strings.Index(p.payloadForFile(fileName), marker)
}

func (p *DescriptorParser) findEnumValueDeclaration(fileName, name string) int {
	return p.findFieldDeclaration(fileName, name)
}

func (p *DescriptorParser) lineAt(fileName string, index int) int {
	if index < 0 {
		return 0
	}
	payload := p.payloadForFile(fileName)
	if payload == "" {
		return 0
	}
	if index > len(payload) {
		index = len(payload)
	}
	return strings.Count(payload[:index], "\n") + 1
}

func (p *DescriptorParser) payloadForFile(fileName string) string {
	if payload, ok := p.payload[fileName]; ok {
		return payload
	}
	file, ok := p.matchedFiles[fileName]
	if !ok || file == nil {
		return ""
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	readFile, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	payload := string(readFile)
	p.payload[fileName] = payload
	return payload
}

func mapSlice[T, R any](values []T, mapper func(T) R) []R {
	return uarray.Map(values, mapper)
}

func filter[T any](values []T, predicate func(T) bool) []T {
	return uarray.Filter(values, predicate)
}

func contains[T comparable](needle T, values []T) int {
	return uarray.Contains(values, needle)
}

func containsPredicate[T any](values []T, predicate func(T) bool) (int, *T) {
	return uarray.ContainsPredicate(values, predicate)
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
	case "jwt":
		return ValueTypeJWT, nil
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
