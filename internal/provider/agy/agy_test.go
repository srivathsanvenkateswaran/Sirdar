package agy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// TestMain doubles as the fake `agy` binary: when SIRDAR_FAKE_AGY names a
// script, the test binary replays that script instead of running tests.
func TestMain(m *testing.M) {
	if script := os.Getenv("SIRDAR_FAKE_AGY"); script != "" {
		os.Exit(fakeCLI(script))
	}
	os.Exit(m.Run())
}

// fakeCLI replays a JSONL script on stdout. Directive lines drive the fake:
//
//	{"$wait":"user"}    block until a user line arrives on stdin
//	{"$exit":N}         exit with N
//	{"$stderr":"boom"}  write a line to stderr
//
// Every stdin line is echoed to stderr prefixed "STDIN:", the command line
// as "ARGV:<json>", and any environment variable the adapter is meant to
// have stripped as "ENV:<name>=<value>", so a test can inspect all three
// through Result.StderrTail.
//
// The `--version` and `models` invocations Doctor makes are answered
// before the script is opened, since neither has one.
func fakeCLI(script string) int {
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version":
			fmt.Println("1.2.3")
			return 0
		case "models":
			fmt.Println("Fetching available models...")
			fmt.Println("gemini-3.6-flash-low\tGemini 3.6 Flash (Low)")
			fmt.Println("gemini-3.1-pro-high\tGemini 3.1 Pro (High)")
			return 0
		}
	}

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	go func() {
		<-interrupted
		fmt.Fprintln(os.Stderr, "SIGINT")
		os.Exit(0)
	}()

	fmt.Fprintln(os.Stderr, "ARGV:"+mustJSON(os.Args[1:]))
	for _, name := range strippedEnvKeys {
		if v, ok := os.LookupEnv(name); ok {
			fmt.Fprintln(os.Stderr, "ENV:"+name+"="+v)
		}
	}

	f, err := os.Open(script)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake agy:", err)
		return 2
	}
	defer f.Close()

	fromSirdar := make(chan string, 64)
	go func() {
		defer close(fromSirdar)
		in := bufio.NewScanner(os.Stdin)
		in.Buffer(make([]byte, 0, 64*1024), 16<<20)
		for in.Scan() {
			fmt.Fprintln(os.Stderr, "STDIN:"+in.Text())
			fromSirdar <- in.Text()
		}
	}()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, `{"$`) {
			fmt.Fprintln(os.Stdout, line)
			continue
		}
		var directive struct {
			Wait   string `json:"$wait"`
			Exit   *int   `json:"$exit"`
			Stderr string `json:"$stderr"`
		}
		if err := json.Unmarshal([]byte(line), &directive); err != nil {
			fmt.Fprintln(os.Stderr, "fake agy: bad directive:", line)
			return 2
		}
		switch {
		case directive.Exit != nil:
			return *directive.Exit
		case directive.Stderr != "":
			fmt.Fprintln(os.Stderr, directive.Stderr)
		case directive.Wait != "":
			want := directive.Wait
			if want == "user" {
				want = `"event":"user"`
			}
			for {
				got, ok := <-fromSirdar
				if !ok {
					fmt.Fprintln(os.Stderr, "fake agy: stdin closed waiting for "+directive.Wait)
					return 3
				}
				if strings.Contains(got, want) {
					break
				}
			}
		}
	}
	return 0
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

func fakeSpec(t *testing.T, script string) provider.SessionSpec {
	t.Helper()
	abs, err := filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}
	// Start writes the session's project file under $HOME/.gemini, and a
	// test has no business touching the operator's own copy of that
	// directory even though the file is removed again on Wait.
	fakeHome(t)
	return provider.SessionSpec{
		Cwd:          t.TempDir(),
		Prompt:       "hello",
		OutputSchema: []byte(`{"type": "object"}`),
		Policy:       &provider.PermissionPolicy{BashAllow: []string{"git log*"}},
		Binary:       os.Args[0],
		Env: append(os.Environ(),
			"SIRDAR_FAKE_AGY="+abs,
			"GEMINI_API_KEY=should-be-stripped",
			"ANTIGRAVITY_SIDECAR_UI_TOKEN=should-be-stripped"),
	}
}

