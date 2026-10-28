package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type publicationStatusFailure struct {
	Backend
	output string
	name   string
	failed bool
}

func (backend *publicationStatusFailure) Status(context.Context) (SyncStatus, error) {
	if _, err := os.Stat(filepath.Join(backend.output, "index.html")); err == nil && !backend.failed {
		backend.failed = true
		return SyncStatus{}, errors.New("private backend status details")
	}
	status := SyncStatus{Backend: backend.name, State: "clean"}
	if backend.name == "git" {
		status.Branch, status.Head = "main", strings.Repeat("a", 40)
	}
	return status, nil
}

func (*publicationStatusFailure) Commit(context.Context, []string, Actor, string) (string, error) {
	return "", errors.New("private backend commit details")
}

func TestPublicationStatusFailureRetainsBackendAndBranchHistory(t *testing.T) {
	for _, name := range []string{"local", "git"} {
		t.Run(name, func(t *testing.T) {
			original, config := managedFixture(t)
			require.NoError(t, original.Close())
			storage := &publicationStatusFailure{Backend: NewLocalBackend(), output: config.OutputDir, name: name}
			config.Storage = storage
			service, err := New(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, service.Close()) })
			draft := saveText(t, service, "index.md", "# Publication with unavailable storage status\n")
			result, err := service.Publish(context.Background(), PublishInput{ID: draft.ID, Revision: draft.Revision}, editor)
			require.NoError(t, err)
			assert.True(t, result.Published)
			assert.True(t, storage.failed)
			assert.Equal(t, name, result.Sync.Backend)
			assert.Equal(t, "error", result.Sync.State)
			assert.True(t, result.Sync.Retryable)
			assert.Contains(t, result.Sync.Error, "restart is required")
			assert.NotContains(t, result.Sync.Error, "private backend")
			if name == "git" {
				assert.Equal(t, "main", result.Sync.Branch)
			}
			_, err = service.History(context.Background(), "index.md")
			assert.ErrorIs(t, err, ErrConflict, "unconfirmed publication authority must block public operations")
			require.NoError(t, service.Close())
			recovered, err := New(config)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, recovered.Close()) })
			history, err := recovered.History(context.Background(), "index.md")
			require.NoError(t, err)
			assert.Len(t, history, 2, "recovery must retain history under the original backend and branch")
			file, err := recovered.Read(context.Background(), "index.md")
			require.NoError(t, err)
			assert.Equal(t, "# Publication with unavailable storage status\n", file.Content)
		})
	}
}
