// Package worktree manages the linked git worktrees Sirdar runs sessions
// in. Two flows use them and they use them the same way, which is why the
// machinery lives here rather than in either of them:
//
//   - `sirdar fix` checks its fix branch out into a worktree of the run's
//     own, so the operator's tree is neither edited nor swept into the
//     commit (internal/fix).
//   - `sirdar triage --at` and `sirdar rca --at` check a historical commit
//     out at a detached HEAD, so a session can reason about the repository
//     as it stood when a ticket was filed (internal/run).
//
// Both put the directory at <root>/.sirdar/worktrees/<run-id>, so the run
// id, `sirdar runs` and what is on disk all say the same thing, and one
// exclusion in .git/info/exclude covers Sirdar's whole footprint in the
// repository.
//
// What is *not* here is the workspace's own .sirdar/. The configuration,
// the playbooks and the note templates are read from the main tree
// whichever worktree a session stands in: they are what shapes the session,
// and a historical checkout of them is not what the operator configured.
package worktree

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// Git runs git commands in one working tree.
type Git struct{ Dir string }

// Out runs a git command and returns its trimmed standard output. The error
// carries the command's own stderr, because "exit status 1" on its own has
// never told anybody anything.
func (g Git) Out(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Run is Out for a command whose output nobody reads.
func (g Git) Run(ctx context.Context, args ...string) error {
	_, err := g.Out(ctx, args...)
	return err
}

// Dir is where a run's linked worktrees live, inside the workspace's own
// .sirdar/ so that one exclusion covers Sirdar's whole footprint in the
// repository.
func Dir(root string) string {
	return filepath.Join(root, ".sirdar", "worktrees")
}

// Path is the linked worktree for one run: named after the run id, so
// `sirdar runs` and the directory on disk say the same thing.
func Path(root, runID string) string {
	return filepath.Join(Dir(root), runID)
}

// ResolveCommit turns whatever the operator wrote after --at — a sha, a
// tag, `HEAD~12`, a branch name — into the full sha of a commit that
// actually exists here, and refuses anything else before a directory or a
// run is created for it.
func ResolveCommit(ctx context.Context, g Git, rev string) (string, error) {
	sha, err := g.Out(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil || sha == "" {
		return "", fmt.Errorf("worktree: %q names no commit in this repository", rev)
	}
	return sha, nil
}

// Add checks branch out into its own directory, cut from start, without
// touching the tree the operator is standing in.
//
// -B is used for the same reason `git checkout -B` was: a rerun for the
// same ticket names the same branch, and a branch left over from an earlier
// attempt must be reset to the freshly fetched start point rather than
// refuse the run. Any worktree still holding that branch is removed first —
// git checks a branch out in one worktree at a time, and what a rerun wants
// is this attempt, not the last one's directory.
func Add(ctx context.Context, g Git, path, branch, start string) error {
	if err := Release(ctx, g, path, branch); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("worktree: create the worktree directory: %w", err)
	}
	return g.Run(ctx, "worktree", "add", "-B", branch, path, start)
}

// AddDetached checks commit out into its own directory at a detached HEAD.
// No branch is made or moved: a retrospective run reads the repository as it
// stood at that commit and writes nothing, so there is nothing for a branch
// name to mean.
func AddDetached(ctx context.Context, g Git, path, commit string) error {
	if err := Release(ctx, g, path, ""); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("worktree: create the worktree directory: %w", err)
	}
	return g.Run(ctx, "worktree", "add", "--detach", path, commit)
}

// Release clears whatever would make `git worktree add` refuse:
// registrations whose directory is gone, a directory left at path, and —
// when branch is named — any worktree still holding it. Only worktrees
// under the workspace's own .sirdar/worktrees/ are removed: a branch the
// operator has checked out in a worktree of their own is theirs, and the
// refusal from git is the right answer there.
func Release(ctx context.Context, g Git, path, branch string) error {
	if err := g.Run(ctx, "worktree", "prune"); err != nil {
		return err
	}
	// Both sides are resolved through symlinks before they are compared:
	// git answers with the real path (/private/var/... on macOS) while the
	// workspace root is often the symlinked one (/var/...), and a plain
	// string comparison would decide none of these worktrees are Sirdar's.
	mine := RealPath(Dir(g.Dir))
	want := RealPath(path)
	for _, wt := range List(ctx, g) {
		wtPath := RealPath(wt.Path)
		if !Within(mine, wtPath) {
			continue
		}
		// An empty branch matches nothing: a detached checkout holds no
		// branch, and treating "" as one would take away every other
		// detached worktree Sirdar has open.
		if wtPath != want && (branch == "" || wt.Branch != branch) {
			continue
		}
		if err := g.Run(ctx, "worktree", "remove", "--force", wt.Path); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(path); err == nil {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("worktree: clear the worktree directory %s: %w", path, err)
		}
	}
	return g.Run(ctx, "worktree", "prune")
}

