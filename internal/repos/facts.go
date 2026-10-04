package repos

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Facts is what a read-only look at a clone says: whether it is there,
// whether it is a git repository, which branch it is on, and how far that
// branch is behind its upstream as of the clone's last fetch.
type Facts struct {
	Exists bool `json:"exists"`
	Git    bool `json:"git"`
	// Branch is the checked-out branch, or "" on a detached HEAD.
	Branch string `json:"branch,omitempty"`
	// Upstream is the branch's upstream ("origin/main"), "" when none.
	Upstream string `json:"upstream,omitempty"`
	// Behind and Ahead count commits against the upstream as the last
	// fetch left it. Nothing here fetches, so this is never fresher than
	// FetchedAt.
	Behind int `json:"behind"`
	Ahead  int `json:"ahead"`
	// FetchedAt is when the clone last fetched (FETCH_HEAD's time); zero
	// when it never has.
	FetchedAt time.Time `json:"fetchedAt,omitzero"`
}

// ReadFacts looks at the clone at path with read-only git queries. It never
// fetches, and it runs git with optional locks off, so it does not refresh
// the clone's index either.
func ReadFacts(ctx context.Context, path string) Facts {
	var f Facts
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return f
	}
	f.Exists = true
	if out, err := gitRead(ctx, path, "rev-parse", "--is-inside-work-tree"); err != nil || out != "true" {
		return f
	}
	f.Git = true
	if out, err := gitRead(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		f.Branch = out
	}
	if out, err := gitRead(ctx, path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		f.Upstream = out
		if counts, err := gitRead(ctx, path, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
			if parts := strings.Fields(counts); len(parts) == 2 {
				f.Ahead, _ = strconv.Atoi(parts[0])
				f.Behind, _ = strconv.Atoi(parts[1])
			}
		}
	}
	if out, err := gitRead(ctx, path, "rev-parse", "--git-path", "FETCH_HEAD"); err == nil {
		p := out
		if !filepath.IsAbs(p) {
			p = filepath.Join(path, p)
		}
		if st, err := os.Stat(p); err == nil {
			f.FetchedAt = st.ModTime()
		}
	}
	return f
}

// Summary is the facts in a few words: "main · 3 behind origin/main ·
// fetched 2 days ago", "not found", "a plain directory".
func (f Facts) Summary(now time.Time) string {
	switch {
	case !f.Exists:
		return "not found"
	case !f.Git:
		return "a plain directory"
	}
	var parts []string
	if f.Branch != "" {
		parts = append(parts, f.Branch)
	} else {
		parts = append(parts, "detached HEAD")
	}
	switch {
	case f.Upstream == "":
		parts = append(parts, "no upstream")
	case f.Behind == 0:
		parts = append(parts, "up to date with "+f.Upstream)
	default:
		parts = append(parts, fmt.Sprintf("%d behind %s", f.Behind, f.Upstream))
	}
	if f.FetchedAt.IsZero() {
		parts = append(parts, "never fetched")
	} else {
		parts = append(parts, "fetched "+ago(now.Sub(f.FetchedAt)))
	}
	return strings.Join(parts, " · ")
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	}
	return plural(int(d/(24*time.Hour)), "day") + " ago"
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// gitRead runs one read-only git query in dir. GIT_OPTIONAL_LOCKS=0 keeps
// git from taking the index lock to refresh stat data on the way.
func gitRead(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// originCache holds what `git remote get-url origin` said per clone for a
// minute: a configuration is loaded on every request, and the composer
// resolves on every pause in typing.
var originCache = struct {
	sync.Mutex
	m map[string]originEntry
}{m: map[string]originEntry{}}

type originEntry struct {
	origin string
	at     time.Time
}

// originTTL is how long a read origin is reused.
const originTTL = time.Minute

// Origin is the clone's origin remote URL, or "" when the path is not a git
// clone or has no origin. Read-only.
func Origin(path string) string {
	now := time.Now()
	originCache.Lock()
	if e, ok := originCache.m[path]; ok && now.Sub(e.at) < originTTL {
		originCache.Unlock()
		return e.origin
	}
	originCache.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	origin, err := gitRead(ctx, path, "remote", "get-url", "origin")
	if err != nil {
		origin = ""
	}
	originCache.Lock()
	originCache.m[path] = originEntry{origin: origin, at: now}
	originCache.Unlock()
	return origin
}
