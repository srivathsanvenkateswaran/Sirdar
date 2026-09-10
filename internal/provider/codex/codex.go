// Package codex adapts the OpenAI Codex CLI to Sirdar's provider contract by
// driving `codex app-server`, its line-delimited JSON-RPC 2.0 stdio protocol.
//
// Sessions run with sandbox "read-only" and approvalPolicy "never", so the
// sandbox itself refuses writes and approval requests should not arrive. Any
// that do are declined, and every decline is surfaced as an EvPermission event
// so an operator can see what the agent tried to do.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

const (
	// clientVersion is reported in initialize.clientInfo.version.
	clientVersion = "0.1.0-dev"
	// defaultBinary is used when SessionSpec.Binary is empty.
	defaultBinary = "codex"
	// shutdownGrace is how long a closed stdin gets to end the process
	// before it is killed.
	shutdownGrace = 5 * time.Second
	// stderrTailLines is how much of the child's stderr is kept for
	// diagnostics.
	stderrTailLines = 50
)

// lastPumpDone exposes the most recent session's pump-exit signal to tests, so
// a failed Start can be checked for a leaked goroutine. Tests in this package
// therefore must not run in parallel.
var lastPumpDone chan struct{}

// codexProvider is the provider.Provider implementation for Codex.
type codexProvider struct{}

// New returns the Codex provider.
func New() provider.Provider { return codexProvider{} }

func (codexProvider) Name() string { return "codex" }

// Start spawns `<binary> app-server`, performs the initialize handshake,
// starts (or resumes) a thread and issues the first turn. It returns once the
// turn has been accepted; activity arrives on the session's event channel.
func (codexProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	binary := spec.Binary
	if binary == "" {
		binary = defaultBinary
	}

	cmd := exec.Command(binary, "app-server")
	cmd.Dir = spec.Cwd
	if len(spec.Env) > 0 {
		cmd.Env = spec.Env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("codex: stderr pipe: %w", err)
	}

	s := &session{
		cmd:        cmd,
		spec:       spec,
		events:     make(chan provider.Event),
		turnDone:   make(chan struct{}),
		exited:     make(chan struct{}),
		stdoutDone: make(chan struct{}),
		stderrDone: make(chan struct{}),
		stopped:    make(chan struct{}),
		pumpDone:   make(chan struct{}),
	}
	lastPumpDone = s.pumpDone
	s.queue.cond = sync.NewCond(&s.queue.mu)
	s.conn = newConn(stdout, stdin)
	s.conn.onNotify = s.onNotify
	s.conn.onRequest = s.onRequest

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codex: start %s: %w", binary, err)
	}

	go s.pump()
	go func() {
		defer close(s.stdoutDone)
		s.conn.run()
		// stdout is at EOF, so the child can send nothing more: end the
		// event stream here whatever state the turn was left in.
		s.closeStream()
	}()
	go func() {
		defer close(s.stderrDone)
		s.tail.readFrom(stderr)
	}()
	// exec.Cmd closes the stdout/stderr pipes as soon as Wait sees the
	// process exit, so Wait must not run until both readers have drained.
	go func() {
		<-s.stdoutDone
		<-s.stderrDone
		err := cmd.Wait()
		s.mu.Lock()
		if s.exitErr == nil {
			s.exitErr = err
		}
		s.mu.Unlock()
		close(s.exited)
	}()

	if err := s.handshake(ctx); err != nil {
		s.abort()
		return nil, err
	}
	return s, nil
}

