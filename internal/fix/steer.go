package fix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// SteerResult is what a steer on a fix run did: the branch is the same,
// the worktree is the same, and the commit on it is either the one the
// run had or that commit amended with what the steered session changed.
type SteerResult struct {
	Key    string
	Branch string
	RunID  string
	State  store.State
	Report Report

	// Commit is the head of the fix branch after the steer. Amended says
	// it is a new sha: the session changed the tree and the run's commit
	// was rewritten to carry it. An unchanged tree leaves the commit and
	// Amended false.
	Commit  string
	Amended bool

	// DiffPath is fix.diff, rewritten when the run is local and the
	// commit was amended.
	DiffPath string
	Worktree string

	// Blocked is the deviation the new report declares, when it declares
	// one. A steer never pushes, so this only says what a later
	// `sirdar fix KEY --accept-deviation` will be accepting.
	Blocked string
}

// Steer continues a fix run with a follow-up instruction, in the same
// worktree and on the same branch the first session used.
//
// The run has to still have somewhere to stand: a `--local` run keeps its
// worktree and so does one blocked on a deviation, while a pushed run had
// its worktree removed and its branch is on the remote — that one is
// refused, and the answer is a new fix. The snapshot guard is taken
// before the session and checked after it exactly as Run does it, and the
// same rule applies: a difference fails the run and commits nothing.
//
// When the session changed the tree, the run's commit is amended rather
// than followed by a second one. The branch is unpushed by construction,
// so rewriting it costs nothing, and one reviewed commit per fix is what
// `--accept-deviation` compares the branch against. Nothing is pushed.
func Steer(ctx context.Context, deps runner.Deps, runID, text string) (SteerResult, error) {
	cfg := deps.Config
	if cfg == nil {
		return SteerResult{}, fmt.Errorf("fix: no workspace configuration")
	}
	stderr := deps.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	_, state, err := store.Open(cfg.Root, runID)
	if err != nil {
		return SteerResult{}, err
	}
	res := SteerResult{Key: state.Key, Branch: state.Fix.Branch, RunID: runID, State: state,
		Commit: state.Fix.Commit, DiffPath: state.Fix.DiffPath}
	if state.Kind != store.KindFix {
		return res, fmt.Errorf("fix: %s is a %s run, not a fix", runID, state.Kind)
	}
	// The run-level refusals first, so a live or spent run is refused in
	// the same words whichever kind it is, and before git is asked
	// anything.
	if err := runner.RefuseSteer(cfg.Budget.MaxTurns, cfg.Budget.MaxMinutes, cfg.Budget.MaxUSD, state); err != nil {
		return res, err
	}
	if err := provider.RefuseFix(deps.Provider); err != nil {
		return res, err
	}
	if state.Fix.Pushed {
		return res, fmt.Errorf("fix: %s pushed %s already; its worktree is gone and the branch is on the remote, so a follow-up is a new `sirdar fix`, or a commit of your own on that branch",
			runID, state.Fix.Branch)
	}
	if state.Fix.Branch == "" {
		return res, fmt.Errorf("fix: %s recorded no branch, so there is nowhere to continue it", runID)
	}

	// Where the session stands: the run's own worktree when it had one,
	// else the operator's tree, which then has to still be on the fix
	// branch. Either way the branch head has to be the commit the run
	// recorded — a branch somebody moved is not what the instruction is
	// about.
	main := git{dir: cfg.Root}
	work := main
	root := ""
	if state.Fix.Worktree != "" {
		wt := safeWorktree(cfg.Root, state.Fix.Worktree, stderr, state.Key)
		if wt == "" || !isWorktree(wt) {
			return res, fmt.Errorf("fix: the worktree %s ran in, %s, is gone; a follow-up is a new `sirdar fix`",
				runID, relToRoot(cfg.Root, state.Fix.Worktree))
		}
		work, root = git{dir: wt}, wt
		res.Worktree = wt
	} else if cur := main.currentBranch(ctx); cur != state.Fix.Branch {
		return res, fmt.Errorf("fix: %s ran in place on %s, and the working tree is now on %q; check %s out to continue it",
			runID, state.Fix.Branch, cur, state.Fix.Branch)
	}
	head, err := work.head(ctx)
	if err != nil {
		return res, err
	}
	if state.Fix.Commit != "" && head != state.Fix.Commit {
		return res, fmt.Errorf("fix: %s is now at %s, not at %s, the commit run %s made; a follow-up would rewrite a commit the run does not own",
			state.Fix.Branch, short(head), short(state.Fix.Commit), runID)
	}

	// The note the commit message is written from. Its status is not
	// checked again: the fix it approved has already been made, and this
	// is a refinement of that commit, not a second fix.
	tn, err := loadTriage(cfg.Root, state.Key, Options{})
	if err != nil {
		return res, err
	}

	before, err := takeSnapshot(ctx, cfg.Root, work.dir)
	if err != nil {
		return res, fmt.Errorf("fix: the reserved files could not be read before the session, so the run cannot be checked afterwards: %w", err)
	}

	r := &runner.Runner{Deps: deps}
	out, err := r.Steer(ctx, runID, text, runner.SteerOptions{Root: root})
	if err != nil {
		return res, err
	}
	res.State = out.State

	after, snapErr := takeSnapshot(ctx, cfg.Root, work.dir)
	if snapErr != nil {
		markRunFailed(cfg.Root, runID, "the reserved files could not be re-read after the session", stderr, state.Key)
		return res, fmt.Errorf("fix: the reserved files could not be re-read after the session, so the run cannot be trusted; nothing was committed: %w", snapErr)
	}
	if changed := diffSnapshots(before, after); len(changed) > 0 {
		markRunFailed(cfg.Root, runID, "the session changed reserved files", stderr, state.Key)
		res.State.Status, res.State.Reason = store.StatusFailed, "the session changed reserved files"
		return res, tamperError(changed)
	}
	if out.State.Status != store.StatusCompleted {
		return res, fmt.Errorf("fix: the session ended %s: %s", out.State.Status, out.State.Reason)
	}

	doc, err := runner.FixReport(cfg.Root, state.Key, runID)
	if err != nil {
		return res, err
	}
	if err := note.Validate(note.Fix, doc); err != nil {
		return res, fmt.Errorf("fix: %w", err)
	}
	if err := json.Unmarshal(doc, &res.Report); err != nil {
		return res, fmt.Errorf("fix: parse the agent's report: %w", err)
	}
	if strings.TrimSpace(res.Report.Summary) == "" {
		return res, fmt.Errorf("fix: the agent's report has an empty summary")
	}

	commit, amended, err := amendChanges(ctx, work, tn, res.Report, state.Fix.Commit != "")
	if err != nil {
		return res, err
	}
	res.Commit, res.Amended = commit, amended
	if amended {
		fmt.Fprintf(stderr, "[%s] commit %s on %s (amended)\n", state.Key, short(commit), state.Fix.Branch)
		recordCommit(cfg.Root, runID, state.Fix.Branch, state.Fix.Base, commit, stderr, state.Key)
		res.State.Fix.Commit = commit
		if state.Fix.Local {
			res.DiffPath = writeDiff(ctx, work, cfg.Root, runID, commit, stderr, state.Key)
			recordFixState(cfg.Root, runID, stderr, state.Key, func(s *store.State) { s.Fix.DiffPath = res.DiffPath })
			res.State.Fix.DiffPath = res.DiffPath
		}
		if !state.Eval {
			appendRegister(cfg.Root, state.Key, res.State, tn)
		}
	} else {
		fmt.Fprintf(stderr, "[%s] the tree is unchanged; %s stays at %s\n", state.Key, state.Fix.Branch, short(commit))
	}

	// The deviation gate is judged again from the new report, and what it
	// says replaces what the first report said: a steer that brought the
	// change back in line with the note clears the block, and one that
	// took it further away sets it.
	dev := strings.TrimSpace(res.Report.DeviationFromNote)
	recordFixState(cfg.Root, runID, stderr, state.Key, func(s *store.State) { s.Fix.Deviation = dev })
	res.State.Fix.Deviation = dev
	res.Blocked = dev
	return res, nil
}

// amendChanges stages everything but .sirdar/ and, when that is anything at
// all, rewrites the run's commit to carry it — or makes the first commit,
// for a run that never got as far as one. It reports the branch head and
// whether it moved.
func amendChanges(ctx context.Context, g git, tn triageNote, rep Report, hasCommit bool) (string, bool, error) {
	if err := g.run(ctx, "add", "-A", "--", ".", ":(exclude).sirdar"); err != nil {
		return "", false, err
	}
	if err := g.run(ctx, "diff", "--cached", "--quiet"); err == nil {
		head, err := g.head(ctx)
		return head, false, err
	}
	subject, body := CommitMessage(tn, rep)
	args := []string{"commit", "--no-verify", "-m", subject, "-m", body}
	if hasCommit {
		args = append(args[:1], append([]string{"--amend"}, args[1:]...)...)
	}
	if err := g.run(ctx, args...); err != nil {
		return "", false, err
	}
	head, err := g.head(ctx)
	return head, true, err
}
