package app

import (
	"context"

	"github.com/srivathsanvenkateswaran/sirdar/internal/fix"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// The two error classes the review routes answer with: a run with no change
// to show (404) and a change that must not be edited right now (409). They
// are internal/fix's, re-exported so the HTTP layer can classify them
// without importing the fix package.
var (
	// ErrNoDiff is a run that is not a fix, or whose worktree is gone and
	// which recorded no commit.
	ErrNoDiff = fix.ErrNoDiff
	// ErrRefused is a change that exists but cannot be edited: the run is
	// live, the worktree is gone, the branch is pushed, or the hunk index
	// was computed against a patch that has since moved.
	ErrRefused = fix.ErrDropRefused
)

// RunDiff returns one fix run's change file by file, with the unified patch
// a reviewer reads it from. It starts no session and changes nothing: the
// commit is already made, and this reads it out of the run's own worktree
// or, once that is gone, out of the repository.
func (s *Service) RunDiff(wsID, runID string) (RunDiff, error) {
	root, state, err := s.openFixRun(wsID, runID)
	if err != nil {
		return RunDiff{}, err
	}
	d, err := fix.ReviewDiff(context.Background(), root, state)
	if err != nil {
		return RunDiff{}, err
	}
	return diffOf(d), nil
}

// DropHunk reverts one hunk of one file out of the fix run's commit and
// amends the commit in place, then returns the change as it stands
// afterwards. etag is the hash of the patch the caller was served: a hunk
// index means nothing against a different patch, so a mismatch is refused
// rather than applied to whatever is there now.
func (s *Service) DropHunk(ctx context.Context, wsID, runID, path string, hunk int, etag string) (RunDiff, error) {
	root, state, err := s.openFixRun(wsID, runID)
	if err != nil {
		return RunDiff{}, err
	}
	d, err := fix.Drop(ctx, root, state, path, hunk, etag)
	if err != nil {
		return RunDiff{}, err
	}
	return diffOf(d), nil
}

// openFixRun resolves a workspace and run id to the workspace root and the
// run's state, with the same id validation every other lookup goes through.
func (s *Service) openFixRun(wsID, runID string) (string, store.State, error) {
	root, err := s.root(wsID)
	if err != nil {
		return "", store.State{}, err
	}
	_, state, err := s.openRun(wsID, runID)
	if err != nil {
		return "", store.State{}, err
	}
	return root, state, nil
}

// diffOf converts internal/fix's reading of the change into the wire shape.
func diffOf(d fix.Diff) RunDiff {
	files := make([]DiffFile, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, DiffFile{
			Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions,
		})
	}
	return RunDiff{
		Base: d.Base, Head: d.Head,
		Branch:          d.Branch,
		Worktree:        d.Worktree,
		WorktreePresent: d.WorktreePresent,
		Pushed:          d.Pushed,
		Files:           files,
		Patch:           d.Patch,
		Truncated:       d.Truncated,
		ETag:            d.ETag,
	}
}
