package site

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const (
	maxDirectiveDepth       = 16
	maxDirectives           = 1024
	maxDirectiveHeader      = 4096
	maxDirectiveTitle       = 256
	maxTabs                 = 32
	maxIncludeDepth         = 8
	maxIncludeExpandedBytes = 16 << 20
)

var kindDirective = ast.NewNodeKind("DocumentationDirective")

type directiveNode struct {
	ast.BaseBlock
	Name           string
	ID             string
	Title          string
	Source         string
	ResolvedSource string
	Language       string
	Offset         int
	Content        pageContent
	closed         bool
}

func (n *directiveNode) Kind() ast.NodeKind { return kindDirective }
func (n *directiveNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Name": n.Name, "Title": n.Title}, nil)
}

// Each included document has local heading aliases, while the final page keeps
// one allocation namespace. Repeated fragments retain local self links even when
// their rendered heading IDs need a suffix.
type directiveHeadingIDs struct {
	local   *headingIDs
	shared  *headingIDs
	aliases map[string]string
}

func (g *directiveHeadingIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	local := g.local.Generate(value, kind)
	actual := g.shared.Generate(local, kind)
	g.aliases[string(local)] = string(actual)
	return actual
}

func (g *directiveHeadingIDs) Put(value []byte) {
	g.local.Put(value)
	g.shared.Put(value)
}

type directiveState struct {
	ctx        context.Context
	load       func(string) ([]byte, error)
	primary    string
	transform  func(string) (string, error)
	ids        *headingIDs
	stack      map[string]bool
	expanded   int
	nodes      int
	headings   int
	directives int
	groups     int
}

func newDirectiveState(ctx context.Context, primary string, load func(string) ([]byte, error), transform func(string) (string, error)) *directiveState {
	return &directiveState{ctx: ctx, load: load, primary: primary, transform: transform, ids: &headingIDs{used: make(map[string]bool)}, stack: map[string]bool{primary: true}}
}

func compileMarkdownSource(ctx context.Context, source []byte, current int, manifest Manifest, load func(string) ([]byte, error)) (pageContent, error) {
	name := manifest.Pages[current].Source
	return compileMarkdownState(source, current, manifest, "", nil, newDirectiveState(ctx, name, load, nil), name, 0, 0)
}

type directiveParser struct {
	state      *directiveState
	sourceName string
	err        error
}

func (*directiveParser) Trigger() []byte             { return []byte{':'} }
func (*directiveParser) CanInterruptParagraph() bool { return true }
func (*directiveParser) CanAcceptIndentedLine() bool { return false }

func (p *directiveParser) fail(source []byte, offset int, format string, args ...any) {
	if p.err == nil {
		p.err = directiveError(p.sourceName, source, offset, fmt.Sprintf(format, args...))
	}
}

func directiveError(name string, source []byte, offset int, message string) error {
	offset = min(max(offset, 0), len(source))
	prefix := source[:offset]
	line := bytes.Count(prefix, []byte{'\n'}) + 1
	column := utf8.RuneCount(prefix[bytes.LastIndexByte(prefix, '\n')+1:]) + 1
	return fmt.Errorf("%s:%d:%d: %s", name, line, column, message)
}

func (p *directiveParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	pos := pc.BlockOffset()
	if p.err != nil || pos < 0 || !bytes.HasPrefix(line[pos:], []byte(":::")) {
		return nil, parser.NoChildren
	}
	offset := segment.Start + pos - segment.Padding
	name, attrs, err := parseDirectiveHeader(string(bytes.TrimRight(line[pos:], "\r\n")))
	if err != nil {
		p.fail(reader.Source(), offset, "%s", err)
		return nil, parser.NoChildren
	}
	depth := 1
	for ancestor := parent; ancestor != nil; ancestor = ancestor.Parent() {
		if _, ok := ancestor.(*directiveNode); ok {
			depth++
		}
	}
	if depth > maxDirectiveDepth {
		p.fail(reader.Source(), offset, "directives exceed %d nesting levels", maxDirectiveDepth)
		return nil, parser.NoChildren
	}
	p.state.directives++
	if p.state.directives > maxDirectives {
		p.fail(reader.Source(), offset, "page exceeds %d directives", maxDirectives)
		return nil, parser.NoChildren
	}
	n := &directiveNode{Name: name, Title: attrs["title"], Source: attrs["source"], Language: attrs["language"], Offset: offset}
	if n.Title == "" {
		if name == "tabs" {
			n.Title = "Examples"
		} else if name != "tab" && name != "include" {
			n.Title = strings.ToUpper(name[:1]) + name[1:]
		}
	}
	reader.AdvanceToEOL()
	return n, parser.HasChildren
}

