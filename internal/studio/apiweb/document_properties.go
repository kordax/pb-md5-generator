package apiweb

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

type documentProperties struct {
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	OutputPath string `json:"outputPath"`
	provided   bool
}

func (properties *documentProperties) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return content.ErrInvalid
	}
	type fields documentProperties
	var value fields
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*properties = documentProperties(value)
	properties.provided = true
	return nil
}

type documentPropertiesRevision string

func (revisionValue *documentPropertiesRevision) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if !revision(value) {
		return content.ErrInvalid
	}
	*revisionValue = documentPropertiesRevision(value)
	return nil
}

type documentSaveInput struct {
	Content            *string                    `json:"content"`
	Revision           string                     `json:"revision"`
	DraftID            string                     `json:"draftId"`
	DraftRevision      string                     `json:"draftRevision"`
	BaseRevision       string                     `json:"baseRevision"`
	Publish            bool                       `json:"publish"`
	Message            string                     `json:"message"`
	Properties         documentProperties         `json:"properties"`
	PropertiesRevision documentPropertiesRevision `json:"propertiesRevision"`
}

func validDocumentProperties(properties documentProperties) bool {
	return content.ValidateDocumentProperties(properties.Title, properties.Kind, properties.OutputPath) == nil
}

func applyDocumentDraft(document *Document, change content.Change, draft content.Draft) {
	if change.DocumentID == document.ID {
		if change.Title != "" {
			document.Title = change.Title
		}
		if change.Kind != "" {
			document.Kind = change.Kind
		}
		if change.OutputPath != "" {
			document.OutputPath = change.OutputPath
		}
	}
	document.Draft, document.DraftID, document.DraftRevision = true, draft.ID, draft.Revision
	document.Revision, document.BaseRevision = draft.BaseRevision, draft.BaseRevision
}

func (h *handler) saveDocumentProperties(w http.ResponseWriter, r *http.Request, user *auth.User, id string, input documentSaveInput, rebase bool) {
	if len(input.Message) > 256 || !utf8.ValidString(input.Message) || strings.IndexFunc(input.Message, unicode.IsControl) >= 0 {
		serviceError(w, content.ErrInvalid)
		return
	}
	policy, err := h.policy(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	properties := input.Properties
	draft, err := h.opts.Content.SaveDocumentProperties(r.Context(), content.DocumentPropertiesInput{
		DocumentID: id, Revision: input.Revision, PropertiesRevision: string(input.PropertiesRevision),
		DraftID: input.DraftID, DraftRevision: input.DraftRevision, Content: *input.Content,
		Title: properties.Title, Kind: properties.Kind, OutputPath: properties.OutputPath, Rebase: rebase,
	}, actor(user))
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	changes := changesOf(draft)
	if len(changes) != 1 || changes[0].Operation != "write" || changes[0].DocumentID != id {
		serviceError(w, content.ErrInvalid)
		return
	}
	document := draftDocument(changes[0], draft, string(input.PropertiesRevision), user)
	document.Content = string(changes[0].Data)
	if changes[0].Content != nil {
		document.Content = *changes[0].Content
	}
	h.finishSave(w, r, user, draft, &document, input.Publish, false, input.Message, http.StatusOK, policy)
}
