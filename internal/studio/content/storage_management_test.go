package content

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storageFixture(t *testing.T) (*Service, Config, *ManagedBackend) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is required")
	}
	original, config := managedFixture(t)
	require.NoError(t, original.Close())
	backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
	require.NoError(t, err)
	config.Storage = backend
	service, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	return service, config, backend
}

func storageGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git")
	command.Args = append(command.Args, "-C", root)
	command.Args = append(command.Args, arguments...)
	command.Env = gitEnvironment()
	output, err := command.CombinedOutput()
	require.NoError(t, err, "Git fixture command failed: %s", string(output))
	return strings.TrimSpace(string(output))
}

func storageBare(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	storageGit(t, base, "init", "--bare", "--quiet", "--template=", "--initial-branch=main", bare)
	return bare
}

func storageUpdate(t *testing.T, service *Service, remotes []StorageRemote, pull string) StorageResult {
	t.Helper()
	current, err := service.Storage(context.Background())
	require.NoError(t, err)
	result, err := service.ConfigureStorage(context.Background(), StorageUpdate{Revision: current.Settings.Revision, BaseRevision: current.ProjectRevision, Backend: "git", PullRemote: pull, Remotes: remotes}, editor)
	require.NoError(t, err)
	return result
}

func TestLiveGitEnrollmentPreservesDraftBytesAndLocalHistoryAcrossRestart(t *testing.T) {
	ctx := context.Background()
	service, config, _ := storageFixture(t)
	publishDraft(t, service, saveText(t, service, "index.md", "# Local version one\n"))
	publishDraft(t, service, saveText(t, service, "index.md", "# Local version two\n"))
	history, err := service.History(ctx, "index.md")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(history), 4)
	localID := history[len(history)-1].ID
	draft := saveText(t, service, "index.md", "# Recoverable draft\n")
	payloadName := "drafts/" + draft.ID + "-" + draft.Revision + ".json"
	payloadBefore, err := readSource(config.StateDir, payloadName, MaxBinaryBytes)
	require.NoError(t, err)
	metadataBefore, err := readSource(config.StateDir, "drafts/"+draft.ID+".meta.json", 128<<10)
	require.NoError(t, err)
	local, err := service.Storage(ctx)
	require.NoError(t, err)
	enrolled := storageUpdate(t, service, []StorageRemote{}, "")
	assert.Equal(t, "git", enrolled.Settings.Backend)
	assert.Equal(t, "main", enrolled.Sync.Branch)
	assert.NotEmpty(t, enrolled.Sync.Head)
	assert.NotEqual(t, local.ProjectRevision, enrolled.ProjectRevision)
	assert.NotEqual(t, local.Settings.Revision, enrolled.Settings.Revision)
	payloadAfter, err := readSource(config.StateDir, payloadName, MaxBinaryBytes)
	require.NoError(t, err)
	assert.Equal(t, payloadBefore, payloadAfter)
	metadataAfter, err := readSource(config.StateDir, "drafts/"+draft.ID+".meta.json", 128<<10)
	require.NoError(t, err)
	assert.Equal(t, metadataBefore, metadataAfter)
	saved, err := service.ReadDraft(ctx, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, saved)
	_, err = service.Publish(ctx, PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	preserved, err := service.History(ctx, "index.md")
	require.NoError(t, err)
	assert.Equal(t, history, preserved)
	_, err = service.CreateBranch(ctx, "feature", enrolled.ProjectRevision)
	require.NoError(t, err)
	_, err = service.Checkout(ctx, "feature", enrolled.ProjectRevision)
	require.NoError(t, err)
	foreign, err := service.History(ctx, "index.md")
	require.NoError(t, err)
	assert.Empty(t, foreign)
	project, err := service.Project(ctx)
	require.NoError(t, err)
	_, err = service.Restore(ctx, "index.md", localID, project.Revision, editor)
	assert.ErrorIs(t, err, ErrConflict)
	_, err = service.Checkout(ctx, "main", project.Revision)
	require.NoError(t, err)
	require.NoError(t, service.Close())
	backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir, InitialBackend: "local"})
	require.NoError(t, err)
	config.Storage, config.Initialize = backend, false
	service, err = New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	afterRestart, err := service.Storage(ctx)
	require.NoError(t, err)
	assert.Equal(t, enrolled.Settings, afterRestart.Settings)
	saved, err = service.ReadDraft(ctx, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, saved)
	stillLocal, err := service.History(ctx, "index.md")
	require.NoError(t, err)
	assert.Equal(t, history, stillLocal)
	historicalDraft, err := service.Restore(ctx, "index.md", localID, afterRestart.ProjectRevision, editor)
	require.NoError(t, err, "local history remains restorable on its enrolled branch")
	require.NotNil(t, historicalDraft.Change.Content)
	assert.Equal(t, "# Documentation\n", *historicalDraft.Change.Content)
	require.NoError(t, service.DiscardDraft(ctx, historicalDraft.ID, historicalDraft.Revision, editor))
	staleSaved, err := service.SaveDraft(ctx, DraftInput{ID: draft.ID, Revision: draft.Revision, BaseRevision: draft.BaseRevision, Change: draft.Change}, editor)
	require.NoError(t, err, "a stale draft remains savable without forced rebase")
	rebased, err := service.SaveDraft(ctx, DraftInput{ID: staleSaved.ID, Revision: staleSaved.Revision, BaseRevision: afterRestart.ProjectRevision, Change: staleSaved.Change}, editor)
	require.NoError(t, err)
	published := publishDraft(t, service, rebased)
	assert.NotEmpty(t, published.Commit)
	assert.Equal(t, "# Recoverable draft", storageGit(t, config.Root, "show", "HEAD:index.md"))
}

