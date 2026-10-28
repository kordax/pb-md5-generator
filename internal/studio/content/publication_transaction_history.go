package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const publicationHistoryPlanName = "history-plan.json"

type publicationHistoryFile struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

type publicationHistoryDelta struct {
	Before  map[string]publicationHistoryFile `json:"before"`
	Added   map[string]publicationHistoryFile `json:"added"`
	Removed []string                          `json:"removed"`
}

func validPublicationHistoryName(name string) bool {
	if name == deleteTreeGeneration {
		return true
	}
	if strings.HasSuffix(name, ".json") {
		return validID(strings.TrimSuffix(name, ".json"))
	}
	return strings.HasSuffix(name, ".bin") && validID(strings.TrimSuffix(name, ".bin"))
}

// Read one bounded payload at a time. Partial delta placement can temporarily
// split a metadata/payload pair, but complete published generations cannot.
func publicationHistoryFiles(ctx context.Context, directory string, complete bool) (map[string]publicationHistoryFile, []Revision, int64, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, 0, errors.Join(ErrOwnership, err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, 0, err
	}
	defer root.Close()
	return scanHistoryRoot(ctx, root, directory, complete, validPublicationHistoryName)
}

// Root-opening policies belong to the wrappers. The bounded content scanner
// shares metadata, payload, quota and raw-byte hash validation for both uses.
func scanHistoryRoot(ctx context.Context, root *os.Root, directory string, complete bool, validName func(string) bool) (map[string]publicationHistoryFile, []Revision, int64, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, nil, 0, err
	}
	entries, err := readPublicationDirectoryEntries(file, 2*MaxHistory+2)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(entries) > 2*MaxHistory+1 {
		return nil, nil, 0, ErrLimit
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	files := make(map[string]publicationHistoryFile, len(entries))
	revisions := []Revision{}
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, 0, err
		}
		name := entry.Name()
		if !entry.Type().IsRegular() || !validName(name) {
			return nil, nil, 0, ErrOwnership
		}
		limit := int64(MaxBinaryBytes)
		if name == deleteTreeGeneration || strings.HasSuffix(name, ".json") {
			limit = 64 << 10
		}
		data, err := readSource(directory, name, limit)
		if err != nil {
			return nil, nil, 0, err
		}
		total += int64(len(data))
		if total > MaxStateBytes {
			return nil, nil, 0, ErrLimit
		}
		hash := sha256.Sum256(data)
		files[name] = publicationHistoryFile{Hash: hex.EncodeToString(hash[:]), Size: int64(len(data))}
		if name == deleteTreeGeneration {
			if !validID(string(data)) {
				return nil, nil, 0, ErrOwnership
			}
		} else if complete && strings.HasSuffix(name, ".json") {
			var revision Revision
			if json.Unmarshal(data, &revision) != nil || !validID(revision.ID) || revision.ID != strings.TrimSuffix(name, ".json") || !allowedFile(revision.Path) || !validRevision(revision.Revision) || revision.Size < 0 || revision.Size > MaxBinaryBytes || revision.Deleted && revision.Size != 0 {
				return nil, nil, 0, ErrOwnership
			}
			revisions = append(revisions, revision)
		}
	}
	if complete {
		if len(revisions) > MaxHistory {
			return nil, nil, 0, ErrLimit
		}
		known := make(map[string]bool, len(revisions))
		for _, revision := range revisions {
			payload, exists := files[revision.ID+".bin"]
			if revision.Deleted && exists || !revision.Deleted && (!exists || payload.Size != revision.Size) {
				return nil, nil, 0, ErrOwnership
			}
			known[revision.ID] = true
		}
		for name := range files {
			if strings.HasSuffix(name, ".bin") && !known[strings.TrimSuffix(name, ".bin")] {
				return nil, nil, 0, ErrOwnership
			}
		}
	}
	return files, revisions, total, nil
}

