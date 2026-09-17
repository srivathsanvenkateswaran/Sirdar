package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// TestSteerContinuesTheSameRun drives a steer the way `sirdar serve` and
// the desktop do: a triage completes, the steer goes in as a job, the
// same run id comes back running and then completed, and the detail
// screen sees the instruction on the run.
func TestSteerContinuesTheSameRun(t *testing.T) {
	root := newWorkspace(t)
	p := &stubProvider{script: replay(
		provider.Event{Kind: provider.EvUsage, Turns: 3, CostUSD: 0.42},
		finalEvent(triageDoc),
	)}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()
	wsID := WorkspaceID(root)

	if _, err := svc.StartTriage(context.Background(), wsID, []string{"OMNI-1"}, TriageOptions{}); err != nil {
		t.Fatal(err)
	}
	first := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
	runID := first.Outcomes[0].RunID
	if first.Outcomes[0].Status != string(store.StatusCompleted) {
		t.Fatalf("triage ended %+v", first.Outcomes[0])
	}

	steered := strings.Replace(triageDoc, `"hypothesis":"The export job times out."`, `"hypothesis":"The pager drops the last page."`, 1)
	// The session holds its answer until the watcher has seen the run
	// running again; a stub that answered at once could finish inside one
	// poll interval and the transition would be real but unobserved.
	release := make(chan struct{})
	p.script = func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(provider.Event{Kind: provider.EvUsage, Turns: 2, CostUSD: 0.1}) {
			return
		}
		<-release
		s.emit(finalEvent(steered))
	}
	jobID, err := svc.Steer(context.Background(), wsID, runID, "Re-check the pager", "")
	if err != nil {
		t.Fatal(err)
	}

	// The run went back to running on its own id, then completed.
	waitFor(t, events, "run.updated running", func(e Event) bool {
		return e.Kind == KindRunUpdated && e.Run != nil && e.Run.RunID == runID && e.Run.Status == string(store.StatusRunning)
	})
	close(release)
	done := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished && e.JobID == jobID })
	if len(done.Outcomes) != 1 || done.Outcomes[0].RunID != runID || done.Outcomes[0].Status != string(store.StatusCompleted) {
		t.Fatalf("steer outcomes %+v", done.Outcomes)
	}

	runs, err := svc.Runs(wsID, "OMNI-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("a steer made a second run: %+v", runs)
	}
	if runs[0].Usage.Turns != 5 {
		t.Fatalf("usage did not accumulate: %+v", runs[0].Usage)
	}
	detail, err := svc.Run(wsID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Steers) != 1 || detail.Steers[0].Text != "Re-check the pager" || detail.Steers[0].Continuation != "resume" || detail.Steers[0].At == "" {
		t.Fatalf("detail steers: %+v", detail.Steers)
	}
	evs, _, err := svc.Events(wsID, runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, ev := range evs {
		if ev.Kind == "steer" {
			kinds = append(kinds, "steer:"+ev.Payload.Continuation)
			continue
		}
		kinds = append(kinds, ev.Kind)
	}
	if got := strings.Join(kinds, ","); got != "usage,final,steer:resume,usage,final" {
		t.Fatalf("events: %s", got)
	}
}

// TestSteerRefusesSynchronously: what the run's state alone refuses is
// refused before a job exists, as ErrSteerRefused, so a shell can answer
// at once rather than watching a job fail.
func TestSteerRefusesSynchronously(t *testing.T) {
	root := newWorkspace(t)
	writeState(t, root, "OMNI-1", "20260915T090000Z-aaaa", store.StatusRunning)
	writeState(t, root, "OMNI-1", "20260915T090100Z-bbbb", store.StatusCompleted)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{}, stubHelpdesk{}))
	wsID := WorkspaceID(root)

	for _, tc := range []struct{ runID, text, want string }{
		{"20260915T090000Z-aaaa", "go on", "is running"},
		{"20260915T090100Z-bbbb", "   ", "instruction is empty"},
	} {
		_, err := svc.Steer(context.Background(), wsID, tc.runID, tc.text, "")
		if !errors.Is(err, ErrSteerRefused) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Steer(%s, %q) = %v, want ErrSteerRefused saying %q", tc.runID, tc.text, err, tc.want)
		}
	}
	if _, err := svc.Steer(context.Background(), wsID, "20260915T090200Z-cccc", "go on", ""); !errors.Is(err, ErrNoSuchRun) {
		t.Errorf("Steer on an unknown run: %v", err)
	}
	if jobs := svc.Jobs(); len(jobs) != 0 {
		t.Errorf("a refused steer started a job: %v", jobs)
	}
}
