package apiweb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registrationSeed(t *testing.T, f *fixture, name string, data []byte) {
	t.Helper()
	base := f.current(t, &f.admin)
	saved := resultJSON[saveResult](t, sourceUpload(f, &f.admin, name, base.Revision, data, nil), http.StatusOK)
	f.publish(t, saved.Draft, &f.admin)
}

func registrationInput(t *testing.T, f *fixture, name, kind string) map[string]string {
	t.Helper()
	response := f.request(http.MethodGet, sourceTarget(name, ""), nil, &f.admin, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	digest := sha256.Sum256(response.Body.Bytes())
	require.Equal(t, hex.EncodeToString(digest[:]), response.Header().Get("X-Source-Revision"))
	project := strings.Trim(response.Header().Get("ETag"), `"`)
	require.True(t, revision(project))
	return map[string]string{"path": name, "revision": project, "sourceRevision": response.Header().Get("X-Source-Revision"), "title": "Reviewed source", "kind": kind}
}

func TestSourceRegistrationDigestMatchesExactLiveGETAndHEADBytes(t *testing.T) {
	f := newFixture(t, nil)
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"examples/review.txt", []byte("\ufeff# Reviewed\r\n\r\nExact <>& bytes.\r\n")},
		{"examples/empty.txt", []byte{}},
		{"files/attachment.pdf", []byte{0, 255, 7, 10}},
	} {
		registrationSeed(t, f, test.name, test.data)
		base := f.current(t, &f.admin)
		digest := sha256.Sum256(test.data)
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			response := f.request(method, sourceTarget(test.name, ""), nil, &f.admin, map[string]string{"Range": "bytes=0-1", "If-None-Match": `"` + base.Revision + `"`})
			assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.Equal(t, hex.EncodeToString(digest[:]), response.Header().Get("X-Source-Revision"))
			assert.Equal(t, `"`+base.Revision+`"`, response.Header().Get("ETag"))
			assert.Equal(t, "application/octet-stream", response.Header().Get("Content-Type"))
			assert.Contains(t, response.Header().Get("Content-Disposition"), "attachment;")
			assert.Contains(t, response.Header().Get("Content-Security-Policy"), "sandbox")
			assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			if method == http.MethodHead {
				assert.Zero(t, response.Body.Len())
			} else {
				assert.True(t, bytes.Equal(test.data, response.Body.Bytes()))
			}
		}
	}
	unauthorized := f.request(http.MethodGet, sourceTarget("examples/review.txt", ""), nil, nil, nil)
	assert.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	assert.Empty(t, unauthorized.Header().Get("X-Source-Revision"))
}

func TestSourceRegistrationUsesReviewedBytesAndExplicitPublicationWithoutRewritingSource(t *testing.T) {
	automatic := false
	f := newFixture(t, func(options *Options) {
		options.Publishing = func(context.Context) (PublishingPolicy, error) { return PublishingPolicy{AutoPublish: automatic}, nil }
	})
	name, source := "examples/reviewed.txt", []byte("\ufeff# Reviewed document\r\n\r\nOriginal source <>&.\r\n")
	registrationSeed(t, f, name, source)
	file := filepath.Join(f.root, filepath.FromSlash(name))
	stamp := time.Unix(1700000000, 123456789)
	require.NoError(t, os.Chtimes(file, stamp, stamp))
	before, err := os.Stat(file)
	require.NoError(t, err)
	input := registrationInput(t, f, name, "markdown")
	automatic = true
	created := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/documents/register", input, &f.admin, nil), http.StatusCreated)
	require.NotNil(t, created.Document)
	require.NotNil(t, created.Draft)
	assert.False(t, created.Published)
	assert.Equal(t, input["revision"], created.Revision)
	assert.Equal(t, "examples/reviewed.html", created.Document.OutputPath)
	assert.Equal(t, name, created.Document.Path)
	assert.Equal(t, "markdown", created.Document.Kind)
	assert.Equal(t, input["title"], created.Document.Title)
	require.Regexp(t, `^[a-z0-9][a-z0-9._-]{0,127}$`, created.Document.ID)
	opened := resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/"+created.Document.ID, nil, &f.admin, nil), http.StatusOK)
	assert.Equal(t, string(source), opened.Content)
	assert.Equal(t, created.Draft.ID, opened.DraftID)
	assert.Equal(t, created.Draft.Revision, opened.DraftRevision)
	preview := resultJSON[Preview](t, f.request(http.MethodPost, testAPI+"/preview", map[string]string{"documentId": opened.ID, "content": opened.Content}, &f.admin, nil), http.StatusOK)
	assert.Contains(t, preview.HTML, "Original source")
	publishedCatalog := resultJSON[documentCatalog](t, f.request(http.MethodGet, testAPI+"/documents?published=true", nil, &f.admin, nil), http.StatusOK)
	for _, document := range publishedCatalog.Documents {
		assert.NotEqual(t, created.Document.ID, document.ID)
	}
	stagedInfo, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), stagedInfo.ModTime())
	assert.Equal(t, input["revision"], f.current(t, &f.admin).Revision)
	published := f.publish(t, created.Draft, &f.admin)
	assert.True(t, published.Published)
	after, err := os.Stat(file)
	require.NoError(t, err)
	actual, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, source, actual)
	assert.Equal(t, before.ModTime(), after.ModTime())
	assert.Equal(t, before.Mode(), after.Mode())
	input = registrationInput(t, f, name, "markdown")
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/documents/register", input, &f.admin, nil).Code)
}

