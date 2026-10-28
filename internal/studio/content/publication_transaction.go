package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

const publicationJournalName = "publication.json"
const publicationTransactionPrefix = ".txn-publication-"

type publicationPlan struct {
	Before sourceTree
	After  sourceTree
	Paths  []string
	Draft  Draft
	Input  PublishInput
	Actor  Actor
	Status SyncStatus
}

type publicationJournal struct {
	Format          string   `json:"format"`
	ID              string   `json:"id"`
	Phase           string   `json:"phase"`
	Root            string   `json:"root"`
	Output          string   `json:"output"`
	Before          string   `json:"before"`
	After           string   `json:"after"`
	PreviousHistory string   `json:"previousHistory"`
	NextHistory     string   `json:"nextHistory"`
	HistoryDelta    bool     `json:"historyDelta,omitempty"`
	HistoryPlan     string   `json:"historyPlan,omitempty"`
	OutputBefore    string   `json:"outputBefore"`
	OutputAfter     string   `json:"outputAfter"`
	DraftID         string   `json:"draftId"`
	DraftRevision   string   `json:"draftRevision"`
	Backend         string   `json:"backend"`
	Branch          string   `json:"branch"`
	Head            string   `json:"head"`
	Commit          string   `json:"commit,omitempty"`
	Paths           []string `json:"paths"`
	Actor           Actor    `json:"actor"`
	Message         string   `json:"message"`
}

type publicationTransaction struct {
	service *Service
	journal publicationJournal
}

func (t *publicationTransaction) name() string { return publicationTransactionPrefix + t.journal.ID }
func (t *publicationTransaction) directory() string {
	return filepath.Join(t.service.cfg.StateDir, t.name())
}

func validPublicationJournal(j publicationJournal, s *Service) bool {
	if j.Format != "pb-docs-publication/v1" || !validID(j.ID) || j.Phase != "prepared" && j.Phase != "published" && j.Phase != "rolled-back" || j.Root != s.cfg.Root || j.Output != s.cfg.OutputDir || !validID(j.DraftID) || !validRevision(j.DraftRevision) || j.Backend != "local" && j.Backend != "git" || !validDeleteTreeHead(j.Head) || !validDeleteTreeHead(j.Commit) || len(j.Branch) > 200 || len(j.Message) > 4096 || len(j.Paths) > 2*MaxFiles {
		return false
	}
	if !j.HistoryDelta || !validRevision(j.HistoryPlan) {
		return false
	}
	for _, hash := range []string{j.Before, j.After, j.PreviousHistory, j.NextHistory, j.OutputBefore, j.OutputAfter} {
		if !validRevision(hash) {
			return false
		}
	}
	if _, err := normalizeActor(j.Actor); err != nil {
		return false
	}
	seen := make(map[string]bool, len(j.Paths))
	for _, name := range j.Paths {
		if !allowedFile(name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

// Preparation reserves every history record and syncs the only before-images
// before the journal authorizes any modification of the live publication.
func (s *Service) preparePublication(ctx context.Context, stage, id string, plan publicationPlan) (*publicationTransaction, error) {
	if !validID(id) || stage != filepath.Join(s.cfg.StateDir, publicationTransactionPrefix+id) {
		return nil, ErrOwnership
	}
	if exists, err := storagePathExists(s.cfg.StateDir, publicationJournalName); err != nil || exists {
		return nil, errors.Join(ErrConflict, err)
	}
	owner, err := readSource(stage, ".storage-owner", 128)
	if err != nil || string(owner) != id {
		return nil, errors.Join(ErrOwnership, err)
	}
	history, historyPlan, err := s.preparePublicationHistory(ctx, stage, plan)
	if err != nil {
		return nil, err
	}
	beforeOutput, err := publicationOutputDigest(ctx, s.cfg.OutputDir, true)
	if err != nil {
		return nil, err
	}
	afterOutput, err := publicationOutputDigest(ctx, filepath.Join(stage, "site"), false)
	if err != nil {
		return nil, err
	}
	t := &publicationTransaction{service: s, journal: publicationJournal{Format: "pb-docs-publication/v1", ID: id, Phase: "prepared", Root: s.cfg.Root, Output: s.cfg.OutputDir, Before: plan.Before.Revision, After: plan.After.Revision, PreviousHistory: publicationHistoryHash(history.Before), NextHistory: publicationHistoryHash(history.after()), HistoryDelta: true, HistoryPlan: historyPlan, OutputBefore: beforeOutput, OutputAfter: afterOutput, DraftID: plan.Draft.ID, DraftRevision: plan.Draft.Revision, Backend: plan.Status.Backend, Branch: plan.Status.Branch, Head: plan.Status.Head, Paths: append([]string(nil), plan.Paths...), Actor: plan.Actor, Message: plan.Input.Message}}
	if !validPublicationJournal(t.journal, s) {
		return nil, ErrInvalid
	}
	if err := t.authority(ctx, false); err != nil {
		return nil, err
	}
	before, after, err := t.snapshots(ctx)
	if err != nil || before.Revision != plan.Before.Revision || after.Revision != plan.After.Revision {
		return nil, errors.Join(ErrOwnership, err)
	}
	for _, name := range []string{"source", "backup", "site", "history-next"} {
		if err := syncStorageTree(ctx, filepath.Join(stage, name)); err != nil {
			return nil, err
		}
	}
	if err := syncStorageDirectory(stage, "."); err != nil {
		return nil, err
	}
	s.publicationPending = true
	if err := writeStorageJSON(s.cfg.StateDir, publicationJournalName, t.journal); err != nil {
		if _, checkErr := readSource(s.cfg.StateDir, publicationJournalName, maxStorageJSONBytes); errors.Is(checkErr, ErrNotFound) {
			s.publicationPending = false
			return nil, err
		}
		return t, err
	}
	return t, nil
}

func (t *publicationTransaction) historyMatches(ctx context.Context, name, hash string) error {
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

func (t *publicationTransaction) nextHistoryMatches(ctx context.Context) error {
	return t.nextDeltaHistoryMatches(ctx)
}

func (t *publicationTransaction) installHistory(ctx context.Context) error {
	if err := t.authority(ctx, false); err != nil {
		return err
	}
	return t.installDeltaHistory(ctx)
}

func (t *publicationTransaction) storePhase(phase string) error {
	journal := t.journal
	journal.Phase = phase
	if err := writeStorageJSON(t.service.cfg.StateDir, publicationJournalName, journal); err != nil {
		return err
	}
	t.journal = journal
	return nil
}

func (t *publicationTransaction) markPublished() error {
	ctx, cancel := context.WithTimeout(context.Background(), t.service.cfg.OperationTimeout)
	defer cancel()
	if err := t.verifyAfter(ctx); err != nil {
		return err
	}
	if err := t.syncSources(ctx); err != nil {
		return err
	}
	return t.storePhase("published")
}

func (t *publicationTransaction) recordCommit(commit string) error {
	if t.journal.Phase != "published" || !validDeleteTreeHead(commit) || commit == "" {
		return ErrInvalid
	}
	journal := t.journal
	journal.Commit = commit
	if err := writeStorageJSON(t.service.cfg.StateDir, publicationJournalName, journal); err != nil {
		return err
	}
	t.journal = journal
	return nil
}
