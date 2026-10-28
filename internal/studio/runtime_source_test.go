package studio

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runtimeSourceResult struct {
	Revision  string `json:"revision"`
	Published bool   `json:"published"`
	Draft     struct {
		ID           string `json:"id"`
		Revision     string `json:"revision"`
		BaseRevision string `json:"baseRevision"`
		Path         string `json:"path"`
	} `json:"draft"`
}

func runtimeSourceRevision(t *testing.T, client *runtimeClient) string {
	t.Helper()
	status, _, body := client.request(t, http.MethodGet, "/_docs/api/v1/documents?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	var result struct {
		Revision string `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	require.NotEmpty(t, result.Revision)
	return result.Revision
}

func runtimeSourceRequest(t *testing.T, client *runtimeClient, method, route string, data []byte, revision, draftID, draftRevision string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, client.url+route, bytes.NewReader(data))
	require.NoError(t, err)
	request.Header.Set("Origin", client.origin)
	request.Header.Set("X-CSRF-Token", client.csrf)
	request.Header.Set("Content-Type", "application/octet-stream")
	if revision != "" {
		request.Header.Set("If-Match", `"`+revision+`"`)
	}
	if draftID != "" {
		request.Header.Set("X-Draft-ID", draftID)
	}
	if draftRevision != "" {
		request.Header.Set("X-Draft-Revision", draftRevision)
	}
	request.AddCookie(client.cookie)
	response, err := client.client.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response.StatusCode, body
}

func TestRuntimeSourceRebaseAndDeletedPathHistoryRemainExplicitDrafts(t *testing.T) {
	config, owner, _ := navigationRuntimeFixture(t)
	editor := navigationScopedClient(t, owner, config, "history-source-editor", "editor", []string{"public"}, []string{"public"})
	viewer := navigationScopedClient(t, owner, config, "history-source-viewer", "viewer", []string{"public"}, []string{})
	const path = "public/example.js"
	const route = "/_docs/api/v1/files/content?path=" + path
	base := runtimeSourceRevision(t, editor)
	initial := []byte("\ufeffexport const value = 1;\r\n")
	status, body := runtimeSourceRequest(t, editor, http.MethodPut, route, initial, base, "", "")
	require.Equal(t, http.StatusOK, status)
	var saved runtimeSourceResult
	require.NoError(t, json.Unmarshal(body, &saved))
	require.NotEmpty(t, saved.Draft.ID)
	status, _, body = owner.request(t, http.MethodPut, "/_docs/api/v1/documents/home", map[string]string{"revision": base, "content": "# New independent home\n"})
	require.Equal(t, http.StatusOK, status)
	var home runtimeSourceResult
	require.NoError(t, json.Unmarshal(body, &home))
	status, _, _ = owner.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+home.Draft.ID+"/publish", map[string]string{"revision": home.Draft.Revision})
	require.Equal(t, http.StatusOK, status)
	latest := runtimeSourceRevision(t, editor)
	require.NotEqual(t, base, latest)
	status, _ = runtimeSourceRequest(t, editor, http.MethodPost, "/_docs/api/v1/files/rebase?path="+path, initial, base, saved.Draft.ID, saved.Draft.Revision)
	assert.Equal(t, http.StatusConflict, status)
	merged := append(append([]byte(nil), initial...), []byte("export const reviewed = true;\r\n")...)
	status, body = runtimeSourceRequest(t, editor, http.MethodPost, "/_docs/api/v1/files/rebase?path="+path, merged, latest, saved.Draft.ID, saved.Draft.Revision)
	require.Equal(t, http.StatusOK, status)
	var rebased runtimeSourceResult
	require.NoError(t, json.Unmarshal(body, &rebased))
	assert.False(t, rebased.Published)
	assert.Equal(t, saved.Draft.ID, rebased.Draft.ID)
	assert.Equal(t, latest, rebased.Revision)
	assert.Equal(t, latest, rebased.Draft.BaseRevision)
	status, _, _ = editor.request(t, http.MethodGet, route, nil)
	assert.Equal(t, http.StatusNotFound, status, "rebase must not create the published file")
	status, _, _ = editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+rebased.Draft.ID+"/publish", map[string]string{"revision": rebased.Draft.Revision})
	require.Equal(t, http.StatusOK, status)
	status, _, body = editor.request(t, http.MethodGet, route, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, merged, body)

	status, _, body = editor.request(t, http.MethodGet, "/_docs/api/v1/files/history?path="+path, nil)
	require.Equal(t, http.StatusOK, status)
	var history struct {
		Path      string `json:"path"`
		Revision  string `json:"revision"`
		Revisions []struct {
			ID         string `json:"id"`
			Path       string `json:"path"`
			Deleted    bool   `json:"deleted"`
			Restorable bool   `json:"restorable"`
		} `json:"revisions"`
	}
	require.NoError(t, json.Unmarshal(body, &history))
	assert.Equal(t, path, history.Path)
	var historyID string
	for _, item := range history.Revisions {
		assert.Equal(t, path, item.Path)
		if !item.Deleted {
			historyID = item.ID
			assert.True(t, item.Restorable)
		}
	}
	require.NotEmpty(t, historyID)
	for _, hidden := range []string{"branch", "email", "documentId", "private.md"} {
		assert.NotContains(t, string(body), hidden)
	}
	status, body = navigationRawRequest(t, editor, http.MethodDelete, route, "", nil, history.Revision)
	require.Equal(t, http.StatusOK, status)
	var deletion runtimeSourceResult
	require.NoError(t, json.Unmarshal(body, &deletion))
	status, _, _ = editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+deletion.Draft.ID+"/publish", map[string]string{"revision": deletion.Draft.Revision})
	require.Equal(t, http.StatusOK, status)
	status, _, body = viewer.request(t, http.MethodGet, "/_docs/api/v1/files/history?path="+path, nil)
	require.Equal(t, http.StatusOK, status, "read-only users may inspect permitted deleted-path history")
	require.NoError(t, json.Unmarshal(body, &history))
	for _, item := range history.Revisions {
		assert.False(t, item.Restorable)
	}
	status, _, body = editor.request(t, http.MethodPost, "/_docs/api/v1/files/restore", map[string]string{"path": path, "historyId": historyID, "revision": history.Revision})
	require.Equal(t, http.StatusOK, status)
	var restored runtimeSourceResult
	require.NoError(t, json.Unmarshal(body, &restored))
	assert.False(t, restored.Published)
	require.NotEmpty(t, restored.Draft.ID)
	assert.Equal(t, path, restored.Draft.Path)
	status, _, _ = editor.request(t, http.MethodGet, route, nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, _, _ = editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+restored.Draft.ID+"/publish", map[string]string{"revision": restored.Draft.Revision})
	require.Equal(t, http.StatusOK, status)
	status, _, body = editor.request(t, http.MethodGet, route, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, merged, body)
}

func TestRuntimeSourceRebaseCanStageNewEmptyBufferButNeverBypassesRoles(t *testing.T) {
	config, owner, _ := navigationRuntimeFixture(t)
	editor := navigationScopedClient(t, owner, config, "new-empty-source-editor", "editor", []string{"public"}, []string{"public"})
	viewer := navigationScopedClient(t, owner, config, "new-empty-source-viewer", "viewer", []string{"public"}, []string{})
	base := runtimeSourceRevision(t, editor)
	const route = "/_docs/api/v1/files/rebase?path=public/empty.txt"
	status, _ := runtimeSourceRequest(t, viewer, http.MethodPost, route, nil, base, "", "")
	assert.Equal(t, http.StatusForbidden, status)
	status, body := runtimeSourceRequest(t, editor, http.MethodPost, route, nil, base, "", "")
	require.Equal(t, http.StatusOK, status)
	var result runtimeSourceResult
	require.NoError(t, json.Unmarshal(body, &result))
	assert.False(t, result.Published)
	require.NotEmpty(t, result.Draft.ID)
	status, _, _ = editor.request(t, http.MethodGet, "/_docs/api/v1/files/history?path=private.md", nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, _, _ = editor.request(t, http.MethodPost, "/_docs/api/v1/files/restore", map[string]string{"path": "public/empty.txt", "historyId": result.Draft.ID, "revision": base, "publish": "true"})
	assert.Equal(t, http.StatusBadRequest, status, "restore rejects unknown publication fields")
}
