package apiweb

import (
	"net/http"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
)

type sourceHistoryEntry struct {
	ID         string    `json:"id"`
	Path       string    `json:"path"`
	Revision   string    `json:"revision"`
	Author     string    `json:"author"`
	CreatedAt  time.Time `json:"createdAt"`
	Size       int64     `json:"size"`
	Deleted    bool      `json:"deleted"`
	Restorable bool      `json:"restorable"`
}

type sourceHistoryResult struct {
	Path      string               `json:"path"`
	Revision  string               `json:"revision"`
	Revisions []sourceHistoryEntry `json:"revisions"`
}

func sourceOnlyPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	name, draftID, ok := sourceQuery(r)
	if !ok || draftID != "" {
		serviceError(w, content.ErrInvalid)
		return "", false
	}
	return name, true
}

func (h *handler) sourceHistory(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, "GET, HEAD") {
		return
	}
	name, ok := sourceOnlyPath(w, r)
	if !ok {
		return
	}
	user, ok := h.filePrincipal(w, r, name, false)
	if !ok {
		return
	}
	history, err := h.opts.Content.SourceHistory(r.Context(), name, actor(user))
	if err != nil {
		serviceError(w, err)
		return
	}
	user, ok = h.filePrincipal(w, r, name, false)
	if !ok {
		return
	}
	items := []sourceHistoryEntry{}
	for _, entry := range history.Items {
		if entry.Path != name || !permitted(user, auth.ActionRead, entry.Path) {
			continue
		}
		items = append(items, sourceHistoryEntry{ID: entry.ID, Path: entry.Path, Revision: entry.Revision, Author: entry.Actor.Name, CreatedAt: entry.CreatedAt, Size: entry.Size, Deleted: entry.Deleted, Restorable: !entry.Deleted && permitted(user, auth.ActionWrite, name)})
	}
	w.Header().Set("ETag", "\""+history.Revision+"\"")
	writeJSON(w, http.StatusOK, sourceHistoryResult{Path: name, Revision: history.Revision, Revisions: items})
}

func (h *handler) rebaseSource(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, "POST") {
		return
	}
	name, ok := sourceOnlyPath(w, r)
	if !ok {
		return
	}
	id, expected, headersOK := sourceDraftHeaders(r)
	if !sourceTextPath(name) || !headersOK || !revision(ifMatch(r)) {
		serviceError(w, content.ErrInvalid)
		return
	}
	user, ok := h.filePrincipal(w, r, name, true)
	if !ok {
		return
	}
	data, ok := readSourceBytes(w, r, content.MaxTextBytes)
	if !ok {
		return
	}
	draft, err := h.opts.Content.RebaseSource(r.Context(), content.SourceDraftInput{Path: name, Revision: ifMatch(r), DraftID: id, DraftRevision: expected, Data: data}, actor(user))
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	h.completeSourceSave(w, r, user, draft, PublishingPolicy{})
}

func (h *handler) restoreSource(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, "POST") {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		serviceError(w, content.ErrInvalid)
		return
	}
	var input struct {
		Path      string `json:"path"`
		HistoryID string `json:"historyId"`
		Revision  string `json:"revision"`
	}
	if err := httpjson.Decode(w, r, &input, 32<<10); err != nil {
		writeError(w, httpjson.Status(err), "invalid_request", "Send one valid JSON object using the documented fields and resource limits.", "", nil)
		return
	}
	if !validSourcePath(input.Path) || !draftIdentifier(input.HistoryID) || !revision(input.Revision) {
		serviceError(w, content.ErrInvalid)
		return
	}
	user, ok := h.filePrincipal(w, r, input.Path, true)
	if !ok {
		return
	}
	draft, err := h.opts.Content.RestoreSource(r.Context(), content.RestoreSourceInput{Path: input.Path, HistoryID: input.HistoryID, Revision: input.Revision}, actor(user))
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	h.completeSourceSave(w, r, user, draft, PublishingPolicy{})
}

func (h *handler) completeSourceSave(w http.ResponseWriter, r *http.Request, user *auth.User, draft content.Draft, policy PublishingPolicy) {
	description := describeDraft(draft)
	result := saveResult{Revision: draft.BaseRevision, Draft: &description}
	if policy.AutoPublish && !h.publishResult(w, r, user, draft, "", policy.GitPushOnPublish, &result) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}
