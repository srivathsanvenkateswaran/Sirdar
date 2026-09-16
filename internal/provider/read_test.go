package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readPolicy is a triage policy confined to a workspace root with a run
// directory of its own, which is the shape internal/run builds.
func readPolicy(t *testing.T) (*PermissionPolicy, string, string) {
	t.Helper()
	root := t.TempDir()
	runDir := filepath.Join(t.TempDir(), "runs", "OMNI-1", "20260915T000000Z-aaaa")
	if err := os.MkdirAll(filepath.Join(runDir, "bundle"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &PermissionPolicy{
		Root:      root,
		ReadRoots: []string{runDir, filepath.Join(runDir, "bundle")},
	}, root, runDir
}

func TestReadInsideTheWorkspaceIsAllowed(t *testing.T) {
	p, root, runDir := readPolicy(t)
	cases := []struct{ tool, input string }{
		{"Read", `{"file_path":"src/main.go"}`},
		{"Read", `{"file_path":` + quoteJSON(filepath.Join(root, "src", "main.go")) + `}`},
		{"Grep", `{"pattern":"/etc/passwd","path":"src"}`},
		{"Glob", `{"pattern":"**/*.go"}`},
		{"LS", `{}`},
		{"read_file", `{"path":"go.mod"}`},
		// The run's own directory and bundle are read scope too: the
		// prompt points the session at the attachments it staged.
		{"Read", `{"file_path":` + quoteJSON(filepath.Join(runDir, "bundle", "thread.md")) + `}`},
		{"Read", `{"file_path":` + quoteJSON(filepath.Join(runDir, "events.jsonl")) + `}`},
	}
	for _, c := range cases {
		if d := p.Decide(c.tool, json.RawMessage(c.input)); !d.Allow {
			t.Errorf("%s %s denied: %s", c.tool, c.input, d.Message)
		}
	}
}

func TestReadOutsideTheWorkspaceIsDenied(t *testing.T) {
	p, _, _ := readPolicy(t)
	cases := []struct{ tool, input, target string }{
		{"Read", `{"file_path":"/etc/passwd"}`, "/etc/passwd"},
		{"Read", `{"file_path":"~/.claude/skills/support-triage/SKILL.md"}`,
			"~/.claude/skills/support-triage/SKILL.md"},
		{"Read", `{"file_path":"../../../etc/passwd"}`, "../../../etc/passwd"},
		{"Grep", `{"pattern":"secret","path":"/etc"}`, "/etc"},
		{"Glob", `{"pattern":"/Users/*/.ssh/*"}`, "/Users/*/.ssh/*"},
		{"LS", `{"path":"/"}`, "/"},
		{"read_file", `{"path":"/etc/hosts"}`, "/etc/hosts"},
		{"list_dir", `{"path":"/var"}`, "/var"},
	}
	for _, c := range cases {
		d := p.Decide(c.tool, json.RawMessage(c.input))
		if d.Allow {
			t.Errorf("%s %s allowed", c.tool, c.input)
			continue
		}
		want := "read outside the workspace: " + c.target
		if !strings.Contains(d.Message, want) {
			t.Errorf("%s %s denial %q does not carry %q", c.tool, c.input, d.Message, want)
		}
	}
}

// A read tool that named several paths is refused when any one of them is
// outside: one approval covers the whole call.
func TestReadDeniesTheWholeCallOnOneOutsidePath(t *testing.T) {
	p, _, _ := readPolicy(t)
	d := p.Decide("Read", json.RawMessage(`{"paths":["go.mod","/etc/passwd"]}`))
	if d.Allow {
		t.Fatal("a call naming an outside path among inside ones was allowed")
	}
	if !strings.Contains(d.Message, "/etc/passwd") {
		t.Fatalf("denial %q does not name the outside path", d.Message)
	}
}

func TestReadAlsoWidensTheScope(t *testing.T) {
	p, _, _ := readPolicy(t)
	// The absolute entry names a real directory outside the workspace
	// rather than a hardcoded "/opt/reference". On Windows a POSIX-looking
	// absolute path is rooted on the current drive without being absolute,
	// so ReadScope.Resolve puts the agent's path through filepath.Abs and
	// judges "D:\opt\reference\runbook.md" while the entry the operator
	// wrote stays "\opt\reference" — the two never compare equal, and the
	// entry would cover nothing. A directory that exists on the running
	// machine is the same test on every OS.
	outside := t.TempDir()
	reference := filepath.Join(outside, "reference")
	p.ReadAlso = []string{"~/.claude/skills/*", reference}

	allowed := []string{
		`{"file_path":"~/.claude/skills/support-triage/SKILL.md"}`,
		`{"file_path":` + quoteJSON(filepath.Join(reference, "runbook.md")) + `}`,
		`{"file_path":` + quoteJSON(reference) + `}`,
	}
	for _, input := range allowed {
		if d := p.Decide("Read", json.RawMessage(input)); !d.Allow {
			t.Errorf("readAlso did not allow %s: %s", input, d.Message)
		}
	}
	// A sibling of the directory the entry names, so it is outside every
	// root and outside every pattern.
	elsewhere := quoteJSON(filepath.Join(outside, "other", "runbook.md"))
	if d := p.Decide("Read", json.RawMessage(`{"file_path":`+elsewhere+`}`)); d.Allow {
		t.Error("readAlso allowed a path no pattern names")
	}
}

// A readAlso entry written with "~" also covers the same path spelled
// absolutely, and one written absolutely covers the "~" spelling: the
// operator should not have to guess which form the agent will send.
func TestReadAlsoMatchesBothHomeSpellings(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	p, _, _ := readPolicy(t)
	p.ReadAlso = []string{"~/notes/*"}
	abs := filepath.Join(home, "notes", "x.md")
	if d := p.Decide("Read", json.RawMessage(`{"file_path":`+quoteJSON(abs)+`}`)); !d.Allow {
		t.Errorf("the absolute spelling of a ~ pattern was denied: %s", d.Message)
	}
}

// A symlink inside the workspace pointing out of it is refused like any
// other outside path: the target is resolved before it is judged.
func TestReadResolvesSymlinksBeforeJudging(t *testing.T) {
	p, root, _ := readPolicy(t)
	outside := filepath.Join(t.TempDir(), "secrets.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if d := p.Decide("Read", json.RawMessage(`{"file_path":"link.txt"}`)); d.Allow {
		t.Fatal("a symlink out of the workspace was allowed")
	}
}

// A policy with no root at all confines nothing: that is the eval rubric's
// judge session and the provider fallbacks, where there is no workspace to
// judge a path against. Every run internal/run starts has one.
func TestReadWithNoRootIsUnconfined(t *testing.T) {
	p := &PermissionPolicy{}
	if d := p.Decide("Read", json.RawMessage(`{"file_path":"/etc/passwd"}`)); !d.Allow {
		t.Fatalf("a rootless policy denied a read: %s", d.Message)
	}
}

// Read tools stay read tools in a fix run: the write policy is about where
// a write may land, and a fix session reads its worktree and its own run
// directory.
func TestReadScopeAppliesToAFixRun(t *testing.T) {
	root := t.TempDir()
	runDir := t.TempDir()
	p := FixPolicy(root, nil, nil, nil)
	p.ReadRoots = []string{runDir}

	if d := p.Decide("Read", json.RawMessage(`{"file_path":"src/main.go"}`)); !d.Allow {
		t.Errorf("fix run denied a read inside the worktree: %s", d.Message)
	}
	if d := p.Decide("Read", json.RawMessage(`{"file_path":`+quoteJSON(runDir)+`}`)); !d.Allow {
		t.Errorf("fix run denied a read of its own run directory: %s", d.Message)
	}
	if d := p.Decide("Read", json.RawMessage(`{"file_path":"/etc/passwd"}`)); d.Allow {
		t.Error("fix run allowed a read outside every root")
	}
}

// Arguments that do not parse are refused rather than approved unseen, the
// same way a fetch naming no URL is.
func TestReadWithUnreadableArgumentsIsDenied(t *testing.T) {
	p, _, _ := readPolicy(t)
	if d := p.Decide("Read", json.RawMessage(`["not","an","object"]`)); d.Allow {
		t.Fatal("a read whose arguments do not parse was allowed")
	}
}

// Grep's pattern is a regular expression, not a path: a search for a
// literal "/etc/passwd" inside the workspace is a read of the workspace.
func TestGrepPatternIsNotJudgedAsAPath(t *testing.T) {
	p, _, _ := readPolicy(t)
	if d := p.Decide("Grep", json.RawMessage(`{"pattern":"/etc/passwd"}`)); !d.Allow {
		t.Fatalf("a grep pattern was judged as a path: %s", d.Message)
	}
}

func TestReadScopeRejectsAnEmptyPath(t *testing.T) {
	scope := ReadScope{Roots: []string{t.TempDir()}}
	if _, err := scope.Resolve("  "); err == nil {
		t.Fatal("an empty path resolved")
	}
}
