package apiweb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
	"github.com/kordax/pb-md5-generator/internal/uiassets"
)

const maxJSONBody = 8 << 20
const noncePlaceholder = "__PB_DOCS_CSP_NONCE__"

var assetNames = []string{"index.html", "studio.css", "app.mjs", "core.mjs", "editor.js", "themes.mjs", "storage.mjs", "navigation.mjs", "files.mjs", "editor.js.LEGAL.txt", "THIRD_PARTY_NOTICES.txt"}

type handler struct {
	opts       Options
	base       string
	api        string
	admin      string
	assets     map[string][]byte
	contract   []byte
	slots      chan struct{}
	contentAPI http.Handler
}

func newHTTPHandler(options Options) (http.Handler, error) {
	if options.Auth == nil || options.Content == nil || options.Assets == nil || len(options.Contract) == 0 || options.Preview == nil || options.Appearance == nil {
		return nil, errors.New("CMS HTTP requires authentication, content, assets, contract, preview and appearance dependencies")
	}
	public, err := url.Parse(options.PublicURL)
	if err != nil || public.User != nil || public.Hostname() == "" || public.RawQuery != "" || public.ForceQuery || public.Fragment != "" || public.Opaque != "" || public.Scheme != "https" && (public.Scheme != "http" || !strings.EqualFold(public.Hostname(), "localhost")) {
		return nil, errors.New("CMS public URL must use HTTPS or localhost HTTP")
	}
	base := strings.TrimSuffix(options.BasePath, "/")
	publicBase := strings.TrimSuffix(public.Path, "/")
	if base == "" {
		base = publicBase
	}
	if base != publicBase || base != "" && (!strings.HasPrefix(base, "/") || path.Clean(base) != base || strings.ContainsAny(base, "\\:") || strings.IndexFunc(base, unicode.IsControl) >= 0) || public.RawPath != "" {
		return nil, errors.New("CMS base path must match the clean public URL path")
	}
	if len(options.Contract) > 1<<20 || !utf8.Valid(options.Contract) {
		return nil, errors.New("CMS API contract is invalid or too large")
	}
	h := &handler{opts: options, base: base, api: base + "/_docs/api/v1", admin: base + "/_docs/admin", assets: make(map[string][]byte), contract: bytes.Clone(options.Contract), slots: make(chan struct{}, 4)}
	for index, name := range assetNames {
		data, err := fs.ReadFile(options.Assets, name)
		if errors.Is(err, fs.ErrNotExist) && index >= 9 {
			continue
		}
		if err != nil || len(data) == 0 || len(data) > 4<<20 {
			return nil, fmt.Errorf("CMS administration asset %s is missing or invalid", name)
		}
		h.assets[name] = bytes.Clone(data)
	}
	for _, name := range uiassets.Names() {
		data, _, ok := uiassets.Resource(name)
		if !ok {
			return nil, fmt.Errorf("compiled administration asset %s is unavailable", name)
		}
		h.assets["assets/"+name] = data
	}
	if bytes.Count(h.assets["index.html"], []byte(noncePlaceholder)) != 1 {
		return nil, errors.New("CMS administration index must contain exactly one CSP nonce placeholder")
	}
	h.contentAPI = http.HandlerFunc(h.serveContent)
	return h, nil
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "same-origin")
	writer.Header().Set("X-Frame-Options", "DENY")
	if request.Method == http.MethodHead {
		writer = headWriter{writer}
	}
	current := request.URL.Path
	clean := path.Clean(current)
	if !utf8.ValidString(current) || strings.IndexFunc(current, unicode.IsControl) >= 0 || strings.Contains(current, "\\") || current != clean && current != clean+"/" || strings.Contains(strings.ToLower(request.URL.EscapedPath()), "%2f") {
		writeError(writer, http.StatusBadRequest, "invalid_path", "The request path is invalid.", "", nil)
		return
	}
	if current == h.base+"/_docs/health" {
		if allowMethod(writer, request, "GET, HEAD") {
			writeJSON(writer, http.StatusOK, struct {
				Status string `json:"status"`
			}{Status: "ready"})
		}
		return
	}
	if !h.allowedHost(request.Host) {
		writeError(writer, http.StatusForbidden, "host_denied", "Use the configured workspace address.", "", nil)
		return
	}
	if current == h.admin {
		if !allowMethod(writer, request, "GET, HEAD") {
			return
		}
		http.Redirect(writer, request, (&url.URL{Path: h.admin + "/"}).String(), http.StatusTemporaryRedirect)
		return
	}
	if strings.HasPrefix(current, h.admin+"/") {
		h.serveAsset(writer, request, strings.TrimPrefix(current, h.admin+"/"))
		return
	}
	if current == h.api || strings.HasPrefix(current, h.api+"/") {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		relative := strings.TrimPrefix(current, h.api)
		if relative == "/session" || relative == "/users" || strings.HasPrefix(relative, "/users/") {
			h.opts.Auth.Handler().ServeHTTP(writer, withPath(request, relative))
			return
		}
		if relative == "/settings" || relative == "/themes" || strings.HasPrefix(relative, "/themes/") {
			role := auth.RoleViewer
			if request.Method != http.MethodGet && request.Method != http.MethodHead {
				role = auth.RoleAdmin
			}
			h.authorized(role, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.opts.Appearance.ServeHTTP(w, withPath(r, relative)) })).ServeHTTP(writer, request)
			return
		}
		if relative == "/openapi.yaml" {
			h.authorized(auth.RoleViewer, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !allowMethod(w, r, "GET, HEAD") {
					return
				}
				w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
				w.Header().Set("Content-Length", strconv.Itoa(len(h.contract)))
				complete := r.Clone(r.Context())
				for _, header := range []string{"Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
					complete.Header.Del(header)
				}
				http.ServeContent(w, complete, "openapi.yaml", time.Time{}, bytes.NewReader(h.contract))
			})).ServeHTTP(writer, request)
			return
		}
		role := auth.RoleViewer
		if relative == "/storage" || strings.HasPrefix(relative, "/storage/") || relative == "/sync" || relative == "/branches" || relative == "/branches/checkout" {
			role = auth.RoleAdmin
		} else if request.Method != http.MethodGet && request.Method != http.MethodHead && relative != "/preview" {
			role = auth.RoleEditor
		}
		h.authorized(role, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.contentAPI.ServeHTTP(w, withPath(r, relative)) })).ServeHTTP(writer, request)
		return
	}
	if h.base != "" && current != h.base && !strings.HasPrefix(current, h.base+"/") {
		writeError(writer, http.StatusNotFound, "not_found", "This resource was not found.", "", nil)
		return
	}
	if h.opts.Reader == nil {
		if allowMethod(writer, request, "GET, HEAD") {
			http.Redirect(writer, request, (&url.URL{Path: h.admin + "/"}).String(), http.StatusTemporaryRedirect)
		}
		return
	}
	if (request.Method == http.MethodGet || request.Method == http.MethodHead) && strings.Contains(strings.ToLower(request.Header.Get("Accept")), "text/html") && (current == h.base || strings.HasSuffix(current, "/") || strings.HasSuffix(current, ".html")) {
		user, err := h.opts.Auth.Resolve(request)
		if err != nil {
			serviceError(writer, err)
			return
		}
		if user == nil {
			http.Redirect(writer, request, (&url.URL{Path: h.admin + "/"}).String(), http.StatusSeeOther)
			return
		}
	}
	h.authorized(auth.RoleViewer, false, h.opts.Reader).ServeHTTP(writer, request)
}