func publicationHistoryHash(files map[string]publicationHistoryFile) string {
	names := make([]string, 0, len(files))
	for name := range files {
		if name != deleteTreeGeneration {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		_, _ = io.WriteString(hash, name+"\x00"+files[name].Hash+"\x00")
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (p publicationHistoryDelta) after() map[string]publicationHistoryFile {
	files := maps.Clone(p.Before)
	for _, name := range p.Removed {
		delete(files, name)
	}
	maps.Copy(files, p.Added)
	return files
}

func (s *Service) preparePublicationHistory(ctx context.Context, stage string, plan publicationPlan) (publicationHistoryDelta, string, error) {
	previous, old, previousBytes, err := publicationHistoryFiles(ctx, filepath.Join(s.cfg.StateDir, "history"), true)
	if err != nil {
		return publicationHistoryDelta{}, "", err
	}
	p := publicationHistoryDelta{Before: previous, Added: make(map[string]publicationHistoryFile), Removed: []string{}}
	for _, name := range []string{"history-next", "history-previous"} {
		if err := os.Mkdir(filepath.Join(stage, name), 0o700); err != nil {
			return p, "", err
		}
	}
	created := time.Now().UTC()
	count := 0
	var newBytes int64
	store := func(name string, data []byte) error {
		newBytes += int64(len(data))
		if newBytes > MaxStateBytes-(1<<20) {
			return ErrLimit
		}
		hash := sha256.Sum256(data)
		p.Added[name] = publicationHistoryFile{Hash: hex.EncodeToString(hash[:]), Size: int64(len(data))}
		return writeStorageBytes(filepath.Join(stage, "history-next"), name, data)
	}
	for _, name := range plan.Paths {
		for _, previous := range []bool{true, false} {
			tree, directory := plan.After, filepath.Join(stage, "source")
			if previous {
				tree, directory = plan.Before, filepath.Join(stage, "backup")
				if _, exists := tree.Files[name]; !exists {
					continue
				}
			}
			if count >= MaxHistory {
				return p, "", ErrLimit
			}
			state, exists := tree.Files[name]
			page := historyPage(directory, s.cfg.Root, name)
			if !exists {
				page = historyPage(filepath.Join(stage, "backup"), s.cfg.Root, name)
			}
			id, err := randomID()
			if err != nil {
				return p, "", err
			}
			revision := Revision{ID: id, Path: name, Branch: plan.Status.Branch, Revision: tree.Revision, Actor: plan.Actor, CreatedAt: created.Add(time.Duration(count)), Deleted: !exists, Size: state.Size, Title: page.Title, Kind: page.Kind, Output: page.Path, DocumentID: page.ID}
			metadata, err := json.Marshal(revision)
			if err != nil {
				return p, "", err
			}
			if exists {
				data, err := readSource(directory, name, MaxBinaryBytes)
				if err != nil {
					return p, "", err
				}
				hash := sha256.Sum256(data)
				if int64(len(data)) != state.Size || hex.EncodeToString(hash[:]) != state.Hash {
					return p, "", ErrConflict
				}
				if err := store(id+".bin", data); err != nil {
					return p, "", err
				}
			}
			if err := store(id+".json", metadata); err != nil {
				return p, "", err
			}
			count++
		}
	}
	total, err := stateSize(s.cfg.StateDir)
	if err != nil {
		return p, "", err
	}
	remaining := int64(MaxStateBytes) - (total - previousBytes) - newBytes - (1 << 20)
	if remaining < 0 {
		return p, "", ErrLimit
	}
	sort.Slice(old, func(i, j int) bool {
		if old[i].CreatedAt.Equal(old[j].CreatedAt) {
			return old[i].ID < old[j].ID
		}
		return old[i].CreatedAt.Before(old[j].CreatedAt)
	})
	keep := make(map[string]bool)
	for i := len(old) - 1; i >= 0 && count < MaxHistory; i-- {
		revision := old[i]
		size := previous[revision.ID+".json"].Size + previous[revision.ID+".bin"].Size
		if size > remaining {
			break
		}
		keep[revision.ID+".json"] = true
		if !revision.Deleted {
			keep[revision.ID+".bin"] = true
		}
		remaining -= size
		count++
	}
	for name := range previous {
		if !keep[name] {
			p.Removed = append(p.Removed, name)
		}
	}
	sort.Strings(p.Removed)
	data, err := json.Marshal(p)
	if err != nil || len(data) > maxStorageJSONBytes || len(p.after()) > 2*MaxHistory {
		return p, "", errors.Join(ErrLimit, err)
	}
	if err := writeStorageBytes(stage, publicationHistoryPlanName, data); err != nil {
		return p, "", err
	}
	hash := sha256.Sum256(data)
	return p, hex.EncodeToString(hash[:]), nil
}

func (t *publicationTransaction) historyPlan() (publicationHistoryDelta, error) {
	var p publicationHistoryDelta
	data, err := readSource(t.directory(), publicationHistoryPlanName, maxStorageJSONBytes)
	if err != nil {
		return p, err
	}
	hash := sha256.Sum256(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if hex.EncodeToString(hash[:]) != t.journal.HistoryPlan || decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF || p.Before == nil || p.Added == nil || len(p.Before) > 2*MaxHistory+1 || len(p.Added) > 2*MaxHistory || len(p.Removed) > 2*MaxHistory+1 {
		return p, ErrOwnership
	}
	for _, files := range []map[string]publicationHistoryFile{p.Before, p.Added} {
		for name, file := range files {
			limit := int64(MaxBinaryBytes)
			if name == deleteTreeGeneration || strings.HasSuffix(name, ".json") {
				limit = 64 << 10
			}
			if !validPublicationHistoryName(name) || !validRevision(file.Hash) || file.Size < 0 || file.Size > limit {
				return p, ErrOwnership
			}
		}
	}
	seen := make(map[string]bool, len(p.Removed))
	for _, name := range p.Removed {
		if _, exists := p.Before[name]; !exists || seen[name] {
			return p, ErrOwnership
		}
		seen[name] = true
	}
	for name := range p.Added {
		if _, exists := p.Before[name]; exists || name == deleteTreeGeneration {
			return p, ErrOwnership
		}
	}
	after := p.after()
	if len(after) > 2*MaxHistory || publicationHistoryHash(p.Before) != t.journal.PreviousHistory || publicationHistoryHash(after) != t.journal.NextHistory {
		return p, ErrOwnership
	}
	if _, exists := after[deleteTreeGeneration]; exists {
		return p, ErrOwnership
	}
	var total int64
	records := 0
	for name, file := range after {
		total += file.Size
		if strings.HasSuffix(name, ".json") {
			records++
		}
	}
	if records > MaxHistory || total > MaxStateBytes {
		return p, ErrLimit
	}
	return p, nil
}

// Each journaled immutable file has exactly one known location. This accepts
// interrupted per-file renames while rejecting changed, missing or extra data.
func (t *publicationTransaction) deltaHistoryPlacement(ctx context.Context, p publicationHistoryDelta) error {
	state, err := os.OpenRoot(t.service.cfg.StateDir)
	if err != nil {
		return err
	}
	for _, name := range []string{"history", t.name() + "/history-next", t.name() + "/history-previous"} {
		if err := noSymlinks(state, name); err != nil {
			return errors.Join(err, state.Close())
		}
	}
	if err := state.Close(); err != nil {
		return err
	}
	live, _, _, err := publicationHistoryFiles(ctx, filepath.Join(t.service.cfg.StateDir, "history"), false)
	if err != nil {
		return err
	}
	added, _, _, err := publicationHistoryFiles(ctx, filepath.Join(t.directory(), "history-next"), false)
	if err != nil {
		return err
	}
	removed, _, _, err := publicationHistoryFiles(ctx, filepath.Join(t.directory(), "history-previous"), false)
	if err != nil {
		return err
	}
	if t.journal.Phase != "published" {
		if !maps.Equal(live, p.Before) || !maps.Equal(added, p.Added) || len(removed) != 0 {
			return ErrOwnership
		}
		return nil
	}
	for name, file := range live {
		before, hadBefore := p.Before[name]
		after, isAdded := p.Added[name]
		if (!hadBefore || before != file) && (!isAdded || after != file) {
			return ErrOwnership
		}
	}
	evict := make(map[string]bool, len(p.Removed))
	for _, name := range p.Removed {
		evict[name] = true
	}
	for name, file := range removed {
		if !evict[name] || p.Before[name] != file {
			return ErrOwnership
		}
	}
	for name, file := range added {
		if expected, exists := p.Added[name]; !exists || expected != file {
			return ErrOwnership
		}
	}
	for name, file := range p.Before {
		current, inLive := live[name]
		retained, inRemoved := removed[name]
		if inLive == inRemoved || inLive && current != file || inRemoved && (!evict[name] || retained != file) {
			return ErrOwnership
		}
	}
	for name, file := range p.Added {
		current, inLive := live[name]
		staged, inAdded := added[name]
		if inLive == inAdded || inLive && current != file || inAdded && staged != file {
			return ErrOwnership
		}
	}
	return nil
}

func (t *publicationTransaction) installDeltaHistory(ctx context.Context) error {
	p, err := t.historyPlan()
	if err != nil {
		return err
	}
	return t.deltaHistoryPlacement(ctx, p)
}

func (t *publicationTransaction) nextDeltaHistoryMatches(ctx context.Context) error {
	p, err := t.historyPlan()
	if errors.Is(err, ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		if exists, checkErr := t.stageExists(); checkErr != nil || exists {
			return errors.Join(ErrOwnership, err, checkErr)
		}
		return t.finalDeltaHistoryMatches(ctx)
	}
	if err != nil {
		return err
	}
	return t.deltaHistoryPlacement(ctx, p)
}

func (t *publicationTransaction) finalDeltaHistoryMatches(ctx context.Context) error {
	files, _, _, err := publicationHistoryFiles(ctx, filepath.Join(t.service.cfg.StateDir, "history"), true)
	if err != nil || len(files) > 2*MaxHistory || publicationHistoryHash(files) != t.journal.NextHistory {
		return errors.Join(ErrOwnership, err)
	}
	if _, exists := files[deleteTreeGeneration]; exists {
		return ErrOwnership
	}
	return nil
}

func (t *publicationTransaction) syncDeltaHistory() error {
	return errors.Join(syncStorageDirectory(t.service.cfg.StateDir, "history"), syncStorageDirectory(t.directory(), "history-next"), syncStorageDirectory(t.directory(), "history-previous"))
}

func (t *publicationTransaction) moveDeltaHistory(ctx context.Context) error {
	if t.journal.Phase != "published" {
		return ErrConflict
	}
	p, err := t.historyPlan()
	if err != nil {
		return err
	}
	if err := t.deltaHistoryPlacement(ctx, p); err != nil {
		return err
	}
	root, err := os.OpenRoot(t.service.cfg.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()
	move := func(names []string, from, to string) error {
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			source, destination := from+"/"+name, to+"/"+name
			if exists, err := storagePathExists(t.service.cfg.StateDir, source); err != nil {
				return err
			} else if !exists {
				continue
			}
			if _, err := root.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				return errors.Join(ErrOwnership, err)
			}
			if err := noSymlinks(root, source); err != nil {
				return err
			}
			if err := root.Rename(source, destination); err != nil {
				return err
			}
		}
		return nil
	}
	added := make([]string, 0, len(p.Added))
	for name := range p.Added {
		added = append(added, name)
	}
	sort.Strings(added)
	if err := move(p.Removed, "history", t.name()+"/history-previous"); err != nil {
		return err
	}
	if err := move(added, t.name()+"/history-next", "history"); err != nil {
		return err
	}
	if err := t.syncDeltaHistory(); err != nil {
		return err
	}
	return t.finalDeltaHistoryMatches(ctx)
}
