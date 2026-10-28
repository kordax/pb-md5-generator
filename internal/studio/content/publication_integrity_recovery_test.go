package content

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationRecoveryRejectsDamagedJournalAndPreservesAllCopies(t *testing.T) {
	for _, damage := range []string{"malformed-json", "unknown-field", "trailing-record", "foreign-root", "invalid-digest", "missing-actor", "repeated-path", "missing-history-proof"} {
		t.Run(damage, func(t *testing.T) {
			fixture := preparePublicationRecoveryFixture(t)
			fixture.applySources(t)
			journalPath := filepath.Join(fixture.config.StateDir, publicationJournalName)
			original, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			var journal map[string]any
			require.NoError(t, json.Unmarshal(original, &journal))
			switch damage {
			case "unknown-field":
				journal["unexpectedAuthority"] = true
			case "foreign-root":
				journal["root"] = t.TempDir()
			case "invalid-digest":
				journal["after"] = "invalid digest"
			case "missing-actor":
				delete(journal, "actor")
			case "repeated-path":
				paths, ok := journal["paths"].([]any)
				require.True(t, ok)
				require.NotEmpty(t, paths)
				journal["paths"] = append(paths, paths[0])
			case "missing-history-proof":
				journal["historyDelta"] = false
				delete(journal, "historyPlan")
			}
			damaged, err := json.Marshal(journal)
			require.NoError(t, err)
			switch damage {
			case "malformed-json":
				damaged = []byte("{\n")
			case "trailing-record":
				damaged = append(damaged, []byte("\n{}")...)
			}
			require.NoError(t, os.WriteFile(journalPath, damaged, 0o600))

			_, err = fixture.reopen(t)
			require.Error(t, err)
			files := []struct {
				path    string
				content []byte
			}{
				{path: filepath.Join(fixture.config.Root, "index.md"), content: fixture.replacement},
				{path: filepath.Join(fixture.stage, "backup", "index.md"), content: fixture.original},
				{path: filepath.Join(fixture.stage, "source", "index.md"), content: fixture.replacement},
				{path: filepath.Join(fixture.config.OutputDir, "index.html"), content: fixture.previousSite},
				{path: journalPath, content: damaged},
			}
			for _, file := range files {
				retained, err := os.ReadFile(file.path)
				require.NoError(t, err)
				assert.Equal(t, file.content, retained, "invalid authority must not change or remove any recovery copy")
			}

			// A later startup can recover after the operator restores the exact
			// original proof; a failed startup must release its directory locks.
			require.NoError(t, os.WriteFile(journalPath, original, 0o600))
			recovered, err := New(fixture.config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, recovered.Close()) })
			file, err := recovered.Read(t.Context(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, string(fixture.original), file.Content)
			draft, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
			require.NoError(t, err)
			assert.Equal(t, fixture.draft.Revision, draft.Revision)
			_, err = os.Stat(fixture.stage)
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestPublicationRecoveryRestoresOutputBetweenForwardRenames(t *testing.T) {
	fixture := preparePublicationRecoveryFixture(t)
	fixture.applySources(t)
	installed, err := fixture.transaction.replaceOutput()
	require.NoError(t, err)
	require.True(t, installed)
	// Retain both known output images in exactly the locations left after
	// moving the previous output aside but before installing the candidate.
	require.NoError(t, os.Rename(fixture.config.OutputDir, filepath.Join(fixture.stage, "site")))
	_, err = os.Stat(fixture.config.OutputDir)
	require.ErrorIs(t, err, os.ErrNotExist)
	recovered, err := fixture.reopen(t)
	require.NoError(t, err)
	file, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.original), file.Content)
	output, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, output)
	_, err = os.Stat(fixture.stage)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(fixture.transaction.outputParent(), fixture.transaction.outputHolder()))
	assert.ErrorIs(t, err, os.ErrNotExist)
}