// handshake runs initialize → initialized → thread/start|thread/resume →
// turn/start.
func (s *session) handshake(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := s.conn.call("initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "sirdar",
			"title":   "Sirdar",
			"version": clientVersion,
		},
	}); err != nil {
		return fmt.Errorf("codex: initialize: %w", err)
	}
	if err := s.conn.notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("codex: initialized: %w", err)
	}

	method := "thread/start"
	params := map[string]any{
		"sandbox":        "read-only",
		"approvalPolicy": "never",
	}
	if s.spec.Resume != "" {
		method = "thread/resume"
		params["threadId"] = s.spec.Resume
	} else {
		params["cwd"] = s.spec.Cwd
		if s.spec.Model != "" {
			params["model"] = s.spec.Model
		}
	}
	raw, err := s.conn.call(method, params)
	if err != nil {
		return fmt.Errorf("codex: %s: %w", method, err)
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &started); err != nil {
		return fmt.Errorf("codex: %s result: %w", method, err)
	}
	if started.Thread.ID == "" {
		return fmt.Errorf("codex: %s returned no thread id", method)
	}
	s.mu.Lock()
	s.threadID = started.Thread.ID
	s.mu.Unlock()

	return s.startTurn(s.spec.Prompt, s.spec.Images)
}

// startTurn issues turn/start with the prompt text, any local images and the
// session's output schema.
func (s *session) startTurn(text string, images []string) error {
	input := []map[string]string{{"type": "text", "text": text}}
	for _, path := range images {
		input = append(input, map[string]string{"type": "localImage", "path": path})
	}

	s.mu.Lock()
	threadID := s.threadID
	s.mu.Unlock()

	params := map[string]any{
		"threadId": threadID,
		"input":    input,
	}
	if len(s.spec.OutputSchema) > 0 {
		params["outputSchema"] = json.RawMessage(s.spec.OutputSchema)
	}

	raw, err := s.conn.call("turn/start", params)
	if err != nil {
		return fmt.Errorf("codex: turn/start: %w", err)
	}
	var accepted struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &accepted); err == nil && accepted.Turn.ID != "" {
		s.mu.Lock()
		s.turnID = accepted.Turn.ID
		s.mu.Unlock()
	}
	return nil
}

// ------------------------------------------------------------------ session

type session struct {
	cmd  *exec.Cmd
	spec provider.SessionSpec
	conn *conn
	tail stderrTail

	events chan provider.Event
	queue  eventQueue

	mu         sync.Mutex
	threadID   string
	turnID     string
	finalText  string
	usage      struct{ in, out int64 }
	turns      int
	turnDone   chan struct{}
	turnClosed bool
	turnErr    error

	// streamClosed records that the event queue has been closed, so
	// Events() has ended and no further turn can be started on this
	// session. sendPending is set for the window in which Send is starting
	// a follow-up turn, which keeps the turn that just completed from
	// ending the stream underneath it.
	streamClosed bool
	sendPending  bool

	exitErr error

	exited     chan struct{}
	stdoutDone chan struct{}
	stderrDone chan struct{}

	// stopped is closed by abort to release a pump with no reader;
	// pumpDone closes when the pump goroutine has returned.
	stopped  chan struct{}
	stopOnce sync.Once
	pumpDone chan struct{}

	waitOnce sync.Once
	result   provider.Result
	waitErr  error
}

func (s *session) Events() <-chan provider.Event { return s.events }

func (s *session) Handle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

// Send starts a follow-up turn on the same thread, reusing the output schema.
// It fails once the event stream has ended, because a turn whose events
// nobody can observe is worse than no turn at all: the caller is expected to
// resume the thread in a fresh session instead.
func (s *session) Send(ctx context.Context, userText string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.streamClosed {
		s.mu.Unlock()
		return errors.New("codex: the session's event stream has ended")
	}
	s.sendPending = true
	if s.turnClosed {
		s.turnDone = make(chan struct{})
		s.turnClosed = false
		s.turnErr = nil
	}
	s.mu.Unlock()

	err := s.startTurn(userText, nil)

	s.mu.Lock()
	s.sendPending = false
	s.mu.Unlock()
	if err != nil {
		// No follow-up turn is coming after all, so the stream the
		// completed turn held open has to end here rather than leave the
		// caller ranging over Events() forever.
		s.closeStream()
	}
	return err
}

// CloseInput is a no-op for Codex: a turn's completion already ends the
// event stream, so there is no stdin EOF the session is waiting on. It is
// here because the provider contract has it, and because closing the
// app-server's stdin outright would cut off the shutdown exchange Wait
// still needs.
func (s *session) CloseInput() error { return nil }

