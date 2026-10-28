// Package appearance manages portable site settings and bounded theme registries.
// Every mutation uses the content transaction, compiler and storage backend.
package appearance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
)

const (
	MaxThemes        = 64
	MaxSettingsBytes = 512 << 10
	maxThemeBytes    = 16 << 10
)

type Config struct {
	Content             *content.Service
	Root                string
	Manifest            string
	AuthorizeNavigation func(context.Context, content.Actor) error
}

type Service struct {
	content             *content.Service
	manifest            string
	gate                chan struct{}
	authorizeNavigation func(context.Context, content.Actor) error
}

// Settings is a complete, resolved view. Revision is the project's opaque
// compare-and-swap token; it covers both the manifest and registered themes.
type Settings struct {
	Revision    string                      `json:"revision"`
	Title       string                      `json:"title"`
	Description string                      `json:"description"`
	Logo        string                      `json:"logo"`
	Favicon     string                      `json:"favicon"`
	Footer      string                      `json:"footer"`
	ThemeID     string                      `json:"themeId"`
	Theme       document.Theme              `json:"theme"`
	Navigation  document.NavigationSettings `json:"navigation"`
	Editor      document.EditorSettings     `json:"editor"`
	API         document.APISettings        `json:"api"`
	Publishing  document.PublishingSettings `json:"publishing"`
	Sync        *content.SyncStatus         `json:"sync,omitempty"`

	// NavigationTreeSet makes a nil tree an explicit reset. Without it, a nil
	// tree preserves the current navigation when older clients save settings.
	NavigationTreeSet bool `json:"-"`
}

type Theme struct {
	document.Theme
	Builtin  bool                `json:"builtin"`
	Revision string              `json:"revision,omitempty"`
	Sync     *content.SyncStatus `json:"sync,omitempty"`
}

type snapshot struct {
	manifest site.Manifest
	revision string
	themes   []Theme
}

func New(config Config) (*Service, error) {
	if config.Content == nil || config.Root != "" && !filepath.IsAbs(config.Root) {
		return nil, fmt.Errorf("%w: an existing content service and an absolute optional root are required", content.ErrInvalid)
	}
	if config.Manifest == "" {
		config.Manifest = "docs.json"
	}
	if config.Manifest != "docs.json" {
		return nil, fmt.Errorf("%w: CMS settings require docs.json", content.ErrInvalid)
	}
	return &Service{content: config.Content, manifest: config.Manifest, gate: make(chan struct{}, 1), authorizeNavigation: config.AuthorizeNavigation}, nil
}

func resolved(value snapshot) Settings {
	m := value.manifest
	a := m.Appearance()
	return Settings{Revision: value.revision, Title: m.Title, Description: m.Description, Logo: m.Logo, Favicon: m.Favicon, Footer: m.Footer, ThemeID: a.Theme.ID, Theme: a.Theme, Navigation: a.Navigation, Editor: a.Editor, API: a.API, Publishing: a.Publishing}
}

func (s *Service) Read(ctx context.Context) (Settings, error) {
	value, err := s.snapshot(ctx, false)
	if err != nil {
		return Settings{}, err
	}
	return resolved(value), nil
}

func (s *Service) Themes(ctx context.Context) ([]Theme, error) {
	value, err := s.snapshot(ctx, true)
	if err != nil {
		return nil, err
	}
	return value.themes, nil
}

