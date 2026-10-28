package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/uiassets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterfaceFontsPublishAndServeTheSameBytes(t *testing.T) {
	project, output, manifest := projectFixture(t)
	config := filepath.Join(project, "docs.json")
	require.NoError(t, Build(config, output, Options{}))
	for _, name := range uiassets.Names() {
		t.Run(name, func(t *testing.T) {
			expected, media, ok := uiassets.Resource(name)
			require.True(t, ok)
			data, actualMedia, ok := ReaderResource("_docs/"+name, manifest)
			require.True(t, ok)
			assert.Equal(t, media, actualMedia)
			assert.Equal(t, expected, data)
			published, err := os.ReadFile(filepath.Join(output, "_docs", filepath.FromSlash(name)))
			require.NoError(t, err)
			assert.Equal(t, expected, published)
			if strings.HasSuffix(name, ".woff2") {
				assert.Equal(t, "font/woff2", media)
				require.GreaterOrEqual(t, len(data), 4)
				assert.Equal(t, "wOF2", string(data[:4]))
			}
		})
	}
	for _, name := range []string{"_docs/fonts/../site.css", "_docs/fonts/unknown.woff2", "fonts/dm-sans-latin.woff2"} {
		_, _, ok := ReaderResource(name, manifest)
		assert.False(t, ok, name)
	}
	require.NoError(t, os.WriteFile(filepath.Join(output, "_docs", "fonts", "dm-sans-latin.woff2"), []byte("modified"), 0o600))
	assert.ErrorContains(t, Build(config, output, Options{Check: true}), "out of date")
}
