package studio

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedReaderDirectorySources(t *testing.T, config Config) {
	t.Helper()
	seedRuntimeSources(t, config, "# Routing home\n")
	root := filepath.Join(config.DataDir, "content")
	data, err := os.ReadFile(filepath.Join(root, "docs.json"))
	require.NoError(t, err)
	var manifest site.Manifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	manifest.Pages = append(manifest.Pages,
		site.Page{ID: "directory", Title: "Public directory", Source: "public/directory.md", Path: "guide/index.html", Kind: "markdown"},
		site.Page{ID: "nested-directory", Title: "Nested directory", Source: "public/nested.md", Path: "guide/deep/index.html", Kind: "markdown"},
		site.Page{ID: "restricted-directory", Title: "Restricted directory", Source: "restricted.md", Path: "private/index.html", Kind: "markdown"},
		site.Page{ID: "mixed-directory", Title: "Directory with a private include", Source: "public/mixed.md", Path: "mixed/index.html", Kind: "markdown"},
	)
	data, err = json.Marshal(manifest)
	require.NoError(t, err)
	for name, source := range map[string][]byte{
		"docs.json":           data,
		"public/directory.md": []byte("# Directory guide\n\n[Nested](nested.md)\n"),
		"public/nested.md":    []byte("# Deep directory guide\n"),
		"restricted.md":       []byte("# Restricted directory\n\nDirectory private secret.\n"),
		"public/mixed.md":     []byte("# Mixed directory\n\n:::include source=\"../private.md\"\n:::\n"),
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), source, 0o600))
	}
}

func TestReaderCanonicalPrefixAndDirectoryRoutes(t *testing.T) {
	for _, prefix := range []string{"", "/manual"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			config := runtimeConfigForTest(t)
			config.PublicURL += prefix
			require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
			seedReaderDirectorySources(t, config)
			startRuntimeForTest(t, config)
			client := newRuntimeClient(t, config)
			client.login(t, "owner", ownerTestPassword)

			if prefix != "" {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					status, headers, body := client.request(t, method, "?q=a%2Fb&next=%2Fprivate%2F", nil)
					assert.Equal(t, http.StatusPermanentRedirect, status)
					assert.Equal(t, prefix+"/?q=a%2Fb&next=%2Fprivate%2F", headers.Get("Location"))
					assert.Equal(t, "private, no-store", headers.Get("Cache-Control"))
					assert.Equal(t, "Cookie", headers.Get("Vary"))
					if method == http.MethodHead {
						assert.Empty(t, body)
					}
				}
				status, headers, _ := client.request(t, http.MethodGet, "?", nil)
				assert.Equal(t, http.StatusPermanentRedirect, status)
				assert.Equal(t, prefix+"/?", headers.Get("Location"))
			}

			for _, test := range []struct {
				route string
				title string
			}{
				{route: "/", title: "Routing home"},
				{route: "/guide/", title: "Directory guide"},
				{route: "/guide/deep/", title: "Deep directory guide"},
			} {
				t.Run(test.route, func(t *testing.T) {
					status, headers, body := client.request(t, http.MethodGet, test.route, nil)
					require.Equal(t, http.StatusOK, status, "%s", body)
					assert.Contains(t, string(body), test.title)
					assert.Contains(t, headers.Get("Content-Type"), "text/html")
					indexStatus, indexHeaders, indexBody := client.request(t, http.MethodGet, test.route+"index.html", nil)
					assert.Equal(t, http.StatusOK, indexStatus)
					assert.Equal(t, body, indexBody)
					assert.Equal(t, headers.Get("ETag"), indexHeaders.Get("ETag"))
					headStatus, headHeaders, headBody := client.request(t, http.MethodHead, test.route, nil)
					assert.Equal(t, http.StatusOK, headStatus)
					assert.Empty(t, headBody)
					assert.Equal(t, headers.Get("ETag"), headHeaders.Get("ETag"))

					// Resolve the generated relative link exactly as a user agent does.
					styles := regexp.MustCompile(`href="([^"]*_docs/site\.css)"`).FindStringSubmatch(string(body))
					require.Len(t, styles, 2)
					base, err := url.Parse(client.url + test.route)
					require.NoError(t, err)
					resource, err := url.Parse(styles[1])
					require.NoError(t, err)
					resolved := base.ResolveReference(resource)
					assert.Equal(t, prefix+"/_docs/site.css", resolved.Path)
					resourceStatus, _, _ := client.request(t, http.MethodGet, strings.TrimPrefix(resolved.RequestURI(), prefix), nil)
					assert.Equal(t, http.StatusOK, resourceStatus)
				})
			}
			for _, route := range []string{"/guide", "/guide/deep"} {
				status, headers, _ := client.request(t, http.MethodGet, route+"?q=directory", nil)
				assert.Equal(t, http.StatusPermanentRedirect, status)
				assert.Equal(t, prefix+route+"/?q=directory", headers.Get("Location"))
			}
			status, _, _ := client.request(t, http.MethodGet, "/missing/", nil)
			assert.Equal(t, http.StatusNotFound, status)
		})
	}
}

