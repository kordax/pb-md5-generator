package content

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deletionDirectoryMode(t *testing.T, name string, mode os.FileMode) func() {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permission failures require Unix permissions")
	}
	info, err := os.Stat(name)
	require.NoError(t, err)
	restore := func() { require.NoError(t, os.Chmod(name, info.Mode().Perm())) }
	t.Cleanup(restore)
	require.NoError(t, os.Chmod(name, mode))
	return restore
}

func TestDeletionHistoryAuditIsReadOnlyDuringPrivateFilesystemFailures(t *testing.T) {
	for _, fault := range []string{"missing history", "unreadable history", "unsearchable history", "canceled audit", "oversized record", "invalid generation", "too many records", "too many entries"} {
		t.Run(fault, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			plan := recursivePlan(t, s, "guide")
			draft := stageRecursive(t, s, plan)
			directory := filepath.Join(cfg.StateDir, "history")
			original, _, err := historyGeneration(t.Context(), directory)
			require.NoError(t, err)
			ctx := t.Context()
			var repair func()
			switch fault {
			case "missing history":
				moved := directory + "-offline"
				require.NoError(t, os.Rename(directory, moved))
				repair = func() { require.NoError(t, os.Rename(moved, directory)) }
			case "unreadable history":
				repair = deletionDirectoryMode(t, directory, 0o100)
			case "unsearchable history":
				repair = deletionDirectoryMode(t, directory, 0o400)
			case "canceled audit":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
				repair = func() {}
			case "oversized record":
				versions, err := s.History(t.Context(), "guide/chapter.md")
				require.NoError(t, err)
				require.NotEmpty(t, versions)
				name := "history/" + versions[0].ID + ".json"
				data, err := readSource(cfg.StateDir, name, 64<<10)
				require.NoError(t, err)
				require.NoError(t, writeSource(cfg.StateDir, name, make([]byte, (64<<10)+1)))
				repair = func() { require.NoError(t, writeSource(cfg.StateDir, name, data)) }
			case "invalid generation":
				require.NoError(t, writeSource(directory, deleteTreeGeneration, []byte("invalid receipt")))
				repair = func() { require.NoError(t, removeSource(directory, deleteTreeGeneration)) }
			case "too many records", "too many entries":
				count := MaxHistory + 1
				if fault == "too many entries" {
					count = 2*MaxHistory + 2
				}
				created := make([]string, 0, count)
				for i := 0; i < count; i++ {
					id := fmt.Sprintf("%032x", i+1000)
					name := id + ".json"
					data, err := json.Marshal(Revision{ID: id, Path: "old/file.txt", Revision: plan.Revision, Deleted: true})
					require.NoError(t, err)
					require.NoError(t, writeSource(directory, name, data))
					created = append(created, name)
				}
				repair = func() {
					for _, name := range created {
						require.NoError(t, removeSource(directory, name))
					}
				}
			}
			_, _, err = historyGeneration(ctx, directory)
			require.Error(t, err)
			repair()
			assertDeletionBeforeState(t, s, cfg, draft, original)
			publishDraft(t, s, draft)
		})
	}
}

