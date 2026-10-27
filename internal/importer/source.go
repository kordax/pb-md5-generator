// Package importer turns bounded local API contracts into protocol-aware documents.
package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"gopkg.in/yaml.v3"
)

const (
	MaxDocumentBytes = 4 << 20
	MaxSourceBytes   = 16 << 20
	MaxModelBytes    = 16 << 20
	MaxDocuments     = 64
	MaxNodes         = 100000
	MaxDepth         = 64
	MaxOperations    = 4096
	MaxSchemas       = 8192
	MaxReferences    = 16384
)

// Load reads a project-relative source. The owner supplies filesystem confinement;
// importers never open files, inspect the environment or retrieve remote URLs.
type Load func(name string) ([]byte, error)

// Error gives callers a stable diagnostic code, source and JSON pointer without
// embedding arbitrary contract values in an error message.
type Error struct {
	Code    string
	Source  document.Location
	Pointer string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	where := e.Source.File
	if e.Source.Line > 0 {
		where += fmt.Sprintf(":%d:%d", e.Source.Line, e.Source.Column)
	}
	if e.Pointer != "" {
		where += "#" + e.Pointer
	}
	return fmt.Sprintf("%s: %s: %s", where, e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

type parsedSource struct {
	name      string
	value     any
	raw       json.RawMessage
	locations map[string]document.Location
}

type sources struct {
	ctx        context.Context
	load       Load
	files      map[string]*parsedSource
	bytes      int
	nodes      int
	provenance int
	refs       int
}

func newSources(ctx context.Context, load Load) *sources {
	if ctx == nil {
		ctx = context.Background()
	}
	return &sources{ctx: ctx, load: load, files: make(map[string]*parsedSource)}
}

func (s *sources) fail(file, pointer, code, message string) error {
	location := document.Location{File: file}
	if source := s.files[file]; source != nil {
		if found, ok := source.locations[pointer]; ok {
			location = found
		}
	}
	return &Error{Code: code, Source: location, Pointer: pointer, Message: message}
}

func (s *sources) read(name string, supplied []byte) (*parsedSource, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if source := s.files[name]; source != nil {
		return source, nil
	}
	if !validSourceName(name) {
		return nil, s.fail(name, "", "api.source", "source must be a clean project-relative path")
	}
	if len(s.files) >= MaxDocuments {
		return nil, s.fail(name, "", "api.limit", "too many referenced documents")
	}
	data := supplied
	if data == nil {
		if s.load == nil {
			return nil, s.fail(name, "", "api.reference", "a local reference needs a source loader")
		}
		var err error
		data, err = s.load(name)
		if err != nil {
			return nil, &Error{Code: "api.source", Source: document.Location{File: name}, Message: "cannot read referenced source", Cause: err}
		}
	}
	if len(data) > MaxDocumentBytes || s.bytes > MaxSourceBytes-len(data) {
		return nil, s.fail(name, "", "api.limit", "API source byte budget exceeded")
	}
	s.bytes += len(data)
	if !utf8.Valid(data) {
		return nil, s.fail(name, "", "api.parse", "source is not valid UTF-8")
	}
	// Bound possible AST expansion before the YAML parser allocates nodes.
	units := 0
	for _, char := range data {
		if bytes.IndexByte([]byte("\n\r:{}[]&*"), char) >= 0 {
			units++
			if units > MaxNodes {
				return nil, s.fail(name, "", "api.limit", "API structural complexity budget exceeded")
			}
		}
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return nil, s.fail(name, "", "api.parse", "invalid JSON or YAML document")
	}
	if err := decoder.Decode(new(yaml.Node)); err != io.EOF {
		return nil, s.fail(name, "", "api.parse", "source must contain exactly one document")
	}
	parsed := &parsedSource{name: name, locations: make(map[string]document.Location)}
	s.files[name] = parsed
	value, err := s.convert(parsed, &node, "", 0, make(map[*yaml.Node]bool))
	if err != nil {
		return nil, err
	}
	parsed.value = value
	parsed.raw, err = json.Marshal(value)
	if err != nil {
		return nil, s.fail(name, "", "api.parse", "source contains a value that is not JSON-compatible")
	}
	if len(parsed.raw) > MaxDocumentBytes {
		return nil, s.fail(name, "", "api.limit", "canonical API source exceeds its byte budget")
	}
	return parsed, nil
}

func (s *sources) convert(source *parsedSource, node *yaml.Node, pointer string, depth int, aliases map[*yaml.Node]bool) (any, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	s.nodes++
	s.provenance += len(pointer)
	if depth > MaxDepth || s.nodes > MaxNodes || s.provenance > 8<<20 {
		return nil, s.fail(source.name, pointer, "api.limit", "API node, depth or provenance budget exceeded")
	}
	source.locations[pointer] = document.Location{File: source.name, Line: node.Line, Column: node.Column}
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return nil, s.fail(source.name, pointer, "api.parse", "empty API document")
		}
		return s.convert(source, node.Content[0], pointer, depth, aliases)
	case yaml.AliasNode:
		if node.Alias == nil || aliases[node.Alias] {
			return nil, s.fail(source.name, pointer, "api.parse", "cyclic YAML alias")
		}
		aliases[node.Alias] = true
		value, err := s.convert(source, node.Alias, pointer, depth+1, aliases)
		delete(aliases, node.Alias)
		return value, err
	case yaml.MappingNode:
		object := make(map[string]any, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Value == "<<" {
				return nil, s.fail(source.name, pointer, "api.parse", "complex mapping keys and YAML merges are unsupported")
			}
			if _, duplicate := object[key.Value]; duplicate {
				return nil, s.fail(source.name, pointer, "api.parse", "duplicate object member")
			}
			converted, err := s.convert(source, value, pointer+"/"+escapePointer(key.Value), depth+1, aliases)
			if err != nil {
				return nil, err
			}
			object[key.Value] = converted
		}
		return object, nil
	case yaml.SequenceNode:
		array := make([]any, 0, len(node.Content))
		for i, value := range node.Content {
			converted, err := s.convert(source, value, pointer+"/"+strconv.Itoa(i), depth+1, aliases)
			if err != nil {
				return nil, err
			}
			array = append(array, converted)
		}
		return array, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return nil, nil
		case "!!str", "!!timestamp":
			return strings.Clone(node.Value), nil
		case "!!bool":
			return strings.EqualFold(node.Value, "true"), nil
		case "!!int", "!!float":
			value, err := exactYAMLNumber(node.Value, node.Tag == "!!int")
			if err != nil {
				return nil, s.fail(source.name, pointer, "api.parse", "invalid or excessive numeric literal")
			}
			return value, nil
		}
	}
	return nil, s.fail(source.name, pointer, "api.parse", "unsupported YAML value")
}

