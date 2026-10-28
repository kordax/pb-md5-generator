package studio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/kordax/pb-md5-generator/engine"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"golang.org/x/net/html"
)

func canAccessSource(user *auth.User, action auth.Action, name string) bool {
	for _, segment := range strings.Split(name, "/") {
		if strings.HasPrefix(segment, ".") || strings.EqualFold(segment, ".git") {
			return false
		}
	}
	first, _, _ := strings.Cut(name, "/")
	if (strings.EqualFold(name, "docs.json") || strings.EqualFold(first, "themes")) && !auth.Can(user, auth.ActionAdmin, ".") {
		return false
	}
	return auth.Can(user, action, name)
}

func readSource(rootDir, name string) ([]byte, error) {
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, content.MaxTextBytes+1))
	if err == nil && len(data) > content.MaxTextBytes {
		err = content.ErrLimit
	}
	return data, err
}

func readProjectManifest(rootDir, manifestPath string) (site.Manifest, error) {
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return site.Manifest{}, err
	}
	defer root.Close()
	return site.ReadManifest(root, filepath.Base(manifestPath))
}

func previewDocument(ctx context.Context, contents *content.Service, page apiweb.Document, source []byte) (apiweb.Preview, error) {
	var result apiweb.Preview
	err := contents.View(ctx, func(ctx context.Context, root, manifest string) error {
		user, err := apiweb.Principal(ctx)
		if err != nil || user == nil {
			return auth.ErrUnauthorized
		}
		if !canAccessSource(user, auth.ActionRead, page.Path) {
			return auth.ErrForbidden
		}
		preview, err := site.CompilePreviewContext(ctx, manifest, site.Page{ID: page.ID, Title: page.Title, Source: page.Path, Path: page.OutputPath, Kind: page.Kind}, source, site.PreviewOptions{
			Compiler: compilerOptions(engine.DefaultCodegenSeed),
			CanRead:  func(name string) bool { return canAccessSource(user, auth.ActionRead, name) },
		})
		if errors.Is(err, site.ErrPreviewAccess) {
			return auth.ErrForbidden
		}
		if err != nil {
			message := strings.ReplaceAll(err.Error(), root, "<project>")
			result.Diagnostics = []apiweb.Diagnostic{{File: page.Path, Message: message}}
			return fmt.Errorf("%w: %s", content.ErrInvalid, message)
		}
		// The HTTP composition resolves srcdoc URLs against the publication path.
		result.HTML = string(preview.HTML)
		result.Diagnostics = []apiweb.Diagnostic{}
		return nil
	})
	return result, err
}

