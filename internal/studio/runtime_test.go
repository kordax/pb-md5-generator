package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ownerTestPassword = "Quiet-cedar-library-pencil-2026"

type runtimeClient struct {
	url    string
	origin string
	client *http.Client
	cookie *http.Cookie
	csrf   string
}

func runtimeConfigForTest(t *testing.T) Config {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	config := DefaultConfig(filepath.Join(t.TempDir(), "data"))
	config.Listener = listener
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	config.PublicURL = "http://localhost:" + port
	return config
}

func newRuntimeClient(t *testing.T, config Config) *runtimeClient {
	t.Helper()
	transport := &http.Transport{MaxIdleConns: 4, IdleConnTimeout: time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	publicURL, err := url.Parse(config.PublicURL)
	require.NoError(t, err)
	return &runtimeClient{url: config.PublicURL, origin: publicURL.Scheme + "://" + publicURL.Host, client: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (client *runtimeClient) request(t *testing.T, method, route string, input any) (int, http.Header, []byte) {
	t.Helper()
	var data []byte
	if input != nil {
		var err error
		data, err = json.Marshal(input)
		require.NoError(t, err)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, client.url+route, bytes.NewReader(data))
	require.NoError(t, err)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Origin", client.origin)
	if client.cookie != nil {
		request.AddCookie(client.cookie)
	}
	if client.csrf != "" {
		request.Header.Set("X-CSRF-Token", client.csrf)
	}
	response, err := client.client.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response.StatusCode, response.Header, body
}

func (client *runtimeClient) login(t *testing.T, username, password string) auth.User {
	t.Helper()
	status, headers, body := client.request(t, http.MethodPost, "/_docs/api/v1/session", map[string]string{"username": username, "password": password})
	require.Equal(t, http.StatusOK, status)
	response := &http.Response{Header: headers}
	cookies := response.Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure)
	assert.True(t, cookies[0].HttpOnly)
	client.cookie = cookies[0]
	var session struct {
		User      auth.User `json:"user"`
		CSRFToken string    `json:"csrfToken"`
	}
	require.NoError(t, json.Unmarshal(body, &session))
	require.NotEmpty(t, session.CSRFToken)
	client.csrf = session.CSRFToken
	return session.User
}

func startRuntimeForTest(t *testing.T, config Config) *Server {
	t.Helper()
	server, err := Start(t.Context(), config)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, server.Close(ctx))
	})
	return server
}

func seedRuntimeSources(t *testing.T, config Config, home string) {
	t.Helper()
	root := filepath.Join(config.DataDir, "content")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "public"), 0o700))
	manifest := site.Manifest{Version: 1, Title: "CMS access", Pages: []site.Page{
		{ID: "home", Title: "Home", Source: "index.md", Path: "index.html", Kind: "markdown"},
		{ID: "guide", Title: "Public guide", Source: "public/guide.md", Path: "public/guide.html", Kind: "markdown"},
		{ID: "private", Title: "Secret navigation title", Source: "private.md", Path: "private.html", Kind: "markdown"},
	}, Assets: []string{"public/icon.png", "private.png"}}
	manifestData, err := json.Marshal(manifest)
	require.NoError(t, err)
	files := map[string][]byte{
		"docs.json":         manifestData,
		"index.md":          []byte(home),
		"public/guide.md":   []byte("# Public guide\n\n:::include source=\"snippet.md\"\n:::\n"),
		"public/snippet.md": []byte("Public search needle.\n"),
		"private.md":        []byte("# Private content\n\nSecret body that must remain private.\n"),
		"public/icon.png":   []byte("public attachment"),
		"private.png":       []byte("private attachment"),
	}
	for name, data := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), data, 0o600))
	}
}

