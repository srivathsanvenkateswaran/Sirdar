package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// TestMain doubles as the fake ACP agent: when SIRDAR_FAKE_ACP names a
// script, the test binary replays that script over stdio instead of running
// tests.
func TestMain(m *testing.M) {
	if script := os.Getenv("SIRDAR_FAKE_ACP"); script != "" {
		os.Exit(fakeAgent(script))
	}
	os.Exit(m.Run())
}

// ----------------------------------------------------------- fake ACP agent

// inbound is one client-to-agent line the fake read from stdin.
type inbound struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// fakeAgent replays a JSONL script against stdio and returns a process exit
// code. Directives:
//
//	{"$expect":"<method>","as":"<name>"}   block until that client message
//	                                       arrives; remember its id as <name>
//	{"$reply":{...}}                       respond to the last expected id
//	{"$reply_to":"<name>","result":{...}}  respond to a remembered id
//	{"$request":"<m>","id":N,"params":{}}  send an agent-to-client request,
//	                                       then block until id N is answered,
//	                                       echoing the answer to stderr
//	{"$stderr":"..."}                      write a line to stderr
//	{"$exit":N}                            exit with that code
//
// Any other line is an agent-to-client message written to stdout verbatim,
// with {{cwd}} replaced by the fake's working directory — which is the
// session's Cwd, since the provider sets cmd.Dir. Every stdin line is
// echoed to stderr prefixed "STDIN: " so tests can assert on what the
// provider sent.
func fakeAgent(scriptPath string) int {
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake acp:", err)
		return 2
	}
	cwd, _ := os.Getwd()

	in := make(chan inbound, 64)
	go func() {
		defer close(in)
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			fmt.Fprintf(os.Stderr, "STDIN: %s\n", line)
			var msg inbound
			if err := json.Unmarshal(line, &msg); err != nil {
				fmt.Fprintf(os.Stderr, "fake acp: bad stdin line: %v\n", err)
				continue
			}
			in <- msg
		}
	}()

	out := bufio.NewWriter(os.Stdout)
	write := func(format string, args ...any) {
		fmt.Fprintf(out, format+"\n", args...)
		_ = out.Flush()
	}

	ids := map[string]json.RawMessage{}
	var lastID json.RawMessage

	for _, raw := range bytes.Split(script, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 || bytes.HasPrefix(line, []byte("//")) {
			continue
		}
		line = bytes.ReplaceAll(line, []byte("{{cwd}}"), []byte(cwd))

		var d map[string]json.RawMessage
		if err := json.Unmarshal(line, &d); err != nil {
			fmt.Fprintf(os.Stderr, "fake acp: bad script line: %v\n", err)
			return 2
		}

		switch {
		case d["$expect"] != nil:
			method := unquote(d["$expect"])
			name := method
			if d["as"] != nil {
				name = unquote(d["as"])
			}
			for {
				msg, ok := <-in
				if !ok {
					fmt.Fprintf(os.Stderr, "fake acp: stdin closed waiting for %s\n", method)
					return 3
				}
				if msg.Method == method {
					lastID = msg.ID
					ids[name] = msg.ID
					break
				}
			}

		case d["$reply"] != nil:
			if lastID == nil {
				fmt.Fprintln(os.Stderr, "fake acp: $reply with no expected id")
				return 2
			}
			write(`{"jsonrpc":"2.0","id":%s,"result":%s}`, lastID, d["$reply"])

		case d["$reply_to"] != nil:
			id, ok := ids[unquote(d["$reply_to"])]
			if !ok {
				fmt.Fprintln(os.Stderr, "fake acp: $reply_to names no remembered id")
				return 2
			}
			write(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, d["result"])

		case d["$request"] != nil:
			method := unquote(d["$request"])
			id := d["id"]
			params := d["params"]
			if params == nil {
				params = json.RawMessage("{}")
			}
			write(`{"jsonrpc":"2.0","id":%s,"method":%q,"params":%s}`, id, method, params)
			for {
				msg, ok := <-in
				if !ok {
					fmt.Fprintf(os.Stderr, "fake acp: stdin closed waiting for a reply to %s\n", id)
					return 3
				}
				if sameID(msg.ID, id) {
					answer := msg.Result
					if answer == nil {
						answer = msg.Error
					}
					fmt.Fprintf(os.Stderr, "ANSWER %s %s: %s\n", id, method, answer)
					break
				}
			}

		case d["$stderr"] != nil:
			fmt.Fprintln(os.Stderr, unquote(d["$stderr"]))

		case d["$exit"] != nil:
			var code int
			_ = json.Unmarshal(d["$exit"], &code)
			return code

		default:
			write("%s", line)
		}
	}

	// Drain until the provider closes stdin, so trailing traffic still
	// reaches the stderr echo.
	for range in { //nolint:revive // draining
	}
	return 0
}

