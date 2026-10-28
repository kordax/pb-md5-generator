package content

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type publicationRecoveryFixture struct {
	service      *Service
	config       Config
	draft        Draft
	transaction  *publicationTransaction
	stage        string
	id           string
	before       sourceTree
	after        sourceTree
	paths        []string
	original     []byte
	replacement  []byte
	previousSite []byte
}

func preparePublicationRecoveryFixture(t *testing.T) publicationRecoveryFixture {
	t.Helper()
	return prepareConfiguredPublicationRecoveryFixture(t, nil)
}

func prepareConfiguredPublicationRecoveryFixture(t *testing.T, configure func(*Service, *Config)) publicationRecoveryFixture {
	t.Helper()
	return preparePublicationDraftRecoveryFixture(t, configure, nil)
}

func preparePublicationDraftRecoveryFixture(t *testing.T, configure func(*Service, *Config), prepareDraft func(*Service) Draft) publicationRecoveryFixture {
	t.Helper()
	service, config := managedFixture(t)
	if configure != nil {
		configure(service, &config)
	}
	original := []byte("# Original recovery source\r\n\r\nKeep exact original bytes: π.\r\n")
	replacement := []byte("# Replacement recovery source\n\nA complete replacement publication.\n")
	publishDraft(t, service, saveText(t, service, "index.md", string(original)))
	var draft Draft
	if prepareDraft == nil {
		draft = saveText(t, service, "index.md", string(replacement))
	} else {
		draft = prepareDraft(service)
	}
	previousSite, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
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
	transaction, err := service.preparePublication(t.Context(), stage, id, publicationPlan{Before: before, After: after, Paths: paths, Draft: draft, Input: PublishInput{ID: draft.ID, Revision: draft.Revision}, Actor: editor, Status: status})
	require.NoError(t, err)
	require.NotNil(t, transaction)
	require.NoError(t, transaction.installHistory(t.Context()))
	return publicationRecoveryFixture{service: service, config: config, draft: draft, transaction: transaction, stage: stage, id: id, before: before, after: after, paths: paths, original: original, replacement: replacement, previousSite: previousSite}
}

func (fixture publicationRecoveryFixture) applySources(t *testing.T) {
	t.Helper()
	require.NoError(t, applyFiles(t.Context(), fixture.config.Root, filepath.Join(fixture.stage, "source"), fixture.paths, fixture.after))
	require.NoError(t, removeEmptyDirectories(fixture.config.Root, fixture.before.Dirs, fixture.after.Dirs))
}

func (fixture publicationRecoveryFixture) reopen(t *testing.T) (*Service, error) {
	t.Helper()
	require.NoError(t, fixture.service.Close())
	service, err := New(fixture.config)
	if err == nil {
		t.Cleanup(func() { require.NoError(t, service.Close()) })
	}
	return service, err
}

func TestOrdinaryPublicationRecoveryRestoresAppliedSourcesBeforeOutputSwap(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.original), file.Content)
	project, err := recovered.Project(t.Context())
	require.NoError(t, err)
	assert.Equal(t, fixture.before.Revision, project.Revision)
	published, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, published)
	saved, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.draft.Revision, saved.Revision)
	_, err = os.Stat(fixture.stage)
	assert.True(t, os.IsNotExist(err), "known rollback should clean only its completed transaction")
}

func TestOrdinaryPublicationRecoveryKeepsExactInstalledPublication(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	published, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, published)
	installed, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	require.NotEqual(t, fixture.previousSite, installed)
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), file.Content)
	currentSite, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, installed, currentSite, "the fully installed publication must not be replaced on restart")
	_, err = os.Stat(fixture.stage)
	assert.True(t, os.IsNotExist(err))
}

func TestOrdinaryPublicationRecoveryRefusesExternalSourceAndRetainsEvidence(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	external := []byte("# Independent external edit\n\nThis is neither known publication version.\n")
	require.NoError(t, os.WriteFile(filepath.Join(fixture.config.Root, "index.md"), external, 0o600))
	_, err := fixture.reopen(t)
	require.Error(t, err)
	current, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, external, current)
	backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.original, backup)
	staged, err := os.ReadFile(filepath.Join(fixture.stage, "source", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.replacement, staged)
	published, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, published)
	_, err = os.Stat(fixture.stage)
	assert.NoError(t, err)
}

func TestOrdinaryPublicationRecoveryDoesNotMaskMissingOutput(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	published, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, published)
	// This is the recognized interruption between retaining the new output
	// for rollback and restoring the recorded previous publication.
	holder := filepath.Join(fixture.transaction.outputParent(), fixture.transaction.outputHolder())
	require.NoError(t, os.Rename(fixture.config.OutputDir, filepath.Join(holder, "after")))
	_, err = os.Stat(fixture.config.OutputDir)
	require.True(t, os.IsNotExist(err))
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.original), file.Content)
	currentSite, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, currentSite)
}

func TestOrdinaryPublicationPendingBlocksPublicOperations(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	_, err := fixture.service.Project(t.Context())
	assert.ErrorIs(t, err, ErrConflict)
	_, err = fixture.service.Read(t.Context(), "index.md")
	assert.ErrorIs(t, err, ErrConflict)
	visited := false
	err = fixture.service.ReadView(t.Context(), func(context.Context, ReadSnapshot) error { visited = true; return nil })
	assert.ErrorIs(t, err, ErrConflict)
	assert.False(t, visited)
	unsaved := "must not be staged"
	_, err = fixture.service.SaveDraft(t.Context(), DraftInput{BaseRevision: fixture.before.Revision, Change: Change{Operation: "write", Path: "index.md", Content: &unsaved}}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	_, err = fixture.service.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
	assert.ErrorIs(t, err, ErrConflict)
	require.NoError(t, fixture.transaction.rollback(t.Context()))
	require.NoError(t, fixture.transaction.cleanup())
	_, err = fixture.service.Project(t.Context())
	assert.NoError(t, err, "only verified rollback cleanup should reopen the service")
}

func TestOrdinaryPublicationRecoveryRefusesUnknownOutputWithoutTouchingSources(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	external := []byte("<html><body>Independent external publication</body></html>")
	require.NoError(t, os.WriteFile(filepath.Join(fixture.config.OutputDir, "index.html"), external, 0o600))
	_, err := fixture.reopen(t)
	require.Error(t, err)
	current, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, external, current)
	source, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.replacement, source, "unknown output must stop recovery before source mutations")
	backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.original, backup)
	_, err = os.Stat(fixture.stage)
	assert.NoError(t, err)
}

func TestOrdinaryPublicationRecoveryNeverRemovesUnmarkedLegacyBackup(t *testing.T) {
	service, config := managedFixture(t)
	legacy := filepath.Join(config.StateDir, ".txn-legacy-unmarked", "backup")
	require.NoError(t, os.MkdirAll(legacy, 0o700))
	original := []byte("Unmarked legacy recovery evidence")
	name := filepath.Join(legacy, "index.md")
	require.NoError(t, os.WriteFile(name, original, 0o600))
	require.NoError(t, service.Close())
	reopened, err := New(config)
	if err == nil {
		t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	}
	retained, err := os.ReadFile(name)
	require.NoError(t, err)
	assert.Equal(t, original, retained, "a prefix alone never authorizes legacy transaction deletion")
}
