package studio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCMSPreviewUsesSuppliedPropertiesForRegisteredAndUnsavedDocuments(t *testing.T) {
	config, contents, principal, _ := readViewDirectFixture(t)
	root := filepath.Join(config.DataDir, "content")
	manifestPath := filepath.Join(root, "docs.json")
	manifest, err := readProjectManifest(root, manifestPath)
	require.NoError(t, err)
	var registered site.Page
	for _, page := range manifest.Pages {
		if page.ID == "guide" {
			registered = page
		}
	}
	require.NotEmpty(t, registered.Source)
	manifestBefore, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	sourceBefore, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(registered.Source)))
	require.NoError(t, err)
	for _, document := range []apiweb.Document{
		{ID: registered.ID, Path: registered.Source, Title: "Reviewed HTML title", Kind: "html", OutputPath: "preview/deep/edited.html"},
		{ID: "new-properties-preview", Path: "public/not-yet-saved.txt", Title: "Unsaved HTML title", Kind: "html", OutputPath: "preview/deep/new.html"},
	} {
		t.Run(document.ID, func(t *testing.T) {
			result, err := previewDocument(principal, contents, document, []byte("<h1>Property preview heading</h1><p>**kept literal**</p>"))
			require.NoError(t, err)
			assert.Contains(t, result.HTML, "<title>"+document.Title)
			assert.Contains(t, result.HTML, "Property preview heading")
			assert.Contains(t, result.HTML, "**kept literal**")
			assert.Contains(t, result.HTML, "../../index.html", "navigation must resolve relative to the reviewed publication URL")
			assert.Empty(t, result.Diagnostics)
		})
	}
	manifestAfter, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	assert.Equal(t, manifestBefore, manifestAfter)
	sourceAfter, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(registered.Source)))
	require.NoError(t, err)
	assert.Equal(t, sourceBefore, sourceAfter)
	_, err = os.Lstat(filepath.Join(root, "public", "not-yet-saved.txt"))
	assert.True(t, os.IsNotExist(err), "preview must not create an unsaved source")
	drafts, err := contents.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
}

func TestCMSPreviewPropertyOverridesKeepSourceACLAndHTMLSafety(t *testing.T) {
	config, contents, principal, owner := readViewDirectFixture(t)
	root := filepath.Join(config.DataDir, "content")
	manifest, err := readProjectManifest(root, filepath.Join(root, "docs.json"))
	require.NoError(t, err)
	var public, private site.Page
	for _, page := range manifest.Pages {
		if page.ID == "guide" {
			public = page
		}
		if page.ID == "private" {
			private = page
		}
	}
	require.NotEmpty(t, public.Source)
	require.NotEmpty(t, private.Source)
	document := apiweb.Document{ID: public.ID, Path: public.Source, Title: "Scoped property preview", Kind: "html", OutputPath: "preview/scoped.html"}
	ownerPreview, err := previewDocument(principal, contents, document, []byte("<p>Public preview</p>"))
	require.NoError(t, err)
	require.Contains(t, ownerPreview.HTML, private.Path, "fixture must include private navigation for the owner")
	limited := *owner
	limited.Role = auth.RoleViewer
	require.NoError(t, json.Unmarshal([]byte(`{"grants":{"read":["public"],"write":[]}}`), &limited))
	scoped := readViewPrincipalContext{Context: principal, resolve: func(context.Context) (*auth.User, error) { return &limited, nil }}
	preview, err := previewDocument(scoped, contents, document, []byte("<p>Public preview</p>"))
	require.NoError(t, err)
	assert.Contains(t, preview.HTML, "Public preview")
	assert.NotContains(t, preview.HTML, private.Path)
	document.ID, document.Path = private.ID, private.Source
	denied, err := previewDocument(scoped, contents, document, []byte("<p>Denied supplied content</p>"))
	assert.ErrorIs(t, err, auth.ErrForbidden)
	assert.Empty(t, denied.HTML)
	document.ID, document.Path = public.ID, public.Source
	unsafe, err := previewDocument(principal, contents, document, []byte(`<p>Safe text</p><script>PROPERTY_SCRIPT_SENTINEL()</script><a href="javascript:PROPERTY_URL_SENTINEL()">unsafe</a>`))
	if err != nil {
		assert.ErrorIs(t, err, content.ErrInvalid)
	} else {
		assert.NotContains(t, unsafe.HTML, "PROPERTY_SCRIPT_SENTINEL")
		assert.NotContains(t, unsafe.HTML, "javascript:")
	}
	document.OutputPath = "../escape.html"
	invalid, err := previewDocument(principal, contents, document, []byte("<p>Safe text</p>"))
	assert.ErrorIs(t, err, content.ErrInvalid)
	assert.Empty(t, invalid.HTML)
}
