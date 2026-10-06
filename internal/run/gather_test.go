package run

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/gather"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/testbin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// fakeGatherStep is a configured gather step's program. It appends a line
// to GATHER_STEP_LOG saying it ran and what phase the run's state.json
// was in at that moment (it runs in the workspace root, where the run
// directories are). "env" prints its environment; anything else prints a
// short fact for the prompt to inline.
func fakeGatherStep() int {
	args := testbin.Args()
	phase := "none"
	if matches, _ := filepath.Glob(filepath.Join(".sirdar", "runs", "*", "*", "state.json")); len(matches) == 1 {
		if data, err := os.ReadFile(matches[0]); err == nil {
			var st store.State
			if json.Unmarshal(data, &st) == nil {
				phase = st.Phase
			}
		}
	}
	if path := os.Getenv("GATHER_STEP_LOG"); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "ran %s phase=%s\n", strings.Join(args, " "), phase)
			f.Close()
		}
	}
	if len(args) > 0 && args[0] == "env" {
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
		return 0
	}
	fmt.Println("company 42 is Acme")
	return 0
}

// fakeRunGH answers `gh pr view` and `gh pr diff` for one pull request.
func fakeRunGH() int {
	args := testbin.Args()
	if len(args) < 3 || args[0] != "pr" {
		return testbin.Fail("fake gh: unexpected %v", args)
	}
	switch args[1] {
	case "view":
		b, _ := json.Marshal(map[string]any{
			"number": 7, "title": "Fix the save button", "state": "MERGED", "url": args[2],
			"author": map[string]string{"login": "dev"}, "baseRefName": "main", "headRefName": "fix/save",
			"headRefOid": "0123456789abcdef0123456789abcdef01234567", "mergedAt": "2026-09-30T10:00:00Z",
			"files": []map[string]any{{"path": "save.go", "additions": 1, "deletions": 1}},
			"body":  "Saving no longer errors.",
		})
		os.Stdout.Write(b)
	case "diff":
		fmt.Print("diff --git a/save.go b/save.go\n--- a/save.go\n+++ b/save.go\n@@ -1 +1 @@\n-old\n+new\n")
	default:
		return testbin.Fail("fake gh: unexpected %v", args)
	}
	return 0
}

// fakeHangMCP is an MCP server that starts, lists one read-shaped tool and
// never answers a call to it. It writes its pid to HANG_MCP_PID first, so
// a test can tell whether it is still running, and it ignores its stdin
// closing, so only a kill ends it.
func fakeHangMCP() int {
	if path := os.Getenv("HANG_MCP_PID"); path != "" {
		_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	in := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	for in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"hang","version":"1"}}}`+"\n", req.ID)
		case "tools/list":
			fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"list_rows","inputSchema":{"type":"object"}}]}}`+"\n", req.ID)
		case "tools/call":
			time.Sleep(time.Minute)
		}
		out.Flush()
	}
	time.Sleep(time.Minute)
	return 0
}

// gatherRunner is a runner over a workspace with one configured gather
// step running the fake above, allowed or not. The fake is on the PATH
// the run's child environment carries, and logs to the returned path.
func gatherRunner(t *testing.T, stub *stubProvider, allowed bool) (*Runner, string) {
	t.Helper()
	bin := t.TempDir()
	testbin.Install(t, bin, "gatherstep", "gatherstep")
	allow := ""
	if allowed {
		allow = "  allow: [\"gatherstep *\"]\n"
	}
	cfg := newWorkspaceWith(t, configYAML+"gather:\n"+allow+"  steps:\n    - name: company\n      command: [gatherstep, lookup]\n")
	r := newRunner(cfg, stub, stubTracker{}, stubHelpdesk{})
	log := filepath.Join(t.TempDir(), "step.log")
	r.Env = []string{"PATH=" + bin, "GATHER_STEP_LOG=" + log}
	return r, log
}

// stepLog is what the fake step logged, "" when it never ran.
func stepLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type loggedEvent struct {
	Kind    string       `json:"kind"`
	Payload eventPayload `json:"payload"`
}

