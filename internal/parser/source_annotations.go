package parser

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type sourceAnnotation struct {
	name  string
	value string
	index int
}

type annotationFence struct {
	character byte
	length    int
}

// consume reports lines belonging to a Markdown fence, including its delimiters.
func (f *annotationFence) consume(line string) bool {
	line = cleanCommentLine(line)
	if f.length != 0 {
		if count := leadingFence(line, f.character); count >= f.length && strings.TrimSpace(line[count:]) == "" {
			f.length = 0
		}
		return true
	}
	if len(line) < 3 || line[0] != '`' && line[0] != '~' {
		return false
	}
	count := leadingFence(line, line[0])
	if count < 3 {
		return false
	}
	f.character, f.length = line[0], count
	return true
}

func leadingFence(line string, character byte) int {
	count := 0
	for count < len(line) && line[count] == character {
		count++
	}
	return count
}

// walkCommentAnnotations keeps byte positions relative to the original source.
// A fixed/generated example owns the remainder of its documentation comment.
func walkCommentAnnotations(comment string, base int, visit func(sourceAnnotation) bool) bool {
	var fence annotationFence
	for offset := 0; offset < len(comment); {
		end := strings.IndexByte(comment[offset:], '\n')
		if end < 0 {
			end = len(comment)
		} else {
			end += offset
		}
		line := comment[offset:end]
		if !indentedCommentLine(line) && !fence.consume(line) {
			scanLine := maskInlineCode(line)
			for cursor := 0; cursor < len(scanLine); {
				start, stop, name, ok := nextAnnotationToken(scanLine, cursor)
				if !ok {
					break
				}
				annotation := sourceAnnotation{
					name: name, index: base + offset + start,
					value: strings.Trim(line[stop:], ":\r\n*/ \t"),
				}
				if !visit(annotation) {
					return false
				}
				if name == CodeMarker || name == AutocodeMarker {
					return true
				}
				cursor = stop
			}
		}
		if end == len(comment) {
			break
		}
		offset = end + 1
	}
	return true
}

func commentAnnotationIndex(comment, name string) int {
	result := -1
	walkCommentAnnotations(comment, 0, func(annotation sourceAnnotation) bool {
		if annotation.name == name {
			result = annotation.index
			return false
		}
		return true
	})
	return result
}

// walkSourceComments lexes protobuf strings and comments without interpreting
// marker-like data in options or literals. Adjacent // lines form one comment.
func walkSourceComments(source string, visit func(comment string, offset int) bool) {
	for offset := 0; offset < len(source); {
		switch {
		case source[offset] == '\'' || source[offset] == '"':
			offset = quotedEnd(source, offset, true)
		case strings.HasPrefix(source[offset:], "//"):
			start := offset + 2
			end := sourceLineEnd(source, start)
			for end < len(source) {
				next := end + 1
				for next < len(source) && (source[next] == ' ' || source[next] == '\t' || source[next] == '\r') {
					next++
				}
				if !strings.HasPrefix(source[next:], "//") {
					break
				}
				end = sourceLineEnd(source, next+2)
			}
			if !visit(source[start:end], start) {
				return
			}
			offset = end
		case strings.HasPrefix(source[offset:], "/*"):
			start := offset + 2
			end := strings.Index(source[start:], "*/")
			if end < 0 {
				end = len(source)
			} else {
				end += start
			}
			if !visit(source[start:end], start) {
				return
			}
			offset = min(end+2, len(source))
		default:
			offset++
		}
	}
}

func stripCommentPrefix(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	for _, prefix := range []string{"//", "/*", "*"} {
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimPrefix(strings.TrimPrefix(trimmed, prefix), " ")
		}
	}
	return line
}

func sourceLineEnd(source string, offset int) int {
	if newline := strings.IndexByte(source[offset:], '\n'); newline >= 0 {
		return offset + newline
	}
	return len(source)
}

func quotedEnd(text string, offset int, multiline bool) int {
	quote := text[offset]
	for cursor := offset + 1; cursor < len(text); cursor++ {
		if text[cursor] == '\\' {
			cursor++
		} else if text[cursor] == quote {
			return cursor + 1
		} else if !multiline && (text[cursor] == '\n' || text[cursor] == '\r') {
			return cursor
		}
	}
	return len(text)
}

func quoteInsideWord(text string, offset int) bool {
	if offset == 0 {
		return false
	}
	previous, _ := utf8.DecodeLastRuneInString(text[:offset])
	return unicode.IsLetter(previous) || unicode.IsDigit(previous)
}

// maskInlineCode recognizes matching backtick runs in linear time. Each run is
// indexed once and each width's cursor only advances, including malformed input
// containing many distinct unmatched run widths.
func maskInlineCode(text string) string {
	if !strings.ContainsRune(text, '`') {
		return text
	}
	positions := make(map[int][]int)
	for cursor := 0; cursor < len(text); {
		if text[cursor] != '`' {
			cursor++
			continue
		}
		width := leadingFence(text[cursor:], '`')
		positions[width] = append(positions[width], cursor)
		cursor += width
	}
	indices := make(map[int]int, len(positions))
	var masked []byte
	for cursor := 0; cursor < len(text); {
		if text[cursor] == '"' || text[cursor] == '\'' && !quoteInsideWord(text, cursor) {
			cursor = quotedEnd(text, cursor, false)
			continue
		}
		if text[cursor] == '=' {
			cursor++
			if cursor < len(text) && (text[cursor] == '\'' || text[cursor] == '"') {
				cursor = quotedEnd(text, cursor, false)
			} else {
				for cursor < len(text) && !strings.ContainsRune(" \t\r\n", rune(text[cursor])) {
					cursor++
				}
			}
			continue
		}
		if text[cursor] != '`' {
			cursor++
			continue
		}
		width := leadingFence(text[cursor:], '`')
		list := positions[width]
		index := indices[width]
		for index < len(list) && list[index] <= cursor {
			index++
		}
		indices[width] = index
		if index == len(list) {
			cursor += width
			continue
		}
		end := list[index] + width
		if masked == nil {
			masked = []byte(text)
		}
		for i := cursor; i < end; i++ {
			if masked[i] != '\r' && masked[i] != '\n' {
				masked[i] = ' '
			}
		}
		cursor = end
	}
	if masked == nil {
		return text
	}
	return string(masked)
}

func (p *DescriptorParser) sourceFileMarkers(name string) []sourceAnnotation {
	if p.sourceMarkers == nil {
		p.sourceMarkers = make(map[string][]sourceAnnotation)
	}
	if cached, ok := p.sourceMarkers[name]; ok {
		return cached
	}
	var result []sourceAnnotation
	walkSourceComments(p.payloadForFile(name), func(comment string, offset int) bool {
		return walkCommentAnnotations(comment, offset, func(annotation sourceAnnotation) bool {
			switch annotation.name {
			case TitleMarker, HeaderMarker, IgnoreFileMarker:
				annotation.value = strings.Clone(annotation.value)
				result = append(result, annotation)
			}
			return true
		})
	})
	p.sourceMarkers[name] = result
	return result
}

func sourceAnnotationIndex(source, marker string) int {
	result := -1
	walkSourceComments(source, func(comment string, offset int) bool {
		return walkCommentAnnotations(comment, offset, func(annotation sourceAnnotation) bool {
			if MarkerDelimiter+annotation.name == marker {
				result = annotation.index
				return false
			}
			return true
		})
	})
	return result
}
