package content

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type publicationOutputProofEntry struct {
	Directory bool
	Size      int64
	Hash      [sha256.Size]byte
}

type publicationOutputProofEvidence struct {
	Source map[string]publicationOutputProofEntry
	Output map[string]publicationOutputProofEntry
	Stage  map[string]publicationOutputProofEntry
	Holder map[string]publicationOutputProofEntry
}

func snapshotPublicationOutputProof(t *testing.T, directory string) map[string]publicationOutputProofEntry {
	t.Helper()
	if _, err := os.Lstat(directory); os.IsNotExist(err) {
		return nil
	} else {
		require.NoError(t, err)
	}
	files := make(map[string]publicationOutputProofEntry)
	err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			files[relative] = publicationOutputProofEntry{Directory: true}
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected nonregular proof entry: %s", relative)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		files[relative] = publicationOutputProofEntry{Size: int64(len(data)), Hash: sha256.Sum256(data)}
		return nil
	})
	require.NoError(t, err)
	return files
}

func capturePublicationOutputProof(t *testing.T, fixture publicationRecoveryFixture) publicationOutputProofEvidence {
	t.Helper()
	return publicationOutputProofEvidence{
		Source: snapshotPublicationOutputProof(t, fixture.config.Root),
		Output: snapshotPublicationOutputProof(t, fixture.config.OutputDir),
		Stage:  snapshotPublicationOutputProof(t, fixture.stage),
		Holder: snapshotPublicationOutputProof(t, filepath.Join(fixture.transaction.outputParent(), fixture.transaction.outputHolder())),
	}
}

func assertPublicationOutputProofUnchanged(t *testing.T, fixture publicationRecoveryFixture, before publicationOutputProofEvidence) {
	t.Helper()
	after := capturePublicationOutputProof(t, fixture)
	assert.Equal(t, before.Source, after.Source, "refused recovery must not modify live sources")
	assert.Equal(t, before.Output, after.Output, "refused recovery must not modify live output")
	assert.Equal(t, before.Stage, after.Stage, "all private source/history evidence must survive refusal")
	assert.Equal(t, before.Holder, after.Holder, "the previous output and unknown holder entries must survive refusal")
	require.FileExists(t, filepath.Join(fixture.config.StateDir, publicationJournalName))
}

func TestPublicationOutputProofRestartRefusesUnknownEvidenceThenRecoversAfterRepair(t *testing.T) {
	for _, scenario := range []string{"holder-missing-owner", "holder-wrong-owner", "holder-extra-file", "previous-output-modified", "private-backup-ignored-file"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			expectedSource := snapshotPublicationOutputProof(t, fixture.config.Root)
			expectedOutput := snapshotPublicationOutputProof(t, fixture.config.OutputDir)
			fixture.applySources(t)
			afterOutput := scenario != "private-backup-ignored-file"
			if afterOutput {
				published, err := fixture.transaction.replaceOutput()
				require.NoError(t, err)
				require.True(t, published)
				expectedSource = snapshotPublicationOutputProof(t, fixture.config.Root)
				expectedOutput = snapshotPublicationOutputProof(t, fixture.config.OutputDir)
			}
			holder := filepath.Join(fixture.transaction.outputParent(), fixture.transaction.outputHolder())
			var repair func()
			switch scenario {
			case "holder-missing-owner", "holder-wrong-owner":
				owner := filepath.Join(holder, ".storage-owner")
				original, err := os.ReadFile(owner)
				require.NoError(t, err)
				require.NotEmpty(t, original)
				if scenario == "holder-missing-owner" {
					require.NoError(t, os.Remove(owner))
				} else {
					unknown := append([]byte(nil), original...)
					unknown[0] = '0'
					if original[0] == '0' {
						unknown[0] = '1'
					}
					require.NoError(t, os.WriteFile(owner, unknown, 0o600))
				}
				repair = func() { require.NoError(t, os.WriteFile(owner, original, 0o600)) }
			case "holder-extra-file":
				unknown := filepath.Join(holder, "operator-note.txt")
				require.NoError(t, os.WriteFile(unknown, []byte("Keep this unrelated operator file.\n"), 0o600))
				repair = func() { require.NoError(t, os.Remove(unknown)) }
			case "previous-output-modified":
				previous := filepath.Join(holder, "previous", "index.html")
				original, err := os.ReadFile(previous)
				require.NoError(t, err)
				unknown := append(append([]byte(nil), original...), []byte("\nAn external edit to the only old output copy.\n")...)
				require.NoError(t, os.WriteFile(previous, unknown, 0o600))
				repair = func() { require.NoError(t, os.WriteFile(previous, original, 0o600)) }
			case "private-backup-ignored-file":
				unknown := filepath.Join(fixture.stage, "backup", ".operator-note")
				require.NoError(t, os.WriteFile(unknown, []byte("An ignored file is still somebody's data.\n"), 0o600))
				repair = func() { require.NoError(t, os.Remove(unknown)) }
			}
			require.NotNil(t, repair)
			evidence := capturePublicationOutputProof(t, fixture)
			_, err := fixture.reopen(t)
			require.ErrorIs(t, err, ErrOwnership)
			assertPublicationOutputProofUnchanged(t, fixture, evidence)
			repair()
			reopened, err := fixture.reopen(t)
			require.NoError(t, err)
			assert.Equal(t, expectedSource, snapshotPublicationOutputProof(t, fixture.config.Root))
			assert.Equal(t, expectedOutput, snapshotPublicationOutputProof(t, fixture.config.OutputDir))
			current, err := reopened.Read(t.Context(), "index.md")
			require.NoError(t, err)
			if afterOutput {
				assert.Equal(t, string(fixture.replacement), current.Content)
			} else {
				assert.Equal(t, string(fixture.original), current.Content)
			}
			draft, err := reopened.ReadDraft(t.Context(), fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
			assert.Equal(t, fixture.draft.BaseRevision, draft.BaseRevision)
			assert.Equal(t, fixture.draft.Actor.ID, draft.Actor.ID)
			assert.NoDirExists(t, fixture.stage)
			assert.NoDirExists(t, holder)
			assert.NoFileExists(t, filepath.Join(fixture.config.StateDir, publicationJournalName))
		})
	}
}
