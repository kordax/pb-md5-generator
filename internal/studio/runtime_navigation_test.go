package studio

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func navigationRuntimeFixture(t *testing.T) (Config, *runtimeClient, *document.NavigationTree) {
	t.Helper()
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Navigation owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Navigation home\n")
	root := filepath.Join(config.DataDir, "content")
	manifest, err := readProjectManifest(root, filepath.Join(root, "docs.json"))
	require.NoError(t, err)
	collapsed := true
	tree := &document.NavigationTree{UnlistedPages: "omit", Items: []document.NavigationNode{
		{Kind: "group", ID: "public-guides", Title: "Public docs", Description: "Public group summary", Collapsed: &collapsed, Children: []document.NavigationNode{
			{Kind: "page", PageID: "guide", Title: "Friendly guide"}, {Kind: "page", PageID: "home"},
		}},
		{Kind: "group", ID: "private-navigation", Title: "Hidden group title", Description: "Hidden group description", Children: []document.NavigationNode{
			{Kind: "page", PageID: "private", Title: "Hidden leaf"},
		}},
	}}
	manifest.Navigation = &document.NavigationSettings{ShowTOC: true, ShowPreviousNext: true, StickyHeader: true, Tree: tree}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs.json"), data, 0o600))
	startRuntimeForTest(t, config)
	owner := newRuntimeClient(t, config)
	owner.login(t, "owner", ownerTestPassword)
	return config, owner, tree
}

func navigationScopedClient(t *testing.T, owner *runtimeClient, config Config, username, role string, read, write []string) *runtimeClient {
	t.Helper()
	status, _, _ := owner.request(t, http.MethodPost, "/_docs/api/v1/users", map[string]any{
		"username": username, "displayName": username, "password": ownerTestPassword, "role": role,
		"grants": map[string]any{"read": read, "write": write},
	})
	require.Equal(t, http.StatusCreated, status)
	client := newRuntimeClient(t, config)
	client.login(t, username, ownerTestPassword)
	return client
}

func navigationRawRequest(t *testing.T, client *runtimeClient, method, route, media string, data []byte, revision string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, client.url+route, bytes.NewReader(data))
	require.NoError(t, err)
	request.Header.Set("Origin", client.origin)
	request.Header.Set("X-CSRF-Token", client.csrf)
	if media != "" {
		request.Header.Set("Content-Type", media)
	}
	if revision != "" {
		request.Header.Set("If-Match", `"`+revision+`"`)
	}
	request.AddCookie(client.cookie)
	response, err := client.client.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response.StatusCode, body
}