func (*directiveParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, _ := reader.PeekLine()
	width, pos := util.IndentWidth(line, reader.LineOffset())
	if width > 3 || strings.Trim(string(line[pos:]), " \t\r\n") != ":::" {
		return parser.Continue | parser.HasChildren
	}
	// Container continuations run before their children. A closing fence belongs
	// to the innermost directive, unless it is literal code or raw HTML.
	opened := pc.OpenedBlocks()
	for i := len(opened) - 1; i >= 0; i-- {
		child := opened[i].Node
		if child == node {
			break
		}
		switch child.(type) {
		case *directiveNode, *ast.FencedCodeBlock, *ast.HTMLBlock:
			return parser.Continue | parser.HasChildren
		}
	}
	node.(*directiveNode).closed = true
	reader.AdvanceToEOL()
	return parser.Close
}

func (p *directiveParser) Close(node ast.Node, reader text.Reader, _ parser.Context) {
	n := node.(*directiveNode)
	if !n.closed {
		p.fail(reader.Source(), n.Offset, "unclosed :::%s directive", n.Name)
	}
}

func parseDirectiveHeader(header string) (string, map[string]string, error) {
	if !strings.HasPrefix(header, ":::") {
		return "", nil, fmt.Errorf("directive must start with a triple-colon fence")
	}
	if len(header) > maxDirectiveHeader {
		return "", nil, fmt.Errorf("directive header exceeds %d bytes", maxDirectiveHeader)
	}
	rest := strings.TrimRight(header[3:], " \t")
	if rest == "" {
		return "", nil, fmt.Errorf("unmatched directive closing fence")
	}
	i := 0
	for i < len(rest) && rest[i] >= 'a' && rest[i] <= 'z' {
		i++
	}
	name := rest[:i]
	allowed := map[string]bool{"title": true}
	switch name {
	case "note", "info", "tip", "warning", "danger", "tabs", "tab":
	case "include":
		allowed = map[string]bool{"source": true, "language": true}
	default:
		unknown := rest
		if end := strings.IndexAny(rest, " \t"); end >= 0 {
			unknown = rest[:end]
		}
		return "", nil, fmt.Errorf("unknown directive %q", unknown)
	}
	attrs := make(map[string]string)
	for i < len(rest) {
		if rest[i] != ' ' && rest[i] != '\t' {
			return "", nil, fmt.Errorf("expected whitespace after directive name or attribute")
		}
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
		if i == len(rest) {
			break
		}
		start := i
		for i < len(rest) && rest[i] >= 'a' && rest[i] <= 'z' {
			i++
		}
		key := rest[start:i]
		if !allowed[key] {
			return "", nil, fmt.Errorf("unknown attribute %q for :::%s", key, name)
		}
		if _, exists := attrs[key]; exists {
			return "", nil, fmt.Errorf("duplicate directive attribute %q", key)
		}
		if i >= len(rest) || rest[i] != '=' {
			return "", nil, fmt.Errorf("expected = after directive attribute %q", key)
		}
		i++
		if i >= len(rest) || rest[i] != '"' {
			return "", nil, fmt.Errorf("directive attribute %q requires a JSON double-quoted string", key)
		}
		start = i
		i++
		closed := false
		for i < len(rest) {
			if rest[i] == '\\' {
				i += 2
				continue
			}
			if rest[i] == '"' {
				i++
				closed = true
				break
			}
			i++
		}
		if !closed {
			return "", nil, fmt.Errorf("unterminated quoted directive attribute %q", key)
		}
		var value string
		if err := json.Unmarshal([]byte(rest[start:i]), &value); err != nil {
			return "", nil, fmt.Errorf("invalid JSON string for directive attribute %q: %w", key, err)
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return "", nil, fmt.Errorf("control character in directive attribute %q", key)
			}
		}
		attrs[key] = value
	}
	if title, ok := attrs["title"]; ok && (strings.TrimSpace(title) == "" || len(title) > maxDirectiveTitle) {
		return "", nil, fmt.Errorf("directive title must contain 1–%d bytes of text", maxDirectiveTitle)
	}
	if name == "tab" && attrs["title"] == "" {
		return "", nil, fmt.Errorf(":::tab requires a nonempty title")
	}
	if name == "include" {
		if attrs["source"] == "" {
			return "", nil, fmt.Errorf(":::include requires a source")
		}
		if language, ok := attrs["language"]; ok && !validIncludeLanguage(language) {
			return "", nil, fmt.Errorf("include language must contain 1–32 ASCII letters, digits, _, +, . or -")
		}
	}
	return name, attrs, nil
}

