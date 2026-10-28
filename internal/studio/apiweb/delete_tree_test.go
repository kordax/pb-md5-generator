package apiweb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDeletionFixture(t *testing.T, before func(context.Context, content.Actor, []content.Entry) error) *fixture {
	t.Helper()
	f := newFixture(t, nil)
	replaceContentForHTTPTest(t, f, content.Config{AuthorizeDeleteTree: func(ctx context.Context, actor content.Actor, entries []content.Entry) error {
		if before != nil {
			if err := before(ctx, actor, entries); err != nil {
				return err
			}
		}
		user, err := Principal(ctx)
		if err != nil || user == nil {
			return auth.ErrUnauthorized
		}
		if user.ID != actor.ID {
			return auth.ErrForbidden
		}
		for _, entry := range entries {
			if !permitted(user, auth.ActionRead, entry.Path) || !permitted(user, auth.ActionWrite, entry.Path) {
				return auth.ErrForbidden
			}
		}
		return nil
	}})
	return f
}

func deletionPlan(t *testing.T, f *fixture, name string, identity *credentials) deleteTreePlanDTO {
	t.Helper()
	current := f.current(t, identity)
	return resultJSON[deleteTreePlanDTO](t, f.request(http.MethodPost, testAPI+"/files/delete-preview", map[string]string{"path": name, "revision": current.Revision}, identity, nil), http.StatusOK)
}

func deletionAction(plan deleteTreePlanDTO) map[string]string {
	return map[string]string{"path": plan.Path, "revision": plan.Revision, "planRevision": plan.PlanRevision}
}

func TestRecursiveDeletionIsOneExplicitDraftEvenWhenAutoPublishIsEnabled(t *testing.T) {
	f := newDeletionFixture(t, nil)
	first := f.addDocument(t, "folder/one.md", "One", "# One")
	second := f.addDocument(t, "folder/nested/two.md", "Two", "# Two")
	base := f.current(t, &f.admin)
	asset := resultJSON[saveResult](t, sourceUpload(f, &f.admin, "folder/note.txt", base.Revision, []byte("notes"), nil), http.StatusOK)
	f.publish(t, asset.Draft, &f.admin)
	f.handler.(*handler).opts.Publishing = func(context.Context) (PublishingPolicy, error) { return PublishingPolicy{AutoPublish: true}, nil }
	plan := deletionPlan(t, f, "folder", &f.admin)
	assert.Equal(t, 3, plan.FileCount)
	assert.Equal(t, 2, plan.DirectoryCount)
	assert.Equal(t, 2, plan.DocumentCount)
	assert.EqualValues(t, len("# One")+len("# Two")+len("notes"), plan.TotalBytes)
	require.Len(t, plan.Entries, 5)
	assert.Equal(t, "folder", plan.Entries[0].Path)
	assert.True(t, plan.Entries[0].Directory)
	assert.False(t, plan.Truncated)
	assert.Len(t, plan.PlanRevision, 64)
	staged := resultJSON[saveResult](t, f.request(http.MethodPost, testAPI+"/files/delete-tree", deletionAction(plan), &f.admin, nil), http.StatusOK)
	require.NotNil(t, staged.Draft)
	assert.False(t, staged.Published)
	assert.Equal(t, "delete-tree", staged.Draft.Operation)
	assert.Equal(t, plan.Path, staged.Draft.Path)
	assert.Equal(t, plan.Revision, staged.Draft.BaseRevision)
	assert.Equal(t, http.StatusOK, f.request(http.MethodGet, testAPI+"/documents/"+first.ID, nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusOK, f.request(http.MethodGet, testAPI+"/documents/"+second.ID, nil, &f.admin, nil).Code)
	draft, err := f.content.ReadDraft(t.Context(), staged.Draft.ID)
	require.NoError(t, err)
	require.Len(t, changesOf(draft), 1)
	assert.Equal(t, plan.PlanRevision, changesOf(draft)[0].PlanRevision)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/files/delete-tree", deletionAction(plan), &f.admin, nil).Code)
	published := f.publish(t, staged.Draft, &f.admin)
	assert.True(t, published.Published)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, testAPI+"/documents/"+first.ID, nil, &f.admin, nil).Code)
	assert.Equal(t, http.StatusNotFound, f.request(http.MethodGet, sourceTarget("folder/note.txt", ""), nil, &f.admin, nil).Code)
	assert.Equal(t, "home", f.current(t, &f.admin).ID)
	assert.NoDirExists(t, filepath.Join(f.root, "folder"))
}

