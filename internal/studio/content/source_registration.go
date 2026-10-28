package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
)

// RegisterSourceInput describes fresh page metadata for an existing source.
// SourceRevision is the SHA256 of the exact source bytes the caller reviewed;
// Revision is the complete project revision from the same review.
type RegisterSourceInput struct {
	Path           string `json:"path"`
	Revision       string `json:"revision"`
	SourceRevision string `json:"sourceRevision"`
	Title          string `json:"title"`
	Kind           string `json:"kind"`
	DocumentID     string `json:"documentId,omitempty"`
	OutputPath     string `json:"outputPath,omitempty"`
}

func registrationOutput(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, path.Ext(name))) + ".html"
}

func registrationChange(input RegisterSourceInput) (Change, error) {
	if !allowedFile(input.Path) || binaryFile(input.Path) || strings.EqualFold(input.Path, "docs.json") || !validRevision(input.Revision) || !validRevision(input.SourceRevision) || strings.TrimSpace(input.Title) == "" || !validKind(input.Kind) {
		return Change{}, ErrInvalid
	}
	output := input.OutputPath
	if output == "" {
		output = registrationOutput(input.Path)
	}
	id := input.DocumentID
	if id == "" {
		value, err := randomID()
		if err != nil {
			return Change{}, err
		}
		id = "page-" + value
	}
	change := Change{Operation: "write", Path: input.Path, Title: input.Title, Kind: input.Kind, OutputPath: output, DocumentID: id}
	if err := validateChanges([]Change{change}); err != nil {
		return Change{}, err
	}
	return change, nil
}

func registrationPublishedConflicts(project Project, change Change) error {
	for _, entry := range project.Documents {
		if foldName(entry.Path) == foldName(change.Path) || foldName(entry.ID) == foldName(change.DocumentID) || sourcePathsOverlap(entry.OutputPath, change.OutputPath) {
			return ErrConflict
		}
	}
	return nil
}

// registrationDraftConflicts reserves page metadata as well as source paths.
// Manifest edits are opaque, so any pending manifest edit requires resolving
// that draft before registration rather than guessing which pages it claims.
func (s *Service) registrationDraftConflicts(ctx context.Context, change Change, branch string) error {
	if err := s.sourceDraftConflicts(ctx, change.Path, ""); err != nil {
		return err
	}
	drafts, err := s.drafts()
	if err != nil {
		return err
	}
	for _, draft := range drafts {
		if !s.historyBranchMatches(draft.Branch, branch) {
			continue
		}
		for _, pending := range draftChanges(draft) {
			if sourcePathsOverlap("docs.json", pending.Path) || pending.To != "" && sourcePathsOverlap("docs.json", pending.To) {
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

func registrationSource(directory string, input RegisterSourceInput, tree sourceTree) ([]byte, error) {
	state, ok := tree.Files[input.Path]
	if !ok {
		for name := range tree.Files {
			if foldName(name) == foldName(input.Path) {
				return nil, ErrConflict
			}
		}
		return nil, ErrNotFound
	}
	if state.Hash != input.SourceRevision {
		return nil, ErrConflict
	}
	data, err := readSource(directory, input.Path, MaxTextBytes)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != state.Size || hex.EncodeToString(digest[:]) != input.SourceRevision {
		return nil, ErrConflict
	}
	return data, nil
}

// RegisterSource stages a new page without rewriting its existing source. The
// normal explicit publication path validates and renders the selected kind.
// Registration never follows historical page IDs or consumes another draft.
func (s *Service) RegisterSource(ctx context.Context, input RegisterSourceInput, actor Actor) (Draft, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return Draft{}, err
	}
	defer done()
	change, err := registrationChange(input)
	if err != nil {
		return Draft{}, err
	}
	actor, err = s.sourceActor(ctx, actor, input.Path, SourceReadWrite)
	if err != nil {
		return Draft{}, err
	}
	project, tree, err := s.sourceProject(ctx)
	if err != nil {
		return Draft{}, err
	}
	if project.Revision != input.Revision {
		return Draft{}, ErrConflict
	}
	if err := registrationPublishedConflicts(project, change); err != nil {
		return Draft{}, err
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return Draft{}, err
	}
	if err := s.registrationDraftConflicts(ctx, change, status.Branch); err != nil {
		return Draft{}, err
	}
	data, err := registrationSource(s.cfg.Root, input, tree)
	if err != nil {
		return Draft{}, err
	}
	change, err = sourceWrite(input.Path, data, change)
	if err != nil {
		return Draft{}, err
	}
	guard := s.sourceGuard(ctx, actor, input.Path, status.Branch, input.Revision, true)
	return s.saveDraftLocked(ctx, DraftInput{BaseRevision: input.Revision, Change: change}, actor, func() error {
		if err := guard(); err != nil {
			return err
		}
		// Re-read after fresh authorization: the draft must contain precisely the
		// reviewed bytes even if an external writer changed this source meanwhile.
		if _, err := registrationSource(s.cfg.Root, input, tree); err != nil {
			return err
		}
		return s.registrationDraftConflicts(ctx, change, status.Branch)
	})
}
