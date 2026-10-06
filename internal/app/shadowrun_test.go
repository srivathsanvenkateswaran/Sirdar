package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/shadow"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// writeTriageState writes a triage run's state with an explicit start time,
// so a test can say which of two runs is the newer.
func writeTriageState(t *testing.T, root, key, runID string, status store.Status, startedAt time.Time, shadow bool) {
	t.Helper()
	dir := filepath.Join(root, ".sirdar", "runs", key, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := store.State{
		RunID: runID, Key: key, Kind: store.KindTriage, Status: status,
		Provider: "claude", Model: "claude-haiku-4-5",
		StartedAt: startedAt, UpdatedAt: startedAt, Shadow: shadow,
	}
	if err := (store.Run{Dir: dir}).WriteState(state); err != nil {
		t.Fatal(err)
	}
}

// TestBusyReasonIgnoresShadow: a shadow run is the watcher measuring the
// agent, so neither a running one nor a recent one keeps a webhook from
// triaging the key.
func TestBusyReasonIgnoresShadow(t *testing.T) {
	running := []store.State{{Key: "OMNI-1", Shadow: true, Status: store.StatusRunning, Kind: store.KindTriage}}
	if reason := busyReason(running, 10*time.Minute, at(2)); reason != "" {
		t.Errorf("a running shadow run held the key: %s", reason)
	}
	recent := []store.State{{
		Key: "OMNI-1", Shadow: true, Status: store.StatusCompleted, Kind: store.KindTriage,
		StartedAt: at(1), UpdatedAt: at(1),
	}}
	if reason := busyReason(recent, 10*time.Minute, at(2)); reason != "" {
		t.Errorf("a recent shadow run started the cooldown: %s", reason)
	}
}

func TestQueueLatestRunSkipsShadow(t *testing.T) {
	root := newWorkspace(t)
	t0 := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	writeTriageState(t, root, "OMNI-1", "20260910T090000Z-aaaa", store.StatusCompleted, t0, false)
	writeTriageState(t, root, "OMNI-1", "20260910T100000Z-bbbb", store.StatusCompleted, t0.Add(time.Hour), true)

	tickets := []ticket.TrackerTicket{{Key: "OMNI-1", Title: "Export fails", Type: "bug", Status: "open"}}
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{list: tickets}, stubHelpdesk{}))

	rows, err := svc.Queue(context.Background(), WorkspaceID(root), QueueFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].LatestRun == nil {
		t.Fatalf("rows %+v", rows)
	}
	if got := rows[0].LatestRun.RunID; got != "20260910T090000Z-aaaa" {
		t.Errorf("latest run %q, want the older attended run", got)
	}
}

func TestRunsHidesShadow(t *testing.T) {
	root := newWorkspace(t)
	t0 := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	writeTriageState(t, root, "OMNI-1", "20260910T090000Z-aaaa", store.StatusCompleted, t0, false)
	writeTriageState(t, root, "OMNI-1", "20260910T100000Z-bbbb", store.StatusCompleted, t0.Add(time.Hour), true)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{}, stubHelpdesk{}))
	wsID := WorkspaceID(root)

	runs, err := svc.Runs(wsID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "20260910T090000Z-aaaa" {
		t.Errorf("Runs = %+v, want the attended run alone", runs)
	}

	all, err := svc.runs(wsID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range all {
		if r.RunID == "20260910T100000Z-bbbb" {
			found = true
			if !r.Shadow {
				t.Errorf("the shadow run's summary has Shadow false: %+v", r)
			}
		}
	}
	if len(all) != 2 || !found {
		t.Errorf("runs(includeShadow) = %+v, want both runs", all)
	}
}

func TestQueueLiveSteerRefusesShadow(t *testing.T) {
	root := newWorkspace(t)
	const runID = "20260910T090000Z-aaaa"
	writeTriageState(t, root, "OMNI-1", runID, store.StatusRunning, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC), true)

	_, _, err := QueueLiveSteer(root, runID, "look at the ledger", "", time.Now())
	if !errors.Is(err, ErrSteerRefused) || !strings.Contains(err.Error(), "shadow run") {
		t.Fatalf("QueueLiveSteer = %v, want ErrSteerRefused naming a shadow run", err)
	}
}

