package fix

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// fixRun is a finished fix run standing in a linked worktree: a branch cut
// from main, a commit touching two files, and the state.json that names all
// of it. It is what a review reads, and nothing in it needs an agent.
type fixRun struct {
	*workspace
	fixRunID string
	branch   string
	worktree string
	commit   string
}

// csvFile is the file the fix edits, with enough untouched lines between
// its two edits that git reports them as two hunks rather than one.
func csvFile(rows, header string) string {
	var b strings.Builder
	b.WriteString("package export\n\nfunc rows() int {\n\treturn " + rows + "\n}\n")
	for i := 0; i < 12; i++ {
		b.WriteString("\n// filler line " + strconv.Itoa(i) + "\n")
	}
	b.WriteString("\nfunc header() string {\n\treturn \"" + header + "\"\n}\n")
	return b.String()
}

// newFixRun seeds the repository with a fix branch in a worktree of its
// own, carrying a two-file change: export/csv.go with two separated hunks,
// and a new file export/stream.go.
func newFixRun(t *testing.T) *fixRun {
	t.Helper()
	w := newWorkspace(t, "triaged")

	// Give csv.go two hunks' worth of distance between its two edits.
	mustWrite(t, filepath.Join(w.root, "export", "csv.go"), csvFile("0", "id"))
	run(t, w.root, "git", "add", "-A")
	run(t, w.root, "git", "commit", "-q", "-m", "csv")
	run(t, w.root, "git", "push", "-q", "origin", "main")

	runID := store.NewRunID(time.Now())
	rn, err := store.CreateID(w.root, "OMNI-1", runID)
	if err != nil {
		t.Fatal(err)
	}
	branch := "fix-omni-1-export"
	wt := worktreePath(w.root, runID)
	run(t, w.root, "git", "worktree", "add", "-q", "-B", branch, wt, "origin/main")

	mustWrite(t, filepath.Join(wt, "export", "csv.go"), csvFile("1", "id,name"))
	mustWrite(t, filepath.Join(wt, "export", "stream.go"), "package export\n\nfunc stream() {}\n")
	run(t, wt, "git", "add", "-A")
	run(t, wt, "git", "commit", "-q", "-m", "fix: stream the export")
	commit := run(t, wt, "git", "rev-parse", "HEAD")

	state := store.State{
		RunID: runID, Key: "OMNI-1", Kind: store.KindFix, Status: store.StatusCompleted,
		Provider: "claude", StartedAt: time.Now(), UpdatedAt: time.Now(),
	}
	state.Fix.Branch, state.Fix.Base, state.Fix.Commit, state.Fix.Worktree = branch, "main", commit, wt
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}
	return &fixRun{workspace: w, fixRunID: runID, branch: branch, worktree: wt, commit: commit}
}

