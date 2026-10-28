package appearance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func navigationTree() *document.NavigationTree {
	collapsed := false
	return &document.NavigationTree{UnlistedPages: "omit", Items: []document.NavigationNode{{Kind: "group", ID: "private-menu", Title: "Private global navigation label", Description: "Global menu description", Collapsed: &collapsed, Children: []document.NavigationNode{{Kind: "page", PageID: "home", Title: "Private page override"}}}}}
}

func navigationAuthentication(t *testing.T) (*auth.Service, *http.Cookie, string, string) {
	t.Helper()
	service, err := auth.Open(auth.Config{StateDir: filepath.Join(t.TempDir(), "auth"), PublicURL: "https://studio.test"})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	password := "navigation test passphrase with several words"
	_, err = service.Bootstrap(t.Context(), "owner", "Owner", []byte(password))
	require.NoError(t, err)
	cookie, csrf := loginHTTP(t, service, "owner", password)
	return service, cookie, csrf, password
}

func navigationPayload(t *testing.T, settings Settings, tree any, present bool) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(encoded, &result))
	navigation := result["navigation"].(map[string]any)
	if present {
		navigation["tree"] = tree
	} else {
		delete(navigation, "tree")
	}
	return result
}

func navigationSettings(t *testing.T, service *Service) Settings {
	t.Helper()
	settings, err := service.Read(t.Context())
	require.NoError(t, err)
	return settings
}

func TestNavigationServicePreservesOmittedTreeAndRequiresExplicitReset(t *testing.T) {
	value := newFixture(t)
	settings := navigationSettings(t, value.service)
	settings.Navigation.Tree = navigationTree()
	saved, err := value.service.Update(t.Context(), settings, testActor())
	require.NoError(t, err)
	require.NotNil(t, saved.Navigation.Tree)
	require.NotNil(t, saved.Navigation.Tree.Items[0].Collapsed)
	assert.False(t, *saved.Navigation.Tree.Items[0].Collapsed)
	settings.Navigation.Tree.Items[0].Title = "Caller changed its old tree"
	assert.Equal(t, "Private global navigation label", navigationSettings(t, value.service).Navigation.Tree.Items[0].Title)

	olderClient := saved
	olderClient.Navigation.Tree = nil
	olderClient.Title = "Settings from a client without tree support"
	preserved, err := value.service.Update(t.Context(), olderClient, testActor())
	require.NoError(t, err)
	assert.Equal(t, navigationTree(), preserved.Navigation.Tree)
	assert.False(t, preserved.NavigationTreeSet)
	_, err = value.service.Update(t.Context(), olderClient, testActor())
	assert.ErrorIs(t, err, content.ErrConflict)

	preserved.Navigation.Tree = &document.NavigationTree{Items: []document.NavigationNode{}}
	empty, err := value.service.Update(t.Context(), preserved, testActor())
	require.NoError(t, err)
	require.NotNil(t, empty.Navigation.Tree)
	assert.NotNil(t, empty.Navigation.Tree.Items)
	assert.Empty(t, empty.Navigation.Tree.Items)
	empty.Navigation.Tree, empty.NavigationTreeSet = nil, true
	reset, err := value.service.Update(t.Context(), empty, testActor())
	require.NoError(t, err)
	assert.Nil(t, reset.Navigation.Tree)
}

func TestNavigationServiceAuthorizationCannotBeBypassedByResetOrLegacySave(t *testing.T) {
	value := newFixture(t)
	settings := navigationSettings(t, value.service)
	settings.Navigation.Tree = navigationTree()
	_, err := value.service.Update(t.Context(), settings, testActor())
	require.NoError(t, err)
	value.service.authorizeNavigation = func(context.Context, content.Actor) error { return auth.ErrForbidden }
	before := navigationSettings(t, value.service)
	legacy := before
	legacy.Navigation.Tree = nil
	legacy.Footer = "Allowed ordinary settings change"
	preserved, err := value.service.Update(t.Context(), legacy, testActor())
	require.NoError(t, err)
	assert.Equal(t, before.Navigation.Tree, preserved.Navigation.Tree)
	for _, reset := range []bool{false, true} {
		input := preserved
		if reset {
			input.Navigation.Tree, input.NavigationTreeSet = nil, true
		}
		_, err := value.service.Update(t.Context(), input, testActor())
		assert.ErrorIs(t, err, auth.ErrForbidden)
		assert.Equal(t, preserved.Revision, navigationSettings(t, value.service).Revision)
	}
}

