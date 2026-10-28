package content

import (
	"bytes"
	"context"
	"encoding/json"
)

type SourceAccess string

const (
	SourceRead      SourceAccess = "read"
	SourceReadWrite SourceAccess = "readWrite"
)

type SourceDraftInput struct {
	Path          string
	Revision      string
	DraftID       string
	DraftRevision string
	Data          []byte
}

type RestoreSourceInput struct {
	Path      string
	HistoryID string
	Revision  string
}

type SourceHistoryResult struct {
	Path     string     `json:"path"`
	Revision string     `json:"revision"`
	Items    []Revision `json:"items"`
}

func (s *Service) sourceActor(ctx context.Context, actor Actor, name string, access SourceAccess) (Actor, error) {
	if !allowedFile(name) {
		return Actor{}, ErrInvalid
	}
	actor, err := normalizeActor(actor)
	if err != nil {
		return Actor{}, err
	}
	if s.cfg.AuthorizeSource == nil {
		return Actor{}, ErrUnsupported
	}
	if err := s.cfg.AuthorizeSource(ctx, actor, name, access); err != nil {
		return Actor{}, err
	}
	return actor, ctx.Err()
}

func sourcePathsOverlap(left, right string) bool {
	left, right = foldName(left), foldName(right)
	return sourceInTree(left, right) || sourceInTree(right, left)
}

func (s *Service) sourceDraftConflicts(ctx context.Context, name, ignored string) error {
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return err
	}
	drafts, err := s.drafts()
	if err != nil {
		return err
	}
	for _, draft := range drafts {
		if draft.ID == ignored || !s.historyBranchMatches(draft.Branch, status.Branch) {
			continue
		}
		for _, change := range draftChanges(draft) {
			if sourcePathsOverlap(name, change.Path) || change.To != "" && sourcePathsOverlap(name, change.To) {
				return ErrConflict
			}
		}
	}
	return ctx.Err()
}

func sourceWrite(name string, data []byte, change Change) (Change, error) {
	if !binaryFile(name) && bytes.IndexByte(data, 0) >= 0 {
		return Change{}, ErrInvalid
	}
	change.Operation, change.Path, change.To, change.PlanRevision = "write", name, "", ""
	change.Data, change.Content = nil, nil
	if binaryFile(name) {
		change.Data = append([]byte(nil), data...)
	} else {
		text := string(data)
		change.Content = &text
	}
	if err := validateChanges([]Change{change}); err != nil {
		return Change{}, err
	}
	return change, nil
}

func (s *Service) sourceProject(ctx context.Context) (Project, sourceTree, error) {
	s.cached = sourceTree{}
	return s.project(ctx)
}

func (s *Service) sourceGuard(ctx context.Context, actor Actor, name, branch, revision string, checkBase bool) func() error {
	return func() error {
		status, err := s.cfg.Storage.Status(ctx)
		if err != nil {
			return err
		}
		if status.Branch != branch {
			return ErrConflict
		}
		if checkBase {
			project, _, err := s.sourceProject(ctx)
			if err != nil {
				return err
			}
			if project.Revision != revision {
				return ErrConflict
			}
		}
		_, err = s.sourceActor(ctx, actor, name, SourceReadWrite)
		return err
	}
}

// SourceHistory returns only the requested path, with its project revision from
// the same serialized view. It does not follow document IDs to other paths.
func (s *Service) SourceHistory(ctx context.Context, name string, actor Actor) (SourceHistoryResult, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return SourceHistoryResult{}, err
	}
	defer done()
	actor, err = s.sourceActor(ctx, actor, name, SourceRead)
	if err != nil {
		return SourceHistoryResult{}, err
	}
	project, tree, err := s.project(ctx)
	if err != nil {
		return SourceHistoryResult{}, err
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return SourceHistoryResult{}, err
	}
	revisions, err := s.revisions()
	if err != nil {
		return SourceHistoryResult{}, err
	}
	result := SourceHistoryResult{Path: name, Revision: project.Revision, Items: []Revision{}}
	for i := len(revisions) - 1; i >= 0; i-- {
		item := revisions[i]
		if item.Path == name && s.historyBranchMatches(item.Branch, status.Branch) {
			result.Items = append(result.Items, item)
		}
	}
	if _, exists := tree.Files[name]; !exists && len(result.Items) == 0 {
		return SourceHistoryResult{}, ErrNotFound
	}
	if _, err := s.sourceActor(ctx, actor, name, SourceRead); err != nil {
		return SourceHistoryResult{}, err
	}
	return result, nil
}

