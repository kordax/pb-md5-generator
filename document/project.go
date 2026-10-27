// Package document defines the versioned, renderer-independent documentation model.
package document

import (
	"crypto/sha256"
	"encoding/hex"
)

const FormatVersion = 1

// Location identifies original source text. Generated locations deliberately omit
// line numbers rather than claiming transformed output has the original offsets.
type Location struct {
	File      string `json:"file"`
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	Generated bool   `json:"generated,omitempty"`
}

// Project is a small index. Each page has its own data artifact, so a compiler or
// reader can process a large project without retaining every page and API graph.
type Project struct {
	Schema      string          `json:"$schema"`
	Format      string          `json:"format"`
	Version     int             `json:"version"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Source      Location        `json:"source"`
	Root        string          `json:"root"`
	Pages       []PageReference `json:"pages"`
}

type PageReference struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Path   string   `json:"path"`
	Source Location `json:"source"`
	Kind   string   `json:"kind"`
	Data   string   `json:"data"`
}

// Page is the portable content artifact used by renderers, search adapters and
// tooling. API references preserve transport semantics and recursive references.
type Page struct {
	Schema  string   `json:"$schema"`
	Format  string   `json:"format"`
	Version int      `json:"version"`
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Path    string   `json:"path"`
	Source  Location `json:"source"`
	Kind    string   `json:"kind"`
	Blocks  []Block  `json:"blocks"`
	API     *API     `json:"api,omitempty"`
}

type BlockKind string

const (
	BlockParagraph      BlockKind = "paragraph"
	BlockHeading        BlockKind = "heading"
	BlockCode           BlockKind = "code"
	BlockQuote          BlockKind = "quote"
	BlockList           BlockKind = "list"
	BlockListItem       BlockKind = "listItem"
	BlockTable          BlockKind = "table"
	BlockTableRow       BlockKind = "tableRow"
	BlockTableHeader    BlockKind = "tableHeader"
	BlockTableCell      BlockKind = "tableCell"
	BlockDefinitionList BlockKind = "definitionList"
	BlockDefinitionTerm BlockKind = "definitionTerm"
	BlockDefinition     BlockKind = "definition"
	BlockRule           BlockKind = "rule"
	BlockAnchor         BlockKind = "anchor"
	BlockHTML           BlockKind = "html"
	BlockCallout        BlockKind = "callout"
	BlockTabs           BlockKind = "tabs"
	BlockTab            BlockKind = "tab"
	BlockInclude        BlockKind = "include"
)

// Block represents one structural node. Leaf text and code are stored once;
// containers hold child nodes rather than copying whole Markdown fragments.
type Block struct {
	Kind          BlockKind `json:"kind"`
	Source        Location  `json:"source"`
	ID            string    `json:"id,omitempty"`
	Level         int       `json:"level,omitempty"`
	Text          string    `json:"text,omitempty"`
	Language      string    `json:"language,omitempty"`
	Title         string    `json:"title,omitempty"`
	Variant       string    `json:"variant,omitempty"`
	IncludeSource string    `json:"includeSource,omitempty"`
	Inlines       []Inline  `json:"inlines,omitempty"`
	Children      []Block   `json:"children,omitempty"`
	Ordered       bool      `json:"ordered,omitempty"`
	Start         int       `json:"start,omitempty"`
	Tight         bool      `json:"tight,omitempty"`
	Align         string    `json:"align,omitempty"`
	Omitted       bool      `json:"omitted,omitempty"`
}

type InlineKind string

const (
	InlineText      InlineKind = "text"
	InlineCode      InlineKind = "code"
	InlineEmphasis  InlineKind = "emphasis"
	InlineStrong    InlineKind = "strong"
	InlineStrike    InlineKind = "strikethrough"
	InlineLink      InlineKind = "link"
	InlineImage     InlineKind = "image"
	InlineSoftBreak InlineKind = "softBreak"
	InlineHardBreak InlineKind = "hardBreak"
	InlineCheckbox  InlineKind = "checkbox"
	InlineHTML      InlineKind = "html"
	InlineAnchor    InlineKind = "anchor"
)

type Inline struct {
	Kind        InlineKind `json:"kind"`
	ID          string     `json:"id,omitempty"`
	Text        string     `json:"text,omitempty"`
	Destination string     `json:"destination,omitempty"`
	Title       string     `json:"title,omitempty"`
	Reference   *Reference `json:"reference,omitempty"`
	Children    []Inline   `json:"children,omitempty"`
	Checked     *bool      `json:"checked,omitempty"`
	Omitted     bool       `json:"omitted,omitempty"`
}

// Reference identifies local content independently of its rendered URL.
type Reference struct {
	Page   string `json:"page,omitempty"`
	Asset  string `json:"asset,omitempty"`
	Anchor string `json:"anchor,omitempty"`
}

// PageID is stable for an unchanged path. A manifest can supply an explicit ID
// when a page must keep its identity while its public path changes.
func PageID(path string) string {
	digest := sha256.Sum256([]byte(path))
	return "page-" + hex.EncodeToString(digest[:16])
}
