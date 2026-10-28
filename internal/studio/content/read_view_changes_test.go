package content

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadViewRejectsExternalChangesToReadAndUnreadSources(t *testing.T) {
	for _, change := range []string{"read source", "unread source", "new source", "removed source"} {
		t.Run(change, func(t *testing.T) {
			service, config := managedFixture(t)
			unused := filepath.Join(config.Root, "unused.txt")
			require.NoError(t, os.WriteFile(unused, []byte("old payload"), 0o600))
			err := service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
				data, readErr := fs.ReadFile(snapshot.FS, "index.md")
				require.NoError(t, readErr)
				assert.Equal(t, "# Documentation\n", string(data))
				switch change {
				case "read source":
					require.NoError(t, os.WriteFile(filepath.Join(config.Root, "index.md"), []byte("# External change\n"), 0o600))
					_, readErr = fs.ReadFile(snapshot.FS, "index.md")
					assert.ErrorIs(t, readErr, ErrConflict)
				case "unread source":
					require.NoError(t, os.WriteFile(unused, []byte("new payload"), 0o600))
				case "new source":
					require.NoError(t, os.WriteFile(filepath.Join(config.Root, "new.txt"), []byte("new payload"), 0o600))
					_, readErr = fs.Stat(snapshot.FS, "new.txt")
					assert.ErrorIs(t, readErr, fs.ErrNotExist, "inventory must stay immutable")
				default:
					require.NoError(t, os.Remove(unused))
				}
				return nil
			})
			assert.ErrorIs(t, err, ErrConflict)
			switch change {
			case "read source":
				data, readErr := os.ReadFile(filepath.Join(config.Root, "index.md"))
				require.NoError(t, readErr)
				assert.Equal(t, "# External change\n", string(data))
			case "unread source":
				data, readErr := os.ReadFile(unused)
				require.NoError(t, readErr)
				assert.Equal(t, "new payload", string(data))
			case "new source":
				data, readErr := os.ReadFile(filepath.Join(config.Root, "new.txt"))
				require.NoError(t, readErr)
				assert.Equal(t, "new payload", string(data))
			default:
				_, statErr := os.Stat(unused)
				assert.True(t, os.IsNotExist(statErr))
			}
		})
	}
}

func TestReadViewFinalScanRehashesUnreadFilesDespiteReusableMetadata(t *testing.T) {
	service, config := managedFixture(t)
	filename := filepath.Join(config.Root, "unused.txt")
	require.NoError(t, os.WriteFile(filename, []byte("old payload"), 0o600))
	reviewed, err := service.Project(context.Background())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filename, []byte("new payload"), 0o600))
	observable, err := os.Stat(filename)
	require.NoError(t, err)
	// Model a filesystem whose observable metadata still permits reuse of a
	// warmed old hash. Only the final uncached bytes can detect this change.
	state := service.cached.Files["unused.txt"]
	state.Info = observable
	service.cached.Files["unused.txt"] = state
	err = service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
		assert.Equal(t, reviewed.Revision, snapshot.Revision)
		_, readErr := fs.ReadFile(snapshot.FS, "index.md")
		return readErr
	})
	assert.ErrorIs(t, err, ErrConflict)
	data, err := os.ReadFile(filename)
	require.NoError(t, err)
	assert.Equal(t, "new payload", string(data))
}

func TestReadViewRejectsSymlinkSourcesAndRootWithoutFollowingThem(t *testing.T) {
	t.Run("source changed into symlink", func(t *testing.T) {
		service, config := managedFixture(t)
		filename := filepath.Join(config.Root, "example.txt")
		require.NoError(t, os.WriteFile(filename, []byte("original"), 0o600))
		external := filepath.Join(t.TempDir(), "private.txt")
		require.NoError(t, os.WriteFile(external, []byte("outside-private-value"), 0o600))
		err := service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
			require.NoError(t, os.Remove(filename))
			if linkErr := os.Symlink(external, filename); linkErr != nil {
				t.Skipf("symlinks unavailable: %v", linkErr)
			}
			data, readErr := fs.ReadFile(snapshot.FS, "example.txt")
			assert.ErrorIs(t, readErr, ErrInvalid)
			assert.Empty(t, data)
			assert.NotContains(t, readErr.Error(), external)
			return nil
		})
		assert.ErrorIs(t, err, ErrInvalid)
		data, err := os.ReadFile(external)
		require.NoError(t, err)
		assert.Equal(t, "outside-private-value", string(data))
	})
	for _, phase := range []string{"before callback", "during callback"} {
		t.Run(phase, func(t *testing.T) {
			service, config := managedFixture(t)
			moved := config.Root + ".retained"
			linked := false
			t.Cleanup(func() {
				if linked {
					require.NoError(t, os.Remove(config.Root))
					require.NoError(t, os.Rename(moved, config.Root))
				}
			})
			replace := func() {
				require.NoError(t, os.Rename(config.Root, moved))
				if linkErr := os.Symlink(moved, config.Root); linkErr != nil {
					require.NoError(t, os.Rename(moved, config.Root))
					t.Skipf("symlinks unavailable: %v", linkErr)
				}
				linked = true
			}
			if phase == "before callback" {
				replace()
			}
			called := false
			err := service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
				called = true
				replace()
				data, readErr := fs.ReadFile(snapshot.FS, "index.md")
				assert.ErrorIs(t, readErr, ErrConflict)
				assert.Empty(t, data)
				return nil
			})
			if phase == "before callback" {
				assert.ErrorIs(t, err, ErrInvalid)
				assert.False(t, called)
			} else {
				assert.ErrorIs(t, err, ErrConflict)
				assert.True(t, called)
			}
			data, err := os.ReadFile(filepath.Join(moved, "index.md"))
			require.NoError(t, err)
			assert.Equal(t, "# Documentation\n", string(data))
		})
	}
}

