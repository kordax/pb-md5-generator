package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deletionPreparation struct {
	service   *Service
	config    Config
	draft     Draft
	scope     deleteTreeScope
	before    sourceTree
	after     sourceTree
	directory string
	id        string
}

func deletionBeforeJournal(t *testing.T) deletionPreparation {
	t.Helper()
	s, cfg := recursiveFixture(t)
	plan := recursivePlan(t, s, "guide")
	draft := stageRecursive(t, s, plan)
	scope, err := s.planDeleteTree(t.Context(), DeleteTreeInput{Path: plan.Path, Revision: plan.Revision}, editor, draft.ID)
	require.NoError(t, err)
	directory, id, err := createStorageStage(cfg.StateDir, deleteTreeTransactionPrefix)
	require.NoError(t, err)
	before, after, err := s.candidate(t.Context(), directory, expandedDeleteTree(scope), editor, true)
	require.NoError(t, err)
	backup := filepath.Join(directory, "backup")
	require.NoError(t, os.Mkdir(backup, 0o700))
	require.NoError(t, applyFiles(t.Context(), backup, cfg.Root, changedFiles(before, after), before))
	return deletionPreparation{service: s, config: cfg, draft: draft, scope: scope, before: before, after: after, directory: directory, id: id}
}

func (p deletionPreparation) prepare(t *testing.T) (*deleteTreeTransaction, error) {
	t.Helper()
	status, err := p.service.cfg.Storage.Status(t.Context())
	require.NoError(t, err)
	return p.service.prepareDeleteTreeTransaction(t.Context(), p.directory, p.id, p.scope, p.before, p.after, changedFiles(p.before, p.after), p.draft, PublishInput{}, editor, status)
}

func assertDeletionBeforeState(t *testing.T, s *Service, cfg Config, draft Draft, history string) {
	t.Helper()
	current, err := scanTree(t.Context(), cfg.Root, "\x00", "")
	require.NoError(t, err)
	assert.Equal(t, draft.BaseRevision, current.Revision)
	retained, err := s.readDraft(draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, retained)
	actualHistory, _, err := historyGeneration(t.Context(), filepath.Join(cfg.StateDir, "history"))
	require.NoError(t, err)
	assert.Equal(t, history, actualHistory)
}

func TestDeletionUnavailablePrivatePreparationRetainsSourcesHistoryAndDraft(t *testing.T) {
	p := deletionBeforeJournal(t)
	history, _, err := historyGeneration(t.Context(), filepath.Join(p.config.StateDir, "history"))
	require.NoError(t, err)
	moved := p.config.StateDir + "-unavailable"
	require.NoError(t, os.Rename(p.config.StateDir, moved))
	_, err = p.prepare(t)
	require.Error(t, err)
	require.NoError(t, os.Rename(moved, p.config.StateDir))
	assertDeletionBeforeState(t, p.service, p.config, p.draft, history)
	_, err = readSource(p.directory, "backup/guide/chapter.md", MaxTextBytes)
	require.NoError(t, err)
	require.NoError(t, removeStorageTree(p.config.StateDir, filepath.Base(p.directory)))
	publishDraft(t, p.service, p.draft)
}

func TestDeletionDamagedBeforeImageCannotReachLiveMutation(t *testing.T) {
	p := deletionBeforeJournal(t)
	history, _, err := historyGeneration(t.Context(), filepath.Join(p.config.StateDir, "history"))
	require.NoError(t, err)
	require.NoError(t, writeSource(p.directory, "backup/guide/chapter.md", []byte("Changed private before image\n")))
	_, err = p.prepare(t)
	assert.ErrorIs(t, err, ErrConflict)
	assertDeletionBeforeState(t, p.service, p.config, p.draft, history)
	_, err = readSource(p.config.StateDir, deleteTreeJournalName, 256<<10)
	assert.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, removeStorageTree(p.config.StateDir, filepath.Base(p.directory)))
	publishDraft(t, p.service, p.draft)
}

func TestDeletionCleanupOwnershipMismatchRetainsRecoveryCopies(t *testing.T) {
	s, cfg := recursiveFixture(t)
	deletion, _, _, draft := interruptedDeleteTree(t, s, "guide")
	history := deletion.journal.PreviousHistory
	require.NoError(t, writeSource(deletion.directory(), ".storage-owner", []byte("unknown preparation owner")))
	err := deletion.cleanup()
	assert.ErrorIs(t, err, ErrOwnership)
	assertDeletionBeforeState(t, s, cfg, draft, history)
	backup, err := readSource(deletion.directory(), "backup/guide/chapter.md", MaxTextBytes)
	require.NoError(t, err)
	assert.Equal(t, "# Chapter\n", string(backup))
	_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
	require.NoError(t, err)
	require.NoError(t, writeSource(deletion.directory(), ".storage-owner", []byte(deletion.journal.ID)))
	require.NoError(t, deletion.cleanup())
	publishDraft(t, s, draft)
}
