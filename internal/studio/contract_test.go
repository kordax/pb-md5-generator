package studio

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestCMSContractValidatesWithoutExternalResolution(t *testing.T) {
	api, err := importer.OpenAPI(context.Background(), "api/openapi.yaml", APIContract(), func(string) ([]byte, error) {
		return nil, errors.New("the CMS contract must be self-contained")
	})
	require.NoError(t, err)
	require.NotNil(t, api)
	assert.Equal(t, 41, len(api.Operations))
	first := APIContract()
	first[0] = '!'
	assert.True(t, strings.HasPrefix(string(APIContract()), "openapi:"))
}

func TestCMSContractMutationsDeclareCSRFAndRevisionProtection(t *testing.T) {
	var contract struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(APIContract(), &contract))
	for path, item := range contract.Paths {
		for method, value := range item {
			if method != "post" && method != "put" && method != "patch" && method != "delete" {
				continue
			}
			operation, ok := value.(map[string]any)
			require.True(t, ok)
			if path == "/session" && method == "post" {
				security, exists := operation["security"]
				require.True(t, exists)
				assert.Empty(t, security, "login must be reachable before a session exists")
				continue
			}
			parameters, ok := operation["parameters"].([]any)
			require.True(t, ok, "%s %s declares CSRF protection", method, path)
			found := false
			for _, value := range parameters {
				parameter, valid := value.(map[string]any)
				require.True(t, valid)
				found = found || parameter["$ref"] == "#/components/parameters/CSRF"
			}
			assert.True(t, found, "%s %s declares CSRF protection", method, path)
		}
	}
	revisionOperations := []struct{ path, method string }{
		{"/documents", "post"},
		{"/documents/{id}", "put"},
		{"/documents/{id}", "delete"},
		{"/documents/{id}/move", "post"},
		{"/documents/{id}/restore", "post"},
		{"/documents/{id}/rebase", "post"},
		{"/drafts/{id}", "delete"},
		{"/drafts/{id}/publish", "post"},
		{"/files", "post"},
		{"/files/content", "put"},
		{"/files/content", "delete"},
		{"/files/move", "post"},
		{"/branches", "post"},
		{"/branches/checkout", "post"},
		{"/sync", "post"},
		{"/storage/configuration", "put"},
		{"/storage/remotes/{name}/check", "post"},
		{"/storage/remotes/{name}/push", "post"},
		{"/settings", "put"},
		{"/themes/{id}", "delete"},
	}
	for _, item := range revisionOperations {
		operation, ok := contract.Paths[item.path][item.method].(map[string]any)
		require.True(t, ok)
		parameters, ok := operation["parameters"].([]any)
		require.True(t, ok)
		header := false
		for _, value := range parameters {
			parameter, valid := value.(map[string]any)
			require.True(t, valid)
			header = header || parameter["$ref"] == "#/components/parameters/IfMatch" || parameter["$ref"] == "#/components/parameters/ThemeIfMatch"
		}
		if header {
			continue
		}
		body, ok := operation["requestBody"].(map[string]any)
		require.True(t, ok)
		content, ok := body["content"].(map[string]any)
		require.True(t, ok)
		media, ok := content["application/json"].(map[string]any)
		require.True(t, ok)
		schema, ok := media["schema"].(map[string]any)
		require.True(t, ok)
		if reference, exists := schema["$ref"].(string); exists {
			schema = contract.Components.Schemas[strings.TrimPrefix(reference, "#/components/schemas/")]
		}
		required, ok := schema["required"].([]any)
		require.True(t, ok)
		assert.Contains(t, required, "revision", "%s %s protects against stale writes", item.method, item.path)
	}
}

func contractObject(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	require.True(t, ok, "contract value is an object")
	return result
}

