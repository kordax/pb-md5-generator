package content

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceUnicodeAliasesCannotBypassCompetingDraftPathsOrDestinations(t *testing.T) {
	aliases := []struct{ first, second string }{
		{"examples/Σ.js", "examples/ς.js"},
		{"examples/S.js", "examples/ſ.js"},
	}
	for _, names := range aliases {
		for _, position := range []string{"path", "destination"} {
			for _, action := range []string{"save", "rebase"} {
				t.Run(names.first+"/"+position+"/"+action, func(t *testing.T) {
					s, cfg := managedFixture(t)
					s.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
					project, err := s.Project(t.Context())
					require.NoError(t, err)
					text := "Keep the existing saved text exactly\n"
					change := Change{Operation: "write", Path: names.first, Content: &text}
					if position == "destination" {
						change = Change{Operation: "move", Path: "other/original.js", To: names.first}
					}
					other := Actor{ID: "another-editor"}
					pending, err := s.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: change}, other)
					require.NoError(t, err)
					metadataName := "drafts/" + pending.ID + ".meta.json"
					payloadName := "drafts/" + pending.ID + "-" + pending.Revision + ".json"
					metadata, err := readSource(cfg.StateDir, metadataName, MaxTextBytes)
					require.NoError(t, err)
					payload, err := readSource(cfg.StateDir, payloadName, MaxTextBytes)
					require.NoError(t, err)
					input := SourceDraftInput{Path: names.second, Revision: project.Revision, Data: []byte("New unsaved source\n")}
					if action == "save" {
						_, err = s.SaveSource(t.Context(), input, editor)
					} else {
						_, err = s.RebaseSource(t.Context(), input, editor)
					}
					assert.Equal(t, ErrConflict, err)
					actualMetadata, err := readSource(cfg.StateDir, metadataName, MaxTextBytes)
					require.NoError(t, err)
					actualPayload, err := readSource(cfg.StateDir, payloadName, MaxTextBytes)
					require.NoError(t, err)
					assert.Equal(t, metadata, actualMetadata)
					assert.Equal(t, payload, actualPayload)
					drafts, err := s.Drafts(t.Context())
					require.NoError(t, err)
					require.Len(t, drafts, 1)
					assert.Equal(t, pending.ID, drafts[0].ID)
					current, err := s.Project(t.Context())
					require.NoError(t, err)
					assert.Equal(t, project.Revision, current.Revision)
					_, err = s.Read(t.Context(), names.second)
					assert.ErrorIs(t, err, ErrNotFound)
				})
			}
		}
	}
}

func TestSourcePairedOwnerRebaseStillRejectsAnotherUnicodeAliasDraft(t *testing.T) {
	s, cfg := managedFixture(t)
	s.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
	project, err := s.Project(t.Context())
	require.NoError(t, err)
	owned, err := s.SaveSource(t.Context(), SourceDraftInput{Path: "examples/S.js", Revision: project.Revision, Data: []byte("Keep owned text\n")}, editor)
	require.NoError(t, err)
	other, err := s.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "move", Path: "elsewhere/original.js", To: "examples/ſ.js"}}, Actor{ID: "another-editor"})
	require.NoError(t, err)
	before := make(map[string][]byte)
	for _, draft := range []Draft{owned, other} {
		for _, name := range []string{"drafts/" + draft.ID + ".meta.json", "drafts/" + draft.ID + "-" + draft.Revision + ".json"} {
			before[name], err = readSource(cfg.StateDir, name, MaxTextBytes)
			require.NoError(t, err)
		}
	}
	_, err = s.RebaseSource(t.Context(), SourceDraftInput{Path: owned.Change.Path, Revision: project.Revision, DraftID: owned.ID, DraftRevision: owned.Revision, Data: []byte("Reviewed merged text\n")}, editor)
	assert.Equal(t, ErrConflict, err)
	for name, expected := range before {
		actual, err := readSource(cfg.StateDir, name, MaxTextBytes)
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	}
}
