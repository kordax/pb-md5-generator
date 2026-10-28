package appearance

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requestHTTP(t *testing.T, handler http.Handler, method, target string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if text, ok := body.(string); ok {
		data = []byte(text)
	} else if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, "https://studio.test"+target, bytes.NewReader(data))
	request.Header.Set("Origin", "https://studio.test")
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func loginHTTP(t *testing.T, authentication *auth.Service, username, password string) (*http.Cookie, string) {
	t.Helper()
	response := requestHTTP(t, authentication.Handler(), http.MethodPost, "/session", map[string]string{"username": username, "password": password}, nil, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &session))
	require.NotEmpty(t, session.CSRFToken)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 1)
	return cookies[0], session.CSRFToken
}

func TestSettingsHTTPAuthenticationRightsCSRFAndStrictRequests(t *testing.T) {
	value := newFixture(t)
	authentication, err := auth.Open(auth.Config{StateDir: filepath.Join(t.TempDir(), "auth"), PublicURL: "https://studio.test"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, authentication.Close()) })
	password := "local test passphrase with several words"
	_, err = authentication.Bootstrap(t.Context(), "owner", "Owner", []byte(password))
	require.NoError(t, err)
	cookie, csrf := loginHTTP(t, authentication, "owner", password)
	handler := value.service.Handler(authentication)

	response := requestHTTP(t, handler, http.MethodGet, "/settings", nil, nil, "")
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	response = requestHTTP(t, handler, http.MethodGet, "/settings", nil, cookie, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	var settings Settings
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &settings))
	assert.Equal(t, `"`+settings.Revision+`"`, response.Header().Get("ETag"))
	settings.Title = "HTTP managed handbook"
	response = requestHTTP(t, handler, http.MethodPut, "/settings", settings, cookie, "")
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = requestHTTP(t, handler, http.MethodPut, "/settings", settings, cookie, csrf)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = requestHTTP(t, handler, http.MethodPut, "/settings", settings, cookie, csrf)
	assert.Equal(t, http.StatusConflict, response.Code)

	created := requestHTTP(t, authentication.Handler(), http.MethodPost, "/users", auth.CreateUserRequest{Username: "reader", DisplayName: "Reader", Password: password, Role: auth.RoleViewer}, cookie, csrf)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	readerCookie, readerCSRF := loginHTTP(t, authentication, "reader", password)
	response = requestHTTP(t, handler, http.MethodGet, "/settings", nil, readerCookie, "")
	assert.Equal(t, http.StatusOK, response.Code)
	response = requestHTTP(t, handler, http.MethodPut, "/settings", settings, readerCookie, readerCSRF)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = requestHTTP(t, handler, http.MethodPost, "/themes", customTheme(), readerCookie, readerCSRF)
	assert.Equal(t, http.StatusForbidden, response.Code)

	for _, target := range []string{"/settings?unexpected=1", "/themes?unexpected=1"} {
		response = requestHTTP(t, handler, http.MethodGet, target, nil, cookie, "")
		assert.Equal(t, http.StatusBadRequest, response.Code)
	}
	for _, target := range []string{"/missing", "/themes/a/b"} {
		response = requestHTTP(t, handler, http.MethodGet, target, nil, cookie, "")
		assert.Equal(t, http.StatusNotFound, response.Code)
	}
	response = requestHTTP(t, handler, http.MethodPost, "/settings", nil, cookie, csrf)
	assert.Equal(t, http.StatusMethodNotAllowed, response.Code)
	assert.Equal(t, "GET, PUT", response.Header().Get("Allow"))
	for _, body := range []string{`{"revision":"a","revision":"b"}`, `{"Revision":"a"}`, `{"theme":null}`, `{"unknown":true}`, `[]`} {
		response = requestHTTP(t, handler, http.MethodPut, "/settings", body, cookie, csrf)
		assert.Equal(t, http.StatusBadRequest, response.Code, body)
	}
	response = requestHTTP(t, handler, http.MethodPut, "/settings", `{"title":"`+strings.Repeat("a", MaxSettingsBytes+1)+`"}`, cookie, csrf)
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	request := httptest.NewRequest(http.MethodPut, "https://studio.test/settings", strings.NewReader(`{}`))
	request.AddCookie(cookie)
	request.Header.Set("Origin", "https://studio.test")
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Content-Type", "text/plain")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnsupportedMediaType, response.Code)
	request.Header.Set("Origin", "https://foreign.test")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = requestHTTP(t, value.service.Handler(nil), http.MethodGet, "/settings", nil, nil, "")
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func TestThemeHTTPCreateUpdateDeleteAndFailedSettingsPublication(t *testing.T) {
	value := newFixture(t)
	authentication, err := auth.Open(auth.Config{StateDir: filepath.Join(t.TempDir(), "auth"), PublicURL: "https://studio.test"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, authentication.Close()) })
	password := "another local test passphrase for the owner"
	_, err = authentication.Bootstrap(t.Context(), "owner", "Owner", []byte(password))
	require.NoError(t, err)
	cookie, csrf := loginHTTP(t, authentication, "owner", password)
	handler := value.service.Handler(authentication)
	response := requestHTTP(t, handler, http.MethodGet, "/themes", nil, cookie, "")
	require.Equal(t, http.StatusOK, response.Code)
	var installed struct {
		Themes []Theme `json:"themes"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &installed))
	assert.Len(t, installed.Themes, 3)
	assert.True(t, installed.Themes[0].Builtin)
	response = requestHTTP(t, handler, http.MethodPost, "/themes", customTheme(), cookie, csrf)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var theme Theme
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &theme))
	assert.Equal(t, customTheme().ID, theme.ID)
	assert.NotEmpty(t, theme.Revision)
	response = requestHTTP(t, handler, http.MethodDelete, "/themes/"+theme.ID, nil, cookie, csrf)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	request := httptest.NewRequest(http.MethodDelete, "https://studio.test/themes/"+theme.ID, nil)
	request.AddCookie(cookie)
	request.Header.Set("Origin", "https://studio.test")
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("If-Match", `"`+theme.Revision+`"`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	response = requestHTTP(t, handler, http.MethodGet, "/themes/"+theme.ID, nil, cookie, "")
	assert.Equal(t, http.StatusMethodNotAllowed, response.Code)
	assert.Equal(t, "DELETE", response.Header().Get("Allow"))
	response = requestHTTP(t, handler, http.MethodPut, "/themes", nil, cookie, csrf)
	assert.Equal(t, http.StatusMethodNotAllowed, response.Code)
	response = requestHTTP(t, handler, http.MethodPost, "/themes", `{"id":"invalid","accent":"red"}`, cookie, csrf)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	settings, err := value.service.Read(t.Context())
	require.NoError(t, err)
	settings.Title = "Failed settings remain a draft"
	value.fail.Store(true)
	response = requestHTTP(t, handler, http.MethodPut, "/settings", settings, cookie, csrf)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "compilation failed")
	drafts, err := value.content.Drafts(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, drafts, "rejected compilation retains the settings draft")
}
