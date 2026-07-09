package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kordax/pb-md5-generator/engine/md"
	"github.com/kordax/pb-md5-generator/internal/parser"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestMDGeneratorGenerateAndTOC(t *testing.T) {
	generator := NewMDGenerator(NewCodegenerator())

	clientRequest := requireMessage(t, "ClientRequest")
	serverResponse := requireMessage(t, "ServerResponse")
	tokenRequest := requireMessage(t, "TokenRequest")
	loginStatus := requireEnum(t, "LoginStatus")

	doc, err := generator.Generate([]ParsedFile{
		{
			index:    10,
			filename: "zeta.proto",
			title:    "Zeta API",
			entries: []Entry{
				{index: 2, t: EntryTypeMessage, msg: clientRequest},
				{index: 1, t: EntryTypeEnum, enum: loginStatus},
				{index: 0, t: EntryTypeMessage, msg: tokenRequest},
			},
		},
		{
			index:    1,
			filename: "alpha.proto",
			entries: []Entry{
				{index: 0, t: EntryTypeMessage, msg: serverResponse},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, doc.GetSections(), 4)
	sections := doc.GetSections()
	assert.Equal(t, "alpha.proto", sections[1].GetElements()[0].(*md.Header).GetText())
	assert.Equal(t, "API Description", sections[1].GetElements()[1].(*md.Header).GetText())
	assert.Equal(t, "Zeta API", sections[2].GetElements()[0].(*md.Header).GetText())

	tocSection := sections[0]
	tocMain := tocSection.GetElements()[0].(*md.List)
	assert.Equal(t, "Table Of Contents", tocMain.GetEntries()[0].GetElement().(*md.Text).GetText())

	tocMessages := tocMain.GetEntries()[0].GetElements()[0].(*md.List)
	assert.Equal(t, "ServerResponse", tocMessages.GetEntries()[0].GetElement().(*md.Link).GetText())
	assert.Equal(t, "TokenRequest", tocMessages.GetEntries()[1].GetElement().(*md.Link).GetText())

	enumMain := tocSection.GetElements()[1].(*md.List)
	assert.Equal(t, "Enums", enumMain.GetEntries()[0].GetElement().(*md.Text).GetText())
	enumValues := enumMain.GetEntries()[0].GetElements()[0].(*md.List)
	assert.Equal(t, "LoginStatus", enumValues.GetEntries()[0].GetElement().(*md.Link).GetText())
}

func TestMDGeneratorMessageAndEnum(t *testing.T) {
	generator := NewMDGenerator(NewCodegenerator())
	section := &md.Section{}

	message := Message{
		description: "With typed flags",
		m:           requireMessage(t, "TokenRequest").m,
		fields: []MessageField{
			*NewMessageField(
				protoField("id", descriptorpb.FieldDescriptorProto_TYPE_INT64, requireMessage(t, "TokenRequest").m),
				requireMessage(t, "TokenRequest").m,
				"order id",
				ValueTypeInt,
				&FieldFlags{
					min:       Some(1.0),
					max:       Some(9.0),
					value:     Some("7"),
					maxLength: Some(12),
				},
			),
		},
	}

	require.NoError(t, generator.message(nil, &message, section))

	var fieldsTable *md.Table
	for _, element := range section.GetElements() {
		if table, ok := element.(*md.Table); ok {
			fieldsTable = table
		}
	}
	require.NotNil(t, fieldsTable)
	assert.Equal(t, 7, len(fieldsTable.GetColumns()))

	sectionWithoutDescription := &md.Section{}
	messageWithoutDescription := Message{m: requireMessage(t, "TokenRequest").m}
	require.NoError(t, generator.message(nil, &messageWithoutDescription, sectionWithoutDescription))
	assert.Len(t, sectionWithoutDescription.GetElements(), 3)

	err := generator.message(nil, &Message{m: &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{}}}, &md.Section{})
	assert.Error(t, err)

	err = generator.enum(&Enum{e: &protokit.EnumDescriptor{EnumDescriptorProto: &descriptorpb.EnumDescriptorProto{}}}, &md.Section{})
	assert.Error(t, err)

	enumSection := &md.Section{}
	require.NoError(t, generator.enum(requireEnum(t, "LoginStatus"), enumSection))
	assert.Len(t, enumSection.GetElements(), 3)
}

func TestMDGeneratorListHelpers(t *testing.T) {
	generator := NewMDGenerator(NewCodegenerator())

	entries := []Entry{
		{index: 0, t: EntryTypeMessage, msg: requireMessage(t, "TokenRequest")},
		{index: 1, t: EntryTypeMessage, msg: requireMessage(t, "ServerResponse")},
	}
	list := generator.list(entries, nil, false, 1)
	require.Len(t, list.GetEntries(), 2)
	assert.Equal(t, "TokenRequest", list.GetEntries()[0].GetElement().(*md.Link).GetText())

	rootMessage := &Message{
		m: requireMessage(t, "ClientRequest").m,
		entries: []Entry{
			{index: 0, t: EntryTypeMessage, msg: &Message{m: requireMessage(t, "ServerResponse").m}},
		},
	}
	rootEntries := []Entry{{index: 0, t: EntryTypeMessage, msg: rootMessage}}

	noNested := generator.list(rootEntries, nil, false, 0)
	assert.Len(t, noNested.GetEntries()[0].GetElements(), 0)

	nested := generator.list(rootEntries, nil, false, 1)
	assert.Len(t, nested.GetEntries()[0].GetElements(), 1)

	optionalField := protoField("opt", descriptorpb.FieldDescriptorProto_TYPE_STRING, requireMessage(t, "TokenRequest").m)
	optionalField.Label = descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	requiredField := protoField("req", descriptorpb.FieldDescriptorProto_TYPE_STRING, requireMessage(t, "TokenRequest").m)
	requiredField.Label = descriptorpb.FieldDescriptorProto_LABEL_REQUIRED.Enum()
	assert.Equal(t, "", pbLabel(optionalField))
	assert.Equal(t, "LABEL_REQUIRED", pbLabel(requiredField))
}

func protoField(name string, fieldType descriptorpb.FieldDescriptorProto_Type, message *protokit.Descriptor) *protokit.FieldDescriptor {
	return &protokit.FieldDescriptor{
		FieldDescriptorProto: &descriptorpb.FieldDescriptorProto{Type: &fieldType, Name: &name},
		Message:              message,
		Comments:             &protokit.Comment{},
	}
}

func requireMessage(t *testing.T, name string) *Message {
	t.Helper()

	parsed, err := parser.NewDescriptorParser(testGeneratorDescriptorRequest(t)).Parse()
	require.NoError(t, err)

	for _, file := range parsed {
		for _, entry := range file.Entries() {
			msg := entry.Message()
			if msg != nil && msg.Descriptor().GetName() == name {
				converted := convertMessage(*msg)
				return &converted
			}
		}
	}

	require.Failf(t, "message not found", "message with name %q not found in fixture", name)
	return nil
}

func requireEnum(t *testing.T, name string) *Enum {
	t.Helper()

	parsed, err := parser.NewDescriptorParser(testGeneratorDescriptorRequest(t)).Parse()
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

	require.Failf(t, "enum not found", "enum with name %q not found in fixture", name)
	return nil
}

func testGeneratorDescriptorRequest(t *testing.T) *pluginpb.CodeGeneratorRequest {
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

func strPtr(value string) *string {
	return &value
}