func (s *Service) snapshot(ctx context.Context, themes bool) (snapshot, error) {
	for attempt := 0; attempt < 3; attempt++ {
		project, err := s.content.Project(ctx)
		if err != nil {
			return snapshot{}, err
		}
		file, err := s.content.Read(ctx, s.manifest)
		if err != nil {
			return snapshot{}, err
		}
		if file.Binary || len(file.Content) > 1<<20 {
			return snapshot{}, fmt.Errorf("%w: settings manifest is too large", content.ErrLimit)
		}
		manifest, err := site.DecodeManifest([]byte(file.Content))
		if err != nil {
			return snapshot{}, fmt.Errorf("%w: %v", content.ErrInvalid, err)
		}
		result := snapshot{manifest: manifest, revision: project.Revision}
		if themes {
			for _, theme := range site.BuiltinThemes() {
				result.themes = append(result.themes, Theme{Theme: theme, Builtin: true})
			}
			custom := make([]Theme, 0)
			for _, entry := range project.Files {
				if entry.Directory || !strings.HasPrefix(entry.Path, "themes/") || !strings.HasSuffix(entry.Path, ".json") {
					continue
				}
				if len(custom) >= MaxThemes || entry.Size > maxThemeBytes {
					return snapshot{}, fmt.Errorf("%w: custom theme registry exceeds its limit", content.ErrLimit)
				}
				file, err := s.content.Read(ctx, entry.Path)
				if err != nil {
					return snapshot{}, err
				}
				var theme document.Theme
				if len(file.Content) > maxThemeBytes || httpjson.Unmarshal([]byte(file.Content), &theme) != nil || site.ValidateTheme(theme) != nil || entry.Path != themePath(theme.ID) || builtin(theme.ID) {
					return snapshot{}, fmt.Errorf("%w: invalid custom theme %q", content.ErrInvalid, entry.Path)
				}
				custom = append(custom, Theme{Theme: theme, Revision: project.Revision})
			}
			sort.Slice(custom, func(i, j int) bool { return custom[i].ID < custom[j].ID })
			result.themes = append(result.themes, custom...)
		}
		latest, err := s.content.Project(ctx)
		if err != nil {
			return snapshot{}, err
		}
		if latest.Revision == project.Revision {
			return result, nil
		}
	}
	return snapshot{}, content.ErrConflict
}

