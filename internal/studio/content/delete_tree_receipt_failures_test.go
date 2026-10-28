package content

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeletionPendingReceiptFailureRetainsProofsUntilExactReceiptIsRepaired(t *testing.T) {
	for _, phase := range []string{"prepared", "published"} {
		t.Run(phase, func(t *testing.T) {
			s, cfg, _ := gitManagedFixture(t)
			s.cfg.AuthorizeDeleteTree, cfg.AuthorizeDeleteTree = allowDeleteTree, allowDeleteTree
			publishDraft(t, s, saveText(t, s, "guide/example.js", "const retained = true;\n"))
			deletion, before, after, draft := interruptedDeleteTree(t, s, "guide")
			require.NoError(t, deletion.install(t.Context()))
			applyInterruptedDeletion(t, deletion, before, after)
			if phase == "published" {
				require.NoError(t, deletion.markPublished())
			}
			require.NoError(t, writeSource(cfg.StateDir, "pending.json", []byte("{broken receipt")))
			status, err := s.cfg.Storage.Status(t.Context())
			require.NoError(t, err)
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			assert.ErrorIs(t, s.recoverDeleteTreePending(canceled, deletion.journal, after, status), context.Canceled)
			require.NoError(t, s.Close())
			_, err = New(cfg)
			assert.ErrorIs(t, err, ErrInvalid)
			assert.Equal(t, deletion.journal.Head, fixtureGit(t, cfg.Root, "rev-parse", "HEAD"))
			unknown, err := readSource(cfg.StateDir, "pending.json", MaxTextBytes)
			require.NoError(t, err)
			assert.Equal(t, "{broken receipt", string(unknown))
			_, err = readSource(deletion.directory(), "backup/guide/example.js", MaxTextBytes)
			require.NoError(t, err)
			hashes := make(map[string]string, len(deletion.journal.Paths))
			for _, name := range deletion.journal.Paths {
				hashes[name] = after.Files[name].Hash
			}
			pending := pendingSync{Revision: after.Revision, Paths: deletion.journal.Paths, Branch: deletion.journal.Branch, Actor: editor, Hashes: hashes}
			bytes, err := json.Marshal(pending)
			require.NoError(t, err)
			require.NoError(t, writeSource(cfg.StateDir, "pending.json", bytes))
			reopened, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			retained, err := readSource(cfg.StateDir, "pending.json", MaxTextBytes)
			require.NoError(t, err)
			assert.Equal(t, bytes, retained)
			_, err = reopened.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
		})
	}
}

