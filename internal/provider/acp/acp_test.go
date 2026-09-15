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

	// A symlink inside the workspace pointing out of it, which is the
	// escape a lexical path check would wave through. t.TempDir gives each
	// call its own directory under the test's, so this one is a sibling of
	// the workspace rather than a child.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("private key"), 0o600); err != nil {
		t.Fatalf("write secret.txt: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	return dir
}

func spawn(t *testing.T, script, cwd string, mutate func(*provider.SessionSpec)) provider.Session {
	t.Helper()
	return spawnWith(t, Config{}, script, cwd, mutate)
}

// spawnWith is spawn for a test that needs the acp block itself — acp.mode
// is the only setting the adapter reads out of it at runtime.
func spawnWith(t *testing.T, cfg Config, script, cwd string, mutate func(*provider.SessionSpec)) provider.Session {
	t.Helper()
	spec := specFor(t, script, cwd)
	if mutate != nil {
		mutate(&spec)
	}
	sess, err := New(cfg).Start(context.Background(), spec)
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

// TestSchemaEchoFinalIsRetriedWithASecondPrompt covers the failure this
// package saw live under provider: acp (GitHub Copilot over ACP, which has
// no --json-schema wire enforcement): the agent's first answer is a valid
// JSON object, but it carries the schema's own "$schema" and "title" keys
// alongside the real fields, exactly the shape the sandbox evidence
// recorded. ACP itself does not validate against the schema — it only
// extracts the object — so jsonObject hands that shape through as the
// turn's Final untouched, and it is internal/run's job to notice, strip
// the echoed keys and, when that alone is not enough, retry with the
// sharpened instruction. This test plays the runner's side of that retry
// by hand, the way TestProseFinalIsRetriedWithASecondPrompt plays it for a
// prose answer, and checks the session carries a second prompt through and
// returns a clean final.
func TestSchemaEchoFinalIsRetriedWithASecondPrompt(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-schema-echo-retry.jsonl", cwd, nil)

	const sharpened = "Your previous answer did not match the schema: (root): additional properties " +
		"'$schema', 'title' not allowed. Reply again with the corrected JSON object only. " +
		"Reply with the JSON object only: no `$schema`, no `title`, no surrounding text or code fence."

	var (
		finals  []json.RawMessage
		sendErr error
	)
	for ev := range sess.Events() {
		if ev.Kind != provider.EvFinal || len(ev.Final) == 0 {
			continue
		}
		finals = append(finals, ev.Final)
		if len(finals) == 1 {
			// This is what internal/run's handleFinal does once
			// stripSchemaEcho and a revalidation still leave the note
			// invalid: send the sharpened retry on the running session.
			sendErr = sess.Send(context.Background(), sharpened)
		}
	}
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if sendErr != nil {
		t.Fatalf("Send: %v", sendErr)
	}
	if len(finals) != 2 {
		t.Fatalf("finals = %d, want 2", len(finals))
	}
	if !strings.Contains(string(finals[0]), `"$schema"`) || !strings.Contains(string(finals[0]), `"title"`) {
		t.Errorf("first final = %s, want it to carry the echoed schema keys", finals[0])
	}
	if strings.Contains(string(finals[1]), `"$schema"`) || strings.Contains(string(finals[1]), `"title"`) {
		t.Errorf("second final = %s, want no echoed schema keys", finals[1])
	}
	if string(res.Final) != string(finals[1]) {
		t.Errorf("Result.Final = %s, want the second final", res.Final)
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

	// The initial prompt already carries the sharpened, ACP-only answer
	// instruction: this provider has no wire enforcement to fall back on,
	// so the first ask has to be as explicit as the retry.
	initial := findSent(t, res, "session/prompt")
	if !strings.Contains(initial, "no `$schema`, no `title`") {
		t.Errorf("initial session/prompt = %s, want the sharpened answer instruction", initial)
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
	// That Start returned at all is the leak check: abort joins the event
	// pump before it gives up, so a pump parked on a channel nobody reads
	// would hang here rather than leak quietly.
}

// TestEventPumpExitsWithTheSession is the same goroutine-teardown check on
// the path that succeeds.
func TestEventPumpExitsWithTheSession(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-basic.jsonl", cwd, nil)
	drain(sess)
	if _, err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case <-sess.(*session).pumpExited():
	case <-time.After(5 * time.Second):
		t.Error("the event pump is still running after Wait")
	}
}

// TestGuardsHoldAgainstAMisbehavingAgent covers the three things a run has
// to survive from an agent it does not control: a nested subagent session
// talking over the run's own, a symlink out of the workspace, and a write
// that completed without ever asking.
func TestGuardsHoldAgainstAMisbehavingAgent(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-guards.jsonl", cwd, nil)

	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// The subagent session's message is not this session's answer.
	if string(res.Final) != `{"title":"Guarded","ok":true}` {
		t.Errorf("Result.Final = %s", res.Final)
	}
	for _, ev := range evs {
		if strings.Contains(ev.Text, "leaked") {
			t.Errorf("a subagent session's message reached the transcript: %+v", ev)
		}
	}
	if len(only(evs, provider.EvToolStarted)) != 1 {
		t.Errorf("tool started events = %+v", only(evs, provider.EvToolStarted))
	}

	// Both requests naming the subagent session are refused.
	for _, line := range []string{
		findAnswer(t, res, "session/request_permission"),
		findAnswer(t, res, "fs/read_text_file"),
	} {
		if !strings.Contains(line, "is not it") {
			t.Errorf("subagent request answer = %s, want a refusal", line)
		}
	}

	// The read through the symlink is refused as out of workspace.
	escape := ""
	for _, line := range res.StderrTail {
		if strings.Contains(line, "fs/read_text_file: ") {
			escape = line
		}
	}
	if !strings.Contains(escape, "outside it") {
		t.Errorf("symlinked read answer = %s, want a refusal", escape)
	}
	if content, err := os.ReadFile(filepath.Join(cwd, "escape", "secret.txt")); err != nil || string(content) != "private key" {
		t.Fatalf("the symlink under test does not lead to the file it is meant to: %v", err)
	}

	// The unannounced write cannot be prevented, so it ends the run: a
	// triage session that wrote a file having asked nobody is the
	// read-only guarantee failing, not a warning to file a note under.
	breached := false
	for _, ev := range only(evs, provider.EvBreach) {
		if strings.HasPrefix(ev.Text, "read-only breach: edit") && ev.Tool == "edit" {
			breached = true
		}
	}
	if !breached {
		t.Errorf("a completed edit tool call raised no breach; breaches = %+v", only(evs, provider.EvBreach))
	}
}

// TestPreOpenSessionTrafficIsRefused covers the window between sending
// session/new and its response carrying the real session id. Traffic that
// names a session before this side has one of its own cannot be verified,
// so it must be refused (the two RPC requests) or dropped (the
// notification) rather than treated as this session's own — which is what
// an empty s.sessionID being waved through as "mine" used to do.
func TestPreOpenSessionTrafficIsRefused(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-preopen.jsonl", cwd, nil)

	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// The real turn's answer, not the injected chunk.
	if string(res.Final) != `{"title":"PreOpen","ok":true}` {
		t.Errorf("Result.Final = %s", res.Final)
	}
	for _, ev := range evs {
		if strings.Contains(ev.Text, "leaked") {
			t.Errorf("pre-open traffic reached the transcript: %+v", ev)
		}
	}

	// Neither pre-open request was honoured: no permission decision was
	// made, and no read happened.
	if perms := only(evs, provider.EvPermission); len(perms) != 0 {
		t.Errorf("EvPermission for pre-open traffic = %+v, want none", perms)
	}
	for _, ev := range only(evs, provider.EvToolFinished) {
		if ev.Tool == "fs/read_text_file" {
			t.Errorf("fs/read_text_file for pre-open traffic was served: %+v", ev)
		}
	}
	if content, err := os.ReadFile(filepath.Join(cwd, "inside.txt")); err != nil || string(content) != "line one\nline two\n" {
		t.Fatalf("the file under test does not have the contents the test assumes: %v", err)
	}

	for _, line := range []string{
		findAnswer(t, res, "session/request_permission"),
		findAnswer(t, res, "fs/read_text_file"),
	} {
		if !strings.Contains(line, "is not it") {
			t.Errorf("pre-open request answer = %s, want a refusal", line)
		}
	}

	// All three pieces of pre-open traffic are counted, but only the first
	// is surfaced — so exactly one EvError names it, not three.
	var warnings []provider.Event
	for _, ev := range only(evs, provider.EvError) {
		if strings.Contains(ev.Text, "before session/new returned") {
			warnings = append(warnings, ev)
		}
	}
	if len(warnings) != 1 {
		t.Errorf("pre-open EvError warnings = %+v, want exactly 1", warnings)
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
		// A prose prefix is tolerated, and the last top-level object wins,
		// so an agent that shows an example before answering is read as
		// having answered.
		{"Here you go:\n{\"a\":1}", `{"a":1}`},
		{"e.g. {\"a\":1}\nand the note:\n{\"b\":2}", `{"b":2}`},
		{`{"text":"a } brace in a string"}`, `{"text":"a } brace in a string"}`},
		{"just prose", ""},
		{"[1,2,3]", ""},
		{"{\"a\":", ""},
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
		{"read", "mcp__grafana__query_loki_logs", "mcp__grafana__query_loki_logs"},
		// A kind that describes a change is judged on the kind, whatever
		// the agent chose to call it: an mcp__-shaped title must not carry
		// a write into permissions.mcp, where a read-shaped name passes.
		{"edit", "mcp__editor__apply_diff", "Write"},
		{"delete", "mcp__fs__remove_path", "Write"},
		{"move", "mcp__fs__rename", "Write"},
		{"execute", "mcp__shell__run_command", "Bash"},
		{"other", "Do a thing", "Do a thing"},
		{"", "", "unknown"},
	}
	for _, c := range cases {
		if got := policyTool(c.kind, c.title); got != c.want {
			t.Errorf("policyTool(%q, %q) = %q, want %q", c.kind, c.title, got, c.want)
		}
	}
}

// fixPolicy is the mutate spawn takes to make a session a fix run: writes
// confined to the workspace, shell commands judged against a
// permissions.fixBash list.
func fixPolicy(cwd string) func(*provider.SessionSpec) {
	return func(spec *provider.SessionSpec) {
		spec.Policy = provider.FixPolicy(cwd, []string{"go build*", "go test*"}, nil, nil)
	}
}

// permissionAnswers pairs each request id the fake agent raised with the
// optionId Sirdar selected, or "cancelled" when it picked nothing.
func permissionAnswers(t *testing.T, res provider.Result) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, line := range stderrLines(t, res) {
		rest, ok := strings.CutPrefix(line, "ANSWER ")
		if !ok {
			continue
		}
		id, answer, _ := strings.Cut(rest, " session/request_permission: ")
		if answer == "" {
			continue
		}
		var reply struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		if err := json.Unmarshal([]byte(answer), &reply); err != nil {
			t.Fatalf("answer to %s is not an outcome: %s", id, answer)
		}
		if reply.Outcome.Outcome == "selected" {
			out[id] = reply.Outcome.OptionID
			continue
		}
		out[id] = reply.Outcome.Outcome
	}
	return out
}

// TestFixModeDecidesWritesOnTheirPaths is the whole of what fix mode adds to
// the ACP path: an edit is judged on the file it names rather than refused
// for being an edit, and the same FixPolicy that confines a Claude fix
// confines this one.
func TestFixModeDecidesWritesOnTheirPaths(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-fix-permissions.jsonl", cwd, fixPolicy(cwd))

	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	answers := permissionAnswers(t, res)
	want := map[string]string{
		"30": "yes", // an edit inside the worktree
		"31": "no",  // .git/hooks/x
		"32": "no",  // apply_patch: a patch string, no path anywhere
		"33": "no",  // a move whose destination leaves the workspace
		"34": "yes", // `sh -lc "go build ./..."`, unwrapped to the fixBash list
		"35": "no",  // curl, which no fixBash pattern covers
	}
	for id, wantOption := range want {
		if answers[id] != wantOption {
			t.Errorf("request %s answered %q, want %q (all: %v)", id, answers[id], wantOption, answers)
		}
	}

	perms := only(evs, provider.EvPermission)
	if len(perms) != 6 {
		t.Fatalf("permission events: %d (%+v)", len(perms), perms)
	}
	if perms[0].Decision != "allow" || perms[0].Tool != "Write" {
		t.Errorf("the in-worktree edit: %+v", perms[0])
	}
	if !strings.Contains(perms[1].Text, ".git/") {
		t.Errorf("the .git/hooks edit should say what it is inside: %q", perms[1].Text)
	}
	if !strings.Contains(perms[2].Text, "named no path") {
		t.Errorf("the pathless patch should say so: %q", perms[2].Text)
	}
	if !strings.Contains(perms[3].Text, "outside the workspace") {
		t.Errorf("the escaping move: %q", perms[3].Text)
	}
	if perms[4].Decision != "allow" || perms[4].Tool != "Bash" {
		t.Errorf("the wrapped go build: %+v", perms[4])
	}
	if perms[5].Decision != "deny" {
		t.Errorf("curl: %+v", perms[5])
	}
}

// TestTriageModeStillRefusesEdits is the other half of the same switch: the
// read-only posture is unchanged for a triage run, whatever paths the edit
// names.
func TestTriageModeStillRefusesEdits(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-fix-permissions.jsonl", cwd, nil)

	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	answers := permissionAnswers(t, res)
	if answers["30"] != "no" {
		t.Fatalf("an edit in a triage run was answered %q (all: %v)", answers["30"], answers)
	}
	perms := only(evs, provider.EvPermission)
	if perms[0].Decision != "deny" || !strings.Contains(perms[0].Text, "read-only") {
		t.Fatalf("the triage refusal: %+v", perms[0])
	}
}

// TestEmptyFinalSaysTheTurnEndedWithoutAnAnswer covers the failure the
// Copilot run hit: a turn spent entirely on tool calls and thinking, which
// used to reach the operator as a JSON parse error.
func TestEmptyFinalSaysTheTurnEndedWithoutAnAnswer(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-empty-final.jsonl", cwd, nil)

	evs := drain(sess)
	if _, err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	errs := only(evs, provider.EvError)
	if len(errs) != 1 {
		t.Fatalf("error events: %+v", errs)
	}
	if errs[0].Text != "acp: the agent ended the turn without an answer" {
		t.Fatalf("error text %q", errs[0].Text)
	}
	finals := only(evs, provider.EvFinal)
	if len(finals) != 1 || strings.TrimSpace(finals[0].Text) != "" || len(finals[0].Final) != 0 {
		t.Fatalf("final events: %+v", finals)
	}
}

// --- session modes ---

// modeSent returns the modeId the provider asked for, or "" when it sent
// no session/set_mode at all.
func modeSent(t *testing.T, res provider.Result) string {
	t.Helper()
	for _, line := range stderrLines(t, res) {
		rest, ok := strings.CutPrefix(line, "STDIN: ")
		if !ok {
			continue
		}
		var msg inbound
		if err := json.Unmarshal([]byte(rest), &msg); err != nil || msg.Method != "session/set_mode" {
			continue
		}
		var params struct {
			ModeID string `json:"modeId"`
		}
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			t.Fatalf("session/set_mode params: %v", err)
		}
		return params.ModeID
	}
	return ""
}

