package parser

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/pseudomuto/protokit"
)

func (p *DescriptorParser) getMarker(descriptor *protokit.FileDescriptor, marker string) (string, int, error) {
	for _, annotation := range p.sourceFileMarkers(descriptor.GetName()) {
		if annotation.name == marker {
			return annotation.value, annotation.index, nil
		}
	}
	return "", -1, nil
}

func (p *DescriptorParser) nextMarker(descriptor *protokit.FileDescriptor, marker string) (string, int, error) {
	if p.readOffsets == nil {
		p.readOffsets = make(map[string]int)
	}
	name := descriptor.GetName()
	for _, annotation := range p.sourceFileMarkers(name) {
		if annotation.name == marker && annotation.index >= p.readOffsets[name] {
			p.readOffsets[name] = annotation.index + len(MarkerDelimiter) + len(marker)
			return annotation.value, annotation.index, nil
		}
	}
	return "", -1, nil
}

type markerPosition struct {
	value string
	index int
}

func (p *DescriptorParser) markers(descriptor *protokit.FileDescriptor, marker string) ([]markerPosition, error) {
	result := make([]markerPosition, 0)
	for _, annotation := range p.sourceFileMarkers(descriptor.GetName()) {
		if annotation.name == marker {
			result = append(result, markerPosition{value: annotation.value, index: annotation.index})
		}
	}
	return result, nil
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
	if position, ok := p.topMessagePositions(fileDescriptor)[descriptor.GetName()]; ok {
		return position
	}
	return p.indexOf(fileDescriptor, "message "+descriptor.GetName())
}

func (p *DescriptorParser) indexOf(fileDescriptor *protokit.FileDescriptor, substr string) int {
	payload, _ := p.getPayload(fileDescriptor)
	return strings.Index(codeSource(payload), substr)
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

	if ind := commentAnnotationIndex(str, CodeMarker); ind != -1 {
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
		} else if err := validateXMLCode(block); err != nil {
			return nil, markerError{marker: MarkerDelimiter + CodeMarker, err: fmt.Errorf("invalid XML code example: %w", err)}
		}
		return &Pair[Syntax, string]{
			Left:  syntax,
			Right: block,
		}, nil
	}

	return nil, nil
}

// validateXMLCode checks document structure in addition to Decoder's strict token
// validation: Decoder also accepts streams containing multiple root elements.
func validateXMLCode(block string) error {
	decoder := xml.NewDecoder(strings.NewReader(block))
	depth, roots := 0, 0
	doctype := false
	for {
		start := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			if roots != 1 {
				return fmt.Errorf("expected one XML root element")
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 {
					return fmt.Errorf("multiple XML root elements")
				}
			}
			attributes := make(map[xml.Name]struct{}, len(value.Attr))
			for _, attribute := range value.Attr {
				if _, duplicate := attributes[attribute.Name]; duplicate {
					return fmt.Errorf("duplicate XML attribute %s", attribute.Name.Local)
				}
				attributes[attribute.Name] = struct{}{}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && (len(bytes.TrimSpace(value)) != 0 || strings.Contains(block[start:decoder.InputOffset()], "<![CDATA[")) {
				return fmt.Errorf("text outside XML root element")
			}
		case xml.ProcInst:
			if strings.EqualFold(value.Target, "xml") && (value.Target != "xml" || start != 0) {
				return fmt.Errorf("XML declaration must be at the start of the document")
			}
		case xml.Directive:
			directive := strings.TrimSpace(string(value))
			isDoctype := strings.HasPrefix(directive, "DOCTYPE") && len(directive) > len("DOCTYPE") && strings.ContainsRune(" \t\r\n", rune(directive[len("DOCTYPE")]))
			if depth != 0 || roots != 0 || doctype || !isDoctype {
				return fmt.Errorf("unsupported or misplaced XML directive")
			}
			doctype = true
		}
	}
}

func (p *DescriptorParser) parseAutocode(descriptor *protokit.Descriptor) (*AutocodeOpt, error) {
	comments := descriptor.GetComments()
	str := comments.String()

	if ind := commentAnnotationIndex(str, AutocodeMarker); ind != -1 {
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
	if from == -1 || to <= from {
		return SyntaxXml, -1, fmt.Errorf("syntax tags are missing or malformed")
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
	if err := validateIntegerFieldBounds(descriptor, result); err != nil {
		return nil, err
	}
	return result, nil
}

func applyFieldBounds(result *FieldFlags, values map[string]string) error {
	var minimum, maximum *big.Rat
	if raw, ok := values[AutocodeMaxMarker]; ok {
		value, err := ParseNumericLiteral(raw)
		if err != nil {
			return fmt.Errorf("invalid @%s value %q: %w", AutocodeMaxMarker, raw, err)
		}
		maximum = value
		approximate, _ := value.Float64()
		result.max = Some(approximate)
		result.maxLiteral = Some(strings.Clone(raw))
	}
	if raw, ok := values[AutocodeMinMarker]; ok {
		value, err := ParseNumericLiteral(raw)
		if err != nil {
			return fmt.Errorf("invalid @%s value %q: %w", AutocodeMinMarker, raw, err)
		}
		minimum = value
		approximate, _ := value.Float64()
		result.min = Some(approximate)
		result.minLiteral = Some(strings.Clone(raw))
	}
	if minimum != nil && maximum != nil && minimum.Cmp(maximum) > 0 {
		return fmt.Errorf("@%s must be less than or equal to @%s", AutocodeMinMarker, AutocodeMaxMarker)
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
