package content

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kordax/pb-md5-generator/internal/site"
)

func (s *Service) revisions() ([]Revision, error) {
	entries, err := os.ReadDir(filepath.Join(s.cfg.StateDir, "history"))
	if err != nil {
		return nil, err
	}
	result := []Revision{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if len(result) >= MaxHistory {
			return nil, ErrLimit
		}
		data, err := readSource(s.cfg.StateDir, "history/"+entry.Name(), 64<<10)
		if err != nil {
			return nil, err
		}
		var revision Revision
		if err := json.Unmarshal(data, &revision); err != nil || !validID(revision.ID) {
			return nil, ErrInvalid
		}
		result = append(result, revision)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

func stateSize(directory string) (int64, error) {
	var total int64
	err := filepath.WalkDir(directory, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".txn-") {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return ErrOwnership
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func (s *Service) historySpace(size int64) error {
	revisions, err := s.revisions()
	if err != nil {
		return err
	}
	total, err := stateSize(s.cfg.StateDir)
	if err != nil {
		return err
	}
	for len(revisions) >= MaxHistory || total+size+8192 > MaxStateBytes {
		if len(revisions) == 0 {
			return ErrLimit
		}
		oldest := revisions[0]
		if err := removeSource(s.cfg.StateDir, "history/"+oldest.ID+".json"); err != nil {
			return err
		}
		if err := removeSource(s.cfg.StateDir, "history/"+oldest.ID+".bin"); err != nil {
			return err
		}
		revisions = revisions[1:]
		total, err = stateSize(s.cfg.StateDir)
		if err != nil {
			return err
		}
	}
	return nil
}

func historyPage(directory, fallback, name string) site.Page {
	manifest, err := loadManifest(directory)
	if err != nil {
		manifest, err = loadManifest(fallback)
		if err != nil {
			return site.Page{}
		}
	}
	for _, page := range manifest.Pages {
		if page.Source == name {
			return page
		}
	}
	return site.Page{}
}

func (s *Service) saveRevision(name, directory string, tree sourceTree, actor Actor, branch string, page site.Page) error {
	state, exists := tree.Files[name]
	id, err := randomID()
	if err != nil {
		return err
	}
	revision := Revision{ID: id, Path: name, Branch: branch, Revision: tree.Revision, Actor: actor, CreatedAt: time.Now().UTC(), Deleted: !exists, Size: state.Size, Title: page.Title, Kind: page.Kind, Output: page.Path, DocumentID: page.ID}
	if err := s.historySpace(state.Size); err != nil {
		return err
	}
	if exists {
		data, err := readSource(directory, name, MaxBinaryBytes)
		if err != nil {
			return err
		}
		if err := writeSource(s.cfg.StateDir, "history/"+id+".bin", data); err != nil {
			return err
		}
	}
	if err := s.writeState("history/"+id+".json", revision); err != nil {
		_ = removeSource(s.cfg.StateDir, "history/"+id+".bin")
		return err
	}
	return nil
}

func (s *Service) recordHistory(before, after sourceTree, backup, snapshot string, names []string, actor Actor, branch string) error {
	for _, name := range names {
		beforePage := historyPage(backup, s.cfg.Root, name)
		if _, exists := before.Files[name]; exists {
			if err := s.saveRevision(name, backup, before, actor, branch, beforePage); err != nil {
				return err
			}
		}
		afterPage := historyPage(snapshot, s.cfg.Root, name)
		if _, exists := after.Files[name]; !exists {
			afterPage = beforePage
		}
		if err := s.saveRevision(name, snapshot, after, actor, branch, afterPage); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) History(ctx context.Context, name string) ([]Revision, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	if !allowedFile(name) {
		return nil, ErrInvalid
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return nil, err
	}
	revisions, err := s.revisions()
	if err != nil {
		return nil, err
	}
	documentID := historyPage(s.cfg.Root, s.cfg.Root, name).ID
	result := []Revision{}
	for i := len(revisions) - 1; i >= 0; i-- {
		if (revisions[i].Path == name || (documentID != "" && revisions[i].DocumentID == documentID)) && s.historyBranchMatches(revisions[i].Branch, status.Branch) {
			result = append(result, revisions[i])
		}
	}
	return result, nil
}

// Restore makes an optimistic draft. It never bypasses compiler validation.
func (s *Service) Restore(ctx context.Context, name, id, baseRevision string, actor Actor) (Draft, error) {
	caller := ctx
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return Draft{}, err
	}
	if !allowedFile(name) || !validID(id) {
		done()
		return Draft{}, ErrInvalid
	}
	data, err := readSource(s.cfg.StateDir, "history/"+id+".json", 64<<10)
	if err != nil {
		done()
		return Draft{}, err
	}
	var revision Revision
	if err := json.Unmarshal(data, &revision); err != nil || (revision.Path != name && (revision.DocumentID == "" || revision.DocumentID != historyPage(s.cfg.Root, s.cfg.Root, name).ID)) {
		done()
		return Draft{}, ErrInvalid
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil || !s.historyBranchMatches(revision.Branch, status.Branch) {
		done()
		return Draft{}, ErrConflict
	}
	change := Change{Operation: "delete", Path: name}
	if !revision.Deleted {
		data, err := readSource(s.cfg.StateDir, "history/"+id+".bin", MaxBinaryBytes)
		if err != nil {
			done()
			return Draft{}, err
		}
		change = Change{Operation: "write", Path: name, Title: revision.Title, Kind: revision.Kind, OutputPath: revision.Output, DocumentID: revision.DocumentID}
		if binaryFile(name) {
			change.Data = data
		} else {
			text := string(data)
			change.Content = &text
		}
	}
	// Use the caller's context after releasing the gate. SaveDraft acquires it
	// again to persist an optimistic draft; publication checks the project revision.
	done()
	return s.SaveDraft(caller, DraftInput{BaseRevision: baseRevision, Change: change}, actor)
}
