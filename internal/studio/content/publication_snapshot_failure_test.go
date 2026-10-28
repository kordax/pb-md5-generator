package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationSnapshotPermissionsNeverAcceptIncompleteImages(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("effective root bypasses the filesystem permission denial")
	}
	for _, scenario := range []struct {
		name, image string
		payload     bool
	}{
		{name: "source-directory", image: "source"},
		{name: "backup-directory", image: "backup"},
		{name: "source-payload", image: "source", payload: true},
		{name: "backup-payload", image: "backup", payload: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			evidence := capturePublicationOutputProof(t, fixture)
			draftBytes := readLifecycleDraftBytes(t, fixture)
			identities := make(map[string]os.FileInfo)
			for _, name := range []string{
				filepath.Join(fixture.config.Root, "index.md"),
				filepath.Join(fixture.config.OutputDir, "index.html"),
				fixture.stage,
				filepath.Join(fixture.stage, "source"),
				filepath.Join(fixture.stage, "source", "index.md"),
				filepath.Join(fixture.stage, "backup"),
				filepath.Join(fixture.stage, "backup", "index.md"),
			} {
				info, err := os.Stat(name)
				require.NoError(t, err)
				identities[name] = info
			}
			directory := filepath.Join(fixture.stage, scenario.image)
			denied := directory
			if scenario.payload {
				denied = filepath.Join(directory, "index.md")
			}
			mode := identities[denied].Mode().Perm()
			restored := false
			t.Cleanup(func() {
				if !restored {
					assert.NoError(t, os.Chmod(denied, mode))
				}
			})
			require.NoError(t, os.Chmod(denied, 0))

			if !scenario.payload {
				tree := fixture.after
				if scenario.image == "backup" {
					tree = fixture.before
				}
				require.ErrorIs(t, publicationSnapshotContents(t.Context(), directory, tree), os.ErrPermission)
			}
			before, after, err := fixture.transaction.snapshots(t.Context())
			require.ErrorIs(t, err, os.ErrPermission)
			assert.Empty(t, before.Revision, "a denied image must not produce an accepted digest")
			assert.Empty(t, after.Revision, "a denied image must not produce an accepted digest")

			require.NoError(t, os.Chmod(denied, mode))
			restored = true
			assertPublicationOutputProofUnchanged(t, fixture, evidence)
			assert.Equal(t, draftBytes, readLifecycleDraftBytes(t, fixture))
			for name, original := range identities {
				current, err := os.Stat(name)
				require.NoError(t, err)
				assert.True(t, os.SameFile(original, current), "helper replaced %s", name)
				assert.Equal(t, original.ModTime(), current.ModTime(), "helper rewrote %s", name)
				assert.Equal(t, original.Mode(), current.Mode(), "permissions were not restored for %s", name)
			}
			before, after, err = fixture.transaction.snapshots(t.Context())
			require.NoError(t, err)
			identity := fixture.transaction.journal.Branch + "\x00" + fixture.transaction.journal.Head
			assert.Equal(t, fixture.before.Revision, sourceTreeDigest(before, identity))
			assert.Equal(t, fixture.after.Revision, sourceTreeDigest(after, identity))

			reopened, err := fixture.reopen(t)
			require.NoError(t, err)
			assertLifecycleOriginalPublication(t, fixture)
			assertLifecycleDraftRecovered(t, fixture, reopened)
			assert.Equal(t, draftBytes, readLifecycleDraftBytes(t, fixture))
			assertNoLifecycleTransactionArtifacts(t, fixture)
		})
	}
}
