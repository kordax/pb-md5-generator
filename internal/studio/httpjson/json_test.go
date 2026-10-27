package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalPreservesSourceAndExactNumbers(t *testing.T) {
	type nested struct {
		Name string `json:"name"`
	}
	var target struct {
		Content string                     `json:"content"`
		Child   *nested                    `json:"child"`
		Items   []nested                   `json:"items"`
		Values  map[string]json.RawMessage `json:"values"`
		Numbers map[string]any             `json:"numbers"`
	}
	data := []byte(`{"content":"text 🐈 \ud83d\ude00 \\ud800","child":{"name":"child"},"items":[{"name":"one"}],"values":{"contract":{"ID":1}},"numbers":{"large":18446744073709551615}}`)
	require.NoError(t, Validate(data))
	require.NoError(t, Unmarshal(data, &target))
	assert.Equal(t, "text 🐈 😀 \\ud800", target.Content)
	require.NotNil(t, target.Child)
	assert.Equal(t, "child", target.Child.Name)
	require.Len(t, target.Items, 1)
	assert.Equal(t, "one", target.Items[0].Name)
	assert.JSONEq(t, `{"ID":1}`, string(target.Values["contract"]))
	assert.Equal(t, json.Number("18446744073709551615"), target.Numbers["large"])
}