func unquote(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return strings.Trim(string(raw), `"`)
	}
	return s
}

func sameID(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return ca.String() == cb.String()
}

// -------------------------------------------------------------- test helpers

// workspace is a session Cwd with one stdio MCP server declared, so every
// test asserts what session/new was handed.
func workspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mcp := `{"mcpServers":{"evidence":{"command":"/usr/bin/evidence","args":["--stdio"],"env":{"TOKEN":"t0ken"}}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcp), 0o644); err != nil {
		t.Fatalf("write .mcp.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatalf("write inside.txt: %v", err)
	}
	return dir
}

func spawn(t *testing.T, script, cwd string, mutate func(*provider.SessionSpec)) provider.Session {
	t.Helper()
	spec := specFor(t, script, cwd)
	if mutate != nil {
		mutate(&spec)
	}
	sess, err := New(Config{}).Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return sess
}

func specFor(t *testing.T, script, cwd string) provider.SessionSpec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	path, err := filepath.Abs(filepath.Join("testdata", script))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	return provider.SessionSpec{
		Cwd:          cwd,
		Prompt:       "Triage ticket ABC-1.",
		OutputSchema: []byte(`{"type":"object"}`),
		Policy:       &provider.PermissionPolicy{Root: cwd},
		Binary:       exe,
		Env:          append(os.Environ(), "SIRDAR_FAKE_ACP="+path),
	}
}

func drain(sess provider.Session) []provider.Event {
	var evs []provider.Event
	for ev := range sess.Events() {
		evs = append(evs, ev)
	}
	return evs
}

func only(evs []provider.Event, kind provider.EventKind) []provider.Event {
	var out []provider.Event
	for _, ev := range evs {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// stderrLines returns the fake's stderr echo, which carries both the
// client-to-agent lines ("STDIN: ") and the answers to the agent's own
// requests ("ANSWER ").
func stderrLines(t *testing.T, res provider.Result) []string {
	t.Helper()
	if len(res.StderrTail) == 0 {
		t.Fatalf("the fake agent echoed nothing to stderr")
	}
	return res.StderrTail
}

func findSent(t *testing.T, res provider.Result, method string) string {
	t.Helper()
	for _, line := range stderrLines(t, res) {
		rest, ok := strings.CutPrefix(line, "STDIN: ")
		if !ok {
			continue
		}
		var msg inbound
		if err := json.Unmarshal([]byte(rest), &msg); err != nil {
			continue
		}
		if msg.Method == method {
			return rest
		}
	}
	t.Fatalf("the provider never sent %s; stderr=%v", method, res.StderrTail)
	return ""
}

func findAnswer(t *testing.T, res provider.Result, method string) string {
	t.Helper()
	for _, line := range stderrLines(t, res) {
		if strings.HasPrefix(line, "ANSWER ") && strings.Contains(line, " "+method+": ") {
			_, answer, _ := strings.Cut(line, ": ")
			return answer
		}
	}
	t.Fatalf("the provider never answered %s; stderr=%v", method, res.StderrTail)
	return ""
}

// --------------------------------------------------------------------- tests

func TestSessionRunsATurnAndReturnsTheNote(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-basic.jsonl", cwd, nil)

	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if got := string(res.Final); got != `{"title":"Checkout 500s","ok":true}` {
		t.Errorf("Result.Final = %s", got)
	}
	if res.Handle != "sess-1" || sess.Handle() != "sess-1" {
		t.Errorf("handle = %q / %q, want sess-1", res.Handle, sess.Handle())
	}
	if res.Usage.Turns != 1 {
		t.Errorf("Usage.Turns = %d, want 1", res.Usage.Turns)
	}
	if res.Usage.CostUSD != 0.25 {
		t.Errorf("Usage.CostUSD = %v, want 0.25", res.Usage.CostUSD)
	}

	finals := only(evs, provider.EvFinal)
	if len(finals) != 1 || string(finals[0].Final) != string(res.Final) {
		t.Fatalf("final events = %+v", finals)
	}

	started := only(evs, provider.EvToolStarted)
	if len(started) != 1 || started[0].Tool != "read" {
		t.Errorf("tool started events = %+v", started)
	}
	if got := string(started[0].Input); got != `{"path":"src/checkout.go"}` {
		t.Errorf("tool input = %s", got)
	}
	finishedRead := false
	for _, ev := range only(evs, provider.EvToolFinished) {
		if ev.Tool == "read" {
			finishedRead = true
		}
	}
	if !finishedRead {
		t.Errorf("no tool_call_update was reported finished; events = %+v", only(evs, provider.EvToolFinished))
	}

	// The assistant text is streamed as well as parsed.
	var text strings.Builder
	for _, ev := range only(evs, provider.EvAssistantText) {
		text.WriteString(ev.Text)
	}
	if !strings.Contains(text.String(), "Checkout 500s") {
		t.Errorf("assistant text = %q", text.String())
	}

	// Permissions: the write was rejected, the read allowed. The refused
	// out-of-workspace fs/read_text_file is a permission event too, and is
	// checked separately below.
	var perms []provider.Event
	for _, ev := range only(evs, provider.EvPermission) {
		if ev.Tool != "fs/read_text_file" {
			perms = append(perms, ev)
		}
	}
	if len(perms) != 2 {
		t.Fatalf("permission events = %+v", perms)
	}
	if perms[0].Decision != "deny" || perms[0].Tool != "Write" {
		t.Errorf("first permission = %+v", perms[0])
	}
	if !strings.Contains(perms[0].Text, "read-only") {
		t.Errorf("deny message = %q", perms[0].Text)
	}
	if perms[1].Decision != "allow" || perms[1].Tool != "Read" {
		t.Errorf("second permission = %+v", perms[1])
	}

	if got := findAnswer(t, res, "session/request_permission"); !strings.Contains(got, `"optionId":"no"`) {
		t.Errorf("write permission answer = %s, want the reject_once option", got)
	}
	answers := 0
	for _, line := range res.StderrTail {
		if strings.Contains(line, "session/request_permission: ") {
			answers++
			if answers == 2 && !strings.Contains(line, `"optionId":"yes"`) {
				t.Errorf("read permission answer = %s, want the allow_once option", line)
			}
		}
	}
	if answers != 2 {
		t.Errorf("answered %d permission requests, want 2", answers)
	}

	// fs/read_text_file: inside the workspace is served, outside is refused.
	if got := findAnswer(t, res, "fs/read_text_file"); !strings.Contains(got, "line one") {
		t.Errorf("in-workspace read answer = %s", got)
	}
	outside := ""
	for _, line := range res.StderrTail {
		if strings.Contains(line, "fs/read_text_file: ") {
			outside = line
		}
	}
	if !strings.Contains(outside, "outside it") {
		t.Errorf("out-of-workspace read answer = %s, want a JSON-RPC error", outside)
	}
}

func TestSessionNewCarriesCwdAndWorkspaceMCPServers(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-basic.jsonl", cwd, nil)
	drain(sess)
	res, _ := sess.Wait()

	line := findSent(t, res, "session/new")
	var msg struct {
		Params struct {
			Cwd        string `json:"cwd"`
			MCPServers []struct {
				Name    string   `json:"name"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
				Env     []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"env"`
			} `json:"mcpServers"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("parse session/new: %v", err)
	}
	if msg.Params.Cwd != cwd {
		t.Errorf("session/new cwd = %q, want %q", msg.Params.Cwd, cwd)
	}
	if len(msg.Params.MCPServers) != 1 {
		t.Fatalf("session/new mcpServers = %+v", msg.Params.MCPServers)
	}
	srv := msg.Params.MCPServers[0]
	if srv.Name != "evidence" || srv.Command != "/usr/bin/evidence" || len(srv.Args) != 1 || srv.Args[0] != "--stdio" {
		t.Errorf("mcp server = %+v", srv)
	}
	if len(srv.Env) != 1 || srv.Env[0].Name != "TOKEN" || srv.Env[0].Value != "t0ken" {
		t.Errorf("mcp server env = %+v", srv.Env)
	}

	// The prompt carries the schema and the JSON-only instruction, which is
	// the only structured-output mechanism ACP has.
	prompt := findSent(t, res, "session/prompt")
	if !strings.Contains(prompt, "Triage ticket ABC-1.") || !strings.Contains(prompt, "JSON Schema") {
		t.Errorf("session/prompt = %s", prompt)
	}
}