func validIncludeLanguage(language string) bool {
	if len(language) == 0 || len(language) > 32 {
		return false
	}
	for i, c := range []byte(language) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i == 0 || c != '_' && c != '+' && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

func (s *directiveState) prepare(n *directiveNode, source []byte, current int, manifest Manifest, scope, sourceName string, includeDepth, baseDepth int) error {
	fail := func(message string) error { return directiveError(sourceName, source, n.Offset, message) }
	switch n.Name {
	case "tab":
		parent, ok := n.Parent().(*directiveNode)
		if !ok || parent.Name != "tabs" {
			return fail(":::tab must be a direct child of :::tabs")
		}
	case "tabs":
		if n.ChildCount() == 0 || n.ChildCount() > maxTabs {
			return fail(fmt.Sprintf(":::tabs requires 1–%d tabs", maxTabs))
		}
		s.groups++
		digest := sha256.Sum256([]byte(pageIdentity(manifest.Pages[current]) + "\x00" + scope))
		n.ID = "_docs-tabs-" + hex.EncodeToString(digest[:6]) + "-" + strconv.Itoa(s.groups)
		index := 0
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			tab, ok := child.(*directiveNode)
			if !ok || tab.Name != "tab" {
				return fail(":::tabs accepts only direct :::tab children")
			}
			index++
			tab.ID = n.ID + "-panel-" + strconv.Itoa(index)
		}
	case "include":
		if n.HasChildren() {
			return fail(":::include must have an empty body")
		}
		if s.load == nil {
			return fail("includes are unavailable in this Markdown context")
		}
		if includeDepth >= maxIncludeDepth {
			return fail(fmt.Sprintf("include depth exceeds %d", maxIncludeDepth))
		}
		name, err := resolveIncludeSource(sourceName, n.Source)
		if err != nil {
			return fail(err.Error())
		}
		n.ResolvedSource = name
		if n.Language == "" && s.stack[name] {
			return fail("include cycle through " + name)
		}
		if err := s.ctx.Err(); err != nil {
			return err
		}
		data, err := s.load(name)
		if err != nil {
			return fail("include " + name + ": " + err.Error())
		}
		if !utf8.Valid(data) {
			return fail("include " + name + " is not valid UTF-8")
		}
		if n.Language != "" {
			if len(data) > maxIncludeExpandedBytes-s.expanded {
				return fail(fmt.Sprintf("expanded includes exceed %d bytes", maxIncludeExpandedBytes))
			}
			s.expanded += len(data)
			s.nodes++
			if s.nodes > maxPageNodes {
				return fail(fmt.Sprintf("markdown AST exceeds %d nodes", maxPageNodes))
			}
			search := data[:min(len(data), 32<<10)]
			for !utf8.Valid(search) {
				search = search[:len(search)-1]
			}
			n.Content = pageContent{
				HTML:   "<pre><code class=\"language-" + html.EscapeString(n.Language) + "\">" + html.EscapeString(string(data)) + "</code></pre>\n",
				Text:   strings.Clone(string(search)),
				Blocks: []document.Block{{Kind: document.BlockCode, Source: document.Location{File: name, Line: 1, Column: 1}, Language: n.Language, Text: string(data)}},
			}
			return nil
		}
		s.stack[name] = true
		defer delete(s.stack, name)
		transform := func(destination string) (string, error) {
			destination, err := s.rebaseDestination(destination, name)
			if err != nil {
				return "", err
			}
			if s.transform != nil {
				return s.transform(destination)
			}
			return destination, nil
		}
		// The nested AST keeps its own source buffer and locations. Only bounded
		// rendered/model results enter the host AST, never foreign text segments.
		n.Content, err = compileMarkdownState(data, current, manifest, scope, transform, s, name, includeDepth+1, baseDepth)
		if err != nil {
			return err
		}
	}
	return nil
}

