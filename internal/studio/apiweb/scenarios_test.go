package apiweb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentCreationRejectsInvalidMetadataAndListsUnpublishedDrafts(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	original := map[string]any{"path": "guides/new.md", "title": "New guide", "kind": "markdown", "content": "# New guide", "revision": base.Revision}
	cases := []struct {
		name   string
		update map[string]any
		status int
	}{
		{"escaping source", map[string]any{"path": "../escape.md"}, http.StatusBadRequest},
		{"hidden source", map[string]any{"path": ".git/config"}, http.StatusBadRequest},
		{"manifest source", map[string]any{"path": "docs.json"}, http.StatusForbidden},
		{"theme source", map[string]any{"path": "themes/custom.md"}, http.StatusForbidden},
		{"empty title", map[string]any{"title": ""}, http.StatusBadRequest},
		{"blank title", map[string]any{"title": " \t "}, http.StatusBadRequest},
		{"control title", map[string]any{"title": "bad\nname"}, http.StatusBadRequest},
		{"large title", map[string]any{"title": strings.Repeat("λ", 129)}, http.StatusBadRequest},
		{"unknown format", map[string]any{"kind": "xml"}, http.StatusBadRequest},
		{"nonhtml output", map[string]any{"outputPath": "guide.txt"}, http.StatusBadRequest},
		{"internal output", map[string]any{"outputPath": "_docs/page.html"}, http.StatusBadRequest},
		{"escaping output", map[string]any{"outputPath": "../guide.html"}, http.StatusBadRequest},
		{"unknown field", map[string]any{"actor": "someone else"}, http.StatusBadRequest},
		{"invalid revision", map[string]any{"revision": strings.Repeat("g", 64)}, http.StatusBadRequest},
		{"stale revision", map[string]any{"revision": strings.Repeat("f", 64)}, http.StatusConflict},
		{"existing source", map[string]any{"path": "index.md"}, http.StatusConflict},
		{"oversize source", map[string]any{"content": strings.Repeat("x", content.MaxTextBytes+1)}, http.StatusRequestEntityTooLarge},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			input := maps.Clone(original)
			maps.Copy(input, item.update)
			response := f.request(http.MethodPost, testAPI+"/documents", input, &f.admin, nil)
			assert.Equal(t, item.status, response.Code, response.Body.String())
			assert.Equal(t, "application/json; charset=utf-8", response.Header().Get("Content-Type"))
		})
	}
	missing := maps.Clone(original)
	delete(missing, "content")
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/documents", missing, &f.admin, nil).Code)
	created := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents", original, &f.admin, nil), http.StatusCreated)
	require.NotNil(t, created.Document)
	require.NotNil(t, created.Draft)
	listed := resultJSON[struct {
		Documents []Document `json:"documents"`
		Revision  string     `json:"revision"`
	}](t, f.request(http.MethodGet, testAPI+"/documents", nil, &f.admin, nil), http.StatusOK)
	require.Len(t, listed.Documents, 2)
	assert.Equal(t, base.Revision, listed.Revision)
	var draft *Document
	for i := range listed.Documents {
		if listed.Documents[i].ID == created.Document.ID {
			draft = &listed.Documents[i]
		}
	}
	require.NotNil(t, draft)
	assert.True(t, draft.Draft)
	assert.Equal(t, "guides/new.html", draft.OutputPath)
	assert.Empty(t, draft.Content, "tree metadata does not copy source payloads")
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/documents", original, &f.admin, nil).Code)
	scoped := f.createUser(t, "separategrants", auth.RoleEditor, &auth.Grants{Read: []string{"readable"}, Write: []string{"writable"}})
	for _, name := range []string{"readable/new.md", "writable/new.md"} {
		input := maps.Clone(original)
		input["path"] = name
		assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/documents", input, &scoped, nil).Code)
	}
	assert.Equal(t, http.StatusMethodNotAllowed, f.request(http.MethodPatch, testAPI+"/documents", map[string]string{}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/documents/ABC_9.unknown", nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/documents/invalid$id", nil, &f.admin, nil).Code)
}

