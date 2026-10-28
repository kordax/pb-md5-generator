package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const publicationMovedContent = "# Nested document\r\n\r\nPreserve original folder bytes: π.\r\n"

func prepareDirectoryPublicationRecoveryFixture(t *testing.T) publicationRecoveryFixture {
	t.Helper()
	return preparePublicationDraftRecoveryFixture(t, nil, func(service *Service) Draft {
		publishDraft(t, service, saveText(t, service, "guides/nested/chapter.md", publicationMovedContent))
		project, err := service.Project(t.Context())
		require.NoError(t, err)
		replacement := "# Replacement recovery source\n\nA complete replacement publication.\n"
		draft, err := service.SaveDraft(t.Context(), DraftInput{
			BaseRevision: project.Revision,
			Changes: []Change{
				{Operation: "move", Path: "guides", To: "published"},
				{Operation: "write", Path: "index.md", Content: &replacement},
			},
		}, editor)
		require.NoError(t, err)
		return draft
	})
}

func assertRecoveredPublicationDirectory(t *testing.T, fixture publicationRecoveryFixture, recovered *Service) {
	t.Helper()
	file, err := recovered.Read(t.Context(), "guides/nested/chapter.md")
	require.NoError(t, err)
	assert.Equal(t, publicationMovedContent, file.Content)
	index, err := recovered.Read(t.Context(), "index.md")
	require.NoError(t, err)
	assert.Equal(t, string(fixture.original), index.Content)
	project, err := recovered.Project(t.Context())
	require.NoError(t, err)
	assert.Equal(t, fixture.before.Revision, project.Revision)
	saved, err := recovered.ReadDraft(t.Context(), fixture.draft.ID)
	require.NoError(t, err)
	assert.Equal(t, fixture.draft.Revision, saved.Revision)
	published, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, fixture.previousSite, published)
	_, err = os.Stat(filepath.Join(fixture.config.Root, "published"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(fixture.stage)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPublicationDirectoryRecoveryRestoresNestedMoveAtDifferentInterruptionPoints(t *testing.T) {
	for _, state := range []string{"installed", "copied-before-delete", "deleted-before-copy"} {
		t.Run(state, func(t *testing.T) {
			fixture := prepareDirectoryPublicationRecoveryFixture(t)
			fixture.applySources(t)
			switch state {
			case "copied-before-delete":
				original := filepath.Join(fixture.config.Root, "guides", "nested", "chapter.md")
				require.NoError(t, os.MkdirAll(filepath.Dir(original), 0o700))
				require.NoError(t, os.WriteFile(original, []byte(publicationMovedContent), 0o600))
			case "deleted-before-copy":
				require.NoError(t, os.RemoveAll(filepath.Join(fixture.config.Root, "published")))
			}
			recovered, err := fixture.reopen(t)
			require.NoError(t, err)
			assertRecoveredPublicationDirectory(t, fixture, recovered)
		})
	}
}

func TestPublicationDirectoryRecoveryPreservesUnmanagedChildrenUntilOperatorRepair(t *testing.T) {
	for _, kind := range []string{"hidden-file", "hidden-directory", "symbolic-link"} {
		t.Run(kind, func(t *testing.T) {
			fixture := prepareDirectoryPublicationRecoveryFixture(t)
			fixture.applySources(t)
			external := []byte("Independent operator data, not part of either publication.\r\n")
			obstruction := filepath.Join(fixture.config.Root, "published", "nested", ".operator-data")
			payload := obstruction
			switch kind {
			case "hidden-directory":
				require.NoError(t, os.Mkdir(obstruction, 0o700))
				payload = filepath.Join(obstruction, "notes.md")
			case "symbolic-link":
				payload = filepath.Join(t.TempDir(), "external.md")
				require.NoError(t, os.Symlink(payload, obstruction))
			}
			require.NoError(t, os.WriteFile(payload, external, 0o600))
			_, err := fixture.reopen(t)
			require.Error(t, err)
			retained, err := os.ReadFile(payload)
			require.NoError(t, err)
			assert.Equal(t, external, retained)
			current, err := os.ReadFile(filepath.Join(fixture.config.Root, "index.md"))
			require.NoError(t, err)
			assert.Equal(t, fixture.replacement, current, "refusal must happen before restoring any sources")
			moved, err := os.ReadFile(filepath.Join(fixture.config.Root, "published", "nested", "chapter.md"))
			require.NoError(t, err)
			assert.Equal(t, publicationMovedContent, string(moved))
			backup, err := os.ReadFile(filepath.Join(fixture.stage, "backup", "guides", "nested", "chapter.md"))
			require.NoError(t, err)
			assert.Equal(t, publicationMovedContent, string(backup))
			published, err := os.ReadFile(filepath.Join(fixture.config.OutputDir, "index.html"))
			require.NoError(t, err)
			assert.Equal(t, fixture.previousSite, published)
			if kind == "symbolic-link" {
				target, err := os.Readlink(obstruction)
				require.NoError(t, err)
				assert.Equal(t, payload, target)
			}

			// Explicit operator repair removes only the test-created obstruction.
			// A subsequent real startup must then finish the known rollback.
			require.NoError(t, os.RemoveAll(obstruction))
			recovered, err := New(fixture.config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, recovered.Close()) })
			assertRecoveredPublicationDirectory(t, fixture, recovered)
			if kind == "symbolic-link" {
				retained, err := os.ReadFile(payload)
				require.NoError(t, err)
				assert.Equal(t, external, retained)
			}
		})
	}
}
