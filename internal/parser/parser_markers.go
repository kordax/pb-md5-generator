package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/pseudomuto/protokit"
)

func (p *DescriptorParser) getMarker(descriptor *protokit.FileDescriptor, marker string) (string, int, error) {
	payload, err := p.getPayload(descriptor)
	if err != nil {
		return "", -1, err
	}
	from := indexAnnotation(payload, marker)
	if from == -1 {
		return "", -1, nil
	}
	fromStr := payload[from+len(MarkerDelimiter)+len(marker):]
	to := strings.Index(fromStr, "\n")
	if to == -1 {
		to = len(fromStr)
	}
	return strings.Trim(fromStr[:to], ":\n*/ "), from, nil
}

func (p *DescriptorParser) nextMarker(descriptor *protokit.FileDescriptor, marker string) (string, int, error) {
	payload, err := p.getPayload(descriptor)
	if err != nil {
		return "", -1, err
	}
	offset := p.readOffsets[descriptor.GetName()]
	buf := payload[offset:]
	from := indexAnnotation(buf, marker)
	if from == -1 {
		return "", -1, nil
	}
	markerLength := len(MarkerDelimiter) + len(marker)
	fromStr := buf[from+markerLength:]
	to := strings.Index(fromStr, "\n")
	if to == -1 {
		to = len(fromStr)
	}
	p.readOffsets[descriptor.GetName()] += from + markerLength
	return strings.Trim(fromStr[:to], ":\n*/ "), from + offset, nil
}

type markerPosition struct {
	value string
	index int
}

func (p *DescriptorParser) markers(descriptor *protokit.FileDescriptor, marker string) ([]markerPosition, error) {
	payload, err := p.getPayload(descriptor)
	if err != nil {
		return nil, err
	}

	token := MarkerDelimiter + marker
	offset := 0
	result := make([]markerPosition, 0)
	for {
		relative := indexAnnotation(payload[offset:], marker)
		if relative == -1 {
			return result, nil
		}

		index := offset + relative
		fromStr := payload[index+len(token):]
		to := strings.Index(fromStr, "\n")
		if to == -1 {
			to = len(fromStr)
		}
		result = append(result, markerPosition{
			value: strings.Trim(fromStr[:to], ":\n*/ "),
			index: index,
		})
		offset = index + len(token)
	}
}

func (p *DescriptorParser) getPayload(descriptor *protokit.FileDescriptor) (string, error) {
	if payload, ok := p.payload[descriptor.GetName()]; ok {
		return payload, nil
	}

	readFile := p.payloadForFile(descriptor.GetName())
	p.payload[descriptor.GetName()] = readFile
	return readFile, nil
}

func (p *DescriptorParser) getMessageSourceIndex(fileDescriptor *protokit.FileDescriptor, descriptor *protokit.Descriptor) int {
	return p.indexOf(fileDescriptor, "message "+descriptor.GetName())
}

func (p *DescriptorParser) indexOf(fileDescriptor *protokit.FileDescriptor, substr string) int {
	payload, _ := p.getPayload(fileDescriptor)
	return strings.Index(payload, substr)
}

func (p *DescriptorParser) parseMessageDescription(descriptor *protokit.Descriptor) string {
	return commentDescription(descriptor.GetComments().String())
}

func (p *DescriptorParser) parseMessageFlags(descriptor *protokit.Descriptor) []string {
	return commentFlags(descriptor.GetComments().String())
}

func (p *DescriptorParser) parseEnumDescription(descriptor *protokit.EnumDescriptor) string {
	return commentDescription(descriptor.GetComments().String())
}

func (p *DescriptorParser) parseEnumFlags(descriptor *protokit.EnumDescriptor) []string {
	return commentFlags(descriptor.GetComments().String())
}

