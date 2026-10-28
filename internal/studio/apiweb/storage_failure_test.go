package apiweb

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type statusFailureBackend struct {
	content.Backend
	calls  int
	failAt int
}

func (b *statusFailureBackend) Status(ctx context.Context) (content.SyncStatus, error) {
	b.calls++
	if b.calls == b.failAt {
		return content.SyncStatus{}, errors.New("private storage failure detail")
	}
	return b.Backend.Status(ctx)
}

func replaceContentForHTTPTest(t *testing.T, f *fixture, config content.Config) {
	t.Helper()
	require.NoError(t, f.content.Close())
	config.Root, config.StateDir, config.OutputDir = f.root, filepath.Join(filepath.Dir(f.root), "state"), f.output
	config.Compile = func(ctx context.Context, manifest, output string) error {
		return site.BuildContext(ctx, manifest, output, site.Options{})
	}
	if config.AuthorizeSource == nil {
		config.AuthorizeSource = authorizeSourceForHTTPTest
	}
	service, err := content.New(config)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, service.Close()) })
	f.content, f.handler.(*handler).opts.Content = service, service
}

func TestStorageFailureBetweenMetadataReadsDoesNotReturnPartialResponses(t *testing.T) {
	f := newFixture(t, nil)
	backend := &statusFailureBackend{Backend: content.NewLocalBackend()}
	replaceContentForHTTPTest(t, f, content.Config{Storage: backend})
	base := f.current(t, &f.admin)
	cases := []struct {
		name   string
		target string
		failAt int
	}{
		{"document listing status", "/documents", 2},
		{"current document status", "/documents/home", 2},
		{"source read status", "/documents/home?published=true", 2},
		{"history status", "/documents/home/history", 3},
		{"synchronization project", "/sync/status", 2},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			backend.calls, backend.failAt = 0, item.failAt
			response := f.request(http.MethodGet, testAPI+item.target, nil, &f.admin, nil)
			assert.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "private storage failure")
			assert.NotContains(t, response.Body.String(), base.Content)
		})
	}
	backend.calls, backend.failAt = 0, 0
	assert.Equal(t, base.Content, f.current(t, &f.admin).Content)
}

func TestUnavailableDraftDirectoryKeepsPublishedSourcesReadable(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	directory := filepath.Join(filepath.Dir(f.root), "state", "drafts")
	require.NoError(t, os.Rename(directory, directory+"-unavailable"))
	for _, target := range []string{"/documents", "/documents/home", "/drafts"} {
		response := f.request(http.MethodGet, testAPI+target, nil, &f.admin, nil)
		assert.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), directory)
	}
	response := f.request(http.MethodPost, testAPI+"/documents", map[string]string{"path": "new.md", "title": "New", "kind": "markdown", "content": "# New", "revision": base.Revision}, &f.admin, nil)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, base.Content, f.current(t, &f.admin).Content)
	require.NoError(t, os.Rename(directory+"-unavailable", directory))
	assert.Equal(t, http.StatusOK, f.request(http.MethodGet, testAPI+"/documents", nil, &f.admin, nil).Code)
}

func TestPublicationValidationFailuresRetainRecoverableDraftMetadata(t *testing.T) {
	f := newFixture(t, nil)
	var validationError error
	replaceContentForHTTPTest(t, f, content.Config{Validate: func(context.Context, string, []content.Change, content.Actor) error { return validationError }})
	base := f.current(t, &f.admin)
	var saved *draftDTO
	for _, item := range []struct {
		name   string
		err    error
		status int
	}{
		{"read permission revoked", auth.ErrForbidden, http.StatusForbidden},
		{"content resource limit", content.ErrLimit, http.StatusRequestEntityTooLarge},
		{"request canceled", context.Canceled, http.StatusRequestTimeout},
		{"request deadline", context.DeadlineExceeded, http.StatusRequestTimeout},
		{"internal validator failure", errors.New("private validation failure detail"), http.StatusInternalServerError},
	} {
		t.Run(item.name, func(t *testing.T) {
			validationError = item.err
			input := map[string]any{"content": "# Kept draft", "revision": base.Revision, "publish": true}
			if saved != nil {
				input["draftId"], input["draftRevision"], input["baseRevision"] = saved.ID, saved.Revision, saved.BaseRevision
			}
			response := f.request(http.MethodPut, testAPI+"/documents/home", input, &f.admin, nil)
			failure := resultJSON[struct {
				Error errorDetail `json:"error"`
			}](t, response, item.status)
			require.NotNil(t, failure.Error.Draft)
			require.NotNil(t, failure.Error.Document)
			saved = failure.Error.Draft
			assert.Equal(t, "# Kept draft", failure.Error.Document.Content)
			assert.NotContains(t, response.Body.String(), "private validation")
			assert.Equal(t, base.Content, f.current(t, &f.admin).Content)
		})
	}
	validationError = nil
	published := f.publish(t, saved, &f.admin)
	assert.True(t, published.Published)
	assert.Equal(t, "# Kept draft", f.current(t, &f.admin).Content)
}

func TestSessionRevokedDuringValidationCannotPublish(t *testing.T) {
	f := newFixture(t, nil)
	base := f.current(t, &f.admin)
	called := false
	replaceContentForHTTPTest(t, f, content.Config{Validate: func(ctx context.Context, _ string, _ []content.Change, _ content.Actor) error {
		called = true
		assert.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/session", nil, &f.admin, nil).Code)
		user, err := Principal(ctx)
		assert.Nil(t, user)
		assert.ErrorIs(t, err, auth.ErrUnauthorized)
		return err
	}})
	response := f.request(http.MethodPut, testAPI+"/documents/home", map[string]any{"content": "# Must stay a draft", "revision": base.Revision, "publish": true}, &f.admin, nil)
	failure := resultJSON[struct {
		Error errorDetail `json:"error"`
	}](t, response, http.StatusUnauthorized)
	assert.True(t, called)
	require.NotNil(t, failure.Error.Draft)
	f.admin = f.login(t, "owner", testPassword)
	assert.Equal(t, base.Content, f.current(t, &f.admin).Content)
	assert.Equal(t, "# Must stay a draft", resultJSON[Document](t, f.request(http.MethodGet, testAPI+"/documents/home", nil, &f.admin, nil), http.StatusOK).Content)
}
