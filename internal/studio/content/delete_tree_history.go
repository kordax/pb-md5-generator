package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const deleteTreeGeneration = ".delete-tree-generation"

// historyGeneration reads one bounded payload at a time. A generation contains
// only the existing public history format and its private transaction marker.
func historyGeneration(ctx context.Context, directory string) (string, int64, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", 0, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return "", 0, err
	}
	entries, readErr := dir.ReadDir(2*MaxHistory + 2)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if err := errors.Join(readErr, dir.Close()); err != nil {
		return "", 0, err
	}
	if len(entries) > 2*MaxHistory+1 {
		return "", 0, ErrLimit
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	digest := sha256.New()
	metadata := make(map[string]Revision)
	payloads := make(map[string]int64)
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		name := entry.Name()
		if !entry.Type().IsRegular() || name != deleteTreeGeneration && (!validID(strings.TrimSuffix(strings.TrimSuffix(name, ".bin"), ".json")) || !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".bin")) {
			return "", 0, ErrOwnership
		}
		limit := int64(MaxBinaryBytes)
		if strings.HasSuffix(name, ".json") || name == deleteTreeGeneration {
			limit = 64 << 10
		}
		data, err := readSource(directory, name, limit)
		if err != nil {
			return "", 0, err
		}
		total += int64(len(data))
		if total > MaxStateBytes {
			return "", 0, ErrLimit
		}
		switch {
		case name == deleteTreeGeneration:
			if !validID(string(data)) {
				return "", 0, ErrOwnership
			}
		case strings.HasSuffix(name, ".json"):
			var revision Revision
			id := strings.TrimSuffix(name, ".json")
			if json.Unmarshal(data, &revision) != nil || revision.ID != id || !allowedFile(revision.Path) || !validRevision(revision.Revision) || revision.Size < 0 || revision.Size > MaxBinaryBytes || revision.Deleted && revision.Size != 0 {
				return "", 0, ErrOwnership
			}
			metadata[id] = revision
		case strings.HasSuffix(name, ".bin"):
			payloads[strings.TrimSuffix(name, ".bin")] = int64(len(data))
		}
		if name != deleteTreeGeneration {
			hash := sha256.Sum256(data)
			_, _ = io.WriteString(digest, name+"\x00"+hex.EncodeToString(hash[:])+"\x00")
		}
	}
	if len(metadata) > MaxHistory {
		return "", 0, ErrLimit
	}
	for id, revision := range metadata {
		size, exists := payloads[id]
		if revision.Deleted && exists || !revision.Deleted && (!exists || size != revision.Size) {
			return "", 0, ErrOwnership
		}
	}
	for id := range payloads {
		if _, exists := metadata[id]; !exists {
			return "", 0, ErrOwnership
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), total, nil
}

// reserveDeleteTreeHistory constructs the entire next generation before any
// live change. Only old history is eligible for retention; drafts are not.
func (s *Service) reserveDeleteTreeHistory(ctx context.Context, transaction, id string, before, after sourceTree, paths []string, actor Actor, branch string) (string, string, error) {
	stateRoot, err := os.OpenRoot(s.cfg.StateDir)
	if err != nil {
		return "", "", err
	}
	defer stateRoot.Close()
	if err := noSymlinks(stateRoot, "history"); err != nil {
		return "", "", err
	}
	previousHash, previousBytes, err := historyGeneration(ctx, filepath.Join(s.cfg.StateDir, "history"))
	if err != nil {
		return "", "", err
	}
	old, err := s.revisions()
	if err != nil {
		return "", "", err
	}
	sort.Slice(old, func(i, j int) bool {
		if old[i].CreatedAt.Equal(old[j].CreatedAt) {
			return old[i].ID < old[j].ID
		}
		return old[i].CreatedAt.Before(old[j].CreatedAt)
	})
	stage := filepath.Join(transaction, "history-next")
	if err := os.Mkdir(stage, 0o700); err != nil {
		return "", "", err
	}
	count := 0
	created := time.Now().UTC()
	for _, name := range paths {
		for _, previous := range []bool{true, false} {
			tree, directory := after, filepath.Join(transaction, "source")
			if previous {
				tree, directory = before, filepath.Join(transaction, "backup")
				if _, exists := before.Files[name]; !exists {
					continue
				}
			}
			state, exists := tree.Files[name]
			page := historyPage(directory, s.cfg.Root, name)
			if !exists {
				page = historyPage(filepath.Join(transaction, "backup"), s.cfg.Root, name)
			}
			revisionID, err := randomID()
			if err != nil {
				return "", "", err
			}
			revision := Revision{ID: revisionID, Path: name, Branch: branch, Revision: tree.Revision, Actor: actor, CreatedAt: created.Add(time.Duration(count)), Deleted: !exists, Size: state.Size, Title: page.Title, Kind: page.Kind, Output: page.Path, DocumentID: page.ID}
			metadata, err := json.Marshal(revision)
			if err != nil {
				return "", "", err
			}
			if exists {
				data, err := readSource(directory, name, MaxBinaryBytes)
				if err != nil {
					return "", "", err
				}
				digest := sha256.Sum256(data)
				if int64(len(data)) != state.Size || hex.EncodeToString(digest[:]) != state.Hash {
					return "", "", ErrConflict
				}
				if err := writeStorageBytes(stage, revisionID+".bin", data); err != nil {
					return "", "", err
				}
			}
			if err := writeStorageBytes(stage, revisionID+".json", metadata); err != nil {
				return "", "", err
			}
			count++
		}
	}
	if count > MaxHistory {
		return "", "", ErrLimit
	}
	if err := writeStorageBytes(stage, deleteTreeGeneration, []byte(id)); err != nil {
		return "", "", err
	}
	_, nextBytes, err := historyGeneration(ctx, stage)
	if err != nil {
		return "", "", err
	}
	total, err := stateSize(s.cfg.StateDir)
	if err != nil {
		return "", "", err
	}
	// Keep a bounded margin for the recovery and Git synchronization journals.
	remaining := int64(MaxStateBytes) - (total - previousBytes) - nextBytes - (1 << 20)
	if remaining < 0 {
		return "", "", ErrLimit
	}
	retained := []Revision{}
	for i := len(old) - 1; i >= 0 && count+len(retained) < MaxHistory; i-- {
		revision := old[i]
		metadata, err := readSource(s.cfg.StateDir, "history/"+revision.ID+".json", 64<<10)
		if err != nil {
			return "", "", err
		}
		var payload []byte
		if !revision.Deleted {
			payload, err = readSource(s.cfg.StateDir, "history/"+revision.ID+".bin", MaxBinaryBytes)
			if err != nil || int64(len(payload)) != revision.Size {
				return "", "", errors.Join(ErrOwnership, err)
			}
		}
		size := int64(len(metadata) + len(payload))
		if size > remaining {
			break
		}
		if !revision.Deleted {
			if err := writeStorageBytes(stage, revision.ID+".bin", payload); err != nil {
				return "", "", err
			}
		}
		if err := writeStorageBytes(stage, revision.ID+".json", metadata); err != nil {
			return "", "", err
		}
		remaining -= size
		retained = append(retained, revision)
	}
	nextHash, _, err := historyGeneration(ctx, stage)
	return previousHash, nextHash, err
}
