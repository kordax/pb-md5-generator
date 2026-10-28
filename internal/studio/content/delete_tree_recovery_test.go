package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func interruptedDeleteTree(t *testing.T, s *Service, folder string) (*deleteTreeTransaction, sourceTree, sourceTree, Draft) {
	t.Helper()
	plan := recursivePlan(t, s, folder)
	draft := stageRecursive(t, s, plan)
	scope, err := s.planDeleteTree(t.Context(), DeleteTreeInput{Path: folder, Revision: plan.Revision}, editor, draft.ID)
	require.NoError(t, err)
	transaction, id, err := createStorageStage(s.cfg.StateDir, deleteTreeTransactionPrefix)
	require.NoError(t, err)
	before, after, err := s.candidate(t.Context(), transaction, expandedDeleteTree(scope), editor, true)
	require.NoError(t, err)
	paths := changedFiles(before, after)
	backup := filepath.Join(transaction, "backup")
	require.NoError(t, os.Mkdir(backup, 0o700))
	require.NoError(t, applyFiles(t.Context(), backup, s.cfg.Root, paths, before))
	status, err := s.cfg.Storage.Status(t.Context())
	require.NoError(t, err)
	deletion, err := s.prepareDeleteTreeTransaction(t.Context(), transaction, id, scope, before, after, paths, draft, PublishInput{}, editor, status)
	require.NoError(t, err)
	return deletion, before, after, draft
}

func TestInterruptedRecursiveDeletionRecoversAtEachDurableStep(t *testing.T) {
	for _, phase := range []string{"prepared", "history removed", "history installed", "partial files", "files without directories", "all sources", "published"} {
		t.Run(phase, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			historyBefore, _, err := historyGeneration(t.Context(), filepath.Join(cfg.StateDir, "history"))
			require.NoError(t, err)
			deletion, before, after, draft := interruptedDeleteTree(t, s, "guide")
			if phase == "history removed" {
				root, err := os.OpenRoot(cfg.StateDir)
				require.NoError(t, err)
				require.NoError(t, root.Rename("history", deletion.name()+"/history-previous"))
				require.NoError(t, root.Close())
			} else if phase != "prepared" {
				require.NoError(t, deletion.install(t.Context()))
			}
			if phase == "partial files" {
				require.NoError(t, applyFiles(t.Context(), cfg.Root, filepath.Join(deletion.directory(), "source"), deletion.journal.Paths[:2], after))
			}
			if phase == "files without directories" || phase == "all sources" || phase == "published" {
				require.NoError(t, applyFiles(t.Context(), cfg.Root, filepath.Join(deletion.directory(), "source"), deletion.journal.Paths, after))
			}
			if phase == "all sources" || phase == "published" {
				require.NoError(t, removeEmptyDirectories(cfg.Root, before.Dirs, after.Dirs))
			}
			if phase == "published" {
				require.NoError(t, deletion.markPublished())
			}
			require.NoError(t, s.Close())
			reopened, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			project, err := reopened.Project(t.Context())
			require.NoError(t, err)
			retained, err := reopened.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft, retained)
			if phase == "all sources" || phase == "published" {
				assert.Equal(t, after.Revision, project.Revision)
				_, err = os.Lstat(filepath.Join(cfg.Root, "guide"))
				assert.ErrorIs(t, err, os.ErrNotExist)
				history, err := reopened.History(t.Context(), "guide/chapter.md")
				require.NoError(t, err)
				require.GreaterOrEqual(t, len(history), 2)
				assert.True(t, history[0].Deleted)
				_, err = reopened.BuildCurrent(t.Context(), editor)
				require.NoError(t, err)
			} else {
				assert.Equal(t, before.Revision, project.Revision)
				historyAfter, _, err := historyGeneration(t.Context(), filepath.Join(cfg.StateDir, "history"))
				require.NoError(t, err)
				assert.Equal(t, historyBefore, historyAfter)
				for _, name := range []string{"guide/chapter.md", "guide/examples/literal.js", "guide/files/manual.pdf"} {
					_, err := readSource(cfg.Root, name, MaxBinaryBytes)
					require.NoError(t, err)
				}
			}
			_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
			assert.ErrorIs(t, err, ErrNotFound)
			_, err = os.Lstat(deletion.directory())
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestRecursiveDeletionRecoveryRefusesExternalChangesAndRetainsOnlyCopies(t *testing.T) {
	for _, name := range []string{"guide/chapter.md", "index.md", "guide/unknown.exe"} {
		t.Run(name, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			deletion, _, _, draft := interruptedDeleteTree(t, s, "guide")
			require.NoError(t, deletion.install(t.Context()))
			original, err := readSource(cfg.Root, name, MaxBinaryBytes)
			if name == "guide/unknown.exe" {
				require.ErrorIs(t, err, ErrNotFound)
			} else {
				require.NoError(t, err)
			}
			external := []byte("Externally edited bytes, never overwritten\n")
			require.NoError(t, writeSource(cfg.Root, name, external))
			require.NoError(t, s.Close())
			_, err = New(cfg)
			require.ErrorIs(t, err, ErrConflict)
			actual, err := readSource(cfg.Root, name, MaxBinaryBytes)
			require.NoError(t, err)
			assert.Equal(t, external, actual)
			_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
			require.NoError(t, err)
			backup, err := readSource(deletion.directory(), "backup/guide/chapter.md", MaxTextBytes)
			require.NoError(t, err)
			assert.Equal(t, "# Chapter\n", string(backup))
			_, err = readSource(cfg.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json", MaxBinaryBytes)
			require.NoError(t, err)
			if original == nil {
				require.NoError(t, removeSource(cfg.Root, name))
			} else {
				require.NoError(t, writeSource(cfg.Root, name, original))
			}
			recovered, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, recovered.Close()) })
			_, err = recovered.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
		})
	}
}

