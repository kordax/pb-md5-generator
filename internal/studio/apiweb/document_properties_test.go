package apiweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const propertiesAPI = testBase + "/_docs/api/v1"

type propertiesDraft struct {
	ID           string `json:"id"`
	Revision     string `json:"revision"`
	BaseRevision string `json:"baseRevision"`
}

type propertiesSaveResult struct {
	Document  Document        `json:"document"`
	Draft     propertiesDraft `json:"draft"`
	Published bool            `json:"published"`
}

type propertiesDocumentDetail struct {
	Document
}

func propertiesMetadata(document Document) [5]string {
	return [5]string{document.ID, document.Path, document.Title, document.Kind, document.OutputPath}
}

func propertiesDecode(t *testing.T, response *httptest.ResponseRecorder, status int, target any) {
	t.Helper()
	require.Equal(t, status, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), target))
}

func propertiesPublishedDocument(t *testing.T, fixture *fixture) Document {
	t.Helper()
	var catalog struct {
		Documents []Document `json:"documents"`
	}
	propertiesDecode(t, fixture.request(http.MethodGet, propertiesAPI+"/documents?published=true", nil, &fixture.admin, nil), http.StatusOK, &catalog)
	require.NotEmpty(t, catalog.Documents)
	return catalog.Documents[0]
}

func propertiesSecondaryDocument(t *testing.T, fixture *fixture) Document {
	t.Helper()
	var created propertiesSaveResult
	propertiesDecode(t, fixture.request(http.MethodPost, propertiesAPI+"/documents", map[string]any{
		"path": "guide.md", "title": "Guide", "kind": "markdown", "content": "# Published guide\n", "revision": propertiesRevision(t, fixture),
	}, &fixture.admin, nil), http.StatusCreated, &created)
	require.NotEmpty(t, created.Draft.ID)
	var published propertiesSaveResult
	propertiesDecode(t, fixture.request(http.MethodPost, propertiesAPI+"/drafts/"+created.Draft.ID+"/publish", map[string]any{
		"revision": created.Draft.Revision,
	}, &fixture.admin, nil), http.StatusOK, &published)
	require.True(t, published.Published)
	return propertiesGet(t, fixture, created.Document.ID, true).Document
}

func propertiesRevision(t *testing.T, fixture *fixture) string {
	t.Helper()
	project, err := fixture.content.Project(t.Context())
	require.NoError(t, err)
	return project.Revision
}

func propertiesGet(t *testing.T, fixture *fixture, id string, published bool) propertiesDocumentDetail {
	t.Helper()
	target := propertiesAPI + "/documents/" + id
	if published {
		target += "?published=true"
	}
	var document propertiesDocumentDetail
	propertiesDecode(t, fixture.request(http.MethodGet, target, nil, &fixture.admin, nil), http.StatusOK, &document)
	return document
}

func propertiesValues(title, kind, output string) map[string]any {
	return map[string]any{"title": title, "kind": kind, "outputPath": output}
}

