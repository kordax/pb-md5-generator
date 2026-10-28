package site

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
)

// Appearance resolves omitted manifest groups to the standard reader defaults.
// It is independent of CMS state and remains usable by static renderers.
type Appearance struct {
	Theme      document.Theme
	Navigation document.NavigationSettings
	Editor     document.EditorSettings
	API        document.APISettings
	Publishing document.PublishingSettings
}

func DefaultTheme() document.Theme {
	return document.Theme{ID: "default", Name: "Default", Description: "A clear reading workspace with quiet zinc surfaces and violet accents.", Mode: "light", Accent: "#6557d2", Font: "system", Density: "comfortable", Width: "normal", Radius: 5, Light: &document.ThemePalette{Background: "#ffffff", Surface: "#fafafa", Text: "#18181b", Muted: "#71717a", Border: "#e4e4e7"}, Dark: &document.ThemePalette{Background: "#191a1f", Surface: "#222329", Text: "#e4e4e7", Muted: "#a1a1aa", Border: "#37383f", Accent: "#a99bec"}}
}

// BuiltinThemes returns independent values; callers cannot mutate a global
// registry or affect settings in another project.
func BuiltinThemes() []document.Theme {
	standard := DefaultTheme()
	standard.Light.Accent = standard.Accent
	paper := document.Theme{ID: "paper", Name: "Paper", Description: "Warm ivory and clay, with a quiet amber night palette.", Mode: "light", Accent: "#9a3412", Light: &document.ThemePalette{Background: "#fffbf5", Surface: "#faf2e6", Text: "#292524", Muted: "#6b6058", Border: "#e7dac9", Accent: "#9a3412"}, Dark: &document.ThemePalette{Background: "#191a1f", Surface: "#222329", Text: "#e4e4e7", Muted: "#a1a1aa", Border: "#37383f", Accent: "#fdba74"}}
	midnight := document.Theme{ID: "midnight", Name: "Midnight", Description: "Ink-blue surfaces and soft periwinkle for focused evening reading.", Mode: "dark", Accent: "#a5b4fc", Light: &document.ThemePalette{Background: "#ffffff", Surface: "#fafafa", Text: "#18181b", Muted: "#71717a", Border: "#e4e4e7", Accent: "#4338ca"}, Dark: &document.ThemePalette{Background: "#11121b", Surface: "#191b29", Text: "#e9e9f4", Muted: "#aaabc5", Border: "#30334b", Accent: "#a5b4fc"}}
	themes := []document.Theme{standard, paper, midnight,
		{ID: "ocean", Name: "Ocean", Description: "Clear blue actions across cool porcelain and deep marine surfaces.", Mode: "light", Accent: "#1d5ea8", Light: &document.ThemePalette{Background: "#f7fbff", Surface: "#f0f6fc", Text: "#142b43", Muted: "#52657a", Border: "#cbd9e6", Accent: "#1d5ea8"}, Dark: &document.ThemePalette{Background: "#111d2a", Surface: "#192a3b", Text: "#e0ecf6", Muted: "#9eb3c7", Border: "#334b61", Accent: "#80bbf4"}},
		{ID: "forest", Name: "Forest", Description: "Moss-green accents and natural, softly tinted reading surfaces.", Mode: "light", Accent: "#246844", Light: &document.ThemePalette{Background: "#f7fbf8", Surface: "#f0f6f1", Text: "#20392b", Muted: "#526b5b", Border: "#cbdccd", Accent: "#246844"}, Dark: &document.ThemePalette{Background: "#14221b", Surface: "#1c2e24", Text: "#e0efe5", Muted: "#a0b9a8", Border: "#395243", Accent: "#83c99e"}},
		{ID: "sand", Name: "Sand", Description: "Honeyed canvas, bronze actions and warm sandstone shadows.", Mode: "light", Accent: "#8a571c", Light: &document.ThemePalette{Background: "#fffbf4", Surface: "#f8f0e1", Text: "#423323", Muted: "#75634c", Border: "#e1d3bb", Accent: "#8a571c"}, Dark: &document.ThemePalette{Background: "#272017", Surface: "#342a1e", Text: "#f3e7d3", Muted: "#c4af8f", Border: "#5a4832", Accent: "#e0b372"}},
		{ID: "rose", Name: "Rose", Description: "Berry actions with pale rose surfaces and a warm burgundy night palette.", Mode: "light", Accent: "#a43762", Light: &document.ThemePalette{Background: "#fff8fa", Surface: "#fbeef3", Text: "#442735", Muted: "#7d5b6b", Border: "#e7cdd9", Accent: "#a43762"}, Dark: &document.ThemePalette{Background: "#291b24", Surface: "#382531", Text: "#f5e3ed", Muted: "#c7a4b8", Border: "#5b3f50", Accent: "#e99cbd"}},
		{ID: "glacier", Name: "Glacier", Description: "Crisp cyan and glacial blue with clear, cool contrast.", Mode: "light", Accent: "#17677a", Light: &document.ThemePalette{Background: "#f4fbfc", Surface: "#e8f4f6", Text: "#19373d", Muted: "#526c73", Border: "#c5dce0", Accent: "#17677a"}, Dark: &document.ThemePalette{Background: "#122328", Surface: "#1a3037", Text: "#def0f4", Muted: "#9dbac2", Border: "#34555e", Accent: "#7dc9db"}},
		{ID: "graphite", Name: "Graphite", Description: "Neutral charcoal, silver actions and restrained mineral surfaces.", Mode: "dark", Accent: "#b7c2cf", Light: &document.ThemePalette{Background: "#fafafa", Surface: "#f1f2f3", Text: "#24272b", Muted: "#62676e", Border: "#d3d6da", Accent: "#4f5964"}, Dark: &document.ThemePalette{Background: "#1b1d20", Surface: "#272a2f", Text: "#e8eaed", Muted: "#afb5bd", Border: "#454b53", Accent: "#b7c2cf"}},
		{ID: "aubergine", Name: "Aubergine", Description: "Deep plum surfaces with orchid actions and soft lilac daylight.", Mode: "dark", Accent: "#cf9ce6", Light: &document.ThemePalette{Background: "#fcf8fd", Surface: "#f4edf6", Text: "#39283f", Muted: "#705c79", Border: "#dccfe3", Accent: "#7b3e91"}, Dark: &document.ThemePalette{Background: "#251c2c", Surface: "#33263d", Text: "#f0e5f6", Muted: "#bba5c7", Border: "#55415f", Accent: "#cf9ce6"}},
		{ID: "teal-night", Name: "Teal Night", Description: "Deep teal and sea-glass actions, paired with a gentle mint daylight palette.", Mode: "dark", Accent: "#7ed0b9", Light: &document.ThemePalette{Background: "#f5fcfb", Surface: "#edf8f5", Text: "#173b36", Muted: "#506d67", Border: "#c5dcd6", Accent: "#166b5c"}, Dark: &document.ThemePalette{Background: "#122724", Surface: "#1b3631", Text: "#e0f2ed", Muted: "#a0beb6", Border: "#365a52", Accent: "#7ed0b9"}},
	}
	for index := range themes {
		themes[index].Font = "system"
		themes[index].Density = "comfortable"
		themes[index].Width = "normal"
		themes[index].Radius = 5
	}
	return themes
}

