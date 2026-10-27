package importer

import (
	"context"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
)

// CheckProtobufSource bounds a snapshot before parsing it. Callers that reuse a
// parsed CodeGeneratorRequest for several outputs can perform this check once.
func CheckProtobufSource(ctx context.Context, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	failure := func(code, message string) error {
		return &Error{Code: code, Source: document.Location{File: name}, Message: message}
	}
	if !validSourceName(name) {
		return failure("invalid-source", "source must be a local project-relative path")
	}
	if len(data) > MaxDocumentBytes {
		return failure("source-limit", "protobuf source exceeds the document byte limit")
	}
	if !utf8.Valid(data) {
		return failure("invalid-source", "protobuf source must be UTF-8")
	}
	depth, nodes := 0, 0
	var quote byte
	for index := 0; index < len(data); index++ {
		if index%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		character := data[index]
		if quote != 0 {
			switch character {
			case '\\':
				index++
			case quote:
				quote = 0
			}
			continue
		}
		if character == '/' && index+1 < len(data) {
			if data[index+1] == '/' {
				for index < len(data) && data[index] != '\n' {
					index++
				}
				continue
			}
			if data[index+1] == '*' {
				index += 2
				for index+1 < len(data) && (data[index] != '*' || data[index+1] != '/') {
					index++
				}
				index++
				continue
			}
		}
		switch character {
		case '\'', '"':
			quote = character
		case '{', '[', '(':
			depth++
			nodes++
		case '}', ']', ')':
			depth--
			nodes++
		case ';', ',':
			nodes++
		}
		if depth < 0 {
			return failure("invalid-protobuf", "unbalanced protobuf delimiters")
		}
		if depth > MaxDepth || nodes > MaxNodes {
			return failure("source-limit", "protobuf source exceeds its structural depth or node limit")
		}
	}
	return nil
}