// SaveSource preserves an owned draft's stored base; a new draft requires the
// current project revision. RebaseSource explicitly changes that base instead.
func (s *Service) SaveSource(ctx context.Context, input SourceDraftInput, actor Actor) (Draft, error) {
	return s.saveSource(ctx, input, actor, false)
}

func (s *Service) RebaseSource(ctx context.Context, input SourceDraftInput, actor Actor) (Draft, error) {
	return s.saveSource(ctx, input, actor, true)
}

func (s *Service) saveSource(ctx context.Context, input SourceDraftInput, actor Actor, rebase bool) (Draft, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return Draft{}, err
	}
	defer done()
	if !validRevision(input.Revision) || (input.DraftID == "") != (input.DraftRevision == "") || input.DraftID != "" && (!validID(input.DraftID) || !validRevision(input.DraftRevision)) || rebase && binaryFile(input.Path) {
		return Draft{}, ErrInvalid
	}
	actor, err = s.sourceActor(ctx, actor, input.Path, SourceReadWrite)
	if err != nil {
		return Draft{}, err
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return Draft{}, err
	}
	change := Change{}
	if input.DraftID != "" {
		old, err := s.readDraft(input.DraftID)
		if err != nil {
			return Draft{}, err
		}
		changes := draftChanges(old)
		if old.Actor.ID != actor.ID || len(changes) != 1 || changes[0].Operation != "write" || changes[0].Path != input.Path {
			return Draft{}, ErrNotFound
		}
		if !s.historyBranchMatches(old.Branch, status.Branch) {
			return Draft{}, ErrConflict
		}
		if old.Revision != input.DraftRevision || !rebase && old.BaseRevision != input.Revision {
			return Draft{}, ErrConflict
		}
		change = changes[0]
	}
	currentBase := rebase || input.DraftID == ""
	if currentBase {
		project, _, err := s.sourceProject(ctx)
		if err != nil {
			return Draft{}, err
		}
		if project.Revision != input.Revision {
			return Draft{}, ErrConflict
		}
	}
	if err := s.sourceDraftConflicts(ctx, input.Path, input.DraftID); err != nil {
		return Draft{}, err
	}
	change, err = sourceWrite(input.Path, input.Data, change)
	if err != nil {
		return Draft{}, err
	}
	return s.saveDraftLocked(ctx, DraftInput{ID: input.DraftID, Revision: input.DraftRevision, BaseRevision: input.Revision, Change: change}, actor,
		s.sourceGuard(ctx, actor, input.Path, status.Branch, input.Revision, currentBase))
}

// RestoreSource restores file bytes into a new draft. Historical document
// metadata cannot re-register, move or rename a current page through this API.
func (s *Service) RestoreSource(ctx context.Context, input RestoreSourceInput, actor Actor) (Draft, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return Draft{}, err
	}
	defer done()
	if !validID(input.HistoryID) || !validRevision(input.Revision) {
		return Draft{}, ErrInvalid
	}
	actor, err = s.sourceActor(ctx, actor, input.Path, SourceReadWrite)
	if err != nil {
		return Draft{}, err
	}
	project, _, err := s.sourceProject(ctx)
	if err != nil {
		return Draft{}, err
	}
	if project.Revision != input.Revision {
		return Draft{}, ErrConflict
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return Draft{}, err
	}
	data, err := readSource(s.cfg.StateDir, "history/"+input.HistoryID+".json", 64<<10)
	if err != nil {
		return Draft{}, err
	}
	var revision Revision
	if err := json.Unmarshal(data, &revision); err != nil || revision.ID != input.HistoryID || revision.Path != input.Path || !s.historyBranchMatches(revision.Branch, status.Branch) {
		return Draft{}, ErrNotFound
	}
	if revision.Deleted {
		return Draft{}, ErrInvalid
	}
	data, err = readSource(s.cfg.StateDir, "history/"+input.HistoryID+".bin", MaxBinaryBytes)
	if err != nil {
		return Draft{}, err
	}
	// History revisions identify the complete project, rather than a payload
	// checksum. Preserve that format while checking the selected file's size.
	if revision.Size != int64(len(data)) {
		return Draft{}, ErrConflict
	}
	if err := s.sourceDraftConflicts(ctx, input.Path, ""); err != nil {
		return Draft{}, err
	}
	change, err := sourceWrite(input.Path, data, Change{})
	if err != nil {
		return Draft{}, err
	}
	return s.saveDraftLocked(ctx, DraftInput{BaseRevision: input.Revision, Change: change}, actor,
		s.sourceGuard(ctx, actor, input.Path, status.Branch, input.Revision, true))
}
