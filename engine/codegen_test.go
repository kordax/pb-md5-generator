package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/parser"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestGenerate(t *testing.T) {
	generator := NewCodegenerator()
	messageName := "test message"

	files := []ParsedFile{
		createMockParsedFile(),
	}

	commonDescriptor := &protokit.Descriptor{
		DescriptorProto: &descriptorpb.DescriptorProto{
			Name: &messageName,
		},
	}

	messageWithCode := &Message{
		m:    commonDescriptor,
		code: Some(Pair[Syntax, string]{Left: SyntaxJson, Right: "sample code"}),
	}

	messageWithAutocode := &Message{
		m:        commonDescriptor,
		autocode: Some(AutocodeOpt{syntax: SyntaxJson}),
	}

	messageWithCodeAndAutocode := &Message{
		m:        commonDescriptor,
		code:     Some(Pair[Syntax, string]{Left: SyntaxJson, Right: "sample code"}),
		autocode: Some(AutocodeOpt{syntax: SyntaxJson}),
	}

	messageWithNeither := &Message{
		m: commonDescriptor,
	}

	messageWithMultipleFields := &Message{
		m:        commonDescriptor,
		autocode: Some(AutocodeOpt{syntax: SyntaxJson}),
		fields: []MessageField{
			*NewMessageField(&protokit.FieldDescriptor{}, commonDescriptor, "Desc1", ValueTypeInt, nil),
			*NewMessageField(&protokit.FieldDescriptor{}, commonDescriptor, "Desc2", ValueTypeString, nil),
		},
	}

	messageWithEmbeddedMessage := &Message{
		m:        commonDescriptor,
		autocode: Some(AutocodeOpt{syntax: SyntaxJson}),
		fields: []MessageField{
			*NewMessageField(&protokit.FieldDescriptor{}, commonDescriptor, "Desc", ValueTypeStruct, nil),
		},
	}

	messageWithEnum := &Message{
		m:        commonDescriptor,
		autocode: Some(AutocodeOpt{syntax: SyntaxJson}),
		fields: []MessageField{
			*NewMessageField(&protokit.FieldDescriptor{}, commonDescriptor, "Desc", ValueTypeEnum, nil),
		},
	}

	messageWithNoFields := &Message{
		m:        commonDescriptor,
		autocode: Some(AutocodeOpt{syntax: SyntaxJson}),
	}

	tests := []struct {
		name    string
		files   []ParsedFile
		message *Message
		wantErr bool
	}{
		{
			name:    "Test with code",
			files:   files,
			message: messageWithCode,
			wantErr: false,
		},
		{
			name:    "Test with autocode",
			files:   files,
			message: messageWithAutocode,
			wantErr: false,
		},
		{
			name:    "Test with both code and autocode",
			files:   files,
			message: messageWithCodeAndAutocode,
			wantErr: false,
		},
		{
			name:    "Test with neither code nor autocode",
			files:   files,
			message: messageWithNeither,
			wantErr: true,
		},
		{
			name:    "Test with multiple fields",
			files:   files,
			message: messageWithMultipleFields,
			wantErr: false,
		},
		{
			name:    "Test with embedded message",
			files:   files,
			message: messageWithEmbeddedMessage,
			wantErr: true,
		},
		{
			name:    "Test with enum field",
			files:   files,
			message: messageWithEnum,
			wantErr: false,
		},
		{
			name:    "Test with no fields",
			files:   files,
			message: messageWithNoFields,
			wantErr: false,
		},
		{
			name: "Test with multiple entries in ParsedFile",
			files: []ParsedFile{
				{
					index:    0,
					filename: "multi_entry.proto",
					title:    "MultipleEntries",
					entries:  []Entry{ /* ... multiple entries here ... */ },
				},
			},
			message: &Message{m: commonDescriptor, autocode: Some(AutocodeOpt{syntax: SyntaxJson})},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := generator.Generate(tt.files, tt.message)
			if (err != nil) != tt.wantErr {
				t.Errorf("Generate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

// CreateMockParsedFile creates a mock ParsedFile for testing purposes.
func createMockParsedFile() ParsedFile {
	// Mock Enum
	mockEnum := Enum{
		description: "SampleEnumDescription",
		e:           &protokit.EnumDescriptor{},
		values: []EnumField{
			{
				description: "SampleEnumValueDescription",
				flags:       []string{"flag1", "flag2"},
				d:           &protokit.EnumValueDescriptor{},
			},
		},
		flags: []string{"flag1", "flag2"},
	}

	// Mock MessageField
	mockMessageField := NewMessageField(
		&protokit.FieldDescriptor{},
		&protokit.Descriptor{},
		"SampleDescription",
		ValueTypeString,
		&FieldFlags{
			maxLength:  Some(100),
			min:        Some(0.0),
			max:        Some(100.0),
			value:      Some("SampleValue"),
			customType: Some(ValueTypeInt),
			other:      []string{"flag1", "flag2"},
		},
	)

	// Mock Message
	mockMessage := Message{
		autocode:    Some(AutocodeOpt{syntax: SyntaxJson}),
		code:        Some(Pair[Syntax, string]{Left: SyntaxJson, Right: "SampleCode"}),
		header:      "SampleHeader",
		description: "SampleDescription",
		m:           &protokit.Descriptor{},
		fields:      []MessageField{*mockMessageField},
		entries:     []Entry{},
		flags:       []string{"flag1", "flag2"},
	}

	// Mock ParsedFile
	return ParsedFile{
		index:    0,
		filename: "sample_filename.proto",
		title:    "SampleTitle",
		entries: []Entry{
			{
				index: 0,
				t:     EntryTypeEnum,
				enum:  &mockEnum,
				msg:   nil,
			},
			{
				index: 1,
				t:     EntryTypeMessage,
				enum:  nil,
				msg:   &mockMessage,
			},
			// ... Add more mock entries as needed
		},
	}
}

func TestGenerateFromFieldValues(t *testing.T) {
	generator := NewCodegenerator()
	messageName := "Message"
	messageDescriptor := &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: &messageName}}

	t.Run("string value returns raw string", func(t *testing.T) {
		field := *NewMessageField(
			fieldDescriptor("name", descriptorpb.FieldDescriptorProto_TYPE_STRING, messageDescriptor),
			messageDescriptor,
			"",
			ValueTypeString,
			&FieldFlags{value: Some("fixed")},
		)

		value, err := generator.generateFromField(nil, field)
		require.NoError(t, err)
		assert.Equal(t, "fixed", value)
	})

	t.Run("email value returns raw string", func(t *testing.T) {
		field := *NewMessageField(
			fieldDescriptor("email", descriptorpb.FieldDescriptorProto_TYPE_STRING, messageDescriptor),
			messageDescriptor,
			"",
			ValueTypeEmail,
			&FieldFlags{value: Some("user@example.com")},
		)

		value, err := generator.generateFromField(nil, field)
		require.NoError(t, err)
		assert.Equal(t, "user@example.com", value)
	})

	t.Run("numeric values are parsed", func(t *testing.T) {
		intField := *NewMessageField(fieldDescriptor("count", descriptorpb.FieldDescriptorProto_TYPE_INT64, messageDescriptor), messageDescriptor, "", ValueTypeInt, &FieldFlags{value: Some("42")})
		uintField := *NewMessageField(fieldDescriptor("size", descriptorpb.FieldDescriptorProto_TYPE_UINT64, messageDescriptor), messageDescriptor, "", ValueTypeUInt, &FieldFlags{value: Some("7")})
		floatField := *NewMessageField(fieldDescriptor("ratio", descriptorpb.FieldDescriptorProto_TYPE_FLOAT, messageDescriptor), messageDescriptor, "", ValueTypeFloat, &FieldFlags{value: Some("3.14")})

		intValue, err := generator.generateFromField(nil, intField)
		require.NoError(t, err)
		uintValue, err := generator.generateFromField(nil, uintField)
		require.NoError(t, err)
		floatValue, err := generator.generateFromField(nil, floatField)
		require.NoError(t, err)

		assert.Equal(t, int64(42), intValue)
		assert.Equal(t, uint64(7), uintValue)
		assert.Equal(t, 3.14, floatValue)
	})

	t.Run("bool value is parsed", func(t *testing.T) {
		field := *NewMessageField(
			fieldDescriptor("enabled", descriptorpb.FieldDescriptorProto_TYPE_BOOL, messageDescriptor),
			messageDescriptor,
			"",
			ValueTypeBool,
			&FieldFlags{value: Some("true")},
		)

		value, err := generator.generateFromField(nil, field)
		require.NoError(t, err)
		assert.Equal(t, true, value)
	})

	t.Run("special string values override generated values", func(t *testing.T) {
		tests := []struct {
			name      string
			valueType ValueType
			want      string
		}{
			{name: "phone", valueType: ValueTypePhone, want: "example-phone"},
			{name: "jwt", valueType: ValueTypeJWT, want: "example-access-token"},
			{name: "password", valueType: ValueTypePassword, want: "example-password"},
			{name: "uuid", valueType: ValueTypeUUID, want: "example-idempotency-key"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				field := *NewMessageField(
					fieldDescriptor(tt.name, descriptorpb.FieldDescriptorProto_TYPE_STRING, messageDescriptor),
					messageDescriptor,
					"",
					tt.valueType,
					&FieldFlags{value: Some(tt.want)},
				)

				value, err := generator.generateFromField(nil, field)
				require.NoError(t, err)
				assert.Equal(t, tt.want, value)
			})
		}
	})

	t.Run("random bool is generated", func(t *testing.T) {
		field := *NewMessageField(
			fieldDescriptor("enabled", descriptorpb.FieldDescriptorProto_TYPE_BOOL, messageDescriptor),
			messageDescriptor,
			"",
			ValueTypeBool,
			nil,
		)

		value, err := generator.generateFromField(nil, field)
		require.NoError(t, err)
		_, ok := value.(bool)
		assert.True(t, ok)
	})
}

func TestGenerateFromFieldErrorsDoNotPanic(t *testing.T) {
	generator := NewCodegenerator()

	field := MessageField{
		valueType: ValueType(999),
		d:         fieldDescriptor("broken", descriptorpb.FieldDescriptorProto_TYPE_STRING, nil),
	}

	require.NotPanics(t, func() {
		_, err := generator.generateFromField(nil, field)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type")
	})
}

func TestGenerateFromFieldRandomBranches(t *testing.T) {
	generator := NewCodegenerator()
	messageName := "Message"
	messageDescriptor := &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: &messageName}}

	tests := []struct {
		name      string
		valueType ValueType
		fieldName string
		assertion func(t *testing.T, value any)
	}{
		{
			name:      "int range",
			valueType: ValueTypeInt,
			fieldName: "count",
			assertion: func(t *testing.T, value any) {
				v, ok := value.(int64)
				require.True(t, ok)
				assert.GreaterOrEqual(t, v, int64(0))
			},
		},
		{
			name:      "uint range",
			valueType: ValueTypeUInt,
			fieldName: "size",
			assertion: func(t *testing.T, value any) {
				_, ok := value.(uint64)
				assert.True(t, ok)
			},
		},
		{
			name:      "float range",
			valueType: ValueTypeFloat,
			fieldName: "ratio",
			assertion: func(t *testing.T, value any) {
				_, ok := value.(float64)
				assert.True(t, ok)
			},
		},
		{
			name:      "email",
			valueType: ValueTypeEmail,
			fieldName: "email",
			assertion: func(t *testing.T, value any) {
				assert.Contains(t, value.(string), "@email.com")
			},
		},
		{
			name:      "phone",
			valueType: ValueTypePhone,
			fieldName: "phone",
			assertion: func(t *testing.T, value any) {
				assert.Contains(t, value.(string), "+")
				assert.Contains(t, value.(string), ".")
			},
		},
		{
			name:      "password",
			valueType: ValueTypePassword,
			fieldName: "password",
			assertion: func(t *testing.T, value any) {
				assert.NotEmpty(t, value.(string))
			},
		},
		{
			name:      "uuid",
			valueType: ValueTypeUUID,
			fieldName: "uuid",
			assertion: func(t *testing.T, value any) {
				assert.Len(t, value.(string), 36)
			},
		},
		{
			name:      "jwt",
			valueType: ValueTypeJWT,
			fieldName: "jwt",
			assertion: func(t *testing.T, value any) {
				assert.Contains(t, value.(string), ".")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := *NewMessageField(
				fieldDescriptor(tt.fieldName, descriptorpb.FieldDescriptorProto_TYPE_STRING, messageDescriptor),
				messageDescriptor,
				"",
				tt.valueType,
				nil,
			)

			value, err := generator.generateFromField(nil, field)
			require.NoError(t, err)
			tt.assertion(t, value)
		})
	}

	t.Run("string max length", func(t *testing.T) {
		field := *NewMessageField(
			fieldDescriptor("name", descriptorpb.FieldDescriptorProto_TYPE_STRING, messageDescriptor),
			messageDescriptor,
			"",
			ValueTypeString,
			&FieldFlags{maxLength: Some(2)},
		)
		value, err := generator.generateFromField(nil, field)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(value.(string)), 2)
	})

	t.Run("struct is rejected", func(t *testing.T) {
		field := *NewMessageField(
			fieldDescriptor("nested", descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, messageDescriptor),
			messageDescriptor,
			"",
			ValueTypeStruct,
			nil,
		)
		_, err := generator.generateFromField(nil, field)
		assert.ErrorContains(t, err, "cannot generate code from struct")
	})
}

