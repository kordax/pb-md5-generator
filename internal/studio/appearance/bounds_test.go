package appearance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type offlineBackend struct {
	content.Backend
	commits atomic.Int32
	pushes  atomic.Int32
}

func (backend *offlineBackend) Status(context.Context) (content.SyncStatus, error) {
	return content.SyncStatus{Backend: "git", Branch: "main", Head: strings.Repeat("a", 40), State: "clean"}, nil
}

func (backend *offlineBackend) Commit(context.Context, []string, content.Actor, string) (string, error) {
	backend.commits.Add(1)
	return strings.Repeat("a", 40), nil
}

func (backend *offlineBackend) Push(context.Context) error {
	backend.pushes.Add(1)
	return errors.New("offline")
}

func TestSettingsAndThemePushFailuresExposeRetryWithoutRollingBackPublication(t *testing.T) {
	backend := &offlineBackend{Backend: content.NewLocalBackend()}
	value := fixtureWithStorage(t, backend)
	settings, err := value.service.Read(t.Context())
	require.NoError(t, err)
	settings.Title = "Saved despite remote outage"
	settings.Publishing.GitPushOnPublish = true
	settings, err = value.service.Update(t.Context(), settings, testActor())
	require.NoError(t, err)
	require.NotNil(t, settings.Sync)
	assert.Equal(t, "pending", settings.Sync.State)
	assert.True(t, settings.Sync.Retryable)
	assert.Contains(t, settings.Sync.Error, "Local commit is retained")
	assert.NotContains(t, settings.Sync.Error, "offline")
	assert.EqualValues(t, 1, backend.commits.Load())
	assert.EqualValues(t, 1, backend.pushes.Load())
	current, err := value.service.Read(t.Context())
	require.NoError(t, err)
	assert.Equal(t, settings.Title, current.Title)
	assert.Nil(t, current.Sync, "a read is current state, not a replay of an operation result")
	theme, err := value.service.SaveTheme(t.Context(), customTheme(), "", testActor())
	require.NoError(t, err)
	require.NotNil(t, theme.Sync)
	assert.True(t, theme.Sync.Retryable)
	warning, err := value.service.deleteTheme(t.Context(), theme.ID, theme.Revision, testActor())
	require.NoError(t, err)
	require.NotNil(t, warning)
	assert.True(t, warning.Retryable)
	_, err = value.content.Read(t.Context(), themePath(theme.ID))
	require.ErrorIs(t, err, content.ErrNotFound)
	assert.EqualValues(t, 3, backend.commits.Load())
	assert.EqualValues(t, 3, backend.pushes.Load())
}

func TestThemeRegistryCapsAndInvalidFilesFailBeforeWriting(t *testing.T) {
	value := newFixture(t)
	directory := filepath.Join(value.config.Root, "themes")
	require.NoError(t, os.MkdirAll(directory, 0o750))
	for index := range MaxThemes {
		theme := site.DefaultTheme()
		theme.ID, theme.Name = fmt.Sprintf("theme%d", index), fmt.Sprintf("Theme %d", index)
		data, err := encode(theme)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(directory, theme.ID+".json"), []byte(data), 0o600))
	}
	themes, err := value.service.Themes(t.Context())
	require.NoError(t, err)
	assert.Len(t, themes, MaxThemes+len(site.BuiltinThemes()))
	before, err := value.content.Project(t.Context())
	require.NoError(t, err)
	_, err = value.service.SaveTheme(t.Context(), customTheme(), "", testActor())
	require.ErrorIs(t, err, content.ErrLimit)
	after, err := value.content.Project(t.Context())
	require.NoError(t, err)
	assert.Equal(t, before.Revision, after.Revision)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "theme0.json"), []byte(strings.Repeat("x", maxThemeBytes+1)), 0o600))
	_, err = value.service.Themes(t.Context())
	require.ErrorIs(t, err, content.ErrLimit)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "theme0.json"), []byte(`{"id":"theme0","script":"alert(1)"}`), 0o600))
	_, err = value.service.Themes(t.Context())
	require.ErrorIs(t, err, content.ErrInvalid)
}
