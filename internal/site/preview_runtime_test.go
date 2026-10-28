package site

import (
	"context"
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewStudioLinkIsRuntimeOnlyAndStaticOutputOmitsIt(t *testing.T) {
	source := []byte("# Read\n")
	files := fstest.MapFS{
		"docs.json": {Data: []byte(`{"version":1,"title":"Docs","pages":[{"title":"Read","source":"index.md","path":"index.html"}]}`)},
		"index.md":  {Data: source},
	}
	page := Page{Title: "Read", Source: "index.md", Path: "index.html"}
	compiled, err := CompilePreviewFSContext(context.Background(), files, "docs.json", page, source, PreviewOptions{ReaderStudioURL: "/reference/_docs/admin/"})
	require.NoError(t, err)
	assert.Contains(t, string(compiled.HTML), `<a href="/reference/_docs/admin/">Studio</a>`)
	model, err := json.Marshal(compiled.Page)
	require.NoError(t, err)
	assert.NotContains(t, string(model), "/reference/_docs/admin/")

	static, err := CompilePreviewFSContext(context.Background(), files, "docs.json", page, source, PreviewOptions{})
	require.NoError(t, err)
	assert.NotContains(t, string(static.HTML), ">Studio</a>")
}