func TestCodegenHelpers(t *testing.T) {
	i, err := int64WithinRange(5, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(5), i)

	u, err := uint64WithinRange(3, 3)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), u)

	f, err := float64WithinRange(2.5, 2.5)
	require.NoError(t, err)
	assert.Equal(t, 2.5, f)

	i, err = int64WithinRange(1, 3)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, i, int64(1))
	assert.Less(t, i, int64(3))

	u, err = uint64WithinRange(1, 3)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, u, uint64(1))
	assert.Less(t, u, uint64(3))

	f, err = float64WithinRange(1, 3)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, f, 1.0)
	assert.LessOrEqual(t, f, 3.0)

	index, err := cryptoIndex(1)
	require.NoError(t, err)
	assert.Equal(t, 0, index)

	_, err = cryptoIndex(0)
	assert.Error(t, err)
}

func TestPBTypeToString(t *testing.T) {
	typeName := ".sample.Custom"
	tests := []struct {
		name     string
		typ      descriptorpb.FieldDescriptorProto_Type
		typeName string
		expected string
	}{
		{name: "int64", typ: descriptorpb.FieldDescriptorProto_TYPE_INT64, expected: "int64"},
		{name: "int32", typ: descriptorpb.FieldDescriptorProto_TYPE_INT32, expected: "int32"},
		{name: "uint64", typ: descriptorpb.FieldDescriptorProto_TYPE_UINT64, expected: "uint64"},
		{name: "uint32", typ: descriptorpb.FieldDescriptorProto_TYPE_UINT32, expected: "uint32"},
		{name: "sint64", typ: descriptorpb.FieldDescriptorProto_TYPE_SINT64, expected: "int64"},
		{name: "sint32", typ: descriptorpb.FieldDescriptorProto_TYPE_SINT32, expected: "int32"},
		{name: "fixed64", typ: descriptorpb.FieldDescriptorProto_TYPE_FIXED64, expected: "float64"},
		{name: "fixed32", typ: descriptorpb.FieldDescriptorProto_TYPE_FIXED32, expected: "float32"},
		{name: "double", typ: descriptorpb.FieldDescriptorProto_TYPE_DOUBLE, expected: "float64"},
		{name: "float", typ: descriptorpb.FieldDescriptorProto_TYPE_FLOAT, expected: "float32"},
		{name: "sfixed64", typ: descriptorpb.FieldDescriptorProto_TYPE_SFIXED64, expected: "float64"},
		{name: "sfixed32", typ: descriptorpb.FieldDescriptorProto_TYPE_SFIXED32, expected: "float32"},
		{name: "bool", typ: descriptorpb.FieldDescriptorProto_TYPE_BOOL, expected: "bool"},
		{name: "string", typ: descriptorpb.FieldDescriptorProto_TYPE_STRING, expected: "string"},
		{name: "bytes", typ: descriptorpb.FieldDescriptorProto_TYPE_BYTES, expected: "[]byte"},
		{name: "enum", typ: descriptorpb.FieldDescriptorProto_TYPE_ENUM, typeName: typeName, expected: "sample.Custom"},
		{name: "message", typ: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, typeName: "sample.Message", expected: "sample.Message"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field := fieldDescriptor("value", tt.typ, nil)
			field.TypeName = &tt.typeName
			assert.Equal(t, tt.expected, pbTypeToString(field))
		})
	}
}