func TestProseFinalIsRetriedWithASecondPrompt(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-retry.jsonl", cwd, nil)

	var (
		final    json.RawMessage
		errTexts []string
		sendErr  error
	)
	for ev := range sess.Events() {
		switch ev.Kind {
		case provider.EvError:
			errTexts = append(errTexts, ev.Text)
		case provider.EvFinal:
			if len(ev.Final) > 0 {
				final = ev.Final
				continue
			}
			// This is what the runner does with a note that fails
			// validation: answer on the running session.
			sendErr = sess.Send(context.Background(), "That was not JSON. Reply with the object only.")
		}
	}
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if sendErr != nil {
		t.Fatalf("Send: %v", sendErr)
	}
	if len(errTexts) != 1 || !strings.Contains(errTexts[0], "did not return a JSON note") {
		t.Errorf("error events = %v", errTexts)
	}
	if string(final) != `{"title":"Second time","ok":true}` {
		t.Errorf("final = %s", final)
	}
	if string(res.Final) != string(final) {
		t.Errorf("Result.Final = %s", res.Final)
	}
	if res.Usage.Turns != 2 {
		t.Errorf("Usage.Turns = %d, want 2", res.Usage.Turns)
	}

	prompts := 0
	for _, line := range res.StderrTail {
		if strings.Contains(line, `"method":"session/prompt"`) {
			prompts++
		}
	}
	if prompts != 2 {
		t.Errorf("sent %d session/prompt requests, want 2", prompts)
	}
}

