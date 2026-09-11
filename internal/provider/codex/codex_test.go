package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// TestMain doubles as two fakes: with SIRDAR_FAKE_CODEX naming a script the
// test binary replays it over stdio as the Codex app-server, and with
// SIRDAR_FAKE_MCP naming a server it answers the MCP handshake as a stdio
// MCP server, which is what the live smoke test points Codex at. The
// provider spawns `<binary> app-server`, so both fakes ignore argv.
func TestMain(m *testing.M) {
	if script := os.Getenv("SIRDAR_FAKE_CODEX"); script != "" {
		os.Exit(fakeServer(script))
	}
	if name := os.Getenv("SIRDAR_FAKE_MCP"); name != "" {
		os.Exit(fakeMCPServer(name))
	}
	os.Exit(m.Run())
}

// ---------------------------------------------------------------- fake server

// inbound is one client-to-server line the fake read from stdin.
type inbound struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

// fakeServer replays a JSONL script against stdio and returns a process exit
// code. Directives:
//
//	{"$expect":"<method>"}                        block until that client call
//	                                              arrives; remember its id
//	{"$reply":{...}}                              respond to the remembered id
//	{"$error":{...}}                              fail the remembered id
//	{"$expect_request":"<m>","id":N,"params":{}}  send a server request, then
//	                                              block until id N is answered
//
// Any other line is a server-to-client message, written to stdout verbatim.
// Every stdin line is echoed to stderr prefixed "STDIN:" so tests can assert
// on what the provider sent.
func fakeServer(scriptPath string) int {
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake codex:", err)
		return 2
	}

	// The child's environment is only observable through stderr, which the
	// session keeps as its tail, so the home it was started against is
	// reported the same way stdin is echoed.
	home := os.Getenv("CODEX_HOME")
	fmt.Fprintf(os.Stderr, "ENV: CODEX_HOME=%s\n", home)

	// Codex rewrites auth.json in its own home when it refreshes the
	// ChatGPT token. SIRDAR_FAKE_REFRESH_AUTH makes the fake do the same,
	// which is what the write-back on Wait has to notice.
	if body := os.Getenv("SIRDAR_FAKE_REFRESH_AUTH"); body != "" && home != "" {
		_ = os.WriteFile(filepath.Join(home, "auth.json"), []byte(body), 0o600)
	}

	in := make(chan inbound, 64)
	go func() {
		defer close(in)
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			fmt.Fprintf(os.Stderr, "STDIN: %s\n", line)
			var msg inbound
			if err := json.Unmarshal(line, &msg); err != nil {
				fmt.Fprintf(os.Stderr, "fake codex: bad stdin line: %v\n", err)
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

	var lastID json.RawMessage
	for _, raw := range bytes.Split(script, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 || bytes.HasPrefix(line, []byte("//")) {
			continue
		}
		var directive map[string]json.RawMessage
		if err := json.Unmarshal(line, &directive); err != nil {
			fmt.Fprintf(os.Stderr, "fake codex: bad script line: %v\n", err)
			return 2
		}

		switch {
		case directive["$expect"] != nil:
			var method string
			if err := json.Unmarshal(directive["$expect"], &method); err != nil {
				return 2
			}
			for {
				msg, ok := <-in
				if !ok {
					fmt.Fprintf(os.Stderr, "fake codex: stdin closed waiting for %s\n", method)
					return 3
				}
				if msg.Method == method {
					lastID = msg.ID
					break
				}
			}

		case directive["$reply"] != nil:
			if lastID == nil {
				fmt.Fprintln(os.Stderr, "fake codex: $reply with no expected id")
				return 2
			}
			write(`{"jsonrpc":"2.0","id":%s,"result":%s}`, lastID, directive["$reply"])

		case directive["$error"] != nil:
			if lastID == nil {
				fmt.Fprintln(os.Stderr, "fake codex: $error with no expected id")
				return 2
			}
			write(`{"jsonrpc":"2.0","id":%s,"error":%s}`, lastID, directive["$error"])

		case directive["$expect_request"] != nil:
			var method string
			if err := json.Unmarshal(directive["$expect_request"], &method); err != nil {
				return 2
			}
			id := directive["id"]
			params := directive["params"]
			if params == nil {
				params = json.RawMessage("{}")
			}
			write(`{"jsonrpc":"2.0","id":%s,"method":%q,"params":%s}`, id, method, params)
			for {
				msg, ok := <-in
				if !ok {
					fmt.Fprintf(os.Stderr, "fake codex: stdin closed waiting for reply to %s\n", id)
					return 3
				}
				if sameID(msg.ID, id) {
					break
				}
			}

		default:
			write("%s", line)
		}
	}

	// Drain until the provider closes stdin, so trailing traffic
	// (thread/unsubscribe) still reaches the stderr echo.
	for range in { //nolint:revive // draining
	}
	return 0
}

func sameID(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return ca.String() == cb.String()
}

// -------------------------------------------------------------- test helpers

func startSession(t *testing.T, script string, mutate func(*provider.SessionSpec)) provider.Session {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	path, err := filepath.Abs(filepath.Join("testdata", script))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	spec := provider.SessionSpec{
		Cwd:    t.TempDir(),
		Prompt: "Triage ticket ABC-1.",
		Binary: exe,
		Env:    append(os.Environ(), "SIRDAR_FAKE_CODEX="+path),
	}
	if mutate != nil {
		mutate(&spec)
	}
	sess, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return sess
}

func startFailing(t *testing.T, script string) error {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	path, err := filepath.Abs(filepath.Join("testdata", script))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	sess, err := New().Start(context.Background(), provider.SessionSpec{
		Cwd:    t.TempDir(),
		Prompt: "Triage ticket ABC-1.",
		Binary: exe,
		Env:    append(os.Environ(), "SIRDAR_FAKE_CODEX="+path),
	})
	if err == nil {
		sess.Cancel()
		t.Fatalf("Start succeeded, want an error")
	}
	return err
}

func drain(sess provider.Session) []provider.Event {
	var evs []provider.Event
	for ev := range sess.Events() {
		evs = append(evs, ev)
	}
	return evs
}

func only(t *testing.T, evs []provider.Event, kind provider.EventKind) []provider.Event {
	t.Helper()
	var out []provider.Event
	for _, ev := range evs {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// sent returns the client-to-server lines the fake echoed to stderr, taken
// from the session's result.
func sent(t *testing.T, res provider.Result) []string {
	t.Helper()
	var out []string
	for _, line := range res.StderrTail {
		if rest, ok := strings.CutPrefix(line, "STDIN: "); ok {
			out = append(out, rest)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no STDIN lines captured from the fake server; tail=%v", res.StderrTail)
	}
	return out
}

func findSent(t *testing.T, res provider.Result, method string) string {
	t.Helper()
	for _, line := range sent(t, res) {
		var msg inbound
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Method == method {
			return line
		}
	}
	t.Fatalf("no %s line was sent; sent=%v", method, sent(t, res))
	return ""
}

// -------------------------------------------------------------------- tests

func TestBasicSession(t *testing.T) {
	sess := startSession(t, "script-basic.jsonl", nil)

	// Drain first and wait afterwards, the way the runner does: Events()
	// must close on its own once the turn has completed.
	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if res.Handle != "th-1" {
		t.Errorf("Handle = %q, want th-1", res.Handle)
	}
	if sess.Handle() != "th-1" {
		t.Errorf("sess.Handle() = %q, want th-1", sess.Handle())
	}
	if got := string(res.Final); got != `{"greeting":"hi","n":7}` {
		t.Errorf("Result.Final = %q", got)
	}
	if res.Usage.InputTok != 17104 || res.Usage.OutputTok != 166 {
		t.Errorf("Result.Usage tokens = %d/%d, want 17104/166", res.Usage.InputTok, res.Usage.OutputTok)
	}
	if res.Usage.Turns != 1 {
		t.Errorf("Result.Usage.Turns = %d, want 1", res.Usage.Turns)
	}

	perms := only(t, evs, provider.EvPermission)
	if len(perms) != 1 {
		t.Fatalf("got %d permission events, want 1", len(perms))
	}
	if perms[0].Decision != "deny" {
		t.Errorf("permission decision = %q, want deny", perms[0].Decision)
	}

	tools := only(t, evs, provider.EvToolStarted)
	if len(tools) != 1 || tools[0].Tool != "commandExecution" {
		t.Errorf("tool_started events = %+v, want one commandExecution", tools)
	}
	if len(only(t, evs, provider.EvToolFinished)) != 1 {
		t.Errorf("tool_finished events = %d, want 1", len(only(t, evs, provider.EvToolFinished)))
	}

	texts := only(t, evs, provider.EvAssistantText)
	if len(texts) != 1 || texts[0].Text != "Reading the repository now." {
		t.Errorf("assistant_text events = %+v", texts)
	}

	usage := only(t, evs, provider.EvUsage)
	if len(usage) != 1 {
		t.Fatalf("got %d usage events, want 1", len(usage))
	}
	if usage[0].InputTok != 17104 || usage[0].OutputTok != 166 {
		t.Errorf("usage tokens = %d/%d, want 17104/166", usage[0].InputTok, usage[0].OutputTok)
	}
	if usage[0].CostUSD != 0 {
		t.Errorf("usage CostUSD = %v, want 0", usage[0].CostUSD)
	}

	if n := len(only(t, evs, provider.EvRateLimited)); n != 0 {
		t.Errorf("got %d rate_limited events at usedPercent 91, want 0", n)
	}

	finals := only(t, evs, provider.EvFinal)
	if len(finals) != 1 {
		t.Fatalf("got %d final events, want 1", len(finals))
	}
	if got := string(finals[0].Final); got != `{"greeting":"hi","n":7}` {
		t.Errorf("final event Final = %q", got)
	}
	for _, ev := range evs {
		if len(ev.Raw) == 0 {
			t.Errorf("event %s has no Raw", ev.Kind)
		}
		if ev.At.IsZero() {
			t.Errorf("event %s has no timestamp", ev.Kind)
		}
	}

	// The approval request must have been declined.
	var declined bool
	for _, line := range sent(t, res) {
		if strings.Contains(line, `"decision":"decline"`) {
			declined = true
		}
	}
	if !declined {
		t.Errorf("no decline reply was sent; sent=%v", sent(t, res))
	}
}

func TestThreadStartParams(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"n":{"type":"integer"}}}`)
	sess := startSession(t, "script-basic.jsonl", func(spec *provider.SessionSpec) {
		spec.OutputSchema = schema
		spec.Images = []string{"/work/bundle/attachments/1-shot.png"}
		spec.Model = "gpt-5-codex"
	})
	drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	initLine := findSent(t, res, "initialize")
	for _, want := range []string{`"name":"sirdar"`, `"title":"Sirdar"`, `"version":"` + clientVersion + `"`} {
		if !strings.Contains(initLine, want) {
			t.Errorf("initialize params missing %s: %s", want, initLine)
		}
	}

	startLine := findSent(t, res, "thread/start")
	for _, want := range []string{`"sandbox":"read-only"`, `"approvalPolicy":"untrusted"`, `"model":"gpt-5-codex"`} {
		if !strings.Contains(startLine, want) {
			t.Errorf("thread/start params missing %s: %s", want, startLine)
		}
	}

	turnLine := findSent(t, res, "turn/start")
	var turn struct {
		Params struct {
			ThreadID     string          `json:"threadId"`
			OutputSchema json.RawMessage `json:"outputSchema"`
			Input        []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Path string `json:"path"`
			} `json:"input"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(turnLine), &turn); err != nil {
		t.Fatalf("turn/start unmarshal: %v", err)
	}
	if turn.Params.ThreadID != "th-1" {
		t.Errorf("turn/start threadId = %q, want th-1", turn.Params.ThreadID)
	}
	if len(turn.Params.OutputSchema) == 0 {
		t.Errorf("turn/start has no outputSchema: %s", turnLine)
	}
	if len(turn.Params.Input) != 2 {
		t.Fatalf("turn/start input = %+v, want text + localImage", turn.Params.Input)
	}
	if turn.Params.Input[0].Type != "text" || turn.Params.Input[0].Text != "Triage ticket ABC-1." {
		t.Errorf("turn/start input[0] = %+v", turn.Params.Input[0])
	}
	if turn.Params.Input[1].Type != "localImage" || turn.Params.Input[1].Path != "/work/bundle/attachments/1-shot.png" {
		t.Errorf("turn/start input[1] = %+v", turn.Params.Input[1])
	}
}

func TestTurnFailed(t *testing.T) {
	sess := startSession(t, "script-failed.jsonl", nil)

	evs := drain(sess)
	res, err := sess.Wait()
	if err == nil {
		t.Fatalf("Wait returned no error for a failed turn (result %+v)", res)
	}
	if !strings.Contains(err.Error(), "model stream disconnected") {
		t.Errorf("Wait error = %v, want it to mention the turn error", err)
	}

	errs := only(t, evs, provider.EvError)
	if len(errs) != 1 {
		t.Fatalf("got %d error events, want 1", len(errs))
	}
	if errs[0].Text != "model stream disconnected" {
		t.Errorf("error event text = %q", errs[0].Text)
	}
	if n := len(only(t, evs, provider.EvFinal)); n != 0 {
		t.Errorf("got %d final events for a failed turn, want 0", n)
	}
}

// TestHandshakeErrorReleasesPump covers the goroutine leak a failed handshake
// used to cause: the server emits a notification before the failure, so an
// event is queued, and nobody ever reads Events() because Start returned an
// error and the session was discarded.
func TestHandshakeErrorReleasesPump(t *testing.T) {
	before := runtime.NumGoroutine()

	err := startFailing(t, "script-handshake-error.jsonl")
	if !strings.Contains(err.Error(), "thread start refused") {
		t.Errorf("Start error = %v, want the server's rpc error", err)
	}

	select {
	case <-lastPumpDone:
	case <-time.After(5 * time.Second):
		t.Fatal("pump goroutine is still running after a failed Start")
	}

	// Belt and braces: the whole session's goroutines should be gone too.
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("goroutines: %d before, %d after a failed Start", before, after)
	}
}

