package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const deleteTreeJournalName = "delete-tree.json"
const deleteTreeTransactionPrefix = ".txn-delete-tree-"

type deleteTreeJournal struct {
	Format          string   `json:"format"`
	ID              string   `json:"id"`
	Phase           string   `json:"phase"`
	Path            string   `json:"path"`
	DraftID         string   `json:"draftId"`
	DraftRevision   string   `json:"draftRevision"`
	Before          string   `json:"before"`
	After           string   `json:"after"`
	PreviousHistory string   `json:"previousHistory"`
	NextHistory     string   `json:"nextHistory"`
	Branch          string   `json:"branch"`
	Head            string   `json:"head"`
	Commit          string   `json:"commit,omitempty"`
	Paths           []string `json:"paths"`
	Actor           Actor    `json:"actor"`
	Message         string   `json:"message"`
}

type deleteTreeTransaction struct {
	service *Service
	journal deleteTreeJournal
}

func (t *deleteTreeTransaction) name() string { return deleteTreeTransactionPrefix + t.journal.ID }
func (t *deleteTreeTransaction) directory() string {
	return filepath.Join(t.service.cfg.StateDir, t.name())
}

func (t *deleteTreeTransaction) historyMatches(ctx context.Context, name, hash string) error {
	root, err := os.OpenRoot(t.service.cfg.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := noSymlinks(root, name); err != nil {
		return err
	}
	actual, _, err := historyGeneration(ctx, filepath.Join(t.service.cfg.StateDir, filepath.FromSlash(name)))
	if err != nil || actual != hash {
		return errors.Join(ErrOwnership, err)
	}
	return nil
}

func (t *deleteTreeTransaction) nextHistoryMatches(ctx context.Context) error {
	if err := t.historyMatches(ctx, "history", t.journal.NextHistory); err != nil {
		return err
	}
	marker, err := readSource(t.service.cfg.StateDir, "history/"+deleteTreeGeneration, 128)
	if err != nil || string(marker) != t.journal.ID {
		return errors.Join(ErrOwnership, err)
	}
	return nil
}

func (t *deleteTreeTransaction) syncParents() error {
	return errors.Join(syncStorageDirectory(t.service.cfg.StateDir, "."), syncStorageDirectory(t.directory(), "."))
}

func (t *deleteTreeTransaction) install(ctx context.Context) error {
	if err := t.historyMatches(ctx, "history", t.journal.PreviousHistory); err != nil {
		return err
	}
	root, err := os.OpenRoot(t.service.cfg.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Rename("history", t.name()+"/history-previous"); err != nil {
		return err
	}
	if err := t.syncParents(); err != nil {
		return err
	}
	if err := root.Rename(t.name()+"/history-next", "history"); err != nil {
		return err
	}
	return t.syncParents()
}

func (t *deleteTreeTransaction) rollbackHistory(ctx context.Context) error {
	previous := t.name() + "/history-previous"
	exists, err := storagePathExists(t.service.cfg.StateDir, previous)
	if err != nil {
		return err
	}
	if !exists {
		return t.historyMatches(ctx, "history", t.journal.PreviousHistory)
	}
	if err := t.historyMatches(ctx, previous, t.journal.PreviousHistory); err != nil {
		return err
	}
	root, err := os.OpenRoot(t.service.cfg.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()
	if exists, err := storagePathExists(t.service.cfg.StateDir, "history"); err != nil {
		return err
	} else if exists {
		if err := t.nextHistoryMatches(ctx); err != nil {
			return err
		}
		if err := root.Rename("history", t.name()+"/history-next"); err != nil {
			return err
		}
	}
	if err := root.Rename(previous, "history"); err != nil {
		return err
	}
	return t.syncParents()
}

func (t *deleteTreeTransaction) markPublished() error {
	journal := t.journal
	journal.Phase = "published"
	if err := writeStorageJSON(t.service.cfg.StateDir, deleteTreeJournalName, journal); err != nil {
		return err
	}
	t.journal = journal
	return nil
}

func (t *deleteTreeTransaction) recordCommit(commit string) error {
	if !validDeleteTreeHead(commit) || commit == "" {
		return ErrInvalid
	}
	journal := t.journal
	journal.Commit = commit
	if err := writeStorageJSON(t.service.cfg.StateDir, deleteTreeJournalName, journal); err != nil {
		return err
	}
	t.journal = journal
	return nil
}

func (t *deleteTreeTransaction) cleanup() error {
	exists, err := storagePathExists(t.service.cfg.StateDir, t.name())
	if err != nil {
		return err
	}
	if exists {
		owner, err := readSource(t.directory(), ".storage-owner", 128)
		if err != nil || string(owner) != t.journal.ID {
			return errors.Join(ErrOwnership, err)
		}
		if err := removeStorageTree(t.service.cfg.StateDir, t.name()); err != nil {
			return err
		}
	}
	if err := removeStorageFile(t.service.cfg.StateDir, deleteTreeJournalName); err != nil {
		return err
	}
	t.service.deleteTreePending = false
	return nil
}

// The snapshots carry the file hashes, while this bounded journal records the
// authority and commit points. It is durable before history or source changes.
func (s *Service) prepareDeleteTreeTransaction(ctx context.Context, transaction, id string, scope deleteTreeScope, before, after sourceTree, paths []string, draft Draft, input PublishInput, actor Actor, status SyncStatus) (*deleteTreeTransaction, error) {
	if exists, err := storagePathExists(s.cfg.StateDir, deleteTreeJournalName); err != nil {
		return nil, err
	} else if exists {
		return nil, ErrConflict
	}
	previous, next, err := s.reserveDeleteTreeHistory(ctx, transaction, id, before, after, paths, actor, status.Branch)
	if err != nil {
		return nil, err
	}
	// Each component has its own existing source/output/history budget. Do
	// not count their bounded temporary copies as one Git metadata tree.
	for _, name := range []string{"source", "backup", "site", "history-next"} {
		if err := syncStorageTree(ctx, filepath.Join(transaction, name)); err != nil {
			return nil, err
		}
	}
	if err := syncStorageDirectory(transaction, "."); err != nil {
		return nil, err
	}
	t := &deleteTreeTransaction{service: s, journal: deleteTreeJournal{Format: "pb-docs-delete-tree/v1", ID: id, Phase: "prepared", Path: scope.plan.Path, DraftID: draft.ID, DraftRevision: draft.Revision, Before: before.Revision, After: after.Revision, PreviousHistory: previous, NextHistory: next, Branch: status.Branch, Head: status.Head, Paths: paths, Actor: actor, Message: input.Message}}
	s.deleteTreePending = true
	if err := writeStorageJSON(s.cfg.StateDir, deleteTreeJournalName, t.journal); err != nil {
		// A directory-sync error can follow a successful atomic rename. Keep
		// both generations whenever the journal may already be visible.
		if _, readErr := readSource(s.cfg.StateDir, deleteTreeJournalName, 256<<10); errors.Is(readErr, ErrNotFound) {
			s.deleteTreePending = false
			return nil, err
		}
		return t, err
	}
	return t, nil
}

// sourceTreeDigest uses the same sorted directory traversal as scanTree. It
// lets recovery validate the complete original snapshot before touching files.
func sourceTreeDigest(tree sourceTree, identity string) string {
	children := make(map[string][]string)
	directories := make(map[string]bool, len(tree.Dirs))
	for _, name := range tree.Dirs {
		directories[name] = true
		children[path.Dir(name)] = append(children[path.Dir(name)], name)
	}
	for name := range tree.Files {
		children[path.Dir(name)] = append(children[path.Dir(name)], name)
	}
	for parent := range children {
		sort.Strings(children[parent])
	}
	digest := sha256.New()
	_, _ = io.WriteString(digest, identity+"\x00")
	var visit func(string)
	visit = func(parent string) {
		for _, name := range children[parent] {
			if directories[name] {
				_, _ = io.WriteString(digest, "d\x00"+name+"\x00")
				visit(name)
			} else {
				_, _ = io.WriteString(digest, "f\x00"+name+"\x00"+tree.Files[name].Hash+"\x00")
			}
		}
	}
	visit(".")
	return hex.EncodeToString(digest.Sum(nil))
}

func (t *deleteTreeTransaction) snapshots(ctx context.Context) (sourceTree, sourceTree, error) {
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

func mixedDeleteTree(current, before, after sourceTree) bool {
	for name, state := range current.Files {
		previous, hadBefore := before.Files[name]
		next, hasAfter := after.Files[name]
		if (!hadBefore || state.Hash != previous.Hash) && (!hasAfter || state.Hash != next.Hash) {
			return false
		}
	}
	for name := range after.Files {
		if _, exists := current.Files[name]; !exists {
			return false
		}
	}
	oldDirs, newDirs := make(map[string]bool), make(map[string]bool)
	for _, name := range before.Dirs {
		oldDirs[name] = true
	}
	for _, name := range current.Dirs {
		if !oldDirs[name] {
			return false
		}
		newDirs[name] = true
	}
	for _, name := range after.Dirs {
		if !newDirs[name] {
			return false
		}
	}
	return true
}

func (t *deleteTreeTransaction) restoreSources(ctx context.Context, before, after sourceTree) error {
	identity := t.journal.Branch + "\x00" + t.journal.Head
	current, err := scanTree(ctx, t.service.cfg.Root, identity, "")
	if err != nil {
		return err
	}
	if !mixedDeleteTree(current, before, after) {
		return ErrConflict
	}
	// Reject unmanaged additions even though normal source scans skip them.
	if _, err := t.service.inspectDeleteTree(ctx, t.journal.Path, current); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := applyFiles(ctx, t.service.cfg.Root, filepath.Join(t.directory(), "backup"), t.journal.Paths, before); err != nil {
		return err
	}
	verified, err := scanTree(ctx, t.service.cfg.Root, identity, "")
	if err != nil || verified.Revision != before.Revision {
		return errors.Join(ErrConflict, err)
	}
	t.service.cached = sourceTree{}
	return nil
}

func validDeleteTreeHead(head string) bool {
	if head == "" {
		return true
	}
	if len(head) != 40 && len(head) != 64 {
		return false
	}
	for _, char := range head {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func validDeleteTreeJournal(j deleteTreeJournal) bool {
	if j.Format != "pb-docs-delete-tree/v1" || !validID(j.ID) || j.Phase != "prepared" && j.Phase != "published" || !validPath(j.Path) || j.Path == "docs.json" || !validID(j.DraftID) || !validRevision(j.DraftRevision) || !validRevision(j.Before) || !validRevision(j.After) || !validRevision(j.PreviousHistory) || !validRevision(j.NextHistory) || len(j.Paths) > MaxDeleteTreeFiles+1 || len(j.Branch) > 200 || !validDeleteTreeHead(j.Head) || !validDeleteTreeHead(j.Commit) || len(j.Message) > 4096 {
		return false
	}
	if _, err := normalizeActor(j.Actor); err != nil {
		return false
	}
	seen := make(map[string]bool, len(j.Paths))
	for _, name := range j.Paths {
		if seen[name] || !allowedFile(name) || name != "docs.json" && !sourceInTree(name, j.Path) {
			return false
		}
		seen[name] = true
	}
	return true
}

func (s *Service) recoverPublishedDeleteTree(ctx context.Context, t *deleteTreeTransaction) error {
	journal := t.journal
	if err := t.nextHistoryMatches(ctx); err != nil {
		return err
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return err
	}
	if status.Branch != journal.Branch || status.Head != journal.Head && (journal.Commit == "" || status.Head != journal.Commit) {
		return ErrConflict
	}
	// A legitimate commit changes the project identity. Compare the complete
	// file/directory tree using the original identity, never an arbitrary HEAD.
	current, err := scanTree(ctx, s.cfg.Root, journal.Branch+"\x00"+journal.Head, "")
	if err != nil || current.Revision != journal.After {
		return errors.Join(ErrConflict, err)
	}
	if _, err := s.inspectDeleteTree(ctx, journal.Path, current); err == nil || !errors.Is(err, ErrNotFound) {
		return errors.Join(ErrConflict, err)
	}
	if status.Head == journal.Head {
		if err := s.recoverDeleteTreePending(ctx, journal, current, status); err != nil {
			return err
		}
	}
	return t.cleanup()
}

func (s *Service) recoverDeleteTree(ctx context.Context) error {
	data, err := readSource(s.cfg.StateDir, deleteTreeJournalName, 256<<10)
	if errors.Is(err, ErrNotFound) {
		return s.cleanupDeleteTreeOrphans(ctx)
	}
	if err != nil {
		return err
	}
	var journal deleteTreeJournal
	if json.Unmarshal(data, &journal) != nil || !validDeleteTreeJournal(journal) {
		return ErrOwnership
	}
	t := &deleteTreeTransaction{service: s, journal: journal}
	s.deleteTreePending = true
	if journal.Phase == "published" {
		if err := s.recoverPublishedDeleteTree(ctx, t); err != nil {
			return err
		}
		return s.cleanupDeleteTreeOrphans(ctx)
	}
	owner, err := readSource(t.directory(), ".storage-owner", 128)
	if errors.Is(err, ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		exists, existsErr := storagePathExists(s.cfg.StateDir, t.name())
		if existsErr != nil {
			return existsErr
		}
		if !exists {
			return s.recoverRemovedDeleteTreeStage(ctx, t)
		}
	}
	if err != nil || string(owner) != journal.ID {
		return errors.Join(ErrOwnership, err)
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return err
	}
	// A changed authority is never reset or checked out during recovery.
	if status.Branch != journal.Branch || status.Head != journal.Head {
		return ErrConflict
	}
	before, after, err := t.snapshots(ctx)
	if err != nil {
		return err
	}
	current, err := scanTree(ctx, s.cfg.Root, storageIdentity(status), "")
	if err != nil {
		return err
	}
	if current.Revision == after.Revision {
		if err := t.nextHistoryMatches(ctx); err != nil {
			return err
		}
		if _, err := s.inspectDeleteTree(ctx, journal.Path, current); err == nil || !errors.Is(err, ErrNotFound) {
			return errors.Join(ErrConflict, err)
		}
		if err := s.recoverDeleteTreePending(ctx, journal, after, status); err != nil {
			return err
		}
		if err := t.markPublished(); err != nil {
			return err
		}
	} else {
		if err := t.restoreSources(ctx, before, after); err != nil {
			return err
		}
		if err := t.rollbackHistory(ctx); err != nil {
			return err
		}
	}
	if err := t.cleanup(); err != nil {
		return err
	}
	return s.cleanupDeleteTreeOrphans(ctx)
}

// Cleanup can remove a fully rolled-back stage before the final journal
// unlink becomes durable. Only the exact original authority, source and
// history together establish that this missing stage is safe to forget.
func (s *Service) recoverRemovedDeleteTreeStage(ctx context.Context, t *deleteTreeTransaction) error {
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return err
	}
	if status.Branch != t.journal.Branch || status.Head != t.journal.Head {
		return ErrConflict
	}
	current, err := scanTree(ctx, s.cfg.Root, storageIdentity(status), "")
	if err != nil || current.Revision != t.journal.Before {
		return errors.Join(ErrConflict, err)
	}
	if err := t.historyMatches(ctx, "history", t.journal.PreviousHistory); err != nil {
		return err
	}
	return t.cleanup()
}

func (s *Service) recoverDeleteTreePending(ctx context.Context, journal deleteTreeJournal, after sourceTree, status SyncStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if status.Backend != "git" {
		return nil
	}
	if pending, err := s.pending(); err == nil {
		if pending.Revision == after.Revision {
			return nil
		}
		if pending.Commit == "" || pending.Branch != journal.Branch {
			return ErrConflict
		}
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	paths := append([]string(nil), journal.Paths...)
	hashes := make(map[string]string, len(paths))
	for _, name := range paths {
		hashes[name] = after.Files[name].Hash
	}
	return s.writeState("pending.json", pendingSync{Revision: after.Revision, Paths: paths, Actor: journal.Actor, Message: journal.Message, Branch: journal.Branch, Hashes: hashes})
}

func (s *Service) cleanupDeleteTreeOrphans(ctx context.Context) error {
	root, err := os.OpenRoot(s.cfg.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(2*MaxDrafts + MaxHistory + 64)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if err := errors.Join(readErr, dir.Close()); err != nil {
		return err
	}
	if len(entries) >= 2*MaxDrafts+MaxHistory+64 {
		return ErrLimit
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasPrefix(entry.Name(), deleteTreeTransactionPrefix) {
			continue
		}
		id := strings.TrimPrefix(entry.Name(), deleteTreeTransactionPrefix)
		if !validID(id) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return ErrOwnership
		}
		if err := noSymlinks(root, entry.Name()); err != nil {
			return err
		}
		owner, err := readSource(filepath.Join(s.cfg.StateDir, entry.Name()), ".storage-owner", 128)
		if err != nil || string(owner) != id {
			return errors.Join(ErrOwnership, err)
		}
		if err := removeStorageTree(s.cfg.StateDir, entry.Name()); err != nil {
			return err
		}
	}
	return nil
}
