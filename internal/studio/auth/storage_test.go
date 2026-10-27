package auth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicURLAndConfigurationBoundaries(t *testing.T) {
	for _, input := range []struct {
		url    string
		origin string
		secure bool
	}{
		{"https://Docs.Example.test:443/team/", "https://docs.example.test", true},
		{"http://localhost:80/team/product/", "http://localhost", false},
		{"https://127.2.3.4:8080", "https://127.2.3.4:8080", true},
		{"https://[::1]:8080/", "https://[::1]:8080", true},
	} {
		origin, secure, err := sameOrigin(input.url)
		require.NoError(t, err)
		assert.Equal(t, input.origin, origin)
		assert.Equal(t, input.secure, secure)
	}
	for _, value := range []string{"", "ftp://example.test", "http://example.test", "http://127.0.0.1", "http://[::1]", "http://localhost.", "https://user:password@example.test", "https://example.test/a/../b", "https://example.test/?q=x", "https://example.test/#fragment", "https://:443", "https://example.test:bad", "https:opaque", "http://[::1%25zone]"} {
		_, _, err := sameOrigin(value)
		assert.Error(t, err, value)
	}
	base := testConfig(t)
	for _, alter := range []func(*Config){
		func(config *Config) { config.StateDir = "" },
		func(config *Config) { config.StateDir = "relative" },
		func(config *Config) { config.PublicURL = "http://public.example" },
		func(config *Config) { config.SessionTTL = time.Second },
		func(config *Config) { config.SessionTTL = 31 * 24 * time.Hour },
		func(config *Config) { config.MaxUsers = -1 },
		func(config *Config) { config.MaxUsers = 257 },
		func(config *Config) { config.MaxSessions = -1 },
		func(config *Config) { config.MaxSessions = 4097 },
	} {
		config := base
		alter(&config)
		service, err := Open(config)
		assert.Error(t, err)
		assert.Nil(t, service)
	}
	_, err := openService(base, runtimeOptions{})
	assert.ErrorIs(t, err, ErrInvalid)
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, []byte("not a directory"), 0600))
	base.StateDir = file
	_, err = Open(base)
	assert.Error(t, err)
}

func TestPrivateDirectoryValidatorIsReadOnlyAndRejectsSymlinks(t *testing.T) {
	private := filepath.Join(t.TempDir(), "private")
	assert.Error(t, CheckPrivateDirectory(""))
	assert.Error(t, CheckPrivateDirectory(private))
	_, err := os.Stat(private)
	assert.True(t, os.IsNotExist(err))
	require.NoError(t, os.Mkdir(private, 0700))
	require.NoError(t, CheckPrivateDirectory(private))
	file := filepath.Join(private, "file")
	require.NoError(t, os.WriteFile(file, []byte("sentinel"), 0600))
	assert.Error(t, CheckPrivateDirectory(file))
	root, err := os.OpenRoot(private)
	require.NoError(t, err)
	require.NoError(t, checkOpenedDirectory(root))
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Chmod(private, 0755))
		assert.Error(t, CheckPrivateDirectory(private))
		assert.Error(t, checkOpenedDirectory(root))
		info, err := os.Stat(private)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
		require.NoError(t, os.Chmod(private, 0700))
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(private, link))
		assert.Error(t, CheckPrivateDirectory(link))
		assert.Error(t, CheckPrivateDirectory(link+string(filepath.Separator)))
	}
	require.NoError(t, root.Close())
	assert.Error(t, checkOpenedDirectory(root))
}