func validateMutation(ctx context.Context, snapshot string, changes []content.Change, actor content.Actor, authentication *auth.Service, publishedRoot string) error {
	user, err := apiweb.Principal(ctx)
	if err != nil {
		return auth.ErrUnauthorized
	}
	if authentication == nil {
		return auth.ErrForbidden
	}
	if user == nil {
		if actor.ID == "system" {
			return nil
		}
		return auth.ErrForbidden
	}
	// Repository rebuilds have no document author. Recheck administrator
	// rights and root grants after waiting for the content gate.
	if actor.ID == "" && len(changes) == 0 {
		return authorizeStorage(ctx, actor)
	}
	if user.ID != actor.ID {
		return auth.ErrForbidden
	}
	for _, change := range changes {
		if !canAccessSource(user, auth.ActionWrite, change.Path) || (change.To != "" && !canAccessSource(user, auth.ActionWrite, change.To)) {
			return auth.ErrForbidden
		}
	}
	manifestPath := filepath.Join(snapshot, "docs.json")
	manifest, err := readProjectManifest(snapshot, manifestPath)
	if err != nil {
		return err
	}
	previous, previousErr := readProjectManifest(publishedRoot, filepath.Join(publishedRoot, "docs.json"))
	if previousErr != nil {
		if err := authorizeStorage(ctx, actor); err != nil {
			return err
		}
	} else if err := authorizeNavigationChange(ctx, previous, manifest, actor); err != nil {
		return err
	}
	oldPages := make(map[string]site.Page, len(previous.Pages))
	for _, page := range previous.Pages {
		oldPages[page.ID] = page
	}
	primarySources := make(map[string]bool, len(previous.Pages)+len(manifest.Pages))
	for _, page := range previous.Pages {
		primarySources[page.Source] = true
	}
	for _, page := range manifest.Pages {
		primarySources[page.Source] = true
	}
	var secondaryChanges []content.Change
	for _, change := range changes {
		if change.Path == "docs.json" || sourceUnder(change.Path, "themes") || primarySources[change.Path] {
			continue
		}
		switch strings.ToLower(filepath.Ext(change.Path)) {
		case "", ".md", ".markdown", ".proto", ".json", ".yaml", ".yml", ".html", ".htm", ".txt":
			secondaryChanges = append(secondaryChanges, change)
		}
	}
	newPages := make(map[string]site.Page, len(manifest.Pages))
	for _, page := range manifest.Pages {
		newPages[page.ID] = page
		old, exists := oldPages[page.ID]
		metadataChanged := previousErr != nil || !exists || old != page
		changed := metadataChanged
		if metadataChanged && (!canAccessSource(user, auth.ActionWrite, page.Source) || (exists && !canAccessSource(user, auth.ActionWrite, old.Source))) {
			return auth.ErrForbidden
		}
		for _, change := range changes {
			changed = changed || sourceUnder(page.Source, change.Path) || (change.To != "" && sourceUnder(page.Source, change.To))
		}
		if !changed && len(secondaryChanges) > 0 && canAccessSource(user, auth.ActionRead, page.Source) {
			changed, err = dependencyChanged(ctx, publishedRoot, old, secondaryChanges)
			if err != nil {
				return err
			}
		}
		if !changed {
			continue
		}
		if !canAccessSource(user, auth.ActionRead, page.Source) {
			return auth.ErrForbidden
		}
		source, err := readSource(snapshot, page.Source)
		if err != nil {
			return err
		}
		_, err = site.CompilePreviewContext(ctx, manifestPath, page, source, site.PreviewOptions{
			Compiler: compilerOptions(engine.DefaultCodegenSeed),
			CanRead:  func(name string) bool { return canAccessSource(user, auth.ActionRead, name) },
		})
		if errors.Is(err, site.ErrPreviewAccess) {
			return auth.ErrForbidden
		}
		if err != nil {
			return err
		}
	}
	for id, old := range oldPages {
		if _, exists := newPages[id]; !exists && !canAccessSource(user, auth.ActionWrite, old.Source) {
			return auth.ErrForbidden
		}
	}
	return ctx.Err()
}

// dependencyChanged reads the previous publication as the service, discarding
// its content. Only consumers of changed secondary sources need to be compiled
// again with the author's grants; unreadable consumers never expose their data.
func dependencyChanged(ctx context.Context, root string, page site.Page, changes []content.Change) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	source, err := readSource(root, page.Source)
	if err != nil {
		return true, nil
	}
	previous, err := site.CompilePreviewContext(ctx, filepath.Join(root, "docs.json"), page, source, site.PreviewOptions{Compiler: compilerOptions(engine.DefaultCodegenSeed)})
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return true, nil
	}
	for _, name := range previous.Sources {
		for _, change := range changes {
			if sourceUnder(name, change.Path) || (change.To != "" && sourceUnder(name, change.To)) {
				return true, nil
			}
		}
	}
	return false, nil
}

func sourceUnder(name, parent string) bool {
	for {
		namePart, nameRest, nameMore := strings.Cut(name, "/")
		parentPart, parentRest, parentMore := strings.Cut(parent, "/")
		if !strings.EqualFold(namePart, parentPart) {
			return false
		}
		if !parentMore {
			return true
		}
		if !nameMore {
			return false
		}
		name, parent = nameRest, parentRest
	}
}

// resolvePreviewURLs produces only a same-origin path for relative references.
// Absolute links remain subject to the compiler's URL policy and iframe CSP.
func resolvePreviewURLs(source []byte, basePath, outputPath string) (string, error) {
	document, err := html.Parse(bytes.NewReader(source))
	if err != nil {
		return "", err
	}
	base := &url.URL{Path: strings.TrimSuffix(basePath, "/") + "/" + outputPath}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		for i := range node.Attr {
			attribute := &node.Attr[i]
			if attribute.Key != "href" && attribute.Key != "src" && attribute.Key != "data-search" {
				continue
			}
			link, parseErr := url.Parse(attribute.Val)
			if parseErr == nil && !link.IsAbs() && link.Host == "" && link.Path != "" {
				attribute.Val = base.ResolveReference(link).String()
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	var rendered bytes.Buffer
	if err := html.Render(&rendered, document); err != nil {
		return "", fmt.Errorf("render preview URLs: %w", err)
	}
	return rendered.String(), nil
}
