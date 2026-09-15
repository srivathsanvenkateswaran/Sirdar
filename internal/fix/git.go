package fix

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/worktree"
)

// git runs git commands in one working tree. The linked-worktree half of
// what a fix does with git lives in internal/worktree, which `sirdar triage
// --at` uses too; what stays here is what only a fix needs — the base
// branch, the remote, and the compare URL.
type git struct{ dir string }

func (g git) wt() worktree.Git { return worktree.Git{Dir: g.dir} }

// out runs a git command and returns its trimmed standard output. The error
// carries the command's own stderr, because "exit status 1" on its own has
// never told anybody anything.
func (g git) out(ctx context.Context, args ...string) (string, error) {
	return g.wt().Out(ctx, args...)
}

// run is out for a command whose output nobody reads.
func (g git) run(ctx context.Context, args ...string) error {
	return g.wt().Run(ctx, args...)
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

// patch is the unified diff of one commit with no commit header around it:
// what `--local` writes to fix.diff for a reviewer to read, and what a
// retrospective evaluation compares against the pull request that actually
// fixed the ticket.
func (g git) patch(ctx context.Context, commit string) (string, error) {
	return g.out(ctx, "show", "--format=", "--patch", "--no-color", commit)
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
//
// The mechanics are internal/worktree's, shared with the retrospective
// triage and rca runs. These are the names this package and its tests have
// always used for them.

func worktreesDir(root string) string        { return worktree.Dir(root) }
func worktreePath(root, runID string) string { return worktree.Path(root, runID) }
func realPath(p string) string               { return worktree.RealPath(p) }
func isWorktree(path string) bool            { return worktree.IsWorktree(path) }
func relToRoot(root, path string) string     { return worktree.RelToRoot(root, path) }

// addWorktree checks branch out into its own directory, cut from start,
// without touching the tree the operator is standing in.
func addWorktree(ctx context.Context, g git, path, branch, start string) error {
	return worktree.Add(ctx, g.wt(), path, branch, start)
}

func removeWorktree(ctx context.Context, g git, path string, stderr io.Writer, key string) {
	worktree.Remove(ctx, g.wt(), path, stderr, key)
}

func safeWorktree(root, path string, stderr io.Writer, key string) string {
	return worktree.Safe(root, path, stderr, key)
}

func pruneStaleWorktrees(ctx context.Context, g git, root string, now time.Time, stderr io.Writer) {
	worktree.PruneStale(ctx, g.wt(), root, now, stderr)
}
