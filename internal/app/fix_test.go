package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// writeFixRun writes a completed fix run's state.json the way internal/fix
// leaves it: the branch and commit it made, and whatever it recorded about
// the push.
func writeFixRun(t *testing.T, root, key string, mutate func(*store.State)) string {
	t.Helper()
	rn, err := store.Create(root, key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runID := filepath.Base(rn.Dir)
	state := store.State{
		RunID: runID, Key: key, Kind: store.KindFix, Status: store.StatusCompleted,
		Provider: "claude", Model: "sonnet",
		StartedAt: time.Now().Add(-time.Minute), UpdatedAt: time.Now(),
	}
	mutate(&state)
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}
	return runID
}

// A fix run's branch, commit, pull request and deviation reach the run
// detail screen, because they are on the run's own state.json and nowhere
// else a UI could read them.
func TestRunDetailCarriesFixState(t *testing.T) {
	root := newWorkspace(t)
	runID := writeFixRun(t, root, "OMNI-1", func(s *store.State) {
		s.Fix.Branch = "fix/OMNI-1-export"
		s.Fix.Base = "main"
		s.Fix.Commit = "abc123"
		s.Fix.Deviation = "the note asked for streaming; I raised the timeout instead"
	})
	svc := newService(t, root, stubBuilder(nil, nil, nil))

	detail, err := svc.Run(WorkspaceID(root), runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Kind != string(store.KindFix) {
		t.Fatalf("kind %q, want fix", detail.Kind)
	}
	if detail.Fix == nil {
		t.Fatal("detail carries no fix state")
	}
	if detail.Fix.Branch != "fix/OMNI-1-export" || detail.Fix.Commit != "abc123" || detail.Fix.Base != "main" {
		t.Fatalf("fix state %+v", *detail.Fix)
	}
	if !strings.Contains(detail.Fix.Deviation, "raised the timeout") {
		t.Fatalf("deviation %q", detail.Fix.Deviation)
	}
	if detail.Fix.PRURL != "" {
		t.Fatalf("a blocked fix has no pull request, got %q", detail.Fix.PRURL)
	}

	// The JSON is what the frontend reads; `fix` is absent for a run that
	// recorded nothing, so a triage run is not given an empty object.
	data, err := json.Marshal(DetailOf(root, store.State{RunID: "r", Key: "OMNI-1", Kind: store.KindTriage}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"fix"`) {
		t.Fatalf("a triage run's detail carries a fix object: %s", data)
	}
}

// A fix that cannot start — no approved triage note — finishes as a failed
// job with the reason on the activity pane, rather than hanging or
// producing a run nobody asked for.
func TestStartFixWithoutTriageNoteFails(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, nil, nil))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	jobID, err := svc.StartFix(context.Background(), WorkspaceID(root), "OMNI-1", FixOptions{})
	if err != nil {
		t.Fatal(err)
	}

	done := waitFor(t, events, "job.finished", func(e Event) bool {
		return e.Kind == KindJobFinished && e.JobID == jobID
	})
	if len(done.Outcomes) != 1 || done.Outcomes[0].Status != string(store.StatusFailed) {
		t.Fatalf("outcomes %+v", done.Outcomes)
	}
	// Nothing was written: a fix refused before the branch exists leaves
	// no run directory behind.
	if entries, err := os.ReadDir(filepath.Join(root, ".sirdar", "runs")); err == nil && len(entries) > 0 {
		t.Fatalf("a refused fix wrote runs: %v", entries)
	}
}

func TestStartFixRejectsBadIdentifiers(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(nil, nil, nil))

	if _, err := svc.StartFix(context.Background(), WorkspaceID(root), "../etc", FixOptions{}); err == nil {
		t.Fatal("a key with a path separator: want an error")
	}
	if _, err := svc.StartFix(context.Background(), "nosuch", "OMNI-1", FixOptions{}); err == nil {
		t.Fatal("an unknown workspace: want an error")
	}
}