func TestRecursiveDeletionHistoryReservationFailureNeverMutatesLiveSources(t *testing.T) {
	for _, failure := range []string{"history payload", "state quota"} {
		t.Run(failure, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			plan := recursivePlan(t, s, "guide")
			draft := stageRecursive(t, s, plan)
			before, err := readSource(cfg.Root, "docs.json", MaxTextBytes)
			require.NoError(t, err)
			var repair func()
			if failure == "history payload" {
				versions, err := s.History(t.Context(), "guide/chapter.md")
				require.NoError(t, err)
				require.NotEmpty(t, versions)
				name := "history/" + versions[0].ID + ".bin"
				payload, err := readSource(cfg.StateDir, name, MaxBinaryBytes)
				require.NoError(t, err)
				require.NoError(t, removeSource(cfg.StateDir, name))
				require.NoError(t, os.Mkdir(filepath.Join(cfg.StateDir, filepath.FromSlash(name)), 0o700))
				repair = func() {
					require.NoError(t, os.Remove(filepath.Join(cfg.StateDir, filepath.FromSlash(name))))
					require.NoError(t, writeSource(cfg.StateDir, name, payload))
				}
			} else {
				root, err := os.OpenRoot(cfg.StateDir)
				require.NoError(t, err)
				file, err := root.Create("quota.bin")
				require.NoError(t, err)
				require.NoError(t, file.Truncate(MaxStateBytes))
				require.NoError(t, file.Close())
				require.NoError(t, root.Close())
				repair = func() { require.NoError(t, removeSource(cfg.StateDir, "quota.bin")) }
			}
			_, err = s.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
			require.Error(t, err)
			if failure == "state quota" {
				assert.ErrorIs(t, err, ErrLimit)
			}
			actual, err := readSource(cfg.Root, "docs.json", MaxTextBytes)
			require.NoError(t, err)
			assert.Equal(t, before, actual)
			_, err = readSource(cfg.Root, "guide/chapter.md", MaxTextBytes)
			require.NoError(t, err)
			retained, err := s.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft, retained)
			_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
			assert.ErrorIs(t, err, ErrNotFound)
			repair()
			publishDraft(t, s, draft)
		})
	}
}

type deletionVerificationBackend struct {
	Backend
	root          string
	fail          bool
	beforeFailure func() error
}

func (b *deletionVerificationBackend) Status(ctx context.Context) (SyncStatus, error) {
	if b.fail {
		if _, err := os.Lstat(filepath.Join(b.root, "guide")); errors.Is(err, os.ErrNotExist) {
			if b.beforeFailure != nil {
				return SyncStatus{}, errors.Join(errors.New("verification unavailable"), b.beforeFailure())
			}
			return SyncStatus{}, errors.New("verification unavailable")
		}
	}
	return b.Backend.Status(ctx)
}

