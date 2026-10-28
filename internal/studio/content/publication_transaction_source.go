package content

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
)

func (t *publicationTransaction) authority(ctx context.Context, allowCommit bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := t.service
	for i, directory := range []string{s.cfg.Root, s.cfg.StateDir} {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || len(s.locks) <= i {
			return errors.Join(ErrOwnership, err)
		}
		root, err := os.OpenRoot(directory)
		if err != nil {
			return err
		}
		if err := noSymlinks(root, ".studio-lock"); err != nil {
			return errors.Join(err, root.Close())
		}
		current, statErr := root.Stat(".studio-lock")
		held, heldErr := s.locks[i].Stat()
		closeErr := root.Close()
		if statErr != nil || heldErr != nil || closeErr != nil || !os.SameFile(current, held) {
			return errors.Join(ErrOwnership, statErr, heldErr, closeErr)
		}
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return err
	}
	if status.Backend != t.journal.Backend || status.Branch != t.journal.Branch || status.Head != t.journal.Head && (!allowCommit || t.journal.Commit == "" || status.Head != t.journal.Commit) {
		return ErrConflict
	}
	return nil
}

func (t *publicationTransaction) snapshots(ctx context.Context) (sourceTree, sourceTree, error) {
	root, err := os.OpenRoot(t.service.cfg.StateDir)
	if err != nil {
		return sourceTree{}, sourceTree{}, err
	}
	defer root.Close()
	for _, name := range []string{"source", "backup"} {
		if err := noSymlinks(root, t.name()+"/"+name); err != nil {
			return sourceTree{}, sourceTree{}, err
		}
	}
	identity := t.journal.Branch + "\x00" + t.journal.Head
	after, err := scanTree(ctx, filepath.Join(t.directory(), "source"), identity, "")
	if err != nil || after.Revision != t.journal.After {
		return sourceTree{}, sourceTree{}, errors.Join(ErrOwnership, err)
	}
	backup, err := scanTree(ctx, filepath.Join(t.directory(), "backup"), identity, "")
	if err != nil {
		return sourceTree{}, sourceTree{}, err
	}
	for name, tree := range map[string]sourceTree{"source": after, "backup": backup} {
		if err := publicationSnapshotContents(ctx, filepath.Join(t.directory(), name), tree); err != nil {
			return sourceTree{}, sourceTree{}, err
		}
	}
	before := sourceTree{Files: make(map[string]fileState, len(after.Files)+len(backup.Files)), Dirs: backup.Dirs, Revision: t.journal.Before}
	for name, state := range after.Files {
		before.Files[name] = state
	}
	paths := make(map[string]bool, len(t.journal.Paths))
	for _, name := range t.journal.Paths {
		paths[name] = true
		if state, exists := backup.Files[name]; exists {
			before.Files[name] = state
		} else {
			delete(before.Files, name)
		}
	}
	for name := range backup.Files {
		if !paths[name] {
			return sourceTree{}, sourceTree{}, ErrOwnership
		}
	}
	if sourceTreeDigest(before, identity) != before.Revision {
		return sourceTree{}, sourceTree{}, ErrOwnership
	}
	return before, after, nil
}

// Live writes replace each file atomically. An interrupted batch can contain
// either image of a changed path, but cannot lose or invent unaffected files.
func knownPublicationSource(current, before, after sourceTree) bool {
	for name, state := range current.Files {
		previous, hadBefore := before.Files[name]
		next, hasAfter := after.Files[name]
		if (!hadBefore || state.Hash != previous.Hash) && (!hasAfter || state.Hash != next.Hash) {
			return false
		}
	}
	for name := range before.Files {
		if _, shared := after.Files[name]; shared {
			if _, exists := current.Files[name]; !exists {
				return false
			}
		}
	}
	previousDirs, nextDirs, currentDirs := make(map[string]bool), make(map[string]bool), make(map[string]bool)
	for _, name := range before.Dirs {
		previousDirs[name] = true
	}
	for _, name := range after.Dirs {
		nextDirs[name] = true
	}
	for _, name := range current.Dirs {
		if !previousDirs[name] && !nextDirs[name] {
			return false
		}
		currentDirs[name] = true
	}
	for name := range previousDirs {
		if nextDirs[name] && !currentDirs[name] {
			return false
		}
	}
	return true
}

