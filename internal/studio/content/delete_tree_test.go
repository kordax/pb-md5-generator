package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func allowDeleteTree(context.Context, Actor, []Entry) error { return nil }

func recursiveFixture(t *testing.T) (*Service, Config) {
	t.Helper()
	s, cfg := managedFixture(t)
	s.cfg.AuthorizeDeleteTree, cfg.AuthorizeDeleteTree = allowDeleteTree, allowDeleteTree
	project, err := s.Project(t.Context())
	require.NoError(t, err)
	chapter, code := "# Chapter\n", "const example = '<script>literal</script>';\n"
	draft, err := s.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Changes: []Change{
		{Operation: "write", Path: "guide/chapter.md", Title: "Chapter", DocumentID: "chapter", OutputPath: "guide/chapter.html", Content: &chapter},
		{Operation: "write", Path: "guide/examples/literal.js", Content: &code},
		{Operation: "write", Path: "guide/files/manual.pdf", Data: []byte("%PDF-1.7\nexample\n")},
		{Operation: "mkdir", Path: "guide/empty"},
	}}, editor)
	require.NoError(t, err)
	publishDraft(t, s, draft)
	return s, cfg
}

func recursivePlan(t *testing.T, s *Service, folder string) DeleteTreePlan {
	t.Helper()
	project, err := s.Project(t.Context())
	require.NoError(t, err)
	plan, err := s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: folder, Revision: project.Revision}, editor)
	require.NoError(t, err)
	return plan
}

func stageRecursive(t *testing.T, s *Service, plan DeleteTreePlan) Draft {
	t.Helper()
	draft, err := s.SaveDraft(t.Context(), DraftInput{BaseRevision: plan.Revision, Change: Change{Operation: "delete-tree", Path: plan.Path, PlanRevision: plan.PlanRevision}}, editor)
	require.NoError(t, err)
	return draft
}