func TestDraftRequestsPreserveCASAndRecheckChangedWriteGrants(t *testing.T) {
	f := newFixture(t, nil)
	writer := f.createUser(t, "draftwriter", auth.RoleEditor, nil)
	base := f.current(t, &writer)
	saved := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "# Saved source", "revision": base.Revision}, &writer, nil), http.StatusOK)
	require.NotNil(t, saved.Draft)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/drafts/"+saved.Draft.ID+"/publish", []byte("{"), &writer, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/drafts/"+saved.Draft.ID+"/publish", map[string]string{"revision": "bad"}, &writer, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/drafts/"+saved.Draft.ID+"/publish", map[string]string{"revision": saved.Draft.Revision, "message": strings.Repeat("x", 257)}, &writer, nil).Code)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/drafts/"+saved.Draft.ID+"/publish", map[string]string{"revision": strings.Repeat("f", 64)}, &writer, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodDelete, testAPI+"/drafts/"+saved.Draft.ID, nil, &writer, nil).Code)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodDelete, testAPI+"/drafts/"+saved.Draft.ID, nil, &writer, map[string]string{"If-Match": strings.Repeat("f", 64)}).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, f.request(http.MethodPost, testAPI+"/drafts", map[string]string{}, &writer, nil).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, f.request(http.MethodGet, testAPI+"/drafts/"+saved.Draft.ID, nil, &writer, nil).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, f.request(http.MethodDelete, testAPI+"/drafts/"+saved.Draft.ID+"/publish", nil, &writer, nil).Code)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/documents/home/move", map[string]string{"to": "moved.md", "revision": base.Revision}, &writer, nil).Code)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodDelete, testAPI+"/documents/home", nil, &writer, map[string]string{"If-Match": base.Revision}).Code)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "different window", "revision": base.Revision}, &writer, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "x", "revision": base.Revision, "message": "newline\nmessage"}, &f.admin, nil).Code)
	resultJSON[auth.User](t, f.request(http.MethodPatch, testAPI+"/users/"+writer.user.ID, map[string]any{"grants": auth.Grants{Read: []string{"."}, Write: []string{}}}, &f.admin, nil), http.StatusOK)
	writer = f.login(t, "draftwriter", testPassword)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/drafts/"+saved.Draft.ID+"/publish", map[string]string{"revision": saved.Draft.Revision}, &writer, nil).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "denied", "revision": base.Revision, "draftId": saved.Draft.ID, "draftRevision": saved.Draft.Revision}, &writer, nil).Code)
	assert.Equal(t, "# Saved source", resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &writer, nil), http.StatusOK).Content)
	assert.Equal(t, base.Content, f.current(t, &writer).Content)
	assert.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/drafts/"+saved.Draft.ID, nil, &writer, map[string]string{"If-Match": saved.Draft.Revision}).Code)
}

func TestBatchDraftsDoNotLeakOrPartiallyPublishUnauthorizedChanges(t *testing.T) {
	f := newFixture(t, nil)
	writer := f.createUser(t, "batchwriter", auth.RoleEditor, &auth.Grants{Read: []string{"."}, Write: []string{"public"}})
	base := f.current(t, &writer)
	text := "text from a batch draft"
	draft, err := f.content.SaveDraft(t.Context(), content.DraftInput{BaseRevision: base.Revision, Changes: []content.Change{{Operation: "write", Path: "public/allowed.txt", Content: &text}, {Operation: "write", Path: "private/denied.txt", Content: &text}}}, actor(&writer.user))
	require.NoError(t, err)
	listed := f.request(http.MethodGet, testAPI+"/drafts", nil, &writer, nil)
	require.Equal(t, http.StatusOK, listed.Code)
	assert.Contains(t, listed.Body.String(), draft.ID)
	assert.NotContains(t, listed.Body.String(), text)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/drafts/"+draft.ID+"/publish", map[string]string{"revision": draft.Revision}, &writer, nil).Code)
	_, err = f.content.Read(t.Context(), "public/allowed.txt")
	assert.ErrorIs(t, err, content.ErrNotFound)
	_, err = f.content.ReadDraft(t.Context(), draft.ID)
	assert.NoError(t, err)
	resultJSON[auth.User](t, f.request(http.MethodPatch, testAPI+"/users/"+writer.user.ID, map[string]any{"grants": auth.Grants{Read: []string{"public"}, Write: []string{"public"}}}, &f.admin, nil), http.StatusOK)
	writer = f.login(t, "batchwriter", testPassword)
	assert.NotContains(t, f.request(http.MethodGet, testAPI+"/drafts", nil, &writer, nil).Body.String(), draft.ID)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodDelete, testAPI+"/drafts/"+draft.ID, nil, &writer, map[string]string{"If-Match": draft.Revision}).Code)
	move, err := f.content.SaveDraft(t.Context(), content.DraftInput{BaseRevision: base.Revision, Change: content.Change{Operation: "move", Path: "public/source.txt", To: "private/destination.txt"}}, actor(&writer.user))
	require.NoError(t, err)
	assert.NotContains(t, f.request(http.MethodGet, testAPI+"/drafts", nil, &writer, nil).Body.String(), move.ID)
}

