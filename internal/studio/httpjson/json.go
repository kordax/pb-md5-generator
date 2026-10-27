// Package httpjson decodes bounded CMS request objects without ambiguous fields.
package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid JSON request object")
	ErrTooLarge    = errors.New("JSON request exceeds its resource limit")
	ErrContentType = errors.New("request content type must be application/json with UTF-8 encoding")
)

const (
	maxBytes = 32 << 20
	maxDepth = 32
	maxNodes = 50000
)

var rawMessageType = reflect.TypeOf(json.RawMessage{})

// Decode checks the media type and wire limit before decoding a request object.
func Decode(writer http.ResponseWriter, request *http.Request, target any, limit int64) error {
	if err := request.Context().Err(); err != nil {
		return err
	}
	media, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return ErrContentType
	}
	if charset, exists := parameters["charset"]; exists && !strings.EqualFold(charset, "utf-8") {
		return ErrContentType
	}
	if request.Header.Get("Content-Encoding") != "" && !strings.EqualFold(request.Header.Get("Content-Encoding"), "identity") {
		return ErrContentType
	}
	if limit <= 0 || limit > maxBytes || request.ContentLength > limit {
		return ErrTooLarge
	}
	if request.Body == nil {
		return ErrInvalid
	}
	body := http.MaxBytesReader(writer, request.Body, limit)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			return ErrTooLarge
		}
		return ErrInvalid
	}
	if err := request.Context().Err(); err != nil {
		return err
	}
	return Unmarshal(data, target)
}

// Validate checks syntax, UTF-8, nesting, tokens and case-folded duplicate fields.
// It is intended for CMS request DTOs, whose field names are case-sensitive.
func Validate(data []byte) error {
	return validate(data, nil)
}

// Unmarshal requires exact JSON field names and assigns the target only on success.
// Request structs use explicit fields; anonymous embedding is deliberately rejected.
// Null is accepted only through RawMessage or interface values, not optional pointers.
func Unmarshal(data []byte, target any) error {
	value := reflect.ValueOf(target)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return ErrInvalid
	}
	if err := validate(data, value.Type().Elem()); err != nil {
		return err
	}
	decoded := reflect.New(value.Type().Elem())
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(decoded.Interface()); err != nil {
		return ErrInvalid
	}
	value.Elem().Set(decoded.Elem())
	return nil
}

func validate(data []byte, target reflect.Type) error {
	if len(data) > maxBytes {
		return ErrTooLarge
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || !utf8.Valid(data) || !validEscapedUnicode(data) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	state := validation{decoder: decoder, fields: make(map[reflect.Type]map[string]reflect.Type)}
	if err := state.value(target, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

type validation struct {
	decoder *json.Decoder
	fields  map[reflect.Type]map[string]reflect.Type
	nodes   int
}

func (v *validation) value(target reflect.Type, depth int) error {
	if depth > maxDepth {
		return ErrTooLarge
	}
	v.nodes++
	if v.nodes > maxNodes {
		return ErrTooLarge
	}
	for target != nil && target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if target == rawMessageType || (target != nil && target.Kind() == reflect.Interface) {
		target = nil
	}
	token, err := v.decoder.Token()
	if err != nil {
		return ErrInvalid
	}
	delimiter, structured := token.(json.Delim)
	if !structured {
		if token == nil && target != nil {
			return ErrInvalid
		}
		return nil
	}
	switch delimiter {
	case '{':
		return v.object(target, depth)
	case '[':
		return v.array(target, depth)
	default:
		return ErrInvalid
	}
}

func (v *validation) object(target reflect.Type, depth int) error {
	var fields map[string]reflect.Type
	if target != nil {
		switch target.Kind() {
		case reflect.Struct:
			var err error
			fields, err = v.objectFields(target)
			if err != nil {
				return err
			}
		case reflect.Map:
			if target.Key().Kind() != reflect.String {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	seen := make(map[string]struct{})
	for v.decoder.More() {
		token, err := v.decoder.Token()
		if err != nil {
			return ErrInvalid
		}
		name, ok := token.(string)
		if !ok {
			return ErrInvalid
		}
		folded := foldField(name)
		if _, exists := seen[folded]; exists {
			return ErrInvalid
		}
		seen[folded] = struct{}{}
		var child reflect.Type
		if fields != nil {
			var exists bool
			child, exists = fields[name]
			if !exists {
				return ErrInvalid
			}
		} else if target != nil {
			child = target.Elem()
		}
		if err := v.value(child, depth+1); err != nil {
			return err
		}
	}
	closing, err := v.decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return ErrInvalid
	}
	return nil
}

func (v *validation) array(target reflect.Type, depth int) error {
	var element reflect.Type
	if target != nil {
		if target.Kind() != reflect.Array && target.Kind() != reflect.Slice {
			return ErrInvalid
		}
		element = target.Elem()
	}
	for v.decoder.More() {
		if err := v.value(element, depth+1); err != nil {
			return err
		}
	}
	closing, err := v.decoder.Token()
	if err != nil || closing != json.Delim(']') {
		return ErrInvalid
	}
	return nil
}

func (v *validation) objectFields(target reflect.Type) (map[string]reflect.Type, error) {
	if fields, exists := v.fields[target]; exists {
		return fields, nil
	}
	fields := make(map[string]reflect.Type)
	for i := 0; i < target.NumField(); i++ {
		field := target.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			continue
		}
		if field.Anonymous {
			return nil, ErrInvalid
		}
		if name == "" {
			name = field.Name
		}
		if _, exists := fields[name]; exists {
			return nil, ErrInvalid
		}
		fields[name] = field.Type
	}
	v.fields[target] = fields
	return fields, nil
}

func foldField(value string) string {
	return strings.Map(func(current rune) rune {
		lowest := current
		for folded := unicode.SimpleFold(current); folded != current; folded = unicode.SimpleFold(folded) {
			lowest = min(lowest, folded)
		}
		return lowest
	}, value)
}

func validEscapedUnicode(data []byte) bool {
	quoted := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		value, ok := escapedCodeUnit(data, i+1)
		if !ok {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, valid := escapedCodeUnit(data, i+3)
		if !valid || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return !quoted
}

func escapedCodeUnit(data []byte, start int) (uint16, bool) {
	if start+4 > len(data) {
		return 0, false
	}
	var value uint16
	for _, digit := range data[start : start+4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

// Status returns the HTTP status for a request-decoding failure.
func Status(err error) int {
	switch {
	case errors.Is(err, ErrContentType):
		return http.StatusUnsupportedMediaType
	case errors.Is(err, ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout
	default:
		return http.StatusBadRequest
	}
}