func (p *DescriptorParser) parseCode(descriptor *protokit.Descriptor) (*Pair[Syntax, string], error) {
	marker := MarkerDelimiter + CodeMarker

	comments := descriptor.GetComments()
	str := comments.String()

	if ind := indexAnnotation(str, CodeMarker); ind != -1 {
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
	comments := descriptor.GetComments()
	str := comments.String()

	if ind := indexAnnotation(str, AutocodeMarker); ind != -1 {
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
	case "json":
		return SyntaxJson, len(codeStr), nil
	case "xml":
		return SyntaxXml, len(codeStr), nil
	default:
		return SyntaxJson, -1, fmt.Errorf("unknown syntax %q, expected json or xml", code)
	}
}

func (p *DescriptorParser) parseFieldDescription(descriptor *protokit.FieldDescriptor) string {
	return commentDescription(descriptor.GetComments().String())
}

func (p *DescriptorParser) parseEnumValueDescription(descriptor *protokit.EnumValueDescriptor) string {
	return commentDescription(descriptor.GetComments().String())
}

func (p *DescriptorParser) parseFieldFlags(descriptor *protokit.FieldDescriptor) (*FieldFlags, error) {
	values, flags, err := parseFieldAnnotations(descriptor.GetComments().String(), p.strictAnnotations)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 && len(flags) == 0 {
		return nil, nil
	}

	result := &FieldFlags{other: flags}
	if err := applyFieldBounds(result, values); err != nil {
		return nil, err
	}
	if err := applyFieldMaxLength(result, values); err != nil {
		return nil, err
	}
	if raw, ok := values[AutocodeValueMarker]; ok {
		result.value = Some(raw)
	}
	if err := applyFieldCustomType(result, values); err != nil {
		return nil, err
	}
	return result, nil
}

func applyFieldBounds(result *FieldFlags, values map[string]string) error {
	if raw, ok := values[AutocodeMaxMarker]; ok {
		value, err := parseFiniteAnnotationFloat(AutocodeMaxMarker, raw)
		if err != nil {
			return err
		}
		result.max = Some(value)
	}
	if raw, ok := values[AutocodeMinMarker]; ok {
		value, err := parseFiniteAnnotationFloat(AutocodeMinMarker, raw)
		if err != nil {
			return err
		}
		result.min = Some(value)
	}
	if result.min.Present() && result.max.Present() && *result.min.Get() >= *result.max.Get() {
		return fmt.Errorf("@%s must be less than @%s", AutocodeMinMarker, AutocodeMaxMarker)
	}
	return nil
}

func applyFieldMaxLength(result *FieldFlags, values map[string]string) error {
	raw, ok := values[AutocodeMaxLengthMarker]
	if !ok {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fmt.Errorf("invalid @%s value %q: expected a non-negative integer", AutocodeMaxLengthMarker, raw)
	}
	result.maxLength = Some(value)
	return nil
}

func applyFieldCustomType(result *FieldFlags, values map[string]string) error {
	raw, ok := values[AutocodeTypeMarker]
	if !ok {
		return nil
	}
	value, err := mapStringToValueType(raw)
	if err != nil {
		return err
	}
	result.customType = Some(value)
	return nil
}

func parseFiniteAnnotationFloat(marker, raw string) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid @%s value %q: %w", marker, raw, err)
	}
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("invalid @%s value %q: expected a finite number", marker, raw)
	}
	return value, nil
}

func (p *DescriptorParser) parseEnumValueFlags(descriptor *protokit.EnumValueDescriptor) ([]string, error) {
	return commentFlags(descriptor.GetComments().String()), nil
}

func parseAutocodeChar(marker string, parameters []string) (any, error) {
	for _, parameter := range parameters {
		name, raw, ok := strings.Cut(strings.TrimSpace(parameter), "=")
		if !ok || name != marker {
			continue
		}
		value, _, err := parseAnnotationValue(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to read parameter %q: %w", marker, err)
		}
		switch marker {
		case AutocodeValueMarker, AutocodeTypeMarker:
			return value, nil
		case AutocodeMaxLengthMarker:
			return strconv.Atoi(value)
		default:
			return strconv.ParseFloat(value, 64)
		}
	}
	return nil, nil
}
