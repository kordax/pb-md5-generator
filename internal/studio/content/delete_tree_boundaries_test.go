package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecursiveDeletionCancellationAndRevocationAfterReservationAreAtomic(t *testing.T) {
	for _, mode := range []string{"cancel", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			plan := recursivePlan(t, s, "guide")
			draft := stageRecursive(t, s, plan)
			historyBefore, _, err := historyGeneration(t.Context(), filepath.Join(cfg.StateDir, "history"))
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			denied := errors.New("fresh authorization revoked")
			observed := false
			s.cfg.AuthorizeDeleteTree = func(ctx context.Context, actor Actor, entries []Entry) error {
				assert.Equal(t, editor.ID, actor.ID)
				if _, err := readSource(cfg.StateDir, deleteTreeJournalName, 256<<10); err == nil {
					observed = true
					if mode == "cancel" {
						cancel()
						return ctx.Err()
					}
					return denied
				}
				return nil
			}
			_, err = s.Publish(ctx, PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
			if mode == "cancel" {
				assert.ErrorIs(t, err, context.Canceled)
			} else {
				assert.ErrorIs(t, err, denied)
			}
			assert.True(t, observed)
			s.cfg.AuthorizeDeleteTree = allowDeleteTree
			project, err := s.Project(t.Context())
			require.NoError(t, err)
			assert.Equal(t, plan.Revision, project.Revision)
			historyAfter, _, err := historyGeneration(t.Context(), filepath.Join(cfg.StateDir, "history"))
			require.NoError(t, err)
			assert.Equal(t, historyBefore, historyAfter)
			retained, err := s.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft, retained)
			_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
			assert.ErrorIs(t, err, ErrNotFound)
			publishDraft(t, s, draft)
		})
	}
}