func TestCMSContractHTTPRoutesAndDefaultErrors(t *testing.T) {
	var root map[string]any
	require.NoError(t, yaml.Unmarshal(APIContract(), &root))
	paths := contractObject(t, root["paths"])
	servers, ok := root["servers"].([]any)
	require.True(t, ok)
	require.Len(t, servers, 1)
	assert.Equal(t, "./", contractObject(t, servers[0])["url"], "the HTTP contract preserves deployment prefixes")
	routes := map[string][]string{
		"/openapi.yaml":           {"get"},
		"/session":                {"get", "post", "delete"},
		"/users":                  {"get", "post"},
		"/users/{id}":             {"patch", "delete"},
		"/documents":              {"get", "post"},
		"/documents/{id}":         {"get", "put", "delete"},
		"/documents/{id}/rebase":  {"post"},
		"/documents/{id}/move":    {"post"},
		"/documents/{id}/history": {"get"},
		"/documents/{id}/restore": {"post"},
		"/drafts":                 {"get"},
		"/drafts/{id}":            {"delete"},
		"/drafts/{id}/publish":    {"post"},
		"/preview":                {"post"},
		"/files":                  {"get", "post"},
		"/files/content":          {"get", "put", "delete"},
		"/files/move":             {"post"},
		"/branches":               {"get", "post"},
		"/branches/checkout":      {"post"},
		"/sync/status":            {"get"},
		"/sync":                   {"post"},
		"/settings":               {"get", "put"},
		"/themes":                 {"get", "post"},
		"/themes/{id}":            {"delete"},
	}
	routes["/storage/configuration"] = []string{"get", "put"}
	routes["/storage/remotes/{name}/check"] = []string{"post"}
	routes["/storage/remotes/{name}/push"] = []string{"post"}
	require.Len(t, paths, len(routes))
	for path, methods := range routes {
		item := contractObject(t, paths[path])
		for _, method := range methods {
			operation := contractObject(t, item[method])
			responses := contractObject(t, operation["responses"])
			assert.Equal(t, "#/components/responses/Failure", contractObject(t, responses["default"])["$ref"], "%s %s has JSON errors for methods, media and busy-workspace failures", method, path)
			if method == "get" {
				continue
			}
			parameters, ok := operation["parameters"].([]any)
			require.True(t, ok)
			origin := false
			for _, parameter := range parameters {
				origin = origin || contractObject(t, parameter)["$ref"] == "#/components/parameters/Origin"
			}
			assert.True(t, origin, "%s %s requires a matching Origin", method, path)
		}
	}
	contract := contractObject(t, contractObject(t, paths["/openapi.yaml"])["get"])
	assert.NotContains(t, contract, "security", "the contract download inherits session authentication")
	components := contractObject(t, root["components"])
	failure := contractObject(t, contractObject(t, components["responses"])["Failure"])
	media := contractObject(t, contractObject(t, failure["content"])["application/json"])
	assert.Equal(t, "#/components/schemas/Error", contractObject(t, media["schema"])["$ref"])
	create := contractObject(t, contractObject(t, paths["/documents"])["post"])
	created := contractObject(t, contractObject(t, create["responses"])["201"])
	media = contractObject(t, contractObject(t, created["content"])["application/json"])
	assert.Equal(t, "#/components/schemas/SaveResult", contractObject(t, media["schema"])["$ref"])
	branch := contractObject(t, contractObject(t, paths["/branches"])["post"])
	assert.Contains(t, contractObject(t, branch["responses"]), "200")
	assert.NotContains(t, contractObject(t, branch["responses"]), "201")
}

