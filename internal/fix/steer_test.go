package fix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// steeredReport is the report after a follow-up that widened the change.
const steeredReport = `{
  "summary": "Stream the CSV export and cap the page size",
  "filesChanged": ["export/csv.go", "export/page.go"],
  "testsRun": [{"command":"go test ./export/...","result":"ok"}],
  "risks": "none",
  "deviationFromNote": ""
}`

// editPage is the steered session's change: one more file beside the
// first session's edit, which is already committed on the branch.
func editPage(root string) error {
	return os.WriteFile(filepath.Join(root, "export", "page.go"), []byte("package export\n\nconst pageSize = 500\n"), 0o644)
}

// localFix runs one `--local` fix to completion, which is the run a steer
// starts from: committed, unpushed, worktree kept.
func localFix(t *testing.T, w *workspace) Result {
	t.Helper()
	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t}), "OMNI-1", Options{Local: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Commit == "" || res.Worktree == "" {
		t.Fatalf("the local fix left no commit or no worktree: %+v", res)
	}
	return res
}

// TestSteerKeepsTheWorktreeAndAmendsTheCommit is fix-mode steer: the
// session stands in the run's own worktree on the run's own branch, and
// what it changes goes into the run's commit rather than beside it.
func TestSteerKeepsTheWorktreeAndAmendsTheCommit(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	first := localFix(t, w)

	p := &stubProvider{report: steeredReport, edit: editPage, specs: make(chan provider.SessionSpec, 1), t: t}
	res, err := Steer(t.Context(), newDeps(w, p), first.RunID, "Also cap the page size")
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}

	// Same worktree, same branch, write-enabled and confined to it.
	spec := <-p.specs
	if realPath(spec.Cwd) != realPath(first.Worktree) {
		t.Errorf("the session stood in %s, want the run's worktree %s", spec.Cwd, first.Worktree)
	}
	if spec.Mode != provider.ModeFix || spec.Policy == nil || realPath(spec.Policy.Root) != realPath(first.Worktree) {
		t.Errorf("session mode %q policy root %q", spec.Mode, spec.Policy.Root)
	}
	if spec.Resume != "h1" {
		t.Errorf("resume %q, want the run's handle", spec.Resume)
	}
	if res.Worktree == "" || !isWorktree(res.Worktree) || realPath(res.Worktree) != realPath(first.Worktree) {
		t.Errorf("worktree after the steer: %q", res.Worktree)
	}
	if res.Branch != first.Branch {
		t.Errorf("branch %q, want %q", res.Branch, first.Branch)
	}

	// One commit on the branch, not two, and it is the amended one.
	if !res.Amended || res.Commit == first.Commit {
		t.Fatalf("the commit was not amended: %+v", res)
	}
	if n := run(t, w.root, "git", "rev-list", "--count", "main.."+res.Branch); n != "1" {
		t.Errorf("%s is %s commits ahead of main, want 1", res.Branch, n)
	}
	if head := w.head(t, "refs/heads/"+res.Branch); head != res.Commit {
		t.Errorf("branch head %s, result commit %s", head, res.Commit)
	}
	msg := run(t, w.root, "git", "log", "-1", "--format=%B", res.Commit)
	if !strings.HasPrefix(msg, "fix: Stream the CSV export and cap the page size") || !strings.Contains(msg, "export/page.go") {
		t.Errorf("commit message:\n%s", msg)
	}
	files := run(t, w.root, "git", "show", "--stat", "--format=", res.Commit)
	for _, want := range []string{"export/csv.go", "export/page.go"} {
		if !strings.Contains(files, want) {
			t.Errorf("the amended commit does not carry %s:\n%s", want, files)
		}
	}
	if strings.Contains(files, ".sirdar") {
		t.Errorf("the amended commit swept up .sirdar:\n%s", files)
	}

	// Nothing left the machine.
	if remoteHas(t, w.origin, res.Branch) {
		t.Errorf("%s reached the remote", res.Branch)
	}

	// The run state and the diff follow the new commit.
	_, state, err := store.Open(w.root, first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != store.StatusCompleted || state.Fix.Commit != res.Commit || !state.Fix.Local {
		t.Errorf("run state: status %q fix %+v", state.Status, state.Fix)
	}
	if len(state.Steers) != 1 || state.Steers[0].Text != "Also cap the page size" || state.Steers[0].Continuation != "resume" {
		t.Errorf("steers: %+v", state.Steers)
	}
	if state.Usage.Turns != 8 {
		t.Errorf("usage did not accumulate over the two sessions: %+v", state.Usage)
	}
	diff, err := os.ReadFile(res.DiffPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(diff), "pageSize = 500") || !strings.Contains(string(diff), "stream rather than buffer") {
		t.Errorf("fix.diff was not rewritten for the amended commit:\n%s", diff)
	}
	rows, err := store.ReadRegister(w.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1].RunID != first.RunID || rows[1].Kind != "fix" {
		t.Errorf("register rows: %+v", rows)
	}
}

// TestSteerUnchangedTreeLeavesTheCommit: a session that edits nothing —
// it re-ran the tests, or answered a question — leaves the commit as it
// was and nothing to amend.
func TestSteerUnchangedTreeLeavesTheCommit(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	first := localFix(t, w)

	res, err := Steer(t.Context(), newDeps(w, &stubProvider{report: fixReport, t: t}), first.RunID, "Run the tests again")
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if res.Amended || res.Commit != first.Commit {
		t.Fatalf("an unchanged tree moved the commit: %+v", res)
	}
	if head := w.head(t, "refs/heads/"+res.Branch); head != first.Commit {
		t.Errorf("branch head %s, want %s", head, first.Commit)
	}
	rows, err := store.ReadRegister(w.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("register rows: %d, want the first run's alone", len(rows))
	}
}

