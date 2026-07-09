package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/pseudomuto/protokit"
)

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
