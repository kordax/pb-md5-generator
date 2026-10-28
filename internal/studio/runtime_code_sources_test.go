package studio

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeLiteralCodeChangesRecheckConsumerDependencies(t *testing.T) {
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Home\n")
	root := filepath.Join(config.DataDir, "content")
	manifestPath := filepath.Join(root, "docs.json")
	manifest, err := readProjectManifest(root, manifestPath)
	require.NoError(t, err)
	manifest.Pages = append(manifest.Pages, site.Page{ID: "code", Title: "Literal examples", Source: "public/code.md", Path: "public/code.html", Kind: "markdown"})
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, data, 0o600))
	const original = "const example = '<script>literal example</script>';\n"
	extensions := []string{"go", "js", "ts", "sh", "sql", "css", "xml"}
	var source strings.Builder
	for _, extension := range extensions {
		name := "sample." + extension
		require.NoError(t, os.WriteFile(filepath.Join(root, "public", name), []byte(original), 0o600))
		source.WriteString(":::include source=\"" + name + "\" language=\"text\"\n:::\n\n")
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "examples.v1"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "examples.v1", "literal.js"), []byte(original), 0o600))
	// An edited literal source affects this readable page, whose other
	// dependency remains private to the owner. The same consumer check used
	// for Markdown includes must also apply to each newly supported extension.
	source.WriteString(":::include source=\"../private.md\"\n:::\n\n")
	source.WriteString(":::include source=\"../examples.v1/literal.js\" language=\"text\"\n:::\n\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "public", "code.md"), []byte(source.String()), 0o600))
	startRuntimeForTest(t, config)
	owner := newRuntimeClient(t, config)
	owner.login(t, "owner", ownerTestPassword)
	editor := navigationScopedClient(t, owner, config, "code-source-editor", "editor", []string{"public", "examples.v1"}, []string{"public", "examples.v1"})
	status, _, body := editor.request(t, http.MethodGet, "/_docs/api/v1/documents/guide?published=true", nil)
	require.Equal(t, http.StatusOK, status)
	var page apiweb.Document
	require.NoError(t, json.Unmarshal(body, &page))
	for _, extension := range extensions {
		t.Run(extension, func(t *testing.T) {
			name := "public/sample." + extension
			draftID, draftRevision := uploadRuntimeText(t, editor, name, page.Revision, "Changed literal example.\n")
			status, _, body := editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+draftID+"/publish", map[string]string{"revision": draftRevision})
			assert.Equal(t, http.StatusForbidden, status)
			assert.NotContains(t, string(body), "Secret body")
			assert.NotContains(t, string(body), "private.md")
			published, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			require.NoError(t, err)
			assert.Equal(t, original, string(published))
		})
	}
	t.Run("folder with dotted name", func(t *testing.T) {
		plan := runtimeDeletionPlan(t, editor, "examples.v1")
		status, _, body := editor.request(t, http.MethodPost, "/_docs/api/v1/files/delete-tree", map[string]string{
			"path": plan.Path, "revision": plan.Revision, "planRevision": plan.PlanRevision,
		})
		require.Equal(t, http.StatusOK, status)
		var saved struct {
			Draft struct {
				ID       string `json:"id"`
				Revision string `json:"revision"`
			} `json:"draft"`
		}
		require.NoError(t, json.Unmarshal(body, &saved))
		require.NotEmpty(t, saved.Draft.ID)
		status, _, body = editor.request(t, http.MethodPost, "/_docs/api/v1/drafts/"+saved.Draft.ID+"/publish", map[string]string{"revision": saved.Draft.Revision})
		assert.Equal(t, http.StatusForbidden, status, "a directory suffix must not bypass consumer permissions")
		assert.NotContains(t, string(body), "Secret body")
		assert.NotContains(t, string(body), "private.md")
		published, err := os.ReadFile(filepath.Join(root, "examples.v1", "literal.js"))
		require.NoError(t, err)
		assert.Equal(t, original, string(published))
	})
	status, _, body = owner.request(t, http.MethodGet, "/public/code.html", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), "&lt;script&gt;literal example&lt;/script&gt;")
	assert.NotContains(t, string(body), "<script>literal example</script>")
}
