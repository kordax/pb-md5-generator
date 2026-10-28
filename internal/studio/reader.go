package studio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/engine"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

const readerCacheBytes = 8 << 20

type readerResponse struct {
	body           []byte
	contentType    string
	disposition    string
	redirect       string
	redirectStatus int
}

type cmsReader struct {
	config   runtimeConfig
	contents *content.Service
	gate     chan struct{}
	mu       sync.Mutex
	revision string
	cache    map[string]readerResponse
	order    []string
	bytes    int
}

func newReader(config runtimeConfig, _ *auth.Service, contents *content.Service) http.Handler {
	return &cmsReader{config: config, contents: contents, gate: make(chan struct{}, 2), cache: make(map[string]readerResponse)}
}

func (reader *cmsReader) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "same-origin")
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.Header().Set("Vary", "Cookie")
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' https:; connect-src 'self' https: http:; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name, ok := readerPath(request.URL.Path, reader.config.basePath)
	if !ok {
		http.NotFound(writer, request)
		return
	}
	user, err := apiweb.Principal(request.Context())
	if err != nil || user == nil {
		http.Error(writer, "authentication required", http.StatusUnauthorized)
		return
	}
	if reader.config.basePath != "" && request.URL.Path == reader.config.basePath {
		reader.send(writer, request, readerResponse{redirect: reader.config.basePath + "/", redirectStatus: http.StatusPermanentRedirect})
		return
	}
	if name != "_docs/appearance.css" {
		if resource, mediaType, exists := site.ReaderResource(name, site.Manifest{}); exists {
			reader.send(writer, request, readerResponse{body: resource, contentType: mediaType})
			return
		}
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	project, err := reader.contents.Project(ctx)
	if err != nil {
		reader.unavailable(writer)
		return
	}
	// A request may have waited behind publication. Re-resolve the cookie
	// before using a cache entry produced for this user's current grants.
	user, err = apiweb.Principal(ctx)
	if err != nil || user == nil {
		http.Error(writer, "authentication required", http.StatusUnauthorized)
		return
	}
	principal, err := json.Marshal(user)
	if err != nil {
		reader.unavailable(writer)
		return
	}
	digest := sha256.Sum256(principal)
	key := hex.EncodeToString(digest[:]) + ":" + name
	if response, exists := reader.cached(project.Revision, key); exists {
		reader.send(writer, request, response)
		return
	}
	select {
	case reader.gate <- struct{}{}:
		defer func() { <-reader.gate }()
	default:
		reader.unavailable(writer)
		return
	}
	var response readerResponse
	err = reader.contents.View(ctx, func(ctx context.Context, root, manifestPath string) error {
		current, err := apiweb.Principal(ctx)
		if err != nil || current == nil {
			return content.ErrOwnership
		}
		// A changed principal gets a new cache key, never the old wider scope.
		currentJSON, err := json.Marshal(current)
		if err != nil {
			return err
		}
		currentDigest := sha256.Sum256(currentJSON)
		key = hex.EncodeToString(currentDigest[:]) + ":" + name
		manifest, err := readProjectManifest(root, manifestPath)
		if err != nil {
			return err
		}
		manifest.Pages = slices.DeleteFunc(manifest.Pages, func(page site.Page) bool { return !canAccessSource(current, auth.ActionRead, page.Source) })
		manifest.Assets = slices.DeleteFunc(manifest.Assets, func(asset string) bool { return !canAccessSource(current, auth.ActionRead, asset) })
		if manifest.Logo != "" && !canAccessSource(current, auth.ActionRead, manifest.Logo) {
			manifest.Logo = ""
		}
		if manifest.Favicon != "" && !canAccessSource(current, auth.ActionRead, manifest.Favicon) {
			manifest.Favicon = ""
		}
		response, err = reader.render(ctx, root, manifestPath, manifest, current, name)
		return err
	})
	if err != nil {
		if errors.Is(err, content.ErrNotFound) || errors.Is(err, site.ErrPreviewAccess) || errors.Is(err, os.ErrNotExist) {
			http.NotFound(writer, request)
		} else if errors.Is(err, content.ErrOwnership) {
			http.Error(writer, "authentication required", http.StatusUnauthorized)
		} else {
			reader.unavailable(writer)
		}
		return
	}
	reader.remember(project.Revision, key, response)
	reader.send(writer, request, response)
}

