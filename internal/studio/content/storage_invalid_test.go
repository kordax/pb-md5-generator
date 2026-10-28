package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storageRetainedBytes(t *testing.T, config Config, draft Draft) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	for _, name := range []string{filepath.Join(config.Root, "index.md"), filepath.Join(config.StateDir, "drafts", draft.ID+".meta.json"), filepath.Join(config.StateDir, "drafts", draft.ID+"-"+draft.Revision+".json"), filepath.Join(config.StateDir, "storage.json")} {
		data, err := os.ReadFile(name)
		if os.IsNotExist(err) && filepath.Base(name) == "storage.json" {
			continue
		}
		require.NoError(t, err)
		result[name] = data
	}
	return result
}

func assertStorageRetained(t *testing.T, before map[string][]byte) {
	t.Helper()
	for name, expected := range before {
		actual, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.Equal(t, expected, actual, name)
	}
}

func storageSparseFile(t *testing.T, name string, size int64) {
	t.Helper()
	file, err := os.Create(name)
	require.NoError(t, err)
	err = file.Truncate(size)
	closeErr := file.Close()
	require.NoError(t, err)
	require.NoError(t, closeErr)
}

func TestManagedStorageConstructorRejectsInvalidOptionsBeforeCreatingDirectories(t *testing.T) {
	base := t.TempDir()
	valid := ManagedStorageConfig{Root: filepath.Join(base, "sources"), StateDir: filepath.Join(base, "state")}
	for _, scenario := range []struct {
		name   string
		change func(*ManagedStorageConfig)
	}{
		{"missing root", func(c *ManagedStorageConfig) { c.Root = "" }},
		{"missing state", func(c *ManagedStorageConfig) { c.StateDir = "" }},
		{"same directory", func(c *ManagedStorageConfig) { c.StateDir = c.Root }},
		{"state inside source", func(c *ManagedStorageConfig) { c.StateDir = filepath.Join(c.Root, "private") }},
		{"source inside state", func(c *ManagedStorageConfig) { c.Root = filepath.Join(c.StateDir, "sources") }},
		{"unknown backend", func(c *ManagedStorageConfig) { c.InitialBackend = "http" }},
		{"unsafe remote", func(c *ManagedStorageConfig) { c.InitialRemote = "../origin" }},
		{"portable device name", func(c *ManagedStorageConfig) { c.InitialRemote = "NUL" }},
		{"negative timeout", func(c *ManagedStorageConfig) { c.Timeout = -time.Second }},
		{"short timeout", func(c *ManagedStorageConfig) { c.Timeout = time.Second - time.Nanosecond }},
		{"long timeout", func(c *ManagedStorageConfig) { c.Timeout = 2*time.Minute + time.Nanosecond }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			config := valid
			scenario.change(&config)
			backend, err := NewManagedBackend(config)
			assert.ErrorIs(t, err, ErrInvalid)
			assert.Nil(t, backend)
		})
	}
	for _, timeout := range []time.Duration{0, time.Second, 2 * time.Minute} {
		config := valid
		config.Timeout = timeout
		backend, err := NewManagedBackend(config)
		require.NoError(t, err)
		assert.Equal(t, valid.Root, backend.ManagedRoot())
		_, err = backend.Status(t.Context())
		assert.ErrorIs(t, err, ErrOwnership, "unowned storage must not be used before recovery")
	}
	assert.NoDirExists(t, valid.Root)
	assert.NoDirExists(t, valid.StateDir)
}

func TestManagedLocalBackendKeepsSourcesAndDraftsWhenGitOperationsAreUnavailable(t *testing.T) {
	service, config, backend := storageFixture(t)
	draft := saveText(t, service, "index.md", "# Retain my local draft")
	before := storageRetainedBytes(t, config, draft)
	branches, err := backend.Branches(t.Context())
	require.NoError(t, err)
	assert.Empty(t, branches)
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"create", func() error { return backend.CreateBranch(t.Context(), "other") }},
		{"checkout", func() error { return backend.Checkout(t.Context(), "other") }},
		{"fetch", func() error { return backend.Fetch(t.Context()) }},
		{"pull", func() error { return backend.Pull(t.Context()) }},
		{"restore head", func() error { return backend.RestoreHead(t.Context(), strings.Repeat("a", 40)) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			assert.ErrorIs(t, operation.run(), ErrUnsupported)
			assertStorageRetained(t, before)
		})
	}
	assert.NoDirExists(t, filepath.Join(config.Root, ".git"))
}

