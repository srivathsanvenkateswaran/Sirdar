package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// Added reports what `sirdar golden add` wrote.
type Added struct {
	Key          string
	RunID        string
	Dir          string
	BundleDir    string
	ExpectedPath string
	// SkeletonWritten is false when an expected.json was already there and
	// was left alone: it is the human's file, and a second `golden add`
	// must not overwrite the assertions they trimmed.
	SkeletonWritten bool
}

// Add copies a completed run's bundle into the golden set under key and
// writes an expected.json skeleton from the note that run produced. The
// skeleton holds the three things worth asserting mechanically — the
// confidence, the classification, and the code the note pointed at — and is
// meant to be trimmed by hand: an assertion nobody chose to keep is an
// assertion that will fail for the wrong reason six months from now.
//
// runID may be empty, in which case the newest completed triage run for the
// key is used.
func Add(root, goldenRoot, key, runID string) (Added, error) {
	a := Added{Key: key, RunID: runID}
	if !store.ValidKey(key) {
		return a, fmt.Errorf("eval: %q is not a usable ticket key", key)
	}

	runDir, runID, err := findRun(root, key, runID)
	if err != nil {
		return a, err
	}
	a.RunID = runID

	bundle := filepath.Join(runDir, "bundle")
	if _, err := os.Stat(filepath.Join(bundle, "ticket.json")); err != nil {
		return a, fmt.Errorf("eval: run %s has no bundle to copy: %w", runID, err)
	}

	a.Dir = filepath.Join(ExpandDir(goldenRoot), key)
	a.BundleDir = filepath.Join(a.Dir, "bundle")
	if err := os.MkdirAll(a.BundleDir, 0o755); err != nil {
		return a, fmt.Errorf("eval: create %s: %w", a.BundleDir, err)
	}
	if err := copyTree(bundle, a.BundleDir); err != nil {
		return a, fmt.Errorf("eval: copy bundle: %w", err)
	}

	a.ExpectedPath = filepath.Join(a.Dir, "expected.json")
	if fileExists(a.ExpectedPath) {
		return a, nil
	}
	doc, err := os.ReadFile(filepath.Join(runDir, "result.json"))
	if err != nil {
		// A bundle without a note is still a usable golden entry: the
		// human can write the assertions from scratch.
		return a, nil
	}
	skeleton, err := Skeleton(doc)
	if err != nil {
		return a, err
	}
	if err := os.WriteFile(a.ExpectedPath, skeleton, 0o644); err != nil {
		return a, fmt.Errorf("eval: write %s: %w", a.ExpectedPath, err)
	}
	a.SkeletonWritten = true
	return a, nil
}

// findRun resolves the run directory to copy from: the named run, or the
// newest completed triage run for the key.
func findRun(root, key, runID string) (string, string, error) {
	if runID != "" {
		run, state, err := store.Open(root, runID)
		if err != nil {
			return "", "", err
		}
		if state.Key != key {
			return "", "", fmt.Errorf("eval: run %s belongs to %s, not %s", runID, state.Key, key)
		}
		return run.Dir, runID, nil
	}
	states, err := store.List(root, key)
	if err != nil {
		return "", "", err
	}
	for _, s := range states {
		if s.Kind == store.KindTriage && s.Status == store.StatusCompleted {
			return filepath.Join(root, ".sirdar", "runs", key, s.RunID), s.RunID, nil
		}
	}
	return "", "", fmt.Errorf("eval: no completed triage run for %s; run `sirdar triage %s` first", key, key)
}

// noteFields are the parts of a triage document the skeleton is built from.
type noteFields struct {
	Classification string `json:"classification"`
	RootCause      struct {
		Confidence string   `json:"confidence"`
		CodeRefs   []string `json:"codeRefs"`
	} `json:"rootCause"`
}

// Skeleton renders the starting expected.json for a produced triage
// document. Code references are trimmed to the file, without the line
// number: a line moves with the next commit, and an assertion that breaks
// on an unrelated edit teaches nobody anything.
func Skeleton(doc []byte) ([]byte, error) {
	var f noteFields
	if err := json.Unmarshal(doc, &f); err != nil {
		return nil, fmt.Errorf("eval: parse triage note: %w", err)
	}
	out := map[string]any{}
	if f.RootCause.Confidence != "" {
		out["rootCause.confidence"] = f.RootCause.Confidence
	}
	if f.Classification != "" {
		out["classification"] = f.Classification
	}
	if files := refFiles(f.RootCause.CodeRefs); len(files) > 0 {
		out["rootCause.codeRefs_contains"] = files
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("eval: marshal skeleton: %w", err)
	}
	return append(data, '\n'), nil
}

// refFiles strips the ":line" from each "path:line" reference and drops
// duplicates, keeping the order the note listed them in.
func refFiles(refs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ref := range refs {
		file := ref
		if i := strings.LastIndex(ref, ":"); i > 0 {
			file = ref[:i]
		}
		file = strings.TrimSpace(file)
		if file == "" || seen[file] {
			continue
		}
		seen[file] = true
		out = append(out, file)
	}
	return out
}
