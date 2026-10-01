package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		{"20260915T090000Z-aaaa", "   ", "instruction is empty"},
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

// TestSteerOnARunningRunIsQueued: a run that is still working is not
// refused. The instruction goes into its inbox for the executor that owns
// it, and no job is started — the empty job id says so.
func TestSteerOnARunningRunIsQueued(t *testing.T) {
	root := newWorkspace(t)
	writeState(t, root, "OMNI-1", "20260915T090000Z-aaaa", store.StatusRunning)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{}, stubHelpdesk{}))
	id, err := svc.Steer(context.Background(), WorkspaceID(root), "20260915T090000Z-aaaa", "  also check the export worker ", "")
	if err != nil || id != "" {
		t.Fatalf("Steer = %q, %v; want a queued steer and no job", id, err)
	}
	if jobs := svc.Jobs(); len(jobs) != 0 {
		t.Errorf("a queued steer started a job: %v", jobs)
	}
	rn, _, err := store.Open(root, "20260915T090000Z-aaaa")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := rn.SteerInbox()
	if err != nil || len(inbox) != 1 || inbox[0].Text != "also check the export worker" {
		t.Fatalf("inbox = %+v, %v", inbox, err)
	}
}

// waitForInbox holds a stub session until a steer is in its run's inbox,
// so the turn boundary that follows is guaranteed to find it.
func waitForInbox(dir string) {
	for {
		if inbox, _ := (store.Run{Dir: dir}).SteerInbox(); len(inbox) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestLiveSteerDeliveredThroughTheService is the whole path as the desktop
// takes it: a triage job is working, a steer arrives through the service,
// the executor sends it into the session at the turn boundary, and the run
// completes once — one job, one session, the steer on the record as live.
func TestLiveSteerDeliveredThroughTheService(t *testing.T) {
	root := newWorkspace(t)
	running := make(chan string, 1)
	steered := strings.Replace(triageDoc, `"hypothesis":"The export job times out."`, `"hypothesis":"The pager drops the last page."`, 1)
	p := &stubProvider{}
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		s.emit(provider.Event{Kind: provider.EvUsage, Turns: 3})
		running <- filepath.Base(spec.RunDir)
		waitForInbox(spec.RunDir)
		if !s.emit(finalEvent(triageDoc)) {
			return
		}
		select {
		case <-s.sent:
		case <-time.After(5 * time.Second):
			return
		}
		s.emit(provider.Event{Kind: provider.EvUsage, Turns: 4})
		s.emit(finalEvent(steered))
	}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()
	wsID := WorkspaceID(root)
	if _, err := svc.StartTriage(context.Background(), wsID, []string{"OMNI-1"}, TriageOptions{}); err != nil {
		t.Fatal(err)
	}
	runID := <-running
	if id, err := svc.Steer(context.Background(), wsID, runID, "Re-check the pager", ""); err != nil || id != "" {
		t.Fatalf("Steer = %q, %v; want queued", id, err)
	}
	done := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
	if len(done.Outcomes) != 1 || done.Outcomes[0].Status != string(store.StatusCompleted) {
		t.Fatalf("outcomes %+v", done.Outcomes)
	}
	_, st, err := store.Open(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.QueuedSteers) != 1 || st.QueuedSteers[0].Status != store.SteerDelivered || st.QueuedSteers[0].Turn != 3 {
		t.Fatalf("queued steers %+v", st.QueuedSteers)
	}
	if len(st.Steers) != 1 || st.Steers[0].Continuation != "live" {
		t.Fatalf("steers %+v", st.Steers)
	}
	// What the composer's chips read, off the summary run.updated carries.
	runs, err := svc.Runs(wsID, "OMNI-1")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %+v, %v", runs, err)
	}
	if q := runs[0].QueuedSteers; len(q) != 1 || q[0].Status != "delivered" || q[0].Turn != 3 || q[0].Text != "Re-check the pager" {
		t.Fatalf("summary queued steers %+v", q)
	}
	p.mu.Lock()
	n := len(p.sessions)
	p.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d sessions, want one", n)
	}
}

// TestHeldSteerAppliedWhenTheRunSettles is the provider that takes no
// message mid-run: the steer is held, and the job that hosted the run
// applies it the moment the run completes, as an ordinary steer on the same
// run — the job's outcome is the steered one.
func TestHeldSteerAppliedWhenTheRunSettles(t *testing.T) {
	root := newWorkspace(t)
	running := make(chan string, 1)
	steered := strings.Replace(triageDoc, `"hypothesis":"The export job times out."`, `"hypothesis":"The pager drops the last page."`, 1)
	p := &stubProvider{sendErr: errors.New("one message per session")}
	first := true
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !first {
			s.emit(provider.Event{Kind: provider.EvUsage, Turns: 2})
			s.emit(finalEvent(steered))
			return
		}
		first = false
		s.emit(provider.Event{Kind: provider.EvUsage, Turns: 3})
		running <- filepath.Base(spec.RunDir)
		waitForInbox(spec.RunDir)
		s.emit(finalEvent(triageDoc))
	}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()
	wsID := WorkspaceID(root)
	if _, err := svc.StartTriage(context.Background(), wsID, []string{"OMNI-1"}, TriageOptions{}); err != nil {
		t.Fatal(err)
	}
	runID := <-running
	if id, err := svc.Steer(context.Background(), wsID, runID, "Re-check the pager", ""); err != nil || id != "" {
		t.Fatalf("Steer = %q, %v; want queued", id, err)
	}
	done := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
	if len(done.Outcomes) != 1 || done.Outcomes[0].RunID != runID || done.Outcomes[0].Status != string(store.StatusCompleted) {
		t.Fatalf("outcomes %+v", done.Outcomes)
	}
	_, st, err := store.Open(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.QueuedSteers) != 1 || st.QueuedSteers[0].Status != store.SteerApplied {
		t.Fatalf("queued steers %+v", st.QueuedSteers)
	}
	if len(st.Steers) != 1 || st.Steers[0].Text != "Re-check the pager" || st.Steers[0].Continuation != "resume" {
		t.Fatalf("steers %+v", st.Steers)
	}
	if st.Usage.Turns != 5 {
		t.Fatalf("usage %+v, want the steer segment's turns added", st.Usage)
	}
}