func TestRecursiveDeletionRejectsStaleProofAndCompetingDraftsWithoutMetadata(t *testing.T) {
	f := newDeletionFixture(t, nil)
	f.addDocument(t, "folder/page.md", "Published page", "# Published")
	plan := deletionPlan(t, f, "folder", &f.admin)
	changed := resultJSON[saveResult](t, f.request(http.MethodPut, testAPI+"/documents/home", map[string]any{"content": "# New project revision", "revision": plan.Revision, "publish": true}, &f.admin, nil), http.StatusOK)
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/files/delete-tree", deletionAction(plan), &f.admin, nil).Code)
	staleProof := deletionAction(plan)
	staleProof["revision"] = changed.Revision
	assert.Equal(t, http.StatusConflict, f.request(http.MethodPost, testAPI+"/files/delete-tree", staleProof, &f.admin, nil).Code)
	plan = deletionPlan(t, f, "folder", &f.admin)
	other := f.createUser(t, "competing-folder-editor", auth.RoleEditor, nil)
	competing := resultJSON[saveResult](t, sourceUpload(f, &other, "folder/page.md", plan.Revision, []byte("private competing draft content"), nil), http.StatusOK)
	require.NotNil(t, competing.Draft)
	for _, response := range []*httptest.ResponseRecorder{
		f.request(http.MethodPost, testAPI+"/files/delete-preview", map[string]string{"path": plan.Path, "revision": plan.Revision}, &f.admin, nil),
		f.request(http.MethodPost, testAPI+"/files/delete-tree", deletionAction(plan), &f.admin, nil),
	} {
		assert.Equal(t, http.StatusConflict, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), competing.Draft.ID)
		assert.NotContains(t, response.Body.String(), other.user.Username)
		assert.NotContains(t, response.Body.String(), "private competing draft content")
		assert.NotContains(t, response.Body.String(), "fileCount")
		assert.NotContains(t, response.Body.String(), "entries")
	}
	assertSourceAttachment(t, f.request(http.MethodGet, sourceTarget("folder/page.md", competing.Draft.ID), nil, &other, nil), []byte("private competing draft content"), competing.Draft.Revision)
}

func TestRecursiveDeletionChecksTheCompleteInventoryBeforeReturningTruncatedEntries(t *testing.T) {
	var deny atomic.Bool
	var seen atomic.Int64
	last := "bulk/file-100.txt"
	f := newDeletionFixture(t, func(_ context.Context, _ content.Actor, entries []content.Entry) error {
		seen.Store(int64(len(entries)))
		if deny.Load() {
			for _, entry := range entries {
				if entry.Path == last {
					return auth.ErrForbidden
				}
			}
		}
		return nil
	})
	require.NoError(t, os.Mkdir(filepath.Join(f.root, "bulk"), 0o750))
	for index := 0; index <= 100; index++ {
		require.NoError(t, os.WriteFile(filepath.Join(f.root, "bulk", fmt.Sprintf("file-%03d.txt", index)), []byte("x"), 0o600))
	}
	plan := deletionPlan(t, f, "bulk", &f.admin)
	assert.Equal(t, 101, plan.FileCount)
	assert.Equal(t, 1, plan.DirectoryCount)
	assert.EqualValues(t, 101, plan.TotalBytes)
	assert.True(t, plan.Truncated)
	require.Len(t, plan.Entries, 100)
	assert.Equal(t, "bulk", plan.Entries[0].Path)
	assert.EqualValues(t, 102, seen.Load())
	for _, entry := range plan.Entries {
		assert.NotEqual(t, last, entry.Path)
	}
	deny.Store(true)
	response := f.request(http.MethodPost, testAPI+"/files/delete-preview", map[string]string{"path": plan.Path, "revision": plan.Revision}, &f.admin, nil)
	assert.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), last)
	assert.NotContains(t, response.Body.String(), "fileCount")
	assert.NotContains(t, response.Body.String(), "entries")
	assert.DirExists(t, filepath.Join(f.root, "bulk"))
}