// TestSteerReevaluatesTheDeviation: the new report's deviation replaces
// the old one on the run, in both directions.
func TestSteerReevaluatesTheDeviation(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	first := localFix(t, w)

	deviating := strings.Replace(steeredReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "The cap lives in the pager, not the exporter"`, 1)
	res, err := Steer(t.Context(), newDeps(w, &stubProvider{report: deviating, edit: editPage, t: t}), first.RunID, "Cap the page size")
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if res.Blocked == "" || res.State.Fix.Deviation != res.Blocked {
		t.Fatalf("the deviation was not recorded: %+v", res)
	}
	if remoteHas(t, w.origin, res.Branch) {
		t.Errorf("a steer pushed %s", res.Branch)
	}

	res, err = Steer(t.Context(), newDeps(w, &stubProvider{report: steeredReport, t: t}), first.RunID, "Fine, keep it there")
	if err != nil {
		t.Fatalf("second Steer: %v", err)
	}
	if res.Blocked != "" || res.State.Fix.Deviation != "" {
		t.Fatalf("the deviation was not cleared: %+v", res)
	}
}

// TestSteerRefusals: a pushed run, a run whose worktree is gone, a run
// whose branch has moved, and a provider that cannot run a fix. Each is
// refused before a session starts and before git is changed.
func TestSteerRefusals(t *testing.T) {
	noGH(t)

	t.Run("pushed", func(t *testing.T) {
		w := newWorkspace(t, "triaged")
		res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t}), "OMNI-1", Options{NoPR: true})
		if err != nil || !res.Pushed {
			t.Fatalf("Run: %v %+v", err, res)
		}
		_, err = Steer(t.Context(), newDeps(w, &stubProvider{report: fixReport, t: t}), res.RunID, "More")
		if err == nil || !strings.Contains(err.Error(), "pushed") {
			t.Fatalf("Steer on a pushed run: %v", err)
		}
	})

	t.Run("worktree gone", func(t *testing.T) {
		w := newWorkspace(t, "triaged")
		first := localFix(t, w)
		run(t, w.root, "git", "worktree", "remove", "--force", first.Worktree)
		p := &stubProvider{report: fixReport, specs: make(chan provider.SessionSpec, 1), t: t}
		_, err := Steer(t.Context(), newDeps(w, p), first.RunID, "More")
		if err == nil || !strings.Contains(err.Error(), "is gone") {
			t.Fatalf("Steer without the worktree: %v", err)
		}
		if len(p.specs) != 0 {
			t.Fatal("a session was started")
		}
		if head := w.head(t, "refs/heads/"+first.Branch); head != first.Commit {
			t.Errorf("the branch moved: %s", head)
		}
	})

	t.Run("branch moved", func(t *testing.T) {
		w := newWorkspace(t, "triaged")
		first := localFix(t, w)
		mustWrite(t, filepath.Join(first.Worktree, "export", "extra.go"), "package export\n")
		run(t, first.Worktree, "git", "add", "-A", "--", ".", ":(exclude).sirdar")
		run(t, first.Worktree, "git", "commit", "-q", "--no-verify", "-m", "somebody else's commit")
		_, err := Steer(t.Context(), newDeps(w, &stubProvider{report: fixReport, t: t}), first.RunID, "More")
		if err == nil || !strings.Contains(err.Error(), "not at") {
			t.Fatalf("Steer on a moved branch: %v", err)
		}
	})

	t.Run("provider cannot fix", func(t *testing.T) {
		w := newWorkspace(t, "triaged")
		first := localFix(t, w)
		_, err := Steer(t.Context(), newDeps(w, &noFixProvider{t: t}), first.RunID, "More")
		if err == nil || !strings.Contains(err.Error(), errNoFix.Error()) {
			t.Fatalf("Steer on a provider that cannot fix: %v", err)
		}
	})

	t.Run("not a fix run", func(t *testing.T) {
		w := newWorkspace(t, "triaged")
		_, err := Steer(t.Context(), newDeps(w, &stubProvider{report: fixReport, t: t}), w.runID, "More")
		if err == nil || !strings.Contains(err.Error(), "not a fix") {
			t.Fatalf("Steer on the triage run: %v", err)
		}
	})
}

// TestSteerGuardFailsTheRun: the snapshot guard covers a steered session
// the same as a first one. A hook written during it fails the run, and
// the commit is left where it was.
func TestSteerGuardFailsTheRun(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	first := localFix(t, w)

	hook := filepath.Join(w.root, ".git", "hooks", "pre-commit")
	p := &stubProvider{report: steeredReport, edit: editThen(hook, "#!/bin/sh\nexit 0\n"), t: t}
	res, err := Steer(t.Context(), newDeps(w, p), first.RunID, "More")
	if err == nil {
		t.Fatal("a session that wrote a hook was accepted")
	}
	if res.State.Status != store.StatusFailed {
		t.Errorf("state %q, want failed", res.State.Status)
	}
	if head := w.head(t, "refs/heads/"+first.Branch); head != first.Commit {
		t.Errorf("the commit was amended after a guard failure: %s", head)
	}
	_, state, err := store.Open(w.root, first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != store.StatusFailed || state.Fix.Commit != first.Commit {
		t.Errorf("run state: %q %+v", state.Status, state.Fix)
	}
}
