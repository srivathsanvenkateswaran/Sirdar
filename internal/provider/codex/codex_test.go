package codex

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

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// TestMain doubles as the fake Codex app-server: when SIRDAR_FAKE_CODEX names
// a script, the test binary replays that script over stdio instead of running
// tests. The provider spawns `<binary> app-server`, so the fake ignores argv.
func TestMain(m *testing.M) {
	if script := os.Getenv("SIRDAR_FAKE_CODEX"); script != "" {
		os.Exit(fakeServer(script))
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

// sent returns the client-to-server lines the fake echoed to stderr.
func sent(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range lastStderrTail {
		if rest, ok := strings.CutPrefix(line, "STDIN: "); ok {
			out = append(out, rest)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no STDIN lines captured from the fake server; tail=%v", lastStderrTail)
	}
	return out
}

func findSent(t *testing.T, method string) string {
	t.Helper()
	for _, line := range sent(t) {
		var msg inbound
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Method == method {
			return line
		}
	}
	t.Fatalf("no %s line was sent; sent=%v", method, sent(t))
	return ""
}

// -------------------------------------------------------------------- tests

func TestBasicSession(t *testing.T) {
	sess := startSession(t, "script-basic.jsonl", nil)

	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	evs := drain(sess)

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
	for _, line := range sent(t) {
		if strings.Contains(line, `"decision":"decline"`) {
			declined = true
		}
	}
	if !declined {
		t.Errorf("no decline reply was sent; sent=%v", sent(t))
	}
}

func TestThreadStartParams(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"n":{"type":"integer"}}}`)
	sess := startSession(t, "script-basic.jsonl", func(spec *provider.SessionSpec) {
		spec.OutputSchema = schema
		spec.Images = []string{"/work/bundle/attachments/1-shot.png"}
		spec.Model = "gpt-5-codex"
	})
	if _, err := sess.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(sess)

	initLine := findSent(t, "initialize")
	for _, want := range []string{`"name":"sirdar"`, `"title":"Sirdar"`, `"version":"` + clientVersion + `"`} {
		if !strings.Contains(initLine, want) {
			t.Errorf("initialize params missing %s: %s", want, initLine)
		}
	}

	startLine := findSent(t, "thread/start")
	for _, want := range []string{`"sandbox":"read-only"`, `"approvalPolicy":"never"`, `"model":"gpt-5-codex"`} {
		if !strings.Contains(startLine, want) {
			t.Errorf("thread/start params missing %s: %s", want, startLine)
		}
	}

	turnLine := findSent(t, "turn/start")
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

	res, err := sess.Wait()
	if err == nil {
		t.Fatalf("Wait returned no error for a failed turn (result %+v)", res)
	}
	if !strings.Contains(err.Error(), "model stream disconnected") {
		t.Errorf("Wait error = %v, want it to mention the turn error", err)
	}
	evs := drain(sess)

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
