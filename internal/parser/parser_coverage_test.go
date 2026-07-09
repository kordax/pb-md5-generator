package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestParserGettersAndWrappers(t *testing.T) {
	file := ParsedFile{index: 7, filename: "sample.proto", title: "Demo API", entries: []Entry{{index: 2, t: EntryTypeMessage}}}
	assert.Equal(t, 7, file.Index())
	assert.Equal(t, "sample.proto", file.Filename())
	assert.Equal(t, "Demo API", file.Title())
	assert.Len(t, file.Entries(), 1)

	entry := Entry{index: 3, t: EntryTypeEnum}
	assert.Equal(t, 3, entry.Index())
	assert.Equal(t, EntryTypeEnum, entry.Type())
	assert.Nil(t, entry.Message())
	assert.Nil(t, entry.Enum())

	fieldDescriptor := &protokit.FieldDescriptor{FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{Name: ptrTo("token")}}
	msgDescriptor := &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: ptrTo("Message")}}
	field := NewMessageField(fieldDescriptor, msgDescriptor, "Message field", ValueTypeStruct, &FieldFlags{
		maxLength: Some(10),
		min:       Some(2.0),
		max:       Some(33.0),
		value:     Some("v"),
		other:     []string{"raw"},
	})
	assert.Equal(t, ValueTypeStruct, field.ValueType())
	assert.Equal(t, "token", field.Descriptor().GetName())
	assert.Equal(t, msgDescriptor, field.Parent())
	assert.Equal(t, "Message field", field.Description())
	assert.True(t, field.Flags().Get().GetValue().Present())
	assert.Equal(t, "v", *field.Flags().Get().GetValue().Get())
	assert.Equal(t, []string{"raw"}, field.Flags().Get().Other())

	enumValues := []EnumField{{
		description: "value",
		flags:       []string{"tag=main"},
		d:           &protokit.EnumValueDescriptor{EnumValueDescriptorProto: &descriptorpb.EnumValueDescriptorProto{Name: ptrTo("RED")}},
	}}
	enum := Enum{
		description: "desc",
		e:           &protokit.EnumDescriptor{EnumDescriptorProto: &descriptorpb.EnumDescriptorProto{Name: ptrTo("Color")}},
		values:      enumValues,
		flags:       []string{"enum-flag"},
	}
	assert.Equal(t, "desc", enum.Description())
	assert.Equal(t, "Color", enum.Descriptor().GetName())
	assert.Len(t, enum.Values(), 1)
	assert.Equal(t, []string{"enum-flag"}, enum.Flags())
	assert.Equal(t, "value", enum.Values()[0].Description())
	assert.Equal(t, []string{"tag=main"}, enum.Values()[0].Flags())
	assert.Equal(t, "RED", enum.Values()[0].Descriptor().GetName())

	msgDescriptorForMessage := &protokit.Descriptor{
		DescriptorProto: &descriptorpb.DescriptorProto{Name: ptrTo("Message")},
	}
	msg := Message{
		autocode:    Some(AutocodeOpt{syntax: SyntaxXml}),
		code:        Some(Pair[Syntax, string]{Left: SyntaxJson, Right: "{\"ok\":true}"}),
		header:      "Header",
		description: "Description",
		m:           msgDescriptorForMessage,
		fields:      []MessageField{*field},
		entries:     []Entry{},
		flags:       []string{"msg-flag"},
	}
	assert.True(t, msg.Autocode().Present())
	assert.Equal(t, SyntaxXml, msg.Autocode().Get().Syntax())
	assert.True(t, msg.Code().Present())
	assert.Equal(t, SyntaxJson, msg.Code().Get().Left)
	assert.Equal(t, "{\"ok\":true}", msg.Code().Get().Right)
	assert.Equal(t, "Header", msg.Header())
	assert.Equal(t, "Description", msg.Description())
	assert.Equal(t, "Message", msg.Descriptor().GetName())
	assert.Len(t, msg.Fields(), 1)
	assert.Len(t, msg.Entries(), 0)
	assert.Equal(t, []string{"msg-flag"}, msg.Flags())

	flags := FieldFlags{
		maxLength:  Some(10),
		min:        Some(2.0),
		max:        Some(33.0),
		value:      Some("x"),
		customType: Some(ValueTypePhone),
		other:      []string{"raw"},
	}
	assert.Equal(t, 10, *flags.GetMaxLength().Get())
	assert.Equal(t, 2.0, *flags.GetMin().Get())
	assert.Equal(t, 33.0, *flags.GetMax().Get())
	assert.Equal(t, "x", *flags.GetValue().Get())
	assert.Equal(t, ValueTypePhone, *flags.GetCustomType().Get())
	assert.Equal(t, []string{"raw"}, flags.Other())
	assert.Equal(t, SyntaxJson, parserSyntaxJson())

	assert.Equal(t, []int{2, 4}, MapSlice([]int{1, 2}, func(v int) int { return v * 2 }))
	assert.Equal(t, []int{2}, Filter([]int{1, 2, 3}, func(v int) bool { return v%2 == 0 }))
	assert.Equal(t, 1, Contains(2, []int{1, 2, 3}))
	assert.Equal(t, -1, Contains(4, []int{1, 2, 3}))

	index, value := ContainsPredicate([]string{"a", "b"}, func(v string) bool { return v == "b" })
	require.Equal(t, 1, index)
	require.NotNil(t, value)
	assert.Equal(t, "b", *value)
	index, value = ContainsPredicate([]string{"a", "b"}, func(v string) bool { return v == "z" })
	assert.Equal(t, -1, index)
	assert.Nil(t, value)

	vt, err := MapStringToValueType("uuid")
	require.NoError(t, err)
	assert.Equal(t, ValueTypeUUID, vt)
	_, err = MapStringToValueType("bad")
	assert.Error(t, err)

	sType := &protokit.FieldDescriptor{
		FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{Name: ptrTo("email"), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
	}
	assert.Equal(t, ValueTypeEmail, ProtoToFieldValueType(sType))
}

