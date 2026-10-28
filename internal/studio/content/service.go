package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Service struct {
	cfg                Config
	gate               chan struct{}
	locks              []*os.File
	closed             bool
	cached             sourceTree
	deleteTreePending  bool
	publicationPending bool
}

type ownership struct {
	Format string `json:"format"`
	Root   string `json:"root"`
	Output string `json:"output"`
}

func matchesOwnership(data []byte, config Config) bool {
	var owner ownership
	return json.Unmarshal(data, &owner) == nil && owner.Format == "pb-docs-managed/v1" && owner.Root == config.Root && owner.Output == config.OutputDir
}

func New(config Config) (*Service, error) {
	if config.Root == "" || config.StateDir == "" || config.OutputDir == "" || config.Compile == nil {
		return nil, fmt.Errorf("%w: explicit root, state, output and compiler are required", ErrInvalid)
	}
	if config.OperationTimeout == 0 {
		config.OperationTimeout = 30 * time.Second
	}
	if config.OperationTimeout < time.Second || config.OperationTimeout > 2*time.Minute {
		return nil, ErrInvalid
	}
	values := []*string{&config.Root, &config.StateDir, &config.OutputDir}
	for _, value := range values {
		absolute, err := filepath.Abs(*value)
		if err != nil {
			return nil, err
		}
		resolved, err := resolveLocation(absolute)
		if err != nil {
			return nil, err
		}
		*value = resolved
	}
	for i, left := range values {
		for j, right := range values {
			if i != j && contains(*left, *right) {
				return nil, fmt.Errorf("%w: root, state and output must be disjoint", ErrInvalid)
			}
		}
	}
	if err := rejectDevelopmentCheckout(config.Root, config.StateDir, config.OutputDir); err != nil {
		return nil, err
	}
	for i, value := range values {
		permissions := os.FileMode(0o750)
		if i == 1 {
			permissions = 0o700
		}
		if i == 2 {
			// The previous output may be between journaled renames. Do not
			// disguise that state by creating an empty replacement here.
			if err := os.MkdirAll(filepath.Dir(*value), permissions); err != nil {
				return nil, err
			}
		} else if err := os.MkdirAll(*value, permissions); err != nil {
			return nil, err
		}
		info, err := os.Lstat(*value)
		if i == 2 && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: managed locations must be real directories", ErrInvalid)
		}
		if i == 1 && !privateStateDirectory(info) {
			return nil, fmt.Errorf("%w: state directory must be private and owned by the current user", ErrOwnership)
		}
	}
	if existing, err := readSource(config.StateDir, "owner.json", 8192); err == nil {
		if !matchesOwnership(existing, config) {
			return nil, ErrOwnership
		}
	} else if err != ErrNotFound {
		return nil, err
	}
	locks, err := acquireManagedLocks(config.Root, config.StateDir, config.OutputDir)
	if err != nil {
		return nil, err
	}
	retainLocks := false
	defer func() {
		if !retainLocks {
			for _, lock := range locks {
				_ = lock.Close()
			}
		}
	}()
	ownerData, err := readSource(config.StateDir, "owner.json", 8192)
	if err == ErrNotFound {
		if !config.Initialize {
			return nil, ErrOwnership
		}
		entries, err := os.ReadDir(config.StateDir)
		if err != nil || len(entries) != 1 || entries[0].Name() != ".studio-lock" {
			return nil, ErrOwnership
		}
		if err := os.MkdirAll(config.OutputDir, 0o750); err != nil {
			return nil, err
		}
		output, err := os.ReadDir(config.OutputDir)
		if err != nil || len(output) != 0 {
			return nil, fmt.Errorf("%w: initial output must be empty", ErrOwnership)
		}
		entries, err = os.ReadDir(config.Root)
		if err != nil {
			return nil, err
		}
		emptyRoot := true
		for _, entry := range entries {
			emptyRoot = emptyRoot && (entry.Name() == ".git" || entry.Name() == ".studio-lock")
		}
		if emptyRoot {
			if err := writeSource(config.Root, "index.md", []byte("# Documentation\n")); err != nil {
				return nil, err
			}
			manifest := []byte("{\"version\":1,\"title\":\"Documentation\",\"pages\":[{\"id\":\"home\",\"title\":\"Home\",\"source\":\"index.md\",\"path\":\"index.html\",\"kind\":\"markdown\"}]}\n")
			if err := writeSource(config.Root, "docs.json", manifest); err != nil {
				return nil, err
			}
		}
		if _, err := loadManifest(config.Root); err != nil {
			return nil, err
		}
		ownerData, err = json.Marshal(ownership{Format: "pb-docs-managed/v1", Root: config.Root, Output: config.OutputDir})
		if err != nil {
			return nil, err
		}
		if err := writeSource(config.StateDir, "owner.json", ownerData); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if !matchesOwnership(ownerData, config) {
		return nil, ErrOwnership
	}
	for _, name := range []string{"drafts"} {
		if err := os.MkdirAll(filepath.Join(config.StateDir, name), 0o700); err != nil {
			return nil, err
		}
	}
	if config.Storage == nil {
		config.Storage = NewLocalBackend()
	}
	if rooted, ok := config.Storage.(interface{ ManagedRoot() string }); ok && rooted.ManagedRoot() != config.Root {
		return nil, ErrOwnership
	}
	if managed, ok := config.Storage.(*ManagedBackend); ok {
		if managed.managedState() != config.StateDir {
			return nil, ErrOwnership
		}
		if err := managed.Recover(context.Background()); err != nil {
			return nil, err
		}
	}
	service := &Service{cfg: config, gate: make(chan struct{}, 1), locks: locks}
	recoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), config.OperationTimeout)
	recoveryErr := service.recoverPublication(recoveryCtx)
	if recoveryErr == nil {
		recoveryErr = service.recoverDeleteTree(recoveryCtx)
	}
	cancelRecovery()
	if recoveryErr != nil {
		return nil, recoveryErr
	}
	if err := os.MkdirAll(config.OutputDir, 0o750); err != nil {
		return nil, err
	}
	stateRoot, err := os.OpenRoot(config.StateDir)
	if err != nil {
		return nil, err
	}
	err = noSymlinks(stateRoot, "history")
	if err == nil {
		err = stateRoot.MkdirAll("history", 0o700)
	}
	err = errors.Join(err, stateRoot.Close())
	if err != nil {
		return nil, err
	}
	if _, err := service.Project(context.Background()); err != nil {
		return nil, errors.Join(err, service.Close())
	}
	retainLocks = true
	return service, nil
}

