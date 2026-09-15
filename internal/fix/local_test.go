package fix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// TestLocalCommitsAndWritesTheDiffAndPushesNothing is what `--local` is
// for: the fix is produced, read and kept, and nothing about it reaches
// anybody's remote. A retrospective evaluation runs hundreds of these
// against historical commits, and a single accidental push would be a
// branch on a real repository for a ticket that was closed a year ago.
func TestLocalCommitsAndWritesTheDiffAndPushesNothing(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)

	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t}), "OMNI-1", Options{Local: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Commit == "" {
		t.Fatalf("no commit was made: %+v", res)
	}
	if res.Pushed || res.PRURL != "" {
		t.Errorf("a local run pushed or opened a pull request: %+v", res)
	}
	if !res.Local {
		t.Errorf("the result does not say it was local: %+v", res)
	}

	// Nothing reached the remote: no branch, and main is where it was.
	if remoteHas(t, w.origin, res.Branch) {
		t.Errorf("%s reached the remote", res.Branch)
	}
	if out := run(t, w.origin, "git", "branch", "--list"); strings.Contains(out, "fix-omni-1") {
		t.Errorf("the remote has a fix branch: %q", out)
	}

	// The commit's diff is in the run directory, and it is the diff of the
	// change the session made.
	if res.DiffPath == "" {
		t.Fatalf("no diff path: %+v", res)
	}
	if want := filepath.Join(w.root, ".sirdar", "runs", "OMNI-1", res.RunID, "fix.diff"); res.DiffPath != want {
		t.Errorf("diff path %q, want %q", res.DiffPath, want)
	}
	diff, err := os.ReadFile(res.DiffPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"export/csv.go", "+// stream rather than buffer"} {
		if !strings.Contains(string(diff), want) {
			t.Errorf("fix.diff does not carry %q:\n%s", want, diff)
		}
	}
	// It is a diff, not a commit listing: a reader pipes it into `git
	// apply`, and a commit header would break that.
	if strings.Contains(string(diff), "commit "+res.Commit) {
		t.Errorf("fix.diff carries the commit header:\n%s", diff)
	}

	// The worktree is kept: the commit and the diff in it are the output.
	if res.Worktree == "" || !isWorktree(res.Worktree) {
		t.Errorf("the worktree was removed: %q", res.Worktree)
	}

	// The run state says the same three things, for a screen that reads
	// the run directory rather than this result.
	_, state, err := store.Open(w.root, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Fix.Local || state.Fix.Commit != res.Commit || state.Fix.DiffPath != res.DiffPath {
		t.Errorf("run state Fix: %+v", state.Fix)
	}
	if state.Fix.Pushed {
		t.Error("the run state says the branch was pushed")
	}

	// The triage note is untouched. fix-pushed would be a lie: nothing was.
	for _, path := range []string{w.runNote, w.filedNote} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "status: triaged") {
			t.Errorf("%s was moved off triaged by a local run:\n%s", path, body)
		}
	}
}

// TestLocalStillRefusesAReservedPath: --local pushes nothing, which is not
// a reason to relax the layer that stops a session writing to the files
// that judge it. The snapshot guard runs before the commit either way.
func TestLocalStillRefusesAReservedPath(t *testing.T) {
	for _, c := range []struct {
		name, expect string
		path         func(w *workspace, root string) string
	}{
		{
			name:   "the workspace configuration",
			expect: ".sirdar/config.yaml",
			path:   func(w *workspace, _ string) string { return filepath.Join(w.root, ".sirdar", "config.yaml") },
		},
		{
			name:   "a git hook in the shared git directory",
			expect: ".git/hooks/pre-commit",
			path:   func(w *workspace, _ string) string { return filepath.Join(w.root, ".git", "hooks", "pre-commit") },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorkspace(t, "triaged")
			noGH(t)
			edit := func(root string) error {
				return editThen(c.path(w, root), "#!/bin/sh\nowned\n")(root)
			}

			res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: edit, t: t}), "OMNI-1", Options{Local: true})
			if err == nil {
				t.Fatalf("Run accepted a local session that wrote %s: %+v", c.expect, res)
			}
			if !strings.Contains(err.Error(), c.expect) {
				t.Errorf("the refusal does not name %q:\n%v", c.expect, err)
			}
			if res.Commit != "" || res.DiffPath != "" {
				t.Errorf("the run committed or wrote a diff anyway: %+v", res)
			}
			if head := run(t, w.root, "git", "log", "-1", "--pretty=%s"); head != "init" {
				t.Errorf("a commit was made: %q", head)
			}
		})
	}
}

