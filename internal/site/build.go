package site

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
)

const inventoryPath = "_docs/build.json"

// Options controls a build. ImportProto receives the already bounded source
// snapshot, and must return Markdown without resolving remote dependencies.
type Options struct {
	Check       bool
	ImportProto func(name string, source []byte) ([]byte, error)
	ImportPage  func(ctx context.Context, kind, name string, source []byte, load func(string) ([]byte, error)) (ImportResult, error)
}

type searchEntry struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}

type inventory struct {
	Format string   `json:"format"`
	Files  []string `json:"files"`
}

// Build compiles one page at a time and replaces only an empty or managed output
// directory after the entire build succeeds. Check never creates or writes files.
func Build(configPath, output string, options Options) error {
	return BuildContext(context.Background(), configPath, output, options)
}

// BuildContext supports cooperative cancellation between bounded compiler phases.
func BuildContext(ctx context.Context, configPath, output string, options Options) error {
	if ctx == nil {
		return fmt.Errorf("build context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(configPath))
	if err != nil {
		return fmt.Errorf("open project: %w", err)
	}
	defer root.Close()
	manifest, err := loadManifest(root, filepath.Base(configPath))
	if err != nil {
		return err
	}
	output, err = validateOutput(root.Name(), output, manifest)
	if err != nil {
		return err
	}
	resolvedConfig, err := filepath.EvalSymlinks(configPath)
	if err != nil {
		return fmt.Errorf("resolve project manifest: %w", err)
	}
	if within(output, resolvedConfig) {
		return fmt.Errorf("output contains project manifest")
	}
	if err := validateManagedOutput(output); err != nil {
		return err
	}

	stage := ""
	if !options.Check {
		if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
			return err
		}
		stage, err = os.MkdirTemp(filepath.Dir(output), ".docs-build-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
	}
	files := []string{inventoryPath}
	write := func(name string, data []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		files = append(files, name)
		if options.Check {
			return compareFile(output, name, data)
		}
		return stageFile(stage, name, data)
	}
	project := ProjectModel(manifest)
	project.Source = document.Location{File: filepath.Base(configPath)}
	hasAPI, interactive := false, false
	metadata := make([]pageContent, len(manifest.Pages))
	search := make([]searchEntry, 0, len(manifest.Pages))
	searchSize := 0
	linkCount, linkBytes := 0, 0
	for i, page := range manifest.Pages {
		content, api, err := compileSourcePage(ctx, root, output, i, manifest, options)
		if err != nil {
			return fmt.Errorf("page %s: %w", page.Source, err)
		}
		linkCount += len(content.Anchors) + len(content.References)
		for anchor := range content.Anchors {
			linkBytes += len(anchor)
		}
		for _, ref := range content.References {
			linkBytes += len(ref.fragment)
		}
		if linkCount > maxProjectLinks || linkBytes > maxLinkBytes {
			return fmt.Errorf("project link metadata exceeds %d entries or %d bytes", maxProjectLinks, maxLinkBytes)
		}
		html, err := renderPage(manifest, i, content)
		if err != nil {
			return fmt.Errorf("render page %s: %w", page.Source, err)
		}
		if err := write(page.Path, html); err != nil {
			return err
		}
		if content.Blocks == nil {
			content.Blocks = []document.Block{}
		}
		pageModel := document.Page{Schema: "../schema/page-v1.json", Format: "pb-docs/page", Version: document.FormatVersion, ID: page.ID, Title: page.Title, Path: page.Path, Source: document.Location{File: page.Source}, Kind: page.Kind, Blocks: content.Blocks, API: api}
		data, err := marshalModel(pageModel, maxPageDataBytes)
		if err != nil {
			return fmt.Errorf("export page %s: %w", page.Source, err)
		}
		if err := write(pageDataPath(page), data); err != nil {
			return err
		}
		hasAPI, interactive = hasAPI || api != nil, interactive || content.Interactive
		entry := searchEntry{Title: page.Title, URL: page.Path, Text: content.Text}
		searchSize += len(entry.Text) + len(entry.Title) + len(entry.URL)
		if searchSize > maxSearchBytes {
			return fmt.Errorf("search index exceeds %d bytes; split the project", maxSearchBytes)
		}
		search = append(search, entry)
		// Release page HTML and text before compiling the next source. Keep only
		// anchors/references until forward links have been checked.
		metadata[i] = pageContent{Anchors: content.Anchors, References: content.References}
	}
	for i, content := range metadata {
		for _, ref := range content.References {
			if ref.fragment != "" && !metadata[ref.page].Anchors[ref.fragment] {
				return fmt.Errorf("page %s: missing anchor #%s in %s", manifest.Pages[i].Source, ref.fragment, manifest.Pages[ref.page].Source)
			}
		}
	}
	index, err := json.Marshal(search)
	if err != nil {
		return err
	}
	if len(index) > maxSearchBytes {
		return fmt.Errorf("encoded search index exceeds %d bytes", maxSearchBytes)
	}
	if err := write("_docs/search.json", index); err != nil {
		return err
	}
	projectData, err := marshalModel(project, maxSearchBytes)
	if err != nil {
		return err
	}
	if err := write("_docs/project.json", projectData); err != nil {
		return err
	}
	for _, kind := range document.SchemaNames() {
		schema, err := document.JSONSchema(kind)
		if err != nil {
			return err
		}
		if err := write("_docs/schema/"+kind+"-v1.json", schema); err != nil {
			return err
		}
	}
	if hasAPI {
		if err := write("_docs/api.css", []byte(apiCSS)); err != nil {
			return err
		}
	}
	if interactive {
		if err := write("_docs/api-client.js", []byte(apiClientJS)); err != nil {
			return err
		}
	}
	if err := write("_docs/appearance.css", []byte(appearanceCSS(manifest))); err != nil {
		return err
	}
	for name, content := range map[string]string{"_docs/site.css": siteCSS, "_docs/site.js": siteJS} {
		if err := write(name, []byte(content)); err != nil {
			return err
		}
	}
	for _, asset := range manifest.Assets {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := readLimited(root, asset, maxAssetBytes)
		if err != nil {
			return fmt.Errorf("asset %s: %w", asset, err)
		}
		if err := write("assets/"+asset, data); err != nil {
			return err
		}
	}
	sort.Strings(files)
	build, err := json.Marshal(inventory{Format: "pb-docs-site/v1", Files: files})
	if err != nil {
		return err
	}
	if options.Check {
		if err := compareFile(output, inventoryPath, build); err != nil {
			return err
		}
		return checkFileSet(output, files)
	}
	if err := stageFile(stage, inventoryPath, build); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publishStage(stage, output)
}

func validateOutput(project, output string, manifest Manifest) (string, error) {
	if output == "" {
		return "", fmt.Errorf("output directory is required")
	}
	absolute, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(absolute); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("output must not be a symlink")
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		return "", err
	}
	// Resolve an existing ancestor so parent-directory symlinks cannot turn an
	// apparently separate destination into the project or one of its ancestors.
	ancestor := absolute
	var suffix []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(ancestor)
		if resolveErr == nil {
			absolute = filepath.Join(append([]string{resolved}, suffix...)...)
			break
		}
		if !errors.Is(resolveErr, fs.ErrNotExist) || filepath.Dir(ancestor) == ancestor {
			return "", resolveErr
		}
		suffix = append([]string{filepath.Base(ancestor)}, suffix...)
		ancestor = filepath.Dir(ancestor)
	}
	if within(absolute, project) {
		return "", fmt.Errorf("output cannot replace the project or a parent directory")
	}
	for _, name := range append(pageSources(manifest), manifest.Assets...) {
		source, err := filepath.EvalSymlinks(filepath.Join(project, filepath.FromSlash(name)))
		if err != nil {
			return "", fmt.Errorf("source %s: %w", name, err)
		}
		if within(absolute, source) {
			return "", fmt.Errorf("output contains source %s", name)
		}
	}
	return absolute, nil
}

func pageSources(manifest Manifest) []string {
	sources := make([]string, 0, len(manifest.Pages))
	for _, page := range manifest.Pages {
		sources = append(sources, page.Source)
	}
	return sources
}

func within(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func stageFile(directory, name string, data []byte) error {
	file := filepath.Join(directory, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o600)
}

func compareFile(directory, name string, expected []byte) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("site is out of date: %w", err)
	}
	defer root.Close()
	actual, err := readLimited(root, name, int64(len(expected)))
	if err != nil || !bytes.Equal(actual, expected) {
		return fmt.Errorf("site is out of date: %s", name)
	}
	return nil
}