// fakeHome points os.UserHomeDir at a temporary directory for the length
// of one test, so the ~/.gemini tree a session reads and writes — the
// project file above all, which Start creates and Wait deletes — is the
// test's own and never the operator's.
//
// It is idempotent: a test that has already called it, directly or
// through fakeSpec, gets the same directory back rather than a second one
// that would strand the paths it has already built.
func fakeHome(t *testing.T) string {
	t.Helper()
	if home := os.Getenv("SIRDAR_TEST_HOME"); home != "" {
		return home
	}
	home := t.TempDir()
	t.Setenv("SIRDAR_TEST_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	return home
}

func writeScript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// drain collects every event a session emits, grouped by kind.
type drained struct {
	all         []provider.Event
	text        string
	toolStarted []string
	permissions []provider.Event
	errs        []provider.Event
	breaches    []provider.Event
	blind       []provider.Event
	systems     []string
	final       *provider.Event
	usage       []provider.Event
}

func drain(t *testing.T, s provider.Session) drained {
	t.Helper()
	var d drained
	for ev := range s.Events() {
		d.all = append(d.all, ev)
		switch ev.Kind {
		case provider.EvAssistantText:
			d.text += ev.Text
		case provider.EvToolStarted:
			d.toolStarted = append(d.toolStarted, ev.Tool)
		case provider.EvPermission:
			d.permissions = append(d.permissions, ev)
		case provider.EvError:
			d.errs = append(d.errs, ev)
		case provider.EvBreach:
			d.breaches = append(d.breaches, ev)
		case provider.EvBlind:
			d.blind = append(d.blind, ev)
		case provider.EvSystem:
			d.systems = append(d.systems, ev.Text)
		case provider.EvUsage:
			d.usage = append(d.usage, ev)
		case provider.EvFinal:
			e := ev
			d.final = &e
		}
		if len(ev.Raw) == 0 {
			t.Errorf("event %s has no Raw", ev.Kind)
		}
	}
	return d
}

func TestBasicSession(t *testing.T) {
	s, err := New().Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}

	// The deltas of one step concatenate, and the schema answer arrives as
	// a step of its own.
	if !strings.HasPrefix(d.text, "Reading the repository.") {
		t.Errorf("assistant text %q: the deltas should concatenate", d.text)
	}
	if got := strings.Join(d.toolStarted, ","); got != "view_file,run_command" {
		t.Errorf("tools started %q", got)
	}
	if len(d.permissions) != 1 || d.permissions[0].Decision != "deny" || d.permissions[0].Tool != "run_command" {
		t.Fatalf("permissions %+v: the CLI's own refusal should read as a deny", d.permissions)
	}
	if d.final == nil {
		t.Fatal("no final event")
	}
	if string(d.final.Final) != `{"greeting":"hello","n":7}` {
		t.Errorf("structured output %s", d.final.Final)
	}
	// input_tokens + cache_read_tokens, and output + thinking.
	if d.final.InputTok != 3000 || d.final.OutputTok != 80 || d.final.Turns != 1 {
		t.Errorf("final usage in=%d out=%d turns=%d", d.final.InputTok, d.final.OutputTok, d.final.Turns)
	}
	if d.final.CostUSD != 0 {
		t.Errorf("cost %v: this wire carries none", d.final.CostUSD)
	}
	if res.Handle != "conv-1" {
		t.Errorf("handle %q: the conversation id is the resume handle", res.Handle)
	}
	if res.Usage.InputTok != 3000 {
		t.Errorf("result usage %+v", res.Usage)
	}
	if !containsSubstring(d.systems, "auto-denied") {
		t.Errorf("the result line's denied_actions were not reported: %v", d.systems)
	}
	// A refused tool is not a breach; nothing should have been raised.
	if len(d.errs) != 0 {
		t.Errorf("unexpected errors %+v", d.errs)
	}
	if len(d.breaches) != 0 {
		t.Errorf("unexpected breaches %+v", d.breaches)
	}
}

func TestArgsCarryThePlanModeGuarantee(t *testing.T) {
	p := NewConfig(Config{Model: "gemini-3.8-flash-low", Effort: "low"})
	spec := provider.SessionSpec{
		OutputSchema: []byte("{\n  \"type\": \"object\"\n}"),
		Budget:       provider.Budget{MaxMinutes: 25},
		Resume:       "conv-7",
	}
	args := p.(*Provider).args(spec, "sirdar-abc123")
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--output-format stream-json",
		"--input-format stream-json",
		"--mode plan",
		"--project sirdar-abc123",
		"--model gemini-3.8-flash-low",
		"--effort low",
		"--print-timeout 25m",
		"--conversation conv-7",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
	// --disable-slash-commands is what made round 1's plan mode a no-op:
	// the CLI answered every run with "--mode plan has no effect while
	// slash command expansion is disabled". Plan mode is the only
	// read-only lever this provider has, so the flag stays off.
	if strings.Contains(joined, "--disable-slash-commands") {
		t.Errorf("args carry --disable-slash-commands, which disables --mode plan: %v", args)
	}
	// A session whose project file could not be written runs without the
	// flag rather than with an empty one.
	if bare := strings.Join(p.(*Provider).args(spec, ""), " "); strings.Contains(bare, "--project") {
		t.Errorf("args carry --project with no project id: %v", bare)
	}
	// The prompt goes on stdin, so --print takes an empty value rather
	// than swallowing the next argument.
	if args[len(args)-1] != "--print=" {
		t.Errorf("last arg %q, want --print=", args[len(args)-1])
	}
	if !strings.Contains(joined, `--json-schema {"type":"object"}`) {
		t.Errorf("schema was not compacted: %v", args)
	}
}

