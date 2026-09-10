package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// stubBuilder is the DepsBuilder the service tests inject: no adapter
// subprocess, no agent binary, everything else as a real run sees it.
func stubBuilder(p provider.Provider, tr source.Tracker, hd source.Helpdesk) DepsBuilder {
	return func(cfg *config.Config, providerName, model string, stderr io.Writer) (runner.Deps, func(), error) {
		return runner.Deps{
			Config:   cfg,
			Provider: p,
			Tracker:  tr,
			Helpdesk: hd,
			Stderr:   io.Discard,
			Env:      []string{"PATH=/usr/bin"},
		}, func() {}, nil
	}
}

// newService returns a started service over one workspace.
func newService(t *testing.T, root string, build DepsBuilder) *Service {
	t.Helper()
	svc := New(newRegistry(t, root), build, Options{Interval: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	t.Cleanup(svc.Stop)
	return svc
}

func TestStartTriageRunsToCompletion(t *testing.T) {
	root := newWorkspace(t)
	p := &stubProvider{script: replay(
		provider.Event{Kind: provider.EvUsage, Turns: 3, InputTok: 100, OutputTok: 20, CostUSD: 0.42},
		finalEvent(triageDoc),
	)}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	wsID := WorkspaceID(root)
	jobID, err := svc.StartTriage(context.Background(), wsID, []string{"OMNI-1"}, TriageOptions{})
	if err != nil {
		t.Fatal(err)
	}

	done := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
	if done.JobID != jobID || done.WorkspaceID != wsID {
		t.Fatalf("job.finished %+v want job %s workspace %s", done, jobID, wsID)
	}
	if len(done.Outcomes) != 1 {
		t.Fatalf("outcomes %+v", done.Outcomes)
	}
	out := done.Outcomes[0]
	if out.Key != "OMNI-1" || out.Status != string(store.StatusCompleted) || out.RunID == "" {
		t.Fatalf("outcome %+v", out)
	}

	runs, err := svc.Runs(wsID, "OMNI-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs %+v", runs)
	}
	run := runs[0]
	if run.Status != string(store.StatusCompleted) || run.RunID != out.RunID {
		t.Fatalf("run %+v", run)
	}
	if run.Provider != "claude" || run.Usage.Turns != 3 || run.Usage.CostUSD != 0.42 {
		t.Fatalf("run %+v", run)
	}
	if run.StartedAt == "" || len(run.Notes) == 0 {
		t.Fatalf("run %+v", run)
	}

	detail, err := svc.Run(wsID, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Handle != "handle-abc" || detail.Budget.MaxTurns != 60 {
		t.Fatalf("detail %+v", detail)
	}
	if !strings.HasSuffix(detail.PromptPath, "prompt.md") || !strings.HasSuffix(detail.BundleDir, "bundle") {
		t.Fatalf("detail paths %+v", detail)
	}

	note, err := svc.Note(wsID, run.RunID, "triage")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "OMNI-1") {
		t.Fatalf("note:\n%s", note)
	}
	if _, err := svc.Prompt(wsID, run.RunID); err != nil {
		t.Fatal(err)
	}

	recorded, next, err := svc.Events(wsID, run.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 2 || next != 2 {
		t.Fatalf("events %d next %d", len(recorded), next)
	}
	if recorded[1].Kind != string(provider.EvFinal) {
		t.Fatalf("last event %+v", recorded[1])
	}
	rest, next2, err := svc.Events(wsID, run.RunID, next)
	if err != nil || len(rest) != 0 || next2 != next {
		t.Fatalf("tail after %d: %d events next %d err %v", next, len(rest), next2, err)
	}

	rows, err := svc.Register(wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Key != "OMNI-1" || rows[0].Kind != "triage" || rows[0].NotePath == "" {
		t.Fatalf("register %+v", rows)
	}

	// The job is gone once it has finished.
	if err := svc.Cancel(jobID); !errors.Is(err, ErrNoSuchJob) {
		t.Fatalf("cancel after finish: %v", err)
	}
}

func TestCancelBlocksTheRun(t *testing.T) {
	root := newWorkspace(t)
	started := make(chan struct{}, 1)
	svc := newService(t, root, stubBuilder(&stubProvider{script: block(started)}, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	wsID := WorkspaceID(root)
	jobID, err := svc.StartTriage(context.Background(), wsID, []string{"OMNI-1"}, TriageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent session never started")
	}

	if err := svc.Cancel(jobID); err != nil {
		t.Fatal(err)
	}

	done := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
	if len(done.Outcomes) != 1 || done.Outcomes[0].Status != string(store.StatusBlocked) {
		t.Fatalf("outcomes %+v", done.Outcomes)
	}
	runs, err := svc.Runs(wsID, "OMNI-1")
	if err != nil || len(runs) != 1 || runs[0].Status != string(store.StatusBlocked) {
		t.Fatalf("runs %+v err %v", runs, err)
	}
	if runs[0].Reason == "" {
		t.Fatalf("a blocked run should say why: %+v", runs[0])
	}
	if err := svc.Cancel(JobID("job-nope")); !errors.Is(err, ErrNoSuchJob) {
		t.Fatalf("cancel of an unknown job: %v", err)
	}
}

func TestQueueDecoratesWithLatestRun(t *testing.T) {
	root := newWorkspace(t)
	writeState(t, root, "OMNI-1", "20260910T090000Z-dddd", store.StatusCompleted)

	tickets := []ticket.TrackerTicket{
		{Key: "OMNI-1", Title: "Export fails", Priority: "high", Status: "open", Assignee: "me",
			URL: "https://t/OMNI-1", HelpdeskRef: "555", UpdatedAt: time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)},
		{Key: "OMNI-2", Title: "Login loops", Priority: "low", Status: "open"},
	}
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{list: tickets}, stubHelpdesk{}))

	rows, err := svc.Queue(context.Background(), WorkspaceID(root), QueueFilter{Assignee: "me", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("tickets %+v", rows)
	}
	if rows[0].Key != "OMNI-1" || rows[0].HelpdeskRef != "555" || rows[0].UpdatedAt == "" {
		t.Fatalf("ticket %+v", rows[0])
	}
	if rows[0].LatestRun == nil || rows[0].LatestRun.RunID != "20260910T090000Z-dddd" ||
		rows[0].LatestRun.Status != string(store.StatusCompleted) {
		t.Fatalf("latest run %+v", rows[0].LatestRun)
	}
	if rows[1].LatestRun != nil {
		t.Fatalf("a ticket with no run carries one: %+v", rows[1].LatestRun)
	}
}

func TestQueueUnsupported(t *testing.T) {
	root := newWorkspace(t)

	// No tracker at all.
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, nil, stubHelpdesk{}))
	if _, err := svc.Queue(context.Background(), WorkspaceID(root), QueueFilter{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("no tracker: %v", err)
	}

	// A tracker whose adapter cannot list.
	refusing := stubTracker{err: &source.Error{Code: source.Unsupported, Message: "list is not implemented"}}
	svc2 := newService(t, root, stubBuilder(&stubProvider{script: replay()}, refusing, stubHelpdesk{}))
	if _, err := svc2.Queue(context.Background(), WorkspaceID(root), QueueFilter{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported adapter: %v", err)
	}

	// An unknown workspace is a different failure.
	if _, err := svc.Queue(context.Background(), "deadbeef1234", QueueFilter{}); !errors.Is(err, ErrNoSuchWorkspace) {
		t.Fatalf("unknown workspace: %v", err)
	}
}

func TestSubscribeDoesNotBlockOnASlowSubscriber(t *testing.T) {
	svc := New(newRegistry(t), nil, Options{Buffer: 4})

	slow, unsubscribeSlow := svc.Subscribe()
	defer unsubscribeSlow()
	fast, unsubscribeFast := svc.Subscribe()

	read := make(chan int)
	go func() {
		n := 0
		for range fast {
			n++
		}
		read <- n
	}()

	const published = 200
	done := make(chan struct{})
	go func() {
		for i := 0; i < published; i++ {
			svc.publish(Event{Kind: KindLog, Text: fmt.Sprint(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a subscriber that never reads")
	}

	// The subscriber that never read kept the newest events and lost the
	// oldest, rather than holding the publisher up.
	if len(slow) != 4 {
		t.Fatalf("slow subscriber holds %d events, want its full buffer of 4", len(slow))
	}
	for want := published - 4; want < published; want++ {
		if got := (<-slow).Text; got != fmt.Sprint(want) {
			t.Fatalf("slow subscriber holds %q, want %d", got, want)
		}
	}

	unsubscribeFast()
	select {
	case n := <-read:
		if n == 0 {
			t.Fatal("the reading subscriber received nothing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unsubscribe did not close the channel")
	}
}

func TestDoctorReportsTheWorkspace(t *testing.T) {
	root := newWorkspace(t)
	svc := New(newRegistry(t, root), nil, Options{})

	checks, err := svc.Doctor(context.Background(), WorkspaceID(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) < 4 {
		t.Fatalf("checks %+v", checks)
	}
	if checks[0].Name != "config" || !checks[0].OK {
		t.Fatalf("first check %+v", checks[0])
	}
	byName := map[string]Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	for _, name := range []string{"sources.tracker", "sources.helpdesk", "notes.dir", "notes.templates"} {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("no %s check in %+v", name, checks)
		}
		if !c.OK {
			t.Fatalf("%s failed: %s", name, c.Detail)
		}
	}
	if _, err := svc.Doctor(context.Background(), "deadbeef1234"); !errors.Is(err, ErrNoSuchWorkspace) {
		t.Fatalf("unknown workspace: %v", err)
	}
}
