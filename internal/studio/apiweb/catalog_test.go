package apiweb

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type documentCatalog struct {
	Documents []Document `json:"documents"`
	Revision  string     `json:"revision"`
}

func TestPublishedDocumentCatalogExcludesDraftsAndFiltersCurrentSourceGrants(t *testing.T) {
	f := newFixture(t, nil)
	public := f.addDocument(t, "public/guide.md", "Public guide", "# Public")
	private := f.addDocument(t, "private/secret.md", "Secret published title", "# Private")
	current := f.current(t, &f.admin)
	created := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents", map[string]string{"path": "public/unpublished.md", "title": "Unpublished page title", "kind": "markdown", "content": "# Draft only", "revision": current.Revision}, &f.admin, nil), http.StatusCreated)
	require.NotNil(t, created.Document)
	defaultCatalog := resultJSON[documentCatalog](t, f.request(http.MethodGet, testAPI+"/documents", nil, &f.admin, nil), http.StatusOK)
	ids := map[string]bool{}
	for _, doc := range defaultCatalog.Documents {
		ids[doc.ID] = true
	}
	assert.True(t, ids[created.Document.ID], "default catalog still includes the caller's unpublished documents")
	response := f.request(http.MethodGet, testAPI+"/documents?published=true", nil, &f.admin, nil)
	published := resultJSON[documentCatalog](t, response, http.StatusOK)
	assert.Equal(t, current.Revision, published.Revision)
	ids = map[string]bool{}
	for _, doc := range published.Documents {
		ids[doc.ID] = true
		assert.False(t, doc.Draft)
		assert.Empty(t, doc.DraftID)
	}
	assert.True(t, ids[public.ID])
	assert.True(t, ids[private.ID])
	assert.False(t, ids[created.Document.ID])
	assert.NotContains(t, response.Body.String(), "Unpublished page title")
	assert.Len(t, published.Documents, 3)
	withDrafts := resultJSON[documentCatalog](t, f.request(http.MethodGet, testAPI+"/documents?published=false", nil, &f.admin, nil), http.StatusOK)
	assert.Len(t, withDrafts.Documents, len(defaultCatalog.Documents))

	viewer := f.createUser(t, "catalog-reader", auth.RoleViewer, &auth.Grants{Read: []string{"public"}, Write: []string{}})
	response = f.request(http.MethodGet, testAPI+"/documents?published=true", nil, &viewer, nil)
	filtered := resultJSON[documentCatalog](t, response, http.StatusOK)
	require.Len(t, filtered.Documents, 1)
	assert.Equal(t, public.ID, filtered.Documents[0].ID)
	assert.Equal(t, current.Revision, filtered.Revision)
	assert.NotContains(t, response.Body.String(), "Secret published title")
	assert.NotContains(t, response.Body.String(), "private/secret.md")
	assert.NotContains(t, response.Body.String(), "Unpublished page title")
	noPages := f.createUser(t, "catalog-empty", auth.RoleViewer, &auth.Grants{Read: []string{"unmatched"}, Write: []string{}})
	response = f.request(http.MethodGet, testAPI+"/documents?published=true", nil, &noPages, nil)
	empty := resultJSON[documentCatalog](t, response, http.StatusOK)
	assert.NotNil(t, empty.Documents)
	assert.Empty(t, empty.Documents)
	assert.Contains(t, response.Body.String(), `"documents":[]`)
	for _, query := range []string{"published=", "published=1", "published=TRUE", "published=%ZZ", "published=true&published=false"} {
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testAPI+"/documents?"+query, nil, &f.admin, nil).Code)
	}
}

func TestPublishedCatalogDoesNotReadOrExposeUnavailableDraftMetadata(t *testing.T) {
	f := newFixture(t, nil)
	current := f.current(t, &f.admin)
	resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents", map[string]string{"path": "unpublished.md", "title": "Private draft metadata", "kind": "markdown", "content": "# Draft", "revision": current.Revision}, &f.admin, nil), http.StatusCreated)
	drafts := filepath.Join(filepath.Dir(f.root), "state", "drafts")
	backup := drafts + "-retained"
	require.NoError(t, os.Rename(drafts, backup))
	require.NoError(t, os.WriteFile(drafts, []byte("unavailable draft directory"), 0o600))
	t.Cleanup(func() {
		require.NoError(t, os.Remove(drafts))
		require.NoError(t, os.Rename(backup, drafts))
	})
	response := f.request(http.MethodGet, testAPI+"/documents?published=true", nil, &f.admin, nil)
	published := resultJSON[documentCatalog](t, response, http.StatusOK)
	require.Len(t, published.Documents, 1)
	assert.Equal(t, "home", published.Documents[0].ID)
	assert.NotContains(t, response.Body.String(), "Private draft metadata")
	assert.NotEqual(t, http.StatusOK, f.request(http.MethodGet, testAPI+"/documents", nil, &f.admin, nil).Code)
}

type catalogReadGate struct {
	content.Backend
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (b *catalogReadGate) Status(ctx context.Context) (content.SyncStatus, error) {
	if b.armed.CompareAndSwap(true, false) {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			return content.SyncStatus{}, ctx.Err()
		}
	}
	return b.Backend.Status(ctx)
}

func TestPublishedCatalogRechecksSessionAfterWaitingForContent(t *testing.T) {
	f := newFixture(t, nil)
	backend := &catalogReadGate{Backend: content.NewLocalBackend(), entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	}()
	replaceContentForHTTPTest(t, f, content.Config{Storage: backend})
	viewer := f.createUser(t, "queued-catalog-reader", auth.RoleViewer, nil)
	backend.armed.Store(true)
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() { completed <- f.request(http.MethodGet, testAPI+"/documents?published=true", nil, &viewer, nil) }()
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("catalog read did not wait for content")
	}
	require.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/session", nil, &viewer, nil).Code)
	close(backend.release)
	select {
	case response := <-completed:
		assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), `"documents"`)
	case <-time.After(5 * time.Second):
		t.Fatal("catalog read did not finish after content was released")
	}
}

func TestImportedAdministrationModulesAreRequiredAndServedExactly(t *testing.T) {
	f := newFixture(t, nil)
	original := f.handler.(*handler).opts
	assets := fstest.MapFS{}
	for _, name := range assetNames {
		data, err := fs.ReadFile(original.Assets, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		require.NoError(t, err)
		assets[name] = &fstest.MapFile{Data: data}
	}
	for _, required := range []string{"navigation.mjs", "storage.mjs", "files.mjs"} {
		incomplete := fstest.MapFS{}
		for name, file := range assets {
			if name != required {
				incomplete[name] = file
			}
		}
		options := original
		options.Assets = incomplete
		result, err := New(options)
		assert.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), required)
		response := f.request(http.MethodGet, testBase+"/_docs/admin/"+required, nil, nil, nil)
		require.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, assets[required].Data, response.Body.Bytes())
		assert.Contains(t, response.Header().Get("Content-Type"), "javascript")
	}
	delete(assets, "editor.js.LEGAL.txt")
	delete(assets, "THIRD_PARTY_NOTICES.txt")
	options := original
	options.Assets = assets
	_, err := New(options)
	assert.NoError(t, err, "optional legal notices do not mask required imported modules")
}