func TestArgsDefaultToTheCheapestModel(t *testing.T) {
	args := New().(*Provider).args(provider.SessionSpec{OutputSchema: []byte(`{}`)}, "")
	if !strings.Contains(strings.Join(args, " "), "--model "+defaultModel) {
		t.Errorf("args %v: a workspace naming no model should get the cheapest tier", args)
	}
	// A per-run --model beats the block's.
	args = NewConfig(Config{Model: "a"}).(*Provider).args(provider.SessionSpec{
		OutputSchema: []byte(`{}`), Model: "b",
	}, "")
	if !strings.Contains(strings.Join(args, " "), "--model b") {
		t.Errorf("args %v: spec.Model should win", args)
	}
}

// TestFixModeIsRefused is the contract the research capture forces: with no
// way to mediate a tool call, a write-enabled session cannot be confined,
// so it is never started.
func TestFixModeIsRefused(t *testing.T) {
	spec := fakeSpec(t, "testdata/script-basic.jsonl")
	spec.Mode = provider.ModeFix
	spec.Policy = provider.FixPolicy(spec.Cwd, []string{"go test*"}, nil, nil)

	s, err := New().Start(context.Background(), spec)
	if err == nil {
		s.Cancel()
		_, _ = s.Wait()
		t.Fatal("fix mode started; it must be refused")
	}
	if !errors.Is(err, ErrFixUnsupported) {
		t.Fatalf("error %v, want ErrFixUnsupported", err)
	}
	if !strings.Contains(err.Error(), "dangerously-skip-permissions") {
		t.Errorf("the refusal should say why: %v", err)
	}
}

