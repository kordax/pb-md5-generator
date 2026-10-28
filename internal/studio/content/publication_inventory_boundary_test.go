package content

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func publicationInventoryFixture(t *testing.T) publicationRecoveryFixture {
	t.Helper()
	fixture := preparePublicationRecoveryFixture(t)
	require.NoError(t, fixture.transaction.rollback(t.Context()))
	require.NoError(t, fixture.transaction.cleanup())
	return fixture
}

func TestPublicationProvisionalStageRefusalPreservesCandidateAndOriginal(t *testing.T) {
	for _, failure := range []string{"readonly-state", "missing-owner", "foreign-owner"} {
		t.Run(failure, func(t *testing.T) {
			if failure == "readonly-state" && os.Geteuid() == 0 {
				t.Skip("root bypasses directory write permissions")
			}
			fixture := publicationInventoryFixture(t)
			require.NoError(t, os.Mkdir(fixture.stage, 0o700))
			require.NoError(t, os.Mkdir(filepath.Join(fixture.stage, "source"), 0o700))
			candidatePath := filepath.Join(fixture.stage, "source", "index.md")
			require.NoError(t, os.WriteFile(candidatePath, fixture.replacement, 0o600))
			ownerPath := filepath.Join(fixture.stage, ".storage-owner")
			owner := []byte(fixture.transaction.journal.ID)
			if failure == "foreign-owner" {
				owner = []byte("foreign owner")
			}
			if failure != "missing-owner" {
				require.NoError(t, os.WriteFile(ownerPath, owner, 0o600))
			}
			info, err := os.Stat(fixture.config.StateDir)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, os.Chmod(fixture.config.StateDir, info.Mode().Perm())) })
			if failure == "readonly-state" {
				require.NoError(t, os.Chmod(fixture.config.StateDir, 0o500))
			}
			err = fixture.transaction.service.checkLegacyPublications(t.Context())
			if failure == "readonly-state" {
				require.ErrorIs(t, err, os.ErrPermission)
			} else {
				require.ErrorIs(t, err, ErrOwnership)
			}
			retained, err := os.ReadFile(candidatePath)
			require.NoError(t, err)
			assert.Equal(t, fixture.replacement, retained)
			retainedOwner, err := os.ReadFile(ownerPath)
			if failure == "missing-owner" {
				assert.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.NoError(t, err)
				assert.Equal(t, owner, retainedOwner)
			}
			require.NoError(t, os.Chmod(fixture.config.StateDir, info.Mode().Perm()))
			require.NoError(t, os.WriteFile(ownerPath, []byte(fixture.transaction.journal.ID), 0o600))
			recovered, err := fixture.reopen(t)
			require.NoError(t, err)
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.original), file.Content)
			site, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, fixture.previousSite, site)
			draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
		})
	}
}

func TestPublicationRecoveryInventoriesRejectIncompleteObservation(t *testing.T) {
	for _, inventory := range []string{"state", "output-parent"} {
		for _, failure := range []string{"cancelled", "entry-limit", "unreadable", "missing"} {
			t.Run(inventory+"/"+failure, func(t *testing.T) {
				if failure == "unreadable" && os.Geteuid() == 0 {
					t.Skip("root bypasses directory read permissions")
				}
				fixture := publicationInventoryFixture(t)
				directory := fixture.config.StateDir
				check := fixture.transaction.service.checkLegacyPublications
				if inventory == "output-parent" {
					directory = filepath.Dir(fixture.config.OutputDir)
					check = fixture.transaction.service.checkOrphanOutputs
				}
				ctx := t.Context()
				var repair func()
				switch failure {
				case "cancelled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					repair = func() {}
				case "entry-limit":
					paths := make([]string, 0, MaxFiles+1)
					for i := range MaxFiles + 1 {
						path := filepath.Join(directory, fmt.Sprintf("inventory-bound-%05d", i))
						require.NoError(t, os.WriteFile(path, nil, 0o600))
						paths = append(paths, path)
					}
					repair = func() {
						for _, path := range paths {
							require.NoError(t, os.Remove(path))
						}
					}
				case "unreadable":
					info, err := os.Stat(directory)
					require.NoError(t, err)
					require.NoError(t, os.Chmod(directory, 0))
					repair = func() { require.NoError(t, os.Chmod(directory, info.Mode().Perm())) }
					t.Cleanup(repair)
				case "missing":
					parked := filepath.Join(t.TempDir(), "retained-inventory")
					require.NoError(t, os.Rename(directory, parked))
					repair = func() { require.NoError(t, os.Rename(parked, directory)) }
				}
				err := check(ctx)
				switch failure {
				case "cancelled":
					require.ErrorIs(t, err, context.Canceled)
				case "entry-limit":
					require.ErrorIs(t, err, ErrLimit)
				case "unreadable":
					require.ErrorIs(t, err, os.ErrPermission)
				case "missing":
					require.ErrorIs(t, err, os.ErrNotExist)
				}
				repair()
				recovered, err := fixture.reopen(t)
				require.NoError(t, err)
				file, err := recovered.Read(t.Context(), "index.md")
				require.NoError(t, err)
				assert.Equal(t, string(fixture.original), file.Content)
				site, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
				require.NoError(t, err)
				assert.Equal(t, fixture.previousSite, site)
				draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
				require.NoError(t, err)
				assert.Equal(t, fixture.draft.Revision, draft.Revision)
			})
		}
	}
}
