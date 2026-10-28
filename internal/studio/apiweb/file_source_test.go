package apiweb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sourceTarget(name, draft string) string {
	values := url.Values{"path": []string{name}}
	if draft != "" {
		values.Set("draftId", draft)
	}
	return testAPI + "/files/content?" + values.Encode()
}

func sourceUpload(f *fixture, identity *credentials, name, base string, data []byte, draft *draftDTO) *httptest.ResponseRecorder {
	headers := map[string]string{"Content-Type": "application/octet-stream", "If-Match": `"` + base + `"`}
	if draft != nil {
		headers["X-Draft-ID"], headers["X-Draft-Revision"] = draft.ID, draft.Revision
	}
	return f.request(http.MethodPut, sourceTarget(name, ""), data, identity, headers)
}

func assertSourceAttachment(t *testing.T, response *httptest.ResponseRecorder, data []byte, revision string) {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, data, response.Body.Bytes())
	assert.Equal(t, "application/octet-stream", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Header().Get("Content-Disposition"), "attachment")
	assert.Equal(t, "sandbox; default-src 'none'", response.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	assert.Equal(t, `"`+revision+`"`, response.Header().Get("ETag"))
}

func TestRegisteredDocumentAdoptsRawTextDraftWithoutChangingOwnerCAS(t *testing.T) {
	for _, text := range []string{"", "\ufeff# Сохранённый исходник\r\nUnicode: 日本語 <>&\r\n"} {
		name := "bom_crlf_unicode"
		if text == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			base := f.current(t, &f.admin)
			created := resultJSON[saveResult](t, sourceUpload(f, &f.admin, "index.md", base.Revision, []byte(text), nil), http.StatusOK)
			require.NotNil(t, created.Draft)
			opened := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &f.admin, nil), http.StatusOK)
			assert.Equal(t, text, opened.Content)
			assert.Equal(t, created.Draft.ID, opened.DraftID)
			assert.Equal(t, created.Draft.Revision, opened.DraftRevision)
			assert.Equal(t, created.Draft.BaseRevision, opened.BaseRevision)
			assert.Equal(t, base.Revision, opened.Revision)

			input := map[string]any{"content": text + "\nDocument editor edit\n", "revision": opened.Revision, "draftId": opened.DraftID, "draftRevision": opened.DraftRevision, "baseRevision": opened.BaseRevision}
			updated := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", input, &f.admin, nil), http.StatusOK)
			require.NotNil(t, updated.Draft)
			assert.Equal(t, created.Draft.ID, updated.Draft.ID)
			assert.NotEqual(t, created.Draft.Revision, updated.Draft.Revision)
			assert.Equal(t, created.Draft.BaseRevision, updated.Draft.BaseRevision)
			assert.Equal(t, http.StatusConflict, f.request(http.MethodPut, testAPI+"/documents/home", input, &f.admin, nil).Code)
			assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget("index.md", updated.Draft.ID), nil, &f.admin, nil), []byte(input["content"].(string)), updated.Draft.Revision)
		})
	}
}

