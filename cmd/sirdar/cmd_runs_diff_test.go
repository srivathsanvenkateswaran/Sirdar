package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// diffWorkspace is a workspace whose repository holds a finished fix run: a
// branch in a linked worktree with a two-file commit on it, and the
// state.json that names the branch, the base and the commit. No agent and
// no remote are involved — `sirdar runs diff` reads the run record and git.
type diffWorkspace struct {
	root  string
	runID string
	wt    string
}

func newDiffWorkspace(t *testing.T) *diffWorkspace {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root, notes := newWorkspace(t, "fakeclaude.sh")
	_ = notes

	git(t, root, "init", "-b", "main", ".")
	if err := exec.Command("git", "-C", root, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}
	writeFile(t, filepath.Join(root, ".git", "info", "exclude"), ".sirdar/\n")
	writeFile(t, filepath.Join(root, "export", "csv.go"), csvSource("0"))
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")

	runID := store.NewRunID(time.Now())
	rn, err := store.CreateID(root, "OMNI-1", runID)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, ".sirdar", "worktrees", runID)
	git(t, root, "worktree", "add", "-q", "-B", "fix-omni-1-export", wt, "main")
	writeFile(t, filepath.Join(wt, "export", "csv.go"), csvSource("1"))
	writeFile(t, filepath.Join(wt, "export", "stream.go"), "package export\n\nfunc stream() {}\n")
	git(t, wt, "add", "-A")
	git(t, wt, "commit", "-q", "-m", "fix: stream the export")

	state := store.State{
		RunID: runID, Key: "OMNI-1", Kind: store.KindFix, Status: store.StatusCompleted,
		Provider: "claude", StartedAt: time.Now(), UpdatedAt: time.Now(),
	}
	state.Fix.Branch, state.Fix.Base = "fix-omni-1-export", "main"
	state.Fix.Commit = git(t, wt, "rev-parse", "HEAD")
	state.Fix.Worktree = wt
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}
	return &diffWorkspace{root: root, runID: runID, wt: wt}
}

// csvSource has its two edit sites far enough apart to be two hunks.
func csvSource(rows string) string {
	var b strings.Builder
	b.WriteString("package export\n\nfunc rows() int {\n\treturn " + rows + "\n}\n")
	for i := 0; i < 12; i++ {
		b.WriteString("\n// filler line " + strconv.Itoa(i) + "\n")
	}
	b.WriteString("\nfunc header() string {\n\treturn \"id\"\n}\n")
	return b.String()
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunsDiffPrintsThePatch(t *testing.T) {
	w := newDiffWorkspace(t)
	chdir(t, w.root)

	out, _ := mustRun(t, 0, "runs", "diff", w.runID)
	for _, want := range []string{
		"diff --git a/export/csv.go b/export/csv.go",
		"+++ b/export/stream.go",
		"+func stream() {}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("patch has no %q:\n%s", want, out)
		}
	}
}

func TestRunsDiffListsTheFiles(t *testing.T) {
	w := newDiffWorkspace(t)
	chdir(t, w.root)

	out, _ := mustRun(t, 0, "runs", "diff", w.runID, "--files")
	if !strings.Contains(out, "modified") || !strings.Contains(out, "export/csv.go") {
		t.Errorf("file list:\n%s", out)
	}
	if !strings.Contains(out, "added") || !strings.Contains(out, "export/stream.go") {
		t.Errorf("file list:\n%s", out)
	}
}

func TestRunsDiffDropsAHunkAndAmends(t *testing.T) {
	w := newDiffWorkspace(t)
	chdir(t, w.root)

	before := git(t, w.wt, "rev-parse", "HEAD")
	out, errb := mustRun(t, 0, "runs", "diff", w.runID, "--drop", "export/stream.go:0")

	if strings.Contains(out, "export/stream.go") {
		t.Errorf("the dropped file is still in the printed patch:\n%s", out)
	}
	if !strings.Contains(errb, "dropped export/stream.go hunk 0") {
		t.Errorf("stderr does not say what was dropped:\n%s", errb)
	}
	after := git(t, w.wt, "rev-parse", "HEAD")
	if after == before {
		t.Error("the commit was not amended")
	}
	if subject := git(t, w.wt, "log", "-1", "--format=%s"); subject != "fix: stream the export" {
		t.Errorf("the commit message changed: %q", subject)
	}
	if n := git(t, w.wt, "rev-list", "--count", "main..HEAD"); n != "1" {
		t.Errorf("the branch has %s commits; a drop amends rather than adding", n)
	}
	if _, err := os.Stat(filepath.Join(w.wt, "export", "stream.go")); !os.IsNotExist(err) {
		t.Errorf("the dropped file is still on disk: %v", err)
	}
}

func TestRunsDiffRefusesABadDropSpec(t *testing.T) {
	w := newDiffWorkspace(t)
	chdir(t, w.root)

	for _, spec := range []string{"export/csv.go", "export/csv.go:x", "export/csv.go:-1", ":0"} {
		if _, errb := mustRun(t, exitUsage, "runs", "diff", w.runID, "--drop", spec); !strings.Contains(errb, "PATH:N") {
			t.Errorf("%q: stderr does not explain the shape:\n%s", spec, errb)
		}
	}
}

func TestRunsDiffReportsARunWithNothingToShow(t *testing.T) {
	w := newDiffWorkspace(t)
	chdir(t, w.root)

	_, errb := mustRun(t, 1, "runs", "diff", "20260101T000000Z-zzzz")
	if !strings.Contains(errb, "not found") {
		t.Errorf("stderr:\n%s", errb)
	}

	// A triage run is a run, and has no fix diff.
	rn, err := store.Create(w.root, "OMNI-2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	triageID := filepath.Base(rn.Dir)
	if err := rn.WriteState(store.State{
		RunID: triageID, Key: "OMNI-2", Kind: store.KindTriage, Status: store.StatusCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	if _, errb := mustRun(t, 1, "runs", "diff", triageID); !strings.Contains(errb, "no diff") {
		t.Errorf("stderr:\n%s", errb)
	}
}

func TestParseDrop(t *testing.T) {
	for _, tc := range []struct {
		spec string
		path string
		hunk int
		bad  bool
	}{
		{spec: "export/csv.go:0", path: "export/csv.go", hunk: 0},
		{spec: "export/csv.go:12", path: "export/csv.go", hunk: 12},
		{spec: "a:b/c.go:3", path: "a:b/c.go", hunk: 3},
		{spec: "export/csv.go", bad: true},
		{spec: "export/csv.go:", bad: true},
		{spec: "export/csv.go:-1", bad: true},
		{spec: ":2", bad: true},
	} {
		path, hunk, err := parseDrop(tc.spec)
		if tc.bad {
			if err == nil {
				t.Errorf("%q: want an error, got %q:%d", tc.spec, path, hunk)
			}
			continue
		}
		if err != nil || path != tc.path || hunk != tc.hunk {
			t.Errorf("%q: got %q:%d (%v), want %q:%d", tc.spec, path, hunk, err, tc.path, tc.hunk)
		}
	}
}
