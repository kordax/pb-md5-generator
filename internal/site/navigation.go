package site

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
)

const (
	MaxNavigationDepth            = 8
	MaxNavigationNodes            = 4096
	MaxNavigationBytes            = 256 << 10
	MaxNavigationLabelBytes       = 256
	MaxNavigationDescriptionBytes = 512
)

// NavigationItem is a renderer view. Only page references have a Path; groups
// cannot introduce URLs. Active groups contain the current visible page.
type NavigationItem struct {
	Kind        string
	ID          string
	PageID      string
	Path        string
	Title       string
	Description string
	Collapsed   bool
	Current     bool
	Active      bool
	Children    []NavigationItem
}

type NavigationView struct {
	Items       []NavigationItem
	Pages       []NavigationItem
	Breadcrumbs []NavigationItem
}

func navigationText(value string, limit int, required, multiline bool) bool {
	if !utf8.ValidString(value) || len(value) > limit || required && strings.TrimSpace(value) == "" {
		return false
	}
	if value != "" && strings.TrimSpace(value) == "" && !multiline {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) && (!multiline || char != '\n' && char != '\t') {
			return false
		}
	}
	return true
}

func validateNavigationShape(tree *document.NavigationTree) error {
	if tree == nil {
		return nil
	}
	if tree.Items == nil || tree.UnlistedPages != "" && tree.UnlistedPages != "append" && tree.UnlistedPages != "omit" {
		return fmt.Errorf("navigation requires an items array and an append or omit unlistedPages policy")
	}
	count := 0
	groups, pages := make(map[string]bool), make(map[string]bool)
	active := make(map[*document.NavigationNode]bool)
	var visit func([]document.NavigationNode, int) error
	visit = func(nodes []document.NavigationNode, depth int) error {
		if depth > MaxNavigationDepth || len(nodes) > MaxNavigationNodes-count {
			return fmt.Errorf("navigation exceeds its depth or node limit")
		}
		for i := range nodes {
			node := &nodes[i]
			if active[node] {
				return fmt.Errorf("navigation must not contain cycles")
			}
			count++
			if count > MaxNavigationNodes {
				return fmt.Errorf("navigation exceeds its node limit")
			}
			if !navigationText(node.Title, MaxNavigationLabelBytes, node.Kind == "group", false) || !navigationText(node.Description, MaxNavigationDescriptionBytes, false, true) {
				return fmt.Errorf("navigation labels or descriptions are invalid")
			}
			switch node.Kind {
			case "page":
				if !validPageID(node.PageID) || pages[node.PageID] || node.ID != "" || node.Description != "" || node.Collapsed != nil || node.Children != nil {
					return fmt.Errorf("navigation page references must be unique and contain only page fields")
				}
				pages[node.PageID] = true
			case "group":
				if !validThemeID(node.ID) || groups[node.ID] || node.PageID != "" || len(node.Children) == 0 {
					return fmt.Errorf("navigation groups require unique safe IDs, labels and children")
				}
				groups[node.ID] = true
				active[node] = true
				if err := visit(node.Children, depth+1); err != nil {
					return err
				}
				delete(active, node)
			default:
				return fmt.Errorf("navigation node kind must be page or group")
			}
		}
		return nil
	}
	if err := visit(tree.Items, 1); err != nil {
		return err
	}
	// Shape, string and cycle limits above bound this allocation even for Go
	// callers. JSON escaping is included in the final encoded-byte limit.
	data, err := json.Marshal(tree)
	if err != nil || len(data) > MaxNavigationBytes {
		return fmt.Errorf("navigation exceeds its encoded byte limit")
	}
	return nil
}

func navigationPageMap(pages []Page) (map[string]Page, error) {
	if len(pages) > maxPages {
		return nil, fmt.Errorf("navigation page catalog exceeds its limit")
	}
	result := make(map[string]Page, len(pages))
	paths := make(map[string]bool, len(pages))
	for _, page := range pages {
		page.ID = pageIdentity(page)
		if !validPageID(page.ID) || !outputPath(page.Path) || (!utf8.ValidString(page.Title) || len(page.Title) > MaxNavigationLabelBytes || strings.TrimSpace(page.Title) == "") || paths[page.Path] {
			return nil, fmt.Errorf("navigation page catalog contains an invalid page")
		}
		if _, exists := result[page.ID]; exists {
			return nil, fmt.Errorf("navigation page catalog contains duplicate page identities")
		}
		result[page.ID], paths[page.Path] = page, true
	}
	return result, nil
}

