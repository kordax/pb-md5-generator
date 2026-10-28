package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runtimeDeletionPlan(t *testing.T, client *runtimeClient, path string) content.DeleteTreePlan {
	t.Helper()
	status, _, body := client.request(t, http.MethodGet, "/_docs/api/v1/documents", nil)
	require.Equal(t, http.StatusOK, status)
	var project struct {
		Revision string `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(body, &project))
	status, _, body = client.request(t, http.MethodPost, "/_docs/api/v1/files/delete-preview", map[string]string{"path": path, "revision": project.Revision})
	require.Equal(t, http.StatusOK, status, "deletion preview: %s", body)
	var plan content.DeleteTreePlan
	require.NoError(t, json.Unmarshal(body, &plan))
	require.NotEmpty(t, plan.PlanRevision)
	return plan
}

func TestDeleteTreeAuthorizationRequiresAuthenticatedOwnerAndInventory(t *testing.T) {
	for _, actor := range []content.Actor{{}, {ID: "system"}, {ID: "unknown"}} {
		assert.ErrorIs(t, authorizeDeleteTree(context.Background(), actor, []content.Entry{{Path: "public", Directory: true}}), auth.ErrForbidden)
	}
}

func TestRuntimeDeleteTreeStagesThenPublishesWithScopedGrantsAndNavigation(t *testing.T) {
	config, owner, _ := navigationRuntimeFixture(t)
	editor := navigationScopedClient(t, owner, config, "folder-editor", "editor", []string{"public"}, []string{"public"})
	plan := runtimeDeletionPlan(t, editor, "public")
	assert.Equal(t, "public", plan.Path)
	assert.Positive(t, plan.FileCount)
	assert.Positive(t, plan.DirectoryCount)
	assert.Equal(t, 1, plan.DocumentCount)
	for _, entry := range plan.Entries {
		assert.True(t, entry.Path == "public" || strings.HasPrefix(entry.Path, "public/"))
	}
	source := filepath.Join(config.DataDir, "content", "public", "guide.md")
	original, err := os.ReadFile(source)
	require.NoError(t, err)
	status, _, body := editor.request(t, http.MethodPost, "/_docs/api/v1/files/delete-tree", map[string]string{
		"path": plan.Path, "revision": plan.Revision, "planRevision": plan.PlanRevision,
	})
	require.Equal(t, http.StatusOK, status, "stage tree deletion: %s", body)
	var staged struct {
		Published bool `json:"published"`
		Draft     struct {
			ID        string `json:"id"`
			Revision  string `json:"revision"`
			Path      string `json:"path"`
			Operation string `json:"operation"`
		} `json:"draft"`
	}
	require.NoError(t, json.Unmarshal(body, &staged))
	assert.False(t, staged.Published)
	require.NotEmpty(t, staged.Draft.ID)
	assert.Equal(t, "delete-tree", staged.Draft.Operation)
	assert.Equal(t, plan.Path, staged.Draft.Path)
	current, err := os.ReadFile(source)
	require.NoError(t, err)
	assert.Equal(t, original, current, "staging must leave the source intact")
	status, _, _ = editor.request(t, http.MethodGet, "/_docs/api/v1/documents/guide?published=true", nil)
	require.Equal(t, http.StatusOK, status)

	status, _, body = editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+staged.Draft.ID+"/publish", map[string]string{"revision": staged.Draft.Revision})
	require.Equal(t, http.StatusOK, status, "publish tree deletion: %s", body)
	_, err = os.Stat(filepath.Join(config.DataDir, "content", "public"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	status, _, _ = editor.request(t, http.MethodGet, "/_docs/api/v1/documents/guide?published=true", nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, _, body = owner.request(t, http.MethodGet, "/_docs/project.json", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "Hidden group title")
	assert.NotContains(t, string(body), "Friendly guide")
	assert.Contains(t, string(body), "index.html", "the homepage outside the folder must remain published")
}

func TestRuntimeDeleteTreeRejectsPartialAccessWithoutInventory(t *testing.T) {
	config, owner, _ := navigationRuntimeFixture(t)
	editor := navigationScopedClient(t, owner, config, "single-source-editor", "editor", []string{"public/guide.md"}, []string{"public/guide.md"})
	status, _, body := editor.request(t, http.MethodGet, "/_docs/api/v1/documents", nil)
	require.Equal(t, http.StatusOK, status)
	var project struct {
		Revision string `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(body, &project))
	status, _, body = editor.request(t, http.MethodPost, "/_docs/api/v1/files/delete-preview", map[string]string{"path": "public", "revision": project.Revision})
	assert.Equal(t, http.StatusNotFound, status, "an unreadable target folder stays undisclosed")
	for _, hidden := range []string{"guide.md", "private.md", "entries", "fileCount", "documentCount", "planRevision"} {
		assert.NotContains(t, string(body), hidden)
	}
	childWriter := navigationScopedClient(t, owner, config, "child-only-writer", "editor", []string{"public"}, []string{"public/guide.md"})
	status, _, body = childWriter.request(t, http.MethodPost, "/_docs/api/v1/files/delete-preview", map[string]string{"path": "public", "revision": project.Revision})
	assert.Equal(t, http.StatusForbidden, status, "write access to a child does not authorize deleting its folder")
	assert.NotContains(t, string(body), "entries")
	assert.NotContains(t, string(body), "fileCount")
}

func TestRuntimeDeleteTreePlanRejectsNewOverlappingDraftWithoutDiscardingIt(t *testing.T) {
	config, owner, _ := navigationRuntimeFixture(t)
	editor := navigationScopedClient(t, owner, config, "planned-folder-editor", "editor", []string{"public"}, []string{"public"})
	plan := runtimeDeletionPlan(t, editor, "public")
	const draftText = "# Owner's recoverable guide draft\n"
	status, body := navigationRawRequest(t, owner, http.MethodPut, "/_docs/api/v1/files/content?path=public/guide.md", "application/octet-stream", []byte(draftText), plan.Revision)
	require.Equal(t, http.StatusOK, status)
	var saved struct {
		Draft content.Draft `json:"draft"`
	}
	require.NoError(t, json.Unmarshal(body, &saved))
	require.NotEmpty(t, saved.Draft.ID)
	status, _, body = editor.request(t, http.MethodPost, "/_docs/api/v1/files/delete-tree", map[string]string{
		"path": plan.Path, "revision": plan.Revision, "planRevision": plan.PlanRevision,
	})
	assert.Equal(t, http.StatusConflict, status)
	assert.NotContains(t, string(body), saved.Draft.ID)
	assert.NotContains(t, string(body), draftText)
	assert.NotContains(t, string(body), "Owner")
	status, _, body = owner.request(t, http.MethodGet, "/_docs/api/v1/drafts", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), saved.Draft.ID, "a rejected deletion must preserve the existing draft")
	status, _, body = editor.request(t, http.MethodGet, "/_docs/api/v1/files/content?path=public/guide.md", nil)
	require.Equal(t, http.StatusOK, status)
	assert.NotEqual(t, draftText, string(body), "an unpublished draft must not replace the source")
}
