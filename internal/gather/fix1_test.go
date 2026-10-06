package gather

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A relative program is the workspace's, not whatever sits at that path
// below the Sirdar process's own working directory: the file checked must
// be the file run.
func TestRelativeProgramResolvesAgainstRoot(t *testing.T) {
	root := t.TempDir()
	fakeIn(t, root)
	cfg := newWorkspace(t, root, "gather:\n  allow: [\"./bin/gatherfake *\"]\n  steps:\n    - name: greet\n      command: [./bin/gatherfake, hello]\n")
	in := runInput(t, cfg, "triage")
	res := Run(context.Background(), in)
	if e := res.Entries[0]; e.Status != StatusOK {
		t.Fatalf("entry = %+v, want ok", e)
	}
	out, err := ReadOutput(filepath.Join(in.ContextDir, "greet.md"))
	if err != nil || out != "hello" {
		t.Fatalf("output = %q, %v", out, err)
	}
}

// A relative PATH entry names a directory only relative to whichever
// process is asking, so it is not searched at all.
func TestLookPathIgnoresRelativePathEntries(t *testing.T) {
	here, there := t.TempDir(), t.TempDir()
	for _, d := range []string{here, there} {
		if err := os.WriteFile(filepath.Join(d, "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(here)
	sep := string(os.PathListSeparator)
	got, err := lookPath("tool", []string{"PATH=." + sep + there}, "darwin", there)
	if err != nil || got != filepath.Join(there, "tool") {
		t.Fatalf("lookPath = %q, %v; want %q", got, err, filepath.Join(there, "tool"))
	}
	if got, err := lookPath("tool", []string{"PATH=."}, "darwin", there); err == nil {
		t.Fatalf("lookPath with PATH=. found %q, want not found", got)
	}
}

// A commit id from gh's JSON goes into git's argv, so anything that is not
// a hex object name stops the history before git runs.
func TestHistoryRefusesABadSha(t *testing.T) {
	cfg := newWorkspace(t, "", "")
	g := &gatherer{in: Input{Config: cfg, Env: os.Environ()}, cfg: cfg, now: time.Now}
	for _, sha := range []string{"--output=/tmp/x", "abc;id", "HEAD", "abc12", strings.Repeat("a", 65)} {
		h := g.history(context.Background(), cfg.Root, sha, true, []string{"a.txt"})
		if h.status != StatusFailed || h.reason != "the pull request names no valid commit" {
			t.Errorf("history(%q) = %+v, want failed: the pull request names no valid commit", sha, h)
		}
	}
}

// A tag name read back from the clone goes into the next git argv, so one
// that would read as a flag is refused.
func TestHistoryRefusesADashTag(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	if err := exec.Command("git", "-C", root, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "a.txt")
	git(t, root, "commit", "-q", "-m", "A")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "commit", "-q", "-am", "B")
	b := git(t, root, "rev-parse", "HEAD")
	git(t, root, "update-ref", "refs/tags/--output=x", b)

	cfg := newWorkspace(t, root, "")
	g := &gatherer{in: Input{Config: cfg, Env: os.Environ()}, cfg: cfg, now: time.Now}
	h := g.history(context.Background(), root, b, true, []string{"a.txt"})
	if h.status != StatusFailed || !strings.Contains(h.reason, `"--output=x"`) {
		t.Fatalf("history = %+v, want failed naming the tag", h)
	}
}

// A path from the PR's file list is one argv element; one carrying shell
// syntax fails safe and the reason says which.
func TestHistoryPathWithShellSyntax(t *testing.T) {
	cfg := newWorkspace(t, "", "")
	g := &gatherer{in: Input{Config: cfg, Env: os.Environ()}, cfg: cfg, now: time.Now}
	h := g.history(context.Background(), cfg.Root, strings.Repeat("a", 40), true, []string{"$(touch pwned)/x.txt"})
	if h.status == StatusOK || !strings.Contains(h.reason, "$(touch pwned)/x.txt") {
		t.Fatalf("history = %+v, want a failure naming the path", h)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "pwned")); !os.IsNotExist(err) {
		t.Fatal("the path was run by a shell")
	}
}

// The gather deadline fails the row running when it passes and skips the
// rows not yet started. This test changes the parallel package variable,
// so it and every test that calls Run must stay serial.
func TestGatherDeadline(t *testing.T) {
	old := parallel
	parallel = 1
	t.Cleanup(func() { parallel = old })

	root := t.TempDir()
	fake := fakeIn(t, root)
	step := func(name string) string {
		return "    - name: " + name + "\n      timeoutSeconds: 20\n      command: [" + q(fake) + ", sleep]\n"
	}
	cfg := newWorkspace(t, root, "gather:\n  totalSeconds: 1\n  allow: ["+q(fake+" *")+"]\n  steps:\n"+step("one")+step("two"))
	start := time.Now()
	res := Run(context.Background(), runInput(t, cfg, "triage"))
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Run took %s, want under 5s", took)
	}
	if e := res.Entries[0]; e.Status != StatusFailed || e.Reason != "gather deadline" {
		t.Fatalf("row 1 = %+v, want failed: gather deadline", e)
	}
	if e := res.Entries[1]; e.Status != StatusSkipped || e.Reason != "gather deadline" {
		t.Fatalf("row 2 = %+v, want skipped: gather deadline", e)
	}
}

