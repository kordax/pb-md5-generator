package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testHTTPService(t *testing.T) (*Service, *testClock, *httptest.Server, *http.Client) {
	t.Helper()
	return testHTTPServiceWithConfig(t, nil)
}

func testHTTPServiceWithConfig(t *testing.T, alter func(*Config)) (*Service, *testClock, *httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	config := testConfig(t)
	config.PublicURL = "https://" + server.Listener.Addr().String()
	if alter != nil {
		alter(&config)
	}
	clock := newTestClock()
	service, err := openService(config, runtimeOptions{iterations: 2, now: clock.now, derive: derivePassword})
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Close() })
	server.Config.Handler = service.Handler()
	server.StartTLS()
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := server.Client()
	client.Jar, client.Timeout = jar, 5*time.Second
	return service, clock, server, client
}

func callHTTP(t *testing.T, client *http.Client, origin, method, resource string, input any, csrf string) (*http.Response, []byte) {
	t.Helper()
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		require.NoError(t, err)
	}
	request, err := http.NewRequest(method, origin+resource, bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	require.NoError(t, err)
	return response, data
}

func loginHTTP(t *testing.T, client *http.Client, origin, username string) SessionResponse {
	t.Helper()
	response, data := callHTTP(t, client, origin, http.MethodPost, "/session", map[string]string{"username": username, "password": testPassword}, "")
	require.Equal(t, http.StatusOK, response.StatusCode, string(data))
	var session SessionResponse
	require.NoError(t, json.Unmarshal(data, &session))
	require.NotNil(t, session.User)
	require.NotEmpty(t, session.CSRFToken)
	return session
}

func TestRealTLSSessionCookiesCSRFAndUserManagement(t *testing.T) {
	service, _, server, client := testHTTPService(t)
	response, body := callHTTP(t, client, server.URL, http.MethodGet, "/session", nil, "")
	require.Equal(t, http.StatusOK, response.StatusCode)
	assert.JSONEq(t, `{"user":null,"setupRequired":true}`, string(body))
	assert.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	assert.Equal(t, "nosniff", response.Header.Get("X-Content-Type-Options"))
	admin := testAdmin(t, service)
	response, body = callHTTP(t, client, server.URL, http.MethodPost, "/session", map[string]string{"username": "admin", "password": testPassword}, "")
	require.Equal(t, http.StatusOK, response.StatusCode, string(body))
	cookies := response.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "__Host-docs_session", cookies[0].Name)
	assert.Equal(t, "/", cookies[0].Path)
	assert.Empty(t, cookies[0].Domain)
	assert.True(t, cookies[0].HttpOnly)
	assert.True(t, cookies[0].Secure)
	assert.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite)
	assert.Greater(t, cookies[0].MaxAge, 0)
	var session SessionResponse
	require.NoError(t, json.Unmarshal(body, &session))
	require.NotNil(t, session.User)
	assert.Equal(t, admin.ID, session.User.ID)
	assert.NotContains(t, string(body), "password")
	assert.NotContains(t, string(body), "hash")
	assert.NotContains(t, string(body), "version")
	assert.Empty(t, response.Header.Get("Access-Control-Allow-Origin"))
	response, body = callHTTP(t, client, server.URL, http.MethodPost, "/users", CreateUserRequest{Username: "viewer", Password: testPassword, Role: RoleViewer}, "")
	assert.Equal(t, http.StatusForbidden, response.StatusCode, string(body))
	assert.Contains(t, string(body), "csrf_failed")
	response, body = callHTTP(t, client, server.URL, http.MethodPost, "/users", CreateUserRequest{Username: "viewer", Password: testPassword, Role: RoleViewer}, session.CSRFToken)
	require.Equal(t, http.StatusCreated, response.StatusCode, string(body))
	var viewer User
	require.NoError(t, json.Unmarshal(body, &viewer))
	assert.Equal(t, RoleViewer, viewer.Role)
	response, body = callHTTP(t, client, server.URL, http.MethodGet, "/users", nil, "")
	require.Equal(t, http.StatusOK, response.StatusCode, string(body))
	var list struct {
		Users []User `json:"users"`
	}
	require.NoError(t, json.Unmarshal(body, &list))
	assert.Len(t, list.Users, 2)
	response, body = callHTTP(t, client, server.URL, http.MethodPatch, "/users/"+viewer.ID, map[string]any{"displayName": "Documentation reader", "grants": map[string]any{"read": []string{"docs"}, "write": []string{}}}, session.CSRFToken)
	require.Equal(t, http.StatusOK, response.StatusCode, string(body))
	assert.Contains(t, string(body), "Documentation reader")
	assert.NotContains(t, string(body), "pbkdf2")
	response, body = callHTTP(t, client, server.URL, http.MethodDelete, "/users/"+viewer.ID, nil, session.CSRFToken)
	require.Equal(t, http.StatusNoContent, response.StatusCode, string(body))
	response, body = callHTTP(t, client, server.URL, http.MethodDelete, "/session", nil, session.CSRFToken)
	require.Equal(t, http.StatusNoContent, response.StatusCode, string(body))
	require.Len(t, response.Cookies(), 1)
	assert.Equal(t, -1, response.Cookies()[0].MaxAge)
	response, body = callHTTP(t, client, server.URL, http.MethodGet, "/session", nil, "")
	require.Equal(t, http.StatusOK, response.StatusCode)
	assert.JSONEq(t, `{"user":null}`, string(body))
}

