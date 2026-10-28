package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
)

type pendingSync struct {
	Revision string            `json:"revision"`
	Paths    []string          `json:"paths"`
	Actor    Actor             `json:"actor"`
	Message  string            `json:"message"`
	Commit   string            `json:"commit,omitempty"`
	Branch   string            `json:"branch,omitempty"`
	Hashes   map[string]string `json:"hashes"`
}

func changeData(change Change) []byte {
	if change.Content != nil {
		return []byte(*change.Content)
	}
	return change.Data
}

func renamePrefix(name, from, to string) string {
	if name == from {
		return to
	}
	if strings.HasPrefix(name, from+"/") {
		return to + strings.TrimPrefix(name, from)
	}
	return name
}

func mutateSnapshot(ctx context.Context, directory string, changes []Change) error {
	manifest, err := loadManifest(directory)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := noSymlinks(root, change.Path); err != nil {
			return err
		}
		if err := checkPathCase(root, change.Path); err != nil {
			return err
		}
		switch change.Operation {
		case "write":
			if err := writeSource(directory, change.Path, changeData(change)); err != nil {
				return err
			}
			if change.Path == "docs.json" {
				manifest, err = loadManifest(directory)
				if err != nil {
					return err
				}
				continue
			}
			index := -1
			for i := range manifest.Pages {
				if manifest.Pages[i].Source == change.Path {
					index = i
					break
				}
			}
			if index < 0 && change.Title != "" {
				kind := change.Kind
				if kind == "" {
					kind = "markdown"
					switch strings.ToLower(path.Ext(change.Path)) {
					case ".proto":
						kind = "protobuf"
					case ".json", ".yaml", ".yml":
						kind = "openapi"
					case ".html", ".htm":
						kind = "html"
					}
				}
				output := change.OutputPath
				if output == "" {
					output = strings.ToLower(strings.TrimSuffix(change.Path, path.Ext(change.Path))) + ".html"
				}
				id := change.DocumentID
				if id == "" {
					id = document.PageID(output)
				}
				manifest.Pages = append(manifest.Pages, site.Page{ID: id, Source: change.Path, Path: output, Title: change.Title, Kind: kind})
			} else if index >= 0 {
				if change.DocumentID != "" && change.DocumentID != manifest.Pages[index].ID {
					return ErrConflict
				}
				if change.Title != "" {
					manifest.Pages[index].Title = change.Title
				}
				if change.Kind != "" {
					manifest.Pages[index].Kind = change.Kind
				}
				if change.OutputPath != "" {
					manifest.Pages[index].Path = change.OutputPath
				}
			} else if binaryFile(change.Path) || strings.Contains(".txt.json.yaml.yml.proto", path.Ext(change.Path)) {
				found := false
				for _, asset := range manifest.Assets {
					found = found || asset == change.Path
				}
				if !found {
					manifest.Assets = append(manifest.Assets, change.Path)
				}
			}
		case "move":
			if err := checkPathCase(root, change.To); err != nil {
				return err
			}
			if err := noSymlinks(root, change.To); err != nil {
				return err
			}
			if _, err := root.Lstat(change.To); err == nil || !os.IsNotExist(err) {
				return ErrConflict
			}
			info, err := root.Stat(change.Path)
			if os.IsNotExist(err) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if !info.IsDir() && !allowedFile(change.To) {
				return ErrInvalid
			}
			if err := root.MkdirAll(path.Dir(change.To), 0o750); err != nil {
				return err
			}
			if err := root.Rename(change.Path, change.To); err != nil {
				return err
			}
			for i := range manifest.Pages {
				page := &manifest.Pages[i]
				old := page.Source
				page.Source = renamePrefix(page.Source, change.Path, change.To)
				if old == change.Path {
					if change.Title != "" {
						page.Title = change.Title
					}
					if change.OutputPath != "" {
						page.Path = change.OutputPath
					}
				}
			}
			for i, asset := range manifest.Assets {
				manifest.Assets[i] = renamePrefix(asset, change.Path, change.To)
			}
		case "delete":
			info, err := root.Stat(change.Path)
			if os.IsNotExist(err) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if info.IsDir() {
				return fmt.Errorf("%w: remove files before removing their folder", ErrInvalid)
			}
			if err := root.Remove(change.Path); err != nil {
				return err
			}
			pages := manifest.Pages[:0]
			for _, page := range manifest.Pages {
				if page.Source != change.Path {
					pages = append(pages, page)
				}
			}
			manifest.Pages = pages
			assets := manifest.Assets[:0]
			for _, asset := range manifest.Assets {
				if asset != change.Path {
					assets = append(assets, asset)
				}
			}
			manifest.Assets = assets
		case "mkdir":
			if err := root.MkdirAll(change.Path, 0o750); err != nil {
				return err
			}
		case "rmdir":
			info, err := root.Stat(change.Path)
			if os.IsNotExist(err) {
				return ErrNotFound
			}
			if err != nil || !info.IsDir() {
				return ErrInvalid
			}
			if err := root.Remove(change.Path); err != nil {
				return fmt.Errorf("%w: folder must be empty", ErrInvalid)
			}
		}
	}
	data, err := readSource(directory, "docs.json", 1<<20)
	if err != nil {
		return err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ErrInvalid
	}
	envelope["pages"], err = json.Marshal(manifest.Pages)
	if err != nil {
		return err
	}
	if len(manifest.Assets) == 0 {
		delete(envelope, "assets")
	} else if envelope["assets"], err = json.Marshal(manifest.Assets); err != nil {
		return err
	}
	data, err = json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	if err := writeSource(directory, "docs.json", append(data, '\n')); err != nil {
		return err
	}
	_, err = loadManifest(directory)
	return err
}