func TestRecursiveDeletionAuthorizationReceivesOnlyBoundedCopiesAndFailsClosed(t *testing.T) {
	s, _ := recursiveFixture(t)
	project, err := s.Project(t.Context())
	require.NoError(t, err)
	input := DeleteTreeInput{Path: "guide", Revision: project.Revision}
	denied := errors.New("full inventory denied")
	s.cfg.AuthorizeDeleteTree = func(_ context.Context, _ Actor, entries []Entry) error {
		if len(entries) > 1 {
			for _, entry := range entries {
				assert.Empty(t, entry.ID)
				assert.Empty(t, entry.Title)
				assert.Empty(t, entry.OutputPath)
				assert.Empty(t, entry.Revision)
			}
			return denied
		}
		return nil
	}
	plan, err := s.PlanDeleteTree(t.Context(), input, editor)
	assert.ErrorIs(t, err, denied)
	assert.Empty(t, plan.Entries)
	assert.Zero(t, plan.TotalBytes)
	s.cfg.AuthorizeDeleteTree = func(_ context.Context, _ Actor, entries []Entry) error {
		entries[0].Path = "unrelated"
		return nil
	}
	plan, err = s.PlanDeleteTree(t.Context(), input, editor)
	require.NoError(t, err)
	assert.Equal(t, "guide", plan.Path)
	assert.Equal(t, "guide", plan.Entries[0].Path)
	var nilContext context.Context
	_, err = s.PlanDeleteTree(nilContext, input, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = s.PlanDeleteTree(t.Context(), input, Actor{})
	assert.ErrorIs(t, err, ErrInvalid)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.PlanDeleteTree(ctx, input, editor)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRecursiveDeletionDirectoryAndByteCapsPreserveCompleteUnstagedTrees(t *testing.T) {
	t.Run("entry count", func(t *testing.T) {
		s, cfg := managedFixture(t)
		s.cfg.AuthorizeDeleteTree = allowDeleteTree
		for i := 0; i < MaxDeleteTreeEntries-1; i++ {
			require.NoError(t, os.MkdirAll(filepath.Join(cfg.Root, "folders", fmt.Sprintf("folder-%04d", i)), 0o750))
		}
		plan := recursivePlan(t, s, "folders")
		assert.Equal(t, MaxDeleteTreeEntries, plan.DirectoryCount)
		assert.Zero(t, plan.FileCount)
		assert.True(t, plan.Truncated)
		publishDraft(t, s, stageRecursive(t, s, plan))
		_, err := os.Lstat(filepath.Join(cfg.Root, "folders"))
		assert.ErrorIs(t, err, os.ErrNotExist)
		for i := 0; i < MaxDeleteTreeEntries; i++ {
			require.NoError(t, os.MkdirAll(filepath.Join(cfg.Root, "too-many", fmt.Sprintf("folder-%04d", i)), 0o750))
		}
		project, err := s.Project(t.Context())
		require.NoError(t, err)
		denied, err := s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: "too-many", Revision: project.Revision}, editor)
		assert.ErrorIs(t, err, ErrLimit)
		assert.Empty(t, denied.Entries)
	})
	t.Run("payload bytes", func(t *testing.T) {
		s, cfg := managedFixture(t)
		s.cfg.AuthorizeDeleteTree = allowDeleteTree
		payload := make([]byte, MaxBinaryBytes)
		for i := 0; i < 4; i++ {
			require.NoError(t, writeSource(cfg.Root, fmt.Sprintf("large/file-%d.pdf", i), payload))
		}
		plan := recursivePlan(t, s, "large")
		assert.Equal(t, int64(MaxDeleteTreeBytes), plan.TotalBytes)
		require.NoError(t, writeSource(cfg.Root, "large/over.pdf", []byte("one extra byte")))
		project, err := s.Project(t.Context())
		require.NoError(t, err)
		denied, err := s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: "large", Revision: project.Revision}, editor)
		assert.ErrorIs(t, err, ErrLimit)
		assert.Empty(t, denied.Entries)
		for i := 0; i < 4; i++ {
			data, err := readSource(cfg.Root, fmt.Sprintf("large/file-%d.pdf", i), MaxBinaryBytes)
			require.NoError(t, err)
			assert.Equal(t, MaxBinaryBytes, len(data))
		}
	})
}

func TestRecursiveDeletionCannotRemoveHomepageOrTraverseSymlinks(t *testing.T) {
	s, cfg := recursiveFixture(t)
	manifest, err := loadManifest(cfg.Root)
	require.NoError(t, err)
	manifest.Pages[0].Source = "guide/home.md"
	require.NoError(t, writeSource(cfg.Root, "guide/home.md", []byte("# Home\n")))
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, writeSource(cfg.Root, "docs.json", data))
	project, err := s.Project(t.Context())
	require.NoError(t, err)
	_, err = s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: "guide", Revision: project.Revision}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	assert.ErrorContains(t, err, "homepage")
	outside := t.TempDir()
	require.NoError(t, writeSource(outside, "keep.txt", []byte("Outside bytes\n")))
	require.NoError(t, os.Symlink(outside, filepath.Join(cfg.Root, "linked")))
	_, err = s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: "linked", Revision: project.Revision}, editor)
	assert.Error(t, err)
	retained, err := readSource(outside, "keep.txt", MaxTextBytes)
	require.NoError(t, err)
	assert.Equal(t, "Outside bytes\n", string(retained))
}

