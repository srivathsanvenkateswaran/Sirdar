package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// TestMain doubles as the fake `claude` binary: when SIRDAR_FAKE_CLAUDE names
// a script, the test binary replays that script instead of running tests.
func TestMain(m *testing.M) {
	if script := os.Getenv("SIRDAR_FAKE_CLAUDE"); script != "" {
		os.Exit(fakeCLI(script))
	}
	os.Exit(m.Run())
}

// fakeCLI replays a JSONL script on stdout. Directive lines drive the fake:
// {"$wait":"control_response"} blocks until a stdin line contains that text,
// {"$wait":"user"} blocks until a user line arrives, {"$exit":N} exits with N,
// {"$stderr":"boom"} writes to stderr. Every stdin line is echoed to stderr
// prefixed "STDIN:" so tests can inspect what Sirdar wrote.
func fakeCLI(script string) int {
	// Report a clean interrupt so a test can tell SIGINT from SIGKILL.
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	go func() {
		<-interrupted
		fmt.Fprintln(os.Stderr, "SIGINT")
		os.Exit(0)
	}()

	f, err := os.Open(script)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake claude:", err)
		return 2
	}
	defer f.Close()

	fromSirdar := make(chan string, 64)
	go func() {
		defer close(fromSirdar)
		in := bufio.NewScanner(os.Stdin)
		in.Buffer(make([]byte, 0, 64*1024), 16<<20)
		for in.Scan() {
			line := in.Text()
			fmt.Fprintln(os.Stderr, "STDIN:"+line)
			fromSirdar <- line
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
			fmt.Fprintln(os.Stderr, "fake claude: bad directive:", line)
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
				want = `"type":"user"`
			}
			for {
				got, ok := <-fromSirdar
				if !ok {
					fmt.Fprintln(os.Stderr, "fake claude: stdin closed waiting for "+directive.Wait)
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

func fakeSpec(t *testing.T, script string) provider.SessionSpec {
	t.Helper()
	abs, err := filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}
	return provider.SessionSpec{
		Cwd:          t.TempDir(),
		Prompt:       "hello",
		OutputSchema: []byte(`{"type":"object"}`),
		Policy:       &provider.PermissionPolicy{BashAllow: []string{"git log*"}},
		Binary:       os.Args[0],
		Env:          append(os.Environ(), "SIRDAR_FAKE_CLAUDE="+abs, "ANTHROPIC_API_KEY=should-be-stripped"),
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

func TestBasicSession(t *testing.T) {
	p := New()
	spec := fakeSpec(t, "testdata/script-basic.jsonl")

	s, err := p.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var perms []provider.Event
	var final *provider.Event
	var tools []string
	var systems []string
	var rl int
	var usage *provider.Event
	for ev := range s.Events() {
		switch ev.Kind {
		case provider.EvPermission:
			perms = append(perms, ev)
		case provider.EvToolStarted:
			tools = append(tools, ev.Tool)
		case provider.EvSystem:
			systems = append(systems, ev.Text)
		case provider.EvRateLimited:
			rl++
			if ev.Text != "five_hour" || !ev.ResetsAt.Equal(time.Unix(1789049400, 0)) {
				t.Errorf("rate limit event %+v", ev)
			}
		case provider.EvUsage:
			e := ev
			usage = &e
		case provider.EvFinal:
			e := ev
			final = &e
		}
		if len(ev.Raw) == 0 {
			t.Errorf("event %s has no Raw", ev.Kind)
		}
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(perms) != 2 || perms[0].Decision != "deny" || perms[1].Decision != "allow" {
		t.Fatalf("perms %+v", perms)
	}
	if perms[0].Tool != "Write" || perms[1].Tool != "Bash" {
		t.Fatalf("permission tools %+v", perms)
	}
	if len(tools) != 2 || tools[0] != "Write" || tools[1] != "Bash" {
		t.Fatalf("tool started %v", tools)
	}
	// The fixture carries two rate_limit_event lines: the routine
	// "allowed" one that must not park the pool, and a "rejected" one that
	// must.
	if rl != 1 {
		t.Fatalf("rate limited events %d, want only the rejected one", rl)
	}
	if !contains(systems, "rate limit allowed five_hour") {
		t.Fatalf("the allowed rate-limit line was not reported as informational: %v", systems)
	}
	if usage == nil || usage.InputTok != 18 || usage.OutputTok != 516 {
		t.Fatalf("usage %+v", usage)
	}
	if final == nil || string(final.Final) != `{"greeting":"hi","n":7}` {
		t.Fatalf("final %+v", final)
	}
	if res.Handle != "sess-1" || res.Usage.Turns != 3 || res.Usage.CostUSD != 0.07 {
		t.Fatalf("result %+v", res)
	}
	if res.Usage.InputTok != 18 || res.Usage.OutputTok != 516 {
		t.Fatalf("result usage %+v", res.Usage)
	}
	if string(res.Final) != `{"greeting":"hi","n":7}` || res.Text != `{"greeting":"hi","n":7}` {
		t.Fatalf("result final %s / text %q", res.Final, res.Text)
	}
	if res.ExitErr != nil {
		t.Fatalf("exit err %v", res.ExitErr)
	}
	if s.Handle() != "sess-1" {
		t.Fatalf("handle %q", s.Handle())
	}
}

func TestArgsAndEnv(t *testing.T) {
	spec := provider.SessionSpec{Model: "m1", Budget: provider.Budget{MaxTurns: 9}, Resume: "s9", OutputSchema: []byte(`{}`),
		Env: []string{"A=1", "ANTHROPIC_API_KEY=k", "B=2"}}
	got := args(spec)
	for _, want := range []string{"-p", "--permission-prompt-tool", "stdio", "--model", "m1", "--max-turns", "9", "--resume", "s9", "--json-schema", "--disallowedTools"} {
		if !contains(got, want) {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
	if contains(got, "--bare") {
		t.Fatal("--bare must never be passed")
	}
	env := childEnv(spec)
	for _, e := range env {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			t.Fatal("api key leaked")
		}
	}
	if !contains(env, "A=1") || !contains(env, "B=2") {
		t.Fatalf("child env dropped entries: %v", env)
	}
	spec.Env = append(spec.Env, "SIRDAR_BILLING=api")
	if !containsPrefix(childEnv(spec), "ANTHROPIC_API_KEY=") {
		t.Fatal("api billing must keep the key")
	}
}

func TestFakeBinaryStdinDenyText(t *testing.T) {
	p := New()
	s, err := p.Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for range s.Events() {
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	var denyLine, allowLine, promptLine string
	for _, line := range res.StderrTail {
		if !strings.HasPrefix(line, "STDIN:") {
			continue
		}
		switch {
		case strings.Contains(line, `"behavior":"deny"`):
			denyLine = line
		case strings.Contains(line, `"behavior":"allow"`):
			allowLine = line
		case strings.Contains(line, `"type":"user"`):
			promptLine = line
		}
	}
	if denyLine == "" {
		t.Fatalf("no deny control_response on stdin, tail: %v", res.StderrTail)
	}
	if !strings.Contains(denyLine, "Sirdar policy: triage runs are read-only") {
		t.Fatalf("deny response missing policy message: %s", denyLine)
	}
	if !strings.Contains(denyLine, `"request_id":"r1"`) || !strings.Contains(denyLine, `"subtype":"success"`) {
		t.Fatalf("deny response malformed: %s", denyLine)
	}
	if allowLine == "" || !strings.Contains(allowLine, `"updatedInput":{"command":"git log -1"}`) {
		t.Fatalf("allow response missing updatedInput: %s", allowLine)
	}
	if promptLine == "" || !strings.Contains(promptLine, `"content":"hello"`) {
		t.Fatalf("prompt line not written to stdin: %s", promptLine)
	}
}

func TestMalformedLineAndExitCode(t *testing.T) {
	script := writeScript(t,
		`not json at all`,
		`{"type":"system","subtype":"init","session_id":"s2"}`,
		`{"$stderr":"boom"}`,
		`{"$exit":3}`,
	)
	spec := fakeSpec(t, script)
	s, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var malformed int
	for ev := range s.Events() {
		if ev.Kind == provider.EvError && ev.Text == "malformed line" {
			malformed++
		}
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatalf("Wait must return the result on non-zero exit: %v", err)
	}
	if malformed != 1 {
		t.Fatalf("malformed count %d", malformed)
	}
	if res.Handle != "s2" {
		t.Fatalf("handle %q", res.Handle)
	}
	if res.ExitErr == nil || !strings.Contains(res.ExitErr.Error(), "3") || !strings.Contains(res.ExitErr.Error(), "boom") {
		t.Fatalf("exit err %v", res.ExitErr)
	}
}

func TestSendFollowUpTurn(t *testing.T) {
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"s3"}`,
		`{"$wait":"user"}`,
		`{"$wait":"user"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second turn"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"session_id":"s3","result":"plain text","total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":2}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), "try again"); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for ev := range s.Events() {
		if ev.Kind == provider.EvAssistantText {
			texts = append(texts, ev.Text)
		}
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 || texts[0] != "second turn" {
		t.Fatalf("assistant texts %v", texts)
	}
	if res.Final != nil {
		t.Fatalf("Final must be nil without structured_output: %s", res.Final)
	}
	if res.Text != "plain text" {
		t.Fatalf("text %q", res.Text)
	}
	if err := s.Send(context.Background(), "too late"); err == nil {
		t.Fatal("Send after exit must fail")
	}
	var sent int
	for _, line := range res.StderrTail {
		if strings.HasPrefix(line, "STDIN:") && strings.Contains(line, `"type":"user"`) {
			sent++
		}
	}
	if sent != 2 {
		t.Fatalf("expected prompt + follow-up on stdin, got %d: %v", sent, res.StderrTail)
	}
}

func TestUnsupportedControlRequestDenied(t *testing.T) {
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"s4"}`,
		`{"type":"control_request","request_id":"r9","request":{"subtype":"hook_callback","callback_id":"c1"}}`,
		`{"$wait":"control_response"}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"session_id":"s4","result":"done","total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	var systems []string
	for ev := range s.Events() {
		if ev.Kind == provider.EvSystem {
			systems = append(systems, ev.Text)
		}
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !contains(systems, "unsupported control request") {
		t.Fatalf("system events %v", systems)
	}
	var denied bool
	for _, line := range res.StderrTail {
		if strings.Contains(line, `"request_id":"r9"`) && strings.Contains(line, `"unsupported control request"`) {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("unsupported control request not denied: %v", res.StderrTail)
	}
}

func TestDoctor(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "claude-ok")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\ncase \"$1\" in\n--version) echo '2.1.267 (Claude Code)';;\nauth) echo 'Logged in as tester';;\nesac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	checks := New().Doctor(context.Background(), ok)
	if len(checks) != 2 {
		t.Fatalf("checks %+v", checks)
	}
	if checks[0].Name != "claude --version" || !checks[0].OK || !strings.HasPrefix(checks[0].Detail, "2.1.267") {
		t.Fatalf("version check %+v", checks[0])
	}
	if checks[1].Name != "claude auth status" || !checks[1].OK || checks[1].Detail != "Logged in as tester" {
		t.Fatalf("auth check %+v", checks[1])
	}

	old := filepath.Join(dir, "claude-old")
	if err := os.WriteFile(old, []byte("#!/bin/sh\ncase \"$1\" in\n--version) echo '1.0.0';;\nauth) echo 'unknown command: auth' >&2; exit 1;;\nesac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	checks = New().Doctor(context.Background(), old)
	if !checks[1].OK || checks[1].Detail != "auth status not supported by this version" {
		t.Fatalf("old auth check %+v", checks[1])
	}

	checks = New().Doctor(context.Background(), filepath.Join(dir, "missing"))
	if checks[0].OK {
		t.Fatalf("missing binary must fail the version check: %+v", checks[0])
	}
}

func TestCancelSendsInterrupt(t *testing.T) {
	// The script stops at a control_response that never arrives, so the fake
	// is alive and blocked when Cancel lands.
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"s5"}`,
		`{"$wait":"control_response"}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"session_id":"s5","result":"never reached","total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range s.Events() {
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for s.Handle() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Handle() != "s5" {
		t.Fatalf("session did not start, handle %q", s.Handle())
	}

	s.Cancel()

	type outcome struct {
		res provider.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := s.Wait()
		done <- outcome{res, err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Wait after Cancel: %v", got.err)
		}
		if got.res.ExitErr == nil {
			t.Fatal("a cancelled session must report ExitErr")
		}
		if got.res.Handle != "s5" {
			t.Fatalf("handle %q", got.res.Handle)
		}
		if !containsPrefix(got.res.StderrTail, "SIGINT") {
			t.Fatalf("fake was not interrupted (SIGKILL?), stderr tail: %v", got.res.StderrTail)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return within 3s of Cancel")
	}
	<-drained
}

func TestUnparseableControlRequestIsAnswered(t *testing.T) {
	// tool_name has the wrong type, so the strict decode fails; the CLI is
	// still blocked and must get a reply.
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"s6"}`,
		`{"type":"control_request","request_id":"r9","request":{"subtype":"can_use_tool","tool_name":123}}`,
		`{"$wait":"control_response"}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"session_id":"s6","result":"done","total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	var noted bool
	var final bool
	for ev := range s.Events() {
		if ev.Text == "unparseable control request" {
			noted = true
		}
		if ev.Kind == provider.EvFinal {
			final = true
		}
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !final {
		t.Fatal("session did not reach its result line")
	}
	if res.ExitErr != nil {
		t.Fatalf("exit err %v", res.ExitErr)
	}
	if !noted {
		t.Fatal("unparseable control request was not surfaced")
	}
	var denied bool
	for _, line := range res.StderrTail {
		if strings.Contains(line, `"request_id":"r9"`) && strings.Contains(line, `"behavior":"deny"`) &&
			strings.Contains(line, "unsupported control request") {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("no deny written for the unparseable request: %v", res.StderrTail)
	}
}

func TestUnparseableControlRequestWithoutID(t *testing.T) {
	script := writeScript(t,
		`{"type":"control_request","request":{"subtype":"can_use_tool","tool_name":123}}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"session_id":"s7","result":"done","total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	var errors int
	for ev := range s.Events() {
		if ev.Kind == provider.EvError && ev.Text == "unparseable control request" {
			errors++
		}
	}
	if _, err := s.Wait(); err != nil {
		t.Fatal(err)
	}
	if errors != 1 {
		t.Fatalf("expected one error event for an unanswerable control request, got %d", errors)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func containsPrefix(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
