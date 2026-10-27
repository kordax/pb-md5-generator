package studio

import (
	"context"
	"encoding/json"
	"net/http"
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

func TestRuntimeCannotPreviewOrPublishPrivateDependencies(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Home\n")
	root := filepath.Join(config.DataDir, "content")
	manifestPath := filepath.Join(root, "docs.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest site.Manifest
	require.NoError(t, json.Unmarshal(manifestBytes, &manifest))
	manifest.Pages = append(manifest.Pages, site.Page{ID: "api", Title: "Public API", Source: "public/api.yaml", Path: "public/api.html", Kind: "openapi"})
	manifestBytes, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, manifestBytes, 0o600))
	apiSource := "openapi: 3.1.0\ninfo: {title: Public API, version: 1.0.0}\npaths:\n  /ping:\n    get:\n      responses:\n        '200': {description: OK}\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "public", "api.yaml"), []byte(apiSource), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "private.schema.yaml"), []byte("type: object\nproperties:\n  secret: {const: private-schema-content}\n"), 0o600))
	startRuntimeForTest(t, config)
	owner := newRuntimeClient(t, config)
	owner.login(t, "owner", ownerTestPassword)
	status, _, _ := owner.request(t, http.MethodPost, "/_docs/api/v1/users", map[string]any{
		"username": "editor", "displayName": "Scoped editor", "password": "Scoped-editor-cedar-library-2026", "role": "editor", "grants": map[string]any{"read": []string{"public"}, "write": []string{"public"}},
	})
	require.Equal(t, http.StatusCreated, status)
	editor := newRuntimeClient(t, config)
	editor.login(t, "editor", "Scoped-editor-cedar-library-2026")
	cases := []struct{ id, source string }{
		{"guide", ":::include source=\"../private.md\"\n:::\n"},
		{"api", apiSource + "components:\n  schemas:\n    Hidden:\n      $ref: '../private.schema.yaml'\n"},
	}
	for _, test := range cases {
		t.Run(test.id, func(t *testing.T) {
			status, _, body := editor.request(t, http.MethodPost, "/_docs/api/v1/preview", map[string]string{"documentId": test.id, "content": test.source})
			assert.Equal(t, http.StatusForbidden, status)
			assert.NotContains(t, string(body), "Secret body")
			assert.NotContains(t, string(body), "private-schema-content")
			status, _, body = editor.request(t, http.MethodGet, "/_docs/api/v1/documents/"+test.id+"?published=true", nil)
			require.Equal(t, http.StatusOK, status)
			var document apiweb.Document
			require.NoError(t, json.Unmarshal(body, &document))
			status, _, body = editor.request(t, http.MethodPut, "/_docs/api/v1/documents/"+test.id, map[string]any{"content": test.source, "revision": document.Revision, "publish": true})
			assert.Equal(t, http.StatusForbidden, status)
			assert.NotContains(t, string(body), "Secret body")
			assert.NotContains(t, string(body), "private-schema-content")
		})
	}
	status, _, body := editor.request(t, http.MethodGet, "/public/guide.html", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Public search needle")
	assert.NotContains(t, string(body), "Secret body")
}

func TestRuntimeFailedListenReleasesServiceLocks(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	occupied := config.Listener
	config.Listener = nil
	config.Address = occupied.Addr().String()
	_, err := Start(t.Context(), config)
	require.Error(t, err)
	require.NoError(t, occupied.Close())
	server, err := Start(t.Context(), config)
	require.NoError(t, err)
	require.NoError(t, server.Close(t.Context()))
	assert.ErrorIs(t, Bootstrap(t.Context(), config, "another", "Another", []byte(ownerTestPassword)), auth.ErrConflict)
}

func TestRuntimeCancellationStopsHTTPAndReleasesLocks(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, config) }()
	client := newRuntimeClient(t, config)
	status, _, _ := client.request(t, http.MethodGet, "/_docs/health", nil)
	require.Equal(t, http.StatusOK, status)
	address := config.Listener.Addr().String()
	cancel()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("CMS did not stop after cancellation")
	}
	config.Listener, config.Address = nil, address
	server, err := Start(t.Context(), config)
	require.NoError(t, err)
	require.NoError(t, server.Close(t.Context()))
}
