package site

import (
	"context"
	"fmt"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/uiassets"
)

// ProjectModel indexes an already validated manifest without reading any source
// or output files. A CMS reader can pass its authorized subset of pages/assets.
func ProjectModel(manifest Manifest) document.Project {
	project := document.Project{Schema: "schema/project-v1.json", Format: "pb-docs/project", Version: document.FormatVersion, Title: manifest.Title, Description: manifest.Description, Source: document.Location{File: "docs.json"}, Root: "../", Pages: make([]document.PageReference, 0, len(manifest.Pages)), Logo: manifest.Logo, Favicon: manifest.Favicon, Footer: manifest.Footer, ThemeID: manifest.ThemeID, Theme: manifest.Theme, Navigation: manifest.Navigation, Editor: manifest.Editor, API: manifest.API, Publishing: manifest.Publishing}
	for _, page := range manifest.Pages {
		project.Pages = append(project.Pages, document.PageReference{ID: page.ID, Title: page.Title, Path: page.Path, Source: document.Location{File: page.Source}, Kind: page.Kind, Data: strings.TrimPrefix(pageDataPath(page), "_docs/")})
	}
	return project
}

// EncodePageModel preflights escaped JSON size before encoding a page. It uses
// the same 16 MiB budget as static publication and normalizes an empty block list.
func EncodePageModel(ctx context.Context, page document.Page) ([]byte, error) {
	if page.Blocks == nil {
		page.Blocks = []document.Block{}
	}
	return encodeModelContext(ctx, page, maxPageDataBytes)
}

// EncodeProjectModel applies the static project index's 8 MiB encoded budget.
func EncodeProjectModel(ctx context.Context, project document.Project) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("model encoding context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if project.Navigation != nil && project.Navigation.Tree != nil {
		if len(project.Pages) > maxPages {
			return nil, fmt.Errorf("navigation page catalog exceeds its limit")
		}
		pages := make([]Page, 0, len(project.Pages))
		for _, page := range project.Pages {
			pages = append(pages, Page{ID: page.ID, Title: page.Title, Path: page.Path})
		}
		if err := ValidateNavigation(project.Navigation.Tree, pages); err != nil {
			return nil, err
		}
	}
	return encodeModelContext(ctx, project, maxSearchBytes)
}

func encodeModelContext(ctx context.Context, value any, limit int) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("model encoding context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := marshalModel(value, limit)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

// ReaderResource serves only compiled reader assets and portable schemas. It
// never reads source files or a published project index, so a permission-aware
// CMS reader can use it without exposing metadata from another user's pages.
func ReaderResource(name string, manifest Manifest) ([]byte, string, bool) {
	var data, media string
	switch name {
	case "_docs/site.css":
		data, media = siteCSS, "text/css; charset=utf-8"
	case "_docs/site.js":
		data, media = siteJS, "application/javascript; charset=utf-8"
	case "_docs/api.css":
		data, media = apiCSS, "text/css; charset=utf-8"
	case "_docs/api-client.js":
		data, media = apiClientJS, "application/javascript; charset=utf-8"
	case "_docs/appearance.css":
		if manifest.ValidateAppearance() != nil {
			return nil, "", false
		}
		data, media = appearanceCSS(manifest), "text/css; charset=utf-8"
	default:
		if strings.HasPrefix(name, "_docs/") {
			if data, media, ok := uiassets.Resource(strings.TrimPrefix(name, "_docs/")); ok {
				return data, media, true
			}
		}
		for _, kind := range document.SchemaNames() {
			if name != "_docs/schema/"+kind+"-v1.json" {
				continue
			}
			data, err := document.JSONSchema(kind)
			if err != nil {
				return nil, "", false
			}
			return data, "application/schema+json; charset=utf-8", true
		}
		return nil, "", false
	}
	return []byte(data), media, true
}