// TestShadowQuotaPauseWithoutStart: `sirdar shadow run` and `shadow watch
// --once` never call Start, so the quota recorded in the event logs has to
// reach the gate some other way, or a backfill at 88% of the seven-day
// window would still start.
func TestShadowQuotaPauseWithoutStart(t *testing.T) {
	for _, path := range []string{"run", "poll"} {
		t.Run(path, func(t *testing.T) {
			root := newWorkspace(t)
			writeConfig(t, root, shadowConfig(5, ""))
			raw := strings.Replace(claudeRateLimitRaw, `"seven_day":{"utilization":0.4`, `"seven_day":{"utilization":0.88`, 1)
			writeTriageState(t, root, "OMNI-9", "20261006T080000Z-op01", store.StatusCompleted, time.Now().Add(-2*time.Hour), false)
			writeEventLog(t, root, "OMNI-9", "20261006T080000Z-op01",
				fmt.Sprintf(`{"t":%q,"kind":"rate_limited","payload":{"raw":%s}}`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), raw))
			tr := &shadowTracker{}
			tr.set(ticket.TrackerTicket{Key: "OMNI-1", Title: "Export fails", Type: "bug", Status: "open", CreatedAt: time.Now()})
			svc := New(newRegistry(t, root), stubBuilder(&stubProvider{script: completes()}, tr, stubHelpdesk{}), Options{})
			wsID := WorkspaceID(root)

			switch path {
			case "run":
				started, err := svc.shadowRun(context.Background(), wsID, []string{"OMNI-1"}, false)
				if err != nil {
					t.Fatal(err)
				}
				if len(started) != 1 || !strings.HasPrefix(started[0].Skipped, "seven-day quota at 88%") {
					t.Fatalf("started %+v, want OMNI-1 skipped on the seven-day quota", started)
				}
			case "poll":
				st, err := svc.shadowPoll(context.Background(), wsID)
				if err != nil {
					t.Fatal(err)
				}
				if st.State != "paused" || !strings.HasPrefix(st.Reason, "seven-day quota at 88%") {
					t.Fatalf("status %s %q, want paused on the seven-day quota", st.State, st.Reason)
				}
			}
			entries, err := shadow.Read(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Ev == "started" {
					t.Fatalf("a run started over the quota: %+v", e)
				}
			}
		})
	}
}

// TestResumeRefusesShadow: the watcher resumes its own runs through the
// runner; an operator's resume through the service would spend around the
// shadow caps, leave the ledger's finished line stale, and could race the
// watcher's retry of the same run.
func TestResumeRefusesShadow(t *testing.T) {
	root := newWorkspace(t)
	const runID = "20261006T090000Z-ab12"
	writeTriageState(t, root, "OMNI-1", runID, store.StatusBlocked, time.Now(), true)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{}, stubHelpdesk{}))
	_, err := svc.Resume(context.Background(), WorkspaceID(root), runID, "go on", "", nil)
	if !errors.Is(err, ErrSteerRefused) {
		t.Fatalf("err = %v, want ErrSteerRefused", err)
	}
	if want := "is a shadow run; it is a measurement and is not resumed"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want %q", err, want)
	}
}

// TestShadowInFlightSpendFromTheLedger: serve's run in flight is in the
// ledger as a started line with no finished one, and its spend so far is in
// its state file. A `shadow run` or `shadow status` in another process has
// no record of it in memory, and must still count it.
func TestShadowInFlightSpendFromTheLedger(t *testing.T) {
	root := newWorkspace(t)
	writeConfig(t, root, shadowConfig(5, "  maxUSDPerDay: 0.5\n"))
	const runID = "20261006T090000Z-sv01"
	now := time.Now()
	dir := filepath.Join(root, ".sirdar", "runs", "OMNI-2", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := store.State{RunID: runID, Key: "OMNI-2", Kind: store.KindTriage, Status: store.StatusRunning,
		Provider: "claude", StartedAt: now, UpdatedAt: now, Shadow: true, Usage: store.Usage{CostUSD: 0.8}}
	if err := (store.Run{Dir: dir}).WriteState(st); err != nil {
		t.Fatal(err)
	}
	for _, e := range []shadow.Entry{
		{At: now.Add(-time.Minute), Ev: "seen", Key: "OMNI-2"},
		{At: now, Ev: "started", Key: "OMNI-2", RunID: runID, Attempt: 1, PID: os.Getpid(), Host: hostName()},
	} {
		if err := shadow.Append(root, e); err != nil {
			t.Fatal(err)
		}
	}
	tr := &shadowTracker{}
	svc := New(newRegistry(t, root), stubBuilder(&stubProvider{script: completes()}, tr, stubHelpdesk{}), Options{})
	wsID := WorkspaceID(root)

	status, err := svc.shadowStatus(wsID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Today.USD != 0.8 {
		t.Errorf("today.usd = %v, want the 0.8 serve's run has spent", status.Today.USD)
	}
	started, err := svc.shadowRun(context.Background(), wsID, []string{"OMNI-1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || !strings.HasPrefix(started[0].Skipped, "daily USD cap reached") {
		t.Fatalf("started %+v, want OMNI-1 skipped on the daily USD cap", started)
	}
}

// TestShadowRunSkipsAKeyInFlight: a second attempt started beside one still
// going would be a concurrent run of the same ticket that the daily run cap
// never counts.
func TestShadowRunSkipsAKeyInFlight(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	now := f.clock.Now()
	f.append(t, shadow.Entry{At: now.Add(-time.Minute), Ev: "seen", Key: "OMNI-1"})
	f.append(t, shadow.Entry{At: now, Ev: "started", Key: "OMNI-1", RunID: "20261006T043000Z-sv01", Attempt: 1, PID: os.Getpid(), Host: hostName()})
	started, err := f.svc.shadowRun(context.Background(), f.wsID, []string{"OMNI-1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := "a shadow run of OMNI-1 is still going (20261006T043000Z-sv01)"; len(started) != 1 || started[0].Skipped != want || started[0].RunID != "" {
		t.Fatalf("started %+v, want skipped %q", started, want)
	}
	if n := len(ofEv(f.ledger(t), "started")); n != 1 {
		t.Errorf("%d started lines, want only the one in flight", n)
	}
}
