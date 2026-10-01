package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSteerInboxQueuesAndMerges covers the queue a live steer goes into: two
// appends read back oldest first, a torn line is skipped, and merging into a
// state adds only what the state does not already account for — keeping the
// state's own record of a steer that was already delivered.
func TestSteerInboxQueuesAndMerges(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rn, err := Create(root, "SBX-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rn.SteerInbox(); err != nil || got != nil {
		t.Fatalf("empty inbox = %v, %v; want nil, nil", got, err)
	}
	a, err := rn.QueueSteer("check the export worker", "", now)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(filepath.Join(rn.Dir, "steers.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("{\"ID\":\"torn\n")
	f.Close()
	b, err := rn.QueueSteer("and the tax rounding", "opus", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.ID == b.ID || a.Status != SteerQueued {
		t.Fatalf("queued = %+v, %+v", a, b)
	}
	inbox, err := rn.SteerInbox()
	if err != nil || len(inbox) != 2 || inbox[0].Text != a.Text || inbox[1].Model != "opus" {
		t.Fatalf("inbox = %+v, %v", inbox, err)
	}

	st := State{QueuedSteers: []QueuedSteer{{ID: a.ID, Text: a.Text, Status: SteerDelivered, Turn: 4}}}
	added, err := rn.MergeSteerInbox(&st)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].ID != b.ID {
		t.Fatalf("added = %+v, want only the second", added)
	}
	if len(st.QueuedSteers) != 2 || st.QueuedSteers[0].Status != SteerDelivered || st.QueuedSteers[0].Turn != 4 ||
		st.QueuedSteers[1].Status != SteerQueued {
		t.Fatalf("merged = %+v", st.QueuedSteers)
	}
	if again, _ := rn.MergeSteerInbox(&st); len(again) != 0 {
		t.Fatalf("second merge added %+v", again)
	}
}
