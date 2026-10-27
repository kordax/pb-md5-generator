package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// BuildCurrent validates the current managed sources without editing or committing
// them. Failed initial builds leave the CMS available for repairs by its caller.
func (s *Service) BuildCurrent(ctx context.Context, actor Actor) (PublishResult, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return PublishResult{}, err
	}
	defer done()
	actor, err = normalizeActor(actor)
	if err != nil {
		return PublishResult{}, err
	}
	transaction, err := os.MkdirTemp(s.cfg.StateDir, ".txn-")
	if err != nil {
		return PublishResult{}, err
	}
	defer os.RemoveAll(transaction)
	before, _, err := s.candidate(ctx, transaction, nil, actor, false)
	if err != nil {
		return PublishResult{}, err
	}
	s.cached = sourceTree{}
	current, _, err := s.project(ctx)
	if err != nil || current.Revision != before.Revision {
		return PublishResult{}, errors.Join(ErrConflict, err)
	}
	published, err := replaceOutput(filepath.Join(transaction, "site"), s.cfg.OutputDir)
	if !published {
		return PublishResult{}, err
	}
	result := PublishResult{Revision: before.Revision, Published: true}
	result.Sync, err = s.cfg.Storage.Status(ctx)
	if err != nil {
		result.Sync = SyncStatus{State: "error", Retryable: true, Error: "Site is published; storage status is unavailable"}
	}
	return result, nil
}
