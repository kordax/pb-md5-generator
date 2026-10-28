package apiweb

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func workflowTarget(action, name string) string {
	return testAPI + "/files/" + action + "?" + url.Values{"path": []string{name}}.Encode()
}

func workflowRebase(f *fixture, user *credentials, name, revision string, data []byte, draft *draftDTO) *httptest.ResponseRecorder {
	headers := map[string]string{"Content-Type": "application/octet-stream", "If-Match": revision}
	if draft != nil {
		headers["X-Draft-ID"], headers["X-Draft-Revision"] = draft.ID, draft.Revision
	}
	return f.request(http.MethodPost, workflowTarget("rebase", name), data, user, headers)
}

func workflowHistory(t *testing.T, f *fixture, user *credentials, name string) sourceHistoryResult {
	t.Helper()
	response := f.request(http.MethodGet, workflowTarget("history", name), nil, user, nil)
	result := resultJSON[sourceHistoryResult](t, response, http.StatusOK)
	assert.Equal(t, name, result.Path)
	assert.Equal(t, `"`+result.Revision+`"`, response.Header().Get("ETag"))
	return result
}

func workflowRestore(f *fixture, user *credentials, name, historyID, revision string) *httptest.ResponseRecorder {
	return f.request(http.MethodPost, testAPI+"/files/restore", map[string]string{"path": name, "historyId": historyID, "revision": revision}, user, nil)
}

func TestSourceWorkflowReviewedRebaseKeepsOwnerCASAndNeverAutoPublishes(t *testing.T) {
	automatic := false
	f := newFixture(t, func(options *Options) {
		options.Publishing = func(context.Context) (PublishingPolicy, error) { return PublishingPolicy{AutoPublish: automatic}, nil }
	})
	owner := f.createUser(t, "source-rebase-owner", auth.RoleEditor, nil)
	other := f.createUser(t, "source-rebase-other", auth.RoleEditor, nil)
	name, publishedText := "examples/rebase.ts", []byte("export const published = true;\n")
	base := f.current(t, &owner)
	seed := resultJSON[saveResult](t, sourceUpload(f, &owner, name, base.Revision, publishedText, nil), http.StatusOK)
	f.publish(t, seed.Draft, &owner)
	base = f.current(t, &owner)
	original := resultJSON[saveResult](t, sourceUpload(f, &owner, name, base.Revision, []byte("owner unsent edits"), nil), http.StatusOK)
	require.NotNil(t, original.Draft)
	advance := resultJSON[saveResult](t, sourceUpload(f, &f.admin, "examples/advance.sql", base.Revision, []byte("select 1;\n"), nil), http.StatusOK)
	f.publish(t, advance.Draft, &f.admin)
	latest := f.current(t, &owner)
	require.NotEqual(t, base.Revision, latest.Revision)
	automatic = true
	merged := []byte("\ufeffexport const merged = '日本語';\r\n")
	rebased := resultJSON[saveResult](t, workflowRebase(f, &owner, name, latest.Revision, merged, original.Draft), http.StatusOK)
	require.NotNil(t, rebased.Draft)
	assert.False(t, rebased.Published)
	assert.Equal(t, original.Draft.ID, rebased.Draft.ID)
	assert.NotEqual(t, original.Draft.Revision, rebased.Draft.Revision)
	assert.Equal(t, latest.Revision, rebased.Revision)
	assert.Equal(t, latest.Revision, rebased.Draft.BaseRevision)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(name, ""), nil, &owner, nil), publishedText, latest.Revision)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(name, rebased.Draft.ID), nil, &owner, nil), merged, rebased.Draft.Revision)
	assert.Equal(t, http.StatusConflict, workflowRebase(f, &owner, name, latest.Revision, []byte("stale window"), original.Draft).Code)
	for _, response := range []*httptest.ResponseRecorder{
		workflowRebase(f, &other, name, latest.Revision, []byte("foreign replacement"), rebased.Draft),
		workflowRebase(f, &owner, "examples/wrong.ts", latest.Revision, []byte("wrong path"), rebased.Draft),
	} {
		assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), rebased.Draft.ID)
		assert.NotContains(t, response.Body.String(), rebased.Draft.Revision)
		assert.NotContains(t, response.Body.String(), string(merged))
	}
	unselected := workflowRebase(f, &owner, name, latest.Revision, []byte("must not select saved owner draft"), nil)
	assert.Equal(t, http.StatusConflict, unselected.Code, unselected.Body.String())
	assert.NotContains(t, unselected.Body.String(), rebased.Draft.ID)
	empty := resultJSON[saveResult](t, workflowRebase(f, &owner, "examples/new-empty.ts", latest.Revision, []byte{}, nil), http.StatusOK)
	require.NotNil(t, empty.Draft)
	assert.False(t, empty.Published)
	assert.NotEqual(t, rebased.Draft.ID, empty.Draft.ID)
	emptyResponse := f.request(http.MethodGet, sourceTarget("examples/new-empty.ts", empty.Draft.ID), nil, &owner, nil)
	assert.Equal(t, http.StatusOK, emptyResponse.Code)
	assert.Zero(t, emptyResponse.Body.Len())
	assert.Equal(t, "0", emptyResponse.Header().Get("Content-Length"))
	assert.Equal(t, `"`+empty.Draft.Revision+`"`, emptyResponse.Header().Get("ETag"))
	assert.Equal(t, latest.Revision, f.current(t, &f.admin).Revision)
}

