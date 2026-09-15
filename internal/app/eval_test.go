package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// newEvalService returns a service whose golden set is a directory of the
// test's own, so nothing here reads or writes ~/.sirdar/golden.
func newEvalService(t *testing.T, root, golden string) *Service {
	t.Helper()
	svc := New(newRegistry(t, root), stubBuilder(nil, nil, nil), Options{
		Interval:  20 * time.Millisecond,
		GoldenDir: golden,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	t.Cleanup(svc.Stop)
	return svc
}

// writeGolden puts one entry in a golden set: the bundle a run replays and,
// when assertions are given, the file a human trimmed.
func writeGolden(t *testing.T, golden, key, assertions string) {
	t.Helper()
	dir := filepath.Join(golden, key, "bundle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ticket.json"), []byte(`{"key":"`+key+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if assertions != "" {
		if err := os.WriteFile(filepath.Join(golden, key, "expected.json"), []byte(assertions), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writeTriageRun writes a completed triage run with the bundle and the
// document `sirdar golden add` copies.
func writeTriageRun(t *testing.T, root, key string) string {
	t.Helper()
	rn, err := store.Create(root, key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runID := filepath.Base(rn.Dir)
	if err := os.WriteFile(filepath.Join(rn.Dir, "bundle", "ticket.json"), []byte(`{"key":"`+key+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rn.Dir, "result.json"), []byte(triageDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	err = rn.WriteState(store.State{
		RunID: runID, Key: key, Kind: store.KindTriage, Status: store.StatusCompleted,
		Provider: "claude", Model: "sonnet",
		StartedAt: time.Now().Add(-time.Minute), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return runID
}

func TestGoldenListsTheSet(t *testing.T) {
	root, golden := newWorkspace(t), t.TempDir()
	writeGolden(t, golden, "OMNI-1", `{"classification":"code","rootCause.confidence":"high"}`)
	writeGolden(t, golden, "OMNI-2", "")
	svc := newEvalService(t, root, golden)

	entries, err := svc.Golden(WorkspaceID(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries %+v", entries)
	}
	if entries[0].Key != "OMNI-1" || entries[0].Assertions != 2 {
		t.Fatalf("first entry %+v", entries[0])
	}
	if entries[1].Key != "OMNI-2" || entries[1].Assertions != 0 {
		t.Fatalf("second entry %+v", entries[1])
	}
	if entries[0].HasExpectedNote {
		t.Error("no expected.md was written, so none should be reported")
	}
}

// A golden set nobody has added to yet is an empty list, not an error: the
// screen says so and offers the button that fills it.
func TestGoldenOnMissingDirectoryIsEmpty(t *testing.T) {
	root := newWorkspace(t)
	svc := newEvalService(t, root, filepath.Join(t.TempDir(), "never-created"))

	entries, err := svc.Golden(WorkspaceID(root))
	if err != nil {
		t.Fatalf("a golden set that does not exist: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries %+v", entries)
	}
}

func TestAddGoldenTakesTheKeyFromTheRun(t *testing.T) {
	root, golden := newWorkspace(t), t.TempDir()
	runID := writeTriageRun(t, root, "OMNI-7")
	svc := newEvalService(t, root, golden)

	entry, err := svc.AddGolden(WorkspaceID(root), "", runID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Key != "OMNI-7" {
		t.Fatalf("entry %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(golden, "OMNI-7", "bundle", "ticket.json")); err != nil {
		t.Fatalf("the bundle was not copied: %v", err)
	}
	// The skeleton is written from the run's own note, which is what
	// gives the entry its assertions.
	if entry.Assertions == 0 {
		t.Fatal("no assertion skeleton was written")
	}
	if _, err := svc.AddGolden(WorkspaceID(root), "", ""); err == nil {
		t.Fatal("neither a key nor a run id: want an error")
	}
	if _, err := svc.AddGolden(WorkspaceID(root), "", "no-such-run"); err == nil {
		t.Fatal("an unknown run id: want an error")
	}
}

func TestEvalReportsNewestFirst(t *testing.T) {
	root, golden := newWorkspace(t), t.TempDir()
	dir := filepath.Join(root, ".sirdar", "eval")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	older := eval.Report{Provider: "claude", Model: "sonnet", Results: []eval.Result{{Key: "OMNI-1", Passed: 1, Total: 2}}}
	newer := eval.Report{Provider: "codex", Model: "gpt", Results: []eval.Result{{Key: "OMNI-1", Passed: 2, Total: 2, SchemaValid: true}}}
	for name, rep := range map[string]eval.Report{
		"20260910T090000Z.json": older,
		"20260911T090000Z.json": newer,
	} {
		data, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A file that is not a report must not cost the list its reports.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("scratch"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := newEvalService(t, root, golden)

	reports, err := svc.EvalReports(WorkspaceID(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports %+v", reports)
	}
	if reports[0].Provider != "codex" {
		t.Fatalf("newest report is %+v", reports[0])
	}
	if !strings.HasSuffix(reports[0].Path, "20260911T090000Z.json") {
		t.Fatalf("path %q", reports[0].Path)
	}
	if len(reports[0].Results) != 1 || reports[0].Results[0].Passed != 2 {
		t.Fatalf("results %+v", reports[0].Results)
	}
}

// An eval of an empty golden set fails as a job rather than as a request:
// nothing to replay is a finding, and it reaches the activity pane.
func TestStartEvalWithoutGoldenBundles(t *testing.T) {
	root, golden := newWorkspace(t), t.TempDir()
	svc := newEvalService(t, root, golden)
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	jobID, err := svc.StartEval(context.Background(), WorkspaceID(root), nil, EvalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	logged := waitFor(t, events, "the empty-golden-set log line", func(e Event) bool {
		return e.Kind == KindLog && strings.Contains(e.Text, "holds no golden bundles")
	})
	if logged.Text == "" {
		t.Fatal("the log event carried no text")
	}
	waitFor(t, events, "job.finished", func(e Event) bool {
		return e.Kind == KindJobFinished && e.JobID == jobID
	})
}

func TestStartEvalRejectsBadKeys(t *testing.T) {
	root := newWorkspace(t)
	svc := newEvalService(t, root, t.TempDir())

	if _, err := svc.StartEval(context.Background(), WorkspaceID(root), []string{"../etc"}, EvalOptions{}); err == nil {
		t.Fatal("a key with a path separator: want an error")
	}
	if _, err := svc.StartEval(context.Background(), "nosuch", nil, EvalOptions{}); err == nil {
		t.Fatal("an unknown workspace: want an error")
	}
}

// A retro report is a different table with different columns. It is
// written into the same directory and must not turn up in the eval list as
// a row with nothing in it.
func TestRetroReportsStayOutOfTheEvalList(t *testing.T) {
	root, golden := newWorkspace(t), t.TempDir()
	dir := filepath.Join(root, ".sirdar", "eval")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(eval.Report{
		At: time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC), Provider: "claude",
		Results: []eval.Result{{Key: "OMNI-1", State: "completed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "20260911T090000Z.json"), plain, 0o644); err != nil {
		t.Fatal(err)
	}
	retro, err := json.Marshal(eval.RetroReport{
		At: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC), Provider: "codex", Rubric: true,
		Results: []eval.RetroResult{{
			Key: "OMNI-1", BaseCommit: "abc123", CostUSD: 3.75,
			Triage:      &eval.Stage{RunID: "r-triage", State: "completed"},
			TriageScore: &eval.TriageScore{Classification: "code", Confidence: "high"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "20260915T090000Z"+eval.RetroSuffix), retro, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := newEvalService(t, root, golden)

	reports, err := svc.EvalReports(WorkspaceID(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].Provider != "claude" {
		t.Fatalf("eval reports %+v", reports)
	}

	latest, err := svc.LatestRetro(WorkspaceID(root))
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil {
		t.Fatal("no retro report")
	}
	if latest.Provider != "codex" || len(latest.Results) != 1 || latest.Results[0].Key != "OMNI-1" {
		t.Fatalf("retro report %+v", latest)
	}
	if !strings.HasSuffix(latest.Path, eval.RetroSuffix) {
		t.Errorf("path %q", latest.Path)
	}
}

func TestLatestRetroWithoutOneIsNil(t *testing.T) {
	root, golden := newWorkspace(t), t.TempDir()
	svc := newEvalService(t, root, golden)
	latest, err := svc.LatestRetro(WorkspaceID(root))
	if err != nil {
		t.Fatal(err)
	}
	if latest != nil {
		t.Errorf("got %+v, want nil", latest)
	}
	if _, err := svc.LatestRetro("nosuch"); err == nil {
		t.Error("an unknown workspace is an error")
	}
}

// A retro job runs against the keys that carry a retro.json, and a golden
// set where no key does says so by name rather than reporting an empty
// table.
func TestStartRetroEvalReportsAGoldenSetWithNoRetroEntry(t *testing.T) {
	root, golden := newWorkspace(t), t.TempDir()
	svc := newEvalService(t, root, golden)
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	if _, err := svc.StartEval(context.Background(), WorkspaceID(root), nil, EvalOptions{Retro: true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, "the no-retro-bundles log line", func(e Event) bool {
		return e.Kind == KindLog && strings.Contains(e.Text, eval.RetroFile)
	})
}
