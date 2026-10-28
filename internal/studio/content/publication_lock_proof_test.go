package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationRefusesLostDirectoryLockWithoutChangingSavedWork(t *testing.T) {
	for _, location := range []string{"source", "state"} {
		for _, damage := range []string{"missing", "replacement"} {
			t.Run(location+"/"+damage, func(t *testing.T) {
				fixture := prepareUnjournaledPublicationFixture(t)
				require.NoError(t, removeStorageTree(fixture.config.StateDir, filepath.Base(fixture.stage)))
				directory := fixture.config.Root
				if location == "state" {
					directory = fixture.config.StateDir
				}
				lock := filepath.Join(directory, ".studio-lock")
				original, err := os.Stat(lock)
				require.NoError(t, err)
				retained := filepath.Join(t.TempDir(), "held-lock")
				require.NoError(t, os.Rename(lock, retained))
				repaired := false
				repair := func() {
					if repaired {
						return
					}
					if damage == "replacement" {
						require.NoError(t, os.Remove(lock))
					}
					require.NoError(t, os.Rename(retained, lock))
					repaired = true
				}
				t.Cleanup(repair)
				if damage == "replacement" {
					require.NoError(t, os.WriteFile(lock, nil, 0o600))
				}

				_, err = fixture.service.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
				require.ErrorIs(t, err, ErrOwnership)
				source, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
				require.NoError(t, err)
				assert.Equal(t, fixture.original, source)
				output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
				require.NoError(t, err)
				assert.Equal(t, fixture.previousSite, output)
				held, err := os.Stat(retained)
				require.NoError(t, err)
				assert.True(t, os.SameFile(original, held), "the held lock itself must not be replaced or removed during refusal")

				repair()
				draft, err := fixture.service.ReadDraft(t.Context(), fixture.draft.ID)
				require.NoError(t, err)
				assert.Equal(t, fixture.draft.Revision, draft.Revision)
				_, err = fixture.service.Publish(t.Context(), PublishInput{ID: fixture.draft.ID, Revision: fixture.draft.Revision}, editor)
				require.NoError(t, err)
				file, err := fixture.service.Read(t.Context(), "index.md")
				require.NoError(t, err)
				assert.Equal(t, string(fixture.replacement), file.Content)
			})
		}
	}
}
