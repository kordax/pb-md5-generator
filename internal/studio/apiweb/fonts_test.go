package apiweb

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/uiassets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdministrationFontsUseFixedAssetsAndSecurityHeaders(t *testing.T) {
	f := newFixture(t, nil)
	for _, name := range uiassets.Names() {
		t.Run(name, func(t *testing.T) {
			expected, media, ok := uiassets.Resource(name)
			require.True(t, ok)
			target := testBase + "/_docs/admin/assets/" + name
			response := f.request(http.MethodGet, target, nil, nil, nil)
			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, expected, response.Body.Bytes())
			assert.Equal(t, media, response.Header().Get("Content-Type"))
			assert.Equal(t, strconv.Itoa(len(expected)), response.Header().Get("Content-Length"))
			assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			head := f.request(http.MethodHead, target, nil, nil, nil)
			assert.Equal(t, http.StatusOK, head.Code)
			assert.Empty(t, head.Body.Bytes())
			assert.Equal(t, strconv.Itoa(len(expected)), head.Header().Get("Content-Length"))
			assert.Equal(t, http.StatusMethodNotAllowed, f.request(http.MethodPost, target, nil, nil, nil).Code)
		})
	}
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testBase+"/_docs/admin/assets/fonts/unknown.woff2", nil, nil, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodGet, testBase+"/_docs/admin/assets/fonts/../studio.css", nil, nil, nil).Code)
}