func (s *Service) enter(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, content.ErrInvalid
	}
	select {
	case s.gate <- struct{}{}:
		return func() { <-s.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func builtin(id string) bool {
	for _, theme := range site.BuiltinThemes() {
		if theme.ID == id {
			return true
		}
	}
	return false
}

func themePath(id string) string {
	return "themes/" + id + ".json"
}

func encode(value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func (s *Service) publish(ctx context.Context, changes []content.Change, revision, message string, actor content.Actor, push bool) (*content.SyncStatus, error) {
	draft, err := s.content.SaveDraft(ctx, content.DraftInput{BaseRevision: revision, Changes: changes}, actor)
	if err != nil {
		return nil, err
	}
	result, err := s.content.Publish(ctx, content.PublishInput{ID: draft.ID, Revision: draft.Revision, Message: message, Push: push}, actor)
	if err != nil {
		return nil, err
	}
	if push && (result.Sync.Retryable || result.Sync.Error != "") {
		return &result.Sync, nil
	}
	return nil, nil
}

// Update stores the selected registry theme's resolved tokens in the manifest.
// ThemeID is authoritative: caller-supplied theme tokens cannot bypass the
// registry, builtin immutability or the current project's revision guard.
func (s *Service) Update(ctx context.Context, settings Settings, actor content.Actor) (Settings, error) {
	done, err := s.enter(ctx)
	if err != nil {
		return Settings{}, err
	}
	defer done()
	value, err := s.snapshot(ctx, true)
	if err != nil {
		return Settings{}, err
	}
	if settings.Revision == "" || settings.Revision != value.revision {
		return Settings{}, content.ErrConflict
	}
	if settings.NavigationTreeSet || settings.Navigation.Tree != nil {
		if s.authorizeNavigation != nil {
			if err := s.authorizeNavigation(ctx, actor); err != nil {
				return Settings{}, err
			}
		}
	} else {
		settings.Navigation.Tree = resolved(value).Navigation.Tree
	}
	settings.Navigation.Tree, err = site.CloneNavigation(settings.Navigation.Tree)
	if err != nil {
		return Settings{}, fmt.Errorf("%w: invalid navigation", content.ErrInvalid)
	}
	if err := site.ValidateTheme(settings.Theme); err != nil {
		return Settings{}, fmt.Errorf("%w: %v", content.ErrInvalid, err)
	}
	if settings.Publishing.GitPushOnPublish {
		status, err := s.content.Status(ctx)
		if err != nil {
			return Settings{}, err
		}
		if status.Backend != "git" {
			return Settings{}, fmt.Errorf("%w: automatic Git push requires the Git storage backend", content.ErrInvalid)
		}
	}
	var selected *document.Theme
	for _, theme := range value.themes {
		if theme.ID == settings.ThemeID {
			copy := theme.Theme
			selected = &copy
			break
		}
	}
	if selected == nil {
		return Settings{}, fmt.Errorf("%w: selected theme does not exist", content.ErrInvalid)
	}
	manifest := value.manifest
	manifest.Title, manifest.Description = settings.Title, settings.Description
	manifest.Logo, manifest.Favicon, manifest.Footer = settings.Logo, settings.Favicon, settings.Footer
	manifest.ThemeID, manifest.Theme = selected.ID, selected
	manifest.Navigation, manifest.Editor, manifest.API, manifest.Publishing = &settings.Navigation, &settings.Editor, &settings.API, &settings.Publishing
	if err := site.ValidateManifest(&manifest); err != nil {
		return Settings{}, fmt.Errorf("%w: %v", content.ErrInvalid, err)
	}
	bounded := settings
	bounded.Sync = nil
	data, err := json.Marshal(bounded)
	if err != nil {
		return Settings{}, fmt.Errorf("%w: invalid settings", content.ErrInvalid)
	}
	if len(data) > MaxSettingsBytes {
		return Settings{}, content.ErrLimit
	}
	text, err := encode(manifest)
	if err != nil {
		return Settings{}, err
	}
	sync, err := s.publish(ctx, []content.Change{{Operation: "write", Path: s.manifest, Content: &text}}, value.revision, "Update site settings", actor, settings.Publishing.GitPushOnPublish)
	if err != nil {
		return Settings{}, err
	}
	updated, err := s.Read(ctx)
	updated.Sync = sync
	return updated, err
}

func (s *Service) SaveTheme(ctx context.Context, theme document.Theme, revision string, actor content.Actor) (Theme, error) {
	done, err := s.enter(ctx)
	if err != nil {
		return Theme{}, err
	}
	defer done()
	if err := site.ValidateTheme(theme); err != nil || builtin(theme.ID) {
		return Theme{}, fmt.Errorf("%w: custom theme tokens are invalid or the ID belongs to a builtin theme", content.ErrInvalid)
	}
	value, err := s.snapshot(ctx, true)
	if err != nil {
		return Theme{}, err
	}
	exists := false
	for _, current := range value.themes {
		if current.ID == theme.ID {
			exists = true
			break
		}
	}
	if exists && revision != value.revision || !exists && revision != "" {
		return Theme{}, content.ErrConflict
	}
	if !exists && len(value.themes)-len(site.BuiltinThemes()) >= MaxThemes {
		return Theme{}, content.ErrLimit
	}
	text, err := encode(theme)
	if err != nil {
		return Theme{}, err
	}
	changes := []content.Change{{Operation: "write", Path: themePath(theme.ID), Content: &text}}
	if value.manifest.Appearance().Theme.ID == theme.ID {
		value.manifest.ThemeID, value.manifest.Theme = theme.ID, &theme
		manifest, err := encode(value.manifest)
		if err != nil {
			return Theme{}, err
		}
		changes = append(changes, content.Change{Operation: "write", Path: s.manifest, Content: &manifest})
	}
	sync, err := s.publish(ctx, changes, value.revision, "Save documentation theme", actor, value.manifest.Appearance().Publishing.GitPushOnPublish)
	if err != nil {
		return Theme{}, err
	}
	latest, err := s.content.Project(ctx)
	if err != nil {
		return Theme{}, err
	}
	return Theme{Theme: theme, Revision: latest.Revision, Sync: sync}, nil
}

func (s *Service) DeleteTheme(ctx context.Context, id, revision string, actor content.Actor) error {
	_, err := s.deleteTheme(ctx, id, revision, actor)
	return err
}

func (s *Service) deleteTheme(ctx context.Context, id, revision string, actor content.Actor) (*content.SyncStatus, error) {
	done, err := s.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	if builtin(id) {
		return nil, fmt.Errorf("%w: builtin themes cannot be deleted", content.ErrInvalid)
	}
	value, err := s.snapshot(ctx, true)
	if err != nil {
		return nil, err
	}
	found := false
	for _, theme := range value.themes {
		if theme.ID == id {
			found = true
			break
		}
	}
	if !found {
		return nil, content.ErrNotFound
	}
	if revision == "" || revision != value.revision {
		return nil, content.ErrConflict
	}
	if value.manifest.Appearance().Theme.ID == id {
		return nil, fmt.Errorf("%w: select another theme before deleting the active theme", content.ErrInvalid)
	}
	return s.publish(ctx, []content.Change{{Operation: "delete", Path: themePath(id)}}, value.revision, "Delete documentation theme", actor, value.manifest.Appearance().Publishing.GitPushOnPublish)
}
