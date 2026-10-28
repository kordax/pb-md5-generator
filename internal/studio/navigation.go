package studio

import (
	"context"
	"reflect"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/site"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

func manifestNavigationTree(manifest site.Manifest) *document.NavigationTree {
	if manifest.Navigation == nil {
		return nil
	}
	return manifest.Navigation.Tree
}

func navigationPageIDs(pages []site.Page) map[string]bool {
	ids := make(map[string]bool, len(pages))
	for _, page := range pages {
		ids[page.ID] = true
	}
	return ids
}

func filterReaderNavigation(manifest *site.Manifest) error {
	if manifest.Navigation == nil || manifest.Navigation.Tree == nil {
		return nil
	}
	tree, err := site.FilterNavigation(manifest.Navigation.Tree, navigationPageIDs(manifest.Pages))
	if err != nil {
		return err
	}
	navigation := *manifest.Navigation
	navigation.Tree = tree
	manifest.Navigation = &navigation
	return nil
}

func authorizeNavigationChange(ctx context.Context, previous, candidate site.Manifest, actor content.Actor) error {
	// Deleting an authorized document also removes its menu entry. Other
	// changes to the shared tree require access to the complete project.
	pruned, err := site.PruneNavigation(manifestNavigationTree(previous), navigationPageIDs(candidate.Pages))
	if err != nil {
		return err
	}
	if reflect.DeepEqual(pruned, manifestNavigationTree(candidate)) {
		return nil
	}
	return authorizeStorage(ctx, actor)
}
