package appearance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNullableSourceGroupsRemainReadableAfterPublication(t *testing.T) {
	value := newFixture(t)
	file, err := value.content.Read(t.Context(), "docs.json")
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal([]byte(file.Content), &manifest))
	manifest["themeId"] = "paper"
	for _, name := range []string{"theme", "navigation", "editor", "api", "publishing"} {
		manifest[name] = nil
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	text := string(data)
	project, err := value.content.Project(t.Context())
	require.NoError(t, err)
	draft, err := value.content.SaveDraft(t.Context(), content.DraftInput{BaseRevision: project.Revision, Change: content.Change{Operation: "write", Path: "docs.json", Content: &text}}, testActor())
	require.NoError(t, err)
	publication, err := value.content.Publish(t.Context(), content.PublishInput{ID: draft.ID, Revision: draft.Revision}, testActor())
	require.NoError(t, err)
	assert.True(t, publication.Published)
	settings, err := value.service.Read(t.Context())
	require.NoError(t, err, "source decoding must match the successful publication")
	assert.Equal(t, "paper", settings.ThemeID)
	assert.Equal(t, "serif", settings.Theme.Font)
	assert.True(t, settings.Navigation.ShowTOC)
	assert.True(t, settings.Navigation.StickyHeader)
	assert.Equal(t, "markdown", settings.Editor.DefaultFormat)
	assert.True(t, settings.API.Enabled)
	assert.Equal(t, 30000, settings.API.RequestTimeoutMs)
	assert.False(t, settings.Publishing.AutoPublish)
	assert.False(t, settings.Publishing.GitPushOnPublish)
	compiled, err := os.ReadFile(filepath.Join(value.config.OutputDir, "_docs", "project.json"))
	require.NoError(t, err)
	var artifact map[string]any
	require.NoError(t, json.Unmarshal(compiled, &artifact))
	for _, name := range []string{"theme", "navigation", "editor", "api", "publishing"} {
		assert.NotContains(t, artifact, name, "unset pointer groups are omitted in portable output")
	}
	var model document.Project
	require.NoError(t, json.Unmarshal(compiled, &model))
	assert.Equal(t, "paper", model.ThemeID)
}
