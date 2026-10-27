package site

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// encoding/json buffers a whole value even with Encoder and an io.Writer. Check
// a conservative encoded-size bound first, including HTML/string escapes and
// repeated source metadata, so the writer limit cannot be reached after an
// unbounded intermediate allocation. The model has no custom marshalers except
// RawMessage; unknown model shapes fail explicitly.
func marshalModel(value any, limit int) ([]byte, error) {
	if limit < 1 {
		return nil, fmt.Errorf("document model limit must be positive")
	}
	budget := modelBudget{remaining: limit - 1, limit: limit}
	if err := budget.value(reflect.ValueOf(value), 0); err != nil {
		return nil, err
	}
	buffer := boundedBuffer{limit: limit}
	encoder := json.NewEncoder(&buffer)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode document model: %w", err)
	}
	return buffer.Bytes(), nil
}

type modelBudget struct {
	remaining int
	limit     int
}

func (b *modelBudget) add(size int) error {
	if size < 0 || size > b.remaining {
		return fmt.Errorf("document model exceeds %d encoded bytes", b.limit)
	}
	b.remaining -= size
	return nil
}

var (
	rawMessageType    = reflect.TypeFor[json.RawMessage]()
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

func (b *modelBudget) value(value reflect.Value, depth int) error {
	if depth > 256 {
		return fmt.Errorf("document model nesting exceeds 256 levels")
	}
	if !value.IsValid() {
		return b.add(4)
	}
	valueType := value.Type()
	isRaw := valueType == rawMessageType || valueType.Kind() == reflect.Pointer && valueType.Elem() == rawMessageType
	if !isRaw && (valueType.Implements(jsonMarshalerType) || valueType.Implements(textMarshalerType) || reflect.PointerTo(valueType).Implements(jsonMarshalerType) || reflect.PointerTo(valueType).Implements(textMarshalerType)) {
		return fmt.Errorf("unsupported custom document model marshaler")
	}
	if value.Type() == rawMessageType {
		if value.IsNil() {
			return b.add(4)
		}
		raw := value.Bytes()
		if !utf8.Valid(raw) || !json.Valid(raw) {
			return fmt.Errorf("invalid raw JSON in document model")
		}
		if err := b.add(len(raw)); err != nil {
			return err
		}
		for _, char := range string(raw) {
			switch char {
			case '<', '>', '&':
				if err := b.add(5); err != nil {
					return err
				}
			case '\u2028', '\u2029':
				if err := b.add(3); err != nil {
					return err
				}
			}
		}
		return nil
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return b.add(4)
		}
		return b.value(value.Elem(), depth+1)
	case reflect.String:
		return b.string(value.String())
	case reflect.Bool:
		if value.Bool() {
			return b.add(4)
		}
		return b.add(5)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return b.add(len(strconv.FormatInt(value.Int(), 10)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return b.add(len(strconv.FormatUint(value.Uint(), 10)))
	case reflect.Float32, reflect.Float64:
		data, err := json.Marshal(value.Interface())
		if err != nil {
			return fmt.Errorf("invalid number in document model")
		}
		return b.add(len(data))
	case reflect.Slice:
		if value.IsNil() {
			return b.add(4)
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return b.add(2 + base64.StdEncoding.EncodedLen(value.Len()))
		}
		fallthrough
	case reflect.Array:
		if err := b.add(2 + max(0, value.Len()-1)); err != nil {
			return err
		}
		for i := range value.Len() {
			if err := b.value(value.Index(i), depth+1); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if value.IsNil() {
			return b.add(4)
		}
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("document model map keys must be strings")
		}
		if err := b.add(2 + max(0, value.Len()-1)); err != nil {
			return err
		}
		iterator := value.MapRange()
		for iterator.Next() {
			if err := b.string(iterator.Key().String()); err != nil {
				return err
			}
			if err := b.add(1); err != nil {
				return err
			}
			if err := b.value(iterator.Value(), depth+1); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		if err := b.add(2); err != nil {
			return err
		}
		count := 0
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if field.PkgPath != "" {
				continue
			}
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" || strings.Contains(options, "omitempty") && emptyModelValue(value.Field(i)) {
				continue
			}
			if field.Anonymous || strings.Contains(options, "string") {
				return fmt.Errorf("unsupported custom document model field")
			}
			if name == "" {
				name = field.Name
			}
			if err := b.add(1 + min(1, count)); err != nil {
				return err
			}
			count++
			if err := b.string(name); err != nil {
				return err
			}
			if err := b.value(value.Field(i), depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported document model value")
	}
}

func (b *modelBudget) string(value string) error {
	if err := b.add(2); err != nil {
		return err
	}
	for len(value) > 0 {
		char, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		encoded := size
		switch {
		case char == utf8.RuneError && size == 1, char == '<', char == '>', char == '&', char == '\u2028', char == '\u2029':
			encoded = 6
		case char == '"', char == '\\', char == '\n', char == '\r', char == '\t', char == '\b', char == '\f':
			encoded = 2
		case char < 32:
			encoded = 6
		}
		if err := b.add(encoded); err != nil {
			return err
		}
	}
	return nil
}

func emptyModelValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return value.IsZero()
	}
	return false
}
