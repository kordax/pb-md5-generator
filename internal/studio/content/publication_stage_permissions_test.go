package content

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationStagePermissionFailuresRetainRecoveryEvidence(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires enforced directory read permissions for an unprivileged owner")
	}
	for _, test := range []struct {
		name string
		path func(publicationRecoveryFixture) string
	}{
		{"private source snapshot", func(fixture publicationRecoveryFixture) string { return filepath.Join(fixture.stage, "source") }},
		{"private backup snapshot", func(fixture publicationRecoveryFixture) string { return filepath.Join(fixture.stage, "backup") }},
		{"live history", func(fixture publicationRecoveryFixture) string {
			return filepath.Join(fixture.config.StateDir, "history")
		}},
		{"recorded next history", func(fixture publicationRecoveryFixture) string { return filepath.Join(fixture.stage, "history-next") }},
		{"recorded previous history", func(fixture publicationRecoveryFixture) string {
			return filepath.Join(fixture.stage, "history-previous")
		}},
		{"current output root", func(fixture publicationRecoveryFixture) string { return fixture.config.OutputDir }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			fixture.applySources(t)
			evidence := publicationOutputLimitEvidence(t, fixture)
			historyPath := filepath.Join(fixture.config.StateDir, "history")
			historyBefore, _, _, err := publicationHistoryFiles(t.Context(), historyPath, false)
			require.NoError(t, err)
			directory := test.path(fixture)
			info, err := os.Lstat(directory)
			require.NoError(t, err)
			require.True(t, info.IsDir(), "the inaccessible location must be a real recorded directory")
			mode := info.Mode().Perm()
			t.Cleanup(func() {
				if err := os.Chmod(directory, mode); err != nil && !os.IsNotExist(err) {
					assert.NoError(t, err)
				}
			})
			require.NoError(t, os.Chmod(directory, 0))
			probe, permissionErr := os.Open(directory)
			if probe != nil {
				require.NoError(t, probe.Close())
			}
			require.ErrorIs(t, permissionErr, os.ErrPermission, "the fault must reach an actual denied directory read")
			reopened, restartErr := fixture.reopen(t)
			// Restore access only for inspecting survivors, before any further
			// recovery attempt. No source, output, owner or journal is repaired.
			require.NoError(t, os.Chmod(directory, mode))
			require.ErrorIs(t, restartErr, os.ErrPermission, "startup must fail at the recorded filesystem access, not an unrelated owner check")
			require.Nil(t, reopened)
			assertPublicationOutputLimitEvidence(t, evidence)
			historyAfter, _, _, err := publicationHistoryFiles(t.Context(), historyPath, false)
			require.NoError(t, err)
			assert.Equal(t, historyBefore, historyAfter, "failed restart must preserve every immutable history payload and metadata byte")
			retained, err := fixture.service.readDraft(fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, retained.Revision)
			recovered, err := fixture.reopen(t)
			require.NoError(t, err, "repairing only the original permission bits must permit known recovery")
			project, err := recovered.Project(t.Context())
			require.NoError(t, err)
			assert.Equal(t, fixture.before.Revision, project.Revision)
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.original), file.Content)
			output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.True(t, bytes.Equal(fixture.previousSite, output))
			draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.ID, draft.ID)
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
			restoredHistory, _, _, err := publicationHistoryFiles(t.Context(), historyPath, false)
			require.NoError(t, err)
			assert.Equal(t, historyBefore, restoredHistory)
			_, err = os.Lstat(fixture.stage)
			assert.True(t, os.IsNotExist(err), "only verified recovery may remove the private stage")
		})
	}
}
