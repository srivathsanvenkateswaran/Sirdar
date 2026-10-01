package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// applyHeld applies the steers typed on each run while it worked that its
// session could not take mid-run. A run this process hosted is the one
// whose settling nobody else is watching for, so it is applied here, before
// the command reports, and the report is of the steered run.
func applyHeld(ctx context.Context, deps runner.Deps, outs []runner.Outcome) []runner.Outcome {
	for i, o := range outs {
		if o.State.RunID == "" {
			continue
		}
		if out, ok := app.ApplyHeldSteers(ctx, deps, o.State.RunID); ok {
			outs[i] = out
		}
	}
	return outs
}

// steerPollEvery is how often `sirdar steer` on a live run reads the run's
// state for what became of its instruction. A variable so a test can make
// it short.
var steerPollEvery = 200 * time.Millisecond

// queueLiveSteer is `sirdar steer` on a run that is still working: the
// instruction goes into the run's queue, and the command waits to say what
// became of it — delivered at a turn, or held until the run settles — but
// never runs a session itself, because the process hosting the run does.
// handled is false when the run is not working and the caller steers it
// the ordinary way.
func queueLiveSteer(ctx context.Context, root, runID, text, model string, stdout, stderr io.Writer) (code int, handled bool) {
	q, queued, err := app.QueueLiveSteer(root, runID, text, model, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1, true
	}
	if !queued {
		return 0, false
	}
	res, err := app.WaitQueuedSteer(ctx, root, runID, q.ID, steerPollEvery)
	if err != nil && res.Status == store.SteerQueued {
		// Interrupted while waiting: the instruction is in the queue all
		// the same, and the run takes it whether or not anyone watches.
		fmt.Fprintln(stdout, "queued")
		return 0, true
	}
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1, true
	}
	switch res.Status {
	case store.SteerDelivered:
		fmt.Fprintf(stdout, "queued; delivered at turn %d\n", res.Turn)
	case store.SteerDropped:
		fmt.Fprintf(stdout, "queued; not delivered: %s\n", res.Reason)
		return 1, true
	default:
		fmt.Fprintln(stdout, "queued; applied when the run settles")
	}
	return 0, true
}
