package content

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ordinaryRollbackFaultBackend struct {
	Backend
	path      string
	installed []byte
	failure   error
	triggered bool
	faultErr  error
}

func (backend *ordinaryRollbackFaultBackend) Status(ctx context.Context) (SyncStatus, error) {
	status, err := backend.Backend.Status(ctx)
	if err != nil || backend.triggered {
		return status, err
	}
	data, err := os.ReadFile(backend.path)
	if err != nil || !bytes.Equal(data, backend.installed) {
		return status, nil
	}
	// Candidate compilation writes elsewhere. Only the live installed source
	// arms this one-shot failure, after applyFiles and before the output swap.
	backend.triggered = true
	if err := os.Remove(backend.path); err != nil {
		backend.faultErr = err
	} else if err := os.Mkdir(backend.path, 0o700); err != nil {
		backend.faultErr = err
	} else {
		backend.faultErr = os.WriteFile(filepath.Join(backend.path, "unmanaged-child"), []byte("preserve external obstruction\n"), 0o600)
	}
	return SyncStatus{}, errors.Join(backend.failure, backend.faultErr)
}

func TestOrdinaryPublicationFailedRollbackRetainsExactSourceBackup(t *testing.T) {
	service, config := managedFixture(t)
	const original = "# Original publication\r\n\r\nRetain exact rollback bytes: π.\r\n"
	const replacement = "# Replacement publication\n\nThis must not replace the last successful site.\n"
	publishDraft(t, service, saveText(t, service, "index.md", original))
	live := filepath.Join(config.Root, "index.md")
	originalSource, err := os.ReadFile(live)
	require.NoError(t, err)
	require.True(t, bytes.Equal([]byte(original), originalSource))
	publishedSite, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	draft := saveText(t, service, "index.md", replacement)
	metadataPath := filepath.Join(config.StateDir, "drafts", draft.ID+".meta.json")
	payloadPath := filepath.Join(config.StateDir, "drafts", draft.ID+"-"+draft.Revision+".json")
	metadataBefore, err := os.ReadFile(metadataPath)
	require.NoError(t, err)
	payloadBefore, err := os.ReadFile(payloadPath)
	require.NoError(t, err)
	injected := errors.New("injected post-apply verification failure")
	backend := &ordinaryRollbackFaultBackend{Backend: service.cfg.Storage, path: live, installed: []byte(replacement), failure: injected}
	service.cfg.Storage = backend
	result, err := service.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.True(t, backend.triggered, "verification must observe the new live source before the injected failure")
	require.NoError(t, backend.faultErr, "the rollback obstruction must have been installed completely")
	assert.ErrorIs(t, err, injected)
	assert.False(t, result.Published)
	obstruction, err := os.ReadFile(filepath.Join(live, "unmanaged-child"))
	require.NoError(t, err)
	assert.True(t, bytes.Equal([]byte("preserve external obstruction\n"), obstruction))
	metadataAfter, err := os.ReadFile(metadataPath)
	require.NoError(t, err)
	payloadAfter, err := os.ReadFile(payloadPath)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(metadataBefore, metadataAfter), "failed publication must retain draft metadata")
	assert.True(t, bytes.Equal(payloadBefore, payloadAfter), "failed publication must retain the exact saved draft")
	currentSite, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(publishedSite, currentSite), "the last successful site must remain unchanged")
	transactions, err := filepath.Glob(filepath.Join(config.StateDir, ".txn-*"))
	require.NoError(t, err)
	require.NotEmpty(t, transactions, "failed source rollback must retain its private transaction backup")
	retained := false
	for _, transaction := range transactions {
		backup, err := os.ReadFile(filepath.Join(transaction, "backup", "index.md"))
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		retained = retained || bytes.Equal(originalSource, backup)
	}
	require.True(t, retained, "the retained backup must contain the exact original source bytes")
	_, err = service.Project(t.Context())
	assert.ErrorIs(t, err, ErrConflict, "ambiguous rollback must block public reads")
	_, err = service.Read(t.Context(), "index.md")
	assert.ErrorIs(t, err, ErrConflict)
	require.NoError(t, service.Close())
	_, err = New(config)
	require.Error(t, err, "restart must not overwrite the independent obstruction")
	obstruction, err = os.ReadFile(filepath.Join(live, "unmanaged-child"))
	require.NoError(t, err)
	assert.True(t, bytes.Equal([]byte("preserve external obstruction\n"), obstruction))
	// An operator resolves the unknown filesystem state using the retained
	// original bytes. Only then can startup verify and finish the rollback.
	require.NoError(t, os.Remove(filepath.Join(live, "unmanaged-child")))
	require.NoError(t, os.Remove(live))
	require.NoError(t, os.WriteFile(live, originalSource, 0o600))
	recovered, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, recovered.Close()) })
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.True(t, bytes.Equal(originalSource, []byte(file.Content)))
	saved, err := recovered.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft.Revision, saved.Revision)
	currentSite, err = os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(publishedSite, currentSite))
	for _, transaction := range transactions {
		_, err := os.Stat(transaction)
		assert.True(t, os.IsNotExist(err), "verified rollback cleanup should remove its own completed transaction")
	}
}
