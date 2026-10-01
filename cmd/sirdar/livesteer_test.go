package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// TestSteerOnALiveRunSaysWhatBecameOfIt is `sirdar steer RUN "…"` on a run
// another process is hosting: the instruction is queued, and the command
// prints what the executor did with it — delivered at a turn, or held until
// the run settles — standing in for that executor with a goroutine that
// resolves the queued line the way it would.
func TestSteerOnALiveRunSaysWhatBecameOfIt(t *testing.T) {
	steerPollEvery = 5 * time.Millisecond
	for _, tc := range []struct {
		name    string
		resolve func(st *store.State, q store.QueuedSteer)
		want    string
	}{
		{"delivered", func(st *store.State, q store.QueuedSteer) {
			q.Status, q.Turn = store.SteerDelivered, 4
			st.QueuedSteers = append(st.QueuedSteers, q)
		}, "queued; delivered at turn 4\n"},
		{"held", func(st *store.State, q store.QueuedSteer) {
			q.Status = store.SteerHeld
			st.QueuedSteers = append(st.QueuedSteers, q)
			st.Status = store.StatusCompleted
		}, "queued; applied when the run settles\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			runID := "20261001T090000Z-aaaa"
			rn, err := store.CreateID(root, "SBX-1", runID)
			if err != nil {
				t.Fatal(err)
			}
			st := store.State{RunID: runID, Key: "SBX-1", Kind: store.KindTriage, Status: store.StatusRunning}
			if err := rn.WriteState(st); err != nil {
				t.Fatal(err)
			}
			go func() {
				for {
					inbox, _ := rn.SteerInbox()
					if len(inbox) > 0 {
						tc.resolve(&st, inbox[0])
						_ = rn.WriteState(st)
						return
					}
					time.Sleep(2 * time.Millisecond)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var out, errb bytes.Buffer
			code, handled := queueLiveSteer(ctx, root, runID, "also check the export worker", "", &out, &errb)
			if !handled || code != 0 || out.String() != tc.want {
				t.Fatalf("handled %v code %d out %q err %q; want %q", handled, code, out.String(), errb.String(), tc.want)
			}
		})
	}
}

// TestSteerOnASettledRunIsNotQueued: a finished run is left to the ordinary
// steer, which needs a provider — queueLiveSteer does not touch it.
func TestSteerOnASettledRunIsNotQueued(t *testing.T) {
	root := t.TempDir()
	runID := "20261001T090000Z-bbbb"
	rn, err := store.CreateID(root, "SBX-1", runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rn.WriteState(store.State{RunID: runID, Key: "SBX-1", Status: store.StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if _, handled := queueLiveSteer(context.Background(), root, runID, "go on", "", &out, &errb); handled {
		t.Fatalf("a settled run was queued on: %q %q", out.String(), errb.String())
	}
	if inbox, _ := rn.SteerInbox(); len(inbox) != 0 {
		t.Fatalf("inbox = %+v", inbox)
	}
}