// Cancel interrupts the running turn and closes stdin.
func (s *session) Cancel() {
	s.mu.Lock()
	threadID, turnID := s.threadID, s.turnID
	s.mu.Unlock()
	_ = s.conn.notify("turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID})
	_ = s.conn.closeWrite()
}

// Wait blocks until the current turn ends, shuts the app-server down and
// returns the session's result.
func (s *session) Wait() (provider.Result, error) {
	s.waitOnce.Do(func() { s.result, s.waitErr = s.wait() })
	return s.result, s.waitErr
}

func (s *session) wait() (provider.Result, error) {
	for {
		s.mu.Lock()
		done := s.turnDone
		s.mu.Unlock()

		select {
		case <-done:
		case <-s.exited:
		}

		s.mu.Lock()
		same := s.turnDone == done
		s.mu.Unlock()
		if same {
			break
		}
	}

	s.mu.Lock()
	threadID := s.threadID
	s.mu.Unlock()
	_ = s.conn.notify("thread/unsubscribe", map[string]any{"threadId": threadID})

	s.shutdown()

	s.mu.Lock()
	res := provider.Result{Handle: s.threadID, ExitErr: s.exitErr, StderrTail: s.tail.lines()}
	if s.finalText != "" && json.Valid([]byte(s.finalText)) {
		res.Final = json.RawMessage(s.finalText)
	} else {
		res.Text = s.finalText
	}
	res.Usage.InputTok = s.usage.in
	res.Usage.OutputTok = s.usage.out
	res.Usage.Turns = s.turns
	err := s.turnErr
	if err == nil {
		err = s.exitErr
	}
	s.mu.Unlock()

	return res, err
}

// shutdown closes stdin, waits for the process (killing it after the grace
// period), then publishes the stderr tail and closes the event stream.
func (s *session) shutdown() {
	_ = s.conn.closeWrite()

	if !waitFor(s.exited, shutdownGrace) {
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		if !waitFor(s.exited, shutdownGrace) {
			s.mu.Lock()
			if s.exitErr == nil {
				s.exitErr = errors.New("codex: app-server did not exit")
			}
			s.mu.Unlock()
		}
	}

	s.closeStream()
}

// closeStream ends Events() for good: the queue is closed (pop drains what
// is already buffered before reporting the end) and no further turn may be
// started. It is safe to call more than once.
func (s *session) closeStream() {
	s.mu.Lock()
	s.streamClosed = true
	s.mu.Unlock()
	s.queue.close()
}

// endStream closes the event stream after a turn has completed, unless a
// follow-up turn is being started, in which case the stream stays open for
// it. The provider contract says Events() closes when the session ends, and
// a caller that drains Events() before calling Wait — which is what the
// runner does — would otherwise block forever on a channel nothing closes.
func (s *session) endStream() {
	s.mu.Lock()
	pending := s.sendPending
	s.mu.Unlock()
	if pending {
		return
	}
	s.closeStream()
}

// abort tears down a session whose handshake failed. Start returns an error in
// that case, so the caller never receives the session and never reads its
// events: release the pump before shutting the queue down.
func (s *session) abort() {
	s.stopOnce.Do(func() { close(s.stopped) })
	s.shutdown()
	<-s.pumpDone
}

// waitFor reports whether ch closed within d.
func waitFor(ch <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

// endTurn records a turn's outcome and releases Wait.
func (s *session) endTurn(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnErr == nil {
		s.turnErr = err
	}
	if !s.turnClosed {
		s.turnClosed = true
		close(s.turnDone)
	}
}

// -------------------------------------------------------------- event stream

// eventQueue is an unbounded buffer between the reader goroutine and the
// consumer of Events(). It keeps notification handling from blocking on a
// slow — or absent — consumer, which would deadlock the JSON-RPC reader.
type eventQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []provider.Event
	closed bool
}

func (q *eventQueue) push(ev provider.Event) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.items = append(q.items, ev)
	q.cond.Signal()
}

func (q *eventQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	q.cond.Signal()
}