func TestRecursiveDeletionRequestRolesCSRFAndBounds(t *testing.T) {
	f := newDeletionFixture(t, nil)
	f.addDocument(t, "folder/page.md", "Page", "# Page")
	plan := deletionPlan(t, f, "folder", &f.admin)
	viewer := f.createUser(t, "delete-viewer", auth.RoleViewer, nil)
	readOnly := f.createUser(t, "delete-readonly", auth.RoleEditor, &auth.Grants{Read: []string{"folder"}, Write: []string{"elsewhere"}})
	hidden := f.createUser(t, "delete-hidden", auth.RoleEditor, &auth.Grants{Read: []string{"elsewhere"}, Write: []string{"."}})
	for _, operation := range []string{"delete-preview", "delete-tree"} {
		var body any = map[string]string{"path": plan.Path, "revision": plan.Revision}
		if operation == "delete-tree" {
			body = deletionAction(plan)
		}
		for _, account := range []struct {
			identity *credentials
			status   int
		}{{nil, http.StatusUnauthorized}, {&viewer, http.StatusForbidden}, {&readOnly, http.StatusForbidden}, {&hidden, http.StatusNotFound}} {
			response := f.request(http.MethodPost, testAPI+"/files/"+operation, body, account.identity, nil)
			assert.Equal(t, account.status, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), "entries")
			assert.NotContains(t, response.Body.String(), "fileCount")
		}
		assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/files/"+operation, body, &f.admin, map[string]string{"X-CSRF-Token": ""}).Code)
		assert.Equal(t, http.StatusForbidden, f.request(http.MethodPost, testAPI+"/files/"+operation, body, &f.admin, map[string]string{"Origin": ""}).Code)
		assert.Equal(t, http.StatusUnsupportedMediaType, f.request(http.MethodPost, testAPI+"/files/"+operation, body, &f.admin, map[string]string{"Content-Type": "text/plain"}).Code)
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/files/"+operation+"?unexpected=true", body, &f.admin, nil).Code)
		method := f.request(http.MethodGet, testAPI+"/files/"+operation, nil, &f.admin, nil)
		assert.Equal(t, http.StatusMethodNotAllowed, method.Code)
		assert.Equal(t, "POST", method.Header().Get("Allow"))
	}
	for _, body := range []string{`{"path":".","revision":"` + plan.Revision + `"}`, `{"path":"folder","revision":null}`, `{"path":"folder","Revision":"` + plan.Revision + `"}`, `{"path":"folder","revision":"` + plan.Revision + `","actor":"spoof"}`} {
		assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/files/delete-preview", []byte(body), &f.admin, nil).Code)
	}
	assert.Equal(t, http.StatusRequestEntityTooLarge, f.request(http.MethodPost, testAPI+"/files/delete-preview", []byte(`{"path":"`+strings.Repeat("x", maxDeleteTreeJSONBody)+`"}`), &f.admin, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.request(http.MethodPost, testAPI+"/files/delete-tree", map[string]any{"path": plan.Path, "revision": plan.Revision, "planRevision": plan.PlanRevision, "publish": true}, &f.admin, nil).Code)
}

func TestRecursiveDeletionRechecksRevokedSessionInsideTheContentGate(t *testing.T) {
	var armed atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	f := newDeletionFixture(t, func(ctx context.Context, _ content.Actor, _ []content.Entry) error {
		if armed.CompareAndSwap(true, false) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	f.addDocument(t, "folder/page.md", "Page", "# Page")
	plan := deletionPlan(t, f, "folder", &f.admin)
	armed.Store(true)
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		completed <- f.request(http.MethodPost, testAPI+"/files/delete-tree", deletionAction(plan), &f.admin, nil)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("deletion did not reach authorization inside the content gate")
	}
	require.Equal(t, http.StatusNoContent, f.request(http.MethodDelete, testAPI+"/session", nil, &f.admin, nil).Code)
	close(release)
	select {
	case response := <-completed:
		assert.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
		assert.NotContains(t, response.Body.String(), "entries")
		assert.NotContains(t, response.Body.String(), "planRevision")
	case <-time.After(5 * time.Second):
		t.Fatal("deletion did not finish after authorization was released")
	}
	f.admin = f.login(t, "owner", testPassword)
	assert.DirExists(t, filepath.Join(f.root, "folder"))
	drafts := resultJSON[struct {
		Drafts []draftDTO `json:"drafts"`
	}](t, f.request(http.MethodGet, testAPI+"/drafts", nil, &f.admin, nil), http.StatusOK)
	assert.Empty(t, drafts.Drafts)
}