func (h *handler) allowedHost(host string) bool {
	public, _ := url.Parse(h.opts.PublicURL)
	candidate, err := url.Parse(public.Scheme + "://" + host)
	if err != nil || candidate.Hostname() == "" || candidate.User != nil || candidate.Path != "" || candidate.RawQuery != "" || candidate.Fragment != "" || candidate.Opaque != "" || !strings.EqualFold(public.Hostname(), candidate.Hostname()) {
		return false
	}
	port := func(value *url.URL) string {
		if value.Port() != "" {
			return value.Port()
		}
		if value.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return port(public) == port(candidate)
}

func withPath(request *http.Request, value string) *http.Request {
	copy := request.Clone(request.Context())
	copy.URL.Path, copy.URL.RawPath = value, ""
	return copy
}

type headWriter struct{ http.ResponseWriter }

func (h headWriter) Write(data []byte) (int, error) { return len(data), nil }

func (h *handler) authorized(role auth.Role, contentRequest bool, next http.Handler) http.Handler {
	return h.opts.Auth.Require(role, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		original := request
		resolve := principalResolver(func(ctx context.Context) (*auth.User, error) {
			user, err := h.opts.Auth.Resolve(original.WithContext(ctx))
			if err == nil && user == nil {
				err = auth.ErrUnauthorized
			}
			return user, err
		})
		request = request.WithContext(context.WithValue(request.Context(), principalKey{}, resolve))
		if contentRequest {
			select {
			case h.slots <- struct{}{}:
				defer func() { <-h.slots }()
			default:
				writer.Header().Set("Retry-After", "1")
				writeError(writer, http.StatusServiceUnavailable, "busy", "The workspace is busy. Please try again shortly.", "", nil)
				return
			}
		}
		next.ServeHTTP(writer, request)
	}))
}

