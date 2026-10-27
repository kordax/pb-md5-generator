package site

import (
	"fmt"
	"html"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"
)

const maxContentDepth = 64

type contentModel struct {
	source    []byte
	file      string
	generated bool
	lines     []int
	current   int
	manifest  Manifest
}

func modelBlocksWithSource(root ast.Node, source []byte, current int, manifest Manifest, sourceName string) ([]document.Block, error) {
	page := manifest.Pages[current]
	model := contentModel{source: source, file: sourceName, generated: page.Kind != "" && page.Kind != "markdown", current: current, manifest: manifest, lines: []int{0}}
	if !model.generated {
		for i, value := range source {
			if value == '\n' {
				model.lines = append(model.lines, i+1)
			}
		}
	}
	return model.blocks(root, 0)
}

func (m *contentModel) blocks(parent ast.Node, depth int) ([]document.Block, error) {
	if depth > maxContentDepth {
		return nil, fmt.Errorf("content nesting exceeds %d levels", maxContentDepth)
	}
	blocks := make([]document.Block, 0)
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		block := document.Block{Source: m.location(node)}
		container := false
		switch n := node.(type) {
		case *directiveNode:
			block.ID = n.ID
			switch n.Name {
			case "note", "info", "tip", "warning", "danger":
				block.Kind, block.Variant, block.Title, container = document.BlockCallout, n.Name, n.Title, true
			case "tabs":
				block.Kind, container = document.BlockTabs, true
			case "tab":
				block.Kind, block.Title, container = document.BlockTab, n.Title, true
			case "include":
				block.Kind, block.IncludeSource, block.Language = document.BlockInclude, n.ResolvedSource, n.Language
				block.Children = n.Content.Blocks
			default:
				return nil, fmt.Errorf("unsupported directive block %s", n.Name)
			}
		case *ast.Paragraph, *ast.TextBlock:
			block.Kind = document.BlockParagraph
		case *ast.Heading:
			block.Kind, block.Level = document.BlockHeading, n.Level
			id, _ := n.AttributeString("id")
			if value, ok := id.([]byte); ok {
				block.ID = string(value)
			}
		case *ast.FencedCodeBlock:
			block.Kind, block.Language, block.Text = document.BlockCode, string(n.Language(m.source)), string(n.Lines().Value(m.source))
		case *ast.CodeBlock:
			block.Kind, block.Text = document.BlockCode, string(n.Lines().Value(m.source))
		case *ast.Blockquote:
			block.Kind, container = document.BlockQuote, true
		case *ast.List:
			block.Kind, container = document.BlockList, true
			block.Ordered, block.Start, block.Tight = n.IsOrdered(), n.Start, n.IsTight
		case *ast.ListItem:
			block.Kind, container = document.BlockListItem, true
		case *ast.ThematicBreak:
			block.Kind = document.BlockRule
		case *anchorNode:
			block.Kind, block.ID = document.BlockAnchor, n.ID
		case *ast.HTMLBlock:
			block.Kind, block.Omitted = document.BlockHTML, true
			block.Text = string(n.Lines().Value(m.source))
			if n.HasClosure() {
				block.Text += string(n.ClosureLine.Value(m.source))
			}
		case *extast.Table:
			block.Kind, container = document.BlockTable, true
		case *extast.TableHeader:
			block.Kind, container = document.BlockTableHeader, true
		case *extast.TableRow:
			block.Kind, container = document.BlockTableRow, true
		case *extast.TableCell:
			block.Kind = document.BlockTableCell
			switch n.Alignment {
			case extast.AlignLeft:
				block.Align = "left"
			case extast.AlignCenter:
				block.Align = "center"
			case extast.AlignRight:
				block.Align = "right"
			}
		case *extast.DefinitionList:
			block.Kind, container = document.BlockDefinitionList, true
		case *extast.DefinitionTerm:
			block.Kind = document.BlockDefinitionTerm
		case *extast.DefinitionDescription:
			block.Kind, container = document.BlockDefinition, true
		default:
			return nil, fmt.Errorf("unsupported content block %s", node.Kind())
		}
		var err error
		if container {
			block.Children, err = m.blocks(node, depth+1)
		} else if node.FirstChild() != nil {
			block.Inlines, err = m.inlines(node, depth+1)
		}
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func (m *contentModel) inlines(parent ast.Node, depth int) ([]document.Inline, error) {
	if depth > maxContentDepth {
		return nil, fmt.Errorf("content nesting exceeds %d levels", maxContentDepth)
	}
	inlines := make([]document.Inline, 0)
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		inline := document.Inline{}
		switch n := node.(type) {
		case *ast.Text:
			value := util.UnescapePunctuations(n.Segment.Value(m.source))
			inline.Kind, inline.Text = document.InlineText, html.UnescapeString(string(value))
			inlines = append(inlines, inline)
			if n.HardLineBreak() {
				inlines = append(inlines, document.Inline{Kind: document.InlineHardBreak})
			} else if n.SoftLineBreak() {
				inlines = append(inlines, document.Inline{Kind: document.InlineSoftBreak})
			}
			continue
		case *ast.String:
			inline.Kind, inline.Text = document.InlineText, string(n.Value)
		case *ast.CodeSpan:
			inline.Kind, inline.Text = document.InlineCode, codeSpanText(n, m.source)
		case *ast.Emphasis:
			inline.Kind = document.InlineEmphasis
			if n.Level == 2 {
				inline.Kind = document.InlineStrong
			}
		case *extast.Strikethrough:
			inline.Kind = document.InlineStrike
		case *ast.Link:
			inline.Kind, inline.Destination, inline.Title = document.InlineLink, string(util.URLEscape(n.Destination, true)), html.UnescapeString(string(n.Title))
			inline.Reference = m.reference(inline.Destination)
		case *ast.Image:
			inline.Kind, inline.Destination, inline.Title = document.InlineImage, string(util.URLEscape(n.Destination, true)), html.UnescapeString(string(n.Title))
			inline.Reference = m.reference(inline.Destination)
		case *ast.AutoLink:
			inline.Kind, inline.Destination = document.InlineLink, autoLinkDestination(n, m.source)
			inline.Children = []document.Inline{{Kind: document.InlineText, Text: string(n.Label(m.source))}}
		case *extast.TaskCheckBox:
			checked := n.IsChecked
			inline.Kind, inline.Checked = document.InlineCheckbox, &checked
		case *ast.RawHTML:
			inline.Kind, inline.Text, inline.Omitted = document.InlineHTML, string(n.Segments.Value(m.source)), true
		case *anchorNode:
			inline.Kind, inline.ID = document.InlineAnchor, n.ID
		default:
			return nil, fmt.Errorf("unsupported inline content %s", node.Kind())
		}
		if node.FirstChild() != nil && inline.Kind != document.InlineCode {
			children, err := m.inlines(node, depth+1)
			if err != nil {
				return nil, err
			}
			inline.Children = children
		}
		inlines = append(inlines, inline)
	}
	return inlines, nil
}

