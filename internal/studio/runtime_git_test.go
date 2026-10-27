package studio

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runtimeGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	hooks := t.TempDir()
	command := exec.CommandContext(t.Context(), "git", append([]string{"-c", "core.hooksPath=" + hooks}, args...)...)
	command.Dir = directory
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0"}
	output, err := command.CombinedOutput()
	require.NoError(t, err, "local Git fixture failed: %s", output)
}

func TestRuntimeGitBranchAndPublicationUseFreshAdministrator(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Initial Git publication\n")
	root := filepath.Join(config.DataDir, "content")
	remote := filepath.Join(t.TempDir(), "origin.git")
	runtimeGit(t, "", "init", "--bare", remote)
	runtimeGit(t, root, "init", "--initial-branch=main")
	runtimeGit(t, root, "config", "user.name", "Fixture")
	runtimeGit(t, root, "config", "user.email", "fixture@example.invalid")
	runtimeGit(t, root, "add", "--", ".")
	runtimeGit(t, root, "commit", "-m", "seed")
	runtimeGit(t, root, "remote", "add", "origin", remote)
	runtimeGit(t, root, "push", "--set-upstream", "origin", "main")
	config.Backend = "git"
	startRuntimeForTest(t, config)
	client := newRuntimeClient(t, config)
	client.login(t, "owner", ownerTestPassword)
	getDocument := func() apiweb.Document {
		status, _, body := client.request(t, http.MethodGet, "/_docs/api/v1/documents/home?published=true", nil)
		require.Equal(t, http.StatusOK, status)
		var document apiweb.Document
		require.NoError(t, json.Unmarshal(body, &document))
		return document
	}
	page := getDocument()
	status, _, _ := client.request(t, http.MethodPost, "/_docs/api/v1/branches", map[string]string{"name": "editing", "revision": page.Revision})
	require.Equal(t, http.StatusOK, status)
	page = getDocument()
	status, _, _ = client.request(t, http.MethodPost, "/_docs/api/v1/branches/checkout", map[string]string{"name": "editing", "revision": page.Revision})
	require.Equal(t, http.StatusOK, status)
	page = getDocument()
	status, _, body := client.request(t, http.MethodPut, "/_docs/api/v1/documents/home", map[string]any{"content": "# Edited on Git branch\n", "revision": page.Revision, "publish": true})
	require.Equal(t, http.StatusOK, status)
	var saved struct {
		Commit    string `json:"commit"`
		Published bool   `json:"published"`
	}
	require.NoError(t, json.Unmarshal(body, &saved))
	assert.True(t, saved.Published)
	assert.NotEmpty(t, saved.Commit)
	page = getDocument()
	status, _, _ = client.request(t, http.MethodPost, "/_docs/api/v1/sync", map[string]string{"action": "push", "revision": page.Revision})
	assert.Equal(t, http.StatusOK, status)
	page = getDocument()
	status, _, _ = client.request(t, http.MethodPost, "/_docs/api/v1/sync", map[string]string{"action": "pull", "revision": page.Revision})
	assert.Equal(t, http.StatusOK, status)
	page = getDocument()
	status, _, _ = client.request(t, http.MethodPost, "/_docs/api/v1/branches/checkout", map[string]string{"name": "main", "revision": page.Revision})
	require.Equal(t, http.StatusOK, status)
	status, _, body = client.request(t, http.MethodGet, "/index.html", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Initial Git publication")
	assert.NotContains(t, string(body), "Edited on Git branch")
}
