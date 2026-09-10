package htmltext

import (
	"reflect"
	"testing"
)

func TestToMarkdown(t *testing.T) {
	tests := []struct {
		name       string
		html       string
		wantText   string
		wantImages []string
	}{
		{
			name:     "paragraphs",
			html:     "<p>First paragraph.</p><p>Second paragraph.</p>",
			wantText: "First paragraph.\n\nSecond paragraph.",
		},
		{
			name:     "br within paragraph",
			html:     "<p>Line one<br>Line two<br/>Line three</p>",
			wantText: "Line one\nLine two\nLine three",
		},
		{
			name:     "headings",
			html:     "<h1>Title</h1><h2>Subtitle</h2><h6>Tiny</h6>",
			wantText: "# Title\n\n## Subtitle\n\n###### Tiny",
		},
		{
			name:     "strong and b",
			html:     "<p><strong>bold</strong> and <b>also bold</b></p>",
			wantText: "**bold** and **also bold**",
		},
		{
			name:     "em and i",
			html:     "<p><em>emphasis</em> and <i>also emphasis</i></p>",
			wantText: "*emphasis* and *also emphasis*",
		},
		{
			name:     "inline code",
			html:     "<p>Run <code>go test ./...</code> now.</p>",
			wantText: "Run `go test ./...` now.",
		},
		{
			name:     "pre fenced block",
			html:     "<pre>\nfunc main() {\n\tprintln(\"hi\")\n}\n</pre>",
			wantText: "```\nfunc main() {\n\tprintln(\"hi\")\n}\n```",
		},
		{
			name:     "unordered list",
			html:     "<ul><li>one</li><li>two</li></ul>",
			wantText: "- one\n- two",
		},
		{
			name:     "ordered list",
			html:     "<ol><li>first</li><li>second</li></ol>",
			wantText: "1. first\n2. second",
		},
		{
			name: "nested list",
			html: "<ul><li>parent<ul><li>child one</li><li>child two</li></ul></li>" +
				"<li>sibling</li></ul>",
			wantText: "- parent\n  - child one\n  - child two\n- sibling",
		},
		{
			name:     "emphasis inside link",
			html:     `<p>See <a href="https://example.com/doc"><strong>the docs</strong></a> for more.</p>`,
			wantText: "See [**the docs**](https://example.com/doc) for more.",
		},
		{
			name:     "link with bare text equal to href",
			html:     `<p>Visit <a href="https://example.com">https://example.com</a> today.</p>`,
			wantText: "Visit https://example.com today.",
		},
		{
			name:       "image with alt",
			html:       `<p>See <img src="https://example.com/shot.png" alt="a screenshot"> below.</p>`,
			wantText:   "See ![a screenshot](https://example.com/shot.png) below.",
			wantImages: []string{"https://example.com/shot.png"},
		},
		{
			name: "table with th header",
			html: "<table><thead><tr><th>Name</th><th>Status</th></tr></thead>" +
				"<tbody><tr><td>build</td><td>green</td></tr>" +
				"<tr><td>deploy</td><td>pending</td></tr></tbody></table>",
			wantText: "| Name | Status |\n| --- | --- |\n| build | green |\n| deploy | pending |",
		},
		{
			name:     "table with no th, first row becomes header",
			html:     "<table><tr><td>Col A</td><td>Col B</td></tr><tr><td>1</td><td>2</td></tr></table>",
			wantText: "| Col A | Col B |\n| --- | --- |\n| 1 | 2 |",
		},
		{
			name:     "blockquote",
			html:     "<blockquote><p>First line.</p><p>Second line.</p></blockquote>",
			wantText: "> First line.\n>\n> Second line.",
		},
		{
			name:     "scripts styles and comments dropped",
			html:     "<style>.x{color:red}</style><script>alert('x')</script><!-- note --><p>Kept text.</p>",
			wantText: "Kept text.",
		},
		{
			name:     "entities decoded",
			html:     "<p>Fish &amp; chips &mdash; caf&eacute;</p>",
			wantText: "Fish & chips — café",
		},
		{
			name:     "whitespace collapsed like a browser",
			html:     "<p>Too    many\n\n   spaces   and\ttabs</p>",
			wantText: "Too many spaces and tabs",
		},
		{
			name:     "leading and trailing blank lines trimmed",
			html:     "\n\n<p>Only paragraph.</p>\n\n",
			wantText: "Only paragraph.",
		},
		{
			name: "jira zendesk style div span soup with nbsp",
			html: "<div><span>Customer reports that</span>&nbsp;<span>the <strong>export</strong> button</span></div>" +
				"<div>is unresponsive on <em>Safari</em>.</div>",
			wantText: "Customer reports that the **export** button\n\nis unresponsive on *Safari*.",
		},
		{
			name:     "rally style p with br",
			html:     "<p>Steps to reproduce:<br/>1. Open the app<br/>2. Click submit<br/>3. Observe crash</p>",
			wantText: "Steps to reproduce:\n1. Open the app\n2. Click submit\n3. Observe crash",
		},
		{
			name: "ado description with table",
			html: "<div><p>Repro details below.</p><table><tr><th>Field</th><th>Value</th></tr>" +
				"<tr><td>Environment</td><td>Staging</td></tr></table></div>",
			wantText: "Repro details below.\n\n| Field | Value |\n| --- | --- |\n| Environment | Staging |",
		},
		{
			name:     "malformed unclosed tags and stray closing div",
			html:     "<p>Unclosed paragraph<div>Nested without close</div></p></div><p>Trailer</p>",
			wantText: "Unclosed paragraph\n\nNested without close\n\nTrailer",
		},
		{
			name:     "empty input",
			html:     "",
			wantText: "",
		},
		{
			name:     "rtl arabic paragraph preserved verbatim",
			html:     "<p>يرجى مراجعة هذا الطلب في أقرب وقت ممكن.</p>",
			wantText: "يرجى مراجعة هذا الطلب في أقرب وقت ممكن.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotImages := ToMarkdown(tt.html)
			if gotText != tt.wantText {
				t.Errorf("ToMarkdown() text =\n%q\nwant\n%q", gotText, tt.wantText)
			}
			if !reflect.DeepEqual(gotImages, tt.wantImages) && !(len(gotImages) == 0 && len(tt.wantImages) == 0) {
				t.Errorf("ToMarkdown() images = %v, want %v", gotImages, tt.wantImages)
			}
		})
	}
}