func TestSettingsHTTPHidesGlobalTreeAndPreservesItForScopedAdministrators(t *testing.T) {
	value := newFixture(t)
	settings := navigationSettings(t, value.service)
	settings.Navigation.Tree = navigationTree()
	_, err := value.service.Update(t.Context(), settings, testActor())
	require.NoError(t, err)
	authentication, ownerCookie, ownerCSRF, password := navigationAuthentication(t)
	handler := value.service.Handler(authentication)
	owner := requestHTTP(t, handler, http.MethodGet, "/settings", nil, ownerCookie, "")
	require.Equal(t, http.StatusOK, owner.Code, owner.Body.String())
	assert.Contains(t, owner.Body.String(), "Private global navigation label")
	for _, account := range []struct {
		name   string
		role   auth.Role
		grants *auth.Grants
	}{
		{"viewer", auth.RoleViewer, nil},
		{"editor", auth.RoleEditor, nil},
		{"write-scoped", auth.RoleAdmin, &auth.Grants{Read: []string{"."}, Write: []string{"docs.json"}}},
		{"read-scoped", auth.RoleAdmin, &auth.Grants{Read: []string{"index.md"}, Write: []string{"."}}},
	} {
		t.Run(account.name, func(t *testing.T) {
			created := requestHTTP(t, authentication.Handler(), http.MethodPost, "/users", auth.CreateUserRequest{Username: account.name, DisplayName: account.name, Password: password, Role: account.role, Grants: account.grants}, ownerCookie, ownerCSRF)
			require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
			cookie, csrf := loginHTTP(t, authentication, account.name, password)
			response := requestHTTP(t, handler, http.MethodGet, "/settings", nil, cookie, "")
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "Private global navigation label")
			assert.NotContains(t, response.Body.String(), "Private page override")
			assert.NotContains(t, response.Body.String(), `"tree"`)
			var ordinary Settings
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &ordinary))
			ordinary.Title = "Ordinary settings by " + account.name
			if account.role == auth.RoleAdmin {
				response = requestHTTP(t, handler, http.MethodPut, "/settings", ordinary, cookie, csrf)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				assert.NotContains(t, response.Body.String(), `"tree"`)
				assert.NotContains(t, response.Body.String(), "Private global navigation label")
				ordinary = navigationSettings(t, value.service)
				assert.Equal(t, navigationTree(), ordinary.Navigation.Tree)
			}
			for _, tree := range []any{nil, navigationTree()} {
				response := requestHTTP(t, handler, http.MethodPut, "/settings", navigationPayload(t, ordinary, tree, true), cookie, csrf)
				assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			}
		})
	}
	assert.Equal(t, navigationTree(), navigationSettings(t, value.service).Navigation.Tree)
}

func TestNavigationHTTPPresenceStrictFieldsAndExpandedRequestBudget(t *testing.T) {
	value := newFixture(t)
	authentication, cookie, csrf, _ := navigationAuthentication(t)
	handler := value.service.Handler(authentication)
	settings := navigationSettings(t, value.service)
	tree := &document.NavigationTree{Items: []document.NavigationNode{{Kind: "page", PageID: "home", Title: strings.Repeat("<", 256)}}}
	for index := range 7 {
		tree.Items = []document.NavigationNode{{Kind: "group", ID: fmt.Sprintf("section%d", index), Title: strings.Repeat("<", 256), Description: strings.Repeat("<", 512), Children: tree.Items}}
	}
	payload := navigationPayload(t, settings, tree, true)
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	require.Greater(t, len(encoded), 32<<10)
	response := requestHTTP(t, handler, http.MethodPut, "/settings", string(encoded), cookie, csrf)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, tree, navigationSettings(t, value.service).Navigation.Tree)
	current := navigationSettings(t, value.service)
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`[]`),
		json.RawMessage(`{"items":null}`),
		json.RawMessage(`{"items":[],"script":"alert(1)"}`),
		json.RawMessage(`{"items":[],"items":[]}`),
		json.RawMessage(`{"items":[{"kind":"page","pageId":"home","Title":"wrong case"}]}`),
		json.RawMessage(`{"items":[{"kind":"page","pageId":"home","collapsed":false}]}`),
	} {
		response := requestHTTP(t, handler, http.MethodPut, "/settings", navigationPayload(t, current, invalid, true), cookie, csrf)
		assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		assert.Equal(t, current.Revision, navigationSettings(t, value.service).Revision)
	}
	body := navigationPayload(t, current, nil, false)
	body["navigation"].(map[string]any)["showTOC"] = nil
	assert.Equal(t, http.StatusBadRequest, requestHTTP(t, handler, http.MethodPut, "/settings", body, cookie, csrf).Code)
	body = navigationPayload(t, current, nil, false)
	body["NavigationTreeSet"] = true
	assert.Equal(t, http.StatusBadRequest, requestHTTP(t, handler, http.MethodPut, "/settings", body, cookie, csrf).Code)
	response = requestHTTP(t, handler, http.MethodPut, "/settings", navigationPayload(t, current, &document.NavigationTree{Items: []document.NavigationNode{}}, true), cookie, csrf)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	empty := navigationSettings(t, value.service)
	require.NotNil(t, empty.Navigation.Tree)
	assert.Empty(t, empty.Navigation.Tree.Items)
	response = requestHTTP(t, handler, http.MethodPut, "/settings", navigationPayload(t, empty, nil, true), cookie, csrf)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Nil(t, navigationSettings(t, value.service).Navigation.Tree)
}

type navigationReadGate struct {
	content.Backend
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (b *navigationReadGate) Status(ctx context.Context) (content.SyncStatus, error) {
	if b.armed.CompareAndSwap(true, false) {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			return content.SyncStatus{}, ctx.Err()
		}
	}
	return b.Backend.Status(ctx)
}

func TestSettingsTreeReadRechecksSessionAfterWaitingForContent(t *testing.T) {
	backend := &navigationReadGate{Backend: content.NewLocalBackend(), entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	}()
	value := fixtureWithStorage(t, backend)
	settings := navigationSettings(t, value.service)
	settings.Navigation.Tree = navigationTree()
	_, err := value.service.Update(t.Context(), settings, testActor())
	require.NoError(t, err)
	authentication, cookie, csrf, _ := navigationAuthentication(t)
	handler := value.service.Handler(authentication)
	backend.armed.Store(true)
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() { completed <- requestHTTP(t, handler, http.MethodGet, "/settings", nil, cookie, "") }()
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("settings read did not wait for content")
	}
	response := requestHTTP(t, authentication.Handler(), http.MethodDelete, "/session", nil, cookie, csrf)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
	close(backend.release)
	select {
	case response := <-completed:
		assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "Private global navigation label")
	case <-time.After(5 * time.Second):
		t.Fatal("settings read did not finish after content was released")
	}
}
