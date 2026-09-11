package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// --- the fake MCP server -------------------------------------------------

// mcpFakeEnv makes the test binary re-exec itself as an MCP server rather
// than run tests, the same trick internal/mcpclient uses: `go test` builds
// the fake for free, and no fixture binary has to be checked in.
const mcpFakeEnv = "SIRDAR_OPENAI_MCP_FAKE"

func TestMain(m *testing.M) {
	if os.Getenv(mcpFakeEnv) == "" {
		os.Exit(m.Run())
	}
	runFakeMCP()
}

// runFakeMCP speaks just enough MCP for the loop: the handshake, one tool,
// and a text result. It writes one line to stderr on startup, which is
// what the StderrTail assertions look for.
func runFakeMCP() {
	fmt.Fprintln(os.Stderr, "fake mcp server: listening on stdio")

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var frame struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string `json:"name"`
				Arguments struct {
					Message string `json:"message"`
				} `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		switch frame.Method {
		case "initialize":
			mcpReply(frame.ID, `{"protocolVersion":"2025-06-18","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"fake-mcp","version":"1.0.0"}}`)
		case "tools/list":
			mcpReply(frame.ID, `{"tools":[{"name":"echo","description":"echo a message","inputSchema":{"type":"object","properties":{"message":{"type":"string"}}}}]}`)
		case "tools/call":
			if frame.Params.Name != "echo" {
				mcpReply(frame.ID, `{"content":[{"type":"text","text":"no such tool"}],"isError":true}`)
				continue
			}
			result, err := json.Marshal(map[string]any{
				"content": []map[string]string{{"type": "text", "text": "echo: " + frame.Params.Arguments.Message}},
			})
			if err != nil {
				continue
			}
			mcpReply(frame.ID, string(result))
		case "notifications/initialized":
		default:
			if len(frame.ID) > 0 {
				fmt.Printf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"no such method"}}`+"\n", frame.ID)
			}
		}
	}
	os.Exit(0)
}

func mcpReply(id json.RawMessage, result string) {
	fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", id, result)
}

// writeMCPConfig points a workspace's .mcp.json at this test binary.
func writeMCPConfig(t *testing.T, root string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"fake": map[string]any{
				"command": self,
				"env":     map[string]string{mcpFakeEnv: "1"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- the fake chat server ------------------------------------------------

// capturedRequest is what the loop sent for one turn.
type capturedRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		Name       string `json:"name"`
		ToolCallID string `json:"tool_call_id"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
	ToolChoice string `json:"tool_choice"`
}

// chatServer is a scripted Chat Completions endpoint: it answers each turn
// from the reply function and records what it was asked.
type chatServer struct {
	srv   *httptest.Server
	reply func(turn int, req capturedRequest) string

	mu       sync.Mutex
	requests []capturedRequest
}

func newChatServer(t *testing.T, reply func(turn int, req capturedRequest) string) *chatServer {
	t.Helper()
	cs := &chatServer{reply: reply}
	cs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var req capturedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cs.mu.Lock()
		cs.requests = append(cs.requests, req)
		turn := len(cs.requests) - 1
		cs.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cs.reply(turn, req)))
	}))
	t.Cleanup(cs.srv.Close)
	return cs
}

func (c *chatServer) captured() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedRequest(nil), c.requests...)
}

// toolCallReply is one assistant message asking for one tool call.
func toolCallReply(id, name, args string, prompt, completion int) string {
	return reply(map[string]any{
		"role": "assistant",
		"tool_calls": []map[string]any{{
			"id":       id,
			"type":     "function",
			"function": map[string]string{"name": name, "arguments": args},
		}},
	}, "tool_calls", prompt, completion)
}

// textReply is one assistant message with no tool call.
func textReply(text string, prompt, completion int) string {
	return reply(map[string]any{"role": "assistant", "content": text}, "stop", prompt, completion)
}