// sentMethods lists the client-to-agent methods in the order they went out,
// which is what "before the prompt" has to be asserted against.
func sentMethods(t *testing.T, res provider.Result) []string {
	t.Helper()
	var out []string
	for _, line := range stderrLines(t, res) {
		rest, ok := strings.CutPrefix(line, "STDIN: ")
		if !ok {
			continue
		}
		var msg inbound
		if err := json.Unmarshal([]byte(rest), &msg); err != nil || msg.Method == "" {
			continue
		}
		out = append(out, msg.Method)
	}
	return out
}

func systemText(evs []provider.Event, substr string) bool {
	for _, ev := range only(evs, provider.EvSystem) {
		if strings.Contains(ev.Text, substr) {
			return true
		}
	}
	return false
}

// TestReadOnlyRunSelectsThePlanMode: an agent that advertises modes is put
// into its read-only one, and it is put there before the prompt that would
// otherwise run in whatever mode the session opened in. kimi's capture is
// the reason this matters — its `default` mode approves an in-workspace
// write before a permission request is even built, so a triage run that
// never sets a mode is asked about nothing it cares about.
func TestReadOnlyRunSelectsThePlanMode(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-modes.jsonl", cwd, nil)

	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if got := modeSent(t, res); got != "plan" {
		t.Errorf("session/set_mode modeId = %q, want plan", got)
	}
	methods := sentMethods(t, res)
	set, prompt := indexOf(methods, "session/set_mode"), indexOf(methods, "session/prompt")
	if set < 0 || prompt < 0 || set > prompt {
		t.Errorf("methods = %v, want session/set_mode before session/prompt", methods)
	}
	if !systemText(evs, "acp mode plan selected for this read-only session") {
		t.Errorf("the chosen mode was not recorded; system events = %+v", only(evs, provider.EvSystem))
	}
}

