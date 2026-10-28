package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sourceFixture(t *testing.T) (*Service, Config) {
	t.Helper()
	service, config := managedFixture(t)
	service.cfg.AuthorizeSource = func(ctx context.Context, actor Actor, _ string, _ SourceAccess) error {
		if actor.ID != editor.ID {
			return ErrNotFound
		}
		return ctx.Err()
	}
	return service, config
}

func sourceBase(t *testing.T, service *Service) string {
	t.Helper()
	project, err := service.Project(t.Context())
	require.NoError(t, err)
	return project.Revision
}

func TestSourceOwnerCASAndReviewedRebasePreserveExactBytes(t *testing.T) {
	service, config := sourceFixture(t)
	base := sourceBase(t, service)
	original := []byte("\ufeffconst value = '<script>literal</script>';\r\n")
	draft, err := service.SaveSource(t.Context(), SourceDraftInput{Path: "examples/client.js", Revision: base, Data: original}, editor)
	require.NoError(t, err)
	require.NotNil(t, draft.Change.Content)
	assert.Equal(t, original, []byte(*draft.Change.Content))
	assert.Equal(t, base, sourceBase(t, service), "staging must not change published sources")
	firstRevision := draft.Revision
	updated := append(append([]byte(nil), original...), []byte("// kept bytes\r\n")...)
	draft, err = service.SaveSource(t.Context(), SourceDraftInput{Path: "examples/client.js", Revision: base, DraftID: draft.ID, DraftRevision: draft.Revision, Data: updated}, editor)
	require.NoError(t, err)
	assert.NotEqual(t, firstRevision, draft.Revision)
	_, err = service.SaveSource(t.Context(), SourceDraftInput{Path: "examples/client.js", Revision: base, DraftID: draft.ID, DraftRevision: firstRevision, Data: nil}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	publishDraft(t, service, saveText(t, service, "index.md", "# Independently published home\n"))
	latest := sourceBase(t, service)
	require.NotEqual(t, base, latest)
	draft, err = service.SaveSource(t.Context(), SourceDraftInput{Path: "examples/client.js", Revision: base, DraftID: draft.ID, DraftRevision: draft.Revision, Data: updated}, editor)
	require.NoError(t, err)
	assert.Equal(t, base, draft.BaseRevision, "ordinary resave must not silently rebase")
	_, err = service.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	_, err = service.RebaseSource(t.Context(), SourceDraftInput{Path: "examples/client.js", Revision: base, DraftID: draft.ID, DraftRevision: draft.Revision, Data: updated}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	rebased, err := service.RebaseSource(t.Context(), SourceDraftInput{Path: "examples/client.js", Revision: latest, DraftID: draft.ID, DraftRevision: draft.Revision, Data: updated}, editor)
	require.NoError(t, err)
	assert.Equal(t, draft.ID, rebased.ID)
	assert.Equal(t, latest, rebased.BaseRevision)
	_, err = os.Stat(filepath.Join(config.Root, "examples", "client.js"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	publishDraft(t, service, rebased)
	data, err := os.ReadFile(filepath.Join(config.Root, "examples", "client.js"))
	require.NoError(t, err)
	assert.Equal(t, updated, data)
}

func TestSourceHistoryRestoresDeletedAndEmptyFilesWithoutPageMetadata(t *testing.T) {
	service, config := sourceFixture(t)
	const path = "examples/snippet.md"
	original := []byte("# Original snippet\n")
	draft, err := service.SaveSource(t.Context(), SourceDraftInput{Path: path, Revision: sourceBase(t, service), Data: original}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	draft, err = service.SaveSource(t.Context(), SourceDraftInput{Path: path, Revision: sourceBase(t, service), Data: []byte{}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	history, err := service.SourceHistory(t.Context(), path, editor)
	require.NoError(t, err)
	assert.Equal(t, sourceBase(t, service), history.Revision)
	var originalID, emptyID string
	for _, item := range history.Items {
		assert.Equal(t, path, item.Path)
		if !item.Deleted && item.Size == int64(len(original)) {
			originalID = item.ID
		}
		if !item.Deleted && item.Size == 0 {
			emptyID = item.ID
		}
	}
	require.NotEmpty(t, originalID)
	require.NotEmpty(t, emptyID)
	deletion, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: sourceBase(t, service), Change: Change{Operation: "delete", Path: path}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, deletion)
	history, err = service.SourceHistory(t.Context(), path, editor)
	require.NoError(t, err, "history remains readable when its source is absent")
	require.NotEmpty(t, history.Items)
	_, err = service.RestoreSource(t.Context(), RestoreSourceInput{Path: "examples/other.md", HistoryID: originalID, Revision: history.Revision}, editor)
	assert.ErrorIs(t, err, ErrNotFound)
	restored, err := service.RestoreSource(t.Context(), RestoreSourceInput{Path: path, HistoryID: originalID, Revision: history.Revision}, editor)
	require.NoError(t, err)
	assert.Empty(t, restored.Change.DocumentID)
	assert.Empty(t, restored.Change.Title)
	assert.Empty(t, restored.Change.Kind)
	assert.Empty(t, restored.Change.OutputPath)
	require.NotNil(t, restored.Change.Content)
	assert.Equal(t, original, []byte(*restored.Change.Content))
	_, err = os.Stat(filepath.Join(config.Root, filepath.FromSlash(path)))
	assert.ErrorIs(t, err, os.ErrNotExist)
	publishDraft(t, service, restored)
	empty, err := service.RestoreSource(t.Context(), RestoreSourceInput{Path: path, HistoryID: emptyID, Revision: sourceBase(t, service)}, editor)
	require.NoError(t, err)
	require.NotNil(t, empty.Change.Content)
	assert.Empty(t, *empty.Change.Content)
	publishDraft(t, service, empty)
	file, err := service.Read(t.Context(), path)
	require.NoError(t, err)
	assert.Empty(t, file.Content)
}

func TestSourceAuthorizationAndCompetingDraftsNeverLoseText(t *testing.T) {
	service, _ := sourceFixture(t)
	base := sourceBase(t, service)
	denied := errors.New("source permission revoked")
	calls := 0
	service.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error {
		calls++
		if calls > 1 {
			return denied
		}
		return nil
	}
	_, err := service.SaveSource(t.Context(), SourceDraftInput{Path: "example.go", Revision: base, Data: []byte("package example\n")}, editor)
	assert.ErrorIs(t, err, denied, "permissions must be checked again immediately before storage")
	drafts, err := service.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
	assert.Equal(t, base, sourceBase(t, service))
	service.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
	other := Actor{ID: "other", Name: "Other"}
	pending, err := service.SaveDraft(t.Context(), DraftInput{BaseRevision: base, Change: Change{Operation: "move", Path: "old", To: "examples"}}, other)
	require.NoError(t, err)
	_, err = service.RebaseSource(t.Context(), SourceDraftInput{Path: "examples/new.js", Revision: base, Data: []byte("new text")}, editor)
	assert.ErrorIs(t, err, ErrConflict, "a new reviewed draft cannot replace a competing move destination")
	kept, err := service.ReadDraft(t.Context(), pending.ID)
	require.NoError(t, err)
	assert.Equal(t, pending, kept)
}

func TestSourceOperationsFailClosedAndRejectInvalidOrStaleInputs(t *testing.T) {
	service, _ := managedFixture(t)
	base := sourceBase(t, service)
	_, err := service.SourceHistory(t.Context(), "index.md", editor)
	assert.ErrorIs(t, err, ErrUnsupported)
	service.cfg.AuthorizeSource = func(context.Context, Actor, string, SourceAccess) error { return nil }
	for _, input := range []SourceDraftInput{
		{Path: "../escape.md", Revision: base},
		{Path: "bad.exe", Revision: base},
		{Path: "bad.md", Revision: "stale"},
		{Path: "bad.md", Revision: base, DraftID: strings.Repeat("a", 32)},
		{Path: "bad.md", Revision: base, DraftRevision: base},
		{Path: "bad.md", Revision: base, Data: []byte{0xff}},
		{Path: "bad.md", Revision: base, Data: []byte{0}},
	} {
		_, err := service.SaveSource(t.Context(), input, editor)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	_, err = service.RebaseSource(t.Context(), SourceDraftInput{Path: "asset.png", Revision: base, Data: []byte{137, 80, 78, 71}}, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = service.SourceHistory(t.Context(), "absent.md", editor)
	assert.ErrorIs(t, err, ErrNotFound)
	publishDraft(t, service, saveText(t, service, "index.md", "# A newer project\n"))
	_, err = service.SaveSource(t.Context(), SourceDraftInput{Path: "new.md", Revision: base, Data: []byte("Unreviewed old base")}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	drafts, err := service.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, drafts)
}