// Do not remove a directory containing a file the managed scanner deliberately
// excludes. Recovery must preserve such additions instead of following them.
func (t *publicationTransaction) checkRemovedDirectories(ctx context.Context, current, before, after sourceTree) error {
	keep := make(map[string]bool, len(before.Dirs))
	for _, name := range before.Dirs {
		keep[name] = true
	}
	root, err := os.OpenRoot(t.service.cfg.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	visited := make(map[string]bool)
	var inspect func(string) error
	inspect = func(name string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if visited[name] {
			return nil
		}
		visited[name] = true
		if len(visited) > MaxFiles {
			return ErrLimit
		}
		if err := noSymlinks(root, name); err != nil {
			return err
		}
		directory, err := root.Open(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		entries, err := readPublicationDirectoryEntries(directory, MaxFiles+1)
		if err != nil {
			return err
		}
		if len(entries) > MaxFiles {
			return ErrLimit
		}
		for _, entry := range entries {
			child := path.Join(name, entry.Name())
			if !validPath(child) || entry.Type()&os.ModeSymlink != 0 {
				return ErrOwnership
			}
			if entry.IsDir() {
				if err := inspect(child); err != nil {
					return err
				}
			} else if _, exists := current.Files[child]; !exists || !entry.Type().IsRegular() {
				return ErrOwnership
			}
		}
		return nil
	}
	for _, name := range after.Dirs {
		if !keep[name] {
			if err := inspect(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *publicationTransaction) currentSource(ctx context.Context) (sourceTree, error) {
	return scanTree(ctx, t.service.cfg.Root, t.journal.Branch+"\x00"+t.journal.Head, "")
}

func (t *publicationTransaction) syncSources(ctx context.Context) error {
	tree, err := t.currentSource(ctx)
	if err != nil {
		return err
	}
	for i := len(tree.Dirs) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := syncStorageDirectory(t.service.cfg.Root, tree.Dirs[i]); err != nil {
			return err
		}
	}
	return syncStorageDirectory(t.service.cfg.Root, ".")
}

func (t *publicationTransaction) verifyBefore(ctx context.Context) error {
	if err := t.authority(ctx, false); err != nil {
		return err
	}
	current, err := t.currentSource(ctx)
	if err != nil || current.Revision != t.journal.Before {
		return errors.Join(ErrOwnership, err)
	}
	output, err := publicationOutputDigest(ctx, t.service.cfg.OutputDir, true)
	if err != nil || output != t.journal.OutputBefore {
		return errors.Join(ErrOwnership, err)
	}
	return t.historyMatches(ctx, "history", t.journal.PreviousHistory)
}

func (t *publicationTransaction) verifyAfter(ctx context.Context) error {
	if err := t.authority(ctx, true); err != nil {
		return err
	}
	current, err := t.currentSource(ctx)
	if err != nil || current.Revision != t.journal.After {
		return errors.Join(ErrOwnership, err)
	}
	output, err := publicationOutputDigest(ctx, t.service.cfg.OutputDir, false)
	if err != nil || output != t.journal.OutputAfter {
		return errors.Join(ErrOwnership, err)
	}
	return t.nextHistoryMatches(ctx)
}

func (t *publicationTransaction) rollback(ctx context.Context) error {
	if t.journal.Phase == "published" {
		return ErrConflict
	}
	if err := t.authority(ctx, false); err != nil {
		return err
	}
	before, after, err := t.snapshots(ctx)
	if err != nil {
		return err
	}
	current, err := t.currentSource(ctx)
	if err != nil || !knownPublicationSource(current, before, after) {
		return errors.Join(ErrOwnership, err)
	}
	if err := t.installDeltaHistory(ctx); err != nil {
		return err
	}
	if err := t.checkRemovedDirectories(ctx, current, before, after); err != nil {
		return err
	}
	if err := t.rollbackOutput(ctx); err != nil {
		return err
	}
	if err := applyFiles(ctx, t.service.cfg.Root, filepath.Join(t.directory(), "backup"), t.journal.Paths, before); err != nil {
		return err
	}
	if err := removeEmptyDirectories(t.service.cfg.Root, after.Dirs, before.Dirs); err != nil {
		return err
	}
	if err := t.syncSources(ctx); err != nil {
		return err
	}
	if err := t.verifyBefore(ctx); err != nil {
		return err
	}
	t.service.cached = sourceTree{}
	return t.storePhase("rolled-back")
}

// Private source snapshots must contain exactly the managed inventory. Unlike
// the live source tree, these directories have no legitimate ignored files.
func publicationSnapshotContents(ctx context.Context, directory string, tree sourceTree) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	dirs := make(map[string]bool, len(tree.Dirs))
	for _, name := range tree.Dirs {
		dirs[name] = true
	}
	count := 0
	var walk func(string, int) error
	walk = func(name string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 64 {
			return ErrLimit
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		entries, err := readPublicationDirectoryEntries(file, MaxFiles+1)
		if err != nil {
			return err
		}
		count += len(entries)
		if count > MaxFiles {
			return ErrLimit
		}
		for _, entry := range entries {
			child := path.Join(name, entry.Name())
			if !validPath(child) || entry.Type()&os.ModeSymlink != 0 {
				return ErrOwnership
			}
			if entry.IsDir() {
				if !dirs[child] {
					return ErrOwnership
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			} else if _, exists := tree.Files[child]; !exists || !entry.Type().IsRegular() {
				return ErrOwnership
			}
		}
		return nil
	}
	return walk(".", 0)
}