func TestExclusiveStateDirectoryLockAndPrivacy(t *testing.T) {
	service, clock := testService(t)
	config := service.config
	_, err := openService(config, runtimeOptions{iterations: 2, now: clock.now, derive: derivePassword})
	require.ErrorContains(t, err, "another process")
	require.NoError(t, service.Close())
	require.NoError(t, service.Close())
	reopened, err := openService(config, runtimeOptions{iterations: 2, now: clock.now, derive: derivePassword})
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Chmod(config.StateDir, 0755))
		_, err := Open(config)
		assert.ErrorContains(t, err, "private")
		require.NoError(t, os.Chmod(config.StateDir, 0700))
		require.NoError(t, os.Chmod(filepath.Join(config.StateDir, "auth.lock"), 0644))
		_, err = Open(config)
		assert.ErrorContains(t, err, "private")
	}
}

func TestAuthenticationStateRejectsCorruptionWithoutReset(t *testing.T) {
	config := testConfig(t)
	service, err := Open(config)
	require.NoError(t, err)
	admin := testAdmin(t, service)
	_, token, err := service.login(context.Background(), admin.Username, testPassword, "client")
	require.NoError(t, err)
	baseline, err := os.ReadFile(filepath.Join(config.StateDir, stateFile))
	require.NoError(t, err)
	require.NoError(t, service.Close())
	mutations := []struct {
		name   string
		mutate func(*diskState)
	}{
		{"version", func(state *diskState) { state.Version = 2 }},
		{"nil users", func(state *diskState) { state.Users = nil }},
		{"nil sessions", func(state *diskState) { state.Sessions = nil }},
		{"CSRF key", func(state *diskState) { state.CSRFKey = "bad" }},
		{"user ID", func(state *diskState) { user := state.Users[admin.ID]; user.ID = "bad"; state.Users[admin.ID] = user }},
		{"user version", func(state *diskState) { user := state.Users[admin.ID]; user.Version = 0; state.Users[admin.ID] = user }},
		{"username case", func(state *diskState) {
			user := state.Users[admin.ID]
			user.Username = "ADMIN"
			state.Users[admin.ID] = user
		}},
		{"duplicate username", func(state *diskState) {
			user := state.Users[admin.ID]
			user.ID = strings.Repeat("a", 32)
			state.Users[user.ID] = user
		}},
		{"display controls", func(state *diskState) {
			user := state.Users[admin.ID]
			user.DisplayName = "bad\nname"
			state.Users[admin.ID] = user
		}},
		{"algorithm", func(state *diskState) {
			user := state.Users[admin.ID]
			user.Password.Algorithm = "plaintext"
			state.Users[admin.ID] = user
		}},
		{"KDF floor", func(state *diskState) {
			user := state.Users[admin.ID]
			user.Password.Iterations = 599999
			state.Users[admin.ID] = user
		}},
		{"KDF ceiling", func(state *diskState) {
			user := state.Users[admin.ID]
			user.Password.Iterations = 2000001
			state.Users[admin.ID] = user
		}},
		{"salt", func(state *diskState) {
			user := state.Users[admin.ID]
			user.Password.Salt = "bad"
			state.Users[admin.ID] = user
		}},
		{"hash", func(state *diskState) {
			user := state.Users[admin.ID]
			user.Password.Hash = "bad"
			state.Users[admin.ID] = user
		}},
		{"no enabled admin", func(state *diskState) {
			user := state.Users[admin.ID]
			user.Disabled = true
			state.Users[admin.ID] = user
		}},
		{"session hash", func(state *diskState) {
			session := state.Sessions[sessionHash(token)]
			delete(state.Sessions, sessionHash(token))
			state.Sessions["bad"] = session
		}},
		{"session user", func(state *diskState) {
			session := state.Sessions[sessionHash(token)]
			session.UserID = "missing"
			state.Sessions[sessionHash(token)] = session
		}},
		{"session version", func(state *diskState) {
			session := state.Sessions[sessionHash(token)]
			session.Version++
			state.Sessions[sessionHash(token)] = session
		}},
		{"session expiry", func(state *diskState) {
			session := state.Sessions[sessionHash(token)]
			session.Expires = session.Created
			state.Sessions[sessionHash(token)] = session
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			var state diskState
			require.NoError(t, json.Unmarshal(baseline, &state))
			test.mutate(&state)
			data, err := json.Marshal(state)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(config.StateDir, stateFile), data, 0600))
			_, err = Open(config)
			require.Error(t, err)
			current, err := os.ReadFile(filepath.Join(config.StateDir, stateFile))
			require.NoError(t, err)
			assert.Equal(t, data, current)
		})
	}
	for _, raw := range [][]byte{[]byte("{"), []byte("{} {}"), append(append([]byte{}, baseline[:len(baseline)-1]...), []byte(`,"unknown":true}`)...), {0xff}, []byte(strings.Repeat("x", maxStateBytes+1))} {
		require.NoError(t, os.WriteFile(filepath.Join(config.StateDir, stateFile), raw, 0600))
		_, err := Open(config)
		assert.Error(t, err)
	}
	require.NoError(t, os.WriteFile(filepath.Join(config.StateDir, stateFile), baseline, 0600))
	reopened, err := Open(config)
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Chmod(filepath.Join(config.StateDir, stateFile), 0644))
		_, err := Open(config)
		assert.ErrorContains(t, err, "private regular file")
	}
}

