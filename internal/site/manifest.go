// Package site compiles a versioned documentation project to portable static files.
package site

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
)

const (
	maxManifestBytes = 1 << 20
	maxSourceBytes   = 4 << 20
	maxAssetBytes    = 16 << 20
	maxSearchBytes   = 8 << 20
	maxPages         = 2048
	maxAssets        = 256
	maxMarkupUnits   = 50000
	maxPageNodes     = 50000
	maxPageHeadings  = 8192
	maxProjectLinks  = 100000
	maxLinkBytes     = 8 << 20
	maxRenderedBytes = 16 << 20
)

// Manifest is the v1 project contract. Paths use forward slashes on every OS.
type Manifest struct {
	Schema      string                       `json:"$schema,omitempty"`
	Version     int                          `json:"version"`
	Title       string                       `json:"title"`
	Description string                       `json:"description,omitempty"`
	Pages       []Page                       `json:"pages"`
	Assets      []string                     `json:"assets,omitempty"`
	Logo        string                       `json:"logo,omitempty"`
	Favicon     string                       `json:"favicon,omitempty"`
	Footer      string                       `json:"footer,omitempty"`
	ThemeID     string                       `json:"themeId,omitempty"`
	Theme       *document.Theme              `json:"theme,omitempty"`
	Navigation  *document.NavigationSettings `json:"navigation,omitempty"`
	Editor      *document.EditorSettings     `json:"editor,omitempty"`
	API         *document.APISettings        `json:"api,omitempty"`
	Publishing  *document.PublishingSettings `json:"publishing,omitempty"`

	// ReaderStudioURL is set only by the live CMS reader and is never portable project data.
	ReaderStudioURL string `json:"-"`
}

// Page declares a Markdown, protobuf or OpenAPI source and an explicit HTML destination.
type Page struct {
	ID     string `json:"id,omitempty"`
	Title  string `json:"title"`
	Source string `json:"source"`
	Path   string `json:"path"`
	Kind   string `json:"kind,omitempty"`
}

func loadManifest(root *os.Root, name string) (Manifest, error) {
	data, err := readLimited(root, name, maxManifestBytes)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	return DecodeManifest(data)
}

// DecodeManifest applies the authoring source contract used by static builds
// and the CMS. Null optional pointer groups follow omitted-group defaults;
// HTTP request payloads have their own stricter decoding rules.
func DecodeManifest(data []byte) (Manifest, error) {
	if len(data) > maxManifestBytes {
		return Manifest{}, fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes)
	}
	if !utf8.Valid(data) {
		return Manifest{}, fmt.Errorf("manifest is not valid UTF-8")
	}
	if err := validateJSON(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Manifest{}, fmt.Errorf("manifest must contain exactly one JSON object")
	}
	if err := manifest.validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m *Manifest) validate() error {
	if m.Version != 1 {
		return fmt.Errorf("unsupported manifest version %d; expected 1", m.Version)
	}
	if strings.TrimSpace(m.Title) == "" || len(m.Title) > 256 {
		return fmt.Errorf("manifest title must contain 1 to 256 bytes")
	}
	if len(m.Description) > 4096 {
		return fmt.Errorf("manifest description exceeds 4096 bytes")
	}
	if len(m.Pages) == 0 || len(m.Pages) > maxPages {
		return fmt.Errorf("manifest must declare 1 to %d pages", maxPages)
	}
	paths := make(map[string]bool, len(m.Pages))
	identities := make(map[string]bool, len(m.Pages))
	sources := make(map[string]bool, len(m.Pages))
	for i := range m.Pages {
		page := &m.Pages[i]
		if strings.TrimSpace(page.Title) == "" || len(page.Title) > 256 {
			return fmt.Errorf("page %d: title must contain 1 to 256 bytes", i+1)
		}
		if !localPath(page.Source) {
			return fmt.Errorf("page %d: source must be a clean relative path", i+1)
		}
		if !outputPath(page.Path) {
			return fmt.Errorf("page %d: path must be a lowercase relative .html path", i+1)
		}
		if paths[page.Path] || sources[page.Source] {
			return fmt.Errorf("page %d: duplicate path or source", i+1)
		}
		paths[page.Path], sources[page.Source] = true, true
		if page.ID == "" {
			page.ID = document.PageID(page.Path)
		}
		if !validPageID(page.ID) || identities[page.ID] {
			return fmt.Errorf("page %d: invalid or duplicate id %q", i+1, page.ID)
		}
		identities[page.ID] = true
		if page.Kind == "" {
			page.Kind = "markdown"
		}
		if page.Kind != "markdown" && page.Kind != "html" && page.Kind != "protobuf" && page.Kind != "openapi" {
			return fmt.Errorf("page %d: unsupported kind %q", i+1, page.Kind)
		}
	}
	if !paths["index.html"] {
		return fmt.Errorf("manifest must declare an index.html homepage")
	}
	if len(m.Assets) > maxAssets {
		return fmt.Errorf("manifest exceeds %d assets", maxAssets)
	}
	seenAssets := make(map[string]bool, len(m.Assets))
	for _, asset := range m.Assets {
		if !localPath(asset) || seenAssets[strings.ToLower(asset)] {
			return fmt.Errorf("invalid or duplicate asset %q", asset)
		}
		switch strings.ToLower(path.Ext(asset)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico", ".pdf", ".txt", ".json", ".yaml", ".yml", ".proto":
		default:
			return fmt.Errorf("asset %q: unsupported extension", asset)
		}
		seenAssets[strings.ToLower(asset)] = true
	}
	return m.ValidateAppearance()
}

// ReadManifest reads and validates a confined portable project manifest.
func ReadManifest(root *os.Root, name string) (Manifest, error) {
	return loadManifest(root, name)
}

// ValidateManifest validates and normalizes IDs and source kinds in a manifest.
func ValidateManifest(manifest *Manifest) error {
	if manifest == nil {
		return fmt.Errorf("manifest is required")
	}
	return manifest.validate()
}

func localPath(value string) bool {
	if len(value) > 4096 || !fs.ValidPath(value) || value == "." || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 32 || strings.ContainsRune("\\:?*\"<>|#%", r) {
			return false
		}
	}
	for _, segment := range strings.Split(value, "/") {
		if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
		switch base {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
			return false
		}
	}
	return true
}

func outputPath(value string) bool {
	if !localPath(value) || !strings.HasSuffix(value, ".html") || strings.HasPrefix(value, "_docs/") || strings.HasPrefix(value, "assets/") {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("/-_.", r) {
			return false
		}
	}
	return true
}

func readLimited(root *os.Root, name string, limit int64) ([]byte, error) {
	// Reject special files before opening: opening a FIFO could otherwise block.
	info, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular file of at most %d bytes", name, limit)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular file of at most %d bytes", name, limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	return data, nil
}
