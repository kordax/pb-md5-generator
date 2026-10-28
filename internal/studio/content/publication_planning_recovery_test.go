package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareUnjournaledPublicationFixture(t *testing.T) publicationRecoveryFixture {
	t.Helper()
	service, config := managedFixture(t)
	original := []byte("# Published source before planning\r\n")
	replacement := []byte("# Unpublished candidate\n")
	publishDraft(t, service, saveText(t, service, "index.md", string(original)))
	draft := saveText(t, service, "index.md", string(replacement))
	output, err := os.ReadFile(filepath.Join(config.OutputDir, "index.html"))
	require.NoError(t, err)
	stage, id, err := createStorageStage(config.StateDir, publicationTransactionPrefix)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(stage, "source"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(stage, "source", "index.md"), replacement, 0o600))
	return publicationRecoveryFixture{service: service, config: config, draft: draft, stage: stage, id: id, original: original, replacement: replacement, previousSite: output}
}

func assertUnpublishedPlanningRecovery(t *testing.T, fixture publicationRecoveryFixture, recovered *Service) {
	t.Helper()
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.original), file.Content)
	draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.draft.Revision, draft.Revision)
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, output)
	_, err = os.Lstat(fixture.stage)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPublicationPlanningRestartCleansOnlyOwnedUnjournaledCandidates(t *testing.T) {
	fixture := prepareUnjournaledPublicationFixture(t)
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	assertUnpublishedPlanningRecovery(t, fixture, recovered)
}

func TestPublicationPlanningRestartRetainsUnprovenDataUntilOperatorRepair(t *testing.T) {
	for _, damage := range []string{"missing-owner", "wrong-owner", "backup-without-journal", "orphan-output", "orphan-output-without-stage", "symbolic-link"} {
		t.Run(damage, func(t *testing.T) {
			fixture := prepareUnjournaledPublicationFixture(t)
			owner := filepath.Join(fixture.stage, ".storage-owner")
			var repair func()
			retainedPath := ""
			retainedContent := []byte(nil)
			switch damage {
			case "missing-owner", "wrong-owner":
				if damage == "missing-owner" {
					require.NoError(t, os.Remove(owner))
				} else {
					require.NoError(t, os.WriteFile(owner, []byte("unrecognized owner"), 0o600))
				}
				repair = func() { require.NoError(t, os.WriteFile(owner, []byte(fixture.id), 0o600)) }
			case "backup-without-journal":
				backup := filepath.Join(fixture.stage, "backup")
				require.NoError(t, os.Mkdir(backup, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(backup, "index.md"), fixture.original, 0o600))
				quarantine := filepath.Join(fixture.config.StateDir, "operator-retained-backup-"+fixture.id)
				retainedPath = filepath.Join(quarantine, "index.md")
				retainedContent = fixture.original
				repair = func() { require.NoError(t, os.Rename(backup, quarantine)) }
			case "orphan-output", "orphan-output-without-stage":
				if damage == "orphan-output-without-stage" {
					require.NoError(t, removeStorageTree(fixture.config.StateDir, filepath.Base(fixture.stage)))
				}
				parent := filepath.Dir(fixture.config.OutputDir)
				holder := filepath.Join(parent, publicationOutputPrefix(fixture.config.OutputDir)+fixture.id)
				require.NoError(t, os.Mkdir(holder, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(holder, "retained.html"), fixture.previousSite, 0o600))
				quarantine := filepath.Join(parent, "operator-retained-output-"+fixture.id)
				retainedPath = filepath.Join(quarantine, "retained.html")
				retainedContent = fixture.previousSite
				repair = func() { require.NoError(t, os.Rename(holder, quarantine)) }
			case "symbolic-link":
				quarantine := filepath.Join(fixture.config.StateDir, "operator-retained-stage-"+fixture.id)
				require.NoError(t, os.Rename(fixture.stage, quarantine))
				require.NoError(t, os.Symlink(quarantine, fixture.stage))
				repair = func() {
					require.NoError(t, os.Remove(fixture.stage))
					require.NoError(t, os.Rename(quarantine, fixture.stage))
				}
			}

			_, err := fixture.reopen(t)
			require.Error(t, err)
			candidate, err := os.ReadFile(filepath.Join(fixture.stage, "source", "index.md"))
			if damage == "orphan-output-without-stage" {
				assert.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.NoError(t, err)
				assert.Equal(t, fixture.replacement, candidate)
			}
			source, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
			require.NoError(t, err)
			assert.Equal(t, fixture.original, source)
			output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, fixture.previousSite, output)
			if damage == "symbolic-link" {
				_, err := os.Readlink(fixture.stage)
				assert.NoError(t, err, "startup must preserve the link instead of following it for deletion")
			}
			if damage == "backup-without-journal" {
				backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "index.md"))
				require.NoError(t, err)
				assert.Equal(t, fixture.original, backup)
			}

			// The operator keeps any surviving original outside the private
			// candidate instead of deleting it to bypass startup checks.
			require.NotNil(t, repair)
			repair()
			recovered, err := New(fixture.config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, recovered.Close()) })
			assertUnpublishedPlanningRecovery(t, fixture, recovered)
			if retainedPath != "" {
				retained, err := os.ReadFile(retainedPath)
				require.NoError(t, err)
				assert.Equal(t, retainedContent, retained, "explicitly quarantined originals must remain available")
			}
		})
	}
}
