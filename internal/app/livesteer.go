package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// A steer on a working run is queued, not refused. The executor that owns
// the run delivers it into the live session at the next turn boundary when
// the session takes another user message, and holds it otherwise; a held
// steer is applied here, as an ordinary steer, the moment the run settles.
// See internal/run/livesteer.go for the executor's half.

// live reports whether a run's state says an executor owns it right now.
func live(s store.State) bool {
	return s.Status == store.StatusPreparing || s.Status == store.StatusRunning
}

// QueueLiveSteer queues text on runID when the run is still working, and
// reports whether it did. A run that has settled is not touched, and the
// caller steers it the ordinary way. An eval is refused here as it is on a
// settled run: its note is a measurement.
func QueueLiveSteer(root, runID, text, model string, now time.Time) (store.QueuedSteer, bool, error) {
	rn, st, err := store.Open(root, runID)
	if err != nil {
		return store.QueuedSteer{}, false, err
	}
	if !live(st) {
		return store.QueuedSteer{}, false, nil
	}
	if st.Eval {
		return store.QueuedSteer{}, false, fmt.Errorf("%w: run %s is an eval replay; its note is a measurement and is not steered", ErrSteerRefused, runID)
	}
	q, err := rn.QueueSteer(strings.TrimSpace(text), strings.TrimSpace(model), now)
	return q, err == nil, err
}

// heldLocks keeps two appliers in this process — the job that hosted the
// run and a steer that raced its settling — from applying the same held
// steers twice. The first marks them applied before it starts the session;
// the second then finds nothing to do, or a live run whose executor takes
// whatever is left.
var heldLocks sync.Map // runID -> *sync.Mutex

// ApplyHeldSteers applies every steer that was queued on runID while it
// worked and that no session read, now that the run has settled. They go in
// as one follow-up, through Steer, so a fix run is continued in its worktree
// with its commit amended. It returns the outcome of the last steer it ran,
// and false when there was nothing it could apply.
//
// A run stopped by a person drops them: Stop means stop, and a steer would
// start it again. A run blocked on a question or a limit keeps them held,
// for the session that resumes it. A run nothing can steer — over budget,
// an eval, a pushed fix — drops them with the reason.
func ApplyHeldSteers(ctx context.Context, deps runner.Deps, runID string) (runner.Outcome, bool) {
	if deps.Config == nil {
		return runner.Outcome{}, false
	}
	mu, _ := heldLocks.LoadOrStore(runID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	var last runner.Outcome
	applied := false
	// Each pass applies what was held when it looked; a steer typed during
	// that segment is delivered live or held again, and the next pass
	// takes it. The bound only stops a loop that would never settle.
	for range 8 {
		rn, st, err := store.Open(deps.Config.Root, runID)
		if err != nil {
			break
		}
		held, err := runner.HeldSteers(rn, &st)
		if err != nil || len(held) == 0 || live(st) {
			break
		}
		ids := make(map[string]bool, len(held))
		model := ""
		for _, q := range held {
			ids[q.ID] = true
			if model == "" {
				model = q.Model
			}
		}
		now := time.Now()
		if deps.Now != nil {
			now = deps.Now()
		}
		if st.Status == store.StatusBlocked {
			if st.Reason == "interrupted" {
				runner.ResolveSteers(&st, ids, store.SteerDropped, "the run was stopped", now)
				_ = rn.WriteState(st)
			}
			break
		}
		if err := runner.RefuseSteer(deps.Config.Budget.MaxTurns, deps.Config.Budget.MaxMinutes, deps.Config.Budget.MaxUSD, st); err != nil {
			runner.ResolveSteers(&st, ids, store.SteerDropped, firstLineOf(err.Error()), now)
			_ = rn.WriteState(st)
			break
		}
		if ctx.Err() != nil {
			break
		}
		runner.ResolveSteers(&st, ids, store.SteerApplied, "", now)
		if err := rn.WriteState(st); err != nil {
			break
		}
		if deps.Stderr != nil {
			fmt.Fprintf(deps.Stderr, "[%s] applying %d held steer(s) now the run has settled\n", st.Key, len(held))
		}
		out, err := Steer(ctx, deps, runID, runner.JoinSteers(held), model)
		if err != nil && out.State.RunID == "" {
			// Refused before a session started: the run is as it was,
			// so the steers did not happen and say why.
			if rn, st, oerr := store.Open(deps.Config.Root, runID); oerr == nil {
				runner.ResolveSteers(&st, ids, store.SteerDropped, firstLineOf(err.Error()), now)
				_ = rn.WriteState(st)
			}
			break
		}
		last, applied = out, true
		if err != nil && !errors.Is(err, context.Canceled) && deps.Stderr != nil {
			fmt.Fprintf(deps.Stderr, "[%s] %v\n", st.Key, err)
		}
	}
	return last, applied
}

// firstLineOf is the first line of s, for a reason shown beside a chip.
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// SteerResolution is what became of one queued steer, as `sirdar steer`
// reports it.
type SteerResolution struct {
	Status string
	Turn   int
	Reason string
}

// WaitQueuedSteer waits until the executor has said what it did with the
// queued steer id: delivered it at a turn, or held it for the run to settle.
// A run that settles without accounting for it holds it too, since the
// settled run's applier reads the same inbox. It polls the run's state at
// every interval until ctx ends.
func WaitQueuedSteer(ctx context.Context, root, runID, id string, every time.Duration) (SteerResolution, error) {
	if every <= 0 {
		every = 200 * time.Millisecond
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		_, st, err := store.Open(root, runID)
		if err != nil {
			return SteerResolution{}, err
		}
		for _, q := range st.QueuedSteers {
			if q.ID == id && q.Status != store.SteerQueued {
				return SteerResolution{Status: q.Status, Turn: q.Turn, Reason: q.Reason}, nil
			}
		}
		if !live(st) {
			return SteerResolution{Status: store.SteerHeld}, nil
		}
		select {
		case <-ctx.Done():
			return SteerResolution{Status: store.SteerQueued}, ctx.Err()
		case <-t.C:
		}
	}
}