func TestNewDescriptorParserPanics(t *testing.T) {
	assert.Panics(t, func() {
		_ = NewDescriptorParser(&pluginpb.CodeGeneratorRequest{
			FileToGenerate: []string{"missing.proto"},
			Parameter:      proto.String("Mother.proto="),
		})
	})

	tmpDir := t.TempDir()
	assert.Panics(t, func() {
		_ = NewDescriptorParser(&pluginpb.CodeGeneratorRequest{
			FileToGenerate: []string{"missing.proto"},
			Parameter:      proto.String("Mmissing.proto=" + tmpDir),
		})
	})
}

func TestParseFlowWithMarkers(t *testing.T) {
	parser := &DescriptorParser{
		descriptors: []*protokit.FileDescriptor{
			{
				FileDescriptorProto: &descriptorpb.FileDescriptorProto{
					Name:    ptrTo("sample.proto"),
					Syntax:  ptrTo("proto3"),
					Package: ptrTo("sample"),
				},
				Messages: []*protokit.Descriptor{
					{
						DescriptorProto: &descriptorpb.DescriptorProto{Name: ptrTo("Alpha")},
						Comments:        &protokit.Comment{Leading: "Alpha message."},
						Fields: []*protokit.FieldDescriptor{
							{
								FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{
									Name:  ptrTo("keep"),
									Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
									Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
								},
								Comments: &protokit.Comment{Leading: "Keep message."},
							},
							{
								FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{
									Name:  ptrTo("ignore"),
									Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
									Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
								},
								Comments: &protokit.Comment{Leading: "Ignore this field.\n@ignore"},
							},
						},
					},
					{
						DescriptorProto: &descriptorpb.DescriptorProto{Name: ptrTo("Beta")},
						Comments:        &protokit.Comment{Leading: "Beta message."},
					},
					{
						DescriptorProto: &descriptorpb.DescriptorProto{Name: ptrTo("Gamma")},
						Comments:        &protokit.Comment{Leading: "Gamma message."},
					},
				},
				Enums: []*protokit.EnumDescriptor{
					{
						EnumDescriptorProto: &descriptorpb.EnumDescriptorProto{
							Name: ptrTo("State"),
							Value: []*descriptorpb.EnumValueDescriptorProto{
								{Name: ptrTo("STATE_UNKNOWN")},
								{Name: ptrTo("STATE_OK")},
							},
						},
						Comments: &protokit.Comment{Leading: "State enum."},
						Values: []*protokit.EnumValueDescriptor{
							{EnumValueDescriptorProto: &descriptorpb.EnumValueDescriptorProto{Name: ptrTo("STATE_UNKNOWN")}, Comments: &protokit.Comment{Leading: "Unknown"}},
							{EnumValueDescriptorProto: &descriptorpb.EnumValueDescriptorProto{Name: ptrTo("STATE_OK")}, Comments: &protokit.Comment{Leading: "Ok"}},
						},
					},
				},
			},
		},
		payload: map[string]string{
			"sample.proto": strings.Join([]string{
				"// @title: My API",
				"// @header: GroupA",
				"message Alpha {}",
				"message Beta {}",
				"// @header: GroupB",
				"message Gamma {}",
				"",
			}, "\n"),
		},
		readOffsets: make(map[string]int),
	}

	parsed, err := parser.Parse()
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	assert.Equal(t, "My API", parsed[0].Title())
	assert.Len(t, parsed[0].Entries(), 4)
	assert.Equal(t, "GroupA", parsed[0].Entries()[0].Message().Header())
	assert.Len(t, parsed[0].Entries()[0].Message().Fields(), 1)
	assert.Equal(t, "GroupA", parsed[0].Entries()[1].Message().Header())
	assert.Equal(t, "GroupB", parsed[0].Entries()[2].Message().Header())
	assert.Equal(t, EntryTypeEnum, parsed[0].Entries()[3].Type())

	ignoreAll := &DescriptorParser{
		descriptors: []*protokit.FileDescriptor{
			{
				FileDescriptorProto: &descriptorpb.FileDescriptorProto{
					Name: ptrTo("ignore.proto"),
				},
			},
		},
		payload:     map[string]string{"ignore.proto": "// @ignore-file\n"},
		readOffsets: make(map[string]int),
	}
	ignored, err := ignoreAll.Parse()
	require.NoError(t, err)
	assert.Len(t, ignored, 0)
}

