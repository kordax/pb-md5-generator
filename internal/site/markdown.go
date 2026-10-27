package site

import (
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type heading struct {
	Title string
	ID    string
	Level int
}

type reference struct {
	page     int
	fragment string
}

type pageContent struct {
	HTML        string
	Text        string
	Headings    []heading
	Anchors     map[string]bool
	References  []reference
	Blocks      []document.Block
	APIData     string
	Interactive bool
}

// headingIDs keeps readable Unicode IDs and makes repeated headings unique.
type headingIDs struct {
	used map[string]bool
	next map[string]int
}

func (g *headingIDs) Generate(value []byte, _ ast.NodeKind) []byte {
	var slug strings.Builder
	for _, r := range strings.ToLower(string(value)) {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), r == '_', r == '-':
			slug.WriteRune(r)
		case unicode.IsSpace(r), r == '.':
			slug.WriteByte('-')
		}
	}
	base := strings.Trim(slug.String(), "-")
	if base == "" {
		base = "section"
	}
	if !g.used[base] {
		g.used[base] = true
		return []byte(base)
	}
	if g.next == nil {
		g.next = make(map[string]int)
	}
	suffix := max(1, g.next[base])
	for {
		id := fmt.Sprintf("%s-%d", base, suffix)
		suffix++
		if !g.used[id] {
			g.next[base] = suffix
			g.used[id] = true
			return []byte(id)
		}
	}
}

func (g *headingIDs) Put(value []byte) { g.used[string(value)] = true }

// Only this exact legacy generator anchor is admitted from raw HTML. Other HTML
// remains disabled by goldmark, including attributes and executable elements.
var legacyAnchor = regexp.MustCompile(`^\s*<a (?:name|id)="([A-Za-z0-9_.:-]+)"></a>\s*$`)
var legacyAnchorStart = regexp.MustCompile(`^<a (?:name|id)="([A-Za-z0-9_.:-]+)">$`)
var kindAnchor = ast.NewNodeKind("DocumentationAnchor")

type anchorNode struct {
	ast.BaseInline
	ID string
}

func (n *anchorNode) Kind() ast.NodeKind { return kindAnchor }
func (n *anchorNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"ID": n.ID}, nil)
}

type anchorRenderer struct{}

func (anchorRenderer) RegisterFuncs(register renderer.NodeRendererFuncRegisterer) {
	register.Register(kindAnchor, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, err := w.WriteString(`<a id="` + html.EscapeString(n.(*anchorNode).ID) + `"></a>` + "\n")
			return ast.WalkSkipChildren, err
		}
		return ast.WalkContinue, nil
	})
}

func compileMarkdown(source []byte, current int, manifest Manifest) (pageContent, error) {
	return compileMarkdownScoped(source, current, manifest, "", nil)
}

