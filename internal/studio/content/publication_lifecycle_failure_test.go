package content

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type publicationLifecycleDraftBytes struct {
	metadata []byte
	payload  []byte
}

func preparePublicLifecycleFixture(t *testing.T) publicationRecoveryFixture {
	t.Helper()
	fixture := prepareUnjournaledPublicationFixture(t)
	require.NoError(t, removeStorageTree(fixture.config.StateDir, filepath.Base(fixture.stage)))
	return fixture
}

func readLifecycleDraftBytes(t *testing.T, fixture publicationRecoveryFixture) publicationLifecycleDraftBytes {
	t.Helper()
	metadata, err := os.ReadFile(filepath.Join(fixture.config.StateDir, "drafts", fixture.draft.ID+".meta.json"))
	require.NoError(t, err)
	payload, err := os.ReadFile(filepath.Join(fixture.config.StateDir, "drafts", fixture.draft.ID+"-"+fixture.draft.Revision+".json"))
	require.NoError(t, err)
	return publicationLifecycleDraftBytes{metadata: metadata, payload: payload}
}

func makeLifecycleDirectoryReadOnly(t *testing.T, directory string) func() {
	t.Helper()
	info, err := os.Stat(directory)
	require.NoError(t, err)
	mode := info.Mode().Perm()
	t.Cleanup(func() { assert.NoError(t, os.Chmod(directory, mode)) })
	require.NoError(t, os.Chmod(directory, mode&^0o222))
	return func() { require.NoError(t, os.Chmod(directory, mode)) }
}

func assertLifecycleOriginalPublication(t *testing.T, fixture publicationRecoveryFixture) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.original, source)
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, output)
}

func assertLifecycleDraftRecovered(t *testing.T, fixture publicationRecoveryFixture, service *Service) {
	t.Helper()
	draft, err := service.ReadDraft(t.Context(), fixture.draft.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.draft.Revision, draft.Revision)
	assert.Equal(t, fixture.draft.BaseRevision, draft.BaseRevision)
	assert.Equal(t, fixture.draft.Actor.ID, draft.Actor.ID)
	assert.Equal(t, draftChanges(fixture.draft), draftChanges(draft))
}

func assertNoLifecycleTransactionArtifacts(t *testing.T, fixture publicationRecoveryFixture) {
	t.Helper()
	require.NoFileExists(t, filepath.Join(fixture.config.StateDir, publicationJournalName))
	entries, err := os.ReadDir(fixture.config.StateDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), publicationTransactionPrefix), "unrecovered publication: %s", entry.Name())
	}
}

func assertLifecycleRetainedBeforeImage(t *testing.T, fixture publicationRecoveryFixture) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture.config.StateDir, publicationJournalName))
	require.NoError(t, err)
	var journal publicationJournal
	require.NoError(t, json.Unmarshal(data, &journal))
	require.NotEmpty(t, journal.ID)
	stage := filepath.Join(fixture.config.StateDir, publicationTransactionPrefix+journal.ID)
	owner, err := os.ReadFile(filepath.Join(stage, ".storage-owner"))
	require.NoError(t, err)
	assert.Equal(t, journal.ID, string(owner))
	before, err := os.ReadFile(filepath.Join(stage, "backup", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.original, before)
	after, err := os.ReadFile(filepath.Join(stage, "source", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.replacement, after)
}

func TestPublicationLifecycleReadOnlyStateKeepsPublicationAndDraftThenRetries(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("effective root bypasses the directory permission denial")
	}
	fixture := preparePublicLifecycleFixture(t)
	saved := readLifecycleDraftBytes(t, fixture)
	restore := makeLifecycleDirectoryReadOnly(t, fixture.config.StateDir)
	result, err := fixture.service.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
	require.ErrorIs(t, err, os.ErrPermission)
	assert.False(t, result.Published)
	assertLifecycleOriginalPublication(t, fixture)
	assert.Equal(t, saved, readLifecycleDraftBytes(t, fixture))
	assertLifecycleDraftRecovered(t, fixture, fixture.service)
	assertNoLifecycleTransactionArtifacts(t, fixture)
	restore()
	result, err = fixture.service.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
	require.NoError(t, err)
	require.True(t, result.Published)
	current, err := fixture.service.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), current.Content)
	assertNoLifecycleTransactionArtifacts(t, fixture)
}