func (f *fixRun) state(t *testing.T) store.State {
	t.Helper()
	_, st, err := store.Open(f.root, f.fixRunID)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (f *fixRun) diff(t *testing.T) Diff {
	t.Helper()
	d, err := ReviewDiff(context.Background(), f.root, f.state(t))
	if err != nil {
		t.Fatalf("ReviewDiff: %v", err)
	}
	return d
}

func TestReviewDiffReadsTheWorktree(t *testing.T) {
	f := newFixRun(t)
	d := f.diff(t)

	if !d.WorktreePresent || d.Worktree != f.worktree {
		t.Errorf("worktree %q present=%v", d.Worktree, d.WorktreePresent)
	}
	if d.Branch != f.branch {
		t.Errorf("branch %q", d.Branch)
	}
	if d.Head != f.commit {
		t.Errorf("head %q want %q", d.Head, f.commit)
	}
	if d.Base != f.head(t, "origin/main") {
		t.Errorf("base %q want %q", d.Base, f.head(t, "origin/main"))
	}
	if d.Pushed {
		t.Error("nothing was pushed")
	}
	if d.Truncated {
		t.Error("a small patch is not truncated")
	}
	if d.ETag == "" {
		t.Error("no etag")
	}

	want := []FileChange{
		{Path: "export/csv.go", Status: "modified", Additions: 2, Deletions: 2},
		{Path: "export/stream.go", Status: "added", Additions: 3, Deletions: 0},
	}
	if len(d.Files) != len(want) {
		t.Fatalf("files %+v", d.Files)
	}
	for i, w := range want {
		if d.Files[i] != w {
			t.Errorf("file %d: got %+v want %+v", i, d.Files[i], w)
		}
	}
	if !strings.Contains(d.Patch, "diff --git a/export/csv.go b/export/csv.go") {
		t.Errorf("patch:\n%s", d.Patch)
	}
	if !strings.Contains(d.Patch, "+++ b/export/stream.go") {
		t.Errorf("patch:\n%s", d.Patch)
	}
}

func TestReviewDiffFallsBackToTheRepositoryWhenTheWorktreeIsGone(t *testing.T) {
	f := newFixRun(t)
	run(t, f.root, "git", "worktree", "remove", "--force", f.worktree)

	d := f.diff(t)
	if d.WorktreePresent {
		t.Error("the worktree is gone")
	}
	if d.Head != f.commit {
		t.Errorf("head %q want %q", d.Head, f.commit)
	}
	if len(d.Files) != 2 {
		t.Fatalf("files %+v", d.Files)
	}
}

func TestReviewDiffRefusesARunWithNothingToShow(t *testing.T) {
	f := newFixRun(t)
	run(t, f.root, "git", "worktree", "remove", "--force", f.worktree)

	st := f.state(t)
	st.Fix.Worktree, st.Fix.Commit = "", ""
	if _, err := ReviewDiff(context.Background(), f.root, st); !errors.Is(err, ErrNoDiff) {
		t.Fatalf("err %v", err)
	}

	st = f.state(t)
	st.Kind = store.KindTriage
	if _, err := ReviewDiff(context.Background(), f.root, st); !errors.Is(err, ErrNoDiff) {
		t.Fatalf("a triage run has no fix diff: %v", err)
	}
}

func TestReviewDiffRecordsDeletesAndRenames(t *testing.T) {
	f := newFixRun(t)
	run(t, f.worktree, "git", "rm", "-q", "export/stream.go")
	run(t, f.worktree, "git", "mv", "export/csv.go", "export/rows.go")
	run(t, f.worktree, "git", "commit", "-q", "--amend", "--no-edit")

	d := f.diff(t)
	got := map[string]string{}
	for _, file := range d.Files {
		got[file.Path] = file.Status
	}
	if got["export/rows.go"] != "renamed" {
		t.Errorf("files %+v", d.Files)
	}
	if _, ok := got["export/stream.go"]; ok {
		t.Errorf("a file added and removed in the same commit is not in the diff: %+v", d.Files)
	}
}

func TestDropRevertsOneHunkAndAmends(t *testing.T) {
	f := newFixRun(t)
	before := f.diff(t)

	// csv.go's two edits are far enough apart to be two hunks.
	hunks := hunksFor(t, before.Patch, "export/csv.go")
	if len(hunks) != 2 {
		t.Fatalf("csv.go hunks: %d\n%s", len(hunks), before.Patch)
	}

	after, err := Drop(context.Background(), f.root, f.state(t), "export/csv.go", 1, before.ETag)
	if err != nil {
		t.Fatalf("Drop: %v", err)
	}

	if after.Head == before.Head {
		t.Error("the commit was not amended")
	}
	if subject := run(t, f.worktree, "git", "log", "-1", "--format=%s"); subject != "fix: stream the export" {
		t.Errorf("the commit message changed: %q", subject)
	}
	if n := run(t, f.worktree, "git", "rev-list", "--count", "origin/main..HEAD"); n != "1" {
		t.Errorf("the branch has %s commits; the drop must amend, not add", n)
	}
	if branch := run(t, f.worktree, "git", "rev-parse", "--abbrev-ref", "HEAD"); branch != f.branch {
		t.Errorf("branch %q", branch)
	}

	// The second edit is gone and the first is still there.
	body := readFile(t, filepath.Join(f.worktree, "export", "csv.go"))
	if strings.Contains(body, "id,name") {
		t.Errorf("the dropped hunk is still applied:\n%s", body)
	}
	if !strings.Contains(body, "return 1") {
		t.Errorf("the kept hunk was reverted too:\n%s", body)
	}

	if len(hunksFor(t, after.Patch, "export/csv.go")) != 1 {
		t.Errorf("the returned diff still has two hunks:\n%s", after.Patch)
	}
	for _, file := range after.Files {
		if file.Path == "export/csv.go" && (file.Additions != 1 || file.Deletions != 1) {
			t.Errorf("csv.go counts %+v", file)
		}
	}
	if after.ETag == before.ETag {
		t.Error("the etag did not move with the patch")
	}

	// The run state names the amended commit, and the run log says why.
	if st := f.state(t); st.Fix.Commit != after.Head {
		t.Errorf("state commit %q want %q", st.Fix.Commit, after.Head)
	}
	assertReviewEvent(t, filepath.Join(f.root, ".sirdar", "runs", "OMNI-1", f.fixRunID, "events.jsonl"),
		"export/csv.go", 1)
}

func TestDropTheLastHunkOfAFileRemovesItFromTheDiff(t *testing.T) {
	f := newFixRun(t)
	before := f.diff(t)

	after, err := Drop(context.Background(), f.root, f.state(t), "export/stream.go", 0, before.ETag)
	if err != nil {
		t.Fatalf("Drop: %v", err)
	}
	for _, file := range after.Files {
		if file.Path == "export/stream.go" {
			t.Fatalf("the added file is still in the diff: %+v", after.Files)
		}
	}
	if _, err := os.Stat(filepath.Join(f.worktree, "export", "stream.go")); !os.IsNotExist(err) {
		t.Errorf("the added file is still on disk: %v", err)
	}
}

func TestDropIsRefused(t *testing.T) {
	ctx := context.Background()

	t.Run("live run", func(t *testing.T) {
		f := newFixRun(t)
		etag := f.diff(t).ETag
		st := f.state(t)
		st.Status = store.StatusRunning
		if _, err := Drop(ctx, f.root, st, "export/csv.go", 0, etag); !errors.Is(err, ErrDropRefused) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("pushed branch", func(t *testing.T) {
		f := newFixRun(t)
		etag := f.diff(t).ETag
		st := f.state(t)
		st.Fix.Pushed = true
		if _, err := Drop(ctx, f.root, st, "export/csv.go", 0, etag); !errors.Is(err, ErrDropRefused) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("worktree gone", func(t *testing.T) {
		f := newFixRun(t)
		etag := f.diff(t).ETag
		run(t, f.root, "git", "worktree", "remove", "--force", f.worktree)
		if _, err := Drop(ctx, f.root, f.state(t), "export/csv.go", 0, etag); !errors.Is(err, ErrDropRefused) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("stale etag", func(t *testing.T) {
		f := newFixRun(t)
		if _, err := Drop(ctx, f.root, f.state(t), "export/csv.go", 0, "not-the-patch"); !errors.Is(err, ErrDropRefused) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("hunk index out of range", func(t *testing.T) {
		f := newFixRun(t)
		etag := f.diff(t).ETag
		if _, err := Drop(ctx, f.root, f.state(t), "export/csv.go", 7, etag); !errors.Is(err, ErrDropRefused) {
			t.Fatalf("err %v", err)
		}
		if _, err := Drop(ctx, f.root, f.state(t), "export/csv.go", -1, etag); !errors.Is(err, ErrDropRefused) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("no such file in the diff", func(t *testing.T) {
		f := newFixRun(t)
		etag := f.diff(t).ETag
		if _, err := Drop(ctx, f.root, f.state(t), "export/nowhere.go", 0, etag); !errors.Is(err, ErrDropRefused) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("the worktree left the run's branch", func(t *testing.T) {
		f := newFixRun(t)
		etag := f.diff(t).ETag
		run(t, f.worktree, "git", "checkout", "-q", "--detach")
		if _, err := Drop(ctx, f.root, f.state(t), "export/csv.go", 0, etag); !errors.Is(err, ErrDropRefused) {
			t.Fatalf("err %v", err)
		}
	})
}

// A refused drop changes nothing: the commit, the branch and the tree are
// exactly as they were.
func TestARefusedDropChangesNothing(t *testing.T) {
	f := newFixRun(t)
	before := f.diff(t)
	if _, err := Drop(context.Background(), f.root, f.state(t), "export/csv.go", 9, "stale"); err == nil {
		t.Fatal("expected a refusal")
	}
	if head := run(t, f.worktree, "git", "rev-parse", "HEAD"); head != before.Head {
		t.Errorf("head moved to %q", head)
	}
	if status := run(t, f.worktree, "git", "status", "--porcelain"); status != "" {
		t.Errorf("the tree is dirty:\n%s", status)
	}
}

func TestPatchIsCappedAtTwoMebibytes(t *testing.T) {
	f := newFixRun(t)

	var b strings.Builder
	for i := 0; i < 90000; i++ {
		b.WriteString("this line exists only to make the patch large enough to be capped\n")
	}
	mustWrite(t, filepath.Join(f.worktree, "export", "big.txt"), b.String())
	run(t, f.worktree, "git", "add", "-A")
	run(t, f.worktree, "git", "commit", "-q", "--amend", "--no-edit")

	d := f.diff(t)
	if !d.Truncated {
		t.Fatalf("a %d-byte patch was not truncated", len(d.Patch))
	}
	if len(d.Patch) > patchCap {
		t.Errorf("patch is %d bytes, over the %d cap", len(d.Patch), patchCap)
	}
	// The cap falls on a line boundary, so what is served still parses.
	if !strings.HasSuffix(d.Patch, "\n") {
		t.Error("the truncated patch does not end on a line")
	}
}

// --- helpers ---

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// hunksFor is the test's own reading of the patch, so the assertions do not
// lean on the same parser the implementation uses to split it.
func hunksFor(t *testing.T, patch, path string) []string {
	t.Helper()
	var hunks []string
	inFile := false
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inFile = strings.HasSuffix(line, " b/"+path)
		case inFile && strings.HasPrefix(line, "@@"):
			hunks = append(hunks, line)
		}
	}
	return hunks
}

func assertReviewEvent(t *testing.T, path, wantPath string, wantHunk int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the run log: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e struct {
			Kind    string `json:"kind"`
			Payload struct {
				Action string `json:"action"`
				Path   string `json:"path"`
				Hunk   int    `json:"hunk"`
			} `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Kind != "review" {
			continue
		}
		if e.Payload.Action == "drop" && e.Payload.Path == wantPath && e.Payload.Hunk == wantHunk {
			return
		}
	}
	t.Fatalf("no review event for %s hunk %d in\n%s", wantPath, wantHunk, data)
}
