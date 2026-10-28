package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

type deleteTreeScope struct {
	plan    DeleteTreePlan
	entries []Entry
	hashes  map[string]string
}

func sourceInTree(name, folder string) bool {
	return name == folder || strings.HasPrefix(name, folder+"/")
}

func deleteTreeChange(changes []Change) (Change, bool) {
	if len(changes) == 1 && changes[0].Operation == "delete-tree" {
		return changes[0], true
	}
	return Change{}, false
}

func (s *Service) authorizeDeleteTree(ctx context.Context, actor Actor, entries []Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.cfg.AuthorizeDeleteTree == nil {
		return ErrUnsupported
	}
	return s.cfg.AuthorizeDeleteTree(ctx, actor, append([]Entry(nil), entries...))
}

// PlanDeleteTree returns no partial scope. Every entry, including the target
// folder, must pass the fresh read/write authorization hook before counts or
// names are returned. Planning never changes source, history or saved drafts.
func (s *Service) PlanDeleteTree(ctx context.Context, input DeleteTreeInput, actor Actor) (DeleteTreePlan, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return DeleteTreePlan{}, err
	}
	defer done()
	actor, err = normalizeActor(actor)
	if err != nil {
		return DeleteTreePlan{}, err
	}
	scope, err := s.planDeleteTree(ctx, input, actor, "")
	if err != nil {
		return DeleteTreePlan{}, err
	}
	return scope.plan, nil
}

func (s *Service) planDeleteTree(ctx context.Context, input DeleteTreeInput, actor Actor, excludeDraft string) (deleteTreeScope, error) {
	if !validPath(input.Path) || input.Path == "docs.json" || !validRevision(input.Revision) {
		return deleteTreeScope{}, ErrInvalid
	}
	if err := s.authorizeDeleteTree(ctx, actor, []Entry{{Path: input.Path, Directory: true}}); err != nil {
		return deleteTreeScope{}, err
	}
	s.cached = sourceTree{}
	project, tree, err := s.project(ctx)
	if err != nil {
		return deleteTreeScope{}, err
	}
	if project.Revision != input.Revision {
		return deleteTreeScope{}, ErrConflict
	}
	scope, err := s.inspectDeleteTree(ctx, input.Path, tree)
	if err != nil {
		return deleteTreeScope{}, err
	}
	if err := s.authorizeDeleteTree(ctx, actor, scope.entries); err != nil {
		return deleteTreeScope{}, err
	}
	manifest, err := loadManifest(s.cfg.Root)
	if err != nil {
		return deleteTreeScope{}, err
	}
	for _, page := range manifest.Pages {
		if !sourceInTree(page.Source, input.Path) {
			continue
		}
		if page.Path == "index.html" {
			return deleteTreeScope{}, fmt.Errorf("%w: the homepage cannot be removed with its folder", ErrConflict)
		}
		scope.plan.DocumentCount++
	}
	if scope.plan.DocumentCount >= len(manifest.Pages) {
		return deleteTreeScope{}, fmt.Errorf("%w: the last document cannot be removed", ErrConflict)
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return deleteTreeScope{}, err
	}
	drafts, err := s.drafts()
	if err != nil {
		return deleteTreeScope{}, err
	}
	for _, draft := range drafts {
		if draft.ID == excludeDraft || !s.historyBranchMatches(draft.Branch, status.Branch) {
			continue
		}
		for _, change := range draftChanges(draft) {
			if sourceInTree(change.Path, input.Path) || sourceInTree(input.Path, change.Path) || change.To != "" && (sourceInTree(change.To, input.Path) || sourceInTree(input.Path, change.To)) {
				return deleteTreeScope{}, fmt.Errorf("%w: saved edits must be resolved before deleting the folder", ErrConflict)
			}
		}
	}
	scope.plan.Path, scope.plan.Revision = input.Path, input.Revision
	digest := sha256.New()
	_, _ = io.WriteString(digest, "pb-docs-delete-tree/v1\x00"+actor.ID+"\x00"+input.Path+"\x00"+input.Revision+"\x00"+status.Branch+"\x00"+status.Head+"\x00")
	for _, entry := range scope.entries {
		_, _ = fmt.Fprintf(digest, "%s\x00%t\x00%d\x00%s\x00", entry.Path, entry.Directory, entry.Size, scope.hashes[entry.Path])
	}
	scope.plan.PlanRevision = hex.EncodeToString(digest.Sum(nil))
	scope.plan.Entries = make([]DeleteTreeEntry, 0, min(len(scope.entries), MaxDeleteTreePreviewEntries))
	for _, entry := range scope.entries[:min(len(scope.entries), MaxDeleteTreePreviewEntries)] {
		scope.plan.Entries = append(scope.plan.Entries, DeleteTreeEntry{Path: entry.Path, Directory: entry.Directory, Size: entry.Size})
	}
	scope.plan.Truncated = len(scope.entries) > MaxDeleteTreePreviewEntries
	return scope, nil
}