func TestClosedContentServiceFailsWithoutPublishingOrExposingPaths(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	require.NoError(t, f.content.Close())
	requests := []struct {
		method string
		target string
		body   any
	}{
		{http.MethodGet, "/documents", nil},
		{http.MethodGet, "/documents/home", nil},
		{http.MethodGet, "/drafts", nil},
		{http.MethodGet, "/files", nil},
		{http.MethodGet, "/sync/status", nil},
		{http.MethodGet, "/branches", nil},
		{http.MethodPost, "/documents", map[string]string{"path": "new.md", "title": "New", "kind": "markdown", "content": "# New", "revision": base.Revision}},
		{http.MethodPost, "/files", map[string]string{"path": "folder", "revision": base.Revision}},
		{http.MethodPost, "/branches", map[string]string{"name": "feature", "revision": base.Revision}},
		{http.MethodPost, "/sync", map[string]string{"action": "fetch", "revision": base.Revision}},
		{http.MethodPost, "/preview", map[string]string{"documentId": "home", "content": "source"}},
	}
	for _, item := range requests {
		response := f.request(item.method, testAPI+item.target, item.body, &f.admin, nil)
		assert.Equal(t, http.StatusInternalServerError, response.Code, "%s %s: %s", item.method, item.target, response.Body.String())
		assert.NotContains(t, response.Body.String(), filepath.Dir(f.root))
	}
}

type failingUpload struct{ closed bool }

func (f *failingUpload) Read([]byte) (int, error) {
	return 0, errors.New("private filesystem I/O detail")
}
func (f *failingUpload) Close() error { f.closed = true; return nil }

func TestRawUploadsBoundDeclaredAndStreamedBodiesAndCloseFailures(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	run := func(body io.ReadCloser, size int64, ctx context.Context) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, testOrigin+testAPI+"/files/content?path=upload.pdf", nil).WithContext(ctx)
		r.Body, r.ContentLength = body, size
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("If-Match", base.Revision)
		r.Header.Set("X-CSRF-Token", f.admin.csrf)
		r.AddCookie(f.admin.cookie)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	assert.Equal(t, http.StatusRequestEntityTooLarge, run(io.NopCloser(strings.NewReader("small")), content.MaxBinaryBytes+1, t.Context()).Code)
	assert.Equal(t, http.StatusRequestEntityTooLarge, run(io.NopCloser(bytes.NewReader(make([]byte, content.MaxBinaryBytes+1))), -1, t.Context()).Code)
	failure := &failingUpload{}
	response := run(failure, -1, t.Context())
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.True(t, failure.closed)
	assert.NotContains(t, response.Body.String(), "private filesystem")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.Equal(t, http.StatusRequestTimeout, run(io.NopCloser(strings.NewReader("small")), 5, ctx).Code)
	assert.Equal(t, http.StatusUnsupportedMediaType, f.request(http.MethodPut, testAPI+"/files/content?path=upload.pdf", []byte("data"), &f.admin, map[string]string{"Content-Type": "application/octet-stream", "Content-Encoding": "br", "If-Match": base.Revision}).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/files/move", map[string]string{"path": "index.md", "to": "moved.md", "revision": ""}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testAPI+"/files?path=one&path=two", nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testAPI+"/files?path=../outside", nil, &f.admin, nil).Code)
}