func TestCanceledStorageManagementRetainsPublishedSourcesDraftsAndConfiguration(t *testing.T) {
	service, config, _ := storageFixture(t)
	draft := saveText(t, service, "index.md", "# Retained across cancellation")
	before := storageRetainedBytes(t, config, draft)
	current, err := service.Storage(t.Context())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"configuration", func() error { _, err := service.Storage(ctx); return err }},
		{"settings", func() error { _, err := service.StorageSettings(ctx); return err }},
		{"configure", func() error {
			_, err := service.ConfigureStorage(ctx, StorageUpdate{Revision: current.Settings.Revision, BaseRevision: current.ProjectRevision, Backend: "git", Remotes: []StorageRemote{}}, editor)
			return err
		}},
		{"check", func() error {
			_, err := service.CheckRemote(ctx, "origin", current.Settings.Revision, editor)
			return err
		}},
		{"push", func() error {
			_, err := service.PushRemote(ctx, "origin", current.ProjectRevision, current.Settings.Revision, editor)
			return err
		}},
		{"draft branch", func() error { _, err := service.MatchesCurrentBranch(ctx, ""); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			assert.ErrorIs(t, operation.run(), context.Canceled)
			assertStorageRetained(t, before)
		})
	}
	assert.NoDirExists(t, filepath.Join(config.Root, ".git"))
	settings, err := service.StorageSettings(t.Context())
	require.NoError(t, err)
	assert.Equal(t, current.Settings, settings)
}

func TestPlainStorageRefusesLiveManagementWithoutLosingAnExistingDraft(t *testing.T) {
	service, config := managedFixture(t)
	draft := saveText(t, service, "index.md", "# Plain backend draft")
	before := storageRetainedBytes(t, config, draft)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	status, err := service.Storage(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "local", status.Settings.Backend)
	assert.Equal(t, project.Revision, status.ProjectRevision)
	settings, err := service.StorageSettings(t.Context())
	require.NoError(t, err)
	assert.Equal(t, status.Settings, settings)
	_, err = service.ConfigureStorage(t.Context(), StorageUpdate{Revision: project.Revision, BaseRevision: project.Revision, Backend: "git", Remotes: []StorageRemote{}}, editor)
	assert.ErrorIs(t, err, ErrUnsupported)
	_, err = service.CheckRemote(t.Context(), "origin", project.Revision, editor)
	assert.ErrorIs(t, err, ErrUnsupported)
	_, err = service.PushRemote(t.Context(), "origin", project.Revision, project.Revision, editor)
	assert.ErrorIs(t, err, ErrUnsupported)
	assertStorageRetained(t, before)
	assert.NoDirExists(t, filepath.Join(config.Root, ".git"))
}

