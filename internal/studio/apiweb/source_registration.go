package apiweb

import (
	"net/http"

	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

// registerDocument binds new page metadata to reviewed live bytes. Registration
// always stays in a new draft until the user explicitly publishes it.
func (h *handler) registerDocument(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, "POST") {
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		serviceError(w, content.ErrInvalid)
		return
	}
	var input struct {
		Path           string `json:"path"`
		Revision       string `json:"revision"`
		SourceRevision string `json:"sourceRevision"`
		Title          string `json:"title"`
		Kind           string `json:"kind"`
		ID             string `json:"id"`
		OutputPath     string `json:"outputPath"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if !decode(w, r, &input) {
		return
	}
	if !validSourcePath(input.Path) || !revision(input.Revision) || !revision(input.SourceRevision) {
		serviceError(w, content.ErrInvalid)
		return
	}
	user, ok := h.filePrincipal(w, r, input.Path, true)
	if !ok {
		return
	}
	draft, err := h.opts.Content.RegisterSource(r.Context(), content.RegisterSourceInput{
		Path: input.Path, Revision: input.Revision, SourceRevision: input.SourceRevision,
		Title: input.Title, Kind: input.Kind, DocumentID: input.ID, OutputPath: input.OutputPath,
	}, actor(user))
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	user, ok = h.filePrincipal(w, r, input.Path, true)
	if !ok {
		return
	}
	change := changesOf(draft)[0]
	doc := draftDocument(change, draft, draft.BaseRevision, user)
	if change.Content != nil {
		doc.Content = *change.Content
	}
	description := describeDraft(draft)
	writeJSON(w, http.StatusCreated, saveResult{Revision: draft.BaseRevision, Document: &doc, Draft: &description})
}