func (m Manifest) Appearance() Appearance {
	result := Appearance{Theme: DefaultTheme(), Navigation: document.NavigationSettings{ShowTOC: true, ShowPreviousNext: true, StickyHeader: true}, Editor: document.EditorSettings{DefaultFormat: "markdown"}, API: document.APISettings{Enabled: true, ShowCurl: true, RequestTimeoutMs: 30000, MaxResponseBytes: 1 << 20}}
	if m.Theme != nil {
		result.Theme = *m.Theme
	} else if m.ThemeID != "" {
		for _, theme := range BuiltinThemes() {
			if theme.ID == m.ThemeID {
				result.Theme = theme
				break
			}
		}
	}
	if m.Navigation != nil {
		result.Navigation = *m.Navigation
	}
	if m.Editor != nil {
		result.Editor = *m.Editor
	}
	if m.API != nil {
		result.API = *m.API
	}
	if m.Publishing != nil {
		result.Publishing = *m.Publishing
	}
	return result
}

func validThemeID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	first := id[0]
	if (first < 'a' || first > 'z') && (first < '0' || first > '9') {
		return false
	}
	for _, char := range id {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func color(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}

func readableText(value string, maximum int, required bool) bool {
	if !utf8.ValidString(value) || len(value) > maximum || required && strings.TrimSpace(value) == "" {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) && char != '\n' && char != '\t' {
			return false
		}
	}
	return true
}