func TestMapStringToValueType(t *testing.T) {
	tests := map[string]ValueType{
		"int":      ValueTypeInt,
		"uint":     ValueTypeUInt,
		"float":    ValueTypeFloat,
		"bool":     ValueTypeBool,
		"string":   ValueTypeString,
		"enum":     ValueTypeEnum,
		"jwt":      ValueTypeJWT,
		"uuid":     ValueTypeUUID,
		"struct":   ValueTypeStruct,
		"email":    ValueTypeEmail,
		"phone":    ValueTypePhone,
		"password": ValueTypePassword,
	}

	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			actual, err := mapStringToValueType(input)
			require.NoError(t, err)
			assert.Equal(t, expected, actual)
		})
	}

	_, err := mapStringToValueType("unknown")
	assert.Error(t, err)
}

func fieldDescriptor(name string, typ descriptorpb.FieldDescriptorProto_Type, message *protokit.Descriptor) *protokit.FieldDescriptor {
	return &protokit.FieldDescriptor{
		FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{
			Name: &name,
			Type: &typ,
		},
		Message: message,
	}
}

func TestGenerateFromMessageNestedKeepsFields(t *testing.T) {
	g := NewCodegenerator()

	innerDescriptor := &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Inner")}}
	outerDescriptor := &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Outer")}}

	inner := Message{
		m: innerDescriptor,
		fields: []MessageField{
			*NewMessageField(fieldDescriptor("innerName", descriptorpb.FieldDescriptorProto_TYPE_STRING, innerDescriptor), innerDescriptor, "", ValueTypeString, &FieldFlags{value: Some("inner")}),
		},
	}

	nestedField := NewMessageField(fieldDescriptor("nested", descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, outerDescriptor), outerDescriptor, "", ValueTypeStruct, nil)
	nestedField.isMsg = &inner
	plainField := NewMessageField(fieldDescriptor("plain", descriptorpb.FieldDescriptorProto_TYPE_STRING, outerDescriptor), outerDescriptor, "", ValueTypeString, &FieldFlags{value: Some("plain")})

	outer := Message{
		m: outerDescriptor,
		fields: []MessageField{
			*nestedField,
			*plainField,
		},
	}

	result, err := g.generateFromMessage(nil, &outer, nil)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))

	rootMap, ok := parsed[outer.m.GetName()].(map[string]any)
	require.True(t, ok)
	innerMap, ok := rootMap["nested"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "inner", innerMap["innerName"])
	assert.Equal(t, "plain", rootMap["plain"])
}

