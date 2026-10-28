package content

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestartReclaimsOwnedStoragePreparationsBeforeJournalCreation(t *testing.T) {
	service, config, _ := storageFixture(t)
	draft := saveText(t, service, "index.md", "# Keep the draft\n")
	var stages []string
	for _, prefix := range []string{".txn-storage-", ".txn-configuration-"} {
		stage, _, err := createStorageStage(config.StateDir, prefix)
		require.NoError(t, err)
		require.NoError(t, writeSource(stage, "source/index.md", []byte("orphaned preparation\n")))
		stages = append(stages, stage)
	}
	id, err := randomID()
	require.NoError(t, err)
	empty := filepath.Join(config.StateDir, ".txn-storage-"+id)
	require.NoError(t, os.Mkdir(empty, 0o700))
	stages = append(stages, empty)
	require.NoError(t, service.Close())
	backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
	require.NoError(t, err)
	config.Storage = backend
	restarted, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Close()) })
	for _, stage := range stages {
		_, err := os.Lstat(stage)
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
	retained, err := restarted.ReadDraft(context.Background(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, retained)
	file, err := restarted.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Documentation\n", file.Content)
}

func TestRestartRefusesUnrecognizedNonemptyPreparation(t *testing.T) {
	service, config, _ := storageFixture(t)
	id, err := randomID()
	require.NoError(t, err)
	name := ".txn-storage-" + id
	require.NoError(t, os.Mkdir(filepath.Join(config.StateDir, name), 0o700))
	require.NoError(t, writeSource(config.StateDir, name+"/operator.txt", []byte("retain unrecognized data")))
	require.NoError(t, service.Close())
	backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
	require.NoError(t, err)
	config.Storage = backend
	_, err = New(config)
	assert.ErrorIs(t, err, ErrOwnership)
	retained, err := readSource(config.StateDir, name+"/operator.txt", 1024)
	require.NoError(t, err)
	assert.Equal(t, "retain unrecognized data", string(retained))
}

func TestRemoteConfigurationRecoveryPreservesHeadAndUsesSettingsCommitPoint(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(strconv.FormatBool(committed), func(t *testing.T) {
			ctx := context.Background()
			service, config, backend := storageFixture(t)
			origin := storageBare(t)
			initial := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: origin, Push: true}}, "origin")
			draft := saveText(t, service, "index.md", "# Draft across config recovery\n")
			status, err := backend.Status(ctx)
			require.NoError(t, err)
			tree, err := scanTree(ctx, config.Root, storageIdentity(status), "")
			require.NoError(t, err)
			stage, id, err := createStorageStage(config.StateDir, ".txn-storage-")
			require.NoError(t, err)
			backend.mu.Lock()
			next := backend.settings
			next.Remotes = []StorageRemote{{Name: "backup", URL: storageBare(t), Push: true}, {Name: "origin", URL: origin, Push: true}}
			next.Revision, err = storageDigest(next)
			require.NoError(t, err)
			transaction := storageTransaction{Format: "pb-docs-storage-transaction/v1", ID: id, Old: backend.settings, New: next}
			require.NoError(t, backend.prepareStorageLocked(ctx, &transaction, stage, "", tree, editor, ""))
			backend.mu.Unlock()
			require.NoError(t, writeStorageJSON(config.StateDir, "storage-transaction.json", transaction))
			require.NoError(t, writeStorageBytes(config.Root, ".git/config", transaction.NewGitConfig))
			if committed {
				require.NoError(t, writeStorageJSON(config.StateDir, "storage.json", transaction.New))
			}
			require.NoError(t, service.Close())
			restartedBackend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
			require.NoError(t, err)
			config.Storage = restartedBackend
			restarted, err := New(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, restarted.Close()) })
			current, err := restarted.Storage(ctx)
			require.NoError(t, err)
			assert.Equal(t, initial.Sync.Head, current.Sync.Head)
			if committed {
				assert.Equal(t, transaction.New.Revision, current.Settings.Revision)
				assert.Len(t, current.Settings.Remotes, 2)
			} else {
				assert.Equal(t, initial.Settings, current.Settings)
			}
			retained, err := restarted.ReadDraft(ctx, draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft, retained)
		})
	}
}

