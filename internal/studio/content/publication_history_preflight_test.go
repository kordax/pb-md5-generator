package content

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationRecoveryDoesNotTreatMissingPlanAsCompletedCleanup(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	installed, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, installed)
	planPath := filepath.Join(fixture.stage, publicationHistoryPlanName)
	plan, err := os.ReadFile(planPath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(planPath))
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)

	_, err = fixture.reopen(t)
	require.Error(t, err)
	current, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.replacement, current)
	retained, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, output, retained)
	backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
	require.NoError(t, err)
	assert.Equal(t, fixture.original, backup)
	previous, err := os.ReadFile(filepath.Join(fixture.transaction.outputParent(), fixture.transaction.outputHolder(), "previous", "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, previous)
	_, err = os.Stat(planPath)
	assert.ErrorIs(t, err, os.ErrNotExist, "startup must not invent the missing authority proof")

	require.NoError(t, os.WriteFile(planPath, plan, 0o600))
	recovered, err := New(fixture.config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, recovered.Close()) })
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.replacement), file.Content)
	_, err = os.Stat(fixture.stage)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPublicationHistoryPreflightPreservesSourcesDraftAndDamagedRecords(t *testing.T) {
	for _, damage := range []string{"metadata", "missing-payload", "orphan-payload"} {
		t.Run(damage, func(t *testing.T) {
			fixture := prepareUnjournaledPublicationFixture(t)
			require.NoError(t, removeStorageTree(fixture.config.StateDir, filepath.Base(fixture.stage)))
			history := filepath.Join(fixture.config.StateDir, "history")
			entries, err := os.ReadDir(history)
			require.NoError(t, err)
			var revision Revision
			metadataPath := ""
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(history, entry.Name()))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &revision))
				if !revision.Deleted {
					metadataPath = filepath.Join(history, entry.Name())
					break
				}
			}
			require.NotEmpty(t, metadataPath)
			var repair func()
			var damagedPath string
			var expected []byte
			retainedPath := ""
			switch damage {
			case "metadata":
				original, err := os.ReadFile(metadataPath)
				require.NoError(t, err)
				damagedPath, expected = metadataPath, []byte("{\n")
				require.NoError(t, os.WriteFile(damagedPath, expected, 0o600))
				repair = func() { require.NoError(t, os.WriteFile(metadataPath, original, 0o600)) }
			case "missing-payload":
				payload := filepath.Join(history, revision.ID+".bin")
				original, err := os.ReadFile(payload)
				require.NoError(t, err)
				damagedPath = filepath.Join(fixture.config.StateDir, "operator-retained-"+revision.ID+".bin")
				expected = original
				require.NoError(t, os.Rename(payload, damagedPath))
				repair = func() { require.NoError(t, os.Rename(damagedPath, payload)) }
			case "orphan-payload":
				id, err := randomID()
				require.NoError(t, err)
				damagedPath = filepath.Join(history, id+".bin")
				expected = []byte("Independent history bytes without a metadata record")
				require.NoError(t, os.WriteFile(damagedPath, expected, 0o600))
				retainedPath = filepath.Join(fixture.config.StateDir, "operator-retained-"+id+".bin")
				repair = func() { require.NoError(t, os.Rename(damagedPath, retainedPath)) }
			}

			_, err = fixture.service.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
			require.Error(t, err)
			file, err := fixture.service.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.original), file.Content)
			draft, err := fixture.service.ReadDraft(t.Context(), fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
			output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, fixture.previousSite, output)
			retained, err := os.ReadFile(damagedPath)
			require.NoError(t, err)
			assert.Equal(t, expected, retained, "failed history preflight must preserve the independent evidence")

			require.NotNil(t, repair)
			repair()
			_, err = fixture.service.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
			require.NoError(t, err)
			file, err = fixture.service.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.replacement), file.Content)
			if retainedPath != "" {
				retained, err := os.ReadFile(retainedPath)
				require.NoError(t, err)
				assert.Equal(t, expected, retained)
			}
		})
	}
}
