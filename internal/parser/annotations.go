package parser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type annotationForm uint8

const (
	annotationBare annotationForm = iota
	annotationEquals
	annotationBracket
	annotationColon
	annotationDocument
)

type parsedAnnotation struct {
	name       string
	value      string
	attributes map[string]string
	flags      []string
	form       annotationForm
	standalone bool
}

var knownAnnotations = map[string]struct{}{
	IgnoreFileMarker:         {},
	IgnoreMarker:             {},
	TitleMarker:              {},
	HeaderMarker:             {},
	CodeMarker:               {},
	AutocodeMarker:           {},
	AutocodeMaxMarker:        {},
	AutocodeMinMarker:        {},
	AutocodeMaxLengthMarker:  {},
	AutocodeValueMarker:      {},
	AutocodeTypeMarker:       {},
	DocumentAnnotationMarker: {},
}

var messageAnnotationNames = map[string]struct{}{
	IgnoreFileMarker: {},
	IgnoreMarker:     {},
	TitleMarker:      {},
	HeaderMarker:     {},
	CodeMarker:       {},
	AutocodeMarker:   {},
}

var enumAnnotationNames = map[string]struct{}{
	IgnoreFileMarker: {},
	IgnoreMarker:     {},
	TitleMarker:      {},
	HeaderMarker:     {},
}

var enumValueAnnotationNames = map[string]struct{}{
	IgnoreMarker: {},
}

var fieldAnnotationNames = map[string]struct{}{
	IgnoreMarker:             {},
	AutocodeMaxMarker:        {},
	AutocodeMinMarker:        {},
	AutocodeMaxLengthMarker:  {},
	AutocodeValueMarker:      {},
	AutocodeTypeMarker:       {},
	DocumentAnnotationMarker: {},
}

func parseCommentAnnotations(comment string) ([]parsedAnnotation, error) {
	lines := strings.Split(comment, "\n")
	result := make([]parsedAnnotation, 0)
	for _, rawLine := range lines {
		line := cleanCommentLine(rawLine)
		if line == "" {
			continue
		}

		annotations, stop, err := parseAnnotationLine(line)
		if err != nil {
			return nil, err
		}
		result = append(result, annotations...)
		if stop {
			break
		}
	}
	return result, nil
}

func parseAnnotationLine(line string) ([]parsedAnnotation, bool, error) {
	result := make([]parsedAnnotation, 0)
	for offset := 0; offset < len(line); {
		start, end, name, ok := nextAnnotationToken(line, offset)
		if !ok {
			break
		}

		annotation := parsedAnnotation{
			name:       name,
			form:       annotationBare,
			standalone: strings.TrimSpace(line[:start]) == "",
		}
		tail := line[end:]
		known := isKnownAnnotation(name)

		switch {
		case name == DocumentAnnotationMarker:
			attributes, flags, err := parseDocumentArguments(tail)
			if err != nil {
				return nil, false, fmt.Errorf("invalid @%s annotation: %w", name, err)
			}
			annotation.form = annotationDocument
			annotation.attributes = attributes
			annotation.flags = flags
			result = append(result, annotation)
			return result, false, nil
		case strings.HasPrefix(tail, "["):
			closing := strings.IndexByte(tail, ']')
			if closing == -1 {
				if known {
					return nil, false, fmt.Errorf("invalid @%s annotation: missing closing ]", name)
				}
				result = append(result, annotation)
				return result, false, nil
			}
			annotation.form = annotationBracket
			annotation.value = tail[1:closing]
			offset = end + closing + 1
		case strings.HasPrefix(tail, "="):
			value, consumed, err := parseAnnotationValue(tail[1:])
			if err != nil {
				return nil, false, fmt.Errorf("invalid @%s annotation: %w", name, err)
			}
			annotation.form = annotationEquals
			annotation.value = value
			offset = end + 1 + consumed
		case strings.HasPrefix(tail, ":"):
			annotation.form = annotationColon
			annotation.value = strings.TrimSpace(tail[1:])
			offset = len(line)
		default:
			offset = end
		}

		result = append(result, annotation)
		if name == CodeMarker {
			return result, true, nil
		}
	}
	return result, false, nil
}