func TestInvalidStorageShapesAndNoopKeepEveryStoredRevision(t *testing.T) {
	service, config, _ := storageFixture(t)
	draft := saveText(t, service, "index.md", "# Keep before invalid configuration")
	before := storageRetainedBytes(t, config, draft)
	current, err := service.Storage(t.Context())
	require.NoError(t, err)
	base := StorageUpdate{Revision: current.Settings.Revision, BaseRevision: current.ProjectRevision, Backend: "git", Remotes: []StorageRemote{}}
	remote := StorageRemote{Name: "origin", URL: storageBare(t), Push: false}
	for _, scenario := range []struct {
		name   string
		change func(*StorageUpdate)
	}{
		{"nil remotes", func(c *StorageUpdate) { c.Remotes = nil }},
		{"unknown backend", func(c *StorageUpdate) { c.Backend = "other" }},
		{"missing pull authority", func(c *StorageUpdate) { c.PullRemote = "missing"; c.Remotes = []StorageRemote{remote} }},
		{"too many remotes", func(c *StorageUpdate) {
			for index := 0; index <= MaxStorageRemotes; index++ {
				c.Remotes = append(c.Remotes, StorageRemote{Name: fmt.Sprintf("mirror%d", index), URL: remote.URL})
			}
		}},
		{"local with remote", func(c *StorageUpdate) { c.Backend = "local"; c.Remotes = []StorageRemote{remote} }},
		{"local with pull", func(c *StorageUpdate) { c.Backend = "local"; c.PullRemote = "origin" }},
		{"invalid settings revision", func(c *StorageUpdate) { c.Revision = "old" }},
		{"invalid project revision", func(c *StorageUpdate) { c.BaseRevision = "old" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			input := base
			scenario.change(&input)
			_, err := service.ConfigureStorage(t.Context(), input, editor)
			assert.ErrorIs(t, err, ErrInvalid)
			assertStorageRetained(t, before)
			assert.NoDirExists(t, filepath.Join(config.Root, ".git"))
		})
	}
	unchanged, err := service.ConfigureStorage(t.Context(), StorageUpdate{Revision: current.Settings.Revision, BaseRevision: current.ProjectRevision, Backend: "local", Remotes: []StorageRemote{}}, editor)
	require.NoError(t, err)
	assert.Equal(t, current, unchanged)
	assertStorageRetained(t, before)
}

func TestStorageJSONRejectedWritesLeaveExistingBytesIntact(t *testing.T) {
	t.Run("marshal and individual limits", func(t *testing.T) {
		state := t.TempDir()
		name := filepath.Join(state, "settings.json")
		require.NoError(t, writeStorageJSON(state, "settings.json", map[string]string{"title": "retained"}))
		before, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.Error(t, writeStorageJSON(state, "settings.json", make(chan int)))
		assert.ErrorIs(t, writeStorageJSON(state, "settings.json", strings.Repeat("x", maxStorageJSONBytes)), ErrLimit)
		after, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.Equal(t, before, after)
		storageSparseFile(t, filepath.Join(state, "oversize.json"), maxStorageJSONBytes+1)
		assert.ErrorIs(t, writeStorageJSON(state, "oversize.json", "small"), ErrLimit)
		info, err := os.Stat(filepath.Join(state, "oversize.json"))
		require.NoError(t, err)
		assert.EqualValues(t, maxStorageJSONBytes+1, info.Size())
	})
	t.Run("aggregate quota accounts for replacement", func(t *testing.T) {
		state := t.TempDir()
		require.NoError(t, writeStorageJSON(state, "settings.json", "before"))
		before, err := os.ReadFile(filepath.Join(state, "settings.json"))
		require.NoError(t, err)
		storageSparseFile(t, filepath.Join(state, "existing-history.bin"), MaxStateBytes-int64(len(before)))
		assert.ErrorIs(t, writeStorageJSON(state, "settings.json", "too long to replace at capacity"), ErrLimit)
		after, err := os.ReadFile(filepath.Join(state, "settings.json"))
		require.NoError(t, err)
		assert.Equal(t, before, after)
		require.NoError(t, writeStorageJSON(state, "settings.json", "planet"))
		after, err = os.ReadFile(filepath.Join(state, "settings.json"))
		require.NoError(t, err)
		assert.JSONEq(t, `"planet"`, string(after))
	})
	t.Run("nonregular destination", func(t *testing.T) {
		state := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(state, "settings.json"), 0o700))
		assert.Error(t, writeStorageJSON(state, "settings.json", "replacement"))
		assert.DirExists(t, filepath.Join(state, "settings.json"))
	})
}