func TestPublicationLifecycleReadOnlySourceRetainsRecoveryEvidenceAndGuard(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("effective root bypasses the directory permission denial")
	}
	fixture := preparePublicLifecycleFixture(t)
	saved := readLifecycleDraftBytes(t, fixture)
	restore := makeLifecycleDirectoryReadOnly(t, fixture.config.Root)
	result, err := fixture.service.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
	require.ErrorIs(t, err, os.ErrPermission)
	assert.False(t, result.Published)
	assertLifecycleOriginalPublication(t, fixture)
	assert.Equal(t, saved, readLifecycleDraftBytes(t, fixture))
	assertLifecycleRetainedBeforeImage(t, fixture)
	_, err = fixture.service.Project(t.Context())
	require.ErrorIs(t, err, ErrConflict)
	_, err = fixture.service.Read(t.Context(), "index.md")
	require.ErrorIs(t, err, ErrConflict)
	restore()
	reopened, err := fixture.reopen(t)
	require.NoError(t, err)
	assertLifecycleOriginalPublication(t, fixture)
	assertLifecycleDraftRecovered(t, fixture, reopened)
	assert.Equal(t, saved, readLifecycleDraftBytes(t, fixture))
	assertNoLifecycleTransactionArtifacts(t, fixture)
	result, err = reopened.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
	require.NoError(t, err)
	assert.True(t, result.Published)
}

type publicationLifecycleCancellationBackend struct {
	Backend
	filename  string
	candidate []byte
	cancel    context.CancelFunc
	once      sync.Once
	fired     atomic.Bool
}

func (backend *publicationLifecycleCancellationBackend) Status(ctx context.Context) (SyncStatus, error) {
	data, err := os.ReadFile(backend.filename)
	if err == nil && bytes.Equal(data, backend.candidate) {
		backend.once.Do(func() {
			backend.fired.Store(true)
			backend.cancel()
		})
	}
	return backend.Backend.Status(ctx)
}

func TestPublicationLifecycleRequestCancellationAfterLiveWriteKeepsDraftAndOldSite(t *testing.T) {
	fixture := preparePublicLifecycleFixture(t)
	saved := readLifecycleDraftBytes(t, fixture)
	request, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Observe the real live path at storage verification. Cancel the actual
	// request only when its new bytes are present; delegate status unchanged.
	observer := &publicationLifecycleCancellationBackend{Backend: fixture.service.cfg.Storage, filename: filepath.Join(fixture.config.Root, "index.md"), candidate: fixture.replacement, cancel: cancel}
	fixture.service.cfg.Storage = observer
	result, err := fixture.service.Publish(request, PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
	require.True(t, observer.fired.Load(), "cancellation must happen after the actual live write")
	require.ErrorIs(t, request.Err(), context.Canceled)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, result.Published)
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, output)
	assert.Equal(t, saved, readLifecycleDraftBytes(t, fixture))
	if _, err := os.Stat(filepath.Join(fixture.config.StateDir, publicationJournalName)); err == nil {
		assertLifecycleRetainedBeforeImage(t, fixture)
		_, err = fixture.service.Project(t.Context())
		require.ErrorIs(t, err, ErrConflict)
	} else {
		require.ErrorIs(t, err, os.ErrNotExist)
		assertLifecycleOriginalPublication(t, fixture)
		assertNoLifecycleTransactionArtifacts(t, fixture)
	}
	reopened, err := fixture.reopen(t)
	require.NoError(t, err)
	assertLifecycleOriginalPublication(t, fixture)
	assertLifecycleDraftRecovered(t, fixture, reopened)
	assert.Equal(t, saved, readLifecycleDraftBytes(t, fixture))
	assertNoLifecycleTransactionArtifacts(t, fixture)
}
