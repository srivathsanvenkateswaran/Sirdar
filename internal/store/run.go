// Package store manages on-disk run state, event logs, and the run
// register for Sirdar triage and RCA runs.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind identifies the flavor of a run.
type Kind string

const (
	KindTriage Kind = "triage"
	KindRCA    Kind = "rca"
	KindFix    Kind = "fix"
)

// Status identifies where a run is in its lifecycle.
type Status string

const (
	StatusPreparing  Status = "preparing"
	StatusRunning    Status = "running"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusBlocked    Status = "blocked"
	StatusOverBudget Status = "over_budget"
)

// Usage tracks provider consumption for a run.
type Usage struct {
	Turns        int
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}

// State is the persisted state of a single run.
type State struct {
	RunID, Key           string
	Kind                 Kind
	Status               Status
	Provider, Model      string
	Handle               string // provider session/thread id for resume
	Reason               string // failure/blocked reason
	StartedAt, UpdatedAt time.Time
	Usage                Usage
	Budget               struct {
		MaxTurns   int
		MaxMinutes int
		MaxUSD     float64
	}
	Notes      []string // paths written
	StderrTail []string
	Warnings   []string
}

// NewRunID returns a sortable, unique run id of the form
// "20060102T150405Z-abcd".
func NewRunID(now time.Time) string {
	var buf [2]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand.Read on a supported platform does not fail; if it
		// somehow does, fall back to a fixed suffix rather than panicking.
		copy(buf[:], []byte{0, 0})
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(buf[:])
}

// Run identifies one run's directory on disk:
// <root>/.sirdar/runs/<key>/<run-id>.
type Run struct {
	Dir string
}

func runsDir(root string) string {
	return filepath.Join(root, ".sirdar", "runs")
}

// ValidKey reports whether key is safe to use as a path segment. A ticket
// key arrives from the command line and is joined into the runs directory
// and into note filenames, so one carrying a separator or ".." would put a
// run's files somewhere nobody is looking for them.
func ValidKey(key string) bool {
	if key == "" || key == "." || key == ".." {
		return false
	}
	if strings.Contains(key, "..") || strings.ContainsAny(key, `/\`) ||
		strings.ContainsRune(key, os.PathSeparator) {
		return false
	}
	return true
}

// Create makes a new run directory (including its bundle and
// bundle/attachments subdirectories) for key and returns the Run.
func Create(root, key string, now time.Time) (Run, error) {
	if !ValidKey(key) {
		return Run{}, fmt.Errorf("store: invalid run key %q", key)
	}
	runID := NewRunID(now)
	dir := filepath.Join(runsDir(root), key, runID)
	if err := os.MkdirAll(filepath.Join(dir, "bundle", "attachments"), 0o755); err != nil {
		return Run{}, fmt.Errorf("store: create run dir: %w", err)
	}
	return Run{Dir: dir}, nil
}

// Open finds a run by id under any key and returns it along with its
// current state.
func Open(root, runID string) (Run, State, error) {
	matches, err := filepath.Glob(filepath.Join(runsDir(root), "*", runID))
	if err != nil {
		return Run{}, State{}, fmt.Errorf("store: open run %s: %w", runID, err)
	}
	if len(matches) == 0 {
		return Run{}, State{}, fmt.Errorf("store: run %s not found", runID)
	}
	run := Run{Dir: matches[0]}
	state, err := run.ReadState()
	if err != nil {
		return Run{}, State{}, err
	}
	return run, state, nil
}

// BundleDir returns the run's bundle directory.
func (r Run) BundleDir() string {
	return filepath.Join(r.Dir, "bundle")
}

func (r Run) statePath() string {
	return filepath.Join(r.Dir, "state.json")
}

// WriteState persists s to state.json atomically (write to a temp file in
// the same directory, then rename).
func (r Run) WriteState(s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal state: %w", err)
	}
	tmp := r.statePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("store: write state tmp: %w", err)
	}
	if err := os.Rename(tmp, r.statePath()); err != nil {
		return fmt.Errorf("store: rename state: %w", err)
	}
	return nil
}

// ReadState reads and parses state.json for the run.
func (r Run) ReadState() (State, error) {
	data, err := os.ReadFile(r.statePath())
	if err != nil {
		return State{}, fmt.Errorf("store: read state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("store: unmarshal state: %w", err)
	}
	return s, nil
}

// List returns the state of every run under root, newest StartedAt first.
// When key is "" all keys are included; otherwise only that key's runs are
// returned. Run directories with no readable state.json are skipped.
func List(root, key string) ([]State, error) {
	pattern := filepath.Join(runsDir(root), "*", "*")
	if key != "" {
		pattern = filepath.Join(runsDir(root), key, "*")
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("store: list runs: %w", err)
	}

	states := make([]State, 0, len(matches))
	for _, dir := range matches {
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		s, err := (Run{Dir: dir}).ReadState()
		if err != nil {
			continue
		}
		states = append(states, s)
	}

	sort.Slice(states, func(i, j int) bool {
		return states[i].StartedAt.After(states[j].StartedAt)
	})
	return states, nil
}

// LatestNote returns the path to note.md of the newest run for key whose
// Kind is kind and whose Status is "completed" and whose note.md exists on
// disk. It errors if no such run is found.
func LatestNote(root, key string, kind Kind) (string, error) {
	states, err := List(root, key)
	if err != nil {
		return "", err
	}
	for _, s := range states {
		if s.Kind != kind || s.Status != StatusCompleted {
			continue
		}
		notePath := filepath.Join(runsDir(root), s.Key, s.RunID, "note.md")
		if _, err := os.Stat(notePath); err == nil {
			return notePath, nil
		}
	}
	return "", fmt.Errorf("store: no completed %s run for %s", kind, key)
}
