package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFieldAnnotationsCanonicalDocument(t *testing.T) {
	comment := `Contact ops@example.com.
@doc type=email example="Alice Doe <alice@example.com>" max_len=255 ignore`

	values, flags, err := parseFieldAnnotations(comment, true)
	require.NoError(t, err)

	assert.Equal(t, "email", values[AutocodeTypeMarker])
	assert.Equal(t, "Alice Doe <alice@example.com>", values[AutocodeValueMarker])
	assert.Equal(t, "255", values[AutocodeMaxLengthMarker])
	assert.Equal(t, []string{IgnoreMarker}, flags)
	assert.Equal(t, "Contact ops@example.com.", commentDescription(comment))
}

func TestParseFieldAnnotationsLegacySyntax(t *testing.T) {
	comment := `@type=email
@val="Alice Doe <alice@example.com>"
@len=255`

	values, flags, err := parseFieldAnnotations(comment, true)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		AutocodeTypeMarker:      "email",
		AutocodeValueMarker:     "Alice Doe <alice@example.com>",
		AutocodeMaxLengthMarker: "255",
	}, values)
	assert.Empty(t, flags)
}

func TestCanonicalDocumentAliasesAndSingleQuotes(t *testing.T) {
	values, flags, err := parseFieldAnnotations(
		`@doc: max-length=8 example='Alice\'s email' ignore`,
		true,
	)
	require.NoError(t, err)
	assert.Equal(t, "8", values[AutocodeMaxLengthMarker])
	assert.Equal(t, "Alice's email", values[AutocodeValueMarker])
	assert.Equal(t, []string{IgnoreMarker}, flags)
}

func TestAnnotationSyntaxErrors(t *testing.T) {
	_, _, err := parseFieldAnnotations("@len[12", false)
	assert.ErrorContains(t, err, "missing closing ]")

	_, _, err = parseFieldAnnotations("@doc min=1 min=2", true)
	assert.ErrorContains(t, err, "duplicate attribute")

	_, _, err = parseFieldAnnotations("@val=", false)
	assert.ErrorContains(t, err, "missing value")
}

func TestAnnotationsPreserveOrdinaryAtText(t *testing.T) {
	comment := "Contact ops@example.com or mention @owner in prose."

	_, _, err := parseFieldAnnotations(comment, true)
	require.NoError(t, err)
	assert.Equal(t, comment, commentDescription(comment))
	assert.Equal(t, -1, indexAnnotation(comment, TitleMarker))
	assert.Equal(t, -1, indexAnnotation("@titlecase value", TitleMarker))
}

func TestStrictAnnotationErrors(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		want    string
	}{
		{name: "unknown standalone", comment: "@typo=value", want: "unknown annotation @typo"},
		{name: "wrong context", comment: "@header: value", want: "not valid in this context"},
		{name: "duplicate", comment: "@min=1\n@min=2", want: "duplicate annotation @min"},
		{name: "duplicate flag", comment: "@doc ignore\n@ignore", want: "duplicate annotation @ignore"},
		{name: "unknown document attribute", comment: "@doc mystery=1", want: "unknown @doc attribute"},
		{name: "unterminated quote", comment: "@val=\"broken", want: "unterminated quoted value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseFieldAnnotations(tt.comment, true)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestCommentFlagsPreserveLegacyValues(t *testing.T) {
	assert.Equal(t, []string{"enum=hidden", "code[json]"}, commentFlags("@enum=hidden @code[json]"))
}

func TestParseFiniteAnnotationFloat(t *testing.T) {
	value, err := parseFiniteAnnotationFloat(AutocodeMinMarker, "1.25")
	require.NoError(t, err)
	assert.Equal(t, 1.25, value)

	for _, raw := range []string{"NaN", "+Inf", "not-a-number"} {
		_, err := parseFiniteAnnotationFloat(AutocodeMaxMarker, raw)
		assert.ErrorContains(t, err, "invalid @max value")
	}
}

func TestParseSyntaxRejectsUnknownFormat(t *testing.T) {
	syntax, consumed, err := parseSyntax("[YAML]")
	assert.ErrorContains(t, err, "unknown syntax")
	assert.Equal(t, -1, consumed)
	assert.Equal(t, SyntaxJson, syntax)

	syntax, consumed, err = parseSyntax("[JSON]:")
	require.NoError(t, err)
	assert.Equal(t, SyntaxJson, syntax)
	assert.Equal(t, len("[JSON]:"), consumed)
}
