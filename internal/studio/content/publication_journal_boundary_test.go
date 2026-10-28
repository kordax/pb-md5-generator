package content

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationJournalBoundaryRefusalPreservesRecoveryCopies(t *testing.T) {
	for _, fault := range []string{"missing private stage owner", "foreign private stage owner", "future journal format"} {
		t.Run(fault, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			fixture.applySources(t)
			evidence := publicationOutputLimitEvidence(t, fixture)
			target := filepath.Join(fixture.stage, ".storage-owner")
			if fault == "future journal format" {
				target = filepath.Join(fixture.config.StateDir, publicationJournalName)
			}
			original, err := os.ReadFile(target)
			require.NoError(t, err)
			info, err := os.Lstat(target)
			require.NoError(t, err)
			var damaged []byte
			switch fault {
			case "missing private stage owner":
				require.NoError(t, os.Remove(target))
			case "foreign private stage owner":
				require.Equal(t, fixture.id, string(original))
				damaged = bytes.Clone(original)
				require.NotEmpty(t, damaged)
				if damaged[0] == '0' {
					damaged[0] = '1'
				} else {
					damaged[0] = '0'
				}
				require.NoError(t, os.WriteFile(target, damaged, info.Mode().Perm()))
			case "future journal format":
				var journal map[string]any
				require.NoError(t, json.Unmarshal(original, &journal))
				format, ok := journal["format"].(string)
				require.True(t, ok)
				journal["format"] = format + "-future"
				damaged, err = json.Marshal(journal)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(target, damaged, info.Mode().Perm()))
			}
			delete(evidence, target)
			reopened, err := fixture.reopen(t)
			require.Error(t, err)
			require.Nil(t, reopened)
			assertPublicationOutputLimitEvidence(t, evidence)
			if fault == "missing private stage owner" {
				_, err := os.Lstat(target)
				assert.True(t, os.IsNotExist(err), "startup must not grant ownership by recreating a missing marker")
			} else {
				retained, err := os.ReadFile(target)
				require.NoError(t, err)
				assert.True(t, bytes.Equal(damaged, retained), "startup must retain untrusted authority evidence without rewriting it")
			}
			retainedDraft, err := fixture.service.readDraft(fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, retainedDraft.Revision)
			// Only the operator's exact saved authority proof is restored. All
			// source, output, snapshot and history files remain untouched here.
			require.NoError(t, os.WriteFile(target, original, info.Mode().Perm()))
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
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
			_, err = os.Lstat(fixture.stage)
			assert.True(t, os.IsNotExist(err))
		})
	}
}
