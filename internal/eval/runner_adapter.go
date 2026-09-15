package eval

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/srivathsanvenkateswaran/sirdar/internal/fix"
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

// runAtCommit is the adapter between the retro orchestration and the two
// packages that do the work: internal/run for the triage and the rca,
// internal/fix for the fix.
//
// Every session it starts stands at the golden entry's base commit, in a
// linked worktree of the run's own, and every one is marked as an eval.
// That mark is the difference between a retro and three ordinary runs: no
// note is filed into the notes directory, no register row is appended, and
// nothing a later triage, rca or fix reads as "the newest note for this
// key" is touched. It is also why the fix and rca stages have to be told
// which note they are working from — an eval's triage note is deliberately
// invisible to store.LatestNote, so nothing else would find it.
//
// Nothing here leaves the machine. The fix runs with Local set, which stops
// it at the commit in its own worktree, and the rca is given no pull
// request URL: a retro rca is blind or it is not a measurement.
type runAtCommit struct{ deps runner.Deps }

// root is the workspace every stage writes its run directory into.
func (r runAtCommit) root() string {
	if r.deps.Config == nil {
		return ""
	}
	return r.deps.Config.Root
}

// stage maps a finished run onto the row a score reads. The two paths are
// named whether or not the files are there: a stage that produced neither
// is an unscored column, which readFile already treats as one.
func (r runAtCommit) stage(key string, state store.State) Stage {
	s := Stage{
		RunID:   state.RunID,
		State:   string(state.Status),
		Reason:  state.Reason,
		Turns:   state.Usage.Turns,
		CostUSD: state.Usage.CostUSD,
	}
	if !state.StartedAt.IsZero() && state.UpdatedAt.After(state.StartedAt) {
		s.Minutes = state.UpdatedAt.Sub(state.StartedAt).Minutes()
	}
	if state.RunID != "" && r.root() != "" {
		dir := filepath.Join(r.root(), ".sirdar", "runs", key, state.RunID)
		s.DocPath = filepath.Join(dir, "result.json")
		s.NotePath = filepath.Join(dir, "note.md")
	}
	return s
}

// failedStage is the row for a stage that never reached a run state, so
// there is nothing on disk to name and the reason is all there is.
func failedStage(what string, err error) Stage {
	return Stage{
		State:  string(store.StatusFailed),
		Reason: fmt.Sprintf("%s: %v", what, err),
	}
}

// TriageAt replays the golden bundle with the repository standing at the
// commit the fix branched from. It is an ordinary eval replay in every
// respect but that: the same prompt, the same policy, the same read-only
// tool set, and a note that stays in the run directory.
func (r runAtCommit) TriageAt(ctx context.Context, key string, o TriageAt) (Stage, error) {
	rn := &runner.Runner{Deps: r.deps}
	outs, err := rn.Triage(ctx, []string{key}, runner.Options{
		Model:       o.Model,
		Concurrency: 1,
		BundleDir:   o.BundleDir,
		Eval:        true,
		At:          o.Commit,
	})
	what := "triage at " + short(o.Commit)
	if err == nil && len(outs) == 0 {
		err = fmt.Errorf("the run produced no outcome")
	}
	if err != nil {
		return failedStage(what, err), fmt.Errorf("eval: %s: %w", what, err)
	}
	return r.stage(key, outs[0].State), nil
}

// FixLocalAt runs the fix flow against the triage note the retro's own
// triage stage just produced, at the same commit, and stops at the commit
// it makes: nothing is pushed, no pull request is opened, and the worktree
// holding that commit is kept so the diff beside it can be read.
func (r runAtCommit) FixLocalAt(ctx context.Context, key string, o FixLocalAt) (FixStage, error) {
	res, err := fix.Run(ctx, r.deps, key, fix.Options{
		Model:       o.Model,
		At:          o.Commit,
		Local:       true,
		Eval:        true,
		TriageNote:  o.TriageNote,
		TriageRunID: o.TriageRunID,
	})
	what := "fix --local at " + short(o.Commit)

	var st FixStage
	if res.State.RunID == "" {
		st.Stage = failedStage(what, errOrRefusal(err))
		st.RunID = res.RunID
	} else {
		st.Stage = r.stage(key, res.State)
	}
	st.DiffPath = res.DiffPath
	st.Commit = res.Commit

	// A run blocked on a deviation still made the commit and wrote the
	// diff, which is what the score reads. The block belongs on the row as
	// a reason, not as a missing answer.
	if res.Blocked != "" && st.Reason == "" {
		st.Reason = "the agent reported a deviation from the note: " + res.Blocked
	}
	if err != nil {
		if st.Reason == "" {
			st.Reason = err.Error()
		}
		st.State = string(store.StatusFailed)
		return st, fmt.Errorf("eval: %s: %w", what, err)
	}
	return st, nil
}

// RCAAt runs a blind rca at the same commit, reviewing the retro's own
// triage note. It is given no pull request URL and no resolution text: the
// point of the stage is what the agent concludes without being shown the
// answer.
func (r runAtCommit) RCAAt(ctx context.Context, key string, o RCAAt) (Stage, error) {
	rn := &runner.Runner{Deps: r.deps}
	out, err := rn.RCA(ctx, key, runner.RCAOptions{
		Options: runner.Options{
			Model:     o.Model,
			BundleDir: o.BundleDir,
			Eval:      true,
			At:        o.Commit,
		},
		TriageNote: o.TriageNote,
	})
	if err != nil {
		what := "rca at " + short(o.Commit)
		st := r.stage(key, out.State)
		if st.State == "" {
			st = failedStage(what, err)
		}
		return st, fmt.Errorf("eval: %s: %w", what, err)
	}
	return r.stage(key, out.State), nil
}

// errOrRefusal is the reason for a stage that produced no run at all. A nil
// error there means the flow declined to start and said nothing about why,
// which should not happen and is worth printing plainly rather than leaving
// an empty cell.
func errOrRefusal(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("the fix flow started no run and gave no reason")
}

// short abbreviates a commit for a message: a full sha in a reason line is
// forty characters of noise.
func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
