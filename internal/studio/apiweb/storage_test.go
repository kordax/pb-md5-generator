package apiweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStorageFixture(t *testing.T, beforeAuthorize func(context.Context, content.Actor) error) *fixture {
	t.Helper()
	directory := t.TempDir()
	root, state, output := filepath.Join(directory, "sources"), filepath.Join(directory, "state"), filepath.Join(directory, "site")
	backend, err := content.NewManagedBackend(content.ManagedStorageConfig{Root: root, StateDir: state, InitialBackend: "local", InitialRemote: "origin"})
	require.NoError(t, err)
	service, err := content.New(content.Config{Root: root, StateDir: state, OutputDir: output, Initialize: true, Storage: backend, Compile: func(ctx context.Context, manifest, output string) error {
		return site.BuildContext(ctx, manifest, output, site.Options{})
	}, AuthorizeStorage: func(ctx context.Context, actor content.Actor) error {
		if beforeAuthorize != nil {
			if err := beforeAuthorize(ctx, actor); err != nil {
				return err
			}
		}
		user, err := Principal(ctx)
		if err != nil {
			return auth.ErrUnauthorized
		}
		if user == nil {
			return auth.ErrForbidden
		}
		if !storageAccess(user) || actor.ID != "" && actor.ID != user.ID {
			return auth.ErrForbidden
		}
		return nil
	}})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	f := newFixture(t, func(options *Options) { options.Content = service })
	f.content, f.root, f.output = service, root, output
	return f
}

func storageConfiguration(t *testing.T, f *fixture) content.StorageResult {
	t.Helper()
	return resultJSON[content.StorageResult](t, f.request(http.MethodGet, testAPI+"/storage/configuration", nil, &f.admin, nil), http.StatusOK)
}

func storageUpdate(current content.StorageResult, backend string, remotes []content.StorageRemote) content.StorageUpdate {
	return content.StorageUpdate{Revision: current.Settings.Revision, BaseRevision: current.ProjectRevision, Backend: backend, PullRemote: "", Remotes: remotes}
}

func storageGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return strings.TrimSpace(string(output))
}

func TestStorageConfigurationRequiresRootAdministratorAndKeepsMaskedStatus(t *testing.T) {
	f := newStorageFixture(t, nil)
	current := storageConfiguration(t, f)
	viewer := f.createUser(t, "storage-viewer", auth.RoleViewer, nil)
	editor := f.createUser(t, "storage-editor", auth.RoleEditor, nil)
	readScoped := f.createUser(t, "storage-read-admin", auth.RoleAdmin, &auth.Grants{Read: []string{"public"}, Write: []string{"."}})
	writeScoped := f.createUser(t, "storage-write-admin", auth.RoleAdmin, &auth.Grants{Read: []string{"."}, Write: []string{"public"}})
	for _, identity := range []*credentials{nil, &viewer, &editor, &readScoped, &writeScoped} {
		want := http.StatusForbidden
		if identity == nil {
			want = http.StatusUnauthorized
		}
		for _, operation := range []struct {
			method string
			path   string
			body   any
		}{
			{http.MethodGet, "/storage/configuration", nil},
			{http.MethodPut, "/storage/configuration", storageUpdate(current, "local", []content.StorageRemote{})},
			{http.MethodPost, "/storage/remotes/origin/check", map[string]string{"revision": current.Settings.Revision}},
			{http.MethodPost, "/storage/remotes/origin/push", map[string]string{"revision": current.Settings.Revision, "baseRevision": current.ProjectRevision}},
			{http.MethodGet, "/branches", nil},
			{http.MethodPost, "/branches", map[string]string{"name": "other", "revision": current.ProjectRevision}},
			{http.MethodPost, "/branches/checkout", map[string]string{"name": "other", "revision": current.ProjectRevision}},
			{http.MethodPost, "/sync", map[string]string{"action": "fetch", "revision": current.ProjectRevision}},
		} {
			response := f.request(operation.method, testAPI+operation.path, operation.body, identity, nil)
			assert.Equal(t, want, response.Code, "%s %s: %s", operation.method, operation.path, response.Body.String())
			assert.NotContains(t, response.Body.String(), f.root)
		}
	}
	initial := f.request(http.MethodGet, testAPI+"/storage/configuration", nil, &f.admin, nil)
	assert.Equal(t, "\""+current.Settings.Revision+"\"", initial.Header().Get("ETag"))
	var shape struct {
		Settings struct {
			Remotes json.RawMessage `json:"remotes"`
		} `json:"settings"`
		Remotes json.RawMessage `json:"remotes"`
	}
	require.NoError(t, json.Unmarshal(initial.Body.Bytes(), &shape))
	assert.JSONEq(t, "[]", string(shape.Settings.Remotes))
	assert.JSONEq(t, "[]", string(shape.Remotes))
	head := f.request(http.MethodHead, testAPI+"/storage/configuration", nil, &f.admin, nil)
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.String())
	assert.Equal(t, initial.Header().Get("ETag"), head.Header().Get("ETag"))

	configured := resultJSON[content.StorageResult](t, f.request(http.MethodPut, testAPI+"/storage/configuration", storageUpdate(current, "git", []content.StorageRemote{}), &f.admin, nil), http.StatusOK)
	assert.Equal(t, "main", configured.Sync.Branch)
	for _, identity := range []*credentials{&viewer, &editor, &readScoped, &writeScoped} {
		status := resultJSON[syncDTO](t, f.request(http.MethodGet, testAPI+"/sync/status", nil, identity, nil), http.StatusOK)
		assert.Equal(t, "git", status.Backend)
		assert.Equal(t, configured.ProjectRevision, status.Revision)
		assert.Empty(t, status.Branch)
		assert.Empty(t, status.LastCommit)
		assert.Empty(t, status.LastError)
		assert.False(t, status.Dirty)
		assert.False(t, status.PendingPush)
	}
	asset := f.request(http.MethodGet, testBase+"/_docs/admin/storage.mjs", nil, nil, nil)
	assert.Equal(t, http.StatusOK, asset.Code)
	assert.Contains(t, asset.Header().Get("Content-Type"), "javascript")
	assert.Contains(t, asset.Body.String(), "Storage")
}