// Close waits for the active operation and releases all managed location locks.
func (s *Service) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.OperationTimeout)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
		if s.closed {
			return nil
		}
		s.closed = true
		var err error
		for _, lock := range s.locks {
			err = errors.Join(err, lock.Close())
		}
		s.locks = nil
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func rejectDevelopmentCheckout(locations ...string) error {
	working, err := os.Getwd()
	if err != nil {
		return err
	}
	for current := working; ; current = filepath.Dir(current) {
		data, err := readSource(current, "go.mod", 1<<20)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: development checkout cannot be verified", ErrOwnership)
		}
		if err == nil && strings.Contains(string(data), "module github.com/kordax/pb-md5-generator") {
			for _, location := range locations {
				if contains(current, location) || contains(location, current) {
					return fmt.Errorf("%w: development checkout cannot be managed content", ErrOwnership)
				}
			}
			break
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

func (s *Service) enter(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.OperationTimeout)
	select {
	case s.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.gate
			cancel()
			return nil, nil, err
		}
		if s.closed || s.deleteTreePending || s.publicationPending {
			<-s.gate
			cancel()
			if s.closed {
				return nil, nil, ErrOwnership
			}
			return nil, nil, fmt.Errorf("%w: interrupted publication requires recovery", ErrConflict)
		}
		return ctx, func() { <-s.gate; cancel() }, nil
	case <-ctx.Done():
		cancel()
		return nil, nil, ctx.Err()
	}
}

