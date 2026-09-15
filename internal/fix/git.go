package fix

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// git runs git commands in one working tree.
type git struct{ dir string }

// out runs a git command and returns its trimmed standard output. The error
// carries the command's own stderr, because "exit status 1" on its own has
// never told anybody anything.
func (g git) out(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.dir
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

// run is out for a command whose output nobody reads.
func (g git) run(ctx context.Context, args ...string) error {
	_, err := g.out(ctx, args...)
	return err
}

// clean reports whether the working tree has nothing uncommitted in it.
func (g git) clean(ctx context.Context) (bool, string, error) {
	status, err := g.out(ctx, "status", "--porcelain")
	if err != nil {
		return false, "", err
	}
	return status == "", status, nil
}

// defaultBranch is the branch a fix is cut from: what origin/HEAD points at,
// falling back to whichever of main and master the remote actually has. The
// fallback matters on a remote that was cloned before origin/HEAD was set,
// where the symbolic ref simply is not there.
func (g git) defaultBranch(ctx context.Context) (string, error) {
	if ref, err := g.out(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(ref, "origin/"); name != "" {
			return name, nil
		}
	}
	for _, name := range []string{"main", "master"} {
		if err := g.run(ctx, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("fix: cannot tell which branch origin's default is; pass --base")
}

// head is the commit the working tree is on.
func (g git) head(ctx context.Context) (string, error) {
	return g.out(ctx, "rev-parse", "HEAD")
}

// currentBranch is the branch name HEAD is on, or "" in a detached head.
func (g git) currentBranch(ctx context.Context) string {
	name, err := g.out(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || name == "HEAD" {
		return ""
	}
	return name
}

// remoteURL is origin's URL.
func (g git) remoteURL(ctx context.Context) (string, error) {
	return g.out(ctx, "remote", "get-url", "origin")
}

// compareURL turns a remote URL into the browser page that opens a pull
// request from branch onto base, so a workspace without `gh` still gets one
// click rather than a shrug. It returns "" for a remote whose shape it does
// not recognise, which is honest: guessing a URL that 404s is worse than
// saying nothing.
func compareURL(remote, base, branch string) string {
	host, path, ok := parseRemote(remote)
	if !ok {
		return ""
	}
	switch {
	case strings.Contains(host, "github"):
		return fmt.Sprintf("https://%s/%s/compare/%s...%s?expand=1", host, path, base, branch)
	case strings.Contains(host, "gitlab"):
		return fmt.Sprintf("https://%s/%s/-/merge_requests/new?merge_request[source_branch]=%s&merge_request[target_branch]=%s",
			host, path, url.QueryEscape(branch), url.QueryEscape(base))
	default:
		return ""
	}
}

// parseRemote splits a git remote URL into its host and its owner/repo
// path, handling both the scp-like "git@host:owner/repo.git" form and the
// URL forms.
func parseRemote(remote string) (string, string, bool) {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	if remote == "" {
		return "", "", false
	}
	if rest, ok := strings.CutPrefix(remote, "git@"); ok {
		host, path, found := strings.Cut(rest, ":")
		if !found || host == "" || path == "" {
			return "", "", false
		}
		return host, strings.Trim(path, "/"), true
	}
	u, err := url.Parse(remote)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", "", false
	}
	return u.Host, path, true
}

// --- linked worktrees -------------------------------------------------

// worktreesDir is where a fix run's linked worktrees live, inside the
// workspace's own .sirdar/ so that one exclusion covers Sirdar's whole
// footprint in the repository.
func worktreesDir(root string) string {
	return filepath.Join(root, ".sirdar", "worktrees")
}

// worktreePath is the linked worktree for one run: named after the run id,
// so `sirdar runs` and the directory on disk say the same thing.
func worktreePath(root, runID string) string {
	return filepath.Join(worktreesDir(root), runID)
}

// addWorktree checks branch out into its own directory, cut from
// origin/base, without touching the tree the operator is standing in.
//
// -B is used for the same reason `git checkout -B` was: a rerun for the
// same ticket names the same branch, and a branch left over from an
// earlier attempt must be reset to the freshly fetched base rather than
// refuse the run. Any worktree still holding that branch is removed first
// — git checks a branch out in one worktree at a time, and what a rerun
// wants is this attempt, not the last one's directory.
func addWorktree(ctx context.Context, g git, path, branch, base string) error {
	if err := releaseBranch(ctx, g, path, branch); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("fix: create the worktree directory: %w", err)
	}
	return g.run(ctx, "worktree", "add", "-B", branch, path, "origin/"+base)
}

// releaseBranch clears whatever would make `git worktree add` refuse:
// registrations whose directory is gone, a directory left at path, and any
// worktree still holding branch. Only worktrees under the workspace's own
// .sirdar/worktrees/ are removed — a branch the operator has checked out in
// a worktree of their own is theirs, and the refusal from git is the right
// answer there.
func releaseBranch(ctx context.Context, g git, path, branch string) error {
	if err := g.run(ctx, "worktree", "prune"); err != nil {
		return err
	}
	// Both sides are resolved through symlinks before they are compared:
	// git answers with the real path (/private/var/... on macOS) while the
	// workspace root is often the symlinked one (/var/...), and a plain
	// string comparison would decide none of these worktrees are Sirdar's.
	mine := realPath(worktreesDir(g.dir))
	want := realPath(path)
	for _, wt := range listWorktrees(ctx, g) {
		wtPath := realPath(wt.path)
		if !within(mine, wtPath) {
			continue
		}
		if wtPath != want && wt.branch != branch {
			continue
		}
		if err := g.run(ctx, "worktree", "remove", "--force", wt.path); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(path); err == nil {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("fix: clear the worktree directory %s: %w", path, err)
		}
	}
	return g.run(ctx, "worktree", "prune")
}

// removeWorktree takes a finished run's worktree away and prunes the
// registration behind it. It is called from the main tree, never from
// inside the worktree being removed, and a failure is reported rather than
// fatal: the commit is pushed by then, and a leftover directory is untidy,
// not wrong.
//
// --force is deliberate. What is left in the tree after the commit is
// build output — a bin/, an obj/, a node_modules/ the session's `make test`
// created — and refusing to clean up because a build left something behind
// would strand a directory for every fix that ever ran.
func removeWorktree(ctx context.Context, g git, path string, stderr io.Writer, key string) {
	if path == "" {
		return
	}
	if err := g.run(ctx, "worktree", "remove", "--force", path); err != nil {
		fmt.Fprintf(stderr, "[%s] the worktree %s was not removed: %v\n", key, path, err)
	}
	if err := g.run(ctx, "worktree", "prune"); err != nil {
		fmt.Fprintf(stderr, "[%s] git worktree prune: %v\n", key, err)
	}
}

// worktreeEntry is one line-group of `git worktree list --porcelain`.
type worktreeEntry struct {
	path   string
	branch string // "" for a detached head
}

// listWorktrees reads the repository's registered worktrees. A git that
// refuses the question reports none, which leaves the caller doing what it
// did before worktrees existed rather than failing the run over a listing.
func listWorktrees(ctx context.Context, g git) []worktreeEntry {
	out, err := g.out(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var entries []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.path != "" {
			entries = append(entries, cur)
		}
		cur = worktreeEntry{}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.path = filepath.Clean(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return entries
}

// isWorktree reports whether path is a working tree git knows about: a
// directory carrying the .git file or directory that makes it one.
func isWorktree(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return false
	}
	return true
}

// realPath resolves a path through symlinks, falling back to the absolute
// form of what it was given for a path that does not exist yet.
func realPath(p string) string {
	resolved, err := provider.EvalNearest(p)
	if err != nil {
		resolved = p
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		return abs
	}
	return filepath.Clean(resolved)
}

// within reports whether target is dir or lies inside it, the same
// segment-by-segment comparison the reserved-path check makes.
func within(dir, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
