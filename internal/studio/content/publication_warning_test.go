package content

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuccessfulSynchronizationPreservesPublicationHistoryWarning(t *testing.T) {
	service, config := managedFixture(t)
	draft := saveText(t, service, "index.md", "# Published despite history failure\n")
	history := filepath.Join(config.StateDir, "history")
	require.NoError(t, os.Remove(history))
	require.NoError(t, os.WriteFile(history, []byte("unavailable directory"), 0o600))
	result, err := service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.NoError(t, err)
	assert.True(t, result.Published)
	assert.Equal(t, "local", result.Sync.State)
	assert.Contains(t, result.Sync.Error, "revision retention needs attention")
	data, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "Published despite history failure")
}
