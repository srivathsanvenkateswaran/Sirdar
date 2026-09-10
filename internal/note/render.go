package note

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Meta carries the values a note's frontmatter and header need that are not
// part of the validated JSON document: identifiers, dates, and the run that
// produced it.
type Meta struct {
	Key, TrackerURL, HelpdeskID, HelpdeskURL, Customer, CustomerID string
	Date, DateReported                                             string // YYYY-MM-DD
	Priority, Service                                              string
	RunID, Provider                                                string
	Links                                                          struct {
		Triage, RCA, Resolution string // wiki-link targets (file stems)
	}
}

// Renderer renders notes from Go text/template files. TemplatesDir, when
// set, is checked first for a "<kind>.md.tmpl" override before falling back
// to the embedded default for that kind.
type Renderer struct {
	TemplatesDir string // "" = embedded defaults
}

//go:embed templates/*.md.tmpl
var embeddedTemplates embed.FS

//go:embed samples/*.json
var sampleDocs embed.FS

var templateFuncs = template.FuncMap{
	"join":    joinFunc,
	"fill":    fillFunc,
	"date":    dateFunc,
	"default": defaultFunc,
	"yq":      yqFunc,
}

func templateFilename(kind Kind) string {
	return string(kind) + ".md.tmpl"
}

// Render decodes doc as JSON and executes the template for kind against
// map[string]any{"doc": <decoded doc>, "meta": m}. For RCA and Resolution,
// doc is the combined rca+resolution document, and the template reads
// .doc.rca or .doc.resolution.
func (r Renderer) Render(kind Kind, doc []byte, m Meta) (string, error) {
	tmpl, err := r.loadTemplate(kind)
	if err != nil {
		return "", err
	}

	var decoded any
	if err := json.Unmarshal(doc, &decoded); err != nil {
		return "", fmt.Errorf("note: parse document: %w", err)
	}

	data := map[string]any{"doc": decoded, "meta": m}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("note: render %s template: %w", kind, err)
	}
	return buf.String(), nil
}

// loadTemplate parses the template for kind: the override in
// r.TemplatesDir when one exists, else the embedded default.
func (r Renderer) loadTemplate(kind Kind) (*template.Template, error) {
	name := templateFilename(kind)

	if r.TemplatesDir != "" {
		path := filepath.Join(r.TemplatesDir, name)
		if _, err := os.Stat(path); err == nil {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("note: read template %s: %w", path, err)
			}
			tmpl, err := template.New(name).Funcs(templateFuncs).Parse(string(raw))
			if err != nil {
				return nil, fmt.Errorf("note: parse template %s: %w", path, err)
			}
			return tmpl, nil
		}
	}

	raw, err := embeddedTemplates.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("note: read embedded template %s: %w", name, err)
	}
	tmpl, err := template.New(name).Funcs(templateFuncs).Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("note: parse embedded template %s: %w", name, err)
	}
	return tmpl, nil
}

// DefaultTemplate returns the embedded default template for kind, which is
// what `sirdar init --templates` writes into a workspace as the starting
// point for a vault-specific override.
func DefaultTemplate(kind Kind) ([]byte, error) {
	name := templateFilename(kind)
	raw, err := embeddedTemplates.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("note: read embedded template %s: %w", name, err)
	}
	return raw, nil
}

// Check parses and renders every kind's active template (a custom override
// when configured, else the embedded default) against a built-in sample
// document, to catch a broken template before it is used on a real run. It
// also parses the rendered frontmatter block with a YAML parser, so a
// custom template that produces invalid YAML (an unescaped value, say) is
// rejected here rather than surfacing as a broken note later. Used by
// doctor and init.
func (r Renderer) Check() error {
	for _, kind := range []Kind{Triage, RCA, Resolution} {
		doc, meta, err := sampleFor(kind)
		if err != nil {
			return err
		}
		rendered, err := r.Render(kind, doc, meta)
		if err != nil {
			return fmt.Errorf("note: check %s template: %w", kind, err)
		}
		fm, err := extractFrontmatter(rendered)
		if err != nil {
			return fmt.Errorf("note: check %s template: %w", kind, err)
		}
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(fm), &parsed); err != nil {
			return fmt.Errorf("note: check %s template: rendered frontmatter is not valid YAML: %w", kind, err)
		}
	}
	return nil
}

