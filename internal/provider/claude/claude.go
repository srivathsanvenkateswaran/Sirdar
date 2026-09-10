// Package claude adapts the Claude Code CLI (`claude -p` with stream-json on
// both stdin and stdout) to the provider.Session contract: it starts the
// process, replies to permission control requests from a PermissionPolicy,
// and turns each output line into a provider.Event.
package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

const (
	defaultBinary   = "claude"
	maxLineBytes    = 16 << 20 // a single stream-json line can carry a big tool result
	eventBuffer     = 64
	stderrTailLines = 50
	doctorTimeout   = 10 * time.Second
	interruptGrace  = 10 * time.Second
	// disallowedTools is belt-and-braces with PermissionPolicy: the CLI
	// refuses these before it ever asks Sirdar.
	disallowedTools = "Write,Edit,MultiEdit,NotebookEdit"
)

// Provider starts Claude Code sessions.
type Provider struct{}

// New returns the Claude Code provider.
func New() provider.Provider { return &Provider{} }

// Name identifies this provider in config and run records.
func (p *Provider) Name() string { return "claude" }

// args builds the command line. `--bare` is never passed.
func args(spec provider.SessionSpec) []string {
	out := []string{
		"-p",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",
		"--permission-mode", "default",
	}
	// The runner always supplies a schema; an empty one is a caller bug, so
	// it is passed through rather than silently dropped.
	out = append(out, "--json-schema", compactJSON(spec.OutputSchema))
	if spec.Model != "" {
		out = append(out, "--model", spec.Model)
	}
	if spec.Budget.MaxTurns > 0 {
		out = append(out, "--max-turns", strconv.Itoa(spec.Budget.MaxTurns))
	}
	if spec.Resume != "" {
		out = append(out, "--resume", spec.Resume)
	}
	// With a config named, the session loads those MCP servers and only
	// those: --strict-mcp-config is what keeps the operator's own global
	// connectors — deploy, buy, send — out of a read-only triage run.
	if spec.MCPConfig != "" {
		out = append(out, "--strict-mcp-config", "--mcp-config", spec.MCPConfig)
	}
	return append(out, "--disallowedTools", disallowedTools)
}

