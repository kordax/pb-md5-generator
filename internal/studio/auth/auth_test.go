package auth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPassword = "Unusual test passphrase number 492!"
const nextPassword = "Another unique test passphrase 871!"

type testClock struct{ seconds atomic.Int64 }

func newTestClock() *testClock {
	clock := &testClock{}
	clock.seconds.Store(time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC).Unix())
	return clock
}

func (c *testClock) now() time.Time { return time.Unix(c.seconds.Load(), 0).UTC() }
func (c *testClock) advance(duration time.Duration) {
	c.seconds.Add(int64(duration / time.Second))
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{StateDir: filepath.Join(t.TempDir(), "private-state"), PublicURL: "https://docs.example.test/team/"}
}

func testService(t *testing.T) (*Service, *testClock) {
	t.Helper()
	clock := newTestClock()
	service, err := openService(testConfig(t), runtimeOptions{iterations: 2, now: clock.now, derive: derivePassword})
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Close() })
	return service, clock
}

func testAdmin(t *testing.T, service *Service) User {
	t.Helper()
	user, err := service.Bootstrap(context.Background(), "admin", "Administrator", []byte(testPassword))
	require.NoError(t, err)
	return user
}

func testUser(t *testing.T, service *Service, admin *User, username string, role Role) User {
	t.Helper()
	user, err := service.createUser(context.Background(), admin, CreateUserRequest{Username: username, DisplayName: username, Password: testPassword, Role: role})
	require.NoError(t, err)
	return user
}

func testRequest(service *Service, token, method, resource string) *http.Request {
	request := httptest.NewRequest(method, "https://docs.example.test"+resource, nil)
	request.AddCookie(&http.Cookie{Name: service.CookieName(), Value: token})
	return request
}

