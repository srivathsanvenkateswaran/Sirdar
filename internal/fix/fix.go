// Package fix implements `sirdar fix`: the one Sirdar flow that writes.
//
// Everything else Sirdar does is read-only, and this is the exception a
// human opens deliberately. The gate is the triage note: a person reads it,
// agrees with its Proposed Fix, and runs this command, which is what
// "approved" means here — there is no separate approval record, because a
// second artefact nobody reads would only look like one.
//
// What the command does around the agent is as much of the point as the
// agent is. It cuts a branch from a freshly fetched default branch into a
// linked worktree of its own, runs the session there rather than in the
// tree the operator is working in, never touches the base branch, never
// force-pushes, and stops before the push when the agent says it did
// something other than what the note described. The commit it writes
// carries no AI attribution: the engineer who reviewed the note is the
// author of the change.
//
// The worktree is <workspace>/.sirdar/worktrees/<run-id>, made with
// `git worktree add` and taken away with `git worktree remove` once the
// branch is pushed. It is what makes the flow safe to run on a machine
// somebody is using: the operator's uncommitted work is not in the way and
// is not swept up, their HEAD does not move, and a session's edits land in
// a directory nothing else is reading. A run blocked on a deviation keeps
// its worktree, because the commit sitting in it is what the operator is
// being asked to review and what `--accept-deviation` publishes.
//
// fix.inPlace: true restores the old behaviour — `git checkout -B` in the
// operator's own tree, which needs that tree clean and leaves it on the fix
// branch.
package fix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/worktree"
)

// approvedStatuses are the triage-note statuses a fix may start from.
// "triaged" is a note a human has just read; "fix-approved" is one they
// marked explicitly. Anything else — resolved, fix-pushed, wont-fix, or a
// status this workspace invented — means the ticket has moved on, and
// re-running a fix over it would open a second pull request for a change
// that already exists.
var approvedStatuses = map[string]bool{
	"triaged":      true,
	"fix-approved": true,
}

// Options are the flags of one `sirdar fix` invocation.
type Options struct {
	Model string
	// Base overrides the branch the fix is cut from and targeted at.
	Base string
	// DryRun stops after the branch and the prompt: no agent, no commit,
	// no push.
	DryRun bool
	// NoPR pushes the branch and stops there.
	NoPR bool
	// AcceptDeviation allows the push when the agent reported doing
	// something other than the note's Proposed Fix.
	AcceptDeviation bool

	// At cuts the fix branch from this commit instead of from
	// origin/<base>, so a fix can be generated against the code as it
	// stood when the ticket was filed rather than against today's tip.
	// Anything `git rev-parse` accepts will do: a sha, a tag, HEAD~12.
	At string

	// Local stops the flow at the commit. Nothing is pushed, no pull
	// request is opened, the worktree is kept, and the commit's unified
	// diff is written to fix.diff in the run directory. It is what a
	// retrospective evaluation runs: the fix has to be produced and read,
	// and must not reach anybody's remote.
	Local bool
}

// Report is the agent's JSON answer.
type Report struct {
	Summary           string   `json:"summary"`
	FilesChanged      []string `json:"filesChanged"`
	TestsRun          []Test   `json:"testsRun"`
	Risks             string   `json:"risks"`
	DeviationFromNote string   `json:"deviationFromNote"`
}

// Test is one build or test command the agent ran.
type Test struct {
	Command string `json:"command"`
	Result  string `json:"result"`
}

// Result is everything the command did, for the CLI to print and for a
// caller to assert on.
type Result struct {
	Key    string
	Branch string
	Base   string
	RunID  string
	State  store.State
	Report Report

	// Commit is the sha of the commit this run made, empty when it made
	// none.
	Commit string
	Pushed bool

	// PRURL is the pull request `gh` opened. CompareURL, PRTitle and
	// PRBody are the fallback for a workspace without `gh`: the page to
	// open and the text to paste into it.
	PRURL      string
	CompareURL string
	PRTitle    string
	PRBody     string

	// NotesUpdated lists the triage-note copies whose frontmatter was
	// moved to fix-pushed.
	NotesUpdated []string

	// Blocked is set when the agent reported a deviation and
	// --accept-deviation was not given: the branch is committed and local,
	// and this says why.
	Blocked string

	// Worktree is the linked worktree the session ran in, empty in
	// in-place mode. It is still on disk when the result carries a
	// Blocked reason or an error; on the success path it has been removed
	// by the time the caller sees this — unless the run was local, which
	// keeps it.
	Worktree string

	// Local says the run stopped at the commit: nothing was pushed and no
	// pull request was opened. DiffPath is the commit's unified diff, in
	// the run directory, and At is the commit the branch was cut from when
	// --at named one.
	Local    bool
	DiffPath string
	At       string

	DryRun bool
}

// ghPath finds the GitHub CLI. It is a variable so a test can say there is
// none, or point at one that answers without reaching the network.
var ghPath = func() (string, bool) {
	p, err := exec.LookPath("gh")
	return p, err == nil
}

