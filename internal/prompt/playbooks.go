package prompt

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// defaultPlaybooks holds the five scaffold playbooks written by
// ScaffoldPlaybooks, embedded at build time.
//
//go:embed playbooks/*.md
var defaultPlaybooks embed.FS

const defaultPlaybooksSubdir = "playbooks"

// Playbook is one playbook loaded from a workspace's playbooks directory:
// its name (the filename without the .md extension), its markdown body with
// any frontmatter block removed, and what that block said about when the
// playbook applies.
type Playbook struct {
	Name, Body string
	// Kinds is nil when the playbook names none, which means every kind.
	Kinds []string
	// When is empty for an always-on playbook.
	When []string
	// About is frontmatter `about`, else the body's lede (TitleAndLede).
	About string
	// Path is the file's absolute path, for the listing line.
	Path string
	// FrontmatterErr is why a frontmatter block was ignored; "" when it parsed or was absent.
	FrontmatterErr string
}

// LedeMax is how much of a playbook's first paragraph a summary carries:
// the Settings list's row and a listed playbook's line in the prompt are one
// line of prose beside a name, not a preview.
const LedeMax = 160

// playbookKinds are the run kinds a playbook's `kinds` may name. They are
// spelled as store.Kind spells them; the package does not import store to
// say so, because the prompt stays free of the run's own types.
var playbookKinds = map[string]bool{"session": true, "triage": true, "rca": true, "fix": true}

// frontmatter is the YAML block a playbook may open with. Its keys are the
// whole vocabulary: an unknown one is refused rather than ignored, because
// a misspelt `when` would otherwise make a scoped playbook always-on with
// nobody told.
type frontmatter struct {
	Kinds []string `yaml:"kinds"`
	When  []string `yaml:"when"`
	About string   `yaml:"about"`
}

// ParsePlaybook builds the Playbook for one file's contents. A file whose
// first line is exactly `---` opens a frontmatter block that ends at the
// next line that is exactly `---`; the block is removed from the body.
//
// A block that does not parse, names an unknown key or kind, or carries an
// `about` that is not one short line is ignored and the reason is kept in
// FrontmatterErr: the playbook then loads as if it had no frontmatter. That
// fails open on purpose, because a typo should cost prompt bytes, not hide
// a gotcha the operator wrote down. A block with no closing line is no
// block at all, and the whole file is the body.
func ParsePlaybook(name, path, data string) Playbook {
	// An editor that saves with a UTF-8 byte-order mark puts it before the
	// opening ---, and the block would otherwise be read as body text: the
	// playbook would load always-on with no warning to say why.
	data = strings.TrimPrefix(data, "\uFEFF")
	p := Playbook{Name: name, Body: data, Path: path}
	if block, rest, found, closed := splitFrontmatter(data); found {
		if !closed {
			p.FrontmatterErr = "no closing ---"
		} else {
			p.Body = rest
			fm, err := decodeFrontmatter(block)
			if err != nil {
				p.FrontmatterErr = err.Error()
			} else {
				// An empty `kinds: []` would read as "no kind at all", which
				// hides the playbook from every run; it is taken as naming
				// none, the same as leaving the key out.
				if len(fm.Kinds) > 0 {
					p.Kinds = fm.Kinds
				}
				for _, w := range fm.When {
					if w = strings.TrimSpace(w); w != "" {
						p.When = append(p.When, w)
					}
				}
				p.About = fm.About
			}
		}
	}
	if p.About == "" {
		_, p.About = TitleAndLede(p.Body)
	}
	return p
}

// splitFrontmatter finds the frontmatter block at the top of data. found
// is whether the first line opens one; closed is whether a later line ends
// it. rest is data after the closing line, that line's newline included.
func splitFrontmatter(data string) (block, rest string, found, closed bool) {
	lines := strings.SplitAfter(data, "\n")
	if fenceLine(lines[0]) != "---" {
		return "", data, false, false
	}
	start := len(lines[0])
	off := start
	for _, line := range lines[1:] {
		if fenceLine(line) == "---" {
			return data[start:off], data[off+len(line):], true, true
		}
		off += len(line)
	}
	return "", data, true, false
}

// fenceLine is line without its line ending, so a file saved with \r\n
// opens and closes its block the same as one saved with \n.
func fenceLine(line string) string {
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
}

