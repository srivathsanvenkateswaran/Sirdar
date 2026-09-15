package fix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// --- fixtures ---------------------------------------------------------

const triageDocJSON = `{
  "ticket": {"key":"OMNI-1","title":"Export fails","trackerUrl":"https://tracker.example/OMNI-1","helpdeskId":"555","helpdeskUrl":"https://desk.example/555","priority":"high","service":"omni","customer":"Acme","customerId":"4561"},
  "title": "Export fails for large orders",
  "complaint": "The export fails for large orders.",
  "timeline": [{"at":"2026-09-10T08:30:00+03:00","role":"customer","summary":"Reported the export failing."}],
  "reproSteps": ["Request a CSV export for a 600-line order."],
  "rootCause": {"hypothesis":"The export handler buffers every row before writing.","confidence":"medium","evidence":[{"source":"logs","query":"service:export","finding":"Timeout after 30s."}],"codeRefs":["export/csv.go:2"]},
  "blastRadius": "Any order above 500 line items.",
  "classification": "code",
  "proposedFix": {"description":"Stream the export.","files":["export/csv.go"],"remediationSql":"","risks":"none"},
  "openQuestions": []
}`

const triageNoteMD = `---
tags: [support-duty, triage]
tracker_key: OMNI-1
tracker_url: https://tracker.example/OMNI-1
helpdesk_id: "555"
helpdesk_url: https://desk.example/555
customer: Acme
date: 2026-09-10
priority: high
service: omni
status: triaged
run: RUNID
provider: claude
---

# Export fails for large orders

## Proposed Fix

Stream the export.
`

const fixReport = `{
  "summary": "Stream the CSV export instead of buffering every row",
  "filesChanged": ["export/csv.go"],
  "testsRun": [{"command":"go test ./export/...","result":"ok"}],
  "risks": "none",
  "deviationFromNote": ""
}`

func configYAML(notesDir string, inPlace bool) string {
	body := `workspace: test
provider: claude
billing: subscription
notes:
  dir: ` + notesDir + `
budget:
  maxTurns: 60
  maxMinutes: 25
  maxUsd: 5
concurrency: 1
playbooks: .sirdar/playbooks
`
	if inPlace {
		body += "fix:\n  inPlace: true\n"
	}
	return body
}

// workspace is a real git repository with a bare "origin", a committed
// source file, and one completed triage run whose note is filed outside the
// repository the way a vault is.
type workspace struct {
	cfg       *config.Config
	root      string
	origin    string
	notesDir  string
	runNote   string
	filedNote string
	runID     string
	inPlace   bool
}

// head is the sha a ref points at, read from the main tree. Refs are shared
// with every linked worktree, so a branch a fix made in a worktree of its
// own is readable here.
func (w *workspace) head(t *testing.T, ref string) string {
	t.Helper()
	return run(t, w.root, "git", "rev-parse", ref)
}

// worktrees lists the linked worktrees the repository still has, main tree
// excluded.
func (w *workspace) worktrees(t *testing.T) []string {
	t.Helper()
	out := run(t, w.root, "git", "worktree", "list", "--porcelain")
	main := realPath(w.root)
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			// git answers with the resolved path; the temp-directory
			// root a test holds is usually the symlinked one.
			if realPath(p) != main {
				paths = append(paths, realPath(p))
			}
		}
	}
	return paths
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newWorkspace builds the workspace a fix runs against, in the default
// mode: the session gets a linked worktree of its own.
func newWorkspace(t *testing.T, status string) *workspace {
	t.Helper()
	return newWorkspaceIn(t, status, false)
}

// newInPlaceWorkspace is the same workspace with fix.inPlace set, which is
// the behaviour the flow had before linked worktrees: `git checkout -B` in
// the operator's own tree.
func newInPlaceWorkspace(t *testing.T, status string) *workspace {
	t.Helper()
	return newWorkspaceIn(t, status, true)
}