func TestCMSStorageContractProtectsConnectionsAndIndependentRevisions(t *testing.T) {
	var root map[string]any
	require.NoError(t, yaml.Unmarshal(APIContract(), &root))
	paths := contractObject(t, root["paths"])
	components := contractObject(t, root["components"])
	schemas := contractObject(t, components["schemas"])
	for _, name := range []string{"StorageUpdate", "StorageRemoteCheck", "StorageRemotePush"} {
		schema := contractObject(t, schemas[name])
		assert.Equal(t, false, schema["additionalProperties"], name)
		required, ok := schema["required"].([]any)
		require.True(t, ok)
		assert.Contains(t, required, "revision", "%s protects connection settings", name)
		if name != "StorageRemoteCheck" {
			assert.Contains(t, required, "baseRevision", "%s also protects the project", name)
		}
	}
	update := contractObject(t, schemas["StorageUpdate"])
	properties := contractObject(t, update["properties"])
	assert.Equal(t, 8, contractObject(t, properties["remotes"])["maxItems"])
	remote := contractObject(t, schemas["StorageRemote"])
	remoteProperties := contractObject(t, remote["properties"])
	assert.Len(t, remoteProperties, 3, "connection credentials are kept outside the public contract")
	assert.ElementsMatch(t, []any{"name", "url", "push"}, remote["required"])
	assert.Equal(t, 2048, contractObject(t, remoteProperties["url"])["maxLength"])
	for _, path := range []string{"/branches", "/branches/checkout"} {
		operation := contractObject(t, contractObject(t, paths[path])["post"])
		body := contractObject(t, operation["requestBody"])
		media := contractObject(t, contractObject(t, body["content"])["application/json"])
		branch := contractObject(t, contractObject(t, media["schema"])["properties"])
		assert.Equal(t, 128, contractObject(t, branch["name"])["maxLength"], "branch contracts match the Git command boundary")
	}
	parameters := contractObject(t, components["parameters"])
	name := contractObject(t, contractObject(t, parameters["StorageRemoteName"])["schema"])
	assert.Equal(t, "^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", name["pattern"])
	for _, path := range []string{"/storage/configuration", "/storage/remotes/{name}/check", "/storage/remotes/{name}/push"} {
		item := contractObject(t, paths[path])
		for _, method := range []string{"get", "put", "post"} {
			value, exists := item[method]
			if !exists {
				continue
			}
			operation := contractObject(t, value)
			assert.NotContains(t, operation, "security", "%s %s inherits session authentication", method, path)
			assert.Contains(t, operation["summary"], "administrators with root access only")
			responses := contractObject(t, operation["responses"])
			assert.Contains(t, responses, "401")
			assert.Contains(t, responses, "403")
			if method != "get" {
				assert.Contains(t, responses, "409")
			}
		}
	}
	status := contractObject(t, schemas["RemoteSyncStatus"])
	assert.ElementsMatch(t, []any{"name", "state", "retryable"}, status["required"])
	statusProperties := contractObject(t, status["properties"])
	states := contractObject(t, statusProperties["state"])
	assert.Contains(t, states["enum"], "error", "HTTP 200 does not imply successful synchronization")
}

func TestCMSContractLimitsAndPublicationMetadata(t *testing.T) {
	var root map[string]any
	require.NoError(t, yaml.Unmarshal(APIContract(), &root))
	components := contractObject(t, root["components"])
	schemas := contractObject(t, components["schemas"])
	properties := func(name string) map[string]any {
		return contractObject(t, contractObject(t, schemas[name])["properties"])
	}
	for _, field := range []string{"read", "write"} {
		grant := contractObject(t, properties("Grants")[field])
		assert.Equal(t, 32, grant["maxItems"])
		assert.Equal(t, 256, contractObject(t, grant["items"])["maxLength"])
	}
	assert.Equal(t, 256, contractObject(t, properties("User")["displayName"])["maxLength"])
	assert.Equal(t, 4096, contractObject(t, properties("Settings")["description"])["maxLength"])
	assert.Equal(t, "#/components/schemas/ThemeTokens", contractObject(t, properties("Settings")["theme"])["$ref"])
	assert.Equal(t, true, contractObject(t, properties("Settings")["sync"])["readOnly"])
	assert.Equal(t, "^[0-9a-f]{64}$", contractObject(t, schemas["Revision"])["pattern"])
	assert.Equal(t, false, contractObject(t, schemas["DocumentRebase"])["additionalProperties"])
	assert.NotContains(t, properties("DocumentRebase"), "publish")
	assert.NotContains(t, properties("ThemeInput"), "builtin")
	assert.NotContains(t, properties("ThemeInput"), "sync")
	assert.Contains(t, properties("ThemeInput"), "revision")
	assert.NotContains(t, properties("ThemeTokens"), "revision")
	assert.NotContains(t, properties("ThemeTokens"), "builtin")
	palette := properties("ThemePalette")
	require.Len(t, palette, 6)
	for _, field := range []string{"background", "surface", "text", "muted", "border", "accent"} {
		assert.Equal(t, "^#[0-9a-fA-F]{6}$", contractObject(t, palette[field])["pattern"])
	}
	errorProperties := contractObject(t, properties("Error")["error"])
	errorProperties = contractObject(t, errorProperties["properties"])
	assert.Equal(t, "#/components/schemas/Draft", contractObject(t, errorProperties["draft"])["$ref"])
	assert.Equal(t, "#/components/schemas/Document", contractObject(t, errorProperties["document"])["$ref"])
	cookie := contractObject(t, contractObject(t, components["securitySchemes"])["developmentSessionCookie"])
	assert.Contains(t, cookie["description"], "literal localhost")
	assert.Contains(t, cookie["description"], "Secure")
}
