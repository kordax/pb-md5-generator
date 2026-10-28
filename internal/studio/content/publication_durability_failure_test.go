package content

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Observe actual transaction state rather than Status call counts. The backend
// still returns its real status; only the private directory's mode is changed.
type publicationDurabilityPermissionBackend struct {
	Backend
	root, state, phase string
	candidate          []byte
	mode               os.FileMode
	once               sync.Once
	observed           bool
	restrictionErr     error
}

func (backend *publicationDurabilityPermissionBackend) Status(ctx context.Context) (SyncStatus, error) {
	status, err := backend.Backend.Status(ctx)
	if err != nil {
		return status, err
	}
	source, err := os.ReadFile(filepath.Join(backend.root, "index.md"))
	if err != nil || !bytes.Equal(source, backend.candidate) {
		return status, nil
	}
	data, err := os.ReadFile(filepath.Join(backend.state, publicationJournalName))
	if err != nil {
		return status, nil
	}
	var journal publicationJournal
	if json.Unmarshal(data, &journal) != nil || journal.Phase != backend.phase {
		return status, nil
	}
	backend.once.Do(func() {
		backend.observed = true
		backend.restrictionErr = os.Chmod(backend.state, backend.mode&^0o222)
	})
	return status, nil
}

func TestPublicationDurabilityPermissionFailuresRetainInstalledSiteUntilRestart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("effective root bypasses the directory permission denial")
	}
	for _, phase := range []string{"prepared", "published"} {
		t.Run(phase, func(t *testing.T) {
			fixture := preparePublicLifecycleFixture(t)
			saved := readLifecycleDraftBytes(t, fixture)
			info, err := os.Stat(fixture.config.StateDir)
			require.NoError(t, err)
			mode := info.Mode().Perm()
			t.Cleanup(func() { assert.NoError(t, os.Chmod(fixture.config.StateDir, mode)) })
			observer := &publicationDurabilityPermissionBackend{Backend: fixture.service.cfg.Storage, root: fixture.config.Root, state: fixture.config.StateDir, phase: phase, candidate: fixture.replacement, mode: mode}
			fixture.service.cfg.Storage = observer

			result, err := fixture.service.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
			require.True(t, observer.observed, "the requested actual transaction phase must be reached")
			require.NoError(t, observer.restrictionErr)
			require.NoError(t, err)
			require.True(t, result.Published, "installed sources and output must remain a logical publication")
			require.NotEmpty(t, result.Sync.Error, "the retained durability failure must be visible")
			source, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
			require.NoError(t, err)
			require.Equal(t, fixture.replacement, source)
			site, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			require.NotEmpty(t, site)
			require.NotEqual(t, fixture.previousSite, site)
			data, err := os.ReadFile(filepath.Join(fixture.config.StateDir, publicationJournalName))
			require.NoError(t, err)
			var journal publicationJournal
			require.NoError(t, json.Unmarshal(data, &journal))
			require.Equal(t, phase, journal.Phase)
			if phase == "prepared" {
				assertLifecycleRetainedBeforeImage(t, fixture)
			}
			_, err = fixture.service.Project(t.Context())
			require.ErrorIs(t, err, ErrConflict)
			if phase == "prepared" {
				assert.Equal(t, saved, readLifecycleDraftBytes(t, fixture))
			}

			require.NoError(t, os.Chmod(fixture.config.StateDir, mode))
			reopened, err := fixture.reopen(t)
			require.NoError(t, err, "repairing only permissions must allow known installed After to recover")
			file, err := reopened.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.replacement), file.Content)
			recoveredSite, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, site, recoveredSite)
			assertNoLifecycleTransactionArtifacts(t, fixture)
			if phase == "prepared" {
				assertLifecycleDraftRecovered(t, fixture, reopened)
				assert.Equal(t, saved, readLifecycleDraftBytes(t, fixture))
			}
		})
	}
}