func compileMarkdownScoped(source []byte, current int, manifest Manifest, scope string, transform func(string) (string, error)) (pageContent, error) {
	if !utf8.Valid(source) {
		return pageContent{}, fmt.Errorf("source is not valid UTF-8")
	}
	if err := checkMarkupBudget(source); err != nil {
		return pageContent{}, err
	}
	markdown := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithAttribute()),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(anchorRenderer{}, 100))),
	)
	context := parser.NewContext(parser.WithIDs(&headingIDs{used: make(map[string]bool)}))
	document := markdown.Parser().Parse(text.NewReader(source), parser.WithContext(context))
	content := pageContent{Anchors: make(map[string]bool)}
	type anchorReplacement struct {
		node  ast.Node
		close ast.Node
		id    string
	}
	var replacements []anchorReplacement
	nodes, depth := 0, 0
	aliases := make(map[string]string)
	type scopedLink struct {
		node *ast.Link
		ref  int
	}
	var links []scopedLink
	err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			depth--
			return ast.WalkContinue, nil
		}
		depth++
		if depth > maxContentDepth {
			return ast.WalkStop, fmt.Errorf("content nesting exceeds %d levels", maxContentDepth)
		}
		nodes++
		if nodes > maxPageNodes {
			return ast.WalkStop, fmt.Errorf("markdown AST exceeds %d nodes", maxPageNodes)
		}
		switch n := node.(type) {
		case *ast.HTMLBlock:
			if match := legacyAnchor.FindSubmatch(n.Lines().Value(source)); match != nil {
				original := string(match[1])
				id := scopedAnchor(scope, original)
				aliases[original] = id
				if err := addAnchor(content.Anchors, id); err != nil {
					return ast.WalkStop, err
				}
				replacements = append(replacements, anchorReplacement{node: n, id: id})
				return ast.WalkSkipChildren, nil
			}
		case *ast.RawHTML:
			if match := legacyAnchorStart.FindSubmatch(n.Segments.Value(source)); match != nil {
				if closing, ok := n.NextSibling().(*ast.RawHTML); ok && string(closing.Segments.Value(source)) == "</a>" {
					original := string(match[1])
					id := scopedAnchor(scope, original)
					aliases[original] = id
					if err := addAnchor(content.Anchors, id); err != nil {
						return ast.WalkStop, err
					}
					replacements = append(replacements, anchorReplacement{node: n, close: closing, id: id})
				}
			}
		case *ast.Heading:
			if len(content.Headings) >= maxPageHeadings {
				return ast.WalkStop, fmt.Errorf("page exceeds %d headings", maxPageHeadings)
			}
			value, _ := n.AttributeString("id")
			id, _ := value.([]byte)
			original := string(id)
			id = []byte(scopedAnchor(scope, original))
			aliases[original] = string(id)
			n.SetAttributeString("id", id)
			if err := addAnchor(content.Anchors, string(id)); err != nil {
				return ast.WalkStop, err
			}
			content.Headings = append(content.Headings, heading{Title: plainText(n, source), ID: string(id), Level: n.Level})
		case *ast.Link:
			destination := string(util.URLEscape(n.Destination, true))
			if transform != nil {
				var err error
				destination, err = transform(destination)
				if err != nil {
					return ast.WalkStop, err
				}
			}
			target, ref, err := resolveLink(destination, current, manifest, false)
			if err != nil {
				return ast.WalkStop, err
			}
			n.Destination = markdownDestination(target)
			if ref != nil {
				links = append(links, scopedLink{node: n, ref: len(content.References)})
				content.References = append(content.References, *ref)
			}
		case *ast.AutoLink:
			if _, _, err := resolveLink(autoLinkDestination(n, source), current, manifest, false); err != nil {
				return ast.WalkStop, err
			}
		case *ast.Image:
			target, _, err := resolveLink(string(util.URLEscape(n.Destination, true)), current, manifest, true)
			if err != nil {
				return ast.WalkStop, err
			}
			n.Destination = markdownDestination(target)
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return pageContent{}, err
	}
	for _, link := range links {
		ref := &content.References[link.ref]
		if scoped, ok := aliases[ref.fragment]; ref.page == current && ok && scoped != ref.fragment {
			parsed, err := url.Parse(string(util.URLEscape(link.node.Destination, true)))
			if err != nil {
				return pageContent{}, err
			}
			parsed.Fragment, parsed.RawFragment = scoped, ""
			link.node.Destination = markdownDestination(parsed.String())
			ref.fragment = strings.Clone(scoped)
		}
	}
	for _, replacement := range replacements {
		parent := replacement.node.Parent()
		parent.ReplaceChild(parent, replacement.node, &anchorNode{ID: replacement.id})
		if replacement.close != nil {
			parent.RemoveChild(parent, replacement.close)
		}
	}
	buffer := boundedBuffer{limit: maxRenderedBytes}
	if err := markdown.Renderer().Render(&buffer, source, document); err != nil {
		return pageContent{}, err
	}
	content.HTML = buffer.String()
	content.Text = plainText(document, source)
	if len(content.Text) > 32<<10 {
		content.Text = content.Text[:32<<10]
		for !utf8.ValidString(content.Text) {
			content.Text = content.Text[:len(content.Text)-1]
		}
	}
	// Even a short normalized string can be a view into a multi-megabyte
	// builder buffer. Keep only the bytes that enter the bounded search index.
	content.Text = strings.Clone(content.Text)
	content.Blocks, err = modelBlocks(document, source, current, manifest)
	if err != nil {
		return pageContent{}, err
	}
	return content, nil
}