func TestStoragePrivateIODeniesSymlinksAndUnavailableRoots(t *testing.T) {
	state, outside := t.TempDir(), t.TempDir()
	marker := filepath.Join(outside, "marker.json")
	require.NoError(t, os.WriteFile(marker, []byte(`{"retained":true}`), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(state, "link")))
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"write bytes", func() error { return writeStorageBytes(state, "link/marker.json", []byte("replace")) }},
		{"write JSON", func() error { return writeStorageJSON(state, "link/marker.json", "replace") }},
		{"remove file", func() error { return removeStorageFile(state, "link/marker.json") }},
		{"remove tree", func() error { return removeStorageTree(state, "link") }},
		{"sync directory", func() error { return syncStorageDirectory(state, "link") }},
		{"exists", func() error { _, err := storagePathExists(state, "link/marker.json"); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			assert.Error(t, operation.run())
			data, err := os.ReadFile(marker)
			require.NoError(t, err)
			assert.JSONEq(t, `{"retained":true}`, string(data))
		})
	}
	missing := filepath.Join(t.TempDir(), "missing")
	assert.Error(t, writeStorageJSON(missing, "settings.json", "value"))
	assert.Error(t, writeStorageBytes(missing, "settings.json", []byte("value")))
	assert.Error(t, removeStorageFile(missing, "settings.json"))
	assert.Error(t, removeStorageTree(missing, "tree"))
	assert.Error(t, syncStorageDirectory(missing, "."))
	_, err := storagePathExists(missing, "settings.json")
	assert.Error(t, err)
	_, _, err = createStorageStage(missing, ".txn-storage-")
	assert.Error(t, err)
	assert.NoDirExists(t, missing)
	assert.Error(t, syncStorageDirectory(state, "missing"))
}

func TestStoragePreparationRejectsUnsafeAndUnboundedMetadata(t *testing.T) {
	t.Run("missing and canceled", func(t *testing.T) {
		assert.Error(t, syncStorageTree(t.Context(), filepath.Join(t.TempDir(), "missing")))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.ErrorIs(t, syncStorageTree(ctx, t.TempDir()), context.Canceled)
	})
	t.Run("symlink", func(t *testing.T) {
		state, outside := t.TempDir(), t.TempDir()
		require.NoError(t, os.Symlink(outside, filepath.Join(state, "external")))
		assert.ErrorIs(t, syncStorageTree(t.Context(), state), ErrOwnership)
		assert.DirExists(t, outside)
	})
	t.Run("nonregular file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("filesystem socket requires Unix")
		}
		state := t.TempDir()
		listener, err := net.Listen("unix", filepath.Join(state, "metadata.socket"))
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, listener.Close()) })
		assert.ErrorIs(t, syncStorageTree(t.Context(), state), ErrOwnership)
	})
	t.Run("depth", func(t *testing.T) {
		state := t.TempDir()
		deep := state
		for range 65 {
			deep = filepath.Join(deep, "d")
		}
		require.NoError(t, os.MkdirAll(deep, 0o700))
		assert.ErrorIs(t, syncStorageTree(t.Context(), state), ErrLimit)
	})
	t.Run("total bytes", func(t *testing.T) {
		state := t.TempDir()
		storageSparseFile(t, filepath.Join(state, "metadata.pack"), 2*MaxProjectBytes+1)
		assert.ErrorIs(t, syncStorageTree(t.Context(), state), ErrLimit)
	})
	t.Run("entry count", func(t *testing.T) {
		state := t.TempDir()
		for index := 0; index <= MaxFiles*4; index++ {
			require.NoError(t, os.WriteFile(filepath.Join(state, fmt.Sprintf("object-%05d", index)), nil, 0o600))
		}
		assert.ErrorIs(t, syncStorageTree(t.Context(), state), ErrLimit)
	})
}