func TestHTTPOriginJSONLimitsAndGenericLoginFailures(t *testing.T) {
	service, _, server, client := testHTTPService(t)
	testAdmin(t, service)
	for _, origin := range []string{"", "null", "https://attacker.test", server.URL + "/path"} {
		request, err := http.NewRequest(http.MethodPost, server.URL+"/session", strings.NewReader(`{"username":"admin","password":"anything"}`))
		require.NoError(t, err)
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		require.NoError(t, err)
		_ = response.Body.Close()
		assert.Equal(t, http.StatusForbidden, response.StatusCode)
	}
	for _, test := range []struct {
		body     string
		media    string
		encoding string
		status   int
	}{
		{`{}`, "text/plain", "", http.StatusUnsupportedMediaType},
		{`{}`, "application/json; charset=ascii", "", http.StatusUnsupportedMediaType},
		{`{}`, "application/json", "gzip", http.StatusUnsupportedMediaType},
		{`null`, "application/json", "", http.StatusBadRequest},
		{`[]`, "application/json", "", http.StatusBadRequest},
		{`{`, "application/json", "", http.StatusBadRequest},
		{`{} {}`, "application/json", "", http.StatusBadRequest},
		{`{"extra":true}`, "application/json", "", http.StatusBadRequest},
		{`{"username":"admin","username":"admin"}`, "application/json", "", http.StatusBadRequest},
		{`{"username":"admin","Username":"other"}`, "application/json", "", http.StatusBadRequest},
		{`{"Username":"admin"}`, "application/json", "", http.StatusBadRequest},
		{`{"username":null,"password":"wrong"}`, "application/json", "", http.StatusBadRequest},
		{`{"username":"admin","password":null}`, "application/json", "", http.StatusBadRequest},
		{`{"username":"\ud800"}`, "application/json", "", http.StatusBadRequest},
		{strings.Repeat(" ", 8193) + `{}`, "application/json", "", http.StatusBadRequest},
		{`{"password":"` + strings.Repeat("x", 1025) + `"}`, "application/json", "", http.StatusBadRequest},
		{`{"username":"` + strings.Repeat("x", 65) + `"}`, "application/json", "", http.StatusBadRequest},
		{string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}), "application/json", "", http.StatusBadRequest},
	} {
		request, err := http.NewRequest(http.MethodPost, server.URL+"/session", strings.NewReader(test.body))
		require.NoError(t, err)
		request.Header.Set("Origin", server.URL)
		request.Header.Set("Content-Type", test.media)
		request.Header.Set("Content-Encoding", test.encoding)
		response, err := client.Do(request)
		require.NoError(t, err)
		_ = response.Body.Close()
		assert.Equal(t, test.status, response.StatusCode, test.body[:min(len(test.body), 50)])
	}
	for _, username := range []string{"admin", "unknown"} {
		response, body := callHTTP(t, client, server.URL, http.MethodPost, "/session", map[string]string{"username": username, "password": "wrong"}, "")
		assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
		assert.JSONEq(t, `{"error":{"code":"unauthorized","message":"authentication required"}}`, string(body))
	}
}