func TestRecursiveDeletionStagesThenPublishesManifestNavigationAndRecoverableHistory(t *testing.T) {
	s, cfg := recursiveFixture(t)
	installContentNavigation(t, s, cfg, &document.NavigationTree{UnlistedPages: "omit", Items: []document.NavigationNode{
		{Kind: "group", ID: "guides", Title: "Guides", Children: []document.NavigationNode{{Kind: "page", PageID: "chapter"}}},
		{Kind: "page", PageID: "home"},
	}})
	untouched := saveText(t, s, "outside.txt", "Unpublished outside edit\n")
	payloadName := "drafts/" + untouched.ID + "-" + untouched.Revision + ".json"
	payload, err := readSource(cfg.StateDir, payloadName, MaxBinaryBytes)
	require.NoError(t, err)
	before, err := readSource(cfg.Root, "guide/chapter.md", MaxTextBytes)
	require.NoError(t, err)
	plan := recursivePlan(t, s, "guide")
	assert.Equal(t, 3, plan.FileCount)
	assert.Equal(t, 4, plan.DirectoryCount)
	assert.Equal(t, 1, plan.DocumentCount)
	assert.False(t, plan.Truncated)
	require.Len(t, plan.Entries, plan.FileCount+plan.DirectoryCount)
	assert.Equal(t, DeleteTreeEntry{Path: "guide", Directory: true}, plan.Entries[0])
	draft := stageRecursive(t, s, plan)
	assert.Equal(t, "delete-tree", draft.Change.Operation)
	assert.Empty(t, draft.Changes)
	saved, err := s.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, saved)
	stillPublished, err := readSource(cfg.Root, "guide/chapter.md", MaxTextBytes)
	require.NoError(t, err)
	assert.Equal(t, before, stillPublished)
	result := publishDraft(t, s, draft)
	assert.Empty(t, result.Sync.Error)
	_, err = os.Lstat(filepath.Join(cfg.Root, "guide"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	manifest, err := loadManifest(cfg.Root)
	require.NoError(t, err)
	require.Len(t, manifest.Pages, 1)
	require.NotNil(t, manifest.Appearance().Navigation.Tree)
	require.Len(t, manifest.Appearance().Navigation.Tree.Items, 1)
	assert.Equal(t, "home", manifest.Appearance().Navigation.Tree.Items[0].PageID)
	assert.Empty(t, manifest.Assets)
	afterPayload, err := readSource(cfg.StateDir, payloadName, MaxBinaryBytes)
	require.NoError(t, err)
	assert.Equal(t, payload, afterPayload)
	for _, name := range []string{"guide/chapter.md", "guide/examples/literal.js", "guide/files/manual.pdf", "docs.json"} {
		history, err := s.History(t.Context(), name)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(history), 2)
		if name != "docs.json" {
			assert.True(t, history[0].Deleted)
		}
		if name == "guide/chapter.md" {
			assert.Equal(t, "chapter", history[0].DocumentID)
			assert.Equal(t, "Chapter", history[0].Title)
		}
	}
	history, err := s.History(t.Context(), "guide/chapter.md")
	require.NoError(t, err)
	var version Revision
	for _, revision := range history {
		if !revision.Deleted {
			version = revision
			break
		}
	}
	require.NotEmpty(t, version.ID)
	require.NoError(t, s.Close())
	reopened, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	project, err := reopened.Project(t.Context())
	require.NoError(t, err)
	restore, err := reopened.Restore(t.Context(), "guide/chapter.md", version.ID, project.Revision, editor)
	require.NoError(t, err)
	assert.Equal(t, "chapter", restore.Change.DocumentID)
	publishDraft(t, reopened, restore)
	recovered, err := readSource(cfg.Root, "guide/chapter.md", MaxTextBytes)
	require.NoError(t, err)
	assert.Equal(t, before, recovered)
	_, err = readSource(cfg.Root, "guide/examples/literal.js", MaxTextBytes)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRecursiveDeletionConflictsPreserveEveryCompetingDraft(t *testing.T) {
	for _, operation := range []string{"write", "move-in", "move-out", "ancestor", "self"} {
		t.Run(operation, func(t *testing.T) {
			s, cfg := recursiveFixture(t)
			plan := recursivePlan(t, s, "guide")
			text := "Retain this unpublished edit\n"
			change := Change{Operation: "write", Path: "guide/new.md", Content: &text}
			actor := Actor{ID: "other"}
			switch operation {
			case "move-in":
				change = Change{Operation: "move", Path: "outside.txt", To: "guide/incoming.txt"}
			case "move-out":
				change = Change{Operation: "move", Path: "guide/chapter.md", To: "elsewhere.md"}
			case "ancestor":
				change = Change{Operation: "rmdir", Path: "guide"}
			case "self":
				actor = editor
			}
			competing, err := s.SaveDraft(t.Context(), DraftInput{BaseRevision: plan.Revision, Change: change}, actor)
			require.NoError(t, err)
			metadataName := "drafts/" + competing.ID + ".meta.json"
			payloadName := "drafts/" + competing.ID + "-" + competing.Revision + ".json"
			metadata, err := readSource(cfg.StateDir, metadataName, MaxBinaryBytes)
			require.NoError(t, err)
			payload, err := readSource(cfg.StateDir, payloadName, MaxBinaryBytes)
			require.NoError(t, err)
			denied, err := s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: "guide", Revision: plan.Revision}, editor)
			assert.ErrorIs(t, err, ErrConflict)
			assert.Empty(t, denied.Entries)
			_, err = s.SaveDraft(t.Context(), DraftInput{BaseRevision: plan.Revision, Change: Change{Operation: "delete-tree", Path: "guide", PlanRevision: plan.PlanRevision}}, editor)
			assert.ErrorIs(t, err, ErrConflict)
			_, err = s.SaveDraft(t.Context(), DraftInput{ID: competing.ID, Revision: competing.Revision, BaseRevision: plan.Revision, Change: Change{Operation: "delete-tree", Path: "guide", PlanRevision: plan.PlanRevision}}, actor)
			assert.ErrorIs(t, err, ErrConflict)
			actualMetadata, err := readSource(cfg.StateDir, metadataName, MaxBinaryBytes)
			require.NoError(t, err)
			actualPayload, err := readSource(cfg.StateDir, payloadName, MaxBinaryBytes)
			require.NoError(t, err)
			assert.Equal(t, metadata, actualMetadata)
			assert.Equal(t, payload, actualPayload)
		})
	}
	t.Run("added after staging", func(t *testing.T) {
		s, cfg := recursiveFixture(t)
		plan := recursivePlan(t, s, "guide")
		draft := stageRecursive(t, s, plan)
		competing := saveText(t, s, "guide/unpublished.md", "Keep me\n")
		_, err := s.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
		assert.ErrorIs(t, err, ErrConflict)
		for _, expected := range []Draft{draft, competing} {
			actual, err := s.ReadDraft(t.Context(), expected.ID)
			require.NoError(t, err)
			assert.Equal(t, expected, actual)
		}
		require.NoError(t, s.DiscardDraft(t.Context(), competing.ID, competing.Revision, editor))
		publishDraft(t, s, draft)
		_, err = os.Lstat(filepath.Join(cfg.Root, "guide"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestRecursiveDeletionRejectsForgedMixedStaleAndUnmanagedPlans(t *testing.T) {
	s, cfg := recursiveFixture(t)
	plan := recursivePlan(t, s, "guide")
	text := "No changes\n"
	for _, changes := range [][]Change{
		{{Operation: "delete-tree", Path: "guide", PlanRevision: plan.PlanRevision}, {Operation: "write", Path: "other.txt", Content: &text}},
		{{Operation: "delete-tree", Path: "guide", PlanRevision: "invalid"}},
		{{Operation: "delete-tree", Path: "guide", PlanRevision: plan.PlanRevision, Content: &text}},
		{{Operation: "delete-tree", Path: "guide", PlanRevision: plan.PlanRevision, Title: "wrong"}},
		{{Operation: "write", Path: "other.txt", PlanRevision: plan.PlanRevision, Content: &text}},
	} {
		_, err := s.SaveDraft(t.Context(), DraftInput{BaseRevision: plan.Revision, Changes: changes}, editor)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	_, err := s.SaveDraft(t.Context(), DraftInput{BaseRevision: plan.Revision, Change: Change{Operation: "delete-tree", Path: "guide", PlanRevision: strings.Repeat("0", 64)}}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	other, err := s.SaveDraft(t.Context(), DraftInput{BaseRevision: plan.Revision, Change: Change{Operation: "delete-tree", Path: "guide", PlanRevision: plan.PlanRevision}}, Actor{ID: "other"})
	assert.ErrorIs(t, err, ErrConflict)
	assert.Empty(t, other.ID)
	publishDraft(t, s, saveText(t, s, "outside.txt", "Published change\n"))
	_, err = s.SaveDraft(t.Context(), DraftInput{BaseRevision: plan.Revision, Change: Change{Operation: "delete-tree", Path: "guide", PlanRevision: plan.PlanRevision}}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	for _, name := range []string{"guide/unknown.exe", "guide/.hidden/never-read.txt"} {
		require.NoError(t, writeSource(cfg.Root, name, []byte("Unmanaged bytes\n")))
		current, err := s.Project(t.Context())
		require.NoError(t, err)
		denied, err := s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: "guide", Revision: current.Revision}, editor)
		assert.ErrorIs(t, err, ErrConflict)
		assert.Empty(t, denied.Entries)
		assert.NotContains(t, err.Error(), name)
		preserved, err := readSource(cfg.Root, name, MaxTextBytes)
		require.NoError(t, err)
		assert.Equal(t, "Unmanaged bytes\n", string(preserved))
		require.NoError(t, removeSource(cfg.Root, name))
		if strings.Contains(name, ".hidden") {
			require.NoError(t, os.Remove(filepath.Join(cfg.Root, "guide/.hidden")))
		}
	}
	project, err := s.Project(t.Context())
	require.NoError(t, err)
	for _, name := range []string{".", "docs.json", "../guide", "guide/chapter.md", "missing"} {
		_, err := s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: name, Revision: project.Revision}, editor)
		assert.Error(t, err)
	}
	s.cfg.AuthorizeDeleteTree = nil
	_, err = s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: "guide", Revision: project.Revision}, editor)
	assert.ErrorIs(t, err, ErrUnsupported)
}

func TestRecursiveDeletionPreviewAndPublicationBoundaries(t *testing.T) {
	s, cfg := managedFixture(t)
	s.cfg.AuthorizeDeleteTree = allowDeleteTree
	for i := 0; i < MaxDeleteTreeFiles; i++ {
		require.NoError(t, writeSource(cfg.Root, fmt.Sprintf("many/file-%03d.txt", i), []byte(fmt.Sprintf("record %d\n", i))))
	}
	plan := recursivePlan(t, s, "many")
	assert.Equal(t, MaxDeleteTreeFiles, plan.FileCount)
	assert.Equal(t, 1, plan.DirectoryCount)
	require.Len(t, plan.Entries, MaxDeleteTreePreviewEntries)
	assert.True(t, plan.Truncated)
	assert.Equal(t, "many", plan.Entries[0].Path)
	draft := stageRecursive(t, s, plan)
	publishDraft(t, s, draft)
	history, err := s.revisions()
	require.NoError(t, err)
	assert.Equal(t, MaxHistory, len(history))
	for i := 0; i < MaxDeleteTreeFiles; i++ {
		versions, err := s.History(t.Context(), fmt.Sprintf("many/file-%03d.txt", i))
		require.NoError(t, err)
		require.Len(t, versions, 2)
		assert.True(t, versions[0].Deleted)
		payload, err := readSource(cfg.StateDir, "history/"+versions[1].ID+".bin", MaxTextBytes)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("record %d\n", i), string(payload))
	}
	for i := 0; i <= MaxDeleteTreeFiles; i++ {
		require.NoError(t, writeSource(cfg.Root, fmt.Sprintf("over/file-%03d.txt", i), []byte("keep")))
	}
	project, err := s.Project(t.Context())
	require.NoError(t, err)
	denied, err := s.PlanDeleteTree(t.Context(), DeleteTreeInput{Path: "over", Revision: project.Revision}, editor)
	assert.ErrorIs(t, err, ErrLimit)
	assert.Empty(t, denied.Entries)
}