func TestResumeThread(t *testing.T) {
	sess := startSession(t, "script-resume.jsonl", func(spec *provider.SessionSpec) {
		spec.Resume = "th-77"
	})
	drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if res.Handle != "th-77" {
		t.Errorf("Handle = %q, want th-77", res.Handle)
	}
	// The final answer came only from turn/completed.turn.items.
	if got := string(res.Final); got != `{"ok":true}` {
		t.Errorf("Result.Final = %q, want the item from turn/completed", got)
	}

	for _, line := range sent(t, res) {
		var msg inbound
		if json.Unmarshal([]byte(line), &msg) == nil && msg.Method == "thread/start" {
			t.Errorf("thread/start was sent for a resumed session: %s", line)
		}
	}
	resumeLine := findSent(t, res, "thread/resume")
	for _, want := range []string{`"threadId":"th-77"`, `"sandbox":"read-only"`, `"approvalPolicy":"untrusted"`} {
		if !strings.Contains(resumeLine, want) {
			t.Errorf("thread/resume params missing %s: %s", want, resumeLine)
		}
	}
	if strings.Contains(resumeLine, `"cwd"`) {
		t.Errorf("thread/resume should not carry cwd: %s", resumeLine)
	}
}

func TestMCPToolCallAndUserInput(t *testing.T) {
	sess := startSession(t, "script-tools.jsonl", nil)
	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	started := only(t, evs, provider.EvToolStarted)
	if len(started) != 1 || started[0].Tool != "oxo-mysql/query" {
		t.Fatalf("tool_started = %+v, want one oxo-mysql/query", started)
	}
	if !strings.Contains(string(started[0].Input), `"sql":"select 1"`) {
		t.Errorf("tool_started Input = %s, want the whole item", started[0].Input)
	}
	finished := only(t, evs, provider.EvToolFinished)
	if len(finished) != 1 || finished[0].Tool != "oxo-mysql/query" {
		t.Errorf("tool_finished = %+v, want one oxo-mysql/query", finished)
	}

	questions := only(t, evs, provider.EvQuestion)
	if len(questions) != 1 {
		t.Fatalf("got %d question events, want 1", len(questions))
	}
	if !strings.Contains(string(questions[0].Input), "Which database should I query?") {
		t.Errorf("question Input = %s, want the request params", questions[0].Input)
	}

	var answered bool
	for _, line := range sent(t, res) {
		if strings.Contains(line, `"id":88`) && strings.Contains(line, `"answers":{}`) {
			answered = true
		}
	}
	if !answered {
		t.Errorf("requestUserInput was not answered with empty answers; sent=%v", sent(t, res))
	}

	// Text that is not JSON lands in Text, not Final.
	if res.Final != nil {
		t.Errorf("Result.Final = %s, want nil for non-JSON final text", res.Final)
	}
	if res.Text != "No structured output this time." {
		t.Errorf("Result.Text = %q", res.Text)
	}
}

