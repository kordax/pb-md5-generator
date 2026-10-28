package content

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationHistoryFailureKeepsSourcesAndDraft(t *testing.T) {
	service, config := managedFixture(t)
	publishDraft(t, service, saveText(t, service, "index.md", "# Original retained publication\n"))
	originalSource, err := os.ReadFile(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	originalSite, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	const replacement = "# Must remain a draft when history is unavailable\n"
	draft := saveText(t, service, "index.md", replacement)
	history := filepath.Join(config.StateDir, "history")
	require.NoError(t, os.RemoveAll(history))
	const obstruction = "unavailable directory"
	require.NoError(t, os.WriteFile(history, []byte(obstruction), 0o600))
	result, err := service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.Error(t, err)
	assert.False(t, result.Published, "history must be reserved before any live publication")
	currentSource, err := os.ReadFile(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, originalSource, currentSource)
	currentSite, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, originalSite, currentSite)
	saved, err := service.readDraft(draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft.Revision, saved.Revision)
	changes := draftChanges(saved)
	require.Len(t, changes, 1)
	assert.Equal(t, replacement, string(changeData(changes[0])))
	retained, err := os.ReadFile(history)
	require.NoError(t, err)
	assert.Equal(t, obstruction, string(retained), "the unknown history obstruction must never be overwritten")
}
