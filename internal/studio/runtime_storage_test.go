package studio

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runtimeStorageSnapshot(t *testing.T, client *runtimeClient) content.StorageResult {
	t.Helper()
	status, _, body := client.request(t, http.MethodGet, "/_docs/api/v1/storage/configuration", nil)
	require.Equal(t, http.StatusOK, status, "storage configuration: %s", body)
	var result content.StorageResult
	require.NoError(t, json.Unmarshal(body, &result))
	return result
}

func runtimeStorageGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	arguments = append([]string{"-C", directory}, arguments...)
	command := exec.CommandContext(t.Context(), "git", arguments...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_COUNT=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	command.WaitDelay = 2 * time.Second
	output, err := command.CombinedOutput()
	require.NoError(t, err, "temporary Git command: %s", output)
	return strings.TrimSpace(string(output))
}

func TestRuntimeStorageEnrollmentRetainsDraftHistoryAndIndependentTargets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is required for the storage integration scenario")
	}
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Storage owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Original local source\n")
	server := startRuntimeForTest(t, config)
	client := newRuntimeClient(t, config)
	client.login(t, "owner", ownerTestPassword)
	status, _, body := client.request(t, http.MethodGet, "/_docs/api/v1/documents/home?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	var page apiweb.Document
	require.NoError(t, json.Unmarshal(body, &page))
	status, _, _ = client.request(t, http.MethodPut, "/_docs/api/v1/documents/home", map[string]any{"revision": page.Revision, "content": "# Published before Git\n", "publish": true})
	require.Equal(t, http.StatusOK, status)
	status, _, body = client.request(t, http.MethodGet, "/_docs/api/v1/documents/home/history", nil)
	require.Equal(t, http.StatusOK, status)
	var localHistory struct {
		Revisions []struct {
			ID string `json:"id"`
		} `json:"revisions"`
	}
	require.NoError(t, json.Unmarshal(body, &localHistory))
	require.NotEmpty(t, localHistory.Revisions)
	status, _, body = client.request(t, http.MethodGet, "/_docs/api/v1/documents/home?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, json.Unmarshal(body, &page))
	const draftSource = "# Draft saved before enrollment\n\nExact source remains available.\n"
	status, _, body = client.request(t, http.MethodPut, "/_docs/api/v1/documents/home", map[string]any{"revision": page.Revision, "content": draftSource, "publish": false})
	require.Equal(t, http.StatusOK, status)
	var saved struct {
		Draft content.Draft `json:"draft"`
	}
	require.NoError(t, json.Unmarshal(body, &saved))
	require.NotEmpty(t, saved.Draft.ID)
	local := runtimeStorageSnapshot(t, client)
	require.Equal(t, "local", local.Settings.Backend)
	remoteParent := t.TempDir()
	primary := filepath.Join(remoteParent, "primary.git")
	secondary := filepath.Join(remoteParent, "secondary.git")
	runtimeStorageGit(t, remoteParent, "init", "--bare", "--template=", "--initial-branch=main", primary)
	update := content.StorageUpdate{
		Revision: local.Settings.Revision, BaseRevision: local.ProjectRevision, Backend: "git", InitialBranch: "main", PullRemote: "primary",
		Remotes: []content.StorageRemote{{Name: "primary", URL: primary, Push: true}, {Name: "secondary", URL: secondary, Push: true}},
	}
	status, _, body = client.request(t, http.MethodPut, "/_docs/api/v1/storage/configuration", update)
	require.Equal(t, http.StatusOK, status, "enrollment: %s", body)
	var enrolled content.StorageResult
	require.NoError(t, json.Unmarshal(body, &enrolled))
	assert.Equal(t, "git", enrolled.Settings.Backend)
	assert.Equal(t, "main", enrolled.Sync.Branch)
	assert.NotEqual(t, local.ProjectRevision, enrolled.ProjectRevision)
	assert.Empty(t, runtimeStorageGit(t, primary, "for-each-ref", "--format=%(refname)"), "enrollment does not push to a remote")
	status, _, body = client.request(t, http.MethodGet, "/index.html", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Published before Git")
	assert.NotContains(t, string(body), "Draft saved before enrollment")
	status, _, _ = client.request(t, http.MethodPut, "/_docs/api/v1/storage/configuration", update)
	assert.Equal(t, http.StatusConflict, status, "stale settings cannot overwrite enrollment")
	status, _, body = client.request(t, http.MethodGet, "/_docs/api/v1/documents/home", nil)
	require.Equal(t, http.StatusOK, status)
	require.NoError(t, json.Unmarshal(body, &page))
	assert.Equal(t, draftSource, page.Content)
	assert.Equal(t, saved.Draft.ID, page.DraftID)
	assert.Equal(t, saved.Draft.Revision, page.DraftRevision)
	status, _, body = client.request(t, http.MethodGet, "/_docs/api/v1/documents/home/history", nil)
	require.Equal(t, http.StatusOK, status)
	for _, entry := range localHistory.Revisions {
		assert.Contains(t, string(body), entry.ID, "local history remains visible on the enrolled branch")
	}
	status, _, _ = client.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+saved.Draft.ID+"/publish", map[string]any{"revision": saved.Draft.Revision})
	assert.Equal(t, http.StatusConflict, status, "migration never silently rebases a saved draft")
	status, _, body = client.request(t, http.MethodPost, "/_docs/api/v1/documents/home/rebase", map[string]any{
		"content": draftSource, "revision": enrolled.ProjectRevision, "draftId": saved.Draft.ID, "draftRevision": saved.Draft.Revision,
	})
	require.Equal(t, http.StatusOK, status, "explicit rebase: %s", body)
	require.NoError(t, json.Unmarshal(body, &saved))
	status, _, body = client.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+saved.Draft.ID+"/publish", map[string]any{"revision": saved.Draft.Revision, "push": true})
	require.Equal(t, http.StatusOK, status, "publication with partial synchronization: %s", body)
	published := runtimeStorageSnapshot(t, client)
	require.NotEmpty(t, published.Sync.Head)
	assert.Equal(t, published.Sync.Head, runtimeStorageGit(t, primary, "rev-parse", "refs/heads/main"))
	states := make(map[string]content.RemoteSyncStatus)
	for _, state := range published.Remotes {
		states[state.Name] = state
	}
	assert.Equal(t, published.Sync.Head, states["primary"].LastPushedHead)
	assert.Equal(t, "error", states["secondary"].State)
	assert.True(t, states["secondary"].Retryable)
	assert.NotContains(t, states["secondary"].Error, secondary, "public errors do not echo command output")
	runtimeStorageGit(t, remoteParent, "init", "--bare", "--template=", "--initial-branch=main", secondary)
	status, _, body = client.request(t, http.MethodPost, "/_docs/api/v1/storage/remotes/secondary/check", map[string]string{"revision": published.Settings.Revision})
	require.Equal(t, http.StatusOK, status)
	var remote content.RemoteSyncStatus
	require.NoError(t, json.Unmarshal(body, &remote))
	assert.NotEqual(t, "error", remote.State)
	status, _, body = client.request(t, http.MethodPost, "/_docs/api/v1/storage/remotes/secondary/push", map[string]string{"revision": published.Settings.Revision, "baseRevision": published.ProjectRevision})
	require.Equal(t, http.StatusOK, status, "retry one target: %s", body)
	require.NoError(t, json.Unmarshal(body, &remote))
	assert.Equal(t, "ready", remote.State)
	assert.Equal(t, published.Sync.Head, remote.LastPushedHead)
	assert.Equal(t, published.Sync.Head, runtimeStorageGit(t, secondary, "rev-parse", "refs/heads/main"))
	assert.Equal(t, published.Sync.Head, runtimeStorageGit(t, filepath.Join(config.DataDir, "content"), "rev-parse", "HEAD"), "retry does not create another document commit")
	status, _, body = client.request(t, http.MethodGet, "/_docs/api/v1/branches", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "main")
	synchronized := runtimeStorageSnapshot(t, client)
	status, _, body = client.request(t, http.MethodPost, "/_docs/api/v1/sync", map[string]string{"revision": synchronized.ProjectRevision, "action": "fetch"})
	require.Equal(t, http.StatusOK, status, "explicit fetch: %s", body)
	runtimeStorageRejectsInvalidPull(t, client, config, remoteParent, primary, published.Sync.Head)
	require.NoError(t, server.Close(t.Context()))
	restartedConfig := runtimeConfigForTest(t)
	restartedConfig.DataDir = config.DataDir
	startRuntimeForTest(t, restartedConfig)
	restarted := newRuntimeClient(t, restartedConfig)
	restarted.cookie, restarted.csrf = client.cookie, client.csrf
	persisted := runtimeStorageSnapshot(t, restarted)
	assert.Equal(t, "git", persisted.Settings.Backend, "saved storage choice survives the local CLI default")
	assert.Equal(t, published.Settings.Revision, persisted.Settings.Revision)
	assert.Equal(t, published.Sync.Head, persisted.Sync.Head)
	status, _, body = restarted.request(t, http.MethodGet, "/index.html", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Draft saved before enrollment")
}

func runtimeStorageRejectsInvalidPull(t *testing.T, client *runtimeClient, config Config, remoteParent, primary, retainedHead string) {
	t.Helper()
	foreign := filepath.Join(remoteParent, "foreign-editor")
	runtimeStorageGit(t, remoteParent, "clone", "--template=", primary, foreign)
	require.NoError(t, os.WriteFile(filepath.Join(foreign, "docs.json"), []byte("{broken remote manifest"), 0o600))
	runtimeStorageGit(t, foreign, "add", "--", "docs.json")
	runtimeStorageGit(t, foreign, "-c", "user.name=Temporary fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "docs: invalid remote fixture")
	runtimeStorageGit(t, foreign, "push", "origin", "HEAD:refs/heads/main")
	invalidHead := runtimeStorageGit(t, foreign, "rev-parse", "HEAD")
	before := runtimeStorageSnapshot(t, client)
	status, _, _ := client.request(t, http.MethodPost, "/_docs/api/v1/sync", map[string]string{"revision": before.ProjectRevision, "action": "pull"})
	require.Equal(t, http.StatusBadRequest, status, "a malformed remote manifest is rejected")
	assert.Equal(t, invalidHead, runtimeStorageGit(t, filepath.Join(config.DataDir, "content"), "rev-parse", "refs/remotes/primary/main"), "the remote commit was fetched before candidate validation")
	assert.Equal(t, retainedHead, runtimeStorageGit(t, filepath.Join(config.DataDir, "content"), "rev-parse", "HEAD"), "failed validation restores the previous local commit")
	status, _, body := client.request(t, http.MethodGet, "/index.html", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Draft saved before enrollment")
	status, _, body = client.request(t, http.MethodGet, "/_docs/api/v1/documents/home?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Exact source remains available.")
}

func TestRuntimeStorageManagementRequiresUnrestrictedAdministrator(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Unchanged source\n")
	startRuntimeForTest(t, config)
	owner := newRuntimeClient(t, config)
	owner.login(t, "owner", ownerTestPassword)
	before := runtimeStorageSnapshot(t, owner)
	cases := []struct {
		name  string
		role  string
		read  []string
		write []string
	}{
		{"viewer", "viewer", []string{"."}, []string{"."}},
		{"editor", "editor", []string{"."}, []string{"."}},
		{"scoped-read", "admin", []string{"public"}, []string{"."}},
		{"scoped-write", "admin", []string{"."}, []string{"public"}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			status, _, _ := owner.request(t, http.MethodPost, "/_docs/api/v1/users", map[string]any{
				"username": item.name, "displayName": item.name, "password": ownerTestPassword, "role": item.role,
				"grants": map[string]any{"read": item.read, "write": item.write},
			})
			require.Equal(t, http.StatusCreated, status)
			client := newRuntimeClient(t, config)
			client.login(t, item.name, ownerTestPassword)
			for _, route := range []string{"/storage/configuration", "/branches"} {
				status, _, _ = client.request(t, http.MethodGet, "/_docs/api/v1"+route, nil)
				assert.Equal(t, http.StatusForbidden, status, route)
			}
			for _, route := range []string{"/storage/remotes/primary/check", "/storage/remotes/primary/push", "/branches", "/branches/checkout", "/sync"} {
				status, _, _ = client.request(t, http.MethodPost, "/_docs/api/v1"+route, map[string]string{"revision": before.Settings.Revision, "baseRevision": before.ProjectRevision})
				assert.Equal(t, http.StatusForbidden, status, route)
			}
			status, _, _ = client.request(t, http.MethodPut, "/_docs/api/v1/storage/configuration", content.StorageUpdate{
				Revision: before.Settings.Revision, BaseRevision: before.ProjectRevision, Backend: "local", Remotes: []content.StorageRemote{},
			})
			assert.Equal(t, http.StatusForbidden, status)
			status, _, body := client.request(t, http.MethodGet, "/_docs/api/v1/sync/status", nil)
			require.Equal(t, http.StatusOK, status)
			assert.NotContains(t, string(body), "lastCommit")
			assert.NotContains(t, string(body), "lastError")
		})
	}
	anonymous := newRuntimeClient(t, config)
	status, _, _ := anonymous.request(t, http.MethodGet, "/_docs/api/v1/storage/configuration", nil)
	assert.Equal(t, http.StatusUnauthorized, status)
	withoutCSRF := newRuntimeClient(t, config)
	withoutCSRF.cookie = owner.cookie
	status, _, _ = withoutCSRF.request(t, http.MethodPut, "/_docs/api/v1/storage/configuration", content.StorageUpdate{
		Revision: before.Settings.Revision, BaseRevision: before.ProjectRevision, Backend: "local", Remotes: []content.StorageRemote{},
	})
	assert.Equal(t, http.StatusForbidden, status)
	after := runtimeStorageSnapshot(t, owner)
	assert.Equal(t, before.Settings.Revision, after.Settings.Revision)
	assert.Equal(t, before.ProjectRevision, after.ProjectRevision)
}