func TestDocumentPropertiesOverlayAndContentOnlyResavePreservePendingValues(t *testing.T) {
	fixture := newFixture(t, nil)
	published := propertiesSecondaryDocument(t, fixture)
	base := propertiesRevision(t, fixture)
	original := propertiesGet(t, fixture, published.ID, true)
	manifest, err := os.ReadFile(filepath.Join(fixture.root, "docs.json"))
	require.NoError(t, err)
	var first propertiesSaveResult
	propertiesDecode(t, fixture.request(http.MethodPut, propertiesAPI+"/documents/"+published.ID, map[string]any{
		"revision": base, "content": "# Pending content\n", "propertiesRevision": base,
		"properties": propertiesValues("Reviewed title", published.Kind, published.OutputPath),
	}, &fixture.admin, nil), http.StatusOK, &first)
	assert.False(t, first.Published)
	assert.Equal(t, "Reviewed title", first.Document.Title)
	assert.Equal(t, published.ID, first.Document.ID)
	assert.Equal(t, published.Path, first.Document.Path)
	pending := propertiesGet(t, fixture, published.ID, false)
	assert.Equal(t, propertiesMetadata(first.Document), propertiesMetadata(pending.Document))
	assert.Equal(t, "# Pending content\n", pending.Content)
	assert.Equal(t, first.Draft.ID, pending.DraftID)
	assert.Equal(t, first.Draft.Revision, pending.DraftRevision)
	assert.Equal(t, base, pending.PublishedRevision)
	assert.Equal(t, propertiesMetadata(original.Document), propertiesMetadata(propertiesGet(t, fixture, published.ID, true).Document))
	var catalog struct {
		Documents []Document `json:"documents"`
	}
	propertiesDecode(t, fixture.request(http.MethodGet, propertiesAPI+"/documents", nil, &fixture.admin, nil), http.StatusOK, &catalog)
	require.Len(t, catalog.Documents, 2)
	var listed Document
	for _, candidate := range catalog.Documents {
		if candidate.ID == published.ID {
			listed = candidate
		}
	}
	assert.Equal(t, "Reviewed title", listed.Title)
	var second propertiesSaveResult
	propertiesDecode(t, fixture.request(http.MethodPut, propertiesAPI+"/documents/"+published.ID, map[string]any{
		"revision": first.Draft.BaseRevision, "draftId": first.Draft.ID, "draftRevision": first.Draft.Revision, "content": "# Revised content only\n",
	}, &fixture.admin, nil), http.StatusOK, &second)
	assert.Equal(t, first.Draft.ID, second.Draft.ID)
	assert.NotEqual(t, first.Draft.Revision, second.Draft.Revision)
	assert.Equal(t, propertiesMetadata(first.Document), propertiesMetadata(second.Document))
	var changed propertiesSaveResult
	propertiesDecode(t, fixture.request(http.MethodPut, propertiesAPI+"/documents/"+published.ID, map[string]any{
		"revision": second.Draft.BaseRevision, "draftId": second.Draft.ID, "draftRevision": second.Draft.Revision,
		"content": "<h1>HTML replacement</h1><p>**literal text**</p>", "propertiesRevision": base,
		"properties": propertiesValues("HTML title", "html", "reviewed/document.html"),
	}, &fixture.admin, nil), http.StatusOK, &changed)
	assert.Equal(t, "html", changed.Document.Kind)
	assert.Equal(t, "reviewed/document.html", changed.Document.OutputPath)
	assert.Equal(t, published.Path, changed.Document.Path)
	assert.Equal(t, propertiesMetadata(changed.Document), propertiesMetadata(propertiesGet(t, fixture, published.ID, false).Document))
	liveManifest, err := os.ReadFile(filepath.Join(fixture.root, "docs.json"))
	require.NoError(t, err)
	assert.Equal(t, manifest, liveManifest, "staging properties must preserve published navigation and metadata")
	assert.Equal(t, original.Content, propertiesGet(t, fixture, published.ID, true).Content)
}

func TestDocumentPropertiesSeparateReviewedCASAndExplicitRebase(t *testing.T) {
	autoPublish := false
	fixture := newFixture(t, func(options *Options) {
		options.Publishing = func(context.Context) (PublishingPolicy, error) {
			return PublishingPolicy{AutoPublish: autoPublish}, nil
		}
	})
	published := propertiesSecondaryDocument(t, fixture)
	base := propertiesRevision(t, fixture)
	var saved propertiesSaveResult
	propertiesDecode(t, fixture.request(http.MethodPut, propertiesAPI+"/documents/"+published.ID, map[string]any{
		"revision": base, "content": "# Retained local text\n", "propertiesRevision": base,
		"properties": propertiesValues("Pending local title", published.Kind, published.OutputPath),
	}, &fixture.admin, nil), http.StatusOK, &saved)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.root, "unrelated.txt"), []byte("concurrent project change\n"), 0o600))
	latest := propertiesRevision(t, fixture)
	require.NotEqual(t, base, latest)
	stale := fixture.request(http.MethodPut, propertiesAPI+"/documents/"+published.ID, map[string]any{
		"revision": saved.Draft.BaseRevision, "draftId": saved.Draft.ID, "draftRevision": saved.Draft.Revision,
		"content": "must not replace local text", "propertiesRevision": base,
		"properties": propertiesValues("Unreviewed title", published.Kind, published.OutputPath),
	}, &fixture.admin, nil)
	assert.Equal(t, http.StatusConflict, stale.Code)
	retained := propertiesGet(t, fixture, published.ID, false)
	assert.Equal(t, saved.Draft.Revision, retained.DraftRevision)
	assert.Equal(t, "# Retained local text\n", retained.Content)
	assert.Equal(t, "Pending local title", retained.Title)
	assert.Equal(t, latest, retained.PublishedRevision)
	autoPublish = true
	var rebased propertiesSaveResult
	propertiesDecode(t, fixture.request(http.MethodPost, propertiesAPI+"/documents/"+published.ID+"/rebase", map[string]any{
		"revision": latest, "draftId": saved.Draft.ID, "draftRevision": saved.Draft.Revision,
		"content": "# Explicitly merged text\n", "propertiesRevision": latest,
		"properties": propertiesValues("Reviewed merged title", "markdown", "reviewed/merged.html"),
	}, &fixture.admin, nil), http.StatusOK, &rebased)
	assert.False(t, rebased.Published)
	assert.Equal(t, saved.Draft.ID, rebased.Draft.ID)
	assert.Equal(t, latest, rebased.Draft.BaseRevision)
	assert.Equal(t, "Reviewed merged title", rebased.Document.Title)
	assert.Equal(t, "reviewed/merged.html", rebased.Document.OutputPath)
	assert.Equal(t, "# Explicitly merged text\n", propertiesGet(t, fixture, published.ID, false).Content)
	assert.Equal(t, propertiesMetadata(published), propertiesMetadata(propertiesGet(t, fixture, published.ID, true).Document))
}

