package content

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applyInterruptedDeletion(t *testing.T, deletion *deleteTreeTransaction, before, after sourceTree) {
	t.Helper()
	require.NoError(t, applyFiles(t.Context(), deletion.service.cfg.Root, filepath.Join(deletion.directory(), "source"), deletion.journal.Paths, after))
	require.NoError(t, removeEmptyDirectories(deletion.service.cfg.Root, before.Dirs, after.Dirs))
}

func TestPublishedDeletionRecoveryRetainsAmbiguousLiveSourcesUntilRepaired(t *testing.T) {
	for _, change := range []string{"original tree", "partial restoration", "unrelated edit", "unmanaged addition"} {
		t.Run(change, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			deletion, before, after, draft := interruptedDeleteTree(t, s, "guide")
			require.NoError(t, deletion.install(t.Context()))
			applyInterruptedDeletion(t, deletion, before, after)
			require.NoError(t, deletion.markPublished())
			switch change {
			case "original tree":
				require.NoError(t, deletion.restoreSources(t.Context(), before, after))
			case "partial restoration":
				data, err := readSource(deletion.directory(), "backup/guide/chapter.md", MaxTextBytes)
				require.NoError(t, err)
				require.NoError(t, writeSource(cfg.Root, "guide/chapter.md", data))
			case "unrelated edit":
				require.NoError(t, writeSource(cfg.Root, "index.md", []byte("# External homepage\n")))
			case "unmanaged addition":
				require.NoError(t, writeSource(cfg.Root, "guide/unknown.exe", []byte("Never discard an unknown file\n")))
			}
			current, err := scanTree(t.Context(), cfg.Root, "\x00", "")
			require.NoError(t, err)
			require.NoError(t, s.Close())
			_, err = New(cfg)
			require.ErrorIs(t, err, ErrConflict)
			unchanged, err := scanTree(t.Context(), cfg.Root, "\x00", "")
			require.NoError(t, err)
			assert.Equal(t, current.Revision, unchanged.Revision)
			_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
			require.NoError(t, err)
			_, err = readSource(deletion.directory(), "backup/guide/chapter.md", MaxTextBytes)
			require.NoError(t, err)
			_, err = readSource(cfg.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json", MaxBinaryBytes)
			require.NoError(t, err)
			if change == "unmanaged addition" {
				data, err := readSource(cfg.Root, "guide/unknown.exe", MaxTextBytes)
				require.NoError(t, err)
				assert.Equal(t, "Never discard an unknown file\n", string(data))
				require.NoError(t, removeSource(cfg.Root, "guide/unknown.exe"))
			}
			require.NoError(t, applyFiles(t.Context(), cfg.Root, filepath.Join(deletion.directory(), "source"), deletion.journal.Paths, after))
			require.NoError(t, removeEmptyDirectories(cfg.Root, before.Dirs, after.Dirs))
			home, err := readSource(deletion.directory(), "source/index.md", MaxTextBytes)
			require.NoError(t, err)
			require.NoError(t, writeSource(cfg.Root, "index.md", home))
			reopened, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
		})
	}
}

func TestDeletionRecoveryDoesNotResetSwitchedBranchOrUnknownGitHead(t *testing.T) {
	for _, phase := range []string{"prepared", "published"} {
		for _, change := range []string{"branch", "head"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				s, cfg, _ := gitManagedFixture(t)
				s.cfg.AuthorizeDeleteTree, cfg.AuthorizeDeleteTree = allowDeleteTree, allowDeleteTree
				publishDraft(t, s, saveText(t, s, "guide/remove.txt", "Retain this Git source\n"))
				require.NoError(t, s.cfg.Storage.CreateBranch(t.Context(), "other"))
				deletion, before, after, _ := interruptedDeleteTree(t, s, "guide")
				require.NoError(t, deletion.install(t.Context()))
				if phase == "published" {
					applyInterruptedDeletion(t, deletion, before, after)
					require.NoError(t, deletion.markPublished())
				}
				if change == "branch" {
					fixtureGit(t, cfg.Root, "symbolic-ref", "HEAD", "refs/heads/other")
				} else {
					fixtureGit(t, cfg.Root, "-c", "user.name=External", "-c", "user.email=external@example.test", "commit", "--allow-empty", "-m", "External commit")
				}
				externalHead := fixtureGit(t, cfg.Root, "rev-parse", "HEAD")
				externalBranch := fixtureGit(t, cfg.Root, "symbolic-ref", "--short", "HEAD")
				source, err := scanTree(t.Context(), cfg.Root, deletion.journal.Branch+"\x00"+deletion.journal.Head, "")
				require.NoError(t, err)
				require.NoError(t, s.Close())
				_, err = New(cfg)
				require.ErrorIs(t, err, ErrConflict)
				assert.Equal(t, externalHead, fixtureGit(t, cfg.Root, "rev-parse", "HEAD"))
				assert.Equal(t, externalBranch, fixtureGit(t, cfg.Root, "symbolic-ref", "--short", "HEAD"))
				current, err := scanTree(t.Context(), cfg.Root, deletion.journal.Branch+"\x00"+deletion.journal.Head, "")
				require.NoError(t, err)
				assert.Equal(t, source.Revision, current.Revision)
				_, err = readSource(deletion.directory(), "backup/guide/remove.txt", MaxTextBytes)
				require.NoError(t, err)
				_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
				require.NoError(t, err)
				if change == "branch" {
					fixtureGit(t, cfg.Root, "symbolic-ref", "HEAD", "refs/heads/main")
				} else {
					fixtureGit(t, cfg.Root, "update-ref", "refs/heads/main", deletion.journal.Head)
				}
				recovered, err := New(cfg)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, recovered.Close()) })
			})
		}
	}
}

func TestPublishedDeletionRecoveryAcceptsOnlyRecordedOwnCommitAndExactAfterTree(t *testing.T) {
	s, cfg, remote := gitManagedFixture(t)
	s.cfg.AuthorizeDeleteTree, cfg.AuthorizeDeleteTree = allowDeleteTree, allowDeleteTree
	publishDraft(t, s, saveText(t, s, "guide/remove.txt", "Preserve original history\n"))
	remoteHead := fixtureGit(t, remote, "rev-parse", "refs/heads/main")
	deletion, before, after, draft := interruptedDeleteTree(t, s, "guide")
	require.NoError(t, deletion.install(t.Context()))
	applyInterruptedDeletion(t, deletion, before, after)
	require.NoError(t, deletion.markPublished())
	status, err := s.cfg.Storage.Status(t.Context())
	require.NoError(t, err)
	require.NoError(t, s.recoverDeleteTreePending(t.Context(), deletion.journal, after, status))
	pending, err := s.pending()
	require.NoError(t, err)
	result := s.synchronize(t.Context(), PublishResult{Revision: after.Revision, Published: true, Sync: status}, pending, false)
	require.NotEmpty(t, result.Commit)
	require.NoError(t, deletion.recordCommit(result.Commit))
	require.NoError(t, s.Close())
	reopened, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	assert.Equal(t, result.Commit, fixtureGit(t, cfg.Root, "rev-parse", "HEAD"))
	assert.Equal(t, remoteHead, fixtureGit(t, remote, "rev-parse", "refs/heads/main"))
	_, err = readSource(cfg.StateDir, deleteTreeJournalName, 256<<10)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = reopened.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	_, err = readSource(cfg.Root, "guide/remove.txt", MaxTextBytes)
	assert.ErrorIs(t, err, ErrNotFound)
}