func (h *handler) serveAsset(writer http.ResponseWriter, request *http.Request, name string) {
	writer.Header().Set("Cache-Control", "no-store")
	if !allowMethod(writer, request, "GET, HEAD") {
		return
	}
	if name == "" {
		name = "index.html"
	}
	data, exists := h.assets[name]
	if !exists {
		writeError(writer, http.StatusNotFound, "not_found", "This resource was not found.", "", nil)
		return
	}
	if name == "index.html" {
		value := make([]byte, 24)
		if _, err := rand.Read(value); err != nil {
			writeError(writer, http.StatusInternalServerError, "unavailable", "The workspace is unavailable. Please try again.", "", nil)
			return
		}
		nonce := base64.RawStdEncoding.EncodeToString(value)
		data = bytes.Replace(data, []byte(noncePlaceholder), []byte(nonce), 1)
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-"+nonce+"'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-src 'self'; frame-ancestors 'none'")
	}
	media := mime.TypeByExtension(path.Ext(name))
	if path.Ext(name) == ".mjs" || name == "editor.js" {
		media = "text/javascript; charset=utf-8"
	}
	if path.Ext(name) == ".woff2" {
		media = "font/woff2"
	} else if path.Ext(name) == ".md" {
		media = "text/markdown; charset=utf-8"
	}
	if media == "" {
		media = "text/plain; charset=utf-8"
	}
	writer.Header().Set("Content-Type", media)
	writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
	http.ServeContent(writer, request, name, time.Time{}, bytes.NewReader(data))
}

func allowMethod(writer http.ResponseWriter, request *http.Request, methods string) bool {
	for _, method := range strings.Split(methods, ", ") {
		if request.Method == method {
			return true
		}
	}
	writer.Header().Set("Allow", methods)
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "This HTTP method is not supported here.", "", nil)
	return false
}

type errorDetail struct {
	Code            string       `json:"code"`
	Message         string       `json:"message"`
	CurrentRevision string       `json:"currentRevision,omitempty"`
	Diagnostics     []Diagnostic `json:"diagnostics,omitempty"`
	Draft           *draftDTO    `json:"draft,omitempty"`
	Document        *Document    `json:"document,omitempty"`
}

func writeError(writer http.ResponseWriter, status int, code, message, revision string, diagnostics []Diagnostic) {
	writeJSON(writer, status, struct {
		Error errorDetail `json:"error"`
	}{errorDetail{Code: code, Message: message, CurrentRevision: revision, Diagnostics: diagnostics}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil || len(data) > 32<<20 {
		status = http.StatusInternalServerError
		data = []byte(`{"error":{"code":"unavailable","message":"The response could not be prepared."}}`)
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
	writer.WriteHeader(status)
	_, _ = writer.Write(data)
}

func serviceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		writeError(writer, http.StatusUnauthorized, "unauthorized", "Sign in to continue.", "", nil)
	case errors.Is(err, auth.ErrForbidden):
		writeError(writer, http.StatusForbidden, "forbidden", "You do not have permission to perform this action.", "", nil)
	case errors.Is(err, content.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "This resource was not found.", "", nil)
	case errors.Is(err, content.ErrConflict):
		writeError(writer, http.StatusConflict, "conflict", "This content changed elsewhere. Your draft has been kept. Review the latest version before retrying.", "", nil)
	case errors.Is(err, content.ErrLimit):
		writeError(writer, http.StatusRequestEntityTooLarge, "too_large", "This operation exceeds the workspace resource limit.", "", nil)
	case errors.Is(err, content.ErrInvalid):
		writeError(writer, http.StatusBadRequest, "invalid", "The content request is invalid. Check the path, format and revision.", "", nil)
	case errors.Is(err, content.ErrUnsupported):
		writeError(writer, http.StatusConflict, "unsupported", "This operation is unavailable for the configured storage.", "", nil)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(writer, http.StatusRequestTimeout, "timeout", "The operation was canceled or timed out. Your saved drafts remain available.", "", nil)
	default:
		writeError(writer, http.StatusInternalServerError, "unavailable", "The workspace is unavailable. Please try again.", "", nil)
	}
}

func decode(writer http.ResponseWriter, request *http.Request, target any) bool {
	if err := httpjson.Decode(writer, request, target, maxJSONBody); err != nil {
		writeError(writer, httpjson.Status(err), "invalid_request", "Send one valid JSON object using the documented fields and resource limits.", "", nil)
		return false
	}
	return true
}