func changedFiles(before, after sourceTree) []string {
	set := make(map[string]bool)
	for name, old := range before.Files {
		if current, ok := after.Files[name]; !ok || current.Hash != old.Hash {
			set[name] = true
		}
	}
	for name, current := range after.Files {
		if old, ok := before.Files[name]; !ok || old.Hash != current.Hash {
			set[name] = true
		}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (s *Service) candidate(ctx context.Context, transaction string, changes []Change, actor Actor, mutate bool) (sourceTree, sourceTree, error) {
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return sourceTree{}, sourceTree{}, err
	}
	snapshot := filepath.Join(transaction, "source")
	if err := os.Mkdir(snapshot, 0o700); err != nil {
		return sourceTree{}, sourceTree{}, err
	}
	before, err := scanTree(ctx, s.cfg.Root, storageIdentity(status), snapshot)
	if err != nil {
		return before, sourceTree{}, err
	}
	if mutate {
		if err := mutateSnapshot(ctx, snapshot, changes); err != nil {
			return before, sourceTree{}, err
		}
	}
	after, err := scanTree(ctx, snapshot, storageIdentity(status), "")
	if err != nil {
		return before, after, err
	}
	if s.cfg.Validate != nil {
		if err := s.cfg.Validate(ctx, snapshot, changes, actor); err != nil {
			return before, after, err
		}
	}
	output := filepath.Join(transaction, "site")
	if err := s.cfg.Compile(ctx, filepath.Join(snapshot, "docs.json"), output); err != nil {
		message := strings.ReplaceAll(err.Error(), snapshot, "project")
		return before, after, fmt.Errorf("%w: compilation failed: %s", ErrInvalid, message)
	}
	if err := validateOutput(output, false); err != nil {
		return before, after, err
	}
	return before, after, nil
}

func validateOutput(directory string, allowEmpty bool) error {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) && allowEmpty {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 && allowEmpty {
		return nil
	}
	data, err := readSource(directory, "_docs/build.json", 1<<20)
	if err != nil {
		return fmt.Errorf("%w: output is not a compiler-owned site", ErrOwnership)
	}
	var inventory struct {
		Format string   `json:"format"`
		Files  []string `json:"files"`
	}
	if err := json.Unmarshal(data, &inventory); err != nil || inventory.Format != "pb-docs-site/v1" || len(inventory.Files) > 3*MaxFiles {
		return ErrOwnership
	}
	known := make(map[string]bool, len(inventory.Files))
	for _, name := range inventory.Files {
		if !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") {
			return ErrOwnership
		}
		known[name] = true
	}
	var size int64
	return filepath.WalkDir(directory, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || !known[filepath.ToSlash(relative)] {
			return ErrOwnership
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		if size > 512<<20 {
			return ErrLimit
		}
		return nil
	})
}