// pop blocks until an event is available, or reports ok=false once the queue
// is closed and drained.
func (q *eventQueue) pop() (provider.Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return provider.Event{}, false
	}
	ev := q.items[0]
	q.items = q.items[1:]
	return ev, true
}

// pump moves queued events onto the Events channel. It gives up as soon as
// abort closes s.stopped: a session whose Start failed is handed to nobody, so
// there will never be a reader, and a blocked send would leak this goroutine.
func (s *session) pump() {
	defer close(s.events)
	defer close(s.pumpDone)
	for {
		ev, ok := s.queue.pop()
		if !ok {
			return
		}
		select {
		case s.events <- ev:
		case <-s.stopped:
			return
		}
	}
}

func (s *session) emit(ev provider.Event) {
	ev.At = time.Now()
	s.queue.push(ev)
}

// ------------------------------------------------------------ wire → events

// toolItemTypes are the item types reported as tool activity.
var toolItemTypes = map[string]bool{
	"commandExecution": true,
	"mcpToolCall":      true,
	"fileChange":       true,
	"webSearch":        true,
}

// noisyNotifications stream partial output; they carry nothing an operator
// needs in the event log.
var noisyNotifications = map[string]bool{
	"item/agentMessage/delta":           true,
	"item/reasoning/summaryTextDelta":   true,
	"item/reasoning/contentTextDelta":   true,
	"item/commandExecution/outputDelta": true,
	"item/mcpToolCall/progress":         true,
}

// item is the subset of a Codex thread item Sirdar reads.
type item struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Text      string          `json:"text"`
	Phase     string          `json:"phase"`
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Questions json.RawMessage `json:"questions"`
}

