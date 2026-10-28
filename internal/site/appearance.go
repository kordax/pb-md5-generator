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
	return document.Theme{ID: "default", Name: "Default", Description: "Clear, spacious documentation that follows the system color preference.", Mode: "auto", Accent: "#2563eb", Font: "system", Density: "comfortable", Width: "normal", Radius: 10, Dark: &document.ThemePalette{Accent: "#8eb7ff"}}
}

// BuiltinThemes returns independent values; callers cannot mutate a global
// registry or affect settings in another project.
func BuiltinThemes() []document.Theme {
	standard := DefaultTheme()
	paper := document.Theme{ID: "paper", Name: "Paper", Description: "A warm reading surface with serif typography.", Mode: "light", Accent: "#9a3412", Font: "serif", Density: "comfortable", Width: "normal", Radius: 4, Light: &document.ThemePalette{Background: "#fffbf5", Surface: "#faf2e6", Text: "#292524", Muted: "#6b6058", Border: "#e7dac9"}, Dark: &document.ThemePalette{Accent: "#fdba74"}}
	midnight := document.Theme{ID: "midnight", Name: "Midnight", Description: "A compact dark workspace with wide content.", Mode: "dark", Accent: "#a5b4fc", Font: "system", Density: "compact", Width: "wide", Radius: 8, Light: &document.ThemePalette{Accent: "#4338ca"}, Dark: &document.ThemePalette{Background: "#11121b", Surface: "#191b29", Text: "#e9e9f4", Muted: "#aaabc5", Border: "#30334b"}}
	return []document.Theme{standard, paper, midnight}
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
	font := `ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif`
	if a.Theme.Font == "serif" {
		font = `Georgia,"Times New Roman",serif`
	}
	if a.Theme.Font == "mono" {
		font = `ui-monospace,SFMono-Regular,Consolas,monospace`
	}
	width := "900px"
	if a.Theme.Width == "wide" {
		width = "1200px"
	}
	css := ":root{--docs-radius:" + strconv.Itoa(a.Theme.Radius) + "px;--docs-font:" + font + ";--docs-article-width:" + width + "}"
	css += paletteCSS(":root", a.Theme.Light, a.Theme.Accent)
	css += "@media(prefers-color-scheme:dark){" + paletteCSS(":root:not([data-theme=light])", a.Theme.Dark, a.Theme.Accent) + "}"
	css += paletteCSS(":root[data-theme=light]", a.Theme.Light, a.Theme.Accent) + paletteCSS(":root[data-theme=dark]", a.Theme.Dark, a.Theme.Accent)
	css += "body{font-family:var(--docs-font)}article{max-width:var(--docs-article-width)}button,input[type=search],pre,.docs-callout,.docs-tab-panel:last-child{border-radius:var(--docs-radius)}.brand-logo{max-width:180px;max-height:40px;object-fit:contain}.page-sequence{display:flex;justify-content:space-between;gap:20px;margin-top:40px}.page-sequence a{max-width:48%;overflow-wrap:anywhere}.page-sequence .next{margin-inline-start:auto;text-align:end}"
	if !a.Navigation.ShowTOC {
		css += ".layout{grid-template-columns:265px minmax(0,1fr)}.toc{display:none}@media(max-width:1150px){.layout{grid-template-columns:240px minmax(0,1fr)}}@media(max-width:760px){.layout{display:block}}"
	}
	if !a.Navigation.StickyHeader {
		css += ".topbar{position:static}@media(min-width:761px){.sidebar,.toc{top:0;height:100vh}}"
	}
	if a.Theme.Density == "compact" {
		css += "body{font-size:15px;line-height:1.6}main{padding-top:26px}.navigation a{padding:5px 10px}pre{padding:16px}h1{margin-bottom:18px}.breadcrumb{margin-bottom:20px}"
	}
	return css + "\n"
}
