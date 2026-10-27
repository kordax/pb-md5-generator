package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// View supplies an isolated, bounded source snapshot while holding the content
// operation gate. The callback must not re-enter this Service or retain paths
// after it returns. Hidden files, Git metadata and credentials are not copied.
// The callback context preserves request values, including fresh authorization.
func (s *Service) View(ctx context.Context, visit func(context.Context, string, string) error) (err error) {
	if visit == nil {
		return ErrInvalid
	}
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return err
	}
	defer done()
	status, err := s.cfg.Storage.Status(ctx)
	if err != nil {
		return err
	}
	snapshot, err := os.MkdirTemp(s.cfg.StateDir, ".view-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(snapshot)) }()
	if _, err := scanTree(ctx, s.cfg.Root, storageIdentity(status), snapshot); err != nil {
		return err
	}
	if _, err := loadManifest(snapshot); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return visit(ctx, snapshot, filepath.Join(snapshot, "docs.json"))
}
