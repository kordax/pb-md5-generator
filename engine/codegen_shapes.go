package engine

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	maxExampleDepth  = 32
	maxExampleFields = 10000
)

// exampleContext belongs to one generated example. Indexing once avoids walking
// every package for each nested field, and the budget bounds branching schemas.
type exampleContext struct {
	ignoredTypes map[string]bool
	generator    *Codegenerator
	files        []ParsedFile
	messages     map[string]*Message
	active       map[*Message]bool
	remaining    int
}

func newExampleContext(generator *Codegenerator, files []ParsedFile) *exampleContext {
	context := &exampleContext{
		generator:    generator,
		ignoredTypes: ignoredTypeNames(files),
		files:        files,
		messages:     make(map[string]*Message),
		active:       make(map[*Message]bool),
		remaining:    maxExampleFields,
	}
	var collect func([]Entry)
	collect = func(entries []Entry) {
		for _, entry := range entries {
			if entry.msg != nil && entry.msg.m != nil {
				context.messages[strings.TrimPrefix(entry.msg.m.GetFullName(), ".")] = entry.msg
				collect(entry.msg.entries)
			}
		}
	}
	for _, file := range files {
		collect(file.entries)
	}
	return context
}

func (c *exampleContext) messageValue(message *Message, depth int) (map[string]any, error) {
	if depth >= maxExampleDepth {
		return nil, fmt.Errorf("example exceeds maximum message depth %d", maxExampleDepth)
	}
	if c.active[message] {
		return nil, nil
	}
	c.active[message] = true
	defer delete(c.active, message)

	result := make(map[string]any, len(message.fields))
	oneofs := make(map[int32]bool)
	for _, field := range message.fields {
		if field.d == nil {
			return nil, fmt.Errorf("message %s has a field without a descriptor", message.m.GetName())
		}
		if descriptor := field.d.FieldDescriptorProto; descriptor != nil && descriptor.OneofIndex != nil {
			index := descriptor.GetOneofIndex()
			if oneofs[index] {
				continue
			}
			oneofs[index] = true
		}
		c.remaining--
		if c.remaining < 0 {
			return nil, fmt.Errorf("example exceeds maximum field budget %d", maxExampleFields)
		}
		value, err := c.fieldValue(field, depth+1)
		if err != nil {
			return nil, fmt.Errorf("message %s field %s: %w", message.m.GetName(), field.d.GetName(), err)
		}
		if value != nil {
			result[field.d.GetName()] = value
		}
	}
	return result, nil
}

func (c *exampleContext) fieldValue(field MessageField, depth int) (any, error) {
	if c.ignoredTypes[strings.TrimPrefix(field.d.GetTypeName(), ".")] {
		return nil, nil
	}
	nested := field.isMsg
	if nested == nil && field.d.GetTypeName() != "" {
		nested = c.messages[strings.TrimPrefix(field.d.GetTypeName(), ".")]
	}
	if nested != nil && nested.m.GetOptions().GetMapEntry() {
		return c.mapValue(nested, depth)
	}
	var value any
	var err error
	if nested != nil {
		if c.active[nested] {
			if field.d.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
				return []any{}, nil
			}
			return nil, nil
		}
		value, err = c.messageValue(nested, depth)
	} else {
		value, err = c.generator.generateFromField(c.files, field)
		if err == nil && field.d.GetType() == descriptorpb.FieldDescriptorProto_TYPE_BYTES {
			if text, ok := value.(string); ok {
				value = base64.StdEncoding.EncodeToString([]byte(text))
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if field.d.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
		return []any{value}, nil
	}
	return value, nil
}

func (c *exampleContext) mapValue(entry *Message, depth int) (any, error) {
	if len(entry.fields) != 2 {
		return nil, fmt.Errorf("invalid map entry %s", entry.m.GetFullName())
	}
	key, err := c.fieldValue(entry.fields[0], depth)
	if err != nil {
		return nil, err
	}
	value, err := c.fieldValue(entry.fields[1], depth)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return map[string]any{}, nil
	}
	return map[string]any{fmt.Sprint(key): value}, nil
}

func findExampleEnum(entries []Entry, name string) *Enum {
	for _, entry := range entries {
		if entry.enum != nil && strings.TrimPrefix(entry.enum.e.GetFullName(), ".") == strings.TrimPrefix(name, ".") {
			return entry.enum
		}
		if entry.msg != nil {
			if found := findExampleEnum(entry.msg.entries, name); found != nil {
				return found
			}
		}
	}
	return nil
}

func exampleLanguage(syntax Syntax) string {
	if syntax == SyntaxXml {
		return "xml"
	}
	return "json"
}

func marshalExampleXML(value map[string]any) (string, error) {
	var output strings.Builder
	encoder := xml.NewEncoder(&output)
	encoder.Indent("", "\t")
	if err := encodeExampleXML(encoder, xml.StartElement{Name: xml.Name{Local: "example"}}, value); err != nil {
		return "", err
	}
	if err := encoder.Flush(); err != nil {
		return "", err
	}
	return output.String(), nil
}

func encodeExampleXML(encoder *xml.Encoder, start xml.StartElement, value any) error {
	if err := encoder.EncodeToken(start); err != nil {
		return err
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			// Map keys are arbitrary data, so encode them as attributes rather
			// than XML names. Protobuf identifiers are always valid names.
			child := xml.StartElement{Name: xml.Name{Local: key}}
			if !validExampleXMLName(key) {
				child.Name.Local = "entry"
				child.Attr = []xml.Attr{{Name: xml.Name{Local: "key"}, Value: key}}
			}
			if err := encodeExampleXML(encoder, child, typed[key]); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if err := encodeExampleXML(encoder, xml.StartElement{Name: xml.Name{Local: "item"}}, item); err != nil {
				return err
			}
		}
	default:
		if value != nil {
			text := fmt.Sprint(value)
			if number, ok := value.(float64); ok {
				text = strconv.FormatFloat(number, 'g', -1, 64)
			}
			if err := encoder.EncodeToken(xml.CharData(text)); err != nil {
				return err
			}
		}
	}
	return encoder.EncodeToken(start.End())
}

func validExampleXMLName(name string) bool {
	for index, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char == '_' {
			continue
		}
		if index > 0 && (char >= '0' && char <= '9' || char == '-' || char == '.') {
			continue
		}
		return false
	}
	return name != ""
}