func (m *contentModel) location(node ast.Node) document.Location {
	if directive, ok := node.(*directiveNode); ok {
		return m.locationAt(directive.Offset)
	}
	if node.Type() != ast.TypeBlock || node.Lines().Len() == 0 {
		return document.Location{File: m.file, Generated: m.generated}
	}
	return m.locationAt(node.Lines().At(0).Start)
}

func (m *contentModel) locationAt(offset int) document.Location {
	location := document.Location{File: m.file, Generated: m.generated}
	if m.generated || offset < 0 || offset > len(m.source) {
		return location
	}
	line := sort.Search(len(m.lines), func(i int) bool { return m.lines[i] > offset })
	if line > 0 {
		location.Line, location.Column = line, offset-m.lines[line-1]+1
	}
	return location
}

func (m *contentModel) reference(destination string) *document.Reference {
	link, err := url.Parse(destination)
	if err != nil || link.IsAbs() || link.Host != "" {
		return nil
	}
	current := m.manifest.Pages[m.current]
	target := current.Path
	if link.Path != "" {
		target = path.Join(path.Dir(current.Path), link.Path)
	}
	for _, page := range m.manifest.Pages {
		if page.Path == target {
			return &document.Reference{Page: pageIdentity(page), Anchor: strings.Clone(link.Fragment)}
		}
	}
	for _, asset := range m.manifest.Assets {
		if target == "assets/"+asset {
			return &document.Reference{Asset: asset, Anchor: strings.Clone(link.Fragment)}
		}
	}
	return nil
}

func codeSpanText(node *ast.CodeSpan, source []byte) string {
	var result strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if text, ok := child.(*ast.Text); ok {
			result.Write(text.Segment.Value(source))
			if text.SoftLineBreak() && !strings.HasSuffix(result.String(), "\n") {
				result.WriteByte(' ')
			}
		}
	}
	value := strings.ReplaceAll(result.String(), "\n", " ")
	if strings.HasPrefix(value, " ") && strings.HasSuffix(value, " ") && strings.Trim(value, " ") != "" {
		value = value[1 : len(value)-1]
	}
	return value
}

func scopedAnchor(scope, id string) string {
	if scope == "" {
		return id
	}
	return scope + "--" + id
}

func validPageID(value string) bool {
	if len(value) == 0 || len(value) > 128 || value[0] == '.' || value[0] == '_' || value[0] == '-' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && !strings.ContainsRune("._-", char) {
			return false
		}
	}
	return true
}

func pageIdentity(page Page) string {
	if page.ID != "" {
		return page.ID
	}
	return document.PageID(page.Path)
}
