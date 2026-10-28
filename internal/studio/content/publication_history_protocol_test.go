package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationRecoveryRefusesHistoryMovementBeforePublication(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	plan, err := fixture.transaction.historyPlan()
	require.NoError(t, err)
	name := ""
	for candidate := range plan.Added {
		if strings.HasSuffix(candidate, ".bin") {
			name = candidate
			break
		}
	}
	require.NotEmpty(t, name)
	staged := filepath.Join(fixture.stage, "history-next", name)
	live := filepath.Join(fixture.config.StateDir, "history", name)
	payload, err := os.ReadFile(staged)
	require.NoError(t, err)
	require.NoError(t, os.Rename(staged, live))
	fixture.applySources(t)

	_, err = fixture.reopen(t)
	require.Error(t, err)
	current, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.replacement, current, "unexpected history placement must stop recovery before restoring sources")
	retained, err := os.ReadFile(live)
	require.NoError(t, err)
	assert.Equal(t, payload, retained, "recovery must not silently move externally relocated history")
	backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.original, backup)
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, output)

	// Only the explicit repair returns the immutable payload to its planned
	// location. Startup then recovers sources and preserves the saved draft.
	require.NoError(t, os.Rename(live, staged))
	recovered, err := New(fixture.config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, recovered.Close()) })
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.original), file.Content)
	draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.draft.Revision, draft.Revision)
}

func prepareCompletedPublicationCleanupFixture(t *testing.T) (publicationRecoveryFixture, []byte) {
	t.Helper()
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	installed, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, installed)
	require.NoError(t, fixture.transaction.markPublished())
	require.NoError(t, fixture.transaction.moveDeltaHistory(t.Context()))
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	// Simulate the cleanup interruption after both owned directories were
	// removed, but before the durable publication journal was unlinked.
	require.NoError(t, removeStorageTree(fixture.transaction.outputParent(), fixture.transaction.outputHolder()))
	require.NoError(t, removeStorageTree(fixture.config.StateDir, fixture.transaction.name()))
	return fixture, output
}

func TestPublicationRecoveryFinishesPublishedCleanupWithoutStage(t *testing.T) {
	fixture, installed := prepareCompletedPublicationCleanupFixture(t)
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), file.Content)
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, installed, output)
	revisions, err := recovered.revisions()
	require.NoError(t, err)
	found := false
	for _, revision := range revisions {
		if revision.Path == "index.md" && revision.Revision == fixture.after.Revision {
			found = true
		}
	}
	assert.True(t, found, "completed history must contain the installed source revision")
	_, err = os.Stat(filepath.Join(fixture.config.StateDir, publicationJournalName))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(fixture.stage)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPublicationRecoveryPreservesChangedFinalHistoryWithoutStage(t *testing.T) {
	for _, damage := range []string{"payload", "generation-marker"} {
		t.Run(damage, func(t *testing.T) {
			fixture, installed := prepareCompletedPublicationCleanupFixture(t)
			history := filepath.Join(fixture.config.StateDir, "history")
			name := filepath.Join(history, deleteTreeGeneration)
			var original []byte
			damaged := []byte(fixture.id)
			if damage == "payload" {
				entries, err := os.ReadDir(history)
				require.NoError(t, err)
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".bin") {
						name = filepath.Join(history, entry.Name())
						break
					}
				}
				require.NotEqual(t, filepath.Join(history, deleteTreeGeneration), name)
				original, err = os.ReadFile(name)
				require.NoError(t, err)
				damaged = append(append([]byte{}, original...), []byte("external change")...)
			}
			require.NoError(t, os.WriteFile(name, damaged, 0o600))
			_, err := fixture.reopen(t)
			require.Error(t, err)
			retained, err := os.ReadFile(name)
			require.NoError(t, err)
			assert.Equal(t, damaged, retained)
			source, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
			require.NoError(t, err)
			assert.Equal(t, fixture.replacement, source)
			output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, installed, output)
			_, err = os.Stat(filepath.Join(fixture.config.StateDir, publicationJournalName))
			assert.NoError(t, err, "the only remaining recovery proof must be retained")

			if damage == "payload" {
				require.NoError(t, os.WriteFile(name, original, 0o600))
			} else {
				require.NoError(t, os.Remove(name))
			}
			recovered, err := New(fixture.config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, recovered.Close()) })
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.replacement), file.Content)
		})
	}
}