func addAnchor(anchors map[string]bool, id string) error {
	if id == "" || len(id) > 1024 || strings.HasPrefix(id, "_docs-") || strings.ContainsAny(id, " \t\r\n\x00") {
		return fmt.Errorf("invalid or reserved anchor %q", id)
	}
	if anchors[id] {
		return fmt.Errorf("duplicate anchor %q", id)
	}
	anchors[id] = true
	return nil
}

// markdownDestination protects a canonical URI from a second entity decode by
// Goldmark's renderer. The model and renderer then recover the same destination.
func markdownDestination(destination string) []byte {
	return []byte(html.EscapeString(destination))
}

// Autolinks preserve entities and punctuation, and email autolinks need the
// mailto prefix that Goldmark adds when rendering their href.
func autoLinkDestination(link *ast.AutoLink, source []byte) string {
	destination := string(util.URLEscape(link.URL(source), false))
	if link.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(destination), "mailto:") {
		destination = "mailto:" + destination
	}
	return destination
}

// resolveLink validates a URI after its syntax-specific normalization.
func resolveLink(destination string, current int, manifest Manifest, image bool) (string, *reference, error) {
	link, err := url.Parse(destination)
	if err != nil {
		return "", nil, fmt.Errorf("invalid link %q: %w", destination, err)
	}
	if image && link.Scheme == "http" {
		return "", nil, fmt.Errorf("external images require HTTPS: %q", destination)
	}
	if link.IsAbs() || link.Host != "" {
		if link.Scheme != "http" && link.Scheme != "https" && (link.Scheme != "mailto" || image) {
			return "", nil, fmt.Errorf("unsupported link scheme in %q", destination)
		}
		return destination, nil, nil
	}
	page := manifest.Pages[current]
	if link.Path == "" && !image {
		return destination, &reference{page: current, fragment: strings.Clone(link.Fragment)}, nil
	}
	if strings.HasPrefix(link.Path, "/") || strings.Contains(link.Path, "\\") {
		return "", nil, fmt.Errorf("link %q must be source-relative", destination)
	}
	source := path.Join(path.Dir(page.Source), link.Path)
	if !localPath(source) {
		return "", nil, fmt.Errorf("link %q escapes the documentation project", destination)
	}
	if !image {
		for i, candidate := range manifest.Pages {
			if candidate.Source == source || candidate.Path == path.Join(path.Dir(page.Path), link.Path) {
				link.Path = relativeURL(page.Path, candidate.Path)
				link.RawPath = ""
				return link.String(), &reference{page: i, fragment: strings.Clone(link.Fragment)}, nil
			}
		}
	}
	for _, asset := range manifest.Assets {
		if source == asset {
			link.Path = relativeURL(page.Path, "assets/"+asset)
			link.RawPath = ""
			return link.String(), nil, nil
		}
	}
	return "", nil, fmt.Errorf("link %q has no declared page or asset", destination)
}

func relativeURL(from, to string) string {
	fromParts := strings.Split(path.Dir(from), "/")
	toParts := strings.Split(to, "/")
	if len(fromParts) == 1 && fromParts[0] == "." {
		return to
	}
	for len(fromParts) > 0 && len(toParts) > 0 && fromParts[0] == toParts[0] {
		fromParts, toParts = fromParts[1:], toParts[1:]
	}
	return strings.Repeat("../", len(fromParts)) + strings.Join(toParts, "/")
}

func plainText(node ast.Node, source []byte) string {
	var result strings.Builder
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			if n.Type() == ast.TypeBlock {
				result.WriteByte(' ')
			}
			return ast.WalkContinue, nil
		}
		switch value := n.(type) {
		case *ast.Text:
			result.Write(value.Segment.Value(source))
			if value.SoftLineBreak() || value.HardLineBreak() {
				result.WriteByte(' ')
			}
		case *ast.String:
			result.Write(value.Value)
		case *ast.CodeBlock:
			result.Write(value.Lines().Value(source))
		case *ast.FencedCodeBlock:
			result.Write(value.Lines().Value(source))
		case *ast.AutoLink:
			result.Write(value.Label(source))
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(html.UnescapeString(result.String())), " ")
}
