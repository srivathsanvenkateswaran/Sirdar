package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/testbin"
)

// fileAdapter is the example file-backed source adapter, built once by
// TestMain; fakeClaude, fakeClaudeRCA and fakeMCP are the stand-ins for
// the provider CLI and for an MCP server, installed once by TestMain;
// testdataDir is this package's testdata as an absolute path, captured
// before any test changes directory.
//
// The three stand-ins used to be shell scripts in testdata. They are Go
// functions in internal/testbin now, because Windows cannot execute a
// #!/bin/sh file and every end-to-end test here — triage, rca, serve,
// runs diff, mcp — depends on running one.
var (
	fileAdapter   string
	fakeClaude    string
	fakeClaudeRCA string
	fakeMCP       string
	testdataDir   string
)

func TestMain(m *testing.M) {
	testbin.Dispatch(map[string]func() int{
		"fakeclaude":     testbin.FakeClaude,
		"fakeclaude-rca": testbin.FakeClaudeRCA,
		"fakemcp":        testbin.FakeMCP,
	})

	tmp, err := os.MkdirTemp("", "sirdar-cli")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cli test:", err)
		os.Exit(1)
	}
	testdataDir, err = filepath.Abs("testdata")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cli test:", err)
		os.Exit(1)
	}
	fileAdapter = filepath.Join(tmp, "file-adapter"+testbin.Ext)
	build := exec.Command("go", "build", "-o", fileAdapter, "../../examples/adapters/file")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "cli test: build file adapter: %v\n%s", err, out)
		os.RemoveAll(tmp)
		os.Exit(1)
	}
	if err := installFakes(tmp); err != nil {
		fmt.Fprintln(os.Stderr, "cli test:", err)
		os.RemoveAll(tmp)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// installFakes puts the three stand-ins in dir. It is TestMain's, not a
// test's, so every test sees the same paths and none of them pays for the
// copy.
func installFakes(dir string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	for _, f := range []struct {
		into *string
		name string
	}{
		{&fakeClaude, "fakeclaude"},
		{&fakeClaudeRCA, "fakeclaude-rca"},
		{&fakeMCP, "fakemcp"},
	} {
		path := filepath.Join(dir, f.name+testbin.Ext)
		if err := testbin.LinkOrCopy(self, path); err != nil {
			return fmt.Errorf("install %s: %w", f.name, err)
		}
		*f.into = path
	}
	return nil
}

// TestFakeDocumentsValidate proves the canned documents the fake provider
// returns are the real thing: if they did not satisfy the embedded schemas,
// every end-to-end assertion below would be testing the retry path instead
// of the happy one.
func TestFakeDocumentsValidate(t *testing.T) {
	for _, tc := range []struct {
		file string
		kind note.Kind
	}{
		{"triage-doc.json", note.Triage},
		{"rca-doc.json", note.RCA},
	} {
		doc, err := os.ReadFile(filepath.Join(testdataDir, tc.file))
		if err != nil {
			t.Fatal(err)
		}
		if err := note.Validate(tc.kind, doc); err != nil {
			t.Errorf("%s: %v", tc.file, err)
		}
	}
}

func TestTriageThenRCA(t *testing.T) {
	root, notes := newWorkspace(t, fakeClaude)
	chdir(t, root)
	t.Setenv("SIRDAR_FAKE_DOC", filepath.Join(testdataDir, "triage-doc.json"))

	out, errb := mustRun(t, 0, "triage", "OMNI-1")
	if !strings.Contains(out, "OMNI-1") {
		t.Fatalf("digest does not mention the key:\n%s\nstderr:\n%s", out, errb)
	}
	if !strings.Contains(out, "completed") {
		t.Fatalf("digest does not report a completed run:\n%s\nstderr:\n%s", out, errb)
	}
	triageNote := onlyMatch(t, notes, "OMNI-1 *.md")

	writeConfig(t, root, notes, fakeClaudeRCA)
	t.Setenv("SIRDAR_FAKE_DOC", filepath.Join(testdataDir, "rca-doc.json"))

	out, errb = mustRun(t, 0, "rca", "OMNI-1", "--resolution", "fixed by PR 501")
	rcaNote := onlyMatch(t, notes, "OMNI-1 RCA *.md")
	resNote := onlyMatch(t, notes, "OMNI-1 RES *.md")
	for _, path := range []string{rcaNote, resNote} {
		if !strings.Contains(out, path) {
			t.Errorf("rca did not print %s:\n%s\nstderr:\n%s", path, out, errb)
		}
	}
	if _, err := os.Stat(triageNote); err != nil {
		t.Errorf("triage note gone after the rca run: %v", err)
	}

	register := filepath.Join(root, ".sirdar", "register.jsonl")
	data, err := os.ReadFile(register)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 register lines (triage, rca, resolution), got %d:\n%s", len(lines), data)
	}

	out, _ = mustRun(t, 0, "runs")
	if !strings.Contains(out, "OMNI-1") || !strings.Contains(out, "RUN_ID") {
		t.Errorf("runs table:\n%s", out)
	}
	// Neither workspace configured a model, so "claude-fake-5" can only
	// have come from the fake CLI's own init line.
	if !strings.Contains(out, "MODEL") || !strings.Contains(out, "claude-fake-5") {
		t.Errorf("runs table does not name the model that answered:\n%s", out)
	}

	out, _ = mustRun(t, 0, "runs", "OMNI-1", "--json")
	var rows []struct{ RunID, Key, Kind, State, Model string }
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("runs --json: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Errorf("want a triage and an rca run, got %d: %+v", len(rows), rows)
	}
	for _, row := range rows {
		if row.Model != "claude-fake-5" {
			t.Errorf("runs --json row %+v does not carry the reported model", row)
		}
	}

	out, _ = mustRun(t, 0, "register")
	if !strings.Contains(out, "OMNI-1") {
		t.Errorf("register table:\n%s", out)
	}
	if !strings.Contains(out, "MODEL") || !strings.Contains(out, "claude-fake-5") {
		t.Errorf("register table does not name the model that answered:\n%s", out)
	}

	out, _ = mustRun(t, 0, "register", "--markdown")
	for _, want := range []string{"| Issue |", "| Title |", "| Company |"} {
		if !strings.Contains(out, want) {
			t.Errorf("register --markdown is missing the %s column:\n%s", want, out)
		}
	}
	stem := strings.TrimSuffix(filepath.Base(rcaNote), ".md")
	if !strings.Contains(out, "[["+stem+"]]") {
		t.Errorf("register --markdown is missing the rca wiki link %q:\n%s", stem, out)
	}
	// The two cells a human used to fill in: the rca note's title (the
	// newest line for this key wins) and the customer its frontmatter
	// carries.
	if !strings.Contains(out, "Export job times out on large orders") {
		t.Errorf("register --markdown did not carry the note title:\n%s", out)
	}
	if !strings.Contains(out, "شركة") {
		t.Errorf("register --markdown did not carry the company:\n%s", out)
	}
}

