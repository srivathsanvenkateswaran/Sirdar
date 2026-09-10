package htmltext

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// brMarker is a placeholder inserted for <br> while an inline run is being
// built. It survives whitespace collapsing (it contains no whitespace
// characters) and is turned into a real newline only when a block of text
// is finalized, so a <br> nested inside e.g. <strong> doesn't get flattened
// back into a space by an outer collapse pass.
const brMarker = "\x00br\x00"

var wsRun = regexp.MustCompile(`[ \t\r\n\f\v]+`)

// converter walks a parsed HTML tree and accumulates finished blocks
// (paragraphs, headings, list blocks, code fences, quotes, tables), each
// one already collapsed and trimmed, to be joined with blank lines.
type converter struct {
	plain  bool
	blocks []string
	images []string
}

func (c *converter) result() string {
	var kept []string
	for _, b := range c.blocks {
		if b != "" {
			kept = append(kept, b)
		}
	}
	return strings.Trim(strings.Join(kept, "\n\n"), "\n")
}

func (c *converter) convert(h string) {
	lower := strings.ToLower(h)
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		if doc, err := html.Parse(strings.NewReader(h)); err == nil {
			if body := findBody(doc); body != nil {
				c.renderChildren(body)
				return
			}
		}
	}

	nodes, err := html.ParseFragment(strings.NewReader(h), bodyContext())
	if err != nil {
		if s := c.finishInline(h); s != "" {
			c.blocks = append(c.blocks, s)
		}
		return
	}
	wrapper := bodyContext()
	for _, n := range nodes {
		wrapper.AppendChild(n)
	}
	c.renderChildren(wrapper)
}

func bodyContext() *html.Node {
	return &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
}

func findBody(doc *html.Node) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Body {
			found = n
			return
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)
	return found
}

// isSkipTag reports elements whose content must never reach the output.
func isSkipTag(a atom.Atom) bool {
	switch a {
	case atom.Script, atom.Style, atom.Head, atom.Title, atom.Noscript:
		return true
	}
	return false
}

// isBlockNode reports whether n starts a new block, ending whatever inline
// run precedes it.
func isBlockNode(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch n.DataAtom {
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Header, atom.Footer,
		atom.Main, atom.Aside, atom.Nav, atom.Figure, atom.Figcaption,
		atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6,
		atom.Ul, atom.Ol, atom.Pre, atom.Blockquote, atom.Table, atom.Hr:
		return true
	}
	return false
}

// renderChildren walks n's children, grouping consecutive inline content
// into paragraph blocks and dispatching block-level children to
// renderBlock.
func (c *converter) renderChildren(n *html.Node) {
	var buf []*html.Node
	flush := func() {
		if len(buf) == 0 {
			return
		}
		nodes := buf
		buf = nil
		if s := c.finishInline(c.renderInlineNodes(nodes)); s != "" {
			c.blocks = append(c.blocks, s)
		}
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.CommentNode {
			continue
		}
		if ch.Type == html.ElementNode && isSkipTag(ch.DataAtom) {
			continue
		}
		if isBlockNode(ch) {
			flush()
			c.renderBlock(ch)
			continue
		}
		buf = append(buf, ch)
	}
	flush()
}

func (c *converter) renderBlock(n *html.Node) {
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		c.renderHeading(n)
	case atom.Ul, atom.Ol:
		if s := c.renderList(n, 0); strings.TrimSpace(s) != "" {
			c.blocks = append(c.blocks, s)
		}
	case atom.Pre:
		c.renderPre(n)
	case atom.Blockquote:
		c.renderBlockquote(n)
	case atom.Table:
		if s := c.renderTable(n); s != "" {
			c.blocks = append(c.blocks, s)
		}
	case atom.Hr:
		c.blocks = append(c.blocks, "---")
	default:
		// Generic containers (p, div, section, ...) just group their own
		// children the same way the document root does.
		c.renderChildren(n)
	}
}

func (c *converter) renderHeading(n *html.Node) {
	text := c.finishInline(c.renderInlineChildren(n))
	if text == "" {
		return
	}
	if c.plain {
		c.blocks = append(c.blocks, text)
		return
	}
	level := headingLevel(n.DataAtom)
	c.blocks = append(c.blocks, strings.Repeat("#", level)+" "+text)
}

