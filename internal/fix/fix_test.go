package fix

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func configYAML(notesDir string) string {
	return `workspace: test
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

func newWorkspace(t *testing.T, status string) *workspace {
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
	mustWrite(t, filepath.Join(root, ".sirdar", "config.yaml"), configYAML(notesDir))
	if err := os.MkdirAll(filepath.Join(root, ".sirdar", "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, ".sirdar", "playbooks", "50-code.md"), "# Code\n\nRead the handler first.\n")
	mustWrite(t, filepath.Join(root, "export", "csv.go"), "package export\n\nvar rows = 0\n")

	// The run directories and the register are records of the run, not
	// part of the repository — the same exclusion `sirdar init` writes.
	mustWrite(t, filepath.Join(root, ".git", "info", "exclude"), ".sirdar/runs/\n.sirdar/register.jsonl\n.sirdar/eval/\n")

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

	w := &workspace{cfg: cfg, root: root, origin: origin, notesDir: notesDir}
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
// .sirdar/runs/, which a real session does by existing: that file must not
// reach the commit. It goes under runs/ and not beside it because a write
// anywhere else in .sirdar/ is what the guard stops the run for.
func editCSV(root string) error {
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "runs", "scratch.txt"), []byte("run notes\n"), 0o644); err != nil {
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
	if got := run(t, w.root, "git", "rev-parse", "--abbrev-ref", "HEAD"); got != res.Branch {
		t.Errorf("the workspace is on %q, not the fix branch", got)
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
	// attribution of any kind.
	subject := run(t, w.root, "git", "log", "-1", "--pretty=%s")
	if subject != "fix: Stream the CSV export instead of buffering every row" {
		t.Errorf("commit subject %q", subject)
	}
	body := run(t, w.root, "git", "log", "-1", "--pretty=%b")
	if !strings.Contains(body, "Root cause: The export handler buffers every row") {
		t.Errorf("commit body has no root cause:\n%s", body)
	}
	full := run(t, w.root, "git", "log", "-1", "--pretty=%B%n%an <%ae>")
	for _, banned := range []string{"Co-Authored-By", "Co-authored-by", "Generated with", "Claude", "🤖"} {
		if strings.Contains(full, banned) {
			t.Errorf("the commit carries AI attribution (%q):\n%s", banned, full)
		}
	}

	// The change is in the commit and .sirdar is not.
	files := run(t, w.root, "git", "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(files, "export/csv.go") {
		t.Errorf("the edit is not in the commit: %q", files)
	}
	if strings.Contains(files, ".sirdar") {
		t.Errorf("the run directory was committed: %q", files)
	}
	if _, err := os.Stat(filepath.Join(w.root, ".sirdar", "runs", "scratch.txt")); err != nil {
		t.Fatalf("the fixture file under .sirdar is missing: %v", err)
	}

	// main is untouched, and the branch is on the remote.
	if head := run(t, w.root, "git", "rev-parse", "--abbrev-ref", "HEAD"); head == "main" {
		t.Error("the fix was made on main")
	}
	if !remoteHas(t, w.origin, res.Branch) {
		t.Errorf("%s did not reach the remote", res.Branch)
	}
	if mainSha := run(t, w.root, "git", "rev-parse", "main"); mainSha == res.Commit {
		t.Error("main moved")
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

	// The session was a fix session with the write policy.
	spec := <-specs
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

// TestDirtyTreeIsRefused: a fix commits everything in the tree, so it may
// not start on top of somebody's uncommitted work.
func TestDirtyTreeIsRefused(t *testing.T) {
	w := newWorkspace(t, "triaged")
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
func TestNoChangesIsAnError(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	deps := newDeps(w, &stubProvider{report: fixReport, t: t}) // no edit
	_, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err == nil {
		t.Fatal("a session that changed nothing was committed")
	}
	if !strings.Contains(err.Error(), "changed no files") {
		t.Errorf("error %v", err)
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

	hook := filepath.Join(w.root, ".git", "hooks", "pre-commit")
	mustWrite(t, hook, "#!/bin/sh\ntouch \"$(git rev-parse --show-toplevel)/hook-ran\"\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}

	deps := newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t})
	res, err := Run(t.Context(), deps, "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Commit == "" || !res.Pushed {
		t.Fatalf("the pre-commit hook stopped the flow: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(w.root, "hook-ran")); err == nil {
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

// TestAcceptDeviationRerunFallsBackWhenTheBranchMoved: the shortcut is only
// for the commit that was reviewed. A branch that has moved on since is a
// different change, so the ordinary flow runs.
func TestAcceptDeviationRerunFallsBackWhenTheBranchMoved(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	report := strings.Replace(fixReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "different file"`, 1)

	first, err := Run(t.Context(), newDeps(w, &stubProvider{report: report, edit: editCSV, t: t}), "OMNI-1", Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Somebody committed on top of the reviewed commit.
	mustWrite(t, filepath.Join(w.root, "export", "extra.go"), "package export\n")
	run(t, w.root, "git", "add", "-A", "--", ".", ":(exclude).sirdar")
	run(t, w.root, "git", "commit", "-q", "--no-verify", "-m", "another change")
	moved := run(t, w.root, "git", "rev-parse", "HEAD")
	if moved == first.Commit {
		t.Fatal("the branch did not move")
	}
	// The first session's scratch file under .sirdar/ is not part of the
	// repository; the ordinary flow refuses a dirty tree, so clear it.
	if err := os.Remove(filepath.Join(w.root, ".sirdar", "runs", "scratch.txt")); err != nil {
		t.Fatal(err)
	}

	second, err := Run(t.Context(), newDeps(w, &stubProvider{report: report, edit: editCSV, t: t}), "OMNI-1",
		Options{AcceptDeviation: true})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if second.RunID == first.RunID {
		t.Error("the rerun reused a run whose branch had moved on")
	}
	if !second.Pushed {
		t.Errorf("the ordinary flow did not complete: %+v", second)
	}
}