func TestRecursiveDeletionIgnoresOtherBranchDraftsWithoutDiscardingThem(t *testing.T) {
	s, cfg, _ := gitManagedFixture(t)
	s.cfg.AuthorizeDeleteTree = allowDeleteTree
	publishDraft(t, s, saveText(t, s, "guide/example.js", "const value = 1;\n"))
	mainDraft := saveText(t, s, "guide/example.js", "const unpublished = 2;\n")
	payloadName := "drafts/" + mainDraft.ID + "-" + mainDraft.Revision + ".json"
	payload, err := readSource(cfg.StateDir, payloadName, MaxBinaryBytes)
	require.NoError(t, err)
	require.NoError(t, s.cfg.Storage.CreateBranch(t.Context(), "other"))
	err = s.cfg.Storage.Checkout(t.Context(), "other")
	require.NoError(t, err)
	plan := recursivePlan(t, s, "guide")
	result := publishDraft(t, s, stageRecursive(t, s, plan))
	assert.NotEmpty(t, result.Commit)
	assert.Empty(t, result.Sync.Error)
	retained, err := readSource(cfg.StateDir, payloadName, MaxBinaryBytes)
	require.NoError(t, err)
	assert.Equal(t, payload, retained)
	err = s.cfg.Storage.Checkout(t.Context(), "main")
	require.NoError(t, err)
	current, err := s.ReadDraft(t.Context(), mainDraft.ID)
	require.NoError(t, err)
	assert.Equal(t, mainDraft, current)
}

func TestMalformedDeletionJournalsAndSnapshotsNeverLoseRecoverableState(t *testing.T) {
	for _, damage := range []string{"journal JSON", "escaping selected path", "unknown commit", "staged source", "before image", "symlinked snapshot"} {
		t.Run(damage, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			deletion, _, _, draft := interruptedDeleteTree(t, s, "guide")
			journal, err := readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
			require.NoError(t, err)
			var repair func()
			switch damage {
			case "journal JSON":
				require.NoError(t, writeSource(cfg.StateDir, deleteTreeJournalName, []byte("{")))
				repair = func() { require.NoError(t, writeSource(cfg.StateDir, deleteTreeJournalName, journal)) }
			case "escaping selected path", "unknown commit":
				changed := deletion.journal
				changed.Paths = append([]string(nil), changed.Paths...)
				if damage == "escaping selected path" {
					changed.Paths[0] = "../outside.txt"
				} else {
					changed.Commit = strings.Repeat("Z", 40)
				}
				require.NoError(t, writeStorageJSON(cfg.StateDir, deleteTreeJournalName, changed))
				repair = func() { require.NoError(t, writeSource(cfg.StateDir, deleteTreeJournalName, journal)) }
			case "staged source", "before image":
				name := "source/index.md"
				if damage == "before image" {
					name = "backup/guide/chapter.md"
				}
				original, err := readSource(deletion.directory(), name, MaxTextBytes)
				require.NoError(t, err)
				require.NoError(t, writeSource(deletion.directory(), name, []byte("Modified transaction bytes\n")))
				repair = func() { require.NoError(t, writeSource(deletion.directory(), name, original)) }
			case "symlinked snapshot":
				outside := t.TempDir()
				require.NoError(t, writeSource(outside, "retain.txt", []byte("Never follow snapshot symlinks\n")))
				require.NoError(t, os.Rename(filepath.Join(deletion.directory(), "source"), filepath.Join(deletion.directory(), "source-kept")))
				require.NoError(t, os.Symlink(outside, filepath.Join(deletion.directory(), "source")))
				repair = func() {
					require.NoError(t, os.Remove(filepath.Join(deletion.directory(), "source")))
					require.NoError(t, os.Rename(filepath.Join(deletion.directory(), "source-kept"), filepath.Join(deletion.directory(), "source")))
					retained, err := readSource(outside, "retain.txt", MaxTextBytes)
					require.NoError(t, err)
					assert.Equal(t, "Never follow snapshot symlinks\n", string(retained))
				}
			}
			require.NoError(t, s.Close())
			_, err = New(cfg)
			require.Error(t, err)
			retained, err := readSource(cfg.Root, "guide/chapter.md", MaxTextBytes)
			require.NoError(t, err)
			assert.Equal(t, "# Chapter\n", string(retained))
			_, err = readSource(cfg.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json", MaxBinaryBytes)
			require.NoError(t, err)
			repair()
			reopened, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
		})
	}
}
