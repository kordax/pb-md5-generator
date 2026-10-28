package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReaderReadViewServesUncachedModelsWithReadOnlyContentState(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires enforced POSIX directory write permissions")
	}
	config, owner, _ := navigationRuntimeFixture(t)
	viewer := navigationScopedClient(t, owner, config, "read-view-scoped", "viewer", []string{"index.md", "public"}, []string{})
	root := filepath.Join(config.DataDir, "content")
	manifestPath := filepath.Join(root, "docs.json")
	manifest, err := readProjectManifest(root, manifestPath)
	require.NoError(t, err)
	assetName, assetBytes := "public/read-view.pdf", []byte("%PDF-1.4\nRead-view attachment bytes\n%%EOF\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(assetName)), assetBytes, 0o600))
	manifest.Assets = append(manifest.Assets, assetName)
	encoded, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, encoded, 0o600))
	var guide site.Page
	for _, page := range manifest.Pages {
		if page.ID == "guide" {
			guide = page
		}
	}
	require.NotEmpty(t, guide.Source)
	routes := []string{"/" + guide.Path, "/" + site.PageDataPath(guide), "/_docs/project.json", "/_docs/search.json", "/assets/" + assetName}
	clients := map[string]*runtimeClient{"owner": owner, "viewer": viewer}
	baseline := make(map[string]map[string][]byte)
	for _, role := range []string{"owner", "viewer"} {
		baseline[role] = make(map[string][]byte)
		for _, route := range routes {
			status, _, body := clients[role].request(t, http.MethodGet, route, nil)
			require.Equal(t, http.StatusOK, status)
			baseline[role][route] = bytes.Clone(body)
		}
	}
	assert.Contains(t, string(baseline["owner"][routes[0]]), "Hidden group title")
	assert.Equal(t, assetBytes, baseline["viewer"][routes[4]])
	state := filepath.Join(config.DataDir, "state", "content")
	info, err := os.Stat(state)
	require.NoError(t, err)
	entries, err := os.ReadDir(state)
	require.NoError(t, err)
	beforeNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		beforeNames = append(beforeNames, entry.Name())
	}
	require.NoError(t, os.Chmod(state, 0o500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(state, info.Mode().Perm())) })
	probe, err := os.CreateTemp(state, ".write-capability-")
	if err == nil {
		require.NoError(t, probe.Close())
		require.NoError(t, os.Remove(probe.Name()))
		t.Skip("filesystem did not enforce directory write removal")
	}
	require.True(t, os.IsPermission(err), "the previous snapshot View must not be able to create its temporary directory")
	// An unrelated managed source changes the project revision and invalidates
	// every warmed reader entry without changing any expected rendered bytes.
	require.NoError(t, os.WriteFile(filepath.Join(root, "public", "read-view-cache-buster.txt"), []byte("invalidate warm responses\n"), 0o600))
	for _, route := range routes {
		for _, role := range []string{"owner", "viewer"} {
			status, _, body := clients[role].request(t, http.MethodGet, route, nil)
			require.Equal(t, http.StatusOK, status)
			assert.Equal(t, baseline[role][route], body)
			if role == "viewer" {
				for _, hidden := range []string{"private-navigation", "Hidden group title", "Hidden group description", "Hidden leaf"} {
					assert.NotContains(t, string(body), hidden)
				}
			}
		}
	}
	entries, err = os.ReadDir(state)
	require.NoError(t, err)
	afterNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		afterNames = append(afterNames, entry.Name())
	}
	assert.Equal(t, beforeNames, afterNames)
}

