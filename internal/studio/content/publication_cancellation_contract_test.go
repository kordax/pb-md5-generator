package content

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationCancelledTransitionsPreserveRecoveryEvidence(t *testing.T) {
	operations := []struct {
		name string
		run  func(context.Context, publicationRecoveryFixture) error
	}{
		{name: "history-preflight", run: func(ctx context.Context, f publicationRecoveryFixture) error {
			return f.transaction.installHistory(ctx)
		}},
		{name: "snapshots", run: func(ctx context.Context, f publicationRecoveryFixture) error {
			_, _, err := f.transaction.snapshots(ctx)
			return err
		}},
		{name: "source-sync", run: func(ctx context.Context, f publicationRecoveryFixture) error { return f.transaction.syncSources(ctx) }},
		{name: "before-verification", run: func(ctx context.Context, f publicationRecoveryFixture) error { return f.transaction.verifyBefore(ctx) }},
		{name: "rollback", run: func(ctx context.Context, f publicationRecoveryFixture) error { return f.transaction.rollback(ctx) }},
		{name: "source-inventory", run: func(ctx context.Context, f publicationRecoveryFixture) error {
			return publicationSnapshotContents(ctx, filepath.Join(f.stage, "source"), f.after)
		}},
		{name: "output-inventory", run: func(ctx context.Context, f publicationRecoveryFixture) error {
			_, err := publicationOutputDigest(ctx, f.config.OutputDir, true)
			return err
		}},
		{name: "stage-verification", run: func(ctx context.Context, f publicationRecoveryFixture) error {
			_, err := f.transaction.checkStage(ctx)
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			journalPath := filepath.Join(fixture.config.StateDir, publicationJournalName)
			journal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			planPath := filepath.Join(fixture.stage, publicationHistoryPlanName)
			plan, err := os.ReadFile(planPath)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err = operation.run(ctx, fixture)
			require.ErrorIs(t, err, context.Canceled)
			files := []struct {
				path string
				data []byte
			}{
				{path: filepath.Join(fixture.config.Root, "index.md"), data: fixture.original},
				{path: filepath.Join(fixture.stage, "backup", "index.md"), data: fixture.original},
				{path: filepath.Join(fixture.stage, "source", "index.md"), data: fixture.replacement},
				{path: filepath.Join(fixture.config.OutputDir, "index.html"), data: fixture.previousSite},
				{path: journalPath, data: journal},
				{path: planPath, data: plan},
			}
			for _, file := range files {
				retained, err := os.ReadFile(file.path)
				require.NoError(t, err)
				assert.Equal(t, file.data, retained, "a cancelled transition must retain its authority and original images")
			}
			recovered, err := fixture.reopen(t)
			require.NoError(t, err)
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.original), file.Content)
			draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
		})
	}
}

func TestPublicationCancelledHistoryFinalizationResumesInstalledPublication(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	installed, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, installed)
	require.NoError(t, fixture.transaction.markPublished())
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	before, err := os.ReadDir(filepath.Join(fixture.config.StateDir, "history"))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, fixture.transaction.moveDeltaHistory(ctx), context.Canceled)
	after, err := os.ReadDir(filepath.Join(fixture.config.StateDir, "history"))
	require.NoError(t, err)
	beforeNames, afterNames := []string{}, []string{}
	for _, entry := range before {
		beforeNames = append(beforeNames, entry.Name())
	}
	for _, entry := range after {
		afterNames = append(afterNames, entry.Name())
	}
	assert.Equal(t, beforeNames, afterNames)
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), file.Content)
	currentOutput, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, output, currentOutput)
	_, err = os.Stat(fixture.stage)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