func TestSourceWorkflowHistoryRestoresOnlyBytesAtTheExactPath(t *testing.T) {
	automatic := false
	f := newFixture(t, func(options *Options) {
		options.Publishing = func(context.Context) (PublishingPolicy, error) { return PublishingPolicy{AutoPublish: automatic}, nil }
	})
	oldPath, currentPath, text := "private/legacy.md", "public/current.md", "# Original source\r\n"
	document := f.addDocument(t, oldPath, "Private historical title", text)
	base := f.current(t, &f.admin)
	moved := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents/"+document.ID+"/move", map[string]string{"to": currentPath, "revision": base.Revision}, &f.admin, nil), http.StatusOK)
	f.publish(t, moved.Draft, &f.admin)
	history := workflowHistory(t, f, &f.admin, oldPath)
	assert.Equal(t, f.current(t, &f.admin).Revision, history.Revision)
	var live, tombstone string
	for _, entry := range history.Revisions {
		assert.Equal(t, oldPath, entry.Path)
		if entry.Deleted {
			tombstone = entry.ID
			assert.False(t, entry.Restorable)
		} else {
			live = entry.ID
			assert.True(t, entry.Restorable)
		}
	}
	require.NotEmpty(t, live)
	require.NotEmpty(t, tombstone)
	assert.Equal(t, http.StatusBadRequest, workflowRestore(f, &f.admin, oldPath, tombstone, history.Revision).Code)
	scoped := f.createUser(t, "source-history-scoped", auth.RoleEditor, &auth.Grants{Read: []string{"public"}, Write: []string{"public"}})
	visible := f.request(http.MethodGet, workflowTarget("history", currentPath), nil, &scoped, nil)
	visibleHistory := resultJSON[sourceHistoryResult](t, visible, http.StatusOK)
	require.NotEmpty(t, visibleHistory.Revisions)
	for _, entry := range visibleHistory.Revisions {
		assert.Equal(t, currentPath, entry.Path)
	}
	for _, hidden := range []string{oldPath, document.ID, "Private historical title", `"actor"`, `"branch"`, `"email"`, `"documentId"`, `"outputPath"`} {
		assert.NotContains(t, visible.Body.String(), hidden)
	}
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, workflowTarget("history", oldPath), nil, &scoped, nil).Code)
	assert.Equal(t, http.StatusNotFound, workflowRestore(f, &scoped, currentPath, live, visibleHistory.Revision).Code)
	automatic = true
	restored := resultJSON[saveResult](t, workflowRestore(f, &f.admin, oldPath, live, history.Revision), http.StatusOK)
	require.NotNil(t, restored.Draft)
	assert.False(t, restored.Published)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, sourceTarget(oldPath, ""), nil, &f.admin, nil).Code)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(oldPath, restored.Draft.ID), nil, &f.admin, nil), []byte(text), restored.Draft.Revision)
	stored, err := f.content.ReadDraft(t.Context(), restored.Draft.ID)
	require.NoError(t, err)
	for _, change := range changesOf(stored) {
		assert.Empty(t, change.DocumentID)
		assert.Empty(t, change.Title)
		assert.Empty(t, change.Kind)
		assert.Empty(t, change.OutputPath)
	}
	assert.Equal(t, http.StatusConflict, workflowRestore(f, &f.admin, oldPath, live, history.Revision).Code)
	f.publish(t, restored.Draft, &f.admin)
	project, err := f.content.Project(t.Context())
	require.NoError(t, err)
	for _, entry := range project.Documents {
		assert.NotEqual(t, oldPath, entry.Path)
		if entry.ID == document.ID {
			assert.Equal(t, currentPath, entry.Path)
		}
	}
	automatic = false
	base = f.current(t, &f.admin)
	pdf := resultJSON[saveResult](t, sourceUpload(f, &f.admin, "files/empty.pdf", base.Revision, []byte{}, nil), http.StatusOK)
	f.publish(t, pdf.Draft, &f.admin)
	binaryHistory := workflowHistory(t, f, &f.admin, "files/empty.pdf")
	require.NotEmpty(t, binaryHistory.Revisions)
	zero := binaryHistory.Revisions[0]
	assert.Zero(t, zero.Size)
	assert.True(t, zero.Restorable)
	automatic = true
	binary := resultJSON[saveResult](t, workflowRestore(f, &f.admin, "files/empty.pdf", zero.ID, binaryHistory.Revision), http.StatusOK)
	require.NotNil(t, binary.Draft)
	assert.False(t, binary.Published)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, sourceTarget("files/empty.pdf", binary.Draft.ID), nil, &f.admin, nil).Code)
}

