package parser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommentIgnoredUsesAuthoritativeCommentSyntax(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		ignored bool
	}{
		{"bare", "@ignore", true},
		{"prose", "This user's private field. @ignore", true},
		{"raw line", "// @ignore", true},
		{"raw block", "/*\n * @ignore\n */", true},
		{"field document", "@doc: min=1 ignore", true},
		{"similar marker", "@ignored and @ignore-file", false},
		{"quoted", `Example "@ignore" and '@doc: ignore'`, false},
		{"quoted field value", `@doc: example="@ignore"`, false},
		{"inline code", "Use `@ignore` for private fields.", false},
		{"fenced", "```proto\n@ignore\n```", false},
		{"tilde fence", "~~~xml\n<value>@ignore</value>\n~~~", false},
		{"indented", "Example:\n\n    @ignore", false},
		{"tab indented", "\t@ignore", false},
		{"raw indented", "// Example:\n//\n//     @ignore", false},
		{"raw block indented", "/* Example:\n *\n *     @ignore\n */", false},
		{"fixed XML", "@code[xml]\n<value>@ignore</value>", false},
		{"fixed JSON", "@code[json]\n{\"description\":\"@ignore\"}", false},
		{"generated example", "@autocode[json]\n@ignore", false},
		{"after example fence", "```\n@ignore\n```\n@ignore", true},
		{"after indented example", "    @ignore\n@ignore", true},
		{"malformed", "@ignore[", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.ignored, CommentIgnored(test.comment))
			_, flags, err := parseFieldAnnotations(test.comment, false)
			if err == nil {
				ignored := false
				for _, flag := range flags {
					ignored = ignored || flag == IgnoreMarker
				}
				assert.Equal(t, test.ignored, ignored, "normal parsing must share the helper's lexical rules")
			}
		})
	}
}

func TestCommentIgnoreChunksRemainIndependent(t *testing.T) {
	chunks := []string{"@code[xml]\n<value>@ignore</value>", "@ignore"}
	ignored := false
	for _, chunk := range chunks {
		ignored = ignored || CommentIgnored(chunk)
	}
	assert.True(t, ignored)
	assert.False(t, CommentIgnored(strings.Join(chunks, "\n")), "fixed payload owns only its original comment chunk")
}

func TestIndentedCommentExamplesPreserveOffsetsAndDescription(t *testing.T) {
	source := "syntax = \"proto3\";\r\n// Example:\r\n//\r\n//     @ignore\r\n// @ignore\r\nmessage Value {}\r\n"
	expected := strings.LastIndex(source, "@ignore")
	assert.Equal(t, expected, sourceAnnotationIndex(source, MarkerDelimiter+IgnoreMarker))
	assert.Contains(t, commentDescription("Example:\n\n    @ignore"), "@ignore")
	annotations, err := parseCommentAnnotations("    ```\n@ignore")
	require.NoError(t, err)
	require.Len(t, annotations, 1, "an indented fence belongs to a code example, not an open Markdown fence")
	assert.Equal(t, IgnoreMarker, annotations[0].name)
}