func TestSourceRegistrationValidatesReviewAndMetadataBeforeCreatingDrafts(t *testing.T) {
	f := newFixture(t, nil)
	name := "private/registration.txt"
	registrationSeed(t, f, name, []byte("private reviewed source\n"))
	registrationSeed(t, f, "files/registration.pdf", []byte{0, 255, 1})
	input := registrationInput(t, f, name, "markdown")
	for _, test := range []struct {
		field, value string
		status       int
	}{
		{"sourceRevision", "", 400},
		{"sourceRevision", strings.Repeat("A", 64), 400},
		{"sourceRevision", strings.Repeat("0", 64), 409},
		{"revision", strings.Repeat("0", 64), 409},
		{"title", "", 400},
		{"title", strings.Repeat("x", 257), 400},
		{"kind", "javascript", 400},
		{"id", "../escape", 400},
		{"id", strings.Repeat("x", 129), 400},
		{"outputPath", "UPPER.html", 400},
		{"id", "home", 409},
		{"outputPath", "index.html", 409},
		{"outputPath", "index.html/child.html", 409},
	} {
		request := maps.Clone(input)
		request[test.field] = test.value
		response := f.request(http.MethodPost, testAPI+"/documents/register", request, &f.admin, nil)
		assert.Equal(t, test.status, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "private reviewed source")
	}
	for _, field := range []string{"publish", "content"} {
		request := make(map[string]any, len(input)+1)
		for key, value := range input {
			request[key] = value
		}
		request[field] = "must not be accepted"
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/documents/register", request, &f.admin, nil).Code)
	}
	for _, body := range [][]byte{[]byte(`{}`), []byte(`{"path":null}`), []byte(`{"path":"a.txt","path":"b.txt"}`)} {
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/documents/register", body, &f.admin, nil).Code)
	}
	oversize := []byte(`{"path":"` + strings.Repeat("x", 32<<10) + `"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, f.request(http.MethodPost, testAPI+"/documents/register", oversize, &f.admin, nil).Code)
	assert.Equal(t, http.StatusUnsupportedMediaType, f.request(http.MethodPost, testAPI+"/documents/register", []byte(`{}`), &f.admin, map[string]string{"Content-Type": "application/octet-stream"}).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/documents/register?extra=true", input, &f.admin, nil).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, f.request(http.MethodGet, testAPI+"/documents/register", nil, &f.admin, nil).Code)
	binary := registrationInput(t, f, "files/registration.pdf", "markdown")
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/documents/register", binary, &f.admin, nil).Code)
	viewer := f.createUser(t, "registration-viewer", auth.RoleViewer, nil)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/documents/register", input, &viewer, nil).Code)
	scoped := f.createUser(t, "registration-scoped", auth.RoleEditor, &auth.Grants{Read: []string{"public"}, Write: []string{"public"}})
	denied := f.request(http.MethodPost, testAPI+"/documents/register", input, &scoped, nil)
	assert.Equal(t, http.StatusNotFound, denied.Code, denied.Body.String())
	assert.NotContains(t, denied.Body.String(), name)
	assert.NotContains(t, denied.Body.String(), input["sourceRevision"])
	assert.Empty(t, denied.Header().Get("X-Source-Revision"))
	readOnly := f.createUser(t, "registration-readonly", auth.RoleEditor, &auth.Grants{Read: []string{"private"}, Write: []string{}})
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/documents/register", input, &readOnly, nil).Code)
	drafts, err := f.content.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
	assert.Equal(t, input["revision"], f.current(t, &f.admin).Revision)
}

func TestSourceRegistrationKeepsFirstDraftAndRejectsPendingMetadataAliases(t *testing.T) {
	f := newFixture(t, nil)
	registrationSeed(t, f, "examples/first.txt", []byte("first source\n"))
	registrationSeed(t, f, "examples/second.txt", []byte("second source\n"))
	first := registrationInput(t, f, "examples/first.txt", "markdown")
	first["id"], first["outputPath"] = "pending-page", "pages/pending.html"
	second := registrationInput(t, f, "examples/second.txt", "markdown")
	responses := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			responses <- f.request(http.MethodPost, testAPI+"/documents/register", first, &f.admin, nil)
		}()
	}
	close(start)
	var retained *draftDTO
	for range 2 {
		response := <-responses
		if response.Code == http.StatusCreated {
			result := resultJSON[saveResult](t, response, http.StatusCreated)
			require.NotNil(t, result.Draft)
			require.Nil(t, retained, "concurrent registration must create exactly one draft")
			retained = result.Draft
		} else {
			assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "pending-page")
		}
	}
	require.NotNil(t, retained)
	before, err := f.content.ReadDraft(t.Context(), retained.ID)
	require.NoError(t, err)
	for _, test := range []map[string]string{
		{"id": "pending-page", "outputPath": "pages/another.html"},
		{"id": "another-page", "outputPath": "pages/pending.html"},
		{"id": "another-page", "outputPath": "pages/pending.html/child.html"},
	} {
		request := maps.Clone(second)
		for key, value := range test {
			request[key] = value
		}
		response := f.request(http.MethodPost, testAPI+"/documents/register", request, &f.admin, nil)
		assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), retained.ID)
	}
	after, err := f.content.ReadDraft(t.Context(), retained.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	drafts, err := f.content.Drafts(t.Context())
	require.NoError(t, err)
	assert.Len(t, drafts, 1)
}

func TestSourceRegistrationDoesNotConsumePendingRawRestoreOrManifestDraft(t *testing.T) {
	for _, operation := range []string{"raw", "restore", "manifest", "move destination"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t, nil)
			name := "examples/pending.txt"
			registrationSeed(t, f, name, []byte("original source\n"))
			registrationSeed(t, f, "examples/move-from.txt", []byte("move source\n"))
			input := registrationInput(t, f, name, "markdown")
			var pending *draftDTO
			switch operation {
			case "raw":
				pending = resultJSON[saveResult](t, sourceUpload(f, &f.admin, name, input["revision"], []byte("unsent raw draft"), nil), http.StatusOK).Draft
			case "restore":
				history := workflowHistory(t, f, &f.admin, name)
				require.NotEmpty(t, history.Revisions)
				pending = resultJSON[saveResult](t, workflowRestore(f, &f.admin, name, history.Revisions[0].ID, history.Revision), http.StatusOK).Draft
			case "manifest":
				manifest := f.request(http.MethodGet, sourceTarget("docs.json", ""), nil, &f.admin, nil)
				require.Equal(t, http.StatusOK, manifest.Code)
				pending = resultJSON[saveResult](t, sourceUpload(f, &f.admin, "docs.json", input["revision"], manifest.Body.Bytes(), nil), http.StatusOK).Draft
			case "move destination":
				pending = resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/files/move", map[string]string{"path": "examples/move-from.txt", "to": name, "revision": input["revision"]}, &f.admin, nil), http.StatusOK).Draft
			}
			require.NotNil(t, pending)
			before, err := f.content.ReadDraft(t.Context(), pending.ID)
			require.NoError(t, err)
			response := f.request(http.MethodPost, testAPI+"/documents/register", input, &f.admin, nil)
			assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), pending.ID)
			assert.NotContains(t, response.Body.String(), pending.Revision)
			after, err := f.content.ReadDraft(t.Context(), pending.ID)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			assert.Equal(t, input["revision"], f.current(t, &f.admin).Revision)
		})
	}
}

func TestSourceRegistrationAndLiveDigestRecheckRevokedSessionAfterWaiting(t *testing.T) {
	for _, action := range []string{"download", "register"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t, nil)
			var armed atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			backend := &catalogReadGate{Backend: content.NewLocalBackend(), entered: entered, release: release}
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			replaceContentForHTTPTest(t, f, content.Config{Storage: backend, AuthorizeSource: func(ctx context.Context, actor content.Actor, name string, access content.SourceAccess) error {
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
			name, source := "examples/queued-registration.txt", []byte("keep source private after revocation\n")
			registrationSeed(t, f, name, source)
			input := registrationInput(t, f, name, "markdown")
			editor := f.createUser(t, "queued-registration-owner", auth.RoleEditor, nil)
			if action == "download" {
				backend.armed.Store(true)
			} else {
				armed.Store(true)
			}
			completed := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				if action == "download" {
					completed <- f.request(http.MethodGet, sourceTarget(name, ""), nil, &editor, nil)
				} else {
					completed <- f.request(http.MethodPost, testAPI+"/documents/register", input, &editor, nil)
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("registration or live source read did not wait for content")
			}
			require.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/session", nil, &editor, nil).Code)
			close(release)
			select {
			case response := <-completed:
				assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
				for _, hidden := range []string{name, string(source), input["revision"], input["sourceRevision"], `"draft"`, `"document"`} {
					assert.NotContains(t, response.Body.String(), hidden)
				}
				assert.Empty(t, response.Header().Get("X-Source-Revision"))
				assert.Empty(t, response.Header().Get("ETag"))
			case <-time.After(5 * time.Second):
				t.Fatal("registration or live source read did not finish after content was released")
			}
			drafts, err := f.content.Drafts(t.Context())
			require.NoError(t, err)
			assert.Empty(t, drafts)
			assert.Equal(t, input["revision"], f.current(t, &f.admin).Revision)
		})
	}
}