func TestTriageDryRun(t *testing.T) {
	root, notes := newWorkspace(t, fakeClaude)
	chdir(t, root)

	mustRun(t, 0, "triage", "--dry-run", "OMNI-1")

	prompts, err := filepath.Glob(filepath.Join(root, ".sirdar", "runs", "OMNI-1", "*", "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 {
		t.Fatalf("want one prompt.md, got %v", prompts)
	}
	body, err := os.ReadFile(prompts[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "OMNI-1") {
		t.Errorf("prompt does not mention the ticket:\n%s", body)
	}
	if written, _ := filepath.Glob(filepath.Join(notes, "*.md")); len(written) != 0 {
		t.Errorf("a dry run wrote notes: %v", written)
	}
}

func TestInitScaffoldsWorkspace(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, _ := mustRun(t, 0, "init", "--templates")
	if !strings.Contains(out, "config.yaml") {
		t.Errorf("init did not report what it created:\n%s", out)
	}

	cfg, err := os.ReadFile(filepath.Join(dir, ".sirdar", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "workspace: " + filepath.Base(dir); !strings.Contains(string(cfg), want) {
		t.Errorf("config does not name the workspace %q:\n%s", want, cfg)
	}
	if strings.Contains(string(cfg), "<name>") {
		t.Errorf("config still holds the name placeholder:\n%s", cfg)
	}

	for _, kind := range []note.Kind{note.Triage, note.RCA, note.Resolution} {
		path := filepath.Join(dir, ".sirdar", "templates", string(kind)+".md.tmpl")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("--templates did not write %s: %v", path, err)
		}
	}
	if books, _ := filepath.Glob(filepath.Join(dir, ".sirdar", "playbooks", "*.md")); len(books) != 5 {
		t.Errorf("want five scaffolded playbooks, got %v", books)
	}

	exclude, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".sirdar/runs/", ".sirdar/register.jsonl", ".sirdar/eval/", ".sirdar/worktrees/"} {
		if !strings.Contains(string(exclude), want) {
			t.Errorf("exclude is missing %q:\n%s", want, exclude)
		}
	}

	// A second init must not clobber a workspace that already exists, and
	// must not double up the exclude lines.
	if _, errb := runCLI(t, "init"); !strings.Contains(errb, "--force") {
		t.Errorf("a second init should refuse without --force, stderr:\n%s", errb)
	}
	mustRun(t, 0, "init", "--force")
	after, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(after), ".sirdar/runs/") != 1 {
		t.Errorf("exclude line was appended twice:\n%s", after)
	}
}

// TestInitInLinkedWorktreeExcludesInMainTree proves `sirdar init` run inside
// a linked worktree finds the exclude file to write: <root>/.git is a file
// there, not a directory, and the file it names belongs to the main tree's
// .git/info, not to anything under the worktree itself.
func TestInitInLinkedWorktreeExcludesInMainTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}

	main := t.TempDir()
	runGit(t, main, "init", "-b", "main", ".")
	if err := exec.Command("git", "-C", main, "var", "GIT_AUTHOR_IDENT").Run(); err != nil {
		t.Skip("git has no author identity configured in this environment")
	}
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, main, "add", "-A")
	runGit(t, main, "commit", "-q", "-m", "init")

	worktree := filepath.Join(t.TempDir(), "linked")
	runGit(t, main, "worktree", "add", "-b", "wt", worktree)

	if info, err := os.Lstat(filepath.Join(worktree, ".git")); err != nil || info.IsDir() {
		t.Fatalf("expected %s/.git to be a file (a linked worktree), got err=%v isDir=%v", worktree, err, info != nil && info.IsDir())
	}

	chdir(t, worktree)
	mustRun(t, 0, "init")

	exclude, err := os.ReadFile(filepath.Join(main, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("the main tree's exclude file was not written: %v", err)
	}
	for _, want := range []string{".sirdar/runs/", ".sirdar/register.jsonl", ".sirdar/eval/", ".sirdar/worktrees/"} {
		if !strings.Contains(string(exclude), want) {
			t.Errorf("main tree's exclude is missing %q:\n%s", want, exclude)
		}
	}
	if _, err := os.Stat(filepath.Join(worktree, ".git", "info", "exclude")); err == nil {
		t.Errorf("exclude should not be written under the worktree's own .git")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDoctorReportsBrokenWorkspace(t *testing.T) {
	root, notes := newWorkspace(t, fakeClaude)
	writeConfigWith(t, root, notes, fakeClaude, filepath.Join(t.TempDir(), "nonexistent-adapter"))
	chdir(t, root)

	out, errb := runCLI(t, "doctor")
	if !strings.Contains(out, "[XX]") {
		t.Errorf("doctor found nothing wrong with a broken adapter:\n%s\nstderr:\n%s", out, errb)
	}
	if !strings.Contains(out, "[OK]") {
		t.Errorf("doctor reported no passing check:\n%s", out)
	}
}

// TestAgyIsDisabledFromTheCommandLine covers the two ways an operator
// meets the disabled provider: a workspace that still names it, which no
// command but doctor will load, and `--provider agy` on a workspace that
// does not.
func TestAgyIsDisabledFromTheCommandLine(t *testing.T) {
	const refusal = "provider agy is disabled: Google's Antigravity terms do not allow driving " +
		"the CLI from another program; choose claude, codex, openai, acp, qwen or cursor"

	root, _ := newWorkspace(t, fakeClaude)
	chdir(t, root)

	// The override is refused before any ticket is fetched, on a
	// workspace whose own provider is perfectly fine.
	_, errb := mustRun(t, 1, "triage", "OMNI-1", "--provider", "agy")
	if !strings.Contains(errb, refusal) {
		t.Errorf("triage --provider agy:\n%s\nwant %q", errb, refusal)
	}
	_, errb = mustRun(t, 1, "rca", "OMNI-1", "--provider", "agy")
	if !strings.Contains(errb, refusal) {
		t.Errorf("rca --provider agy:\n%s\nwant %q", errb, refusal)
	}

	// A workspace that names the provider does not load at all…
	cfgPath := filepath.Join(root, ".sirdar", "config.yaml")
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	disabled := strings.Replace(string(body), "provider: claude", "provider: agy", 1)
	if err := os.WriteFile(cfgPath, []byte(disabled), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errb = mustRun(t, exitUsage, "triage", "OMNI-1")
	if !strings.Contains(errb, refusal) {
		t.Errorf("triage on a disabled workspace:\n%s\nwant %q", errb, refusal)
	}

	// …except under doctor, which is the command an operator runs to find
	// out why, and answers with one row saying so.
	out, errb := runCLI(t, "doctor")
	if !strings.Contains(out, "[XX] agy — disabled (Antigravity terms)") {
		t.Errorf("doctor on a disabled workspace:\n%s\nstderr:\n%s", out, errb)
	}

	// The acknowledgement brings the workspace back: the adapter is still
	// in the tree and still drives the CLI for anybody who accepts the
	// risk. The binary does not exist here, so the row fails — what
	// matters is that it is the binary's row and not the refusal.
	acked := strings.Replace(disabled, "provider: agy", "provider: agy\nagy:\n  acknowledgeTerms: true", 1)
	if err := os.WriteFile(cfgPath, []byte(acked), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ = runCLI(t, "doctor")
	if strings.Contains(out, "disabled (Antigravity terms)") {
		t.Errorf("doctor still reports the acknowledged provider disabled:\n%s", out)
	}
}

// A warning is not a failure: a doctor run whose worst row only warns
// prints [!!], says so in the summary, and still exits 0. The exit code is
// what a CI gate reads, which is the whole point of the third state.
func TestDoctorWarningsDoNotFailTheRun(t *testing.T) {
	var out bytes.Buffer
	checks := []app.Check{
		{Name: "config", OK: true, Level: "ok", Detail: "valid"},
		{Name: "mcp", OK: true, Level: "warn", Detail: "the agent will have no MCP tools"},
	}
	if code := printChecks(&out, checks); code != 0 {
		t.Errorf("exit %d on a warning-only report, want 0:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "[!!] mcp") {
		t.Errorf("the warning row should be marked [!!]:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1 of 2 checks warned") {
		t.Errorf("the summary should count the warning:\n%s", out.String())
	}

	out.Reset()
	checks = append(checks, app.Check{Name: "notes.dir", Level: "fail", Detail: "unwritable"})
	if code := printChecks(&out, checks); code != 1 {
		t.Errorf("exit %d on a failed check, want 1:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "[XX] notes.dir") {
		t.Errorf("the failing row should be marked [XX]:\n%s", out.String())
	}
	// One summary line naming both counts, not the warning count on one
	// line and the failure count on the next.
	if !strings.Contains(out.String(), "1 of 3 checks failed, 1 warned") {
		t.Errorf("the summary should name both counts on one line:\n%s", out.String())
	}
	if strings.Contains(out.String(), "checks warned\n") {
		t.Errorf("the warning count should not also print its own summary line:\n%s", out.String())
	}
}

func TestCommandsWithoutWorkspace(t *testing.T) {
	chdir(t, t.TempDir())
	for _, args := range [][]string{
		{"triage", "OMNI-1"}, {"rca", "OMNI-1"}, {"resume", "r1"}, {"steer", "r1", "go on"}, {"runs"}, {"register"}, {"doctor"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%v: want exit 2, got %d (stderr %q)", args, code, errb.String())
		}
		if !strings.Contains(errb.String(), "sirdar init") {
			t.Errorf("%v: stderr should point at init, got %q", args, errb.String())
		}
	}
}

func TestUsageErrors(t *testing.T) {
	chdir(t, t.TempDir())
	for _, args := range [][]string{
		{"triage"},                  // no keys
		{"rca"},                     // no key
		{"rca", "OMNI-1", "OMNI-2"}, // rca takes exactly one
		{"resume"},                  // no run id
		{"steer", "r1"},             // no instruction
		{"triage", "--nope", "OMNI-1"},
		{"runs", "A", "B"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%v: want exit 2, got %d (stderr %q)", args, code, errb.String())
		}
		if !strings.Contains(errb.String(), "usage:") {
			t.Errorf("%v: stderr should show usage, got %q", args, errb.String())
		}
	}
}

func TestRCAResolutionFromFile(t *testing.T) {
	root, notes := newWorkspace(t, fakeClaude)
	chdir(t, root)
	t.Setenv("SIRDAR_FAKE_DOC", filepath.Join(testdataDir, "triage-doc.json"))
	mustRun(t, 0, "triage", "OMNI-1")

	resolution := filepath.Join(t.TempDir(), "resolution.md")
	if err := os.WriteFile(resolution, []byte("Ran the remediation SQL after approval.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, root, notes, fakeClaudeRCA)
	t.Setenv("SIRDAR_FAKE_DOC", filepath.Join(testdataDir, "rca-doc.json"))
	mustRun(t, 0, "rca", "OMNI-1", "--resolution", "@"+resolution)

	matches, err := filepath.Glob(filepath.Join(root, ".sirdar", "runs", "OMNI-1", "*", "bundle", "resolution.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("want one bundle resolution.md, got %v", matches)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "remediation SQL") {
		t.Errorf("@file resolution was not read: %q", body)
	}
}

// newWorkspace builds a workspace whose tracker and helpdesk are both the
// example file adapter and whose provider is one of the installed fake
// CLIs. It returns the workspace root and its notes directory.
func newWorkspace(t *testing.T, cli string) (root, notes string) {
	t.Helper()
	root = t.TempDir()
	notes = filepath.Join(t.TempDir(), "notes")
	writeConfig(t, root, notes, cli)
	if err := prompt.ScaffoldPlaybooks(filepath.Join(root, ".sirdar", "playbooks")); err != nil {
		t.Fatal(err)
	}
	return root, notes
}

func writeConfig(t *testing.T, root, notes, cli string) {
	t.Helper()
	writeConfigWith(t, root, notes, cli, fileAdapter)
}

func writeConfigWith(t *testing.T, root, notes, cli, adapter string) {
	t.Helper()
	command := adapter + " -file " + filepath.Join(testdataDir, "tickets.json")
	body := fmt.Sprintf(`workspace: sirdar-test
provider: claude
sources:
  tracker:
    adapter: exec
    command: %s
  helpdesk:
    adapter: exec
    command: %s
notes:
  dir: %s
playbooks: .sirdar/playbooks
providers:
  claude:
    path: %s
`, command, command, notes, cli)

	if err := os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// chdir moves into dir for the rest of the test, since every command but
// init resolves its workspace from the working directory.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatal(err)
		}
	})
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	run(args, &out, &errb)
	return out.String(), errb.String()
}

func mustRun(t *testing.T, want int, args ...string) (stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != want {
		t.Fatalf("%v: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", args, code, want, out.String(), errb.String())
	}
	return out.String(), errb.String()
}

// onlyMatch asserts that exactly one entry in dir matches pattern and
// returns its path.
func onlyMatch(t *testing.T, dir, pattern string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("want exactly one %s in %s, got %v", pattern, dir, matches)
	}
	return matches[0]
}

// The retrospective flags only mean anything together: --pr and --as-of are
// read by --retro, and --retro without a pull request has no ground truth to
// score against. Each is refused before the workspace is even loaded, so the
// operator is told what they meant rather than what failed later.
func TestGoldenAddRetroFlagCombinations(t *testing.T) {
	chdir(t, t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"golden", "add", "OMNI-1", "--pr", "https://github.com/a/b/pull/1"}, want: "only read with --retro"},
		{args: []string{"golden", "add", "OMNI-1", "--as-of", "2026-03-02T10:00:00Z"}, want: "only read with --retro"},
		{args: []string{"golden", "add", "OMNI-1", "--retro"}, want: "at least one --pr"},
		{args: []string{"golden", "add", "OMNI-1", "--retro", "--pr", "https://github.com/a/b/pull/1", "--as-of", "yesterday"}, want: "RFC3339"},
	} {
		var out, errb bytes.Buffer
		if code := run(tc.args, &out, &errb); code != 2 {
			t.Errorf("%v: want exit 2, got %d (stderr %q)", tc.args, code, errb.String())
		}
		if !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%v: stderr = %q, want it to mention %q", tc.args, errb.String(), tc.want)
		}
	}
}

// TestEvalRetroFlags covers the retro half of `sirdar eval` at the command
// level, which is all of it that can be checked without a model: the two
// flags that only mean something with --retro say so, the usage line names
// all three, and a golden set with nothing to replay names the file it was
// looking for rather than printing an empty table.
func TestEvalRetroFlags(t *testing.T) {
	root, _ := newWorkspace(t, fakeClaude)
	chdir(t, root)

	var out, errb bytes.Buffer
	if code := run([]string{"eval", "--with-rca"}, &out, &errb); code != 2 {
		t.Errorf("--with-rca without --retro: exit %d, want 2 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--retro") {
		t.Errorf("the refusal does not say which flag they belong to: %q", errb.String())
	}

	out.Reset()
	errb.Reset()
	run([]string{"eval", "--nope"}, &out, &errb)
	for _, want := range []string{"--retro", "--with-rca", "--rubric"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the usage line does not mention %s:\n%s", want, errb.String())
		}
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"eval", "--retro", "--golden", t.TempDir()}, &out, &errb); code != 1 {
		t.Errorf("a golden set with no retro entry: exit %d, want 1 (stderr %q)", code, errb.String())
	}
	if !strings.Contains(errb.String(), "retro.json") {
		t.Errorf("the message does not name the file it was looking for: %q", errb.String())
	}
}
