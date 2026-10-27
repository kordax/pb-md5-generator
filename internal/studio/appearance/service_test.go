package appearance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	service *Service
	content *content.Service
	config  content.Config
	fail    atomic.Bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return fixtureWithStorage(t, nil)
}

func fixtureWithStorage(t *testing.T, storage content.Backend) *fixture {
	t.Helper()
	base := t.TempDir()
	value := &fixture{}
	value.config = content.Config{Root: filepath.Join(base, "content"), StateDir: filepath.Join(base, "state"), OutputDir: filepath.Join(base, "site"), Initialize: true, Storage: storage}
	value.config.Compile = func(ctx context.Context, manifest, output string) error {
		if value.fail.Load() {
			return errors.New("publication is intentionally unavailable")
		}
		return site.BuildContext(ctx, manifest, output, site.Options{})
	}
	var err error
	value.content, err = content.New(value.config)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, value.content.Close()) })
	value.service, err = New(Config{Content: value.content, Root: value.config.Root})
	require.NoError(t, err)
	_, err = value.content.BuildCurrent(t.Context(), content.Actor{ID: "system", Name: "System"})
	require.NoError(t, err)
	return value
}

func testActor() content.Actor {
	return content.Actor{ID: "alice", Name: "Alice"}
}

func customTheme() document.Theme {
	theme := site.DefaultTheme()
	theme.ID, theme.Name = "orchid", "Orchid"
	theme.Accent = "#a21caf"
	theme.Font, theme.Width = "serif", "wide"
	theme.Light = &document.ThemePalette{Background: "#fdf4ff", Text: "#3b0764"}
	return theme
}

func TestSettingsThemesPublicationRestartAndActiveThemeUpdates(t *testing.T) {
	value := newFixture(t)
	ctx := t.Context()
	settings, err := value.service.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Documentation", settings.Title)
	assert.Equal(t, "default", settings.ThemeID)
	assert.True(t, settings.Navigation.ShowTOC)
	assert.True(t, settings.Navigation.ShowPreviousNext)
	assert.True(t, settings.API.Enabled)
	assert.False(t, settings.Publishing.AutoPublish)
	assert.Equal(t, 30000, settings.API.RequestTimeoutMs)

	theme, err := value.service.SaveTheme(ctx, customTheme(), "", testActor())
	require.NoError(t, err)
	assert.False(t, theme.Builtin)
	require.NotEmpty(t, theme.Revision)
	settings, err = value.service.Read(ctx)
	require.NoError(t, err)
	settings.Title, settings.Footer, settings.ThemeID = "Project handbook", "Maintained by the team", theme.ID
	settings.Navigation.ShowTOC = false
	settings.API.RequestTimeoutMs, settings.API.MaxResponseBytes = 1500, 4096
	settings.Editor.DefaultFormat, settings.Editor.PreviewOnSave = "html", true
	settings, err = value.service.Update(ctx, settings, testActor())
	require.NoError(t, err)
	assert.Equal(t, "Orchid", settings.Theme.Name)
	assert.Equal(t, "#a21caf", settings.Theme.Accent)
	assert.Equal(t, "html", settings.Editor.DefaultFormat)
	html, err := os.ReadFile(filepath.Join(value.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Contains(t, string(html), "Project handbook")
	assert.Contains(t, string(html), "Maintained by the team")
	assert.NotContains(t, string(html), `class="toc"`)
	css, err := os.ReadFile(filepath.Join(value.config.OutputDir, "_docs", "appearance.css"))
	require.NoError(t, err)
	assert.Contains(t, string(css), "#a21caf")
	assert.Contains(t, string(css), "#fdf4ff")
	assert.Contains(t, string(css), "Georgia")
	assert.Contains(t, string(css), "1200px")
	projectData, err := os.ReadFile(filepath.Join(value.config.OutputDir, "_docs", "project.json"))
	require.NoError(t, err)
	var project document.Project
	require.NoError(t, json.Unmarshal(projectData, &project))
	require.NotNil(t, project.Theme)
	assert.Equal(t, theme.ID, project.ThemeID)
	assert.Equal(t, "#a21caf", project.Theme.Accent)

	themes, err := value.service.Themes(ctx)
	require.NoError(t, err)
	theme = themes[len(themes)-1]
	theme.Accent = "#86198f"
	theme, err = value.service.SaveTheme(ctx, theme.Theme, theme.Revision, testActor())
	require.NoError(t, err)
	settings, err = value.service.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "#86198f", settings.Theme.Accent)
	css, err = os.ReadFile(filepath.Join(value.config.OutputDir, "_docs", "appearance.css"))
	require.NoError(t, err)
	assert.Contains(t, string(css), "#86198f")
	require.ErrorIs(t, value.service.DeleteTheme(ctx, theme.ID, theme.Revision, testActor()), content.ErrInvalid)

	require.NoError(t, value.content.Close())
	value.config.Initialize = false
	value.content, err = content.New(value.config)
	require.NoError(t, err)
	value.service, err = New(Config{Content: value.content})
	require.NoError(t, err)
	restored, err := value.service.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, settings, restored)
	history, err := value.content.History(ctx, "docs.json")
	require.NoError(t, err)
	require.NotEmpty(t, history)
	assert.Equal(t, testActor().ID, history[0].Actor.ID)
}

