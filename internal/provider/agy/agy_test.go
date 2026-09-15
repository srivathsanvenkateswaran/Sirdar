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
}

func TestArgsCarryThePlanModeGuarantee(t *testing.T) {
	p := NewConfig(Config{Model: "gemini-3.8-flash-low", Effort: "low"})
	spec := provider.SessionSpec{
		OutputSchema: []byte("{\n  \"type\": \"object\"\n}"),
		Budget:       provider.Budget{MaxMinutes: 25},
		Resume:       "conv-7",
	}
	args := p.(*Provider).args(spec)
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--output-format stream-json",
		"--input-format stream-json",
		"--mode plan",
		"--disable-slash-commands",
		"--model gemini-3.8-flash-low",
		"--effort low",
		"--print-timeout 25m",
		"--conversation conv-7",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
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
	args := New().(*Provider).args(provider.SessionSpec{OutputSchema: []byte(`{}`)})
	if !strings.Contains(strings.Join(args, " "), "--model "+defaultModel) {
		t.Errorf("args %v: a workspace naming no model should get the cheapest tier", args)
	}
	// A per-run --model beats the block's.
	args = NewConfig(Config{Model: "a"}).(*Provider).args(provider.SessionSpec{
		OutputSchema: []byte(`{}`), Model: "b",
	})
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
	for _, e := range d.errs {
		if strings.Contains(e.Text, "read-only guarantee") {
			t.Errorf("a refused write was reported as a breach: %v", e.Text)
		}
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
// which Sirdar can neither see nor override — the run's event log says so.
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
	if !containsSubstring(errTexts(d.errs), "read-only guarantee") {
		t.Fatalf("a completed write was not reported: %+v", d.errs)
	}
}

// TestStateDirWriteIsNotABreach exempts plan mode's own artifact, which is
// written into the CLI's state directory on every plan-mode run.
func TestStateDirWriteIsNotABreach(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	artifact := filepath.Join(home, stateDirParent, stateDirChild, "brain", "c1", "implementation_plan.md")
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
	if containsSubstring(errTexts(d.errs), "read-only guarantee") {
		t.Fatalf("the plan artifact was reported as a breach: %+v", d.errs)
	}
	if !containsSubstring(d.systems, "state directory") {
		t.Errorf("the artifact write was not reported at all: %v", d.systems)
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
	t.Setenv("HOME", t.TempDir())

	checks := p.(*Provider).DoctorWithConfig(context.Background(), "", provider.DoctorConfig{MCPWorkspaceOnly: true})
	byName := map[string]provider.Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	for _, name := range []string{"agy --version", "agy models", "agy model", "agy mcp scope", "agy fix mode"} {
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
	for _, name := range []string{"agy mcp scope", "agy fix mode"} {
		c := byName[name]
		if c.Severity() != provider.LevelWarn {
			t.Errorf("%s should warn, not fail or pass silently: %+v", name, c)
		}
	}
	if !strings.Contains(byName["agy mcp scope"].Detail, "mcp.workspaceOnly cannot be enforced") {
		t.Errorf("mcp row %+v", byName["agy mcp scope"])
	}
}

func TestDoctorWarnsOnAModelTheAccountLacks(t *testing.T) {
	p := NewConfig(Config{Binary: os.Args[0], Model: "gemini-9-ultra"})
	t.Setenv("SIRDAR_FAKE_AGY", "unused-but-selects-the-fake")
	t.Setenv("HOME", t.TempDir())

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