// Run performs the whole fix flow for one key.
func Run(ctx context.Context, deps runner.Deps, key string, o Options) (Result, error) {
	cfg := deps.Config
	if cfg == nil {
		return Result{}, fmt.Errorf("fix: no workspace configuration")
	}
	res := Result{Key: key, DryRun: o.DryRun, Local: o.Local}
	stderr := deps.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	tn, err := loadTriage(cfg.Root, key)
	if err != nil {
		return res, err
	}
	if !approvedStatuses[tn.status] {
		return res, fmt.Errorf("fix: the triage note for %s has status %q; a fix runs from a note whose status is triaged or fix-approved. Read %s, and if you agree with its Proposed Fix, set the status back or run `sirdar fix` on a freshly triaged ticket",
			key, tn.status, tn.path())
	}

	g := git{dir: cfg.Root}

	// Workspace tidiness, ahead of anything else this run does: a linked
	// worktree from a run that finished a day or more ago and has nothing
	// left to say is clutter under .sirdar/worktrees/, not work in
	// progress, and this is the one place in the flow that runs on every
	// invocation regardless of which path below it takes.
	pruneStaleWorktrees(ctx, g, cfg.Root, time.Now(), stderr)

	// A rerun with --accept-deviation is a human saying yes to a commit
	// they have already read. Cutting the branch again from origin would
	// orphan that commit and spend a second session re-deriving it, so
	// when the branch still carries exactly the commit the blocked run
	// recorded, this pushes that commit and opens the pull request for it.
	//
	// When a blocked run left a commit and the branch no longer carries it,
	// the rerun is refused rather than quietly starting a fresh session: the
	// commit the person read is not what would be pushed, and a full agent
	// run is not what "accept what I reviewed" asks for.
	if o.AcceptDeviation && !o.DryRun {
		prior, rep, ok, err := reviewedCommit(ctx, g, cfg.Root, key)
		if err != nil {
			return res, err
		}
		if ok {
			// --local means the same thing on a rerun as it did on the
			// first run: the commit stays where it is. Accepting a
			// deviation there is accepting the diff, not authorising a
			// push nobody asked for.
			if o.Local {
				return acceptedLocal(ctx, g, cfg, key, prior, rep, stderr)
			}
			return pushReviewed(ctx, g, cfg, key, tn, prior, rep, o, stderr)
		}
	}

	inPlace := cfg.Fix.InPlace
	base, branch, start, err := prepareBranch(ctx, g, key, tn, o, inPlace)
	if err != nil {
		return res, err
	}
	res.Base, res.Branch = base, branch
	if o.At != "" {
		// start is what --at resolved to, so the result and the run state
		// name the commit itself rather than whatever shorthand was typed.
		res.At = start
	}

	// Where the session will stand. In-place mode moves the operator's own
	// HEAD onto the branch; otherwise the branch is checked out into a
	// worktree of this run's own, named after the run id that has not been
	// used yet.
	runID := ""
	work := g
	if inPlace {
		if err := g.run(ctx, "checkout", "-B", branch, start); err != nil {
			return res, err
		}
		fmt.Fprintf(stderr, "[%s] branch %s from %s\n", key, branch, start)
	} else {
		runID = store.NewRunID(time.Now())
		res.Worktree = worktreePath(cfg.Root, runID)
		if err := addWorktree(ctx, g, res.Worktree, branch, start); err != nil {
			return res, err
		}
		work = git{dir: res.Worktree}
		fmt.Fprintf(stderr, "[%s] branch %s from %s in %s\n",
			key, branch, start, relToRoot(cfg.Root, res.Worktree))
	}

	// The third confinement layer, and the only one that does not depend on
	// a provider honouring a policy: what .sirdar/ and the hooks directory
	// look like before the session, to compare with what they look like
	// after it. Taken after the branch is cut, because checking out a
	// branch is itself allowed to change a checked-in hooks directory.
	before, err := takeSnapshot(ctx, cfg.Root, work.dir)
	if err != nil {
		return res, fmt.Errorf("fix: the reserved files could not be read before the session, so the run cannot be checked afterwards: %w", err)
	}

	text := prompt.Fix(prompt.FixInput{
		Key:        key,
		Branch:     branch,
		Playbooks:  loadPlaybooks(cfg.ExpandPath(cfg.Playbooks), stderr),
		TriageNote: tn.body,
		RCANote:    rcaNote(cfg.Root, key),
	})

	r := &runner.Runner{Deps: deps}
	out, err := r.Fix(ctx, key, runner.FixOptions{
		Options: runner.Options{Model: o.Model, DryRun: o.DryRun, At: res.At},
		Prompt:  text,
		Branch:  branch,
		Base:    base,
		Local:   o.Local,
		Root:    res.Worktree,
		RunID:   runID,
	})
	res.RunID, res.State = out.State.RunID, out.State
	if err != nil {
		return res, err
	}
	if o.DryRun {
		// Nothing ran in it, so nothing is in it to read. The branch
		// stays: reading the prompt is often the step before running
		// the fix for real.
		removeWorktree(ctx, g, res.Worktree, stderr, key)
		res.Worktree = ""
		return res, nil
	}

	// Before anything reads the report and before the first git command:
	// a hook the session installed is only code the machine runs at the
	// next commit or push, and this is the last moment before both.
	after, snapErr := takeSnapshot(ctx, cfg.Root, work.dir)
	if snapErr != nil {
		markRunFailed(cfg.Root, out.State.RunID, "the reserved files could not be re-read after the session", stderr, key)
		return res, fmt.Errorf("fix: the reserved files could not be re-read after the session, so the run cannot be trusted; nothing was committed or pushed: %w", snapErr)
	}
	if changed := diffSnapshots(before, after); len(changed) > 0 {
		err := tamperError(changed)
		markRunFailed(cfg.Root, out.State.RunID, "the session changed reserved files", stderr, key)
		res.State.Status, res.State.Reason = store.StatusFailed, "the session changed reserved files"
		return res, err
	}

	if out.State.Status != store.StatusCompleted {
		return res, fmt.Errorf("fix: the session ended %s: %s", out.State.Status, out.State.Reason)
	}

	doc, err := runner.FixReport(cfg.Root, key, out.State.RunID)
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

	commit, err := commitChanges(ctx, work, tn, res.Report)
	if err != nil {
		return res, err
	}
	res.Commit = commit
	fmt.Fprintf(stderr, "[%s] commit %s on %s\n", key, short(commit), branch)

	// The commit goes into the run's state before anything can go wrong
	// with the push: it is what a later --accept-deviation rerun looks for
	// to avoid asking a second agent for work a human already reviewed.
	recordCommit(cfg.Root, out.State.RunID, branch, base, commit, stderr, key)
	res.State.Fix.Branch, res.State.Fix.Base, res.State.Fix.Commit = branch, base, commit

	// The diff is written before the deviation gate, not after it: a local
	// run that stopped on a deviation is exactly the one whose diff
	// somebody is about to read.
	if o.Local {
		res.DiffPath = writeDiff(ctx, work, cfg.Root, out.State.RunID, commit, stderr, key)
		recordFixState(cfg.Root, out.State.RunID, stderr, key, func(s *store.State) {
			s.Fix.Local, s.Fix.DiffPath, s.Fix.Worktree = true, res.DiffPath, res.Worktree
		})
		res.State.Fix.Local, res.State.Fix.DiffPath = true, res.DiffPath
	}

	appendRegister(cfg.Root, key, out.State, tn)

	if dev := strings.TrimSpace(res.Report.DeviationFromNote); dev != "" && !o.AcceptDeviation {
		// The worktree stays. The commit in it is what the operator is
		// being asked to read, and --accept-deviation publishes from it.
		res.Blocked = dev
		// The deviation goes into the run state as well as into the
		// command's output: a desktop shell reads the run directory, and
		// the review that unblocks this commit happens there too.
		recordFixState(cfg.Root, out.State.RunID, stderr, key, func(s *store.State) {
			s.Fix.Deviation = dev
		})
		res.State.Fix.Deviation = dev
		return res, nil
	}

	// A local run ends here. Nothing leaves the machine, so there is no
	// push, no pull request and no note moved to fix-pushed — and the
	// worktree stays, because the commit and the diff in it are the whole
	// output.
	if o.Local {
		fmt.Fprintf(stderr, "[%s] local: %s is on %s and was not pushed\n", key, short(commit), branch)
		return res, nil
	}

	if err := publish(ctx, work, cfg, key, tn, o, &res, stderr); err != nil {
		return res, err
	}
	// Pushed, so the worktree has done its job. It is removed from the
	// main tree, never from inside itself.
	removeWorktree(ctx, g, res.Worktree, stderr, key)
	res.Worktree = ""
	return res, nil
}