func TestSettingsCASFailedPublicationAndThemeDeletionPreserveContent(t *testing.T) {
	value := newFixture(t)
	ctx := t.Context()
	settings, err := value.service.Read(ctx)
	require.NoError(t, err)
	original := settings
	html, err := os.ReadFile(filepath.Join(value.config.OutputDir, "index.html"))
	require.NoError(t, err)
	value.fail.Store(true)
	settings.Title = "Unpublished title"
	_, err = value.service.Update(ctx, settings, testActor())
	require.Error(t, err)
	current, err := value.service.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, original, current)
	unchanged, err := os.ReadFile(filepath.Join(value.config.OutputDir, "index.html"))
	require.NoError(t, err)
	assert.Equal(t, html, unchanged)
	drafts, err := value.content.Drafts(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, drafts, "failed settings publication remains recoverable as a draft")
	value.fail.Store(false)
	settings.Title = "Published title"
	updated, err := value.service.Update(ctx, settings, testActor())
	require.NoError(t, err)
	_, err = value.service.Update(ctx, original, testActor())
	require.ErrorIs(t, err, content.ErrConflict)
	assert.NotEqual(t, original.Revision, updated.Revision)

	theme, err := value.service.SaveTheme(ctx, customTheme(), "", testActor())
	require.NoError(t, err)
	_, err = value.service.SaveTheme(ctx, theme.Theme, "", testActor())
	require.ErrorIs(t, err, content.ErrConflict)
	require.ErrorIs(t, value.service.DeleteTheme(ctx, theme.ID, original.Revision, testActor()), content.ErrConflict)
	require.NoError(t, value.service.DeleteTheme(ctx, theme.ID, theme.Revision, testActor()))
	_, err = value.content.Read(ctx, "themes/"+theme.ID+".json")
	require.ErrorIs(t, err, content.ErrNotFound)
	require.ErrorIs(t, value.service.DeleteTheme(ctx, theme.ID, theme.Revision, testActor()), content.ErrNotFound)
	_, err = value.service.SaveTheme(ctx, site.DefaultTheme(), "", testActor())
	require.ErrorIs(t, err, content.ErrInvalid)
	require.ErrorIs(t, value.service.DeleteTheme(ctx, "default", updated.Revision, testActor()), content.ErrInvalid)
}

func TestConcurrentSettingsUpdatesAndValidation(t *testing.T) {
	value := newFixture(t)
	settings, err := value.service.Read(t.Context())
	require.NoError(t, err)
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, title := range []string{"First", "Second"} {
		wait.Go(func() {
			candidate := settings
			candidate.Title = title
			_, err := value.service.Update(t.Context(), candidate, testActor())
			results <- err
		})
	}
	wait.Wait()
	close(results)
	passed, conflicts := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else if errors.Is(err, content.ErrConflict) {
			conflicts++
		} else {
			t.Errorf("unexpected update failure: %v", err)
		}
	}
	assert.Equal(t, 1, passed)
	assert.Equal(t, 1, conflicts)
	settings, err = value.service.Read(t.Context())
	require.NoError(t, err)
	for _, mutate := range []func(*Settings){
		func(value *Settings) { value.Title = " " },
		func(value *Settings) { value.ThemeID = "missing" },
		func(value *Settings) { value.Logo = "https://example.invalid/logo.png" },
		func(value *Settings) { value.Favicon = "undeclared.png" },
		func(value *Settings) { value.API.RequestTimeoutMs = 999 },
		func(value *Settings) { value.API.MaxResponseBytes = 1<<20 + 1 },
		func(value *Settings) { value.Editor.DefaultFormat = "javascript" },
		func(value *Settings) { value.Publishing.GitPushOnPublish = true },
		func(value *Settings) { value.Footer = strings.Repeat("x", 1025) },
	} {
		candidate := settings
		mutate(&candidate)
		_, err := value.service.Update(t.Context(), candidate, testActor())
		require.ErrorIs(t, err, content.ErrInvalid)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = value.service.Update(canceled, settings, testActor())
	require.ErrorIs(t, err, context.Canceled)
	var missing context.Context
	_, err = value.service.Update(missing, settings, testActor())
	require.ErrorIs(t, err, content.ErrInvalid)
	_, err = New(Config{})
	require.ErrorIs(t, err, content.ErrInvalid)
	_, err = New(Config{Content: value.content, Manifest: "other.json"})
	require.ErrorIs(t, err, content.ErrInvalid)
}
