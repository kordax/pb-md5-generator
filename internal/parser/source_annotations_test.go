package parser

import (
	"strconv"
	"strings"
	"testing"

	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseAnnotationSource(t *testing.T, source string) (*DescriptorParser, []ParsedFile, error) {
	t.Helper()
	request, err := internalproto.RequestFromSource("annotations.proto", []byte(source))
	require.NoError(t, err)
	parser := NewDescriptorParserWithOptions(request, ParserOptions{StrictAnnotations: true, Sources: map[string][]byte{"annotations.proto": []byte(source)}})
	files, err := parser.Parse()
	return parser, files, err
}

func TestSourceMarkersIgnoreStringsAndRetainOffsets(t *testing.T) {
	source := `syntax = "proto3";
option java_package = "message Demo @ignore-file @title: fake @header: fake // /* quote \" still string";
option objc_class_prefix = 'other @ignore-file @title: fake';
option php_namespace = "backslash \\";
// @title: Actual API
// @header: First
message Demo {
  string value = 1 [json_name = "fake @code[json] @autocode[xml]"];
}
/* @header: Second */
message Next {}
`
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(newline, func(t *testing.T) {
			input := strings.ReplaceAll(source, "\n", newline)
			parser, files, err := parseAnnotationSource(t, input)
			require.NoError(t, err)
			require.Len(t, files, 1)
			assert.Equal(t, "Actual API", files[0].Title())
			require.Len(t, files[0].Entries(), 2)
			assert.Equal(t, "First", files[0].Entries()[0].Message().Header())
			assert.Equal(t, "Second", files[0].Entries()[1].Message().Header())
			marker, index, err := parser.getMarker(parser.descriptors[0], TitleMarker)
			require.NoError(t, err)
			assert.Equal(t, "Actual API", marker)
			assert.Equal(t, strings.Index(input, "@title: Actual"), index)
			for i, expected := range []string{"First", "Second"} {
				value, index, err := parser.nextMarker(parser.descriptors[0], HeaderMarker)
				require.NoError(t, err)
				assert.Equal(t, expected, value)
				assert.Equal(t, strings.Index(input, "@header: "+expected), index, i)
			}
			_, index, err = parser.nextMarker(parser.descriptors[0], HeaderMarker)
			require.NoError(t, err)
			assert.Equal(t, -1, index)
		})
	}
}

func TestFencedAndInlineExamplesRemainDocumentation(t *testing.T) {
	source := "syntax = \"proto3\";\n// @title: Actual API\n/*\n * Example syntax:\n * ```protobuf\n * // @ignore-file\n * // @title: Example title\n * // @header: Example header\n * // @autocode[xml]\n * // @code[json]: {}\n * // @unknown=value\n * ```\n * Ordinary `inline @autocode[json]` and \"quoted @header: text\".\n */\nmessage Example {}\n"
	_, files, err := parseAnnotationSource(t, source)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "Actual API", files[0].Title())
	message := files[0].Entries()[0].Message()
	assert.False(t, message.Autocode().Present())
	assert.False(t, message.Code().Present())
	assert.Empty(t, message.Header())
	assert.Contains(t, message.Description(), "```protobuf\n// @ignore-file")
	assert.Contains(t, message.Description(), "// @unknown=value\n```")
	assert.Contains(t, message.Description(), "inline @autocode[json]")
	assert.Contains(t, message.Description(), "quoted @header: text")
}

func TestFixedExamplesOwnTheirPayload(t *testing.T) {
	source := `syntax = "proto3";
// @title: Actual API
/*
 * Fixed data is not annotation syntax.
 * @code[json]:
 * {"text":"value @autocode[xml] @ignore-file @title: fake @header: fake", "quoted":"message Example"}
 */
message Example {}
`
	_, files, err := parseAnnotationSource(t, source)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "Actual API", files[0].Title())
	message := files[0].Entries()[0].Message()
	assert.False(t, message.Autocode().Present())
	require.True(t, message.Code().Present())
	assert.Contains(t, message.Code().Get().Right, "@autocode[xml]")
	assert.Empty(t, message.Header())
}

