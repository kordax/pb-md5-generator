package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	files, _, total, err := scanHistoryRoot(ctx, root, directory, true, validGenerationHistoryName)
	if err != nil {
		return "", 0, err
	}
	return publicationHistoryHash(files), total, nil
}

// Retain the shipped filename predicate, including its error ordering for
// malformed double extensions. Complete scans still reject orphan payloads.
func validGenerationHistoryName(name string) bool {
	return name == deleteTreeGeneration || validID(strings.TrimSuffix(strings.TrimSuffix(name, ".bin"), ".json")) && (strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".bin"))
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
