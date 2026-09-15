package eval

import (
	"context"
	"fmt"

	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// NewRetroDeps builds what RunRetro needs out of a workspace's runner
// dependencies: the three runs a retro makes, and the judge --rubric asks.
func NewRetroDeps(deps runner.Deps, model string) RetroDeps {
	d := RetroDeps{
		Runner: runAtCommit{deps: deps},
		Judge:  NewRubricer(deps, model),
		Model:  model,
	}
	if deps.Provider != nil {
		d.Provider = deps.Provider.Name()
	}
	if deps.Config != nil {
		d.Root = deps.Config.Root
		if d.Model == "" {
			d.Model = deps.Config.Model
		}
	}
	return d
}

// runAtCommit is the adapter between the retro orchestration and
// internal/run.
//
// TODO retro-b: the three methods are stubs until `--at <commit>` and
// `fix --local` land. Nothing else in this feature has to change when they
// do — the shapes below are what the orchestration already passes and what
// its scores already read:
//
//	TriageAt   -> Runner.Triage(ctx, []string{key}, runner.Options{
//	                  BundleDir: o.BundleDir, Model: o.Model,
//	                  Concurrency: 1, Eval: true, At: o.Commit})
//	              Stage.DocPath  = <run-dir>/result.json
//	              Stage.NotePath = <run-dir>/note.md  (an eval run files none)
//	FixLocalAt -> fix.Run(ctx, deps, key, fix.Options{
//	                  Local: true, At: o.Commit, Model: o.Model,
//	                  TriageNote: o.TriageNote})
//	              FixStage.DiffPath = state.Fix.DiffPath (<run-dir>/fix.diff)
//	              FixStage.Commit   = state.Fix.Commit
//	              FixStage.DocPath  = <run-dir>/result.json, which is where
//	                                  BuildPassed reads testsRun from
//	RCAAt      -> Runner.RCA(ctx, key, runner.RCAOptions{Options: {...,
//	                  At: o.Commit, Eval: true}})  — no PRURL, deliberately:
//	              a retro RCA is blind or it is not a measurement.
//
// A stage that cannot run says so on the row rather than scoring a run that
// stood at the wrong commit, which is the one failure that would look like
// a result.
type runAtCommit struct{ deps runner.Deps }

// notSupported is the stage a build without retro-b's flags returns.
func notSupported(what string) Stage {
	return Stage{
		State:  string(store.StatusFailed),
		Reason: fmt.Sprintf("%s: %v (`--at <commit>` has not landed in this build)", what, ErrNotSupported),
	}
}

func (r runAtCommit) TriageAt(_ context.Context, _ string, _ TriageAt) (Stage, error) {
	s := notSupported("triage at a commit")
	return s, fmt.Errorf("eval: triage at a commit: %w", ErrNotSupported)
}

func (r runAtCommit) FixLocalAt(_ context.Context, _ string, _ FixLocalAt) (FixStage, error) {
	return FixStage{Stage: notSupported("fix --local at a commit")},
		fmt.Errorf("eval: fix --local at a commit: %w", ErrNotSupported)
}

func (r runAtCommit) RCAAt(_ context.Context, _ string, _ RCAAt) (Stage, error) {
	s := notSupported("rca at a commit")
	return s, fmt.Errorf("eval: rca at a commit: %w", ErrNotSupported)
}
