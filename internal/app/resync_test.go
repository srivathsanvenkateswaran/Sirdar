package app

import (
	"strings"
	"testing"
	"time"
)

// TestSlowSubscriberIsToldWhatItMissed is finding #9 of the session
// performance report: a subscriber that fell behind lost its oldest events
// and nothing said so, leaving a hole in the transcript the UI's dedupe
// would never fill.
func TestSlowSubscriberIsToldWhatItMissed(t *testing.T) {
	root := newWorkspace(t)
	var stderr strings.Builder
	svc := New(newRegistry(t, root), nil, Options{Buffer: 4, Interval: time.Hour, Stderr: &stderr})

	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	// Nothing reads while these go out, so the channel overflows.
	const lines = 20
	for i := 1; i <= lines; i++ {
		ev := RunEvent{Kind: "assistant_text"}
		svc.publish(Event{
			Kind: KindRunEvent, WorkspaceID: "ws", RunID: "20260917T093000Z-dddd",
			Index: i, Event: &ev,
		})
	}

	resyncs := 0
	from := -1
	lowest := lines + 1
	for {
		select {
		case e := <-events:
			switch e.Kind {
			case KindRunResync:
				resyncs++
				from = e.From
				if e.RunID != "20260917T093000Z-dddd" {
					t.Fatalf("resync names run %q", e.RunID)
				}
			case KindRunEvent:
				if e.Index < lowest {
					lowest = e.Index
				}
			}
			continue
		default:
		}
		break
	}

	if resyncs != 1 {
		t.Fatalf("%d run.resync events, want exactly one", resyncs)
	}
	if from >= lowest {
		t.Fatalf("resync says re-read from %d, but the oldest line still in hand is %d: the hole is not covered", from, lowest)
	}
	if svc.dropped == 0 {
		t.Fatal("nothing was counted as dropped")
	}
	if !strings.Contains(stderr.String(), "fell behind") {
		t.Fatalf("the drop was not logged: %q", stderr.String())
	}
}

// A reader that keeps up is never told to re-read, and the buffer is deep
// enough that a burst the executor can produce does not reach the edge.
func TestFastSubscriberIsNeverToldToReRead(t *testing.T) {
	if DefaultBuffer < 4096 {
		t.Fatalf("DefaultBuffer is %d; a fast provider outruns anything smaller", DefaultBuffer)
	}

	root := newWorkspace(t)
	svc := New(newRegistry(t, root), nil, Options{Interval: time.Hour})
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	for i := 1; i <= 2000; i++ {
		ev := RunEvent{Kind: "assistant_text"}
		svc.publish(Event{Kind: KindRunEvent, WorkspaceID: "ws", RunID: "r", Index: i, Event: &ev})
	}
	for i := 1; i <= 2000; i++ {
		e := <-events
		if e.Kind != KindRunEvent || e.Index != i {
			t.Fatalf("event %d is %s index %d", i, e.Kind, e.Index)
		}
	}
	if svc.dropped != 0 {
		t.Fatalf("%d events dropped with a reader that kept up", svc.dropped)
	}
}
