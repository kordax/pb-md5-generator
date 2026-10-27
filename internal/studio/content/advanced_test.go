package content

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaleDraftRemainsSavableUntilExplicitRebase(t *testing.T) {
	service, _ := managedFixture(t)
	stale := saveText(t, service, "index.md", "# My draft\n")
	publishDraft(t, service, saveText(t, service, "index.md", "# Someone published\n"))
	continued := "# My continued offline edits\n"
	draft, err := service.SaveDraft(context.Background(), DraftInput{ID: stale.ID, Revision: stale.Revision, BaseRevision: stale.BaseRevision, Change: Change{Operation: "write", Path: "index.md", Content: &continued}}, editor)
	require.NoError(t, err)
	_, err = service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	stored, err := service.ReadDraft(context.Background(), draft.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.Change.Content)
	assert.Equal(t, continued, *stored.Change.Content)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	draft, err = service.SaveDraft(context.Background(), DraftInput{ID: draft.ID, Revision: draft.Revision, BaseRevision: project.Revision, Change: draft.Change}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	file, err := service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, continued, file.Content)
}

func TestExclusiveRootLockAndMetadataPreservingExternalEdit(t *testing.T) {
	service, config := managedFixture(t)
	_, err := New(config)
	assert.ErrorIs(t, err, ErrConflict)
	other := config
	other.StateDir = filepath.Join(t.TempDir(), "other-state")
	other.OutputDir = filepath.Join(t.TempDir(), "other-output")
	_, err = New(other)
	assert.ErrorIs(t, err, ErrConflict, "different state cannot enable two writers for the same root")
	before, err := service.Project(context.Background())
	require.NoError(t, err)
	file := filepath.Join(config.Root, "index.md")
	info, err := os.Stat(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, []byte("# Xocumentation\n"), 0o600))
	require.NoError(t, os.Chtimes(file, info.ModTime(), info.ModTime()))
	read, err := service.Read(context.Background(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, "# Xocumentation\n", read.Content)
	assert.NotEqual(t, before.Revision, read.Revision, "content reads verify hashes even when an external writer preserves metadata")
	require.NoError(t, service.Close())
	_, err = service.Project(context.Background())
	assert.ErrorIs(t, err, ErrOwnership)
	config.Initialize = false
	reopened, err := New(config)
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
}

func TestManifestDraftAndHistorySurviveDocumentMove(t *testing.T) {
	service, _ := managedFixture(t)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	text := "# A guide\n"
	draft, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "old.md", DocumentID: "stable-guide", Title: "Guide", Content: &text}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	draft, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "move", Path: "old.md", To: "new.md"}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	history, err := service.History(context.Background(), "new.md")
	require.NoError(t, err)
	var historical Revision
	for _, revision := range history {
		if revision.Path == "old.md" && !revision.Deleted {
			historical = revision
		}
	}
	require.NotEmpty(t, historical.ID, "history follows stable identity across source renames")
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	draft, err = service.Restore(context.Background(), "new.md", historical.ID, project.Revision, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "stable-guide", project.Documents[1].ID)
	assert.Equal(t, "new.md", project.Documents[1].Path)
	manifest := `{"version":1,"title":"Changed project","description":"Preserved description","$schema":"urn:pb-docs:manifest:v1","pages":[{"id":"home","title":"Home","source":"index.md","path":"index.html"},{"id":"stable-guide","title":"Guide","source":"new.md","path":"old.html"}]}`
	draft, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "docs.json", Content: &manifest}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Changed project", project.Title)
	assert.Equal(t, "Preserved description", project.Description)
	invalid := `{"version":1,"version":1,"title":"Duplicate field","pages":[]}`
	draft, err = service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "docs.json", Content: &invalid}}, editor)
	require.NoError(t, err)
	_, err = service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	after, err := service.Project(context.Background())
	require.NoError(t, err)
	assert.Equal(t, project.Revision, after.Revision)
}

func TestCaseFoldedPathsAndBoundedDirectoryEnumeration(t *testing.T) {
	service, config := managedFixture(t)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	text := "# Guide\n"
	draft, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: "Guides/Start.md", Title: "Guide", Content: &text}}, editor)
	require.NoError(t, err)
	publishDraft(t, service, draft)
	project, err = service.Project(context.Background())
	require.NoError(t, err)
	for _, change := range []Change{
		{Operation: "write", Path: "guides/other.md", Content: &text},
		{Operation: "write", Path: "Guides/start.md", Content: &text},
		{Operation: "move", Path: "Guides/Start.md", To: "guides/start.md"},
	} {
		draft, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: project.Revision, Change: change}, editor)
		require.NoError(t, err)
		_, err = service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
		assert.ErrorIs(t, err, ErrConflict)
		require.NoError(t, service.DiscardDraft(context.Background(), draft.ID, draft.Revision, editor))
	}
	file, err := service.Read(context.Background(), "Guides/Start.md")
	require.NoError(t, err)
	assert.Equal(t, text, file.Content)
	for i := 0; i < MaxFiles+1; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(config.Root, fmt.Sprintf("file-%04d.txt", i)), nil, 0o600))
	}
	_, err = service.Project(context.Background())
	assert.ErrorIs(t, err, ErrLimit)
}

func TestGitHeadParticipatesInRevisionAndOutputIsStrictlyBounded(t *testing.T) {
	service, config, _ := gitManagedFixture(t)
	project, err := service.Project(context.Background())
	require.NoError(t, err)
	fixtureGit(t, config.Root, "commit", "--allow-empty", "-m", "fixture: observed head changed")
	_, err = service.CreateBranch(context.Background(), "stale-observation", project.Revision)
	assert.ErrorIs(t, err, ErrConflict)
	output := &cappedOutput{limit: 1 << 20}
	input := io.LimitReader(strings.NewReader(strings.Repeat("x", 2<<20)), 2<<20)
	_, err = io.Copy(output, input)
	assert.ErrorIs(t, err, ErrLimit)
	assert.LessOrEqual(t, len(output.Bytes()), 1<<20)
}
