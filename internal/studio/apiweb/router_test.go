package apiweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testOrigin = "https://docs.example"
const testBase = "/team"
const testAPI = testBase + "/_docs/api/v1"
const testPassword = "a long test passphrase with many words"

type fixture struct {
	handler http.Handler
	auth    *auth.Service
	content *content.Service
	root    string
	output  string
	admin   credentials
}

type credentials struct {
	cookie *http.Cookie
	csrf   string
	user   auth.User
}

func newFixture(t *testing.T, modify func(*Options)) *fixture {
	t.Helper()
	root := t.TempDir()
	authService, err := auth.Open(auth.Config{StateDir: filepath.Join(root, "auth"), PublicURL: testOrigin + testBase})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, authService.Close()) })
	_, err = authService.Bootstrap(t.Context(), "owner", "Owner", []byte(testPassword))
	require.NoError(t, err)
	managed, output := filepath.Join(root, "sources"), filepath.Join(root, "site")
	contentService, err := content.New(content.Config{Root: managed, StateDir: filepath.Join(root, "state"), OutputDir: output, Initialize: true, Compile: func(ctx context.Context, manifest, directory string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(filepath.Dir(manifest), "index.md"))
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("INVALID_SOURCE")) {
			return errors.New("deliberate compiler error with private implementation detail")
		}
		return site.BuildContext(ctx, manifest, directory, site.Options{})
	}})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, contentService.Close()) })
	options := Options{BasePath: testBase, PublicURL: testOrigin + testBase, Auth: authService, Content: contentService, Assets: web.Assets(), Contract: []byte("openapi: 3.1.0\n"), Appearance: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"path": r.URL.Path})
	}), Preview: func(_ context.Context, doc Document, source []byte, user *auth.User) (Preview, error) {
		if bytes.Contains(source, []byte("INVALID_SOURCE")) {
			return Preview{Diagnostics: []Diagnostic{{File: doc.Path, Line: 1, Message: "Invalid source"}}}, errors.New("invalid syntax")
		}
		return Preview{HTML: "<p>" + html.EscapeString(string(source)) + "</p>"}, nil
	}}
	if modify != nil {
		modify(&options)
	}
	handler, err := New(options)
	require.NoError(t, err)
	fixture := &fixture{handler: handler, auth: authService, content: contentService, root: managed, output: output}
	fixture.admin = fixture.login(t, "owner", testPassword)
	return fixture
}

func (f *fixture) request(method, target string, body any, identity *credentials, headers map[string]string) *httptest.ResponseRecorder {
	var data []byte
	if body != nil {
		if raw, ok := body.([]byte); ok {
			data = raw
		} else {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				panic(err)
			}
		}
	}
	r := httptest.NewRequest(method, testOrigin+target, bytes.NewReader(data))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Origin", testOrigin)
	}
	if identity != nil {
		r.AddCookie(identity.cookie)
		r.Header.Set("X-CSRF-Token", identity.csrf)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func resultJSON[T any](t *testing.T, response *httptest.ResponseRecorder, status int) T {
	t.Helper()
	require.Equal(t, status, response.Code, response.Body.String())
	var result T
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result), response.Body.String())
	return result
}

func (f *fixture) login(t *testing.T, username, password string) credentials {
	t.Helper()
	response := f.request(http.MethodPost, testAPI+"/session", map[string]string{"username": username, "password": password}, nil, nil)
	result := resultJSON[struct {
		User      auth.User `json:"user"`
		CSRFToken string    `json:"csrfToken"`
	}](t, response, http.StatusOK)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 1)
	require.True(t, cookies[0].Secure)
	require.True(t, cookies[0].HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite)
	return credentials{cookie: cookies[0], csrf: result.CSRFToken, user: result.User}
}

func (f *fixture) createUser(t *testing.T, username string, role auth.Role, grants *auth.Grants) credentials {
	t.Helper()
	input := map[string]any{"username": username, "password": testPassword, "displayName": username, "role": role}
	if grants != nil {
		input["grants"] = grants
	}
	resultJSON[auth.User](t, f.request(http.MethodPost, testAPI+"/users", input, &f.admin, nil), http.StatusCreated)
	return f.login(t, username, testPassword)
}

func (f *fixture) current(t *testing.T, user *credentials) Document {
	t.Helper()
	return resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home?published=true", nil, user, nil), http.StatusOK)
}