func TestCancelEndsTheTurn(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-cancel.jsonl", cwd, nil)

	go func() {
		time.Sleep(50 * time.Millisecond)
		sess.Cancel()
	}()

	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(res.Final) != 0 {
		t.Errorf("Result.Final = %s, want none", res.Final)
	}
	cancelled := false
	for _, ev := range only(evs, provider.EvSystem) {
		if strings.Contains(ev.Text, "cancelled") {
			cancelled = true
		}
	}
	if !cancelled {
		t.Errorf("no cancellation event; events = %+v", evs)
	}
	findSent(t, res, "session/cancel")
}

func TestResumeNeedsTheLoadSessionCapability(t *testing.T) {
	cwd := workspace(t)
	spec := specFor(t, "script-no-resume.jsonl", cwd)
	spec.Resume = "sess-1"

	sess, err := New(Config{}).Start(context.Background(), spec)
	if err == nil {
		sess.Cancel()
		t.Fatalf("Start succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "loadSession") {
		t.Errorf("error = %v", err)
	}
	select {
	case <-lastPumpDone:
	case <-time.After(5 * time.Second):
		t.Error("the event pump leaked after a failed Start")
	}
}

func TestDoctorReportsTheAgentAndItsCapabilities(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	path, err := filepath.Abs(filepath.Join("testdata", "script-doctor.jsonl"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	t.Setenv("SIRDAR_FAKE_ACP", path)

	checks := New(Config{Command: exe}).Doctor(context.Background(), "")
	if len(checks) != 2 {
		t.Fatalf("checks = %+v", checks)
	}
	if !checks[0].OK || !strings.Contains(checks[0].Detail, "gemini-cli 0.59.0") {
		t.Errorf("agent check = %+v", checks[0])
	}
	if !checks[1].OK || !strings.Contains(checks[1].Detail, "loadSession=true") {
		t.Errorf("capability check = %+v", checks[1])
	}
	if !strings.Contains(checks[1].Detail, "image prompts=true") {
		t.Errorf("capability check = %+v", checks[1])
	}
}

func TestDoctorReportsAMissingAgent(t *testing.T) {
	checks := New(Config{Command: filepath.Join(t.TempDir(), "no-such-agent")}).Doctor(context.Background(), "")
	if len(checks) != 1 || checks[0].OK {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestStripFenceAndJSONObject(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"{\"a\":1}", `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n{\"a\":1}\n```", `{"a":1}`},
		{"Here you go:\n{\"a\":1}", ""},
		{"just prose", ""},
		{"[1,2,3]", ""},
	}
	for _, c := range cases {
		got := string(jsonObject(c.in))
		if got != c.want {
			t.Errorf("jsonObject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPolicyToolMapsACPKinds(t *testing.T) {
	cases := []struct{ kind, title, want string }{
		{"read", "Read src/main.go", "Read"},
		{"edit", "Edit src/main.go", "Write"},
		{"execute", "Run tests", "Bash"},
		{"", "mcp__grafana__query_loki_logs", "mcp__grafana__query_loki_logs"},
		{"other", "Do a thing", "Do a thing"},
		{"", "", "unknown"},
	}
	for _, c := range cases {
		if got := policyTool(c.kind, c.title); got != c.want {
			t.Errorf("policyTool(%q, %q) = %q, want %q", c.kind, c.title, got, c.want)
		}
	}
}
