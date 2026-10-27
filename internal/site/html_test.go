package site

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func htmlManifest() Manifest {
	return Manifest{Version: 1, Title: "HTML documentation", Pages: []Page{{ID: "home", Title: "Home", Source: "index.html", Path: "index.html", Kind: "html"}, {ID: "next", Title: "Next", Source: "next.md", Path: "next.html", Kind: "markdown"}}, Assets: []string{"logo.png"}}
}

func TestHTMLCompilesSanitizedPortableContent(t *testing.T) {
	source := `<h1>Safe <em>HTML</em></h1><h2 id="stable">Stable section</h2><p><a href="next.md#target">Next</a> <img src="logo.png" alt="Logo" width="32"></p><details open><summary>Details</summary><table><tr><th scope="col">Name</th><td>Value</td></tr></table></details><script>secretScript()</script><style>body{display:none}</style><iframe src="https://example.com"></iframe><form><input name="password"></form>`
	content, err := compileHTML(t.Context(), []byte(source), 0, htmlManifest())
	require.NoError(t, err)
	assert.Contains(t, content.HTML, `<h1 id="safe-html">`)
	assert.Contains(t, content.HTML, `<h2 id="stable">`)
	assert.Contains(t, content.HTML, `href="next.html#target"`)
	assert.Contains(t, content.HTML, `src="assets/logo.png"`)
	assert.Contains(t, content.HTML, "<table>")
	assert.Contains(t, content.HTML, "<details")
	for _, forbidden := range []string{"<script", "secretScript", "<style", "display:none", "<iframe", "<form", "<input"} {
		assert.NotContains(t, content.HTML, forbidden)
	}
	require.Len(t, content.Headings, 2)
	assert.Equal(t, "Safe HTML", content.Headings[0].Title)
	assert.True(t, content.Anchors["stable"])
	require.Len(t, content.References, 1)
	assert.Equal(t, reference{page: 1, fragment: "target"}, content.References[0])
	require.Len(t, content.Blocks, 1)
	assert.Equal(t, document.BlockHTML, content.Blocks[0].Kind)
	assert.Equal(t, content.HTML, content.Blocks[0].HTML)
	assert.False(t, content.Blocks[0].Omitted)
	assert.Equal(t, "index.html", content.Blocks[0].Source.File)
	assert.NotContains(t, content.Text, "secretScript")
}

func TestHTMLRejectsOrRemovesActivePayloads(t *testing.T) {
	payloads := []string{
		`<a href="javascript:alert(1)" onclick="alert(1)">click</a>`,
		`<a href="jav&#x61;script:alert(1)">click</a>`,
		`<a href="&#x09;javascript:alert(1)">click</a>`,
		`<img src="data:image/svg+xml,<svg onload=alert(1)>">`,
		`<svg><a href="javascript:alert(1)">x</a><script>alert(1)</script></svg>`,
		`<math><mtext><table><mglyph><style><!--</style><img title="--><img src=x onerror=alert(1)>">`,
		`<div style="background:url(javascript:alert(1))" onpointerenter="alert(1)">text</div>`,
		`<iframe srcdoc="<script>alert(1)</script>"></iframe><object data="javascript:alert(1)"></object>`,
		`<template><img src=x onerror=alert(1)></template>`,
		`<a href="//example.com/private">protocol relative</a>`,
		`<img src="http://example.com/image.png">`,
		`<a href="../../private.md">escape</a>`,
	}
	for _, source := range payloads {
		t.Run(source, func(t *testing.T) {
			content, err := compileHTML(t.Context(), []byte(source), 0, htmlManifest())
			if err != nil {
				return
			}
			for _, forbidden := range []string{"<script", "<style", "<iframe", "<object", "<svg", "<math", "<template", "onerror", "onclick", "onpointerenter", "javascript:", "srcdoc=", "style=", "data:image"} {
				assert.NotContains(t, strings.ToLower(content.HTML), forbidden)
			}
		})
	}
}

func TestHTMLBudgetsAnchorsAndCancellation(t *testing.T) {
	for _, source := range []string{
		`<p id="same">one</p><h2 id="same">two</h2>`,
		`<p id="_docs-main">reserved</p>`,
		strings.Repeat("<div>", maxHTMLDepth+1) + "nested" + strings.Repeat("</div>", maxHTMLDepth+1),
		strings.Repeat("<br>", maxHTMLNodes+1),
	} {
		_, err := compileHTML(t.Context(), []byte(source), 0, htmlManifest())
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := compileHTML(ctx, []byte("<p>Cancelled</p>"), 0, htmlManifest())
	assert.ErrorIs(t, err, context.Canceled)
	content, err := compileHTML(t.Context(), []byte("<h1>Repeated</h1><h2>Repeated</h2>"), 0, htmlManifest())
	require.NoError(t, err)
	assert.True(t, content.Anchors["repeated"])
	assert.True(t, content.Anchors["repeated-1"])
}

func TestHTMLPublicationExportsSanitizedModel(t *testing.T) {
	root := t.TempDir()
	manifest := htmlManifest()
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs.json"), data, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "index.html"), []byte(`<h1>Portable HTML</h1><p onclick="bad()">Visible text</p><script>bad()</script>`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "next.md"), []byte("# Next\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "logo.png"), []byte("test image"), 0o600))
	output := filepath.Join(t.TempDir(), "site")
	err = BuildContext(t.Context(), filepath.Join(root, "docs.json"), output, Options{})
	require.NoError(t, err)
	modelBytes, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(PageDataPath(manifest.Pages[0]))))
	require.NoError(t, err)
	var model document.Page
	require.NoError(t, json.Unmarshal(modelBytes, &model))
	assert.Equal(t, "html", model.Kind)
	require.Len(t, model.Blocks, 1)
	assert.Contains(t, model.Blocks[0].HTML, "Portable HTML")
	assert.NotContains(t, model.Blocks[0].HTML, "onclick")
	assert.NotContains(t, model.Blocks[0].HTML, "<script")
}
