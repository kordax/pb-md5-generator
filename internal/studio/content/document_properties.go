package content

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
)

// DocumentPropertiesInput saves one editor buffer and its page properties.
// Revision keeps the ordinary save/rebase base semantics. PropertiesRevision
// independently identifies the current project reviewed for metadata changes.
// Rebase explicitly changes a paired draft's base to that current revision.
type DocumentPropertiesInput struct {
	DocumentID         string
	Revision           string
	PropertiesRevision string
	DraftID            string
	DraftRevision      string
	Content            string
	Title              string
	Kind               string
	OutputPath         string
	Rebase             bool
}

// ValidateDocumentProperties checks the shared save and preview property shape.
// It does not authorize, inspect the project, or reserve an output URL.
func ValidateDocumentProperties(title, kind, outputPath string) error {
	if strings.TrimSpace(title) == "" || len(title) > 256 || !utf8.ValidString(title) || strings.IndexFunc(title, unicode.IsControl) >= 0 || !validKind(kind) || !validOutput(outputPath) {
		return ErrInvalid
	}
	return nil
}

func validDocumentPropertiesInput(input DocumentPropertiesInput) bool {
	return validDocumentID(input.DocumentID) && validRevision(input.Revision) && validRevision(input.PropertiesRevision) &&
		(input.DraftID == "") == (input.DraftRevision == "") &&
		(input.DraftID == "" || validID(input.DraftID) && validRevision(input.DraftRevision))
}

func (s *Service) propertiesDraft(input DocumentPropertiesInput, actor Actor, branch string) (Draft, Change, error) {
	if input.Rebase && input.Revision != input.PropertiesRevision || input.DraftID == "" && input.Revision != input.PropertiesRevision {
		return Draft{}, Change{}, ErrConflict
	}
	if input.DraftID == "" {
		return Draft{}, Change{}, nil
	}
	old, err := s.readDraft(input.DraftID)
	if err != nil {
		return Draft{}, Change{}, err
	}
	if old.Actor.ID != actor.ID {
		return Draft{}, Change{}, ErrNotFound
	}
	changes := draftChanges(old)
	if len(changes) != 1 || changes[0].Operation != "write" || changes[0].DocumentID != "" && changes[0].DocumentID != input.DocumentID {
		return Draft{}, Change{}, ErrConflict
	}
	if old.Revision != input.DraftRevision || !s.historyBranchMatches(old.Branch, branch) || !input.Rebase && old.BaseRevision != input.Revision {
		return Draft{}, Change{}, ErrConflict
	}
	return old, changes[0], nil
}

func propertiesTarget(project Project, id string, old Draft, previous Change) (Entry, error) {
	for _, entry := range project.Documents {
		if entry.ID == id {
			if old.ID != "" && previous.Path != entry.Path {
				return Entry{}, ErrConflict
			}
			return entry, nil
		}
	}
	if old.ID == "" || previous.DocumentID != id || previous.Title == "" {
		return Entry{}, ErrNotFound
	}
	// An owned page draft may not have a published source yet. Its original
	// identity and path are authoritative; request properties cannot move it.
	return Entry{ID: id, Path: previous.Path}, nil
}

func propertiesPublishedConflicts(project Project, change Change) error {
	for _, entry := range project.Files {
		if entry.Directory && foldName(entry.Path) == foldName(change.Path) || !entry.Directory && entry.Path != change.Path && sourcePathsOverlap(entry.Path, change.Path) {
			return ErrConflict
		}
	}
	for _, entry := range project.Documents {
		if entry.ID == change.DocumentID {
			continue
		}
		if sourcePathsOverlap(entry.Path, change.Path) || foldName(entry.ID) == foldName(change.DocumentID) || sourcePathsOverlap(entry.OutputPath, change.OutputPath) {
			return ErrConflict
		}
	}
	return nil
}

// Another author's single write to this published page is an alternative edit,
// not a reservation of a new identity or URL. Its own base and explicit rebase
// still protect publication; it must never be adopted as the caller's draft.
func propertiesOtherAuthorWrite(draft Draft, target Entry, actorID string) bool {
	if draft.Actor.ID == actorID || target.ID == "" || target.OutputPath == "" {
		return false
	}
	changes := draftChanges(draft)
	return len(changes) == 1 && changes[0].Operation == "write" && changes[0].Path == target.Path && (changes[0].DocumentID == "" || changes[0].DocumentID == target.ID)
}