func TestToMarkdownNeverPanics(t *testing.T) {
	inputs := []string{
		"<",
		"<<<>>>",
		"<p><p><p>",
		"</div></div></div>",
		"<table><tr><td>",
		"<ul><li><ul><li>",
		"<script>",
		"not html at all, just text",
		"<a href=\"x\">",
		"<img src=",
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("ToMarkdown(%q) panicked: %v", in, r)
				}
			}()
			ToMarkdown(in)
			ToPlain(in)
			InlineImageSrcs(in)
		}()
	}
}

func TestToPlain(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "no emphasis markers",
			html: "<p><strong>bold</strong> and <em>italic</em> and <code>code</code></p>",
			want: "bold and italic and code",
		},
		{
			name: "link as text (href)",
			html: `<p>See <a href="https://example.com/doc">the docs</a>.</p>`,
			want: "See the docs (https://example.com/doc).",
		},
		{
			name: "link bare when text equals href",
			html: `<a href="https://example.com">https://example.com</a>`,
			want: "https://example.com",
		},
		{
			name: "heading has no hash",
			html: "<h2>Section Title</h2><p>Body.</p>",
			want: "Section Title\n\nBody.",
		},
		{
			name: "paragraphs still blank-line separated",
			html: "<p>One.</p><p>Two.</p>",
			want: "One.\n\nTwo.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToPlain(tt.html)
			if got != tt.want {
				t.Errorf("ToPlain() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInlineImageSrcs(t *testing.T) {
	tests := []struct {
		name string
		html string
		want []string
	}{
		{
			name: "document order, duplicates kept",
			html: `<p><img src="a.png"></p><div><img src="b.png" alt="b"><img src="a.png"></div>`,
			want: []string{"a.png", "b.png", "a.png"},
		},
		{
			name: "no images",
			html: "<p>No pictures here.</p>",
			want: nil,
		},
		{
			name: "img with empty src ignored",
			html: `<img src=""><img src="c.png">`,
			want: []string{"c.png"},
		},
		{
			name: "empty input",
			html: "",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InlineImageSrcs(tt.html)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("InlineImageSrcs() = %v, want %v", got, tt.want)
			}
		})
	}
}