func TestDocumentPropertiesPreviewUsesSuppliedMetadataWithoutPersisting(t *testing.T) {
	var previewed Document
	var previewSource string
	fixture := newFixture(t, func(options *Options) {
		options.Preview = func(_ context.Context, document Document, source []byte, _ *auth.User) (Preview, error) {
			previewed, previewSource = document, string(source)
			return Preview{HTML: "<p>Preview only</p>"}, nil
		}
	})
	published := propertiesPublishedDocument(t, fixture)
	before := propertiesRevision(t, fixture)
	response := fixture.request(http.MethodPost, propertiesAPI+"/preview", map[string]any{
		"documentId": published.ID, "content": "<h1>Unsaved HTML</h1>",
		"properties": propertiesValues("Unsaved title", "html", "reviewed/preview.html"),
	}, &fixture.admin, nil)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, published.ID, previewed.ID)
	assert.Equal(t, published.Path, previewed.Path)
	assert.Equal(t, "Unsaved title", previewed.Title)
	assert.Equal(t, "html", previewed.Kind)
	assert.Equal(t, "reviewed/preview.html", previewed.OutputPath)
	assert.Equal(t, "<h1>Unsaved HTML</h1>", previewSource)
	assert.Equal(t, before, propertiesRevision(t, fixture))
	drafts, err := fixture.content.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
	assert.Equal(t, propertiesMetadata(published), propertiesMetadata(propertiesGet(t, fixture, published.ID, true).Document))
}

func TestDocumentPropertiesStrictPayloadAndViewerCannotMutate(t *testing.T) {
	fixture := newFixture(t, nil)
	published := propertiesPublishedDocument(t, fixture)
	base := propertiesRevision(t, fixture)
	for _, test := range []struct {
		name   string
		modify func(map[string]any)
	}{
		{"missing content", func(body map[string]any) { delete(body, "content") }},
		{"missing properties revision", func(body map[string]any) { delete(body, "propertiesRevision") }},
		{"orphan properties revision", func(body map[string]any) { delete(body, "properties") }},
		{"null properties", func(body map[string]any) { body["properties"] = nil }},
		{"partial properties", func(body map[string]any) { body["properties"] = map[string]any{"title": "Incomplete"} }},
		{"unknown property", func(body map[string]any) { body["properties"].(map[string]any)["source"] = "private.md" }},
		{"empty title", func(body map[string]any) { body["properties"].(map[string]any)["title"] = "" }},
		{"unknown kind", func(body map[string]any) { body["properties"].(map[string]any)["kind"] = "javascript" }},
		{"unsafe output", func(body map[string]any) { body["properties"].(map[string]any)["outputPath"] = "../escape.html" }},
		{"invalid properties revision", func(body map[string]any) { body["propertiesRevision"] = "unreviewed" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{"revision": base, "content": "# Not saved\n", "propertiesRevision": base,
				"properties": propertiesValues("Review", published.Kind, published.OutputPath)}
			test.modify(body)
			response := fixture.request(http.MethodPut, propertiesAPI+"/documents/"+published.ID, body, &fixture.admin, nil)
			assert.Equal(t, http.StatusBadRequest, response.Code)
		})
	}
	assert.Equal(t, http.StatusBadRequest, fixture.request(http.MethodPost, propertiesAPI+"/preview", map[string]any{
		"documentId": published.ID, "content": "# Preview", "propertiesRevision": base,
		"properties": propertiesValues("Review", published.Kind, published.OutputPath),
	}, &fixture.admin, nil).Code)
	created := fixture.request(http.MethodPost, propertiesAPI+"/users", map[string]any{
		"username": "properties-viewer", "displayName": "Properties Viewer", "password": testPassword, "role": "viewer",
		"grants": map[string]any{"read": []string{"."}, "write": []string{}},
	}, &fixture.admin, nil)
	require.Equal(t, http.StatusCreated, created.Code)
	viewer := fixture.login(t, "properties-viewer", testPassword)
	denied := fixture.request(http.MethodPut, propertiesAPI+"/documents/"+published.ID, map[string]any{
		"revision": base, "content": "# Forbidden", "propertiesRevision": base,
		"properties": propertiesValues("Forbidden title", published.Kind, published.OutputPath),
	}, &viewer, nil)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Equal(t, base, propertiesRevision(t, fixture))
	drafts, err := fixture.content.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
}

