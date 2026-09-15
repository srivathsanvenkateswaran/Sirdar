package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Retro is the `retro.json` a golden entry carries when the ticket's real
// fix is known: the state of the world the agent is put back into, and the
// pull request a human actually merged, which is what its answer is scored
// against.
//
// It is written by `sirdar golden add --retro` (retro-a); nothing here
// writes one.
type Retro struct {
	Key string `json:"key"`
	// AsOf is the cutoff the bundle was captured at: the moment work
	// started on the ticket, before any comment naming the fix.
	AsOf time.Time `json:"asOf"`
	// BaseCommit is the commit the pull request branched from. Every
	// retro run stands at it, so the agent sees the repository as the
	// engineer saw it.
	BaseCommit string `json:"baseCommit"`
	// PRUrls are the merged pull requests the change landed in.
	PRUrls []string `json:"prUrls"`
	// PRDiff is the unified diff's file name, relative to the golden
	// entry's directory. Empty means "pr.diff".
	PRDiff string `json:"prDiff"`
	// PRFiles are the paths that pull request touched.
	PRFiles  []string `json:"prFiles"`
	Redacted Redacted `json:"redacted"`

	// Dir is the golden entry's directory, filled in by LoadRetro rather
	// than read from the file.
	Dir string `json:"-"`
}

// Redacted counts what the as-of capture took out of the bundle, so a
// reader of a retro score can tell how much of the ticket the agent was
// deliberately not shown.
type Redacted struct {
	PRLinks         int `json:"prLinks"`
	CommentsDropped int `json:"commentsDropped"`
}

// RetroFile is the name a golden entry's retro descriptor carries.
const RetroFile = "retro.json"

// defaultPRDiff is the diff's file name when retro.json names none.
const defaultPRDiff = "pr.diff"

// DiffPath is the absolute path of the pull request's diff.
func (r Retro) DiffPath() string {
	name := r.PRDiff
	if name == "" {
		name = defaultPRDiff
	}
	return filepath.Join(r.Dir, name)
}

// ReadDiff parses the pull request's diff.
func (r Retro) ReadDiff() (Diff, error) {
	data, err := os.ReadFile(r.DiffPath())
	if err != nil {
		return Diff{}, fmt.Errorf("eval: read %s: %w", r.DiffPath(), err)
	}
	return ParseDiff(string(data)), nil
}

// Files are the pull request's paths, normalised. retro.json's own list is
// preferred, because it is what the human's tooling recorded; a retro.json
// that carries none falls back to the diff.
func (r Retro) Files(prDiff Diff) []string {
	source := r.PRFiles
	if len(source) == 0 {
		source = prDiff.Paths()
	}
	out := make([]string, 0, len(source))
	for _, p := range source {
		if p = NormalizePath(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// LoadRetro reads one golden key's retro descriptor. A key with no
// retro.json is not an error anywhere it is asked for optionally; callers
// that need one check HasRetro first.
func LoadRetro(root, key string) (Retro, error) {
	dir := filepath.Join(root, key)
	path := filepath.Join(dir, RetroFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Retro{}, fmt.Errorf("eval: read %s: %w", path, err)
	}
	var r Retro
	if err := json.Unmarshal(data, &r); err != nil {
		return Retro{}, fmt.Errorf("eval: parse %s: %w", path, err)
	}
	r.Dir = dir
	if r.Key == "" {
		r.Key = key
	}
	if r.BaseCommit == "" {
		return r, fmt.Errorf("eval: %s names no baseCommit", path)
	}
	if _, err := os.Stat(r.DiffPath()); err != nil {
		return r, fmt.Errorf("eval: %s: %w", key, err)
	}
	return r, nil
}

// HasRetro reports whether a golden key can be replayed as a retro.
func HasRetro(root, key string) bool {
	return fileExists(filepath.Join(root, key, RetroFile))
}

// RetroKeys lists the golden keys that carry a retro.json, sorted. It is
// what `sirdar eval --retro` replays when it is given no keys.
func RetroKeys(root string) ([]string, error) {
	keys, err := Keys(root)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if HasRetro(root, k) {
			out = append(out, k)
		}
	}
	return out, nil
}
