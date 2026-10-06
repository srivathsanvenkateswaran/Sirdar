package gather

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
	"github.com/srivathsanvenkateswaran/sirdar/internal/testbin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

func numbers(refs []PRRef) []int {
	out := make([]int, len(refs))
	for i, r := range refs {
		out[i] = r.Number
	}
	return out
}

func TestPullRequestRefs(t *testing.T) {
	list := []repos.Repo{{Name: "systems", Origin: "git@github.com:silq/OXO.Systems.git"}}
	var more []string
	for n := 514; n < 520; n++ {
		more = append(more, "https://github.com/silq/OXO.Systems/pull/"+strconv.Itoa(n))
	}
	text := "See https://github.com/silq/OXO.Systems/pull/512 and silq/OXO.Systems#513.\n" +
		"Also other/Thing#9, Zoho #29809, and https://github.com/silq/OXO.Systems/pull/512 again.\n" +
		"Upstream: https://github.com/elsewhere/lib/pull/7\n" +
		strings.Join(more, "\n")

	take, skipped := PullRequests(text, list, false, 5, "")
	if got, want := numbers(take), []int{512, 513, 514, 515, 516}; !reflect.DeepEqual(got, want) {
		t.Fatalf("take = %v, want %v", got, want)
	}
	if take[0].URL != "https://github.com/silq/OXO.Systems/pull/512" || take[1].URL != "https://github.com/silq/OXO.Systems/pull/513" {
		t.Fatalf("URLs not canonical: %q %q", take[0].URL, take[1].URL)
	}
	for _, r := range append(append([]PRRef(nil), take...), skipped...) {
		if r.Number == 29809 {
			t.Fatal("a bare #29809 was read as a pull request")
		}
		if r.Owner == "other" {
			t.Fatal("other/Thing#9 names no configured repository and must be ignored")
		}
	}
	if len(skipped) != 1 || skipped[0].Owner != "elsewhere" || skipped[0].Number != 7 {
		t.Fatalf("skipped = %+v, want the elsewhere/lib URL alone", skipped)
	}

	take, skipped = PullRequests(text, list, true, 5, "")
	if got, want := numbers(take), []int{512, 513, 7, 514, 515}; !reflect.DeepEqual(got, want) {
		t.Fatalf("anyRepo take = %v, want %v", got, want)
	}
	if len(skipped) != 0 {
		t.Fatalf("anyRepo skipped = %+v, want none", skipped)
	}

	take, _ = PullRequests(text, list, false, 5, "https://github.com/SILQ/oxo.systems/pull/512")
	if got, want := numbers(take), []int{513, 514, 515, 516, 517}; !reflect.DeepEqual(got, want) {
		t.Fatalf("with skip take = %v, want %v", got, want)
	}
}