func TestGenerateFromFieldEnumAndErrors(t *testing.T) {
	g := NewCodegenerator()

	enum := fixtureEnum(t, "LoginStatus")
	enumValueNames := make([]string, 0, len(enum.values))
	for _, item := range enum.values {
		enumValueNames = append(enumValueNames, item.d.GetName())
	}
	files := []ParsedFile{{
		entries: []Entry{{
			index: 0,
			t:     EntryTypeEnum,
			enum:  enum,
		}},
	}}

	fieldWithMatch := *NewMessageField(
		&protokit.FieldDescriptor{
			FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{
				TypeName: strPtr("." + enum.e.GetFullName()),
			},
		},
		&protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Order")}},
		"",
		ValueTypeEnum,
		nil,
	)
	value, err := g.generateFromField(files, fieldWithMatch)
	require.NoError(t, err)
	require.NotNil(t, value)
	assert.Contains(t, enumValueNames, value.(string))

	fieldMissingEnum := *NewMessageField(
		&protokit.FieldDescriptor{FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{TypeName: strPtr(".Nope")}},
		&protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Order")}},
		"",
		ValueTypeEnum,
		nil,
	)
	value, err = g.generateFromField(nil, fieldMissingEnum)
	require.NoError(t, err)
	assert.Nil(t, value)

	invalidInt := *NewMessageField(
		&protokit.FieldDescriptor{FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{Type: descriptorType(descriptorpb.FieldDescriptorProto_TYPE_INT64)}},
		&protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Order")}},
		"",
		ValueTypeInt,
		&FieldFlags{value: Some("bad")},
	)
	_, err = g.generateFromField(nil, invalidInt)
	require.ErrorContains(t, err, "invalid syntax")

	invalidBool := *NewMessageField(
		&protokit.FieldDescriptor{FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{Type: descriptorType(descriptorpb.FieldDescriptorProto_TYPE_BOOL)}},
		&protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Order")}},
		"",
		ValueTypeBool,
		&FieldFlags{value: Some("notBool")},
	)
	_, err = g.generateFromField(nil, invalidBool)
	require.ErrorContains(t, err, "invalid syntax")
}

func descriptorType(value descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto_Type {
	return &value
}

func fixtureEnum(t *testing.T, name string) *Enum {
	t.Helper()

	parsed, err := parser.NewDescriptorParser(codegenFixtureRequest(t)).Parse()
	require.NoError(t, err)

	for _, file := range parsed {
		for _, entry := range file.Entries() {
			enum := entry.Enum()
			if enum != nil && enum.Descriptor().GetName() == name {
				converted := convertEnum(*enum)
				return &converted
			}
		}
	}

	require.Failf(t, "enum not found", "enum %q not found in fixture", name)
	return nil
}

func codegenFixtureRequest(t *testing.T) *pluginpb.CodeGeneratorRequest {
	t.Helper()
	_, currentFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(currentFile))
	path := filepath.Join(root, "testdata", "test-proto", "test.pb.desc")

	blob, err := os.ReadFile(path)
	require.NoError(t, err)

	fds := &descriptorpb.FileDescriptorSet{}
	require.NoError(t, proto.Unmarshal(blob, fds))

	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"test_proto"},
		Parameter:      proto.String("Mtest_proto=" + filepath.Join(root, "testdata", "test-proto")),
		ProtoFile:      fds.File,
	}
}
