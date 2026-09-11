package qwen

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// TestMain doubles as the fake `qwen` binary: when SIRDAR_FAKE_QWEN names a
// script, the test binary replays that script instead of running tests.
func TestMain(m *testing.M) {
	if script := os.Getenv("SIRDAR_FAKE_QWEN"); script != "" {
		os.Exit(fakeCLI(script))
	}
	os.Exit(m.Run())
}

// fakeCLI replays a JSONL script on stdout. Directive lines drive the fake:
//
//	{"$exit":N}                                exit with N
//	{"$stderr":"boom"}                         write a line to stderr
//	{"$block":true}                            wait to be interrupted
//	{"$hookTool":"write_file","$hookInput":{}} call the permission hook
//
// The prompt read from stdin is echoed to stderr as "STDIN:<prompt>", the
// command line as "ARGV:<json>", and any OPENAI_* variable that survived
// into the child as "ENV:<name>=<value>", so tests can inspect all three
// through Result.StderrTail.
func fakeCLI(script string) int {
	// Report a clean interrupt so a test can tell SIGINT from SIGKILL.
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	go func() {
		<-interrupted
		fmt.Fprintln(os.Stderr, "SIGINT")
		os.Exit(0)
	}()

	fmt.Fprintln(os.Stderr, "ARGV:"+mustJSON(os.Args[1:]))
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_MODEL", "QWEN_MODEL"} {
		if v, ok := os.LookupEnv(name); ok {
			fmt.Fprintln(os.Stderr, "ENV:"+name+"="+v)
		}
	}
	prompt, _ := io.ReadAll(os.Stdin)
	fmt.Fprintln(os.Stderr, "STDIN:"+strings.TrimSpace(string(prompt)))

	f, err := os.Open(script)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake qwen:", err)
		return 2
	}
	defer f.Close()

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
			Exit      *int            `json:"$exit"`
			Stderr    string          `json:"$stderr"`
			Block     bool            `json:"$block"`
			HookTool  string          `json:"$hookTool"`
			HookInput json.RawMessage `json:"$hookInput"`
		}
		if err := json.Unmarshal([]byte(line), &directive); err != nil {
			fmt.Fprintln(os.Stderr, "fake qwen: bad directive:", line)
			return 2
		}
		switch {
		case directive.Exit != nil:
			return *directive.Exit
		case directive.Stderr != "":
			fmt.Fprintln(os.Stderr, directive.Stderr)
		case directive.Block:
			select {}
		case directive.HookTool != "":
			if code := callHook(directive.HookTool, directive.HookInput); code != 0 {
				return code
			}
		}
	}
	return 0
}

