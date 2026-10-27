package content

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestHistorySurvivesRestartAndRestoresThroughDraft(t *testing.T) {
	service, config := managedFixture(t)
	ctx := context.Background()
	project, err := service.Project(ctx)
	require.NoError(t, err)
	file, err := service.Read(ctx, "docs.json")
	require.NoError(t, err)
	var manifest map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(file.Content), &manifest))
	manifest["title"] = json.RawMessage(`"Updated project settings"`)
	updated, err := json.Marshal(manifest)
	require.NoError(t, err)
	text := string(updated)
	draft, err := service.SaveDraft(ctx, DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "docs.json", Content: &text}}, editor)
	require.NoError(t, err)
	published, err := service.Publish(ctx, PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.NoError(t, err)
	require.True(t, published.Published)
	history, err := service.History(ctx, "docs.json")
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, editor.ID, history[0].Actor.ID)
	assert.Equal(t, "docs.json", history[0].Path)
	assert.False(t, history[0].Deleted)
	require.NoError(t, service.Close())
	reopened, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	persisted, err := reopened.History(ctx, "docs.json")
	require.NoError(t, err)
	require.Len(t, persisted, 2)
	assert.Equal(t, history, persisted)
	current, err := reopened.Project(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Updated project settings", current.Title)
	restored, err := reopened.Restore(ctx, "docs.json", persisted[len(persisted)-1].ID, current.Revision, editor)
	require.NoError(t, err)
	require.NotNil(t, restored.Change.Content)
	assert.JSONEq(t, file.Content, *restored.Change.Content)
	_, err = reopened.Publish(ctx, PublishInput{ID: restored.ID, Revision: restored.Revision}, editor)
	require.NoError(t, err)
	current, err = reopened.Project(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Documentation", current.Title)
	_, err = reopened.SaveDraft(ctx, DraftInput{BaseRevision: current.Revision, Change: Change{Operation: "delete", Path: "docs.json"}}, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = reopened.SaveDraft(ctx, DraftInput{BaseRevision: current.Revision, Change: Change{Operation: "move", Path: "docs.json", To: "other.json"}}, editor)
	assert.ErrorIs(t, err, ErrInvalid)
}