func replaceOutput(stage, output string) (bool, error) {
	if err := validateOutput(output, true); err != nil {
		return false, err
	}
	backup, err := os.MkdirTemp(filepath.Dir(output), ".studio-previous-")
	if err != nil {
		return false, err
	}
	if err := os.Remove(backup); err != nil {
		return false, err
	}
	if err := os.Rename(output, backup); err != nil {
		return false, err
	}
	if err := os.Rename(stage, output); err != nil {
		return false, errors.Join(err, os.Rename(backup, output))
	}
	return true, os.RemoveAll(backup)
}

func applyFiles(ctx context.Context, destination, source string, names []string, tree sourceTree) error {
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, directory := range tree.Dirs {
		if err := noSymlinks(root, directory); err != nil {
			return err
		}
		if err := root.MkdirAll(directory, 0o750); err != nil {
			return err
		}
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, exists := tree.Files[name]; !exists {
			if err := removeSource(destination, name); err != nil {
				return err
			}
			continue
		}
		data, err := readSource(source, name, MaxBinaryBytes)
		if err != nil {
			return err
		}
		if err := writeSource(destination, name, data); err != nil {
			return err
		}
	}
	return nil
}

func removeEmptyDirectories(directory string, removed, retained []string) error {
	keep := make(map[string]bool, len(retained))
	for _, name := range retained {
		keep[name] = true
	}
	for i := len(removed) - 1; i >= 0; i-- {
		if !keep[removed[i]] {
			if err := removeSource(directory, removed[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) Publish(ctx context.Context, input PublishInput, actor Actor) (PublishResult, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return PublishResult{}, err
	}
	defer done()
	actor, err = normalizeActor(actor)
	if err != nil {
		return PublishResult{}, err
	}
	draft, err := s.readDraft(input.ID)
	if err != nil {
		return PublishResult{}, err
	}
	if draft.Actor.ID != actor.ID || draft.Revision != input.Revision {
		return PublishResult{}, ErrConflict
	}
	s.cached = sourceTree{}
	project, _, err := s.project(ctx)
	if err != nil {
		return PublishResult{}, err
	}
	if draft.BaseRevision != project.Revision {
		return PublishResult{}, ErrConflict
	}
	if pending, err := s.pending(); err == nil && pending.Commit == "" {
		return PublishResult{}, fmt.Errorf("%w: retry the previous Git synchronization first", ErrConflict)
	} else if err != nil && err != ErrNotFound {
		return PublishResult{}, err
	}
	transaction, err := os.MkdirTemp(s.cfg.StateDir, ".txn-")
	if err != nil {
		return PublishResult{}, err
	}
	defer os.RemoveAll(transaction)
	changes := draftChanges(draft)
	if err := validateChanges(changes); err != nil {
		return PublishResult{}, err
	}
	before, after, err := s.candidate(ctx, transaction, changes, actor, true)
	if err != nil {
		return PublishResult{}, err
	}
	if before.Revision != draft.BaseRevision {
		return PublishResult{}, ErrConflict
	}
	s.cached = sourceTree{}
	current, _, err := s.project(ctx)
	if err != nil || current.Revision != before.Revision {
		return PublishResult{}, errors.Join(ErrConflict, err)
	}
	paths := changedFiles(before, after)
	backup := filepath.Join(transaction, "backup")
	if err := os.Mkdir(backup, 0o700); err != nil {
		return PublishResult{}, err
	}
	if err := applyFiles(ctx, backup, s.cfg.Root, paths, before); err != nil {
		return PublishResult{}, err
	}
	rollback := func() error {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), s.cfg.OperationTimeout)
		defer cancel()
		return errors.Join(applyFiles(rollbackCtx, s.cfg.Root, backup, paths, before), removeEmptyDirectories(s.cfg.Root, after.Dirs, before.Dirs))
	}
	if err := applyFiles(ctx, s.cfg.Root, filepath.Join(transaction, "source"), paths, after); err != nil {
		return PublishResult{}, errors.Join(err, rollback())
	}
	if err := removeEmptyDirectories(s.cfg.Root, before.Dirs, after.Dirs); err != nil {
		return PublishResult{}, errors.Join(err, rollback())
	}
	verificationStatus, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return PublishResult{}, errors.Join(err, rollback())
	}
	verified, err := scanTree(ctx, s.cfg.Root, storageIdentity(verificationStatus), "")
	if err != nil {
		return PublishResult{}, errors.Join(err, rollback())
	}
	if verified.Revision != after.Revision {
		return PublishResult{}, errors.Join(ErrConflict, rollback())
	}
	s.cached = verified
	published, outputErr := replaceOutput(filepath.Join(transaction, "site"), s.cfg.OutputDir)
	if !published {
		return PublishResult{}, errors.Join(outputErr, rollback())
	}
	result := PublishResult{Revision: after.Revision, Published: true}
	status, statusErr := s.cfg.Storage.Status(ctx)
	if statusErr != nil {
		status = verificationStatus
		status.State, status.Retryable, status.Error = "error", true, "Storage status is unavailable"
	}
	result.Sync = status
	if outputErr != nil {
		result.Sync.Error = "The new site is published; previous output cleanup needs attention"
	}
	if err := s.recordHistory(before, after, backup, filepath.Join(transaction, "source"), paths, actor, status.Branch); err != nil {
		result.Sync.Error = "Content is published; revision retention needs attention"
	}
	allowed := make(map[string]bool)
	for _, name := range paths {
		allowed[name] = true
	}
	allowed["docs.json"] = true
	for _, change := range changes {
		if allowedFile(change.Path) {
			allowed[change.Path] = true
		}
		if allowedFile(change.To) {
			allowed[change.To] = true
		}
	}
	if status.Head == "" && status.Backend == "git" {
		for name := range after.Files {
			allowed[name] = true
		}
	}
	commitPaths := make([]string, 0, len(allowed))
	for name := range allowed {
		commitPaths = append(commitPaths, name)
	}
	sort.Strings(commitPaths)
	hashes := make(map[string]string, len(commitPaths))
	for _, name := range commitPaths {
		hashes[name] = after.Files[name].Hash
	}
	pending := pendingSync{Revision: after.Revision, Paths: commitPaths, Actor: actor, Message: input.Message, Branch: status.Branch, Hashes: hashes}
	if err := s.writeState("pending.json", pending); err != nil {
		result.Sync.State, result.Sync.Retryable, result.Sync.Error = "error", true, "Content is published; synchronization state could not be stored"
	} else {
		publicationWarning := result.Sync.Error
		result = s.synchronize(ctx, result, pending, input.Push || s.cfg.PushOnPublish)
		if publicationWarning != "" {
			if result.Sync.Error != "" {
				publicationWarning += "; " + result.Sync.Error
			}
			result.Sync.Error = publicationWarning
		}
	}
	if err := s.discard(draft); err != nil {
		result.Sync.Error = "Content is published; stale draft cleanup needs attention"
	}
	return result, nil
}

