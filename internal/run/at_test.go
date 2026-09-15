package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/worktree"
)

// --- a workspace with a history ---------------------------------------

// atRepo is a real git repository with two commits, whose workspace files
// differ between them: the source file the session would read, the model in
// .sirdar/config.yaml, and the playbook text. That is what makes an --at run
// falsifiable — a run standing at the older commit must see the older
// source and the *newer* configuration, because the configuration is the
// operator's and is read from the main tree either way.
type atRepo struct {
	cfg   *config.Config
	root  string
	notes string
	old   string // the first commit
	tip   string // what main points at
}

func atConfig(model, notesDir string) string {
	return `workspace: test
provider: claude
model: ` + model + `
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

const (
	oldSource = "package export\n\nconst version = \"v1-historical\"\n"
	tipSource = "package export\n\nconst version = \"v2-today\"\n"
)

func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newAtRepo(t *testing.T) *atRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	// The notes directory sits outside the repository, the way a vault
	// does: a note filed into the tree would show up as an uncommitted
	// entry and muddle what this test is actually asserting.
	notes := t.TempDir()
	gitAt(t, root, "init", "-b", "main", ".")
	// The commits are throwaway, but they still need an identity to be
	// made at all. The machine's own is used as it stands; a checkout with
	// none skips rather than inventing one.
	if err := exec.Command("git", "-C", root, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}
	// The run records are not part of the repository — the same exclusion
	// `sirdar init` writes. .git/info/exclude lives in the common git
	// directory, so it covers every linked worktree too.
	writeAt(t, filepath.Join(root, ".git", "info", "exclude"),
		".sirdar/runs/\n.sirdar/register.jsonl\n.sirdar/eval/\n.sirdar/worktrees/\n")

	writeAt(t, filepath.Join(root, ".sirdar", "config.yaml"), atConfig("old-model", notes))
	writeAt(t, filepath.Join(root, ".sirdar", "playbooks", "00-test.md"), "# Exports\n\nthe old playbook.\n")
	writeAt(t, filepath.Join(root, "export", "csv.go"), oldSource)
	gitAt(t, root, "add", "-A")
	gitAt(t, root, "commit", "-q", "-m", "one")
	old := gitAt(t, root, "rev-parse", "HEAD")

	writeAt(t, filepath.Join(root, ".sirdar", "config.yaml"), atConfig("new-model", notes))
	writeAt(t, filepath.Join(root, ".sirdar", "playbooks", "00-test.md"), "# Exports\n\n"+playbookMarker+"\n")
	writeAt(t, filepath.Join(root, "export", "csv.go"), tipSource)
	gitAt(t, root, "add", "-A")
	gitAt(t, root, "commit", "-q", "-m", "two")
	tip := gitAt(t, root, "rev-parse", "HEAD")

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return &atRepo{cfg: cfg, root: root, notes: notes, old: old, tip: tip}
}

// worktrees lists the linked worktrees the repository still has, main tree
// excluded.
func (r *atRepo) worktrees(t *testing.T) []string {
	t.Helper()
	var paths []string
	main := worktree.RealPath(r.root)
	for _, line := range strings.Split(gitAt(t, r.root, "worktree", "list", "--porcelain"), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			if real := worktree.RealPath(strings.TrimSpace(p)); real != main {
				paths = append(paths, real)
			}
		}
	}
	return paths
}

// --- tests ------------------------------------------------------------

// TestTriageAtRunsInAWorktreeAtThatCommit is the whole point of --at: the
// session stands in a checkout of the named commit, sees that commit's
// code, and the tree the operator is standing in is not touched — not its
// HEAD, not its index, not the file the older commit disagrees about.
//
// The configuration is the other half. It is read from the main tree
// whichever commit the session stands at, because it is what the operator
// configured rather than what the repository happened to contain a year
// ago: the run's model is the main tree's, and the worktree's own copy of
// .sirdar/config.yaml — the historical one — is ignored.
func TestTriageAtRunsInAWorktreeAtThatCommit(t *testing.T) {
	repo := newAtRepo(t)
	headBefore := gitAt(t, repo.root, "rev-parse", "HEAD")
	indexBefore := gitAt(t, repo.root, "status", "--porcelain")

	var sawCwd, sawSource, sawWorktreeConfig string
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		sawCwd = spec.Cwd
		if data, err := os.ReadFile(filepath.Join(spec.Cwd, "export", "csv.go")); err == nil {
			sawSource = string(data)
		}
		if data, err := os.ReadFile(filepath.Join(spec.Cwd, ".sirdar", "config.yaml")); err == nil {
			sawWorktreeConfig = string(data)
		}
		replay(finalEvent(triageDoc))(spec, s)
	}}
	r := newRunner(repo.cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(t.Context(), []string{"OMNI-1"}, Options{At: repo.old})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}

	// The session stood in this run's own worktree, at the old commit.
	want := worktree.RealPath(worktree.Path(repo.root, out.State.RunID))
	if worktree.RealPath(sawCwd) != want {
		t.Errorf("the session ran in %q, want the worktree %q", sawCwd, want)
	}
	if sawSource != oldSource {
		t.Errorf("the session read %q, want the code as it was at %s", sawSource, repo.old[:8])
	}

	// Its writes were confined to the same directory, and it was still a
	// read-only triage: --at changes where a session stands, nothing about
	// what it may do.
	spec := p.spec(0)
	if worktree.RealPath(spec.Policy.Root) != want {
		t.Errorf("the policy root is %q, want the worktree %q", spec.Policy.Root, want)
	}
	if spec.Mode != provider.ModeTriage {
		t.Errorf("session mode %v, want the read-only triage mode", spec.Mode)
	}

	// The configuration came from the main tree, not from the checkout:
	// the worktree's own copy says old-model and the run used new-model.
	if !strings.Contains(sawWorktreeConfig, "model: old-model") {
		t.Fatalf("the worktree is not at the old commit; its config.yaml:\n%s", sawWorktreeConfig)
	}
	if out.State.Model != "new-model" {
		t.Errorf("the run used model %q, want the main tree's new-model", out.State.Model)
	}
	// So did the playbooks, which are read from the same .sirdar/.
	prompt := readFile(t, filepath.Join(runDir(t, repo.cfg, out), "prompt.md"))
	if !strings.Contains(prompt, playbookMarker) {
		t.Errorf("the prompt has the historical playbook rather than the main tree's:\n%s", prompt)
	}

	// The run says what it stood at, and so does the note a human reads.
	if out.State.At != repo.old {
		t.Errorf("state.At = %q, want %q", out.State.At, repo.old)
	}
	noteBody := readFile(t, filepath.Join(repo.notes, "OMNI-1 export-fails-for-large-orders.md"))
	if !strings.Contains(noteBody, "at: "+`"`+repo.old+`"`) {
		t.Errorf("the note frontmatter does not record the commit:\n%s", noteBody)
	}

	// The operator's tree is exactly as it was, and the worktree is gone.
	if head := gitAt(t, repo.root, "rev-parse", "HEAD"); head != headBefore {
		t.Errorf("the main tree's HEAD moved: %s -> %s", headBefore, head)
	}
	if index := gitAt(t, repo.root, "status", "--porcelain"); index != indexBefore {
		t.Errorf("the main tree's index changed:\n%s", index)
	}
	if body := readFile(t, filepath.Join(repo.root, "export", "csv.go")); body != tipSource {
		t.Errorf("the main tree's working file changed:\n%s", body)
	}
	if wts := repo.worktrees(t); len(wts) != 0 {
		t.Errorf("the worktree outlived the run: %v", wts)
	}
}

// TestKeepWorktreeLeavesTheCheckoutOnDisk: the removal is the default
// because a directory per run adds up, but an operator who wants to look at
// what the session was looking at asks for it and gets it.
func TestKeepWorktreeLeavesTheCheckoutOnDisk(t *testing.T) {
	repo := newAtRepo(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(repo.cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(t.Context(), []string{"OMNI-1"}, Options{At: repo.old, KeepWorktree: true})
	if err != nil {
		t.Fatal(err)
	}
	path := worktree.Path(repo.root, outs[0].State.RunID)
	if !worktree.IsWorktree(path) {
		t.Fatalf("--keep-worktree removed %s anyway", path)
	}
	if body := readFile(t, filepath.Join(path, "export", "csv.go")); body != oldSource {
		t.Errorf("the kept worktree is not at the old commit:\n%s", body)
	}
}

// TestRCAAtRunsAtThatCommit: an rca reviews a triage note, and a
// retrospective rca has to read the same historical code the triage did.
func TestRCAAtRunsAtThatCommit(t *testing.T) {
	repo := newAtRepo(t)
	triage := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(repo.cfg, triage, stubTracker{}, stubHelpdesk{})
	if _, err := r.Triage(t.Context(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}

	var sawSource string
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		if data, err := os.ReadFile(filepath.Join(spec.Cwd, "export", "csv.go")); err == nil {
			sawSource = string(data)
		}
		replay(finalEvent(rcaDoc))(spec, s)
	}}
	r2 := newRunner(repo.cfg, p, stubTracker{}, stubHelpdesk{})

	out, err := r2.RCA(t.Context(), "OMNI-1", RCAOptions{Options: Options{At: repo.old}})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if sawSource != oldSource {
		t.Errorf("the rca session read %q, want the code as it was at %s", sawSource, repo.old[:8])
	}
	if out.State.At != repo.old {
		t.Errorf("state.At = %q, want %q", out.State.At, repo.old)
	}
	if wts := repo.worktrees(t); len(wts) != 0 {
		t.Errorf("the worktree outlived the run: %v", wts)
	}
}

// TestAtRefusesACommitThatIsNotThere: the refusal happens in preparation,
// before a ticket is fetched or an agent process exists, and says which of
// the two things went wrong.
func TestAtRefusesACommitThatIsNotThere(t *testing.T) {
	repo := newAtRepo(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(repo.cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(t.Context(), []string{"OMNI-1"}, Options{At: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q, want failed", out.State.Status)
	}
	if !strings.Contains(out.State.Reason, "names no commit") {
		t.Errorf("reason %q does not say the commit is unknown", out.State.Reason)
	}
	if p.startCount() != 0 {
		t.Errorf("an agent session was started for a commit that does not exist")
	}
	if wts := repo.worktrees(t); len(wts) != 0 {
		t.Errorf("a worktree was left behind: %v", wts)
	}
}

// TestAtInADryRunTakesItsWorktreeAwayAgain: a dry run writes a bundle and
// a prompt and starts nothing, so there is nothing in the checkout to read
// and no reason to leave it there.
func TestAtInADryRunTakesItsWorktreeAwayAgain(t *testing.T) {
	repo := newAtRepo(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(repo.cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(t.Context(), []string{"OMNI-1"}, Options{At: repo.old, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.At != repo.old {
		t.Errorf("state.At = %q, want %q", outs[0].State.At, repo.old)
	}
	if wts := repo.worktrees(t); len(wts) != 0 {
		t.Errorf("a dry run left its worktree behind: %v", wts)
	}
}