func headingLevel(a atom.Atom) int {
	switch a {
	case atom.H1:
		return 1
	case atom.H2:
		return 2
	case atom.H3:
		return 3
	case atom.H4:
		return 4
	case atom.H5:
		return 5
	case atom.H6:
		return 6
	}
	return 1
}

func (c *converter) renderPre(n *html.Node) {
	raw := extractRawText(n)
	raw = strings.TrimPrefix(raw, "\r\n")
	raw = strings.TrimPrefix(raw, "\n")
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return
	}
	if c.plain {
		c.blocks = append(c.blocks, raw)
		return
	}
	c.blocks = append(c.blocks, "```\n"+raw+"\n```")
}

func extractRawText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		switch {
		case x.Type == html.TextNode:
			sb.WriteString(x.Data)
			return
		case x.Type == html.ElementNode && (x.DataAtom == atom.Script || x.DataAtom == atom.Style):
			return
		case x.Type == html.ElementNode && x.DataAtom == atom.Br:
			sb.WriteString("\n")
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return sb.String()
}

func (c *converter) renderBlockquote(n *html.Node) {
	sub := &converter{plain: c.plain}
	sub.renderChildren(n)
	c.images = append(c.images, sub.images...)
	inner := strings.Join(sub.blocks, "\n\n")
	if strings.TrimSpace(inner) == "" {
		return
	}
	if c.plain {
		c.blocks = append(c.blocks, inner)
		return
	}
	lines := strings.Split(inner, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	c.blocks = append(c.blocks, strings.Join(lines, "\n"))
}

// renderList renders a <ul>/<ol> as a single block: one line per item,
// nested lists indented by two spaces per depth.
func (c *converter) renderList(n *html.Node, depth int) string {
	ordered := n.DataAtom == atom.Ol
	prefix := strings.Repeat("  ", depth)
	var lines []string
	idx := 1
	for li := n.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.DataAtom != atom.Li {
			continue
		}
		var inline []*html.Node
		var nested []string
		for ch := li.FirstChild; ch != nil; ch = ch.NextSibling {
			switch {
			case ch.Type == html.ElementNode && (ch.DataAtom == atom.Ul || ch.DataAtom == atom.Ol):
				if s := c.renderList(ch, depth+1); strings.TrimSpace(s) != "" {
					nested = append(nested, s)
				}
			case ch.Type == html.CommentNode:
				// dropped
			case ch.Type == html.ElementNode && isSkipTag(ch.DataAtom):
				// dropped
			case ch.Type == html.ElementNode && isBlockNode(ch):
				// e.g. a <p> wrapping the item's own text — flatten its
				// children into this item's inline run instead of
				// starting a fresh block.
				for gc := ch.FirstChild; gc != nil; gc = gc.NextSibling {
					inline = append(inline, gc)
				}
			default:
				inline = append(inline, ch)
			}
		}
		text := c.finishInline(c.renderInlineNodes(inline))
		marker := "- "
		if !c.plain && ordered {
			marker = fmt.Sprintf("%d. ", idx)
		}
		if c.plain {
			marker = ""
		}
		idx++
		lines = append(lines, prefix+marker+text)
		lines = append(lines, nested...)
	}
	return strings.Join(lines, "\n")
}

func (c *converter) renderTable(n *html.Node) string {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type != html.ElementNode {
				continue
			}
			switch ch.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(ch)
			case atom.Tr:
				var cells []string
				for cell := ch.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type != html.ElementNode {
						continue
					}
					if cell.DataAtom != atom.Th && cell.DataAtom != atom.Td {
						continue
					}
					text := c.finishInline(c.renderInlineChildren(cell))
					text = strings.ReplaceAll(text, "\n", " ")
					text = strings.ReplaceAll(text, "|", "\\|")
					cells = append(cells, text)
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			}
		}
	}
	walk(n)
	if len(rows) == 0 {
		return ""
	}
	cols := 0
	for _, r := range rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	pad := func(r []string) []string {
		for len(r) < cols {
			r = append(r, "")
		}
		return r
	}
	if c.plain {
		var lines []string
		for _, r := range rows {
			lines = append(lines, strings.Join(pad(r), " | "))
		}
		return strings.Join(lines, "\n")
	}
	header := pad(rows[0])
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	var sb strings.Builder
	sb.WriteString("| " + strings.Join(header, " | ") + " |\n")
	sb.WriteString("| " + strings.Join(sep, " | ") + " |")
	for _, r := range rows[1:] {
		sb.WriteString("\n| " + strings.Join(pad(r), " | ") + " |")
	}
	return sb.String()
}