// TestPolicyNeverDecidesAWrite is the policy test the plan-mode guarantee
// needs. A triage session is handed a policy that would allow writes —
// provider.FixPolicy, rooted at the workspace — and the session must still
// never turn the CLI's refusal into an allow, because it never consults
// the policy at all. The permission path this provider has is one-way:
// report what the CLI already decided.
func TestPolicyNeverDecidesAWrite(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m","permission_mode":"request-review"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"ACTIVE","step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"TargetFile":"/work/src/a.go"}}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"ERROR","step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"TargetFile":"/work/src/a.go"},"error":{"type":"TOOL_ERROR","message":"permission check failed for write_file \"/work/src/a.go\": user denied permission for write_file(/work/src/a.go)"}}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{},"denied_actions":[{"action":"write_file","display_name":"WriteToFile"}]}}`,
	)
	spec := fakeSpec(t, script)
	// A policy that says yes to everything a fix may do. Nothing should
	// consult it.
	spec.Policy = provider.FixPolicy(spec.Cwd, []string{"*"}, []string{"*"}, nil)

	s, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}

	if len(d.permissions) != 1 {
		t.Fatalf("permissions %+v", d.permissions)
	}
	if d.permissions[0].Decision != "deny" {
		t.Fatalf("decision %q: a permissive policy must not lift the CLI's refusal", d.permissions[0].Decision)
	}
	if d.permissions[0].Tool != "write_to_file" {
		t.Errorf("tool %q", d.permissions[0].Tool)
	}
	// The refusal is not a breach: nothing was written.
	if len(d.breaches) != 0 {
		t.Errorf("a refused write was reported as a breach: %+v", d.breaches)
	}
	// And Sirdar never wrote a permission answer back: the only thing on
	// stdin is the user turn.
	res, _ := s.Wait()
	for _, line := range res.StderrTail {
		if strings.HasPrefix(line, "STDIN:") && !strings.Contains(line, `"event":"user"`) {
			t.Errorf("unexpected line written to the CLI: %s", line)
		}
	}
}

// TestCompletedWriteIsReportedAsABreach is the other half: when the CLI
// does not refuse — because the operator's own settings.json allowed it,
// which Sirdar can neither see nor override — the session raises a breach,
// which is a kind of its own and not one more error line for the
// malformed-line counter to shrug off.
func TestCompletedWriteIsReportedAsABreach(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"TargetFile":"/work/src/a.go"}}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.breaches) != 1 {
		t.Fatalf("a completed write was not raised as a breach: %+v", d.all)
	}
	if len(d.errs) != 0 {
		t.Errorf("a breach was also raised as an error: %+v", d.errs)
	}
	breach := d.breaches[0]
	// The first line is the run's terminal reason, so it names the tool
	// and the path and stops there.
	if got := firstLineOf(breach.Text); got != "read-only breach: write_to_file /work/src/a.go" {
		t.Errorf("breach reason %q", got)
	}
	if breach.Tool != "write_to_file" {
		t.Errorf("breach tool %q", breach.Tool)
	}
	if !strings.Contains(breach.Text, "settings.json") {
		t.Errorf("the breach should say where to look: %q", breach.Text)
	}
}

// TestCompletedRunCommandIsABreach is the controller's ruling on the one
// unexplained capture: a plan-mode `touch x` that reported DONE with no
// file on disk. Until the CLI explains it, a completed run_command is a
// breach like any other — the failure mode of being wrong is a run that
// ends failed, not a run that files a read-only note over a command that
// really ran.
func TestCompletedRunCommandIsABreach(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"touch x"}}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.breaches) != 1 {
		t.Fatalf("a completed run_command was not raised as a breach: %+v", d.all)
	}
	// The command is what the reason names, not a path.
	if got := firstLineOf(d.breaches[0].Text); got != "read-only breach: run_command touch x" {
		t.Errorf("breach reason %q", got)
	}
}

// TestCompletedMCPCallIsABreach: an MCP server's own tools are opaque to
// this adapter — one `call_mcp_tool` step whatever the server did — and
// the session sees whatever servers the operator configured globally. Left
// off execTools, an MCP write is the one kind that completes in silence.
func TestCompletedMCPCallIsABreach(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"call_mcp_tool","tool_info":{"name":"call_mcp_tool","parameters":{"ServerName":"github","ToolName":"create_pull_request"}}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.breaches) != 1 {
		t.Fatalf("a completed call_mcp_tool was not raised as a breach: %+v", d.all)
	}
	if got := firstLineOf(d.breaches[0].Text); got != "read-only breach: call_mcp_tool github/create_pull_request" {
		t.Errorf("breach reason %q", got)
	}
}

// TestBreachNamesThePathFromTheActiveLine is the shape the research
// capture actually has: the ACTIVE line spells the parameters out and the
// DONE line that follows elides tool_info. Reading the path off the DONE
// line alone gets nothing, so the session remembers the ACTIVE line's
// arguments by step_index and matches them on DONE.
func TestBreachNamesThePathFromTheActiveLine(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"TargetFile":"/work/src/a.go"}}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"DONE","step_type":"tool","tool_name":"write_to_file","duration_seconds":0.099246,"tool_info":{}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.breaches) != 1 {
		t.Fatalf("breaches %+v", d.all)
	}
	if got := firstLineOf(d.breaches[0].Text); got != "read-only breach: write_to_file /work/src/a.go" {
		t.Errorf("breach reason %q: the path should come from the ACTIVE line", got)
	}
	// The finished-tool event itself carries the remembered arguments too,
	// so the run's event log records what the call was on.
	for _, ev := range d.all {
		if ev.Kind == provider.EvToolFinished && !strings.Contains(string(ev.Input), "/work/src/a.go") {
			t.Errorf("tool_finished input %s: the remembered arguments should be filled in", ev.Input)
		}
	}
}

// TestRememberedPathExemptsThePlanArtifact is the same pairing on the
// exemption side: a plan artifact whose DONE line elides its path must
// still be recognised as the artifact rather than reported as a breach on
// a path nobody can see.
func TestRememberedPathExemptsThePlanArtifact(t *testing.T) {
	home := fakeHome(t)
	artifact := filepath.Join(home, stateDirParent, stateDirChild, planDirName, "c1", "implementation_plan.md")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}

	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"TargetFile":`+mustJSON(artifact)+`}}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"DONE","step_type":"tool","tool_name":"write_to_file","tool_info":{}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.breaches) != 0 {
		t.Fatalf("the plan artifact was reported as a breach: %+v", d.breaches)
	}
	if !containsSubstring(d.systems, "plan directory") {
		t.Errorf("the artifact write was not reported at all: %v", d.systems)
	}
}

// TestPlanDirWriteIsNotABreach exempts plan mode's own artifact, which is
// written into <state dir>/brain/<conversation id>/ on every plan-mode
// run.
func TestPlanDirWriteIsNotABreach(t *testing.T) {
	home := fakeHome(t)
	artifact := filepath.Join(home, stateDirParent, stateDirChild, planDirName, "c1", "implementation_plan.md")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}

	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"TargetFile":`+mustJSON(artifact)+`}}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.breaches) != 0 {
		t.Fatalf("the plan artifact was reported as a breach: %+v", d.breaches)
	}
	if !containsSubstring(d.systems, "plan directory") {
		t.Errorf("the artifact write was not reported at all: %v", d.systems)
	}
}

// TestStateDirOutsideThePlanDirIsABreach is what the narrowed exemption
// buys. scratch/ sits beside brain/ under the same state directory, and it
// is where the research capture found a default-mode run depositing a file
// the agent had been told to write to an absolute path. A directory-wide
// exemption reported that as bookkeeping; this one reports it as what it
// is. A write into another conversation's brain/ directory is a breach for
// the same reason: it is not this session's artifact.
func TestStateDirOutsideThePlanDirIsABreach(t *testing.T) {
	home := fakeHome(t)
	t.Setenv("USERPROFILE", home)
	stateRoot := filepath.Join(home, stateDirParent, stateDirChild)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"scratch", filepath.Join(stateRoot, "scratch", "a.txt")},
		{"another conversation", filepath.Join(stateRoot, planDirName, "c2", "implementation_plan.md")},
		{"state dir root", filepath.Join(stateRoot, "settings.json")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Dir(tc.path), 0o755); err != nil {
				t.Fatal(err)
			}
			script := writeScript(t,
				`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
				`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"write_to_file","tool_info":{"name":"write_to_file","parameters":{"TargetFile":`+mustJSON(tc.path)+`}}}}`,
				`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
			)
			s, err := New().Start(context.Background(), fakeSpec(t, script))
			if err != nil {
				t.Fatal(err)
			}
			d := drain(t, s)
			if _, err := s.Wait(); err != nil {
				t.Fatal(err)
			}
			if len(d.breaches) != 1 {
				t.Fatalf("a write to %s was not raised as a breach: %+v", tc.path, d.all)
			}
		})
	}
}

// TestSupportsFixIsFalse is what `sirdar fix` asks before it touches git.
func TestSupportsFixIsFalse(t *testing.T) {
	p := New()
	fs, ok := p.(provider.FixSupport)
	if !ok {
		t.Fatal("the agy provider must declare its fix support, so the refusal can come before the branch")
	}
	if fs.SupportsFix() {
		t.Fatal("agy cannot run a fix session")
	}
	if err := provider.RefuseFix(p); !errors.Is(err, ErrFixUnsupported) {
		t.Fatalf("RefuseFix returned %v, want the same error Start gives", err)
	}
}

// TestSteerIsRefused is what `sirdar steer` asks before it touches the
// run: with no way to refuse a tool call before it runs, an open-ended
// follow-up cannot be held to the read-only guarantee.
func TestSteerIsRefused(t *testing.T) {
	c, err := provider.PlanSteer(New())
	if c != provider.ContinueNone || !errors.Is(err, ErrSteerUnsupported) {
		t.Fatalf("PlanSteer = %q, %v; want ContinueNone and ErrSteerUnsupported", c, err)
	}
}

// TestSendRunsAnotherTurn is the schema retry. Unlike Qwen Code, this CLI
// takes --json-schema and --input-format stream-json together, so the retry
// is one more stdin line rather than a fresh --resume.
func TestSendRunsAnotherTurn(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{\"title\":\"nope\"}","num_turns":1,"structured_output":{"title":"nope"},"usage":{"input_tokens":10,"output_tokens":2}}}`,
		// Waiting on "user" alone would match the prompt line, which is
		// already in the fake's stdin queue before the script starts; the
		// retry's own text is what distinguishes the second turn.
		`{"$wait":"did not validate"}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{\"title\":\"ok\"}","num_turns":2,"structured_output":{"title":"ok"},"usage":{"input_tokens":30,"output_tokens":6}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}

	var finals []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range s.Events() {
			if ev.Kind == provider.EvFinal {
				finals = append(finals, string(ev.Final))
				if len(finals) == 1 {
					if err := s.Send(context.Background(), "that did not validate, try again"); err != nil {
						t.Errorf("send: %v", err)
					}
				}
			}
		}
	}()
	<-done

	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(finals) != 2 || finals[1] != `{"title":"ok"}` {
		t.Fatalf("finals %v: a follow-up user message should run another turn", finals)
	}
	// The result line's totals are the conversation's own, so the second
	// turn replaces rather than adds.
	if res.Usage.InputTok != 30 || res.Usage.Turns != 2 {
		t.Errorf("usage %+v: result figures are cumulative and should replace", res.Usage)
	}
	sent := 0
	for _, line := range res.StderrTail {
		if strings.HasPrefix(line, "STDIN:") {
			sent++
			if !strings.Contains(line, `"event":"user"`) || !strings.Contains(line, `"type":"text"`) {
				t.Errorf("stdin line is not the CLI's own shape: %s", line)
			}
		}
	}
	if sent != 2 {
		t.Errorf("wrote %d user lines, want 2", sent)
	}
}

func TestChildEnvStripsRedirectionVariables(t *testing.T) {
	s, err := New().Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range res.StderrTail {
		if strings.HasPrefix(line, "ENV:") {
			t.Errorf("a stripped variable reached the child: %s", line)
		}
	}
	for _, name := range []string{"GEMINI_API_KEY", "ANTIGRAVITY_SIDECAR_UI_TOKEN"} {
		if !containsSubstring(d.systems, "removed "+name) {
			t.Errorf("stripping %s was not reported: %v", name, d.systems)
		}
	}
}

func TestMCPStrictIsReportedAsUnenforceable(t *testing.T) {
	spec := fakeSpec(t, "testdata/script-basic.jsonl")
	spec.MCPStrict = true
	s, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if !containsSubstring(d.systems, "mcp.workspaceOnly is not enforceable") {
		t.Fatalf("a strict-MCP session said nothing about it: %v", d.systems)
	}
	// And no flag was invented for it.
	res, _ := s.Wait()
	for _, line := range res.StderrTail {
		if strings.HasPrefix(line, "ARGV:") && strings.Contains(line, "mcp") {
			t.Errorf("an MCP flag was passed; the CLI has none: %s", line)
		}
	}
}

func TestNonZeroExitIsReported(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"$stderr":"error: stream input message is missing the \"event\" field"}`,
		`{"$exit":1}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	drain(t, s)
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitErr == nil || !strings.Contains(res.ExitErr.Error(), "exited with code 1") {
		t.Fatalf("exit error %v", res.ExitErr)
	}
	if !containsSubstring(res.StderrTail, "missing the \"event\" field") {
		t.Errorf("stderr tail %v", res.StderrTail)
	}
}

func TestMalformedLineBecomesAnError(t *testing.T) {
	script := writeScript(t,
		`not json at all`,
		`{"conversation_id":"c1"}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.errs) != 2 {
		t.Fatalf("errors %+v, want one per malformed line", d.errs)
	}
}