// writeDiff puts the commit's unified diff in the run directory as
// fix.diff and returns its path. A failure is reported and otherwise
// ignored: the commit exists either way, and the diff is a convenience for
// whoever reads it next — `git show` on the branch says the same thing.
func writeDiff(ctx context.Context, g git, root, runID, commit string, stderr io.Writer, key string) string {
	patch, err := g.patch(ctx, commit)
	if err != nil {
		fmt.Fprintf(stderr, "[%s] the diff for %s was not written: %v\n", key, short(commit), err)
		return ""
	}
	rn, _, err := store.Open(root, runID)
	if err != nil {
		fmt.Fprintf(stderr, "[%s] the diff for %s was not written: %v\n", key, short(commit), err)
		return ""
	}
	path := filepath.Join(rn.Dir, "fix.diff")
	if err := os.WriteFile(path, []byte(strings.TrimRight(patch, "\n")+"\n"), 0o644); err != nil {
		fmt.Fprintf(stderr, "[%s] the diff for %s was not written: %v\n", key, short(commit), err)
		return ""
	}
	return path
}

// acceptedLocal is `--accept-deviation --local` on a rerun: the human has
// read the commit the previous run left and said yes to it, and yes means
// the diff, not a push. Nothing is published; the result names the commit,
// the diff and the worktree it is all sitting in, exactly as the first run
// did.
func acceptedLocal(ctx context.Context, g git, cfg *config.Config, key string, prior store.State, rep Report, stderr io.Writer) (Result, error) {
	res := Result{
		Key:      key,
		Branch:   prior.Fix.Branch,
		Base:     prior.Fix.Base,
		At:       prior.At,
		RunID:    prior.RunID,
		State:    prior,
		Report:   rep,
		Commit:   prior.Fix.Commit,
		Local:    true,
		DiffPath: prior.Fix.DiffPath,
		Worktree: safeWorktree(cfg.Root, prior.Fix.Worktree, stderr, key),
	}
	work := g
	if isWorktree(res.Worktree) {
		work = git{dir: res.Worktree}
	} else {
		res.Worktree = ""
	}
	if res.DiffPath == "" {
		res.DiffPath = writeDiff(ctx, work, cfg.Root, prior.RunID, res.Commit, stderr, key)
	}
	recordFixState(cfg.Root, prior.RunID, stderr, key, func(s *store.State) {
		s.Fix.Local, s.Fix.DiffPath = true, res.DiffPath
		s.Fix.Deviation = ""
	})
	res.State.Fix.Local, res.State.Fix.DiffPath, res.State.Fix.Deviation = true, res.DiffPath, ""
	fmt.Fprintf(stderr, "[%s] local: %s on %s is the commit run %s made; it stays where it is\n",
		key, short(res.Commit), res.Branch, prior.RunID)
	return res, nil
}

