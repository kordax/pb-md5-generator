package content

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restoreStorageLocation(t *testing.T, original, unavailable string) {
	t.Helper()
	if _, err := os.Lstat(unavailable); err == nil {
		require.NoError(t, os.Rename(unavailable, original))
	}
}

func TestPublicStorageOperationsRecoverAfterManagedLocationReturns(t *testing.T) {
	for _, location := range []string{"source", "state"} {
		t.Run(location, func(t *testing.T) {
			ctx := t.Context()
			service, config, _ := storageFixture(t)
			remote := storageBare(t)
			current := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: remote, Push: true}}, "origin")
			_, err := service.PushRemote(ctx, "origin", current.ProjectRevision, current.Settings.Revision, editor)
			require.NoError(t, err)
			draft := saveText(t, service, "index.md", "# Retain while a managed location is unavailable\n")
			before := storageRetainedBytes(t, config, draft)
			commits := storageGit(t, config.Root, "rev-list", "--count", "HEAD")
			original := config.Root
			if location == "state" {
				original = config.StateDir
			}
			unavailable := original + ".unavailable"
			require.NoError(t, os.Rename(original, unavailable))
			t.Cleanup(func() { restoreStorageLocation(t, original, unavailable) })
			_, err = service.Storage(ctx)
			assert.Error(t, err)
			_, err = service.StorageSettings(ctx)
			assert.Error(t, err)
			_, err = service.CheckRemote(ctx, "origin", current.Settings.Revision, editor)
			assert.Error(t, err)
			_, err = service.PushRemote(ctx, "origin", current.ProjectRevision, current.Settings.Revision, editor)
			assert.Error(t, err)
			_, err = service.ConfigureStorage(ctx, StorageUpdate{Revision: current.Settings.Revision, BaseRevision: current.ProjectRevision, Backend: "git", PullRemote: "origin", Remotes: current.Settings.Remotes}, editor)
			assert.Error(t, err)
			assert.NoDirExists(t, original, "management must not silently recreate and adopt an unavailable location")
			restoreStorageLocation(t, original, unavailable)
			assertStorageRetained(t, before)
			repaired, err := service.Storage(ctx)
			require.NoError(t, err)
			assert.Equal(t, current.Settings, repaired.Settings)
			assert.Equal(t, current.ProjectRevision, repaired.ProjectRevision)
			checked, err := service.CheckRemote(ctx, "origin", current.Settings.Revision, editor)
			require.NoError(t, err)
			assert.Equal(t, "ready", checked.State)
			pushed, err := service.PushRemote(ctx, "origin", current.ProjectRevision, current.Settings.Revision, editor)
			require.NoError(t, err)
			assert.Equal(t, "ready", pushed.State)
			assert.Equal(t, current.Sync.Head, pushed.LastPushedHead)
			assert.Equal(t, commits, storageGit(t, config.Root, "rev-list", "--count", "HEAD"))
			assert.Equal(t, current.Sync.Head, storageGit(t, remote, "rev-parse", "refs/heads/main"))
			assertStorageRetained(t, before)
		})
	}
}

func TestEnrollmentPreflightLocationLossPreservesLocalStateAndRetriesAfterRestart(t *testing.T) {
	for _, location := range []string{"source", "state"} {
		t.Run(location, func(t *testing.T) {
			service, config, _ := storageFixture(t)
			initial, err := service.Storage(t.Context())
			require.NoError(t, err)
			draft := saveText(t, service, "index.md", "# Keep draft when preflight loses its location\n")
			before := storageRetainedBytes(t, config, draft)
			original := config.Root
			if location == "state" {
				original = config.StateDir
			}
			unavailable := original + ".unavailable"
			t.Cleanup(func() { restoreStorageLocation(t, original, unavailable) })
			compile := service.cfg.Compile
			service.cfg.Compile = func(ctx context.Context, manifest, output string) error {
				if err := compile(ctx, manifest, output); err != nil {
					return err
				}
				return os.Rename(original, unavailable)
			}
			_, err = service.ConfigureStorage(t.Context(), StorageUpdate{Revision: initial.Settings.Revision, BaseRevision: initial.ProjectRevision, Backend: "git", Remotes: []StorageRemote{}}, editor)
			require.Error(t, err)
			assert.NoDirExists(t, original)
			restoreStorageLocation(t, original, unavailable)
			assertStorageRetained(t, before)
			exists, err := storagePathExists(config.Root, ".git")
			require.NoError(t, err)
			assert.False(t, exists, "an incomplete preflight must not expose Git authority")
			stored, err := readStoredStorage(config.StateDir)
			require.NoError(t, err)
			assert.Equal(t, initial.Settings, stored.StorageSettings)
			require.NoError(t, service.Close())
			backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
			require.NoError(t, err)
			config.Storage = backend
			restarted, err := New(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, restarted.Close()) })
			entries, err := os.ReadDir(config.StateDir)
			require.NoError(t, err)
			for _, entry := range entries {
				assert.False(t, strings.HasPrefix(entry.Name(), ".txn-storage-") || strings.HasPrefix(entry.Name(), ".txn-configuration-"), "owned failed preparation must be reclaimed on restart")
			}
			assertStorageRetained(t, before)
			enrolled := storageUpdate(t, restarted, []StorageRemote{}, "")
			assert.Equal(t, "git", enrolled.Settings.Backend)
			assert.Equal(t, "main", enrolled.Sync.Branch)
			retained, err := restarted.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft, retained)
		})
	}
}

