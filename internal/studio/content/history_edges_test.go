package content

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryRestoresBinaryAndDeletedVersionsThroughDrafts(t *testing.T) {
	service, _ := managedFixture(t)
	name := "attachments/versioned.pdf"
	original := []byte{0x00, 0xff, 1, 2, 3}
	write := func(data []byte) {
		t.Helper()
		project, err := service.Project(t.Context())
		require.NoError(t, err)
		draft, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: name, Data: data}}, editor)
		require.NoError(t, err)
		publishDraft(t, service, draft)
	}
	write(original)
	history, err := service.History(t.Context(), name)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	originalID := history[0].ID
	newer := []byte{0xff, 9, 8, 7, 0x00}
	write(newer)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	restored, err := service.Restore(t.Context(), name, originalID, project.Revision, editor)
	require.NoError(t, err)
	assert.Equal(t, "write", restored.Change.Operation)
	assert.Nil(t, restored.Change.Content)
	assert.Equal(t, original, restored.Change.Data)
	current, err := service.Read(t.Context(), name)
	require.NoError(t, err)
	assert.Equal(t, newer, current.Data, "restoring a revision does not publish it")
	publishDraft(t, service, restored)
	current, err = service.Read(t.Context(), name)
	require.NoError(t, err)
	assert.True(t, current.Binary)
	assert.Equal(t, original, current.Data)
	project, err = service.Project(t.Context())
	require.NoError(t, err)
	removal, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "delete", Path: name}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, removal)
	history, err = service.History(t.Context(), name)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	require.True(t, history[0].Deleted)
	deletedID := history[0].ID
	write(newer)
	project, err = service.Project(t.Context())
	require.NoError(t, err)
	restoreDeletion, err := service.Restore(t.Context(), name, deletedID, project.Revision, editor)
	require.NoError(t, err)
	assert.Equal(t, "delete", restoreDeletion.Change.Operation)
	assert.Nil(t, restoreDeletion.Change.Content)
	assert.Empty(t, restoreDeletion.Change.Data)
	current, err = service.Read(t.Context(), name)
	require.NoError(t, err)
	assert.Equal(t, newer, current.Data)
	publishDraft(t, service, restoreDeletion)
	_, err = service.Read(t.Context(), name)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestHistoryRestoreRejectsForeignMissingAndIncompleteVersions(t *testing.T) {
	service, config := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	name := project.Documents[0].Path
	publishDraft(t, service, saveText(t, service, name, "# First saved version"))
	history, err := service.History(t.Context(), name)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	version := history[0]
	publishDraft(t, service, saveText(t, service, name, "# Current source remains"))
	project, err = service.Project(t.Context())
	require.NoError(t, err)
	_, err = service.Restore(t.Context(), "another.md", version.ID, project.Revision, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = service.Restore(t.Context(), name, "../outside", project.Revision, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = service.Restore(t.Context(), name, strings.Repeat("0", 32), project.Revision, editor)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = service.Restore(t.Context(), name, version.ID, "invalid-revision", editor)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = service.History(t.Context(), "../outside.md")
	assert.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, os.Remove(filepath.Join(config.StateDir, "history", version.ID+".bin")))
	_, err = service.Restore(t.Context(), name, version.ID, project.Revision, editor)
	assert.ErrorIs(t, err, ErrNotFound)
	current, err := service.Read(t.Context(), name)
	require.NoError(t, err)
	assert.Equal(t, "# Current source remains", current.Content)
	drafts, err := service.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
}

func TestHistoryRetentionEvictsOldestPayloadsAndKeepsCurrentSource(t *testing.T) {
	service, config := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	name := project.Documents[0].Path
	publishDraft(t, service, saveText(t, service, name, "# Initial retained version"))
	first, err := service.History(t.Context(), name)
	require.NoError(t, err)
	require.NotEmpty(t, first)
	expiredID := first[0].ID
	var newest string
	for i := range MaxHistory/2 + 3 {
		newest = fmt.Sprintf("# History version %d", i)
		publishDraft(t, service, saveText(t, service, name, newest))
	}
	history, err := service.History(t.Context(), name)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	assert.LessOrEqual(t, len(history), MaxHistory)
	assert.Greater(t, len(history), MaxHistory/2)
	for _, version := range history {
		assert.NotEqual(t, expiredID, version.ID)
	}
	for _, suffix := range []string{".json", ".bin"} {
		_, err := os.Stat(filepath.Join(config.StateDir, "history", expiredID+suffix))
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
	entries, err := os.ReadDir(filepath.Join(config.StateDir, "history"))
	require.NoError(t, err)
	assert.LessOrEqual(t, len(entries), MaxHistory*2)
	current, err := service.Read(t.Context(), name)
	require.NoError(t, err)
	assert.Equal(t, newest, current.Content)
	_, err = service.Restore(t.Context(), name, expiredID, current.Revision, editor)
	assert.ErrorIs(t, err, ErrNotFound)
	restored, err := service.Restore(t.Context(), name, history[0].ID, current.Revision, editor)
	require.NoError(t, err)
	require.NotNil(t, restored.Change.Content)
	assert.Equal(t, newest, *restored.Change.Content)
}

func TestHistoryVersionsRemainIsolatedAcrossGitBranches(t *testing.T) {
	service, _, _ := gitManagedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	name := project.Documents[0].Path
	history, err := service.History(t.Context(), name)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	mainVersion := history[0]
	_, err = service.CreateBranch(t.Context(), "history-isolation", project.Revision)
	require.NoError(t, err)
	_, err = service.Checkout(t.Context(), "history-isolation", project.Revision)
	require.NoError(t, err)
	branch, err := service.Project(t.Context())
	require.NoError(t, err)
	_, err = service.Restore(t.Context(), name, mainVersion.ID, branch.Revision, editor)
	assert.ErrorIs(t, err, ErrConflict)
	history, err = service.History(t.Context(), name)
	require.NoError(t, err)
	assert.Empty(t, history)
	drafts, err := service.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
	_, err = service.Checkout(t.Context(), "main", branch.Revision)
	require.NoError(t, err)
	main, err := service.Project(t.Context())
	require.NoError(t, err)
	restored, err := service.Restore(t.Context(), name, mainVersion.ID, main.Revision, editor)
	require.NoError(t, err)
	assert.Equal(t, "main", restored.Branch)
	assert.Equal(t, mainVersion.Path, restored.Change.Path)
}

func TestHistoryMissingDirectoryAndCorruptMetadataFailWithoutSourceLoss(t *testing.T) {
	service, config := managedFixture(t)
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	name := project.Documents[0].Path
	publishDraft(t, service, saveText(t, service, name, "# Source outside history"))
	versions, err := service.History(t.Context(), name)
	require.NoError(t, err)
	require.NotEmpty(t, versions)
	directory := filepath.Join(config.StateDir, "history")
	require.NoError(t, os.Rename(directory, directory+"-unavailable"))
	_, err = service.History(t.Context(), name)
	assert.Error(t, err)
	current, err := service.Read(t.Context(), name)
	require.NoError(t, err)
	assert.Equal(t, "# Source outside history", current.Content)
	require.NoError(t, os.Rename(directory+"-unavailable", directory))
	metadata := filepath.Join(directory, versions[0].ID+".json")
	original, err := os.ReadFile(metadata)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metadata, []byte(`{"id":"invalid"}`), 0o600))
	_, err = service.History(t.Context(), name)
	assert.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, os.WriteFile(metadata, original, 0o600))
	history, err := service.History(t.Context(), name)
	require.NoError(t, err)
	assert.Len(t, history, len(versions))
	data, err := os.ReadFile(filepath.Join(config.Root, filepath.FromSlash(name)))
	require.NoError(t, err)
	assert.Equal(t, []byte("# Source outside history"), data)
}