// TestFixRunSelectsAnEditMode is the other half: a fix session may write,
// so it must not be put in the mode that refuses to.
func TestFixRunSelectsAnEditMode(t *testing.T) {
	cwd := workspace(t)
	sess := spawnWith(t, Config{}, "script-modes.jsonl", cwd, fixPolicy(cwd))

	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := modeSent(t, res); got != "edit" {
		t.Errorf("session/set_mode modeId = %q, want edit", got)
	}
	if !systemText(evs, "acp mode edit selected for this fix session") {
		t.Errorf("the chosen mode was not recorded; system events = %+v", only(evs, provider.EvSystem))
	}
}

// TestConfiguredModeOverridesTheChoice: acp.mode is the escape hatch for an
// agent whose read-only mode is spelled something this adapter does not
// know, so it wins over the adapter's own pick.
func TestConfiguredModeOverridesTheChoice(t *testing.T) {
	cwd := workspace(t)
	sess := spawnWith(t, Config{Mode: "ask"}, "script-modes.jsonl", cwd, nil)

	drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := modeSent(t, res); got != "ask" {
		t.Errorf("session/set_mode modeId = %q, want the configured ask", got)
	}
}

// TestAgentWithNoModesIsSaidOnce: most ACP agents expose no modes at all,
// and the run should say so rather than pretending a mode was set.
func TestAgentWithNoModesIsSaidOnce(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-basic.jsonl", cwd, nil)

	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := modeSent(t, res); got != "" {
		t.Errorf("session/set_mode was sent (%q) to an agent that advertised no modes", got)
	}
	said := 0
	for _, ev := range only(evs, provider.EvSystem) {
		if strings.Contains(ev.Text, "offers no session modes") {
			said++
		}
	}
	if said != 1 {
		t.Errorf("the no-modes notice was emitted %d times, want 1", said)
	}
}

