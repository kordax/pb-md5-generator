// Package uiassets contains shared, self-hosted reader and administration assets.
package uiassets

import (
	"embed"
	"strings"
)

//go:embed fonts/*.woff2 fonts/OFL-DM-Sans.txt fonts/OFL-IBM-Plex.txt fonts/SOURCES.md
var files embed.FS

// Names returns the fixed resource names distributed with both interfaces.
func Names() []string {
	return []string{
		"fonts/dm-sans-latin.woff2",
		"fonts/dm-sans-latin-ext.woff2",
		"fonts/ibm-plex-mono-latin.woff2",
		"fonts/ibm-plex-mono-cyrillic.woff2",
		"fonts/ibm-plex-mono-medium-latin.woff2",
		"fonts/ibm-plex-mono-medium-cyrillic.woff2",
		"fonts/OFL-DM-Sans.txt",
		"fonts/OFL-IBM-Plex.txt",
		"fonts/SOURCES.md",
	}
}

// Resource reads only an explicitly distributed asset, never arbitrary paths.
func Resource(name string) ([]byte, string, bool) {
	for _, allowed := range Names() {
		if name != allowed {
			continue
		}
		data, err := files.ReadFile(name)
		if err != nil {
			return nil, "", false
		}
		media := "text/plain; charset=utf-8"
		if strings.HasSuffix(name, ".woff2") {
			media = "font/woff2"
		} else if strings.HasSuffix(name, ".md") {
			media = "text/markdown; charset=utf-8"
		}
		return data, media, true
	}
	return nil, "", false
}
