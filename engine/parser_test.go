package engine

import (
	"testing"

	"github.com/kordax/pb-md5-generator/engine/render"
	"github.com/pseudomuto/protokit"
	"github.com/stretchr/testify/assert"
)

func TestDescriptorParser_ParseHeader(t *testing.T) {
	request, err := testRequest()
	assert.NoError(t, err)
	parser := NewDescriptorParser(request)
	header, _, err := parser.nextMarker(protokit.ParseCodeGenRequest(request)[0], HeaderMarker)
	assert.NoError(t, err)
	assert.NotEmpty(t, header)
}

func TestDescriptorParser_DescriptorToDocument(t *testing.T) {
	request, err := testRequest()
	assert.NoError(t, err)
	parser := NewDescriptorParser(request)
	entries, err := parser.Parse()
	assert.NoError(t, err)
	assert.NotEmpty(t, entries)

	generator := NewMDGenerator(NewCodegenerator())
	document, err := generator.Generate(entries)
	assert.NoError(t, err)
	assert.NotEmpty(t, document)

	renderer := render.NewMarkdownRenderer(render.DefaultConfig())
	marshalled, err := renderer.Render(document)
	assert.NoError(t, err)
	assert.NotEmpty(t, marshalled)
}
