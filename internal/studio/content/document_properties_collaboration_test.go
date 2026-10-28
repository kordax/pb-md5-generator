package content

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentPropertiesRebaseKeepsOtherAuthorsOrdinaryDocumentDrafts(t *testing.T) {
	service, config, original := documentPropertiesFixture(t)
	input := documentPropertiesInput(t, service, "guide", "# Alice local text\n")
	aliceText, bobText := input.Content, "# Bob local text\n"
	alice, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: input.Revision, Change: Change{Operation: "write", Path: "guide.md", DocumentID: "guide", Title: input.Title, Kind: input.Kind, OutputPath: input.OutputPath, Content: &aliceText}}, editor)
	require.NoError(t, err)
	bobActor := Actor{ID: "bob", Name: "Bob", Email: "bob@example.test"}
	bob, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: input.Revision, Change: Change{Operation: "write", Path: "guide.md", DocumentID: "guide", Title: input.Title, Kind: input.Kind, OutputPath: input.OutputPath, Content: &bobText}}, bobActor)
	require.NoError(t, err)
	thirdActor := Actor{ID: "carol", Name: "Carol", Email: "carol@example.test"}
	third, err := service.SaveDraft(context.Background(), DraftInput{BaseRevision: input.Revision, Change: Change{Operation: "write", Path: "guide.md", DocumentID: "guide", Title: "Third published title", Kind: "markdown", OutputPath: "reviewed/third.html", Content: &original}}, thirdActor)
	require.NoError(t, err)
	thirdPublication, err := service.Publish(context.Background(), PublishInput{ID: third.ID, Revision: third.Revision}, thirdActor)
	require.NoError(t, err)
	require.True(t, thirdPublication.Published)
	current, err := service.Project(context.Background())
	require.NoError(t, err)
	input.Rebase, input.Revision, input.PropertiesRevision = true, current.Revision, current.Revision
	input.DraftID, input.DraftRevision = bob.ID, bob.Revision
	_, err = service.SaveDocumentProperties(context.Background(), input, editor)
	assert.ErrorIs(t, err, ErrNotFound, "a different author must never adopt the other draft")
	input.DraftID, input.DraftRevision = alice.ID, alice.Revision
	input.Content = "# Alice explicitly merged text\n"
	saved, err := service.SaveDocumentProperties(context.Background(), input, editor)
	require.NoError(t, err, "another author's text-only write is not a new identity or URL reservation")
	assert.Equal(t, alice.ID, saved.ID)
	assert.Equal(t, current.Revision, saved.BaseRevision)
	retained, err := service.ReadDraft(context.Background(), bob.ID)
	require.NoError(t, err)
	assert.Equal(t, bob, retained)
	body, err := os.ReadFile(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	assert.Equal(t, original, string(body))
	publishDraft(t, service, saved)
	_, err = service.Publish(context.Background(), PublishInput{ID: bob.ID, Revision: bob.Revision}, bobActor)
	assert.ErrorIs(t, err, ErrConflict)
	input = documentPropertiesInput(t, service, "guide", "# Bob explicitly merged text\n")
	input.Rebase, input.DraftID, input.DraftRevision = true, bob.ID, bob.Revision
	saved, err = service.SaveDocumentProperties(context.Background(), input, bobActor)
	require.NoError(t, err)
	assert.Equal(t, bob.ID, saved.ID)
	publish, err := service.Publish(context.Background(), PublishInput{ID: saved.ID, Revision: saved.Revision}, bobActor)
	require.NoError(t, err)
	assert.True(t, publish.Published)
	body, err = os.ReadFile(filepath.Join(config.Root, "guide.md"))
	require.NoError(t, err)
	assert.Equal(t, input.Content, string(body))
}