func (s *Service) pending() (pendingSync, error) {
	data, err := readSource(s.cfg.StateDir, "pending.json", 64<<20)
	if err != nil {
		return pendingSync{}, err
	}
	var pending pendingSync
	if err := json.Unmarshal(data, &pending); err != nil {
		return pending, ErrInvalid
	}
	return pending, nil
}

func (s *Service) synchronize(ctx context.Context, result PublishResult, pending pendingSync, push bool) PublishResult {
	commit, err := s.cfg.Storage.Commit(ctx, pending.Paths, pending.Actor, pending.Message)
	if err != nil {
		result.Sync.State, result.Sync.Retryable, result.Sync.Error = "error", true, "Local content is published; Git commit failed and can be retried"
		return result
	}
	pending.Commit, result.Commit = commit, commit
	project, _, err := s.project(ctx)
	if err != nil {
		result.Sync.State, result.Sync.Retryable, result.Sync.Error = "error", true, "Local commit is retained; current source revision is unavailable"
		return result
	}
	pending.Revision, result.Revision = project.Revision, project.Revision
	if err := s.writeState("pending.json", pending); err != nil {
		result.Sync.State, result.Sync.Retryable, result.Sync.Error = "error", true, "Local content is published; synchronization state could not be stored"
		return result
	}
	if push {
		if err := s.cfg.Storage.Push(ctx); err != nil {
			result.Sync.State, result.Sync.Retryable, result.Sync.Error = "pending", true, "Local commit is retained; remote push failed and can be retried"
			return result
		}
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		result.Sync.State, result.Sync.Retryable, result.Sync.Error = "error", true, "Local content is published; storage status is unavailable"
		return result
	}
	result.Sync = status
	if !push && status.Backend == "git" {
		result.Sync.State, result.Sync.Retryable = "pending", true
		return result
	}
	if err := removeSource(s.cfg.StateDir, "pending.json"); err != nil {
		result.Sync.Error = "Publication succeeded; synchronization journal cleanup needs attention"
	}
	return result
}

