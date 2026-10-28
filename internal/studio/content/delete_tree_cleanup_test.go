package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompletedRollbackRecoversJournalUnlinkCrashOnlyForExactOriginalState(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "original state"
		if changed {
			name = "external edit"
		}
		t.Run(name, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			deletion, before, after, draft := interruptedDeleteTree(t, s, "guide")
			require.NoError(t, deletion.install(t.Context()))
			require.NoError(t, deletion.restoreSources(t.Context(), before, after))
			require.NoError(t, deletion.rollbackHistory(t.Context()))
			// Simulate death after stage removal but before journal unlink.
			require.NoError(t, removeStorageTree(cfg.StateDir, deletion.name()))
			if changed {
				require.NoError(t, writeSource(cfg.Root, "index.md", []byte("# Preserve external source\n")))
			}
			require.NoError(t, s.Close())
			reopened, err := New(cfg)
			if changed {
				require.ErrorIs(t, err, ErrConflict)
				_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
				require.NoError(t, err)
				actual, readErr := readSource(cfg.Root, "index.md", MaxTextBytes)
				require.NoError(t, readErr)
				assert.Equal(t, "# Preserve external source\n", string(actual))
				require.NoError(t, writeSource(cfg.Root, "index.md", []byte("# Documentation\n")))
				reopened, err = New(cfg)
			}
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			project, err := reopened.Project(t.Context())
			require.NoError(t, err)
			assert.Equal(t, before.Revision, project.Revision)
			retained, err := reopened.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft, retained)
			_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
			assert.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestRecursiveDeletionPreservesUnrecognizedAndExternallyModifiedHistory(t *testing.T) {
	for _, damage := range []string{"orphan payload", "metadata identity", "missing payload", "history symlink"} {
		t.Run(damage, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			plan := recursivePlan(t, s, "guide")
			draft := stageRecursive(t, s, plan)
			var repair func()
			switch damage {
			case "orphan payload":
				name := "history/" + strings.Repeat("a", 32) + ".bin"
				require.NoError(t, writeSource(cfg.StateDir, name, []byte("Keep this unrecognized history payload\n")))
				repair = func() {
					data, err := readSource(cfg.StateDir, name, MaxTextBytes)
					require.NoError(t, err)
					assert.Equal(t, "Keep this unrecognized history payload\n", string(data))
					require.NoError(t, removeSource(cfg.StateDir, name))
				}
			case "metadata identity", "missing payload":
				versions, err := s.History(t.Context(), "guide/chapter.md")
				require.NoError(t, err)
				require.NotEmpty(t, versions)
				name := "history/" + versions[0].ID + ".json"
				if damage == "missing payload" {
					name = "history/" + versions[0].ID + ".bin"
				}
				data, err := readSource(cfg.StateDir, name, MaxBinaryBytes)
				require.NoError(t, err)
				if damage == "metadata identity" {
					changed := strings.Replace(string(data), versions[0].ID, strings.Repeat("a", 32), 1)
					require.NoError(t, writeSource(cfg.StateDir, name, []byte(changed)))
				} else {
					require.NoError(t, removeSource(cfg.StateDir, name))
				}
				repair = func() { require.NoError(t, writeSource(cfg.StateDir, name, data)) }
			case "history symlink":
				outside := t.TempDir()
				require.NoError(t, writeSource(outside, "retain.txt", []byte("External history must remain untouched\n")))
				require.NoError(t, os.Rename(filepath.Join(cfg.StateDir, "history"), filepath.Join(cfg.StateDir, "history-kept")))
				require.NoError(t, os.Symlink(outside, filepath.Join(cfg.StateDir, "history")))
				repair = func() {
					require.NoError(t, os.Remove(filepath.Join(cfg.StateDir, "history")))
					require.NoError(t, os.Rename(filepath.Join(cfg.StateDir, "history-kept"), filepath.Join(cfg.StateDir, "history")))
					data, err := readSource(outside, "retain.txt", MaxTextBytes)
					require.NoError(t, err)
					assert.Equal(t, "External history must remain untouched\n", string(data))
				}
			}
			_, err := s.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
			require.Error(t, err)
			_, err = readSource(cfg.Root, "guide/chapter.md", MaxTextBytes)
			require.NoError(t, err)
			retained, err := s.ReadDraft(t.Context(), draft.ID)
			require.NoError(t, err)
			assert.Equal(t, draft, retained)
			repair()
			publishDraft(t, s, draft)
		})
	}
}

func TestPublishedDeletionRefusesModifiedHistoryAndSnapshotsUntilRepair(t *testing.T) {
	s, cfg := recursiveFixture(t)
	deletion, before, after, _ := interruptedDeleteTree(t, s, "guide")
	require.NoError(t, deletion.install(t.Context()))
	applyInterruptedDeletion(t, deletion, before, after)
	require.NoError(t, deletion.markPublished())
	marker, err := readSource(cfg.StateDir, "history/"+deleteTreeGeneration, 128)
	require.NoError(t, err)
	require.NoError(t, writeSource(cfg.StateDir, "history/"+deleteTreeGeneration, []byte(strings.Repeat("b", 32))))
	require.NoError(t, s.Close())
	_, err = New(cfg)
	assert.ErrorIs(t, err, ErrOwnership)
	_, err = readSource(deletion.directory(), "backup/guide/chapter.md", MaxTextBytes)
	require.NoError(t, err)
	require.NoError(t, writeSource(cfg.StateDir, "history/"+deleteTreeGeneration, marker))
	reopened, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
}