func TestDocumentPropertiesFailedPublicationRetainsDraftAndPublishedNavigation(t *testing.T) {
	fixture := newFixture(t, nil)
	published := propertiesPublishedDocument(t, fixture)
	var stable propertiesSaveResult
	propertiesDecode(t, fixture.request(http.MethodPut, propertiesAPI+"/documents/"+published.ID, map[string]any{
		"revision": propertiesRevision(t, fixture), "content": "# Stable publication\n", "publish": true,
	}, &fixture.admin, nil), http.StatusOK, &stable)
	require.True(t, stable.Published)
	manifest, err := os.ReadFile(filepath.Join(fixture.root, "docs.json"))
	require.NoError(t, err)
	page, err := os.ReadFile(filepath.Join(fixture.output, filepath.FromSlash(published.OutputPath)))
	require.NoError(t, err)
	base := propertiesRevision(t, fixture)
	response := fixture.request(http.MethodPut, propertiesAPI+"/documents/"+published.ID, map[string]any{
		"revision": base, "content": "# INVALID_SOURCE retained text\n", "publish": true, "propertiesRevision": base,
		"properties": propertiesValues("Retained pending title", published.Kind, published.OutputPath),
	}, &fixture.admin, nil)
	var failure struct {
		Error struct {
			Code     string          `json:"code"`
			Draft    propertiesDraft `json:"draft"`
			Document Document        `json:"document"`
		} `json:"error"`
	}
	propertiesDecode(t, response, http.StatusUnprocessableEntity, &failure)
	assert.Equal(t, "invalid_content", failure.Error.Code)
	require.NotEmpty(t, failure.Error.Draft.ID)
	assert.Equal(t, "Retained pending title", failure.Error.Document.Title)
	pending := propertiesGet(t, fixture, published.ID, false)
	assert.Equal(t, failure.Error.Draft.ID, pending.DraftID)
	assert.Equal(t, failure.Error.Draft.Revision, pending.DraftRevision)
	assert.Equal(t, "# INVALID_SOURCE retained text\n", pending.Content)
	assert.Equal(t, propertiesMetadata(failure.Error.Document), propertiesMetadata(pending.Document))
	liveManifest, err := os.ReadFile(filepath.Join(fixture.root, "docs.json"))
	require.NoError(t, err)
	assert.Equal(t, manifest, liveManifest)
	livePage, err := os.ReadFile(filepath.Join(fixture.output, filepath.FromSlash(published.OutputPath)))
	require.NoError(t, err)
	assert.Equal(t, page, livePage)
	live := propertiesGet(t, fixture, published.ID, true)
	assert.Equal(t, propertiesMetadata(published), propertiesMetadata(live.Document))
	assert.Equal(t, "# Stable publication\n", live.Content)
}