// ValidateTheme permits a bounded vocabulary of inert design tokens.
func ValidateTheme(theme document.Theme) error {
	if !validThemeID(theme.ID) || !readableText(theme.Name, 120, true) || !readableText(theme.Description, 512, false) {
		return fmt.Errorf("theme ID, name or description is invalid")
	}
	if theme.Mode != "auto" && theme.Mode != "light" && theme.Mode != "dark" {
		return fmt.Errorf("theme mode must be auto, light or dark")
	}
	if !color(theme.Accent) {
		return fmt.Errorf("theme accent must be a six digit hexadecimal color")
	}
	if theme.Font != "system" && theme.Font != "serif" && theme.Font != "mono" {
		return fmt.Errorf("theme font must be system, serif or mono")
	}
	if theme.Density != "comfortable" && theme.Density != "compact" {
		return fmt.Errorf("theme density must be comfortable or compact")
	}
	if theme.Width != "normal" && theme.Width != "wide" {
		return fmt.Errorf("theme width must be normal or wide")
	}
	if theme.Radius < 0 || theme.Radius > 24 {
		return fmt.Errorf("theme radius must be between 0 and 24")
	}
	for _, palette := range []*document.ThemePalette{theme.Light, theme.Dark} {
		if palette == nil {
			continue
		}
		for _, value := range []string{palette.Background, palette.Surface, palette.Text, palette.Muted, palette.Border, palette.Accent} {
			if value != "" && !color(value) {
				return fmt.Errorf("theme palette values must be six digit hexadecimal colors")
			}
		}
	}
	return nil
}