func TestManagedGitSynchronizationRetriesTargetsAndRollsBackInvalidPulledSources(t *testing.T) {
	service, config, _ := storageFixture(t)
	origin := storageBare(t)
	mirrorBase := t.TempDir()
	mirror := filepath.Join(mirrorBase, "mirror.git")
	initial := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: origin, Push: true}, {Name: "mirror", URL: mirror, Push: true}}, "origin")
	draft := saveText(t, service, "index.md", "# Keep this draft during synchronization")
	before := storageRetainedBytes(t, config, draft)
	_, err := service.Push(t.Context(), initial.ProjectRevision)
	require.Error(t, err, "one unavailable destination must report the partial failure")
	assert.Equal(t, initial.Sync.Head, storageGit(t, origin, "rev-parse", "refs/heads/main"))
	partial, err := service.Storage(t.Context())
	require.NoError(t, err)
	require.Len(t, partial.Remotes, 2)
	states := map[string]RemoteSyncStatus{}
	for _, status := range partial.Remotes {
		states[status.Name] = status
	}
	assert.Equal(t, "ready", states["origin"].State)
	assert.Equal(t, "error", states["mirror"].State)
	assertStorageRetained(t, before)
	storageGit(t, mirrorBase, "init", "--bare", "--quiet", "--template=", "--initial-branch=main", mirror)
	_, err = service.Push(t.Context(), partial.ProjectRevision)
	require.NoError(t, err)
	assert.Equal(t, initial.Sync.Head, storageGit(t, mirror, "rev-parse", "refs/heads/main"))

	cloneBase := t.TempDir()
	clone := filepath.Join(cloneBase, "remote-edit")
	storageGit(t, cloneBase, "clone", "--quiet", "--", origin, clone)
	storageGit(t, clone, "config", "user.name", "Remote Editor")
	storageGit(t, clone, "config", "user.email", "remote-editor@example.test")
	require.NoError(t, os.WriteFile(filepath.Join(clone, "index.md"), []byte("# Accepted remote update\n"), 0o600))
	storageGit(t, clone, "add", "--", "index.md")
	storageGit(t, clone, "commit", "--quiet", "--no-gpg-sign", "-m", "Update remote document")
	storageGit(t, clone, "push", "--quiet", "origin", "main")
	fetched, err := service.Fetch(t.Context(), partial.ProjectRevision)
	require.NoError(t, err)
	assert.Equal(t, 1, fetched.Behind)
	_, err = service.Pull(t.Context(), partial.ProjectRevision)
	require.NoError(t, err)
	accepted, err := os.ReadFile(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Accepted remote update\n", string(accepted))
	delete(before, filepath.Join(config.Root, "index.md"))
	assertStorageRetained(t, before)
	current, err := service.Storage(t.Context())
	require.NoError(t, err)
	manifest, err := os.ReadFile(filepath.Join(config.Root, "docs.json"))
	require.NoError(t, err)
	published, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(clone, "docs.json"), []byte("{"), 0o600))
	storageGit(t, clone, "add", "--", "docs.json")
	storageGit(t, clone, "commit", "--quiet", "--no-gpg-sign", "-m", "Invalid remote manifest")
	storageGit(t, clone, "push", "--quiet", "origin", "main")
	_, err = service.Pull(t.Context(), current.ProjectRevision)
	require.Error(t, err)
	assert.Equal(t, current.Sync.Head, storageGit(t, config.Root, "rev-parse", "HEAD"))
	rootManifest, err := os.ReadFile(filepath.Join(config.Root, "docs.json"))
	require.NoError(t, err)
	assert.Equal(t, manifest, rootManifest)
	rootSource, err := os.ReadFile(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, accepted, rootSource)
	siteAfter, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, published, siteAfter)
	assertStorageRetained(t, before)
}

func TestMalformedRemoteCacheEntriesCannotReplaceAuthoritativeStorage(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*storedRemoteStatus)
	}{
		{"unsafe name", func(s *storedRemoteStatus) { s.Name = "../origin" }},
		{"unsafe branch", func(s *storedRemoteStatus) { s.Branch = "../main" }},
		{"empty branch", func(s *storedRemoteStatus) { s.Branch = "" }},
		{"unsafe URL", func(s *storedRemoteStatus) { s.URL = "https://example.test/repo" }},
		{"unknown state", func(s *storedRemoteStatus) { s.State = "uploaded" }},
		{"invalid head", func(s *storedRemoteStatus) { s.Head = strings.Repeat("z", 40) }},
		{"invalid acknowledged head", func(s *storedRemoteStatus) { s.LastPushedHead = strings.Repeat("a", 39) }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service, config, _ := storageFixture(t)
			draft := saveText(t, service, "index.md", "# Retain when cached state is malformed")
			before := storageRetainedBytes(t, config, draft)
			require.NoError(t, service.Close())
			entry := storedRemoteStatus{URL: filepath.Join(t.TempDir(), "origin.git"), RemoteSyncStatus: RemoteSyncStatus{Name: "origin", Branch: "main", Head: strings.Repeat("a", 40), LastPushedHead: strings.Repeat("a", 40), State: "ready"}}
			scenario.change(&entry)
			cache := remoteJournal{Format: "pb-docs-remotes/v1", States: []storedRemoteStatus{entry}}
			require.NoError(t, writeStorageJSON(config.StateDir, "storage-remotes.json", cache))
			cacheBefore, err := os.ReadFile(filepath.Join(config.StateDir, "storage-remotes.json"))
			require.NoError(t, err)
			backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
			require.NoError(t, err)
			config.Storage = backend
			reopened, err := New(config)
			if reopened != nil {
				t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
			}
			assert.ErrorIs(t, err, ErrInvalid)
			assert.Nil(t, reopened)
			assertStorageRetained(t, before)
			cacheAfter, err := os.ReadFile(filepath.Join(config.StateDir, "storage-remotes.json"))
			require.NoError(t, err)
			assert.Equal(t, cacheBefore, cacheAfter)
			assert.NoDirExists(t, filepath.Join(config.Root, ".git"))
		})
	}
}

