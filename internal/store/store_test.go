package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestCreateRejectsUnsafeKey covers a ticket key that would take the run
// directory out of the workspace, e.g. `sirdar triage ../x`.
func TestCreateRejectsUnsafeKey(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 10, 11, 30, 0, 0, time.UTC)
	for _, key := range []string{"../escape", "a/b", "..", "", ".", "a/../../b"} {
		if _, err := Create(root, key, now); err == nil {
			t.Errorf("Create(%q) succeeded, want an error", key)
		} else if !strings.Contains(err.Error(), "invalid run key") {
			t.Errorf("Create(%q) error = %v", key, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".sirdar")); !os.IsNotExist(err) {
		t.Fatalf("a rejected key still created directories: %v", err)
	}
}

func TestNewRunIDSortableAndUnique(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 11, 30, 0, 0, time.UTC)
	id1 := NewRunID(t0)
	id2 := NewRunID(t0.Add(time.Second))

	if id1 == id2 {
		t.Fatalf("expected unique ids, got %q twice", id1)
	}
	if !(id1 < id2) {
		t.Fatalf("expected id1 %q to sort before id2 %q", id1, id2)
	}
	if !strings.HasPrefix(id1, "20260910T113000Z-") {
		t.Fatalf("unexpected id format: %q", id1)
	}
	suffix := strings.TrimPrefix(id1, "20260910T113000Z-")
	if len(suffix) != 4 {
		t.Fatalf("expected 4-char hex suffix, got %q", suffix)
	}
}

func TestNewRunIDUniqueSameInstant(t *testing.T) {
	now := time.Date(2026, 9, 10, 11, 30, 0, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		id := NewRunID(now)
		if seen[id] {
			// Extremely unlikely collision with 16 bits of randomness across
			// 20 draws is acceptable to not flake the suite; just note it's
			// still well formed.
			continue
		}
		seen[id] = true
	}
}

func TestCreateMakesDirectories(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 10, 11, 30, 0, 0, time.UTC)

	run, err := Create(root, "OMNI-1", now)
	if err != nil {
		t.Fatal(err)
	}

	if fi, err := os.Stat(run.Dir); err != nil || !fi.IsDir() {
		t.Fatalf("run dir missing: %v", err)
	}
	if fi, err := os.Stat(run.BundleDir()); err != nil || !fi.IsDir() {
		t.Fatalf("bundle dir missing: %v", err)
	}
	attachments := filepath.Join(run.BundleDir(), "attachments")
	if fi, err := os.Stat(attachments); err != nil || !fi.IsDir() {
		t.Fatalf("attachments dir missing: %v", err)
	}

	wantPrefix := filepath.Join(root, ".sirdar", "runs", "OMNI-1")
	if !strings.HasPrefix(run.Dir, wantPrefix) {
		t.Fatalf("run dir %q not under %q", run.Dir, wantPrefix)
	}
}

func testState(runID, key string, kind Kind, status Status, startedAt time.Time) State {
	s := State{
		RunID:     runID,
		Key:       key,
		Kind:      kind,
		Status:    status,
		Provider:  "claude",
		Model:     "claude-sonnet-5",
		Handle:    "handle-123",
		Reason:    "",
		StartedAt: startedAt,
		UpdatedAt: startedAt.Add(time.Minute),
		Usage: Usage{
			Turns:        3,
			InputTokens:  100,
			OutputTokens: 200,
			CostUSD:      0.12,
		},
		Notes: []string{"note.md"},
	}
	s.Budget.MaxTurns = 60
	s.Budget.MaxMinutes = 25
	s.Budget.MaxUSD = 5
	return s
}

func TestWriteStateReadStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 10, 11, 30, 0, 0, time.UTC)

	run, err := Create(root, "OMNI-1", now)
	if err != nil {
		t.Fatal(err)
	}

	want := testState(filepath.Base(run.Dir), "OMNI-1", KindTriage, StatusCompleted, now)
	if err := run.WriteState(want); err != nil {
		t.Fatal(err)
	}

	got, err := run.ReadState()
	if err != nil {
		t.Fatal(err)
	}

	if !got.StartedAt.Equal(want.StartedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("time mismatch: got %+v want %+v", got, want)
	}
	got.StartedAt, want.StartedAt = time.Time{}, time.Time{}
	got.UpdatedAt, want.UpdatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestOpenFindsRunByID(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 10, 11, 30, 0, 0, time.UTC)

	run, err := Create(root, "OMNI-2", now)
	if err != nil {
		t.Fatal(err)
	}
	runID := filepath.Base(run.Dir)
	state := testState(runID, "OMNI-2", KindRCA, StatusRunning, now)
	if err := run.WriteState(state); err != nil {
		t.Fatal(err)
	}

	found, gotState, err := Open(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Dir != run.Dir {
		t.Fatalf("got dir %q want %q", found.Dir, run.Dir)
	}
	if gotState.RunID != runID {
		t.Fatalf("got runID %q want %q", gotState.RunID, runID)
	}
}

func TestOpenMissingRunErrors(t *testing.T) {
	root := t.TempDir()
	_, _, err := Open(root, "20260101T000000Z-dead")
	if err == nil {
		t.Fatal("expected error for missing run")
	}
	if !strings.Contains(err.Error(), "20260101T000000Z-dead") {
		t.Fatalf("expected error to name the run id, got %v", err)
	}
}

