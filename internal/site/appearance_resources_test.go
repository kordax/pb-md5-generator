package site

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReaderResourcesAreFixedAndThemeBytesAreIndependent(t *testing.T) {
	manifest := Manifest{Title: "Docs"}
	for _, name := range []string{"_docs/site.css", "_docs/site.js", "_docs/api.css", "_docs/api-client.js", "_docs/appearance.css", "_docs/schema/manifest-v1.json", "_docs/schema/project-v1.json", "_docs/schema/page-v1.json"} {
		first, media, ok := ReaderResource(name, manifest)
		require.True(t, ok, name)
		require.NotEmpty(t, first)
		require.NotEmpty(t, media)
		original := bytes.Clone(first)
		first[0] = '!'
		second, _, ok := ReaderResource(name, manifest)
		require.True(t, ok)
		assert.Equal(t, original, second)
	}
	for _, name := range []string{"docs.json", "_docs/project.json", "_docs/search.json", "_docs/pages/private.json", "../site.js", "_docs/schema/../../docs.json"} {
		data, _, ok := ReaderResource(name, manifest)
		assert.False(t, ok, name)
		assert.Nil(t, data)
	}
	theme := DefaultTheme()
	theme.Accent = "red; background:url(https://external.invalid/)"
	manifest.ThemeID, manifest.Theme = theme.ID, &theme
	data, _, ok := ReaderResource("_docs/appearance.css", manifest)
	assert.False(t, ok)
	assert.Nil(t, data)
}

func TestReaderAppearanceKeepsCoreAssetsWithinBudget(t *testing.T) {
	for _, theme := range BuiltinThemes() {
		manifest := Manifest{Title: "Docs", ThemeID: theme.ID, Theme: &theme}
		total := 0
		for _, name := range []string{"_docs/site.css", "_docs/site.js", "_docs/appearance.css"} {
			data, _, ok := ReaderResource(name, manifest)
			require.True(t, ok)
			var compressed bytes.Buffer
			writer := gzip.NewWriter(&compressed)
			_, err := writer.Write(data)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			total += compressed.Len()
		}
		assert.LessOrEqual(t, total, 40<<10)
		t.Logf("%s core reader assets: %d gzip bytes", theme.ID, total)
	}
}