func (s *Service) Status(ctx context.Context) (SyncStatus, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return SyncStatus{}, err
	}
	defer done()
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return status, err
	}
	if pending, err := s.pending(); err == nil {
		status.State, status.Retryable = "pending", true
		if pending.Commit == "" {
			status.State = "error"
			status.Error = "Published changes need a Git commit retry"
		}
	} else if err != ErrNotFound {
		return status, err
	}
	return status, nil
}

func (s *Service) Branches(ctx context.Context) ([]Branch, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := s.authorizeStorage(ctx, Actor{}); err != nil {
		return nil, err
	}
	return s.cfg.Storage.Branches(ctx)
}

func (s *Service) branchOperation(ctx context.Context, action, name, revision string) (SyncStatus, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return SyncStatus{}, err
	}
	defer done()
	if err := s.authorizeStorage(ctx, Actor{}); err != nil {
		return SyncStatus{}, err
	}
	s.cached = sourceTree{}
	project, tree, err := s.project(ctx)
	if err != nil {
		return SyncStatus{}, err
	}
	if revision == "" || project.Revision != revision {
		return SyncStatus{}, ErrConflict
	}
	previous, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return previous, err
	}
	switch action {
	case "create":
		err = s.cfg.Storage.CreateBranch(ctx, name)
	case "checkout":
		err = s.cfg.Storage.Checkout(ctx, name)
	case "fetch":
		err = s.cfg.Storage.Fetch(ctx)
	case "pull":
		err = s.cfg.Storage.Pull(ctx)
	case "push":
		if pending, pendingErr := s.pending(); pendingErr == nil {
			if pending.Branch != previous.Branch || (pending.Commit != "" && pending.Commit != previous.Head) {
				return SyncStatus{}, ErrConflict
			}
			for name, expected := range pending.Hashes {
				if tree.Files[name].Hash != expected {
					return SyncStatus{}, ErrConflict
				}
			}
			result := s.synchronize(ctx, PublishResult{Revision: project.Revision, Published: true, Sync: previous}, pending, true)
			return result.Sync, nil
		} else if pendingErr != ErrNotFound {
			return SyncStatus{}, pendingErr
		}
		err = s.cfg.Storage.Push(ctx)
	default:
		return SyncStatus{}, ErrInvalid
	}
	if err != nil {
		return SyncStatus{}, err
	}
	if action != "checkout" && action != "pull" {
		return s.cfg.Storage.Status(ctx)
	}
	transaction, err := os.MkdirTemp(s.cfg.StateDir, ".txn-")
	if err != nil {
		return SyncStatus{}, err
	}
	defer os.RemoveAll(transaction)
	_, _, buildErr := s.candidate(ctx, transaction, nil, Actor{}, false)
	if buildErr == nil {
		published, err := replaceOutput(filepath.Join(transaction, "site"), s.cfg.OutputDir)
		if published {
			if err := removeSource(s.cfg.StateDir, "pending.json"); err != nil {
				return SyncStatus{}, err
			}
			return s.cfg.Storage.Status(ctx)
		}
		buildErr = err
	}
	rollbackCtx, cancel := context.WithTimeout(context.Background(), s.cfg.OperationTimeout)
	defer cancel()
	var rollbackErr error
	if action == "checkout" {
		rollbackErr = s.cfg.Storage.Checkout(rollbackCtx, previous.Branch)
	} else {
		rollbackErr = s.cfg.Storage.RestoreHead(rollbackCtx, previous.Head)
	}
	return SyncStatus{}, errors.Join(buildErr, rollbackErr)
}

func (s *Service) CreateBranch(ctx context.Context, name, revision string) (SyncStatus, error) {
	return s.branchOperation(ctx, "create", name, revision)
}
func (s *Service) Checkout(ctx context.Context, name, revision string) (SyncStatus, error) {
	return s.branchOperation(ctx, "checkout", name, revision)
}
func (s *Service) Fetch(ctx context.Context, revision string) (SyncStatus, error) {
	return s.branchOperation(ctx, "fetch", "", revision)
}
func (s *Service) Pull(ctx context.Context, revision string) (SyncStatus, error) {
	return s.branchOperation(ctx, "pull", "", revision)
}
func (s *Service) Push(ctx context.Context, revision string) (SyncStatus, error) {
	return s.branchOperation(ctx, "push", "", revision)
}
