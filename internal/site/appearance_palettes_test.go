package site

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func paletteContrast(t *testing.T, foreground, background string) float64 {
	t.Helper()
	luminance := func(value string) float64 {
		rgb, err := strconv.ParseUint(strings.TrimPrefix(value, "#"), 16, 32)
		require.NoError(t, err)
		result := 0.0
		for index, weight := range []float64{0.2126, 0.7152, 0.0722} {
			channel := float64((rgb>>uint(16-index*8))&255) / 255
			if channel <= 0.04045 {
				channel /= 12.92
			} else {
				channel = math.Pow((channel+0.055)/1.055, 2.4)
			}
			result += channel * weight
		}
		return result
	}
	a, b := luminance(foreground), luminance(background)
	return (math.Max(a, b) + 0.05) / (math.Min(a, b) + 0.05)
}

func TestBuiltinSchemesAreCompleteAccessibleAndKeepTheWorkspace(t *testing.T) {
	themes := BuiltinThemes()
	require.GreaterOrEqual(t, len(themes), 10)
	ids, names, pairs := map[string]bool{}, map[string]bool{}, map[[2]document.ThemePalette]bool{}
	for _, theme := range themes {
		t.Run(theme.ID, func(t *testing.T) {
			require.False(t, ids[theme.ID], "duplicate theme ID")
			require.False(t, names[theme.Name], "duplicate theme name")
			ids[theme.ID], names[theme.Name] = true, true
			require.NoError(t, ValidateTheme(theme))
			assert.Contains(t, []string{"light", "dark"}, theme.Mode)
			assert.Equal(t, "system", theme.Font)
			assert.Equal(t, "comfortable", theme.Density)
			assert.Equal(t, "normal", theme.Width)
			assert.Equal(t, 5, theme.Radius)
			require.NotNil(t, theme.Light)
			require.NotNil(t, theme.Dark)
			pair := [2]document.ThemePalette{*theme.Light, *theme.Dark}
			assert.False(t, pairs[pair], "schemes must provide different palette pairs")
			pairs[pair] = true
			for mode, palette := range map[string]*document.ThemePalette{"light": theme.Light, "dark": theme.Dark} {
				for _, value := range []string{palette.Background, palette.Surface, palette.Text, palette.Muted, palette.Border, palette.Accent} {
					require.True(t, color(value), "complete %s palette required", mode)
				}
				for _, surface := range []string{palette.Background, palette.Surface} {
					for role, foreground := range map[string]string{"body": palette.Text, "muted": palette.Muted, "link": palette.Accent} {
						assert.GreaterOrEqual(t, paletteContrast(t, foreground, surface), 4.5, "%s %s text", mode, role)
					}
					assert.GreaterOrEqual(t, paletteContrast(t, palette.Accent, surface), 3.0, "%s focus ring", mode)
				}
				assert.GreaterOrEqual(t, paletteContrast(t, palette.Background, palette.Accent), 4.5, "%s primary label on accent", mode)
			}
			manifest := Manifest{Title: "Docs", ThemeID: theme.ID}
			require.NoError(t, manifest.ValidateAppearance())
			assert.Equal(t, theme, manifest.Appearance().Theme)
		})
	}
	assert.Contains(t, ids, "default")
	assert.Contains(t, ids, "paper")
	assert.Contains(t, ids, "midnight")
	assert.Equal(t, "#6557d2", DefaultTheme().Accent)
}

func TestBuiltinSchemeRegistriesAndPalettePointersAreIndependent(t *testing.T) {
	first, expected := BuiltinThemes(), BuiltinThemes()
	for index := range first {
		assert.NotSame(t, first[index].Light, expected[index].Light)
		assert.NotSame(t, first[index].Dark, expected[index].Dark)
		first[index].Name = "Changed"
		first[index].Light.Background = "#123456"
		first[index].Dark.Accent = "#654321"
	}
	assert.Equal(t, expected, BuiltinThemes())
	standard := DefaultTheme()
	standard.Light.Text, standard.Dark.Text = "#123456", "#654321"
	pristine := DefaultTheme()
	assert.NotEqual(t, standard.Light.Text, pristine.Light.Text)
	assert.NotEqual(t, standard.Dark.Text, pristine.Dark.Text)
}

func TestEverySchemeExportsItsDarkWorkbenchInBothReaderModes(t *testing.T) {
	apiBlock := regexp.MustCompile(`:root\{--api-bg:([^}]*)\}`)
	for _, theme := range BuiltinThemes() {
		for _, mode := range []string{"light", "dark", "auto"} {
			t.Run(theme.ID+"/"+mode, func(t *testing.T) {
				theme.Mode = mode
				manifest := Manifest{Title: "Docs", ThemeID: theme.ID, Theme: &theme}
				data, media, ok := ReaderResource("_docs/appearance.css", manifest)
				require.True(t, ok)
				assert.Equal(t, "text/css; charset=utf-8", media)
				blocks := apiBlock.FindAllStringSubmatch(string(data), -1)
				require.Len(t, blocks, 1, "workbench tokens belong to an unconditional root rule")
				tokens := "--api-bg:" + blocks[0][1]
				for variable, value := range map[string]string{"--api-bg": theme.Dark.Background, "--api-panel": theme.Dark.Surface, "--api-text": theme.Dark.Text, "--api-muted": theme.Dark.Muted, "--api-line": theme.Dark.Border, "--api-accent": theme.Dark.Accent, "--api-code": theme.Dark.Surface, "--api-on-accent": theme.Dark.Background} {
					assert.Contains(t, tokens, variable+":"+value+";")
				}
			})
		}
	}
}

func TestSparseCustomPalettesResolveWithoutLosingTypographyOrAccents(t *testing.T) {
	defaults := DefaultTheme()
	custom := defaults
	custom.ID, custom.Name, custom.Accent = "custom", "Custom", "#246844"
	custom.Font, custom.Density, custom.Width, custom.Radius = "serif", "compact", "wide", 12
	custom.Light = nil
	custom.Dark = &document.ThemePalette{Surface: "#19352a", Accent: "#83c99e"}
	light := resolvedPalette(custom.Light, defaults.Light, custom.Accent)
	dark := resolvedPalette(custom.Dark, defaults.Dark, custom.Accent)
	assert.Equal(t, custom.Accent, light.Accent)
	assert.Equal(t, defaults.Light.Background, light.Background)
	assert.Equal(t, defaults.Dark.Background, dark.Background)
	assert.Equal(t, custom.Dark.Surface, dark.Surface)
	assert.Equal(t, custom.Dark.Accent, dark.Accent)
	assert.Empty(t, custom.Dark.Text, "resolution must not mutate authored tokens")
	css := appearanceCSS(Manifest{Title: "Docs", ThemeID: custom.ID, Theme: &custom})
	assert.Contains(t, css, `Georgia,"Times New Roman",serif`)
	assert.Contains(t, css, "--docs-radius:12px")
	assert.Contains(t, css, "--docs-article-width:1200px")
	assert.Contains(t, css, "body{font-size:15px;line-height:1.6}")
	assert.Contains(t, css, "--api-bg:"+dark.Background+";")
	assert.Contains(t, css, "--api-panel:"+dark.Surface+";")
	assert.Contains(t, css, "--api-text:"+dark.Text+";")
	assert.Contains(t, css, "--api-accent:"+dark.Accent+";")
	assert.Contains(t, paletteCSS(":root", nil, custom.Accent), "--accent:"+custom.Accent+";")
	assert.Empty(t, defaults.Light.Accent, "the default constructor preserves the theme accent fallback")
}
