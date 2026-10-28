package document

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Theme contains portable design tokens. Renderers never interpret tokens as CSS
// source, URLs, scripts or remote font instructions.
type Theme struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Mode        string        `json:"mode"`
	Accent      string        `json:"accent"`
	Font        string        `json:"font"`
	Density     string        `json:"density"`
	Width       string        `json:"width"`
	Radius      int           `json:"radius"`
	Light       *ThemePalette `json:"light,omitempty"`
	Dark        *ThemePalette `json:"dark,omitempty"`
}

// ThemePalette optionally overrides individual colors in a theme's light or
// dark appearance. Every supplied value is a six digit hexadecimal color.
type ThemePalette struct {
	Background string `json:"background,omitempty"`
	Surface    string `json:"surface,omitempty"`
	Text       string `json:"text,omitempty"`
	Muted      string `json:"muted,omitempty"`
	Border     string `json:"border,omitempty"`
	Accent     string `json:"accent,omitempty"`
}

type NavigationSettings struct {
	ShowTOC          bool            `json:"showTOC"`
	ShowPreviousNext bool            `json:"showPreviousNext"`
	StickyHeader     bool            `json:"stickyHeader"`
	Tree             *NavigationTree `json:"tree,omitempty"`
}

// NavigationTree refers to page identities, never authored URLs. A nil tree
// keeps the legacy flat order; a nonnil tree with no items remains custom.
type NavigationTree struct {
	UnlistedPages string           `json:"unlistedPages,omitempty"`
	Items         []NavigationNode `json:"items"`
}

// NavigationNode is either a page reference with an optional label or a group
// with a stable ID and children. Collapsed preserves an explicitly supplied
// false value without making group-only fields valid on page references.
type NavigationNode struct {
	Kind        string           `json:"kind"`
	PageID      string           `json:"pageId,omitempty"`
	ID          string           `json:"id,omitempty"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Collapsed   *bool            `json:"collapsed,omitempty"`
	Children    []NavigationNode `json:"children,omitempty"`
}

type navigationStringField struct {
	value   string
	present bool
}

func (f *navigationStringField) UnmarshalJSON(data []byte) error {
	f.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("navigation strings must not be null")
	}
	return json.Unmarshal(data, &f.value)
}

type navigationBoolField struct {
	value   *bool
	present bool
}

func (f *navigationBoolField) UnmarshalJSON(data []byte) error {
	f.present = true
	return json.Unmarshal(data, &f.value)
}

type navigationChildrenField struct {
	value   []NavigationNode
	present bool
}

func (f *navigationChildrenField) UnmarshalJSON(data []byte) error {
	f.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("navigation children must be an array")
	}
	return json.Unmarshal(data, &f.value)
}

func decodeNavigationFields(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("navigation contains invalid fields")
	}
	return nil
}

// UnmarshalJSON preserves field presence for the discriminated node union.
// Empty values on another node kind are still disallowed authored fields.
func (n *NavigationNode) UnmarshalJSON(data []byte) error {
	var fields struct {
		Kind        navigationStringField   `json:"kind"`
		PageID      navigationStringField   `json:"pageId"`
		ID          navigationStringField   `json:"id"`
		Title       navigationStringField   `json:"title"`
		Description navigationStringField   `json:"description"`
		Collapsed   navigationBoolField     `json:"collapsed"`
		Children    navigationChildrenField `json:"children"`
	}
	if err := decodeNavigationFields(data, &fields); err != nil {
		return err
	}
	switch fields.Kind.value {
	case "page":
		if !fields.PageID.present || fields.ID.present || fields.Description.present || fields.Collapsed.present || fields.Children.present {
			return fmt.Errorf("navigation page contains invalid fields")
		}
	case "group":
		if !fields.ID.present || !fields.Title.present || !fields.Children.present || fields.PageID.present {
			return fmt.Errorf("navigation group contains invalid fields")
		}
	default:
		return fmt.Errorf("navigation node kind must be page or group")
	}
	*n = NavigationNode{Kind: fields.Kind.value, PageID: fields.PageID.value, ID: fields.ID.value, Title: fields.Title.value, Description: fields.Description.value, Collapsed: fields.Collapsed.value, Children: fields.Children.value}
	return nil
}

func (t *NavigationTree) UnmarshalJSON(data []byte) error {
	var fields struct {
		UnlistedPages navigationStringField `json:"unlistedPages"`
		Items         []NavigationNode      `json:"items"`
	}
	if err := decodeNavigationFields(data, &fields); err != nil {
		return err
	}
	if fields.Items == nil || fields.UnlistedPages.present && fields.UnlistedPages.value != "append" && fields.UnlistedPages.value != "omit" {
		return fmt.Errorf("navigation requires an items array and an append or omit unlistedPages policy")
	}
	*t = NavigationTree{UnlistedPages: fields.UnlistedPages.value, Items: fields.Items}
	return nil
}

type EditorSettings struct {
	DefaultFormat string `json:"defaultFormat"`
	PreviewOnSave bool   `json:"previewOnSave"`
}

type APISettings struct {
	Enabled          bool `json:"enabled"`
	ShowCurl         bool `json:"showCurl"`
	RequestTimeoutMs int  `json:"requestTimeoutMs"`
	MaxResponseBytes int  `json:"maxResponseBytes"`
}

type PublishingSettings struct {
	AutoPublish      bool `json:"autoPublish"`
	GitPushOnPublish bool `json:"gitPushOnPublish"`
}