func TestSourceWorkflowRejectsMalformedRequestsWithoutCreatingDrafts(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	name := "examples/request.ts"
	for _, test := range []struct {
		name, target, media string
		data                []byte
		headers             map[string]string
		status              int
	}{
		{"unknown query", workflowTarget("rebase", name) + "&unexpected=true", "application/octet-stream", []byte("x"), nil, 400},
		{"draft query", workflowTarget("rebase", name) + "&draftId=" + strings.Repeat("a", 32), "application/octet-stream", []byte("x"), nil, 400},
		{"unpaired headers", workflowTarget("rebase", name), "application/octet-stream", []byte("x"), map[string]string{"X-Draft-ID": strings.Repeat("a", 32)}, 400},
		{"wrong media", workflowTarget("rebase", name), "application/json", []byte("x"), nil, 415},
		{"encoding", workflowTarget("rebase", name), "application/octet-stream", []byte("x"), map[string]string{"Content-Encoding": "gzip"}, 415},
		{"invalid utf8", workflowTarget("rebase", name), "application/octet-stream", []byte{0xff}, nil, 400},
		{"too much text", workflowTarget("rebase", name), "application/octet-stream", bytes.Repeat([]byte("x"), content.MaxTextBytes+1), nil, 413},
		{"binary rebase", workflowTarget("rebase", "files/file.pdf"), "application/octet-stream", []byte{}, nil, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := map[string]string{"Content-Type": test.media, "If-Match": base.Revision}
			for key, value := range test.headers {
				headers[key] = value
			}
			response := f.request(http.MethodPost, test.target, test.data, &f.admin, headers)
			assert.Equal(t, test.status, response.Code, response.Body.String())
		})
	}
	for _, body := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"path":null}`),
		[]byte(`{"path":"a.ts","path":"b.ts"}`),
		[]byte(`{"unexpected":true}`),
	} {
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/files/restore", body, &f.admin, nil).Code)
	}
	oversize := []byte(`{"path":"` + strings.Repeat("x", 32<<10) + `"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, f.request(http.MethodPost, testAPI+"/files/restore", oversize, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/files/restore?unexpected=true", map[string]string{}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusNotFound, workflowRestore(f, &f.admin, name, strings.Repeat("a", 32), base.Revision).Code)
	assert.Equal(t, http.StatusConflict, workflowRestore(f, &f.admin, name, strings.Repeat("a", 32), strings.Repeat("0", 64)).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, workflowTarget("history", name)+"&unexpected=true", nil, &f.admin, nil).Code)
	for _, action := range []string{"rebase", "restore"} {
		response := f.request(http.MethodGet, testAPI+"/files/"+action, nil, &f.admin, nil)
		assert.Equal(t, http.StatusMethodNotAllowed, response.Code)
		assert.Equal(t, "POST", response.Header().Get("Allow"))
	}
	readOnly := f.createUser(t, "source-readonly-editor", auth.RoleEditor, &auth.Grants{Read: []string{"examples"}, Write: []string{}})
	assert.Equal(t, http.StatusForbidden, workflowRebase(f, &readOnly, name, base.Revision, []byte("no write"), nil).Code)
	assert.Equal(t, http.StatusForbidden, workflowRestore(f, &readOnly, name, strings.Repeat("a", 32), base.Revision).Code)
	missing := f.request(http.MethodGet, workflowTarget("history", name), nil, &readOnly, nil)
	assert.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
	assert.NotContains(t, missing.Body.String(), `"revisions"`)
	assert.Empty(t, missing.Header().Get("ETag"))
	drafts, err := f.content.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
	assert.Equal(t, base.Revision, f.current(t, &f.admin).Revision)
}

