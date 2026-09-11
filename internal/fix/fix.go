// Package fix implements `sirdar fix`: the one Sirdar flow that writes.
//
// Everything else Sirdar does is read-only, and this is the exception a
// human opens deliberately. The gate is the triage note: a person reads it,
// agrees with its Proposed Fix, and runs this command, which is what
// "approved" means here — there is no separate approval record, because a
// second artefact nobody reads would only look like one.
//
// What the command does around the agent is as much of the point as the
// agent is. It refuses a dirty working tree, cuts a branch from a freshly
// fetched default branch, never touches that branch itself, never
// force-pushes, and stops before the push when the agent says it did
// something other than what the note described. The commit it writes
// carries no AI attribution: the engineer who reviewed the note is the
// author of the change.
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
	res := Result{Key: key, DryRun: o.DryRun}
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

	// A rerun with --accept-deviation is a human saying yes to a commit
	// they have already read. Cutting the branch again from origin would
	// orphan that commit and spend a second session re-deriving it, so
	// when the branch still carries exactly the commit the blocked run
	// recorded, this pushes that commit and opens the pull request for it.
	if o.AcceptDeviation && !o.DryRun {
		if prior, rep, ok := reviewedCommit(ctx, g, cfg.Root, key); ok {
			return pushReviewed(ctx, g, cfg, key, tn, prior, rep, o, stderr)
		}
	}

	base, branch, err := prepareBranch(ctx, g, key, tn, o)
	if err != nil {
		return res, err
	}
	res.Base, res.Branch = base, branch
	fmt.Fprintf(stderr, "[%s] branch %s from origin/%s\n", key, branch, base)

	text := prompt.Fix(prompt.FixInput{
		Key:        key,
		Branch:     branch,
		Playbooks:  loadPlaybooks(cfg.ExpandPath(cfg.Playbooks), stderr),
		TriageNote: tn.body,
		RCANote:    rcaNote(cfg.Root, key),
	})

	r := &runner.Runner{Deps: deps}
	out, err := r.Fix(ctx, key, runner.FixOptions{
		Options: runner.Options{Model: o.Model, DryRun: o.DryRun},
		Prompt:  text,
		Branch:  branch,
		Base:    base,
	})
	res.RunID, res.State = out.State.RunID, out.State
	if err != nil {
		return res, err
	}
	if o.DryRun {
		return res, nil
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

	commit, err := commitChanges(ctx, g, tn, res.Report)
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

	appendRegister(cfg.Root, key, out.State, tn)

	if dev := strings.TrimSpace(res.Report.DeviationFromNote); dev != "" && !o.AcceptDeviation {
		res.Blocked = dev
		return res, nil
	}

	if err := publish(ctx, g, cfg, key, tn, o, &res, stderr); err != nil {
		return res, err
	}
	return res, nil
}

