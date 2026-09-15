package note

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Relink is one body-link rewrite: every note name Pattern would produce
// for Key, whatever slug it was filled with, becomes Stem.
//
// It exists because a triage note's body links to its own follow-ups
// before they are written, under the slug the triage title predicted. The
// rca run titles the issue again, files under the name that title makes,
// and used to update only the frontmatter — leaving the body pointing at a
// note nobody ever wrote, which in Obsidian is a link that offers to
// create it.
type Relink struct {
	// Pattern is the notes.filenames.<kind> pattern the link was built
	// from, e.g. "{key} RCA {slug}.md".
	Pattern string
	// Key is the ticket key that pattern's "{key}" was filled with.
	Key string
	// Stem is the name the note was actually filed under, without ".md".
	Stem string
}

// UpdateTriageStatus rewrites the leading YAML frontmatter block of the
// triage note at path in place: it sets (or adds) the "status:" line, and
// for each of "rca" and "resolution" present in links, sets (or adds
// immediately before the closing "---") the matching "<key>:" line.
//
// Each relink then rewrites the body's own links to the same notes, so the
// header line a reader clicks leads where the frontmatter says. Nothing
// else in the body is touched: only text matching the filename pattern
// filled with this ticket's key is replaced, so a sentence that happens to
// mention the key is left alone. The file is replaced atomically.
func UpdateTriageStatus(path, status string, links map[string]string, relinks ...Relink) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("note: read %s: %w", path, err)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) < 1 || strings.TrimRight(lines[0], "\r") != "---" {
		return fmt.Errorf("note: %s has no frontmatter", path)
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fmt.Errorf("note: %s frontmatter is not closed", path)
	}

	fm := append([]string(nil), lines[1:end]...)
	body := lines[end:] // starts with the closing "---" line

	fm = setFrontmatterKey(fm, "status", status)
	for _, key := range []string{"rca", "resolution"} {
		val, ok := links[key]
		if !ok {
			continue
		}
		fm = setFrontmatterKey(fm, key, val)
	}

	out := append([]string{"---"}, fm...)
	out = append(out, applyRelinks(body, relinks)...)

	return atomicWriteFile(path, []byte(strings.Join(out, "\n")))
}

// slugExpr matches what Slug produces: lower-case alphanumeric runs joined
// by single dashes. Bounding the "{slug}" placeholder this tightly is what
// keeps a rewrite to the one link it is about — an unbounded wildcard
// would run from a link on the triage note's header line through the
// separator and into the next one.
const slugExpr = `[a-z0-9]+(?:-[a-z0-9]+)*`

// applyRelinks rewrites every predicted link in the note's body to the
// name the note was really filed under.
//
// Two relinks whose patterns match the same text are both dropped rather
// than applied in order: a workspace that files its rca and its resolution
// under the same pattern has no way to tell one link from the other, and
// guessing would point half of them at the wrong note.
func applyRelinks(body []string, relinks []Relink) []string {
	type rule struct {
		re   *regexp.Regexp
		stem string
	}
	var rules []rule
	shape := map[string]int{}
	for _, r := range relinks {
		re := linkPattern(r.Pattern, r.Key)
		if re == nil || strings.TrimSpace(r.Stem) == "" {
			continue
		}
		shape[re.String()]++
		rules = append(rules, rule{re: re, stem: r.Stem})
	}
	for _, r := range rules {
		if shape[r.re.String()] > 1 {
			continue
		}
		for i, line := range body {
			body[i] = r.re.ReplaceAllLiteralString(line, r.stem)
		}
	}
	return body
}

// linkPattern compiles a filename pattern into a regexp matching every
// name it produces for one ticket key: the literal parts as they stand,
// "{key}" as that key, "{slug}" as any slug, and no ".md", since a link in
// the body carries the stem alone. It returns nil for a pattern that names
// neither placeholder, which is a pattern this rewrite cannot locate.
func linkPattern(pattern, key string) *regexp.Regexp {
	stem := strings.TrimSuffix(strings.TrimSpace(pattern), ".md")
	key = strings.TrimSpace(key)
	if stem == "" || key == "" ||
		!strings.Contains(stem, "{key}") || !strings.Contains(stem, "{slug}") {
		return nil
	}
	const keyMark, slugMark = "\x00key\x00", "\x00slug\x00"
	marked := strings.ReplaceAll(stem, "{key}", keyMark)
	marked = strings.ReplaceAll(marked, "{slug}", slugMark)

	expr := regexp.QuoteMeta(marked)
	expr = strings.ReplaceAll(expr, regexp.QuoteMeta(keyMark), regexp.QuoteMeta(key))
	expr = strings.ReplaceAll(expr, regexp.QuoteMeta(slugMark), slugExpr)

	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	return re
}

// KV is one frontmatter key and the value to set it to.
type KV struct{ Key, Value string }

// UpdateFrontmatter sets each pair in the leading YAML frontmatter block of
// the note at path, in the order given: an existing "<key>:" line has its
// value replaced, a key that is not there yet is appended just before the
// closing "---". Everything after the frontmatter is copied through byte for
// byte and the file is replaced atomically, so a note a human has edited
// keeps every word of what they wrote.
//
// Values are written as they arrive. A caller with a value that needs YAML
// quoting — a wiki link, anything with a colon in it — quotes it itself.
func UpdateFrontmatter(path string, pairs []KV) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("note: read %s: %w", path, err)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) < 1 || strings.TrimRight(lines[0], "\r") != "---" {
		return fmt.Errorf("note: %s has no frontmatter", path)
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fmt.Errorf("note: %s frontmatter is not closed", path)
	}

	fm := append([]string(nil), lines[1:end]...)
	for _, kv := range pairs {
		fm = setFrontmatterKey(fm, kv.Key, kv.Value)
	}
	out := append([]string{"---"}, fm...)
	out = append(out, lines[end:]...)
	return atomicWriteFile(path, []byte(strings.Join(out, "\n")))
}

// Frontmatter returns the value of key in the note's leading YAML
// frontmatter block, with surrounding quotes stripped, or "" when there is
// no such block or key.
func Frontmatter(text, key string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	prefix := key + ":"
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return ""
		}
		if strings.HasPrefix(line, prefix) {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"\'`)
		}
	}
	return ""
}

// setFrontmatterKey replaces the value of an existing "<key>:" line in fm,
// or appends a new one when none exists.
func setFrontmatterKey(fm []string, key, value string) []string {
	prefix := key + ":"
	for i, l := range fm {
		if strings.HasPrefix(l, prefix) {
			fm[i] = key + ": " + value
			return fm
		}
	}
	return append(fm, key+": "+value)
}

// atomicWriteFile writes data to path by writing a temp file in the same
// directory and renaming it over path.
func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".note-*.tmp")
	if err != nil {
		return fmt.Errorf("note: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("note: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("note: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("note: rename temp file: %w", err)
	}
	return nil
}