func TestDeletionPreparationRejectsLostProofsAndUnwritableJournalBeforeLiveChange(t *testing.T) {
	for _, fault := range []string{"missing before image", "occupied generation", "symlinked output", "unwritable journal", "occupied journal"} {
		t.Run(fault, func(t *testing.T) {
			p := deletionBeforeJournal(t)
			history, _, err := historyGeneration(t.Context(), filepath.Join(p.config.StateDir, "history"))
			require.NoError(t, err)
			var repair func()
			switch fault {
			case "missing before image":
				require.NoError(t, removeSource(p.directory, "backup/guide/chapter.md"))
				repair = func() {}
			case "occupied generation":
				require.NoError(t, writeSource(p.directory, "history-next/retain.txt", []byte("Do not replace unknown preparation\n")))
				repair = func() {
					data, err := readSource(p.directory, "history-next/retain.txt", MaxTextBytes)
					require.NoError(t, err)
					assert.Equal(t, "Do not replace unknown preparation\n", string(data))
				}
			case "symlinked output":
				outside := t.TempDir()
				require.NoError(t, writeSource(outside, "retain.txt", []byte("Never follow output links\n")))
				require.NoError(t, os.Symlink(outside, filepath.Join(p.directory, "site", "unsafe")))
				repair = func() {
					data, err := readSource(outside, "retain.txt", MaxTextBytes)
					require.NoError(t, err)
					assert.Equal(t, "Never follow output links\n", string(data))
				}
			case "unwritable journal":
				repair = deletionDirectoryMode(t, p.config.StateDir, 0o500)
			case "occupied journal":
				require.NoError(t, writeSource(p.config.StateDir, deleteTreeJournalName+"/retain.txt", []byte("Keep unknown journal object\n")))
				repair = func() {
					data, err := readSource(p.config.StateDir, deleteTreeJournalName+"/retain.txt", MaxTextBytes)
					require.NoError(t, err)
					assert.Equal(t, "Keep unknown journal object\n", string(data))
					require.NoError(t, removeStorageTree(p.config.StateDir, deleteTreeJournalName))
				}
			}
			_, err = p.prepare(t)
			require.Error(t, err)
			repair()
			assert.False(t, p.service.deleteTreePending)
			assertDeletionBeforeState(t, p.service, p.config, p.draft, history)
			require.NoError(t, removeStorageTree(p.config.StateDir, filepath.Base(p.directory)))
			publishDraft(t, p.service, p.draft)
		})
	}
}

func TestDeletionHistorySwapFailuresKeepOldGenerationAndDraftUntilRepair(t *testing.T) {
	for _, fault := range []string{"occupied previous generation", "missing next generation", "unwritable history parent", "unreadable installed generation", "unwritable rollback parent"} {
		t.Run(fault, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			deletion, _, _, draft := interruptedDeleteTree(t, s, "guide")
			var repair func()
			switch fault {
			case "occupied previous generation":
				require.NoError(t, writeSource(deletion.directory(), "history-previous/retain.txt", []byte("Never overwrite unknown history\n")))
				err := deletion.install(t.Context())
				require.Error(t, err)
				repair = func() {
					data, err := readSource(deletion.directory(), "history-previous/retain.txt", MaxTextBytes)
					require.NoError(t, err)
					assert.Equal(t, "Never overwrite unknown history\n", string(data))
					require.NoError(t, removeStorageTree(deletion.directory(), "history-previous"))
				}
			case "missing next generation":
				require.NoError(t, os.Rename(filepath.Join(deletion.directory(), "history-next"), filepath.Join(deletion.directory(), "history-next-offline")))
				require.Error(t, deletion.install(t.Context()))
				repair = func() { require.NoError(t, deletion.rollbackHistory(t.Context())) }
			case "unwritable history parent":
				repair = deletionDirectoryMode(t, cfg.StateDir, 0o500)
				require.Error(t, deletion.install(t.Context()))
			case "unreadable installed generation":
				require.NoError(t, deletion.install(t.Context()))
				restoreMode := deletionDirectoryMode(t, filepath.Join(cfg.StateDir, "history"), 0o100)
				require.Error(t, deletion.rollbackHistory(t.Context()))
				repair = func() {
					restoreMode()
					require.NoError(t, deletion.rollbackHistory(t.Context()))
				}
			case "unwritable rollback parent":
				require.NoError(t, deletion.install(t.Context()))
				restoreMode := deletionDirectoryMode(t, cfg.StateDir, 0o500)
				require.Error(t, deletion.rollbackHistory(t.Context()))
				repair = func() {
					restoreMode()
					require.NoError(t, deletion.rollbackHistory(t.Context()))
				}
			}
			repair()
			assertDeletionBeforeState(t, s, cfg, draft, deletion.journal.PreviousHistory)
			_, err := readSource(deletion.directory(), "backup/guide/chapter.md", MaxTextBytes)
			require.NoError(t, err)
			require.NoError(t, deletion.cleanup())
			publishDraft(t, s, draft)
		})
	}
}
