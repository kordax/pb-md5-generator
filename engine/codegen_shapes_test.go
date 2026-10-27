package engine

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalparser "github.com/kordax/pb-md5-generator/internal/parser"
	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func parseExampleSource(t *testing.T, source string) ([]ParsedFile, *pluginpb.CodeGeneratorRequest) {
	t.Helper()
	root := t.TempDir()
	name := filepath.Join(root, "api.proto")
	require.NoError(t, os.WriteFile(name, []byte(source), 0o600))
	request, err := (internalproto.SourceParser{ProtoDir: root}).RequestFromFiles([]string{name})
	require.NoError(t, err)
	files, err := NewDescriptorParserWithOptions(request, ParserOptions{
		StrictAnnotations: true,
		Sources:           map[string][]byte{"api.proto": []byte(source)},
	}).Parse()
	require.NoError(t, err)
	return files, request
}

func exampleMessage(t *testing.T, files []ParsedFile, name string) *Message {
	t.Helper()
	for _, file := range files {
		for _, entry := range file.entries {
			if entry.msg != nil && entry.msg.m.GetName() == name {
				return entry.msg
			}
		}
	}
	require.FailNow(t, "message not found", name)
	return nil
}

func TestGeneratedExampleMatchesProtobufShapes(t *testing.T) {
	files, request := parseExampleSource(t, `syntax = "proto3";
package shapes;
message Child { string name = 1; }
// @autocode[json]
message Example {
  repeated string tags = 1;
  Child child = 2;
  map<string, Child> children = 3;
  map<int32, string> labels = 4;
  oneof destination { string email = 5; int32 account = 6; }
  bytes payload = 7;
  // @doc example=18446744073709551615
  uint64 size = 8;
  enum State { UNKNOWN = 0; READY = 1; }
  // @doc example=READY
  State state = 9;
  fixed32 checksum = 10;
  sfixed32 offset = 11;
}`)
	block, err := NewCodegenerator().Generate(files, exampleMessage(t, files, "Example"))
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(block.GetText()), &document))
	fileDescriptor, err := protodesc.NewFile(request.ProtoFile[0], nil)
	require.NoError(t, err)
	message := dynamicpb.NewMessage(fileDescriptor.Messages().ByName("Example"))
	require.NoError(t, protojson.Unmarshal(document["Example"], message), "generated example must decode as the documented protobuf type")
	fields := message.Descriptor().Fields()
	assert.Equal(t, 1, message.Get(fields.ByName("tags")).List().Len())
	assert.Equal(t, 1, message.Get(fields.ByName("children")).Map().Len())
	assert.Equal(t, 1, message.Get(fields.ByName("labels")).Map().Len())
	assert.Equal(t, protoreflect.Name("email"), message.WhichOneof(message.Descriptor().Oneofs().Get(0)).Name())
	assert.Equal(t, uint64(math.MaxUint64), message.Get(fields.ByName("size")).Uint())
	assert.Equal(t, protoreflect.EnumNumber(1), message.Get(fields.ByName("state")).Enum())
}

func TestRecursiveExamplesTerminateWithEmptyContainers(t *testing.T) {
	files, _ := parseExampleSource(t, `syntax = "proto3"; package shapes;
// @autocode[json]
message Node {
  string name = 1;
  Node next = 2;
  repeated Node children = 3;
  map<string, Node> links = 4;
}`)
	block, err := NewCodegenerator().Generate(files, exampleMessage(t, files, "Node"))
	require.NoError(t, err)
	var parsed map[string]map[string]any
	// The envelope also has a string trx, so decode its body separately.
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(block.GetText()), &envelope))
	require.NoError(t, json.Unmarshal([]byte(`{"Node":`+string(envelope["Node"])+`}`), &parsed))
	assert.NotContains(t, parsed["Node"], "next")
	assert.Empty(t, parsed["Node"]["children"])
	assert.Empty(t, parsed["Node"]["links"])
}

func TestAutocodeXMLIsXMLAndEscapesValues(t *testing.T) {
	files, _ := parseExampleSource(t, `syntax = "proto3";
// @autocode[xml]
message Example {
  // @doc example="<tag>&value"
  string text = 1;
  repeated string items = 2;
  map<int32, string> labels = 3;
}`)
	block, err := NewCodegenerator().Generate(files, exampleMessage(t, files, "Example"))
	require.NoError(t, err)
	decoder := xml.NewDecoder(strings.NewReader(block.GetText()))
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if data, ok := token.(xml.CharData); ok {
			text.Write(data)
		}
	}
	assert.Contains(t, block.GetText(), "<example>")
	assert.Contains(t, text.String(), "<tag>&value")
	assert.NotContains(t, block.GetText(), "{\n")
}