// inspectDeleteTree deliberately does not use walkSource: that scanner skips
// unmanaged names, while a destructive operation must refuse them without
// traversing or silently deleting their data.
func (s *Service) inspectDeleteTree(ctx context.Context, folder string, tree sourceTree) (deleteTreeScope, error) {
	root, err := os.OpenRoot(s.cfg.Root)
	if err != nil {
		return deleteTreeScope{}, err
	}
	defer root.Close()
	if err := noSymlinks(root, folder); err != nil {
		return deleteTreeScope{}, err
	}
	if err := checkPathCase(root, folder); err != nil {
		return deleteTreeScope{}, err
	}
	info, err := root.Lstat(folder)
	if os.IsNotExist(err) {
		return deleteTreeScope{}, ErrNotFound
	}
	if err != nil {
		return deleteTreeScope{}, err
	}
	if !info.IsDir() {
		return deleteTreeScope{}, ErrInvalid
	}
	result := deleteTreeScope{entries: []Entry{{Path: folder, Directory: true}}, hashes: make(map[string]string)}
	result.plan.DirectoryCount = 1
	var walk func(string, int) error
	walk = func(parent string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 64 {
			return ErrLimit
		}
		directory, err := root.Open(parent)
		if err != nil {
			return err
		}
		entries, readErr := directory.ReadDir(MaxDeleteTreeEntries + 1)
		closeErr := directory.Close()
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(entries) > MaxDeleteTreeEntries-len(result.entries) {
			return ErrLimit
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := path.Join(parent, entry.Name())
			if !validPath(name) || entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: folder contains unmanaged entries", ErrConflict)
			}
			if len(result.entries) >= MaxDeleteTreeEntries {
				return ErrLimit
			}
			if entry.IsDir() {
				result.entries = append(result.entries, Entry{Path: name, Directory: true})
				result.plan.DirectoryCount++
				if err := walk(name, depth+1); err != nil {
					return err
				}
				continue
			}
			if !allowedFile(name) || !entry.Type().IsRegular() {
				return fmt.Errorf("%w: folder contains unmanaged entries", ErrConflict)
			}
			result.plan.FileCount++
			if result.plan.FileCount > MaxDeleteTreeFiles {
				return ErrLimit
			}
			data, err := readSource(s.cfg.Root, name, MaxBinaryBytes)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(data)
			hash := hex.EncodeToString(digest[:])
			if previous, exists := tree.Files[name]; !exists || previous.Hash != hash || previous.Size != int64(len(data)) {
				return ErrConflict
			}
			result.plan.TotalBytes += int64(len(data))
			if result.plan.TotalBytes > MaxDeleteTreeBytes {
				return ErrLimit
			}
			result.entries = append(result.entries, Entry{Path: name, Size: int64(len(data))})
			result.hashes[name] = hash
		}
		return nil
	}
	if err := walk(folder, 0); err != nil {
		return deleteTreeScope{}, err
	}
	sort.Slice(result.entries, func(i, j int) bool { return result.entries[i].Path < result.entries[j].Path })
	return result, nil
}

func expandedDeleteTree(scope deleteTreeScope) []Change {
	changes := make([]Change, 0, len(scope.entries))
	var directories []string
	for _, entry := range scope.entries {
		if entry.Directory {
			directories = append(directories, entry.Path)
		} else {
			changes = append(changes, Change{Operation: "delete", Path: entry.Path})
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		if len(directories[i]) == len(directories[j]) {
			return directories[i] > directories[j]
		}
		return len(directories[i]) > len(directories[j])
	})
	for _, directory := range directories {
		changes = append(changes, Change{Operation: "rmdir", Path: directory})
	}
	return changes
}
