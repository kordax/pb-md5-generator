package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func decodePublication(data []byte, s *Service) (publicationJournal, error) {
	var journal publicationJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil || !validPublicationJournal(journal, s) {
		return journal, errors.Join(ErrOwnership, err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return journal, ErrOwnership
	}
	return journal, nil
}

func (t *publicationTransaction) stageExists() (bool, error) {
	exists, err := storagePathExists(t.service.cfg.StateDir, t.name())
	if err != nil || !exists {
		return exists, err
	}
	owner, err := readSource(t.directory(), ".storage-owner", 128)
	if err != nil || string(owner) != t.journal.ID {
		return true, errors.Join(ErrOwnership, err)
	}
	return true, nil
}

func (t *publicationTransaction) checkStage(ctx context.Context) (bool, error) {
	exists, err := t.stageExists()
	if err != nil || !exists {
		return exists, err
	}
	root, err := os.OpenRoot(t.directory())
	if err != nil {
		return true, err
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return true, err
	}
	entries, err := readPublicationDirectoryEntries(file, 8)
	if err != nil {
		return true, err
	}
	if len(entries) > 7 {
		return true, ErrOwnership
	}
	for _, entry := range entries {
		name := entry.Name()
		if (name == ".storage-owner" || name == publicationHistoryPlanName) && entry.Type().IsRegular() {
			continue
		}
		if name != "source" && name != "backup" && name != "site" && name != "history-next" && name != "history-previous" || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return true, ErrOwnership
		}
		if name == "site" {
			actual, err := publicationOutputDigest(ctx, filepath.Join(t.directory(), name), false)
			if err != nil || actual != t.journal.OutputAfter {
				return true, errors.Join(ErrOwnership, err)
			}
		}
	}
	p, err := t.historyPlan()
	if err != nil {
		return true, err
	}
	if t.journal.Phase != "published" {
		if err := t.deltaHistoryPlacement(ctx, p); err != nil {
			return true, err
		}
	}
	_, _, err = t.snapshots(ctx)
	return true, err
}

// Retire a verified tree before removing its contents. If deleting a child or
// unlinking the directory fails, the journal can still recognize the original
// location as removed. A retired remainder is kept for operator inspection.
func removePublicationTree(parent, name string) error {
	retired := filepath.Join(parent, ".retired-"+name)
	if _, err := renamePublication(filepath.Join(parent, name), retired); err != nil {
		return err
	}
	return removeStorageTree(parent, filepath.Base(retired))
}

// Cleanup is the only path that clears the service's publication guard. The
// previous output remains beside the live output until these proofs succeed.
func (t *publicationTransaction) cleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), t.service.cfg.OperationTimeout)
	defer cancel()
	if t.journal.Phase == "published" {
		if err := t.verifyAfter(ctx); err != nil {
			return err
		}
	} else if err := t.verifyBefore(ctx); err != nil {
		return err
	}
	exists, err := t.checkStage(ctx)
	if err != nil {
		return err
	}
	hasHolder, err := t.checkOutputHolder(ctx)
	if err != nil {
		return err
	}
	if t.journal.Phase == "published" && exists {
		if err := t.moveDeltaHistory(ctx); err != nil {
			return err
		}
		if err := t.verifyAfter(ctx); err != nil {
			return err
		}
	}
	if hasHolder {
		if err := removePublicationTree(t.outputParent(), t.outputHolder()); err != nil {
			return err
		}
	}
	if exists {
		if err := removePublicationTree(t.service.cfg.StateDir, t.name()); err != nil {
			return err
		}
	}
	if err := removeStorageFile(t.service.cfg.StateDir, publicationJournalName); err != nil {
		return err
	}
	t.service.publicationPending = false
	t.service.cached = sourceTree{}
	return nil
}

func (s *Service) recoverPublicationPending(ctx context.Context, t *publicationTransaction) error {
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return err
	}
	if status.Backend != "git" || status.Head != t.journal.Head {
		return nil
	}
	if pending, err := s.pending(); err == nil {
		if pending.Revision == t.journal.After {
			if pending.Branch != t.journal.Branch || len(pending.Paths) != len(t.journal.Paths) || len(pending.Hashes) != len(t.journal.Paths) {
				return ErrConflict
			}
			seen := make(map[string]bool, len(pending.Paths))
			for _, name := range pending.Paths {
				seen[name] = true
			}
			after, err := t.currentSource(ctx)
			if err != nil || after.Revision != t.journal.After {
				return errors.Join(ErrOwnership, err)
			}
			for _, name := range t.journal.Paths {
				hash, exists := pending.Hashes[name]
				if !seen[name] || !exists || hash != after.Files[name].Hash {
					return ErrConflict
				}
			}
			if pending.Actor.ID != t.journal.Actor.ID || pending.Message != t.journal.Message {
				return ErrConflict
			}
			return nil
		}
		if pending.Commit == "" || pending.Commit != t.journal.Head || pending.Branch != t.journal.Branch {
			return ErrConflict
		}
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	after, err := t.currentSource(ctx)
	if err != nil || after.Revision != t.journal.After {
		return errors.Join(ErrOwnership, err)
	}
	hashes := make(map[string]string, len(t.journal.Paths))
	for _, name := range t.journal.Paths {
		hashes[name] = after.Files[name].Hash
	}
	pending := pendingSync{Revision: t.journal.After, Paths: append([]string(nil), t.journal.Paths...), Actor: t.journal.Actor, Message: t.journal.Message, Branch: t.journal.Branch, Hashes: hashes}
	return writeStorageJSON(s.cfg.StateDir, "pending.json", pending)
}

