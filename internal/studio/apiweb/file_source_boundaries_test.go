package apiweb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceDraftReadRechecksSessionAfterContentWait(t *testing.T) {
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
	editor := f.createUser(t, "queued-source-owner", auth.RoleEditor, nil)
	base := f.current(t, &editor)
	name, secret := "examples/queued.js", []byte("private queued source must remain a draft\n")
	created := resultJSON[saveResult](t, sourceUpload(f, &editor, name, base.Revision, secret, nil), http.StatusOK)
	require.NotNil(t, created.Draft)
	before, err := f.content.ReadDraft(t.Context(), created.Draft.ID)
	require.NoError(t, err)
	backend.armed.Store(true)
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		completed <- f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &editor, nil)
	}()
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owned source read did not wait inside the content gate")
	}
	require.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/session", nil, &editor, nil).Code)
	close(backend.release)
	select {
	case response := <-completed:
		assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		for _, hidden := range []string{string(secret), created.Draft.ID, created.Draft.Revision, created.Draft.BaseRevision, name} {
			assert.NotContains(t, response.Body.String(), hidden)
		}
		assert.Empty(t, response.Header().Get("ETag"))
		assert.Empty(t, response.Header().Get("X-Base-Revision"))
		assert.Empty(t, response.Header().Get("Content-Disposition"))
	case <-time.After(5 * time.Second):
		t.Fatal("owned source read did not finish after content was released")
	}
	after, err := f.content.ReadDraft(t.Context(), created.Draft.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, base.Revision, f.current(t, &f.admin).Revision)
	editor = f.login(t, "queued-source-owner", testPassword)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &editor, nil), secret, created.Draft.Revision)
}

func TestSourceFilePublicBoundaryFailuresRetainOwnerDraft(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	name, secret := "examples/boundary.ts", []byte("preserve this owner draft\n")
	created := resultJSON[saveResult](t, sourceUpload(f, &f.admin, name, base.Revision, secret, nil), http.StatusOK)
	require.NotNil(t, created.Draft)
	before, err := f.content.ReadDraft(t.Context(), created.Draft.ID)
	require.NoError(t, err)
	unknown := f.request(http.MethodGet, sourceTarget(name, "")+"&unexpected=true", nil, &f.admin, nil)
	assert.Equal(t, http.StatusBadRequest, unknown.Code, unknown.Body.String())
	unsupported := f.request(http.MethodPost, sourceTarget(name, ""), nil, &f.admin, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, unsupported.Code, unsupported.Body.String())
	assert.Equal(t, "GET, HEAD, PUT, DELETE", unsupported.Header().Get("Allow"))
	missing := &draftDTO{ID: strings.Repeat("0", 32), Revision: strings.Repeat("0", 64)}
	assert.Equal(t, http.StatusNotFound, sourceUpload(f, &f.admin, name, base.Revision, []byte("missing draft replacement"), missing).Code)
	reader := f.createUser(t, "readable-source-editor", auth.RoleEditor, &auth.Grants{Read: []string{"examples"}, Write: []string{}})
	denied := sourceUpload(f, &reader, name, base.Revision, []byte("read-only replacement"), nil)
	assert.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	after, err := f.content.ReadDraft(t.Context(), created.Draft.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	assert.Equal(t, base.Revision, f.current(t, &f.admin).Revision)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &f.admin, nil), secret, created.Draft.Revision)
}