func TestRawSourceDraftCanReopenBeforePublicationAndResaveWithOwnerCAS(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	name := "examples/client.js"
	first := []byte("export const value = '<script>not executed</script>';\n")
	created := resultJSON[saveResult](t, sourceUpload(f, &f.admin, name, base.Revision, first, nil), http.StatusOK)
	require.NotNil(t, created.Draft)
	assert.False(t, created.Published)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, sourceTarget(name, ""), nil, &f.admin, nil).Code)
	response := f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &f.admin, nil)
	assertSourceAttachment(t, response, first, created.Draft.Revision)
	assert.Equal(t, base.Revision, response.Header().Get("X-Base-Revision"))
	head := f.request(http.MethodHead, sourceTarget(name, created.Draft.ID), nil, &f.admin, nil)
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.String())
	assert.Equal(t, response.Header().Get("Content-Length"), head.Header().Get("Content-Length"))

	second := []byte("export const value = 'owner revision two';\n")
	updated := resultJSON[saveResult](t, sourceUpload(f, &f.admin, name, base.Revision, second, created.Draft), http.StatusOK)
	require.NotNil(t, updated.Draft)
	assert.Equal(t, created.Draft.ID, updated.Draft.ID)
	assert.NotEqual(t, created.Draft.Revision, updated.Draft.Revision)
	assert.Equal(t, base.Revision, updated.Draft.BaseRevision)
	stale := sourceUpload(f, &f.admin, name, base.Revision, []byte("stale editor window"), created.Draft)
	assert.Equal(t, http.StatusConflict, stale.Code)
	assert.NotContains(t, stale.Body.String(), updated.Draft.Revision)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(name, updated.Draft.ID), nil, &f.admin, nil), second, updated.Draft.Revision)

	published := f.publish(t, updated.Draft, &f.admin)
	assert.True(t, published.Published)
	response = f.request(http.MethodGet, sourceTarget(name, ""), nil, &f.admin, nil)
	assertSourceAttachment(t, response, second, published.Revision)
	assert.Empty(t, response.Header().Get("X-Base-Revision"))
	complete := f.request(http.MethodGet, sourceTarget(name, ""), nil, &f.admin, map[string]string{"Range": "bytes=0-2", "If-None-Match": `"` + published.Revision + `"`})
	assertSourceAttachment(t, complete, second, published.Revision)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, sourceTarget(name, updated.Draft.ID), nil, &f.admin, nil).Code)
	project, err := f.content.Project(t.Context())
	require.NoError(t, err)
	require.Len(t, project.Documents, 1, "literal code sources must not become document pages")
	assert.Equal(t, "home", project.Documents[0].ID)
	manifest, err := f.content.Read(t.Context(), "docs.json")
	require.NoError(t, err)
	var sourceManifest struct {
		Assets []string `json:"assets"`
	}
	require.NoError(t, json.Unmarshal([]byte(manifest.Content), &sourceManifest))
	assert.NotContains(t, sourceManifest.Assets, name, "literal JavaScript must not become an active asset through suffix matching")
	files := resultJSON[struct {
		Files []fileDTO `json:"files"`
	}](t, f.request(http.MethodGet, testAPI+"/files?path=examples", nil, &f.admin, nil), http.StatusOK)
	require.Len(t, files.Files, 1)
	assert.Equal(t, name, files.Files[0].Path)
}

