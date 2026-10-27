package apiweb

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitForHTTPTest(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	args := []string{"--no-pager", "-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-c", "user.name=HTTP Test", "-c", "user.email=http-test@localhost", "-c", "protocol.file.allow=always"}
	command := exec.CommandContext(t.Context(), "git", append(args, arguments...)...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "git %v: %s", arguments, output)
	return strings.TrimSpace(string(output))
}

func useGitStorage(t *testing.T, f *fixture) string {
	t.Helper()
	require.NoError(t, f.content.Close())
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitForHTTPTest(t, filepath.Dir(remote), "init", "--bare", "--initial-branch=main", remote)
	gitForHTTPTest(t, f.root, "init", "--initial-branch=main")
	gitForHTTPTest(t, f.root, "add", "--", "docs.json", "index.md")
	gitForHTTPTest(t, f.root, "commit", "-m", "Initial documentation")
	gitForHTTPTest(t, f.root, "remote", "add", "origin", remote)
	gitForHTTPTest(t, f.root, "push", "-u", "origin", "main")
	backend, err := content.NewGitBackend(content.GitConfig{Root: f.root, Remote: "origin", Timeout: 10 * time.Second})
	require.NoError(t, err)
	service, err := content.New(content.Config{Root: f.root, StateDir: filepath.Join(filepath.Dir(f.root), "state"), OutputDir: f.output, Storage: backend, Compile: func(ctx context.Context, manifest, output string) error {
		return site.BuildContext(ctx, manifest, output, site.Options{})
	}})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	f.content, f.handler.(*handler).opts.Content = service, service
	return remote
}

func TestRepositoryHTTPBranchesPublicationAndSynchronizationUseRealLocalGit(t *testing.T) {
	f := newFixture(t, nil)
	remote := useGitStorage(t, f)
	base := f.current(t, &f.admin)
	mainDraft := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "# Draft on main", "revision": base.Revision}, &f.admin, nil), http.StatusOK)
	newDocument := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents", map[string]string{"path": "main-only.md", "title": "Main-only draft", "kind": "markdown", "content": "# Draft only on main", "revision": base.Revision}, &f.admin, nil), http.StatusCreated)
	require.NotNil(t, mainDraft.Draft)
	require.NotNil(t, newDocument.Document)
	branches := resultJSON[struct {
		Branches []struct {
			Name    string `json:"name"`
			Current bool   `json:"current"`
			Commit  string `json:"commit"`
		} `json:"branches"`
	}](t, f.request(http.MethodGet, testAPI+"/branches", nil, &f.admin, nil), http.StatusOK)
	require.Len(t, branches.Branches, 1)
	assert.Equal(t, "main", branches.Branches[0].Name)
	assert.True(t, branches.Branches[0].Current)
	assert.NotEmpty(t, branches.Branches[0].Commit)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/branches", map[string]string{"name": "bad..name", "revision": base.Revision}, &f.admin, nil).Code)
	created := resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches", map[string]string{"name": "feature/cms", "revision": base.Revision}, &f.admin, nil), http.StatusOK)
	assert.Equal(t, "git", created.Backend)
	selected := resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches/checkout", map[string]string{"name": "feature/cms", "revision": created.Revision}, &f.admin, nil), http.StatusOK)
	assert.Equal(t, "feature/cms", selected.Branch)
	assert.NotEqual(t, base.Revision, selected.Revision)
	feature := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &f.admin, nil), http.StatusOK)
	assert.False(t, feature.Draft)
	assert.Equal(t, base.Content, feature.Content)
	listed := f.request(http.MethodGet, testAPI+"/documents", nil, &f.admin, nil)
	assert.Equal(t, http.StatusOK, listed.Code)
	assert.NotContains(t, listed.Body.String(), newDocument.Document.ID)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/drafts/"+mainDraft.Draft.ID+"/publish", map[string]string{"revision": mainDraft.Draft.Revision}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/branches/checkout", map[string]string{"name": "main", "revision": base.Revision}, &f.admin, nil).Code)
	published := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]any{"content": "# Published on feature", "revision": feature.Revision, "publish": true}, &f.admin, nil), http.StatusOK)
	assert.True(t, published.Published)
	require.NotNil(t, published.Sync)
	assert.True(t, published.Sync.PendingPush)
	assert.NotEmpty(t, published.Commit)
	assert.Contains(t, gitForHTTPTest(t, f.root, "show", "HEAD:index.md"), "Published on feature")
	pushed := resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/sync", map[string]string{"action": "push", "revision": published.Revision}, &f.admin, nil), http.StatusOK)
	assert.False(t, pushed.PendingPush)
	assert.Equal(t, published.Commit, gitForHTTPTest(t, filepath.Dir(remote), "--git-dir", remote, "rev-parse", "refs/heads/feature/cms"))
	for _, action := range []string{"fetch", "pull"} {
		current := f.current(t, &f.admin)
		status := resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/sync", map[string]string{"action": action, "revision": current.Revision}, &f.admin, nil), http.StatusOK)
		assert.Equal(t, "feature/cms", status.Branch)
	}
	current := f.current(t, &f.admin)
	main := resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches/checkout", map[string]string{"name": "main", "revision": current.Revision}, &f.admin, nil), http.StatusOK)
	assert.Equal(t, "main", main.Branch)
	assert.Equal(t, base.Content, f.current(t, &f.admin).Content)
	ownDraft := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &f.admin, nil), http.StatusOK)
	assert.True(t, ownDraft.Draft)
	assert.Equal(t, "# Draft on main", ownDraft.Content)
	assert.Contains(t, f.request(http.MethodGet, testAPI+"/documents", nil, &f.admin, nil).Body.String(), newDocument.Document.ID)
}

func TestFolderMoveChecksDescendantsAndRetainsAttachmentBytes(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	folder := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/files", map[string]string{"path": "assets/source", "revision": base.Revision}, &f.admin, nil), http.StatusCreated)
	published := f.publish(t, folder.Draft, &f.admin)
	data := []byte("local attachment bytes")
	upload := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/files/content?path=assets%2Fsource%2Fsample.pdf", data, &f.admin, map[string]string{"Content-Type": "application/octet-stream", "If-Match": published.Revision}), http.StatusOK)
	published = f.publish(t, upload.Draft, &f.admin)
	move := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/files/move", map[string]string{"path": "assets/source", "to": "assets/destination", "revision": published.Revision}, &f.admin, nil), http.StatusOK)
	f.publish(t, move.Draft, &f.admin)
	response := f.request(http.MethodGet, testAPI+"/files/content?path=assets%2Fdestination%2Fsample.pdf", nil, &f.admin, nil)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, data, response.Body.Bytes())
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/files/content?path=assets%2Fsource%2Fsample.pdf", nil, &f.admin, nil).Code)
}
