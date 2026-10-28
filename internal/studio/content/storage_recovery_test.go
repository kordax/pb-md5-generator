package content

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func interruptedEnrollment(t *testing.T, config Config, backend *ManagedBackend) storageTransaction {
	t.Helper()
	ctx := context.Background()
	status, err := backend.Status(ctx)
	require.NoError(t, err)
	tree, err := scanTree(ctx, config.Root, storageIdentity(status), "")
	require.NoError(t, err)
	id, err := randomID()
	require.NoError(t, err)
	stage := filepath.Join(config.StateDir, ".txn-storage-"+id)
	require.NoError(t, os.Mkdir(stage, 0o700))
	backend.mu.Lock()
	defer backend.mu.Unlock()
	next := storedStorage{Format: "pb-docs-storage/v1", StorageSettings: StorageSettings{Backend: "git", Remotes: []StorageRemote{}}, LocalHistoryBranch: "main"}
	next.Revision, err = storageDigest(next)
	require.NoError(t, err)
	transaction := storageTransaction{Format: "pb-docs-storage-transaction/v1", ID: id, Old: backend.settings, New: next, Enrolling: true}
	require.NoError(t, backend.prepareStorageLocked(ctx, &transaction, stage, config.Root, tree, editor, "main"))
	require.NoError(t, writeStorageJSON(config.StateDir, "storage-transaction.json", transaction))
	return transaction
}

func TestGitEnrollmentRecoveryUsesDurableSettingsCommitPoint(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		install bool
		commit  bool
	}{
		{name: "prepared before install"},
		{name: "installed before settings commit", install: true},
		{name: "committed", install: true, commit: true},
		{name: "committed destination needs replay", commit: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			service, config, backend := storageFixture(t)
			publishDraft(t, service, saveText(t, service, "index.md", "# Durable local publication\n"))
			draft := saveText(t, service, "index.md", "# Unsaved editor work\n")
			transaction := interruptedEnrollment(t, config, backend)
			if scenario.install {
				require.NoError(t, os.Rename(filepath.Join(config.StateDir, ".txn-storage-"+transaction.ID, "source", ".git"), filepath.Join(config.Root, ".git")))
				require.NoError(t, syncStorageDirectory(config.Root, "."))
			}
			if scenario.commit {
				require.NoError(t, writeStorageJSON(config.StateDir, "storage.json", transaction.New))
			}
			require.NoError(t, service.Close())
			restartedBackend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
			require.NoError(t, err)
			config.Storage, config.Initialize = restartedBackend, false
			restarted, err := New(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, restarted.Close()) })
			current, err := restarted.Storage(ctx)
			require.NoError(t, err)
			if scenario.commit {
				assert.Equal(t, "git", current.Settings.Backend)
				assert.Equal(t, "main", current.Sync.Branch)
				assert.Equal(t, transaction.New.Revision, current.Settings.Revision)
			} else {
				assert.Equal(t, "local", current.Settings.Backend)
				assert.Equal(t, transaction.Old.Revision, current.Settings.Revision)
				exists, err := storagePathExists(config.Root, ".git")
				require.NoError(t, err)
				assert.False(t, exists)
			}
			retained, err := restarted.ReadDraft(ctx, draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft, retained)
			file, err := restarted.Read(ctx, "index.md")
			require.NoError(t, err)
			assert.Equal(t, "# Durable local publication\n", file.Content)
			_, err = readSource(config.StateDir, "storage-transaction.json", 1<<20)
			assert.ErrorIs(t, err, ErrNotFound)
			exists, err := storagePathExists(config.StateDir, ".txn-storage-"+transaction.ID)
			require.NoError(t, err)
			assert.False(t, exists)
		})
	}
}

