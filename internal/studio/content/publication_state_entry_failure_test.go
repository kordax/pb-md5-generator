package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationPrivateInventoryOverflowPreservesRecoveryProof(t *testing.T) {
	for _, inventory := range []string{"stage", "output-holder"} {
		t.Run(inventory, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			directory := fixture.stage
			expectedSource := fixture.original
			if inventory == "output-holder" {
				fixture.applySources(t)
				installed, err := fixture.transaction.replaceOutput()
				require.NoError(t, err)
				require.True(t, installed)
				require.NoError(t, fixture.transaction.markPublished())
				directory = filepath.Join(fixture.transaction.outputParent(), fixture.transaction.outputHolder())
				expectedSource = fixture.replacement
			}
			sitePath := filepath.Join(fixture.config.OutputDir, "index.html")
			site, err := os.ReadFile(sitePath)
			require.NoError(t, err)
			journalPath := filepath.Join(fixture.config.StateDir, publicationJournalName)
			journal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			paths := make([]string, 0, 8)
			for i := range 8 {
				path := filepath.Join(directory, fmt.Sprintf("foreign-%d", i))
				require.NoError(t, os.WriteFile(path, []byte("foreign data"), 0o600))
				paths = append(paths, path)
			}
			_, err = fixture.reopen(t)
			require.ErrorIs(t, err, ErrOwnership)
			retainedJournal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			var expectedJournal, actualJournal publicationJournal
			require.NoError(t, json.Unmarshal(journal, &expectedJournal))
			require.NoError(t, json.Unmarshal(retainedJournal, &actualJournal))
			if inventory == "stage" {
				// The proven Before state may be recorded before cleanup refuses
				// foreign private entries. Every other recovery proof stays intact.
				expectedJournal.Phase = "rolled-back"
			}
			assert.Equal(t, expectedJournal, actualJournal)
			retainedSite, err := os.ReadFile(sitePath)
			require.NoError(t, err)
			assert.Equal(t, site, retainedSite)
			for _, path := range paths {
				retained, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, "foreign data", string(retained))
				require.NoError(t, os.Remove(path))
			}
			recovered, err := fixture.reopen(t)
			require.NoError(t, err)
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(expectedSource), file.Content)
		})
	}
}

func TestPublicationUnreadableJournalEntriesPreserveOriginals(t *testing.T) {
	for _, entry := range []string{"directory", "symlink", "unreadable-file"} {
		t.Run(entry, func(t *testing.T) {
			if entry == "unreadable-file" && os.Geteuid() == 0 {
				t.Skip("root bypasses file read permissions")
			}
			fixture := preparePublicationRecoveryFixture(t)
			journalPath := filepath.Join(fixture.config.StateDir, publicationJournalName)
			original, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			saved := filepath.Join(t.TempDir(), "original-journal.json")
			require.NoError(t, os.Rename(journalPath, saved))
			switch entry {
			case "directory":
				require.NoError(t, os.Mkdir(journalPath, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(journalPath, "foreign.txt"), []byte("foreign data"), 0o600))
			case "symlink":
				require.NoError(t, os.Symlink(saved, journalPath))
			case "unreadable-file":
				require.NoError(t, os.WriteFile(journalPath, original, 0o600))
				require.NoError(t, os.Chmod(journalPath, 0))
			}
			before, err := os.Lstat(journalPath)
			require.NoError(t, err)
			_, err = fixture.reopen(t)
			require.Error(t, err)
			if entry == "unreadable-file" {
				assert.ErrorIs(t, err, os.ErrPermission)
			}
			after, err := os.Lstat(journalPath)
			require.NoError(t, err)
			assert.True(t, os.SameFile(before, after))
			assert.Equal(t, before.Mode(), after.Mode())
			assert.Equal(t, before.ModTime(), after.ModTime())
			retainedOriginal, err := os.ReadFile(saved)
			require.NoError(t, err)
			assert.Equal(t, original, retainedOriginal)
			live, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
			require.NoError(t, err)
			assert.Equal(t, fixture.original, live)
			site, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, fixture.previousSite, site)
			if entry == "directory" {
				foreign := filepath.Join(journalPath, "foreign.txt")
				retained, err := os.ReadFile(foreign)
				require.NoError(t, err)
				assert.Equal(t, "foreign data", string(retained))
				require.NoError(t, os.Remove(foreign))
			}
			require.NoError(t, os.Remove(journalPath))
			require.NoError(t, os.Rename(saved, journalPath))
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