func parseDocumentArguments(input string) (map[string]string, []string, error) {
	input = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input), ":"))
	attributes := make(map[string]string)
	flags := make([]string, 0)
	for input != "" {
		keyEnd := 0
		for keyEnd < len(input) && isAnnotationNameRune(rune(input[keyEnd])) {
			keyEnd++
		}
		if keyEnd == 0 {
			return nil, nil, fmt.Errorf("expected an attribute name near %q", input)
		}
		key := input[:keyEnd]
		input = strings.TrimLeftFunc(input[keyEnd:], unicode.IsSpace)
		if !strings.HasPrefix(input, "=") {
			flags = append(flags, key)
			input = strings.TrimLeftFunc(input, unicode.IsSpace)
			continue
		}

		value, consumed, err := parseAnnotationValue(input[1:])
		if err != nil {
			return nil, nil, fmt.Errorf("attribute %s: %w", key, err)
		}
		if _, exists := attributes[key]; exists {
			return nil, nil, fmt.Errorf("duplicate attribute %q", key)
		}
		attributes[key] = value
		input = strings.TrimLeftFunc(input[1+consumed:], unicode.IsSpace)
	}
	return attributes, flags, nil
}

func parseAnnotationValue(input string) (string, int, error) {
	leading := len(input) - len(strings.TrimLeftFunc(input, unicode.IsSpace))
	input = input[leading:]
	if input == "" {
		return "", 0, fmt.Errorf("missing value")
	}

	if input[0] == '"' || input[0] == '\'' {
		quote := input[0]
		escaped := false
		for i := 1; i < len(input); i++ {
			switch {
			case escaped:
				escaped = false
			case input[i] == '\\':
				escaped = true
			case input[i] == quote:
				raw := input[:i+1]
				if quote == '\'' {
					value := strings.ReplaceAll(raw[1:len(raw)-1], "\\'", "'")
					value = strings.ReplaceAll(value, "\\\\", "\\")
					return value, leading + i + 1, nil
				}
				value, err := strconv.Unquote(raw)
				if err != nil {
					return "", 0, err
				}
				return value, leading + i + 1, nil
			}
		}
		return "", 0, fmt.Errorf("unterminated quoted value")
	}

	end := strings.IndexFunc(input, unicode.IsSpace)
	if end == -1 {
		end = len(input)
	}
	return input[:end], leading + end, nil
}

func validateCommentAnnotations(comment string, strict bool, allowed map[string]struct{}) error {
	annotations, err := parseCommentAnnotations(comment)
	if err != nil {
		return err
	}
	if !strict {
		return nil
	}

	seen := make(map[string]struct{}, len(annotations))
	for _, annotation := range annotations {
		if !isKnownAnnotation(annotation.name) {
			if annotation.standalone {
				return fmt.Errorf("unknown annotation @%s", annotation.name)
			}
			continue
		}
		if _, ok := allowed[annotation.name]; !ok {
			return fmt.Errorf("annotation @%s is not valid in this context", annotation.name)
		}
		if _, duplicate := seen[annotation.name]; duplicate {
			return fmt.Errorf("duplicate annotation @%s", annotation.name)
		}
		seen[annotation.name] = struct{}{}
	}
	return nil
}

func parseFieldAnnotations(comment string, strict bool) (map[string]string, []string, error) {
	if err := validateCommentAnnotations(comment, strict, fieldAnnotationNames); err != nil {
		return nil, nil, err
	}
	annotations, err := parseCommentAnnotations(comment)
	if err != nil {
		return nil, nil, err
	}

	values := make(map[string]string)
	flags := make([]string, 0)
	seenFlags := make(map[string]struct{})
	addFlag := func(name string) error {
		if _, exists := seenFlags[name]; exists && strict {
			return fmt.Errorf("duplicate annotation @%s", name)
		}
		seenFlags[name] = struct{}{}
		flags = append(flags, name)
		return nil
	}
	setValue := func(name, value string) error {
		if _, exists := values[name]; exists && strict {
			return fmt.Errorf("duplicate annotation @%s", name)
		}
		values[name] = value
		return nil
	}

	for _, annotation := range annotations {
		switch annotation.name {
		case DocumentAnnotationMarker:
			for key, value := range annotation.attributes {
				canonical, ok := canonicalDocumentAttribute(key)
				if !ok {
					if strict {
						return nil, nil, fmt.Errorf("unknown @doc attribute %q", key)
					}
					continue
				}
				if err := setValue(canonical, value); err != nil {
					return nil, nil, err
				}
			}
			for _, flag := range annotation.flags {
				if flag != IgnoreMarker {
					if strict {
						return nil, nil, fmt.Errorf("unknown @doc flag %q", flag)
					}
					continue
				}
				if err := addFlag(flag); err != nil {
					return nil, nil, err
				}
			}
		case IgnoreMarker:
			if err := addFlag(IgnoreMarker); err != nil {
				return nil, nil, err
			}
		case AutocodeMaxMarker, AutocodeMinMarker, AutocodeMaxLengthMarker, AutocodeValueMarker, AutocodeTypeMarker:
			if annotation.form != annotationEquals {
				if strict {
					return nil, nil, fmt.Errorf("annotation @%s requires =value", annotation.name)
				}
				continue
			}
			if err := setValue(annotation.name, annotation.value); err != nil {
				return nil, nil, err
			}
		}
	}
	return values, flags, nil
}

