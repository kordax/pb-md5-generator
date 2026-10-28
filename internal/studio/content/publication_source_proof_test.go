package content

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type publicationSourceProofFault struct {
	modified []string
	verify   func(*testing.T)
	repair   func(*testing.T)
}

func sourceProofAddedFile(t *testing.T, name string) publicationSourceProofFault {
	t.Helper()
	_, err := os.Lstat(name)
	require.True(t, os.IsNotExist(err))
	data := []byte("# Independent unplanned managed file\n")
	require.NoError(t, os.WriteFile(name, data, 0o600))
	info, err := os.Lstat(name)
	require.NoError(t, err)
	return publicationSourceProofFault{
		verify: func(t *testing.T) {
			current, err := os.Lstat(name)
			require.NoError(t, err)
			require.True(t, current.Mode().IsRegular())
			assert.True(t, os.SameFile(info, current))
			assert.Equal(t, info.ModTime(), current.ModTime())
			retained, err := os.ReadFile(name)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(data, retained), "startup must preserve the unplanned file")
		},
		repair: func(t *testing.T) { require.NoError(t, os.Remove(name)) },
	}
}

func sourceProofAddedDirectory(t *testing.T, name string) publicationSourceProofFault {
	t.Helper()
	require.NoError(t, os.Mkdir(name, 0o700))
	info, err := os.Lstat(name)
	require.NoError(t, err)
	return publicationSourceProofFault{
		verify: func(t *testing.T) {
			current, err := os.Lstat(name)
			require.NoError(t, err)
			require.True(t, current.IsDir())
			assert.True(t, os.SameFile(info, current))
			assert.Equal(t, info.ModTime(), current.ModTime())
			entries, err := os.ReadDir(name)
			require.NoError(t, err)
			assert.Empty(t, entries)
		},
		repair: func(t *testing.T) { require.NoError(t, os.Remove(name)) },
	}
}

func sourceProofMissingFile(t *testing.T, name string) publicationSourceProofFault {
	t.Helper()
	original, err := os.ReadFile(name)
	require.NoError(t, err)
	info, err := os.Lstat(name)
	require.NoError(t, err)
	require.NoError(t, os.Remove(name))
	return publicationSourceProofFault{
		modified: []string{name},
		verify: func(t *testing.T) {
			_, err := os.Lstat(name)
			assert.True(t, os.IsNotExist(err), "startup must not initialize or restore an unexpectedly missing shared source")
		},
		repair: func(t *testing.T) { require.NoError(t, os.WriteFile(name, original, info.Mode().Perm())) },
	}
}

func sourceProofMissingDirectory(t *testing.T, name string) publicationSourceProofFault {
	t.Helper()
	info, err := os.Lstat(name)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	entries, err := os.ReadDir(name)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, os.Remove(name))
	return publicationSourceProofFault{
		verify: func(t *testing.T) {
			_, err := os.Lstat(name)
			assert.True(t, os.IsNotExist(err), "startup must not invent a missing shared directory")
		},
		repair: func(t *testing.T) { require.NoError(t, os.Mkdir(name, info.Mode().Perm())) },
	}
}

func sourceProofCorruptedBackup(t *testing.T, name string) publicationSourceProofFault {
	t.Helper()
	original, err := os.ReadFile(name)
	require.NoError(t, err)
	info, err := os.Lstat(name)
	require.NoError(t, err)
	corrupt := []byte("# Independent original backup modification\n")
	require.False(t, bytes.Equal(original, corrupt))
	require.NoError(t, os.WriteFile(name, corrupt, info.Mode().Perm()))
	return publicationSourceProofFault{
		modified: []string{name},
		verify: func(t *testing.T) {
			current, err := os.Lstat(name)
			require.NoError(t, err)
			require.True(t, current.Mode().IsRegular())
			assert.True(t, os.SameFile(info, current))
			retained, err := os.ReadFile(name)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(corrupt, retained), "startup must retain the ambiguous backup for operator review")
		},
		repair: func(t *testing.T) { require.NoError(t, os.WriteFile(name, original, info.Mode().Perm())) },
	}
}

func TestPublicationSourceProofRecovery(t *testing.T) {
	const sharedDirectory = "source-proof-shared-empty"
	for _, test := range []struct {
		name   string
		inject func(*testing.T, publicationRecoveryFixture) publicationSourceProofFault
	}{
		{"unexpected managed file", func(t *testing.T, fixture publicationRecoveryFixture) publicationSourceProofFault {
			return sourceProofAddedFile(t, filepath.Join(fixture.config.Root, "source-proof-external.md"))
		}},
		{"unexpected empty directory", func(t *testing.T, fixture publicationRecoveryFixture) publicationSourceProofFault {
			return sourceProofAddedDirectory(t, filepath.Join(fixture.config.Root, "source-proof-external-empty"))
		}},
		{"missing shared manifest", func(t *testing.T, fixture publicationRecoveryFixture) publicationSourceProofFault {
			return sourceProofMissingFile(t, filepath.Join(fixture.config.Root, "docs.json"))
		}},
		{"missing shared empty directory", func(t *testing.T, fixture publicationRecoveryFixture) publicationSourceProofFault {
			return sourceProofMissingDirectory(t, filepath.Join(fixture.config.Root, sharedDirectory))
		}},
		{"unplanned canonical backup file", func(t *testing.T, fixture publicationRecoveryFixture) publicationSourceProofFault {
			return sourceProofAddedFile(t, filepath.Join(fixture.stage, "backup", "source-proof-unplanned.md"))
		}},
		{"corrupted original backup bytes", func(t *testing.T, fixture publicationRecoveryFixture) publicationSourceProofFault {
			return sourceProofCorruptedBackup(t, filepath.Join(fixture.stage, "backup", "index.md"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareConfiguredPublicationRecoveryFixture(t, func(_ *Service, config *Config) {
				require.NoError(t, os.Mkdir(filepath.Join(config.Root, sharedDirectory), 0o700))
			})
			require.Contains(t, fixture.before.Dirs, sharedDirectory)
			require.Contains(t, fixture.after.Dirs, sharedDirectory)
			fixture.applySources(t)
			evidence := publicationOutputLimitEvidence(t, fixture)
			fault := test.inject(t, fixture)
			for _, name := range fault.modified {
				delete(evidence, name)
			}
			reopened, err := fixture.reopen(t)
			require.Error(t, err)
			require.Nil(t, reopened)
			// All unaffected live sources, private snapshots, output and journal
			// must remain exact; rejection must precede any source restoration.
			assertPublicationOutputLimitEvidence(t, evidence)
			fault.verify(t)
			// Repair only this external fault, preserving original permissions.
			// No history, journal, other source or output is edited to force success.
			fault.repair(t)
			recovered, err := fixture.reopen(t)
			require.NoError(t, err)
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
			_, err = os.Lstat(fixture.stage)
			assert.True(t, os.IsNotExist(err))
		})
	}
}
