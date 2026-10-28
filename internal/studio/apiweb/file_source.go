package apiweb

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

func sourceTextPath(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".proto", ".json", ".yaml", ".yml", ".html", ".htm", ".txt", ".go", ".js", ".ts", ".sh", ".sql", ".css", ".xml":
		return true
	default:
		return false
	}
}

func draftIdentifier(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}

func sourceQuery(r *http.Request) (string, string, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) < 1 || len(values) > 2 || len(values["path"]) != 1 || !validSourcePath(values["path"][0]) {
		return "", "", false
	}
	draft := ""
	if items, exists := values["draftId"]; exists {
		if len(items) != 1 || !draftIdentifier(items[0]) {
			return "", "", false
		}
		draft = items[0]
	}
	if len(values) == 2 && draft == "" {
		return "", "", false
	}
	return values["path"][0], draft, true
}

func sourceDraftHeaders(r *http.Request) (string, string, bool) {
	ids, revisions := r.Header.Values("X-Draft-ID"), r.Header.Values("X-Draft-Revision")
	if len(r.Header.Values("X-Draft-Rebase")) != 0 {
		return "", "", false
	}
	if len(ids) == 0 && len(revisions) == 0 {
		return "", "", true
	}
	if len(ids) != 1 || len(revisions) != 1 || !draftIdentifier(ids[0]) || !revision(revisions[0]) {
		return "", "", false
	}
	return ids[0], revisions[0], true
}

func (h *handler) fileContent(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, "GET, HEAD, PUT, DELETE") {
		return
	}
	name, ok := queryPath(r)
	draftID := ""
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		name, draftID, ok = sourceQuery(r)
	}
	if !ok {
		serviceError(w, content.ErrInvalid)
		return
	}
	user, ok := h.filePrincipal(w, r, name, false)
	if !ok {
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if draftID != "" {
			h.downloadDraft(w, r, name, draftID)
		} else {
			h.download(w, r, name)
		}
		return
	}
	if !permitted(user, auth.ActionWrite, name) {
		serviceError(w, auth.ErrForbidden)
		return
	}
	if r.Method == http.MethodDelete {
		h.deleteFile(w, r, user, name)
		return
	}
	draftID, draftRevision, ok := sourceDraftHeaders(r)
	if !ok || !revision(ifMatch(r)) || draftID != "" && !sourceTextPath(name) {
		serviceError(w, content.ErrInvalid)
		return
	}
	h.uploadFile(w, r, user, name, draftID, draftRevision)
}

func (h *handler) uploadFile(w http.ResponseWriter, r *http.Request, user *auth.User, name, draftID, draftRevision string) {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/octet-stream" || len(params) != 0 || r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_request", "Upload original bytes with Content-Type application/octet-stream.", "", nil)
		return
	}
	limit := int64(content.MaxBinaryBytes)
	if sourceTextPath(name) {
		limit = content.MaxTextBytes
	}
	if r.ContentLength > limit {
		serviceError(w, content.ErrLimit)
		return
	}
	defer r.Body.Close()
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			serviceError(w, content.ErrLimit)
		} else {
			serviceError(w, content.ErrInvalid)
		}
		return
	}
	if err := r.Context().Err(); err != nil {
		serviceError(w, err)
		return
	}
	if draftID == "" {
		h.stage(w, r, user, content.Change{Operation: "write", Path: name, Data: data}, ifMatch(r), nil)
		return
	}
	h.resaveFile(w, r, name, draftID, draftRevision, data)
}

func (h *handler) resaveFile(w http.ResponseWriter, r *http.Request, name, id, expected string, data []byte) {
	draft, err := h.opts.Content.ReadDraft(r.Context(), id)
	if err != nil {
		serviceError(w, err)
		return
	}
	user, ok := h.filePrincipal(w, r, name, false)
	if !ok {
		return
	}
	changes := changesOf(draft)
	if !draftVisible(draft, user, true) || len(changes) != 1 || changes[0].Operation != "write" || changes[0].Path != name {
		serviceError(w, content.ErrNotFound)
		return
	}
	if draft.Revision != expected || draft.BaseRevision != ifMatch(r) {
		h.conflict(w, r)
		return
	}
	change := changes[0]
	change.Content, change.Data = nil, data
	// The stored base is authoritative. A changed published project does not
	// silently rebase a successful owner-CAS save; publication may still conflict.
	h.save(w, r, user, content.DraftInput{ID: draft.ID, Revision: expected, BaseRevision: draft.BaseRevision, Change: change}, nil, false, false, "", http.StatusOK)
}

func (h *handler) downloadDraft(w http.ResponseWriter, r *http.Request, name, id string) {
	if !sourceTextPath(name) {
		serviceError(w, content.ErrInvalid)
		return
	}
	draft, err := h.opts.Content.ReadDraft(r.Context(), id)
	if err != nil {
		serviceError(w, err)
		return
	}
	current, err := h.opts.Content.MatchesCurrentBranch(r.Context(), draft.Branch)
	if err != nil {
		serviceError(w, err)
		return
	}
	user, ok := h.filePrincipal(w, r, name, false)
	if !ok {
		return
	}
	changes := changesOf(draft)
	if !current || !draftVisible(draft, user, true) || len(changes) != 1 || changes[0].Operation != "write" || changes[0].Path != name {
		serviceError(w, content.ErrNotFound)
		return
	}
	data := changes[0].Data
	if changes[0].Content != nil {
		data = []byte(*changes[0].Content)
	}
	if len(data) > content.MaxTextBytes || !utf8.Valid(data) {
		serviceError(w, content.ErrInvalid)
		return
	}
	w.Header().Set("X-Base-Revision", draft.BaseRevision)
	writeFileDownload(w, r, name, data, draft.Revision)
}

func writeFileDownload(w http.ResponseWriter, r *http.Request, name string, data []byte, revision string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("ETag", "\""+revision+"\"")
	w.Header().Set("Cache-Control", "no-store")
	complete := r.Clone(r.Context())
	for _, header := range []string{"Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		complete.Header.Del(header)
	}
	http.ServeContent(w, complete, path.Base(name), time.Time{}, bytes.NewReader(data))
}
