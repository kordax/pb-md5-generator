package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetsContainPortableAdministrationEntryAndLocalEditor(t *testing.T) {
	root := Assets()
	for _, name := range []string{"index.html", "studio.css", "app.mjs", "core.mjs", "editor.js"} {
		data, err := fs.ReadFile(root, name)
		require.NoError(t, err, name)
		assert.NotEmpty(t, data, name)
	}
	index, err := fs.ReadFile(root, "index.html")
	require.NoError(t, err)
	html := string(index)
	assert.Equal(t, 1, strings.Count(html, "__PB_DOCS_CSP_NONCE__"))
	assert.Contains(t, html, `<script type="module" src="app.mjs"></script>`)
	assert.Contains(t, html, `<link rel="stylesheet" href="studio.css">`)
	assert.NotContains(t, html, "onclick=")
	assert.NotContains(t, html, "<style")
	for _, match := range regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		assert.False(t, strings.Contains(match[1], "://"), "entry point must not need a remote asset: %s", match[1])
	}
	_, err = fs.Stat(root, "package.json")
	assert.ErrorIs(t, err, fs.ErrNotExist)
	_, err = fs.Stat(root, "node_modules")
	assert.ErrorIs(t, err, fs.ErrNotExist)
	alias, err := fs.ReadFile(FS(), "index.html")
	require.NoError(t, err)
	assert.Equal(t, index, alias)
}