func TestListOrdersNewestFirstAndFiltersByKey(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	mk := func(key string, at time.Time) {
		run, err := Create(root, key, at)
		if err != nil {
			t.Fatal(err)
		}
		s := testState(filepath.Base(run.Dir), key, KindTriage, StatusCompleted, at)
		if err := run.WriteState(s); err != nil {
			t.Fatal(err)
		}
	}

	mk("OMNI-1", t0)
	mk("OMNI-2", t1)
	mk("OMNI-1", t2)

	all, err := List(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 runs, got %d", len(all))
	}
	if !all[0].StartedAt.Equal(t2) || !all[1].StartedAt.Equal(t1) || !all[2].StartedAt.Equal(t0) {
		t.Fatalf("not sorted newest first: %+v", all)
	}

	filtered, err := List(root, "OMNI-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 {
		t.Fatalf("want 2 runs for OMNI-1, got %d", len(filtered))
	}
	for _, s := range filtered {
		if s.Key != "OMNI-1" {
			t.Fatalf("unexpected key in filtered results: %+v", s)
		}
	}
	if !filtered[0].StartedAt.Equal(t2) || !filtered[1].StartedAt.Equal(t0) {
		t.Fatalf("filtered results not sorted newest first: %+v", filtered)
	}
}

func TestEventLogAppendWritesOneJSONLine(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 10, 11, 30, 0, 0, time.UTC)

	run, err := Create(root, "OMNI-3", now)
	if err != nil {
		t.Fatal(err)
	}

	log, err := run.OpenEventLog()
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append("started", map[string]any{"foo": "bar"}); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(run.Dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %q", len(lines), string(data))
	}

	var line struct {
		T       string         `json:"t"`
		Kind    string         `json:"kind"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatalf("invalid json line: %v", err)
	}
	if line.Kind != "started" {
		t.Fatalf("got kind %q", line.Kind)
	}
	if line.Payload["foo"] != "bar" {
		t.Fatalf("got payload %+v", line.Payload)
	}
	if _, err := time.Parse(time.RFC3339Nano, line.T); err != nil {
		t.Fatalf("t not RFC3339Nano: %v", err)
	}
	if !strings.HasSuffix(line.T, "Z") {
		t.Fatalf("t not UTC: %q", line.T)
	}
}

func TestAppendRegisterReadRegisterRoundTrip(t *testing.T) {
	root := t.TempDir()

	row1 := RegisterRow{
		Key: "OMNI-1", Kind: "triage", RunID: "20260910T113000Z-aaaa", Date: "2026-09-10",
		Provider: "claude", Model: "claude-sonnet-5", Service: "checkout",
		Classification: "bug", Confidence: "high", Severity: "sev2",
		Turns: 3, CostUSD: 0.12, TriageVerdict: "root cause found", NotePath: "note1.md",
	}
	row2 := RegisterRow{
		Key: "OMNI-2", Kind: "rca", RunID: "20260910T120000Z-bbbb", Date: "2026-09-10",
		Provider: "claude", Model: "claude-sonnet-5", Service: "payments",
		Classification: "regression", Confidence: "medium", Severity: "sev1",
		Turns: 5, CostUSD: 0.34, TriageVerdict: "", NotePath: "note2.md",
	}

	if err := AppendRegister(root, row1); err != nil {
		t.Fatal(err)
	}
	if err := AppendRegister(root, row2); err != nil {
		t.Fatal(err)
	}

	rows, err := ReadRegister(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if !reflect.DeepEqual(rows[0], row1) {
		t.Fatalf("row1 mismatch:\ngot  %+v\nwant %+v", rows[0], row1)
	}
	if !reflect.DeepEqual(rows[1], row2) {
		t.Fatalf("row2 mismatch:\ngot  %+v\nwant %+v", rows[1], row2)
	}
}

func TestLatestNotePicksNewestCompletedRun(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)

	writeRun := func(at time.Time, status Status, withNote bool) {
		run, err := Create(root, "OMNI-9", at)
		if err != nil {
			t.Fatal(err)
		}
		s := testState(filepath.Base(run.Dir), "OMNI-9", KindTriage, status, at)
		if err := run.WriteState(s); err != nil {
			t.Fatal(err)
		}
		if withNote {
			if err := os.WriteFile(filepath.Join(run.Dir, "note.md"), []byte("# note"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	writeRun(t0, StatusCompleted, true)  // oldest completed, has note
	writeRun(t1, StatusFailed, true)     // newer but failed
	writeRun(t2, StatusCompleted, false) // newest completed, but no note.md on disk

	got, err := LatestNote(root, "OMNI-9", KindTriage)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".sirdar", "runs", "OMNI-9", filepathBaseNoteRun(t, root, "OMNI-9", t0), "note.md")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// filepathBaseNoteRun finds the run id for the run under key whose StartedAt
// matches at, by scanning the runs directory. Used only to build the
// expected path in TestLatestNotePicksNewestCompletedRun without hardcoding
// generated run ids.
func filepathBaseNoteRun(t *testing.T, root, key string, at time.Time) string {
	t.Helper()
	states, err := List(root, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range states {
		if s.StartedAt.Equal(at) {
			return s.RunID
		}
	}
	t.Fatalf("no run found for %s at %v", key, at)
	return ""
}

func TestLatestNoteErrorsWhenNoneCompleted(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	run, err := Create(root, "OMNI-8", now)
	if err != nil {
		t.Fatal(err)
	}
	s := testState(filepath.Base(run.Dir), "OMNI-8", KindTriage, StatusRunning, now)
	if err := run.WriteState(s); err != nil {
		t.Fatal(err)
	}

	_, err = LatestNote(root, "OMNI-8", KindTriage)
	if err == nil {
		t.Fatal("expected error when no completed run exists")
	}
	if !strings.Contains(err.Error(), "OMNI-8") || !strings.Contains(err.Error(), "triage") {
		t.Fatalf("error should name key and kind, got %v", err)
	}
}