func reply(message map[string]any, finish string, prompt, completion int) string {
	b, err := json.Marshal(map[string]any{
		"model": "test-model",
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
		"usage": map[string]int{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      prompt + completion,
		},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// scripted answers turn n with the nth body, and repeats the last one
// after that.
func scripted(bodies ...string) func(int, capturedRequest) string {
	return func(turn int, _ capturedRequest) string {
		if turn >= len(bodies) {
			return bodies[len(bodies)-1]
		}
		return bodies[turn]
	}
}

// --- session helpers -----------------------------------------------------

const noteSchema = `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`

const noteJSON = `{"title":"Export times out on large orders"}`

func newSession(t *testing.T, cs *chatServer, cfg LoopConfig, spec provider.SessionSpec) provider.Session {
	t.Helper()
	cfg.Chat.BaseURL = cs.srv.URL
	if cfg.Chat.Model == "" {
		cfg.Chat.Model = "test-model"
	}
	if spec.OutputSchema == nil {
		spec.OutputSchema = []byte(noteSchema)
	}
	if spec.Policy == nil {
		spec.Policy = &provider.PermissionPolicy{}
	}
	if spec.Prompt == "" {
		spec.Prompt = "Triage OMNI-1."
	}
	sess, err := NewProvider(cfg).Start(t.Context(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(sess.Cancel)
	return sess
}

// drain reads every event to the end of the stream.
func drain(t *testing.T, sess provider.Session) []provider.Event {
	t.Helper()
	var out []provider.Event
	for ev := range sess.Events() {
		out = append(out, ev)
	}
	return out
}

// summary renders an event stream as short strings, so a test can assert
// on the whole sequence rather than on hand-picked indexes.
func summary(events []provider.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		switch ev.Kind {
		case provider.EvPermission:
			out = append(out, fmt.Sprintf("permission:%s:%s", ev.Tool, ev.Decision))
		case provider.EvToolStarted, provider.EvToolFinished:
			out = append(out, fmt.Sprintf("%s:%s", ev.Kind, ev.Tool))
		default:
			out = append(out, string(ev.Kind))
		}
	}
	return out
}

func kindsEqual(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// --- tests ---------------------------------------------------------------

// TestLoopReadsRefusesCallsMCPAndSubmits is the whole contract in one run:
// a local tool, a command the policy refuses, a tool on the workspace's
// MCP server, and the note.
func TestLoopReadsRefusesCallsMCPAndSubmits(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("the export times out\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMCPConfig(t, root)

	cs := newChatServer(t, scripted(
		toolCallReply("c1", "read_file", `{"path":"notes.txt"}`, 100, 10),
		toolCallReply("c2", "bash", `{"command":"rm -rf /"}`, 100, 10),
		toolCallReply("c3", "mcp__fake__echo", `{"message":"hi"}`, 100, 10),
		toolCallReply("c4", submitNoteTool, noteJSON, 100, 10),
	))

	sess := newSession(t, cs, LoopConfig{
		MaxContextTokens:   128000,
		PriceInputPerMTok:  3,
		PriceOutputPerMTok: 15,
		MCPWorkspaceOnly:   true,
	}, provider.SessionSpec{
		Cwd:    root,
		Policy: &provider.PermissionPolicy{BashAllow: []string{"git log*"}},
		Budget: provider.Budget{MaxTurns: 10},
	})

	events := drain(t, sess)
	got := summary(events)
	want := []string{
		"usage", "permission:read_file:allow", "tool_started:read_file", "tool_finished:read_file",
		"usage", "permission:bash:deny",
		"usage", "permission:mcp__fake__echo:allow", "tool_started:mcp__fake__echo", "tool_finished:mcp__fake__echo",
		"usage", "final",
		"system", // the end-of-session barrier
	}
	if !kindsEqual(got, want...) {
		t.Fatalf("event sequence:\n got %v\nwant %v", got, want)
	}

	byKind := func(kind provider.EventKind) []provider.Event {
		var out []provider.Event
		for _, ev := range events {
			if ev.Kind == kind {
				out = append(out, ev)
			}
		}
		return out
	}

	// The local tool really read the file, and the MCP server really
	// answered.
	finished := byKind(provider.EvToolFinished)
	if !strings.Contains(finished[0].Text, "the export times out") {
		t.Errorf("read_file output = %q", finished[0].Text)
	}
	if finished[1].Text != "echo: hi" {
		t.Errorf("mcp echo output = %q", finished[1].Text)
	}

	// The refused command carries the policy's own message, and the model
	// was told, in the next request, that it was denied.
	denied := byKind(provider.EvPermission)[1]
	if !strings.Contains(denied.Text, "not in the allow-list") {
		t.Errorf("denial message = %q", denied.Text)
	}
	requests := cs.captured()
	if len(requests) != 4 {
		t.Fatalf("the loop took %d turns, want 4", len(requests))
	}
	if got := lastToolMessage(t, requests[2]); !strings.HasPrefix(got, "denied: ") {
		t.Errorf("tool message after a denial = %q", got)
	}

	// Usage accumulates across turns, and cost comes from the price table:
	// 400 prompt tokens at $3/M plus 40 completion tokens at $15/M.
	usage := byKind(provider.EvUsage)
	last := usage[len(usage)-1]
	if last.Turns != 4 || last.InputTok != 400 || last.OutputTok != 40 {
		t.Errorf("cumulative usage = turns %d, in %d, out %d", last.Turns, last.InputTok, last.OutputTok)
	}
	if want := 400.0/1e6*3 + 40.0/1e6*15; math.Abs(last.CostUSD-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", last.CostUSD, want)
	}

	final := byKind(provider.EvFinal)[0]
	if string(final.Final) != noteJSON {
		t.Errorf("Final = %s", final.Final)
	}

	// Every tool the model was offered: the six local ones, the MCP
	// server's, and submit_note carrying the run's schema.
	var names []string
	for _, tool := range requests[0].Tools {
		names = append(names, tool.Function.Name)
	}
	if want := []string{"read_file", "list_dir", "grep", "glob", "bash", "web_fetch", "mcp__fake__echo", submitNoteTool}; !kindsEqual(names, want...) {
		t.Errorf("tools offered = %v, want %v", names, want)
	}
	if got := string(requests[0].Tools[len(requests[0].Tools)-1].Function.Parameters); got != noteSchema {
		t.Errorf("submit_note parameters = %s, want the run's schema", got)
	}

	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if string(res.Final) != noteJSON {
		t.Errorf("Result.Final = %s", res.Final)
	}
	if res.ExitErr != nil {
		t.Errorf("Result.ExitErr = %v", res.ExitErr)
	}
	if res.Usage.Turns != 4 || res.Usage.InputTok != 400 {
		t.Errorf("Result.Usage = %+v", res.Usage)
	}
	if !strings.Contains(strings.Join(res.StderrTail, "\n"), "fake mcp server: listening") {
		t.Errorf("StderrTail = %q, want the MCP server's stderr", res.StderrTail)
	}
	if h := sess.Handle(); h != "" {
		t.Errorf("Handle() = %q, want empty: this provider cannot resume", h)
	}
}

// lastToolMessage returns the content of the last tool message in a
// request, which is what the model was told about the previous call.
func lastToolMessage(t *testing.T, req capturedRequest) string {
	t.Helper()
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "tool" {
			return req.Messages[i].Content
		}
	}
	t.Fatal("no tool message in the request")
	return ""
}

// TestLoopSendContinuesAfterAFinal covers the runner's schema retry: the
// note failed validation, the answer has to be asked for again on the same
// session, and the events of that second attempt have to be observable.
func TestLoopSendContinuesAfterAFinal(t *testing.T) {
	cs := newChatServer(t, scripted(
		toolCallReply("c1", submitNoteTool, `{"title":""}`, 50, 5),
		toolCallReply("c2", submitNoteTool, noteJSON, 50, 5),
	))
	sess := newSession(t, cs, LoopConfig{}, provider.SessionSpec{Cwd: t.TempDir()})

	const retry = "Your previous answer did not match the schema. Reply again."
	var finals []string
	for ev := range sess.Events() {
		if ev.Kind != provider.EvFinal {
			continue
		}
		finals = append(finals, string(ev.Final))
		if len(finals) == 1 {
			if err := sess.Send(t.Context(), retry); err != nil {
				t.Fatalf("Send after a final: %v", err)
			}
		}
	}

	if len(finals) != 2 || finals[1] != noteJSON {
		t.Fatalf("finals = %v, want the retry's note as the second", finals)
	}
	requests := cs.captured()
	if len(requests) != 2 {
		t.Fatalf("the loop took %d turns, want 2", len(requests))
	}
	last := requests[1].Messages[len(requests[1].Messages)-1]
	if last.Role != "user" || last.Content != retry {
		t.Errorf("the retry turn's last message = %+v", last)
	}
	// The submitted note is answered like any other tool call, so the
	// retry request is a transcript the server will accept.
	if got := lastToolMessage(t, requests[1]); got == "" {
		t.Error("the submit_note call was left unanswered in the retry request")
	}

	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if string(res.Final) != noteJSON {
		t.Errorf("Result.Final = %s, want the retry's note", res.Final)
	}
	if err := sess.Send(t.Context(), "too late"); err == nil {
		t.Error("Send after the stream ended should fail")
	}
}

// TestLoopProseEarnsOneNudgeThenFails covers a model that will not call
// the tool: one reminder, and then the session ends rather than spending
// the budget on more prose.
func TestLoopProseEarnsOneNudgeThenFails(t *testing.T) {
	cs := newChatServer(t, scripted(
		textReply("I think the export times out.", 10, 5),
		textReply("As I said, it times out.", 10, 5),
	))
	sess := newSession(t, cs, LoopConfig{}, provider.SessionSpec{Cwd: t.TempDir()})

	events := drain(t, sess)
	got := summary(events)
	want := []string{
		"usage", "assistant_text", "system", // the nudge
		"usage", "assistant_text", "error",
		"system", // the end-of-session barrier
	}
	if !kindsEqual(got, want...) {
		t.Fatalf("event sequence:\n got %v\nwant %v", got, want)
	}
	if last := events[len(events)-2]; !strings.Contains(last.Text, "prose twice") {
		t.Errorf("error text = %q", last.Text)
	}

	requests := cs.captured()
	if len(requests) != 2 {
		t.Fatalf("the loop took %d turns, want 2", len(requests))
	}
	nudge := requests[1].Messages[len(requests[1].Messages)-1]
	if nudge.Role != "user" || nudge.Content != Nudge() {
		t.Errorf("the second turn's last message = %+v, want the nudge", nudge)
	}

	res, _ := sess.Wait()
	if res.ExitErr == nil {
		t.Error("Result.ExitErr is nil after a session that produced no note")
	}
	if len(res.Final) != 0 {
		t.Errorf("Result.Final = %s, want none", res.Final)
	}
}

// TestLoopAcceptsAJSONObjectReply covers the model that answers with the
// note as text instead of calling the tool. The note is what matters, so
// it is taken.
func TestLoopAcceptsAJSONObjectReply(t *testing.T) {
	cs := newChatServer(t, scripted(textReply(noteJSON, 10, 5)))
	sess := newSession(t, cs, LoopConfig{}, provider.SessionSpec{Cwd: t.TempDir()})

	events := drain(t, sess)
	if got := summary(events); !kindsEqual(got, "usage", "final", "system") {
		t.Fatalf("event sequence = %v", got)
	}
	res, _ := sess.Wait()
	if string(res.Final) != noteJSON {
		t.Errorf("Result.Final = %s", res.Final)
	}
}

// TestLoopStopsAtTheTurnBudget covers a model that keeps calling tools and
// never submits: the run has to stop at the budget, with an error saying
// why.
func TestLoopStopsAtTheTurnBudget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := newChatServer(t, scripted(toolCallReply("c", "read_file", `{"path":"a.txt"}`, 10, 5)))
	sess := newSession(t, cs, LoopConfig{}, provider.SessionSpec{
		Cwd:    root,
		Budget: provider.Budget{MaxTurns: 2},
	})

	events := drain(t, sess)
	last := events[len(events)-2]
	if last.Kind != provider.EvError || !strings.Contains(last.Text, "2 turns") {
		t.Fatalf("last event = %+v, want the turn-budget error", last)
	}
	if got := len(cs.captured()); got != 2 {
		t.Errorf("the loop took %d turns, want the 2 it was given", got)
	}

	// The runner decides "over budget" from a usage event whose turn
	// count is past the budget, so the loop has to emit one before it
	// stops. Without it the run is filed as a plain failure, and the
	// operator cannot tell a spent budget from a broken endpoint.
	over := events[len(events)-3]
	if over.Kind != provider.EvUsage || over.Turns <= 2 {
		t.Fatalf("the event before the error = %+v, want a usage event past the 2-turn budget", over)
	}
}

// TestLoopBlocksOnARateLimit covers a 429 the chat client's one retry did
// not clear. That is a blocked run, not a failed one: the runner reads the
// reset time off the event and holds its queue until then.
func TestLoopBlocksOnARateLimit(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		// The first Retry-After is how long the client's own retry
		// waits, so the test asks it to wait for nothing; the second is
		// the one that becomes ResetsAt.
		if n == 1 {
			w.Header().Set("Retry-After", "0")
		} else {
			w.Header().Set("Retry-After", "90")
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	t.Cleanup(srv.Close)

	sess, err := NewProvider(LoopConfig{Chat: Config{BaseURL: srv.URL, Model: "test-model"}}).
		Start(t.Context(), provider.SessionSpec{
			Cwd:          t.TempDir(),
			Prompt:       "Triage OMNI-1.",
			OutputSchema: []byte(noteSchema),
			Policy:       &provider.PermissionPolicy{},
		})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(sess.Cancel)

	before := time.Now()
	events := drain(t, sess)

	mu.Lock()
	got := hits
	mu.Unlock()
	if got != 2 {
		t.Errorf("the endpoint was called %d times, want the request plus its one retry", got)
	}

	var limited *provider.Event
	for i := range events {
		if events[i].Kind == provider.EvRateLimited {
			limited = &events[i]
		}
		if events[i].Kind == provider.EvError {
			t.Errorf("a rate limit was also reported as an error: %q", events[i].Text)
		}
	}
	if limited == nil {
		t.Fatalf("no rate-limited event in %v", summary(events))
	}
	if !strings.Contains(limited.Text, "429") {
		t.Errorf("event text = %q, want the status in it", limited.Text)
	}
	// Retry-After: 90 is a relative number of seconds, so the window
	// reopens about a minute and a half after the call was refused.
	if delta := limited.ResetsAt.Sub(before); delta < 80*time.Second || delta > 100*time.Second {
		t.Errorf("ResetsAt is %s away, want about 90s", delta)
	}

	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	var rl *RateLimitError
	if !errors.As(res.ExitErr, &rl) {
		t.Errorf("Result.ExitErr = %v, want a *RateLimitError", res.ExitErr)
	}
}

// TestLoopAnswersEveryCallWhenSubmitNoteIsNotLast covers a model that
// batches submit_note with other calls. Every id in the batch has to come
// back answered: the schema retry sends the transcript on again, and most
// endpoints reject one carrying a tool_call_id nothing replied to.
func TestLoopAnswersEveryCallWhenSubmitNoteIsNotLast(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	batch := reply(map[string]any{
		"role": "assistant",
		"tool_calls": []map[string]any{
			{"id": "c1", "type": "function", "function": map[string]string{"name": submitNoteTool, "arguments": noteJSON}},
			{"id": "c2", "type": "function", "function": map[string]string{"name": "read_file", "arguments": `{"path":"a.txt"}`}},
			{"id": "c3", "type": "function", "function": map[string]string{"name": "list_dir", "arguments": `{"path":"."}`}},
		},
	}, "tool_calls", 100, 10)

	cs := newChatServer(t, scripted(batch, toolCallReply("c4", submitNoteTool, noteJSON, 100, 10)))
	sess := newSession(t, cs, LoopConfig{}, provider.SessionSpec{
		Cwd:    root,
		Budget: provider.Budget{MaxTurns: 5},
	})

	finals := 0
	for ev := range sess.Events() {
		if ev.Kind == provider.EvFinal {
			finals++
			if finals == 1 {
				// The runner's schema retry lands here, mid-drain.
				if err := sess.Send(t.Context(), "that note failed validation, try again"); err != nil {
					t.Fatalf("Send: %v", err)
				}
			}
		}
	}
	if finals == 0 {
		t.Fatal("no final event")
	}

	requests := cs.captured()
	if len(requests) < 2 {
		t.Fatalf("the retry turn was never sent (%d requests)", len(requests))
	}
	retry := requests[len(requests)-1]
	answered := map[string]string{}
	for _, m := range retry.Messages {
		if m.Role == "tool" {
			answered[m.ToolCallID] = m.Content
		}
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		if _, ok := answered[id]; !ok {
			t.Errorf("tool_call_id %q went unanswered in the retry transcript", id)
		}
	}
	// The calls after submit_note are answered, not run: the note is in,
	// and spending more of the budget gathering evidence for it is not
	// worth the turn.
	if !strings.Contains(answered["c2"], "skipped") {
		t.Errorf("the call after submit_note answered %q, want it marked skipped", answered["c2"])
	}
}

// TestLoopStopsWhenCostPassesTheBudget covers the USD budget, which only
// exists when the workspace configured a price.
func TestLoopStopsWhenCostPassesTheBudget(t *testing.T) {
	cs := newChatServer(t, scripted(textReply("thinking", 1_000_000, 0)))
	sess := newSession(t, cs, LoopConfig{PriceInputPerMTok: 2}, provider.SessionSpec{
		Cwd:    t.TempDir(),
		Budget: provider.Budget{MaxUSD: 1},
	})

	events := drain(t, sess)
	last := events[len(events)-2]
	if last.Kind != provider.EvError || !strings.Contains(last.Text, "$1.00 budget") {
		t.Fatalf("last event = %+v, want the cost-budget error", last)
	}
	if got := len(cs.captured()); got != 1 {
		t.Errorf("the loop took %d turns, want to stop after the first", got)
	}
}

// TestLoopTrimsOldToolResults covers the context guard: past the
// threshold, old tool results are replaced with a marker and the last few
// are kept whole.
func TestLoopTrimsOldToolResults(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("contents of a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ten read_file calls, then the note. Prompt tokens sit above 80% of
	// the configured window from the first turn.
	const turns = 10
	cs := newChatServer(t, func(turn int, _ capturedRequest) string {
		if turn >= turns {
			return toolCallReply("done", submitNoteTool, noteJSON, 90, 5)
		}
		return toolCallReply(fmt.Sprintf("c%d", turn), "read_file", `{"path":"a.txt"}`, 90, 5)
	})

	sess := newSession(t, cs, LoopConfig{MaxContextTokens: 100}, provider.SessionSpec{
		Cwd:    root,
		Budget: provider.Budget{MaxTurns: 20},
	})
	drain(t, sess)

	requests := cs.captured()
	last := requests[len(requests)-1]
	var tools []string
	for _, m := range last.Messages {
		if m.Role == "tool" {
			tools = append(tools, m.Content)
		}
	}
	if len(tools) != turns {
		t.Fatalf("the last request carried %d tool messages, want %d", len(tools), turns)
	}
	trimmed := 0
	for _, content := range tools {
		if content == trimmedMarker {
			trimmed++
		}
	}
	if trimmed != turns-keepToolResults {
		t.Fatalf("%d tool results were trimmed, want %d", trimmed, turns-keepToolResults)
	}
	for i, content := range tools[turns-keepToolResults:] {
		if content == trimmedMarker {
			t.Errorf("recent tool result %d was trimmed", i)
		}
	}
	// A run trimmed below the threshold keeps everything.
	quiet := newChatServer(t, func(turn int, _ capturedRequest) string {
		if turn >= turns {
			return toolCallReply("done", submitNoteTool, noteJSON, 10, 5)
		}
		return toolCallReply(fmt.Sprintf("c%d", turn), "read_file", `{"path":"a.txt"}`, 10, 5)
	})
	second := newSession(t, quiet, LoopConfig{MaxContextTokens: 100}, provider.SessionSpec{
		Cwd:    root,
		Budget: provider.Budget{MaxTurns: 20},
	})
	drain(t, second)
	for _, m := range quiet.captured()[turns].Messages {
		if m.Content == trimmedMarker {
			t.Fatal("a run below the threshold trimmed a tool result")
		}
	}
}

// TestLoopWarnsAboutUnusableMCPServers covers a workspace whose .mcp.json
// names a server that cannot run: the run continues on the local tools,
// with the warning on the event stream.
func TestLoopWarnsAboutUnusableMCPServers(t *testing.T) {
	root := t.TempDir()
	body := `{"mcpServers":{
		"remote": {"type":"http","url":"https://example.com/mcp"},
		"broken": {"command":"/nonexistent/mcp-server"}
	}}`
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cs := newChatServer(t, scripted(toolCallReply("c1", submitNoteTool, noteJSON, 10, 5)))
	sess := newSession(t, cs, LoopConfig{MCPWorkspaceOnly: true}, provider.SessionSpec{Cwd: root})

	events := drain(t, sess)
	var warnings []string
	for _, ev := range events {
		if ev.Kind == provider.EvSystem {
			warnings = append(warnings, ev.Text)
		}
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "remote") || !strings.Contains(joined, "broken") {
		t.Fatalf("warnings = %q, want one per unusable server", warnings)
	}
	res, _ := sess.Wait()
	if string(res.Final) != noteJSON {
		t.Errorf("the run should still finish: Final = %s", res.Final)
	}
	// No MCP tool reached the model, but the local ones did.
	for _, tool := range cs.captured()[0].Tools {
		if strings.HasPrefix(tool.Function.Name, "mcp__") {
			t.Errorf("a tool from an unusable server was offered: %s", tool.Function.Name)
		}
	}
}

// TestLoopCancelEndsTheSession covers the runner's interrupt path: Cancel
// closes the stream and Wait still returns a result.
func TestLoopCancelEndsTheSession(t *testing.T) {
	release := make(chan struct{})
	cs := newChatServer(t, func(turn int, _ capturedRequest) string {
		if turn == 0 {
			<-release
		}
		return toolCallReply("c1", submitNoteTool, noteJSON, 10, 5)
	})
	sess := newSession(t, cs, LoopConfig{}, provider.SessionSpec{Cwd: t.TempDir()})

	sess.Cancel()
	close(release)

	done := make(chan provider.Result, 1)
	go func() {
		drain(t, sess)
		res, _ := sess.Wait()
		done <- res
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the session did not end after Cancel")
	}
}

// TestStartRejectsAnIncompleteConfig covers the two settings a session
// cannot start without.
func TestStartRejectsAnIncompleteConfig(t *testing.T) {
	_, err := NewProvider(LoopConfig{Chat: Config{Model: "m"}}).Start(context.Background(), provider.SessionSpec{})
	if err == nil || !strings.Contains(err.Error(), "baseUrl") {
		t.Fatalf("missing baseUrl: err = %v", err)
	}
	_, err = NewProvider(LoopConfig{Chat: Config{BaseURL: "https://example.com/v1"}}).Start(context.Background(), provider.SessionSpec{})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("missing model: err = %v", err)
	}
}

// TestDoctorProbesTheEndpoint covers the doctor row: the binary argument
// is ignored, the endpoint is pinged, and the model is reported.
func TestDoctorProbesTheEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	}))
	defer srv.Close()

	checks := NewProvider(LoopConfig{Chat: Config{BaseURL: srv.URL, Model: "test-model"}}).
		Doctor(context.Background(), "/path/to/a/binary/that/does/not/exist")
	if len(checks) != 2 {
		t.Fatalf("checks = %+v", checks)
	}
	if !checks[0].OK || checks[0].Detail != srv.URL {
		t.Errorf("endpoint check = %+v", checks[0])
	}
	if !checks[1].OK || checks[1].Detail != "test-model" {
		t.Errorf("model check = %+v", checks[1])
	}

	unreachable := NewProvider(LoopConfig{Chat: Config{BaseURL: srv.URL + "/nope", Model: "m"}}).
		Doctor(context.Background(), "")
	if unreachable[0].OK {
		t.Errorf("an endpoint that answers 404 should fail the check: %+v", unreachable[0])
	}
}

func TestSystemPromptAndUserMessage(t *testing.T) {
	if !strings.Contains(System(), submitNoteTool) {
		t.Error("the system prompt does not name submit_note")
	}
	got := UserMessage("Triage OMNI-1.", []string{"/runs/bundle/shot.png"})
	if !strings.Contains(got, "Triage OMNI-1.") || !strings.Contains(got, "/runs/bundle/shot.png") {
		t.Errorf("UserMessage = %q", got)
	}
	if plain := UserMessage("Triage OMNI-1.", nil); plain != "Triage OMNI-1." {
		t.Errorf("UserMessage with no images = %q", plain)
	}
}

// TestFixModeOffersTheWritingTools: Sirdar's own loop hands the model a
// tool set, so the difference between a triage and a fix is which tools
// exist at all — not a refusal after the model asks.
func TestFixModeOffersTheWritingTools(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      provider.Mode
		wantWrite bool
	}{
		{"triage", provider.ModeTriage, false},
		{"fix", provider.ModeFix, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := newChatServer(t, scripted(toolCallReply("c1", submitNoteTool, noteJSON, 10, 5)))
			sess := newSession(t, cs, LoopConfig{}, provider.SessionSpec{Cwd: t.TempDir(), Mode: tc.mode})
			drain(t, sess)

			reqs := cs.captured()
			if len(reqs) == 0 {
				t.Fatal("the loop made no chat request")
			}
			offered := map[string]bool{}
			for _, tool := range reqs[0].Tools {
				offered[tool.Function.Name] = true
			}
			for _, name := range []string{"read_file", "grep", submitNoteTool} {
				if !offered[name] {
					t.Errorf("%s was not offered in %s mode; tools=%v", name, tc.mode, offered)
				}
			}
			for _, name := range []string{"write_file", "edit_file"} {
				if offered[name] != tc.wantWrite {
					t.Errorf("%s offered=%v in %s mode, want %v", name, offered[name], tc.mode, tc.wantWrite)
				}
			}
		})
	}
}

// TestFixModeSystemPromptDoesNotClaimReadOnly: the standing instruction has
// to describe the tools the session actually has.
func TestFixModeSystemPromptDoesNotClaimReadOnly(t *testing.T) {
	fix := SystemFor(provider.ModeFix)
	if strings.Contains(fix, "Your tools are read-only") {
		t.Error("the fix system prompt still tells the model its tools are read-only")
	}
	if !strings.Contains(fix, submitNoteTool) {
		t.Errorf("the fix system prompt does not name %s", submitNoteTool)
	}
	if SystemFor(provider.ModeTriage) != System() {
		t.Error("triage mode did not get the triage system prompt")
	}
}
