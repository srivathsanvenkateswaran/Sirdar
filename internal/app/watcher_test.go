package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// waitFor reads events until match returns true, or the deadline passes.
func waitFor(t *testing.T, events <-chan Event, what string, match func(Event) bool) Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-events:
			if match(e) {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// writeState writes a run's state.json the way the core does.
func writeState(t *testing.T, root, key, runID string, status store.Status) {
	t.Helper()
	dir := filepath.Join(root, ".sirdar", "runs", key, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := store.State{
		RunID: runID, Key: key, Kind: store.KindTriage, Status: status,
		Provider: "claude", Model: "claude-haiku-4-5",
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := (store.Run{Dir: dir}).WriteState(state); err != nil {
		t.Fatal(err)
	}
}

// appendEvent appends one line to a run's events.jsonl, in the shape
// internal/run's record() writes.
func appendEvent(t *testing.T, root, key, runID, kind, tool string) {
	t.Helper()
	path := filepath.Join(root, ".sirdar", "runs", key, runID, "events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line := fmt.Sprintf(`{"t":%q,"kind":%q,"payload":{"tool":%q}}`+"\n",
		time.Now().UTC().Format(time.RFC3339Nano), kind, tool)
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherSeesNewRunAndTailsEvents(t *testing.T) {
	root := newWorkspace(t)
	reg := newRegistry(t, root)

	events := make(chan Event, 64)
	w := NewWatcher(reg, 20*time.Millisecond, func(e Event) { events <- e })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	defer w.Stop()

	const key, runID = "OMNI-1", "20260910T090000Z-aaaa"
	writeState(t, root, key, runID, store.StatusRunning)

	e := waitFor(t, events, "run.updated for a new run", func(e Event) bool {
		return e.Kind == KindRunUpdated && e.Run != nil && e.Run.RunID == runID
	})
	if e.Run.Status != string(store.StatusRunning) || e.Run.Key != key {
		t.Fatalf("run %+v", e.Run)
	}
	if e.WorkspaceID != WorkspaceID(root) {
		t.Fatalf("workspaceId %q", e.WorkspaceID)
	}

	appendEvent(t, root, key, runID, "tool_started", "Bash")
	appendEvent(t, root, key, runID, "tool_finished", "Bash")

	first := waitFor(t, events, "the first tailed event", func(e Event) bool { return e.Kind == KindRunEvent })
	if first.Index != 1 || first.Event.Kind != "tool_started" || first.Event.Payload.Tool != "Bash" {
		t.Fatalf("first event %+v payload %+v", first, first.Event)
	}
	if first.RunID != runID {
		t.Fatalf("runId %q", first.RunID)
	}
	second := waitFor(t, events, "the second tailed event", func(e Event) bool { return e.Kind == KindRunEvent })
	if second.Index != 2 || second.Event.Kind != "tool_finished" {
		t.Fatalf("second event %+v", second)
	}

	// A state change on the same run is reported again, and the events
	// written as the run ends are still flushed after it leaves running.
	writeState(t, root, key, runID, store.StatusCompleted)
	appendEvent(t, root, key, runID, "final", "")

	done := waitFor(t, events, "run.updated for the completed run", func(e Event) bool {
		return e.Kind == KindRunUpdated && e.Run != nil && e.Run.Status == string(store.StatusCompleted)
	})
	if done.Run.RunID != runID {
		t.Fatalf("run %+v", done.Run)
	}
	third := waitFor(t, events, "the flushed final event", func(e Event) bool { return e.Kind == KindRunEvent })
	if third.Index != 3 || third.Event.Kind != "final" {
		t.Fatalf("third event %+v", third)
	}
}

func TestWatcherPicksUpAWorkspaceAddedLater(t *testing.T) {
	reg := newRegistry(t)
	events := make(chan Event, 32)
	w := NewWatcher(reg, 20*time.Millisecond, func(e Event) { events <- e })
	w.Start(context.Background())
	defer w.Stop()

	root := newWorkspace(t)
	if _, err := reg.Add(root); err != nil {
		t.Fatal(err)
	}
	writeState(t, root, "OMNI-2", "20260910T090500Z-bbbb", store.StatusPreparing)

	e := waitFor(t, events, "run.updated from the new workspace", func(e Event) bool {
		return e.Kind == KindRunUpdated
	})
	if e.Run.Key != "OMNI-2" || e.Run.Status != string(store.StatusPreparing) {
		t.Fatalf("run %+v", e.Run)
	}
}