func TestAdministrationAssetsAndRequestBoundaries(t *testing.T) {
	f := newFixture(t, nil)
	first := f.request(http.MethodGet, testBase+"/_docs/admin/", nil, nil, nil)
	second := f.request(http.MethodGet, testBase+"/_docs/admin/", nil, nil, nil)
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, http.StatusOK, second.Code)
	assert.NotContains(t, first.Body.String(), noncePlaceholder)
	assert.NotEqual(t, first.Header().Get("Content-Security-Policy"), second.Header().Get("Content-Security-Policy"))
	assert.Contains(t, first.Header().Get("Content-Security-Policy"), "script-src 'self'")
	assert.NotContains(t, first.Header().Get("Content-Security-Policy"), "unsafe-inline")
	assert.Equal(t, "no-store", first.Header().Get("Cache-Control"))
	for _, asset := range []string{"studio.css", "app.mjs", "core.mjs", "editor.js", "THIRD_PARTY_NOTICES.txt"} {
		assert.Equal(t, http.StatusOK, f.request(http.MethodGet, testBase+"/_docs/admin/"+asset, nil, nil, nil).Code, asset)
	}
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testBase+"/_docs/admin/package.json", nil, nil, nil).Code)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, "/outside/", nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testBase+"/_docs/admin/../app.mjs", nil, nil, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testAPI+"/documents%2fhome", nil, &f.admin, nil).Code)
	head := f.request(http.MethodHead, testBase+"/_docs/admin/", nil, nil, nil)
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.Bytes())
	assert.NotEmpty(t, head.Header().Get("Content-Length"))
	method := f.request(http.MethodPost, testBase+"/_docs/admin/", map[string]string{}, nil, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, method.Code)
	assert.Equal(t, "GET, HEAD", method.Header().Get("Allow"))
	badHost := httptest.NewRequest(http.MethodGet, "https://attacker.invalid"+testAPI+"/session", nil)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, badHost)
	assert.Equal(t, http.StatusForbidden, response.Code)
	health := httptest.NewRequest(http.MethodGet, "https://localhost"+testBase+"/_docs/health", nil)
	response = httptest.NewRecorder()
	f.handler.ServeHTTP(response, health)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"status":"ready"}`, response.Body.String())
}

func TestAuthenticationCSRFRevocationAndContract(t *testing.T) {
	f := newFixture(t, nil)
	assert.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, testAPI+"/documents", nil, nil, nil).Code)
	assert.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, testAPI+"/openapi.yaml", nil, nil, nil).Code)
	contract := f.request(http.MethodGet, testAPI+"/openapi.yaml", nil, &f.admin, nil)
	assert.Equal(t, http.StatusOK, contract.Code)
	assert.Equal(t, "openapi: 3.1.0\n", contract.Body.String())
	assert.Equal(t, "application/yaml; charset=utf-8", contract.Header().Get("Content-Type"))
	viewer := f.createUser(t, "reader", auth.RoleViewer, nil)
	editor := f.createUser(t, "writer", auth.RoleEditor, nil)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodGet, testAPI+"/users", nil, &editor, nil).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/documents", map[string]string{}, &viewer, nil).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{}, &editor, map[string]string{"X-CSRF-Token": "wrong"}).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{}, &editor, map[string]string{"Origin": "https://elsewhere.invalid"}).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{}, &editor, map[string]string{"Origin": ""}).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, f.request(http.MethodPatch, testAPI+"/documents/home", map[string]string{}, &editor, nil).Code)
	resultJSON[auth.User](t, f.request(http.MethodPatch, testAPI+"/users/"+editor.user.ID, map[string]bool{"disabled": true}, &f.admin, nil), http.StatusOK)
	assert.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, testAPI+"/documents/home", nil, &editor, nil).Code)
	assert.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/session", nil, &viewer, nil).Code)
	assert.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, testAPI+"/documents", nil, &viewer, nil).Code)
}

func TestDraftIsolationStaleSavesAndExplicitRebase(t *testing.T) {
	f := newFixture(t, nil)
	alice := f.createUser(t, "alice", auth.RoleEditor, nil)
	bob := f.createUser(t, "bob", auth.RoleEditor, nil)
	base := f.current(t, &alice)
	saved := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]any{"content": "# Alice draft", "revision": base.Revision}, &alice, nil), http.StatusOK)
	require.NotNil(t, saved.Draft)
	assert.False(t, saved.Published)
	assert.Equal(t, base.Content, f.current(t, &alice).Content)
	aliceDoc := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &alice, nil), http.StatusOK)
	assert.Equal(t, "# Alice draft", aliceDoc.Content)
	bobDoc := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &bob, nil), http.StatusOK)
	assert.Equal(t, base.Content, bobDoc.Content)
	assert.False(t, bobDoc.Draft)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodDelete, testAPI+"/drafts/"+saved.Draft.ID, nil, &bob, map[string]string{"If-Match": saved.Draft.Revision}).Code)
	bobSave := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]any{"content": "# Bob published", "revision": base.Revision, "publish": true}, &bob, nil), http.StatusOK)
	require.True(t, bobSave.Published)
	assert.NotEqual(t, base.Revision, bobSave.Revision)
	stale := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]any{"content": "# Alice draft, kept safely", "revision": aliceDoc.Revision, "draftId": aliceDoc.DraftID, "draftRevision": aliceDoc.DraftRevision, "baseRevision": aliceDoc.BaseRevision}, &alice, nil), http.StatusOK)
	assert.Equal(t, base.Revision, stale.Draft.BaseRevision)
	conflict := f.request(http.MethodPost, testAPI+"/drafts/"+stale.Draft.ID+"/publish", map[string]string{"revision": stale.Draft.Revision}, &alice, nil)
	assert.Equal(t, http.StatusConflict, conflict.Code)
	assert.Contains(t, conflict.Body.String(), bobSave.Revision)
	assert.Equal(t, "# Bob published", f.current(t, &alice).Content)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/documents/home/rebase", map[string]string{"content": "# Merged", "revision": bobSave.Revision}, &alice, nil).Code)
	rebased := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents/home/rebase", map[string]string{"content": "# Merged", "revision": bobSave.Revision, "draftId": stale.Draft.ID, "draftRevision": stale.Draft.Revision}, &alice, nil), http.StatusOK)
	assert.Equal(t, bobSave.Revision, rebased.Draft.BaseRevision)
	published := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/drafts/"+rebased.Draft.ID+"/publish", map[string]string{"revision": rebased.Draft.Revision}, &alice, nil), http.StatusOK)
	assert.True(t, published.Published)
	assert.Equal(t, "# Merged", f.current(t, &alice).Content)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "# Outdated window", "revision": base.Revision, "draftId": stale.Draft.ID, "draftRevision": stale.Draft.Revision}, &alice, nil).Code)
}

func TestPublicationFailureRetainsSiteAndDraft(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	first := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]any{"content": "# Healthy", "revision": base.Revision, "publish": true}, &f.admin, nil), http.StatusOK)
	before, err := os.ReadFile(filepath.Join(f.output, "index.html"))
	require.NoError(t, err)
	failed := f.request(http.MethodPut, testAPI+"/documents/home", map[string]any{"content": "INVALID_SOURCE", "revision": first.Revision, "publish": true}, &f.admin, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, failed.Code)
	failure := resultJSON[struct {
		Error errorDetail `json:"error"`
	}](t, failed, http.StatusUnprocessableEntity)
	require.NotNil(t, failure.Error.Draft)
	require.NotNil(t, failure.Error.Document)
	assert.Equal(t, "INVALID_SOURCE", failure.Error.Document.Content)
	assert.NotContains(t, failed.Body.String(), "private implementation")
	after, err := os.ReadFile(filepath.Join(f.output, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, "# Healthy", f.current(t, &f.admin).Content)
	draft := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &f.admin, nil), http.StatusOK)
	assert.True(t, draft.Draft)
	assert.Equal(t, "INVALID_SOURCE", draft.Content)
	preview := f.request(http.MethodPost, testAPI+"/preview", map[string]string{"documentId": "home", "content": "INVALID_SOURCE"}, &f.admin, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, preview.Code)
	assert.Contains(t, preview.Body.String(), "Invalid source")
	corrected := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "# Corrected without reloading", "revision": failure.Error.Draft.BaseRevision, "baseRevision": failure.Error.Draft.BaseRevision, "draftId": failure.Error.Draft.ID, "draftRevision": failure.Error.Draft.Revision}, &f.admin, nil), http.StatusOK)
	require.NotNil(t, corrected.Draft)
	assert.Equal(t, "# Healthy", f.current(t, &f.admin).Content)
	assert.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/drafts/"+corrected.Draft.ID, nil, &f.admin, map[string]string{"If-Match": corrected.Draft.Revision}).Code)
	assert.False(t, resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &f.admin, nil), http.StatusOK).Draft)
}

func TestBoundedContentConcurrencyAndFreshPrincipal(t *testing.T) {
	entered, release := make(chan struct{}, 4), make(chan struct{})
	f := newFixture(t, func(options *Options) {
		options.Preview = func(ctx context.Context, _ Document, _ []byte, _ *auth.User) (Preview, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return Preview{HTML: "<p>done</p>"}, nil
			case <-ctx.Done():
				return Preview{}, ctx.Err()
			}
		}
	})
	var wait sync.WaitGroup
	wait.Add(4)
	for range 4 {
		go func() {
			defer wait.Done()
			response := f.request(http.MethodPost, testAPI+"/preview", map[string]string{"documentId": "home", "content": "preview"}, &f.admin, nil)
			assert.Equal(t, http.StatusOK, response.Code)
		}()
	}
	for range 4 {
		<-entered
	}
	busy := f.request(http.MethodPost, testAPI+"/preview", map[string]string{"documentId": "home", "content": "extra"}, &f.admin, nil)
	assert.Equal(t, http.StatusServiceUnavailable, busy.Code)
	assert.Equal(t, "1", busy.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusOK, f.request(http.MethodGet, testBase+"/_docs/admin/app.mjs", nil, nil, nil).Code)
	close(release)
	wait.Wait()
	user, err := Principal(t.Context())
	assert.Nil(t, user)
	assert.NoError(t, err)
	var remembered context.Context
	f = newFixture(t, func(options *Options) {
		options.Reader = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remembered = r.Context()
			w.WriteHeader(http.StatusOK)
		})
	})
	assert.Equal(t, http.StatusOK, f.request(http.MethodGet, testBase+"/", nil, &f.admin, nil).Code)
	principal, err := Principal(remembered)
	require.NoError(t, err)
	require.Equal(t, f.admin.user.ID, principal.ID)
	assert.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/session", nil, &f.admin, nil).Code)
	principal, err = Principal(remembered)
	assert.Nil(t, principal)
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}
