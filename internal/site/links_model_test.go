package site

import (
	"bytes"
	"html"
	"regexp"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func destinationManifest() Manifest {
	return Manifest{Pages: []Page{
		{ID: "home", Title: "Home", Source: "home.md", Path: "index.html", Kind: "markdown"},
		{ID: "chapter", Title: "Chapter", Source: "chapter.md", Path: "guide/chapter.html", Kind: "markdown"},
	}, Assets: []string{"image.png", "manual.pdf"}}
}

func modelDestinationInlines(blocks []document.Block) []document.Inline {
	var result []document.Inline
	var visit func([]document.Inline)
	visit = func(inlines []document.Inline) {
		for _, inline := range inlines {
			if inline.Kind == document.InlineLink || inline.Kind == document.InlineImage {
				result = append(result, inline)
			}
			visit(inline.Children)
		}
	}
	for _, block := range blocks {
		visit(block.Inlines)
		result = append(result, modelDestinationInlines(block.Children)...)
	}
	return result
}

func renderedDestinations(content string) []string {
	matches := regexp.MustCompile(`(?:href|src)="([^"]*)"`).FindAllStringSubmatch(content, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, html.UnescapeString(match[1]))
	}
	return result
}

func TestCommonMarkDestinationsMatchRenderedHTMLAndModel(t *testing.T) {
	source := []byte(strings.Join([]string{
		`[local](chapter&#46;md#topic)`,
		`[escaped](chapter\.md)`,
		`[query](https://example.test/?a=1&amp;b=2)`,
		`[once](https://example.test/?q=&amp;amp;)`,
		`[reserved](https://example.test/a%2Fb%252Fc?q=a%26b%2526c#x%2Fy)`,
		`![remote](https://images.test/icon.png?a=1&amp;b=2)`,
		`![asset](image\.png?a=1&amp;b=2#part)`,
		`[PDF](manual.pdf#page=3)`,
		`<https://example.test/?a=1&amp;b=2>`,
		`<help@example.test>`,
	}, "\n\n"))
	original := bytes.Clone(source)
	content, err := compileMarkdown(source, 0, destinationManifest())
	require.NoError(t, err)
	assert.Equal(t, original, source, "normalization must not modify the source snapshot")
	expected := []string{
		"guide/chapter.html#topic",
		"guide/chapter.html",
		"https://example.test/?a=1&b=2",
		"https://example.test/?q=&amp;",
		"https://example.test/a%2Fb%252Fc?q=a%26b%2526c#x%2Fy",
		"https://images.test/icon.png?a=1&b=2",
		"assets/image.png?a=1&b=2#part",
		"assets/manual.pdf#page=3",
		"https://example.test/?a=1&amp;b=2",
		"mailto:help@example.test",
	}
	assert.Equal(t, expected, renderedDestinations(content.HTML))
	inlines := modelDestinationInlines(content.Blocks)
	require.Len(t, inlines, len(expected))
	for index, inline := range inlines {
		assert.Equal(t, expected[index], inline.Destination, "destination %d", index)
	}
	assert.Equal(t, &document.Reference{Page: "chapter", Anchor: "topic"}, inlines[0].Reference)
	assert.Equal(t, &document.Reference{Page: "chapter"}, inlines[1].Reference)
	assert.Equal(t, &document.Reference{Asset: "image.png", Anchor: "part"}, inlines[6].Reference)
	assert.Equal(t, &document.Reference{Asset: "manual.pdf", Anchor: "page=3"}, inlines[7].Reference)
	assert.Contains(t, content.HTML, `?q=&amp;amp;`, "a literal entity in the URI must survive exactly one CommonMark decode")
}

func TestCommonMarkDestinationNormalizationKeepsSchemeValidation(t *testing.T) {
	for _, source := range []string{
		`[x](jav&#x61;script:alert%281%29)`,
		`[x](javascript\:alert%281%29)`,
		`[x](file&#58;///etc/passwd)`,
		`[x](&#47;&#47;example.test/path)`,
		`[x](%2Fetc%2Fpasswd)`,
		`![x](data&#58;image/png;base64,AAAA)`,
		`![x](http&#58;//example.test/image.png)`,
	} {
		t.Run(source, func(t *testing.T) {
			input := []byte(source)
			original := bytes.Clone(input)
			_, err := compileMarkdown(input, 0, destinationManifest())
			assert.Error(t, err)
			assert.Equal(t, original, input)
		})
	}
}

func TestCommonMarkScopedDestinationDoesNotDecodeEntitiesTwice(t *testing.T) {
	source := []byte("# Section\n\n[scoped](?a=1&amp;amp;b=2#section)\n")
	original := bytes.Clone(source)
	content, err := compileMarkdownScoped(source, 0, destinationManifest(), "operation", nil)
	require.NoError(t, err)
	assert.Equal(t, original, source)
	expected := "?a=1&amp;b=2#operation--section"
	assert.Equal(t, []string{expected}, renderedDestinations(content.HTML))
	inlines := modelDestinationInlines(content.Blocks)
	require.Len(t, inlines, 1)
	assert.Equal(t, expected, inlines[0].Destination)
	assert.Equal(t, &document.Reference{Page: "home", Anchor: "operation--section"}, inlines[0].Reference)
	require.Len(t, content.References, 1)
	assert.Equal(t, "operation--section", content.References[0].fragment)
}

func TestCommonMarkDestinationTransformReceivesNormalizedURI(t *testing.T) {
	var seen []string
	content, err := compileMarkdownScoped([]byte(`[local](chapter&#46;md)`), 0, destinationManifest(), "", func(destination string) (string, error) {
		seen = append(seen, destination)
		return destination + "?q=&amp;", nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"chapter.md"}, seen)
	expected := "guide/chapter.html?q=&amp;"
	assert.Equal(t, []string{expected}, renderedDestinations(content.HTML))
	inlines := modelDestinationInlines(content.Blocks)
	require.Len(t, inlines, 1)
	assert.Equal(t, expected, inlines[0].Destination)
}