func TestRemoteCacheFromAnotherConnectionCannotAcknowledgeCurrentHead(t *testing.T) {
	for _, changedField := range []string{"name", "url"} {
		t.Run(changedField, func(t *testing.T) {
			service, config, _ := storageFixture(t)
			remote := storageBare(t)
			current := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: remote, Push: true}}, "origin")
			draft := saveText(t, service, "index.md", "# Draft retained during cached connection change")
			before := storageRetainedBytes(t, config, draft)
			entry := storedRemoteStatus{URL: remote, RemoteSyncStatus: RemoteSyncStatus{Name: "origin", Branch: current.Sync.Branch, Head: current.Sync.Head, LastPushedHead: current.Sync.Head, State: "ready"}}
			if changedField == "name" {
				entry.Name = "old-origin"
			} else {
				entry.URL = filepath.Join(t.TempDir(), "different-origin.git")
			}
			require.NoError(t, service.Close())
			require.NoError(t, writeStorageJSON(config.StateDir, "storage-remotes.json", remoteJournal{Format: "pb-docs-remotes/v1", States: []storedRemoteStatus{entry}}))
			backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
			require.NoError(t, err)
			config.Storage = backend
			reopened, err := New(config)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
			actual, err := reopened.Storage(t.Context())
			require.NoError(t, err)
			require.Len(t, actual.Remotes, 1)
			assert.Equal(t, "origin", actual.Remotes[0].Name)
			assert.Equal(t, "pending", actual.Remotes[0].State)
			assert.Empty(t, actual.Remotes[0].LastPushedHead)
			assert.Equal(t, current.Settings.Revision, actual.Settings.Revision)
			assert.Equal(t, current.ProjectRevision, actual.ProjectRevision)
			assertStorageRetained(t, before)
		})
	}
}

func TestExistingGitStorageImportsSafeConnectionsWithoutContactingThem(t *testing.T) {
	service, config, origin := gitManagedFixture(t)
	draft := saveText(t, service, "index.md", "# Keep draft while adopting an existing clone")
	before := storageRetainedBytes(t, config, draft)
	archive := filepath.Join(t.TempDir(), "unavailable-archive.git")
	storageGit(t, config.Root, "remote", "add", "archive", archive)
	require.NoError(t, service.Close())
	backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir, InitialBackend: "git", InitialRemote: "origin"})
	require.NoError(t, err)
	config.Storage = backend
	reopened, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	current, err := reopened.Storage(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "git", current.Settings.Backend)
	assert.Equal(t, "origin", current.Settings.PullRemote)
	assert.Equal(t, []StorageRemote{{Name: "archive", URL: archive, Push: false}, {Name: "origin", URL: origin, Push: true}}, current.Settings.Remotes)
	assert.NoDirExists(t, archive)
	assertStorageRetained(t, before)
}