func (s *Service) project(ctx context.Context) (Project, sourceTree, error) {
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return Project{}, sourceTree{}, err
	}
	tree, err := scanTreeCached(ctx, s.cfg.Root, storageIdentity(status), "", s.cached.Files)
	if err != nil {
		return Project{}, tree, err
	}
	s.cached = tree
	manifest, err := loadManifest(s.cfg.Root)
	if err != nil {
		return Project{}, tree, err
	}
	result := Project{Title: manifest.Title, Description: manifest.Description, Revision: tree.Revision, Documents: []Entry{}, Files: []Entry{}}
	pages := make(map[string]Entry)
	for _, page := range manifest.Pages {
		state, ok := tree.Files[page.Source]
		if !ok {
			return Project{}, tree, fmt.Errorf("%w: document source is missing", ErrInvalid)
		}
		entry := Entry{ID: page.ID, Path: page.Source, Title: page.Title, Kind: page.Kind, OutputPath: page.Path, Size: state.Size, Revision: tree.Revision}
		result.Documents = append(result.Documents, entry)
		pages[page.Source] = entry
	}
	for _, name := range sortedFiles(tree) {
		entry := tree.Files[name].Entry
		if page, ok := pages[name]; ok {
			entry = page
		}
		entry.Revision = tree.Revision
		result.Files = append(result.Files, entry)
	}
	for _, name := range tree.Dirs {
		result.Files = append(result.Files, Entry{Path: name, Directory: true, Revision: tree.Revision})
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	return result, tree, nil
}

func (s *Service) Project(ctx context.Context) (Project, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return Project{}, err
	}
	defer done()
	project, _, err := s.project(ctx)
	return project, err
}

func (s *Service) Read(ctx context.Context, name string) (File, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return File{}, err
	}
	defer done()
	if !allowedFile(name) {
		return File{}, ErrInvalid
	}
	project, tree, err := s.project(ctx)
	if err != nil {
		return File{}, err
	}
	for _, entry := range project.Files {
		if entry.Path != name || entry.Directory {
			continue
		}
		limit := int64(MaxTextBytes)
		if binaryFile(name) {
			limit = MaxBinaryBytes
		}
		data, err := readSource(s.cfg.Root, name, limit)
		if err != nil {
			return File{}, err
		}
		if !binaryFile(name) && !utf8.Valid(data) {
			return File{}, ErrInvalid
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != tree.Files[name].Hash {
			s.cached = sourceTree{}
			refreshed, _, err := s.project(ctx)
			if err != nil {
				return File{}, err
			}
			for _, candidate := range refreshed.Files {
				if candidate.Path == name {
					entry = candidate
					break
				}
			}
		}
		result := File{Entry: entry, Binary: binaryFile(name)}
		if result.Binary {
			result.Data = data
		} else {
			result.Content = string(data)
		}
		return result, nil
	}
	return File{}, ErrNotFound
}

func normalizeActor(actor Actor) (Actor, error) {
	if actor.ID == "" || len(actor.ID) > 128 || len(actor.Name) > 256 || len(actor.Email) > 320 {
		return actor, ErrInvalid
	}
	for _, value := range []string{actor.ID, actor.Name, actor.Email} {
		if !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return actor, ErrInvalid
		}
	}
	if actor.Name == "" {
		actor.Name = actor.ID
	}
	if actor.Email == "" {
		actor.Email = "editor@localhost"
	}
	return actor, nil
}

func draftChanges(draft Draft) []Change {
	if len(draft.Changes) != 0 {
		return draft.Changes
	}
	return []Change{draft.Change}
}

