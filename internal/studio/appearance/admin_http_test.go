package appearance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func themePayload(t *testing.T, theme document.Theme, revision string) map[string]any {
	t.Helper()
	data, err := json.Marshal(theme)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	if revision != "" {
		result["revision"] = revision
	}
	return result
}

func TestThemeHTTPHonorsIndividualSourceGrantsAndRevisionGuards(t *testing.T) {
	value := newFixture(t)
	authentication, err := auth.Open(auth.Config{StateDir: filepath.Join(t.TempDir(), "auth"), PublicURL: "https://studio.test"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, authentication.Close()) })
	password := "another local passphrase for source-grant tests"
	_, err = authentication.Bootstrap(t.Context(), "owner", "Owner", []byte(password))
	require.NoError(t, err)
	ownerCookie, ownerCSRF := loginHTTP(t, authentication, "owner", password)
	response := requestHTTP(t, authentication.Handler(), http.MethodPost, "/users", auth.CreateUserRequest{Username: "theme-admin", DisplayName: "Theme administrator", Password: password, Role: auth.RoleAdmin, Grants: &auth.Grants{Read: []string{"."}, Write: []string{"docs.json", "themes/orchid.json"}}}, ownerCookie, ownerCSRF)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	cookie, csrf := loginHTTP(t, authentication, "theme-admin", password)
	handler := value.service.Handler(authentication)
	theme := customTheme()
	theme.ID = "forbidden"
	response = requestHTTP(t, handler, http.MethodPost, "/themes", theme, cookie, csrf)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = requestHTTP(t, handler, http.MethodDelete, "/themes/forbidden", nil, cookie, csrf)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = requestHTTP(t, handler, http.MethodPost, "/themes", customTheme(), cookie, csrf)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var created Theme
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	updated := created.Theme
	updated.Name, updated.Accent = "Orchid revised", "#86198f"
	payload := themePayload(t, updated, created.Revision)
	response = requestHTTP(t, handler, http.MethodPost, "/themes", payload, cookie, csrf)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var saved Theme
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &saved))
	assert.Equal(t, updated.Name, saved.Name)
	assert.NotEqual(t, created.Revision, saved.Revision)
	response = requestHTTP(t, handler, http.MethodPost, "/themes", payload, cookie, csrf)
	assert.Equal(t, http.StatusConflict, response.Code)

	for _, body := range []string{`{"id":"orchid","id":"other"}`, `{"id":"orchid","script":"alert(1)"}`, `{"light":null}`} {
		response = requestHTTP(t, handler, http.MethodPost, "/themes", body, cookie, csrf)
		assert.Equal(t, http.StatusBadRequest, response.Code)
	}
	response = requestHTTP(t, handler, http.MethodPost, "/themes", `{"name":"`+strings.Repeat("a", maxThemeBytes)+`"}`, cookie, csrf)
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	request := httptest.NewRequest(http.MethodDelete, "https://studio.test/themes/"+saved.ID, nil)
	request.AddCookie(cookie)
	request.Header.Set("Origin", "https://studio.test")
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("If-Match", `"`+saved.Revision+`"`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotFound, response.Code)
	_, err = value.content.Read(t.Context(), themePath(saved.ID))
	require.ErrorIs(t, err, content.ErrNotFound)

	// A malformed registry cannot replace valid publication and does not stop
	// general settings from remaining available for administrative repair.
	require.NoError(t, os.MkdirAll(filepath.Join(value.config.Root, "themes"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(value.config.Root, "themes", "orchid.json"), []byte(`{"id":"orchid","font":"remote-script"}`), 0o600))
	response = requestHTTP(t, handler, http.MethodGet, "/themes", nil, cookie, "")
	assert.Equal(t, http.StatusBadRequest, response.Code)
	response = requestHTTP(t, handler, http.MethodGet, "/settings", nil, cookie, "")
	assert.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, os.WriteFile(filepath.Join(value.config.Root, "themes", "orchid.json"), []byte(strings.Repeat("x", maxThemeBytes+1)), 0o600))
	response = requestHTTP(t, handler, http.MethodGet, "/themes", nil, cookie, "")
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	request = httptest.NewRequest(http.MethodGet, "https://studio.test/settings", nil).WithContext(canceled)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusRequestTimeout, response.Code)
}