// publish is everything after the commit: the push, the pull request, and
// the two copies of the triage note. Both the ordinary flow and the
// --accept-deviation rerun end here, so a commit reaches the remote the
// same way whichever of them made it.
//
// The push is made with --no-verify: see the comment on the call.
func publish(ctx context.Context, g git, cfg *config.Config, key string, tn triageNote, o Options, res *Result, stderr io.Writer) error {
	// --no-verify for the same reason the commit carries it: a pre-push
	// hook is code, and this push happens minutes after an agent session
	// had write access to the tree. A repository that keeps its hooks in
	// core.hooksPath rather than .git/hooks keeps them in an ordinary
	// source directory, which is one edit away from being the shell the
	// session was refused everywhere else.
	if err := g.run(ctx, "push", "--no-verify", "-u", "origin", res.Branch); err != nil {
		return err
	}
	res.Pushed = true
	fmt.Fprintf(stderr, "[%s] pushed %s\n", key, res.Branch)

	res.PRTitle, res.PRBody = pullRequestText(key, tn, res.Report, cfg.Fix.PRIncludesComplaint)
	if remote, err := g.remoteURL(ctx); err == nil {
		res.CompareURL = compareURL(remote, res.Base, res.Branch)
	}
	if !o.NoPR {
		if url, err := openPR(ctx, cfg.Root, res.Base, res.Branch, res.PRTitle, res.PRBody); err != nil {
			fmt.Fprintf(stderr, "[%s] %v\n", key, err)
		} else {
			res.PRURL = url
		}
	}

	res.NotesUpdated = updateNotes(tn, res.PRURL, res.Commit, stderr, key)

	// What the push produced is recorded on the run that made the commit —
	// which on an --accept-deviation rerun is the earlier run, not a new
	// one — so the screen showing that run says the work has left the
	// machine.
	//
	// The push itself is recorded, not only the pull request URL. With
	// --no-pr, or when `gh` failed and the operator was told to open the
	// request by hand, the branch is on the remote and there is no URL; a
	// reader that had only the URL would take that for work still waiting
	// on them.
	if res.RunID != "" {
		recordFixState(cfg.Root, res.RunID, stderr, key, func(s *store.State) {
			s.Fix.Pushed = res.Pushed
			s.Fix.PRURL = res.PRURL
		})
		res.State.Fix.Pushed, res.State.Fix.PRURL = res.Pushed, res.PRURL
	}
	return nil
}

// reviewedCommit finds the commit a previous fix run left on its branch for
// a human to read: the newest fix run for the key, when it recorded a
// commit and its branch still points at exactly that commit.
//
// A key with no such run — nothing recorded a commit, or the newest fix run
// failed before it made one — returns ok false and no error, and the caller
// runs the ordinary flow: `--accept-deviation` on a first fix is a valid
// thing to ask for and means "do not stop if the agent deviates".
//
// A run that did record a commit whose branch has since moved or gone is an
// error, not a fall-through. The commit a person read is not the branch's
// head any more, so neither pushing it nor starting a fresh session is what
// they asked for; they are told which it was and left to choose.
//
// Only the newest fix run is considered. An older commit that somebody left
// on a branch months ago is not what "--accept-deviation" refers to.
func reviewedCommit(ctx context.Context, g git, root, key string) (store.State, Report, bool, error) {
	var rep Report
	states, err := store.List(root, key)
	if err != nil {
		return store.State{}, rep, false, nil
	}
	for _, s := range states {
		if s.Kind != store.KindFix || s.Eval {
			continue
		}
		if s.Status != store.StatusCompleted || s.Fix.Commit == "" || s.Fix.Branch == "" {
			return store.State{}, rep, false, nil
		}
		head, err := g.out(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+s.Fix.Branch)
		if err != nil {
			return store.State{}, rep, false, fmt.Errorf(
				"fix: --accept-deviation would push %s, the commit run %s left for review, but branch %s no longer exists:"+
					" branch moved; rerun without --accept-deviation to start a fresh fix, or restore the branch at that commit",
				short(s.Fix.Commit), s.RunID, s.Fix.Branch)
		}
		if head != s.Fix.Commit {
			return store.State{}, rep, false, fmt.Errorf(
				"fix: --accept-deviation would push %s, the commit run %s left for review, but branch %s is now at %s:"+
					" branch moved; rerun without --accept-deviation to start a fresh fix, or reset %s to the reviewed commit",
				short(s.Fix.Commit), s.RunID, s.Fix.Branch, short(head), s.Fix.Branch)
		}
		doc, err := runner.FixReport(root, key, s.RunID)
		if err != nil {
			return store.State{}, rep, false, nil
		}
		if err := json.Unmarshal(doc, &rep); err != nil || strings.TrimSpace(rep.Summary) == "" {
			return store.State{}, rep, false, nil
		}
		return s, rep, true, nil
	}
	return store.State{}, rep, false, nil
}