func validateChanges(changes []Change) error {
	if len(changes) == 0 || len(changes) > MaxChanges {
		return ErrLimit
	}
	var total int
	for _, change := range changes {
		if !validPath(change.Path) || (change.Path == "docs.json" && change.Operation != "write") || (change.To != "" && (!validPath(change.To) || change.To == "docs.json")) {
			return ErrInvalid
		}
		if change.Operation != "delete-tree" && change.PlanRevision != "" {
			return ErrInvalid
		}
		switch change.Operation {
		case "delete-tree":
			if len(changes) != 1 || !validRevision(change.PlanRevision) || change.To != "" || change.Title != "" || change.Kind != "" || change.OutputPath != "" || change.DocumentID != "" {
				return ErrInvalid
			}
		case "write":
			if !allowedFile(change.Path) || (change.Content != nil && len(change.Data) != 0) {
				return ErrInvalid
			}
			data := change.Data
			if change.Content != nil {
				data = []byte(*change.Content)
			}
			limit := MaxTextBytes
			if binaryFile(change.Path) {
				limit = MaxBinaryBytes
			} else if !utf8.Valid(data) {
				return ErrInvalid
			}
			if len(data) > limit {
				return ErrLimit
			}
			total += len(data)
		case "move":
			if change.To == "" || strings.HasPrefix(change.To+"/", change.Path+"/") {
				return ErrInvalid
			}
		case "delete", "mkdir", "rmdir":
		default:
			return ErrInvalid
		}
		if change.Operation != "write" && (change.Content != nil || len(change.Data) != 0) {
			return ErrInvalid
		}
		if !utf8.ValidString(change.Title) || len(change.Title) > 256 || strings.IndexFunc(change.Title, unicode.IsControl) >= 0 || (change.Kind != "" && !validKind(change.Kind)) || (change.OutputPath != "" && !validOutput(change.OutputPath)) || (change.DocumentID != "" && !validDocumentID(change.DocumentID)) {
			return ErrInvalid
		}
	}
	if total > MaxBinaryBytes {
		return ErrLimit
	}
	return nil
}

func (s *Service) SaveDraft(ctx context.Context, input DraftInput, actor Actor) (Draft, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return Draft{}, err
	}
	defer done()
	return s.saveDraftLocked(ctx, input, actor, nil)
}

func (s *Service) saveDraftLocked(ctx context.Context, input DraftInput, actor Actor, guard func() error) (Draft, error) {
	var err error
	actor, err = normalizeActor(actor)
	if err != nil {
		return Draft{}, err
	}
	changes := input.Changes
	if len(changes) == 0 {
		changes = []Change{input.Change}
	} else if input.Change.Operation != "" {
		return Draft{}, ErrInvalid
	}
	if err := validateChanges(changes); err != nil {
		return Draft{}, err
	}
	if !validRevision(input.BaseRevision) {
		return Draft{}, ErrInvalid
	}
	var old Draft
	if input.ID != "" {
		old, err = s.readDraft(input.ID)
		if err != nil {
			return Draft{}, err
		}
		if old.Actor.ID != actor.ID || old.Revision != input.Revision {
			return Draft{}, ErrConflict
		}
	} else {
		metadata, err := s.drafts()
		if err != nil {
			return Draft{}, err
		}
		if len(metadata) >= MaxDrafts {
			return Draft{}, ErrLimit
		}
		input.ID, err = randomID()
		if err != nil {
			return Draft{}, err
		}
	}
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return Draft{}, err
	}
	if old.ID != "" && !s.historyBranchMatches(old.Branch, status.Branch) {
		return Draft{}, ErrConflict
	}
	if change, recursive := deleteTreeChange(changes); recursive {
		if old.ID != "" {
			if _, previousRecursive := deleteTreeChange(draftChanges(old)); !previousRecursive {
				return Draft{}, ErrConflict
			}
		}
		scope, err := s.planDeleteTree(ctx, DeleteTreeInput{Path: change.Path, Revision: input.BaseRevision}, actor, old.ID)
		if err != nil {
			return Draft{}, err
		}
		if scope.plan.PlanRevision != change.PlanRevision {
			return Draft{}, ErrConflict
		}
	}
	draft := Draft{ID: input.ID, BaseRevision: input.BaseRevision, Branch: status.Branch, Actor: actor, UpdatedAt: time.Now().UTC()}
	if len(input.Changes) != 0 {
		draft.Changes = changes
	} else {
		draft.Change = changes[0]
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return Draft{}, err
	}
	digest := sha256.Sum256(data)
	draft.Revision = hex.EncodeToString(digest[:])
	if guard != nil {
		if err := guard(); err != nil {
			return Draft{}, err
		}
	}
	if err := s.storeDraft(draft, old.Revision); err != nil {
		return Draft{}, err
	}
	return draft, nil
}