func TestRuntimeLoginEditPublishAndShutdown(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Documentation owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Published home\n")
	server := startRuntimeForTest(t, config)
	assert.Equal(t, config.Listener.Addr().String(), server.Address().String())
	client := newRuntimeClient(t, config)
	status, _, _ := client.request(t, http.MethodGet, "/_docs/health", nil)
	assert.Equal(t, http.StatusOK, status)
	client.login(t, "owner", ownerTestPassword)
	status, _, body := client.request(t, http.MethodGet, "/index.html", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Published home")
	status, _, body = client.request(t, http.MethodGet, "/_docs/api/v1/documents/home?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	var page apiweb.Document
	require.NoError(t, json.Unmarshal(body, &page))
	status, _, body = client.request(t, http.MethodPut, "/_docs/api/v1/documents/home", map[string]any{"content": "# Edited through CMS\n\nPublished atomically.\n", "revision": page.Revision, "publish": true})
	require.Equal(t, http.StatusOK, status)
	var saved struct {
		Published bool `json:"published"`
	}
	require.NoError(t, json.Unmarshal(body, &saved))
	assert.True(t, saved.Published)
	status, _, body = client.request(t, http.MethodGet, "/index.html", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Edited through CMS")
	assert.NotContains(t, string(body), "Published home")
	status, _, body = client.request(t, http.MethodGet, "/_docs/api/v1/openapi.yaml", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "openapi: 3.1.0")
	require.NoError(t, server.Close(t.Context()))
	// A fresh service can open the same directory locks after shutdown.
	assert.ErrorIs(t, Bootstrap(t.Context(), config, "another", "Another owner", []byte(ownerTestPassword)), auth.ErrConflict)
}

func TestRuntimeReaderFiltersNavigationSearchModelsAndAssets(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Owner home\n")
	startRuntimeForTest(t, config)
	owner := newRuntimeClient(t, config)
	owner.login(t, "owner", ownerTestPassword)
	status, _, body := owner.request(t, http.MethodGet, "/private.html", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Secret body")
	status, _, body = owner.request(t, http.MethodPost, "/_docs/api/v1/users", map[string]any{
		"username": "reader", "displayName": "Scoped reader", "password": "Scoped-reader-cedar-library-2026", "role": "viewer", "grants": map[string]any{"read": []string{"public"}, "write": []string{}},
	})
	require.Equal(t, http.StatusCreated, status)
	var created auth.User
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotEmpty(t, created.ID)
	reader := newRuntimeClient(t, config)
	reader.login(t, "reader", "Scoped-reader-cedar-library-2026")
	for _, route := range []string{"/public/guide.html", "/_docs/project.json", "/_docs/search.json", "/" + site.PageDataPath(site.Page{ID: "guide"})} {
		t.Run(route, func(t *testing.T) {
			status, _, body := reader.request(t, http.MethodGet, route, nil)
			require.Equal(t, http.StatusOK, status)
			for _, hidden := range []string{"Secret body", "Secret navigation title", "private.md", "private.html", "private.png", "Owner home", "index.html"} {
				assert.NotContains(t, string(body), hidden)
			}
			if route == "/_docs/search.json" {
				assert.Contains(t, string(body), "Public search needle")
			}
		})
	}
	for _, route := range []string{"/private.html", "/index.html", "/assets/private.png", "/" + site.PageDataPath(site.Page{ID: "private"})} {
		status, _, body := reader.request(t, http.MethodGet, route, nil)
		assert.Equal(t, http.StatusNotFound, status)
		assert.NotContains(t, string(body), "Secret body")
	}
	status, headers, body := reader.request(t, http.MethodGet, "/assets/public/icon.png", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, headers.Get("Content-Disposition"), "attachment")
	assert.Equal(t, "public attachment", string(body))
	status, headers, _ = reader.request(t, http.MethodGet, "/", nil)
	assert.Equal(t, http.StatusSeeOther, status)
	assert.Equal(t, "/public/guide.html", headers.Get("Location"))
	status, _, _ = owner.request(t, http.MethodPatch, "/_docs/api/v1/users/"+created.ID, map[string]any{"grants": map[string]any{"read": []string{}, "write": []string{}}})
	require.Equal(t, http.StatusOK, status)
	status, _, _ = reader.request(t, http.MethodGet, "/public/guide.html", nil)
	assert.Equal(t, http.StatusUnauthorized, status)
	reader.login(t, "reader", "Scoped-reader-cedar-library-2026")
	status, _, _ = reader.request(t, http.MethodGet, "/public/guide.html", nil)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestRuntimeKeepsInvalidSourceRepairable(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, ":::note title=\"Broken\"\nUnclosed block\n")
	startRuntimeForTest(t, config)
	client := newRuntimeClient(t, config)
	client.login(t, "owner", ownerTestPassword)
	status, _, _ := client.request(t, http.MethodGet, "/index.html", nil)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	status, _, body := client.request(t, http.MethodGet, "/_docs/api/v1/documents/home?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	var page apiweb.Document
	require.NoError(t, json.Unmarshal(body, &page))
	status, _, _ = client.request(t, http.MethodPut, "/_docs/api/v1/documents/home", map[string]any{"content": "# Repaired\n", "revision": page.Revision, "publish": true})
	require.Equal(t, http.StatusOK, status)
	status, _, body = client.request(t, http.MethodGet, "/index.html", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Repaired")
}
