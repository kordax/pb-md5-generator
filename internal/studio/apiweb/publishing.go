package apiweb

import (
	"net/http"
	"strings"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

func (h *handler) authorizeChanges(r *http.Request, user *auth.User, changes []content.Change) error {
	project, err := h.opts.Content.Project(r.Context())
	if err != nil {
		return err
	}
	for _, change := range changes {
		if !permitted(user, auth.ActionRead, change.Path) || !permitted(user, auth.ActionWrite, change.Path) || change.To != "" && (!permitted(user, auth.ActionRead, change.To) || !permitted(user, auth.ActionWrite, change.To)) {
			return auth.ErrForbidden
		}
		if change.Operation != "move" && change.Operation != "delete" && change.Operation != "rmdir" && change.Operation != "delete-tree" {
			continue
		}
		for _, entry := range project.Files {
			if !strings.HasPrefix(entry.Path, change.Path+"/") {
				continue
			}
			if !permitted(user, auth.ActionRead, entry.Path) || !permitted(user, auth.ActionWrite, entry.Path) {
				return auth.ErrForbidden
			}
			if change.To != "" {
				destination := change.To + strings.TrimPrefix(entry.Path, change.Path)
				if !permitted(user, auth.ActionRead, destination) || !permitted(user, auth.ActionWrite, destination) {
					return auth.ErrForbidden
				}
			}
		}
	}
	return nil
}

func (h *handler) stage(w http.ResponseWriter, r *http.Request, user *auth.User, change content.Change, base string, document *Document) {
	if !revision(base) || !validSourcePath(change.Path) || change.To != "" && !validSourcePath(change.To) {
		serviceError(w, content.ErrInvalid)
		return
	}
	if err := h.authorizeChanges(r, user, []content.Change{change}); err != nil {
		serviceError(w, err)
		return
	}
	project, err := h.opts.Content.Project(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	if project.Revision != base {
		h.conflict(w, r)
		return
	}
	status := http.StatusOK
	if change.Operation == "mkdir" {
		status = http.StatusCreated
	}
	h.save(w, r, user, content.DraftInput{BaseRevision: base, Change: change}, document, false, false, "", status)
}

func (h *handler) drafts(w http.ResponseWriter, r *http.Request, user *auth.User) {
	if !allowMethod(w, r, "GET, HEAD") {
		return
	}
	drafts, err := h.ownedDrafts(r, user)
	if err != nil {
		serviceError(w, err)
		return
	}
	items := []draftDTO{}
	for _, draft := range drafts {
		items = append(items, describeDraft(draft))
	}
	writeJSON(w, http.StatusOK, struct {
		Drafts []draftDTO `json:"drafts"`
	}{items})
}

func (h *handler) draft(w http.ResponseWriter, r *http.Request, user *auth.User, id string, publish bool) {
	methods := "DELETE"
	if publish {
		methods = "POST"
	}
	if !allowMethod(w, r, methods) {
		return
	}
	draft, err := h.opts.Content.ReadDraft(r.Context(), id)
	if err != nil || !draftVisible(draft, user, false) {
		serviceError(w, content.ErrNotFound)
		return
	}
	if !publish {
		if !revision(ifMatch(r)) {
			serviceError(w, content.ErrInvalid)
			return
		}
		if err := h.opts.Content.DiscardDraft(r.Context(), id, ifMatch(r), actor(user)); err != nil {
			h.operationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		Revision string `json:"revision"`
		Message  string `json:"message"`
		Push     bool   `json:"push"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !revision(input.Revision) || len(input.Message) > 256 {
		serviceError(w, content.ErrInvalid)
		return
	}
	if draft.Revision != input.Revision {
		h.conflict(w, r)
		return
	}
	if input.Push && !auth.Can(user, auth.ActionAdmin, "") {
		serviceError(w, auth.ErrForbidden)
		return
	}
	if err := h.authorizeChanges(r, user, changesOf(draft)); err != nil {
		serviceError(w, err)
		return
	}
	policy, err := h.policy(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	result := saveResult{}
	if !h.publishResult(w, r, user, draft, input.Message, input.Push || policy.GitPushOnPublish, &result) {
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) history(w http.ResponseWriter, r *http.Request, user *auth.User, state documentState) {
	revisions, err := h.opts.Content.History(r.Context(), state.doc.Path)
	if err != nil {
		serviceError(w, err)
		return
	}
	type entry struct {
		ID        string    `json:"id"`
		Path      string    `json:"path"`
		Revision  string    `json:"revision"`
		Author    string    `json:"author,omitempty"`
		CreatedAt time.Time `json:"createdAt"`
		Size      int64     `json:"size"`
	}
	items := []entry{}
	for _, revision := range revisions {
		if permitted(user, auth.ActionRead, revision.Path) {
			items = append(items, entry{ID: revision.ID, Path: revision.Path, Revision: revision.Revision, Author: revision.Actor.Name, CreatedAt: revision.CreatedAt, Size: revision.Size})
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Revisions []entry `json:"revisions"`
	}{items})
}

func (h *handler) restore(w http.ResponseWriter, r *http.Request, user *auth.User, state documentState) {
	var input struct {
		HistoryID string `json:"historyId"`
		Revision  string `json:"revision"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !revision(input.Revision) || !validIdentifier(input.HistoryID) {
		serviceError(w, content.ErrInvalid)
		return
	}
	if state.draft != nil || input.Revision != state.project.Revision {
		h.conflict(w, r)
		return
	}
	revisions, err := h.opts.Content.History(r.Context(), state.doc.Path)
	if err != nil {
		serviceError(w, err)
		return
	}
	found := false
	for _, revision := range revisions {
		if revision.ID == input.HistoryID && permitted(user, auth.ActionRead, revision.Path) {
			found = true
			break
		}
	}
	if !found {
		serviceError(w, content.ErrNotFound)
		return
	}
	draft, err := h.opts.Content.Restore(r.Context(), state.doc.Path, input.HistoryID, input.Revision, actor(user))
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	description := describeDraft(draft)
	result := saveResult{Revision: draft.BaseRevision, Draft: &description}
	changes := changesOf(draft)
	if len(changes) == 1 && changes[0].Operation == "write" && changes[0].Content != nil {
		state.doc.Content = *changes[0].Content
		state.doc.Draft, state.doc.DraftID, state.doc.DraftRevision, state.doc.BaseRevision = true, draft.ID, draft.Revision, draft.BaseRevision
		result.Document = &state.doc
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) storage(w http.ResponseWriter, r *http.Request, user *auth.User) {
	if r.URL.Path == "/sync/status" {
		if !allowMethod(w, r, "GET, HEAD") {
			return
		}
		h.storageStatus(w, r)
		return
	}
	if !storageAccess(user) {
		serviceError(w, auth.ErrForbidden)
		return
	}
	if r.URL.RawQuery != "" {
		serviceError(w, content.ErrInvalid)
		return
	}
	if r.URL.Path == "/branches" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		branches, err := h.opts.Content.Branches(r.Context())
		if err != nil {
			serviceError(w, err)
			return
		}
		type branch struct {
			Name    string `json:"name"`
			Current bool   `json:"current"`
			Commit  string `json:"commit,omitempty"`
		}
		items := []branch{}
		for _, item := range branches {
			items = append(items, branch{Name: item.Name, Current: item.Current, Commit: item.Head})
		}
		writeJSON(w, http.StatusOK, struct {
			Branches []branch `json:"branches"`
		}{items})
		return
	}
	methods := "POST"
	if r.URL.Path == "/branches" {
		methods = "GET, HEAD, POST"
	}
	if !allowMethod(w, r, methods) {
		return
	}
	var input struct {
		Name     string
		Action   string
		Revision string
	}
	if r.URL.Path == "/sync" {
		var request struct {
			Action   string `json:"action"`
			Revision string `json:"revision"`
		}
		if !decode(w, r, &request) {
			return
		}
		input.Action, input.Revision = request.Action, request.Revision
	} else {
		var request struct {
			Name     string `json:"name"`
			Revision string `json:"revision"`
		}
		if !decode(w, r, &request) {
			return
		}
		input.Name, input.Revision = request.Name, request.Revision
	}
	if !revision(input.Revision) {
		serviceError(w, content.ErrInvalid)
		return
	}
	var err error
	switch r.URL.Path {
	case "/branches":
		_, err = h.opts.Content.CreateBranch(r.Context(), input.Name, input.Revision)
	case "/branches/checkout":
		_, err = h.opts.Content.Checkout(r.Context(), input.Name, input.Revision)
	case "/sync":
		switch input.Action {
		case "fetch":
			_, err = h.opts.Content.Fetch(r.Context(), input.Revision)
		case "pull":
			_, err = h.opts.Content.Pull(r.Context(), input.Revision)
		case "push":
			_, err = h.opts.Content.Push(r.Context(), input.Revision)
		default:
			err = content.ErrInvalid
		}
	}
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	h.storageStatus(w, r)
}

func (h *handler) storageStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.opts.Content.Status(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	project, err := h.opts.Content.Project(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	result := describeSync(status, project.Revision)
	user, err := Principal(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	if !storageAccess(user) {
		result = syncDTO{Backend: status.Backend, Revision: project.Revision}
	}
	writeJSON(w, http.StatusOK, result)
}