func TestRawSourceDraftOwnershipPathAndHeaderFailuresNeverExposePrivateContent(t *testing.T) {
	f := newFixture(t, nil)
	other := f.createUser(t, "other-source-editor", auth.RoleEditor, nil)
	otherAdmin := f.createUser(t, "other-source-admin", auth.RoleAdmin, nil)
	base := f.current(t, &f.admin)
	name, secret := "examples/source.ts", "private owner source marker"
	created := resultJSON[saveResult](t, sourceUpload(f, &f.admin, name, base.Revision, []byte(secret), nil), http.StatusOK)
	require.NotNil(t, created.Draft)
	for _, response := range []*httptest.ResponseRecorder{
		f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &other, nil),
		f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &otherAdmin, nil),
		f.request(http.MethodGet, sourceTarget("examples/other.ts", created.Draft.ID), nil, &f.admin, nil),
		sourceUpload(f, &other, name, base.Revision, []byte("foreign replacement"), created.Draft),
		sourceUpload(f, &otherAdmin, name, base.Revision, []byte("admin foreign replacement"), created.Draft),
		sourceUpload(f, &f.admin, "examples/other.ts", base.Revision, []byte("mismatched replacement"), created.Draft),
	} {
		assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), secret)
		assert.NotContains(t, response.Body.String(), created.Draft.ID)
		assert.NotContains(t, response.Body.String(), created.Draft.Revision)
		assert.Empty(t, response.Header().Get("X-Base-Revision"))
		assert.Empty(t, response.Header().Get("ETag"))
	}
	for _, headers := range []map[string]string{
		{"X-Draft-ID": created.Draft.ID},
		{"X-Draft-Revision": created.Draft.Revision},
		{"X-Draft-ID": strings.Repeat("a", 33), "X-Draft-Revision": created.Draft.Revision},
		{"X-Draft-ID": created.Draft.ID, "X-Draft-Revision": strings.Repeat("a", 65)},
		{"X-Draft-ID": strings.Repeat("A", 32), "X-Draft-Revision": created.Draft.Revision},
		{"X-Draft-ID": created.Draft.ID, "X-Draft-Revision": "\"" + created.Draft.Revision + "\""},
		{"X-Draft-Rebase": "true"},
	} {
		headers["Content-Type"], headers["If-Match"] = "application/octet-stream", base.Revision
		response := f.request(http.MethodPut, sourceTarget(name, ""), []byte("bad headers"), &f.admin, headers)
		assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), secret)
	}
	for _, repeated := range []string{"X-Draft-ID", "X-Draft-Revision"} {
		r := httptest.NewRequest(http.MethodPut, testOrigin+sourceTarget(name, ""), bytes.NewBufferString("duplicate headers"))
		r.AddCookie(f.admin.cookie)
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("X-CSRF-Token", f.admin.csrf)
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("If-Match", base.Revision)
		r.Header.Set("X-Draft-ID", created.Draft.ID)
		r.Header.Set("X-Draft-Revision", created.Draft.Revision)
		r.Header.Add(repeated, r.Header.Get(repeated))
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, r)
		assert.Equal(t, http.StatusBadRequest, response.Code)
	}
	for _, target := range []string{
		sourceTarget(name, "") + "&draftId=short",
		sourceTarget(name, created.Draft.ID) + "&draftId=" + created.Draft.ID,
		sourceTarget(name, created.Draft.ID) + "&unexpected=true",
	} {
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, target, nil, &f.admin, nil).Code)
	}
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &f.admin, nil), []byte(secret), created.Draft.Revision)
}

func TestRawSourceFormerEditorCannotReadDraftWithoutCurrentWritePermission(t *testing.T) {
	f := newFixture(t, nil)
	former := f.createUser(t, "former-source-editor", auth.RoleEditor, nil)
	base := f.current(t, &former)
	name := "examples/former.js"
	secret := []byte("former editor private draft bytes")
	created := resultJSON[saveResult](t, sourceUpload(f, &former, name, base.Revision, secret, nil), http.StatusOK)
	require.NotNil(t, created.Draft)
	changed := f.request(http.MethodPatch, testAPI+"/users/"+former.user.ID, map[string]string{"role": "viewer"}, &f.admin, nil)
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())
	assert.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &former, nil).Code)
	former = f.login(t, "former-source-editor", testPassword)
	response := f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &former, nil)
	assert.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), string(secret))
	assert.NotContains(t, response.Body.String(), created.Draft.ID)
	assert.Empty(t, response.Header().Get("ETag"))
	assert.Empty(t, response.Header().Get("X-Base-Revision"))
}

func TestRawSourceAllowsOnlyBoundedTextDraftViewsAndKeepsBinaryDownloads(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	for _, extension := range []string{"go", "js", "ts", "sh", "sql", "css", "xml"} {
		name := "examples/literal." + extension
		text := []byte("<tag>literal " + extension + "</tag>\n")
		created := resultJSON[saveResult](t, sourceUpload(f, &f.admin, name, base.Revision, text, nil), http.StatusOK)
		require.NotNil(t, created.Draft)
		assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget(name, created.Draft.ID), nil, &f.admin, nil), text, created.Draft.Revision)
	}
	for _, name := range []string{".git/hidden.js", "examples/.hidden.js", "credentials.json", "secret-values.yaml", "examples/blocked.exe", "examples/unapproved.mjs"} {
		response := sourceUpload(f, &f.admin, name, base.Revision, []byte("not admitted"), nil)
		assert.Equal(t, http.StatusBadRequest, response.Code, name+": "+response.Body.String())
	}
	assert.Equal(t, http.StatusBadRequest, sourceUpload(f, &f.admin, "examples/invalid.js", base.Revision, []byte{0xff}, nil).Code)
	assert.Equal(t, http.StatusRequestEntityTooLarge, sourceUpload(f, &f.admin, "examples/large.js", base.Revision, make([]byte, content.MaxTextBytes+1), nil).Code)
	binary := []byte{'%', 'P', 'D', 'F', 0, 0xff}
	created := resultJSON[saveResult](t, sourceUpload(f, &f.admin, "examples/sample.pdf", base.Revision, binary, nil), http.StatusOK)
	require.NotNil(t, created.Draft)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, sourceTarget("examples/sample.pdf", created.Draft.ID), nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, sourceUpload(f, &f.admin, "examples/sample.pdf", base.Revision, binary, created.Draft).Code)
	published := f.publish(t, created.Draft, &f.admin)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget("examples/sample.pdf", ""), nil, &f.admin, nil), binary, published.Revision)
}

