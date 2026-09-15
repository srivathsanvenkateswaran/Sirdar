package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/fix"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// ErrSteerRefused wraps every run-level reason a steer cannot start: the
// run is live, over a budget, an eval, or the instruction is empty. It is
// known before a job exists, so the HTTP layer answers it as a conflict
// rather than starting a job that would only fail.
var ErrSteerRefused = errors.New("app: steer refused")

// Steer continues a finished run with a follow-up instruction, whichever
// kind of run it is. A fix run goes through internal/fix, which stands the
// session in the run's worktree, guards the reserved files and amends the
// commit; every other kind goes straight to the runner. The CLI and the
// service both come through here, so the two cannot drift.
func Steer(ctx context.Context, deps runner.Deps, runID, text string) (runner.Outcome, error) {
	if deps.Config == nil {
		return runner.Outcome{}, fmt.Errorf("app: no workspace configuration")
	}
	_, state, err := store.Open(deps.Config.Root, runID)
	if err != nil {
		return runner.Outcome{}, err
	}
	if state.Kind != store.KindFix {
		r := &runner.Runner{Deps: deps}
		return r.Steer(ctx, runID, text, runner.SteerOptions{})
	}
	res, err := fix.Steer(ctx, deps, runID, text)
	out := runner.Outcome{Key: res.Key, State: res.State, Digest: res.Digest}
	if err != nil {
		return out, err
	}
	if res.Blocked != "" && deps.Stderr != nil {
		fmt.Fprintf(deps.Stderr, "[%s] the agent reports deviating from the note: %s\n", res.Key, res.Blocked)
	}
	return out, nil
}

// Steer is the service half: it refuses what can be refused from the run's
// state alone, synchronously, and starts a job for the rest. The provider's
// own refusal — cursor, agy — is only known once the job has built its
// dependencies, and ends the job failed with the reason on the activity
// pane, the way a refused fix does.
func (s *Service) Steer(ctx context.Context, wsID, runID, text string) (JobID, error) {
	if err := checkID(ErrNoSuchRun, "run", runID); err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("%w: the instruction is empty", ErrSteerRefused)
	}
	_, cfg, err := s.load(wsID)
	if err != nil {
		return "", err
	}
	_, state, err := store.Open(cfg.Root, runID)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
	}
	if err := runner.RefuseSteer(cfg.Budget.MaxTurns, cfg.Budget.MaxMinutes, cfg.Budget.MaxUSD, state); err != nil {
		return "", fmt.Errorf("%w: %w", ErrSteerRefused, err)
	}
	key := state.Key
	return s.start(ctx, wsID, "", "", func(jctx context.Context, deps runner.Deps) []JobOutcome {
		out, err := Steer(jctx, deps, runID, text)
		if err != nil {
			s.log(err)
			if out.State.RunID == "" {
				return []JobOutcome{{Key: key, Status: string(store.StatusFailed), RunID: runID}}
			}
		}
		return outcomesOf([]runner.Outcome{out})
	}, func(err error) []JobOutcome {
		s.log(err)
		return []JobOutcome{{Key: key, Status: string(store.StatusFailed), RunID: runID}}
	})
}
