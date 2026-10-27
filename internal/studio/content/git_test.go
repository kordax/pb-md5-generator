package content

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixtureGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	flags := []string{"-C", directory, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "-c", "commit.gpgSign=false", "-c", "core.hooksPath=" + os.DevNull, "-c", "protocol.file.allow=always"}
	command := exec.CommandContext(ctx, "git", append(flags, arguments...)...)
	command.Env = gitEnvironment()
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", string(output))
	return strings.TrimSpace(string(output))
}

func gitManagedFixture(t *testing.T) (*Service, Config, string) {
	t.Helper()
	base := t.TempDir()
	remote, root := filepath.Join(base, "remote.git"), filepath.Join(base, "managed")
	fixtureGit(t, base, "init", "--bare", "--initial-branch=main", remote)
	fixtureGit(t, base, "clone", "--", remote, root)
	backend, err := NewGitBackend(GitConfig{Root: root})
	require.NoError(t, err)
	config := Config{Root: root, StateDir: filepath.Join(base, "state"), OutputDir: filepath.Join(base, "site"), Initialize: true, Storage: backend, Compile: func(ctx context.Context, manifest, output string) error {
		return site.BuildContext(ctx, manifest, output, site.Options{})
	}}
	service, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	draft := saveText(t, service, "index.md", "# Base\n")
	result, err := service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision, Push: true}, editor)
	require.NoError(t, err)
	require.True(t, result.Published)
	require.NotEmpty(t, result.Commit)
	require.Empty(t, result.Sync.Error)
	return service, config, remote
}

func peerClone(t *testing.T, remote string) string {
	t.Helper()
	base := t.TempDir()
	peer := filepath.Join(base, "peer")
	fixtureGit(t, base, "clone", "--", remote, peer)
	return peer
}

func TestGitCommitExplicitActorPushFailureRetryAndHooks(t *testing.T) {
	service, config, remote := gitManagedFixture(t)
	assert.Equal(t, "Alice Example|alice@example.test", fixtureGit(t, config.Root, "show", "-s", "--format=%an|%ae", "HEAD"))
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := "#!/bin/sh\nprintf fired > '" + marker + "'\n"
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, ".git", "hooks", "post-commit"), []byte(hook), 0o700))
	require.NoError(t, os.Rename(remote, remote+".offline"))
	draft := saveText(t, service, "index.md", "# Local publication\n")
	result, err := service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision, Push: true}, editor)
	require.NoError(t, err)
	require.True(t, result.Published)
	require.NotEmpty(t, result.Commit)
	assert.Equal(t, "pending", result.Sync.State)
	assert.True(t, result.Sync.Retryable)
	assert.NotEmpty(t, result.Sync.Error)
	assert.NoFileExists(t, marker, "client Git hooks must not execute")
	file, err := service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Local publication\n", file.Content)
	assert.Equal(t, result.Commit, fixtureGit(t, config.Root, "rev-parse", "HEAD"))
	require.NoError(t, os.Rename(remote+".offline", remote))
	config.Initialize = false
	require.NoError(t, service.Close())
	service, err = New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	status, err := service.Push(context.Background(), project.Revision)
	require.NoError(t, err)
	assert.Empty(t, status.Error)
	assert.False(t, status.Dirty)
	assert.Equal(t, result.Commit, fixtureGit(t, remote, "rev-parse", "refs/heads/main"))
	assert.Equal(t, "2", fixtureGit(t, config.Root, "rev-list", "--count", "HEAD"), "retry must preserve the existing commit")
}