func readViewDirectFixture(t *testing.T) (runtimeConfig, *content.Service, context.Context, *auth.User) {
	t.Helper()
	config := runtimeConfigForTest(t)
	require.NoError(t, Bootstrap(t.Context(), config, "owner", "Read-view owner", []byte(ownerTestPassword)))
	seedRuntimeSources(t, config, "# Read-view source marker\n")
	prepared, err := prepareConfig(config)
	require.NoError(t, err)
	resources := &runtimeResources{}
	t.Cleanup(func() { require.NoError(t, resources.close()) })
	authentication, err := newRuntimeAuth(prepared, resources)
	require.NoError(t, err)
	contents, err := newRuntimeContent(prepared, authentication, resources)
	require.NoError(t, err)
	var principalContext context.Context
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principalContext = r.Context()
		w.WriteHeader(http.StatusNoContent)
	})
	handler, err := apiweb.New(apiweb.Options{PublicURL: prepared.PublicURL, Auth: authentication, Content: contents,
		Assets: web.Assets(), Contract: APIContract(), Appearance: probe, Reader: probe,
		Preview: func(context.Context, apiweb.Document, []byte, *auth.User) (apiweb.Preview, error) {
			return apiweb.Preview{}, nil
		},
	})
	require.NoError(t, err)
	loginBody, err := json.Marshal(map[string]string{"username": "owner", "password": ownerTestPassword})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, prepared.PublicURL+"/_docs/api/v1/session", bytes.NewReader(loginBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", prepared.PublicURL)
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, request)
	require.Equal(t, http.StatusOK, login.Code)
	cookies := login.Result().Cookies()
	require.Len(t, cookies, 1)
	request = httptest.NewRequest(http.MethodGet, prepared.PublicURL+"/reader-context-probe", nil)
	request.AddCookie(cookies[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.NotNil(t, principalContext)
	owner, err := apiweb.Principal(principalContext)
	require.NoError(t, err)
	require.NotNil(t, owner)
	return prepared, contents, principalContext, owner
}

// Preserve the opaque authentication context key and its resolver's function
// type while letting the test model a freshly changed principal at resolution.
type readViewPrincipalContext struct {
	context.Context
	resolve func(context.Context) (*auth.User, error)
}

func (ctx readViewPrincipalContext) Value(key any) any {
	value := ctx.Context.Value(key)
	if value != nil {
		target := reflect.TypeOf(value)
		resolve := reflect.ValueOf(ctx.resolve)
		if target.Kind() == reflect.Func && resolve.Type().ConvertibleTo(target) {
			return resolve.Convert(target).Interface()
		}
	}
	return value
}

func TestReaderReadViewCanceledRequestCannotServeOrCacheContent(t *testing.T) {
	config, contents, principal, _ := readViewDirectFixture(t)
	ctx, cancel := context.WithCancel(principal)
	cancel()
	reader := newReader(config, nil, contents).(*cmsReader)
	request := httptest.NewRequest(http.MethodGet, config.PublicURL+"/index.html", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	reader.ServeHTTP(response, request)
	assert.NotEqual(t, http.StatusOK, response.Code)
	assert.NotContains(t, response.Body.String(), "Read-view source marker")
	assert.Empty(t, response.Header().Get("ETag"))
	assert.Empty(t, reader.cache)
	assert.Zero(t, reader.bytes)
	_, err := contents.Project(t.Context())
	require.NoError(t, err, "cancellation must release the content gate")
}

func TestReaderReadViewFinalPrincipalControlsResponseAndCache(t *testing.T) {
	config, contents, principal, owner := readViewDirectFixture(t)
	calls := 0
	baselineContext := readViewPrincipalContext{Context: principal, resolve: func(context.Context) (*auth.User, error) {
		calls++
		return owner, nil
	}}
	baselineReader := newReader(config, nil, contents).(*cmsReader)
	baseline := httptest.NewRecorder()
	baselineReader.ServeHTTP(baseline, httptest.NewRequest(http.MethodGet, config.PublicURL+"/index.html", nil).WithContext(baselineContext))
	require.Equal(t, http.StatusOK, baseline.Code)
	require.Contains(t, baseline.Body.String(), "Read-view source marker")
	require.GreaterOrEqual(t, calls, 3)
	lastResolution := calls // Calibrate the boundary rather than hard-code a call count.
	limited := *owner
	limited.Role = auth.RoleViewer
	require.NoError(t, json.Unmarshal([]byte(`{"grants":{"read":["public"],"write":[]}}`), &limited))
	for _, test := range []struct {
		name   string
		status int
		user   *auth.User
		err    error
	}{
		{"revoked", http.StatusUnauthorized, nil, auth.ErrUnauthorized},
		{"reduced grants", http.StatusServiceUnavailable, &limited, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolutions := 0
			ctx := readViewPrincipalContext{Context: principal, resolve: func(context.Context) (*auth.User, error) {
				resolutions++
				if resolutions == lastResolution {
					return test.user, test.err
				}
				return owner, nil
			}}
			reader := newReader(config, nil, contents).(*cmsReader)
			response := httptest.NewRecorder()
			reader.ServeHTTP(response, httptest.NewRequest(http.MethodGet, config.PublicURL+"/index.html", nil).WithContext(ctx))
			assert.Equal(t, lastResolution, resolutions)
			assert.Equal(t, test.status, response.Code)
			assert.NotContains(t, response.Body.String(), "Read-view source marker")
			assert.Empty(t, response.Header().Get("ETag"))
			assert.Empty(t, reader.cache)
			assert.Zero(t, reader.bytes)
			assert.False(t, strings.Contains(response.Body.String(), "Hidden"))
		})
	}
}
