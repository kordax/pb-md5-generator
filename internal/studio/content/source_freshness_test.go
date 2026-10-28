package content

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sourceSnapshotChangeBackend struct {
	Backend
	ready  func() bool
	change func() error
}

func (b *sourceSnapshotChangeBackend) Status(ctx context.Context) (SyncStatus, error) {
	if b.change != nil && b.ready() {
		change := b.change
		b.change = nil
		if err := change(); err != nil {
			return SyncStatus{}, err
		}
	}
	return b.Backend.Status(ctx)
}

// A stat-poor filesystem can report the same observable file identity after a
// same-size edit with restored timestamps. Keep its old cached content hash
// and use the edited file's observable stat to model that condition portably.
// The assertions below concern public CAS and retained bytes, not cache state.
func sourceEditWithUnchangedObservableStat(t *testing.T, s *Service, cfg Config, external []byte) error {
	t.Helper()
	name := filepath.Join(cfg.Root, "index.md")
	before, err := os.Stat(name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(name, external, before.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(name, before.ModTime(), before.ModTime()); err != nil {
		return err
	}
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	cached, exists := s.cached.Files["index.md"]
	require.True(t, exists, "the fixture must have an already observed source snapshot")
	cached.Info = info
	s.cached.Files["index.md"] = cached
	return nil
}

func TestSourceReviewedCASRejectsStatPoorFilesystemChangesWithoutLosingDrafts(t *testing.T) {
	for _, timing := range []string{"before call", "after initial snapshot"} {
		for _, action := range []string{"new source", "owned rebase", "history restore"} {
			t.Run(timing+"/"+action, func(t *testing.T) {
				s, cfg := managedFixture(t)
				s.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
				const historicalPath = "examples/history.go"
				const historicalText = "package original\n"
				publishDraft(t, s, saveText(t, s, historicalPath, historicalText))
				project, err := s.Project(t.Context())
				require.NoError(t, err)
				owned, err := s.SaveSource(t.Context(), SourceDraftInput{Path: "notes/owned.go", Revision: project.Revision, Data: []byte("Keep the original owned draft\r\n")}, editor)
				require.NoError(t, err)
				metadataName := "drafts/" + owned.ID + ".meta.json"
				payloadName := "drafts/" + owned.ID + "-" + owned.Revision + ".json"
				metadata, err := readSource(cfg.StateDir, metadataName, MaxTextBytes)
				require.NoError(t, err)
				payload, err := readSource(cfg.StateDir, payloadName, MaxTextBytes)
				require.NoError(t, err)
				history, err := s.History(t.Context(), historicalPath)
				require.NoError(t, err)
				require.NotEmpty(t, history)
				require.False(t, history[0].Deleted)
				original, err := readSource(cfg.Root, "index.md", MaxTextBytes)
				require.NoError(t, err)
				external := append([]byte(nil), original...)
				require.NotEmpty(t, external)
				external[0] = '!'
				changed := false
				change := func() error {
					changed = true
					return sourceEditWithUnchangedObservableStat(t, s, cfg, external)
				}
				if timing == "before call" {
					require.NoError(t, change())
				} else {
					s.cfg.Storage = &sourceSnapshotChangeBackend{Backend: s.cfg.Storage, ready: func() bool {
						_, exists := s.cached.Files["index.md"]
						return exists
					}, change: change}
				}
				var rejected Draft
				switch action {
				case "new source":
					rejected, err = s.SaveSource(t.Context(), SourceDraftInput{Path: "notes/new.go", Revision: project.Revision, Data: []byte("package candidate\n")}, editor)
				case "owned rebase":
					rejected, err = s.RebaseSource(t.Context(), SourceDraftInput{Path: owned.Change.Path, Revision: project.Revision, DraftID: owned.ID, DraftRevision: owned.Revision, Data: []byte("Reviewed merged candidate\n")}, editor)
				case "history restore":
					rejected, err = s.RestoreSource(t.Context(), RestoreSourceInput{Path: historicalPath, HistoryID: history[0].ID, Revision: project.Revision}, editor)
				}
				require.True(t, changed)
				assert.ErrorIs(t, err, ErrConflict)
				assert.Empty(t, rejected.ID)
				actualMetadata, err := readSource(cfg.StateDir, metadataName, MaxTextBytes)
				require.NoError(t, err)
				actualPayload, err := readSource(cfg.StateDir, payloadName, MaxTextBytes)
				require.NoError(t, err)
				assert.Equal(t, metadata, actualMetadata)
				assert.Equal(t, payload, actualPayload)
				retained, err := s.ReadDraft(t.Context(), owned.ID)
				require.NoError(t, err)
				assert.Equal(t, owned, retained)
				drafts, err := s.Drafts(t.Context())
				require.NoError(t, err)
				require.Len(t, drafts, 1)
				published, err := readSource(cfg.Root, historicalPath, MaxTextBytes)
				require.NoError(t, err)
				assert.Equal(t, historicalText, string(published))
				actualExternal, err := readSource(cfg.Root, "index.md", MaxTextBytes)
				require.NoError(t, err)
				assert.Equal(t, external, actualExternal)
				current, err := s.Project(t.Context())
				require.NoError(t, err)
				assert.NotEqual(t, project.Revision, current.Revision)
			})
		}
	}
}
