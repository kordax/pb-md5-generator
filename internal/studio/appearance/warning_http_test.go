package appearance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPPushWarningsPreserveSavedSettingsAndDeletedThemes(t *testing.T) {
	backend := &offlineBackend{Backend: content.NewLocalBackend()}
	value := fixtureWithStorage(t, backend)
	authentication, err := auth.Open(auth.Config{StateDir: filepath.Join(t.TempDir(), "auth"), PublicURL: "https://studio.test"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, authentication.Close()) })
	password := "a local test passphrase with sufficient length"
	_, err = authentication.Bootstrap(t.Context(), "owner", "Owner", []byte(password))
	require.NoError(t, err)
	cookie, csrf := loginHTTP(t, authentication, "owner", password)
	handler := value.service.Handler(authentication)
	settings, err := value.service.Read(t.Context())
	require.NoError(t, err)
	settings.Title = "Saved locally"
	settings.Publishing.GitPushOnPublish = true
	response := requestHTTP(t, handler, http.MethodPut, "/settings", settings, cookie, csrf)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var saved Settings
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &saved))
	assert.Equal(t, settings.Title, saved.Title)
	require.NotNil(t, saved.Sync)
	assert.True(t, saved.Sync.Retryable)
	assert.Contains(t, saved.Sync.Error, "Local commit is retained")
	response = requestHTTP(t, handler, http.MethodPost, "/themes", customTheme(), cookie, csrf)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var theme Theme
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &theme))
	require.NotNil(t, theme.Sync)
	request := httptest.NewRequest(http.MethodDelete, "https://studio.test/themes/"+theme.ID, nil)
	request.AddCookie(cookie)
	request.Header.Set("Origin", "https://studio.test")
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("If-Match", `"`+theme.Revision+`"`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var deletion struct {
		Sync *content.SyncStatus `json:"sync"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &deletion))
	require.NotNil(t, deletion.Sync)
	assert.True(t, deletion.Sync.Retryable)
	_, err = value.content.Read(t.Context(), themePath(theme.ID))
	require.ErrorIs(t, err, content.ErrNotFound)
}