func TestValuesAndValueRule(t *testing.T) {
	v := config.GatherVariable{
		Pattern: `(?i)(?:domain|الدومين)\W{0,6}([a-z0-9][a-z0-9-]{1,62})`,
		From:    config.GatherSources,
		Max:     3,
	}
	in := Input{
		Bundle:      ticket.Bundle{Thread: ticket.Thread{{Text: "الدومين: dama"}}},
		Instruction: "domain = dama, Domain: acme",
	}
	values, rejected := Values(v, in)
	if want := []string{"dama", "acme"}; !reflect.DeepEqual(values, want) {
		t.Fatalf("Values = %v, want %v", values, want)
	}
	if len(rejected) != 0 {
		t.Fatalf("rejected = %v, want none", rejected)
	}

	for _, bad := range []string{"a;b", "-x", "$(id)", "a b", strings.Repeat("a", 129), "", "a'b", "a\x01b", `a\b`} {
		if ValidValue(bad) {
			t.Errorf("ValidValue(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{"dama", "4376", strings.Repeat("a", 128), "acme.example"} {
		if !ValidValue(good) {
			t.Errorf("ValidValue(%q) = false, want true", good)
		}
	}

	// A capture that fails the rule is reported and never used.
	v.Pattern = `id=(\S+)`
	values, rejected = Values(v, Input{Instruction: "id=ok1 id=-rf id=ok2"})
	if !reflect.DeepEqual(values, []string{"ok1", "ok2"}) || !reflect.DeepEqual(rejected, []string{"-rf"}) {
		t.Fatalf("Values = %v rejected %v, want [ok1 ok2] and [-rf]", values, rejected)
	}
}

func TestRender(t *testing.T) {
	expand := func(s string) string { return "/home/op/" + strings.TrimPrefix(s, "~/") }
	argv, err := Render([]string{"git", "-C", "~/x", "log", "--grep={{d}}"}, map[string]string{"d": "v1"}, expand)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"git", "-C", "/home/op/x", "log", "--grep=v1"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("Render = %v, want %v", argv, want)
	}
	if _, err := Render([]string{"{{d}}"}, map[string]string{"d": "v1"}, expand); err == nil {
		t.Fatal("a variable in argv[0] must be an error")
	}

	args, err := RenderArgs(map[string]any{"table": "{{d}}", "n": 3, "list": []any{"a-{{d}}"}}, map[string]string{"d": "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != `{"list":["a-v1"],"n":3,"table":"v1"}` {
		t.Fatalf("RenderArgs = %s", args)
	}

	if got := DisplayLine([]string{"git", "log", "--format=%h %s", "it's"}); got != `git log '--format=%h %s' 'it'\''s'` {
		t.Fatalf("DisplayLine = %s", got)
	}
}

// fakeIn installs the command fake inside the workspace, where the
// allow-list's path check lets a program be named by its absolute path.
func fakeIn(t *testing.T, root string) string {
	t.Helper()
	return testbin.Install(t, filepath.Join(root, "bin"), "gatherfake", "gatherfake")
}

func TestDeniedStepNeverRuns(t *testing.T) {
	root := t.TempDir()
	fake := fakeIn(t, root)
	cfg := newWorkspace(t, root, "gather:\n  steps:\n    - name: greet\n      command: ["+q(fake)+", hello]\n")
	in := runInput(t, cfg, "triage")
	log := filepath.Join(t.TempDir(), "log")
	in.Env = append(in.Env, "GATHER_FAKE_LOG="+log)

	res := Run(context.Background(), in)
	if len(res.Entries) != 1 || res.Entries[0].Status != StatusDenied {
		t.Fatalf("entries = %+v, want one denied row", res.Entries)
	}
	if !strings.Contains(res.Entries[0].Reason, "permissions.bash") {
		t.Fatalf("reason = %q, want the policy's message", res.Entries[0].Reason)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("the fake ran: %v", err)
	}
}

func TestAllowedStepRuns(t *testing.T) {
	root := t.TempDir()
	fake := fakeIn(t, root)
	cfg := newWorkspace(t, root, "gather:\n  allow: ["+q(fake+" *")+"]\n  steps:\n    - name: greet\n      command: ["+q(fake)+", hello]\n")
	in := runInput(t, cfg, "triage")
	var started int
	var done []Entry
	in.Started = func(n int) { started = n }
	in.Done = func(e Entry) { done = append(done, e) }

	res := Run(context.Background(), in)
	if started != 1 || len(done) != 1 {
		t.Fatalf("Started(%d), %d Done calls; want 1 and 1", started, len(done))
	}
	e := res.Entries[0]
	if e.Status != StatusOK || e.File != "context/greet.md" || e.Kind != KindCommand {
		t.Fatalf("entry = %+v", e)
	}
	body, err := os.ReadFile(filepath.Join(in.ContextDir, "greet.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# greet\n", "- Call: `" + DisplayLine([]string{fake, "hello"}) + "`\n", "- Took: ", " ms\n", "- Status: ok\n", "```\nhello\n```\n"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("greet.md lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "- Values:") || strings.Contains(string(body), "- Reason:") {
		t.Errorf("greet.md has a Values or Reason line it should omit:\n%s", body)
	}
	if e.Bytes != len("hello\n") {
		t.Errorf("Bytes = %d, want %d", e.Bytes, len("hello\n"))
	}
}

func TestStepTimeoutKillsTheGroup(t *testing.T) {
	root := t.TempDir()
	fake := fakeIn(t, root)
	cfg := newWorkspace(t, root, "gather:\n  allow: ["+q(fake+" *")+"]\n  steps:\n    - name: slow\n      timeoutSeconds: 1\n      command: ["+q(fake)+", sleep]\n")
	in := runInput(t, cfg, "triage")

	start := time.Now()
	res := Run(context.Background(), in)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Run took %s, want under 5s", took)
	}
	e := res.Entries[0]
	if e.Status != StatusFailed || e.Reason != "timed out after 1s" {
		t.Fatalf("entry = %+v, want failed: timed out after 1s", e)
	}
}

func TestFailingStepReportsExitAndStderr(t *testing.T) {
	root := t.TempDir()
	fake := fakeIn(t, root)
	cfg := newWorkspace(t, root, "gather:\n  allow: ["+q(fake+" *")+"]\n  steps:\n    - name: broken\n      command: ["+q(fake)+", fail]\n")
	res := Run(context.Background(), runInput(t, cfg, "triage"))
	if e := res.Entries[0]; e.Status != StatusFailed || e.Reason != "exit 3: boom" {
		t.Fatalf("entry = %+v, want failed: exit 3: boom", e)
	}
}

// This test changes the parallel package variable, so it and every test
// that calls Run must stay serial.
func TestByteCapsAndTruncationMarker(t *testing.T) {
	old := parallel
	parallel = 1
	t.Cleanup(func() { parallel = old })

	root := t.TempDir()
	fake := fakeIn(t, root)
	step := func(name string) string {
		return "    - name: " + name + "\n      kinds: [triage]\n      command: [" + q(fake) + ", big]\n"
	}
	cfg := newWorkspace(t, root, "gather:\n  maxBytes: 1024\n  totalBytes: 2048\n  allow: ["+q(fake+" *")+"]\n  steps:\n"+
		step("one")+step("two")+step("three"))
	in := runInput(t, cfg, "triage")

	res := Run(context.Background(), in)
	if e := res.Entries[0]; e.Status != StatusTruncated || e.Bytes != 1024 {
		t.Fatalf("first entry = %+v, want truncated at 1024 bytes", e)
	}
	body, err := os.ReadFile(filepath.Join(in.ContextDir, "one.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(body), "[truncated by Sirdar at 1024 of 307200 bytes]\n") {
		t.Fatalf("one.md does not end with the marker:\n…%s", body[len(body)-80:])
	}
	capped := 0
	for _, e := range res.Entries {
		if e.Status == StatusSkipped && e.Reason == "context cap" {
			capped++
		}
	}
	if capped == 0 {
		t.Fatalf("no row was skipped for the context cap: %+v", res.Entries)
	}
}

// mcpWorkspace is a workspace whose .mcp.json starts the fake MCP server
// as "fake", with the gather step the MCP tests share.
func mcpWorkspace(t *testing.T, extra string) *config.Config {
	t.Helper()
	root := t.TempDir()
	fake := testbin.Install(t, t.TempDir(), "fakemcp", "fakemcp")
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"fake":{"command":`+q(fake)+`}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return newWorkspace(t, root, extra+`gather:
  variables:
    domain:
      pattern: '(?i)domain\W{0,6}([a-z0-9][a-z0-9-]{1,62})'
  steps:
    - name: rows
      mcp:
        server: fake
        tool: list_rows
        args: {table: "{{domain}}"}
`)
}

func TestMCPStep(t *testing.T) {
	cfg := mcpWorkspace(t, "")
	in := runInput(t, cfg, "triage")
	in.Instruction = "domain: dama"
	res := Run(context.Background(), in)
	e := res.Entries[0]
	if e.Status != StatusOK || e.Kind != KindMCP || e.Name != "rows-dama" || e.File != "context/rows-dama.md" {
		t.Fatalf("entry = %+v", e)
	}
	if e.Summary != "fake list_rows(table=dama)" {
		t.Fatalf("Summary = %q", e.Summary)
	}
	if e.Call != `fake.list_rows({"table":"dama"})` {
		t.Fatalf("Call = %q", e.Call)
	}
	out, err := ReadOutput(filepath.Join(in.ContextDir, "rows-dama.md"))
	if err != nil {
		t.Fatal(err)
	}
	if out != "rows of dama" {
		t.Fatalf("output = %q, want %q", out, "rows of dama")
	}
	body, _ := os.ReadFile(filepath.Join(in.ContextDir, "rows-dama.md"))
	if !strings.Contains(string(body), "- Values: domain=dama\n") {
		t.Fatalf("rows-dama.md lacks the Values line:\n%s", body)
	}

	denied := mcpWorkspace(t, "permissions:\n  mcp: [mcp__fake__other]\n")
	in = runInput(t, denied, "triage")
	in.Instruction = "domain: dama"
	log := filepath.Join(t.TempDir(), "log")
	in.Env = append(in.Env, "GATHER_FAKE_LOG="+log)
	res = Run(context.Background(), in)
	if e := res.Entries[0]; e.Status != StatusDenied || e.Reason == "" {
		t.Fatalf("entry = %+v, want denied with a reason", e)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("the server was started for a denied tool")
	}
}

func TestStepWithoutAValueIsSkipped(t *testing.T) {
	cfg := newWorkspace(t, "", `gather:
  variables:
    domain:
      pattern: 'domain=(\S+)'
  steps:
    - name: rows
      mcp: {server: fake, tool: list_rows, args: {table: "{{domain}}"}}
`)
	in := runInput(t, cfg, "triage")
	in.Instruction = "domain=-rf and nothing else"
	res := Run(context.Background(), in)
	e := res.Entries[0]
	if e.Status != StatusSkipped || e.Reason != `no domain in the ticket; value "-rf" rejected` {
		t.Fatalf("entry = %+v", e)
	}
}

func TestHistory(t *testing.T) {
	root := t.TempDir()
	b := historyRepo(t, root)
	cfg := newWorkspace(t, root, "")
	g := &gatherer{in: Input{Config: cfg, Env: os.Environ()}, cfg: cfg, now: time.Now}

	h := g.history(context.Background(), root, b, true, []string{"a.txt", "dir with (parens)/b.txt"})
	if h.status != StatusOK {
		t.Fatalf("history = %+v", h)
	}
	if want := "Merged as " + b[:7] + "; first released in v1.1.0 ("; !strings.HasPrefix(h.output, want) {
		t.Fatalf("output opens %q, want %q", strings.SplitN(h.output, "\n", 2)[0], want)
	}
	if !strings.Contains(strings.SplitN(h.output, "\n", 2)[0], "previous release v1.0.0") {
		t.Fatalf("first line lacks the previous release: %q", strings.SplitN(h.output, "\n", 2)[0])
	}
	between := h.output[strings.Index(h.output, "## Commits on these files between releases"):strings.Index(h.output, "## Before the change")]
	if !strings.Contains(between, b[:7]) || !strings.Contains(between, "B: the change") {
		t.Fatalf("between-releases log lacks B:\n%s", h.output)
	}
	if !strings.Contains(h.summary, "1 commits on these files") {
		t.Fatalf("summary = %q", h.summary)
	}

	// The path with spaces and parentheses is passed as a literal
	// pathspec, and still matches B on its own.
	h = g.history(context.Background(), root, b, true, []string{"dir with (parens)/b.txt"})
	if h.status != StatusOK || !strings.Contains(h.output, "B: the change") {
		t.Fatalf("literal pathspec history = %+v", h)
	}

	h = g.history(context.Background(), root, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", true, []string{"a.txt"})
	if h.status != StatusFailed || !strings.Contains(h.reason, "it is behind origin") {
		t.Fatalf("missing sha history = %+v", h)
	}
}

func TestPRBuiltin(t *testing.T) {
	root := t.TempDir()
	b := historyRepo(t, root)
	git(t, root, "remote", "add", "origin", "https://github.com/silq/OXO.Systems.git")
	cfg := newWorkspace(t, root, "")

	ghDir := t.TempDir()
	testbin.Install(t, ghDir, "gh", "fakegh")
	in := runInput(t, cfg, "triage")
	in.Instruction = "Regressed by https://github.com/silq/OXO.Systems/pull/512"
	in.Env = append(withPath(os.Environ(), ghDir+string(os.PathListSeparator)+os.Getenv("PATH")), "GATHER_FAKE_SHA="+b)

	res := Run(context.Background(), in)
	if len(res.Entries) != 2 {
		t.Fatalf("entries = %+v, want a pr and a history row", res.Entries)
	}
	pr, hist := res.Entries[0], res.Entries[1]
	if pr.Name != "pr-OXO.Systems-512" || pr.Status != StatusOK || pr.Diff != "context/pr-OXO.Systems-512.diff" || pr.DiffBytes == 0 {
		t.Fatalf("pr row = %+v", pr)
	}
	if want := `PR silq/OXO.Systems#512 "Fix the export of empty reports" · merged 2026-09-30 · 2 files`; pr.Summary != want {
		t.Fatalf("pr summary = %q, want %q", pr.Summary, want)
	}
	out, err := ReadOutput(filepath.Join(in.ContextDir, "pr-OXO.Systems-512.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PR silq/OXO.Systems#512: Fix the export of empty reports", "## Files (2)", "- a.txt (+3 −1)", "## Body"} {
		if !strings.Contains(out, want) {
			t.Errorf("pr file lacks %q:\n%s", want, out)
		}
	}
	if hist.Name != "history-OXO.Systems-512" || hist.Kind != KindHistory || hist.Status != StatusOK {
		t.Fatalf("history row = %+v", hist)
	}
}

// withPath is env with PATH replaced.
func withPath(env []string, path string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+path)
}

func TestWindowsBatchRefused(t *testing.T) {
	cfg := newWorkspace(t, "", "gather:\n  allow: [\"x *\"]\n  steps:\n    - name: batch\n      command: [x, hello]\n")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.bat"), []byte("@echo hello\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := runInput(t, cfg, "triage")
	in.GOOS = "windows"
	in.Env = []string{"PATH=" + dir}
	res := Run(context.Background(), in)
	if e := res.Entries[0]; e.Status != StatusDenied || e.Reason != "a batch file would be run through cmd.exe" {
		t.Fatalf("entry = %+v", e)
	}
}

func TestNoPlanWritesNothing(t *testing.T) {
	cfg := newWorkspace(t, "", "")
	in := runInput(t, cfg, "triage")
	in.Bundle = ticket.Bundle{Tracker: &ticket.TrackerTicket{Key: "T-1", Title: "Nothing to see"}}
	called := false
	in.Started = func(int) { called = true }
	res := Run(context.Background(), in)
	if called || len(res.Entries) != 0 {
		t.Fatalf("Started called %v, entries %+v; want neither", called, res.Entries)
	}
	if _, err := os.Stat(in.ContextDir); !os.IsNotExist(err) {
		t.Fatalf("context dir exists: %v", err)
	}
}

func TestNoReferenceSessionWritesOnlyWithAFile(t *testing.T) {
	cfg := mcpWorkspace(t, "")
	in := runInput(t, cfg, "session")
	res := Run(context.Background(), in)
	if len(res.Entries) != 1 || res.Entries[0].Status != StatusSkipped {
		t.Fatalf("entries = %+v, want one skipped row", res.Entries)
	}
	if _, err := os.Stat(in.ContextDir); !os.IsNotExist(err) {
		t.Fatalf("context dir exists: %v", err)
	}

	// The same skipped row on a run with a ticket is still filed, so the
	// run can say the step was considered.
	in = runInput(t, cfg, "triage")
	in.Bundle = ticket.Bundle{Tracker: &ticket.TrackerTicket{Key: "T-1", Title: "Exports are empty"}}
	Run(context.Background(), in)
	got, err := ReadIndex(in.ContextDir)
	if err != nil || len(got) != 1 || got[0].Status != StatusSkipped {
		t.Fatalf("ReadIndex = %+v, %v; want the one skipped row", got, err)
	}
}

func TestIndexRoundTrip(t *testing.T) {
	root := t.TempDir()
	fake := fakeIn(t, root)
	cfg := newWorkspace(t, root, "gather:\n  allow: ["+q(fake+" *")+"]\n  steps:\n    - name: greet\n      command: ["+q(fake)+", hello]\n    - name: nope\n      command: [rm, -rf, x]\n")
	in := runInput(t, cfg, "triage")
	res := Run(context.Background(), in)

	got, err := ReadIndex(in.ContextDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, res.Entries) {
		t.Fatalf("ReadIndex = %+v\nRun      = %+v", got, res.Entries)
	}
	if got[1].Status != StatusDenied {
		t.Fatalf("rm row = %+v, want denied", got[1])
	}
	out, err := ReadOutput(filepath.Join(in.ContextDir, "greet.md"))
	if err != nil || out != "hello" {
		t.Fatalf("ReadOutput = %q, %v", out, err)
	}

	missing, err := ReadIndex(t.TempDir())
	if missing != nil || err != nil {
		t.Fatalf("ReadIndex of an empty dir = %v, %v; want nil, nil", missing, err)
	}
}

func TestPreview(t *testing.T) {
	root := t.TempDir()
	fake := fakeIn(t, root)
	cfg := newWorkspace(t, root, "gather:\n  allow: ["+q(fake+" *")+"]\n  steps:\n    - name: greet\n      command: ["+q(fake)+", hello]\n    - name: nope\n      command: [rm, -rf, x]\n")
	rows, _ := Preview(cfg, os.Environ())
	if len(rows) != 2 || rows[0].Verdict != PreviewAllowed || rows[1].Verdict != PreviewDenied {
		t.Fatalf("rows = %+v", rows)
	}

	mcfg := mcpWorkspace(t, "")
	rows, _ = Preview(mcfg, os.Environ())
	if len(rows) != 1 || rows[0].Verdict != PreviewAllowed || !rows[0].ServerPresent || rows[0].Call != `fake.list_rows({"table":"x"})` {
		t.Fatalf("mcp rows = %+v", rows)
	}
}
