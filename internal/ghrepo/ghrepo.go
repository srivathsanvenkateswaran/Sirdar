// Package ghrepo notices when a ticket or a Slack thread names a GitHub
// repository that is not the workspace's own: a pull request link into
// another service, a file URL in a sibling repository. It says so and does
// nothing else — no cross-repository reading, no clone. The intake chip and
// the triage prompt both use it, so a session told "the PR is in owner/repo"
// can say the code it needs may live there instead of guessing from the
// repository it was given.
package ghrepo

import (
	"context"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// mention is a github.com URL's owner and repository. It matches the http
// form and the scp-like git@github.com:owner/repo form alike.
var mention = regexp.MustCompile(`(?i)\bgithub\.com[/:]([a-z0-9][a-z0-9-]{0,38})/([a-z0-9._-]{1,100})`)

// notOwners are the first path segments github.com uses for pages that are
// not a repository.
var notOwners = map[string]bool{
	"about": true, "apps": true, "collections": true, "enterprise": true, "explore": true,
	"features": true, "login": true, "marketplace": true, "notifications": true, "orgs": true,
	"pricing": true, "settings": true, "sponsors": true, "topics": true, "user-attachments": true,
	"users": true, "search": true, "pulls": true, "issues": true, "new": true, "codespaces": true,
}

// Mentioned is every owner/repo a text names in a github.com URL, first
// seen first, each once (compared without case).
func Mentioned(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mention.FindAllStringSubmatch(text, -1) {
		owner, repo := m[1], strings.TrimSuffix(strings.TrimRight(m[2], "."), ".git")
		if notOwners[strings.ToLower(owner)] || repo == "" {
			continue
		}
		slug := owner + "/" + repo
		if seen[strings.ToLower(slug)] {
			continue
		}
		seen[strings.ToLower(slug)] = true
		out = append(out, slug)
	}
	return out
}

// FromRemote is the owner/repo of a git remote URL on github.com, in
// either the https or the scp-like form; "" for any other host.
func FromRemote(remote string) string {
	remote = strings.TrimSuffix(strings.TrimSpace(remote), "/")
	remote = strings.TrimSuffix(remote, ".git")
	var host, path string
	if rest, ok := strings.CutPrefix(remote, "git@"); ok {
		host, path, _ = strings.Cut(rest, ":")
	} else if u, err := url.Parse(remote); err == nil {
		host, path = u.Hostname(), u.Path
	}
	if !strings.EqualFold(host, "github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// Origin is the workspace's origin remote as owner/repo when it is on
// github.com, "" when there is no origin or it is elsewhere.
func Origin(ctx context.Context, root string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return FromRemote(string(out))
}

// Foreign is every repository text names that is not origin. With no
// GitHub origin to compare against it is nothing: "not this workspace" is
// only worth saying when it is known what this workspace is.
func Foreign(text, origin string) []string {
	if origin == "" {
		return nil
	}
	var out []string
	for _, slug := range Mentioned(text) {
		if !strings.EqualFold(slug, origin) {
			out = append(out, slug)
		}
	}
	return out
}