func TestRawSourceResaveCannotRebindDraftToAnotherGitBranch(t *testing.T) {
	f := newFixture(t, nil)
	useGitStorage(t, f)
	base := f.current(t, &f.admin)
	data := []byte("package retained\n")
	created := resultJSON[saveResult](t, sourceUpload(f, &f.admin, "examples/branch.go", base.Revision, data, nil), http.StatusOK)
	require.NotNil(t, created.Draft)
	resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches", map[string]string{"name": "other", "revision": base.Revision}, &f.admin, nil), http.StatusOK)
	resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches/checkout", map[string]string{"name": "other", "revision": base.Revision}, &f.admin, nil), http.StatusOK)
	response := sourceUpload(f, &f.admin, "examples/branch.go", base.Revision, []byte("package wrongbranch\n"), created.Draft)
	assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, sourceTarget("examples/branch.go", created.Draft.ID), nil, &f.admin, nil).Code)
	other := f.current(t, &f.admin)
	resultJSON[syncDTO](t, f.request(http.MethodPost, testAPI+"/branches/checkout", map[string]string{"name": "main", "revision": other.Revision}, &f.admin, nil), http.StatusOK)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget("examples/branch.go", created.Draft.ID), nil, &f.admin, nil), data, created.Draft.Revision)
}

func TestRawSourceResavePreservesStaleBaseAndReturnsNewSavedDraftOnPublishConflict(t *testing.T) {
	f := newFixture(t, nil)
	alice := f.createUser(t, "source-alice", auth.RoleEditor, nil)
	bob := f.createUser(t, "source-bob", auth.RoleEditor, nil)
	base := f.current(t, &alice)
	created := resultJSON[saveResult](t, sourceUpload(f, &alice, "examples/change.go", base.Revision, []byte("package first\n"), nil), http.StatusOK)
	require.NotNil(t, created.Draft)
	changed := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]any{"content": "# New published revision", "revision": base.Revision, "publish": true}, &bob, nil), http.StatusOK)
	require.True(t, changed.Published)
	f.handler.(*handler).opts.Publishing = func(_ context.Context) (PublishingPolicy, error) {
		return PublishingPolicy{AutoPublish: true}, nil
	}
	response := sourceUpload(f, &alice, "examples/change.go", base.Revision, []byte("package retained\n"), created.Draft)
	failure := resultJSON[struct {
		Error errorDetail `json:"error"`
	}](t, response, http.StatusConflict)
	require.NotNil(t, failure.Error.Draft)
	assert.Equal(t, created.Draft.ID, failure.Error.Draft.ID)
	assert.NotEqual(t, created.Draft.Revision, failure.Error.Draft.Revision)
	assert.Equal(t, base.Revision, failure.Error.Draft.BaseRevision)
	assert.Equal(t, changed.Revision, failure.Error.CurrentRevision)
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget("examples/change.go", created.Draft.ID), nil, &alice, nil), []byte("package retained\n"), failure.Error.Draft.Revision)
	assert.Equal(t, http.StatusConflict, sourceUpload(f, &alice, "examples/change.go", changed.Revision, []byte("implicit rebase"), failure.Error.Draft).Code)
	assert.Equal(t, "# New published revision", f.current(t, &alice).Content)
}