// TestAtCutsTheBranchFromTheGivenCommit: the branch's parent is the commit
// --at named, not the tip of origin's default branch, so the session works
// on the code as it stood rather than on today's.
func TestAtCutsTheBranchFromTheGivenCommit(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	old := run(t, w.root, "git", "rev-parse", "HEAD")

	// Main moves on, the way it does between a ticket being filed and
	// somebody getting round to it.
	mustWrite(t, filepath.Join(w.root, "export", "later.go"), "package export\n\n// added after the ticket\n")
	run(t, w.root, "git", "add", "-A")
	run(t, w.root, "git", "commit", "-q", "-m", "later work")
	run(t, w.root, "git", "push", "-q", "origin", "main")
	tip := run(t, w.root, "git", "rev-parse", "HEAD")

	var sawLater bool
	edit := func(root string) error {
		_, err := os.Stat(filepath.Join(root, "export", "later.go"))
		sawLater = err == nil
		return editCSV(root)
	}

	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: edit, t: t}), "OMNI-1",
		Options{Local: true, At: old})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.At != old {
		t.Errorf("result At = %q, want the resolved commit %q", res.At, old)
	}
	if parent := run(t, w.root, "git", "rev-parse", res.Branch+"^"); parent != old {
		t.Errorf("%s was cut from %s, want %s (the tip is %s)", res.Branch, parent, old, tip)
	}
	if sawLater {
		t.Error("the session saw a file that was only added after the commit it was meant to stand at")
	}

	_, state, err := store.Open(w.root, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.At != old {
		t.Errorf("run state At = %q, want %q", state.At, old)
	}
}

// TestAtRefusesACommitThatIsNotThere: the refusal happens in the preflight,
// before a worktree is made or an agent process exists.
func TestAtRefusesACommitThatIsNotThere(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)

	res, err := Run(t.Context(), newDeps(w, &stubProvider{report: fixReport, edit: editCSV, t: t}), "OMNI-1",
		Options{Local: true, At: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"})
	if err == nil {
		t.Fatalf("Run accepted a commit that does not exist: %+v", res)
	}
	if !strings.Contains(err.Error(), "names no commit") {
		t.Errorf("the refusal does not say the commit is unknown:\n%v", err)
	}
	if res.RunID != "" {
		t.Errorf("a run directory was created: %+v", res)
	}
}

// TestAcceptDeviationStaysLocal: --local means the same thing on a rerun as
// it did on the first run. Accepting a deviation is accepting the diff; it
// is not authorising a push nobody asked for.
func TestAcceptDeviationStaysLocal(t *testing.T) {
	w := newWorkspace(t, "triaged")
	noGH(t)
	deviating := strings.Replace(fixReport, `"deviationFromNote": ""`,
		`"deviationFromNote": "the buffering was in the writer, not the handler"`, 1)

	first, err := Run(t.Context(), newDeps(w, &stubProvider{report: deviating, edit: editCSV, t: t}), "OMNI-1", Options{Local: true})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Blocked == "" || first.Commit == "" || first.DiffPath == "" {
		t.Fatalf("the first run did not block on the deviation with a commit and a diff: %+v", first)
	}

	// A second session must not be started: the human read this commit.
	second, err := Run(t.Context(), newDeps(w, &refusingProvider{t: t}), "OMNI-1", Options{Local: true, AcceptDeviation: true})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if second.Commit != first.Commit {
		t.Errorf("the rerun published %s, want the reviewed commit %s", second.Commit, first.Commit)
	}
	if second.Pushed || second.PRURL != "" {
		t.Errorf("--accept-deviation --local pushed: %+v", second)
	}
	if remoteHas(t, w.origin, first.Branch) {
		t.Errorf("%s reached the remote", first.Branch)
	}
	if second.DiffPath != first.DiffPath {
		t.Errorf("diff path %q, want the one the first run wrote, %q", second.DiffPath, first.DiffPath)
	}
	if second.Worktree == "" || !isWorktree(second.Worktree) {
		t.Errorf("the worktree was removed: %q", second.Worktree)
	}

	_, state, err := store.Open(w.root, first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Fix.Local || state.Fix.Pushed || state.Fix.Deviation != "" {
		t.Errorf("run state Fix after the accepted rerun: %+v", state.Fix)
	}
}
