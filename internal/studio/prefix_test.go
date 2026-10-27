package studio

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeHostingPrefixAndUnsavedPreviewURLs(t *testing.T) {
	config := runtimeConfigForTest(t)
	config.PublicURL += "/manual"
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Prefix home\n")
	startRuntimeForTest(t, config)
	client := newRuntimeClient(t, config)
	client.login(t, "owner", ownerTestPassword)
	status, _, body := client.request(t, http.MethodPost, "/_docs/api/v1/preview", map[string]string{"documentId": "guide", "content": "# Unsaved preview\n\n[Home](../index.md)\n"})
	require.Equal(t, http.StatusOK, status)
	var preview apiweb.Preview
	require.NoError(t, json.Unmarshal(body, &preview))
	assert.Contains(t, preview.HTML, "Unsaved preview")
	assert.Contains(t, preview.HTML, `href="/manual/_docs/site.css"`)
	assert.Contains(t, preview.HTML, `href="/manual/index.html"`)
	assert.NotContains(t, preview.HTML, `href="../_docs/site.css"`)
	status, _, _ = client.request(t, http.MethodGet, "/_docs/site.css", nil)
	assert.Equal(t, http.StatusOK, status)
	status, _, _ = client.request(t, http.MethodPost, "/_docs/api/v1/preview", map[string]string{"documentId": "guide", "content": ":::note title=\"Broken\"\n"})
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	status, _, body = client.request(t, http.MethodGet, "/_docs/api/v1/documents/home?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	var home apiweb.Document
	require.NoError(t, json.Unmarshal(body, &home))
	status, _, body = client.request(t, http.MethodPost, "/_docs/api/v1/documents", map[string]any{"path": "new.md", "title": "New document", "kind": "markdown", "content": "# New unsaved source\n", "revision": home.Revision})
	require.Equal(t, http.StatusCreated, status)
	var created struct {
		Document apiweb.Document `json:"document"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotEmpty(t, created.Document.ID)
	status, _, body = client.request(t, http.MethodPost, "/_docs/api/v1/preview", map[string]string{"documentId": created.Document.ID, "content": "# Unsaved new document\n"})
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, json.Unmarshal(body, &preview))
	assert.Contains(t, preview.HTML, "Unsaved new document")
	assert.Contains(t, preview.HTML, `href="/manual/_docs/site.css"`)
	// A prefix deployment must not expose the same API at the host root.
	rootClient := newRuntimeClient(t, config)
	rootClient.url = strings.TrimSuffix(config.PublicURL, "/manual")
	status, _, _ = rootClient.request(t, http.MethodGet, "/_docs/health", nil)
	assert.Equal(t, http.StatusNotFound, status)
}