func TestResultErrorStatus(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"ERROR","response":"","error":"stream input message is missing the \"event\" field","num_turns":0}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if d.final != nil {
		t.Errorf("an ERROR result must not produce a final event: %+v", d.final)
	}
	if !containsSubstring(errTexts(d.errs), "missing the \"event\" field") {
		t.Fatalf("errors %+v", d.errs)
	}
}

func TestCancelStopsTheSession(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"$wait":"never-arrives"}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range s.Events() {
		}
	}()
	s.Cancel()
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitErr == nil {
		t.Fatal("a cancelled session should report why it ended")
	}
}

func TestDoctorRows(t *testing.T) {
	p := NewConfig(Config{Binary: os.Args[0], Model: "gemini-3.6-flash-low"})
	t.Setenv("SIRDAR_FAKE_AGY", "unused-but-selects-the-fake")
	fakeHome(t)

	checks := p.(*Provider).DoctorWithConfig(context.Background(), "", provider.DoctorConfig{MCPWorkspaceOnly: true})
	byName := map[string]provider.Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	for _, name := range []string{"agy --version", "agy models", "agy model", "agy settings", "agy mcp scope", "agy fix mode"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing doctor row %q: %+v", name, checks)
		}
	}
	if !byName["agy --version"].OK || byName["agy --version"].Detail != "1.2.3" {
		t.Errorf("version row %+v", byName["agy --version"])
	}
	if !byName["agy models"].OK {
		t.Errorf("login row %+v", byName["agy models"])
	}
	if byName["agy model"].Severity() != provider.LevelOK {
		t.Errorf("a configured model on the list should pass: %+v", byName["agy model"])
	}
	for _, name := range []string{"agy settings", "agy mcp scope", "agy fix mode"} {
		c := byName[name]
		if c.Severity() != provider.LevelWarn {
			t.Errorf("%s should warn, not fail or pass silently: %+v", name, c)
		}
	}
	if !strings.Contains(byName["agy mcp scope"].Detail, "mcp.workspaceOnly cannot be enforced") {
		t.Errorf("mcp row %+v", byName["agy mcp scope"])
	}
}

