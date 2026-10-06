package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