func indexOf(values []string, want string) int {
	for i, v := range values {
		if v == want {
			return i
		}
	}
	return -1
}

// --- tool calls nobody approved ---

// unmediated pairs each tool call in script-unmediated.jsonl with the kind
// of event it produced: "breach", "error", or "" for nothing at all.
func unmediated(evs []provider.Event) map[string]string {
	out := map[string]string{}
	for _, ev := range evs {
		var kind string
		switch ev.Kind {
		case provider.EvBreach:
			kind = "breach"
		case provider.EvError:
			kind = "error"
		default:
			continue
		}
		switch {
		case strings.Contains(ev.Text, "ledger.go") && strings.Contains(ev.Text, "escape"):
			out["out"] = kind
		case strings.Contains(ev.Text, "secret.txt"):
			out["out"] = kind
		case strings.Contains(ev.Text, "ledger.go"):
			out["in"] = kind
		case strings.Contains(ev.Text, "sirdar-acp-probe"):
			out["exec"] = kind
		case strings.Contains(ev.Text, "sub-agent"):
			out["spawn"] = kind
		case strings.Contains(ev.Text, `"fetch"`):
			out["fetch"] = kind
		}
	}
	return out
}

// TestUnmediatedCallsBreachAReadOnlyRun. A triage or rca session that
// completes a write, a command or a sub-agent spawn having asked nobody has
// had its read-only guarantee fail, and the run layer answers an EvBreach
// by cancelling the session and filing nothing. A fetch stays a warning: it
// is neither, and what it cost is that permissions.fetch never saw the
// destination.
func TestUnmediatedCallsBreachAReadOnlyRun(t *testing.T) {
	cwd := workspace(t)
	sess := spawn(t, "script-unmediated.jsonl", cwd, nil)

	evs := drain(sess)
	if _, err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	got := unmediated(evs)
	want := map[string]string{
		"in":    "breach",
		"out":   "breach",
		"exec":  "breach",
		"spawn": "breach",
		"fetch": "error",
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("%s produced %q, want %q (all: %v)", name, got[name], kind, got)
		}
	}

	// The call that did go through session/request_permission, and the
	// think call whose title merely contains the word "task", produce
	// nothing: there is one breach per unapproved call and no more.
	if n := len(only(evs, provider.EvBreach)); n != 4 {
		t.Errorf("breaches = %d, want 4: %+v", n, only(evs, provider.EvBreach))
	}
	for _, ev := range only(evs, provider.EvBreach) {
		if !strings.HasPrefix(ev.Text, "read-only breach: ") {
			t.Errorf("a breach's first line must be the run's reason: %q", ev.Text)
		}
	}
}

