package parser

import (
	"strings"
	"testing"

	internalproto "github.com/kordax/pb-md5-generator/internal/proto"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFixedXMLCodePreservesValidDocuments(t *testing.T) {
	for _, payload := range []string{
		`<root/>`,
		`<node key="a &amp; b">&lt;value&gt; &amp; &#35;</node>`,
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<root><child>value</child></root>",
		"<!-- before -->\n<root><![CDATA[a < b & c]]></root>\n<!-- after -->",
		"<?example test?>\n<root/>",
		"<!DOCTYPE\troot>\n<root/>",
		`<root xmlns:a="urn:one" xmlns:b="urn:two" a:key="a" b:key="b"/>`,
	} {
		t.Run(payload, func(t *testing.T) {
			parsed, err := (&DescriptorParser{}).parseCode(&protokit.Descriptor{
				Comments: &protokit.Comment{Leading: "@code[xml]:\n" + payload},
			})
			require.NoError(t, err)
			require.NotNil(t, parsed)
			assert.Equal(t, SyntaxXml, parsed.Left)
			assert.Equal(t, payload, parsed.Right, "XML validation must preserve authored escaping and formatting")
		})
	}
}

func TestFixedXMLCodeErrorsAreLocated(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
	}{
		{name: "empty"},
		{name: "whitespace", payload: " \n\t"},
		{name: "only comment", payload: "<!-- no root -->"},
		{name: "unclosed element", payload: "<root>"},
		{name: "mismatched tags", payload: "<outer><inner></outer>"},
		{name: "multiple roots", payload: "<root/><another/>"},
		{name: "leading text", payload: "unexpected<root/>"},
		{name: "trailing text", payload: "<root/>unexpected"},
		{name: "invalid entity", payload: "<root>&unknown;</root>"},
		{name: "unescaped ampersand", payload: "<root>a & b</root>"},
		{name: "duplicate attribute", payload: `<root key="one" key="two"/>`},
		{name: "duplicate expanded attribute", payload: `<root xmlns:a="urn:one" xmlns:b="urn:one" a:key="a" b:key="b"/>`},
		{name: "misplaced declaration", payload: `<!-- before --><?xml version="1.0"?><root/>`},
		{name: "reserved processing instruction", payload: `<?XML version="1.0"?><root/>`},
		{name: "trailing CDATA", payload: `<root/><![CDATA[ ]]>`},
		{name: "misplaced directive", payload: `<root/><!DOCTYPE root>`},
		{name: "unknown directive", payload: `<!other root><root/>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "syntax = \"proto3\";\n\n/*\n * @code[xml]:\n * " + strings.ReplaceAll(test.payload, "\n", "\n * ") + "\n */\nmessage Example {}\n"
			request, err := internalproto.RequestFromSource("api.proto", []byte(source))
			require.NoError(t, err)
			_, err = NewDescriptorParserWithOptions(request, ParserOptions{
				StrictAnnotations: true,
				Sources:           map[string][]byte{"api.proto": []byte(source)},
			}).Parse()
			require.ErrorContains(t, err, "invalid XML code example")
			var located SourceError
			require.ErrorAs(t, err, &located)
			assert.Equal(t, "api.proto", located.File)
			assert.Equal(t, 4, located.Line)
			assert.Equal(t, "message Example marker @code", located.Entity)
		})
	}
}
