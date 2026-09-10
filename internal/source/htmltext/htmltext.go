// Package htmltext converts HTML fragments or documents, as returned by
// issue trackers and helpdesk systems, into readable Markdown-flavoured
// text. Jira, Azure DevOps, Rally, Zendesk and Freshdesk all hand back rich
// HTML for descriptions and comments; this package gives every adapter a
// single, consistent way to turn that into text an agent can read and a
// human can skim, without pulling in a full HTML rendering stack.
package htmltext

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ToMarkdown converts an HTML fragment or document to readable
// Markdown-flavoured text: paragraphs separated by blank lines; <br> →
// newline; <h1..h6> → "#".."######"; <strong>/<b> → **x**; <em>/<i> → *x*;
// <code> → `x`; <pre> → fenced block; <ul>/<ol> → "- " / "1. " items with
// nesting by two spaces; <a href> → [text](href) (bare text when href ==
// text); <img src alt> → ![alt](src), with src also appended to the
// returned images slice; <table> → pipe table (header from <th>, else the
// first row); <blockquote> → "> " prefix; scripts, styles and comments are
// dropped; entities are decoded; whitespace is collapsed like a browser
// would; leading and trailing blank lines are trimmed. It never panics on
// malformed HTML.
func ToMarkdown(h string) (text string, images []string) {
	c := &converter{plain: false}
	c.convert(h)
	return c.result(), c.images
}

// ToPlain is ToMarkdown without any markers: links render as "text (href)"
// and there is no emphasis, headings, or list/table markup — just the
// running text, suitable for a one-line summary.
func ToPlain(h string) string {
	c := &converter{plain: true}
	c.convert(h)
	return c.result()
}

// InlineImageSrcs returns every <img src> found in h, in document order.
// Adapters use it to find inline attachments worth downloading without
// paying for a full Markdown conversion.
func InlineImageSrcs(h string) []string {
	nodes, err := html.ParseFragment(strings.NewReader(h), &html.Node{
		Type:     html.ElementNode,
		Data:     "body",
		DataAtom: atom.Body,
	})
	if err != nil {
		return nil
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Img {
			if src, ok := attr(n, "src"); ok && src != "" {
				out = append(out, src)
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

// attr returns the value of the named attribute on n, if present.
func attr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}
