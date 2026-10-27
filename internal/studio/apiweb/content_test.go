package apiweb

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (f *fixture) publish(t *testing.T, draft *draftDTO, user *credentials) saveResult {
	t.Helper()
	require.NotNil(t, draft)
	return resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/drafts/"+draft.ID+"/publish", map[string]string{"revision": draft.Revision}, user, nil), http.StatusOK)
}

func (f *fixture) addDocument(t *testing.T, name, title, text string) Document {
	t.Helper()
	base := f.current(t, &f.admin)
	created := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents", map[string]any{"path": name, "title": title, "kind": "markdown", "content": text, "revision": base.Revision}, &f.admin, nil), http.StatusCreated)
	require.NotNil(t, created.Document)
	f.publish(t, created.Draft, &f.admin)
	return resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+created.Document.ID, nil, &f.admin, nil), http.StatusOK)
}

func TestPermissionFilteredDocumentsFilesAndDrafts(t *testing.T) {
	f := newFixture(t, nil)
	public := f.addDocument(t, "guides/public/readme.md", "Public guide", "# Public guide")
	private := f.addDocument(t, "guides/private/readme.md", "Private guide", "# PRIVATE_SOURCE")
	reader := f.createUser(t, "scopedreader", auth.RoleViewer, &auth.Grants{Read: []string{"guides/public"}, Write: []string{}})
	writer := f.createUser(t, "scopedwriter", auth.RoleEditor, &auth.Grants{Read: []string{"guides/public"}, Write: []string{"guides/public"}})
	listed := f.request(http.MethodGet, testAPI+"/documents", nil, &reader, nil)
	result := resultJSON[struct {
		Documents []Document `json:"documents"`
	}](t, listed, http.StatusOK)
	require.Len(t, result.Documents, 1)
	assert.Equal(t, public.ID, result.Documents[0].ID)
	assert.False(t, result.Documents[0].CanWrite)
	assert.NotContains(t, listed.Body.String(), "Private")
	assert.NotContains(t, listed.Body.String(), private.ID)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/documents/"+private.ID, nil, &reader, nil).Code)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodPost, testAPI+"/preview", map[string]string{"documentId": private.ID, "content": "guess"}, &reader, nil).Code)
	assert.Equal(t, http.StatusOK, f.request(http.MethodPost, testAPI+"/preview", map[string]string{"documentId": public.ID, "content": "preview"}, &reader, nil).Code)
	for _, target := range []string{"docs.json", "themes/private.json", "guides/private/readme.md"} {
		assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/files/content?path="+url.QueryEscape(target), nil, &writer, nil).Code, target)
	}
	root := f.request(http.MethodGet, testAPI+"/files?path=.", nil, &reader, nil)
	assert.Equal(t, http.StatusOK, root.Code)
	assert.Contains(t, root.Body.String(), "guides")
	assert.NotContains(t, root.Body.String(), "docs.json")
	folder := f.request(http.MethodGet, testAPI+"/files?path=guides", nil, &reader, nil)
	assert.Equal(t, http.StatusOK, folder.Code)
	assert.Contains(t, folder.Body.String(), "public")
	assert.NotContains(t, folder.Body.String(), "private")
	base := f.current(t, &f.admin)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/files/move", map[string]string{"path": "guides", "to": "moved", "revision": base.Revision}, &writer, nil).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/documents/"+public.ID+"/move", map[string]string{"to": "guides/private/stolen.md", "revision": base.Revision}, &writer, nil).Code)
	saved := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/"+public.ID, map[string]string{"content": "# Writer draft", "revision": base.Revision}, &writer, nil), http.StatusOK)
	require.NotNil(t, saved.Draft)
	assert.NotContains(t, f.request(http.MethodGet, testAPI+"/drafts", nil, &f.admin, nil).Body.String(), saved.Draft.ID)
	assert.NotContains(t, f.request(http.MethodGet, testAPI+"/drafts", nil, &reader, nil).Body.String(), saved.Draft.ID)
	assert.Contains(t, f.request(http.MethodGet, testAPI+"/drafts", nil, &writer, nil).Body.String(), saved.Draft.ID)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/drafts/"+saved.Draft.ID+"/publish", map[string]any{"revision": saved.Draft.Revision, "push": true}, &writer, nil).Code)
	f.publish(t, saved.Draft, &writer)
	assert.Equal(t, "# Writer draft", resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+public.ID, nil, &reader, nil), http.StatusOK).Content)
}