// renderInlineNodes concatenates the raw (un-collapsed) inline rendering
// of each node.
func (c *converter) renderInlineNodes(nodes []*html.Node) string {
	var sb strings.Builder
	for _, n := range nodes {
		sb.WriteString(c.renderInline(n))
	}
	return sb.String()
}

// renderInlineChildren is renderInlineNodes over n's own children, with
// dropped nodes (comments, script/style) filtered out first.
func (c *converter) renderInlineChildren(n *html.Node) string {
	var buf []*html.Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == html.CommentNode {
			continue
		}
		if ch.Type == html.ElementNode && isSkipTag(ch.DataAtom) {
			continue
		}
		buf = append(buf, ch)
	}
	return c.renderInlineNodes(buf)
}

func (c *converter) renderInline(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return n.Data
	case html.CommentNode, html.DoctypeNode:
		return ""
	case html.ElementNode:
		switch n.DataAtom {
		case atom.Script, atom.Style, atom.Head, atom.Title, atom.Noscript:
			return ""
		case atom.Br:
			return brMarker
		case atom.Strong, atom.B:
			return c.wrapInline(c.renderInlineChildren(n), "**")
		case atom.Em, atom.I:
			return c.wrapInline(c.renderInlineChildren(n), "*")
		case atom.Code:
			return c.wrapInline(c.renderInlineChildren(n), "`")
		case atom.A:
			return c.renderLink(n)
		case atom.Img:
			return c.renderImg(n)
		default:
			// span, u, mark, font, and any other inline or unknown
			// element: drop the tag, keep its content.
			return c.renderInlineChildren(n)
		}
	default:
		return ""
	}
}

// wrapInline hugs marker to the trimmed inner text, preserving at most one
// space on each side so word spacing across the tag boundary survives.
func (c *converter) wrapInline(inner, marker string) string {
	t := strings.TrimSpace(collapseWS(inner))
	if t == "" {
		return ""
	}
	if c.plain {
		return leadingSpace(inner) + t + trailingSpace(inner)
	}
	return leadingSpace(inner) + marker + t + marker + trailingSpace(inner)
}

func (c *converter) renderLink(n *html.Node) string {
	href, _ := attr(n, "href")
	text := strings.TrimSpace(collapseWS(c.renderInlineChildren(n)))
	if text == "" {
		text = href
	}
	if href == "" || text == href {
		return text
	}
	if c.plain {
		return text + " (" + href + ")"
	}
	return "[" + text + "](" + href + ")"
}

func (c *converter) renderImg(n *html.Node) string {
	src, _ := attr(n, "src")
	alt, _ := attr(n, "alt")
	if src != "" {
		c.images = append(c.images, src)
	}
	if c.plain {
		return alt
	}
	return "![" + alt + "](" + src + ")"
}

func leadingSpace(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if unicode.IsSpace(r[0]) {
		return " "
	}
	return ""
}

func trailingSpace(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if unicode.IsSpace(r[len(r)-1]) {
		return " "
	}
	return ""
}

// collapseWS collapses runs of HTML-insignificant whitespace to a single
// space, the way a browser renders them, and normalizes &nbsp; to a plain
// space. It leaves brMarker untouched.
func collapseWS(s string) string {
	s = strings.ReplaceAll(s, " ", " ")
	return wsRun.ReplaceAllString(s, " ")
}

// finishInline collapses whitespace, trims the ends, and turns any
// surviving brMarker placeholders into real newlines. It is the last step
// applied to a run of inline content before it becomes (part of) a block.
func (c *converter) finishInline(raw string) string {
	s := collapseWS(raw)
	s = strings.ReplaceAll(s, " "+brMarker, brMarker)
	s = strings.ReplaceAll(s, brMarker+" ", brMarker)
	s = strings.ReplaceAll(s, brMarker, "\n")
	return strings.TrimSpace(s)
}