func TestAuthenticationStateRejectsSymlinksAndDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating Windows symlinks may require elevated privileges")
	}
	for _, name := range []string{"auth.lock", stateFile} {
		t.Run(name, func(t *testing.T) {
			config := testConfig(t)
			require.NoError(t, os.MkdirAll(config.StateDir, 0700))
			outside := filepath.Join(t.TempDir(), "outside")
			require.NoError(t, os.WriteFile(outside, []byte("outside sentinel"), 0600))
			require.NoError(t, os.Symlink(outside, filepath.Join(config.StateDir, name)))
			_, err := Open(config)
			require.Error(t, err)
			data, err := os.ReadFile(outside)
			require.NoError(t, err)
			assert.Equal(t, "outside sentinel", string(data))
		})
	}
	config := testConfig(t)
	require.NoError(t, os.MkdirAll(filepath.Join(config.StateDir, stateFile), 0700))
	_, err := Open(config)
	assert.Error(t, err)
}

func TestSessionAndUserCapacityDoNotGrowWithoutBound(t *testing.T) {
	t.Run("global session cap", func(t *testing.T) {
		clock := newTestClock()
		config := testConfig(t)
		config.MaxSessions = 1
		service, err := openService(config, runtimeOptions{iterations: 2, now: clock.now, derive: derivePassword})
		require.NoError(t, err)
		defer func() { _ = service.Close() }()
		testAdmin(t, service)
		_, token, err := service.login(context.Background(), "admin", testPassword, "client")
		require.NoError(t, err)
		_, _, err = service.login(context.Background(), "admin", testPassword, "client")
		assert.ErrorIs(t, err, ErrBusy)
		assert.Len(t, service.state.Sessions, 1)
		require.NoError(t, service.logout(token))
		_, _, err = service.login(context.Background(), "admin", testPassword, "client")
		require.NoError(t, err)
	})
	t.Run("per-user cap evicts oldest", func(t *testing.T) {
		service, clock := testService(t)
		testAdmin(t, service)
		first := ""
		for i := 0; i < maxSessionsPerUser+1; i++ {
			_, token, err := service.login(context.Background(), "admin", testPassword, "client")
			require.NoError(t, err)
			if i == 0 {
				first = token
			}
			clock.advance(time.Minute)
		}
		assert.Len(t, service.state.Sessions, maxSessionsPerUser)
		_, present := service.state.Sessions[sessionHash(first)]
		assert.False(t, present)
	})
	t.Run("user cap", func(t *testing.T) {
		clock := newTestClock()
		config := testConfig(t)
		config.MaxUsers = 2
		service, err := openService(config, runtimeOptions{iterations: 2, now: clock.now, derive: derivePassword})
		require.NoError(t, err)
		defer func() { _ = service.Close() }()
		admin := testAdmin(t, service)
		testUser(t, service, &admin, "viewer", RoleViewer)
		_, err = service.createUser(context.Background(), &admin, CreateUserRequest{Username: "another", Password: testPassword, Role: RoleEditor})
		assert.ErrorIs(t, err, ErrConflict)
		assert.Len(t, service.state.Users, 2)
	})
}