func (s *Service) recoverPublication(ctx context.Context) error {
	data, err := readSource(s.cfg.StateDir, publicationJournalName, maxStorageJSONBytes)
	if errors.Is(err, ErrNotFound) {
		return s.checkLegacyPublications(ctx)
	}
	if err != nil {
		return err
	}
	journal, err := decodePublication(data, s)
	if err != nil {
		return err
	}
	s.publicationPending = true
	if exists, err := storagePathExists(s.cfg.StateDir, deleteTreeJournalName); err != nil || exists {
		return errors.Join(ErrConflict, err)
	}
	t := &publicationTransaction{service: s, journal: journal}
	if journal.Phase == "published" {
		if err := t.verifyAfter(ctx); err != nil {
			return err
		}
		if err := s.recoverPublicationPending(ctx, t); err != nil {
			return err
		}
		if err := t.cleanup(); err != nil {
			return err
		}
		return s.checkLegacyPublications(ctx)
	}
	if err := t.authority(ctx, false); err != nil {
		return err
	}
	exists, err := t.stageExists()
	if err != nil {
		return err
	}
	if !exists || journal.Phase == "rolled-back" {
		if err := t.verifyBefore(ctx); err != nil {
			return err
		}
		if err := t.cleanup(); err != nil {
			return err
		}
		return s.checkLegacyPublications(ctx)
	}
	if _, _, err := t.snapshots(ctx); err != nil {
		return err
	}
	current, err := t.currentSource(ctx)
	if err != nil {
		return err
	}
	output, outputErr := publicationOutputDigest(ctx, s.cfg.OutputDir, false)
	if current.Revision == journal.After && outputErr == nil && output == journal.OutputAfter && t.nextHistoryMatches(ctx) == nil {
		if err := t.markPublished(); err != nil {
			return err
		}
		if err := s.recoverPublicationPending(ctx, t); err != nil {
			return err
		}
	} else if err := t.rollback(ctx); err != nil {
		return err
	}
	if err := t.cleanup(); err != nil {
		return err
	}
	return s.checkLegacyPublications(ctx)
}

// A legacy directory has no durable authority proof. In particular a backup
// can be the only surviving original after a failed rollback, so never remove
// it or rebuild from possibly mixed live sources during startup.
func (s *Service) checkLegacyPublications(ctx context.Context) error {
	root, err := os.OpenRoot(s.cfg.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()
	directory, err := root.Open(".")
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
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if !strings.HasPrefix(name, ".txn-") || strings.HasPrefix(name, deleteTreeTransactionPrefix) || strings.HasPrefix(name, ".txn-storage-") {
			continue
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return ErrOwnership
		}
		if exists, err := storagePathExists(s.cfg.StateDir, name+"/backup"); err != nil || exists {
			return errors.Join(ErrConflict, err)
		}
		if strings.HasPrefix(name, publicationTransactionPrefix) && validID(strings.TrimPrefix(name, publicationTransactionPrefix)) {
			id := strings.TrimPrefix(name, publicationTransactionPrefix)
			owner, err := readSource(s.cfg.StateDir, name+"/.storage-owner", 128)
			if err != nil || string(owner) != id {
				return errors.Join(ErrOwnership, err)
			}
			if exists, err := storagePathExists(filepath.Dir(s.cfg.OutputDir), publicationOutputPrefix(s.cfg.OutputDir)+id); err != nil || exists {
				return errors.Join(ErrConflict, err)
			}
			if err := removePublicationTree(s.cfg.StateDir, name); err != nil {
				return err
			}
		}
	}
	return s.checkOrphanOutputs(ctx)
}

func (s *Service) checkOrphanOutputs(ctx context.Context) error {
	root, err := os.OpenRoot(filepath.Dir(s.cfg.OutputDir))
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := readPublicationDirectoryEntries(file, MaxFiles+1)
	if err != nil {
		return err
	}
	if len(entries) > MaxFiles {
		return ErrLimit
	}
	prefix := publicationOutputPrefix(s.cfg.OutputDir)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), prefix) {
			return ErrConflict
		}
	}
	return nil
}