func TestDeletionJournalWriteFailuresDoNotGuessCommitAuthority(t *testing.T) {
	for _, phase := range []string{"publication marker", "commit receipt"} {
		t.Run(phase, func(t *testing.T) {
			s, cfg, _ := gitManagedFixture(t)
			s.cfg.AuthorizeDeleteTree, cfg.AuthorizeDeleteTree = allowDeleteTree, allowDeleteTree
			publishDraft(t, s, saveText(t, s, "guide/example.js", "const before = true;\n"))
			deletion, before, after, draft := interruptedDeleteTree(t, s, "guide")
			require.NoError(t, deletion.install(t.Context()))
			applyInterruptedDeletion(t, deletion, before, after)
			var commit string
			if phase == "commit receipt" {
				require.NoError(t, deletion.markPublished())
				var err error
				commit, err = s.cfg.Storage.Commit(t.Context(), deletion.journal.Paths, editor, "Delete example folder")
				require.NoError(t, err)
				require.NotEmpty(t, commit)
			}
			restoreMode := deletionDirectoryMode(t, cfg.StateDir, 0o500)
			if phase == "publication marker" {
				require.Error(t, deletion.markPublished())
			} else {
				require.Error(t, deletion.recordCommit(commit))
			}
			restoreMode()
			_, err := readSource(deletion.directory(), "backup/guide/example.js", MaxTextBytes)
			require.NoError(t, err)
			_, err = readSource(cfg.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json", MaxBinaryBytes)
			require.NoError(t, err)
			require.NoError(t, s.Close())
			reopened, err := New(cfg)
			if phase == "commit receipt" {
				require.ErrorIs(t, err, ErrConflict)
				assert.Equal(t, commit, fixtureGit(t, cfg.Root, "rev-parse", "HEAD"))
				require.NoError(t, deletion.recordCommit(commit))
				reopened, err = New(cfg)
			}
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
		})
	}
}

func TestCompletedDeletionAbortRetainsJournalWhenUnlinkIsDenied(t *testing.T) {
	s, cfg := recursiveFixture(t)
	deletion, _, _, draft := interruptedDeleteTree(t, s, "guide")
	require.NoError(t, removeStorageTree(cfg.StateDir, deletion.name()))
	restoreMode := deletionDirectoryMode(t, cfg.StateDir, 0o500)
	require.Error(t, deletion.cleanup())
	restoreMode()
	assertDeletionBeforeState(t, s, cfg, draft, deletion.journal.PreviousHistory)
	_, err := readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	reopened, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	_, err = reopened.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
}

func TestDeletionRetentionEvictsOnlyOldHistoryWithinStateQuota(t *testing.T) {
	s, cfg := recursiveFixture(t)
	outside := saveText(t, s, "outside.txt", "Keep unrelated unpublished bytes\n")
	plan := recursivePlan(t, s, "guide")
	draft := stageRecursive(t, s, plan)
	versions, err := s.revisions()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(versions), 2)
	for i := 0; i < 2; i++ {
		versions[i].CreatedAt = time.Unix(2, 0).UTC()
		require.NoError(t, writeStorageJSON(cfg.StateDir, "history/"+versions[i].ID+".json", versions[i]))
	}
	largeID, deletedID := strings.Repeat("e", 32), strings.Repeat("d", 32)
	large := Revision{ID: largeID, Path: "old/large.pdf", Revision: plan.Revision, Actor: editor, CreatedAt: time.Unix(1, 0).UTC(), Size: 2 << 20}
	require.NoError(t, writeSource(cfg.StateDir, "history/"+largeID+".bin", make([]byte, large.Size)))
	require.NoError(t, writeStorageJSON(cfg.StateDir, "history/"+largeID+".json", large))
	deleted := Revision{ID: deletedID, Path: "old/deleted.txt", Revision: plan.Revision, Actor: editor, CreatedAt: time.Now().UTC(), Deleted: true}
	require.NoError(t, writeStorageJSON(cfg.StateDir, "history/"+deletedID+".json", deleted))
	total, err := stateSize(cfg.StateDir)
	require.NoError(t, err)
	root, err := os.OpenRoot(cfg.StateDir)
	require.NoError(t, err)
	quota, err := root.Create("quota.bin")
	require.NoError(t, err)
	require.NoError(t, quota.Truncate(MaxStateBytes-total-(128<<10)))
	require.NoError(t, quota.Close())
	require.NoError(t, root.Close())
	publishDraft(t, s, draft)
	_, err = readSource(cfg.StateDir, "history/"+largeID+".json", 64<<10)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = readSource(cfg.StateDir, "history/"+largeID+".bin", MaxBinaryBytes)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = readSource(cfg.StateDir, "history/"+deletedID+".json", 64<<10)
	require.NoError(t, err)
	retained, err := s.ReadDraft(t.Context(), outside.ID)
	require.NoError(t, err)
	assert.Equal(t, outside, retained)
	history, err := s.History(t.Context(), "guide/chapter.md")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(history), 2)
	assert.True(t, history[0].Deleted)
	require.NoError(t, removeSource(cfg.StateDir, "quota.bin"))
}

func TestDeletionOrphanCleanupNeverRemovesUnrecognizedOwnedNamespaceEntries(t *testing.T) {
	s, cfg := recursiveFixture(t)
	name := deleteTreeTransactionPrefix + "not-a-transaction"
	require.NoError(t, writeSource(cfg.StateDir, name, []byte("Keep unknown namespace bytes\n")))
	require.NoError(t, s.Close())
	_, err := New(cfg)
	assert.ErrorIs(t, err, ErrOwnership)
	retained, err := readSource(cfg.StateDir, name, MaxTextBytes)
	require.NoError(t, err)
	assert.Equal(t, "Keep unknown namespace bytes\n", string(retained))
	require.NoError(t, removeSource(cfg.StateDir, name))
	reopened, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	_, err = readSource(filepath.Join(cfg.Root, "guide"), "chapter.md", MaxTextBytes)
	require.NoError(t, err)
}