func TestStorageRequestsEnforcePresenceStrictJSONOriginAndIndependentCAS(t *testing.T) {
	f := newStorageFixture(t, nil)
	current := storageConfiguration(t, f)
	remote := filepath.Join(t.TempDir(), "remote.git")
	storageGit(t, filepath.Dir(remote), "init", "--bare", remote)
	valid := map[string]any{"revision": current.Settings.Revision, "baseRevision": current.ProjectRevision, "backend": "git", "pullRemote": "origin", "remotes": []any{map[string]any{"name": "origin", "url": remote, "push": false}}}
	for _, key := range []string{"revision", "baseRevision", "backend", "pullRemote", "remotes"} {
		input := make(map[string]any, len(valid))
		for name, value := range valid {
			if name != key {
				input[name] = value
			}
		}
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPut, testAPI+"/storage/configuration", input, &f.admin, nil).Code, key)
	}
	for _, key := range []string{"name", "url", "push"} {
		item := map[string]any{"name": "origin", "url": remote, "push": false}
		delete(item, key)
		input := storageUpdate(current, "git", []content.StorageRemote{})
		data, err := json.Marshal(input)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(data, &body))
		body["remotes"] = []any{item}
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPut, testAPI+"/storage/configuration", body, &f.admin, nil).Code, key)
	}
	encoded, err := json.Marshal(valid)
	require.NoError(t, err)
	for _, data := range [][]byte{
		[]byte(`{"revision":null}`),
		[]byte(strings.Replace(string(encoded), `"remotes":[`, `"remotes":null,"ignored":[`, 1)),
		[]byte(strings.Replace(string(encoded), `"push":false`, `"push":null`, 1)),
		[]byte(strings.TrimSuffix(string(encoded), "}") + `,"privateKey":"secret"}`),
		[]byte(strings.TrimSuffix(string(encoded), "}") + `,"revision":"` + current.Settings.Revision + `"}`),
		[]byte(strings.Replace(string(encoded), `"revision":`, `"Revision":`, 1)),
	} {
		response := f.request(http.MethodPut, testAPI+"/storage/configuration", data, &f.admin, nil)
		assert.Equal(t, http.StatusBadRequest, response.Code, "%s", data)
		assert.NotContains(t, response.Body.String(), "secret")
	}
	assert.Equal(t, http.StatusUnsupportedMediaType, f.request(http.MethodPut, testAPI+"/storage/configuration", valid, &f.admin, map[string]string{"Content-Type": "text/plain"}).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPut, testAPI+"/storage/configuration", valid, &f.admin, map[string]string{"X-CSRF-Token": ""}).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPut, testAPI+"/storage/configuration", valid, &f.admin, map[string]string{"Origin": ""}).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPut, testAPI+"/storage/configuration", valid, &f.admin, map[string]string{"Origin": "https://untrusted.example"}).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testAPI+"/storage/configuration?refresh=true", nil, &f.admin, nil).Code)
	oversized := []byte(`{"unused":"` + strings.Repeat("x", maxStorageJSONBody) + `"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, f.request(http.MethodPut, testAPI+"/storage/configuration", oversized, &f.admin, nil).Code)
	method := f.request(http.MethodPost, testAPI+"/storage/configuration", valid, &f.admin, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, method.Code)
	assert.Equal(t, "GET, HEAD, PUT", method.Header().Get("Allow"))
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/storage/unknown", nil, &f.admin, nil).Code)

	configured := resultJSON[content.StorageResult](t, f.request(http.MethodPut, testAPI+"/storage/configuration", valid, &f.admin, nil), http.StatusOK)
	require.Len(t, configured.Settings.Remotes, 1)
	assert.False(t, configured.Settings.Remotes[0].Push, "explicit false must be accepted")
	assert.NotEqual(t, current.Settings.Revision, configured.Settings.Revision)
	stale := f.request(http.MethodPut, testAPI+"/storage/configuration", valid, &f.admin, nil)
	assert.Equal(t, http.StatusConflict, stale.Code)
	assert.NotContains(t, stale.Body.String(), "currentRevision")
	assert.Equal(t, configured.Settings.Revision, storageConfiguration(t, f).Settings.Revision)
}

func TestStorageRemoteAttemptsUseSafeStatusAndSeparateSettingsAndProjectRevisions(t *testing.T) {
	f := newStorageFixture(t, nil)
	current := storageConfiguration(t, f)
	remote := filepath.Join(t.TempDir(), "origin.git")
	storageGit(t, filepath.Dir(remote), "init", "--bare", remote)
	missing := filepath.Join(t.TempDir(), "unavailable-private-target.git")
	update := storageUpdate(current, "git", []content.StorageRemote{{Name: "origin", URL: remote, Push: true}, {Name: "mirror", URL: missing, Push: true}})
	update.PullRemote = "origin"
	configured := resultJSON[content.StorageResult](t, f.request(http.MethodPut, testAPI+"/storage/configuration", update, &f.admin, nil), http.StatusOK)
	probe := resultJSON[content.RemoteSyncStatus](t, f.request(http.MethodPost, testAPI+"/storage/remotes/origin/check", map[string]string{"revision": configured.Settings.Revision}, &f.admin, nil), http.StatusOK)
	assert.Equal(t, "ready", probe.State)
	assert.Empty(t, probe.Error)
	push := map[string]string{"revision": configured.Settings.Revision, "baseRevision": configured.ProjectRevision}
	pushed := resultJSON[content.RemoteSyncStatus](t, f.request(http.MethodPost, testAPI+"/storage/remotes/origin/push", push, &f.admin, nil), http.StatusOK)
	assert.Equal(t, "ready", pushed.State)
	assert.Equal(t, pushed.Head, pushed.LastPushedHead)
	assert.NotEmpty(t, storageGit(t, remote, "rev-parse", "refs/heads/main"))
	failure := resultJSON[content.RemoteSyncStatus](t, f.request(http.MethodPost, testAPI+"/storage/remotes/mirror/push", push, &f.admin, nil), http.StatusOK)
	assert.Equal(t, "error", failure.State)
	assert.True(t, failure.Retryable)
	assert.NotEmpty(t, failure.Error)
	assert.NotContains(t, failure.Error, missing)
	assert.NotContains(t, failure.Error, "fatal:")
	assert.Equal(t, configured.ProjectRevision, storageConfiguration(t, f).ProjectRevision)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/storage/remotes/origin/check", map[string]string{"revision": current.Settings.Revision}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/storage/remotes/origin/push", map[string]string{"revision": configured.Settings.Revision, "baseRevision": strings.Repeat("0", 64)}, &f.admin, nil).Code)
	for _, operation := range []struct {
		path string
		body any
	}{
		{"check", map[string]any{}},
		{"check", map[string]string{"revision": "invalid"}},
		{"check", map[string]string{"revision": configured.Settings.Revision, "baseRevision": configured.ProjectRevision}},
		{"push", map[string]string{"revision": configured.Settings.Revision}},
		{"push", map[string]string{"baseRevision": configured.ProjectRevision}},
		{"push", map[string]string{"revision": configured.Settings.Revision, "baseRevision": "invalid"}},
	} {
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/storage/remotes/origin/"+operation.path, operation.body, &f.admin, nil).Code)
	}
	wrongMethod := f.request(http.MethodGet, testAPI+"/storage/remotes/origin/check", nil, &f.admin, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, wrongMethod.Code)
	assert.Equal(t, "POST", wrongMethod.Header().Get("Allow"))
}

func TestStorageEnrollmentKeepsUnpublishedDocumentsOnTheInitialBranchOnly(t *testing.T) {
	f := newStorageFixture(t, nil)
	base := f.current(t, &f.admin)
	home := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "# Local unsaved home", "revision": base.Revision}, &f.admin, nil), http.StatusOK)
	created := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents", map[string]string{"path": "guide/new.md", "title": "Local new document", "kind": "markdown", "content": "# Local unpublished document", "revision": base.Revision}, &f.admin, nil), http.StatusCreated)
	require.NotNil(t, home.Draft)
	require.NotNil(t, created.Draft)
	require.NotNil(t, created.Document)
	current := storageConfiguration(t, f)
	configured := resultJSON[content.StorageResult](t, f.request(http.MethodPut, testAPI+"/storage/configuration", storageUpdate(current, "git", []content.StorageRemote{}), &f.admin, nil), http.StatusOK)
	local := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+created.Document.ID, nil, &f.admin, nil), http.StatusOK)
	assert.Equal(t, created.Draft.ID, local.DraftID)
	assert.Equal(t, created.Draft.Revision, local.DraftRevision)
	assert.Equal(t, base.Revision, local.BaseRevision)
	assert.Equal(t, "# Local unpublished document", local.Content)
	listed := resultJSON[struct {
		Documents []Document `json:"documents"`
	}](t, f.request(http.MethodGet, testAPI+"/documents", nil, &f.admin, nil), http.StatusOK)
	assert.Condition(t, func() bool {
		for _, doc := range listed.Documents {
			if doc.ID == created.Document.ID {
				return true
			}
		}
		return false
	})
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/drafts/"+created.Draft.ID+"/publish", map[string]string{"revision": created.Draft.Revision}, &f.admin, nil).Code)
	resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches", map[string]string{"name": "other", "revision": configured.ProjectRevision}, &f.admin, nil), http.StatusOK)
	resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches/checkout", map[string]string{"name": "other", "revision": configured.ProjectRevision}, &f.admin, nil), http.StatusOK)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/documents/"+created.Document.ID, nil, &f.admin, nil).Code)
	onOther := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &f.admin, nil), http.StatusOK)
	assert.False(t, onOther.Draft)
	assert.Equal(t, base.Content, onOther.Content)
	recovery := resultJSON[struct {
		Drafts []draftDTO `json:"drafts"`
	}](t, f.request(http.MethodGet, testAPI+"/drafts", nil, &f.admin, nil), http.StatusOK)
	assert.Contains(t, recovery.Drafts, *created.Draft, "direct owner-only draft recovery remains available")
	other := storageConfiguration(t, f)
	resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches/checkout", map[string]string{"name": "main", "revision": other.ProjectRevision}, &f.admin, nil), http.StatusOK)
	latest := storageConfiguration(t, f)
	rebased := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents/"+created.Document.ID+"/rebase", map[string]string{"content": "# Explicitly rebased local document", "revision": latest.ProjectRevision, "draftId": created.Draft.ID, "draftRevision": created.Draft.Revision}, &f.admin, nil), http.StatusOK)
	require.NotNil(t, rebased.Draft)
	assert.Equal(t, latest.ProjectRevision, rebased.Draft.BaseRevision)
	assert.True(t, f.publish(t, rebased.Draft, &f.admin).Published)
	preservedHome := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &f.admin, nil), http.StatusOK)
	assert.Equal(t, home.Draft.ID, preservedHome.DraftID)
	assert.Equal(t, home.Draft.Revision, preservedHome.DraftRevision)
	assert.Equal(t, base.Revision, preservedHome.BaseRevision)
}

func TestStorageRechecksRevokedSessionInsideTheContentGate(t *testing.T) {
	var armed atomic.Bool
	entered, release := make(chan content.Actor, 1), make(chan struct{})
	f := newStorageFixture(t, func(ctx context.Context, actor content.Actor) error {
		if !armed.CompareAndSwap(true, false) {
			return nil
		}
		entered <- actor
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	current := storageConfiguration(t, f)
	armed.Store(true)
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		completed <- f.request(http.MethodPut, testAPI+"/storage/configuration", storageUpdate(current, "git", []content.StorageRemote{}), &f.admin, nil)
	}()
	select {
	case actor := <-entered:
		assert.Equal(t, f.admin.user.ID, actor.ID)
		assert.Equal(t, f.admin.user.DisplayName, actor.Name)
	case <-time.After(5 * time.Second):
		t.Fatal("configuration did not reach authorization inside the content gate")
	}
	logout := f.request(http.MethodDelete, testAPI+"/session", nil, &f.admin, nil)
	require.Equal(t, http.StatusNoContent, logout.Code, logout.Body.String())
	close(release)
	select {
	case response := <-completed:
		assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("configuration did not finish after authorization was released")
	}
	f.admin = f.login(t, "owner", testPassword)
	unchanged := storageConfiguration(t, f)
	assert.Equal(t, "local", unchanged.Settings.Backend)
	assert.Equal(t, current.Settings.Revision, unchanged.Settings.Revision)
	assert.Equal(t, current.ProjectRevision, unchanged.ProjectRevision)
	_, err := os.Stat(filepath.Join(f.root, ".git"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}