func TestExistingGitStorageRejectsUnsafeConnectionConfigurationWithoutReplacingDrafts(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*testing.T, Config, string)
	}{
		{"unsafe transport", func(t *testing.T, config Config, _ string) {
			storageGit(t, config.Root, "remote", "set-url", "origin", "https://example.test/repo")
		}},
		{"different push URL", func(t *testing.T, config Config, _ string) {
			storageGit(t, config.Root, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "other.git"))
		}},
		{"unsafe portable remote", func(t *testing.T, config Config, origin string) {
			storageGit(t, config.Root, "remote", "add", "CON", origin)
		}},
		{"too many remotes", func(t *testing.T, config Config, origin string) {
			for index := 0; index < MaxStorageRemotes; index++ {
				storageGit(t, config.Root, "remote", "add", fmt.Sprintf("mirror%d", index), origin)
			}
		}},
		{"corrupt Git metadata", func(t *testing.T, config Config, _ string) {
			require.NoError(t, os.WriteFile(filepath.Join(config.Root, ".git", "config"), []byte("[broken"), 0o600))
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service, config, origin := gitManagedFixture(t)
			draft := saveText(t, service, "index.md", "# Preserve original draft after startup refusal")
			before := storageRetainedBytes(t, config, draft)
			require.NoError(t, service.Close())
			scenario.change(t, config, origin)
			backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir, InitialBackend: "git", InitialRemote: "origin"})
			require.NoError(t, err)
			config.Storage = backend
			reopened, err := New(config)
			if reopened != nil {
				t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
			}
			assert.Error(t, err)
			assert.Nil(t, reopened)
			assertStorageRetained(t, before)
			assert.NoFileExists(t, filepath.Join(config.StateDir, "storage.json"))
		})
	}
}

func TestUnavailableGitCannotMutateManagedSettingsSourcesOrDrafts(t *testing.T) {
	service, config, _ := storageFixture(t)
	current := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: storageBare(t), Push: true}}, "origin")
	draft := saveText(t, service, "index.md", "# Retain while executable is unavailable")
	before := storageRetainedBytes(t, config, draft)
	t.Setenv("PATH", t.TempDir())
	_, err := service.Storage(t.Context())
	assert.Error(t, err)
	_, err = service.StorageSettings(t.Context())
	assert.Error(t, err)
	_, err = service.ConfigureStorage(t.Context(), StorageUpdate{Revision: current.Settings.Revision, BaseRevision: current.ProjectRevision, Backend: "git", PullRemote: "origin", Remotes: current.Settings.Remotes}, editor)
	assert.Error(t, err)
	_, err = service.CheckRemote(t.Context(), "origin", current.Settings.Revision, editor)
	assert.Error(t, err)
	_, err = service.PushRemote(t.Context(), "origin", current.ProjectRevision, current.Settings.Revision, editor)
	assert.Error(t, err)
	_, err = service.MatchesCurrentBranch(t.Context(), "main")
	assert.Error(t, err)
	assertStorageRetained(t, before)
}

type managedStorageFaultBackend struct {
	Backend
	head         string
	commitError  error
	statusError  error
	statusCall   int
	failStatusAt int
}

func (f *managedStorageFaultBackend) Commit(context.Context, []string, Actor, string) (string, error) {
	return f.head, f.commitError
}

func (f *managedStorageFaultBackend) Status(ctx context.Context) (SyncStatus, error) {
	f.statusCall++
	if f.statusError != nil && f.statusCall == f.failStatusAt {
		return SyncStatus{}, f.statusError
	}
	return f.Backend.Status(ctx)
}

