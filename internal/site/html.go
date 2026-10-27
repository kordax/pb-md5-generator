package site

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	maxHTMLNodes = 50000
	maxHTMLDepth = 64
)

// Policies are constructed once and never changed while rendering sources.
var safeHTMLPolicy = newSafeHTMLPolicy()

func newSafeHTMLPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements("p", "br", "hr", "div", "span", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre", "code", "em", "strong", "b", "i", "s", "u", "del", "ins", "mark", "small", "sub", "sup", "ul", "ol", "li", "dl", "dt", "dd", "table", "caption", "thead", "tbody", "tfoot", "tr", "th", "td", "details", "summary", "a", "img", "kbd", "samp", "var", "abbr", "time")
	policy.AllowAttrs("id", "title", "aria-label").Globally()
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowAttrs("src", "alt").OnElements("img")
	policy.AllowAttrs("width", "height", "colspan", "rowspan", "start", "value").Matching(regexp.MustCompile(`^[0-9]{1,5}$`)).OnElements("img", "th", "td", "ol", "li")
	policy.AllowAttrs("scope").Matching(regexp.MustCompile(`^(row|col|rowgroup|colgroup)$`)).OnElements("th")
	policy.AllowAttrs("open").OnElements("details")
	policy.AllowAttrs("datetime").OnElements("time", "ins", "del")
	policy.RequireParseableURLs(true)
	policy.AllowRelativeURLs(true)
	policy.AllowURLSchemes("http", "https", "mailto")
	policy.SkipElementsContent("script", "style", "iframe", "object", "embed", "svg", "math", "template", "noscript")
	return policy
}

func compileHTML(ctx context.Context, source []byte, current int, manifest Manifest) (pageContent, error) {
	content := pageContent{Anchors: make(map[string]bool)}
	if err := ctx.Err(); err != nil {
		return pageContent{}, err
	}
	sanitized := safeHTMLPolicy.SanitizeBytes(source)
	if err := checkHTMLTokens(ctx, sanitized); err != nil {
		return pageContent{}, err
	}
	parent := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(bytes.NewReader(sanitized), parent)
	if err != nil {
		return pageContent{}, fmt.Errorf("parse HTML: %w", err)
	}
	for _, node := range nodes {
		parent.AppendChild(node)
	}
	count := 0
	if err := walkHTML(ctx, parent, 0, &count, func(node *html.Node) error {
		for _, attribute := range node.Attr {
			if attribute.Key == "id" {
				if err := addAnchor(content.Anchors, attribute.Val); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return pageContent{}, err
	}
	ids := headingIDs{used: make(map[string]bool, len(content.Anchors))}
	for id := range content.Anchors {
		ids.used[id] = true
	}
	count = 0
	if err := walkHTML(ctx, parent, 0, &count, func(node *html.Node) error {
		if node.Type != html.ElementNode {
			return nil
		}
		for i := range node.Attr {
			attribute := &node.Attr[i]
			if (node.Data == "a" && attribute.Key == "href") || (node.Data == "img" && attribute.Key == "src") {
				destination, ref, err := resolveLink(attribute.Val, current, manifest, node.Data == "img")
				if err != nil {
					return err
				}
				attribute.Val = destination
				if ref != nil {
					content.References = append(content.References, *ref)
				}
			}
		}
		if len(node.Data) == 2 && node.Data[0] == 'h' && node.Data[1] >= '1' && node.Data[1] <= '6' {
			if len(content.Headings) >= maxPageHeadings {
				return fmt.Errorf("page exceeds %d headings", maxPageHeadings)
			}
			title := htmlText(node)
			id := ""
			for _, attribute := range node.Attr {
				if attribute.Key == "id" {
					id = attribute.Val
				}
			}
			if id == "" {
				id = string(ids.Generate([]byte(title), 0))
				if err := addAnchor(content.Anchors, id); err != nil {
					return err
				}
				node.Attr = append(node.Attr, html.Attribute{Key: "id", Val: id})
			}
			level, _ := strconv.Atoi(node.Data[1:])
			content.Headings = append(content.Headings, heading{Title: title, ID: id, Level: level})
		}
		return nil
	}); err != nil {
		return pageContent{}, err
	}
	var output htmlOutput
	for node := parent.FirstChild; node != nil; node = node.NextSibling {
		if err := html.Render(&output, node); err != nil {
			return pageContent{}, err
		}
	}
	// Sanitization is the final transform, including canonicalized link values.
	content.HTML = string(safeHTMLPolicy.SanitizeBytes(output.Bytes()))
	content.Text = htmlText(parent)
	content.Blocks = []document.Block{{Kind: document.BlockHTML, Source: document.Location{File: manifest.Pages[current].Source, Line: 1, Column: 1}, Text: content.Text, HTML: content.HTML}}
	return content, ctx.Err()
}

func checkHTMLTokens(ctx context.Context, source []byte) error {
	tokens := html.NewTokenizer(bytes.NewReader(source))
	tokens.SetMaxBuf(maxSourceBytes)
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count > maxHTMLNodes*2 {
			return fmt.Errorf("HTML exceeds %d tokens", maxHTMLNodes*2)
		}
		if tokens.Next() == html.ErrorToken {
			if err := tokens.Err(); err != nil && err != io.EOF {
				return fmt.Errorf("parse HTML tokens: %w", err)
			}
			return nil
		}
	}
}

func walkHTML(ctx context.Context, node *html.Node, depth int, count *int, visit func(*html.Node) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxHTMLDepth {
		return fmt.Errorf("HTML nesting exceeds %d levels", maxHTMLDepth)
	}
	*count++
	if *count > maxHTMLNodes {
		return fmt.Errorf("HTML exceeds %d nodes", maxHTMLNodes)
	}
	if err := visit(node); err != nil {
		return err
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if err := walkHTML(ctx, child, depth+1, count, visit); err != nil {
			return err
		}
	}
	return nil
}

func htmlText(node *html.Node) string {
	var text strings.Builder
	var appendText func(*html.Node)
	appendText = func(current *html.Node) {
		if text.Len() >= 32<<10 {
			return
		}
		if current.Type == html.TextNode {
			remaining := (32 << 10) - text.Len()
			value := current.Data
			if len(value) > remaining {
				value = value[:remaining]
				for !utf8.ValidString(value) {
					value = value[:len(value)-1]
				}
			}
			text.WriteString(value)
		}
		separate := false
		if current.Type == html.ElementNode {
			switch current.Data {
			case "p", "div", "br", "hr", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr", "th", "td", "blockquote", "pre", "details", "summary", "dt", "dd", "caption":
				separate = true
			}
		}
		if separate && text.Len() > 0 && text.Len() < 32<<10 {
			text.WriteByte(' ')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			appendText(child)
		}
		if separate && text.Len() < 32<<10 {
			text.WriteByte(' ')
		}
	}
	appendText(node)
	return strings.Join(strings.Fields(text.String()), " ")
}

type htmlOutput struct{ data bytes.Buffer }

func (output *htmlOutput) Bytes() []byte { return output.data.Bytes() }

func (output *htmlOutput) Write(data []byte) (int, error) {
	if len(data) > maxRenderedBytes-output.data.Len() {
		return 0, fmt.Errorf("HTML exceeds %d rendered bytes", maxRenderedBytes)
	}
	return output.data.Write(data)
}
