package gather

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/testbin"
)

func TestMain(m *testing.M) {
	testbin.Dispatch(map[string]func() int{
		"gatherfake": fakeCommand,
		"fakegh":     fakeGH,
		"fakemcp":    fakeMCP,
	})
	os.Exit(m.Run())
}

// logArgs appends the fake's argv to the file GATHER_FAKE_LOG names, so a
// test can tell whether a fake ran at all.
func logArgs(what string) {
	path := os.Getenv("GATHER_FAKE_LOG")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, what)
}

// fakeCommand is a command step's program: "sleep" outlives any step
// timeout, "big" prints 300 KiB, "fail" exits 3 with "boom" on stderr, and
// anything else prints "hello".
func fakeCommand() int {
	args := testbin.Args()
	logArgs(strings.Join(args, " "))
	if len(args) > 0 {
		switch args[0] {
		case "sleep":
			time.Sleep(30 * time.Second)
			return 0
		case "big":
			os.Stdout.Write([]byte(strings.Repeat("a", 300*1024)))
			return 0
		case "fail":
			fmt.Fprintln(os.Stderr, "boom")
			return 3
		}
	}
	fmt.Println("hello")
	return 0
}

// fakeSHA is the merge commit the fake gh reports unless the test names a
// real one in GATHER_FAKE_SHA.
const fakeSHA = "0123456789abcdef0123456789abcdef01234567"

// fakeGH answers `gh pr view` with a fixed pull request and `gh pr diff`
// with a two-file diff.
func fakeGH() int {
	args := testbin.Args()
	logArgs("gh " + strings.Join(args, " "))
	if len(args) < 3 || args[0] != "pr" {
		return testbin.Fail("fake gh: unexpected %v", args)
	}
	switch args[1] {
	case "view":
		sha := os.Getenv("GATHER_FAKE_SHA")
		if sha == "" {
			sha = fakeSHA
		}
		view := map[string]any{
			"number":      512,
			"title":       "Fix the export of empty reports",
			"state":       "MERGED",
			"url":         args[2],
			"author":      map[string]string{"login": "dev"},
			"baseRefName": "main",
			"headRefName": "fix/export",
			"headRefOid":  sha,
			"mergedAt":    "2026-09-30T10:00:00Z",
			"mergeCommit": map[string]string{"oid": sha},
			"files": []map[string]any{
				{"path": "a.txt", "additions": 3, "deletions": 1},
				{"path": "dir with (parens)/b.txt", "additions": 1, "deletions": 0},
			},
			"body": "Exports no longer fail on an empty report.",
		}
		b, _ := json.Marshal(view)
		os.Stdout.Write(b)
	case "diff":
		fmt.Print("diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n" +
			"diff --git a/dir with (parens)/b.txt b/dir with (parens)/b.txt\n--- a/dir with (parens)/b.txt\n+++ b/dir with (parens)/b.txt\n@@ -0,0 +1 @@\n+added\n")
	default:
		return testbin.Fail("fake gh: unexpected %v", args)
	}
	return 0
}

// fakeMCP is testbin.FakeMCP that records it was started.
func fakeMCP() int {
	logArgs("start")
	return testbin.FakeMCP()
}

// minimalConfig is the smallest workspace config.Load accepts.
const minimalConfig = `
workspace: demo
sources:
  helpdesk:
    adapter: zohodesk
    orgId: "1"
    baseUrl: https://desk.example
    token: env:ZOHO
`

// newWorkspace writes a workspace with body appended to minimalConfig and
// loads it the way a run does.
func newWorkspace(t *testing.T, root, body string) *config.Config {
	t.Helper()
	if root == "" {
		root = t.TempDir()
	}
	if err := os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(minimalConfig+body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// q quotes s for YAML.
func q(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// runInput is an Input for cfg with its directories under a fresh run
// directory.
func runInput(t *testing.T, cfg *config.Config, kind string) Input {
	t.Helper()
	runDir := t.TempDir()
	bundle := filepath.Join(runDir, "bundle")
	return Input{
		Kind:       kind,
		Config:     cfg,
		RunDir:     runDir,
		BundleDir:  bundle,
		ContextDir: filepath.Join(bundle, "context"),
		Env:        os.Environ(),
	}
}

// git runs git in dir for a test, failing it on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// historyRepo makes a repository with commits A, B and C, A tagged
// v1.0.0 and B (the "merge") tagged v1.1.0, B touching a.txt and a file
// whose path has spaces and parentheses. It returns B's sha.
func historyRepo(t *testing.T, dir string) string {
	t.Helper()
	git(t, dir, "init", "-q", "-b", "main")
	// The commits are made with whatever identity git already has here,
	// as internal/repos' tests do; a machine with none skips.
	if err := exec.Command("git", "-C", dir, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) {
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", msg)
	}
	write("a.txt", "one\n")
	write("dir with (parens)/b.txt", "one\n")
	commit("A: first")
	git(t, dir, "tag", "v1.0.0")
	write("a.txt", "two\n")
	write("dir with (parens)/b.txt", "two\n")
	commit("B: the change")
	b := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "tag", "v1.1.0")
	write("c.txt", "three\n")
	commit("C: later")
	return b
}