// ValidateNavigation checks the complete source tree before any permission
// filtering. Errors never disclose page labels, source paths or identifiers.
func ValidateNavigation(tree *document.NavigationTree, pages []Page) error {
	if err := validateNavigationShape(tree); err != nil || tree == nil {
		return err
	}
	catalog, err := navigationPageMap(pages)
	if err != nil {
		return err
	}
	var visit func([]document.NavigationNode) error
	visit = func(nodes []document.NavigationNode) error {
		for _, node := range nodes {
			if node.Kind == "page" {
				if _, exists := catalog[node.PageID]; !exists {
					return fmt.Errorf("navigation references a page that is not in the project")
				}
			} else if err := visit(node.Children); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(tree.Items)
}

func cloneNavigationNodes(nodes []document.NavigationNode, retained map[string]bool, filter bool) []document.NavigationNode {
	result := make([]document.NavigationNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Kind == "page" && filter && !retained[node.PageID] {
			continue
		}
		if node.Kind == "group" {
			node.Children = cloneNavigationNodes(node.Children, retained, filter)
			if filter && len(node.Children) == 0 {
				continue
			}
		}
		if node.Collapsed != nil {
			collapsed := *node.Collapsed
			node.Collapsed = &collapsed
		}
		result = append(result, node)
	}
	return result
}

// CloneNavigation owns every node slice and optional bool. Nil means legacy;
// an explicit empty items array remains a distinct custom tree.
func CloneNavigation(tree *document.NavigationTree) (*document.NavigationTree, error) {
	if err := validateNavigationShape(tree); err != nil || tree == nil {
		return nil, err
	}
	return &document.NavigationTree{UnlistedPages: tree.UnlistedPages, Items: cloneNavigationNodes(tree.Items, nil, false)}, nil
}

// FilterNavigation retains only authorized page IDs and removes every field
// of groups left empty. A nil retained map authorizes no pages, never all pages.
func FilterNavigation(tree *document.NavigationTree, retainedPageIDs map[string]bool) (*document.NavigationTree, error) {
	if err := validateNavigationShape(tree); err != nil || tree == nil {
		return nil, err
	}
	return &document.NavigationTree{UnlistedPages: tree.UnlistedPages, Items: cloneNavigationNodes(tree.Items, retainedPageIDs, true)}, nil
}

// PruneNavigation is the deletion counterpart of permission filtering. Its
// input is the set of retained page IDs, so ID-preserving moves need no rewrite.
func PruneNavigation(tree *document.NavigationTree, retainedPageIDs map[string]bool) (*document.NavigationTree, error) {
	return FilterNavigation(tree, retainedPageIDs)
}

func navigationPageItem(page Page, title, current string) NavigationItem {
	if title == "" {
		title = page.Title
	}
	return NavigationItem{Kind: "page", PageID: page.ID, Path: page.Path, Title: title, Current: page.ID == current, Active: page.ID == current}
}

func navigationBreadcrumbs(items []NavigationItem, ancestors []NavigationItem) []NavigationItem {
	for _, item := range items {
		if !item.Active {
			continue
		}
		children := item.Children
		item.Children = nil
		trail := append(append([]NavigationItem{}, ancestors...), item)
		if item.Current {
			return trail
		}
		if result := navigationBreadcrumbs(children, trail); len(result) != 0 {
			return result
		}
	}
	return nil
}

// ResolveNavigation consumes an already permission-filtered manifest. Omission
// changes navigation/sequence only; the page catalog still controls access and
// search. Renderers resolve relative links from the validated item.Path.
func ResolveNavigation(manifest Manifest, currentPageID string) (NavigationView, error) {
	result := NavigationView{Items: []NavigationItem{}, Pages: []NavigationItem{}, Breadcrumbs: []NavigationItem{}}
	var tree *document.NavigationTree
	if manifest.Navigation != nil {
		tree = manifest.Navigation.Tree
	}
	if err := ValidateNavigation(tree, manifest.Pages); err != nil {
		return NavigationView{}, err
	}
	catalog, err := navigationPageMap(manifest.Pages)
	if err != nil {
		return NavigationView{}, err
	}
	listed := make(map[string]bool, len(catalog))
	var resolve func([]document.NavigationNode) []NavigationItem
	resolve = func(nodes []document.NavigationNode) []NavigationItem {
		items := make([]NavigationItem, 0, len(nodes))
		for _, node := range nodes {
			if node.Kind == "page" {
				item := navigationPageItem(catalog[node.PageID], node.Title, currentPageID)
				listed[node.PageID] = true
				items, result.Pages = append(items, item), append(result.Pages, item)
				continue
			}
			item := NavigationItem{Kind: "group", ID: node.ID, Title: node.Title, Description: node.Description, Children: resolve(node.Children), Collapsed: node.Collapsed != nil && *node.Collapsed}
			for _, child := range item.Children {
				item.Active = item.Active || child.Active
			}
			items = append(items, item)
		}
		return items
	}
	if tree != nil {
		result.Items = resolve(tree.Items)
	}
	if tree == nil || tree.UnlistedPages != "omit" {
		for _, page := range manifest.Pages {
			id := pageIdentity(page)
			if listed[id] {
				continue
			}
			item := navigationPageItem(catalog[id], "", currentPageID)
			result.Items, result.Pages = append(result.Items, item), append(result.Pages, item)
		}
	}
	if breadcrumbs := navigationBreadcrumbs(result.Items, nil); len(breadcrumbs) != 0 {
		result.Breadcrumbs = breadcrumbs
	} else if page, exists := catalog[currentPageID]; exists {
		result.Breadcrumbs = []NavigationItem{navigationPageItem(page, "", currentPageID)}
	}
	return result, nil
}