func (s *Service) drafts() ([]Draft, error) {
	entries, err := os.ReadDir(filepath.Join(s.cfg.StateDir, "drafts"))
	if err != nil {
		return nil, err
	}
	result := []Draft{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		if len(result) >= MaxDrafts {
			return nil, ErrLimit
		}
		data, err := readSource(s.cfg.StateDir, "drafts/"+entry.Name(), 128<<10)
		if err != nil {
			return nil, err
		}
		var draft Draft
		if err := json.Unmarshal(data, &draft); err != nil || !validID(draft.ID) {
			return nil, ErrInvalid
		}
		result = append(result, draft)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

func (s *Service) Drafts(ctx context.Context) ([]Draft, error) {
	_, done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return s.drafts()
}

func (s *Service) readDraft(id string) (Draft, error) {
	if !validID(id) {
		return Draft{}, ErrInvalid
	}
	data, err := readSource(s.cfg.StateDir, "drafts/"+id+".meta.json", 128<<10)
	if err != nil {
		return Draft{}, err
	}
	var metadata Draft
	if err := json.Unmarshal(data, &metadata); err != nil || !validRevision(metadata.Revision) {
		return Draft{}, ErrInvalid
	}
	data, err = readSource(s.cfg.StateDir, "drafts/"+id+"-"+metadata.Revision+".json", 128<<20)
	if err != nil {
		return Draft{}, err
	}
	var draft Draft
	if err := json.Unmarshal(data, &draft); err != nil || draft.ID != id || draft.Revision != metadata.Revision {
		return Draft{}, ErrInvalid
	}
	return draft, nil
}

func (s *Service) ReadDraft(ctx context.Context, id string) (Draft, error) {
	_, done, err := s.enter(ctx)
	if err != nil {
		return Draft{}, err
	}
	defer done()
	return s.readDraft(id)
}

func (s *Service) storeDraft(draft Draft, previous string) error {
	name := "drafts/" + draft.ID + "-" + draft.Revision + ".json"
	if err := s.writeState(name, draft); err != nil {
		return err
	}
	metadata := draft
	metadata.Change.Content, metadata.Change.Data = nil, nil
	metadata.Changes = append([]Change(nil), draft.Changes...)
	for i := range metadata.Changes {
		metadata.Changes[i].Content, metadata.Changes[i].Data = nil, nil
	}
	if err := s.writeState("drafts/"+draft.ID+".meta.json", metadata); err != nil {
		_ = removeSource(s.cfg.StateDir, name)
		return err
	}
	if previous != "" {
		return removeSource(s.cfg.StateDir, "drafts/"+draft.ID+"-"+previous+".json")
	}
	return nil
}

func (s *Service) DiscardDraft(ctx context.Context, id, revision string, actor Actor) error {
	_, done, err := s.enter(ctx)
	if err != nil {
		return err
	}
	defer done()
	draft, err := s.readDraft(id)
	if err != nil {
		return err
	}
	if draft.Actor.ID != actor.ID || draft.Revision != revision {
		return ErrConflict
	}
	return s.discard(draft)
}

func (s *Service) discard(draft Draft) error {
	if err := removeSource(s.cfg.StateDir, "drafts/"+draft.ID+".meta.json"); err != nil {
		return err
	}
	return removeSource(s.cfg.StateDir, "drafts/"+draft.ID+"-"+draft.Revision+".json")
}

func (s *Service) writeState(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var total int64
	err = filepath.WalkDir(s.cfg.StateDir, func(name string, entry fs.DirEntry, walkErr error) error {
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
	if err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(s.cfg.StateDir, filepath.FromSlash(name))); err == nil {
		total -= info.Size()
	}
	if total+int64(len(data)) > MaxStateBytes {
		return ErrLimit
	}
	return writeSource(s.cfg.StateDir, name, data)
}
