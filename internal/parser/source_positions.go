package parser

import (
	"math"
	"slices"
	"strings"

	"github.com/pseudomuto/protokit"
	"google.golang.org/protobuf/types/descriptorpb"
)

func (p *DescriptorParser) topMessagePositions(file *protokit.FileDescriptor) map[string]int {
	if p.messagePositions == nil {
		p.messagePositions = make(map[string]map[string]int)
	}
	if positions, ok := p.messagePositions[file.GetName()]; ok {
		return positions
	}
	source := p.payloadForFile(file.GetName())
	lineOffsets := []int{0}
	for offset := 0; offset < len(source); {
		newline := strings.IndexByte(source[offset:], '\n')
		if newline < 0 {
			break
		}
		offset += newline + 1
		lineOffsets = append(lineOffsets, offset)
	}
	positions := make(map[string]int)
	for _, location := range file.GetSourceCodeInfo().GetLocation() {
		path, span := location.GetPath(), location.GetSpan()
		if len(path) != 2 || path[0] != 4 || path[1] < 0 || int64(path[1]) >= int64(len(file.GetMessageType())) || len(span) < 2 || span[0] < 0 || span[1] < 0 || int64(span[0]) >= int64(len(lineOffsets)) {
			continue
		}
		position := lineOffsets[span[0]] + int(span[1])
		if position <= len(source) {
			positions[file.GetMessageType()[path[1]].GetName()] = position
		}
	}
	p.messagePositions[file.GetName()] = positions
	return positions
}

func messageSourcePath(file *protokit.FileDescriptor, message *protokit.Descriptor) []int32 {
	var names []string
	for current := message; current != nil; current = current.Parent {
		names = append(names, current.GetName())
	}
	messages := file.GetMessageType()
	var result []int32
	for level := len(names) - 1; level >= 0; level-- {
		found := false
		for index, candidate := range messages {
			if candidate.GetName() != names[level] {
				continue
			}
			kind := int32(3)
			if level == len(names)-1 {
				kind = 4
			}
			if index > math.MaxInt32 {
				return nil
			}
			result = append(result, kind, int32(index))
			messages = candidate.GetNestedType()
			found = true
			break
		}
		if !found {
			return nil
		}
	}
	return result
}

func sourceLocation(file *protokit.FileDescriptor, path []int32) *descriptorpb.SourceCodeInfo_Location {
	if len(path) == 0 {
		return nil
	}
	for _, location := range file.GetSourceCodeInfo().GetLocation() {
		if slices.Equal(location.GetPath(), path) {
			return location
		}
	}
	return nil
}

func sourceOffset(source string, line, column int32) int {
	if line < 0 || column < 0 {
		return -1
	}
	offset := 0
	for current := int32(0); current < line; current++ {
		newline := strings.IndexByte(source[offset:], '\n')
		if newline < 0 {
			return -1
		}
		offset += newline + 1
	}
	end := sourceLineEnd(source, offset)
	if int64(column) > int64(end-offset) {
		return -1
	}
	return offset + int(column)
}

func (p *DescriptorParser) fieldSourceIndex(field *protokit.FieldDescriptor) int {
	file := field.GetFile()
	path := messageSourcePath(file, field.Message)
	for index, candidate := range field.Message.GetField() {
		if candidate.GetName() == field.GetName() {
			if index > math.MaxInt32 {
				return -1
			}
			path = append(path, 2, int32(index))
			if location := sourceLocation(file, path); location != nil && len(location.GetSpan()) >= 2 {
				span := location.GetSpan()
				return sourceOffset(p.payloadForFile(file.GetName()), span[0], span[1])
			}
			break
		}
	}
	return p.findFieldDeclaration(file.GetName(), field.GetName())
}

func (p *DescriptorParser) messageMarkerIndex(message *protokit.Descriptor, marker string) int {
	file := message.GetFile()
	source := p.payloadForFile(file.GetName())
	span := sourceLocation(file, messageSourcePath(file, message)).GetSpan()
	if len(span) < 2 {
		return sourceAnnotationIndex(source, marker)
	}
	boundary := sourceOffset(source, span[0], span[1])
	trailing := commentAnnotationIndex(message.GetComments().GetLeading(), strings.TrimPrefix(marker, MarkerDelimiter)) < 0 && commentAnnotationIndex(message.GetComments().GetTrailing(), strings.TrimPrefix(marker, MarkerDelimiter)) >= 0
	if trailing {
		if len(span) == 3 {
			boundary = sourceOffset(source, span[0], span[2])
		} else if len(span) >= 4 {
			boundary = sourceOffset(source, span[2], span[3])
		}
	}
	result := -1
	walkSourceComments(source, func(comment string, offset int) bool {
		return walkCommentAnnotations(comment, offset, func(annotation sourceAnnotation) bool {
			if MarkerDelimiter+annotation.name != marker {
				return true
			}
			if trailing && annotation.index >= boundary {
				result = annotation.index
				return false
			}
			if !trailing && annotation.index < boundary {
				result = annotation.index
			}
			return true
		})
	})
	return result
}

// codeSource masks comments and string literals while retaining byte and line
// positions. It is used only for legacy requests without source locations.
func codeSource(source string) string {
	masked := []byte(source)
	for cursor := 0; cursor < len(source); {
		var end int
		switch {
		case source[cursor] == '\'' || source[cursor] == '"':
			end = quotedEnd(source, cursor, true)
		case strings.HasPrefix(source[cursor:], "//"):
			end = sourceLineEnd(source, cursor+2)
		case strings.HasPrefix(source[cursor:], "/*"):
			if close := strings.Index(source[cursor+2:], "*/"); close >= 0 {
				end = cursor + 2 + close + 2
			} else {
				end = len(source)
			}
		default:
			cursor++
			continue
		}
		for index := cursor; index < end; index++ {
			if masked[index] != '\r' && masked[index] != '\n' {
				masked[index] = ' '
			}
		}
		cursor = end
	}
	return string(masked)
}
