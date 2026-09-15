package app

import (
	"context"
	"fmt"

	"github.com/srivathsanvenkateswaran/sirdar/internal/fix"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
)

// StartFix runs the confined fix flow for one key in the background and
// returns the job id that can cancel it, the same way every other start
// does. The run itself surfaces through the watcher: internal/fix writes
// the branch, the commit, any deviation and the pull request URL into the
// run's own state.json, which is what the run detail screen reads.
//
// Nothing here reimplements the CLI's semantics — this calls the same
// fix.Run `sirdar fix` calls, with the same options — so the human gate
// (the triage note's status), the snapshot guard and the deviation stop
// hold identically from a UI.
func (s *Service) StartFix(ctx context.Context, wsID, key string, o FixOptions) (JobID, error) {
	if err := checkID(ErrNoSuchRun, "key", key); err != nil {
		return "", err
	}
	return s.start(ctx, wsID, o.Provider, o.Model, func(jctx context.Context, deps runner.Deps) []JobOutcome {
		res, err := fix.Run(jctx, deps, key, fix.Options{
			Model:           o.Model,
			Base:            o.Base,
			At:              o.At,
			DryRun:          o.DryRun,
			Local:           o.Local,
			NoPR:            o.NoPR,
			AcceptDeviation: o.AcceptDeviation,
		})
		if err != nil {
			// A fix refused before the run directory exists — a dirty
			// tree, a note nobody approved — has no run to report, so the
			// reason goes to the activity pane and the key is reported
			// failed. A refusal after the session started already has a
			// state.json saying so.
			s.log(err)
			if res.State.RunID == "" {
				return s.failed([]string{key}, nil)
			}
		}
		if res.Blocked != "" {
			// A commit on a local branch is easy to lose track of, so the
			// activity pane says where it is as well as why it stopped.
			s.log(fmt.Errorf("fix %s: the commit is on %s and was not pushed: the agent reported deviating from the note: %s",
				key, res.Branch, res.Blocked))
		}
		return outcomesOf([]runner.Outcome{{Key: key, State: res.State}})
	}, func(err error) []JobOutcome { return s.failed([]string{key}, err) })
}
