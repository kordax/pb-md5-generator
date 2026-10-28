package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationOutputRollbackRetainsInstalledCandidate(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	installed, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, installed)
	output := filepath.Join(fixture.config.OutputDir, "index.html")
	candidate, err := os.ReadFile(output)
	require.NoError(t, err)
	require.NoError(t, fixture.transaction.rollbackOutput(t.Context()))
	previous, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, previous)
	retained := filepath.Join(fixture.transaction.outputParent(), fixture.transaction.outputHolder(), "after", "index.html")
	retainedCandidate, err := os.ReadFile(retained)
	require.NoError(t, err)
	assert.Equal(t, candidate, retainedCandidate, "rollback must retain the installed candidate until the transaction is resolved")
	live, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.replacement, live, "output rollback alone must not change source")
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.original), file.Content)
	draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.draft.Revision, draft.Revision)
	_, err = os.Stat(fixture.stage)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPublicationOutputPreparationFailuresRetainPreviousSite(t *testing.T) {
	for _, failure := range []string{"existing-holder", "readonly-parent"} {
		t.Run(failure, func(t *testing.T) {
			if failure == "readonly-parent" && os.Geteuid() == 0 {
				t.Skip("root bypasses directory write permissions")
			}
			fixture := preparePublicationRecoveryFixture(t)
			fixture.applySources(t)
			journalPath := filepath.Join(fixture.config.StateDir, publicationJournalName)
			journal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			parent := fixture.transaction.outputParent()
			holder := filepath.Join(parent, fixture.transaction.outputHolder())
			var repair func()
			if failure == "existing-holder" {
				require.NoError(t, os.Mkdir(holder, 0o700))
				repair = func() { require.NoError(t, os.Remove(holder)) }
			} else {
				info, err := os.Stat(parent)
				require.NoError(t, err)
				require.NoError(t, os.Chmod(parent, 0o500))
				t.Cleanup(func() { require.NoError(t, os.Chmod(parent, info.Mode().Perm())) })
				repair = func() { require.NoError(t, os.Chmod(parent, info.Mode().Perm())) }
			}
			installed, err := fixture.transaction.replaceOutput()
			require.Error(t, err)
			assert.False(t, installed)
			if failure == "existing-holder" {
				assert.ErrorIs(t, err, os.ErrExist)
			} else {
				assert.ErrorIs(t, err, os.ErrPermission)
			}
			previous, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, fixture.previousSite, previous)
			retainedJournal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			assert.Equal(t, journal, retainedJournal)
			backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
			require.NoError(t, err)
			assert.Equal(t, fixture.original, backup)
			repair()
			recovered, err := fixture.reopen(t)
			require.NoError(t, err)
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.original), file.Content)
			draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
		})
	}
}

func TestPublicationRenameFailuresPreserveOriginalImages(t *testing.T) {
	for _, failure := range []string{"existing-destination", "source-symlink", "readonly-parent", "missing-parent"} {
		t.Run(failure, func(t *testing.T) {
			if failure == "readonly-parent" && os.Geteuid() == 0 {
				t.Skip("root bypasses directory write permissions")
			}
			parent := t.TempDir()
			source := filepath.Join(parent, "source")
			destination := filepath.Join(parent, "destination")
			require.NoError(t, os.Mkdir(source, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(source, "index.html"), []byte("retained source"), 0o600))
			original := source
			from, to := source, destination
			switch failure {
			case "existing-destination":
				require.NoError(t, os.Mkdir(destination, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(destination, "index.html"), []byte("retained destination"), 0o600))
			case "source-symlink":
				original = filepath.Join(parent, "original")
				require.NoError(t, os.Rename(source, original))
				require.NoError(t, os.Symlink(original, source))
			case "readonly-parent":
				require.NoError(t, os.Chmod(parent, 0o500))
				t.Cleanup(func() { require.NoError(t, os.Chmod(parent, 0o700)) })
			case "missing-parent":
				from = filepath.Join(parent, "missing", "source")
				to = filepath.Join(parent, "missing", "destination")
			}
			before, err := os.Stat(original)
			require.NoError(t, err)
			installed, err := renamePublication(from, to)
			require.Error(t, err)
			assert.False(t, installed)
			if failure == "existing-destination" {
				assert.ErrorIs(t, err, ErrOwnership)
				retained, err := os.ReadFile(filepath.Join(destination, "index.html"))
				require.NoError(t, err)
				assert.Equal(t, "retained destination", string(retained))
			} else {
				_, err = os.Lstat(destination)
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
			after, err := os.Stat(original)
			require.NoError(t, err)
			assert.True(t, os.SameFile(before, after))
			retained, err := os.ReadFile(filepath.Join(original, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, "retained source", string(retained))
		})
	}
}