func TestPasswordDerivationKnownVectorsAndProductionFloor(t *testing.T) {
	vectors := []struct {
		iterations int
		expected   string
	}{
		{1, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{2, "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
	}
	for _, vector := range vectors {
		key, err := derivePassword("password", []byte("salt"), vector.iterations)
		require.NoError(t, err)
		assert.Equal(t, vector.expected, hex.EncodeToString(key))
	}
	service, err := Open(testConfig(t))
	require.NoError(t, err)
	defer func() { _ = service.Close() }()
	admin := testAdmin(t, service)
	record := service.state.Users[admin.ID].Password
	assert.GreaterOrEqual(t, record.Iterations, 600000)
	assert.Equal(t, "pbkdf2-sha256", record.Algorithm)
	assert.NotContains(t, record.Hash, testPassword)
	assert.Equal(t, "__Host-docs_session", service.CookieName())
}

func TestPasswordAndUserPolicies(t *testing.T) {
	for _, password := range []string{"short", strings.Repeat("x", 1025), string([]byte{0xff}) + strings.Repeat("x", 20), "passwordpassword", "correct horse battery staple", strings.Repeat(" ", 20)} {
		assert.Error(t, validatePassword(password, "admin", "Admin"))
	}
	assert.NoError(t, validatePassword("  a Unicode passphrase 雪だるま  ", "admin", "Admin"))
	assert.NoError(t, validatePassword(strings.Repeat("雪", 15), "admin", "Admin"))
	assert.Error(t, validatePassword(strings.Repeat("雪", 400), "admin", "Admin"))
	assert.Error(t, validatePassword("longusernamelong", "longusernamelong", "Name"))
	assert.Error(t, validatePassword("Long display name", "user", "Long display name"))
	for _, request := range []CreateUserRequest{
		{Username: "", Password: testPassword, Role: RoleViewer},
		{Username: strings.Repeat("x", 65), Password: testPassword, Role: RoleViewer},
		{Username: "has space", Password: testPassword, Role: RoleViewer},
		{Username: "user", DisplayName: "\n", Password: testPassword, Role: RoleViewer},
		{Username: "user", DisplayName: strings.Repeat("x", 257), Password: testPassword, Role: RoleViewer},
		{Username: "user", Password: testPassword, Role: "superuser"},
		{Username: "user", Password: "short", Role: RoleViewer},
		{Username: "user", Password: testPassword, Role: RoleViewer, Grants: &Grants{Read: []string{"../"}}},
	} {
		_, err := validatedUser(request)
		assert.Error(t, err)
	}
	user, err := validatedUser(CreateUserRequest{Username: "Mixed.Name_1-2", Password: testPassword, Role: RoleViewer})
	require.NoError(t, err)
	assert.Equal(t, "mixed.name_1-2", user.Username)
	assert.Equal(t, "Mixed.Name_1-2", user.DisplayName)
}

func TestAuthorizationGrantsNeverElevateRoles(t *testing.T) {
	for _, role := range []Role{RoleAdmin, RoleEditor, RoleViewer} {
		user := &User{Role: role}
		assert.True(t, Can(user, ActionRead, "docs/page.md"))
		assert.Equal(t, role != RoleViewer, Can(user, ActionWrite, "docs/page.md"))
		assert.Equal(t, role == RoleAdmin, Can(user, ActionAdmin, "."))
		user.Grants = &Grants{Read: []string{"docs"}, Write: []string{"docs/public"}}
		assert.True(t, Can(user, ActionRead, "docs/page.md"))
		assert.True(t, Can(user, ActionRead, "docs"))
		assert.False(t, Can(user, ActionRead, "docs-private/page.md"))
		assert.False(t, Can(user, ActionRead, "other.md"))
		assert.False(t, Can(user, ActionWrite, "docs/page.md"))
		assert.Equal(t, role != RoleViewer, Can(user, ActionWrite, "docs/public/page.md"))
		user.Grants = &Grants{}
		assert.False(t, Can(user, ActionRead, "docs/page.md"))
		assert.False(t, Can(user, ActionWrite, "docs/page.md"))
		user.Grants = &Grants{Read: []string{"."}}
		assert.True(t, Can(user, ActionRead, "any/page.md"))
		for _, invalid := range []string{"", "../secret", "/absolute", "docs/../secret", `docs\secret`, "C:drive", "docs\nsecret", strings.Repeat("a", 4097)} {
			assert.False(t, Can(user, ActionRead, invalid), invalid)
		}
		user.Disabled = true
		assert.False(t, Can(user, ActionAdmin, "."))
		assert.False(t, Can(user, ActionRead, "docs/page.md"))
	}
	assert.False(t, Can(nil, ActionRead, "docs"))
	assert.False(t, Can(&User{Role: "unknown"}, ActionRead, "docs"))
	assert.False(t, Can(&User{Role: RoleAdmin}, "unknown", "docs"))
	grants, err := normalizeGrants(&Grants{Read: []string{"docs", "docs"}, Write: []string{"src"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"docs"}, grants.Read)
	assert.Equal(t, []string{"src"}, grants.Write)
	_, err = normalizeGrants(&Grants{Read: []string{strings.Repeat("x", 257)}})
	assert.Error(t, err)
	_, err = normalizeGrants(&Grants{Read: make([]string, 33)})
	assert.Error(t, err)
}

func TestBootstrapManagementAndLastAdminInvariant(t *testing.T) {
	service, _ := testService(t)
	assert.True(t, service.NeedsSetup())
	admin := testAdmin(t, service)
	assert.False(t, service.NeedsSetup())
	_, err := service.Bootstrap(context.Background(), "another", "Another", []byte(testPassword))
	assert.ErrorIs(t, err, ErrConflict)
	viewer := testUser(t, service, &admin, "viewer", RoleViewer)
	_, err = service.createUser(context.Background(), &admin, CreateUserRequest{Username: "VIEWER", Password: testPassword, Role: RoleEditor})
	assert.ErrorIs(t, err, ErrConflict)
	_, err = service.listUsers(nil)
	assert.ErrorIs(t, err, ErrUnauthorized)
	_, err = service.listUsers(&viewer)
	assert.ErrorIs(t, err, ErrForbidden)
	users, err := service.listUsers(&admin)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "admin", users[0].Username)
	assert.ErrorIs(t, service.deleteUser(&admin, admin.ID), ErrLastAdmin)
	disabled := true
	role := RoleEditor
	_, err = service.updateUser(context.Background(), &admin, admin.ID, UpdateUserRequest{Disabled: &disabled})
	assert.ErrorIs(t, err, ErrLastAdmin)
	_, err = service.updateUser(context.Background(), &admin, admin.ID, UpdateUserRequest{Role: &role})
	assert.ErrorIs(t, err, ErrLastAdmin)
	_, err = service.updateUser(context.Background(), &admin, viewer.ID, UpdateUserRequest{})
	assert.ErrorIs(t, err, ErrInvalid)
	second := testUser(t, service, &admin, "second", RoleAdmin)
	require.NoError(t, service.deleteUser(&second, admin.ID))
	_, err = service.listUsers(&admin)
	assert.ErrorIs(t, err, ErrUnauthorized)
	assert.ErrorIs(t, service.deleteUser(&second, "missing"), ErrNotFound)
}

func TestSessionsPersistExpireAndContainOnlyHashes(t *testing.T) {
	service, clock := testService(t)
	admin := testAdmin(t, service)
	_, token, err := service.login(context.Background(), "ADMIN", testPassword, "127.0.0.1")
	require.NoError(t, err)
	csrf := service.csrfToken(token)
	data, err := os.ReadFile(filepath.Join(service.config.StateDir, stateFile))
	require.NoError(t, err)
	assert.NotContains(t, string(data), testPassword)
	assert.NotContains(t, string(data), token)
	assert.NotContains(t, string(data), csrf)
	assert.Contains(t, string(data), sessionHash(token))
	info, err := os.Stat(filepath.Join(service.config.StateDir, stateFile))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	config := service.config
	require.NoError(t, service.Close())
	reopened, err := openService(config, runtimeOptions{iterations: 2, now: clock.now, derive: derivePassword})
	require.NoError(t, err)
	defer func() { _ = reopened.Close() }()
	user, err := reopened.Resolve(testRequest(reopened, token, http.MethodGet, "/session"))
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, admin.ID, user.ID)
	assert.Equal(t, csrf, reopened.csrfToken(token))
	clock.advance(25 * time.Hour)
	user, err = reopened.Resolve(testRequest(reopened, token, http.MethodGet, "/session"))
	require.NoError(t, err)
	assert.Nil(t, user)
	_, next, err := reopened.login(context.Background(), "admin", testPassword, "127.0.0.1")
	require.NoError(t, err)
	assert.Len(t, reopened.state.Sessions, 1)
	require.NoError(t, reopened.logout(next))
	require.NoError(t, reopened.logout(next))
	assert.Empty(t, reopened.state.Sessions)
}

func TestRevocationOnPasswordRoleDisabledAndGrants(t *testing.T) {
	for _, kind := range []string{"password", "role", "disabled", "grants"} {
		t.Run(kind, func(t *testing.T) {
			service, _ := testService(t)
			admin := testAdmin(t, service)
			editor := testUser(t, service, &admin, "editor", RoleEditor)
			_, token, err := service.login(context.Background(), "editor", testPassword, "127.0.0.1")
			require.NoError(t, err)
			patch := UpdateUserRequest{}
			switch kind {
			case "password":
				password := nextPassword
				patch.Password = &password
			case "role":
				role := RoleViewer
				patch.Role = &role
			case "disabled":
				disabled := true
				patch.Disabled = &disabled
			case "grants":
				patch.Grants = json.RawMessage(`{"read":["docs"],"write":[]}`)
			}
			_, err = service.updateUser(context.Background(), &admin, editor.ID, patch)
			require.NoError(t, err)
			user, err := service.Resolve(testRequest(service, token, http.MethodGet, "/session"))
			require.NoError(t, err)
			assert.Nil(t, user)
			assert.Empty(t, service.state.Sessions)
		})
	}
}

func TestUnknownUsersUseDummyKDFAndLoginRateLimitsAreBounded(t *testing.T) {
	service, clock := testService(t)
	testAdmin(t, service)
	var calls atomic.Int64
	service.runtime.derive = func(password string, salt []byte, iterations int) ([]byte, error) {
		calls.Add(1)
		assert.Len(t, salt, 32)
		assert.Equal(t, 2, iterations)
		return derivePassword(password, salt, iterations)
	}
	for _, username := range []string{"unknown", "admin"} {
		_, _, err := service.login(context.Background(), username, "wrong password", "127.0.0.1")
		assert.ErrorIs(t, err, ErrUnauthorized)
	}
	assert.Equal(t, int64(2), calls.Load())
	for i := 0; i < 4; i++ {
		_, _, err := service.login(context.Background(), "unknown", "wrong", "127.0.0.1")
		assert.ErrorIs(t, err, ErrUnauthorized)
	}
	_, _, err := service.login(context.Background(), "unknown", "wrong", "another-client")
	assert.ErrorIs(t, err, ErrRateLimited)
	clock.advance(time.Minute)
	_, _, err = service.login(context.Background(), "unknown", "wrong", "127.0.0.1")
	assert.ErrorIs(t, err, ErrUnauthorized)
	for i := 0; i < 10000; i++ {
		service.allowLogin(fmt.Sprintf("client-%d", i), fmt.Sprintf("name-%d", i))
	}
	assert.LessOrEqual(t, len(service.rates), 1024)
	assert.LessOrEqual(t, service.rateCount, 60)
}

func TestConcurrentKDFLimitAndPasswordChangeLoginRace(t *testing.T) {
	t.Run("bounded hashers", func(t *testing.T) {
		service, _ := testService(t)
		testAdmin(t, service)
		started := make(chan struct{}, 2)
		release := make(chan struct{})
		service.runtime.derive = func(password string, salt []byte, iterations int) ([]byte, error) {
			started <- struct{}{}
			<-release
			return derivePassword(password, salt, iterations)
		}
		results := make(chan error, 2)
		for _, username := range []string{"unknown-one", "unknown-two"} {
			go func() {
				_, _, err := service.login(context.Background(), username, "wrong", username)
				results <- err
			}()
		}
		<-started
		<-started
		_, _, err := service.login(context.Background(), "unknown-three", "wrong", "three")
		assert.ErrorIs(t, err, ErrBusy)
		close(release)
		assert.ErrorIs(t, <-results, ErrUnauthorized)
		assert.ErrorIs(t, <-results, ErrUnauthorized)
	})
	t.Run("old password cannot create session after reset", func(t *testing.T) {
		service, _ := testService(t)
		admin := testAdmin(t, service)
		editor := testUser(t, service, &admin, "editor", RoleEditor)
		started, release := make(chan struct{}), make(chan struct{})
		service.runtime.derive = func(password string, salt []byte, iterations int) ([]byte, error) {
			if password == testPassword {
				close(started)
				<-release
			}
			return derivePassword(password, salt, iterations)
		}
		result := make(chan error, 1)
		go func() {
			_, _, err := service.login(context.Background(), "editor", testPassword, "client")
			result <- err
		}()
		<-started
		password := nextPassword
		_, err := service.updateUser(context.Background(), &admin, editor.ID, UpdateUserRequest{Password: &password})
		require.NoError(t, err)
		close(release)
		assert.ErrorIs(t, <-result, ErrUnauthorized)
		assert.Empty(t, service.state.Sessions)
	})
}

func TestHashCancellationAndStorageFailure(t *testing.T) {
	service, _ := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Bootstrap(ctx, "admin", "Admin", []byte(testPassword))
	assert.ErrorIs(t, err, context.Canceled)
	service.runtime.derive = func(string, []byte, int) ([]byte, error) { return nil, errors.New("private KDF failure") }
	_, err = service.Bootstrap(context.Background(), "admin", "Admin", []byte(testPassword))
	assert.ErrorIs(t, err, ErrStorage)
	service.runtime.derive = derivePassword
	require.NoError(t, service.root.Close())
	_, err = service.Bootstrap(context.Background(), "admin", "Admin", []byte(testPassword))
	assert.ErrorIs(t, err, ErrStorage)
	assert.True(t, service.NeedsSetup())
}