// TestDirtyTreeOfOnlySirdarFilesSaysSo: the commonest way to meet "working
// tree not clean" is a workspace whose .sirdar/ is not excluded, and the
// error that just says "commit or stash" sends people to commit their own
// run records.
func TestDirtyTreeOfOnlySirdarFilesSaysSo(t *testing.T) {
	w := newWorkspace(t, "triaged")
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

// writeHook installs an executable hook that leaves a marker and fails.
func writeHook(t *testing.T, path, marker string) {
	t.Helper()
	mustWrite(t, path, "#!/bin/sh\ntouch \"$(git rev-parse --show-toplevel)/"+marker+"\"\nexit 1\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
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
	if _, err := os.Stat(filepath.Join(w.root, "push-hook-ran")); err == nil {
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
	cases := []struct {
		name   string
		path   string
		hooks  string // core.hooksPath to configure, if any
		expect string
	}{
		{name: "a git hook", path: ".git/hooks/pre-commit", expect: ".git/hooks/pre-commit"},
		{name: "the workspace configuration", path: ".sirdar/config.yaml", expect: ".sirdar/config.yaml"},
		{name: "a playbook", path: ".sirdar/playbooks/50-code.md", expect: ".sirdar/playbooks/50-code.md"},
		{name: "the configured hooks path", path: ".githooks/pre-commit", hooks: ".githooks", expect: ".githooks/pre-commit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorkspace(t, "triaged")
			noGH(t)
			if c.hooks != "" {
				run(t, w.root, "git", "config", "core.hooksPath", c.hooks)
			}

			res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editThen(c.path, "#!/bin/sh\nowned\n"), t: t}), "OMNI-1", Options{})
			if err == nil {
				t.Fatalf("Run accepted a session that wrote %s: %+v", c.path, res)
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
	want := filepath.Join(w.root, ".githooks")
	if len(spec.Policy.ExtraReserved) != 1 || spec.Policy.ExtraReserved[0] != want {
		t.Fatalf("the session reserved %v, want [%s]", spec.Policy.ExtraReserved, want)
	}
	in, _ := json.Marshal(map[string]string{"file_path": ".githooks/pre-commit"})
	if d := spec.Policy.Decide("Write", in); d.Allow {
		t.Error("the session's policy allowed a write to the configured hooks path")
	}
}
