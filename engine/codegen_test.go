package engine

import (
	"testing"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"github.com/pseudomuto/protokit"
	arrayutils "gitlab.com/kordax/basic-utils/array-utils"
	"gitlab.com/kordax/basic-utils/opt"
	refutils "gitlab.com/kordax/basic-utils/ref-utils"
)

func TestGenerate(t *testing.T) {
	generator := NewCodegenerator()

	files := []ParsedFile{
		createMockParsedFile(),
	}

	commonDescriptor := &protokit.Descriptor{
		DescriptorProto: &descriptor.DescriptorProto{
			Name: refutils.Ref("test message"),
		},
	}

	messageWithCode := &Message{
		m:    commonDescriptor,
		code: opt.Of(arrayutils.Pair[Syntax, string]{Left: SyntaxJson, Right: "sample code"}),
	}

	messageWithAutocode := &Message{
		m:        commonDescriptor,
		autocode: opt.Of(AutocodeOpt{syntax: SyntaxJson}),
	}

	messageWithCodeAndAutocode := &Message{
		m:        commonDescriptor,
		code:     opt.Of(arrayutils.Pair[Syntax, string]{Left: SyntaxJson, Right: "sample code"}),
		autocode: opt.Of(AutocodeOpt{syntax: SyntaxJson}),
	}

	messageWithNeither := &Message{
		m: commonDescriptor,
	}

	messageWithMultipleFields := &Message{
		m:        commonDescriptor,
		autocode: opt.Of(AutocodeOpt{syntax: SyntaxJson}),
		fields: []MessageField{
			*NewMessageField(&protokit.FieldDescriptor{}, commonDescriptor, "Desc1", ValueTypeInt, nil),
			*NewMessageField(&protokit.FieldDescriptor{}, commonDescriptor, "Desc2", ValueTypeString, nil),
		},
	}

	messageWithEmbeddedMessage := &Message{
		m:        commonDescriptor,
		autocode: opt.Of(AutocodeOpt{syntax: SyntaxJson}),
		fields: []MessageField{
			*NewMessageField(&protokit.FieldDescriptor{}, commonDescriptor, "Desc", ValueTypeStruct, nil),
		},
	}

	messageWithEnum := &Message{
		m:        commonDescriptor,
		autocode: opt.Of(AutocodeOpt{syntax: SyntaxJson}),
		fields: []MessageField{
			*NewMessageField(&protokit.FieldDescriptor{}, commonDescriptor, "Desc", ValueTypeEnum, nil),
		},
	}

	messageWithNoFields := &Message{
		m:        commonDescriptor,
		autocode: opt.Of(AutocodeOpt{syntax: SyntaxJson}),
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
			message: &Message{m: commonDescriptor, autocode: opt.Of(AutocodeOpt{syntax: SyntaxJson})},
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
			maxLength:  opt.Of(100),
			min:        opt.Of(0.0),
			max:        opt.Of(100.0),
			value:      opt.Of("SampleValue"),
			customType: opt.Of(ValueTypeInt),
			other:      []string{"flag1", "flag2"},
		},
	)

	// Mock Message
	mockMessage := Message{
		autocode:    opt.Of(AutocodeOpt{syntax: SyntaxJson}),
		code:        opt.Of(arrayutils.Pair[Syntax, string]{Left: SyntaxJson, Right: "SampleCode"}),
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