func newWorkspaceIn(t *testing.T, status string, inPlace bool) *workspace {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}

	root := t.TempDir()
	run(t, root, "git", "init", "-b", "main", ".")
	// The commits this test makes are throwaway, but they still need an
	// identity to be made at all. The machine's own is used as it stands;
	// a checkout with none skips rather than inventing one.
	if err := exec.Command("git", "-C", root, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}

	notesDir := t.TempDir()
	mustWrite(t, filepath.Join(root, ".sirdar", "config.yaml"), configYAML(notesDir, inPlace))
	if err := os.MkdirAll(filepath.Join(root, ".sirdar", "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, ".sirdar", "playbooks", "50-code.md"), "# Code\n\nRead the handler first.\n")
	mustWrite(t, filepath.Join(root, "export", "csv.go"), "package export\n\nvar rows = 0\n")

	// The run directories and the register are records of the run, not
	// part of the repository — the same exclusion `sirdar init` writes.
	mustWrite(t, filepath.Join(root, ".git", "info", "exclude"),
		".sirdar/runs/\n.sirdar/register.jsonl\n.sirdar/eval/\n.sirdar/worktrees/\n")

	run(t, root, "git", "add", "-A")
	run(t, root, "git", "commit", "-q", "-m", "init")

	origin := filepath.Join(t.TempDir(), "origin.git")
	run(t, root, "git", "init", "--bare", "-b", "main", origin)
	run(t, root, "git", "remote", "add", "origin", origin)
	run(t, root, "git", "push", "-q", "-u", "origin", "main")
	run(t, root, "git", "remote", "set-head", "origin", "main")

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	w := &workspace{cfg: cfg, root: root, origin: origin, notesDir: notesDir, inPlace: inPlace}
	w.seedTriageRun(t, status)
	return w
}

// seedTriageRun writes the completed triage run a fix reads: its state, its
// note.md, its result.json, and the copy filed in the notes directory.
func (w *workspace) seedTriageRun(t *testing.T, status string) {
	t.Helper()
	rn, err := store.Create(w.root, "OMNI-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w.runID = filepath.Base(rn.Dir)
	body := strings.Replace(triageNoteMD, "RUNID", w.runID, 1)
	body = strings.Replace(body, "status: triaged", "status: "+status, 1)

	w.runNote = filepath.Join(rn.Dir, "note.md")
	w.filedNote = filepath.Join(w.notesDir, "OMNI-1 export-fails.md")
	mustWrite(t, w.runNote, body)
	mustWrite(t, w.filedNote, body)
	mustWrite(t, filepath.Join(rn.Dir, "result.json"), triageDocJSON)

	if err := rn.WriteState(store.State{
		RunID: w.runID, Key: "OMNI-1", Kind: store.KindTriage, Status: store.StatusCompleted,
		Provider: "claude", StartedAt: time.Now(), UpdatedAt: time.Now(),
		Notes: []string{w.runNote, w.filedNote},
	}); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- stub provider ----------------------------------------------------

type stubSession struct{ events chan provider.Event }

func (s *stubSession) Events() <-chan provider.Event               { return s.events }
func (s *stubSession) Send(ctx context.Context, text string) error { return nil }
func (s *stubSession) CloseInput() error                           { return nil }
func (s *stubSession) Handle() string                              { return "h1" }
func (s *stubSession) Cancel()                                     {}
func (s *stubSession) Wait() (provider.Result, error)              { return provider.Result{Handle: "h1"}, nil }

// stubProvider stands in for the agent: it makes the edit a real fix
// session would make, then answers with the report.
type stubProvider struct {
	report string
	edit   func(root string) error
	specs  chan provider.SessionSpec
	t      *testing.T
}

func (p *stubProvider) Name() string                                               { return "claude" }
func (p *stubProvider) Doctor(ctx context.Context, binary string) []provider.Check { return nil }

func (p *stubProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	if p.specs != nil {
		select {
		case p.specs <- spec:
		default:
		}
	}
	if p.edit != nil {
		if err := p.edit(spec.Cwd); err != nil {
			p.t.Fatalf("stub edit: %v", err)
		}
	}
	s := &stubSession{events: make(chan provider.Event, 4)}
	go func() {
		s.events <- provider.Event{Kind: provider.EvUsage, Turns: 4, CostUSD: 0.1}
		s.events <- provider.Event{Kind: provider.EvFinal, Final: json.RawMessage(p.report)}
		close(s.events)
	}()
	return s, nil
}

// editCSV is the change the stub agent makes. It also drops a file under
// .sirdar/runs/ in the tree it is standing in, which a real session does by
// existing: that file must not reach the commit. It goes under runs/ and
// not beside it because a write anywhere else in .sirdar/ is what the guard
// stops the run for.
func editCSV(root string) error {
	scratch := filepath.Join(root, ".sirdar", "runs")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(scratch, "scratch.txt"), []byte("run notes\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "export", "csv.go"),
		[]byte("package export\n\n// stream rather than buffer\nvar rows = 1\n"), 0o644)
}

func newDeps(w *workspace, p provider.Provider) runner.Deps {
	return runner.Deps{Config: w.cfg, Provider: p, Env: []string{"PATH=/usr/bin"}}
}

// noGH makes the flow behave as though the GitHub CLI is not installed, so
// no test ever reaches a real remote.
func noGH(t *testing.T) {
	t.Helper()
	saved := ghPath
	ghPath = func() (string, bool) { return "", false }
	t.Cleanup(func() { ghPath = saved })
}

// remoteHas reports whether the bare origin carries the branch.
func remoteHas(t *testing.T, origin, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "--git-dir", origin, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// --- tests ------------------------------------------------------------

// TestDryRunStopsAfterTheBranchAndPrompt: --dry-run is what an operator
// runs to read the prompt before spending a session on it, so it has to
// make the branch and nothing else.
func TestDryRunStopsAfterTheBranchAndPrompt(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	p := &stubProvider{report: fixReport, t: t}
	deps := newDeps(w, p)

	res, err := Run(t.Context(), deps, "OMNI-1", Options{DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Branch != "fix-omni-1-export-fails-for-large-orders" {
		t.Errorf("branch %q", res.Branch)
	}
	if res.Base != "main" {
		t.Errorf("base %q", res.Base)
	}
	// The branch exists, cut from origin/main, and the operator's own tree
	// never moved: the checkout happened in a worktree of the run's own.
	if got := run(t, w.root, "git", "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("the operator's tree is on %q; a fix must not move it", got)
	}
	if w.head(t, res.Branch) != w.head(t, "origin/main") {
		t.Errorf("%s was not cut from origin/main", res.Branch)
	}
	// Nothing ran in the worktree, so it is not left behind.
	if wts := w.worktrees(t); len(wts) != 0 {
		t.Errorf("a dry run left worktrees behind: %v", wts)
	}
	if res.Worktree != "" {
		t.Errorf("the result still names a worktree: %s", res.Worktree)
	}

	prompt, err := os.ReadFile(filepath.Join(w.root, ".sirdar", "runs", "OMNI-1", res.RunID, "prompt.md"))
	if err != nil {
		t.Fatalf("no prompt was written: %v", err)
	}
	for _, want := range []string{"Proposed Fix", "Read the handler first", res.Branch, "deviationFromNote"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("the prompt does not mention %q", want)
		}
	}
	if res.Commit != "" || res.Pushed {
		t.Error("a dry run committed or pushed")
	}
	if remoteHas(t, w.origin, res.Branch) {
		t.Error("a dry run pushed the branch")
	}
}

// TestFixCommitsPushesAndRecords is the whole flow: the agent's edit
// becomes a commit with no attribution, the branch reaches the remote, both
// copies of the triage note move to fix-pushed, and the register records it.
func TestFixCommitsPushesAndRecords(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	specs := make(chan provider.SessionSpec, 1)
	deps := newDeps(w, &stubProvider{report: fixReport, edit: editCSV, specs: specs, t: t})

	res, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Pushed || res.Commit == "" {
		t.Fatalf("result %+v", res)
	}

	// The commit is on the fix branch, says what it did, and carries no
	// attribution of any kind. It is read off the branch: the operator's
	// own tree is still on main, which is the point of the worktree.
	subject := run(t, w.root, "git", "log", "-1", "--pretty=%s", res.Branch)
	if subject != "fix: Stream the CSV export instead of buffering every row" {
		t.Errorf("commit subject %q", subject)
	}
	body := run(t, w.root, "git", "log", "-1", "--pretty=%b", res.Branch)
	if !strings.Contains(body, "Root cause: The export handler buffers every row") {
		t.Errorf("commit body has no root cause:\n%s", body)
	}
	full := run(t, w.root, "git", "log", "-1", "--pretty=%B%n%an <%ae>", res.Branch)
	for _, banned := range []string{"Co-Authored-By", "Co-authored-by", "Generated with", "Claude", "🤖"} {
		if strings.Contains(full, banned) {
			t.Errorf("the commit carries AI attribution (%q):\n%s", banned, full)
		}
	}

	// The change is in the commit and .sirdar is not.
	files := run(t, w.root, "git", "show", "--name-only", "--pretty=format:", res.Commit)
	if !strings.Contains(files, "export/csv.go") {
		t.Errorf("the edit is not in the commit: %q", files)
	}
	if strings.Contains(files, ".sirdar") {
		t.Errorf("the run directory was committed: %q", files)
	}

	// main is untouched, the operator's tree never left it, and the branch
	// is on the remote.
	if head := run(t, w.root, "git", "rev-parse", "--abbrev-ref", "HEAD"); head != "main" {
		t.Errorf("the operator's tree is on %q", head)
	}
	if !remoteHas(t, w.origin, res.Branch) {
		t.Errorf("%s did not reach the remote", res.Branch)
	}
	if mainSha := w.head(t, "main"); mainSha == res.Commit {
		t.Error("main moved")
	}

	// The worktree is gone, and the workspace has no uncommitted entry to
	// show for any of it: .sirdar/worktrees/ is excluded the same way the
	// run directories are, and .git/info/exclude is shared by every linked
	// worktree because it lives in the common git directory.
	if wts := w.worktrees(t); len(wts) != 0 {
		t.Errorf("the worktree was not removed: %v", wts)
	}
	if status := run(t, w.root, "git", "status", "--porcelain"); status != "" {
		t.Errorf("the fix left the workspace dirty:\n%s", status)
	}

	// Both copies of the triage note record the outcome.
	if len(res.NotesUpdated) != 2 {
		t.Errorf("notes updated: %v", res.NotesUpdated)
	}
	for _, path := range []string{w.runNote, w.filedNote} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "status: fix-pushed") {
			t.Errorf("%s was not moved to fix-pushed:\n%s", path, text)
		}
		if !strings.Contains(text, "commit: "+res.Commit) {
			t.Errorf("%s does not record the commit", path)
		}
		if !strings.Contains(text, "# Export fails for large orders") {
			t.Errorf("%s lost its body", path)
		}
	}

	// The register has the fix row.
	rows, err := store.ReadRegister(w.root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range rows {
		if row.Kind == "fix" && row.Key == "OMNI-1" && row.RunID == res.RunID {
			found = true
			if row.Turns != 4 {
				t.Errorf("register row turns %d", row.Turns)
			}
		}
	}
	if !found {
		t.Errorf("no fix row in the register: %+v", rows)
	}

	// The session was a fix session with the write policy, and it stood in
	// the worktree rather than in the operator's tree.
	spec := <-specs
	if !strings.Contains(spec.Cwd, filepath.Join(".sirdar", "worktrees")) {
		t.Errorf("the session ran in %q, not in a linked worktree", spec.Cwd)
	}
	if spec.Policy != nil && spec.Policy.Root != spec.Cwd {
		t.Errorf("the policy confines writes to %q, the session stands in %q", spec.Policy.Root, spec.Cwd)
	}
	if !spec.Mode.IsFix() {
		t.Error("the session was not started in fix mode")
	}
	if !spec.Policy.IsFix() {
		t.Error("the session did not get the fix policy")
	}
	if len(spec.Policy.BashAllow) == 0 || spec.Policy.BashAllow[0] != w.cfg.Permissions.FixBash[0] {
		t.Errorf("the session's bash list is %v, not permissions.fixBash", spec.Policy.BashAllow)
	}
	// The commit and the push are Sirdar's, so the session's own list does
	// not reach them.
	for _, banned := range []string{"git commit -m x", "git push origin main"} {
		if ok, _ := provider.MatchCommand(w.root, spec.Policy.BashAllow, banned); ok {
			t.Errorf("the fix session may run %q", banned)
		}
	}

	// No gh, so the fallback carries the text a human pastes.
	if res.PRURL != "" {
		t.Errorf("a pull request was opened without gh: %s", res.PRURL)
	}
	for _, want := range []string{"## Symptom", "## Root cause", "## Fix", "https://tracker.example/OMNI-1", "https://desk.example/555"} {
		if !strings.Contains(res.PRBody, want) {
			t.Errorf("the pull request body does not carry %q:\n%s", want, res.PRBody)
		}
	}
	if want := "[OMNI-1] fix: Stream the CSV export instead of buffering every row"; res.PRTitle != want {
		t.Errorf("pull request title %q", res.PRTitle)
	}
}

// TestDeviationBlocksThePush is the hard rule: an agent that did something
// other than the note described leaves its work on a local branch for a
// human to read.
func TestDeviationBlocksThePush(t *testing.T) {
	w := newWorkspace(t, "fix-approved")
	noGH(t)
	report := strings.Replace(fixReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "The note named export/csv.go, but the buffering is in export/writer.go"`, 1)
	deps := newDeps(w, &stubProvider{report: report, edit: editCSV, t: t})

	res, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Pushed {
		t.Fatal("a deviating fix was pushed")
	}
	if res.Commit == "" {
		t.Error("the work was thrown away instead of being left on the branch")
	}
	if !strings.Contains(res.Blocked, "export/writer.go") {
		t.Errorf("the deviation was not reported: %q", res.Blocked)
	}
	if remoteHas(t, w.origin, res.Branch) {
		t.Error("the branch reached the remote")
	}
	data, err := os.ReadFile(w.filedNote)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status: fix-approved") {
		t.Error("the triage note was moved on despite the push being blocked")
	}

	// The worktree is kept: the commit in it is what the operator is being
	// asked to read, and --accept-deviation publishes from it.
	if res.Worktree == "" || !isWorktree(res.Worktree) {
		t.Fatalf("the blocked run did not keep its worktree: %q", res.Worktree)
	}
	if wts := w.worktrees(t); len(wts) != 1 || wts[0] != realPath(res.Worktree) {
		t.Errorf("worktrees %v, want just %s", wts, res.Worktree)
	}
	if got := run(t, res.Worktree, "git", "rev-parse", "HEAD"); got != res.Commit {
		t.Errorf("the worktree is at %s, not the commit %s the operator is reading", got, res.Commit)
	}
	// And the state file names it, which is how a later rerun finds it.
	if res.State.Fix.Worktree != res.Worktree {
		t.Errorf("the run state recorded %q, the result says %q", res.State.Fix.Worktree, res.Worktree)
	}

	// With --accept-deviation the same run goes through.
	w2 := newWorkspace(t, "triaged")
	deps2 := newDeps(w2, &stubProvider{report: report, edit: editCSV, t: t})
	res2, err := Run(t.Context(), deps2, "OMNI-1", Options{AcceptDeviation: true})
	if err != nil {
		t.Fatalf("Run with --accept-deviation: %v", err)
	}
	if !res2.Pushed || res2.Blocked != "" {
		t.Fatalf("--accept-deviation did not push: %+v", res2)
	}
	if !strings.Contains(res2.PRBody, "Deviation from the triage note") {
		t.Error("the pull request body hides the deviation")
	}
}

// TestInPlaceModeCommitsInTheOperatorsTree is the escape hatch working as
// it did before linked worktrees: `git checkout -B` on the tree the
// operator is standing in, the session run there, and the tree left on the
// fix branch afterwards. No worktree is made and none is left behind.
func TestInPlaceModeCommitsInTheOperatorsTree(t *testing.T) {
	w := newInPlaceWorkspace(t, "triaged")
	noGH(t)
	specs := make(chan provider.SessionSpec, 1)
	deps := newDeps(w, &stubProvider{report: fixReport, edit: editCSV, specs: specs, t: t})

	res, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Pushed || res.Commit == "" {
		t.Fatalf("result %+v", res)
	}
	if res.Worktree != "" {
		t.Errorf("in-place mode made a worktree: %s", res.Worktree)
	}
	if wts := w.worktrees(t); len(wts) != 0 {
		t.Errorf("in-place mode left worktrees behind: %v", wts)
	}
	// The operator's own tree is what moved, and it is on the fix branch.
	if got := run(t, w.root, "git", "rev-parse", "--abbrev-ref", "HEAD"); got != res.Branch {
		t.Errorf("the operator's tree is on %q, not the fix branch %s", got, res.Branch)
	}
	if head := run(t, w.root, "git", "rev-parse", "HEAD"); head != res.Commit {
		t.Errorf("HEAD is %s, not the fix commit %s", head, res.Commit)
	}
	files := run(t, w.root, "git", "show", "--name-only", "--pretty=format:", res.Commit)
	if !strings.Contains(files, "export/csv.go") {
		t.Errorf("the edit is not in the commit: %q", files)
	}
	if strings.Contains(files, ".sirdar") {
		t.Errorf("the run directory was committed: %q", files)
	}
	if !remoteHas(t, w.origin, res.Branch) {
		t.Errorf("%s did not reach the remote", res.Branch)
	}
	// The session stood in the workspace itself, and the write policy is
	// confined to it.
	spec := <-specs
	if realPath(spec.Cwd) != realPath(w.root) {
		t.Errorf("the session ran in %q, not the workspace %q", spec.Cwd, w.root)
	}
	if spec.Policy == nil || spec.Policy.Root != spec.Cwd {
		t.Errorf("the policy confines writes to %v, the session stands in %q", spec.Policy, spec.Cwd)
	}
}

// TestDirtyTreeIsRefusedInPlace: fix.inPlace commits everything in the tree
// it is standing in, so it may not start on top of somebody's uncommitted
// work.
func TestDirtyTreeIsRefusedInPlace(t *testing.T) {
	w := newInPlaceWorkspace(t, "triaged")
	noGH(t)
	mustWrite(t, filepath.Join(w.root, "export", "scratch.go"), "package export\n")

	deps := newDeps(w, &stubProvider{report: fixReport, t: t})
	_, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err == nil {
		t.Fatal("a dirty working tree was accepted")
	}
	if !strings.Contains(err.Error(), "working tree not clean") {
		t.Errorf("error %v", err)
	}
	if got := run(t, w.root, "git", "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("a branch was made anyway; HEAD is %q", got)
	}
}

// TestADirtyTreeIsNoObstacleToAWorktreeRun is the reason the default
// changed. The operator's uncommitted work is not in the tree the session
// edits, is not swept into its commit, and is still there afterwards — so
// the preflight that used to refuse the run has nothing to refuse.
func TestADirtyTreeIsNoObstacleToAWorktreeRun(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	scratch := filepath.Join(w.root, "export", "scratch.go")
	mustWrite(t, scratch, "package export\n\n// half-finished work\n")

	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run refused a dirty tree in worktree mode: %v", err)
	}
	if !res.Pushed {
		t.Fatalf("result %+v", res)
	}
	files := run(t, w.root, "git", "show", "--name-only", "--pretty=format:", res.Commit)
	if strings.Contains(files, "scratch.go") {
		t.Errorf("the operator's uncommitted work was swept into the commit: %q", files)
	}
	if data, err := os.ReadFile(scratch); err != nil || !strings.Contains(string(data), "half-finished") {
		t.Errorf("the operator's uncommitted work was disturbed: %v", err)
	}
	if got := run(t, w.root, "git", "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("the operator's tree is on %q", got)
	}
}

// TestUnapprovedNoteIsRefused: the human gate is the note's status, and a
// note that has moved on is not an approval.
func TestUnapprovedNoteIsRefused(t *testing.T) {
	for _, status := range []string{"resolved", "fix-pushed", ""} {
		t.Run("status="+status, func(t *testing.T) {
			w := newWorkspace(t, status)
			noGH(t)
			deps := newDeps(w, &stubProvider{report: fixReport, t: t})
			_, err := Run(t.Context(), deps, "OMNI-1", Options{})
			if err == nil {
				t.Fatalf("status %q was accepted as an approval", status)
			}
			if !strings.Contains(err.Error(), "triaged or fix-approved") {
				t.Errorf("error %v", err)
			}
		})
	}
}

func TestNoTriageNoteIsRefused(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	deps := newDeps(w, &stubProvider{report: fixReport, t: t})
	if _, err := Run(t.Context(), deps, "OMNI-404", Options{}); err == nil {
		t.Fatal("a key with no triage note was accepted")
	}
}

// TestNoChangesIsAnError: an agent that reported a fix and edited nothing
// has produced an empty commit, which is worse than a failure.
//
// The run record has to say so too. The session itself ran to the end and
// wrote a valid report, so the runner called it completed — which left the
// first live `provider: qwen` fix looking like a success next to an empty
// branch, its agent's own summary explaining that every write had been
// refused. The state is failed, and the summary is the reason.
func TestNoChangesIsAnError(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	deps := newDeps(w, &stubProvider{report: fixReport, t: t}) // no edit
	res, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err == nil {
		t.Fatal("a session that changed nothing was committed")
	}
	if !strings.Contains(err.Error(), "changed no files") {
		t.Errorf("error %v", err)
	}
	if res.State.Status != store.StatusFailed {
		t.Errorf("result status %q, want failed", res.State.Status)
	}
	if !strings.Contains(res.State.Reason, "changed no files") {
		t.Errorf("result reason %q", res.State.Reason)
	}

	// And on disk, which is what `sirdar runs` and the desktop shell read.
	_, state, err := store.Open(w.root, res.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != store.StatusFailed {
		t.Fatalf("run state %q reason %q, want failed", state.Status, state.Reason)
	}
	if !strings.Contains(state.Reason, "Stream the CSV export instead of buffering every row") {
		t.Errorf("the reason does not carry the agent's summary: %q", state.Reason)
	}
}

// TestPullRequestIsOpenedWithGH points the flow at a fake `gh` so the
// capture of the URL, and its arrival in the note's frontmatter, are
// covered without reaching GitHub.
func TestPullRequestIsOpenedWithGH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	w := newWorkspace(t, "triaged")

	const prURL = "https://github.com/acme/oxo/pull/42"
	fake := filepath.Join(t.TempDir(), "gh")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"auth\" ]; then exit 0; fi\necho %s\n", prURL)
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := ghPath
	ghPath = func() (string, bool) { return fake, true }
	t.Cleanup(func() { ghPath = saved })

	deps := newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t})
	res, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PRURL != prURL {
		t.Fatalf("pull request URL %q", res.PRURL)
	}
	data, err := os.ReadFile(w.filedNote)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `pr: "`+prURL+`"`) {
		t.Errorf("the note does not record the pull request:\n%s", data)
	}
}

// TestNoPRSkipsTheGitHubCLI: --no-pr pushes and stops.
func TestNoPRSkipsTheGitHubCLI(t *testing.T) {
	w := newWorkspace(t, "triaged")
	saved := ghPath
	ghPath = func() (string, bool) {
		t.Error("gh was consulted with --no-pr")
		return "", false
	}
	t.Cleanup(func() { ghPath = saved })

	deps := newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t})
	res, err := Run(t.Context(), deps, "OMNI-1", Options{NoPR: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Pushed || res.PRURL != "" {
		t.Fatalf("result %+v", res)
	}
	// This is exactly the shape a screen would misread if it took "no pull
	// request URL" for "not pushed yet": the branch is on the remote and
	// there is nothing left for a person to accept.
	_, state, err := store.Open(w.root, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Fix.Pushed {
		t.Errorf("the run state does not record the push: %+v", state.Fix)
	}
	if state.Fix.Deviation != "" {
		t.Errorf("a clean run recorded a deviation: %q", state.Fix.Deviation)
	}
}

// The same again for a push whose `gh` call failed: the operator was told
// to open the request by hand, and the branch is still on the remote.
func TestAFailedPullRequestStillRecordsThePush(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t) // no gh at all is the same shape as a gh that failed

	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Pushed || res.PRURL != "" {
		t.Fatalf("result %+v", res)
	}
	_, state, err := store.Open(w.root, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Fix.Pushed {
		t.Errorf("the run state does not record the push: %+v", state.Fix)
	}
	if state.Fix.Commit == "" || state.Fix.Branch == "" {
		t.Errorf("the run state lost the commit or the branch: %+v", state.Fix)
	}
}

// A blocked run is the other side of it: a commit, no push, and the
// deviation that says why.
func TestABlockedRunRecordsNoPush(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	report := strings.Replace(fixReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "touched a different file"`, 1)

	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: report, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, state, err := store.Open(w.root, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Fix.Pushed {
		t.Error("a blocked run recorded a push")
	}
	if state.Fix.Commit == "" || state.Fix.Deviation == "" {
		t.Errorf("the blocked run's state is incomplete: %+v", state.Fix)
	}
}

// --- unit tests -------------------------------------------------------

func TestBranchName(t *testing.T) {
	for _, tc := range []struct{ key, title, want string }{
		{"OMNI-1", "Export fails for large orders", "fix-omni-1-export-fails-for-large-orders"},
		{"OMNI-2510", "", "fix-omni-2510"},
		{"omni-3", "A title that runs on and on and on and on and on and on", "fix-omni-3-a-title-that-runs-on-and-on-and-on-and-o"},
	} {
		if got := BranchName(tc.key, tc.title); got != tc.want {
			t.Errorf("BranchName(%q, %q) = %q, want %q", tc.key, tc.title, got, tc.want)
		}
	}
}

func TestCompareURL(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"git@github.com:acme/oxo.git", "https://github.com/acme/oxo/compare/main...fix-omni-1?expand=1"},
		{"https://github.com/acme/oxo.git", "https://github.com/acme/oxo/compare/main...fix-omni-1?expand=1"},
		{"ssh://git@gitlab.com/acme/oxo.git", "https://gitlab.com/acme/oxo/-/merge_requests/new?merge_request[source_branch]=fix-omni-1&merge_request[target_branch]=main"},
		{"/srv/git/oxo.git", ""},
		{"", ""},
	} {
		if got := compareURL(tc.remote, "main", "fix-omni-1"); got != tc.want {
			t.Errorf("compareURL(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
}

func TestLastURL(t *testing.T) {
	out := "Warning: 3 uncommitted changes\nhttps://github.com/acme/oxo/pull/42\n"
	if got := lastURL(out); got != "https://github.com/acme/oxo/pull/42" {
		t.Errorf("lastURL = %q", got)
	}
	if got := lastURL("nothing here"); got != "" {
		t.Errorf("lastURL = %q", got)
	}
}

// --- round 2 live-run fix: literal \n in the agent's JSON summary ------

// TestSummarySubjectUnescapesAndSplits is the live-run bug: an agent's JSON
// summary carried a doubled backslash before the n, which json.Unmarshal
// decodes into the two literal characters \ and n rather than a line
// break, so the whole multi-line summary landed on the commit subject line
// verbatim, backslashes and all.
func TestSummarySubjectUnescapesAndSplits(t *testing.T) {
	for _, tc := range []struct {
		name           string
		summary        string
		wantSubject    string
		wantBodyPrefix string // "" means body must be empty
	}{
		{
			name:           "literal backslash-n from a doubled JSON escape",
			summary:        `Count a customer return once in Ledger.ApplyMovement\n\nThe Return case added the amount twice.`,
			wantSubject:    "Count a customer return once in Ledger.ApplyMovement",
			wantBodyPrefix: "The Return case added the amount twice.",
		},
		{
			name:           "literal backslash-r-backslash-n",
			summary:        `Fix the export timeout\r\nStream rows instead of buffering.`,
			wantSubject:    "Fix the export timeout",
			wantBodyPrefix: "Stream rows instead of buffering.",
		},
		{
			name:           "a real newline needs no unescaping",
			summary:        "Fix the export timeout\nStream rows instead of buffering.",
			wantSubject:    "Fix the export timeout",
			wantBodyPrefix: "Stream rows instead of buffering.",
		},
		{
			name:           "single line has no body",
			summary:        "Stream the CSV export instead of buffering every row",
			wantSubject:    "Stream the CSV export instead of buffering every row",
			wantBodyPrefix: "",
		},
		{
			name:           "leading blank lines are skipped before the subject is picked",
			summary:        "\n\n  Fix the export timeout  \nDetails follow.",
			wantSubject:    "Fix the export timeout",
			wantBodyPrefix: "Details follow.",
		},
		{
			name:           "an attribution trailer never reaches the body",
			summary:        "Fix the export timeout\nStream rows.\nCo-Authored-By: Claude <noreply@anthropic.com>\nGenerated with Claude Code",
			wantSubject:    "Fix the export timeout",
			wantBodyPrefix: "Stream rows.",
		},
		{
			name:           "a subject over 72 characters is cut at a word boundary",
			summary:        "Stop the reconciliation job from double counting a customer's returned order line items",
			wantSubject:    "Stop the reconciliation job from double counting a customer's returned",
			wantBodyPrefix: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject, body := summarySubject(tc.summary)
			if subject != tc.wantSubject {
				t.Errorf("subject = %q, want %q", subject, tc.wantSubject)
			}
			if len(subject) > subjectMaxLen {
				t.Errorf("subject %q is %d characters, over the %d cap", subject, len(subject), subjectMaxLen)
			}
			if strings.Contains(subject, `\n`) || strings.Contains(subject, "\n") {
				t.Errorf("subject %q still carries a line break", subject)
			}
			if tc.wantBodyPrefix == "" {
				if body != "" {
					t.Errorf("body = %q, want empty", body)
				}
				return
			}
			if !strings.HasPrefix(body, tc.wantBodyPrefix) {
				t.Errorf("body = %q, want prefix %q", body, tc.wantBodyPrefix)
			}
			for _, banned := range []string{"Co-Authored-By", "Generated with"} {
				if strings.Contains(body, banned) {
					t.Errorf("body carries an AI attribution trailer (%q): %q", banned, body)
				}
			}
		})
	}
}

func TestCapLine(t *testing.T) {
	for _, tc := range []struct {
		s    string
		max  int
		want string
	}{
		{"short", 72, "short"},
		{"exactly seven", 13, "exactly seven"},
		{"cut at the nearest word boundary before the limit is reached here", 40, "cut at the nearest word boundary before"},
		{"nospacesatalltocutonwithinthelimitatall", 10, "nospacesat"},
	} {
		if got := capLine(tc.s, tc.max); got != tc.want {
			t.Errorf("capLine(%q, %d) = %q, want %q", tc.s, tc.max, got, tc.want)
		}
		if len(capLine(tc.s, tc.max)) > tc.max {
			t.Errorf("capLine(%q, %d) exceeded the cap", tc.s, tc.max)
		}
	}
}

func TestStripAIAttribution(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"line one\nCo-Authored-By: Claude <noreply@anthropic.com>\nline two", "line one\nline two"},
		{"line one\nco-authored-by: someone\nline two", "line one\nline two"},
		{"Generated with Claude Code\nline two", "line two"},
		{"nothing to strip here", "nothing to strip here"},
	} {
		if got := stripAIAttribution(tc.in); got != tc.want {
			t.Errorf("stripAIAttribution(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCollapseBlankLines(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a\n\n\nb", "a\n\nb"},
		{"a\n\n\n\n\nb", "a\n\nb"},
		{"a\n\nb", "a\n\nb"},
		{"a\nb", "a\nb"},
	} {
		if got := collapseBlankLines(tc.in); got != tc.want {
			t.Errorf("collapseBlankLines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUnescapeNewlines(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`a\nb`, "a\nb"},
		{`a\r\nb`, "a\nb"},
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"a\nb", "a\nb"},
	} {
		if got := unescapeNewlines(tc.in); got != tc.want {
			t.Errorf("unescapeNewlines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCommitMessageUnescapesTheSubject reproduces the live-run bug end to
// end: CommitMessage must not let the agent's escaped newlines or an
// attribution trailer reach the subject, and any remaining lines must move
// into the body ahead of the root-cause paragraph.
func TestCommitMessageUnescapesTheSubject(t *testing.T) {
	var tn triageNote
	tn.doc.RootCause.Hypothesis = "The Return case added the amount twice."
	rep := Report{
		Summary: `Count a customer return once in Ledger.ApplyMovement\n\nThe Return case added the amount twice.\nCo-Authored-By: Claude <noreply@anthropic.com>`,
	}

	subject, body := CommitMessage(tn, rep)
	if subject != "fix: Count a customer return once in Ledger.ApplyMovement" {
		t.Errorf("subject = %q", subject)
	}
	if strings.Contains(subject, `\n`) {
		t.Errorf("subject still carries a literal backslash-n: %q", subject)
	}
	if strings.Contains(body, "Co-Authored-By") {
		t.Errorf("body carries an AI attribution trailer:\n%s", body)
	}
	if !strings.Contains(body, "Root cause: The Return case added the amount twice.") {
		t.Errorf("body lost the root cause:\n%s", body)
	}
	if strings.Contains(body, "\n\n\n") {
		t.Errorf("body has a run of 3+ blank lines:\n%q", body)
	}
}

// TestPullRequestTextUnescapesTheTitle is the same scenario against the pull
// request fallback text, since the agent's report feeds both.
func TestPullRequestTextUnescapesTheTitle(t *testing.T) {
	var tn triageNote
	tn.doc.Title = "Return counted twice"
	rep := Report{
		Summary: `Count a customer return once\nGenerated with Claude Code`,
	}

	title, body := pullRequestText("OMNI-9", tn, rep, false)
	if title != "[OMNI-9] fix: Count a customer return once" {
		t.Errorf("title = %q", title)
	}
	if strings.Contains(body, "Generated with") {
		t.Errorf("body carries an AI attribution trailer:\n%s", body)
	}
}

// --- round 1 review: hooks, reruns, dirty trees, PR text --------------

// TestTheCommitDoesNotRunRepositoryHooks: the commit is made moments after
// a session with write access to the tree, so whatever is in .git/hooks at
// that point must not be executed by it. The hook here fails loudly and
// leaves a marker; both would show if --no-verify were dropped.
func TestTheCommitDoesNotRunRepositoryHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a shell script")
	}
	w := newWorkspace(t, "triaged")
	noGH(t)

	writeHook(t, filepath.Join(w.root, ".git", "hooks", "pre-commit"), "hook-ran")

	deps := newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t})
	res, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Commit == "" || !res.Pushed {
		t.Fatalf("the pre-commit hook stopped the flow: %+v", res)
	}
	if hookRan(w, "hook-ran") {
		t.Error("the repository's pre-commit hook was executed by Sirdar's own commit")
	}
}

// refusingProvider fails the test if a session is started at all.
type refusingProvider struct{ t *testing.T }

func (p *refusingProvider) Name() string                                               { return "claude" }
func (p *refusingProvider) Doctor(ctx context.Context, binary string) []provider.Check { return nil }

func (p *refusingProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	p.t.Error("a second agent session was started for a commit a human had already reviewed")
	return nil, fmt.Errorf("no session")
}

// TestAcceptDeviationRerunPushesTheReviewedCommit: rerunning with
// --accept-deviation means "yes, push that" — the commit the operator just
// read. Re-cutting the branch from origin would orphan it and spend a
// second session deriving something else.
func TestAcceptDeviationRerunPushesTheReviewedCommit(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	report := strings.Replace(fixReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "The note named export/csv.go, but the buffering is in export/writer.go"`, 1)

	first, err := Run(t.Context(), newDeps(w, &stubProvider{report: report, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if first.Pushed || first.Commit == "" || first.Blocked == "" {
		t.Fatalf("the first run did not block with a commit: %+v", first)
	}
	// The worktree the reviewed commit was made in is still there; the
	// rerun publishes out of it rather than out of the operator's tree.
	if first.Worktree == "" || !isWorktree(first.Worktree) {
		t.Fatalf("the blocked run did not keep its worktree: %q", first.Worktree)
	}

	second, err := Run(t.Context(), newDeps(w, &refusingProvider{t: t}), "OMNI-1", Options{AcceptDeviation: true})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if !second.Pushed || second.Blocked != "" {
		t.Fatalf("the rerun did not push: %+v", second)
	}
	if second.Commit != first.Commit {
		t.Errorf("the rerun pushed %s, not the reviewed commit %s", second.Commit, first.Commit)
	}
	if second.Branch != first.Branch || second.Base != first.Base {
		t.Errorf("the rerun changed the branch: %s/%s, was %s/%s", second.Base, second.Branch, first.Base, first.Branch)
	}
	if second.RunID != first.RunID {
		t.Errorf("the rerun made a new run directory %s; the work is run %s's", second.RunID, first.RunID)
	}
	if head := run(t, w.root, "git", "rev-parse", second.Branch); head != first.Commit {
		t.Errorf("the branch is at %s, not the reviewed commit %s", head, first.Commit)
	}
	if !remoteHas(t, w.origin, second.Branch) {
		t.Errorf("%s did not reach the remote", second.Branch)
	}
	// Published, so the worktree has done its job and is taken away.
	if second.Worktree != "" {
		t.Errorf("the rerun's result still names a worktree: %s", second.Worktree)
	}
	if isWorktree(first.Worktree) {
		t.Errorf("the worktree survived the push: %s", first.Worktree)
	}
	if wts := w.worktrees(t); len(wts) != 0 {
		t.Errorf("the repository still has worktrees: %v", wts)
	}
	// The deviation is still in the pull request text, and the report is
	// the one the session filed.
	if !strings.Contains(second.PRBody, "export/writer.go") {
		t.Errorf("the pull request body lost the deviation:\n%s", second.PRBody)
	}
	if second.Report.Summary != "Stream the CSV export instead of buffering every row" {
		t.Errorf("the rerun lost the agent's report: %+v", second.Report)
	}
	// One fix, one register row.
	rows, err := store.ReadRegister(w.root)
	if err != nil {
		t.Fatal(err)
	}
	fixRows := 0
	for _, row := range rows {
		if row.Kind == "fix" {
			fixRows++
		}
	}
	if fixRows != 1 {
		t.Errorf("the rerun appended a second register row: %+v", rows)
	}
	// Both copies of the note record the outcome, now that it is pushed.
	data, err := os.ReadFile(w.filedNote)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status: fix-pushed") {
		t.Error("the triage note was not moved on by the rerun")
	}
}

// TestAcceptDeviationRerunIsRefusedWhenTheBranchMoved: the shortcut is only
// for the commit that was reviewed. A branch that has moved on since holds
// a different change, and the rerun is refused rather than quietly spending
// a second agent session: "accept what I read" is not "do it again".
func TestAcceptDeviationRerunIsRefusedWhenTheBranchMoved(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	report := strings.Replace(fixReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "different file"`, 1)

	first, err := Run(t.Context(), newDeps(w, &stubProvider{report: report, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if first.Worktree == "" || !isWorktree(first.Worktree) {
		t.Fatalf("the blocked run did not keep its worktree: %q", first.Worktree)
	}

	// Somebody committed on top of the reviewed commit, in the worktree
	// the branch is checked out in.
	mustWrite(t, filepath.Join(first.Worktree, "export", "extra.go"), "package export\n")
	run(t, first.Worktree, "git", "add", "-A", "--", ".", ":(exclude).sirdar")
	run(t, first.Worktree, "git", "commit", "-q", "--no-verify", "-m", "another change")
	moved := w.head(t, first.Branch)
	if moved == first.Commit {
		t.Fatal("the branch did not move")
	}

	second, err := Run(t.Context(), newDeps(w, &refusingProvider{t: t}), "OMNI-1",
		Options{AcceptDeviation: true})
	if err == nil {
		t.Fatalf("the rerun started a fresh session over a moved branch: %+v", second)
	}
	// The message has to say both what happened and what to do next; the
	// desktop panel shows it verbatim.
	for _, want := range []string{"branch moved", "without --accept-deviation", first.Branch} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	if second.RunID != "" {
		t.Errorf("the refused rerun still made run %s", second.RunID)
	}
	if second.Pushed {
		t.Error("the refused rerun pushed")
	}
}

// A branch that was deleted after the review is refused the same way: the
// commit is not on it, whatever the reason.
func TestAcceptDeviationRerunIsRefusedWhenTheBranchIsGone(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	report := strings.Replace(fixReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "different file"`, 1)

	first, err := Run(t.Context(), newDeps(w, &stubProvider{report: report, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The blocked run kept its worktree and the branch is checked out
	// there, so the operator's "I deleted it" is a worktree removal and
	// then the branch.
	if isWorktree(first.Worktree) {
		run(t, w.root, "git", "worktree", "remove", "--force", first.Worktree)
	}
	run(t, w.root, "git", "checkout", "-q", first.Base)
	run(t, w.root, "git", "branch", "-q", "-D", first.Branch)

	if _, err := Run(t.Context(), newDeps(w, &refusingProvider{t: t}), "OMNI-1",
		Options{AcceptDeviation: true}); err == nil {
		t.Fatal("a rerun against a deleted branch was not refused")
	} else if !strings.Contains(err.Error(), "branch moved") {
		t.Errorf("the refusal does not say the branch moved: %v", err)
	}
}

// --accept-deviation on a key that has no reviewed commit is not a rerun at
// all: it is the ordinary first fix, asking not to stop if the agent
// deviates. That must still run.
func TestAcceptDeviationOnAFirstFixRunsTheOrdinaryFlow(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	report := strings.Replace(fixReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "different file"`, 1)

	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: report, edit: editCSV, t: t}), "OMNI-1",
		Options{AcceptDeviation: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Pushed || res.Blocked != "" {
		t.Fatalf("the first fix did not push through the deviation: %+v", res)
	}
	// A pushed run is finished with its worktree, so nothing is left
	// registered for the next run to trip over.
	if res.Worktree != "" && isWorktree(res.Worktree) {
		t.Errorf("the pushed run left its worktree registered: %s", res.Worktree)
	}
}

// TestPushReviewedRefusesATamperedWorktreePath: a state.json is not
// adversarial for anything else the fix flow reads back from it, but
// Fix.Worktree feeds `git worktree remove --force`, which deletes whatever
// is at the path with no further question. A state naming a path outside
// .sirdar/worktrees/ — standing in for a hand-edited state.json, or an
// operator's own worktree recorded there by accident — must not be trusted:
// the rerun falls back to the main tree and the path named is left alone.
func TestPushReviewedRefusesATamperedWorktreePath(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	report := strings.Replace(fixReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "different file"`, 1)

	first, err := Run(t.Context(), newDeps(w, &stubProvider{report: report, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if first.Worktree == "" || first.Blocked == "" {
		t.Fatalf("the first run did not block with a worktree: %+v", first)
	}

	// An operator's own worktree, entirely outside .sirdar/worktrees/ —
	// what a tampered state.json is standing in for.
	victim := filepath.Join(t.TempDir(), "operators-own-worktree")
	run(t, w.root, "git", "worktree", "add", "-b", "operators-own-branch", victim)

	rn, state, err := store.Open(w.root, first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	state.Fix.Worktree = victim
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	deps := newDeps(w, &refusingProvider{t: t})
	deps.Stderr = &stderr

	second, err := Run(t.Context(), deps, "OMNI-1", Options{AcceptDeviation: true})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if !second.Pushed {
		t.Fatalf("the rerun did not push: %+v", second)
	}
	if second.Commit != first.Commit {
		t.Errorf("the rerun did not push the reviewed commit: got %s, want %s", second.Commit, first.Commit)
	}
	if second.Worktree != "" {
		t.Errorf("the rerun trusted the tampered worktree path: %q", second.Worktree)
	}
	if !strings.Contains(stderr.String(), victim) {
		t.Errorf("no warning naming the out-of-bounds worktree:\n%s", stderr.String())
	}
	// The point of the guard: the path named outside .sirdar/worktrees/ is
	// never handed to `git worktree remove --force`.
	if !isWorktree(victim) {
		t.Errorf("the operator's own worktree was removed: %s", victim)
	}
	found := false
	for _, wt := range w.worktrees(t) {
		if wt == realPath(victim) {
			found = true
		}
	}
	if !found {
		t.Errorf("git no longer knows about the operator's worktree %s: %v", victim, w.worktrees(t))
	}
}

// TestDirtyTreeOfOnlySirdarFilesSaysSo: the commonest way to meet "working
// tree not clean" is a workspace whose .sirdar/ is not excluded, and the
// error that just says "commit or stash" sends people to commit their own
// run records.
func TestDirtyTreeOfOnlySirdarFilesSaysSo(t *testing.T) {
	w := newInPlaceWorkspace(t, "triaged")
	noGH(t)
	mustWrite(t, filepath.Join(w.root, ".sirdar", "notes.md"), "scratch\n")

	deps := newDeps(w, &stubProvider{report: fixReport, t: t})
	_, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err == nil {
		t.Fatal("a dirty working tree was accepted")
	}
	for _, want := range []string{"under .sirdar/", ".git/info/exclude"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}

	// A real uncommitted change still gets the ordinary refusal.
	mustWrite(t, filepath.Join(w.root, "export", "scratch.go"), "package export\n")
	_, err = Run(t.Context(), deps, "OMNI-1", Options{})
	if err == nil {
		t.Fatal("a dirty working tree was accepted")
	}
	if !strings.Contains(err.Error(), "commit or stash") {
		t.Errorf("a tree with real changes got the .sirdar advice: %v", err)
	}
}

func TestSirdarOnly(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{"?? .sirdar/notes.md", true},
		{" M .sirdar/config.yaml\n?? .sirdar/runs/OMNI-1/state.json", true},
		{"?? .sirdar/notes.md\n M export/csv.go", false},
		{" M export/csv.go", false},
		{`?? ".sirdar/a file.md"`, true},
		{"R  .sirdar/a.md -> .sirdar/b.md", true},
		{"R  .sirdar/a.md -> export/b.md", false},
		{"", false},
	} {
		if got := sirdarOnly(tc.status); got != tc.want {
			t.Errorf("sirdarOnly(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

// TestPullRequestTextKeepsTheComplaintOut: a pull request is often public,
// and the complaint is a quotation from a customer's support ticket.
func TestPullRequestTextKeepsTheComplaintOut(t *testing.T) {
	var tn triageNote
	tn.doc.Title = "Export fails for large orders"
	tn.doc.Complaint = "Ahmed at Acme says the export dies every morning"
	rep := Report{Summary: "Stream the export"}

	_, body := pullRequestText("OMNI-1", tn, rep, false)
	if strings.Contains(body, "Ahmed") {
		t.Errorf("the customer's complaint reached the pull request body by default:\n%s", body)
	}
	if !strings.Contains(body, tn.doc.Title) {
		t.Errorf("the body does not say what broke:\n%s", body)
	}

	_, withIt := pullRequestText("OMNI-1", tn, rep, true)
	if !strings.Contains(withIt, "Ahmed") {
		t.Errorf("fix.prIncludesComplaint did not include the complaint:\n%s", withIt)
	}
}

// --- round 2 review: hooks paths and the reserved-file guard -----------

// writeHook installs an executable hook that leaves a marker and fails. The
// marker goes into the repository's common git directory rather than the
// working tree's top level, because a fix run's tree is a linked worktree
// that is taken away again when the run succeeds.
func writeHook(t *testing.T, path, marker string) {
	t.Helper()
	mustWrite(t, path, "#!/bin/sh\ntouch \"$(git rev-parse --path-format=absolute --git-common-dir)/"+marker+"\"\nexit 1\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// hookRan reports whether a hook installed by writeHook left its marker.
func hookRan(w *workspace, marker string) bool {
	_, err := os.Stat(filepath.Join(w.root, ".git", marker))
	return err == nil
}

// TestThePushDoesNotRunRepositoryHooks: --no-verify on the commit covered
// pre-commit and left pre-push, which runs minutes later on the same tree
// and with the same shell. A repository using husky has both.
func TestThePushDoesNotRunRepositoryHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook is a shell script")
	}
	w := newWorkspace(t, "triaged")
	noGH(t)
	writeHook(t, filepath.Join(w.root, ".git", "hooks", "pre-push"), "push-hook-ran")

	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Pushed {
		t.Fatalf("the pre-push hook stopped the push: %+v", res)
	}
	if hookRan(w, "push-hook-ran") {
		t.Error("the repository's pre-push hook was executed by Sirdar's own push")
	}
}

// editThen returns a stub edit that makes the ordinary source change and
// then writes one more file, the way a provider that ignores the
// permission policy would.
func editThen(path, body string) func(root string) error {
	return func(root string) error {
		if err := editCSV(root); err != nil {
			return err
		}
		full := path
		if !filepath.IsAbs(full) {
			full = filepath.Join(root, filepath.FromSlash(path))
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		return os.WriteFile(full, []byte(body), 0o755)
	}
}

// TestTheGuardStopsARunThatTouchedAReservedFile is the layer that does not
// depend on a provider honouring anything: the policy and the tool both
// refuse these paths, and this catches the write anyway — which is the
// case that matters for Codex, whose sandbox Sirdar configures but does
// not implement, and for any ACP agent.
func TestTheGuardStopsARunThatTouchedAReservedFile(t *testing.T) {
	// Three of the four paths are named from the workspace, not from the
	// tree the session stands in: the workspace's .sirdar/ and the shared
	// .git/hooks are outside the worktree, which is exactly why the guard
	// still has to watch them. The fourth is the checked-in hooks
	// directory, which the worktree has a copy of.
	cases := []struct {
		name   string
		path   func(w *workspace, root string) string
		hooks  string // core.hooksPath to configure, if any
		expect string
	}{
		{
			name:   "a git hook in the shared git directory",
			path:   func(w *workspace, _ string) string { return filepath.Join(w.root, ".git", "hooks", "pre-commit") },
			expect: ".git/hooks/pre-commit",
		},
		{
			name:   "the workspace configuration",
			path:   func(w *workspace, _ string) string { return filepath.Join(w.root, ".sirdar", "config.yaml") },
			expect: ".sirdar/config.yaml",
		},
		{
			name: "a playbook",
			path: func(w *workspace, _ string) string {
				return filepath.Join(w.root, ".sirdar", "playbooks", "50-code.md")
			},
			expect: ".sirdar/playbooks/50-code.md",
		},
		{
			name:   "the configured hooks path",
			path:   func(_ *workspace, root string) string { return filepath.Join(root, ".githooks", "pre-commit") },
			hooks:  ".githooks",
			expect: ".githooks/pre-commit",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorkspace(t, "triaged")
			noGH(t)
			if c.hooks != "" {
				run(t, w.root, "git", "config", "core.hooksPath", c.hooks)
			}

			edit := func(root string) error {
				return editThen(c.path(w, root), "#!/bin/sh\nowned\n")(root)
			}
			res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: edit, t: t}), "OMNI-1", Options{})
			if err == nil {
				t.Fatalf("Run accepted a session that wrote %s: %+v", c.expect, res)
			}
			for _, want := range []string{c.expect, "nothing was committed and nothing was pushed"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %q:\n%v", want, err)
				}
			}
			if res.Commit != "" || res.Pushed {
				t.Errorf("the run committed or pushed anyway: %+v", res)
			}
			// Nothing was restored, and nothing reached the remote.
			if head := run(t, w.root, "git", "log", "-1", "--pretty=%s"); head != "init" {
				t.Errorf("a commit was made: %q", head)
			}
			if out := run(t, w.origin, "git", "branch", "--list"); strings.Contains(out, "fix-omni-1") {
				t.Errorf("the fix branch reached the remote: %q", out)
			}
			// The run is recorded as failed rather than as the completed
			// session it was until the guard ran.
			if res.RunID != "" {
				_, state, err := store.Open(w.root, res.RunID)
				if err != nil {
					t.Fatal(err)
				}
				if state.Status != store.StatusFailed {
					t.Errorf("the run state is %s, want failed", state.Status)
				}
			}
		})
	}
}

// TestTheGuardLetsAnOrdinaryFixThrough is the other half: the run writes
// its own records under .sirdar/runs/ while the session is going on, and
// none of that is tampering.
func TestTheGuardLetsAnOrdinaryFixThrough(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	run(t, w.root, "git", "config", "core.hooksPath", ".githooks")
	mustWrite(t, filepath.Join(w.root, ".githooks", "pre-commit"), "#!/bin/sh\nexit 0\n")
	run(t, w.root, "git", "add", "-A")
	run(t, w.root, "git", "commit", "-q", "-m", "hooks")
	run(t, w.root, "git", "push", "-q", "origin", "main")

	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Commit == "" || !res.Pushed {
		t.Fatalf("an ordinary fix was stopped: %+v", res)
	}
}

// TestTheFixSessionReservesTheConfiguredHooksPath: the session is told
// about the directory as well as being watched over it, so the policy and
// the tools refuse the write before the guard has to notice it.
func TestTheFixSessionReservesTheConfiguredHooksPath(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	run(t, w.root, "git", "config", "core.hooksPath", ".githooks")

	specs := make(chan provider.SessionSpec, 1)
	if _, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, specs: specs, t: t}), "OMNI-1", Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spec := <-specs
	if spec.Policy == nil {
		t.Fatal("the fix session started with no policy")
	}
	// The hooks path is resolved against the tree the session stands in,
	// which is the worktree: that is the tree the commit is made from, and
	// so the tree whose core.hooksPath git would consult.
	want := filepath.Join(spec.Cwd, ".githooks")
	if len(spec.Policy.ExtraReserved) != 1 || spec.Policy.ExtraReserved[0] != want {
		t.Fatalf("the session reserved %v, want [%s]", spec.Policy.ExtraReserved, want)
	}
	in, _ := json.Marshal(map[string]string{"file_path": ".githooks/pre-commit"})
	if d := spec.Policy.Decide("Write", in); d.Allow {
		t.Error("the session's policy allowed a write to the configured hooks path")
	}
}

// TestTheWorktreeSessionReservesItsOwnGitAndSirdar: the reserved names are
// relative to the tree the session stands in, so `.git/` and `.sirdar/`
// inside the worktree are refused exactly as they are in an ordinary
// checkout — and the workspace's `.sirdar/`, which is outside the session's
// root altogether, is refused before the reserved-name rule is reached.
func TestTheWorktreeSessionReservesItsOwnGitAndSirdar(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)

	specs := make(chan provider.SessionSpec, 1)
	if _, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, specs: specs, t: t}), "OMNI-1", Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spec := <-specs
	if spec.Policy == nil {
		t.Fatal("the fix session started with no policy")
	}
	if !strings.Contains(spec.Cwd, filepath.Join(".sirdar", "worktrees")) {
		t.Fatalf("the session ran in %q, not in a linked worktree", spec.Cwd)
	}

	refused := []string{
		filepath.Join(spec.Cwd, ".git", "hooks", "pre-commit"),
		filepath.Join(spec.Cwd, ".git", "config"),
		filepath.Join(spec.Cwd, ".sirdar", "config.yaml"),
		filepath.Join(w.root, ".sirdar", "config.yaml"),
		filepath.Join(w.root, ".sirdar", "playbooks", "50-code.md"),
		filepath.Join(w.root, "export", "csv.go"), // outside the session's root
	}
	for _, path := range refused {
		in, _ := json.Marshal(map[string]string{"file_path": path})
		if d := spec.Policy.Decide("Write", in); d.Allow {
			t.Errorf("the session's policy allowed a write to %s", path)
		}
	}
	// An ordinary source file in the worktree is what the session is for.
	in, _ := json.Marshal(map[string]string{"file_path": filepath.Join(spec.Cwd, "export", "csv.go")})
	if d := spec.Policy.Decide("Write", in); !d.Allow {
		t.Errorf("the session may not edit its own tree: %s", d.Message)
	}
}

// --- pruning stale fix worktrees ---------------------------------------

// seedFixWorktree registers a linked worktree under .sirdar/worktrees/ on a
// branch of its own, and a run whose state.json says status and updatedAt,
// so pruneStaleWorktrees has something to judge it by. The run id doubles
// as the worktree's directory name, the convention worktreePath itself
// uses.
func seedFixWorktree(t *testing.T, root, runID, branch string, status store.Status, updatedAt time.Time) string {
	t.Helper()
	path := worktreePath(root, runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "worktree", "add", "-b", branch, path)

	rn, err := store.CreateID(root, "OMNI-1", runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rn.WriteState(store.State{
		RunID: runID, Key: "OMNI-1", Kind: store.KindFix, Status: status,
		StartedAt: updatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPruneStaleWorktreesLeavesRunningAndBlockedAlone: only a worktree
// whose run ended — completed or failed — a day or more ago is clutter.
// One still running, blocked on an operator, or simply too recent to judge
// safely is left exactly as it is.
func TestPruneStaleWorktreesLeavesRunningAndBlockedAlone(t *testing.T) {
	w := newWorkspace(t, "triaged")
	now := time.Now()
	day := 24 * time.Hour

	oldCompleted := seedFixWorktree(t, w.root, "20260101T000000Z-0001", "prune-completed", store.StatusCompleted, now.Add(-2*day))
	oldFailed := seedFixWorktree(t, w.root, "20260101T000000Z-0002", "prune-failed", store.StatusFailed, now.Add(-2*day))
	oldBlocked := seedFixWorktree(t, w.root, "20260101T000000Z-0003", "prune-blocked", store.StatusBlocked, now.Add(-2*day))
	recentCompleted := seedFixWorktree(t, w.root, "20260101T000000Z-0004", "prune-recent", store.StatusCompleted, now.Add(-1*time.Hour))
	running := seedFixWorktree(t, w.root, "20260101T000000Z-0005", "prune-running", store.StatusRunning, now.Add(-2*day))

	var stderr bytes.Buffer
	pruneStaleWorktrees(t.Context(), git{dir: w.root}, w.root, now, &stderr)

	live := map[string]bool{}
	for _, path := range w.worktrees(t) {
		live[path] = true
	}
	if live[realPath(oldCompleted)] {
		t.Errorf("an old completed run's worktree survived pruning: %s", oldCompleted)
	}
	if live[realPath(oldFailed)] {
		t.Errorf("an old failed run's worktree survived pruning: %s", oldFailed)
	}
	for name, path := range map[string]string{
		"blocked": oldBlocked, "too-recent": recentCompleted, "running": running,
	} {
		if !live[realPath(path)] {
			t.Errorf("the %s run's worktree was pruned: %s", name, path)
		}
	}
	for _, want := range []string{"20260101T000000Z-0001", "20260101T000000Z-0002"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("no log line named the pruned run %s:\n%s", want, stderr.String())
		}
	}
	for _, dontWant := range []string{"20260101T000000Z-0003", "20260101T000000Z-0004", "20260101T000000Z-0005"} {
		if strings.Contains(stderr.String(), dontWant) {
			t.Errorf("a worktree that should have been left alone was logged as pruned (%s):\n%s", dontWant, stderr.String())
		}
	}
}

// noFixProvider is a provider that cannot run a write session at all —
// provider agy, whose CLI gives Sirdar no way to mediate a tool call. It
// fails the test if a session is ever started on it.
type noFixProvider struct{ t *testing.T }

var errNoFix = errors.New("provider agy: fix mode is refused")

func (p *noFixProvider) Name() string                                               { return "agy" }
func (p *noFixProvider) Doctor(ctx context.Context, binary string) []provider.Check { return nil }
func (p *noFixProvider) SupportsFix() bool                                          { return false }
func (p *noFixProvider) FixRefusal() error                                          { return errNoFix }

func (p *noFixProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	p.t.Error("a session was started on a provider that cannot run a fix")
	return nil, errNoFix
}

// TestFixIsRefusedBeforeAnyGitSideEffect: the refusal used to come from the
// provider's own Start, by which point the flow had fetched the default
// branch, cut a fix branch from it and checked that branch out into a
// linked worktree — all left behind for a session that was never going to
// run. The workspace has to be exactly as the operator left it.
func TestFixIsRefusedBeforeAnyGitSideEffect(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)

	branchesBefore := run(t, w.root, "git", "branch", "--list")
	headBefore := w.head(t, "HEAD")
	runDirsBefore := runDirNames(t, w.root)

	res, err := Run(t.Context(), newDeps(w, &noFixProvider{t: t}), "OMNI-1", Options{})
	if err == nil {
		t.Fatal("fix ran on a provider that cannot run one")
	}
	if !errors.Is(err, errNoFix) {
		t.Fatalf("error %v, want the provider's own refusal", err)
	}
	if res.Branch != "" || res.Worktree != "" || res.Commit != "" {
		t.Errorf("the refused run reported work it should not have done: %+v", res)
	}

	if got := run(t, w.root, "git", "branch", "--list"); got != branchesBefore {
		t.Errorf("branches changed:\nbefore %q\nafter  %q", branchesBefore, got)
	}
	if got := w.head(t, "HEAD"); got != headBefore {
		t.Errorf("HEAD moved from %s to %s", headBefore, got)
	}
	if trees := w.worktrees(t); len(trees) != 0 {
		t.Errorf("a linked worktree was added: %v", trees)
	}
	if _, err := os.Stat(filepath.Join(w.root, ".sirdar", "worktrees")); !os.IsNotExist(err) {
		t.Errorf(".sirdar/worktrees was created: %v", err)
	}
	// The triage note the fixture wrote lives under .sirdar/runs/OMNI-1/,
	// so what matters is that the refused fix added nothing beside it.
	if got := runDirNames(t, w.root); got != runDirsBefore {
		t.Errorf("run directories changed:\nbefore %v\nafter  %v", runDirsBefore, got)
	}
}

// runDirNames lists every run directory in the workspace, as one string a
// test can compare before and after.
func runDirNames(t *testing.T, root string) string {
	t.Helper()
	var names []string
	base := filepath.Join(root, ".sirdar", "runs")
	keys, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, key := range keys {
		runs, err := os.ReadDir(filepath.Join(base, key.Name()))
		if err != nil {
			continue
		}
		for _, r := range runs {
			names = append(names, key.Name()+"/"+r.Name())
		}
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}

// --dry-run makes a branch and a prompt on purpose, so it is refused too:
// a provider that cannot run a fix has no prompt worth reading.
func TestDryRunIsRefusedOnAProviderThatCannotFix(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	branchesBefore := run(t, w.root, "git", "branch", "--list")

	if _, err := Run(t.Context(), newDeps(w, &noFixProvider{t: t}), "OMNI-1", Options{DryRun: true}); err == nil {
		t.Fatal("--dry-run ran on a provider that cannot run a fix")
	}
	if got := run(t, w.root, "git", "branch", "--list"); got != branchesBefore {
		t.Errorf("branches changed:\nbefore %q\nafter  %q", branchesBefore, got)
	}
}