func TestDocumentPropertiesRejectsExplicitInvalidRevisionWithoutProperties(t *testing.T) {
	fixture := newFixture(t, func(options *Options) {
		options.Publishing = func(context.Context) (PublishingPolicy, error) {
			return PublishingPolicy{AutoPublish: true}, nil
		}
	})
	published := propertiesPublishedDocument(t, fixture)
	base := propertiesRevision(t, fixture)
	sourcePath := filepath.Join(fixture.root, filepath.FromSlash(published.Path))
	before, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	for _, route := range []struct {
		name, method, suffix string
	}{
		{"save", http.MethodPut, ""},
		{"rebase", http.MethodPost, "/rebase"},
	} {
		for _, value := range []struct {
			name string
			data any
		}{
			{"empty", ""},
			{"null", nil},
		} {
			t.Run(route.name+"/"+value.name, func(t *testing.T) {
				response := fixture.request(route.method, propertiesAPI+"/documents/"+published.ID+route.suffix, map[string]any{
					"revision": base, "content": "# Malformed request must never publish\n", "propertiesRevision": value.data,
				}, &fixture.admin, nil)
				assert.Equal(t, http.StatusBadRequest, response.Code)
			})
		}
	}
	assert.Equal(t, base, propertiesRevision(t, fixture))
	after, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	drafts, err := fixture.content.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
}

type propertiesPublicationBackend struct {
	content.Backend
	change      func() error
	statusCalls int
	changed     bool
}

func (backend *propertiesPublicationBackend) Status(ctx context.Context) (content.SyncStatus, error) {
	status, err := backend.Backend.Status(ctx)
	if err != nil || backend.change == nil {
		return status, err
	}
	backend.statusCalls++
	// A published-only document read takes one Project snapshot and then Read.
	// This synchronous backend boundary changes the source before Read finishes,
	// without scheduling assumptions or modifying the HTTP implementation.
	if backend.statusCalls == 2 {
		change := backend.change
		backend.change = nil
		if err := change(); err != nil {
			return content.SyncStatus{}, err
		}
		backend.changed = true
	}
	return status, nil
}

func TestDocumentPropertiesPublishedReadRejectsMixedMetadataAndSourceRevision(t *testing.T) {
	fixture := newFixture(t, nil)
	backend := &propertiesPublicationBackend{Backend: content.NewLocalBackend()}
	replaceContentForHTTPTest(t, fixture, content.Config{Storage: backend})
	published := propertiesPublishedDocument(t, fixture)
	before := propertiesGet(t, fixture, published.ID, true)
	manifestPath := filepath.Join(fixture.root, "docs.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest site.Manifest
	require.NoError(t, json.Unmarshal(manifestBytes, &manifest))
	found := false
	for index := range manifest.Pages {
		if manifest.Pages[index].ID == published.ID {
			manifest.Pages[index].Title = "Concurrent reviewed title"
			manifest.Pages[index].Kind = "html"
			found = true
		}
	}
	require.True(t, found)
	updatedManifest, err := json.Marshal(manifest)
	require.NoError(t, err)
	const freshContent = "<h1>Concurrent source version B</h1>\n"
	backend.change = func() error {
		if err := os.WriteFile(manifestPath, updatedManifest, 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(fixture.root, filepath.FromSlash(published.Path)), []byte(freshContent), 0o600)
	}
	response := fixture.request(http.MethodGet, propertiesAPI+"/documents/"+published.ID+"?published=true", nil, &fixture.admin, nil)
	require.True(t, backend.changed, "the newer publication must land between metadata and source reads")
	var conflict struct {
		Error struct {
			Code            string `json:"code"`
			CurrentRevision string `json:"currentRevision"`
		} `json:"error"`
	}
	propertiesDecode(t, response, http.StatusConflict, &conflict)
	assert.Equal(t, "conflict", conflict.Error.Code)
	latest := propertiesRevision(t, fixture)
	assert.NotEqual(t, before.PublishedRevision, latest)
	assert.Equal(t, latest, conflict.Error.CurrentRevision)
	assert.NotContains(t, response.Body.String(), "Concurrent reviewed title")
	assert.NotContains(t, response.Body.String(), "Concurrent source version B")
	assert.Empty(t, response.Header().Get("ETag"))
	after := propertiesGet(t, fixture, published.ID, true)
	assert.Equal(t, published.ID, after.ID)
	assert.Equal(t, published.Path, after.Path)
	assert.Equal(t, "Concurrent reviewed title", after.Title)
	assert.Equal(t, "html", after.Kind)
	assert.Equal(t, freshContent, after.Content)
	assert.Equal(t, latest, after.PublishedRevision)
	assert.Equal(t, latest, after.Revision)
}
