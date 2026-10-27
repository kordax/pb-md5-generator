package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
)

const (
	maxImportedDocuments = 64
	maxImportedBytes     = 16 << 20
	maxPageDataBytes     = 16 << 20
)

// ImportResult joins an optional readable Markdown fallback and a typed API.
// Imported snapshots and the injected loader are read-only and source-local.
type ImportResult struct {
	Markdown []byte
	API      *document.API
}

func pageDataPath(page Page) string {
	digest := sha256.Sum256([]byte(pageIdentity(page)))
	return "_docs/pages/" + hex.EncodeToString(digest[:16]) + ".json"
}

func compileSourcePage(ctx context.Context, root *os.Root, output string, current int, manifest Manifest, options Options) (pageContent, *document.API, error) {
	if err := ctx.Err(); err != nil {
		return pageContent{}, nil, err
	}
	page := manifest.Pages[current]
	source, err := readLimited(root, page.Source, maxSourceBytes)
	if err != nil {
		return pageContent{}, nil, err
	}
	var api *document.API
	markdown := source
	if page.Kind != "markdown" && page.Kind != "" {
		if options.ImportPage != nil {
			load := confinedSourceLoader(ctx, root, output, page.Source, source)
			result, err := options.ImportPage(ctx, page.Kind, page.Source, source, load)
			if err != nil {
				return pageContent{}, nil, fmt.Errorf("import %s: %w", page.Source, err)
			}
			markdown, api = result.Markdown, result.API
			if page.Kind == "openapi" && api == nil {
				return pageContent{}, nil, fmt.Errorf("openapi importer returned no typed API")
			}
		} else if page.Kind == "protobuf" && options.ImportProto != nil {
			markdown, err = options.ImportProto(page.Source, source)
			if err != nil {
				return pageContent{}, nil, fmt.Errorf("import %s: %w", page.Source, err)
			}
		} else {
			return pageContent{}, nil, fmt.Errorf("%s importer is unavailable", page.Kind)
		}
		if len(markdown) > maxSourceBytes {
			return pageContent{}, nil, fmt.Errorf("generated page exceeds %d bytes", maxSourceBytes)
		}
	}
	if err := ctx.Err(); err != nil {
		return pageContent{}, nil, err
	}
	if api == nil {
		var load func(string) ([]byte, error)
		if page.Kind == "markdown" || page.Kind == "" {
			load = confinedSourceLoader(ctx, root, output, page.Source, source)
		}
		content, err := compileMarkdownSource(ctx, markdown, current, manifest, load)
		return content, nil, err
	}
	content, err := renderAPI(ctx, api, current, manifest, pageDataPath(page))
	if err != nil {
		return pageContent{}, nil, err
	}
	if len(markdown) > 0 {
		legacy, err := compileMarkdown(markdown, current, manifest)
		if err != nil {
			return pageContent{}, nil, err
		}
		if err := mergePageContent(&content, legacy); err != nil {
			return pageContent{}, nil, err
		}
		content.HTML += "<details class=\"api-source-reference\"><summary>Source reference and examples</summary>" + legacy.HTML + "</details>"
		content.Blocks = legacy.Blocks
	}
	if len(content.HTML) > maxRenderedBytes {
		return pageContent{}, nil, fmt.Errorf("API reference exceeds %d rendered bytes", maxRenderedBytes)
	}
	if len(content.Headings) > maxPageHeadings {
		return pageContent{}, nil, fmt.Errorf("page exceeds %d headings", maxPageHeadings)
	}
	content.APIData = pageDataPath(page)
	return content, api, nil
}

func confinedSourceLoader(ctx context.Context, root *os.Root, output, primary string, source []byte) func(string) ([]byte, error) {
	loaded := map[string][]byte{primary: source}
	total := len(source)
	return func(name string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !localPath(name) {
			return nil, fmt.Errorf("import source must be a clean project-relative path")
		}
		if data, exists := loaded[name]; exists {
			return data, nil
		}
		if len(loaded) >= maxImportedDocuments {
			return nil, fmt.Errorf("import exceeds %d documents", maxImportedDocuments)
		}
		real, err := filepath.EvalSymlinks(filepath.Join(root.Name(), filepath.FromSlash(name)))
		if err != nil {
			return nil, fmt.Errorf("import source %s: %w", name, err)
		}
		if within(output, real) {
			return nil, fmt.Errorf("output contains import source %s", name)
		}
		data, err := readLimited(root, name, min(int64(maxSourceBytes), int64(maxImportedBytes-total)))
		if err != nil {
			return nil, fmt.Errorf("import source %s: %w", name, err)
		}
		total += len(data)
		loaded[name] = data
		return data, nil
	}
}

func mergePageContent(target *pageContent, extra pageContent) error {
	for anchor := range extra.Anchors {
		if err := addAnchor(target.Anchors, anchor); err != nil {
			return err
		}
	}
	target.Headings = append(target.Headings, extra.Headings...)
	target.References = append(target.References, extra.References...)
	text := target.Text + " " + extra.Text
	if len(text) > 32<<10 {
		text = text[:32<<10]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	target.Text = strings.Clone(text)
	return nil
}