func resolveIncludeSource(sourceName, raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, "\\\x00") {
		return "", fmt.Errorf("include source must be a local file path relative to its declaring file")
	}
	name := path.Join(path.Dir(sourceName), raw)
	if !localPath(name) {
		return "", fmt.Errorf("include source escapes the documentation project")
	}
	return name, nil
}

func (s *directiveState) rebaseDestination(destination, sourceName string) (string, error) {
	link, err := url.Parse(destination)
	if err != nil {
		return "", err
	}
	if link.IsAbs() || link.Host != "" || link.Path == "" {
		return destination, nil
	}
	if strings.HasPrefix(link.Path, "/") || strings.Contains(link.Path, "\\") {
		return "", fmt.Errorf("link %q must be source-relative", destination)
	}
	target := path.Join(path.Dir(sourceName), link.Path)
	if !localPath(target) {
		return "", fmt.Errorf("link %q escapes the documentation project", destination)
	}
	link.Path = relativeURL(s.primary, target)
	link.RawPath = ""
	return link.String(), nil
}

type directiveRenderer struct{}

func (directiveRenderer) RegisterFuncs(register renderer.NodeRendererFuncRegisterer) {
	register.Register(kindDirective, renderDirective)
}

func renderDirective(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*directiveNode)
	var output string
	if entering {
		switch n.Name {
		case "include":
			for _, part := range []string{"<div class=\"docs-include\">", n.Content.HTML, "</div>\n"} {
				if _, err := w.WriteString(part); err != nil {
					return ast.WalkStop, err
				}
			}
			return ast.WalkSkipChildren, nil
		case "tabs":
			var links strings.Builder
			links.WriteString("<div class=\"docs-tabs\" data-docs-tabs><div class=\"docs-tab-links\" data-docs-tablist aria-label=\"")
			links.WriteString(html.EscapeString(n.Title))
			links.WriteString("\">")
			i := 0
			for child := n.FirstChild(); child != nil; child = child.NextSibling() {
				tab := child.(*directiveNode)
				i++
				links.WriteString("<a id=\"" + html.EscapeString(n.ID) + "-tab-" + strconv.Itoa(i) + "\" href=\"#" + html.EscapeString(tab.ID) + "\" data-docs-tab=\"" + html.EscapeString(tab.ID) + "\">" + html.EscapeString(tab.Title) + "</a>")
			}
			links.WriteString("</div>\n")
			output = links.String()
		case "tab":
			output = "<section id=\"" + html.EscapeString(n.ID) + "\" class=\"docs-tab-panel\" data-docs-tabpanel><p class=\"docs-tab-title\">" + html.EscapeString(n.Title) + "</p>\n"
		default:
			output = "<aside class=\"docs-callout docs-callout-" + n.Name + "\"><p class=\"docs-callout-title\">" + html.EscapeString(n.Title) + "</p>\n"
		}
	} else {
		switch n.Name {
		case "include":
			return ast.WalkContinue, nil
		case "tabs":
			output = "</div>\n"
		case "tab":
			output = "</section>\n"
		default:
			output = "</aside>\n"
		}
	}
	_, err := w.WriteString(output)
	return ast.WalkContinue, err
}
