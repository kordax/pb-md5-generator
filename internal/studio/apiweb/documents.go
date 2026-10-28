package apiweb

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

type documentState struct {
	project content.Project
	doc     Document
	draft   *content.Draft
}

func (h *handler) ownedDrafts(r *http.Request, user *auth.User) ([]content.Draft, error) {
	drafts, err := h.opts.Content.Drafts(r.Context())
	if err != nil {
		return nil, err
	}
	result := []content.Draft{}
	for _, draft := range drafts {
		if draftVisible(draft, user, false) {
			result = append(result, draft)
		}
	}
	return result, nil
}

func draftDocument(change content.Change, draft content.Draft, current string, user *auth.User) Document {
	return Document{ID: change.DocumentID, Path: change.Path, Title: change.Title, Kind: change.Kind, OutputPath: change.OutputPath, Revision: draft.BaseRevision, PublishedRevision: current, Draft: true, DraftID: draft.ID, DraftRevision: draft.Revision, BaseRevision: draft.BaseRevision, CanWrite: permitted(user, auth.ActionWrite, change.Path)}
}

func (h *handler) documentState(r *http.Request, user *auth.User, id string, publishedOnly bool) (documentState, error) {
	project, err := h.opts.Content.Project(r.Context())
	state := documentState{project: project}
	if err != nil {
		return state, err
	}
	for _, entry := range project.Documents {
		if entry.ID == id && permitted(user, auth.ActionRead, entry.Path) {
			state.doc = describeDocument(entry, user)
			break
		}
	}
	if !publishedOnly {
		drafts, err := h.ownedDrafts(r, user)
		if err != nil {
			return state, err
		}
		drafts, err = h.currentDrafts(r, drafts)
		if err != nil {
			return state, err
		}
		for _, draft := range drafts {
			changes := changesOf(draft)
			if len(changes) != 1 || changes[0].Operation != "write" {
				continue
			}
			change := changes[0]
			if change.DocumentID != id && (state.doc.ID == "" || change.Path != state.doc.Path) {
				continue
			}
			if state.doc.ID == "" && (change.DocumentID == "" || change.Title == "" || change.Kind == "") {
				continue
			}
			if state.doc.ID == "" {
				state.doc = draftDocument(change, draft, project.Revision, user)
			} else {
				applyDocumentDraft(&state.doc, change, draft)
			}
			state.draft = &draft
			break
		}
	}
	if state.doc.ID == "" {
		return state, content.ErrNotFound
	}
	return state, nil
}

