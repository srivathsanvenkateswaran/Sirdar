// Package eval replays stored ticket bundles through a real triage run and
// scores what comes out. A golden set is a directory of bundles a human has
// already triaged by hand, with, optionally, the note they wrote and a short
// list of assertions they were prepared to stand behind. Scoring one is
// therefore a comparison against a person's answer, not against another
// model's.
//
// Nothing here simulates a run: eval hands internal/run the bundle instead
// of the ticket sources and lets the same preparation, session, validation
// and filing happen. What is scored is a run.
package eval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultDir is where a golden set lives unless the operator names another
// one. It is outside the repository on purpose: the bundles hold real
// customer conversations and belong nowhere near a git remote.
const DefaultDir = "~/.sirdar/golden"

// Golden is one key's entry in a golden set: the bundle a run replays, and
// the two optional files a human writes to say what a good answer looks
// like.
type Golden struct {
	Key        string
	Dir        string
	BundleDir  string
	ExpectedMD string // path to expected.md, "" when there is none
	Expected   []Expectation
}

// Expectation is one assertion out of expected.json, already parsed into
// the path it reads, the comparison it makes, and the value it wants.
type Expectation struct {
	// Key is the entry exactly as it was written in expected.json, which
	// is what a failure report quotes back.
	Key  string          `json:"key"`
	Path string          `json:"path"`
	Kind Kind            `json:"kind"`
	Want json.RawMessage `json:"want"`
}

// Kind is the comparison an expectation makes.
type Kind string

const (
	// KindEquals compares the value at a dot path for equality.
	KindEquals Kind = "equals"
	// KindContains requires every wanted string to appear as a substring
	// of at least one element of the array at the path.
	KindContains Kind = "contains"
	// KindMin requires the array at the path to hold at least n elements.
	KindMin Kind = "min"
)

// suffixes maps the suffix a key carries to the comparison it asks for.
// A key with no suffix is an equality check.
var suffixes = []struct {
	suffix string
	kind   Kind
}{
	{"_contains", KindContains},
	{"_min", KindMin},
}

// ExpandDir expands a leading "~/" in a golden-set path. A relative path is
// left as it is, so a caller can point at a fixture directory.
func ExpandDir(dir string) string {
	if dir == "" {
		dir = DefaultDir
	}
	if rest, ok := strings.CutPrefix(dir, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return dir
}

// Keys lists every key in a golden set, sorted, skipping any directory with
// no bundle in it.
func Keys(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("eval: read golden set %s: %w", root, err)
	}
	var keys []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "bundle", "ticket.json")); err != nil {
			continue
		}
		keys = append(keys, e.Name())
	}
	sort.Strings(keys)
	return keys, nil
}

// Load reads one key's golden entry. A missing expected.json is not an
// error: a bundle on its own still scores the two things that need no human
// answer, whether the note validated and whether the run completed.
func Load(root, key string) (Golden, error) {
	g := Golden{Key: key, Dir: filepath.Join(root, key)}
	g.BundleDir = filepath.Join(g.Dir, "bundle")
	if _, err := os.Stat(filepath.Join(g.BundleDir, "ticket.json")); err != nil {
		return g, fmt.Errorf("eval: %s has no bundle: %w", key, err)
	}
	if path := filepath.Join(g.Dir, "expected.md"); fileExists(path) {
		g.ExpectedMD = path
	}

	path := filepath.Join(g.Dir, "expected.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return g, nil
	}
	if err != nil {
		return g, fmt.Errorf("eval: read %s: %w", path, err)
	}
	g.Expected, err = ParseExpectations(data)
	if err != nil {
		return g, fmt.Errorf("eval: %s: %w", path, err)
	}
	return g, nil
}

// ParseExpectations turns an expected.json object into assertions. Entries
// are returned in key order, so a report reads the same way twice.
func ParseExpectations(data []byte) ([]Expectation, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("not a JSON object of assertions: %w", err)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]Expectation, 0, len(keys))
	for _, k := range keys {
		e := Expectation{Key: k, Path: k, Kind: KindEquals, Want: raw[k]}
		for _, s := range suffixes {
			if p, ok := strings.CutSuffix(k, s.suffix); ok {
				e.Path, e.Kind = p, s.kind
				break
			}
		}
		if e.Path == "" {
			return nil, fmt.Errorf("assertion %q names no field", k)
		}
		out = append(out, e)
	}
	return out, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// copyTree copies src over dst recursively. Symlinks are skipped: a golden
// bundle is data, and a link in one has nothing to point at elsewhere.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