func TestMultiplePushTargetsFailIndependentlyAndRecoverWithoutLosingPublication(t *testing.T) {
	ctx := context.Background()
	service, config, _ := storageFixture(t)
	origin := storageBare(t)
	missing := filepath.Join(t.TempDir(), "unavailable.git")
	initial := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: origin, Push: true}, {Name: "backup", URL: missing, Push: true}}, "origin")
	checked, err := service.CheckRemote(ctx, "origin", initial.Settings.Revision, editor)
	require.NoError(t, err)
	assert.Equal(t, "ready", checked.State)
	failed, err := service.CheckRemote(ctx, "backup", initial.Settings.Revision, editor)
	require.NoError(t, err)
	assert.Equal(t, "error", failed.State)
	assert.True(t, failed.Retryable)
	assert.NotContains(t, failed.Error, missing)
	draft := saveText(t, service, "index.md", "# Published despite a failed backup\n")
	published, err := service.Publish(ctx, PublishInput{ID: draft.ID, Revision: draft.Revision, Push: true}, editor)
	require.NoError(t, err)
	assert.True(t, published.Published)
	assert.True(t, published.Sync.Retryable)
	assert.Equal(t, published.Commit, storageGit(t, filepath.Dir(origin), "--git-dir="+origin, "rev-parse", "refs/heads/main"))
	assert.Equal(t, published.Commit, storageGit(t, config.Root, "rev-parse", "HEAD"))
	file, err := service.Read(ctx, "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Published despite a failed backup\n", file.Content)
	current, err := service.Storage(ctx)
	require.NoError(t, err)
	for _, state := range current.Remotes {
		if state.Name == "backup" {
			assert.Equal(t, "error", state.State)
		} else {
			assert.Equal(t, published.Commit, state.LastPushedHead)
		}
	}
	backup := storageBare(t)
	reconfigured := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: origin, Push: true}, {Name: "backup", URL: backup, Push: true}}, "origin")
	pushed, err := service.PushRemote(ctx, "backup", reconfigured.ProjectRevision, reconfigured.Settings.Revision, editor)
	require.NoError(t, err)
	assert.Equal(t, "ready", pushed.State)
	assert.Equal(t, published.Commit, pushed.LastPushedHead)
	assert.Equal(t, published.Commit, storageGit(t, filepath.Dir(backup), "--git-dir="+backup, "rev-parse", "refs/heads/main"))
	settled, err := service.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, "clean", settled.State)
	assert.False(t, settled.Retryable)
	_, upstreamErr := exec.Command("git", "-C", config.Root, "config", "--get", "branch.main.remote").Output()
	assert.Error(t, upstreamErr, "push targets must not overwrite branch upstream authority")
}

func TestStorageConfigurationCASAndPrivateTransportValidation(t *testing.T) {
	ctx := context.Background()
	service, _, _ := storageFixture(t)
	initial, err := service.Storage(ctx)
	require.NoError(t, err)
	for _, remotes := range [][]StorageRemote{
		{{Name: "origin", URL: "https://host/repo.git", Push: true}},
		{{Name: "origin", URL: "ssh://user:secret@host/repo.git", Push: true}},
		{{Name: "origin", URL: "ssh://host/repo.git?token=secret", Push: true}},
		{{Name: "origin", URL: "file://localhost/tmp/repo.git", Push: true}},
		{{Name: "origin", URL: "relative.git", Push: true}},
		{{Name: "origin.lock", URL: "git@host:repo.git", Push: true}},
		{{Name: "CON", URL: "git@host:repo.git", Push: true}},
		{{Name: "origin", URL: "git@host:one.git"}, {Name: "Origin", URL: "git@host:two.git"}},
	} {
		_, err := service.ConfigureStorage(ctx, StorageUpdate{Revision: initial.Settings.Revision, BaseRevision: initial.ProjectRevision, Backend: "git", Remotes: remotes}, editor)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	_, err = service.ConfigureStorage(ctx, StorageUpdate{Revision: strings.Repeat("0", 64), BaseRevision: initial.ProjectRevision, Backend: "git", Remotes: []StorageRemote{}}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	_, err = service.ConfigureStorage(ctx, StorageUpdate{Revision: initial.Settings.Revision, BaseRevision: strings.Repeat("0", 64), Backend: "git", Remotes: []StorageRemote{}}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	enrolled := storageUpdate(t, service, []StorageRemote{}, "")
	_, err = service.ConfigureStorage(ctx, StorageUpdate{Revision: enrolled.Settings.Revision, BaseRevision: enrolled.ProjectRevision, Backend: "local", Remotes: []StorageRemote{}}, editor)
	assert.ErrorIs(t, err, ErrUnsupported)
	current, err := service.Storage(ctx)
	require.NoError(t, err)
	assert.Equal(t, enrolled.Settings, current.Settings)
	assert.Equal(t, enrolled.ProjectRevision, current.ProjectRevision)
	_, err = service.CheckRemote(ctx, "missing", current.Settings.Revision, editor)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = service.PushRemote(ctx, "missing", current.ProjectRevision, strings.Repeat("0", 64), editor)
	assert.ErrorIs(t, err, ErrConflict)
	assert.False(t, errors.Is(err, ErrUnsupported))
}
