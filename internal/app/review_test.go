package app

import (
	"context"
	"errors"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/fix"
)

func TestDiffOfCarriesTheChange(t *testing.T) {
	got := diffOf(fix.Diff{
		Base: "aaa", Head: "bbb", Branch: "fix-omni-1", Worktree: "/w/.sirdar/worktrees/r1",
		WorktreePresent: true, Pushed: true, Truncated: true, Patch: "diff --git a/x b/x\n", ETag: "e1",
		Files: []fix.FileChange{{Path: "x", Status: "renamed", Additions: 3, Deletions: 1}},
	})
	if got.Base != "aaa" || got.Head != "bbb" || got.Branch != "fix-omni-1" {
		t.Fatalf("diff %+v", got)
	}
	if !got.WorktreePresent || !got.Pushed || !got.Truncated || got.ETag != "e1" {
		t.Fatalf("flags %+v", got)
	}
	if len(got.Files) != 1 || got.Files[0] != (DiffFile{Path: "x", Status: "renamed", Additions: 3, Deletions: 1}) {
		t.Fatalf("files %+v", got.Files)
	}
}

// An empty file list is an empty JSON array rather than null, the same way
// every other list the UI iterates is.
func TestDiffOfHasNoNilFileList(t *testing.T) {
	if diffOf(fix.Diff{}).Files == nil {
		t.Fatal("nil files")
	}
}

// The ids go through the same validation every other run lookup does, and
// an id nobody knows is a 404 rather than a path handed to git.
func TestReviewRoutesRejectUnknownIDs(t *testing.T) {
	root := newWorkspace(t)
	svc := New(newRegistry(t, root), stubBuilder(&stubProvider{script: replay()}, stubTracker{}, stubHelpdesk{}), Options{})

	if _, err := svc.RunDiff("deadbeef1234", "r1"); !errors.Is(err, ErrNoSuchWorkspace) {
		t.Fatalf("unknown workspace: %v", err)
	}
	if _, err := svc.RunDiff(WorkspaceID(root), "../elsewhere"); !errors.Is(err, ErrNoSuchRun) {
		t.Fatalf("path-shaped run id: %v", err)
	}
	if _, err := svc.DropHunk(context.Background(), WorkspaceID(root), "nope", "x.go", 0, "e1"); !errors.Is(err, ErrNoSuchRun) {
		t.Fatalf("unknown run: %v", err)
	}
}