func TestInvalidFieldsNeverPartiallyChangeTarget(t *testing.T) {
	cases := []string{
		`{"role":"editor","role":"admin"}`,
		`{"role":"editor","Role":"admin"}`,
		`{"role":"editor","\u0072ole":"admin"}`,
		`{"Role":"admin"}`,
		`{"role":"editor","unknown":true}`,
		`{"role":"editor"} {"role":"admin"}`,
		`{"role":42}`,
		`{"role":null}`,
		`{"role":"\ud800"}`,
		`{"role":"\udc00"}`,
		`{"role":"\ud800\ud800"}`,
		`{"role":"\ud800x"}`,
		`{"role":"\ud800\u0000"}`,
		`{"role":"\uZZZZ"}`,
		`{"role":"trailing escape\`,
		`[]`, `null`, `"object"`, `42`, ``,
	}
	for _, data := range cases {
		t.Run(data, func(t *testing.T) {
			target := struct {
				Role string `json:"role"`
			}{Role: "untouched"}
			require.ErrorIs(t, Unmarshal([]byte(data), &target), ErrInvalid)
			assert.Equal(t, "untouched", target.Role)
		})
	}
	require.ErrorIs(t, Validate([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}), ErrInvalid)
	require.ErrorIs(t, Validate([]byte(`{"K":1,"\u212a":2}`)), ErrInvalid)
	require.ErrorIs(t, Validate([]byte(`{"s":1,"ſ":2}`)), ErrInvalid)
}

func TestRequestStructureAndResourceLimits(t *testing.T) {
	require.ErrorIs(t, Validate([]byte(`{"a":`+strings.Repeat("[", maxDepth+2)+`0`+strings.Repeat("]", maxDepth+2)+`}`)), ErrTooLarge)
	require.ErrorIs(t, Validate([]byte(`{"a":[`+strings.Repeat("null,", maxNodes)+`null]}`)), ErrTooLarge)
	require.ErrorIs(t, Validate(bytes.Repeat([]byte{' '}, maxBytes+1)), ErrTooLarge)
	for _, data := range []string{`{"a":]}`, `{"a":[1,]}`, `{"a":{1:2}}`, `{"a":1,}`, `{"a":true}false`} {
		require.ErrorIs(t, Validate([]byte(data)), ErrInvalid)
	}
	var target struct {
		Child struct {
			Name string `json:"name"`
		} `json:"child"`
	}
	require.ErrorIs(t, Unmarshal([]byte(`{"child":{"Name":"wrong case"}}`), &target), ErrInvalid)
	require.ErrorIs(t, Unmarshal([]byte(`{"child":[]}`), &target), ErrInvalid)
	require.ErrorIs(t, Unmarshal([]byte(`{"child":{}}`), target), ErrInvalid)
	var pointer *struct{}
	require.ErrorIs(t, Unmarshal([]byte(`{}`), pointer), ErrInvalid)
	type Embedded struct {
		Role string `json:"role"`
	}
	var anonymous struct{ Embedded }
	require.ErrorIs(t, Unmarshal([]byte(`{"role":"viewer"}`), &anonymous), ErrInvalid)
	var integerKeys map[int]string
	require.ErrorIs(t, Unmarshal([]byte(`{"1":"value"}`), &integerKeys), ErrInvalid)
	var nullable struct {
		Name  *string         `json:"name"`
		Flag  *bool           `json:"flag"`
		Items []string        `json:"items"`
		Raw   json.RawMessage `json:"raw"`
	}
	for _, data := range []string{`{"name":null}`, `{"flag":null}`, `{"items":null}`} {
		require.ErrorIs(t, Unmarshal([]byte(data), &nullable), ErrInvalid)
	}
	require.NoError(t, Unmarshal([]byte(`{"raw":null}`), &nullable))
	assert.Equal(t, "null", string(nullable.Raw))
	require.NoError(t, Unmarshal([]byte(`{}`), &nullable))
}

type observedBody struct {
	reader io.Reader
	closed bool
}

func (b *observedBody) Read(data []byte) (int, error) { return b.reader.Read(data) }
func (b *observedBody) Close() error                  { b.closed = true; return nil }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestDecodeChecksHTTPAndClosesConsumedBody(t *testing.T) {
	for _, test := range []struct {
		name, media, encoding, body string
		limit                       int64
		status                      int
	}{
		{name: "valid", media: "application/json; charset=UTF-8", body: `{"name":"ok"}`, limit: 100, status: 0},
		{name: "wrong media", media: "text/plain", body: `{}`, limit: 100, status: 415},
		{name: "bad media syntax", media: "application/json;broken", body: `{}`, limit: 100, status: 415},
		{name: "wrong charset", media: "application/json; charset=latin1", body: `{}`, limit: 100, status: 415},
		{name: "encoded body", media: "application/json", encoding: "gzip", body: `{}`, limit: 100, status: 415},
		{name: "known oversized", media: "application/json", body: `{"name":"large"}`, limit: 5, status: 413},
		{name: "invalid limit", media: "application/json", body: `{}`, limit: 0, status: 413},
		{name: "bad JSON", media: "application/json", body: `{`, limit: 100, status: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.media)
			request.Header.Set("Content-Encoding", test.encoding)
			var target struct {
				Name string `json:"name"`
			}
			err := Decode(httptest.NewRecorder(), request, &target, test.limit)
			if test.status == 0 {
				require.NoError(t, err)
				assert.Equal(t, "ok", target.Name)
			} else {
				require.Error(t, err)
				assert.Equal(t, test.status, Status(err))
			}
		})
	}
	for _, test := range []struct {
		reader io.Reader
		limit  int64
		error  error
	}{
		{reader: strings.NewReader(`{"name":"overflow"}`), limit: 5, error: ErrTooLarge},
		{reader: brokenReader{}, limit: 100, error: ErrInvalid},
	} {
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.Header.Set("Content-Type", "application/json")
		request.ContentLength = -1
		body := &observedBody{reader: test.reader}
		request.Body = body
		var target map[string]any
		require.ErrorIs(t, Decode(httptest.NewRecorder(), request, &target, test.limit), test.error)
		assert.True(t, body.closed)
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Content-Type", "application/json")
	request.Body = nil
	var target map[string]any
	require.ErrorIs(t, Decode(httptest.NewRecorder(), request, &target, 100), ErrInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, Decode(httptest.NewRecorder(), request.WithContext(ctx), &target, 100), context.Canceled)
	assert.Equal(t, http.StatusRequestTimeout, Status(context.Canceled))
	assert.Equal(t, http.StatusRequestTimeout, Status(context.DeadlineExceeded))
}

func FuzzRequestJSON(f *testing.F) {
	for _, seed := range []string{`{}`, `{"role":"viewer"}`, `{"role":"editor","Role":"admin"}`, `{"source":"\ud83d\ude00"}`, `{"source":"\\ud800"}`, `{"source":"\ud800"}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 32<<10 {
			t.Skip()
		}
		var target map[string]any
		_ = Validate(data)
		_ = Unmarshal(data, &target)
	})
}