func TestRecursiveDeletionFreshAuthorizationAndValidationFailureRetainSources(t *testing.T) {
	s, cfg := recursiveFixture(t)
	plan := recursivePlan(t, s, "guide")
	draft := stageRecursive(t, s, plan)
	denied := errors.New("access revoked")
	s.cfg.AuthorizeDeleteTree = func(context.Context, Actor, []Entry) error { return denied }
	_, err := s.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, denied)
	s.cfg.AuthorizeDeleteTree = allowDeleteTree
	s.cfg.Validate = func(context.Context, string, []Change, Actor) error {
		return fmt.Errorf("%w: private-consumer.md", ErrInvalid)
	}
	_, err = s.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.NotContains(t, err.Error(), "private-consumer")
	actual, err := s.ReadDraft(t.Context(), draft.ID)
	require.NoError(t, err)
	assert.Equal(t, draft, actual)
	_, err = readSource(cfg.Root, "guide/chapter.md", MaxTextBytes)
	require.NoError(t, err)
	s.cfg.Validate = nil
	publishDraft(t, s, draft)
}

func TestLiteralCodeSourcesStayUTF8AndNeverBecomeImplicitOrExplicitAssets(t *testing.T) {
	s, cfg := managedFixture(t)
	text := "<script>alert('literal')</script>\n"
	for _, ext := range []string{"go", "js", "ts", "sh", "sql", "css", "xml"} {
		name := "examples/example." + ext
		draft := saveText(t, s, name, text)
		publishDraft(t, s, draft)
		file, err := s.Read(t.Context(), name)
		require.NoError(t, err)
		assert.False(t, file.Binary)
		assert.Equal(t, text, file.Content)
	}
	manifest, err := loadManifest(cfg.Root)
	require.NoError(t, err)
	assert.Empty(t, manifest.Assets)
	for _, name := range []string{"examples/invalid.js", "examples/invalid.css"} {
		project, err := s.Project(t.Context())
		require.NoError(t, err)
		_, err = s.SaveDraft(t.Context(), DraftInput{BaseRevision: project.Revision, Change: Change{Operation: "write", Path: name, Data: []byte{0xff}}}, editor)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	manifest.Assets = []string{"examples/example.js"}
	encoded, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, writeSource(cfg.Root, "docs.json", encoded))
	_, err = s.Project(t.Context())
	assert.ErrorIs(t, err, ErrInvalid)
}