func loggedEvents(t *testing.T, dir string) []loggedEvent {
	t.Helper()
	var out []loggedEvent
	for _, line := range eventLogLines(t, dir) {
		var ev loggedEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

// The gather's lines are on the event log before anything the provider
// says, so the session screen shows them first, and a short result is
// inlined in the prompt.
func TestGatherEventsComeBeforeTheFirstProviderLine(t *testing.T) {
	stub := &stubProvider{script: replay(usage(1), finalEvent(triageDoc))}
	r, log := gatherRunner(t, stub, true)
	out := triageOne(t, r, Options{NoteOnly: true})
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !strings.Contains(stepLog(t, log), "ran lookup") {
		t.Fatal("the gather step never ran")
	}
	dir := runDir(t, r.Config, out)
	evs := loggedEvents(t, dir)
	var rest []loggedEvent
	for _, ev := range evs {
		if ev.Kind == "system" && strings.HasPrefix(ev.Payload.Text, "Playbooks:") {
			continue
		}
		rest = append(rest, ev)
	}
	if len(rest) < 4 {
		t.Fatalf("events: %+v", evs)
	}
	if rest[0].Kind != "system" || rest[0].Payload.Text != "Gathering context: 1 lookup" {
		t.Errorf("first line %+v, want Gathering context: 1 lookup", rest[0])
	}
	g := rest[1].Payload
	if rest[1].Kind != "gather" || g.Phase != "gather" || g.Decision != "ok" || g.Tool != "company" ||
		g.Text != "gatherstep lookup → gatherstep lookup" {
		t.Errorf("second line %+v, want the step's gather line", rest[1])
	}
	if rest[2].Kind != "system" || !strings.HasPrefix(rest[2].Payload.Text, "Gathered 1 of 1 in ") || !strings.HasSuffix(rest[2].Payload.Text, "s") {
		t.Errorf("third line %+v, want Gathered 1 of 1 in …s", rest[2])
	}
	if rest[3].Kind != "usage" {
		t.Errorf("fourth line %+v, want the provider's first", rest[3])
	}

	text := readFile(t, filepath.Join(dir, "prompt.md"))
	if !strings.Contains(text, "# Gathered context") || !strings.Contains(text, "```\ncompany 42 is Acme\n```") {
		t.Errorf("prompt.md does not inline the gathered result:\n%s", text)
	}
}

// While the gatherers run, the run's state says so; afterwards it does not.
func TestStatePhaseIsGatherWhileGathering(t *testing.T) {
	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r, log := gatherRunner(t, stub, true)
	out := triageOne(t, r, Options{DryRun: true})
	if got := stepLog(t, log); !strings.Contains(got, "phase=gather") {
		t.Fatalf("the step saw %q, want phase=gather", got)
	}
	_, st, err := store.Open(r.Config.Root, out.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != "" {
		t.Fatalf("Phase after the run = %q, want empty", st.Phase)
	}
}

// replayBundle writes a golden bundle, with a context/ holding one
// gathered result when withContext is set.
func replayBundle(t *testing.T, withContext bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := ticket.WriteBundle(dir, sampleBundle()); err != nil {
		t.Fatal(err)
	}
	if !withContext {
		return dir
	}
	ctxDir := filepath.Join(dir, "context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := "# company\n\nCall: `recorded`\n\n```\nrecorded fact\n```\n"
	if err := os.WriteFile(filepath.Join(ctxDir, "company.md"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	index, _ := json.Marshal([]gather.Entry{{Name: "company", File: "context/company.md", Kind: "command", Status: "ok", Bytes: 13, Call: "recorded", Summary: "recorded lookup"}})
	if err := os.WriteFile(filepath.Join(ctxDir, gather.IndexFile), index, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A replayed bundle keeps the context it was recorded with, and nothing
// live is called to make more.
func TestReplayedBundleKeepsContextAndRunsNoGatherer(t *testing.T) {
	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r, log := gatherRunner(t, stub, true)
	out := triageOne(t, r, Options{BundleDir: replayBundle(t, true), DryRun: true})
	if got := stepLog(t, log); got != "" {
		t.Fatalf("a gatherer ran on a replay: %q", got)
	}
	text := readFile(t, filepath.Join(runDir(t, r.Config, out), "prompt.md"))
	if !strings.Contains(text, "- context/company.md — recorded lookup (0.0 KiB)") || !strings.Contains(text, "recorded fact") {
		t.Errorf("prompt.md does not carry the replayed context:\n%s", text)
	}
}

// A replayed bundle without context gets none.
func TestReplayedBundleWithoutContextGetsNone(t *testing.T) {
	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r, log := gatherRunner(t, stub, true)
	out := triageOne(t, r, Options{BundleDir: replayBundle(t, false), DryRun: true})
	if got := stepLog(t, log); got != "" {
		t.Fatalf("a gatherer ran on a replay: %q", got)
	}
	dir := runDir(t, r.Config, out)
	if text := readFile(t, filepath.Join(dir, "prompt.md")); strings.Contains(text, "# Gathered context") {
		t.Errorf("prompt.md has gathered context from nowhere:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "bundle", "context")); !os.IsNotExist(err) {
		t.Errorf("bundle/context exists on a replay without one: %v", err)
	}
}

func TestEvalRunsNoGatherer(t *testing.T) {
	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r, log := gatherRunner(t, stub, true)
	triageOne(t, r, Options{Eval: true, NoteOnly: true, DryRun: true})
	if got := stepLog(t, log); got != "" {
		t.Fatalf("a gatherer ran on an eval: %q", got)
	}
}

func TestNoGatherOption(t *testing.T) {
	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r, log := gatherRunner(t, stub, true)
	out := triageOne(t, r, Options{NoGather: true, DryRun: true})
	if got := stepLog(t, log); got != "" {
		t.Fatalf("a gatherer ran under --no-gather: %q", got)
	}
	if hasLine(t, runDir(t, r.Config, out), "Gathering context") {
		t.Fatal("events.jsonl reports a gather under --no-gather")
	}
}

// A session with no ticket still reads the pull request its instruction
// names, and gets a context directory for it without becoming a run with
// a ticket.
func TestNoReferenceSessionWithAPRLink(t *testing.T) {
	bin := t.TempDir()
	testbin.Install(t, bin, "gh", "rungh")
	cfg := newWorkspaceWith(t, configYAML+"gather:\n  pr:\n    anyRepo: true\n")
	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, stub, stubTracker{}, stubHelpdesk{})
	r.Env = []string{"PATH=" + bin}

	out, err := r.Session(context.Background(), "", Options{NoBundle: true, DryRun: true, Instruction: "Look at https://github.com/acme/web/pull/7"})
	if err != nil {
		t.Fatal(err)
	}
	dir := runDir(t, cfg, out)
	if _, err := os.Stat(filepath.Join(dir, "bundle", "context", "pr-web-7.md")); err != nil {
		t.Fatalf("the pull request was not gathered: %v\nwarnings: %q", err, out.State.Warnings)
	}
	if _, err := os.Stat(filepath.Join(dir, "bundle", "ticket.json")); !os.IsNotExist(err) {
		t.Fatalf("a session with no ticket has bundle/ticket.json: %v", err)
	}
	text := readFile(t, filepath.Join(dir, "prompt.md"))
	if !strings.Contains(text, "# Gathered context") || strings.Contains(text, "# Ticket") {
		t.Errorf("prompt.md:\n%s", text)
	}
}

// A step the allow-list refuses is the operator's configuration problem:
// it is a run warning, and the session never hears of it.
func TestDeniedStepIsAWarningNotPrompt(t *testing.T) {
	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r, log := gatherRunner(t, stub, false)
	out := triageOne(t, r, Options{DryRun: true})
	if got := stepLog(t, log); got != "" {
		t.Fatalf("a denied step ran: %q", got)
	}
	if !hasWarningContaining(out.State.Warnings, "gather step company denied: gatherstep lookup: ") {
		t.Fatalf("warnings %q do not report the denied step", out.State.Warnings)
	}
	text := readFile(t, filepath.Join(runDir(t, r.Config, out), "prompt.md"))
	if strings.Contains(text, "gatherstep") || strings.Contains(text, "denied") {
		t.Errorf("prompt.md mentions the denied step:\n%s", text)
	}
}

// The gather steps run with the session's own child environment, with the
// credentials Sirdar holds stripped from it — and never with this
// process's whole environment, which is what a nil Env would hand them.
func TestGatherStepsGetTheStrippedChildEnv(t *testing.T) {
	bin := t.TempDir()
	testbin.Install(t, bin, "gatherstep", "gatherstep")
	cfg := newWorkspaceWith(t, configWithCredential+"gather:\n  allow: [\"gatherstep *\"]\n  steps:\n    - name: env\n      command: [gatherstep, env]\n")
	stub := &stubProvider{script: replay(finalEvent(triageDoc))}

	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"explicit base", []string{"PATH=" + bin, "ZOHO_TOKEN=secret-token", "KEEP_ME=yes"}},
		{"process base", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ZOHO_TOKEN", "secret-token")
			t.Setenv("KEEP_ME", "yes")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			r := newRunner(cfg, stub, stubTracker{}, stubHelpdesk{})
			r.Env = tc.env
			out := triageOne(t, r, Options{DryRun: true})
			got := readFile(t, filepath.Join(runDir(t, cfg, out), "bundle", "context", "env.md"))
			if strings.Contains(got, "secret-token") {
				t.Errorf("the gather step was handed the helpdesk token:\n%s", got)
			}
			if !strings.Contains(got, "KEEP_ME=yes") || !strings.Contains(got, "SIRDAR_BILLING=") {
				t.Errorf("the gather step did not get the session's child env:\n%s", got)
			}
		})
	}
	if env := (&Runner{Deps: Deps{Config: cfg, Env: []string{}}}).gatherEnv(); env == nil {
		t.Fatal("gatherEnv returned nil")
	}
}

// The gather deadline bounds an MCP step as it bounds a command: a server
// that never answers is given up on when the deadline passes, and is
// stopped — killed, when it will not exit — before the run goes on.
func TestGatherDeadlineStopsAHungMCPServer(t *testing.T) {
	hang := testbin.Install(t, t.TempDir(), "hangmcp", "hangmcp")
	pidFile := filepath.Join(t.TempDir(), "pid")
	cfg := newWorkspaceWith(t, configYAML+"gather:\n  totalSeconds: 1\n  steps:\n    - name: rows\n      timeoutSeconds: 30\n      mcp: {server: hang, tool: list_rows}\n")
	mcpJSON, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"hang": map[string]any{"command": hang, "env": map[string]string{"HANG_MCP_PID": pidFile}}}})
	if err := os.WriteFile(filepath.Join(cfg.Root, ".mcp.json"), mcpJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	stub := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, stub, stubTracker{}, stubHelpdesk{})

	start := time.Now()
	out := triageOne(t, r, Options{DryRun: true})
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("the run took %s; the gather deadline did not bound the MCP step", took)
	}
	if !hasLine(t, runDir(t, cfg, out), `"decision":"failed"`) {
		t.Fatalf("no failed gather line:\n%s", strings.Join(eventLogLines(t, runDir(t, cfg, out)), "\n"))
	}
	if !hasLine(t, runDir(t, cfg, out), "gather deadline") {
		t.Errorf("the MCP row does not say the gather deadline ended it:\n%s", strings.Join(eventLogLines(t, runDir(t, cfg, out)), "\n"))
	}
	if runtime.GOOS == "windows" {
		return
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the hung server never started: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	proc, err := os.FindProcess(pid)
	if err == nil && proc.Signal(syscall.Signal(0)) == nil {
		t.Fatalf("the hung MCP server (pid %d) is still running after the gather", pid)
	}
}