func TestManagedStorageStartupAndGETNeverProbeRemotes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX Git fixture wrapper")
	}
	service, config, _ := storageFixture(t)
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	wrapperDir := t.TempDir()
	marker := filepath.Join(wrapperDir, "network-attempt.txt")
	shellQuote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nfor argument do\ncase \"$argument\" in fetch|push|ls-remote) printf attempted > " + shellQuote(marker) + "; exit 97;; esac\ndone\nexec " + shellQuote(realGit) + " \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "git"), []byte(script), 0o700))
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	enrolled := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: "ssh://git@never-contact.invalid/repo.git", Push: true}}, "origin")
	_, err = service.Storage(context.Background())
	require.NoError(t, err)
	_, err = os.Lstat(marker)
	assert.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, service.Close())
	backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
	require.NoError(t, err)
	config.Storage = backend
	restarted, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Close()) })
	_, err = restarted.Storage(context.Background())
	require.NoError(t, err)
	_, err = os.Lstat(marker)
	assert.ErrorIs(t, err, os.ErrNotExist)
	checked, err := restarted.CheckRemote(context.Background(), "origin", enrolled.Settings.Revision, editor)
	require.NoError(t, err)
	assert.Equal(t, "error", checked.State)
	assert.True(t, checked.Retryable)
	assert.NotContains(t, checked.Error, "never-contact.invalid")
	_, err = os.Lstat(marker)
	require.NoError(t, err, "the explicit check reaches the intercepted transport command")
}

func TestDamagedPersistedStorageNeverFallsBackAndLosesAuthority(t *testing.T) {
	for _, name := range []string{"storage.json", "storage-remotes.json", "storage-transaction.json"} {
		t.Run(name, func(t *testing.T) {
			service, config, _ := storageFixture(t)
			draft := saveText(t, service, "index.md", "# Draft retained after metadata damage\n")
			require.NoError(t, service.Close())
			require.NoError(t, writeSource(config.StateDir, name, []byte("{\"format\":\"unknown\"}")))
			backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
			require.NoError(t, err)
			config.Storage = backend
			_, err = New(config)
			require.Error(t, err)
			data, err := readSource(config.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json", MaxBinaryBytes)
			require.NoError(t, err)
			assert.Contains(t, string(data), "Draft retained after metadata damage")
			file, err := readSource(config.Root, "index.md", MaxTextBytes)
			require.NoError(t, err)
			assert.Equal(t, "# Documentation\n", string(file))
		})
	}
}

func TestRemoteAcknowledgementCacheIsBoundedAndConservativeAfterEviction(t *testing.T) {
	_, _, backend := storageFixture(t)
	remote := StorageRemote{Name: "origin", URL: "git@host:repo.git", Push: true}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	for i := range 300 {
		branch := "branch-" + strconv.Itoa(i)
		backend.setRemoteStatusLocked(remote, RemoteSyncStatus{Name: remote.Name, Branch: branch, Head: strings.Repeat("a", 40), LastPushedHead: strings.Repeat("a", 40), State: "ready"})
	}
	require.Len(t, backend.states, 256)
	revisited := backend.remoteStatusLocked(remote, SyncStatus{Branch: "branch-0", Head: strings.Repeat("a", 40)})
	assert.Equal(t, "pending", revisited.State)
	assert.True(t, revisited.Retryable)
	assert.Empty(t, revisited.LastPushedHead)
	recent := backend.remoteStatusLocked(remote, SyncStatus{Branch: "branch-299", Head: strings.Repeat("a", 40)})
	assert.Equal(t, "ready", recent.State)
	require.NoError(t, backend.persistRemoteStatesLocked())
}
