package content

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareEmptyOutputPublication(t *testing.T) publicationRecoveryFixture {
	t.Helper()
	service, config := managedFixture(t)
	entries, err := os.ReadDir(config.OutputDir)
	require.NoError(t, err)
	require.Empty(t, entries, "the configured previous output must really be empty before preparation")
	original, err := os.ReadFile(filepath.Join(config.Root, "index.md"))
	require.NoError(t, err)
	replacement := []byte("# First publication after empty output\n\nPreserve this exact candidate.\n")
	draft := saveText(t, service, "index.md", string(replacement))
	stage, id, err := createStorageStage(config.StateDir, publicationTransactionPrefix)
	require.NoError(t, err)
	before, after, err := service.candidate(t.Context(), stage, draftChanges(draft), editor, true)
	require.NoError(t, err)
	paths := changedFiles(before, after)
	backup := filepath.Join(stage, "backup")
	require.NoError(t, os.Mkdir(backup, 0o700))
	require.NoError(t, applyFiles(t.Context(), backup, config.Root, paths, before))
	status, err := service.cfg.Storage.Status(t.Context())
	require.NoError(t, err)
	transaction, err := service.preparePublication(t.Context(), stage, id, publicationPlan{
		Before: before, After: after, Paths: paths, Draft: draft,
		Input: PublishInput{ID: draft.ID, Revision: draft.Revision}, Actor: editor, Status: status,
	})
	require.NoError(t, err)
	require.NoError(t, transaction.installHistory(t.Context()))
	return publicationRecoveryFixture{service: service, config: config, draft: draft, transaction: transaction,
		stage: stage, id: id, before: before, after: after, paths: paths, original: original, replacement: replacement}
}

func TestPublicationEmptyOutputPublicPublishInstallsFirstSite(t *testing.T) {
	service, config := managedFixture(t)
	entries, err := os.ReadDir(config.OutputDir)
	require.NoError(t, err)
	require.Empty(t, entries)
	const source = "# First public empty-output publication\n\nOwned source and output.\n"
	draft := saveText(t, service, "index.md", source)
	published, err := service.Publish(t.Context(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
	require.NoError(t, err)
	require.True(t, published.Published)
	file, err := service.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, source, file.Content)
	assert.Equal(t, published.Revision, file.Revision)
	output, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Contains(t, string(output), "First public empty-output publication")
	digest, err := publicationOutputDigest(t.Context(), config.OutputDir, false)
	require.NoError(t, err)
	_, err = service.ReadDraft(t.Context(), draft.ID)
	assert.ErrorIs(t, err, ErrNotFound, "successful public publication consumes its exact owned draft")
	require.NoError(t, service.Close())
	reopened, err := New(config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	current, err := publicationOutputDigest(t.Context(), config.OutputDir, false)
	require.NoError(t, err)
	assert.Equal(t, digest, current, "restart must preserve the exact installed first site")
}

func TestPublicationEmptyOutputInterruptedSwapRestoresKnownEmptyDirectory(t *testing.T) {
	fixture := prepareEmptyOutputPublication(t)
	emptyInfo, err := os.Lstat(fixture.config.OutputDir)
	require.NoError(t, err)
	fixture.applySources(t)
	transaction := fixture.transaction
	holder := filepath.Join(transaction.outputParent(), transaction.outputHolder())
	require.NoError(t, os.Mkdir(holder, 0o700))
	require.NoError(t, writeStorageBytes(transaction.outputParent(), transaction.outputHolder()+"/.storage-owner", []byte(fixture.id)))
	require.NoError(t, syncStorageDirectory(transaction.outputParent(), "."))
	// This is the real replaceOutput interruption after retaining the old
	// directory, before renaming the compiled candidate into the output path.
	moved, err := renamePublication(fixture.config.OutputDir, filepath.Join(holder, "previous"))
	require.NoError(t, err)
	require.True(t, moved)
	_, err = os.Lstat(fixture.config.OutputDir)
	require.True(t, os.IsNotExist(err))
	entries, err := os.ReadDir(filepath.Join(holder, "previous"))
	require.NoError(t, err)
	require.Empty(t, entries)
	backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(fixture.original, backup))
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	current, err := os.Lstat(fixture.config.OutputDir)
	require.NoError(t, err)
	assert.True(t, current.IsDir())
	assert.True(t, os.SameFile(emptyInfo, current), "recovery must restore the recorded empty directory, not invent a replacement")
	entries, err = os.ReadDir(fixture.config.OutputDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
	project, err := recovered.Project(t.Context())
	require.NoError(t, err)
	assert.Equal(t, fixture.before.Revision, project.Revision)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.original), file.Content)
	draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.draft.Revision, draft.Revision)
	for _, name := range []string{fixture.stage, holder} {
		_, err := os.Lstat(name)
		assert.True(t, os.IsNotExist(err))
	}
}

func TestPublicationEmptyOutputCompletedInstallationKeepsExactAfter(t *testing.T) {
	fixture := prepareEmptyOutputPublication(t)
	fixture.applySources(t)
	published, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, published)
	holder := filepath.Join(fixture.transaction.outputParent(), fixture.transaction.outputHolder())
	previous, err := os.ReadDir(filepath.Join(holder, "previous"))
	require.NoError(t, err)
	require.Empty(t, previous)
	expected, err := publicationOutputDigest(t.Context(), fixture.config.OutputDir, false)
	require.NoError(t, err)
	assert.Equal(t, fixture.transaction.journal.OutputAfter, expected)
	// A crash after both actual output renames, before the publication receipt,
	// is recognized from the exact candidate source, history and output proofs.
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	project, err := recovered.Project(t.Context())
	require.NoError(t, err)
	assert.Equal(t, fixture.after.Revision, project.Revision)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), file.Content)
	actual, err := publicationOutputDigest(t.Context(), fixture.config.OutputDir, false)
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
	draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.draft.Revision, draft.Revision)
	for _, name := range []string{fixture.stage, holder} {
		_, err := os.Lstat(name)
		assert.True(t, os.IsNotExist(err))
	}
}