func TestNewDocumentDraftPreviewMoveHistoryAndRestore(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	created := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents", map[string]string{"path": "guides/new.md", "title": "A new page", "kind": "markdown", "content": "# First version", "revision": base.Revision}, &f.admin, nil), http.StatusCreated)
	require.NotNil(t, created.Document)
	id := created.Document.ID
	doc := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+id, nil, &f.admin, nil), http.StatusOK)
	assert.Equal(t, "# First version", doc.Content)
	assert.True(t, doc.Draft)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/documents/"+id+"?published=true", nil, &f.admin, nil).Code)
	preview := resultJSON[Preview](t, f.request(http.MethodPost, testAPI+"/preview", map[string]string{"documentId": id, "content": "<script>unsafe</script>"}, &f.admin, nil), http.StatusOK)
	assert.NotContains(t, preview.HTML, "<script>")
	assert.NotNil(t, preview.Diagnostics)
	f.publish(t, created.Draft, &f.admin)
	doc = resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+id, nil, &f.admin, nil), http.StatusOK)
	updated := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/"+id, map[string]any{"content": "# Second version", "revision": doc.Revision, "publish": true}, &f.admin, nil), http.StatusOK)
	move := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents/"+id+"/move", map[string]string{"to": "reference/moved.md", "revision": updated.Revision}, &f.admin, nil), http.StatusOK)
	f.publish(t, move.Draft, &f.admin)
	doc = resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+id, nil, &f.admin, nil), http.StatusOK)
	assert.Equal(t, "reference/moved.md", doc.Path)
	assert.Equal(t, "# Second version", doc.Content)
	history := resultJSON[struct {
		Revisions []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"revisions"`
	}](t, f.request(http.MethodGet, testAPI+"/documents/"+id+"/history", nil, &f.admin, nil), http.StatusOK)
	require.NotEmpty(t, history.Revisions)
	first := history.Revisions[len(history.Revisions)-1]
	restored := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents/"+id+"/restore", map[string]string{"historyId": first.ID, "revision": doc.Revision}, &f.admin, nil), http.StatusOK)
	require.NotNil(t, restored.Draft)
	assert.False(t, restored.Published)
	assert.Equal(t, "# Second version", resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+id+"?published=true", nil, &f.admin, nil), http.StatusOK).Content)
	f.publish(t, restored.Draft, &f.admin)
	assert.Equal(t, "# First version", resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+id, nil, &f.admin, nil), http.StatusOK).Content)
}

func TestMovedHistoryCannotExposeVersionsFromUnreadableSourcePaths(t *testing.T) {
	f := newFixture(t, nil)
	private := f.addDocument(t, "private/guide.md", "Guide", "# PRIVATE_HISTORICAL_SECRET")
	updated := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/"+private.ID, map[string]any{"content": "# Public safe version", "revision": private.Revision, "publish": true}, &f.admin, nil), http.StatusOK)
	privateHistory, err := f.content.History(t.Context(), private.Path)
	require.NoError(t, err)
	require.NotEmpty(t, privateHistory)
	move := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents/"+private.ID+"/move", map[string]string{"to": "public/guide.md", "revision": updated.Revision}, &f.admin, nil), http.StatusOK)
	f.publish(t, move.Draft, &f.admin)
	writer := f.createUser(t, "publicwriter", auth.RoleEditor, &auth.Grants{Read: []string{"public"}, Write: []string{"public"}})
	doc := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+private.ID, nil, &writer, nil), http.StatusOK)
	assert.Equal(t, "# Public safe version", doc.Content)
	history := f.request(http.MethodGet, testAPI+"/documents/"+private.ID+"/history", nil, &writer, nil)
	require.Equal(t, http.StatusOK, history.Code)
	assert.NotContains(t, history.Body.String(), "private/guide.md")
	for _, revision := range privateHistory {
		assert.NotContains(t, history.Body.String(), revision.ID)
		assert.Equal(t, http.StatusNotFound, f.request(http.MethodPost, testAPI+"/documents/"+private.ID+"/restore", map[string]string{"historyId": revision.ID, "revision": doc.Revision}, &writer, nil).Code)
	}
	drafts := f.request(http.MethodGet, testAPI+"/drafts", nil, &writer, nil)
	assert.JSONEq(t, `{"drafts":[]}`, drafts.Body.String())
}

func TestAttachmentBytesDownloadsFoldersAndDeletion(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	folder := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/files", map[string]string{"path": "attachments", "revision": base.Revision}, &f.admin, nil), http.StatusCreated)
	published := f.publish(t, folder.Draft, &f.admin)
	body := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3}
	upload := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/files/content?path=attachments%2Fpicture.png", body, &f.admin, map[string]string{"Content-Type": "application/octet-stream", "If-Match": "\"" + published.Revision + "\""}), http.StatusOK)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/files/content?path=attachments%2Fpicture.png", nil, &f.admin, nil).Code)
	published = f.publish(t, upload.Draft, &f.admin)
	download := f.request(http.MethodGet, testAPI+"/files/content?path=attachments%2Fpicture.png", nil, &f.admin, nil)
	require.Equal(t, http.StatusOK, download.Code)
	assert.Equal(t, body, download.Body.Bytes())
	assert.Equal(t, "application/octet-stream", download.Header().Get("Content-Type"))
	assert.Contains(t, download.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, download.Header().Get("Content-Security-Policy"), "sandbox")
	assert.Equal(t, "\""+published.Revision+"\"", download.Header().Get("ETag"))
	head := f.request(http.MethodHead, testAPI+"/files/content?path=attachments%2Fpicture.png", nil, &f.admin, nil)
	assert.Empty(t, head.Body.Bytes())
	assert.Equal(t, download.Header().Get("Content-Length"), head.Header().Get("Content-Length"))
	assert.Equal(t, http.StatusUnsupportedMediaType, f.request(http.MethodPut, testAPI+"/files/content?path=attachments%2Fpicture.png", body, &f.admin, map[string]string{"If-Match": published.Revision}).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPut, testAPI+"/files/content?path=attachments%2Fpicture.png", body, &f.admin, map[string]string{"Content-Type": "application/octet-stream"}).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testAPI+"/files/content?path=..%2Foutside", nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testAPI+"/files/content?path=index.md&path=docs.json", nil, &f.admin, nil).Code)
	move := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/files/move", map[string]string{"path": "attachments/picture.png", "to": "attachments/renamed.png", "revision": published.Revision}, &f.admin, nil), http.StatusOK)
	published = f.publish(t, move.Draft, &f.admin)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/files/content?path=attachments%2Fpicture.png", nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodDelete, testAPI+"/files/content?path=attachments", nil, &f.admin, map[string]string{"If-Match": published.Revision}).Code)
	deleted := resultJSON[saveResult](t, f.request(http.MethodDelete, testAPI+"/files/content?path=attachments%2Frenamed.png", nil, &f.admin, map[string]string{"If-Match": published.Revision}), http.StatusOK)
	published = f.publish(t, deleted.Draft, &f.admin)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/files/content?path=attachments%2Frenamed.png", nil, &f.admin, nil).Code)
	folderRemoval := resultJSON[saveResult](t, f.request(http.MethodDelete, testAPI+"/files/content?path=attachments", nil, &f.admin, map[string]string{"If-Match": published.Revision}), http.StatusOK)
	assert.Equal(t, "rmdir", folderRemoval.Draft.Operation)
	f.publish(t, folderRemoval.Draft, &f.admin)
}

func TestStrictRequestsPublicationPolicyAndStorageAuthorization(t *testing.T) {
	f := newFixture(t, func(options *Options) {
		options.Publishing = func(context.Context) (PublishingPolicy, error) { return PublishingPolicy{AutoPublish: true}, nil }
	})
	writer := f.createUser(t, "policywriter", auth.RoleEditor, nil)
	base := f.current(t, &writer)
	for _, body := range []string{`{"content":"x","revision":"` + base.Revision + `","Content":"y"}`, `{"content":null,"revision":"` + base.Revision + `"}`, `{"content":"x","revision":"` + base.Revision + `","unknown":true}`, `{"revision":"` + base.Revision + `"}`} {
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPut, testAPI+"/documents/home", []byte(body), &writer, nil).Code)
	}
	assert.Equal(t, http.StatusUnsupportedMediaType, f.request(http.MethodPut, testAPI+"/documents/home", []byte(`{}`), &writer, map[string]string{"Content-Type": "text/plain"}).Code)
	published := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "# Published by policy", "revision": base.Revision}, &writer, nil), http.StatusOK)
	assert.True(t, published.Published)
	assert.Equal(t, "# Published by policy", f.current(t, &writer).Content)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodGet, testAPI+"/branches", nil, &writer, nil).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/sync", map[string]string{"action": "push", "revision": published.Revision}, &writer, nil).Code)
	status := resultJSON[syncDTO](t, f.request(http.MethodGet, testAPI+"/sync/status", nil, &writer, nil), http.StatusOK)
	assert.Equal(t, "local", status.Backend)
	assert.Equal(t, published.Revision, status.Revision)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/branches", map[string]string{"name": "new", "revision": published.Revision}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/sync", map[string]string{"action": "execute arbitrary shell", "revision": published.Revision}, &f.admin, nil).Code)
	tooLarge := strings.Repeat("x", content.MaxTextBytes+1)
	assert.Equal(t, http.StatusRequestEntityTooLarge, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": tooLarge, "revision": published.Revision}, &writer, nil).Code)
}
