package site

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openAPICorpus(t testing.TB, operations int) []byte {
	t.Helper()
	paths := make(map[string]any, operations)
	schemas := make(map[string]any, operations)
	for i := range operations {
		name := fmt.Sprintf("Item%d", i)
		schemas[name] = map[string]any{
			"type": "object", "required": []string{"id", "name"},
			"properties": map[string]any{
				"id": map[string]any{"type": "integer", "minimum": 1},
				"name": map[string]any{"type": "string", "maxLength": 120},
				"related": map[string]any{"$ref": "#/components/schemas/" + name},
			},
		}
		paths[fmt.Sprintf("/items/%d/{id}", i)] = map[string]any{"get": map[string]any{
			"operationId": fmt.Sprintf("getItem%d", i), "summary": "Read one item", "tags": []string{"Catalog"},
			"parameters": []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}}},
			"responses": map[string]any{"200": map[string]any{
				"description": "Requested item", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + name}}},
			}},
		}}
	}
	data, err := json.Marshal(map[string]any{
		"openapi": "3.1.0", "info": map[string]any{"title": "Catalog corpus", "version": "1.0.0"},
		"servers": []any{map[string]any{"url": "https://api.example.test/v1"}},
		"paths": paths, "components": map[string]any{"schemas": schemas},
	})
	require.NoError(t, err)
	return data
}

func apiCorpusOptions() Options {
	return Options{ImportPage: func(ctx context.Context, _, name string, source []byte, load func(string) ([]byte, error)) (ImportResult, error) {
		api, err := importer.OpenAPI(ctx, name, source, importer.Load(load))
		return ImportResult{API: api}, err
	}}
}

func TestRepeatedAPIBuildResources(t *testing.T) {
	root := t.TempDir()
	config := writeModelFixture(t, root, "docs.json", `{"version":1,"title":"API corpus","pages":[{"title":"Catalog","source":"api.json","path":"index.html","kind":"openapi"}]}`)
	writeModelFixture(t, root, "api.json", string(openAPICorpus(t, 2)))
	output := filepath.Join(root, "out")
	options := apiCorpusOptions()
	require.NoError(t, Build(config, output, options))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	goroutines := runtime.NumGoroutine()
	beforeFD, fdErr := os.ReadDir("/proc/self/fd")
	for range 100 {
		require.NoError(t, Build(config, output, options))
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	assert.LessOrEqual(t, int64(after.HeapAlloc)-int64(before.HeapAlloc), int64(12<<20), "API importer, renderer and JSON export must release completed page graphs")
	assert.LessOrEqual(t, runtime.NumGoroutine(), goroutines+2)
	if fdErr == nil {
		afterFD, err := os.ReadDir("/proc/self/fd")
		require.NoError(t, err)
		assert.LessOrEqual(t, len(afterFD), len(beforeFD)+1)
	}
}

func BenchmarkBuildOpenAPI128Operations(b *testing.B) {
	root := b.TempDir()
	config := filepath.Join(root, "docs.json")
	source := openAPICorpus(b, 128)
	b.Logf("source bytes=%d sha256=%x", len(source), sha256.Sum256(source))
	require.NoError(b, os.WriteFile(config, []byte(`{"version":1,"title":"API corpus","pages":[{"title":"Catalog","source":"api.json","path":"index.html","kind":"openapi"}]}`), 0o600))
	require.NoError(b, os.WriteFile(filepath.Join(root, "api.json"), source, 0o600))
	output := filepath.Join(root, "out")
	options := apiCorpusOptions()
	// Warm the bounded specification validator cache before measuring builds.
	require.NoError(b, Build(config, output, options))
	b.SetBytes(int64(len(source)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		require.NoError(b, Build(config, output, options))
	}
}