func (reader *cmsReader) render(ctx context.Context, root, manifestPath string, manifest site.Manifest, user *auth.User, name string) (readerResponse, error) {
	response := readerResponse{contentType: "application/json; charset=utf-8"}
	if name == "_docs/appearance.css" {
		data, mediaType, ok := site.ReaderResource(name, manifest)
		if !ok {
			return readerResponse{}, content.ErrInvalid
		}
		return readerResponse{body: data, contentType: mediaType}, nil
	}
	if name == "_docs/project.json" {
		if err := filterReaderNavigation(&manifest); err != nil {
			return readerResponse{}, err
		}
		model := site.ProjectModel(manifest)
		data, err := site.EncodeProjectModel(ctx, model)
		response.body = data
		return response, err
	}
	if name == "_docs/search.json" {
		type searchEntry struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Text  string `json:"text"`
		}
		var index readerJSON
		if err := index.appendJSON([]byte("[")); err != nil {
			return readerResponse{}, err
		}
		count := 0
		for _, page := range manifest.Pages {
			compiled, err := reader.compile(ctx, root, manifestPath, page, user)
			if errors.Is(err, site.ErrPreviewAccess) {
				continue
			}
			if err != nil {
				return readerResponse{}, err
			}
			entry, err := json.Marshal(searchEntry{Title: page.Title, URL: page.Path, Text: searchText(compiled.Page)})
			if err != nil {
				return readerResponse{}, err
			}
			if count > 0 {
				if err := index.appendJSON([]byte(",")); err != nil {
					return readerResponse{}, err
				}
			}
			if err := index.appendJSON(entry); err != nil {
				return readerResponse{}, err
			}
			count++
		}
		if err := index.appendJSON([]byte("]")); err != nil {
			return readerResponse{}, err
		}
		response.body = index.data
		return response, nil
	}
	if strings.HasPrefix(name, "assets/") {
		asset := strings.TrimPrefix(name, "assets/")
		if !slices.Contains(manifest.Assets, asset) {
			return readerResponse{}, content.ErrNotFound
		}
		data, err := readReaderAsset(root, asset)
		if err != nil {
			return readerResponse{}, err
		}
		mediaType := http.DetectContentType(data)
		response.contentType = "application/octet-stream"
		response.disposition = mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(asset)})
		if slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/x-icon", "image/vnd.microsoft.icon"}, mediaType) {
			response.contentType, response.disposition = mediaType, ""
		}
		response.body = data
		return response, nil
	}
	if name == "" {
		for _, page := range manifest.Pages {
			if page.Path == "index.html" {
				name = page.Path
				break
			}
		}
		if name == "" && len(manifest.Pages) > 0 {
			return readerResponse{redirect: reader.config.basePath + "/" + manifest.Pages[0].Path}, nil
		}
	}
	for _, page := range manifest.Pages {
		directory := name != "" && page.Path == name+"/index.html"
		if name != page.Path && name != site.PageDataPath(page) && !directory {
			continue
		}
		compiled, err := reader.compile(ctx, root, manifestPath, page, user)
		if err != nil {
			return readerResponse{}, err
		}
		if directory {
			return readerResponse{redirect: reader.config.basePath + "/" + name + "/", redirectStatus: http.StatusPermanentRedirect}, nil
		}
		if name == page.Path {
			return readerResponse{body: compiled.HTML, contentType: "text/html; charset=utf-8"}, nil
		}
		data, err := site.EncodePageModel(ctx, compiled.Page)
		response.body = data
		return response, err
	}
	return readerResponse{}, content.ErrNotFound
}

func (reader *cmsReader) compile(ctx context.Context, root, manifestPath string, page site.Page, user *auth.User) (site.PreviewResult, error) {
	source, err := readSource(root, page.Source)
	if err != nil {
		return site.PreviewResult{}, err
	}
	return site.CompilePreviewContext(ctx, manifestPath, page, source, site.PreviewOptions{
		Compiler: compilerOptions(engine.DefaultCodegenSeed),
		CanRead:  func(name string) bool { return canAccessSource(user, auth.ActionRead, name) },
	})
}

func readReaderAsset(rootDir, name string) ([]byte, error) {
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, content.MaxBinaryBytes+1))
	if err == nil && len(data) > content.MaxBinaryBytes {
		err = content.ErrLimit
	}
	return data, err
}