// TestDoctorReadsTheCLIsSettingsFile is the row that names the file the
// read-only guarantee actually rests on. Sirdar cannot see this file from
// inside a session and cannot override it with a flag, so doctor reading
// it out is the only warning an operator gets before a permissions.allow
// rule turns a refusal into a completed write.
func TestDoctorReadsTheCLIsSettingsFile(t *testing.T) {
	home := fakeHome(t)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SIRDAR_FAKE_AGY", "unused-but-selects-the-fake")

	root := filepath.Join(home, "work", "omni")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, stateDirParent, stateDirChild)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"permissions":{"allow":["write_file","run_command(git *)"],"deny":["run_command(rm *)"],"ask":["browser"]},` +
		`"trustedWorkspaces":[` + mustJSON(root) + `]}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	p := NewConfig(Config{Binary: os.Args[0]})
	row := doctorRow(t, p, provider.DoctorConfig{Root: root}, "agy settings")
	if row.Severity() != provider.LevelWarn {
		t.Fatalf("the settings row should warn: %+v", row)
	}
	for _, want := range []string{
		"permissions.allow: run_command(git *), write_file",
		"permissions.deny: run_command(rm *)",
		"permissions.ask: browser",
		"auto-denied",
		"this workspace is in trustedWorkspaces",
		"turns agy's refusal into a completed write",
	} {
		if !strings.Contains(row.Detail, want) {
			t.Errorf("settings row is missing %q:\n%s", want, row.Detail)
		}
	}
}

