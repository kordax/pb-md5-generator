package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationInterruptedRetirementKeepsInstalledSiteAndRetainedEvidence(t *testing.T) {
	for _, interruption := range []string{"stage", "partial-stage", "output-holder"} {
		t.Run(interruption, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			fixture.applySources(t)
			installed, err := fixture.transaction.replaceOutput()
			require.NoError(t, err)
			require.True(t, installed)
			require.NoError(t, fixture.transaction.markPublished())
			require.NoError(t, fixture.transaction.moveDeltaHistory(t.Context()))
			site, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			parent, name := fixture.config.StateDir, fixture.transaction.name()
			if interruption == "output-holder" {
				parent, name = fixture.transaction.outputParent(), fixture.transaction.outputHolder()
			}
			retired := filepath.Join(parent, ".retired-"+name)
			require.NoError(t, os.Rename(filepath.Join(parent, name), retired))
			if interruption == "partial-stage" {
				require.NoError(t, os.RemoveAll(filepath.Join(retired, "source")))
				require.NoError(t, os.Remove(filepath.Join(retired, ".storage-owner")))
			}
			note := filepath.Join(retired, "operator-note.txt")
			require.NoError(t, os.WriteFile(note, []byte("retain for inspection"), 0o600))
			recovered, err := fixture.reopen(t)
			require.NoError(t, err)
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.replacement), file.Content)
			currentSite, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, site, currentSite)
			retainedNote, err := os.ReadFile(note)
			require.NoError(t, err)
			assert.Equal(t, "retain for inspection", string(retainedNote))
			_, err = os.Stat(fixture.stage)
			assert.ErrorIs(t, err, os.ErrNotExist)
			_, err = os.Stat(filepath.Join(fixture.config.StateDir, publicationJournalName))
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestPublicationJournalUnlinkFailureRetainsInstalledSiteAndRetryAuthority(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	installed, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, installed)
	require.NoError(t, fixture.transaction.markPublished())
	require.NoError(t, fixture.transaction.moveDeltaHistory(t.Context()))
	require.NoError(t, removePublicationTree(fixture.transaction.outputParent(), fixture.transaction.outputHolder()))
	require.NoError(t, removePublicationTree(fixture.config.StateDir, fixture.transaction.name()))
	journalPath := filepath.Join(fixture.config.StateDir, publicationJournalName)
	journal, err := os.ReadFile(journalPath)
	require.NoError(t, err)
	sitePath := filepath.Join(fixture.config.OutputDir, "index.html")
	site, err := os.ReadFile(sitePath)
	require.NoError(t, err)
	info, err := os.Stat(fixture.config.StateDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Chmod(fixture.config.StateDir, info.Mode().Perm())) })
	require.NoError(t, os.Chmod(fixture.config.StateDir, 0o500))
	_, err = fixture.reopen(t)
	require.ErrorIs(t, err, os.ErrPermission)
	retainedJournal, err := os.ReadFile(journalPath)
	require.NoError(t, err)
	assert.Equal(t, journal, retainedJournal)
	retainedSite, err := os.ReadFile(sitePath)
	require.NoError(t, err)
	assert.Equal(t, site, retainedSite)
	require.NoError(t, os.Chmod(fixture.config.StateDir, info.Mode().Perm()))
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), file.Content)
	_, err = os.Stat(journalPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPublicationRetirementCollisionPreservesForeignDataAndRecoveryProof(t *testing.T) {
	for _, collision := range []string{"stage", "output-holder"} {
		t.Run(collision, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			fixture.applySources(t)
			installed, err := fixture.transaction.replaceOutput()
			require.NoError(t, err)
			require.True(t, installed)
			require.NoError(t, fixture.transaction.markPublished())
			parent, name := fixture.config.StateDir, fixture.transaction.name()
			if collision == "output-holder" {
				parent, name = fixture.transaction.outputParent(), fixture.transaction.outputHolder()
			}
			retired := filepath.Join(parent, ".retired-"+name)
			require.NoError(t, os.Mkdir(retired, 0o700))
			note := filepath.Join(retired, "foreign.txt")
			require.NoError(t, os.WriteFile(note, []byte("foreign data"), 0o600))
			journalPath := filepath.Join(fixture.config.StateDir, publicationJournalName)
			journal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			require.ErrorIs(t, fixture.transaction.cleanup(), ErrOwnership)
			retainedNote, err := os.ReadFile(note)
			require.NoError(t, err)
			assert.Equal(t, "foreign data", string(retainedNote))
			retainedJournal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			assert.Equal(t, journal, retainedJournal)
			owner, err := os.ReadFile(filepath.Join(fixture.stage, ".storage-owner"))
			require.NoError(t, err)
			assert.Equal(t, fixture.transaction.journal.ID, string(owner))
			require.NoError(t, os.Remove(note))
			require.NoError(t, os.Remove(retired))
			recovered, err := fixture.reopen(t)
			require.NoError(t, err)
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.replacement), file.Content)
		})
	}
}
