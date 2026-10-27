package apiweb

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewJSONBudgetMatchesStandardEscapingBoundaries(t *testing.T) {
	const budget = 16 << 20
	for _, item := range []struct{ name, unit string }{
		{"ASCII", "a"},
		{"HTML delimiters", "<>&"},
		{"quotes and backslashes", "\"\\"},
		{"short control escapes", "\b\f\n\r\t"},
		{"other control escapes", "\x00\x01"},
		{"JavaScript line separators", "\u2028\u2029"},
		{"Unicode", "λ😀"},
	} {
		t.Run(item.name, func(t *testing.T) {
			encoded, err := json.Marshal(item.unit)
			require.NoError(t, err)
			unitBytes := len(encoded) - 2
			accepted := strings.Repeat(item.unit, budget/unitBytes)
			require.LessOrEqual(t, len(accepted), budget)
			assert.True(t, boundedPreview(Preview{HTML: accepted}))
			assert.False(t, boundedPreview(Preview{HTML: accepted + item.unit}))
		})
	}
}

func TestEscapingAmplificationIsRejectedBeforePreparingHTTPResponse(t *testing.T) {
	amplified := strings.Repeat("&", 16<<20)
	f := newFixture(t, func(options *Options) {
		options.Preview = func(context.Context, Document, []byte, *auth.User) (Preview, error) {
			return Preview{HTML: amplified}, nil
		}
	})
	response := f.request(http.MethodPost, testAPI+"/preview", map[string]string{"documentId": "home", "content": "source"}, &f.admin, nil)
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	assert.Less(t, response.Body.Len(), 1024)
	assert.Contains(t, response.Body.String(), "too_large")
	assert.NotContains(t, response.Body.String(), "&")
}