func TestExampleErrorsReachMarkdownGenerator(t *testing.T) {
	files, _ := parseExampleSource(t, `syntax = "proto3";
// @autocode[json]
message Broken {
  // @doc example=not-an-integer
  int32 value = 1;
}`)
	_, err := NewMDGenerator(NewCodegenerator()).Generate(files)
	require.ErrorContains(t, err, "generate example for Broken")
	assert.ErrorContains(t, err, "field value")
}

func TestWideIntegerRangesDoNotPanic(t *testing.T) {
	generator := NewCodegenerator()
	for range 100 {
		value, err := int64WithinRangeFrom(generator.random, math.MinInt64, math.MaxInt64)
		require.NoError(t, err)
		assert.Less(t, value, int64(math.MaxInt64))
	}
	files, _ := parseExampleSource(t, `syntax = "proto3";
// @autocode[json]
message Wide {
  // @doc min=-9000000000000000000 max=9000000000000000000
  int64 value = 1;
}`)
	_, err := NewCodegenerator().Generate(files, exampleMessage(t, files, "Wide"))
	require.NoError(t, err)
}

func TestIntegerExamplesRejectInvalidBoundsAndWidths(t *testing.T) {
	for _, test := range []struct {
		source     string
		parseError bool
	}{
		{"// @doc min=-1\nuint64 value = 1;", true},
		{"// @doc example=4294967296\nuint32 value = 1;", false},
		{"// @doc example=2147483648\nint32 value = 1;", false},
		{"// @doc min=1e100\nint64 value = 1;", true},
		{"// @doc min=2147483647.8\nint32 value = 1;", true},
		{"// @doc min=4294967295.8\nuint32 value = 1;", true},
		{"// @doc min=1.2 max=1.8\nint32 value = 1;", true},
		{"// @doc min=0.1 max=0.9\nuint32 value = 1;", true},
	} {
		t.Run(test.source, func(t *testing.T) {
			source := "syntax=\"proto3\";\n// @autocode[json]\nmessage Example {\n" + test.source + "\n}"
			request, err := internalproto.RequestFromSource("bounds.proto", []byte(source))
			require.NoError(t, err)
			files, err := NewDescriptorParserWithOptions(request, ParserOptions{StrictAnnotations: true, Sources: map[string][]byte{"bounds.proto": []byte(source)}}).Parse()
			if test.parseError {
				var located internalparser.SourceError
				require.ErrorAs(t, err, &located)
				assert.Equal(t, "bounds.proto", located.File)
				assert.Equal(t, 5, located.Line)
				assert.Equal(t, "field value", located.Entity)
				return
			}
			require.NoError(t, err)
			_, err = NewCodegenerator().Generate(files, exampleMessage(t, files, "Example"))
			require.Error(t, err)
		})
	}
}

func TestExampleDepthAndFieldBudgets(t *testing.T) {
	leaf := &Message{m: &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Leaf")}}}
	for range maxExampleDepth + 1 {
		parent := &Message{m: &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Level")}}}
		field := NewMessageField(fieldDescriptor("child", descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, parent.m), parent.m, "", ValueTypeStruct, nil)
		field.isMsg = leaf
		parent.fields = []MessageField{*field}
		leaf = parent
	}
	leaf.autocode = Some(AutocodeOpt{syntax: SyntaxJson})
	_, err := NewCodegenerator().Generate(nil, leaf)
	require.ErrorContains(t, err, "maximum message depth")

	leaf = &Message{m: &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Leaf")}}}
	for range 15 {
		parent := &Message{m: &protokit.Descriptor{DescriptorProto: &descriptorpb.DescriptorProto{Name: strPtr("Branch")}}}
		for _, name := range []string{"left", "right"} {
			field := NewMessageField(fieldDescriptor(name, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, parent.m), parent.m, "", ValueTypeStruct, nil)
			field.isMsg = leaf
			parent.fields = append(parent.fields, *field)
		}
		leaf = parent
	}
	leaf.autocode = Some(AutocodeOpt{syntax: SyntaxJson})
	_, err = NewCodegenerator().Generate(nil, leaf)
	require.ErrorContains(t, err, "maximum field budget")
}