type readViewStatusBackend struct {
	Backend
	status func(context.Context) (SyncStatus, error)
}

func (backend readViewStatusBackend) Status(ctx context.Context) (SyncStatus, error) {
	return backend.status(ctx)
}

func TestReadViewRejectsChangedStorageIdentityOrUnavailableStatus(t *testing.T) {
	for _, change := range []string{"branch", "head", "initial status error", "final status error"} {
		t.Run(change, func(t *testing.T) {
			service, _ := managedFixture(t)
			original := service.cfg.Storage
			failure := errors.New("backend unavailable")
			status := SyncStatus{Backend: "local", State: "ready", Branch: "main", Head: strings.Repeat("a", 40)}
			calls := 0
			service.cfg.Storage = readViewStatusBackend{Backend: original, status: func(context.Context) (SyncStatus, error) {
				calls++
				if change == "initial status error" || change == "final status error" && calls == 3 {
					return SyncStatus{}, failure
				}
				return status, nil
			}}
			called := false
			err := service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
				called = true
				_, readErr := fs.ReadFile(snapshot.FS, "index.md")
				require.NoError(t, readErr)
				if change == "branch" {
					status.Branch = "other"
				}
				if change == "head" {
					status.Head = strings.Repeat("b", 40)
				}
				return nil
			})
			if strings.Contains(change, "error") {
				assert.ErrorIs(t, err, failure)
			} else {
				assert.ErrorIs(t, err, ErrConflict)
			}
			assert.Equal(t, change != "initial status error", called)
		})
	}
}

func TestReadViewBoundsChangedPayloadAndMetadataInventory(t *testing.T) {
	service, config := managedFixture(t)
	retained := saveText(t, service, "recoverable.md", "# Recoverable draft\n")
	oversized := bytes.Repeat([]byte("x"), MaxTextBytes+1)
	err := service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
		require.NoError(t, os.WriteFile(filepath.Join(config.Root, "index.md"), oversized, 0o600))
		data, readErr := fs.ReadFile(snapshot.FS, "index.md")
		assert.ErrorIs(t, readErr, ErrLimit)
		assert.Empty(t, data)
		return readErr
	})
	assert.ErrorIs(t, err, ErrLimit)
	after, err := service.ReadDraft(context.Background(), retained.ID)
	require.NoError(t, err)
	assert.Equal(t, retained, after)
	data, err := os.ReadFile(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, oversized, data)
	rootInfo, err := os.Stat(config.Root)
	require.NoError(t, err)
	_, err = newReadViewFS(context.Background(), config.Root, rootInfo, sourceTree{Dirs: make([]string, MaxFiles+1)})
	assert.ErrorIs(t, err, ErrLimit)
}

func TestReadViewConcurrentReadsOwnIndependentBytesAndCloseSafely(t *testing.T) {
	service, _ := managedFixture(t)
	var retained fs.FS
	err := service.ReadView(context.Background(), func(_ context.Context, snapshot ReadSnapshot) error {
		retained = snapshot.FS
		var readers sync.WaitGroup
		failures := make(chan error, 8)
		for range 8 {
			readers.Go(func() {
				for range 16 {
					data, readErr := fs.ReadFile(snapshot.FS, "index.md")
					if readErr != nil {
						failures <- readErr
						return
					}
					if string(data) != "# Documentation\n" {
						failures <- fmt.Errorf("a concurrent reader observed another buffer")
						return
					}
					data[0] = '!'
				}
			})
		}
		readers.Wait()
		close(failures)
		for failure := range failures {
			assert.NoError(t, failure)
		}
		return nil
	})
	require.NoError(t, err)
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			_, readErr := fs.ReadFile(retained, "index.md")
			assert.ErrorIs(t, readErr, fs.ErrClosed)
		})
	}
	readers.Wait()
}
