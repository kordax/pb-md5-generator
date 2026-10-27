package apiweb

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstructorRejectsInvalidDependenciesAndHosting(t *testing.T) {
	f := newFixture(t, nil)
	original := f.handler.(*handler).opts
	cases := []struct {
		name   string
		modify func(*Options)
	}{
		{"authentication", func(options *Options) { options.Auth = nil }},
		{"content", func(options *Options) { options.Content = nil }},
		{"assets", func(options *Options) { options.Assets = nil }},
		{"contract", func(options *Options) { options.Contract = nil }},
		{"preview", func(options *Options) { options.Preview = nil }},
		{"appearance", func(options *Options) { options.Appearance = nil }},
		{"insecure remote", func(options *Options) { options.PublicURL = "http://docs.example/team" }},
		{"credentials", func(options *Options) { options.PublicURL = "https://account:placeholder@docs.example/team" }},
		{"query", func(options *Options) { options.PublicURL += "?" }},
		{"fragment", func(options *Options) { options.PublicURL += "#fragment" }},
		{"different prefix", func(options *Options) { options.BasePath = "/other" }},
		{"unclean prefix", func(options *Options) {
			options.BasePath, options.PublicURL = "/team/../other", testOrigin+"/team/../other"
		}},
		{"encoded prefix", func(options *Options) { options.PublicURL = testOrigin + "/%74eam" }},
		{"large contract", func(options *Options) { options.Contract = bytes.Repeat([]byte{'x'}, (1<<20)+1) }},
		{"invalid contract utf8", func(options *Options) { options.Contract = []byte{0xff} }},
		{"missing asset", func(options *Options) { options.Assets = fstest.MapFS{} }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			options := original
			item.modify(&options)
			result, err := New(options)
			assert.Nil(t, result)
			assert.Error(t, err)
		})
	}
	options := original
	options.BasePath = ""
	h, err := New(options)
	require.NoError(t, err)
	assert.Equal(t, testBase, h.(*handler).base)
	assets := fstest.MapFS{}
	for _, name := range assetNames {
		data, err := fs.ReadFile(original.Assets, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		require.NoError(t, err)
		assets[name] = &fstest.MapFile{Data: data}
	}
	assets["index.html"].Data = []byte("no nonce")
	options.Assets = assets
	_, err = New(options)
	assert.Error(t, err)
	assets["index.html"].Data = []byte(noncePlaceholder + noncePlaceholder)
	_, err = New(options)
	assert.Error(t, err)
}

func TestAppearanceAuthorizationAndExactRequestShapes(t *testing.T) {
	f := newFixture(t, nil)
	viewer := f.createUser(t, "boundaryreader", auth.RoleViewer, nil)
	assert.Equal(t, http.StatusOK, f.request(http.MethodGet, testAPI+"/settings", nil, &viewer, nil).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPut, testAPI+"/settings", map[string]string{}, &viewer, nil).Code)
	assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/themes", map[string]string{}, &viewer, nil).Code)
	assert.Equal(t, http.StatusOK, f.request(http.MethodPut, testAPI+"/settings", map[string]string{}, &f.admin, nil).Code)
	base := f.current(t, &f.admin)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/documents/home/rebase", map[string]any{"content": "x", "revision": base.Revision, "publish": false}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/sync", map[string]string{"action": "fetch", "revision": base.Revision, "name": ""}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/branches", map[string]string{"name": "new", "revision": base.Revision, "action": ""}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testAPI+"/documents/home?published=maybe", nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/documents/home/unknown", nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/unknown", nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/files", map[string]string{"path": "../outside", "revision": base.Revision}, &f.admin, nil).Code)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodDelete, testAPI+"/files/content?path=missing.md", nil, &f.admin, map[string]string{"If-Match": base.Revision}).Code)
}

func TestReaderRedirectsHTMLNavigationAndKeepsDataUnauthorized(t *testing.T) {
	f := newFixture(t, func(options *Options) {
		options.Reader = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := Principal(r.Context())
			if err != nil || user == nil {
				serviceError(w, auth.ErrUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		})
	})
	for _, target := range []string{testBase + "/", testBase + "/guide.html"} {
		response := f.request(http.MethodGet, target, nil, nil, map[string]string{"Accept": "text/html,application/xhtml+xml"})
		assert.Equal(t, http.StatusSeeOther, response.Code)
		assert.Equal(t, testBase+"/_docs/admin/", response.Header().Get("Location"))
	}
	for _, target := range []string{testBase + "/_docs/pages/page.json", testBase + "/_docs/assets/site.css", testAPI + "/documents"} {
		response := f.request(http.MethodGet, target, nil, nil, map[string]string{"Accept": "text/html"})
		assert.Equal(t, http.StatusUnauthorized, response.Code)
		assert.Empty(t, response.Header().Get("Location"))
	}
	assert.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, testBase+"/", nil, nil, nil).Code)
	assert.Equal(t, http.StatusOK, f.request(http.MethodGet, testBase+"/", nil, &f.admin, map[string]string{"Accept": "text/html"}).Code)
}

func TestPreviewBoundsCancellationAndPolicyErrors(t *testing.T) {
	mode := "large"
	f := newFixture(t, func(options *Options) {
		options.Preview = func(context.Context, Document, []byte, *auth.User) (Preview, error) {
			switch mode {
			case "large":
				return Preview{HTML: strings.Repeat("x", (16<<20)+1)}, nil
			case "large diagnostic":
				return Preview{Diagnostics: []Diagnostic{{Message: strings.Repeat("x", 4097)}}}, errors.New("source error")
			case "canceled":
				return Preview{}, context.Canceled
			case "forbidden":
				return Preview{}, auth.ErrForbidden
			default:
				return Preview{HTML: "<p>ready</p>"}, nil
			}
		}
	})
	request := func() *httptest.ResponseRecorder {
		return f.request(http.MethodPost, testAPI+"/preview", map[string]string{"documentId": "home", "content": "source"}, &f.admin, nil)
	}
	assert.Equal(t, http.StatusRequestEntityTooLarge, request().Code)
	mode = "large diagnostic"
	assert.Equal(t, http.StatusRequestEntityTooLarge, request().Code)
	mode = "canceled"
	assert.Equal(t, http.StatusRequestTimeout, request().Code)
	mode = "forbidden"
	assert.Equal(t, http.StatusForbidden, request().Code)
	mode = "success"
	assert.Equal(t, http.StatusOK, request().Code)
	f.handler.(*handler).opts.Publishing = func(context.Context) (PublishingPolicy, error) {
		return PublishingPolicy{}, errors.New("settings unavailable")
	}
	base := f.current(t, &f.admin)
	assert.Equal(t, http.StatusInternalServerError, f.request(http.MethodPut, testAPI+"/documents/home", map[string]string{"content": "x", "revision": base.Revision}, &f.admin, nil).Code)
}