func TestRuntimeNavigationFiltersPrivateGroupsFromReaderModelsAndPreview(t *testing.T) {
	config, owner, _ := navigationRuntimeFixture(t)
	editor := navigationScopedClient(t, owner, config, "public-editor", "editor", []string{"public"}, []string{"public"})
	for _, route := range []string{"/public/guide.html", "/_docs/project.json", "/_docs/search.json"} {
		status, _, body := editor.request(t, http.MethodGet, route, nil)
		require.Equal(t, http.StatusOK, status, route)
		for _, hidden := range []string{"Hidden group title", "Hidden group description", "Hidden leaf", "private-navigation", "private.md", "private.html", "index.html"} {
			assert.NotContains(t, string(body), hidden, route)
		}
		if route != "/_docs/search.json" {
			assert.Contains(t, string(body), "Public docs")
			assert.Contains(t, string(body), "Friendly guide")
		}
	}
	status, _, body := editor.request(t, http.MethodPost, "/_docs/api/v1/preview", map[string]string{"documentId": "guide", "content": "# Previewed guide\n"})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Previewed guide")
	assert.Contains(t, string(body), "Public docs")
	assert.NotContains(t, string(body), "Hidden group")
	assert.NotContains(t, string(body), "private-navigation")
	status, _, body = owner.request(t, http.MethodGet, "/_docs/project.json", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Hidden group title", "filtering a scoped response must not mutate the shared source tree")
}

func TestRuntimeNavigationScopedSettingsPreserveHiddenTreeAndRejectExplicitEdits(t *testing.T) {
	config, owner, expectedTree := navigationRuntimeFixture(t)
	scoped := navigationScopedClient(t, owner, config, "settings-admin", "admin", []string{"docs.json", "public"}, []string{"docs.json", "public"})
	status, _, body := scoped.request(t, http.MethodGet, "/_docs/api/v1/settings", nil)
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, string(body), "Hidden group")
	var settings map[string]any
	require.NoError(t, json.Unmarshal(body, &settings))
	navigation, ok := settings["navigation"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, navigation, "tree")
	delete(settings, "sync")
	settings["title"] = "Scoped title update"
	status, _, body = scoped.request(t, http.MethodPut, "/_docs/api/v1/settings", settings)
	require.Equal(t, http.StatusOK, status, "ordinary settings save: %s", body)
	assert.NotContains(t, string(body), "Hidden group")
	var saved map[string]any
	require.NoError(t, json.Unmarshal(body, &saved))
	settings["revision"] = saved["revision"]
	navigation["tree"] = nil
	status, _, _ = scoped.request(t, http.MethodPut, "/_docs/api/v1/settings", settings)
	assert.Equal(t, http.StatusForbidden, status, "explicit reset requires root access")
	navigation["tree"] = expectedTree
	status, _, _ = scoped.request(t, http.MethodPut, "/_docs/api/v1/settings", settings)
	assert.Equal(t, http.StatusForbidden, status, "supplying an unchanged full tree still requires root access")
	status, _, body = owner.request(t, http.MethodGet, "/_docs/api/v1/settings", nil)
	require.Equal(t, http.StatusOK, status)
	var ownerSettings struct {
		Title      string                      `json:"title"`
		Navigation document.NavigationSettings `json:"navigation"`
	}
	require.NoError(t, json.Unmarshal(body, &ownerSettings))
	assert.Equal(t, "Scoped title update", ownerSettings.Title)
	assert.Equal(t, expectedTree, ownerSettings.Navigation.Tree)

	// The same restriction applies when a privileged source file is edited
	// directly, without using the dedicated settings route.
	status, _, data := scoped.request(t, http.MethodGet, "/_docs/api/v1/files/content?path=docs.json", nil)
	require.Equal(t, http.StatusOK, status)
	var manifest site.Manifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	manifest.Navigation.Tree.Items[0].Title = "Unauthorized navigation change"
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	status, _, body = scoped.request(t, http.MethodGet, "/_docs/api/v1/documents", nil)
	require.Equal(t, http.StatusOK, status)
	var project struct {
		Revision string `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(body, &project))
	status, body = navigationRawRequest(t, scoped, http.MethodPut, "/_docs/api/v1/files/content?path=docs.json", "application/octet-stream", data, project.Revision)
	require.Equal(t, http.StatusOK, status)
	var staged struct {
		Draft content.Draft `json:"draft"`
	}
	require.NoError(t, json.Unmarshal(body, &staged))
	require.NotEmpty(t, staged.Draft.ID)
	status, _, _ = scoped.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+staged.Draft.ID+"/publish", map[string]string{"revision": staged.Draft.Revision})
	assert.Equal(t, http.StatusForbidden, status)
	status, _, body = owner.request(t, http.MethodGet, "/_docs/project.json", nil)
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, string(body), "Unauthorized navigation change")
	assert.Contains(t, string(body), "Public docs")
}

func TestRuntimeNavigationEditorDeletionPrunesOnlyItsPageAndKeepsExplicitEmptyProjection(t *testing.T) {
	config, owner, _ := navigationRuntimeFixture(t)
	editor := navigationScopedClient(t, owner, config, "deleting-editor", "editor", []string{"public"}, []string{"public"})
	status, _, body := editor.request(t, http.MethodGet, "/_docs/api/v1/documents/guide?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	var page struct {
		Revision string `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(body, &page))
	status, body = navigationRawRequest(t, editor, http.MethodDelete, "/_docs/api/v1/documents/guide", "", nil, page.Revision)
	require.Equal(t, http.StatusOK, status, "stage authorized deletion: %s", body)
	var saved struct {
		Draft content.Draft `json:"draft"`
	}
	require.NoError(t, json.Unmarshal(body, &saved))
	require.NotEmpty(t, saved.Draft.ID)
	status, _, body = editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+saved.Draft.ID+"/publish", map[string]string{"revision": saved.Draft.Revision})
	require.Equal(t, http.StatusOK, status, "publish authorized deletion: %s", body)
	status, _, body = editor.request(t, http.MethodGet, "/_docs/project.json", nil)
	require.Equal(t, http.StatusOK, status)
	var projected struct {
		Pages      []json.RawMessage           `json:"pages"`
		Navigation document.NavigationSettings `json:"navigation"`
	}
	require.NoError(t, json.Unmarshal(body, &projected))
	assert.Empty(t, projected.Pages)
	require.NotNil(t, projected.Navigation.Tree, "an explicit custom tree must not become legacy flat navigation after pruning")
	assert.Equal(t, "omit", projected.Navigation.Tree.UnlistedPages)
	assert.NotNil(t, projected.Navigation.Tree.Items)
	assert.Empty(t, projected.Navigation.Tree.Items)
	assert.NotContains(t, string(body), "Hidden group")
	status, _, body = owner.request(t, http.MethodGet, "/_docs/project.json", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Hidden group title")
	assert.NotContains(t, string(body), "Friendly guide")
}