// childEnv returns the environment for the child process. ANTHROPIC_API_KEY is
// stripped so the session bills against the subscription login, unless the
// caller marked the run as API-billed with SIRDAR_BILLING=api.
func childEnv(spec provider.SessionSpec) []string {
	base := spec.Env
	if len(base) == 0 {
		base = os.Environ()
	}
	keepKey := false
	for _, e := range base {
		if e == "SIRDAR_BILLING=api" {
			keepKey = true
			break
		}
	}
	out := make([]string, 0, len(base))
	for _, e := range base {
		if !keepKey && strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

func compactJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// Start launches the CLI and sends the prompt as the first user line.
func (p *Provider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	binary := spec.Binary
	if binary == "" {
		binary = defaultBinary
	}
	// runCtx is the one cancellation path: Cancel() cancels it, and so does
	// the caller's ctx. cmd.Cancel turns either into SIGINT, and WaitDelay
	// escalates to SIGKILL if the process has not exited by then.
	runCtx, cancelRun := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, binary, args(spec)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = interruptGrace
	cmd.Dir = spec.Cwd
	cmd.Env = childEnv(spec)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancelRun()
		return nil, fmt.Errorf("claude stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelRun()
		return nil, fmt.Errorf("claude stdout: %w", err)
	}
	tail := &tailWriter{max: stderrTailLines}
	cmd.Stderr = tail

	policy := spec.Policy
	if policy == nil {
		policy = &provider.PermissionPolicy{}
	}
	s := &session{
		binary:    binary,
		cmd:       cmd,
		cancelRun: cancelRun,
		stdin:     stdin,
		stderr:    tail,
		policy:    policy,
		events:    make(chan provider.Event, eventBuffer),
		readDone:  make(chan struct{}),
		done:      make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		cancelRun()
		return nil, fmt.Errorf("start %s: %w", binary, err)
	}
	go s.read(stdout)
	if err := s.writeUser(spec.Prompt); err != nil {
		// The session is never handed to the caller, so reap it here
		// rather than through Cancel/Wait.
		cancelRun()
		go func() {
			<-s.readDone
			_ = cmd.Wait()
		}()
		return nil, fmt.Errorf("claude prompt: %w", err)
	}
	return s, nil
}

// Doctor checks that the binary runs and that a login is present.
func (p *Provider) Doctor(ctx context.Context, binary string) []provider.Check {
	if binary == "" {
		binary = defaultBinary
	}

	version := provider.Check{Name: "claude --version"}
	out, err := runWithTimeout(ctx, binary, "--version")
	switch {
	case err != nil:
		version.Detail = strings.TrimSpace(firstLine(out) + " " + err.Error())
	case !startsWithDigit(firstLine(out)):
		version.Detail = "unexpected version output: " + firstLine(out)
	default:
		version.OK = true
		version.Detail = firstLine(out)
	}

	auth := provider.Check{Name: "claude auth status"}
	out, err = runWithTimeout(ctx, binary, "auth", "status")
	switch {
	case err == nil:
		auth.OK = true
		auth.Detail = authDetail(out)
	case strings.Contains(strings.ToLower(string(out)), "unknown command"):
		auth.OK = true
		auth.Detail = "auth status not supported by this version"
	default:
		auth.Detail = strings.TrimSpace(firstLine(out) + " " + err.Error())
	}

	return []provider.Check{version, auth}
}

// authDetail summarises `claude auth status`. Claude Code 2.1 answers with
// a JSON object, whose first line is "{" and tells the operator nothing;
// older builds answer with a sentence, which is passed through as it is.
// The account's email address, org id and org name are deliberately not
// reported: doctor's output gets pasted into tickets, and a personal
// account's orgName is that account's email with a suffix on it.
func authDetail(out []byte) string {
	var status struct {
		LoggedIn         *bool  `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &status); err != nil {
		return firstLine(out)
	}
	var parts []string
	switch {
	case status.LoggedIn == nil:
	case *status.LoggedIn:
		parts = append(parts, "logged in")
	default:
		parts = append(parts, "not logged in")
	}
	for _, s := range []string{status.AuthMethod, status.SubscriptionType} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return firstLine(out)
	}
	return strings.Join(parts, ", ")
}

func runWithTimeout(ctx context.Context, name string, arg ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, arg...).CombinedOutput()
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func startsWithDigit(s string) bool {
	return s != "" && s[0] >= '0' && s[0] <= '9'
}

// session is one running claude process.
type session struct {
	binary    string
	cmd       *exec.Cmd
	cancelRun context.CancelFunc
	stdin     io.WriteCloser
	stderr    *tailWriter
	policy    *provider.PermissionPolicy

	events   chan provider.Event
	readDone chan struct{} // closed when stdout hits EOF
	done     chan struct{} // closed when the process has been reaped

	writeMu   sync.Mutex // serialises stdin writes
	stdinShut bool       // set once stdin has been closed

	mu      sync.Mutex
	handle  string
	res     provider.Result
	waitErr error
	meter   usageMeter

	waitOnce sync.Once
}

// usageMeter accumulates what the session has spent so far. The CLI
// reports a turn's usage on each assistant line and the session total on
// the result line, so a run can show turns and tokens as they happen
// rather than only once it is over.
type usageMeter struct {
	turns    int
	inTok    int64
	outTok   int64
	costUSD  float64
	reported bool // a result line has given authoritative totals
}

// Events returns the activity stream. The channel is buffered; the caller
// must drain it, because the reader goroutine blocks once the buffer fills and
// Wait does not return until stdout has been read to EOF.
func (s *session) Events() <-chan provider.Event { return s.events }

func (s *session) Handle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handle
}

// Send delivers a follow-up user message, used for the schema-retry turn.
func (s *session) Send(ctx context.Context, userText string) error {
	select {
	case <-s.done:
		return errors.New("claude session has exited")
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.writeUser(userText)
}

// CloseInput closes the CLI's stdin. Started with --input-format
// stream-json, `claude -p` does not exit after its result line: it waits
// for the next user message, holding stdout open, and a run that has its
// answer would otherwise sit there until a budget killed it. Closing stdin
// is the EOF that lets the CLI finish. Calling it more than once, or after
// the process has gone, is not an error.
func (s *session) CloseInput() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.stdinShut {
		return nil
	}
	s.stdinShut = true
	if err := s.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}
	return nil
}

// Wait reaps the process and returns the session's Result. It must be called
// after Events has been drained. A non-zero exit is reported in Result.ExitErr
// rather than as an error, so the caller can still read the handle and
// whatever the session produced.
func (s *session) Wait() (provider.Result, error) {
	s.waitOnce.Do(func() {
		// Nothing more will be sent, and the CLI will not close stdout
		// until it knows that.
		_ = s.CloseInput()
		<-s.readDone
		err := s.cmd.Wait()
		tail := s.stderr.snapshot()

		s.mu.Lock()
		s.res.Handle = s.handle
		s.res.StderrTail = tail
		if err != nil {
			var exitErr *exec.ExitError
			switch {
			case errors.As(err, &exitErr):
				s.res.ExitErr = fmt.Errorf("%s exited with code %d%s", s.binary, exitErr.ExitCode(), formatTail(tail))
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				// Cancel() or the caller's context stopped the session; that
				// is a reported outcome, not a Wait failure.
				s.res.ExitErr = fmt.Errorf("%s cancelled: %w%s", s.binary, err, formatTail(tail))
			default:
				s.res.ExitErr = err
				s.waitErr = err
			}
		}
		s.mu.Unlock()
		s.cancelRun()
		close(s.done)
	})
	<-s.done

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.res, s.waitErr
}

// Cancel stops the session through the same path as a cancelled context:
// cmd.Cancel delivers SIGINT and cmd.WaitDelay escalates to SIGKILL if the
// process is still alive after interruptGrace. It does not block; the outcome
// shows up in Wait's Result.
//
// It marks stdin as shut without closing it, so the Wait that follows does
// not close the pipe out from under a process that is being interrupted:
// a CLI blocked on a read would then exit on EOF and never report the
// signal it was sent.
func (s *session) Cancel() {
	s.writeMu.Lock()
	s.stdinShut = true
	s.writeMu.Unlock()
	s.cancelRun()
}

// read consumes stdout until EOF, answering control requests inline and
// emitting one or more events per line.
func (s *session) read(stdout io.Reader) {
	defer close(s.readDone)
	defer close(s.events)

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		line := append([]byte(nil), raw...)
		if s.answerControl(line) {
			continue
		}
		for _, ev := range decode(line) {
			s.measure(&ev)
			s.absorb(ev)
			s.events <- ev
		}
	}
	if err := sc.Err(); err != nil {
		ev := newEvent(provider.EvError, nil)
		ev.Text = "read claude output: " + err.Error()
		s.events <- ev
	}
}

// answerControl replies to a control_request line and reports whether the line
// was one.
func (s *session) answerControl(raw []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || probe.Type != "control_request" {
		return false
	}
	var req controlRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		// The CLI is blocked waiting for a reply, so a request we cannot read
		// still has to be answered.
		s.denyUnparseable(raw)
		return true
	}

	if req.Request.Subtype != "can_use_tool" {
		s.writeControlResponse(req.RequestID, map[string]any{
			"behavior": "deny",
			"message":  "unsupported control request",
		})
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = "unsupported control request"
		s.events <- ev
		return true
	}

	decision := s.policy.Decide(req.Request.ToolName, req.Request.Input)
	response := map[string]any{"behavior": "deny", "message": decision.Message}
	if decision.Allow {
		input := req.Request.Input
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		response = map[string]any{"behavior": "allow", "updatedInput": input}
	}
	s.writeControlResponse(req.RequestID, response)

	ev := newEvent(provider.EvPermission, raw)
	ev.Tool = req.Request.ToolName
	ev.Input = req.Request.Input
	ev.Decision = "deny"
	if decision.Allow {
		ev.Decision = "allow"
	}
	ev.Text = decision.Message
	s.events <- ev
	return true
}

// denyUnparseable answers a control_request whose body did not parse. The
// request id is recovered leniently so the CLI is unblocked; when even that
// fails the line is reported as an error for the runner to count.
func (s *session) denyUnparseable(raw []byte) {
	requestID := recoverRequestID(raw)
	if requestID == "" {
		ev := newEvent(provider.EvError, raw)
		ev.Text = "unparseable control request"
		s.events <- ev
		return
	}
	s.writeControlResponse(requestID, map[string]any{
		"behavior": "deny",
		"message":  "unsupported control request",
	})
	ev := newEvent(provider.EvSystem, raw)
	ev.Text = "unparseable control request"
	s.events <- ev
}

// recoverRequestID pulls request_id out of a line that failed strict decoding.
func recoverRequestID(raw []byte) string {
	var loose map[string]any
	if err := json.Unmarshal(raw, &loose); err != nil {
		return ""
	}
	id, _ := loose["request_id"].(string)
	return id
}

func (s *session) writeControlResponse(requestID string, response map[string]any) {
	payload := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   response,
		},
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if err := s.writeLine(line); err != nil {
		ev := newEvent(provider.EvError, nil)
		ev.Text = "write control response: " + err.Error()
		s.events <- ev
	}
}

// measure turns a usage event into running session totals. A per-turn
// usage event (from an assistant line) carries that turn's tokens alone
// and no turn number; the result line carries the session's own totals and
// replaces the running count, since it is the figure the operator is
// billed against.
func (s *session) measure(ev *provider.Event) {
	if ev.Kind != provider.EvUsage {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.Turns > 0 || ev.CostUSD > 0 {
		s.meter.turns = ev.Turns
		s.meter.inTok = ev.InputTok
		s.meter.outTok = ev.OutputTok
		s.meter.costUSD = ev.CostUSD
		s.meter.reported = true
		return
	}
	s.meter.turns++
	s.meter.inTok += ev.InputTok
	s.meter.outTok += ev.OutputTok
	ev.Turns = s.meter.turns
	ev.InputTok = s.meter.inTok
	ev.OutputTok = s.meter.outTok
	ev.CostUSD = s.meter.costUSD
}

// absorb records the parts of an event that belong to the terminal Result.
func (s *session) absorb(ev provider.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ev.Kind == provider.EvSystem || ev.Kind == provider.EvFinal {
		var probe struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(ev.Raw, &probe); err == nil && probe.SessionID != "" {
			s.handle = probe.SessionID
		}
	}
	if ev.Kind != provider.EvFinal {
		return
	}
	s.res.Final = ev.Final
	s.res.Text = ev.Text
	s.res.Usage.Turns = ev.Turns
	s.res.Usage.InputTok = ev.InputTok
	s.res.Usage.OutputTok = ev.OutputTok
	s.res.Usage.CostUSD = ev.CostUSD
}

func (s *session) writeUser(text string) error {
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": text,
		},
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.writeLine(line)
}

func (s *session) writeLine(line []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.stdin.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

func formatTail(tail []string) string {
	if len(tail) == 0 {
		return ""
	}
	return ": " + strings.Join(tail, " | ")
}

// tailWriter keeps the last max complete lines written to it, so a failed
// session can report why without holding the whole stderr stream.
type tailWriter struct {
	mu      sync.Mutex
	max     int
	partial []byte
	lines   []string
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		w.push(string(bytes.TrimRight(w.partial[:i], "\r")))
		w.partial = append([]byte(nil), w.partial[i+1:]...)
	}
	return len(p), nil
}

func (w *tailWriter) push(line string) {
	w.lines = append(w.lines, line)
	if len(w.lines) > w.max {
		w.lines = append([]string(nil), w.lines[len(w.lines)-w.max:]...)
	}
}

// snapshot returns the tail, including any unterminated trailing line.
func (w *tailWriter) snapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := append([]string(nil), w.lines...)
	if len(w.partial) > 0 {
		out = append(out, string(w.partial))
	}
	if len(out) > w.max {
		out = out[len(out)-w.max:]
	}
	return out
}