func (s *session) onNotify(method string, params json.RawMessage) {
	raw := envelope(nil, method, params)

	switch method {
	case "item/started", "item/completed":
		var payload struct {
			Item item `json:"item"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return
		}
		var itemRaw struct {
			Item json.RawMessage `json:"item"`
		}
		_ = json.Unmarshal(params, &itemRaw)
		it := payload.Item

		if toolItemTypes[it.Type] {
			kind := provider.EvToolStarted
			if method == "item/completed" {
				kind = provider.EvToolFinished
			}
			s.emit(provider.Event{Kind: kind, Tool: toolName(it), Input: itemRaw.Item, Raw: raw})
			return
		}
		if it.Type != "agentMessage" || method != "item/completed" {
			return
		}
		if hasQuestions(it.Questions) {
			s.emit(provider.Event{Kind: provider.EvQuestion, Text: it.Text, Input: it.Questions, Raw: raw})
			return
		}
		switch it.Phase {
		case "final_answer":
			s.mu.Lock()
			s.finalText = it.Text
			s.mu.Unlock()
		case "commentary":
			s.emit(provider.Event{Kind: provider.EvAssistantText, Text: it.Text, Raw: raw})
		}

	case "turn/started":
		var payload struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if err := json.Unmarshal(params, &payload); err == nil && payload.Turn.ID != "" {
			s.mu.Lock()
			s.turnID = payload.Turn.ID
			s.mu.Unlock()
		}

	case "thread/tokenUsage/updated":
		var payload struct {
			TokenUsage struct {
				Total struct {
					InputTokens  int64 `json:"inputTokens"`
					OutputTokens int64 `json:"outputTokens"`
				} `json:"total"`
			} `json:"tokenUsage"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return
		}
		in, out := payload.TokenUsage.Total.InputTokens, payload.TokenUsage.Total.OutputTokens
		s.mu.Lock()
		s.usage.in, s.usage.out = in, out
		s.mu.Unlock()
		s.emit(provider.Event{Kind: provider.EvUsage, InputTok: in, OutputTok: out, Raw: raw})

	case "account/rateLimits/updated":
		var payload struct {
			RateLimits struct {
				Primary *struct {
					UsedPercent float64 `json:"usedPercent"`
					ResetsAt    int64   `json:"resetsAt"`
				} `json:"primary"`
			} `json:"rateLimits"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return
		}
		primary := payload.RateLimits.Primary
		if primary != nil && primary.UsedPercent >= 100 {
			s.emit(provider.Event{
				Kind:     provider.EvRateLimited,
				Text:     "codex primary window",
				ResetsAt: time.Unix(primary.ResetsAt, 0),
				Raw:      raw,
			})
			return
		}
		s.emit(provider.Event{Kind: provider.EvSystem, Text: method, Raw: raw})

	case "turn/completed":
		s.onTurnCompleted(params, raw)

	default:
		if noisyNotifications[method] {
			return
		}
		s.emit(provider.Event{Kind: provider.EvSystem, Text: method, Raw: raw})
	}
}

func (s *session) onTurnCompleted(params, raw json.RawMessage) {
	var payload struct {
		Turn struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
			Items []item `json:"items"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}

	s.mu.Lock()
	s.turns++
	if s.finalText == "" {
		// The final answer is repeated in the completed turn's items.
		for _, it := range payload.Turn.Items {
			if it.Type == "agentMessage" && it.Phase == "final_answer" {
				s.finalText = it.Text
			}
		}
	}
	finalText := s.finalText
	s.mu.Unlock()

	if payload.Turn.Status == "failed" {
		msg := "codex turn failed"
		if payload.Turn.Error != nil && payload.Turn.Error.Message != "" {
			msg = payload.Turn.Error.Message
		}
		s.emit(provider.Event{Kind: provider.EvError, Text: msg, Raw: raw})
		s.endTurn(errors.New(msg))
		s.endStream()
		return
	}

	ev := provider.Event{Kind: provider.EvFinal, Raw: raw}
	if finalText != "" && json.Valid([]byte(finalText)) {
		ev.Final = json.RawMessage(finalText)
	} else {
		ev.Text = finalText
	}
	s.emit(ev)
	s.endTurn(nil)
	s.endStream()
}

// onRequest answers the server-to-client requests Codex can raise. Triage
// sessions are read-only, so every approval is declined.
func (s *session) onRequest(id json.RawMessage, method string, params json.RawMessage) {
	raw := envelope(id, method, params)

	switch method {
	case "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"item/permissions/requestApproval":
		_ = s.conn.reply(id, map[string]string{"decision": "decline"})
		s.emit(provider.Event{
			Kind:     provider.EvPermission,
			Decision: "deny",
			Tool:     approvalTool(method),
			Input:    params,
			Text:     "Sirdar policy: triage runs are read-only",
			Raw:      raw,
		})

	case "item/tool/requestUserInput":
		_ = s.conn.reply(id, map[string]any{"answers": map[string]any{}})
		s.emit(provider.Event{Kind: provider.EvQuestion, Text: "codex asked for user input", Input: params, Raw: raw})

	case "mcpServer/elicitation/request":
		_ = s.conn.reply(id, map[string]string{"action": "decline"})
		s.emit(provider.Event{Kind: provider.EvSystem, Text: method, Raw: raw})

	default:
		_ = s.conn.reply(id, map[string]any{})
		s.emit(provider.Event{Kind: provider.EvSystem, Text: method, Raw: raw})
	}
}

// approvalTool turns "item/commandExecution/requestApproval" into
// "commandExecution".
func approvalTool(method string) string {
	parts := strings.Split(method, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return method
}

func toolName(it item) string {
	if it.Type == "mcpToolCall" && it.Server != "" && it.Tool != "" {
		return it.Server + "/" + it.Tool
	}
	return it.Type
}

func hasQuestions(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "[]"
}

// envelope rebuilds the message as it arrived, for Event.Raw.
func envelope(id json.RawMessage, method string, params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		params = json.RawMessage("null")
	}
	if hasID(id) {
		return json.RawMessage(fmt.Sprintf(`{"id":%s,"method":%q,"params":%s}`, strings.TrimSpace(string(id)), method, params))
	}
	return json.RawMessage(fmt.Sprintf(`{"method":%q,"params":%s}`, method, params))
}

// ------------------------------------------------------------- stderr buffer

// stderrTail keeps the last stderrTailLines lines the child wrote to stderr.
type stderrTail struct {
	mu  sync.Mutex
	buf []string
}

func (t *stderrTail) readFrom(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		t.add(sc.Text())
	}
}