// callHook posts one PreToolUse event to the hook Sirdar named in the
// settings file, and reports the verdict on stderr as
// "HOOK:<tool>:<decision>:<reason>".
func callHook(tool string, input json.RawMessage) int {
	url, err := hookURLFromSettings(os.Getenv("QWEN_CODE_SYSTEM_SETTINGS_PATH"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake qwen: hook url:", err)
		return 3
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	body := mustJSON(map[string]any{
		"session_id":      "fake",
		"cwd":             ".",
		"hook_event_name": "PreToolUse",
		"permission_mode": "default",
		"tool_name":       tool,
		"tool_input":      input,
		"tool_use_id":     "toolu_" + tool,
	})
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake qwen: hook post:", err)
		return 3
	}
	defer resp.Body.Close()
	var out struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
			HookEventName            string `json:"hookEventName"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fmt.Fprintln(os.Stderr, "fake qwen: hook decode:", err)
		return 3
	}
	o := out.HookSpecificOutput
	fmt.Fprintf(os.Stderr, "HOOK:%s:%s:%s:%s\n", tool, o.HookEventName, o.PermissionDecision, o.PermissionDecisionReason)
	return 0
}

func hookURLFromSettings(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("QWEN_CODE_SYSTEM_SETTINGS_PATH is not set")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var s struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					URL     string `json:"url"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return "", err
	}
	if len(s.Hooks.PreToolUse) == 0 || len(s.Hooks.PreToolUse[0].Hooks) == 0 {
		return "", fmt.Errorf("no PreToolUse hook in %s", path)
	}
	return s.Hooks.PreToolUse[0].Hooks[0].URL, nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
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
		OutputSchema: []byte(`{"type":"object"}`),
		Policy:       &provider.PermissionPolicy{BashAllow: []string{"git log*"}},
		Binary:       os.Args[0],
		Env: append(os.Environ(),
			"SIRDAR_FAKE_QWEN="+abs,
			"OPENAI_API_KEY=should-be-stripped",
			"OPENAI_BASE_URL=http://should-be-stripped",
		),
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

func drain(t *testing.T, s provider.Session) ([]provider.Event, provider.Result) {
	t.Helper()
	var events []provider.Event
	for ev := range s.Events() {
		if len(ev.Raw) == 0 && ev.Kind != provider.EvError {
			t.Errorf("event %s has no Raw", ev.Kind)
		}
		events = append(events, ev)
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return events, res
}

func kinds(events []provider.Event, kind provider.EventKind) []provider.Event {
	var out []provider.Event
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// TestBasicSession replays the transcript captured in
// docs/research/09-qwen-wire-formats.md: an init line, a text turn, a
// shell call the policy allows, a write the policy refuses, and the
// terminal structured_output call.
func TestBasicSession(t *testing.T) {
	s, err := New().Start(context.Background(), fakeSpec(t, "testdata/script-basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events, res := drain(t, s)

	// The hook sees the shell call and the terminal structured_output
	// call. It never sees write_file: Qwen Code's own headless deny list
	// refuses that one before a hook is consulted, which is the static
	// guarantee the adapter leans on.
	perms := kinds(events, provider.EvPermission)
	if len(perms) != 2 {
		t.Fatalf("permission events %+v", perms)
	}
	if perms[0].Tool != "run_shell_command" || perms[0].Decision != "allow" {
		t.Fatalf("shell permission %+v", perms[0])
	}
	if perms[1].Tool != "structured_output" || perms[1].Decision != "allow" {
		t.Fatalf("structured output permission %+v", perms[1])
	}
	writeResult := kinds(events, provider.EvToolFinished)[1]
	if !strings.Contains(writeResult.Text, `Matching deny rule: "edit"`) {
		t.Fatalf("write_file must be refused by the CLI itself: %q", writeResult.Text)
	}

	var tools []string
	for _, ev := range kinds(events, provider.EvToolStarted) {
		tools = append(tools, ev.Tool)
	}
	if len(tools) != 3 || tools[0] != "run_shell_command" || tools[2] != "structured_output" {
		t.Fatalf("tool started %v", tools)
	}
	if n := len(kinds(events, provider.EvToolFinished)); n != 3 {
		t.Fatalf("tool finished %d", n)
	}
	if texts := kinds(events, provider.EvAssistantText); len(texts) != 3 {
		t.Fatalf("assistant texts %d", len(texts))
	}

	final := kinds(events, provider.EvFinal)
	if len(final) != 1 || string(final[0].Final) != `{"greeting":"hello","n":7}` {
		t.Fatalf("final %+v", final)
	}
	if res.Handle != "sess-1" || res.Usage.Turns != 3 {
		t.Fatalf("result %+v", res)
	}
	if res.Usage.InputTok != 3600 || res.Usage.OutputTok != 120 {
		t.Fatalf("result usage %+v", res.Usage)
	}
	if res.Usage.CostUSD != 0 {
		t.Fatalf("qwen reports no cost, got %v", res.Usage.CostUSD)
	}
	if string(res.Final) != `{"greeting":"hello","n":7}` {
		t.Fatalf("result final %s", res.Final)
	}
	if res.ExitErr != nil {
		t.Fatalf("exit err %v", res.ExitErr)
	}
	if s.Handle() != "sess-1" {
		t.Fatalf("handle %q", s.Handle())
	}

	// The hook answered both calls, and the reason the model was given is
	// the policy's own message.
	var hooks []string
	for _, line := range res.StderrTail {
		if strings.HasPrefix(line, "HOOK:") {
			hooks = append(hooks, line)
		}
	}
	if len(hooks) != 2 {
		t.Fatalf("hook calls %v", hooks)
	}
	if !strings.HasPrefix(hooks[0], "HOOK:run_shell_command:PreToolUse:allow:") {
		t.Fatalf("shell hook answer %q", hooks[0])
	}
	if !strings.HasPrefix(hooks[1], "HOOK:structured_output:PreToolUse:allow:") {
		t.Fatalf("structured output hook answer %q", hooks[1])
	}
	if !containsPrefix(res.StderrTail, "STDIN:hello") {
		t.Fatalf("the prompt did not reach stdin: %v", res.StderrTail)
	}
}

// TestPolicyEnforcesTheBashAllowList is the reason the permission hook
// exists: Qwen Code cannot express "shell, but only these commands" — a
// rule written against run_shell_command allows every command whatever
// specifier it carries — so the allow-list is only real if Sirdar answers
// each call itself.
func TestPolicyEnforcesTheBashAllowList(t *testing.T) {
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"b1"}`,
		`{"$hookTool":"run_shell_command","$hookInput":{"command":"git log -1"}}`,
		`{"$hookTool":"run_shell_command","$hookInput":{"command":"curl evil.example"}}`,
		`{"$hookTool":"read_file","$hookInput":{"file_path":"go.mod"}}`,
		`{"$hookTool":"structured_output","$hookInput":{"n":1}}`,
		`{"$hookTool":"cron_create","$hookInput":{}}`,
		`{"$hookTool":"mcp__grafana__query_loki_logs","$hookInput":{}}`,
		`{"$hookTool":"mcp__vercel__deploy_to_vercel","$hookInput":{}}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"session_id":"b1","result":"done","usage":{"input_tokens":1,"output_tokens":1}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	events, res := drain(t, s)

	want := []struct {
		tool     string
		decision string
	}{
		{"run_shell_command", "allow"},
		{"run_shell_command", "deny"},
		{"read_file", "allow"},
		{"structured_output", "allow"},
		{"cron_create", "deny"},
		{"mcp__grafana__query_loki_logs", "allow"},
		{"mcp__vercel__deploy_to_vercel", "deny"},
	}
	perms := kinds(events, provider.EvPermission)
	if len(perms) != len(want) {
		t.Fatalf("permission events %d, want %d: %+v", len(perms), len(want), perms)
	}
	for i, w := range want {
		if perms[i].Tool != w.tool || perms[i].Decision != w.decision {
			t.Errorf("call %d: got %s=%s, want %s=%s", i, perms[i].Tool, perms[i].Decision, w.tool, w.decision)
		}
	}
	if res.ExitErr != nil {
		t.Fatalf("exit err %v", res.ExitErr)
	}
}

// TestUnparseableHookRequestIsDenied is the fail-closed half of the
// mediator: Qwen Code is blocked waiting for an answer, so a payload that
// cannot be read still gets one, and it is a refusal.
func TestUnparseableHookRequestIsDenied(t *testing.T) {
	s := &session{
		policy: &provider.PermissionPolicy{},
		events: make(chan provider.Event, 8),
	}
	rec := &recorder{}
	req, err := http.NewRequest(http.MethodPost, hookPath, strings.NewReader(`{"tool_input":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	s.decide(rec, req)

	var out struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
			HookEventName      string `json:"hookEventName"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(rec.body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.body.String(), err)
	}
	if out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("a request that cannot be read must be denied, got %q", rec.body.String())
	}
	if out.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Fatalf("hookEventName is required: %q", rec.body.String())
	}
	select {
	case ev := <-s.events:
		if ev.Kind != provider.EvError {
			t.Fatalf("event %+v", ev)
		}
	default:
		t.Fatal("an unreadable hook request was not surfaced")
	}
}

type recorder struct {
	body   bytes.Buffer
	header http.Header
	code   int
}

func (r *recorder) Header() http.Header {
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}
func (r *recorder) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *recorder) WriteHeader(code int)        { r.code = code }

func TestArgs(t *testing.T) {
	spec := provider.SessionSpec{
		Model:        "m1",
		Budget:       provider.Budget{MaxTurns: 9, MaxMinutes: 25},
		Resume:       "s9",
		OutputSchema: []byte(`{ "type" : "object" }`),
		Policy:       &provider.PermissionPolicy{BashAllow: []string{"git log*"}},
	}
	got := args(spec, Endpoint{}, nil)
	for _, want := range []string{
		"--output-format", "stream-json", "--approval-mode", "default",
		"--json-schema", "--model", "m1", "--resume", "s9",
		"--max-wall-time", "25m", "--allowed-tools", shellTool,
	} {
		if !contains(got, want) {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
	// The terminal structured_output call needs a turn of its own, or a
	// run that has its answer dies on exit 53 instead of reporting it.
	if v := flagValue(got, "--max-session-turns"); v != "10" {
		t.Fatalf("--max-session-turns %q, want the budget plus one", v)
	}
	if v := flagValue(got, "--json-schema"); v != `{"type":"object"}` {
		t.Fatalf("--json-schema %q", v)
	}
	for _, never := range []string{"--bare", "--safe-mode", "--yolo", "--input-format", "--acp"} {
		if contains(got, never) {
			t.Fatalf("%s must never be passed: %v", never, got)
		}
	}
	if contains(got, "--auth-type") {
		t.Fatal("an unconfigured endpoint must leave auth to the operator's own login")
	}

	// Without bash patterns the shell stays under Qwen Code's own
	// headless deny, which is the only fail-closed guarantee there is:
	// the permission hook fails open when it cannot be reached.
	spec.Policy = &provider.PermissionPolicy{}
	if contains(args(spec, Endpoint{}, nil), "--allowed-tools") {
		t.Fatal("a workspace that named no bash patterns must get no shell")
	}
	spec.Policy = nil
	if contains(args(spec, Endpoint{}, nil), "--allowed-tools") {
		t.Fatal("a nil policy must get no shell")
	}

	full := Endpoint{Model: "m2", BaseURL: "http://x/v1", APIKey: "k"}
	got = args(provider.SessionSpec{OutputSchema: []byte(`{}`)}, full, nil)
	if v := flagValue(got, "--auth-type"); v != "openai" {
		t.Fatalf("a configured endpoint must select openai auth: %v", got)
	}
	if v := flagValue(got, "--model"); v != "m2" {
		t.Fatalf("the endpoint's model must be used when the spec names none: %v", got)
	}
}

// TestMCPArgs is the workspace-only restriction. Qwen Code has no
// --strict-mcp-config: --mcp-config merges with the operator's own
// settings, so the file has to be named *and* its servers allow-listed.
func TestMCPArgs(t *testing.T) {
	spec := provider.SessionSpec{OutputSchema: []byte(`{}`)}
	names, err := mcpServerNames(spec)
	if err != nil {
		t.Fatal(err)
	}
	got := args(spec, Endpoint{}, names)
	if contains(got, "--mcp-config") || contains(got, "--allowed-mcp-server-names") {
		t.Fatalf("no config was named and nothing was strict: %v", got)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"zeta":{"command":"z"},"alpha":{"command":"a"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec.MCPConfig = path
	spec.MCPStrict = true
	names, err = mcpServerNames(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "zeta" {
		t.Fatalf("server names %v", names)
	}
	got = args(spec, Endpoint{}, names)
	if flagValue(got, "--mcp-config") != path {
		t.Fatalf("--mcp-config does not name the file: %v", got)
	}
	if n := countFlag(got, "--allowed-mcp-server-names"); n != 2 {
		t.Fatalf("each server needs its own flag, got %d: %v", n, got)
	}
}

// TestMCPStrictWithoutAWorkspaceConfig is the Claude adapter's N2 in Qwen
// terms: mcp.workspaceOnly with no .mcp.json must load no servers, and
// passing nothing would load every server the operator has.
func TestMCPStrictWithoutAWorkspaceConfig(t *testing.T) {
	names, err := mcpServerNames(provider.SessionSpec{MCPStrict: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != noMCPServer {
		t.Fatalf("names %v, want one sentinel no server matches", names)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err = mcpServerNames(provider.SessionSpec{MCPConfig: path, MCPStrict: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != noMCPServer {
		t.Fatalf("an empty config must still restrict, got %v", names)
	}

	if _, err := mcpServerNames(provider.SessionSpec{MCPConfig: filepath.Join(dir, "missing.json")}); err == nil {
		t.Fatal("a named config that cannot be read is an error")
	}
}

func TestChildEnv(t *testing.T) {
	spec := provider.SessionSpec{Env: []string{
		"A=1", "OPENAI_API_KEY=leaked", "OPENAI_BASE_URL=http://leaked",
		"OPENAI_MODEL=leaked", "QWEN_MODEL=leaked",
		"QWEN_CODE_SYSTEM_SETTINGS_PATH=/somewhere/else", "B=2",
	}}

	env := childEnv(spec, Endpoint{}, "/tmp/s.json")
	for _, banned := range []string{"OPENAI_API_KEY=leaked", "OPENAI_BASE_URL=http://leaked", "OPENAI_MODEL=leaked", "QWEN_MODEL=leaked"} {
		if contains(env, banned) {
			t.Fatalf("%q survived into the child env", banned)
		}
	}
	if !contains(env, "A=1") || !contains(env, "B=2") {
		t.Fatalf("child env dropped entries: %v", env)
	}
	if !contains(env, "QWEN_CODE_SYSTEM_SETTINGS_PATH=/tmp/s.json") {
		t.Fatalf("the session's own settings file must win: %v", env)
	}
	if contains(env, "QWEN_CODE_SYSTEM_SETTINGS_PATH=/somewhere/else") {
		t.Fatalf("the caller's settings path must not survive: %v", env)
	}

	env = childEnv(spec, Endpoint{Model: "m", BaseURL: "http://x/v1", APIKey: "secret"}, "")
	for _, want := range []string{"OPENAI_API_KEY=secret", "OPENAI_BASE_URL=http://x/v1", "OPENAI_MODEL=m"} {
		if !contains(env, want) {
			t.Fatalf("missing %q in %v", want, env)
		}
	}

	// A spec-level model override reaches the auth inference too, which
	// needs a model named alongside the key and the base URL.
	env = childEnv(provider.SessionSpec{Env: []string{"A=1"}, Model: "override"},
		Endpoint{BaseURL: "http://x/v1", APIKey: "secret"}, "")
	if !contains(env, "OPENAI_MODEL=override") {
		t.Fatalf("spec model did not reach the env: %v", env)
	}
}

// TestEndpointSecretsStayOutOfDoctor keeps the key off the one report an
// operator pastes into a ticket.
func TestEndpointSecretsStayOutOfDoctor(t *testing.T) {
	p := &Provider{endpoint: Endpoint{Model: "m", BaseURL: "http://x/v1", APIKey: "sk-secret"}}
	c := p.endpointCheck()
	if !c.OK || !strings.Contains(c.Detail, "http://x/v1") {
		t.Fatalf("endpoint check %+v", c)
	}
	if strings.Contains(c.Detail, "sk-secret") {
		t.Fatalf("the api key leaked into doctor: %q", c.Detail)
	}

	p = &Provider{}
	if c := p.endpointCheck(); !c.OK || !strings.Contains(c.Detail, "own login") {
		t.Fatalf("an unconfigured endpoint is not an error: %+v", c)
	}

	p = &Provider{endpoint: Endpoint{BaseURL: "http://x/v1"}}
	c = p.endpointCheck()
	if c.OK {
		t.Fatal("a half-configured endpoint must fail the check")
	}
	if !strings.Contains(c.Detail, "qwen.model") || !strings.Contains(c.Detail, "qwen.apiKey") {
		t.Fatalf("the check must name what is missing: %q", c.Detail)
	}
}

// TestSendAlwaysFails records the structural limit: --json-schema and
// --input-format stream-json cannot both be passed, so there is no second
// user message and the runner has to resume the handle instead.
func TestSendAlwaysFails(t *testing.T) {
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"s3"}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"session_id":"s3","result":"plain text","usage":{"input_tokens":1,"output_tokens":2}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), "try again"); err == nil {
		t.Fatal("Send must fail so the runner resumes the handle instead")
	} else if !strings.Contains(err.Error(), "resume") {
		t.Fatalf("the error must point at the way out, got %v", err)
	}
	if err := s.CloseInput(); err != nil {
		t.Fatalf("CloseInput must be a no-op: %v", err)
	}
	_, res := drain(t, s)
	if res.Final != nil {
		t.Fatalf("Final must be nil without structured_result: %s", res.Final)
	}
	if res.Text != "plain text" || res.Handle != "s3" {
		t.Fatalf("result %+v", res)
	}
}

// TestTurnBudgetOverrunHasNoResultLine is the shape a run that overran
// --max-session-turns has: the stream stops without a result, exit 53, and
// stderr is the only account of it.
func TestTurnBudgetOverrunHasNoResultLine(t *testing.T) {
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"s4"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"thinking"}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`,
		`{"$stderr":"Reached max session turns for this session."}`,
		`{"$exit":53}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	events, res := drain(t, s)
	if n := len(kinds(events, provider.EvFinal)); n != 0 {
		t.Fatalf("a turn-budget overrun writes no result line, got %d final events", n)
	}
	if res.Handle != "s4" {
		t.Fatalf("handle %q", res.Handle)
	}
	if res.ExitErr == nil {
		t.Fatal("a non-zero exit must be reported")
	}
	for _, want := range []string{"53", "max session turns", "Reached max session turns"} {
		if !strings.Contains(res.ExitErr.Error(), want) {
			t.Fatalf("exit err %q is missing %q", res.ExitErr, want)
		}
	}
}

func TestMalformedLine(t *testing.T) {
	script := writeScript(t,
		`not json at all`,
		`{"type":"system","subtype":"init","session_id":"s2"}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"session_id":"s2","result":"ok","usage":{}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	events, res := drain(t, s)
	var malformed int
	for _, ev := range kinds(events, provider.EvError) {
		if ev.Text == "malformed line" {
			malformed++
		}
	}
	if malformed != 1 {
		t.Fatalf("malformed count %d", malformed)
	}
	if res.ExitErr != nil {
		t.Fatalf("exit err %v", res.ExitErr)
	}
}

// TestUsageAccumulatesWithoutDoubleCountingCache is the token arithmetic
// the capture settled: Qwen Code's input_tokens is the whole prompt and
// cache_read_input_tokens is the part of it that was cached, so adding
// them — which is what the Claude adapter has to do — reports a cached run
// at nearly twice its size.
func TestUsageAccumulatesWithoutDoubleCountingCache(t *testing.T) {
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"u1"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"one"}],"usage":{"input_tokens":0,"output_tokens":0}}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"read_file","input":{}}],"usage":{"input_tokens":1200,"output_tokens":40,"cache_read_input_tokens":800,"total_tokens":1240}}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"two"}],"usage":{"input_tokens":1300,"output_tokens":60,"cache_read_input_tokens":1200,"total_tokens":1360}}}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"session_id":"u1","result":"done","usage":{"input_tokens":2500,"output_tokens":100,"cache_read_input_tokens":2000,"total_tokens":2600}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	events, res := drain(t, s)
	usage := kinds(events, provider.EvUsage)
	if len(usage) != 3 {
		t.Fatalf("want a usage event per turn that spent tokens plus the result, got %d", len(usage))
	}
	if usage[0].Turns != 1 || usage[0].InputTok != 1200 || usage[0].OutputTok != 40 {
		t.Fatalf("first turn %+v", usage[0])
	}
	if usage[1].Turns != 2 || usage[1].InputTok != 2500 || usage[1].OutputTok != 100 {
		t.Fatalf("running total %+v", usage[1])
	}
	if usage[2].InputTok != 2500 || usage[2].Turns != 2 {
		t.Fatalf("the result line's own totals are authoritative: %+v", usage[2])
	}
	if res.Usage.Turns != 2 || res.Usage.InputTok != 2500 {
		t.Fatalf("result usage %+v", res.Usage)
	}
}

func TestCancelSendsInterrupt(t *testing.T) {
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"s5"}`,
		`{"$block":true}`,
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

	deadline := time.Now().Add(3 * time.Second)
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
			t.Fatalf("the fake was not interrupted (SIGKILL?), stderr tail: %v", got.res.StderrTail)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return within 5s of Cancel")
	}
	<-drained
}

// TestSettingsFileIsRemoved keeps the private settings file from
// outliving the session that needed it.
func TestSettingsFileIsRemoved(t *testing.T) {
	script := writeScript(t,
		`{"type":"system","subtype":"init","session_id":"s6"}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"session_id":"s6","result":"ok","usage":{}}`,
	)
	s, err := New().Start(context.Background(), fakeSpec(t, script))
	if err != nil {
		t.Fatal(err)
	}
	dir := s.(*session).settingsIn
	if dir == "" {
		t.Fatal("no settings directory was made")
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
		t.Fatalf("settings file: %v", err)
	}
	_, res := drain(t, s)
	if res.ExitErr != nil {
		t.Fatalf("exit err %v", res.ExitErr)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the settings directory outlived the session: %v", err)
	}
}

func TestSettingsContent(t *testing.T) {
	dir := t.TempDir()
	path, err := writeSettings(dir, "http://127.0.0.1:1234/decide")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	for _, want := range deniedTools {
		if !contains(s.Permissions.Deny, want) {
			t.Fatalf("%q must be denied in the session's settings: %s", want, b)
		}
	}
	url, err := hookURLFromSettings(path)
	if err != nil || url != "http://127.0.0.1:1234/decide" {
		t.Fatalf("hook url %q / %v", url, err)
	}
}

func TestPolicyName(t *testing.T) {
	for tool, want := range map[string]string{
		"run_shell_command":           "Bash",
		"read_file":                   "Read",
		"grep_search":                 "Grep",
		"write_file":                  "Write",
		"edit":                        "Edit",
		"structured_output":           "StructuredOutput",
		"mcp__grafana__query_loki":    "mcp__grafana__query_loki",
		"cron_create":                 "cron_create",
		"a_tool_qwen_has_not_shipped": "a_tool_qwen_has_not_shipped",
	} {
		if got := policyName(tool); got != want {
			t.Errorf("policyName(%q) = %q, want %q", tool, got, want)
		}
	}
}

func TestDoctor(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "qwen-ok")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\necho 0.23.3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	checks := New().Doctor(context.Background(), ok)
	if len(checks) != 2 {
		t.Fatalf("checks %+v", checks)
	}
	if checks[0].Name != "qwen --version" || !checks[0].OK || checks[0].Detail != "0.23.3" {
		t.Fatalf("version check %+v", checks[0])
	}
	if checks[1].Name != "qwen endpoint" || !checks[1].OK {
		t.Fatalf("endpoint check %+v", checks[1])
	}

	odd := filepath.Join(dir, "qwen-odd")
	if err := os.WriteFile(odd, []byte("#!/bin/sh\necho 'qwen code'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if checks = New().Doctor(context.Background(), odd); checks[0].OK {
		t.Fatalf("a version that is not a version must fail: %+v", checks[0])
	}

	if checks = New().Doctor(context.Background(), filepath.Join(dir, "missing")); checks[0].OK {
		t.Fatalf("missing binary must fail the version check: %+v", checks[0])
	}
}

func TestName(t *testing.T) {
	if got := New().Name(); got != "qwen" {
		t.Fatalf("Name() = %q", got)
	}
	if got := NewEndpoint(Endpoint{Binary: "/usr/local/bin/qwen"}).Name(); got != "qwen" {
		t.Fatalf("Name() = %q", got)
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

// flagValue returns the argument after name, or "" when name is absent or
// last.
func flagValue(argv []string, name string) string {
	for i, a := range argv {
		if a == name && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func countFlag(argv []string, name string) int {
	n := 0
	for _, a := range argv {
		if a == name {
			n++
		}
	}
	return n
}