var jsonNumeric = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func exactYAMLNumber(value string, integer bool) (json.Number, error) {
	value = strings.ReplaceAll(value, "_", "")
	if len(value) > 1024 {
		return "", fmt.Errorf("numeric literal is too long")
	}
	if integer {
		number, ok := new(big.Int).SetString(value, 0)
		if !ok {
			return "", fmt.Errorf("invalid integer")
		}
		return json.Number(number.String()), nil
	}
	value = strings.TrimPrefix(value, "+")
	if strings.HasPrefix(value, ".") {
		value = "0" + value
	} else if strings.HasPrefix(value, "-.") {
		value = "-0" + value[1:]
	}
	if i := strings.IndexAny(value, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(value[i+1:])
		if err != nil || exponent < -10000 || exponent > 10000 {
			return "", fmt.Errorf("numeric exponent is excessive")
		}
		if i > 0 && value[i-1] == '.' {
			value = value[:i] + "0" + value[i:]
		}
	} else if strings.HasSuffix(value, ".") {
		value += "0"
	}
	if !jsonNumeric.MatchString(value) {
		return "", fmt.Errorf("invalid decimal")
	}
	return json.Number(value), nil
}

func validSourceName(name string) bool {
	return name != "." && fs.ValidPath(name) && utf8.ValidString(name) && !strings.ContainsAny(name, "\\:#?%\x00\r\n")
}

func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func array(value any) []any           { result, _ := value.([]any); return result }
func text(value any) string           { result, _ := value.(string); return result }
func boolean(value any) bool          { result, _ := value.(bool); return result }
func raw(value any) json.RawMessage   { data, _ := json.Marshal(value); return data }

func sourceLocation(source *parsedSource, pointer string) document.Location {
	if location, ok := source.locations[pointer]; ok {
		return location
	}
	return document.Location{File: source.name}
}

func checkModelSize(value any) error {
	bytes, nodes := 0, 0
	var visit func(reflect.Value) bool
	visit = func(value reflect.Value) bool {
		nodes++
		if nodes > MaxNodes*4 || bytes > MaxModelBytes {
			return false
		}
		switch value.Kind() {
		case reflect.Pointer, reflect.Interface:
			return value.IsNil() || visit(value.Elem())
		case reflect.String:
			bytes += value.Len() * 6 // worst-case JSON escaping
		case reflect.Slice:
			if value.Type().Elem().Kind() == reflect.Uint8 {
				bytes += value.Len()
				break
			}
			fallthrough
		case reflect.Array:
			for i := 0; i < value.Len(); i++ {
				if !visit(value.Index(i)) {
					return false
				}
			}
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				if !visit(value.Field(i)) {
					return false
				}
			}
		case reflect.Map:
			iterator := value.MapRange()
			for iterator.Next() {
				if !visit(iterator.Key()) || !visit(iterator.Value()) {
					return false
				}
			}
		}
		bytes += 16
		return bytes <= MaxModelBytes
	}
	if !visit(reflect.ValueOf(value)) {
		return fmt.Errorf("API model exceeds %d bytes or its node budget", MaxModelBytes)
	}
	return nil
}

// deniedLoader ensures validator internals can never fall back to I/O.
type deniedLoader struct{}

func (deniedLoader) Load(_ string) (any, error) {
	return nil, fmt.Errorf("external schema loading is disabled")
}

func referenceURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.RawQuery != "" || parsed.User != nil {
		return nil, fmt.Errorf("invalid reference URL")
	}
	return parsed, nil
}
