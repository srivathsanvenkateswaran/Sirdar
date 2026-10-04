package app

import (
	"context"
	"errors"
	"fmt"

	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// ErrNoteRefused wraps every reason a run cannot file its note again or have its reply saved:
// the wrong kind of run, no reply yet, or a run still working. It is known from the run's state
// before anything starts, so the HTTP layer answers it as a conflict.
var ErrNoteRefused = errors.New("app: note refused")

// ErrBadSession is a session request that cannot start as asked: no instruction, a reference
// that resolves to nothing, an access level nobody offers. The HTTP layer answers it as a bad
// request.
var ErrBadSession = errors.New("app: bad session request")

// UpdateNote runs one note turn on a triage or RCA run that has replied, and files its note
// and register row again. What the run's state, the workspace's caps and its provider name
// refuse is refused here, before a job exists; what only the built provider knows ends the job
// with the reason in the log, as a steer does.
func (s *Service) UpdateNote(ctx context.Context, wsID, runID string) (JobID, error) {
	if err := checkID(ErrNoSuchRun, "run", runID); err != nil {
		return "", err
	}
	_, cfg, err := s.load(wsID)
	if err != nil {
		return "", err
	}
	rn, state, err := store.Open(cfg.Root, runID)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
	}
	if err := runner.RefuseNote(rn, state); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoteRefused, err)
	}
	// The note turn shares the run's caps and its provider session, so a run that hit a
	// cap or was made under another provider is refused here too, where the operator who
	// clicked Update note sees the reason, rather than only in the job's log.
	if err := runner.RefuseSteer(cfg.Budget.MaxTurns, cfg.Budget.MaxMinutes, cfg.Budget.MaxUSD, state); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoteRefused, err)
	}
	if err := runner.RefuseProvider(string(cfg.Provider), state); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoteRefused, err)
	}
	key := state.Key
	return s.start(ctx, wsID, "", "", func(jctx context.Context, deps runner.Deps) []JobOutcome {
		out, err := (&runner.Runner{Deps: deps}).UpdateNote(jctx, runID, runner.UpdateNoteOptions{})
		if err != nil {
			s.log(err)
			if out.State.RunID == "" {
				// Refused inside the job: the run is as it was, and the
				// outcome says so rather than calling it failed.
				return []JobOutcome{{Key: key, Status: string(state.Status), RunID: runID}}
			}
		}
		return outcomesOf([]runner.Outcome{out})
	}, func(err error) []JobOutcome {
		s.log(err)
		return []JobOutcome{{Key: key, Status: string(state.Status), RunID: runID}}
	})
}

// SaveNote writes a session run's reply into the workspace's notes directory and answers with
// the path it wrote. No model is called, so it answers at once.
func (s *Service) SaveNote(wsID, runID string) (string, error) {
	if err := checkID(ErrNoSuchRun, "run", runID); err != nil {
		return "", err
	}
	_, cfg, err := s.load(wsID)
	if err != nil {
		return "", err
	}
	if _, _, err := store.Open(cfg.Root, runID); err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
	}
	path, err := runner.SaveSessionNote(cfg, runID, s.now())
	if errors.Is(err, runner.ErrNoNote) {
		return "", fmt.Errorf("%w: %w", ErrNoteRefused, err)
	}
	return path, err
}