func TestManagedDelegatedFailuresPreserveSavedRemoteAcknowledgements(t *testing.T) {
	for _, scenario := range []string{"commit", "postcommit status", "second storage metadata read"} {
		t.Run(scenario, func(t *testing.T) {
			service, config, backend := storageFixture(t)
			current := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: storageBare(t), Push: true}}, "origin")
			_, err := service.PushRemote(t.Context(), "origin", current.ProjectRevision, current.Settings.Revision, editor)
			require.NoError(t, err)
			draft := saveText(t, service, "index.md", "# Retained when delegated storage fails")
			before := storageRetainedBytes(t, config, draft)
			ackFile := filepath.Join(config.StateDir, "storage-remotes.json")
			ack, err := os.ReadFile(ackFile)
			require.NoError(t, err)
			marker := errors.New("injected delegated backend failure")
			original := backend.active
			fault := &managedStorageFaultBackend{Backend: original, head: current.Sync.Head}
			if scenario == "commit" {
				fault.commitError = marker
			} else {
				fault.statusError, fault.failStatusAt = marker, 1
				if scenario == "second storage metadata read" {
					fault.failStatusAt = 2
				}
			}
			backend.active = fault
			defer func() { backend.active = original }()
			if scenario == "second storage metadata read" {
				_, err = service.Storage(t.Context())
			} else {
				var head string
				head, err = backend.Commit(t.Context(), []string{"index.md"}, editor, "Test delegated failure")
				assert.Equal(t, current.Sync.Head, head)
			}
			assert.ErrorIs(t, err, marker)
			actual, err := os.ReadFile(ackFile)
			require.NoError(t, err)
			assert.Equal(t, ack, actual)
			assertStorageRetained(t, before)
		})
	}
}

func TestFailedAcknowledgementPersistenceKeepsSourcesDraftsAndConservativeRestartState(t *testing.T) {
	service, config, _ := storageFixture(t)
	remote := storageBare(t)
	initial := storageUpdate(t, service, []StorageRemote{{Name: "origin", URL: remote, Push: true}}, "origin")
	_, err := service.PushRemote(t.Context(), "origin", initial.ProjectRevision, initial.Settings.Revision, editor)
	require.NoError(t, err)
	published := publishDraft(t, service, saveText(t, service, "index.md", "# Published before acknowledgement persistence fails"))
	draft := saveText(t, service, "index.md", "# Keep this later unpublished edit")
	before := storageRetainedBytes(t, config, draft)
	current, err := service.Storage(t.Context())
	require.NoError(t, err)
	assert.Equal(t, published.Revision, current.ProjectRevision)
	ackFile := filepath.Join(config.StateDir, "storage-remotes.json")
	ack, err := os.ReadFile(ackFile)
	require.NoError(t, err)
	backup := filepath.Join(config.StateDir, "retained-remotes.json")
	require.NoError(t, os.Rename(ackFile, backup))
	require.NoError(t, os.Mkdir(ackFile, 0o700))
	_, err = service.PushRemote(t.Context(), "origin", current.ProjectRevision, current.Settings.Revision, editor)
	assert.Error(t, err, "successful transport must not hide failure to persist its acknowledgement")
	assert.Equal(t, current.Sync.Head, storageGit(t, remote, "rev-parse", "refs/heads/main"))
	assertStorageRetained(t, before)
	require.NoError(t, os.Remove(ackFile))
	require.NoError(t, os.Rename(backup, ackFile))
	actual, err := os.ReadFile(ackFile)
	require.NoError(t, err)
	assert.Equal(t, ack, actual)
	require.NoError(t, service.Close())
	backend, err := NewManagedBackend(ManagedStorageConfig{Root: config.Root, StateDir: config.StateDir})
	require.NoError(t, err)
	config.Storage = backend
	reopened, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
	restarted, err := reopened.Storage(t.Context())
	require.NoError(t, err)
	require.Len(t, restarted.Remotes, 1)
	assert.Equal(t, "pending", restarted.Remotes[0].State)
	assert.Equal(t, initial.Sync.Head, restarted.Remotes[0].LastPushedHead)
	assert.Equal(t, current.Sync.Head, restarted.Remotes[0].Head)
	assert.Equal(t, current.ProjectRevision, restarted.ProjectRevision)
	assertStorageRetained(t, before)
}

func TestPrivateStorageJSONRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	for _, data := range []string{`{"backend":"local","remotes":[],"unexpected":true}`, `{"backend":"local","remotes":[]} {}`, `{"backend":"local"`, `{"backend":"local","remotes":[]} trailing`} {
		var result StorageSettings
		assert.ErrorIs(t, decodeStorageJSON([]byte(data), &result), ErrInvalid, data)
	}
	var result StorageSettings
	require.NoError(t, decodeStorageJSON([]byte(`{"backend":"local","pullRemote":"","remotes":[]}`), &result))
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.JSONEq(t, `{"revision":"","backend":"local","pullRemote":"","remotes":[]}`, string(encoded))
}