// publish is everything after the commit: the push, the pull request, and
// the two copies of the triage note. Both the ordinary flow and the
// --accept-deviation rerun end here, so a commit reaches the remote the
// same way whichever of them made it.
func publish(ctx context.Context, g git, cfg *config.Config, key string, tn triageNote, o Options, res *Result, stderr io.Writer) error {
	if err := g.run(ctx, "push", "-u", "origin", res.Branch); err != nil {
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
	return nil
}

// reviewedCommit finds the commit a previous fix run left on its branch for
// a human to read: the newest fix run for the key, when it recorded a
// commit and its branch still points at exactly that commit. A branch that
// has moved on, been deleted, or was never recorded means there is nothing
// to reuse, and the caller runs the ordinary flow.
//
// Only the newest fix run is considered. An older commit that somebody left
// on a branch months ago is not what "--accept-deviation" refers to.
func reviewedCommit(ctx context.Context, g git, root, key string) (store.State, Report, bool) {
	var rep Report
	states, err := store.List(root, key)
	if err != nil {
		return store.State{}, rep, false
	}
	for _, s := range states {
		if s.Kind != store.KindFix || s.Eval {
			continue
		}
		if s.Status != store.StatusCompleted || s.Fix.Commit == "" || s.Fix.Branch == "" {
			return store.State{}, rep, false
		}
		head, err := g.out(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+s.Fix.Branch)
		if err != nil || head != s.Fix.Commit {
			return store.State{}, rep, false
		}
		doc, err := runner.FixReport(root, key, s.RunID)
		if err != nil {
			return store.State{}, rep, false
		}
		if err := json.Unmarshal(doc, &rep); err != nil || strings.TrimSpace(rep.Summary) == "" {
			return store.State{}, rep, false
		}
		return s, rep, true
	}
	return store.State{}, rep, false
}

// pushReviewed completes a fix from the commit a previous run made, with no
// agent session, no new branch and no second register row: the work already
// exists and was recorded when it was made.
func pushReviewed(ctx context.Context, g git, cfg *config.Config, key string, tn triageNote, prior store.State, rep Report, o Options, stderr io.Writer) (Result, error) {
	res := Result{
		Key:    key,
		Branch: prior.Fix.Branch,
		Base:   prior.Fix.Base,
		RunID:  prior.RunID,
		State:  prior,
		Report: rep,
		Commit: prior.Fix.Commit,
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

	if err := publish(ctx, g, cfg, key, tn, o, &res, stderr); err != nil {
		return res, err
	}
	return res, nil
}

// recordCommit writes the branch, base and commit into the fix run's own
// state.json. A failure is reported and otherwise ignored: the commit is
// made, and failing the command here would only make it look as though it
// were not.
func recordCommit(root, runID, branch, base, commit string, stderr io.Writer, key string) {
	rn, state, err := store.Open(root, runID)
	if err != nil {
		fmt.Fprintf(stderr, "[%s] the commit was not recorded in the run state: %v\n", key, err)
		return
	}
	state.Fix.Branch, state.Fix.Base, state.Fix.Commit = branch, base, commit
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

// prepareBranch runs the preflight and puts the workspace on the fix
// branch. Every refusal here happens before an agent process exists.
func prepareBranch(ctx context.Context, g git, key string, tn triageNote, o Options) (string, string, error) {
	if err := g.run(ctx, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", "", fmt.Errorf("fix: the workspace is not a git repository")
	}
	clean, status, err := g.clean(ctx)
	if err != nil {
		return "", "", err
	}
	if !clean {
		return "", "", dirtyTreeError(status)
	}
	if err := g.run(ctx, "fetch", "origin"); err != nil {
		return "", "", err
	}

	base := o.Base
	if base == "" {
		base, err = g.defaultBranch(ctx)
		if err != nil {
			return "", "", err
		}
	}
	branch := BranchName(key, tn.doc.Title)
	if branch == base {
		return "", "", fmt.Errorf("fix: the fix branch would be %s, which is the base branch; a fix never commits to the default branch", base)
	}
	if err := g.run(ctx, "checkout", "-B", branch, "origin/"+base); err != nil {
		return "", "", err
	}
	return base, branch, nil
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
			"  printf '%%s\\n' .sirdar/runs/ .sirdar/register.jsonl .sirdar/eval/ >> .git/info/exclude\n\n"+
			"and run the fix again:\n%s", status)
	}
	return fmt.Errorf("fix: working tree not clean; commit or stash your changes first:\n%s", status)
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
	subject := "fix: " + firstLine(rep.Summary)

	var b strings.Builder
	if cause := strings.TrimSpace(tn.doc.RootCause.Hypothesis); cause != "" {
		b.WriteString("Root cause: " + oneLine(cause) + "\n\n")
	}
	b.WriteString(strings.TrimSpace(rep.Summary))
	if len(rep.FilesChanged) > 0 {
		b.WriteString("\n\nFiles: " + strings.Join(rep.FilesChanged, ", "))
	}
	return subject, b.String()
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
	title := fmt.Sprintf("[%s] fix: %s", key, firstLine(rep.Summary))

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
	b.WriteString(strings.TrimSpace(rep.Summary) + "\n")
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
	if risks := strings.TrimSpace(rep.Risks); risks != "" && !strings.EqualFold(risks, "none") {
		b.WriteString("\nRisks: " + risks + "\n")
	}
	if dev := strings.TrimSpace(rep.DeviationFromNote); dev != "" {
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
	return title, b.String()
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