type storageCleanupFailureBackend struct {
	Backend
	beforeStatus func() error
}

func (b *storageCleanupFailureBackend) Status(ctx context.Context) (SyncStatus, error) {
	if err := b.beforeStatus(); err != nil {
		return SyncStatus{}, err
	}
	return b.Backend.Status(ctx)
}

func requireStoragePermissionFailures(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permission enforcement for an unprivileged process")
	}
}

func TestCommittedStorageCleanupFailureWarnsAndRepairsWithoutReenrollment(t *testing.T) {
	requireStoragePermissionFailures(t)
	service, config, backend := storageFixture(t)
	initial, err := service.Storage(t.Context())
	require.NoError(t, err)
	draft := saveText(t, service, "index.md", "# Keep the draft when committed cleanup fails\n")
	before := storageRetainedBytes(t, config, draft)
	delete(before, filepath.Join(config.StateDir, "storage.json")) // Enrollment intentionally changes the committed settings.
	blockedStage := ""
	t.Cleanup(func() {
		if blockedStage != "" {
			if err := os.Chmod(blockedStage, 0o700); err != nil && !os.IsNotExist(err) {
				assert.NoError(t, err)
			}
		}
	})
	backend.active = &storageCleanupFailureBackend{Backend: backend.active, beforeStatus: func() error {
		if blockedStage != "" {
			return nil
		}
		entries, err := os.ReadDir(config.StateDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".txn-storage-") {
				continue
			}
			stage := filepath.Join(config.StateDir, entry.Name())
			if _, err := readSource(stage, "source/.git/studio-enrollment", 64); err != nil {
				continue
			}
			if err := os.Chmod(stage, 0o500); err != nil {
				return err
			}
			blockedStage = stage
			break
		}
		return nil
	}}
	committed, err := service.ConfigureStorage(t.Context(), StorageUpdate{Revision: initial.Settings.Revision, BaseRevision: initial.ProjectRevision, Backend: "git", Remotes: []StorageRemote{}}, editor)
	require.NoError(t, err)
	require.NotEmpty(t, blockedStage)
	assert.Equal(t, "git", committed.Settings.Backend)
	assert.Contains(t, committed.Sync.Error, "cleanup")
	assertStorageRetained(t, before)
	stored, err := readStoredStorage(config.StateDir)
	require.NoError(t, err)
	assert.Equal(t, committed.Settings, stored.StorageSettings)
	_, err = readSource(config.StateDir, "storage-transaction.json", 1<<20)
	require.NoError(t, err, "committed cleanup failures retain a replayable journal")
	commits := storageGit(t, config.Root, "rev-list", "--count", "HEAD")
	require.NoError(t, os.Chmod(blockedStage, 0o700))
	repaired, err := service.ConfigureStorage(t.Context(), StorageUpdate{Revision: committed.Settings.Revision, BaseRevision: committed.ProjectRevision, Backend: "git", Remotes: []StorageRemote{}}, editor)
	require.NoError(t, err)
	assert.Equal(t, committed.Settings, repaired.Settings)
	assert.Equal(t, committed.ProjectRevision, repaired.ProjectRevision)
	assert.Empty(t, repaired.Sync.Error)
	assert.Equal(t, commits, storageGit(t, config.Root, "rev-list", "--count", "HEAD"))
	_, err = readSource(config.StateDir, "storage-transaction.json", 1<<20)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.NoDirExists(t, blockedStage)
	assertStorageRetained(t, before)
}