// ValidateAppearance validates the portable settings without opening files.
// Brand images must be declared local assets so static exports remain complete.
func (m Manifest) ValidateAppearance() error {
	if !readableText(m.Title, 256, true) || !readableText(m.Description, 4096, false) || !readableText(m.Footer, 1024, false) {
		return fmt.Errorf("site title, description or footer is invalid")
	}
	for _, image := range []string{m.Logo, m.Favicon} {
		if image == "" {
			continue
		}
		if !localPath(image) || !readableText(image, 4096, true) {
			return fmt.Errorf("branding image must be a declared project asset")
		}
		switch strings.ToLower(path.Ext(image)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico":
		default:
			return fmt.Errorf("branding image must use a supported raster image format")
		}
		found := false
		for _, asset := range m.Assets {
			if asset == image {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("branding image %q is not declared in assets", image)
		}
	}
	if m.ThemeID != "" && !validThemeID(m.ThemeID) {
		return fmt.Errorf("themeId is invalid")
	}
	if m.Theme != nil {
		if m.ThemeID != m.Theme.ID {
			return fmt.Errorf("resolved theme ID must match themeId")
		}
		if err := ValidateTheme(*m.Theme); err != nil {
			return err
		}
	} else if m.ThemeID != "" {
		found := false
		for _, theme := range BuiltinThemes() {
			if m.ThemeID == theme.ID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("custom themeId requires resolved theme tokens")
		}
	}
	if m.Navigation != nil {
		if err := ValidateNavigation(m.Navigation.Tree, m.Pages); err != nil {
			return err
		}
	}
	if m.Editor != nil {
		switch m.Editor.DefaultFormat {
		case "markdown", "html", "protobuf", "openapi":
		default:
			return fmt.Errorf("editor defaultFormat is unsupported")
		}
	}
	if m.API != nil && (m.API.RequestTimeoutMs < 1000 || m.API.RequestTimeoutMs > 30000 || m.API.MaxResponseBytes < 1024 || m.API.MaxResponseBytes > 1<<20) {
		return fmt.Errorf("API timeout or response limit is outside the supported range")
	}
	return nil
}

// resolvedPalette fills omitted custom tokens from the reader's standard
// surfaces while retaining the custom theme's own accent as its fallback.
func resolvedPalette(palette, defaults *document.ThemePalette, accent string) document.ThemePalette {
	result := *defaults
	result.Accent = accent
	if palette == nil {
		return result
	}
	for _, pair := range [][2]*string{{&result.Background, &palette.Background}, {&result.Surface, &palette.Surface}, {&result.Text, &palette.Text}, {&result.Muted, &palette.Muted}, {&result.Border, &palette.Border}, {&result.Accent, &palette.Accent}} {
		if *pair[1] != "" {
			*pair[0] = *pair[1]
		}
	}
	return result
}

func paletteCSS(selector string, palette *document.ThemePalette, fallback string) string {
	var out strings.Builder
	out.WriteString(selector + "{")
	out.WriteString("--accent:" + fallback + ";")
	if palette != nil {
		for _, entry := range [][2]string{{"--bg", palette.Background}, {"--panel", palette.Surface}, {"--text", palette.Text}, {"--muted", palette.Muted}, {"--line", palette.Border}, {"--accent", palette.Accent}, {"--code", palette.Surface}} {
			if entry[1] != "" {
				out.WriteString(entry[0] + ":" + entry[1] + ";")
			}
		}
	}
	out.WriteString("}")
	return out.String()
}

// appearanceCSS is generated only from validated tokens. No authored CSS enters
// the stylesheet, and the reader's CSP never needs unsafe-inline.
func appearanceCSS(m Manifest) string {
	a := m.Appearance()
	font := `"DM Sans",ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif`
	if a.Theme.Font == "serif" {
		font = `Georgia,"Times New Roman",serif`
	}
	if a.Theme.Font == "mono" {
		font = `"IBM Plex Mono",ui-monospace,SFMono-Regular,Consolas,monospace`
	}
	width, measure := "900px", "42rem"
	if a.Theme.Width == "wide" {
		width, measure = "1200px", "48rem"
	}
	css := ":root{--docs-radius:" + strconv.Itoa(a.Theme.Radius) + "px;--docs-font:" + font + ";--docs-article-width:" + width + ";--reader-measure:" + measure + "}"
	defaults := DefaultTheme()
	light := resolvedPalette(a.Theme.Light, defaults.Light, a.Theme.Accent)
	dark := resolvedPalette(a.Theme.Dark, defaults.Dark, a.Theme.Accent)
	css += paletteCSS(":root", &light, a.Theme.Accent)
	css += "@media(prefers-color-scheme:dark){" + paletteCSS(":root:not([data-theme=light])", &dark, a.Theme.Accent) + "}"
	css += paletteCSS(":root[data-theme=light]", &light, a.Theme.Accent) + paletteCSS(":root[data-theme=dark]", &dark, a.Theme.Accent)
	// The request workbench remains dark beside a light article, using the
	// selected scheme's composed dark palette rather than fixed charcoal.
	css += ":root{--api-bg:" + dark.Background + ";--api-panel:" + dark.Surface + ";--api-text:" + dark.Text + ";--api-muted:" + dark.Muted + ";--api-line:" + dark.Border + ";--api-accent:" + dark.Accent + ";--api-code:" + dark.Surface + ";--api-on-accent:" + dark.Background + ";}"
	css += "body{font-family:var(--docs-font)}article{max-width:var(--docs-article-width)}button,input[type=search],pre,.docs-callout,.docs-tab-panel:last-child{border-radius:var(--docs-radius)}.brand-logo{max-width:180px;max-height:40px;object-fit:contain}.page-sequence{display:flex;justify-content:space-between;gap:20px;margin-top:40px}.page-sequence a{max-width:48%;overflow-wrap:anywhere}.page-sequence .next{margin-inline-start:auto;text-align:end}"
	if !a.Navigation.ShowTOC {
		css += ".layout{grid-template-columns:224px minmax(0,1fr)}.toc{display:none}@media(max-width:1150px){.layout{grid-template-columns:224px minmax(0,1fr)}}@media(max-width:760px){.layout{display:block}}"
	}
	if !a.Navigation.StickyHeader {
		css += ".topbar{position:static}@media(min-width:761px){.sidebar,.toc{top:0;height:100vh}}"
	}
	if a.Theme.Density == "compact" {
		css += "body{font-size:15px;line-height:1.6}main{padding-top:26px}.navigation a{padding:5px 10px}pre{padding:16px}h1{margin-bottom:18px}.breadcrumb{margin-bottom:20px}"
	}
	return css + "\n"
}
