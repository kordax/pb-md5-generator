package content

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func advanceStorageRemote(t *testing.T, remote, text string) {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "writer")
	storageGit(t, base, "clone", "--quiet", "--template=", "--no-hardlinks", remote, work)
	require.NoError(t, writeSource(work, "index.md", []byte(text)))
	storageGit(t, work, "add", "--", "index.md")
	storageGit(t, work, "-c", "core.hooksPath=", "-c", "user.name=Remote writer", "-c", "user.email=remote@example.test", "commit", "--quiet", "--no-verify", "--no-gpg-sign", "-m", "docs: external source update")
	storageGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
}

func TestMultiplePushConnectionsKeepExactlyOnePullAuthority(t *testing.T) {
	ctx := context.Background()
	service, _, _ := storageFixture(t)
	origin, mirror := storageBare(t), storageBare(t)
	enrolled := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: origin, Push: true}, {Name: "mirror", URL: mirror, Push: true}}, "origin")
	_, err := service.Push(ctx, enrolled.ProjectRevision)
	require.NoError(t, err)
	advanceStorageRemote(t, origin, "# From the pull authority\n")
	advanceStorageRemote(t, mirror, "# Different mirror content\n")
	_, err = service.Fetch(ctx, enrolled.ProjectRevision)
	require.NoError(t, err)
	current, err := service.Storage(ctx)
	require.NoError(t, err)
	assert.Equal(t, "origin", current.Settings.PullRemote)
	assert.Equal(t, 1, current.Sync.Behind)
	_, err = service.Pull(ctx, current.ProjectRevision)
	require.NoError(t, err)
	file, err := service.Read(ctx, "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# From the pull authority\n", file.Content)
	assert.NotContains(t, file.Content, "Different mirror content")
}

func TestEnrollmentRejectsConcurrentSourceChangesWithoutInstallingGit(t *testing.T) {
	ctx := context.Background()
	service, config, _ := storageFixture(t)
	current, err := service.Storage(ctx)
	require.NoError(t, err)
	draft := saveText(t, service, "index.md", "# Recoverable before a source race\n")
	compile := service.cfg.Compile
	service.cfg.Compile = func(ctx context.Context, manifest, output string) error {
		if err := compile(ctx, manifest, output); err != nil {
			return err
		}
		return writeSource(config.Root, "index.md", []byte("# External concurrent edit\n"))
	}
	_, err = service.ConfigureStorage(ctx, StorageUpdate{Revision: current.Settings.Revision, BaseRevision: current.ProjectRevision, Backend: "git", Remotes: []StorageRemote{}}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	exists, err := storagePathExists(config.Root, ".git")
	require.NoError(t, err)
	assert.False(t, exists)
	retained, err := service.ReadDraft(ctx, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, retained)
	file, err := service.Read(ctx, "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# External concurrent edit\n", file.Content)
}
