package content

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceOwnerResaveKeepsNewTextWhileLiveProjectNeedsRepair(t *testing.T) {
	for _, fault := range []string{"malformed manifest", "unrelated invalid UTF8"} {
		t.Run(fault, func(t *testing.T) {
			s, cfg := managedFixture(t)
			s.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
			project, err := s.Project(t.Context())
			require.NoError(t, err)
			const name = "examples/repair.go"
			owned, err := s.SaveSource(t.Context(), SourceDraftInput{Path: name, Revision: project.Revision, Data: []byte("package first\r\n")}, editor)
			require.NoError(t, err)
			originalManifest, err := readSource(cfg.Root, "docs.json", MaxTextBytes)
			require.NoError(t, err)
			corruptPath, corruptBytes := "docs.json", []byte("{temporarily broken manifest")
			if fault == "unrelated invalid UTF8" {
				corruptPath, corruptBytes = "unrelated-broken.txt", []byte{0xff, 0xfe}
			}
			require.NoError(t, writeSource(cfg.Root, corruptPath, corruptBytes))
			merged := []byte("\xef\xbb\xbfpackage merged\r\n// Keep new owner edits exactly.\r\n")
			saved, err := s.SaveSource(t.Context(), SourceDraftInput{Path: name, Revision: owned.BaseRevision, DraftID: owned.ID, DraftRevision: owned.Revision, Data: merged}, editor)
			require.NoError(t, err)
			assert.Equal(t, owned.ID, saved.ID)
			assert.Equal(t, owned.BaseRevision, saved.BaseRevision)
			assert.NotEqual(t, owned.Revision, saved.Revision)
			assert.Equal(t, merged, changeData(saved.Change))
			_, err = s.SaveSource(t.Context(), SourceDraftInput{Path: name, Revision: owned.BaseRevision, DraftID: owned.ID, DraftRevision: owned.Revision, Data: []byte("Stale caller must not replace merged text\n")}, editor)
			assert.ErrorIs(t, err, ErrConflict)
			_, err = s.RebaseSource(t.Context(), SourceDraftInput{Path: name, Revision: project.Revision, DraftID: saved.ID, DraftRevision: saved.Revision, Data: []byte("Cannot review a broken project\n")}, editor)
			require.Error(t, err)
			_, err = s.Publish(t.Context(), PublishInput{ID: saved.ID, Revision: saved.Revision}, editor)
			require.Error(t, err)
			retained, err := s.ReadDraft(t.Context(), saved.ID)
			require.NoError(t, err)
			assert.Equal(t, saved, retained)
			actualCorruption, err := readSource(cfg.Root, corruptPath, MaxTextBytes)
			require.NoError(t, err)
			assert.Equal(t, corruptBytes, actualCorruption)
			_, err = readSource(cfg.Root, name, MaxTextBytes)
			assert.ErrorIs(t, err, ErrNotFound)
			if fault == "malformed manifest" {
				require.NoError(t, writeSource(cfg.Root, "docs.json", originalManifest))
			} else {
				require.NoError(t, removeSource(cfg.Root, corruptPath))
			}
			current, err := s.Project(t.Context())
			require.NoError(t, err)
			rebased, err := s.RebaseSource(t.Context(), SourceDraftInput{Path: name, Revision: current.Revision, DraftID: saved.ID, DraftRevision: saved.Revision, Data: merged}, editor)
			require.NoError(t, err)
			assert.Equal(t, saved.ID, rebased.ID)
			assert.Equal(t, current.Revision, rebased.BaseRevision)
			assert.Equal(t, merged, changeData(rebased.Change))
			publishDraft(t, s, rebased)
			published, err := readSource(cfg.Root, name, MaxTextBytes)
			require.NoError(t, err)
			assert.Equal(t, merged, published)
		})
	}
}
