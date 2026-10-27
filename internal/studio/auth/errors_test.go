package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealTLSManagementErrorContractAndStatePreservation(t *testing.T) {
	service, _, server, client := testHTTPService(t)
	admin := testAdmin(t, service)
	session := loginHTTP(t, client, server.URL, "admin")
	for _, test := range []struct {
		method   string
		resource string
		input    any
		status   int
		code     string
	}{
		{http.MethodPost, "/users", CreateUserRequest{Username: "ADMIN", Password: testPassword, Role: RoleViewer}, http.StatusConflict, "conflict"},
		{http.MethodPost, "/users", CreateUserRequest{Username: "other", Password: testPassword, Role: "owner"}, http.StatusBadRequest, "invalid_request"},
		{http.MethodPost, "/users", map[string]any{"unknown": true}, http.StatusBadRequest, "invalid_request"},
		{http.MethodPost, "/users", map[string]any{"username": "other", "password": testPassword, "role": "viewer", "grants": nil}, http.StatusBadRequest, "invalid_request"},
		{http.MethodPatch, "/users/" + admin.ID, map[string]any{"role": "viewer"}, http.StatusConflict, "last_admin"},
		{http.MethodPatch, "/users/" + admin.ID, map[string]any{"displayName": "bad\nname"}, http.StatusBadRequest, "invalid_request"},
		{http.MethodPatch, "/users/" + admin.ID, map[string]any{"role": "admin", "Role": "viewer"}, http.StatusBadRequest, "invalid_request"},
		{http.MethodPatch, "/users/" + admin.ID, map[string]any{"role": nil, "displayName": "Should not change"}, http.StatusBadRequest, "invalid_request"},
		{http.MethodPatch, "/users/" + admin.ID, map[string]any{"disabled": nil, "displayName": "Should not change"}, http.StatusBadRequest, "invalid_request"},
		{http.MethodPatch, "/users/" + strings.Repeat("f", 32), map[string]any{"displayName": "Missing"}, http.StatusNotFound, "not_found"},
		{http.MethodDelete, "/users/" + strings.Repeat("f", 32), nil, http.StatusNotFound, "not_found"},
		{http.MethodDelete, "/users/" + admin.ID, nil, http.StatusConflict, "last_admin"},
		{http.MethodPut, "/users", nil, http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodGet, "/users/" + admin.ID, nil, http.StatusMethodNotAllowed, "method_not_allowed"},
	} {
		response, data := callHTTP(t, client, server.URL, test.method, test.resource, test.input, session.CSRFToken)
		assert.Equal(t, test.status, response.StatusCode)
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(data, &envelope))
		assert.Equal(t, test.code, envelope.Error.Code)
		assert.NotEmpty(t, envelope.Error.Message)
		assert.NotContains(t, string(data), testPassword)
	}
	assert.Len(t, service.state.Users, 1)
	assert.Equal(t, RoleAdmin, service.state.Users[admin.ID].Role)
	require.NoError(t, service.root.Close())
	response, data := callHTTP(t, client, server.URL, http.MethodPost, "/users", CreateUserRequest{Username: "uncommitted", Password: testPassword, Role: RoleViewer}, session.CSRFToken)
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	assert.Contains(t, string(data), "storage_unavailable")
	assert.NotContains(t, string(data), service.config.StateDir)
	assert.Len(t, service.state.Users, 1)
	response, _ = callHTTP(t, client, server.URL, http.MethodDelete, "/session", nil, session.CSRFToken)
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	_ = service.Close()
	response, _ = callHTTP(t, client, server.URL, http.MethodGet, "/session", nil, "")
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
}

func TestRealTLSSessionCapacityReturnsRetryableError(t *testing.T) {
	service, _, server, client := testHTTPServiceWithConfig(t, func(config *Config) { config.MaxSessions = 1 })
	testAdmin(t, service)
	loginHTTP(t, client, server.URL, "admin")
	response, data := callHTTP(t, client, server.URL, http.MethodPost, "/session", map[string]string{"username": "admin", "password": testPassword}, "")
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	assert.Equal(t, "1", response.Header.Get("Retry-After"))
	assert.Contains(t, string(data), `"code":"busy"`)
	assert.Len(t, service.state.Sessions, 1)
}

func TestCancelledPasswordVerificationCannotCreateSession(t *testing.T) {
	service, _ := testService(t)
	testAdmin(t, service)
	started, release := make(chan struct{}), make(chan struct{})
	service.runtime.derive = func(password string, salt []byte, iterations int) ([]byte, error) {
		close(started)
		<-release
		return derivePassword(password, salt, iterations)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := service.login(ctx, "admin", testPassword, "client")
		result <- err
	}()
	<-started
	cancel()
	close(release)
	assert.ErrorIs(t, <-result, context.Canceled)
	assert.Empty(t, service.state.Sessions)
}

func TestPasswordBytesAreNotTrimmedAndSaltsAreIndependent(t *testing.T) {
	service, _ := testService(t)
	password := "  Unique Unicode passphrase 雪 no normalization  "
	admin, err := service.Bootstrap(context.Background(), "admin", "Admin", []byte(password))
	require.NoError(t, err)
	other, err := service.createUser(context.Background(), &admin, CreateUserRequest{Username: "other", Password: password, Role: RoleEditor})
	require.NoError(t, err)
	first := service.state.Users[admin.ID].Password
	second := service.state.Users[other.ID].Password
	assert.False(t, first.Salt == second.Salt)
	assert.False(t, first.Hash == second.Hash)
	_, _, err = service.login(context.Background(), "admin", strings.TrimSpace(password), "client")
	assert.ErrorIs(t, err, ErrUnauthorized)
	_, _, err = service.login(context.Background(), "admin", password[:20], "client")
	assert.ErrorIs(t, err, ErrUnauthorized)
	_, token, err := service.login(context.Background(), "admin", password, "client")
	require.NoError(t, err)
	assert.True(t, len(token) == 64)
}