func (h *handler) documents(w http.ResponseWriter, r *http.Request, user *auth.User) {
	if !allowMethod(w, r, "GET, HEAD, POST") {
		return
	}
	if r.Method == http.MethodPost {
		h.createDocument(w, r, user)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		serviceError(w, content.ErrInvalid)
		return
	}
	publishedOnly := false
	if values, present := query["published"]; present {
		if len(values) != 1 || values[0] != "true" && values[0] != "false" {
			serviceError(w, content.ErrInvalid)
			return
		}
		publishedOnly = values[0] == "true"
	}
	project, err := h.opts.Content.Project(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	drafts := []content.Draft{}
	if !publishedOnly {
		drafts, err = h.ownedDrafts(r, user)
		if err != nil {
			serviceError(w, err)
			return
		}
	}
	drafts, err = h.currentDrafts(r, drafts)
	if err != nil {
		serviceError(w, err)
		return
	}
	user, err = Principal(r.Context())
	if err != nil || user == nil {
		serviceError(w, auth.ErrUnauthorized)
		return
	}
	items := []Document{}
	seen := make(map[string]int)
	for _, entry := range project.Documents {
		if permitted(user, auth.ActionRead, entry.Path) {
			seen[entry.ID] = len(items)
			items = append(items, describeDocument(entry, user))
		}
	}
	for _, draft := range drafts {
		changes := changesOf(draft)
		if len(changes) != 1 {
			continue
		}
		change := changes[0]
		if change.Operation != "write" || change.DocumentID == "" || change.Title == "" || change.Kind == "" || !permitted(user, auth.ActionRead, change.Path) || !permitted(user, auth.ActionWrite, change.Path) {
			continue
		}
		if index, exists := seen[change.DocumentID]; exists {
			if !items[index].Draft && items[index].Path == change.Path {
				applyDocumentDraft(&items[index], change, draft)
			}
			continue
		}
		seen[change.DocumentID] = len(items)
		items = append(items, draftDocument(change, draft, project.Revision, user))
	}
	writeJSON(w, http.StatusOK, struct {
		Documents []Document `json:"documents"`
		Revision  string     `json:"revision"`
	}{items, project.Revision})
}

func (h *handler) createDocument(w http.ResponseWriter, r *http.Request, user *auth.User) {
	var input struct {
		Path       string  `json:"path"`
		Title      string  `json:"title"`
		Kind       string  `json:"kind"`
		OutputPath string  `json:"outputPath"`
		Content    *string `json:"content"`
		Revision   string  `json:"revision"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Content != nil && len(*input.Content) > content.MaxTextBytes {
		serviceError(w, content.ErrLimit)
		return
	}
	if input.Content == nil || !validSourcePath(input.Path) || input.Title == "" || strings.TrimSpace(input.Title) == "" || len(input.Title) > 256 || !utf8.ValidString(input.Title) || strings.IndexFunc(input.Title, unicode.IsControl) >= 0 || input.Kind != "markdown" && input.Kind != "html" && input.Kind != "protobuf" && input.Kind != "openapi" || !revision(input.Revision) {
		serviceError(w, content.ErrInvalid)
		return
	}
	if !permitted(user, auth.ActionWrite, input.Path) || !permitted(user, auth.ActionRead, input.Path) || reserved(input.Path) {
		serviceError(w, auth.ErrForbidden)
		return
	}
	if input.OutputPath == "" {
		input.OutputPath = strings.TrimSuffix(input.Path, path.Ext(input.Path)) + ".html"
	}
	if !validSourcePath(input.OutputPath) || !strings.HasSuffix(input.OutputPath, ".html") || strings.HasPrefix(input.OutputPath, "_docs/") {
		serviceError(w, content.ErrInvalid)
		return
	}
	project, err := h.opts.Content.Project(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	if project.Revision != input.Revision {
		h.conflict(w, r)
		return
	}
	for _, entry := range project.Files {
		if entry.Path == input.Path {
			h.conflict(w, r)
			return
		}
	}
	drafts, err := h.ownedDrafts(r, user)
	if err != nil {
		serviceError(w, err)
		return
	}
	for _, draft := range drafts {
		for _, change := range changesOf(draft) {
			if change.Path == input.Path {
				h.conflict(w, r)
				return
			}
		}
	}
	id, err := randomDocumentID()
	if err != nil {
		serviceError(w, err)
		return
	}
	doc := Document{ID: id, Path: input.Path, Title: input.Title, Kind: input.Kind, OutputPath: input.OutputPath, Content: *input.Content, CanWrite: true}
	change := content.Change{Operation: "write", Path: input.Path, Title: input.Title, Kind: input.Kind, OutputPath: input.OutputPath, DocumentID: id, Content: input.Content}
	h.save(w, r, user, content.DraftInput{BaseRevision: input.Revision, Change: change}, &doc, false, false, "", http.StatusCreated)
}

func (h *handler) document(w http.ResponseWriter, r *http.Request, user *auth.User, id, action string) {
	methods := ""
	switch action {
	case "":
		methods = "GET, HEAD, PUT, DELETE"
	case "move", "restore", "rebase":
		methods = "POST"
	case "history":
		methods = "GET, HEAD"
	default:
		writeError(w, http.StatusNotFound, "not_found", "This resource was not found.", "", nil)
		return
	}
	if !allowMethod(w, r, methods) {
		return
	}
	published := r.URL.Query().Get("published")
	if published != "" && published != "false" && published != "true" {
		serviceError(w, content.ErrInvalid)
		return
	}
	state, err := h.documentState(r, user, id, published == "true" && (r.Method == http.MethodGet || r.Method == http.MethodHead))
	if err != nil {
		serviceError(w, err)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if action == "history" {
			h.history(w, r, user, state)
			return
		}
		if state.draft != nil {
			draft, err := h.opts.Content.ReadDraft(r.Context(), state.draft.ID)
			if err != nil || !draftVisible(draft, user, false) {
				serviceError(w, content.ErrNotFound)
				return
			}
			if draft.Revision != state.draft.Revision {
				h.conflict(w, r)
				return
			}
			current, err := h.opts.Content.MatchesCurrentBranch(r.Context(), draft.Branch)
			if err != nil {
				serviceError(w, err)
				return
			}
			if !current {
				serviceError(w, content.ErrNotFound)
				return
			}
			change := changesOf(draft)[0]
			data := change.Data
			if change.Content != nil {
				data = []byte(*change.Content)
			}
			if !sourceTextPath(state.doc.Path) || len(data) > content.MaxTextBytes || !utf8.Valid(data) {
				serviceError(w, content.ErrInvalid)
				return
			}
			state.doc.Content = string(data)
		} else {
			file, err := h.opts.Content.Read(r.Context(), state.doc.Path)
			if err != nil || file.Binary {
				if err == nil {
					err = content.ErrInvalid
				}
				serviceError(w, err)
				return
			}
			if file.Revision != state.project.Revision {
				h.conflict(w, r)
				return
			}
			state.doc.Content, state.doc.Revision, state.doc.PublishedRevision = file.Content, file.Revision, file.Revision
		}
		user, err = Principal(r.Context())
		if err != nil || user == nil {
			serviceError(w, auth.ErrUnauthorized)
			return
		}
		if !permitted(user, auth.ActionRead, state.doc.Path) || state.draft != nil && !draftVisible(*state.draft, user, false) {
			serviceError(w, content.ErrNotFound)
			return
		}
		state.doc.CanWrite = permitted(user, auth.ActionWrite, state.doc.Path)
		writeJSON(w, http.StatusOK, state.doc)
		return
	}
	if !permitted(user, auth.ActionWrite, state.doc.Path) {
		serviceError(w, auth.ErrForbidden)
		return
	}
	switch action {
	case "history":
		return
	case "move":
		h.moveDocument(w, r, user, state)
	case "restore":
		h.restore(w, r, user, state)
	case "rebase":
		h.saveDocument(w, r, user, state, true)
	default:
		if r.Method == http.MethodDelete {
			if state.draft != nil {
				h.conflict(w, r)
				return
			}
			h.stage(w, r, user, content.Change{Operation: "delete", Path: state.doc.Path}, ifMatch(r), nil)
			return
		}
		h.saveDocument(w, r, user, state, false)
	}
}

func (h *handler) saveDocument(w http.ResponseWriter, r *http.Request, user *auth.User, state documentState, rebase bool) {
	var input documentSaveInput
	if rebase {
		var request struct {
			Content            *string                    `json:"content"`
			Revision           string                     `json:"revision"`
			DraftID            string                     `json:"draftId"`
			DraftRevision      string                     `json:"draftRevision"`
			Properties         documentProperties         `json:"properties"`
			PropertiesRevision documentPropertiesRevision `json:"propertiesRevision"`
		}
		if !decode(w, r, &request) {
			return
		}
		input.Content, input.Revision, input.DraftID, input.DraftRevision = request.Content, request.Revision, request.DraftID, request.DraftRevision
		input.Properties, input.PropertiesRevision = request.Properties, request.PropertiesRevision
	} else if !decode(w, r, &input) {
		return
	}
	if input.Content != nil && len(*input.Content) > content.MaxTextBytes {
		serviceError(w, content.ErrLimit)
		return
	}
	if input.Content == nil || !revision(input.Revision) || !input.Properties.provided && input.PropertiesRevision != "" || input.Properties.provided && (!revision(string(input.PropertiesRevision)) || !validDocumentProperties(input.Properties)) {
		serviceError(w, content.ErrInvalid)
		return
	}
	base := input.Revision
	if rebase && input.Revision != state.project.Revision {
		h.conflict(w, r)
		return
	}
	if state.draft != nil {
		if input.DraftID != state.draft.ID || input.DraftRevision != state.draft.Revision || !rebase && (input.Revision != state.draft.BaseRevision || input.BaseRevision != "" && input.BaseRevision != state.draft.BaseRevision) {
			h.conflict(w, r)
			return
		}
		if !rebase {
			base = state.draft.BaseRevision
		}
	} else if input.DraftID != "" || input.DraftRevision != "" || input.BaseRevision != "" && input.BaseRevision != input.Revision {
		h.conflict(w, r)
		return
	}
	if input.Properties.provided {
		input.Revision = base
		h.saveDocumentProperties(w, r, user, state.doc.ID, input, rebase)
		return
	}
	state.doc.Content = *input.Content
	change := content.Change{Operation: "write", Path: state.doc.Path, Title: state.doc.Title, Kind: state.doc.Kind, OutputPath: state.doc.OutputPath, DocumentID: state.doc.ID, Content: input.Content}
	h.save(w, r, user, content.DraftInput{ID: input.DraftID, Revision: input.DraftRevision, BaseRevision: base, Change: change}, &state.doc, input.Publish, false, input.Message, http.StatusOK)
}

func (h *handler) moveDocument(w http.ResponseWriter, r *http.Request, user *auth.User, state documentState) {
	var input struct {
		To       string `json:"to"`
		Revision string `json:"revision"`
	}
	if !decode(w, r, &input) {
		return
	}
	if state.draft != nil {
		h.conflict(w, r)
		return
	}
	if !permitted(user, auth.ActionWrite, input.To) || !permitted(user, auth.ActionRead, input.To) {
		serviceError(w, auth.ErrForbidden)
		return
	}
	h.stage(w, r, user, content.Change{Operation: "move", Path: state.doc.Path, To: input.To}, input.Revision, nil)
}

func (h *handler) preview(w http.ResponseWriter, r *http.Request, user *auth.User) {
	if !allowMethod(w, r, "POST") {
		return
	}
	var input struct {
		DocumentID string             `json:"documentId"`
		Content    *string            `json:"content"`
		Properties documentProperties `json:"properties"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Content == nil {
		serviceError(w, content.ErrInvalid)
		return
	}
	if len(*input.Content) > content.MaxTextBytes {
		serviceError(w, content.ErrLimit)
		return
	}
	state, err := h.documentState(r, user, input.DocumentID, false)
	if err != nil {
		serviceError(w, err)
		return
	}
	if input.Properties.provided {
		if !validDocumentProperties(input.Properties) {
			serviceError(w, content.ErrInvalid)
			return
		}
		state.doc.Title, state.doc.Kind, state.doc.OutputPath = input.Properties.Title, input.Properties.Kind, input.Properties.OutputPath
	}
	result, err := h.opts.Preview(r.Context(), state.doc, []byte(*input.Content), user)
	if !boundedPreview(result) {
		serviceError(w, content.ErrLimit)
		return
	}
	if err != nil {
		if errors.Is(err, auth.ErrForbidden) || errors.Is(err, auth.ErrUnauthorized) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			serviceError(w, err)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "invalid_content", "The source could not be rendered. Correct the reported errors and retry.", "", result.Diagnostics)
		return
	}
	if result.Diagnostics == nil {
		result.Diagnostics = []Diagnostic{}
	}
	writeJSON(w, http.StatusOK, result)
}

func boundedPreview(result Preview) bool {
	if len(result.HTML) > 16<<20 || !utf8.ValidString(result.HTML) || len(result.Diagnostics) > 100 {
		return false
	}
	encodedBytes := 0
	for _, char := range result.HTML {
		switch {
		case char == '<' || char == '>' || char == '&' || char == '\u2028' || char == '\u2029':
			encodedBytes += 6
		case char == '"' || char == '\\' || char == '\b' || char == '\f' || char == '\n' || char == '\r' || char == '\t':
			encodedBytes += 2
		case char < 0x20:
			encodedBytes += 6
		default:
			encodedBytes += utf8.RuneLen(char)
		}
		if encodedBytes > 16<<20 {
			return false
		}
	}
	for _, diagnostic := range result.Diagnostics {
		if len(diagnostic.File) > 2048 || len(diagnostic.Message) > 4096 || len(diagnostic.Code) > 128 || len(diagnostic.Severity) > 32 || !utf8.ValidString(diagnostic.Message) || !utf8.ValidString(diagnostic.File) || diagnostic.Line < 0 || diagnostic.Column < 0 {
			return false
		}
	}
	return true
}