// pushReviewed completes a fix from the commit a previous run made, with no
// agent session, no new branch and no second register row: the work already
// exists and was recorded when it was made.
func pushReviewed(ctx context.Context, g git, cfg *config.Config, key string, tn triageNote, prior store.State, rep Report, o Options, stderr io.Writer) (Result, error) {
	res := Result{
		Key:      key,
		Branch:   prior.Fix.Branch,
		Base:     prior.Fix.Base,
		RunID:    prior.RunID,
		State:    prior,
		Report:   rep,
		Commit:   prior.Fix.Commit,
		Worktree: safeWorktree(cfg.Root, prior.Fix.Worktree, stderr, key),
	}
	if res.Base == "" {
		base, err := g.defaultBranch(ctx)
		if err != nil {
			return res, err
		}
		res.Base = base
	}
	if o.Base != "" {
		res.Base = o.Base
	}
	fmt.Fprintf(stderr, "[%s] %s on %s is the commit run %s made; pushing it rather than starting another session\n",
		key, short(res.Commit), res.Branch, prior.RunID)

	// The commit was made in that run's worktree and the branch is still
	// checked out there, so that is where it is published from. A worktree
	// the operator has since deleted is no obstacle: the branch lives in
	// the shared repository, and the main tree can push it.
	work := g
	if isWorktree(res.Worktree) {
		work = git{dir: res.Worktree}
	} else {
		res.Worktree = ""
	}

	if err := publish(ctx, work, cfg, key, tn, o, &res, stderr); err != nil {
		return res, err
	}
	removeWorktree(ctx, g, res.Worktree, stderr, key)
	res.Worktree = ""
	return res, nil
}

// recordCommit writes the branch, base and commit into the fix run's own
// state.json. A failure is reported and otherwise ignored: the commit is
// made, and failing the command here would only make it look as though it
// were not.
func recordCommit(root, runID, branch, base, commit string, stderr io.Writer, key string) {
	recordFixState(root, runID, stderr, key, func(s *store.State) {
		s.Fix.Branch, s.Fix.Base, s.Fix.Commit = branch, base, commit
	})
}

// recordFixState applies mutate to a fix run's own state.json. A failure is
// reported and otherwise ignored: whatever the state was to record has
// already happened, and failing the command here would only make it look as
// though it had not.
func recordFixState(root, runID string, stderr io.Writer, key string, mutate func(*store.State)) {
	rn, state, err := store.Open(root, runID)
	if err != nil {
		fmt.Fprintf(stderr, "[%s] the commit was not recorded in the run state: %v\n", key, err)
		return
	}
	mutate(&state)
	if err := rn.WriteState(state); err != nil {
		fmt.Fprintf(stderr, "[%s] the commit was not recorded in the run state: %v\n", key, err)
	}
}

// --- the triage note --------------------------------------------------

// triageNote is the approved note, in both the places it lives: the copy in
// the run directory and the copy filed in the notes directory. The filed
// copy is the one a human reads and edits, so it is the one whose status
// decides whether a fix may run; both are updated at the end.
type triageNote struct {
	runPath   string
	filedPath string
	body      string
	status    string
	doc       triageDoc // the run's validated JSON, when it is still there
	trackerID string
	helpdesk  string
}

// triageDoc is the part of a triage document the fix flow reads.
type triageDoc struct {
	Title     string `json:"title"`
	Complaint string `json:"complaint"`
	RootCause struct {
		Hypothesis string `json:"hypothesis"`
	} `json:"rootCause"`
	ProposedFix struct {
		Description string `json:"description"`
	} `json:"proposedFix"`
}

// path is the note a message should name: the filed copy when there is one.
func (t triageNote) path() string {
	if t.filedPath != "" {
		return t.filedPath
	}
	return t.runPath
}

// loadTriage finds the newest completed triage run for key and reads both
// copies of the note it produced.
func loadTriage(root, key string) (triageNote, error) {
	var t triageNote
	runPath, err := store.LatestNote(root, key, store.KindTriage)
	if err != nil {
		return t, fmt.Errorf("fix: no completed triage note for %s; run `sirdar triage %s` first", key, key)
	}
	t.runPath = runPath
	t.filedPath = filedCopy(root, runPath)

	data, err := os.ReadFile(t.path())
	if err != nil {
		return t, fmt.Errorf("fix: read triage note: %w", err)
	}
	t.body = string(data)
	t.status = note.Frontmatter(t.body, "status")
	t.trackerID = note.Frontmatter(t.body, "tracker_url")
	t.helpdesk = note.Frontmatter(t.body, "helpdesk_url")

	if doc, err := os.ReadFile(filepath.Join(filepath.Dir(runPath), "result.json")); err == nil {
		_ = json.Unmarshal(doc, &t.doc)
	}
	return t, nil
}