// extractFrontmatter returns the YAML content between the leading and
// closing "---" lines of rendered, the way UpdateTriageStatus locates the
// same block: line by line, looking for an exact "---" line.
func extractFrontmatter(rendered string) (string, error) {
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return "", fmt.Errorf("note: rendered output has no frontmatter")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return strings.Join(lines[1:i], "\n"), nil
		}
	}
	return "", fmt.Errorf("note: frontmatter is not closed")
}

// sampleFor returns the embedded sample document and a matching Meta for
// kind. Triage uses samples/triage.json; RCA and Resolution share
// samples/rca.json, the combined document.
func sampleFor(kind Kind) ([]byte, Meta, error) {
	m := Meta{
		Key:          "OMNI-1",
		TrackerURL:   "https://tracker.example/OMNI-1",
		HelpdeskID:   "12345",
		HelpdeskURL:  "https://helpdesk.example/12345",
		Customer:     "Example Corp",
		CustomerID:   "cust-1",
		Date:         "2026-09-10",
		DateReported: "2026-09-01",
		Priority:     "high",
		Service:      "omni",
		RunID:        "run-1",
		Provider:     "claude-code",
	}

	var file string
	switch kind {
	case Triage:
		file = "samples/triage.json"
	case RCA, Resolution:
		file = "samples/rca.json"
		m.Links.Triage = "OMNI-1 sample-issue"
	default:
		return nil, Meta{}, fmt.Errorf("note: unknown kind %q", kind)
	}

	doc, err := sampleDocs.ReadFile(file)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("note: read sample %s: %w", file, err)
	}
	return doc, m, nil
}

// Filename fills pattern's "{key}" and "{slug}" placeholders, e.g.
// Filename("{key} {slug}.md", "OMNI-1", "export-fails") ->
// "OMNI-1 export-fails.md".
func Filename(pattern, key, slug string) string {
	f := strings.ReplaceAll(pattern, "{key}", key)
	f = strings.ReplaceAll(f, "{slug}", slug)
	return f
}

var (
	slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
	slugTrimDash = regexp.MustCompile(`^-+|-+$`)
)

const slugMaxLen = 60

// Slug lowercases title, maps every run of non-alphanumeric characters to a
// single hyphen, trims leading and trailing hyphens, and caps the result at
// 60 characters.
func Slug(title string) string {
	s := strings.ToLower(title)
	s = slugNonAlnum.ReplaceAllString(s, "-")
	s = slugTrimDash.ReplaceAllString(s, "")
	if len(s) > slugMaxLen {
		s = s[:slugMaxLen]
		s = slugTrimDash.ReplaceAllString(s, "")
	}
	return s
}

// --- template funcs ---

// joinFunc joins a []string or []any (as produced by decoding a JSON array)
// with sep. Used in templates as {{join ", " .doc.reproSteps}}.
func joinFunc(sep string, v any) string {
	switch vals := v.(type) {
	case nil:
		return ""
	case []string:
		return strings.Join(vals, sep)
	case []any:
		parts := make([]string, len(vals))
		for i, e := range vals {
			parts[i] = fmt.Sprint(e)
		}
		return strings.Join(parts, sep)
	default:
		return fmt.Sprint(v)
	}
}

// fillFunc returns v as a string when it is a non-empty string, else a
// visible "<fill: NAME>" marker. Used to mark audit fields the agent
// couldn't source, so a human fills them in by hand.
func fillFunc(name string, v any) string {
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	return fmt.Sprintf("<fill: %s>", name)
}

// dateFunc passes a date string through unchanged. It exists so templates
// have a single named place to format dates from, should that ever be
// needed.
func dateFunc(s string) string {
	return s
}

// defaultFunc returns v as a string when it is non-empty, else fallback.
// Used in templates as {{.doc.foo | default "n/a"}}.
func defaultFunc(fallback string, v any) string {
	switch t := v.(type) {
	case nil:
		return fallback
	case string:
		if strings.TrimSpace(t) == "" {
			return fallback
		}
		return t
	default:
		return fmt.Sprint(v)
	}
}

// yqFunc renders v as a YAML double-quoted scalar: backslash, double quote,
// and control characters are escaped (newline as \n, tab as \t, carriage
// return as \r, anything else below 0x20 or 0x7f as \xHH), so the result is
// always a valid frontmatter value regardless of what the underlying string
// contains — a colon, a leading "#", embedded quotes, non-Latin text. Used
// on every scalar frontmatter value in the note templates, including
// "<fill: NAME>" markers, which contain ": " and would otherwise produce
// invalid YAML as a plain (unquoted) scalar.
func yqFunc(v any) string {
	s := toYQString(v)
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func toYQString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		return fmt.Sprint(v)
	}
}
