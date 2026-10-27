package studio

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kordax/pb-md5-generator/engine"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeCMSFixture(t *testing.T, root, name, data string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o700))
	require.NoError(t, os.WriteFile(file, []byte(data), 0o600))
}

func TestCMSCompilerRendersMixedSources(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	output := filepath.Join(t.TempDir(), "publication")
	writeCMSFixture(t, root, "docs.json", `{"version":1,"title":"CMS fixture","pages":[{"title":"Guide","source":"index.md","path":"index.html"},{"title":"RPC","source":"api/library.proto","path":"reference/rpc.html","kind":"protobuf"},{"title":"HTTP","source":"api/ping.yaml","path":"reference/http.html","kind":"openapi"}]}`)
	writeCMSFixture(t, root, "index.md", "# Guide\n\n:::note title=\"Before publication\"\nValidate the source.\n:::\n\n[RPC](api/library.proto) and [HTTP](api/ping.yaml).\n")
	writeCMSFixture(t, root, "api/library.proto", `syntax = "proto3";
package library;
// @autocode[json]
message Book {
  string title = 1;
  // @ignore
  string internal_note = 2;
}
service Library {
  rpc GetBook(Book) returns (Book);
}
`)
	writeCMSFixture(t, root, "api/ping.yaml", "openapi: 3.1.0\ninfo:\n  title: Ping\n  version: '1'\npaths:\n  /ping:\n    get:\n      operationId: ping\n      responses:\n        '200':\n          description: Healthy\n")
	require.NoError(t, compilePublication(context.Background(), filepath.Join(root, "docs.json"), output))
	for _, test := range []struct{ path, text string }{{"index.html", "Validate the source."}, {"reference/rpc.html", "GetBook"}, {"reference/http.html", "/ping"}} {
		data, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(test.path)))
		require.NoError(t, err)
		assert.Contains(t, string(data), test.text)
		assert.NotContains(t, string(data), "internal_note")
	}
	files, err := os.ReadDir(filepath.Join(output, "_docs", "pages"))
	require.NoError(t, err)
	require.Len(t, files, 3)
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(output, "_docs", "pages", file.Name()))
		require.NoError(t, err)
		assert.NotContains(t, string(data), "internal_note")
	}
	options := compilerOptions(engine.DefaultCodegenSeed)
	options.Check = true
	// Check mode verifies the same compiler/import adapters without rewriting.
	require.NoError(t, site.BuildContext(context.Background(), filepath.Join(root, "docs.json"), output, options))
}

func TestCMSImportFailuresRemainErrors(t *testing.T) {
	importPage := compilerOptions(engine.DefaultCodegenSeed).ImportPage
	for _, test := range []struct{ name, kind, source string }{
		{"unsupported", "unregistered", "source"},
		{"invalid OpenAPI", "openapi", "invalid: document"},
		{"malformed protobuf", "protobuf", "this is not protobuf"},
		{"invalid annotation", "protobuf", "syntax=\"proto3\";\nmessage M {\n// @doc nonexistent=1\nstring value=1;\n}\n"},
		{"empty integer interval", "protobuf", "syntax=\"proto3\";\n// @autocode[json]\nmessage M {\n// @doc min=0.1 max=0.9\nint32 value=1;\n}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := importPage(context.Background(), test.kind, "source.proto", []byte(test.source), nil)
			require.Error(t, err)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := importPage(ctx, "protobuf", "source.proto", []byte("invalid"), nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = renderImportedProto(ctx, "source.proto", nil, nil, engine.DefaultCodegenSeed)
	require.ErrorIs(t, err, context.Canceled)
}