func TestGitBranchesValidateBeforeExposingCandidate(t *testing.T) {
	service, config, _ := gitManagedFixture(t)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	_, err = service.CreateBranch(context.Background(), "feature/docs", project.Revision)
	require.NoError(t, err)
	_, err = service.Checkout(context.Background(), "feature/docs", project.Revision)
	require.NoError(t, err)
	feature, err := service.Project(context.Background())
	require.NoError(t, err)
	assert.NotEqual(t, project.Revision, feature.Revision, "branch identity participates in optimistic revisions")
	_, err = service.Checkout(context.Background(), "main", project.Revision)
	assert.ErrorIs(t, err, ErrConflict)
	_, err = service.Checkout(context.Background(), "main", feature.Revision)
	require.NoError(t, err)
	fixtureGit(t, config.Root, "checkout", "-b", "broken")
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, "index.md"), []byte("# Invalid branch\n\n[Missing](missing.md)\n"), 0o600))
	fixtureGit(t, config.Root, "add", "--", "index.md")
	fixtureGit(t, config.Root, "commit", "-m", "fixture: invalid branch")
	fixtureGit(t, config.Root, "checkout", "main")
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	before, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	_, err = service.Checkout(context.Background(), "broken", project.Revision)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Equal(t, "main", fixtureGit(t, config.Root, "branch", "--show-current"))
	file, err := service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Base\n", file.Content)
	after, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, before, after)
	for _, name := range []string{"--orphan=evil", "../escape", "bad..branch", "bad\nbranch"} {
		_, err := service.CreateBranch(context.Background(), name, project.Revision)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	_, err = service.CreateBranch(context.Background(), "stale", strings.Repeat("0", 64))
	assert.ErrorIs(t, err, ErrConflict)
}

func TestGitPullValidatesAndRollsBackInvalidFastForward(t *testing.T) {
	service, config, remote := gitManagedFixture(t)
	peer := peerClone(t, remote)
	require.NoError(t, os.WriteFile(filepath.Join(peer, "index.md"), []byte("# Valid remote update\n"), 0o600))
	fixtureGit(t, peer, "add", "--", "index.md")
	fixtureGit(t, peer, "commit", "-m", "fixture: valid update")
	fixtureGit(t, peer, "push", "origin", "main")
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	_, err = service.Pull(context.Background(), project.Revision)
	require.NoError(t, err)
	file, err := service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Valid remote update\n", file.Content)
	beforeHead := fixtureGit(t, config.Root, "rev-parse", "HEAD")
	beforeSite, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(peer, "index.md"), []byte("# Invalid remote\n\n[Absent](missing.md)\n"), 0o600))
	fixtureGit(t, peer, "add", "--", "index.md")
	fixtureGit(t, peer, "commit", "-m", "fixture: invalid update")
	fixtureGit(t, peer, "push", "origin", "main")
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	_, err = service.Pull(context.Background(), project.Revision)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Equal(t, beforeHead, fixtureGit(t, config.Root, "rev-parse", "HEAD"))
	file, err = service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Valid remote update\n", file.Content)
	afterSite, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, beforeSite, afterSite)
}

func TestGitDivergenceUnrelatedChangesAndCredentialURLs(t *testing.T) {
	service, config, remote := gitManagedFixture(t)
	peer := peerClone(t, remote)
	result := publishDraft(t, service, saveText(t, service, "index.md", "# Local ahead\n"))
	require.NotEmpty(t, result.Commit)
	require.NoError(t, os.WriteFile(filepath.Join(peer, "index.md"), []byte("# Remote ahead\n"), 0o600))
	fixtureGit(t, peer, "add", "--", "index.md")
	fixtureGit(t, peer, "commit", "-m", "fixture: divergent update")
	fixtureGit(t, peer, "push", "origin", "main")
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	_, err = service.Pull(context.Background(), project.Revision)
	assert.ErrorIs(t, err, ErrConflict)
	assert.Equal(t, result.Commit, fixtureGit(t, config.Root, "rev-parse", "HEAD"))
	file, err := service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Local ahead\n", file.Content)
	fixtureGit(t, config.Root, "remote", "set-url", "origin", "https://fixture:do-not-print@example.invalid/repo")
	_, err = service.Fetch(context.Background(), project.Revision)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "do-not-print")
	assert.NotContains(t, err.Error(), "example.invalid")
	fixtureGit(t, config.Root, "remote", "set-url", "origin", remote)
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, "unrelated.txt"), []byte("operator content"), 0o600))
	newResult := publishDraft(t, service, saveText(t, service, "index.md", "# Still locally published\n"))
	assert.Equal(t, "error", newResult.Sync.State)
	assert.Contains(t, newResult.Sync.Error, "commit")
	assert.NotContains(t, fixtureGit(t, config.Root, "diff", "--cached", "--name-only"), "unrelated.txt")
}