func (s *Service) propertiesDraftConflicts(ctx context.Context, change Change, target Entry, branch, ignored, actorID string) error {
	drafts, err := s.drafts()
	if err != nil {
		return err
	}
	for _, draft := range drafts {
		if draft.ID == ignored || !s.historyBranchMatches(draft.Branch, branch) || propertiesOtherAuthorWrite(draft, target, actorID) {
			continue
		}
		for _, pending := range draftChanges(draft) {
			if sourcePathsOverlap(change.Path, pending.Path) || pending.To != "" && sourcePathsOverlap(change.Path, pending.To) || sourcePathsOverlap("docs.json", pending.Path) || pending.To != "" && sourcePathsOverlap("docs.json", pending.To) {
				return ErrConflict
			}
			output, id := pending.OutputPath, pending.DocumentID
			if pending.Operation == "write" && pending.Title != "" {
				if output == "" {
					output = registrationOutput(pending.Path)
				}
				if id == "" {
					id = document.PageID(output)
				}
			}
			if output != "" && sourcePathsOverlap(output, change.OutputPath) || id != "" && foldName(id) == foldName(change.DocumentID) {
				return ErrConflict
			}
		}
	}
	return ctx.Err()
}

// SaveDocumentProperties stages the exact current editor text and properties.
// It never publishes, moves a source, changes a page ID, or chooses another
// draft. Fresh source authorization and both revision checks run under one gate.
func (s *Service) SaveDocumentProperties(ctx context.Context, input DocumentPropertiesInput, actor Actor) (Draft, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return Draft{}, err
	}
	defer done()
	if len(input.Content) > MaxTextBytes {
		return Draft{}, ErrLimit
	}
	if !validDocumentPropertiesInput(input) {
		return Draft{}, ErrInvalid
	}
	if err := ValidateDocumentProperties(input.Title, input.Kind, input.OutputPath); err != nil {
		return Draft{}, err
	}
	actor, err = normalizeActor(actor)
	if err != nil {
		return Draft{}, err
	}
	if s.cfg.AuthorizeSource == nil {
		return Draft{}, ErrUnsupported
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return Draft{}, err
	}
	old, previous, err := s.propertiesDraft(input, actor, status.Branch)
	if err != nil {
		return Draft{}, err
	}
	project, _, err := s.sourceProject(ctx)
	if err != nil {
		return Draft{}, err
	}
	if project.Revision != input.PropertiesRevision {
		return Draft{}, ErrConflict
	}
	target, err := propertiesTarget(project, input.DocumentID, old, previous)
	if err != nil {
		return Draft{}, err
	}
	if binaryFile(target.Path) || strings.EqualFold(target.Path, "docs.json") || target.OutputPath == "index.html" && input.OutputPath != "index.html" {
		return Draft{}, ErrInvalid
	}
	actor, err = s.sourceActor(ctx, actor, target.Path, SourceReadWrite)
	if err != nil {
		return Draft{}, err
	}
	change, err := sourceWrite(target.Path, []byte(input.Content), Change{DocumentID: target.ID, Title: input.Title, Kind: input.Kind, OutputPath: input.OutputPath})
	if err != nil {
		return Draft{}, err
	}
	if err := propertiesPublishedConflicts(project, change); err != nil {
		return Draft{}, err
	}
	if err := s.propertiesDraftConflicts(ctx, change, target, status.Branch, input.DraftID, actor.ID); err != nil {
		return Draft{}, err
	}
	guard := s.sourceGuard(ctx, actor, target.Path, status.Branch, input.PropertiesRevision, true)
	return s.saveDraftLocked(ctx, DraftInput{ID: input.DraftID, Revision: input.DraftRevision, BaseRevision: input.Revision, Change: change}, actor, func() error {
		if err := guard(); err != nil {
			return err
		}
		return s.propertiesDraftConflicts(ctx, change, target, status.Branch, input.DraftID, actor.ID)
	})
}
