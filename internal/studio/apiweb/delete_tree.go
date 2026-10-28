package apiweb

import (
	"errors"
	"net/http"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
)

const maxDeleteTreeJSONBody = 32 << 10

type deleteTreeEntryDTO struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
}

type deleteTreePlanDTO struct {
	Path           string               `json:"path"`
	Revision       string               `json:"revision"`
	PlanRevision   string               `json:"planRevision"`
	FileCount      int                  `json:"fileCount"`
	DirectoryCount int                  `json:"directoryCount"`
	DocumentCount  int                  `json:"documentCount"`
	TotalBytes     int64                `json:"totalBytes"`
	Entries        []deleteTreeEntryDTO `json:"entries"`
	Truncated      bool                 `json:"truncated"`
}

func (h *handler) filePrincipal(w http.ResponseWriter, r *http.Request, name string, write bool) (*auth.User, bool) {
	user, err := Principal(r.Context())
	if err != nil || user == nil {
		serviceError(w, auth.ErrUnauthorized)
		return nil, false
	}
	if !permitted(user, auth.ActionRead, name) {
		serviceError(w, content.ErrNotFound)
		return nil, false
	}
	if write && !permitted(user, auth.ActionWrite, name) {
		serviceError(w, auth.ErrForbidden)
		return nil, false
	}
	return user, true
}

func (h *handler) deleteTree(w http.ResponseWriter, r *http.Request, _ *auth.User) {
	if !allowMethod(w, r, "POST") {
		return
	}
	if r.URL.RawQuery != "" {
		serviceError(w, content.ErrInvalid)
		return
	}
	preview := r.URL.Path == "/files/delete-preview"
	var name, base, proof string
	if preview {
		var input struct {
			Path     string `json:"path"`
			Revision string `json:"revision"`
		}
		if !decodeDeletion(w, r, &input) {
			return
		}
		name, base = input.Path, input.Revision
	} else {
		var input struct {
			Path         string `json:"path"`
			Revision     string `json:"revision"`
			PlanRevision string `json:"planRevision"`
		}
		if !decodeDeletion(w, r, &input) {
			return
		}
		name, base, proof = input.Path, input.Revision, input.PlanRevision
	}
	if !validSourcePath(name) || !revision(base) || !preview && !revision(proof) {
		serviceError(w, content.ErrInvalid)
		return
	}
	user, ok := h.filePrincipal(w, r, name, true)
	if !ok {
		return
	}
	if preview {
		plan, err := h.opts.Content.PlanDeleteTree(r.Context(), content.DeleteTreeInput{Path: name, Revision: base}, actor(user))
		if err != nil {
			h.deletionError(w, r, err)
			return
		}
		if _, ok := h.filePrincipal(w, r, name, true); !ok {
			return
		}
		result := deleteTreePlanDTO{Path: plan.Path, Revision: plan.Revision, PlanRevision: plan.PlanRevision, FileCount: plan.FileCount, DirectoryCount: plan.DirectoryCount, DocumentCount: plan.DocumentCount, TotalBytes: plan.TotalBytes, Entries: []deleteTreeEntryDTO{}, Truncated: plan.Truncated}
		for _, entry := range plan.Entries {
			result.Entries = append(result.Entries, deleteTreeEntryDTO{Path: entry.Path, Directory: entry.Directory, Size: entry.Size})
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	change := content.Change{Operation: "delete-tree", Path: name, PlanRevision: proof}
	if err := h.authorizeChanges(r, user, []content.Change{change}); err != nil {
		h.deletionError(w, r, err)
		return
	}
	// Tree deletion always remains a reviewable draft. Automatic publishing is
	// deliberately not consulted for this explicitly destructive operation.
	draft, err := h.opts.Content.SaveDraft(r.Context(), content.DraftInput{BaseRevision: base, Change: change}, actor(user))
	if err != nil {
		h.deletionError(w, r, err)
		return
	}
	if _, ok := h.filePrincipal(w, r, name, true); !ok {
		return
	}
	description := describeDraft(draft)
	writeJSON(w, http.StatusOK, saveResult{Revision: draft.BaseRevision, Draft: &description})
}

func decodeDeletion(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := httpjson.Decode(w, r, target, maxDeleteTreeJSONBody); err != nil {
		writeError(w, httpjson.Status(err), "invalid_request", "Send one valid deletion request using the documented fields and limits.", "", nil)
		return false
	}
	return true
}

func (h *handler) deletionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, content.ErrOwnership) {
		writeError(w, http.StatusConflict, "conflict", "This folder cannot be removed safely. Review the current files before retrying.", "", nil)
		return
	}
	h.operationError(w, r, err)
}