// filedCopy locates the notes-directory copy of a triage note from the run
// state that recorded both paths, or "" when there is none.
func filedCopy(root, runNotePath string) string {
	runID := filepath.Base(filepath.Dir(runNotePath))
	_, state, err := store.Open(root, runID)
	if err != nil {
		return ""
	}
	for _, path := range state.Notes {
		if path == runNotePath {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// rcaNote returns the RCA note for the key when one exists. A ticket that
// has been through an RCA has a confirmed cause rather than a hypothesis,
// and handing the fix session both is strictly more than handing it one.
func rcaNote(root, key string) string {
	path, err := store.LatestNote(root, key, store.KindRCA)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func loadPlaybooks(dir string, stderr io.Writer) []prompt.Playbook {
	books, err := prompt.LoadPlaybooks(dir)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: playbooks: %v\n", err)
		return nil
	}
	return books
}

// --- git preflight ----------------------------------------------------

// prepareBranch runs the preflight and settles the base, the branch name
// and the start point the branch is cut from. Every refusal here happens
// before an agent process exists. It does not check anything out: the
// caller does that, into the operator's own tree or into a worktree of the
// run's own.
//
// The start point is origin/<base> ordinarily and the commit --at names
// when it names one. --at also skips the fetch: the start point is already
// in this repository, and a retrospective fix has no business updating the
// operator's remote-tracking refs on its way past.
//
// The dirty-tree refusal applies to in-place mode alone. It exists because
// a fix commits everything in the tree it stands in, and in-place mode
// stands in the operator's. A run in its own worktree commits only what the
// session put there, so uncommitted work elsewhere in the repository is
// neither swept up nor a reason to refuse — which is the point of the
// worktree.
func prepareBranch(ctx context.Context, g git, key string, tn triageNote, o Options, inPlace bool) (string, string, string, error) {
	if err := g.run(ctx, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", "", "", fmt.Errorf("fix: the workspace is not a git repository")
	}
	if inPlace {
		clean, status, err := g.clean(ctx)
		if err != nil {
			return "", "", "", err
		}
		if !clean {
			return "", "", "", dirtyTreeError(status)
		}
	}

	at := ""
	if o.At != "" {
		var err error
		if at, err = worktree.ResolveCommit(ctx, g.wt(), o.At); err != nil {
			return "", "", "", fmt.Errorf("fix: --at %s: %w", o.At, err)
		}
	} else if err := g.run(ctx, "fetch", "origin"); err != nil {
		return "", "", "", err
	}

	// The base is the branch the pull request targets. A local run opens
	// none, so a repository whose origin has no discernible default — a
	// retrospective clone, a repository with no remote at all — is no
	// reason to refuse one, as long as --at said where to start.
	base := o.Base
	if base == "" {
		resolved, err := g.defaultBranch(ctx)
		switch {
		case err == nil:
			base = resolved
		case o.Local && at != "":
		default:
			return "", "", "", err
		}
	}
	branch := BranchName(key, tn.doc.Title)
	if base != "" && branch == base {
		return "", "", "", fmt.Errorf("fix: the fix branch would be %s, which is the base branch; a fix never commits to the default branch", base)
	}

	start := at
	if start == "" {
		start = "origin/" + base
	}
	return base, branch, start, nil
}

// dirtyTreeError explains the refusal, and says the useful thing in the
// case that catches people out: a workspace whose only uncommitted entries
// are Sirdar's own run records. Those belong in .git/info/exclude — which
// is what `sirdar init` writes, and what a workspace initialised before
// that rule existed is missing — not in the commit a fix makes.
func dirtyTreeError(status string) error {
	if sirdarOnly(status) {
		return fmt.Errorf("fix: working tree not clean, but every uncommitted entry is under .sirdar/ — "+
			"Sirdar's own run records and register, not your work. Exclude them from the repository:\n\n"+
			"  printf '%%s\\n' .sirdar/runs/ .sirdar/register.jsonl .sirdar/eval/ .sirdar/worktrees/ >> .git/info/exclude\n\n"+
			"and run the fix again:\n%s", status)
	}
	return fmt.Errorf("fix: working tree not clean; commit or stash your changes first, "+
		"or unset fix.inPlace so the fix runs in a worktree of its own and leaves this tree alone:\n%s", status)
}

// sirdarOnly reports whether every entry in a `git status --porcelain`
// listing names a path under .sirdar/.
func sirdarOnly(status string) bool {
	lines := strings.Split(strings.TrimSpace(status), "\n")
	if strings.TrimSpace(status) == "" {
		return false
	}
	for _, line := range lines {
		paths := statusPaths(line)
		if len(paths) == 0 {
			return false
		}
		for _, path := range paths {
			if !strings.HasPrefix(path, ".sirdar/") {
				return false
			}
		}
	}
	return true
}

// statusPaths pulls the path out of one porcelain status line — two status
// characters, a blank, then the path — and both paths out of a rename,
// whose two are separated by " -> ". Git quotes a path carrying anything
// unusual, so the quotes come off before the prefix is tested.
func statusPaths(line string) []string {
	if len(line) < 4 {
		return nil
	}
	rest := strings.TrimSpace(line[2:])
	if from, to, ok := strings.Cut(rest, " -> "); ok {
		return []string{unquotePath(from), unquotePath(to)}
	}
	return []string{unquotePath(rest)}
}

func unquotePath(p string) string {
	p = strings.TrimSpace(p)
	if unquoted, err := strconv.Unquote(p); err == nil {
		return unquoted
	}
	return strings.Trim(p, `"`)
}

// branchSlugMax keeps a branch name short enough to read in a terminal.
const branchSlugMax = 40

// BranchName is the branch a fix for this key and title goes on:
// "fix-<key lowercased>-<slug of the title>".
func BranchName(key, title string) string {
	name := "fix-" + strings.ToLower(key)
	slug := note.Slug(title)
	if len(slug) > branchSlugMax {
		slug = strings.TrimRight(slug[:branchSlugMax], "-")
	}
	if slug != "" {
		name += "-" + slug
	}
	return name
}

// --- commit, push, pull request ---------------------------------------

// commitChanges stages everything but .sirdar/ and commits it. The message
// is the agent's summary as the subject and the note's root cause under it,
// and it carries no attribution trailer of any kind: the change is the
// work of the engineer who approved the note.
//
// The commit is made with --no-verify. A repository's hooks are code, and
// this commit is made moments after an agent session had write access to
// the tree: running whatever is in .git/hooks at that point would hand a
// prompt injection the shell it was refused everywhere else. Nothing stops
// an operator from running their own hooks over the branch afterwards —
// the pull request is where that check belongs.
func commitChanges(ctx context.Context, g git, tn triageNote, rep Report) (string, error) {
	// .sirdar/ holds run directories and the register, which are records
	// of this run and not part of the fix.
	if err := g.run(ctx, "add", "-A", "--", ".", ":(exclude).sirdar"); err != nil {
		return "", err
	}
	if err := g.run(ctx, "diff", "--cached", "--quiet"); err == nil {
		return "", fmt.Errorf("fix: the agent changed no files; nothing to commit. Its summary was: %s", firstLine(rep.Summary))
	}
	subject, body := CommitMessage(tn, rep)
	if err := g.run(ctx, "commit", "--no-verify", "-m", subject, "-m", body); err != nil {
		return "", err
	}
	return g.head(ctx)
}

// CommitMessage renders the commit subject and body.
func CommitMessage(tn triageNote, rep Report) (string, string) {
	line, rest := summarySubject(rep.Summary)
	subject := "fix: " + line

	var b strings.Builder
	if rest != "" {
		b.WriteString(rest + "\n\n")
	}
	if cause := strings.TrimSpace(tn.doc.RootCause.Hypothesis); cause != "" {
		b.WriteString("Root cause: " + oneLine(cause) + "\n\n")
	}
	if len(rep.FilesChanged) > 0 {
		b.WriteString("Files: " + strings.Join(rep.FilesChanged, ", "))
	}
	return subject, collapseBlankLines(strings.TrimSpace(b.String()))
}

// pullRequestText renders the pull request's title and body: the symptom,
// the root cause the note settled on, the fix the agent made, and the two
// links back to the systems of record.
//
// The symptom is the note's title unless the workspace set
// fix.prIncludesComplaint. The complaint is the customer's own words out of
// a support ticket, and a pull request is often public or read by people
// who have no business with that ticket; the title says what broke without
// quoting whoever reported it.
func pullRequestText(key string, tn triageNote, rep Report, includeComplaint bool) (string, string) {
	line, rest := summarySubject(rep.Summary)
	title := fmt.Sprintf("[%s] fix: %s", key, line)

	symptom := fallback(tn.doc.Title, "See the triage note.")
	if includeComplaint {
		symptom = fallback(tn.doc.Complaint, tn.doc.Title, "See the triage note.")
	}

	var b strings.Builder
	b.WriteString("## Symptom\n\n")
	b.WriteString(symptom + "\n\n")
	b.WriteString("## Root cause\n\n")
	b.WriteString(fallback(tn.doc.RootCause.Hypothesis, "See the triage note.") + "\n\n")
	b.WriteString("## Fix\n\n")
	b.WriteString(line)
	if rest != "" {
		b.WriteString("\n\n" + rest)
	}
	b.WriteString("\n")
	if len(rep.FilesChanged) > 0 {
		b.WriteString("\nFiles changed:\n")
		for _, f := range rep.FilesChanged {
			b.WriteString("- `" + f + "`\n")
		}
	}
	if len(rep.TestsRun) > 0 {
		b.WriteString("\nChecks run:\n")
		for _, t := range rep.TestsRun {
			b.WriteString("- `" + t.Command + "` — " + t.Result + "\n")
		}
	}
	if risks := cleanAgentText(rep.Risks); risks != "" && !strings.EqualFold(risks, "none") {
		b.WriteString("\nRisks: " + risks + "\n")
	}
	if dev := cleanAgentText(rep.DeviationFromNote); dev != "" {
		b.WriteString("\n**Deviation from the triage note:** " + dev + "\n")
	}

	b.WriteString("\n## Links\n\n")
	wrote := false
	for _, l := range []struct{ label, url string }{
		{"Tracker", tn.trackerID},
		{"Helpdesk", tn.helpdesk},
	} {
		if l.url != "" {
			b.WriteString("- " + l.label + ": " + l.url + "\n")
			wrote = true
		}
	}
	if !wrote {
		b.WriteString("- Triage note: " + filepath.Base(tn.path()) + "\n")
	}
	return title, collapseBlankLines(b.String())
}

// openPR creates the pull request with `gh`, returning its URL. A missing
// or unauthenticated `gh` is reported as an error the caller degrades on
// rather than fails on: the branch is pushed either way, and the compare
// URL is already in the result.
func openPR(ctx context.Context, dir, base, branch, title, body string) (string, error) {
	gh, ok := ghPath()
	if !ok {
		return "", fmt.Errorf("gh is not on PATH, so no pull request was opened")
	}
	check := exec.CommandContext(ctx, gh, "auth", "status")
	check.Dir = dir
	if err := check.Run(); err != nil {
		return "", fmt.Errorf("gh is not authenticated (`gh auth login`), so no pull request was opened")
	}
	cmd := exec.CommandContext(ctx, gh, "pr", "create",
		"--base", base, "--head", branch, "--title", title, "--body", body)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh pr create failed: %v", err)
	}
	return lastURL(string(out)), nil
}

// lastURL picks the pull request URL out of gh's output, which prints it on
// a line of its own, sometimes after a line of chatter.
func lastURL(out string) string {
	var url string
	for _, line := range strings.Fields(out) {
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			url = line
		}
	}
	return url
}

// --- records ----------------------------------------------------------

// updateNotes moves both copies of the triage note to fix-pushed and
// records the pull request and the commit in their frontmatter. A note that
// cannot be updated is reported and skipped: the branch is already pushed,
// and failing the command now would only make it look as though it had not
// been.
func updateNotes(tn triageNote, prURL, commit string, stderr io.Writer, key string) []string {
	pairs := []note.KV{{Key: "status", Value: "fix-pushed"}}
	if prURL != "" {
		pairs = append(pairs, note.KV{Key: "pr", Value: `"` + prURL + `"`})
	}
	if commit != "" {
		pairs = append(pairs, note.KV{Key: "commit", Value: commit})
	}

	var updated []string
	for _, path := range []string{tn.runPath, tn.filedPath} {
		if path == "" {
			continue
		}
		if err := note.UpdateFrontmatter(path, pairs); err != nil {
			fmt.Fprintf(stderr, "[%s] triage note %s was not updated: %v\n", key, path, err)
			continue
		}
		updated = append(updated, path)
	}
	return updated
}

// appendRegister records the fix in the workspace's audit index, alongside
// the triage and rca rows for the same key.
func appendRegister(root, key string, state store.State, tn triageNote) {
	_ = store.AppendRegister(root, store.RegisterRow{
		Key:      key,
		Kind:     string(store.KindFix),
		RunID:    state.RunID,
		Date:     time.Now().Format("2006-01-02"),
		Provider: state.Provider,
		Model:    state.Model,
		Turns:    state.Usage.Turns,
		CostUSD:  state.Usage.CostUSD,
		NotePath: tn.path(),
	})
}

// --- small helpers ----------------------------------------------------

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// subjectMaxLen is the git convention for a commit subject line; the same
// cap applies to the summary line a pull request title is built from.
const subjectMaxLen = 72

// summarySubject turns an agent's raw JSON summary into a subject line and
// whatever body text follows it.
//
// An agent occasionally reports a multi-line summary as a JSON string with
// a doubled backslash before the n (or r-n), which json.Unmarshal decodes
// into the two literal characters \ and n rather than a line break;
// unescapeNewlines turns both that and any real line break into the same
// separator so the two cases are handled alike. The first non-empty line
// becomes the subject, capped at subjectMaxLen characters on a word
// boundary with nothing appended in its place. Every following line becomes
// the returned body text, with any AI attribution trailer stripped and runs
// of 3 or more blank lines collapsed to one.
func summarySubject(s string) (subject, body string) {
	lines := strings.Split(unescapeNewlines(s), "\n")

	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) {
		return "", ""
	}
	subject = capLine(strings.TrimSpace(lines[i]), subjectMaxLen)
	body = collapseBlankLines(stripAIAttribution(strings.Join(lines[i+1:], "\n")))
	return subject, strings.TrimSpace(body)
}