func TestRoleMiddlewareCSRFAndRequestContext(t *testing.T) {
	service, _ := testService(t)
	admin := testAdmin(t, service)
	viewer := testUser(t, service, &admin, "viewer", RoleViewer)
	_, token, err := service.login(t.Context(), viewer.Username, testPassword, "client")
	require.NoError(t, err)
	assert.Nil(t, RequestUser(httptest.NewRequest(http.MethodGet, "/", nil)))
	protected := service.Require(RoleViewer, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		user := RequestUser(request)
		require.NotNil(t, user)
		assert.Same(t, user, UserFromContext(request.Context()))
		assert.Equal(t, viewer.ID, user.ID)
		writer.WriteHeader(http.StatusNoContent)
	}))
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, testRequest(service, token, method, "/protected"))
		assert.Equal(t, http.StatusNoContent, response.Code)
	}
	for _, input := range []struct {
		origin string
		csrf   string
		status int
	}{
		{"", "", http.StatusForbidden},
		{"https://attacker.test", service.csrfToken(token), http.StatusForbidden},
		{"https://docs.example.test", "wrong", http.StatusForbidden},
		{"https://docs.example.test", service.csrfToken(token), http.StatusNoContent},
	} {
		request := testRequest(service, token, http.MethodPost, "/protected")
		request.Header.Set("Origin", input.origin)
		request.Header.Set("X-CSRF-Token", input.csrf)
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, request)
		assert.Equal(t, input.status, response.Code)
	}
	response := httptest.NewRecorder()
	service.Require(RoleEditor, protected).ServeHTTP(response, testRequest(service, token, http.MethodGet, "/"))
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = httptest.NewRecorder()
	service.Require("invalid", protected).ServeHTTP(response, testRequest(service, token, http.MethodGet, "/"))
	assert.Equal(t, http.StatusForbidden, response.Code)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	response = httptest.NewRecorder()
	service.Require(RoleViewer, nil).ServeHTTP(response, testRequest(service, token, http.MethodGet, "/"))
	assert.Equal(t, http.StatusNotFound, response.Code)
	for _, cookie := range []string{"bad", strings.Repeat("z", 64), token + "; " + service.CookieName() + "=" + token} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Cookie", service.CookieName()+"="+cookie)
		user, err := service.Resolve(request)
		require.NoError(t, err)
		assert.Nil(t, user)
	}
	require.NoError(t, service.Close())
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func TestRealHTTPViewerManagementDeniedAndRateLimit(t *testing.T) {
	service, clock, server, client := testHTTPService(t)
	admin := testAdmin(t, service)
	testUser(t, service, &admin, "viewer", RoleViewer)
	session := loginHTTP(t, client, server.URL, "viewer")
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		resource := "/users"
		if method == http.MethodPatch || method == http.MethodDelete {
			resource += "/" + admin.ID
		}
		response, _ := callHTTP(t, client, server.URL, method, resource, map[string]string{}, session.CSRFToken)
		assert.Equal(t, http.StatusForbidden, response.StatusCode)
	}
	for i := 0; i < 6; i++ {
		response, _ := callHTTP(t, client, server.URL, http.MethodPost, "/session", map[string]string{"username": "missing", "password": "wrong"}, "")
		if i < 5 {
			assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
		} else {
			assert.Equal(t, http.StatusTooManyRequests, response.StatusCode)
			assert.Equal(t, "60", response.Header.Get("Retry-After"))
		}
	}
	clock.advance(time.Minute)
	response, _ := callHTTP(t, client, server.URL, http.MethodPost, "/session", map[string]string{"username": "missing", "password": "wrong"}, "")
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
}

func TestSecureCookieOriginCanonicalizationAndMethodErrors(t *testing.T) {
	service, _ := testService(t)
	testAdmin(t, service)
	request := httptest.NewRequest(http.MethodPost, "https://docs.example.test/session", strings.NewReader(`{"username":"admin","password":"`+testPassword+`"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Origin", "https://docs.example.test:443")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	cookie := response.Result().Cookies()[0]
	assert.Equal(t, "__Host-docs_session", cookie.Name)
	assert.True(t, cookie.Secure)
	assert.Empty(t, cookie.Domain)
	assert.Equal(t, "/", cookie.Path)
	request = httptest.NewRequest(http.MethodPost, "https://other.example/session", strings.NewReader(`{}`))
	request.Header.Set("Origin", "https://docs.example.test")
	response = httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	for _, resource := range []string{"/missing", "/users/not-an-id", "/users/../../session"} {
		response := httptest.NewRecorder()
		service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, resource, nil))
		assert.Equal(t, http.StatusNotFound, response.Code)
	}
	response = httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/session", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, response.Code)
	assert.Equal(t, "GET, HEAD, POST, DELETE", response.Header().Get("Allow"))
}