func (t *stderrTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, line)
	if len(t.buf) > stderrTailLines {
		t.buf = t.buf[len(t.buf)-stderrTailLines:]
	}
}

func (t *stderrTail) lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.buf))
	copy(out, t.buf)
	return out
}

func (t *stderrTail) String() string { return strings.Join(t.lines(), "\n") }

// ------------------------------------------------------------------- doctor

// Doctor checks that the Codex CLI is installed, logged in, and that its
// app-server starts and reports its MCP servers.
func (codexProvider) Doctor(ctx context.Context, binary string) []provider.Check {
	if binary == "" {
		binary = defaultBinary
	}
	checks := []provider.Check{versionCheck(ctx, binary), loginCheck(ctx, binary)}
	return append(checks, mcpCheck(ctx, binary))
}

func versionCheck(ctx context.Context, binary string) provider.Check {
	out, err := runWithTimeout(ctx, 10*time.Second, binary, "--version")
	if err != nil {
		return provider.Check{Name: "codex binary", Detail: fmt.Sprintf("%s: %v", binary, err)}
	}
	return provider.Check{Name: "codex binary", OK: true, Detail: firstLine(out)}
}

func loginCheck(ctx context.Context, binary string) provider.Check {
	out, err := runWithTimeout(ctx, 10*time.Second, binary, "login", "status")
	if err != nil && out == "" {
		return provider.Check{Name: "codex login", Detail: err.Error()}
	}
	if strings.Contains(out, "Logged in") {
		return provider.Check{Name: "codex login", OK: true, Detail: firstLine(out)}
	}
	detail := firstLine(out)
	if detail == "" {
		detail = "not logged in"
	}
	return provider.Check{Name: "codex login", Detail: detail}
}

// mcpCheck starts the app-server, initializes it and lists the MCP servers it
// is configured with.
func mcpCheck(ctx context.Context, binary string) provider.Check {
	const name = "codex mcp servers"

	cmd := exec.Command(binary, "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}
	if err := cmd.Start(); err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}

	c := newConn(stdout, stdin)
	c.onNotify = func(string, json.RawMessage) {}
	c.onRequest = func(id json.RawMessage, _ string, _ json.RawMessage) {
		_ = c.reply(id, map[string]any{})
	}
	go c.run()

	defer func() {
		_ = c.closeWrite()
		done := make(chan struct{})
		go func() {
			<-c.done
			_ = cmd.Wait()
			close(done)
		}()
		if !waitFor(done, shutdownGrace) {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			waitFor(done, shutdownGrace)
		}
	}()

	if err := ctx.Err(); err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}
	if _, err := c.call("initialize", map[string]any{
		"clientInfo": map[string]string{"name": "sirdar", "title": "Sirdar", "version": clientVersion},
	}); err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}
	_ = c.notify("initialized", map[string]any{})

	raw, err := c.call("mcpServerStatus/list", map[string]any{})
	if err != nil {
		return provider.Check{Name: name, Detail: err.Error()}
	}
	names := mcpServerNames(raw)
	if len(names) == 0 {
		return provider.Check{Name: name, OK: true, Detail: "none"}
	}
	return provider.Check{Name: name, OK: true, Detail: strings.Join(names, ", ")}
}

// mcpServerNames reads server names out of a mcpServerStatus/list result,
// which is either {"servers":[{"name":...}]} or a name-keyed object.
func mcpServerNames(raw json.RawMessage) []string {
	var listed struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(raw, &listed); err == nil && len(listed.Servers) > 0 {
		names := make([]string, 0, len(listed.Servers))
		for _, srv := range listed.Servers {
			if srv.Name != "" {
				names = append(names, srv.Name)
			}
		}
		sort.Strings(names)
		return names
	}

	var keyed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return nil
	}
	if _, ok := keyed["servers"]; ok {
		return nil
	}
	names := make([]string, 0, len(keyed))
	for key := range keyed {
		names = append(names, key)
	}
	sort.Strings(names)
	return names
}

func runWithTimeout(ctx context.Context, d time.Duration, binary string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
