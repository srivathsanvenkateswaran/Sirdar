package fix

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
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