// decodeFrontmatter reads a frontmatter block strictly: unknown keys, kinds
// outside the four run kinds, and an `about` that would not fit on the
// listing line are all errors.
func decodeFrontmatter(block string) (frontmatter, error) {
	var fm frontmatter
	dec := yaml.NewDecoder(bytes.NewReader([]byte(block)))
	dec.KnownFields(true)
	// A block with no YAML document in it (empty, or only comments)
	// decodes to io.EOF: a block that says nothing, not a broken one.
	if err := dec.Decode(&fm); err != nil && !errors.Is(err, io.EOF) {
		return frontmatter{}, err
	}
	for _, k := range fm.Kinds {
		if !playbookKinds[k] {
			return frontmatter{}, fmt.Errorf("unknown kind %q", k)
		}
	}
	if strings.ContainsAny(fm.About, "\r\n") {
		return frontmatter{}, fmt.Errorf("about is more than one line")
	}
	if utf8.RuneCountInString(fm.About) > LedeMax {
		return frontmatter{}, fmt.Errorf("about is longer than %d characters", LedeMax)
	}
	return fm, nil
}

// FrontmatterWarnings is one warning per playbook whose frontmatter was
// ignored, worded for the run's warnings list.
func FrontmatterWarnings(all []Playbook) []string {
	var out []string
	for _, p := range all {
		if p.FrontmatterErr != "" {
			out = append(out, "playbook "+p.Name+": frontmatter ignored: "+p.FrontmatterErr)
		}
	}
	return out
}

// TitleAndLede reads a playbook's first H1 and its first paragraph.
//
// The lede skips headings, list bullets, quotes and fences: what is wanted
// is the sentence that says what the playbook is for, and a playbook that
// opens on a bullet list has none.
func TitleAndLede(body string) (title, lede string) {
	var para []string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			if title == "" && strings.HasPrefix(line, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			}
			if len(para) > 0 {
				break
			}
			continue
		}
		if line == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "---") {
			if len(para) > 0 {
				break
			}
			continue
		}
		if len(para) == 0 && (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") ||
			strings.HasPrefix(line, "> ") || strings.HasPrefix(line, "|")) {
			continue
		}
		para = append(para, line)
	}
	return title, clip(strings.Join(para, " "), LedeMax)
}

// clip cuts s to at most n characters, on a word boundary where there is
// one, and marks the cut with an ellipsis.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	cut := string(runes[:n])
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}

// LoadPlaybooks reads every *.md file directly inside dir, in filename
// order, and returns one Playbook per file, its frontmatter parsed and
// stripped by ParsePlaybook. A dir that does not exist is
// not an error: LoadPlaybooks returns (nil, nil) so a workspace with no
// playbooks configured still gets a valid, empty prompt section.
func LoadPlaybooks(dir string) ([]Playbook, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("prompt: read playbooks dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	playbooks := make([]Playbook, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("prompt: read playbook %s: %w", name, err)
		}
		path, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("prompt: resolve playbook %s: %w", name, err)
		}
		playbooks = append(playbooks, ParsePlaybook(strings.TrimSuffix(name, ".md"), path, string(data)))
	}
	return playbooks, nil
}

// ScaffoldPlaybooks writes the five embedded default playbooks into dir,
// creating dir if it does not exist. It never overwrites a file that is
// already there, so a workspace's edits to a scaffolded playbook (or a
// playbook it wrote itself under the same name) survive a re-run of
// `sirdar init`.
func ScaffoldPlaybooks(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("prompt: create playbooks dir %s: %w", dir, err)
	}

	entries, err := defaultPlaybooks.ReadDir(defaultPlaybooksSubdir)
	if err != nil {
		return fmt.Errorf("prompt: read embedded playbooks: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dst := filepath.Join(dir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // never overwrite an existing playbook
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("prompt: stat %s: %w", dst, err)
		}

		// An embedded FS is addressed with "/" on every OS, so the name
		// is joined with path.Join: filepath.Join would ask for
		// "playbooks\10-helpdesk.md" on Windows and find nothing.
		data, err := defaultPlaybooks.ReadFile(path.Join(defaultPlaybooksSubdir, e.Name()))
		if err != nil {
			return fmt.Errorf("prompt: read embedded playbook %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("prompt: write playbook %s: %w", dst, err)
		}
	}
	return nil
}