func TestAnnotationLocationsUseDeclarationsAndAssociatedComments(t *testing.T) {
	for _, test := range []struct {
		name, source string
		line         int
		entity       string
	}{
		{"duplicate fields", "syntax=\"proto3\";\noption java_package=\"value = 1 @code[json]\";\nmessage First { int32 value=1; }\nmessage Second {\n // @doc min=2147483648\n int32 value=1;\n}\n", 6, "field value"},
		{"nested field", "syntax=\"proto3\";\nmessage Outer {\n message Child {\n // @doc min=2147483648\n int32 value=1;\n }\n}\n", 5, "field value"},
		{"second leading code", "syntax=\"proto3\";\noption java_package=\"fake @code[json]\";\n// @code[json]: {}\nmessage First {}\n// @code[json]: {bad}\nmessage Second {}\n", 5, "message Second marker @code"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := parseAnnotationSource(t, test.source)
			var located SourceError
			require.ErrorAs(t, err, &located)
			assert.Equal(t, "annotations.proto", located.File)
			assert.Equal(t, test.line, located.Line)
			assert.Equal(t, test.entity, located.Entity)
		})
	}
}

func TestTrailingMarkerLocationsFromCompilerComments(t *testing.T) {
	source := "syntax=\"proto3\";\n// @code[json]: {}\nmessage First {}\nmessage Second {} // @code[json]: {bad}\n"
	request, err := internalproto.RequestFromSource("annotations.proto", []byte(source))
	require.NoError(t, err)
	// Some compilers attach a same-line closing-brace comment to the message;
	// protocompile's basic source-info mode omits it. Exercise that input contract.
	found := false
	for _, location := range request.ProtoFile[0].GetSourceCodeInfo().GetLocation() {
		if len(location.Path) == 2 && location.Path[0] == 4 && location.Path[1] == 1 {
			trailing := "@code[json]: {bad}"
			location.TrailingComments = &trailing
			found = true
		}
	}
	require.True(t, found)
	_, err = NewDescriptorParserWithOptions(request, ParserOptions{StrictAnnotations: true, Sources: map[string][]byte{"annotations.proto": []byte(source)}}).Parse()
	var located SourceError
	require.ErrorAs(t, err, &located)
	assert.Equal(t, 4, located.Line)
	assert.Equal(t, "message Second marker @code", located.Entity)
}

func TestAnnotationTokenQuotesAndInlineCode(t *testing.T) {
	for _, comment := range []string{
		`"quoted @title: fake"`, `'quoted @title: fake'`, "`inline @title: fake`", "``inline ` @title: fake``",
	} {
		assert.Equal(t, -1, commentAnnotationIndex(comment, TitleMarker), comment)
	}
	assert.Equal(t, strings.Index("user's API @title: actual", "@title"), commentAnnotationIndex("user's API @title: actual", TitleMarker))
	values, _, err := parseFieldAnnotations("@doc example=\"backtick ` and @min=1\" min=2 max=3", true)
	require.NoError(t, err)
	assert.Equal(t, "backtick ` and @min=1", values[AutocodeValueMarker])
	assert.Equal(t, "2", values[AutocodeMinMarker])
	assert.Equal(t, "3", values[AutocodeMaxMarker])
	parser := &DescriptorParser{}
	pair, err := parser.parseCode(&protokit.Descriptor{Comments: &protokit.Comment{Leading: "Example `payload @autocode[json]`\n@code[json]:\n{}"}})
	require.NoError(t, err)
	require.NotNil(t, pair)
	assert.Equal(t, "{}", pair.Right)
}

func BenchmarkCommentMarkerLexer(b *testing.B) {
	for _, count := range []int{1000, 2000} {
		b.Run(strconv.Itoa(count)+" unmatched widths", func(b *testing.B) {
			var input strings.Builder
			input.WriteString("Examples: ")
			for width := 1; width <= count; width++ {
				input.WriteString(strings.Repeat("`", width))
				input.WriteString(" x ")
			}
			input.WriteString("\n@title: Actual")
			source := input.String()
			expected := strings.Index(source, "@title")
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				if commentAnnotationIndex(source, TitleMarker) != expected {
					b.Fatal("actual marker was lost")
				}
			}
		})
	}
}

func FuzzSourceAnnotationLexer(f *testing.F) {
	for _, source := range []string{"// @title: API\n", `option x="fake @ignore-file"; // @header: Actual`, "/* ```proto\n@title: fake\n``` */", "/* @code[json]:\n{\"s\":\"fake @title\"} */", "'\\\\' // @title: API"} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		walkSourceComments(source, func(comment string, offset int) bool {
			assert.GreaterOrEqual(t, offset, 0)
			assert.LessOrEqual(t, offset+len(comment), len(source))
			return walkCommentAnnotations(comment, offset, func(annotation sourceAnnotation) bool {
				assert.GreaterOrEqual(t, annotation.index, offset)
				assert.Less(t, annotation.index, offset+len(comment))
				assert.Equal(t, byte('@'), source[annotation.index])
				return true
			})
		})
	})
}
