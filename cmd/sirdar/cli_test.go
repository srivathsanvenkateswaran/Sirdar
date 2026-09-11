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

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
)

// fileAdapter is the example file-backed source adapter, built once by
// TestMain; testdataDir is this package's testdata as an absolute path,
// captured before any test changes directory.
var (
	fileAdapter string
	testdataDir string
)

func TestMain(m *testing.M) {
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
	fileAdapter = filepath.Join(tmp, "file-adapter")
	build := exec.Command("go", "build", "-o", fileAdapter, "../../examples/adapters/file")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "cli test: build file adapter: %v\n%s", err, out)
		os.RemoveAll(tmp)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
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
	root, notes := newWorkspace(t, "fakeclaude.sh")
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

	writeConfig(t, root, notes, "fakeclaude-rca.sh")
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

	out, _ = mustRun(t, 0, "runs", "OMNI-1", "--json")
	var rows []struct{ RunID, Key, Kind, State string }
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("runs --json: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Errorf("want a triage and an rca run, got %d: %+v", len(rows), rows)
	}

	out, _ = mustRun(t, 0, "register")
	if !strings.Contains(out, "OMNI-1") {
		t.Errorf("register table:\n%s", out)
	}

	out, _ = mustRun(t, 0, "register", "--markdown")
	if !strings.Contains(out, "| Issue |") {
		t.Errorf("register --markdown is missing the vault header:\n%s", out)
	}
	stem := strings.TrimSuffix(filepath.Base(rcaNote), ".md")
	if !strings.Contains(out, "[["+stem+"]]") {
		t.Errorf("register --markdown is missing the rca wiki link %q:\n%s", stem, out)
	}
}

func TestTriageDryRun(t *testing.T) {
	root, notes := newWorkspace(t, "fakeclaude.sh")
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
	for _, want := range []string{".sirdar/runs/", ".sirdar/register.jsonl", ".sirdar/eval/"} {
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

func TestDoctorReportsBrokenWorkspace(t *testing.T) {
	root, notes := newWorkspace(t, "fakeclaude.sh")
	writeConfigWith(t, root, notes, filepath.Join(testdataDir, "fakeclaude.sh"), "/nonexistent/adapter")
	chdir(t, root)

	out, errb := runCLI(t, "doctor")
	if !strings.Contains(out, "[!!]") {
		t.Errorf("doctor found nothing wrong with a broken adapter:\n%s\nstderr:\n%s", out, errb)
	}
	if !strings.Contains(out, "[OK]") {
		t.Errorf("doctor reported no passing check:\n%s", out)
	}
}

func TestCommandsWithoutWorkspace(t *testing.T) {
	chdir(t, t.TempDir())
	for _, args := range [][]string{
		{"triage", "OMNI-1"}, {"rca", "OMNI-1"}, {"resume", "r1"}, {"runs"}, {"register"}, {"doctor"},
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
	root, notes := newWorkspace(t, "fakeclaude.sh")
	chdir(t, root)
	t.Setenv("SIRDAR_FAKE_DOC", filepath.Join(testdataDir, "triage-doc.json"))
	mustRun(t, 0, "triage", "OMNI-1")

	resolution := filepath.Join(t.TempDir(), "resolution.md")
	if err := os.WriteFile(resolution, []byte("Ran the remediation SQL after approval.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, root, notes, "fakeclaude-rca.sh")
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
// example file adapter and whose provider is one of the fake scripts in
// testdata. It returns the workspace root and its notes directory.
func newWorkspace(t *testing.T, script string) (root, notes string) {
	t.Helper()
	root = t.TempDir()
	notes = filepath.Join(t.TempDir(), "notes")
	writeConfig(t, root, notes, script)
	if err := prompt.ScaffoldPlaybooks(filepath.Join(root, ".sirdar", "playbooks")); err != nil {
		t.Fatal(err)
	}
	return root, notes
}

func writeConfig(t *testing.T, root, notes, script string) {
	t.Helper()
	writeConfigWith(t, root, notes, filepath.Join(testdataDir, script), fileAdapter)
}

func writeConfigWith(t *testing.T, root, notes, script, adapter string) {
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
`, command, command, notes, script)

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