// cleanAgentText applies the same unescaping and attribution stripping as
// summarySubject to a single free-text field the agent wrote, such as
// Report.Risks or Report.DeviationFromNote.
func cleanAgentText(s string) string {
	return strings.TrimSpace(collapseBlankLines(stripAIAttribution(unescapeNewlines(s))))
}

// unescapeNewlines normalizes real CRLF/CR line endings to \n and turns the
// literal two- and four-character escape sequences \r\n and \n — the shape
// a doubled backslash survives json.Unmarshal as — into real line breaks
// too.
func unescapeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, `\r\n`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// capLine cuts s to at most max characters at the last word boundary at or
// before the limit, appending nothing. A line with no space to cut on is
// hard-cut at max.
func capLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ")
}

// stripAIAttribution removes any line that opens with an AI attribution
// trailer — Co-Authored-By: or Generated with — from text the agent wrote;
// the commit and the pull request it produces are the reviewing engineer's,
// not the agent's.
func stripAIAttribution(s string) string {
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, l := range lines {
		low := strings.ToLower(strings.TrimSpace(l))
		if strings.HasPrefix(low, "co-authored-by:") || strings.HasPrefix(low, "generated with") {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

// collapseBlankLines reduces any run of 3 or more consecutive newlines to
// exactly 2, so a blank line between paragraphs survives but a longer gap
// left by stripped or unescaped lines does not.
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// fallback returns the first non-empty of its arguments.
func fallback(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