// A workspace the operator never trusted, and a settings file with no
// permission rules at all, is the common case and has to read plainly.
func TestDoctorSettingsRowWithNoRules(t *testing.T) {
	home := fakeHome(t)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SIRDAR_FAKE_AGY", "unused-but-selects-the-fake")

	dir := filepath.Join(home, stateDirParent, stateDirChild)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"trustedWorkspaces":["/somewhere/else"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	row := doctorRow(t, NewConfig(Config{Binary: os.Args[0]}),
		provider.DoctorConfig{Root: filepath.Join(home, "work")}, "agy settings")
	for _, want := range []string{"permissions.allow: none", "permissions.deny: none", "is not in trustedWorkspaces"} {
		if !strings.Contains(row.Detail, want) {
			t.Errorf("settings row is missing %q:\n%s", want, row.Detail)
		}
	}
	if strings.Contains(row.Detail, "permissions.ask") {
		t.Errorf("an empty ask list should not be listed: %s", row.Detail)
	}
}

// A missing settings file is not an error: the CLI runs on its defaults.
// The row says so rather than reading as a broken installation.
func TestDoctorSettingsRowWithNoFile(t *testing.T) {
	home := fakeHome(t)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SIRDAR_FAKE_AGY", "unused-but-selects-the-fake")

	row := doctorRow(t, NewConfig(Config{Binary: os.Args[0]}), provider.DoctorConfig{Root: home}, "agy settings")
	if row.Severity() != provider.LevelWarn {
		t.Fatalf("the settings row should warn: %+v", row)
	}
	if !strings.Contains(row.Detail, "does not exist") {
		t.Errorf("settings row %q", row.Detail)
	}
}

// doctorRow runs the provider's diagnostics and returns the named row.
func doctorRow(t *testing.T, p provider.Provider, cfg provider.DoctorConfig, name string) provider.Check {
	t.Helper()
	for _, c := range p.(*Provider).DoctorWithConfig(context.Background(), "", cfg) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q row", name)
	return provider.Check{}
}

func TestDoctorWarnsOnAModelTheAccountLacks(t *testing.T) {
	p := NewConfig(Config{Binary: os.Args[0], Model: "gemini-9-ultra"})
	t.Setenv("SIRDAR_FAKE_AGY", "unused-but-selects-the-fake")
	fakeHome(t)

	for _, c := range p.(*Provider).Doctor(context.Background(), "") {
		if c.Name != "agy model" {
			continue
		}
		if c.Severity() != provider.LevelWarn || !strings.Contains(c.Detail, "not on this account") {
			t.Fatalf("model row %+v", c)
		}
		return
	}
	t.Fatal("no model row")
}

// firstLineOf is the run layer's view of an event's text: the terminal
// reason a run is finished with.
func errTexts(events []provider.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Text)
	}
	return out
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// TestSessionWritesAndRemovesItsProjectFile is the lever that lets a
// triage read at all. The CLI takes no flag that grants a permission, and
// Sirdar will not edit the operator's settings.json, so the read rule goes
// in a project file of Sirdar's own, named on the command line and deleted
// when the session ends.
func TestSessionWritesAndRemovesItsProjectFile(t *testing.T) {
	home := fakeHome(t)
	spec := fakeSpec(t, "testdata/script-basic.jsonl")
	s, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, stateDirParent, configDirName, projectsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no projects directory: %v", err)
	}
	var path string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), projectIDPrefix) {
			path = filepath.Join(dir, e.Name())
		}
	}
	if path == "" {
		t.Fatalf("no Sirdar project file was written into %s: %v", dir, entries)
	}

	var got projectFile
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("the project file is not valid JSON: %v", err)
	}
	if got.ID+".json" != filepath.Base(path) {
		t.Errorf("project id %q does not match the file name %q; the CLI looks a project up by id", got.ID, filepath.Base(path))
	}
	if len(got.PermissionGrants.PermissionGrants.Allow) != 1 || got.PermissionGrants.PermissionGrants.Allow[0] != "read_file(*)" {
		t.Errorf("allow rules %v, want exactly read_file(*)", got.PermissionGrants.PermissionGrants.Allow)
	}
	for _, want := range []string{"write_file(*)", "command(*)", "execute_url(*)"} {
		if !containsString(got.PermissionGrants.PermissionGrants.Deny, want) {
			t.Errorf("deny rules %v are missing %q", got.PermissionGrants.PermissionGrants.Deny, want)
		}
	}
	if got.ProjectResources == nil || len(got.ProjectResources.Resources) != 1 ||
		got.ProjectResources.Resources[0].FolderURI != "file://"+spec.Cwd {
		t.Errorf("project resources %+v, want the session's own workspace", got.ProjectResources)
	}

	d := drain(t, s)
	if !containsSubstring(d.systems, "granted this session read_file") {
		t.Errorf("the grant was not reported on the event stream: %v", d.systems)
	}
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the project file outlived the session: %v", err)
	}
}

// TestCancelRemovesTheProjectFile covers the path a breach and a budget
// take, neither of which is guaranteed to reach a Wait that reaps the
// child.
func TestCancelRemovesTheProjectFile(t *testing.T) {
	home := fakeHome(t)
	s, err := New().Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, stateDirParent, configDirName, projectsDirName)
	s.Cancel()
	drain(t, s)
	_, _ = s.Wait()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), projectIDPrefix) {
			t.Errorf("a cancelled session left its project file behind: %s", e.Name())
		}
	}
}