func TestSourceWorkflowRechecksRevokedSessionInsideContentGate(t *testing.T) {
	for _, action := range []string{"history", "rebase", "restore"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t, nil)
			editor := f.createUser(t, "queued-source-workflow", auth.RoleEditor, nil)
			var armed atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			replaceContentForHTTPTest(t, f, content.Config{AuthorizeSource: func(ctx context.Context, actor content.Actor, name string, access content.SourceAccess) error {
				if armed.CompareAndSwap(true, false) {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return authorizeSourceForHTTPTest(ctx, actor, name, access)
			}})
			base := f.current(t, &editor)
			name, text := "examples/queued-workflow.ts", []byte("retained published source\n")
			seed := resultJSON[saveResult](t, sourceUpload(f, &editor, name, base.Revision, text, nil), http.StatusOK)
			f.publish(t, seed.Draft, &editor)
			history := workflowHistory(t, f, &editor, name)
			require.NotEmpty(t, history.Revisions)
			armed.Store(true)
			completed := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				switch action {
				case "history":
					completed <- f.request(http.MethodGet, workflowTarget(action, name), nil, &editor, nil)
				case "rebase":
					completed <- workflowRebase(f, &editor, name, history.Revision, []byte("revoked replacement"), nil)
				case "restore":
					completed <- workflowRestore(f, &editor, name, history.Revisions[0].ID, history.Revision)
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("source operation did not reach authorization inside the content gate")
			}
			require.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/session", nil, &editor, nil).Code)
			close(release)
			select {
			case response := <-completed:
				assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
				for _, hidden := range []string{name, history.Revision, history.Revisions[0].ID, string(text), `"revisions"`, `"draft"`} {
					assert.NotContains(t, response.Body.String(), hidden)
				}
				assert.Empty(t, response.Header().Get("ETag"))
			case <-time.After(5 * time.Second):
				t.Fatal("source operation did not finish after authorization was released")
			}
			drafts, err := f.content.Drafts(t.Context())
			require.NoError(t, err)
			assert.Empty(t, drafts)
			assert.Equal(t, history.Revision, f.current(t, &f.admin).Revision)
			assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(name, ""), nil, &f.admin, nil), text, history.Revision)
		})
	}
}

func TestSourceWorkflowHistoryAndRestoreRemainIsolatedAcrossBranches(t *testing.T) {
	f := newFixture(t, nil)
	useGitStorage(t, f)
	name := "examples/history.go"
	base := f.current(t, &f.admin)
	seed := resultJSON[saveResult](t, sourceUpload(f, &f.admin, name, base.Revision, []byte("package original\n"), nil), http.StatusOK)
	f.publish(t, seed.Draft, &f.admin)
	history := workflowHistory(t, f, &f.admin, name)
	require.NotEmpty(t, history.Revisions)
	retained := resultJSON[saveResult](t, sourceUpload(f, &f.admin, name, history.Revision, []byte("package retained\n"), nil), http.StatusOK)
	require.NotNil(t, retained.Draft)
	resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches", map[string]string{"name": "history-other", "revision": history.Revision}, &f.admin, nil), http.StatusOK)
	resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches/checkout", map[string]string{"name": "history-other", "revision": history.Revision}, &f.admin, nil), http.StatusOK)
	other := workflowHistory(t, f, &f.admin, name)
	assert.NotNil(t, other.Revisions)
	assert.Empty(t, other.Revisions)
	assert.Equal(t, http.StatusNotFound, workflowRestore(f, &f.admin, name, history.Revisions[0].ID, other.Revision).Code)
	assert.Equal(t, http.StatusConflict, workflowRebase(f, &f.admin, name, other.Revision, []byte("package wrongbranch\n"), retained.Draft).Code)
	resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches/checkout", map[string]string{"name": "main", "revision": other.Revision}, &f.admin, nil), http.StatusOK)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(name, retained.Draft.ID), nil, &f.admin, nil), []byte("package retained\n"), retained.Draft.Revision)
}
