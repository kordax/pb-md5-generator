package apiweb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

type PublishingPolicy struct {
	AutoPublish      bool
	GitPushOnPublish bool
}

type draftDTO struct {
	ID           string    `json:"id"`
	Revision     string    `json:"revision"`
	BaseRevision string    `json:"baseRevision"`
	Path         string    `json:"path"`
	To           string    `json:"to,omitempty"`
	Operation    string    `json:"operation"`
	Title        string    `json:"title,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type syncDTO struct {
	Backend     string `json:"backend"`
	Branch      string `json:"branch,omitempty"`
	Revision    string `json:"revision,omitempty"`
	Dirty       bool   `json:"dirty"`
	Ahead       int    `json:"ahead"`
	Behind      int    `json:"behind"`
	PendingPush bool   `json:"pendingPush"`
	LastCommit  string `json:"lastCommit,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

type saveResult struct {
	Revision  string    `json:"revision"`
	Published bool      `json:"published"`
	Document  *Document `json:"document,omitempty"`
	Draft     *draftDTO `json:"draft,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	Sync      *syncDTO  `json:"sync,omitempty"`
}

func (h *handler) serveContent(w http.ResponseWriter, r *http.Request) {
	user, err := Principal(r.Context())
	if err != nil || user == nil {
		serviceError(w, auth.ErrUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/documents":
		h.documents(w, r, user)
	case "/documents/register":
		h.registerDocument(w, r)
	case "/drafts":
		h.drafts(w, r, user)
	case "/preview":
		h.preview(w, r, user)
	case "/files", "/files/content", "/files/move", "/files/delete-preview", "/files/delete-tree":
		h.files(w, r, user)
	case "/files/rebase":
		h.rebaseSource(w, r)
	case "/files/history":
		h.sourceHistory(w, r)
	case "/files/restore":
		h.restoreSource(w, r)
	case "/branches", "/branches/checkout", "/sync", "/sync/status":
		h.storage(w, r, user)
	default:
		if r.URL.Path == "/storage" || strings.HasPrefix(r.URL.Path, "/storage/") {
			h.managedStorage(w, r, user)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) >= 2 && len(parts) <= 3 && validIdentifier(parts[1]) {
			if parts[0] == "documents" {
				action := ""
				if len(parts) == 3 {
					action = parts[2]
				}
				h.document(w, r, user, parts[1], action)
				return
			}
			if parts[0] == "drafts" && (len(parts) == 2 || parts[2] == "publish") {
				h.draft(w, r, user, parts[1], len(parts) == 3)
				return
			}
		}
		writeError(w, http.StatusNotFound, "not_found", "This resource was not found.", "", nil)
	}
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, char := range value {
		if char < 'a' || char > 'z' {
			if char < 'A' || char > 'Z' {
				if char < '0' || char > '9' {
					if char != '-' && char != '_' && char != '.' {
						return false
					}
				}
			}
		}
	}
	return value != "." && value != ".."
}

func validSourcePath(value string) bool {
	if value == "" || len(value) > 2048 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\:") || path.Clean(value) != value || value == "." || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if strings.HasPrefix(part, ".") || part == "" {
			return false
		}
	}
	return true
}

func revision(value string) bool {
	if len(value) != 64 {
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

func ifMatch(r *http.Request) string {
	value := r.Header.Values("If-Match")
	if len(value) != 1 {
		return ""
	}
	match := value[0]
	if len(match) == 66 && match[0] == '"' && match[65] == '"' {
		match = match[1:65]
	}
	if !revision(match) {
		return ""
	}
	return match
}

func reserved(name string) bool {
	first, _, _ := strings.Cut(name, "/")
	return strings.EqualFold(name, "docs.json") || strings.EqualFold(first, "themes")
}

func permitted(user *auth.User, action auth.Action, name string) bool {
	return validSourcePath(name) && (!reserved(name) || auth.Can(user, auth.ActionAdmin, "")) && auth.Can(user, action, name)
}

func actor(user *auth.User) content.Actor {
	return content.Actor{ID: user.ID, Name: user.DisplayName}
}

func changesOf(draft content.Draft) []content.Change {
	if len(draft.Changes) != 0 {
		return draft.Changes
	}
	return []content.Change{draft.Change}
}

func draftVisible(draft content.Draft, user *auth.User, write bool) bool {
	if draft.Actor.ID != user.ID {
		return false
	}
	for _, change := range changesOf(draft) {
		if !permitted(user, auth.ActionRead, change.Path) || change.To != "" && !permitted(user, auth.ActionRead, change.To) {
			return false
		}
		if write && (!permitted(user, auth.ActionWrite, change.Path) || change.To != "" && !permitted(user, auth.ActionWrite, change.To)) {
			return false
		}
	}
	return true
}

func describeDraft(draft content.Draft) draftDTO {
	change := changesOf(draft)[0]
	return draftDTO{ID: draft.ID, Revision: draft.Revision, BaseRevision: draft.BaseRevision, Path: change.Path, To: change.To, Operation: change.Operation, Title: change.Title, Branch: draft.Branch, UpdatedAt: draft.UpdatedAt}
}

func describeSync(status content.SyncStatus, current string) syncDTO {
	return syncDTO{Backend: status.Backend, Branch: status.Branch, Revision: current, Dirty: status.Dirty, Ahead: status.Ahead, Behind: status.Behind, PendingPush: status.State == "pending" || status.Retryable || status.Ahead > 0, LastCommit: status.Head, LastError: status.Error}
}

func describeDocument(entry content.Entry, user *auth.User) Document {
	return Document{ID: entry.ID, Path: entry.Path, Title: entry.Title, Kind: entry.Kind, OutputPath: entry.OutputPath, Revision: entry.Revision, PublishedRevision: entry.Revision, CanWrite: permitted(user, auth.ActionWrite, entry.Path)}
}

func (h *handler) conflict(w http.ResponseWriter, r *http.Request) {
	current := ""
	if project, err := h.opts.Content.Project(r.Context()); err == nil {
		current = project.Revision
	}
	writeError(w, http.StatusConflict, "conflict", "This content changed elsewhere. Your draft has been kept. Compare it with the latest version before retrying.", current, nil)
}

func (h *handler) operationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, content.ErrConflict) {
		h.conflict(w, r)
		return
	}
	serviceError(w, err)
}

func (h *handler) policy(ctx context.Context) (PublishingPolicy, error) {
	if h.opts.Publishing == nil {
		return PublishingPolicy{}, nil
	}
	return h.opts.Publishing(ctx)
}

func (h *handler) save(w http.ResponseWriter, r *http.Request, user *auth.User, input content.DraftInput, document *Document, publish, push bool, message string, status int) {
	if !revision(input.BaseRevision) || len(message) > 256 || !utf8.ValidString(message) || strings.IndexFunc(message, unicode.IsControl) >= 0 {
		serviceError(w, content.ErrInvalid)
		return
	}
	policy, err := h.policy(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	draft, err := h.opts.Content.SaveDraft(r.Context(), input, actor(user))
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	h.finishSave(w, r, user, draft, document, publish, push, message, status, policy)
}

func (h *handler) finishSave(w http.ResponseWriter, r *http.Request, user *auth.User, draft content.Draft, document *Document, publish, push bool, message string, status int, policy PublishingPolicy) {
	result := saveResult{Revision: draft.BaseRevision, Document: document}
	dto := describeDraft(draft)
	result.Draft = &dto
	if document != nil {
		document.Revision, document.BaseRevision = draft.BaseRevision, draft.BaseRevision
		document.Draft, document.DraftID, document.DraftRevision = true, draft.ID, draft.Revision
	}
	if publish || policy.AutoPublish && !strings.HasSuffix(r.URL.Path, "/rebase") {
		if !h.publishResult(w, r, user, draft, message, push || policy.GitPushOnPublish, &result) {
			return
		}
	}
	writeJSON(w, status, result)
}

func (h *handler) publishResult(w http.ResponseWriter, r *http.Request, user *auth.User, draft content.Draft, message string, push bool, result *saveResult) bool {
	if !draftVisible(draft, user, true) {
		serviceError(w, auth.ErrForbidden)
		return false
	}
	published, err := h.opts.Content.Publish(r.Context(), content.PublishInput{ID: draft.ID, Revision: draft.Revision, Message: message, Push: push}, actor(user))
	if err != nil {
		status, code, message, current := http.StatusInternalServerError, "unavailable", "Publication could not finish. The saved draft and last successful site are retained.", ""
		switch {
		case errors.Is(err, content.ErrConflict):
			status, code, message = http.StatusConflict, "conflict", "This content changed elsewhere. Your draft has been kept. Compare it with the latest version before retrying."
			if project, projectErr := h.opts.Content.Project(r.Context()); projectErr == nil {
				current = project.Revision
			}
		case errors.Is(err, content.ErrInvalid):
			status, code, message = http.StatusUnprocessableEntity, "invalid_content", "Publication failed validation. The saved draft and last successful site are retained. Preview the source to review its diagnostics."
		case errors.Is(err, content.ErrLimit):
			status, code, message = http.StatusRequestEntityTooLarge, "too_large", "Publication exceeds the workspace resource limit. Your draft has been kept."
		case errors.Is(err, auth.ErrForbidden):
			status, code, message = http.StatusForbidden, "forbidden", "You do not have permission to publish this content."
		case errors.Is(err, auth.ErrUnauthorized):
			status, code, message = http.StatusUnauthorized, "unauthorized", "Sign in to continue."
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			status, code, message = http.StatusRequestTimeout, "timeout", "Publication was canceled or timed out. Your saved draft remains available."
		}
		description := describeDraft(draft)
		writeJSON(w, status, struct {
			Error errorDetail `json:"error"`
		}{errorDetail{Code: code, Message: message, CurrentRevision: current, Draft: &description, Document: result.Document}})
		return false
	}
	result.Revision, result.Published, result.Commit = published.Revision, published.Published, published.Commit
	sync := describeSync(published.Sync, published.Revision)
	result.Sync, result.Draft = &sync, nil
	if result.Document != nil {
		result.Document.Revision, result.Document.PublishedRevision = published.Revision, published.Revision
		result.Document.Draft, result.Document.DraftID, result.Document.DraftRevision, result.Document.BaseRevision = false, "", "", ""
	}
	return true
}

func randomDocumentID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "doc-" + hex.EncodeToString(value), nil
}