func readerPath(value, prefix string) (string, bool) {
	if prefix != "" {
		if value != prefix && !strings.HasPrefix(value, prefix+"/") {
			return "", false
		}
		value = strings.TrimPrefix(value, prefix)
	}
	name := strings.TrimPrefix(value, "/")
	if name == "" {
		return name, true
	}
	directory := strings.HasSuffix(name, "/")
	if directory {
		name = strings.TrimSuffix(name, "/")
	}
	if strings.ContainsAny(name, "\\\x00\r\n") || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return "", false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return "", false
		}
	}
	if directory {
		name += "/index.html"
	}
	return name, true
}

func (reader *cmsReader) send(writer http.ResponseWriter, request *http.Request, response readerResponse) {
	if response.redirect != "" {
		location := (&url.URL{Path: response.redirect, RawQuery: request.URL.RawQuery, ForceQuery: request.URL.ForceQuery}).String()
		status := response.redirectStatus
		if status == 0 {
			status = http.StatusSeeOther
		}
		http.Redirect(writer, request, location, status)
		return
	}
	writer.Header().Set("Content-Type", response.contentType)
	if response.disposition != "" {
		writer.Header().Set("Content-Disposition", response.disposition)
	}
	digest := sha256.Sum256(response.body)
	writer.Header().Set("ETag", `"`+hex.EncodeToString(digest[:])+`"`)
	http.ServeContent(writer, request, "", time.Time{}, bytes.NewReader(response.body))
}

func (reader *cmsReader) unavailable(writer http.ResponseWriter) {
	writer.Header().Set("Retry-After", "2")
	http.Error(writer, "documentation is unavailable; use the admin editor to repair the project", http.StatusServiceUnavailable)
}

func (reader *cmsReader) cached(revision, key string) (readerResponse, bool) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.revision != revision {
		reader.revision, reader.bytes = revision, 0
		reader.cache, reader.order = make(map[string]readerResponse), nil
	}
	response, exists := reader.cache[key]
	return response, exists
}

func (reader *cmsReader) remember(revision, key string, response readerResponse) {
	if len(response.body) > 2<<20 || response.redirect != "" {
		return
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.revision != revision {
		return
	}
	if _, exists := reader.cache[key]; exists {
		return
	}
	for len(reader.order) >= 64 || reader.bytes+len(response.body) > readerCacheBytes {
		oldest := reader.order[0]
		reader.order = reader.order[1:]
		reader.bytes -= len(reader.cache[oldest].body)
		delete(reader.cache, oldest)
	}
	reader.cache[key] = response
	reader.order = append(reader.order, key)
	reader.bytes += len(response.body)
}

// readerJSON bounds both the encoded bytes and retained buffer capacity.
// A failed append leaves the previous contents intact and is never published.
type readerJSON struct{ data []byte }

func (output *readerJSON) appendJSON(value []byte) error {
	const limit = 16 << 20
	if len(value) > limit-len(output.data) {
		return content.ErrLimit
	}
	needed := len(output.data) + len(value)
	if needed > cap(output.data) {
		capacity := min(limit, max(needed, max(256, cap(output.data)*2)))
		next := make([]byte, len(output.data), capacity)
		copy(next, output.data)
		output.data = next
	}
	output.data = append(output.data, value...)
	return nil
}

func searchText(page document.Page) string {
	var text strings.Builder
	appendRaw := func(value string) {
		if text.Len() >= 32<<10 {
			return
		}
		remaining := (32 << 10) - text.Len()
		if len(value) > remaining {
			value = value[:remaining]
			for !utf8.ValidString(value) {
				value = value[:len(value)-1]
			}
		}
		text.WriteString(value)
	}
	appendText := func(value string) {
		appendRaw(value)
		appendRaw(" ")
	}
	var inlines func([]document.Inline)
	inlines = func(values []document.Inline) {
		for _, inline := range values {
			appendRaw(inline.Text)
			inlines(inline.Children)
		}
	}
	var blocks func([]document.Block)
	blocks = func(values []document.Block) {
		for _, block := range values {
			appendText(block.Text)
			inlines(block.Inlines)
			appendRaw(" ")
			blocks(block.Children)
		}
	}
	appendText(page.Title)
	blocks(page.Blocks)
	if page.API != nil {
		appendText(page.API.Description)
		for _, operation := range page.API.Operations {
			appendText(operation.Summary)
			appendText(operation.Description)
		}
	}
	return strings.Join(strings.Fields(text.String()), " ")
}
