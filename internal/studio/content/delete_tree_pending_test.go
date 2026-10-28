package content

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeletionRecoveryRetainsAmbiguousPendingCommitAndCanRetryAfterRepair(t *testing.T) {
	for _, mode := range []string{"uncommitted", "other branch", "previous committed"} {
		t.Run(mode, func(t *testing.T) {
			s, cfg, remote := gitManagedFixture(t)
			s.cfg.AuthorizeDeleteTree, cfg.AuthorizeDeleteTree = allowDeleteTree, allowDeleteTree
			publishDraft(t, s, saveText(t, s, "guide/example.js", "const original = 1;\n"))
			deletion, before, after, draft := interruptedDeleteTree(t, s, "guide")
			require.NoError(t, deletion.install(t.Context()))
			applyInterruptedDeletion(t, deletion, before, after)
			pending := pendingSync{Revision: strings.Repeat("a", 64), Paths: deletion.journal.Paths, Branch: deletion.journal.Branch, Actor: editor, Hashes: map[string]string{}}
			if mode != "uncommitted" {
				pending.Commit = deletion.journal.Head
			}
			if mode == "other branch" {
				pending.Branch = "another"
			}
			require.NoError(t, s.writeState("pending.json", pending))
			bytesBefore, err := readSource(cfg.StateDir, "pending.json", MaxTextBytes)
			require.NoError(t, err)
			require.NoError(t, s.Close())
			reopened, err := New(cfg)
			if mode != "previous committed" {
				require.ErrorIs(t, err, ErrConflict)
				actual, readErr := readSource(cfg.StateDir, "pending.json", MaxTextBytes)
				require.NoError(t, readErr)
				assert.Equal(t, bytesBefore, actual)
				_, readErr = readSource(deletion.directory(), "backup/guide/example.js", MaxTextBytes)
				require.NoError(t, readErr)
				require.NoError(t, removeSource(cfg.StateDir, "pending.json"))
				reopened, err = New(cfg)
			}
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			recovered, err := reopened.pending()
			require.NoError(t, err)
			assert.Equal(t, after.Revision, recovered.Revision)
			assert.Equal(t, deletion.journal.Branch, recovered.Branch)
			project, err := reopened.Project(t.Context())
			require.NoError(t, err)
			status, err := reopened.Push(t.Context(), project.Revision)
			require.NoError(t, err)
			assert.Empty(t, status.Error)
			assert.False(t, status.Retryable)
			assert.NotEqual(t, deletion.journal.Head, status.Head)
			assert.Equal(t, status.Head, fixtureGit(t, remote, "rev-parse", "refs/heads/main"))
			project, err = reopened.Project(t.Context())
			require.NoError(t, err)
			again, err := reopened.Push(t.Context(), project.Revision)
			require.NoError(t, err)
			assert.Equal(t, status.Head, again.Head)
			retained, err := reopened.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft, retained)
		})
	}
}

func TestFailedDeletionRollbackRetainsSnapshotsHistoryAndDraftUntilRecovery(t *testing.T) {
	s, cfg := recursiveFixture(t)
	plan := recursivePlan(t, s, "guide")
	draft := stageRecursive(t, s, plan)
	originalHome, err := readSource(cfg.Root, "index.md", MaxTextBytes)
	require.NoError(t, err)
	s.cfg.Storage = &deletionVerificationBackend{Backend: s.cfg.Storage, root: cfg.Root, fail: true, beforeFailure: func() error {
		return writeSource(cfg.Root, "index.md", []byte("# Preserve concurrent external edit\n"))
	}}
	_, err = s.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.ErrorContains(t, err, "verification unavailable")
	assert.ErrorIs(t, err, ErrConflict)
	_, err = s.Project(t.Context())
	assert.ErrorIs(t, err, ErrConflict)
	journalBytes, err := readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
	require.NoError(t, err)
	assert.NotEmpty(t, journalBytes)
	entries, err := filepath.Glob(filepath.Join(cfg.StateDir, deleteTreeTransactionPrefix+"*"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	beforeImage, err := readSource(entries[0], "backup/guide/chapter.md", MaxTextBytes)
	require.NoError(t, err)
	assert.Equal(t, "# Chapter\n", string(beforeImage))
	_, err = readSource(cfg.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json", MaxBinaryBytes)
	require.NoError(t, err)
	external, err := readSource(cfg.Root, "index.md", MaxTextBytes)
	require.NoError(t, err)
	assert.Equal(t, "# Preserve concurrent external edit\n", string(external))
	require.NoError(t, s.Close())
	_, err = New(cfg)
	assert.ErrorIs(t, err, ErrConflict)
	require.NoError(t, writeSource(cfg.Root, "index.md", originalHome))
	reopened, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	history, err := reopened.History(t.Context(), "guide/chapter.md")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(history), 2)
	assert.True(t, history[0].Deleted)
	retained, err := reopened.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, retained)
}