func TestReaderDirectoryAliasesPreservePermissionsAndTraversalChecks(t *testing.T) {
	config := runtimeConfigForTest(t)
	config.PublicURL += "/manual"
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Owner", []byte(ownerTestPassword)))
	seedReaderDirectorySources(t, config)
	startRuntimeForTest(t, config)
	owner := newRuntimeClient(t, config)
	owner.login(t, "owner", ownerTestPassword)
	// Prime wider-scope cache entries before the scoped reader requests aliases.
	for _, route := range []string{"/guide/", "/private/", "/mixed/"} {
		status, _, _ := owner.request(t, http.MethodGet, route, nil)
		require.Equal(t, http.StatusOK, status)
	}
	status, _, body := owner.request(t, http.MethodPost, "/_docs/api/v1/users", map[string]any{
		"username": "directory-reader", "displayName": "Directory reader", "password": "Directory-reader-cedar-library-2026", "role": "viewer", "grants": map[string]any{"read": []string{"public"}, "write": []string{}},
	})
	require.Equal(t, http.StatusCreated, status)
	var created auth.User
	require.NoError(t, json.Unmarshal(body, &created))
	reader := newRuntimeClient(t, config)
	reader.login(t, "directory-reader", "Directory-reader-cedar-library-2026")
	for _, route := range []string{"/guide/", "/guide/index.html", "/guide/deep/", "/" + site.PageDataPath(site.Page{ID: "directory"})} {
		status, _, body := reader.request(t, http.MethodGet, route, nil)
		assert.Equal(t, http.StatusOK, status, "%s", body)
		assert.NotContains(t, string(body), "Directory private secret")
		assert.NotContains(t, string(body), "Secret body that must remain private")
	}
	status, headers, _ := reader.request(t, http.MethodGet, "/guide", nil)
	assert.Equal(t, http.StatusPermanentRedirect, status)
	assert.Equal(t, "/manual/guide/", headers.Get("Location"))
	for _, route := range []string{"/private", "/private/", "/private/index.html", "/mixed", "/mixed/", "/mixed/index.html", "/" + site.PageDataPath(site.Page{ID: "restricted-directory"}), "/" + site.PageDataPath(site.Page{ID: "mixed-directory"})} {
		status, headers, body := reader.request(t, http.MethodGet, route, nil)
		assert.Equal(t, http.StatusNotFound, status, "%s", route)
		assert.Empty(t, headers.Get("Location"), "%s", route)
		assert.NotContains(t, string(body), "Directory private secret")
		assert.NotContains(t, string(body), "Secret body that must remain private")
	}
	for _, route := range []string{"/guide/../private/", "/guide/%2e%2e/private/", "/guide//", "/guide/./", "/guide%2findex.html", "/guide%5cindex.html"} {
		status, _, _ := reader.request(t, http.MethodGet, route, nil)
		assert.Equal(t, http.StatusBadRequest, status, "%s", route)
	}
	for _, route := range []string{"/.git/", "/.git/config", "/guide/.private/"} {
		status, _, _ := reader.request(t, http.MethodGet, route, nil)
		assert.Equal(t, http.StatusNotFound, status, "%s", route)
	}
	rootClient := newRuntimeClient(t, config)
	rootClient.url = strings.TrimSuffix(config.PublicURL, "/manual")
	rootClient.cookie = reader.cookie
	for _, route := range []string{"/guide/", "/manualx/guide/"} {
		status, _, _ := rootClient.request(t, http.MethodGet, route, nil)
		assert.Equal(t, http.StatusNotFound, status)
	}
	status, _, _ = owner.request(t, http.MethodPatch, "/_docs/api/v1/users/"+created.ID, map[string]any{"grants": map[string]any{"read": []string{}, "write": []string{}}})
	require.Equal(t, http.StatusOK, status)
	for _, route := range []string{"/guide/", "/guide/index.html", "/guide"} {
		status, _, _ := reader.request(t, http.MethodGet, route, nil)
		assert.Equal(t, http.StatusUnauthorized, status)
	}
	reader.login(t, "directory-reader", "Directory-reader-cedar-library-2026")
	for _, route := range []string{"/guide/", "/guide/index.html", "/guide"} {
		status, _, _ := reader.request(t, http.MethodGet, route, nil)
		assert.Equal(t, http.StatusNotFound, status)
	}
}

func TestReaderDirectoryPathUsesCanonicalCacheIdentity(t *testing.T) {
	for _, prefix := range []string{"", "/manual"} {
		for _, directory := range []string{"guide", "guide/deep"} {
			alias, aliasOK := readerPath(prefix+"/"+directory+"/", prefix)
			canonical, canonicalOK := readerPath(prefix+"/"+directory+"/index.html", prefix)
			assert.True(t, aliasOK)
			assert.True(t, canonicalOK)
			assert.Equal(t, directory+"/index.html", alias)
			assert.Equal(t, canonical, alias)
		}
		for _, name := range []string{"//", "/guide//", "/guide/../", "/guide/./", "/guide/.private/", "/.git/", "/guide\\/", "/guide\x00/"} {
			_, ok := readerPath(prefix+name, prefix)
			assert.False(t, ok, "%s%s", prefix, name)
		}
	}
}