// Two rows never write the same file, however their names come out.
func TestFileNamesNeverCollide(t *testing.T) {
	root := t.TempDir()
	fake := fakeIn(t, root)
	cfg := newWorkspace(t, root, `gather:
  allow: [`+q(fake+" *")+`]
  pr: {anyRepo: true}
  variables:
    v:
      pattern: 'v=(\S+)'
  steps:
    - name: s
      command: [`+q(fake)+`, hello, "{{v}}"]
    - name: x
      command: [`+q(fake)+`, hello, "{{v}}"]
    - name: x-y
      command: [`+q(fake)+`, hello]
`)
	in := runInput(t, cfg, "triage")
	in.Instruction = "v=a/b v=a_b v=y https://github.com/one/lib/pull/7 https://github.com/two/lib/pull/7"
	in.Env = withPath(os.Environ(), t.TempDir())
	res := Run(context.Background(), in)

	seen := map[string]string{}
	for _, e := range res.Entries {
		key := strings.ToLower(e.Name)
		if prev, ok := seen[key]; ok {
			t.Fatalf("two rows named %q: %s and %+v", e.Name, prev, e)
		}
		seen[key] = e.Call
	}
	for _, name := range []string{"pr-lib-7", "pr-lib-7-2", "s-a_b", "s-a_b-2", "x-y", "x-y-2"} {
		if _, ok := seen[name]; !ok {
			t.Errorf("no row named %q in %v", name, seen)
		}
	}
	for _, name := range []string{"s-a_b", "s-a_b-2", "x-y", "x-y-2"} {
		if _, err := os.Stat(filepath.Join(in.ContextDir, name+".md")); err != nil {
			t.Errorf("%s.md: %v", name, err)
		}
	}
}

// A missing gh needs no clone and no git identity to report.
func TestPRWithoutGh(t *testing.T) {
	cfg := newWorkspace(t, "", "gather:\n  pr: {anyRepo: true}\n")
	in := runInput(t, cfg, "triage")
	in.Instruction = "Regressed by https://github.com/silq/OXO.Systems/pull/512"
	in.Env = withPath(os.Environ(), t.TempDir())
	res := Run(context.Background(), in)
	if len(res.Entries) != 1 {
		t.Fatalf("entries = %+v, want the pr row alone", res.Entries)
	}
	if e := res.Entries[0]; e.Status != StatusFailed || e.Reason != "gh is not on PATH" {
		t.Fatalf("pr row = %+v", e)
	}
}
