package studio

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func uploadRuntimeText(t *testing.T, client *runtimeClient, path, revision, source string) (string, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, client.url+"/_docs/api/v1/files/content?path="+url.QueryEscape(path), bytes.NewBufferString(source))
	require.NoError(t, err)
	request.AddCookie(client.cookie)
	request.Header.Set("Origin", client.origin)
	request.Header.Set("X-CSRF-Token", client.csrf)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("If-Match", `"`+revision+`"`)
	response, err := client.client.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	var saved struct {
		Draft struct {
			ID       string `json:"id"`
			Revision string `json:"revision"`
		} `json:"draft"`
	}
	require.NoError(t, json.Unmarshal(body, &saved))
	require.NotEmpty(t, saved.Draft.ID)
	return saved.Draft.ID, saved.Draft.Revision
}

func TestRuntimeChecksConsumersWhenIncludedFileChanges(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Home\n")
	root := filepath.Join(config.DataDir, "content")
	manifestPath := filepath.Join(root, "docs.json")
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest site.Manifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	// A separate readable primary has a restricted dependency. An unrelated
	// file edit must not require permission to that unchanged dependency.
	manifest.Pages = append(manifest.Pages, site.Page{ID: "restricted", Title: "Restricted dependency", Source: "public/restricted.md", Path: "public/restricted.html", Kind: "markdown"})
	data, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, data, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "public", "restricted.md"), []byte(":::include source=\"../private.md\"\n:::\n"), 0o600))
	startRuntimeForTest(t, config)
	owner := newRuntimeClient(t, config)
	owner.login(t, "owner", ownerTestPassword)
	status, _, _ := owner.request(t, http.MethodPost, "/_docs/api/v1/users", map[string]any{
		"username": "editor", "displayName": "Scoped editor", "password": "Scoped-editor-cedar-library-2026", "role": "editor", "grants": map[string]any{"read": []string{"public"}, "write": []string{"public"}},
	})
	require.Equal(t, http.StatusCreated, status)
	editor := newRuntimeClient(t, config)
	editor.login(t, "editor", "Scoped-editor-cedar-library-2026")
	status, _, data = editor.request(t, http.MethodGet, "/_docs/api/v1/documents/guide?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	var page apiweb.Document
	require.NoError(t, json.Unmarshal(data, &page))
	draftID, draftRevision := uploadRuntimeText(t, editor, "public/snippet.md", page.Revision, ":::include source=\"../private.md\"\n:::\n")
	status, _, body := editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+draftID+"/publish", map[string]string{"revision": draftRevision})
	assert.Equal(t, http.StatusForbidden, status)
	assert.NotContains(t, string(body), "Secret body")
	published, err := os.ReadFile(filepath.Join(root, "public", "snippet.md"))
	require.NoError(t, err)
	assert.Equal(t, "Public search needle.\n", string(published))
	status, _, body = editor.request(t, http.MethodGet, "/public/guide.html", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Public search needle")
	assert.NotContains(t, string(body), "Secret body")
	// Saving an independent attachment still succeeds with these grants.
	draftID, draftRevision = uploadRuntimeText(t, editor, "public/extra.txt", page.Revision, "An unrelated public attachment.\n")
	status, _, _ = editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+draftID+"/publish", map[string]string{"revision": draftRevision})
	assert.Equal(t, http.StatusOK, status)
}