// TestUnmediatedWritesInAFixRunWarnOnlyInsideTheRoot. A fix session may
// write, so an unannounced write it made inside its own worktree is a
// warning. One that landed outside is not: staying inside the worktree is
// the whole guarantee `sirdar fix` makes, and a sub-agent spawn is a breach
// in a fix run too, because a sub-agent's own permission state is nothing
// Sirdar configured.
func TestUnmediatedWritesInAFixRunWarnOnlyInsideTheRoot(t *testing.T) {
	cwd := workspace(t)
	sess := spawnWith(t, Config{}, "script-unmediated.jsonl", cwd, fixPolicy(cwd))

	evs := drain(sess)
	if _, err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	got := unmediated(evs)
	want := map[string]string{
		"in":    "error",
		"out":   "breach",
		"exec":  "error",
		"spawn": "breach",
		"fetch": "error",
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("%s produced %q, want %q (all: %v)", name, got[name], kind, got)
		}
	}
}

// TestSubagentTitlesAreMatchedOnTheLeadingIdentifier guards the one thing
// that would make this check worse than useless: failing a run over a
// title that happens to contain the word "task".
func TestSubagentTitlesAreMatchedOnTheLeadingIdentifier(t *testing.T) {
	for _, tc := range []struct {
		kind, title string
		want        bool
	}{
		{"other", "Task", true},
		{"other", "Task(subagent_type=explore)", true},
		{"other", "Agent(subagent_type=explore)", true},
		{"other", "AgentSwarm", true},
		{"execute", "spawn_worker", true},
		{"other", "mcp__orchestrator__task", true},
		{"think", "Update the task list", false},
		{"read", "Read the agent registry", false},
		{"edit", "Write src/agent.go", false},
		{"", "", false},
	} {
		if got := indicatesSubagent(tc.kind, tc.title); got != tc.want {
			t.Errorf("indicatesSubagent(%q, %q) = %v, want %v", tc.kind, tc.title, got, tc.want)
		}
	}
}