func canonicalDocumentAttribute(name string) (string, bool) {
	switch name {
	case AutocodeMaxMarker:
		return AutocodeMaxMarker, true
	case AutocodeMinMarker:
		return AutocodeMinMarker, true
	case AutocodeMaxLengthMarker, "max_len", "max-length":
		return AutocodeMaxLengthMarker, true
	case AutocodeValueMarker, "example":
		return AutocodeValueMarker, true
	case AutocodeTypeMarker:
		return AutocodeTypeMarker, true
	default:
		return "", false
	}
}

func commentDescription(comment string) string {
	lines := strings.Split(comment, "\n")
	description := make([]string, 0, len(lines))
	for _, rawLine := range lines {
		line := cleanCommentLine(rawLine)
		if line == "" {
			continue
		}

		cut := len(line)
		stop := false
		for offset := 0; offset < len(line); {
			start, end, name, ok := nextAnnotationToken(line, offset)
			if !ok {
				break
			}
			if isKnownAnnotation(name) {
				cut = start
				stop = name == CodeMarker || name == AutocodeMarker
				break
			}
			offset = end
		}
		if text := strings.TrimSpace(line[:cut]); text != "" {
			description = append(description, text)
		}
		if stop {
			break
		}
	}
	return strings.Join(description, " ")
}

func commentFlags(comment string) []string {
	annotations, err := parseCommentAnnotations(comment)
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(annotations))
	for _, annotation := range annotations {
		flag := annotation.name
		switch annotation.form {
		case annotationEquals:
			flag += "=" + annotation.value
		case annotationBracket:
			flag += "[" + annotation.value + "]"
		}
		result = append(result, flag)
	}
	return result
}

func indexAnnotation(text, name string) int {
	for offset := 0; offset < len(text); {
		start, end, current, ok := nextAnnotationToken(text, offset)
		if !ok {
			return -1
		}
		if current == name {
			return start
		}
		offset = end
	}
	return -1
}

func nextAnnotationToken(text string, offset int) (int, int, string, bool) {
	for offset < len(text) {
		relative := strings.IndexByte(text[offset:], '@')
		if relative == -1 {
			return 0, 0, "", false
		}
		start := offset + relative
		if start > 0 {
			previous, _ := utf8.DecodeLastRuneInString(text[:start])
			if !unicode.IsSpace(previous) && previous != '*' && previous != '/' {
				offset = start + 1
				continue
			}
		}

		end := start + 1
		for end < len(text) {
			r, size := utf8.DecodeRuneInString(text[end:])
			if !isAnnotationNameRune(r) {
				break
			}
			end += size
		}
		if end == start+1 {
			offset = start + 1
			continue
		}
		if end < len(text) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			if !unicode.IsSpace(r) && r != '=' && r != '[' && r != ':' {
				offset = end
				continue
			}
		}
		return start, end, text[start+1 : end], true
	}
	return 0, 0, "", false
}

func isAnnotationNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_'
}

func isKnownAnnotation(name string) bool {
	_, ok := knownAnnotations[name]
	return ok
}

func cleanCommentLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "/*")
	line = strings.TrimPrefix(line, "//")
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "*")
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(line, "*/")
	return strings.TrimSpace(line)
}
