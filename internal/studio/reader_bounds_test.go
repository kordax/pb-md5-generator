package studio

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReaderCacheHasBoundedRetentionAndRevisionIsolation(t *testing.T) {
	reader := &cmsReader{cache: make(map[string]readerResponse)}
	_, exists := reader.cached("first", "missing")
	assert.False(t, exists)
	for i := range 70 {
		reader.remember("first", fmt.Sprint(i), readerResponse{body: bytes.Repeat([]byte{byte(i)}, 128<<10), contentType: "text/plain"})
	}
	assert.LessOrEqual(t, reader.bytes, readerCacheBytes)
	assert.LessOrEqual(t, len(reader.cache), 64)
	_, exists = reader.cached("first", "0")
	assert.False(t, exists)
	response, exists := reader.cached("first", "69")
	require.True(t, exists)
	assert.Equal(t, byte(69), response.body[0])
	retained := reader.bytes
	for range 100 {
		reader.remember("first", "69", readerResponse{body: []byte("duplicate")})
	}
	assert.Equal(t, retained, reader.bytes)
	reader.remember("first", "huge", readerResponse{body: make([]byte, (2<<20)+1)})
	reader.remember("first", "redirect", readerResponse{redirect: "/new.html"})
	reader.remember("old", "stale", readerResponse{body: []byte("old")})
	for _, key := range []string{"huge", "redirect", "stale"} {
		_, exists := reader.cached("first", key)
		assert.False(t, exists)
	}
	_, exists = reader.cached("second", "69")
	assert.False(t, exists)
	assert.Empty(t, reader.cache)
	assert.Zero(t, reader.bytes)
}

func TestReaderJSONRejectsGrowthBeforeAllocatingBeyondBudget(t *testing.T) {
	var output readerJSON
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for range 16 {
		require.NoError(t, output.appendJSON(chunk))
		assert.LessOrEqual(t, cap(output.data), 16<<20)
	}
	before := sha256.Sum256(output.data)
	assert.ErrorIs(t, output.appendJSON([]byte("x")), content.ErrLimit)
	assert.Len(t, output.data, 16<<20)
	assert.Equal(t, before, sha256.Sum256(output.data))
}

func TestReaderRejectsUnsafePathsAndMethodsBeforeReadingStorage(t *testing.T) {
	for _, name := range []string{"/manualx/index.html", "/manual//index.html", "/manual/../private.html", "/manual/.git/config", "/manual/docs\\private.md", "/manual/bad\x00name"} {
		_, ok := readerPath(name, "/manual")
		assert.False(t, ok, name)
	}
	for _, name := range []string{"/manual", "/manual/", "/manual/guide/index.html"} {
		_, ok := readerPath(name, "/manual")
		assert.True(t, ok, name)
	}
	handler := newReader(runtimeConfig{basePath: "/manual"}, nil, nil)
	for _, test := range []struct {
		method, target string
		status         int
	}{
		{http.MethodPost, "/manual/index.html", http.StatusMethodNotAllowed},
		{http.MethodGet, "/manual/../private.html", http.StatusNotFound},
		{http.MethodGet, "/manual/index.html", http.StatusUnauthorized},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.target, nil))
		assert.Equal(t, test.status, response.Code)
		assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	}
}

func TestReaderSearchPreservesInlineWordsAndBoundsUnicode(t *testing.T) {
	page := document.Page{Title: "Search", Blocks: []document.Block{{Kind: document.BlockParagraph, Inlines: []document.Inline{
		{Kind: document.InlineText, Text: "hel"},
		{Kind: document.InlineStrong, Children: []document.Inline{{Kind: document.InlineText, Text: "lo"}}},
		{Kind: document.InlineText, Text: " world"},
	}}}}
	assert.Contains(t, searchText(page), "hello world")
	page.Blocks = []document.Block{{Kind: document.BlockParagraph, Text: string(bytes.Repeat([]byte("界"), 20000))}}
	text := searchText(page)
	assert.LessOrEqual(t, len(text), 32<<10)
	assert.True(t, utf8.ValidString(text))
}