func TestParseCodeAutocodeAndFlags(t *testing.T) {
	parser := &DescriptorParser{}

	markerJSON := &protokit.Descriptor{
		Comments: &protokit.Comment{Leading: "Example payload\n@code[json]:\n{\"a\":1}"},
	}
	pair, err := parser.parseCode(markerJSON)
	require.NoError(t, err)
	require.NotNil(t, pair)
	assert.Equal(t, SyntaxJson, pair.Left)
	assert.Equal(t, "{\n\t\"a\": 1\n}", pair.Right)

	markerXML := &protokit.Descriptor{
		Comments: &protokit.Comment{Leading: "Example payload\n@code[xml]:\n<node>value</node>"},
	}
	pair, err = parser.parseCode(markerXML)
	require.NoError(t, err)
	require.NotNil(t, pair)
	assert.Equal(t, SyntaxXml, pair.Left)
	assert.Equal(t, "<node>value</node>", pair.Right)

	invalidCode := &protokit.Descriptor{Comments: &protokit.Comment{Leading: "bad\n@code:\n{bad"}}
	pair, err = parser.parseCode(invalidCode)
	require.Error(t, err)
	assert.Nil(t, pair)

	auto := &protokit.Descriptor{Comments: &protokit.Comment{Leading: "Auto\n@autocode[xml]"}}
	autoPair, err := parser.parseAutocode(auto)
	require.NoError(t, err)
	require.NotNil(t, autoPair)
	assert.Equal(t, SyntaxXml, autoPair.Syntax())

	none := &protokit.Descriptor{Comments: &protokit.Comment{Leading: "No markers"}}
	autoPair, err = parser.parseAutocode(none)
	require.NoError(t, err)
	assert.Nil(t, autoPair)

	invalidAutocode := &protokit.Descriptor{Comments: &protokit.Comment{Leading: "Bad\n@autocode"}}
	_, err = parser.parseAutocode(invalidAutocode)
	require.Error(t, err)

	fieldDescriptor := &protokit.FieldDescriptor{
		Comments: &protokit.Comment{Leading: "Field doc\n@max=1\n@min=0\n@len=5\n@val=fixed\n@type=uint"},
	}
	fieldFlags, err := parser.parseFieldFlags(fieldDescriptor)
	require.NoError(t, err)
	require.NotNil(t, fieldFlags)
	require.True(t, fieldFlags.GetMax().Present())
	assert.Equal(t, 1.0, *fieldFlags.GetMax().Get())
	assert.Equal(t, 0.0, *fieldFlags.GetMin().Get())
	assert.Equal(t, 5, *fieldFlags.GetMaxLength().Get())
	assert.Equal(t, "fixed", *fieldFlags.GetValue().Get())
	assert.Equal(t, ValueTypeUInt, *fieldFlags.GetCustomType().Get())

	noFlags, err := parser.parseFieldFlags(&protokit.FieldDescriptor{Comments: &protokit.Comment{Leading: "No flags"}})
	require.NoError(t, err)
	assert.Nil(t, noFlags)

	_, err = parser.parseFieldFlags(&protokit.FieldDescriptor{Comments: &protokit.Comment{Leading: "Bad type\n@type=not_real"}})
	require.Error(t, err)

	assert.Equal(t, []string{"enum=hidden"}, parser.parseEnumFlags(&protokit.EnumDescriptor{Comments: &protokit.Comment{Leading: "Enum doc\n@enum=hidden"}}))
	enumValueFlags, err := parser.parseEnumValueFlags(&protokit.EnumValueDescriptor{Comments: &protokit.Comment{Leading: "Value\n@tag=main"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"tag=main"}, enumValueFlags)

	got, err := parseAutocodeChar("len", []string{"len=10"})
	require.NoError(t, err)
	assert.Equal(t, 10, got)

	got, err = parseAutocodeChar("unknown", []string{"unknown=1.5"})
	require.NoError(t, err)
	assert.Equal(t, 1.5, got)

	_, err = parseAutocodeChar("len", []string{"len=bad"})
	require.Error(t, err)
}

func TestParseSyntaxAndPayloadHelpers(t *testing.T) {
	syntax, _, err := parseSyntax("@code[JSON]:")
	require.NoError(t, err)
	assert.Equal(t, SyntaxJson, syntax)

	_, _, err = parseSyntax("@code")
	require.Error(t, err)

	parser := &DescriptorParser{
		descriptors: []*protokit.FileDescriptor{{
			FileDescriptorProto: &descriptorpb.FileDescriptorProto{Name: ptrTo("helpers.proto")},
		}},
		payload:     map[string]string{"helpers.proto": "// @title: Demo\nmessage A {\n}\n"},
		readOffsets: make(map[string]int),
	}

	title, index, err := parser.getMarker(parser.descriptors[0], TitleMarker)
	require.NoError(t, err)
	assert.Equal(t, "Demo", title)
	assert.GreaterOrEqual(t, index, 0)
	assert.Equal(t, strings.Index(parser.payload["helpers.proto"], "@title"), index)

	nextTitle, nextIndex, err := parser.nextMarker(parser.descriptors[0], TitleMarker)
	require.NoError(t, err)
	assert.Equal(t, "Demo", nextTitle)
	assert.Equal(t, index, nextIndex)

	markdownPayload := parser.payloadForFile("missing.proto")
	assert.Equal(t, "", markdownPayload)
	assert.Equal(t, 0, parser.lineAt("helpers.proto", -1))
}

func TestPayloadHelpersAndFinders(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "api.proto")
	require.NoError(t, os.WriteFile(filePath, []byte("syntax = \"proto3\";\nmessage Request {\n  string email = 1;\n}\n"), 0o600))

	f, err := os.Open(filePath)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	parser := &DescriptorParser{
		matchedFiles: map[string]*os.File{
			"api.proto": f,
		},
		payload:     map[string]string{},
		readOffsets: make(map[string]int),
		descriptors: []*protokit.FileDescriptor{{FileDescriptorProto: &descriptorpb.FileDescriptorProto{Name: ptrTo("api.proto")}}},
	}

	payload := parser.payloadForFile("api.proto")
	require.Equal(t, "syntax = \"proto3\";\nmessage Request {\n  string email = 1;\n}\n", payload)
	// Cached value is reused on second call.
	require.Equal(t, payload, parser.payloadForFile("api.proto"))

	assert.Equal(t, strings.Index(payload, "message"), parser.findDeclaration("api.proto", "message", "Request"))
	assert.True(t, strings.Index(payload, "email") > -1)
	assert.Greater(t, parser.lineAt("api.proto", strings.Index(payload, "message")), 0)
	assert.Equal(t, -1, parser.findFieldDeclaration("api.proto", "missing"))
}

func parserSyntaxJson() Syntax {
	return SyntaxJson
}

func ptrTo[T any](v T) *T {
	return &v
}