func TestEnrolledStorageRefusesMissingProfileWithoutReseedingAuthority(t *testing.T) {
	for _, fallback := range []string{"local", "git"} {
		t.Run(fallback, func(t *testing.T) {
			service, config, _ := storageFixture(t)
			enrolled := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: storageBare(t), Push: true}, {Name: "backup", URL: storageBare(t), Push: true}}, "origin")
			draft := saveText(t, service, "index.md", "# Preserve the draft when the profile disappears\n")
			before := storageRetainedBytes(t, config, draft)
			profile := filepath.Join(config.StateDir, "storage.json")
			delete(before, profile)
			retainedProfile := filepath.Join(t.TempDir(), "retained-storage.json")
			require.NoError(t, service.Close())
			require.NoError(t, os.Rename(profile, retainedProfile))
			backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir, InitialBackend: fallback})
			require.NoError(t, err)
			config.Storage = backend
			reopened, err := New(config)
			if reopened != nil {
				require.NoError(t, reopened.Close())
			}
			assert.Nil(t, reopened)
			assert.ErrorIs(t, err, ErrOwnership)
			assert.NoFileExists(t, profile, "enrolled settings must not be guessed from startup defaults")
			assert.Equal(t, enrolled.Sync.Head, storageGit(t, config.Root, "rev-parse", "HEAD"))
			assertStorageRetained(t, before)
			// Repair restores the retained profile rather than deriving its push
			// policy or initial-branch history mapping from the current clone.
			require.NoError(t, os.Rename(retainedProfile, profile))
			backend, err = NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir, InitialBackend: fallback})
			require.NoError(t, err)
			config.Storage = backend
			reopened, err = New(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			actual, err := reopened.Storage(t.Context())
			require.NoError(t, err)
			assert.Equal(t, enrolled.Settings, actual.Settings)
			assert.Equal(t, enrolled.ProjectRevision, actual.ProjectRevision)
			matches, err := reopened.MatchesCurrentBranch(t.Context(), "")
			require.NoError(t, err)
			assert.True(t, matches)
			assertStorageRetained(t, before)
		})
	}
}

func TestUnreadableCommittedStorageJournalRetainsChoiceUntilRepaired(t *testing.T) {
	requireStoragePermissionFailures(t)
	service, config, backend := storageFixture(t)
	draft := saveText(t, service, "index.md", "# Retain when committed journal is unreadable\n")
	before := storageRetainedBytes(t, config, draft)
	delete(before, filepath.Join(config.StateDir, "storage.json")) // The simulated commit point selects Git before recovery.
	transaction := interruptedEnrollment(t, config, backend)
	require.NoError(t, os.Rename(filepath.Join(config.StateDir, ".txn-storage-"+transaction.ID, "source", ".git"), filepath.Join(config.Root, ".git")))
	require.NoError(t, syncStorageDirectory(config.Root, "."))
	require.NoError(t, writeStorageJSON(config.StateDir, "storage.json", transaction.New))
	journal := filepath.Join(config.StateDir, "storage-transaction.json")
	require.NoError(t, os.Chmod(journal, 0))
	t.Cleanup(func() {
		if err := os.Chmod(journal, 0o600); err != nil && !os.IsNotExist(err) {
			assert.NoError(t, err)
		}
	})
	require.NoError(t, service.Close())
	restartedBackend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
	require.NoError(t, err)
	config.Storage = restartedBackend
	reopened, err := New(config)
	assert.Nil(t, reopened)
	assert.ErrorIs(t, err, os.ErrPermission)
	stored, err := readStoredStorage(config.StateDir)
	require.NoError(t, err)
	assert.Equal(t, transaction.New.StorageSettings, stored.StorageSettings)
	assert.DirExists(t, filepath.Join(config.Root, ".git"))
	assertStorageRetained(t, before)
	require.NoError(t, os.Chmod(journal, 0o600))
	reopened, err = New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	actual, err := reopened.Storage(t.Context())
	require.NoError(t, err)
	assert.Equal(t, transaction.New.StorageSettings, actual.Settings)
	assert.Equal(t, "main", actual.Sync.Branch)
	_, err = readSource(config.StateDir, "storage-transaction.json", 1<<20)
	assert.ErrorIs(t, err, ErrNotFound)
	assertStorageRetained(t, before)
}
