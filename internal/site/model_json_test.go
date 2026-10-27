package site

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type guardedJSONMarshaler struct{ called *bool }

func (m guardedJSONMarshaler) MarshalJSON() ([]byte, error) {
	*m.called = true
	return []byte(`"must not run"`), nil
}

type guardedTextMarshaler struct{ called *bool }

func (m *guardedTextMarshaler) MarshalText() ([]byte, error) {
	*m.called = true
	return []byte("must not run"), nil
}

func TestModelEncodingBoundMatchesJSON(t *testing.T) {
	raw := json.RawMessage(`{"text":"<>&\u2028\u2029","integer":18446744073709551615}`)
	values := []any{
		nil, true, false, int64(math.MinInt64), uint64(math.MaxUint64), 1.25,
		"quotes\" slash\\ control\n\t\x00 <>& \u2028\u2029 Привет 世界", string([]byte{0xff, 0xfe}),
		[]byte{0, 1, 2, 255}, []any{nil, "value", 1}, map[string]any{"first": "<&", "second": false},
		raw, &raw, json.RawMessage(nil),
		document.Page{Format: "pb-docs/page", Version: 1, ID: "page", Title: "Page", Path: "index.html", Source: document.Location{File: "page.md"}, Kind: "markdown", Blocks: []document.Block{{Kind: document.BlockParagraph, Text: "A < B"}}},
	}
	for _, value := range values {
		expected, err := json.Marshal(value)
		require.NoError(t, err)
		t.Run(string(expected), func(t *testing.T) {
			data, err := marshalModel(value, len(expected)+1)
			require.NoError(t, err)
			assert.Equal(t, string(expected)+"\n", string(data))
			_, err = marshalModel(value, len(expected))
			require.Error(t, err)
		})
	}
}

func TestModelRejectsCustomMarshalersBeforeCallingThem(t *testing.T) {
	called := false
	for _, value := range []any{
		guardedJSONMarshaler{called: &called},
		&guardedJSONMarshaler{called: &called},
		&guardedTextMarshaler{called: &called},
		struct{ Value guardedTextMarshaler }{Value: guardedTextMarshaler{called: &called}},
		map[string]any{"nested": guardedJSONMarshaler{called: &called}},
	} {
		_, err := marshalModel(value, 1024)
		require.ErrorContains(t, err, "custom document model marshaler")
		assert.False(t, called)
	}
}

func TestModelRejectsUnsafeOrUnboundedShapes(t *testing.T) {
	cyclic := make([]any, 1)
	cyclic[0] = cyclic
	for _, value := range []any{
		cyclic, map[int]string{1: "unsupported"}, make(chan int), func() {},
		json.RawMessage(`{"incomplete":`), json.RawMessage{0xff}, math.Inf(1), math.NaN(),
		struct {
			Value string `json:",string"`
		}{Value: "unsupported"},
	} {
		_, err := marshalModel(value, 1<<20)
		require.Error(t, err)
	}
	for _, limit := range []int{0, -1, math.MinInt} {
		_, err := marshalModel(nil, limit)
		require.ErrorContains(t, err, "positive")
	}
	_, err := marshalModel(map[string]string{"oversized": strings.Repeat("<", 1<<20)}, 4096)
	require.ErrorContains(t, err, "exceeds")
}
