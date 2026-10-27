package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewIsIsolatedSerializedAndAlwaysRemoved(t *testing.T) {
	service, config := managedFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(config.Root, ".env"), []byte("private-value"), 0o600))
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request-principal")
	entered := make(chan string, 1)
	release := make(chan struct{})
	result := make(chan error, 1)
	callbackError := errors.New("render refused")
	go func() {
		result <- service.View(ctx, func(viewContext context.Context, root, manifest string) error {
			assert.Equal(t, "request-principal", viewContext.Value(contextKey{}))
			assert.Equal(t, filepath.Join(root, "docs.json"), manifest)
			assert.NotEqual(t, config.Root, root)
			_, err := os.Stat(filepath.Join(root, ".env"))
			assert.ErrorIs(t, err, os.ErrNotExist)
			assert.NoError(t, os.WriteFile(filepath.Join(root, "index.md"), []byte("isolated change"), 0o600))
			entered <- root
			<-release
			return callbackError
		})
	}()
	snapshot := <-entered
	waiting, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	_, err := service.Project(waiting)
	cancel()
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	require.ErrorIs(t, <-result, callbackError)
	_, err = os.Stat(snapshot)
	assert.ErrorIs(t, err, os.ErrNotExist)
	original, err := os.ReadFile(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Documentation\n", string(original))
	output, err := os.ReadDir(config.OutputDir)
	require.NoError(t, err)
	assert.Empty(t, output)
	require.ErrorIs(t, service.View(ctx, nil), ErrInvalid)
	canceled, stop := context.WithCancel(ctx)
	stop()
	called := false
	err = service.View(canceled, func(context.Context, string, string) error { called = true; return nil })
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, called)
}

func TestRebasedNewDocumentCannotReplaceAnotherDocumentIdentity(t *testing.T) {
	service, config := managedFixture(t)
	ctx := context.Background()
	project, err := service.Project(ctx)
	require.NoError(t, err)
	first, second := "# First document\n", "# Second document\n"
	changeA := Change{Operation: "write", Path: "guides/new.md", Title: "First", Kind: "markdown", DocumentID: "doc-a", Content: &first}
	changeB := Change{Operation: "write", Path: "guides/new.md", Title: "Second", Kind: "markdown", DocumentID: "doc-b", Content: &second}
	draftA, err := service.SaveDraft(ctx, DraftInput{BaseRevision: project.Revision, Change: changeA}, editor)
	require.NoError(t, err)
	bob := Actor{ID: "bob", Name: "Bob Example", Email: "bob@example.test"}
	draftB, err := service.SaveDraft(ctx, DraftInput{BaseRevision: project.Revision, Change: changeB}, bob)
	require.NoError(t, err)
	published, err := service.Publish(ctx, PublishInput{ID: draftA.ID, Revision: draftA.Revision}, editor)
	require.NoError(t, err)
	require.True(t, published.Published)
	rebased, err := service.SaveDraft(ctx, DraftInput{ID: draftB.ID, Revision: draftB.Revision, BaseRevision: published.Revision, Change: changeB}, bob)
	require.NoError(t, err)
	_, err = service.Publish(ctx, PublishInput{ID: rebased.ID, Revision: rebased.Revision}, bob)
	require.ErrorIs(t, err, ErrConflict)
	file, err := service.Read(ctx, "guides/new.md")
	require.NoError(t, err)
	assert.Equal(t, first, file.Content)
	assert.Equal(t, "doc-a", file.ID)
	retained, err := service.ReadDraft(ctx, rebased.ID)
	require.NoError(t, err)
	require.NotNil(t, retained.Change.Content)
	assert.Equal(t, second, *retained.Change.Content)
	output, err := os.ReadFile(filepath.Join(config.OutputDir, "guides", "new.html"))
	require.NoError(t, err)
	assert.Contains(t, string(output), "First document")
	assert.NotContains(t, string(output), "Second document")
}