func TestGitEnrollmentRecoveryNeverDeletesUnrecognizedMetadata(t *testing.T) {
	service, config, backend := storageFixture(t)
	draft := saveText(t, service, "index.md", "# Retain during refused recovery\n")
	transaction := interruptedEnrollment(t, config, backend)
	require.NoError(t, os.Mkdir(filepath.Join(config.Root, ".git"), 0o700))
	require.NoError(t, writeSource(config.Root, ".git/config", []byte("operator owned metadata\n")))
	require.NoError(t, service.Close())
	restartedBackend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
	require.NoError(t, err)
	config.Storage = restartedBackend
	_, err = New(config)
	assert.ErrorIs(t, err, ErrOwnership)
	kept, err := readSource(config.Root, ".git/config", 128<<10)
	require.NoError(t, err)
	assert.Equal(t, "operator owned metadata\n", string(kept))
	_, err = readSource(config.StateDir, "storage-transaction.json", 1<<20)
	require.NoError(t, err)
	_, err = readSource(config.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json", MaxBinaryBytes)
	require.NoError(t, err)
	exists, err := storagePathExists(config.StateDir, ".txn-storage-"+transaction.ID)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestStorageManagementRechecksRevocationAfterGateWait(t *testing.T) {
	service, _, _ := storageFixture(t)
	initial, err := service.Storage(context.Background())
	require.NoError(t, err)
	var allowed atomic.Bool
	allowed.Store(true)
	type requestKey struct{}
	ctx := context.WithValue(context.Background(), requestKey{}, "request-principal")
	service.cfg.AuthorizeStorage = func(callback context.Context, actor Actor) error {
		assert.Equal(t, "request-principal", callback.Value(requestKey{}))
		assert.Equal(t, editor.ID, actor.ID)
		if !allowed.Load() {
			return ErrOwnership
		}
		return nil
	}
	service.gate <- struct{}{}
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		_, err := service.ConfigureStorage(ctx, StorageUpdate{Revision: initial.Settings.Revision, BaseRevision: initial.ProjectRevision, Backend: "git", Remotes: []StorageRemote{}}, editor)
		result <- err
	}()
	<-started
	allowed.Store(false)
	<-service.gate
	assert.ErrorIs(t, <-result, ErrOwnership)
	exists, err := storagePathExists(service.cfg.Root, ".git")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestStorageRecoveryRetainsUnexpectedGitConfigEdits(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(strconv.FormatBool(committed), func(t *testing.T) {
			ctx := context.Background()
			service, config, backend := storageFixture(t)
			storageUpdate(t, service, []StorageRemote{}, "")
			draft := saveText(t, service, "index.md", "# Retained across config conflicts\n")
			status, err := backend.Status(ctx)
			require.NoError(t, err)
			tree, err := scanTree(ctx, config.Root, storageIdentity(status), "")
			require.NoError(t, err)
			stage, id, err := createStorageStage(config.StateDir, ".txn-storage-")
			require.NoError(t, err)
			backend.mu.Lock()
			next := backend.settings
			next.Remotes = []StorageRemote{{Name: "origin", URL: storageBare(t), Push: true}}
			next.PullRemote = "origin"
			next.Revision, err = storageDigest(next)
			require.NoError(t, err)
			transaction := storageTransaction{Format: "pb-docs-storage-transaction/v1", ID: id, Old: backend.settings, New: next}
			require.NoError(t, backend.prepareStorageLocked(ctx, &transaction, stage, "", tree, editor, ""))
			backend.mu.Unlock()
			require.NoError(t, writeStorageJSON(config.StateDir, "storage-transaction.json", transaction))
			operatorConfig := append(append([]byte{}, transaction.OldGitConfig...), []byte("\n# concurrent operator edit\n")...)
			require.NoError(t, writeStorageBytes(config.Root, ".git/config", operatorConfig))
			if committed {
				require.NoError(t, writeStorageJSON(config.StateDir, "storage.json", transaction.New))
			}
			require.NoError(t, service.Close())
			restartedBackend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
			require.NoError(t, err)
			config.Storage = restartedBackend
			_, err = New(config)
			assert.ErrorIs(t, err, ErrConflict)
			retained, err := readSource(config.Root, ".git/config", 128<<10)
			require.NoError(t, err)
			assert.Equal(t, operatorConfig, retained)
			_, err = readSource(config.StateDir, "storage-transaction.json", 1<<20)
			require.NoError(t, err)
			_, err = readSource(config.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json", MaxBinaryBytes)
			require.NoError(t, err)
			assert.Equal(t, status.Head, storageGit(t, config.Root, "rev-parse", "HEAD"))
		})
	}
}

func TestCommittedEnrollmentCannotFollowStagedGitSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a Windows developer-mode privilege")
	}
	service, config, backend := storageFixture(t)
	draft := saveText(t, service, "index.md", "# Draft retained across staging damage\n")
	transaction := interruptedEnrollment(t, config, backend)
	source := filepath.Join(config.StateDir, ".txn-storage-"+transaction.ID, "source")
	require.NoError(t, removeStorageTree(source, ".git"))
	outside := t.TempDir()
	require.NoError(t, writeSource(outside, "config", transaction.NewGitConfig))
	require.NoError(t, writeSource(outside, "studio-enrollment", []byte(transaction.ID)))
	require.NoError(t, os.Symlink(outside, filepath.Join(source, ".git")))
	require.NoError(t, writeStorageJSON(config.StateDir, "storage.json", transaction.New))
	require.NoError(t, service.Close())
	restartedBackend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
	require.NoError(t, err)
	config.Storage = restartedBackend
	_, err = New(config)
	assert.ErrorIs(t, err, ErrOwnership)
	exists, err := storagePathExists(config.Root, ".git")
	require.NoError(t, err)
	assert.False(t, exists)
	outsideConfig, err := readSource(outside, "config", 128<<10)
	require.NoError(t, err)
	assert.Equal(t, transaction.NewGitConfig, outsideConfig)
	_, err = readSource(config.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json", MaxBinaryBytes)
	require.NoError(t, err)
}

func TestLocalHistoryAndDraftAliasIsRestrictedToEnrolledBranch(t *testing.T) {
	service, _, _ := storageFixture(t)
	enrolled := storageUpdate(t, service, []StorageRemote{}, "")
	matches, err := service.MatchesCurrentBranch(context.Background(), "")
	require.NoError(t, err)
	assert.True(t, matches)
	_, err = service.CreateBranch(context.Background(), "unrelated", enrolled.ProjectRevision)
	require.NoError(t, err)
	_, err = service.Checkout(context.Background(), "unrelated", enrolled.ProjectRevision)
	require.NoError(t, err)
	matches, err = service.MatchesCurrentBranch(context.Background(), "")
	require.NoError(t, err)
	assert.False(t, matches)
	matches, err = service.MatchesCurrentBranch(context.Background(), "main")
	require.NoError(t, err)
	assert.False(t, matches)
}