// TestEventsCloseWithoutWait covers the hang the runner used to walk into:
// it drains Events() to completion and only then calls Wait, so Events()
// has to close on its own once the final turn has completed.
func TestEventsCloseWithoutWait(t *testing.T) {
	sess := startSession(t, "script-basic.jsonl", nil)

	drained := make(chan []provider.Event, 1)
	go func() { drained <- drain(sess) }()

	var evs []provider.Event
	select {
	case evs = <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("Events() did not close within 5s of turn/completed")
	}
	if n := len(only(t, evs, provider.EvFinal)); n != 1 {
		t.Fatalf("got %d final events, want 1", n)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := sess.Wait(); err != nil {
			t.Errorf("Wait: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return within 5s of a drained event stream")
	}
}

// TestSendAfterStreamEnds pins the other half of that contract: once the
// stream has ended, a follow-up turn nobody could observe is refused, so the
// runner falls back to resuming the thread in a fresh session.
func TestSendAfterStreamEnds(t *testing.T) {
	sess := startSession(t, "script-basic.jsonl", nil)
	drain(sess)

	if err := sess.Send(context.Background(), "try again"); err == nil {
		t.Fatal("Send after the event stream ended must return an error")
	}
	if _, err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestParseID(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
		ok   bool
	}{
		{"7", 7, true},
		{` "7" `, 7, true},
		{`"abc"`, 0, false},
		{"null", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseID(json.RawMessage(tc.raw))
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseID(%s) = %d,%v; want %d,%v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

// replyTo returns the result Sirdar sent in answer to the server request
// with the given id, as the fake echoed it back on stderr.
func replyTo(t *testing.T, res provider.Result, id int) string {
	t.Helper()
	want := fmt.Sprintf("%d", id)
	for _, line := range sent(t, res) {
		var msg inbound
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Method == "" && strings.TrimSpace(string(msg.ID)) == want {
			return string(msg.Result)
		}
	}
	t.Fatalf("no reply to request %d; sent=%v", id, sent(t, res))
	return ""
}

// permissionFor returns the EvPermission event for a tool, and whether
// there was one.
func permissionFor(evs []provider.Event, tool string) (provider.Event, bool) {
	for _, ev := range evs {
		if ev.Kind == provider.EvPermission && ev.Tool == tool {
			return ev, true
		}
	}
	return provider.Event{}, false
}

// TestApprovalsGoThroughThePolicy is the read-only guarantee on the Codex
// path. Under approvalPolicy "untrusted" Codex asks before it acts, and
// every answer here comes from the workspace's own permissions — the same
// PermissionPolicy a Claude session is judged by.
//
// The MCP half is the one that matters: a workspace's .mcp.json can name
// any server it likes, and until this landed a tool from one either could
// not run at all (approvalPolicy "never" refuses MCP calls outright) or,
// had the policy been loosened without this, would have run ungated.
func TestApprovalsGoThroughThePolicy(t *testing.T) {
	sess := startSession(t, "script-approvals.jsonl", func(spec *provider.SessionSpec) {
		spec.Policy = &provider.PermissionPolicy{
			BashAllow: []string{"rg *"},
			MCPAllow:  []string{"mcp__notes__notes_lookup", "mcp__grafana__query_*"},
			Root:      spec.Cwd,
		}
	})
	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	for _, tc := range []struct {
		name string
		id   int
		want string
	}{
		{"an allow-listed command runs", 101, `{"decision":"accept"}`},
		{"a command outside the allow-list does not", 102, `{"decision":"decline"}`},
		{"an allow-listed MCP tool runs", 103, `{"action":"accept","content":{}}`},
		{"an MCP tool outside permissions.mcp does not", 104, `{"action":"decline"}`},
		{"an approval with no preceding item is still decided", 105, `{"action":"accept","content":{}}`},
		{"a real elicitation is declined: nobody is watching", 106, `{"action":"decline"}`},
		{"a file change is refused whatever the policy says", 107, `{"decision":"decline"}`},
		// Not {"decision":...}: this request is answered with the profile
		// it is being granted, and an empty one grants nothing.
		{"widening the sandbox grants nothing", 108, `{"permissions":{},"scope":"turn"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := replyTo(t, res, tc.id); got != tc.want {
				t.Errorf("reply to %d = %s, want %s", tc.id, got, tc.want)
			}
		})
	}

	// Every decision is on the record, under the tool's namespaced name,
	// so an operator reading the events log can see what was asked for.
	for tool, want := range map[string]string{
		"mcp__notes__notes_lookup":       "allow",
		"mcp__notes__delete_everything":  "deny",
		"mcp__grafana__query_prometheus": "allow",
		"fileChange":                     "deny",
		"permissions":                    "deny",
	} {
		ev, ok := permissionFor(evs, tool)
		if !ok {
			t.Errorf("no permission event for %s", tool)
			continue
		}
		if ev.Decision != want {
			t.Errorf("permission event for %s = %q, want %q", tool, ev.Decision, want)
		}
	}

	// The denial the agent is shown names the rule it fell foul of, not
	// just "no": a refusal it cannot act on costs the run a turn.
	if ev, ok := permissionFor(evs, "mcp__notes__delete_everything"); ok {
		if !strings.Contains(ev.Text, "permissions.mcp") {
			t.Errorf("the MCP denial does not name the setting: %q", ev.Text)
		}
	}
	var commandDenied bool
	for _, ev := range evs {
		if ev.Kind == provider.EvPermission && ev.Tool == "commandExecution" && ev.Decision == "deny" {
			commandDenied = true
			if !strings.Contains(ev.Text, "allow-list") {
				t.Errorf("the command denial does not name the allow-list: %q", ev.Text)
			}
		}
	}
	if !commandDenied {
		t.Error("the command outside the allow-list was not reported as denied")
	}
}

// TestUnknownApprovalMethodIsRefused pins the round-2 hardening on the
// default arm of onRequest: an approval-shaped server request this package
// does not recognise must be refused and put on the record as an error, not
// answered with {} — a shape that could read as an accept to a future
// app-server version nobody has tested against.
func TestUnknownApprovalMethodIsRefused(t *testing.T) {
	sess := startSession(t, "script-unknown-approval.jsonl", nil)
	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if got := replyTo(t, res, 201); got != `{"action":"decline"}` {
		t.Errorf("reply to the unrecognised approval = %s, want a decline", got)
	}

	var found bool
	for _, ev := range only(t, evs, provider.EvError) {
		if strings.Contains(ev.Text, "item/foo/requestApproval") {
			found = true
		}
	}
	if !found {
		t.Errorf("no EvError named the unrecognised approval method; events=%+v", evs)
	}
}

// TestMCPApprovalOrderingAndFailSafes is the round-2 hardening on MCP
// tool-call resolution: same-server concurrency is judged from a per-server
// FIFO rather than one name a second call can overwrite before the first is
// approved, a message that quotes a different tool than the FIFO expects is
// refused outright rather than trusted either way, and an approval with
// neither a preceding item nor a quoted name is refused rather than decided
// under an empty tool name — which the old code would have waved through,
// since an empty name carries no write verb for the heuristic to catch.
func TestMCPApprovalOrderingAndFailSafes(t *testing.T) {
	sess := startSession(t, "script-mcp-fifo.jsonl", nil)
	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Two calls to "notes" start before either is approved, and neither
	// approval message quotes a name, so the reply can only be right if
	// the FIFO kept them in order: the oldest queued call — a read,
	// allowed by the unconfigured default — is decided first, the second
	// — a write — after it.
	if got := replyTo(t, res, 301); got != `{"action":"accept","content":{}}` {
		t.Errorf("reply to 301 (oldest queued call) = %s, want accept", got)
	}
	if got := replyTo(t, res, 302); got != `{"action":"decline"}` {
		t.Errorf("reply to 302 (second queued call) = %s, want decline", got)
	}
	if ev, ok := permissionFor(evs, "mcp__notes__lookup_alpha"); !ok || ev.Decision != "allow" {
		t.Errorf("permission for lookup_alpha = %+v, ok=%v, want allow", ev, ok)
	}
	if ev, ok := permissionFor(evs, "mcp__notes__delete_beta"); !ok || ev.Decision != "deny" {
		t.Errorf("permission for delete_beta = %+v, ok=%v, want deny", ev, ok)
	}

	// A call starts under one name; its elicitation's own message quotes
	// a different one. Both are refused rather than have one guessed.
	if got := replyTo(t, res, 303); got != `{"action":"decline"}` {
		t.Errorf("reply to 303 (name mismatch) = %s, want decline", got)
	}
	if ev, ok := permissionFor(evs, "mcp__notes__delta_lookup"); !ok || ev.Decision != "deny" || !strings.Contains(ev.Text, "mismatch") {
		t.Errorf("mismatch permission event = %+v, ok=%v, want a deny naming the mismatch", ev, ok)
	}

	// No preceding item, no quoted name: there is nothing to decide
	// under, and an empty tool name must not fail open.
	if got := replyTo(t, res, 304); got != `{"action":"decline"}` {
		t.Errorf("reply to 304 (unresolved name) = %s, want decline", got)
	}
	var unknownDenied bool
	for _, ev := range evs {
		if ev.Kind == provider.EvPermission && ev.Decision == "deny" && strings.Contains(ev.Text, "tool name unknown") {
			unknownDenied = true
		}
	}
	if !unknownDenied {
		t.Error("no permission event denied the unresolved tool name")
	}
}

// TestUnconfiguredPolicyStillDecides pins the default reading: a session
// started with no policy at all refuses shell commands and write-shaped
// MCP tools rather than waving them through.
func TestUnconfiguredPolicyStillDecides(t *testing.T) {
	sess := startSession(t, "script-approvals.jsonl", nil)
	drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	for _, tc := range []struct {
		id   int
		want string
	}{
		{101, `{"decision":"decline"}`},
		{102, `{"decision":"decline"}`},
		// No permissions.mcp, so the write-verb heuristic decides: a
		// lookup is a read, delete_everything is not.
		{103, `{"action":"accept","content":{}}`},
		{104, `{"action":"decline"}`},
	} {
		if got := replyTo(t, res, tc.id); got != tc.want {
			t.Errorf("reply to %d = %s, want %s", tc.id, got, tc.want)
		}
	}
}