func TestUserPatchesValidationAndDefensiveCopies(t *testing.T) {
	service, _ := testService(t)
	admin := testAdmin(t, service)
	request := CreateUserRequest{Username: "editor", Password: testPassword, Role: RoleEditor, Grants: &Grants{Read: []string{"docs"}, Write: []string{"docs"}}}
	editor, err := service.createUser(context.Background(), &admin, request)
	require.NoError(t, err)
	request.Grants.Read[0] = "private"
	editor.Grants.Read[0] = "private"
	assert.Equal(t, "docs", service.state.Users[editor.ID].Grants.Read[0])
	users, err := service.listUsers(&admin)
	require.NoError(t, err)
	for _, user := range users {
		if user.Grants != nil {
			user.Grants.Read[0] = "private"
		}
	}
	assert.Equal(t, "docs", service.state.Users[editor.ID].Grants.Read[0])
	for _, grants := range []string{`{`, `[]`, `{} {}`, `{"other":[]}`, `{"Read":[]}`, `{"read":[],"Read":["."]}`, `{"read":["../outside"]}`} {
		_, err := service.updateUser(context.Background(), &admin, editor.ID, UpdateUserRequest{Grants: json.RawMessage(grants)})
		assert.ErrorIs(t, err, ErrInvalid)
	}
	for _, patch := range []UpdateUserRequest{
		{DisplayName: pointer("")}, {DisplayName: pointer("bad\nname")}, {Role: pointer(Role("owner"))}, {Password: pointer("short")},
	} {
		_, err := service.updateUser(context.Background(), &admin, editor.ID, patch)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	updated, err := service.updateUser(context.Background(), &admin, editor.ID, UpdateUserRequest{Grants: json.RawMessage(`null`)})
	require.NoError(t, err)
	assert.Nil(t, updated.Grants)
	updated, err = service.updateUser(context.Background(), &admin, editor.ID, UpdateUserRequest{Disabled: pointer(true)})
	require.NoError(t, err)
	assert.True(t, updated.Disabled)
	_, _, err = service.login(context.Background(), "editor", testPassword, "client")
	assert.ErrorIs(t, err, ErrUnauthorized)
	_, err = service.updateUser(context.Background(), &admin, "missing", UpdateUserRequest{DisplayName: pointer("Someone")})
	assert.ErrorIs(t, err, ErrNotFound)
	badActor := admin
	badActor.version++
	_, err = service.createUser(context.Background(), &badActor, CreateUserRequest{Username: "denied", Password: testPassword, Role: RoleViewer})
	assert.ErrorIs(t, err, ErrUnauthorized)
}

func pointer[T any](value T) *T { return &value }

func TestAtomicPersistenceFailureDoesNotChangeAuthority(t *testing.T) {
	service, _ := testService(t)
	admin := testAdmin(t, service)
	next := cloneState(service.state)
	next.CSRFKey = strings.Repeat("x", maxStateBytes+1)
	assert.ErrorIs(t, service.commitLocked(next), ErrStorage)
	assert.Len(t, service.state.Users, 1)
	require.NoError(t, service.root.Remove(stateFile))
	require.NoError(t, service.root.Mkdir(stateFile, 0700))
	_, err := service.createUser(context.Background(), &admin, CreateUserRequest{Username: "uncommitted", Password: testPassword, Role: RoleViewer})
	assert.ErrorIs(t, err, ErrStorage)
	assert.Len(t, service.state.Users, 1)
	files, err := os.ReadDir(service.config.StateDir)
	require.NoError(t, err)
	for _, file := range files {
		assert.False(t, strings.HasPrefix(file.Name(), ".auth-"))
	}
}