func TestRecursiveDeletionVerificationFailureRollsBackSourcesAndHistoryThenRetries(t *testing.T) {
	s, cfg := recursiveFixture(t)
	plan := recursivePlan(t, s, "guide")
	draft := stageRecursive(t, s, plan)
	historyBefore, _, err := historyGeneration(t.Context(), filepath.Join(cfg.StateDir, "history"))
	require.NoError(t, err)
	backend := &deletionVerificationBackend{Backend: s.cfg.Storage, root: cfg.Root, fail: true}
	s.cfg.Storage = backend
	_, err = s.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.ErrorContains(t, err, "verification unavailable")
	project, err := s.Project(t.Context())
	require.NoError(t, err)
	assert.Equal(t, plan.Revision, project.Revision)
	historyAfter, _, err := historyGeneration(t.Context(), filepath.Join(cfg.StateDir, "history"))
	require.NoError(t, err)
	assert.Equal(t, historyBefore, historyAfter)
	retained, err := s.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, retained)
	backend.fail = false
	publishDraft(t, s, draft)
}

func TestRecursiveDeletionRestartReclaimsOnlyOwnedOrphans(t *testing.T) {
	s, cfg := recursiveFixture(t)
	transaction, id, err := createStorageStage(cfg.StateDir, deleteTreeTransactionPrefix)
	require.NoError(t, err)
	require.NoError(t, writeSource(transaction, "source/uncommitted.txt", []byte(strings.Repeat("staged", 1024))))
	require.NoError(t, s.Close())
	reopened, err := New(cfg)
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
	_, err = os.Lstat(transaction)
	assert.ErrorIs(t, err, os.ErrNotExist)
	unowned := deleteTreeTransactionPrefix + id
	require.NoError(t, os.Mkdir(filepath.Join(cfg.StateDir, unowned), 0o700))
	require.NoError(t, writeSource(cfg.StateDir, unowned+"/keep.txt", []byte("Keep unknown preparation\n")))
	_, err = New(cfg)
	assert.Error(t, err)
	retained, err := readSource(cfg.StateDir, unowned+"/keep.txt", MaxTextBytes)
	require.NoError(t, err)
	assert.Equal(t, "Keep unknown preparation\n", string(retained))
}

func TestSavedDraftCannotRebindToAnotherNamedBranchAfterWaitingForGate(t *testing.T) {
	s, cfg, _ := gitManagedFixture(t)
	draft := saveText(t, s, "unpublished.txt", "Keep the original branch\n")
	metadataName := "drafts/" + draft.ID + ".meta.json"
	payloadName := "drafts/" + draft.ID + "-" + draft.Revision + ".json"
	metadata, err := readSource(cfg.StateDir, metadataName, MaxBinaryBytes)
	require.NoError(t, err)
	payload, err := readSource(cfg.StateDir, payloadName, MaxBinaryBytes)
	require.NoError(t, err)
	require.NoError(t, s.cfg.Storage.CreateBranch(t.Context(), "other"))
	s.gate <- struct{}{}
	result := make(chan error, 1)
	go func() {
		_, err := s.SaveDraft(t.Context(), DraftInput{ID: draft.ID, Revision: draft.Revision, BaseRevision: draft.BaseRevision, Change: draft.Change}, editor)
		result <- err
	}()
	require.NoError(t, s.cfg.Storage.Checkout(t.Context(), "other"))
	<-s.gate
	assert.ErrorIs(t, <-result, ErrConflict)
	actualMetadata, err := readSource(cfg.StateDir, metadataName, MaxBinaryBytes)
	require.NoError(t, err)
	actualPayload, err := readSource(cfg.StateDir, payloadName, MaxBinaryBytes)
	require.NoError(t, err)
	assert.Equal(t, metadata, actualMetadata)
	assert.Equal(t, payload, actualPayload)
	require.NoError(t, s.cfg.Storage.Checkout(t.Context(), "main"))
	saved, err := s.SaveDraft(t.Context(), DraftInput{ID: draft.ID, Revision: draft.Revision, BaseRevision: draft.BaseRevision, Change: draft.Change}, editor)
	require.NoError(t, err)
	assert.Equal(t, "main", saved.Branch)
	assert.Equal(t, draft.BaseRevision, saved.BaseRevision)
}
