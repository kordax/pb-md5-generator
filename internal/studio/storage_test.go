package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorageAuthorizationRejectsInternalSystemActor(t *testing.T) {
	// Trusted compilation can use a system author; managing storage still
	// requires a freshly authenticated administrator with root access.
	assert.ErrorIs(t, authorizeStorage(context.Background(), content.Actor{ID: "system"}), auth.ErrForbidden)
}

func TestStorageAuthorizationUsesFreshSessionAndAuthenticatedActor(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	prepared, err := prepareConfig(config)
	require.NoError(t, err)
	resources := &runtimeResources{}
	t.Cleanup(func() { assert.NoError(t, resources.close()) })
	authentication, err := newRuntimeAuth(prepared, resources)
	require.NoError(t, err)
	contents, err := newRuntimeContent(prepared, authentication, resources)
	require.NoError(t, err)
	assert.NoError(t, validateMutation(context.Background(), "", nil, content.Actor{ID: "system"}, authentication, ""), "trusted initial compilation requires its authentication dependency")
	assert.ErrorIs(t, validateMutation(context.Background(), "", nil, content.Actor{ID: "untrusted"}, authentication, ""), auth.ErrForbidden)
	assert.ErrorIs(t, validateMutation(context.Background(), "", nil, content.Actor{ID: "system"}, nil, ""), auth.ErrForbidden)
	var run func(string, string, any) *httptest.ResponseRecorder
	var actor content.Actor
	var observed, mutationObserved error
	revoke := false
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoke {
			response := run(http.MethodDelete, "/_docs/api/v1/session", nil)
			require.Equal(t, http.StatusNoContent, response.Code)
		}
		observed = authorizeStorage(r.Context(), actor)
		if revoke || actor.ID == "another-user" {
			mutationObserved = validateMutation(r.Context(), "", nil, actor, authentication, "")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler, err := apiweb.New(apiweb.Options{
		PublicURL: prepared.PublicURL, Auth: authentication, Content: contents,
		Assets: web.Assets(), Contract: APIContract(), Appearance: probe, Reader: probe,
		Preview: func(context.Context, apiweb.Document, []byte, *auth.User) (apiweb.Preview, error) {
			return apiweb.Preview{}, nil
		},
	})
	require.NoError(t, err)
	var cookie *http.Cookie
	var csrf string
	run = func(method, route string, input any) *httptest.ResponseRecorder {
		var data []byte
		if input != nil {
			var err error
			data, err = json.Marshal(input)
			require.NoError(t, err)
		}
		request := httptest.NewRequest(method, prepared.PublicURL+route, bytes.NewReader(data))
		request.Header.Set("Origin", prepared.PublicURL)
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
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
	login := run(http.MethodPost, "/_docs/api/v1/session", map[string]string{"username": "owner", "password": ownerTestPassword})
	require.Equal(t, http.StatusOK, login.Code)
	cookies := login.Result().Cookies()
	require.Equal(t, 1, len(cookies))
	cookie = cookies[0]
	var session struct {
		User      auth.User `json:"user"`
		CSRFToken string    `json:"csrfToken"`
	}
	require.NoError(t, json.Unmarshal(login.Body.Bytes(), &session))
	csrf = session.CSRFToken
	actor.ID = session.User.ID
	response := run(http.MethodGet, "/_docs/api/v1/settings", nil)
	require.Equal(t, http.StatusNoContent, response.Code)
	assert.NoError(t, observed)
	actor.ID = "another-user"
	response = run(http.MethodGet, "/_docs/api/v1/settings", nil)
	require.Equal(t, http.StatusNoContent, response.Code)
	assert.ErrorIs(t, observed, auth.ErrForbidden)
	assert.ErrorIs(t, mutationObserved, auth.ErrForbidden)
	actor.ID, revoke = session.User.ID, true
	response = run(http.MethodGet, "/_docs/api/v1/settings", nil)
	require.Equal(t, http.StatusNoContent, response.Code)
	assert.ErrorIs(t, observed, auth.ErrUnauthorized, "an authenticated request must not retain a revoked session")
	assert.ErrorIs(t, mutationObserved, auth.ErrUnauthorized)
}

func TestInvalidStorageStartupReleasesAccountStateAndListener(t *testing.T) {
	config := runtimeConfigForTest(t)
	config.GitRemote = "-invalid"
	server, err := Start(t.Context(), config)
	require.ErrorIs(t, err, content.ErrInvalid)
	assert.Nil(t, server)
	listener, err := net.Listen("tcp", config.Listener.Addr().String())
	require.NoError(t, err, "a failed storage constructor releases its listener")
	require.NoError(t, listener.Close())
	config.GitRemote = "origin"
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Recovered owner", []byte(ownerTestPassword)), "account state is unlocked after construction fails")
}
