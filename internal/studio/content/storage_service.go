package content

import (
	"context"
	"errors"
	"path/filepath"
)

// authorizeStorage runs after the service gate is acquired, preserving request
// values so revocation while waiting cannot authorize a later storage mutation.
func (s *Service) authorizeStorage(ctx context.Context, actor Actor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.cfg.Validate != nil {
		if err := s.cfg.Validate(ctx, s.cfg.Root, nil, Actor{}); err != nil {
			return err
		}
	}
	if s.cfg.AuthorizeStorage != nil {
		return s.cfg.AuthorizeStorage(ctx, actor)
	}
	return nil
}

func (s *Service) storageResult(ctx context.Context) (StorageResult, error) {
	s.cached = sourceTree{}
	project, _, err := s.project(ctx)
	if err != nil {
		return StorageResult{}, err
	}
	managed, ok := s.cfg.Storage.(*ManagedBackend)
	if !ok {
		status, err := s.cfg.Storage.Status(ctx)
		return StorageResult{Settings: StorageSettings{Backend: status.Backend, Remotes: []StorageRemote{}}, ProjectRevision: project.Revision, Sync: status, Remotes: []RemoteSyncStatus{}}, err
	}
	status, err := managed.Status(ctx)
	if err != nil {
		return StorageResult{}, err
	}
	if pending, err := s.pending(); err == nil {
		status.State, status.Retryable = "pending", true
		if pending.Commit == "" {
			status.State, status.Error = "error", "Published changes need a Git commit retry"
		}
	} else if !errors.Is(err, ErrNotFound) {
		return StorageResult{}, err
	}
	managed.mu.Lock()
	defer managed.mu.Unlock()
	return StorageResult{Settings: managed.settingsLocked(), ProjectRevision: project.Revision, Sync: status, Remotes: managed.remoteStatusesLocked(status)}, nil
}

// Storage returns an atomic local view. It never fetches, probes or pushes a
// remote. Public status endpoints should continue using the narrower Status.
func (s *Service) Storage(ctx context.Context) (StorageResult, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return StorageResult{}, err
	}
	defer done()
	if err := s.authorizeStorage(ctx, Actor{}); err != nil {
		return StorageResult{}, err
	}
	return s.storageResult(ctx)
}

func (s *Service) StorageSettings(ctx context.Context) (StorageSettings, error) {
	result, err := s.Storage(ctx)
	return result.Settings, err
}

func (s *Service) ConfigureStorage(ctx context.Context, input StorageUpdate, actor Actor) (StorageResult, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return StorageResult{}, err
	}
	defer done()
	actor, err = normalizeActor(actor)
	if err != nil {
		return StorageResult{}, err
	}
	if err := s.authorizeStorage(ctx, actor); err != nil {
		return StorageResult{}, err
	}
	managed, ok := s.cfg.Storage.(*ManagedBackend)
	if !ok {
		return StorageResult{}, ErrUnsupported
	}
	if !validRevision(input.Revision) || !validRevision(input.BaseRevision) || input.Remotes == nil {
		return StorageResult{}, ErrInvalid
	}
	s.cached = sourceTree{}
	project, before, err := s.project(ctx)
	if err != nil {
		return StorageResult{}, err
	}
	if input.BaseRevision != project.Revision {
		return StorageResult{}, ErrConflict
	}
	if pending, err := s.pending(); err == nil && pending.Commit == "" {
		return StorageResult{}, storageConfigurationConflict()
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return StorageResult{}, err
	}
	transaction, id, err := createStorageStage(s.cfg.StateDir, ".txn-configuration-")
	if err != nil {
		return StorageResult{}, err
	}
	defer func() { _ = removeStorageTree(s.cfg.StateDir, ".txn-configuration-"+id) }()
	var snapshot string
	managed.mu.Lock()
	enrolling := managed.settings.Backend == "local" && input.Backend == "git"
	managed.mu.Unlock()
	if enrolling {
		candidateBefore, _, err := s.candidate(ctx, transaction, nil, Actor{}, false)
		if err != nil {
			return StorageResult{}, err
		}
		if candidateBefore.Revision != before.Revision {
			return StorageResult{}, ErrConflict
		}
		snapshot = filepath.Join(transaction, "source")
	}
	if err := managed.configure(ctx, input, actor, snapshot, before); err != nil {
		return StorageResult{}, err
	}
	return s.storageResult(ctx)
}

func (s *Service) CheckRemote(ctx context.Context, name, settingsRevision string, actor Actor) (RemoteSyncStatus, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	defer done()
	actor, err = normalizeActor(actor)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	if err := s.authorizeStorage(ctx, actor); err != nil {
		return RemoteSyncStatus{}, err
	}
	if !validRemoteName(name) || !validRevision(settingsRevision) {
		return RemoteSyncStatus{}, ErrInvalid
	}
	managed, ok := s.cfg.Storage.(*ManagedBackend)
	if !ok {
		return RemoteSyncStatus{}, ErrUnsupported
	}
	return managed.checkRemote(ctx, name, settingsRevision)
}

func (s *Service) PushRemote(ctx context.Context, name, projectRevision, settingsRevision string, actor Actor) (RemoteSyncStatus, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	defer done()
	actor, err = normalizeActor(actor)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	if err := s.authorizeStorage(ctx, actor); err != nil {
		return RemoteSyncStatus{}, err
	}
	if !validRemoteName(name) || !validRevision(settingsRevision) || !validRevision(projectRevision) {
		return RemoteSyncStatus{}, ErrInvalid
	}
	s.cached = sourceTree{}
	project, _, err := s.project(ctx)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	if project.Revision != projectRevision {
		return RemoteSyncStatus{}, ErrConflict
	}
	if pending, err := s.pending(); err == nil && pending.Commit == "" {
		return RemoteSyncStatus{}, storageConfigurationConflict()
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return RemoteSyncStatus{}, err
	}
	managed, ok := s.cfg.Storage.(*ManagedBackend)
	if !ok {
		return RemoteSyncStatus{}, ErrUnsupported
	}
	result, err := managed.pushRemote(ctx, name, settingsRevision)
	if err != nil {
		return RemoteSyncStatus{}, err
	}
	if result.State == "ready" {
		status, err := managed.Status(ctx)
		if err != nil {
			return RemoteSyncStatus{}, err
		}
		if status.State != "pending" && status.State != "error" && !status.Dirty {
			if pending, err := s.pending(); err == nil && pending.Commit == status.Head && pending.Branch == status.Branch {
				if err := removeStorageFile(s.cfg.StateDir, "pending.json"); err != nil {
					return RemoteSyncStatus{}, err
				}
			} else if err != nil && !errors.Is(err, ErrNotFound) {
				return RemoteSyncStatus{}, err
			}
		}
	}
	return result, nil
}

// MatchesCurrentBranch includes pre-enrollment local history and drafts only
// on the initial enrolled branch. It never treats an empty branch as global.
func (s *Service) MatchesCurrentBranch(ctx context.Context, branch string) (bool, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return false, err
	}
	defer done()
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return false, err
	}
	return s.historyBranchMatches(branch, status.Branch), nil
}