// TestSweepStaleProjectsLeavesTheOperatorsOwn is the other half of owning
// a file in somebody else's directory: a run killed between writing it and
// reaping the child leaves one behind, and nothing but the next run will
// clear it. Only files carrying Sirdar's own prefix are candidates, and
// only once they are old enough to belong to no running session.
func TestSweepStaleProjectsLeavesTheOperatorsOwn(t *testing.T) {
	home := fakeHome(t)
	dir := filepath.Join(home, stateDirParent, configDirName, projectsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	write := func(name string, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(`{"id":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := now.Add(-age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stale := write(projectIDPrefix+"deadbeef.json", 48*time.Hour)
	fresh := write(projectIDPrefix+"cafebabe.json", time.Minute)
	theirs := write("default-cli-project.json", 30*24*time.Hour)

	if n := sweepStaleProjects(now, staleProjectAge); n != 1 {
		t.Errorf("swept %d files, want 1", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the stale Sirdar project survived the sweep: %v", err)
	}
	for _, keep := range []string{fresh, theirs} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("the sweep removed %s, which it must not touch: %v", keep, err)
		}
	}
}

// TestSessionThatReadsNothingIsBlind is round 1's failure, as a test. Every
// view_file was auto-denied because a headless agy cannot prompt for a
// permission, the agent answered out of the ticket text, and the run filed
// a high-confidence note. The answer still arrives; what changes is that
// the session says, ahead of it, that nothing was read.
func TestSessionThatReadsNothingIsBlind(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"find_by_name","tool_info":{"name":"find_by_name","parameters":{}}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"ERROR","step_type":"tool","tool_name":"view_file","tool_info":{"name":"view_file","parameters":{"AbsolutePath":"/w/ledger.go"},"error":{"type":"TOOL_ERROR","message":"permission check failed for read_file \"/w/ledger.go\": user denied permission for read_file(/w/ledger.go)"}}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{"confidence":"high"}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.blind) != 1 {
		t.Fatalf("blind events %+v, want exactly one", d.blind)
	}
	if got := firstLineOf(d.blind[0].Text); got != "the agent could read nothing (1 reads denied)" {
		t.Errorf("reason %q", got)
	}
	// Ahead of the final, because that is where the run layer files.
	blindAt, finalAt := -1, -1
	for i, ev := range d.all {
		switch ev.Kind {
		case provider.EvBlind:
			blindAt = i
		case provider.EvFinal:
			finalAt = i
		}
	}
	if blindAt < 0 || finalAt < 0 || blindAt > finalAt {
		t.Errorf("blind at %d, final at %d: the verdict must reach the runner before the answer", blindAt, finalAt)
	}
}

// TestACompletedReadIsNotBlind is the case the check must not fire on.
func TestACompletedReadIsNotBlind(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"view_file","tool_info":{"name":"view_file","parameters":{"AbsolutePath":"/w/ledger.go"}}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.blind) != 0 {
		t.Errorf("a session that read a file was called blind: %+v", d.blind)
	}
}

// TestListingFilesIsNotReadingThem is the distinction round 1 turned on:
// that run's find_by_name completed while its view_file was refused, so a
// check that counted any completed tool would have passed a session which
// had seen a list of filenames and not one line of code.
func TestListingFilesIsNotReadingThem(t *testing.T) {
	script := writeScript(t,
		`{"event":"init","conversation_id":"c1","init":{"model":"m"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"find_by_name","tool_info":{"name":"find_by_name","parameters":{}}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"DONE","step_type":"tool","tool_name":"list_dir","tool_info":{"name":"list_dir","parameters":{}}}}`,
		`{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"{}","num_turns":1,"structured_output":{}}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	d := drain(t, s)
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if len(d.blind) != 1 {
		t.Fatalf("blind events %+v, want one: listing filenames is not reading a file", d.blind)
	}
	if got := firstLineOf(d.blind[0].Text); got != "the agent could read nothing (no read tool was called)" {
		t.Errorf("reason %q", got)
	}
}

// TestReadAccessRowFailsWhenNothingCanGrantAread is why this row is a
// failure and not another warning. Every other gap this adapter reports is
// a property of the CLI an operator can read and decide about; a session
// that cannot read is an agent answering a ticket from its description.
func TestReadAccessRowFailsWhenNothingCanGrantARead(t *testing.T) {
	home := fakeHome(t)
	// A plain file where the projects directory belongs is the one
	// failure a test can arrange without depending on file modes, which
	// root ignores.
	blocked := filepath.Join(home, stateDirParent, configDirName, projectsDirName)
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readAccessCheck()
	if got.Severity() != provider.LevelFail {
		t.Errorf("severity %s, want fail: a triage that cannot read files is not a degraded triage", got.Severity())
	}
	if !strings.Contains(got.Detail, "read_file(*)") {
		t.Errorf("the row does not name the rule an operator would have to add: %s", got.Detail)
	}

	// And it passes once the directory Sirdar writes its project file
	// into is a directory it can write to.
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if ok := readAccessCheck(); !ok.OK {
		t.Errorf("read access should be available under a writable home: %s", ok.Detail)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