// Remove takes a finished run's worktree away and prunes the registration
// behind it. It is called from the main tree, never from inside the worktree
// being removed, and a failure is reported rather than fatal: whatever the
// worktree was for has happened by then, and a leftover directory is untidy,
// not wrong.
//
// --force is deliberate. What is left in the tree after the run is build
// output — a bin/, an obj/, a node_modules/ the session's `make test`
// created — and refusing to clean up because a build left something behind
// would strand a directory for every run that ever used one.
//
// path is checked against g.Dir's own worktrees directory before anything
// runs. Most callers pass a path this package built itself, always inside
// that bound, but some come from a run's state.json — data an operator can
// hand-edit or a compromised session could have left behind — and --force
// deletes whatever is at the path with no further question. A path outside
// the bound is refused rather than handed to git.
func Remove(ctx context.Context, g Git, path string, stderr io.Writer, key string) {
	if path == "" {
		return
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if !Within(RealPath(Dir(g.Dir)), RealPath(path)) {
		fmt.Fprintf(stderr, "[%s] refusing to remove %s: it is not inside %s, so this cannot be one of Sirdar's own worktrees\n",
			key, path, Dir(g.Dir))
		return
	}
	if err := g.Run(ctx, "worktree", "remove", "--force", path); err != nil {
		fmt.Fprintf(stderr, "[%s] the worktree %s was not removed: %v\n", key, path, err)
	}
	if err := g.Run(ctx, "worktree", "prune"); err != nil {
		fmt.Fprintf(stderr, "[%s] git worktree prune: %v\n", key, err)
	}
}

// Safe returns path when it lies inside this workspace's own worktrees
// directory, and "" otherwise. It exists for the places a worktree path is
// read back out of a run's state.json rather than built by this package,
// where it feeds both the working directory a git command runs in and the
// argument to `git worktree remove --force`. A tampered or hand-edited
// state.json naming a path outside .sirdar/worktrees/ — an operator's own
// worktree, say — is refused, with a reason on stderr, rather than trusted.
func Safe(root, path string, stderr io.Writer, key string) string {
	if path == "" {
		return ""
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if !Within(RealPath(Dir(root)), RealPath(path)) {
		fmt.Fprintf(stderr, "[%s] the run state names a worktree outside %s (%s); using the main tree instead\n",
			key, Dir(root), path)
		return ""
	}
	return path
}

// Entry is one line-group of `git worktree list --porcelain`.
type Entry struct {
	Path   string
	Branch string // "" for a detached head
}

// List reads the repository's registered worktrees. A git that refuses the
// question reports none, which leaves the caller doing what it did before
// worktrees existed rather than failing the run over a listing.
func List(ctx context.Context, g Git) []Entry {
	out, err := g.Out(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var entries []Entry
	var cur Entry
	flush := func() {
		if cur.Path != "" {
			entries = append(entries, cur)
		}
		cur = Entry{}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = filepath.Clean(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return entries
}

// IsWorktree reports whether path is a working tree git knows about: a
// directory carrying the .git file or directory that makes it one.
func IsWorktree(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return false
	}
	return true
}

// RealPath resolves a path through symlinks, falling back to the absolute
// form of what it was given for a path that does not exist yet.
func RealPath(p string) string {
	resolved, err := provider.EvalNearest(p)
	if err != nil {
		resolved = p
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		return abs
	}
	return filepath.Clean(resolved)
}

// Within reports whether target is dir or lies inside it, the same
// segment-by-segment comparison the reserved-path check makes.
func Within(dir, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// RelToRoot renders a path under the workspace for a progress line.
func RelToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// StaleAge is how long a run's linked worktree outlives its run once that
// run has ended. A completed run has nothing left to write into its
// worktree, and a fix run that reused a branch and later published from it
// does so through the branch and commit in the shared repository, worktree
// or not; so a directory still there a day later is clutter, not work still
// in progress.
const StaleAge = 24 * time.Hour

// PruneStale removes this workspace's own linked worktrees whose run
// reached completed or failed at least StaleAge ago. It runs once at the
// start of every fix, so a workspace where `sirdar fix` runs often does not
// accumulate one directory under .sirdar/worktrees/ per run forever.
//
// Only a worktree whose run state says completed or failed is a candidate.
// One that is still running, or blocked — an operator's answer pending, a
// rate limit, anything that pauses a run rather than ending it — is left
// alone regardless of age, because it may still be resumed into. So is a
// worktree whose run id cannot be read at all: with no state to judge it
// by, the safe assumption is that it is not safe to touch, not that it is.
//
// A failure to remove one is reported and otherwise ignored — it is
// workspace tidiness, not a reason to refuse the run that triggered it —
// and what was removed is logged, since --force deletes it without asking.
func PruneStale(ctx context.Context, g Git, root string, now time.Time, stderr io.Writer) {
	if stderr == nil {
		stderr = io.Discard
	}
	mine := RealPath(Dir(root))
	for _, wt := range List(ctx, g) {
		wtPath := RealPath(wt.Path)
		if !Within(mine, wtPath) {
			continue
		}
		runID := filepath.Base(wtPath)
		_, state, err := store.Open(root, runID)
		if err != nil {
			continue
		}
		if state.Status != store.StatusCompleted && state.Status != store.StatusFailed {
			continue
		}
		if now.Sub(state.UpdatedAt) < StaleAge {
			continue
		}
		if err := g.Run(ctx, "worktree", "remove", "--force", wt.Path); err != nil {
			fmt.Fprintf(stderr, "worktree: prune stale worktree %s: %v\n", RelToRoot(root, wt.Path), err)
			continue
		}
		fmt.Fprintf(stderr, "worktree: pruned stale worktree %s (run %s, %s)\n", RelToRoot(root, wt.Path), runID, state.Status)
	}
	if err := g.Run(ctx, "worktree", "prune"); err != nil {
		fmt.Fprintf(stderr, "worktree: git worktree prune: %v\n", err)
	}
}
