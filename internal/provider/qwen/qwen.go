// Package qwen adapts the Qwen Code CLI (`qwen --output-format stream-json`
// with the prompt on stdin) to the provider.Session contract: it starts the
// process, answers the CLI's PreToolUse hook from a PermissionPolicy, and
// turns each output line into a provider.Event.
//
// Qwen Code is a Gemini CLI fork whose headless mode is modelled on Claude
// Code's, so most of this file is the Claude adapter with the differences
// the wire-format capture found (docs/research/09-qwen-wire-formats.md).
// Three of those differences shape the code:
//
//   - There is no control_request channel in headless mode. The permission
//     mediator is a PreToolUse hook, which Sirdar serves over loopback HTTP
//     from inside this process.
//   - --json-schema and --input-format stream-json are mutually exclusive,
//     so a session takes exactly one user message. Send always fails and
//     the runner carries the schema retry into a --resume of the handle.
//   - The result line reports no cost, so a USD budget cannot bite.
package qwen

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

const (
	defaultBinary   = "qwen"
	maxLineBytes    = 16 << 20 // a single stream-json line can carry a big tool result
	eventBuffer     = 64
	stderrTailLines = 50
	doctorTimeout   = 10 * time.Second
	interruptGrace  = 10 * time.Second

	// hookTimeoutSeconds is how long Qwen Code waits for the permission
	// hook before giving up on it. The decision is a pure function of the
	// policy and returns immediately; the allowance is for a runner that
	// is slow to drain the event channel.
	hookTimeoutSeconds = 15

	// hookPath is the endpoint the child posts PreToolUse events to.
	hookPath = "/decide"

	// noMCPServer is the name passed to --allowed-mcp-server-names when a
	// session must see no MCP servers at all. Qwen Code has no
	// --strict-mcp-config: --mcp-config merges with the operator's own
	// settings, and the allow-list by name is the only thing that
	// narrows the set. A name no server has yields an empty set.
	noMCPServer = "__sirdar_none__"

	// shellTool is the one tool whose headless deny Sirdar ever lifts,
	// and only for a workspace that named permissions.bash patterns. The
	// CLI cannot express "shell, but only these commands" — a rule
	// written against the canonical name allows every command, whatever
	// specifier it carries — so the allow-list is enforced by the hook.
	shellTool = "run_shell_command"
)

// deniedTools are pinned into the session's settings file so writes stay
// refused even if the permission hook never answers. Qwen Code's own
// headless deny list already covers them under --approval-mode default;
// this is the belt-and-braces half, the way the Claude adapter passes
// --disallowedTools alongside its permission policy.
var deniedTools = []string{"write_file", "edit", "notebook_edit"}

// Endpoint is the model endpoint a workspace configured under `qwen:`. It
// is optional: with no fields set the session runs against whatever login
// the operator's own `qwen` binary already has.
//
// APIKey is the resolved secret, not a credential reference. It is passed
// to the child process in its environment and is never logged, never
// written to a run directory, and never reported by Doctor.
type Endpoint struct {
	Binary  string
	Model   string
	BaseURL string
	APIKey  string
}

// complete reports whether the endpoint names everything Qwen Code needs
// to select OpenAI-compatible auth from the environment. Its own
// inference wants the key, the base URL and a model together; short of
// that the CLI falls back to the operator's login.
func (e Endpoint) complete() bool {
	return e.APIKey != "" && e.BaseURL != "" && e.Model != ""
}

// Provider starts Qwen Code sessions.
type Provider struct{ endpoint Endpoint }

// New returns the Qwen Code provider with no endpoint configuration: the
// child runs against the login the operator's `qwen` binary already holds.
func New() provider.Provider { return &Provider{} }

// NewEndpoint returns the Qwen Code provider pointed at a configured
// OpenAI-compatible endpoint.
func NewEndpoint(e Endpoint) provider.Provider { return &Provider{endpoint: e} }

// Name identifies this provider in config and run records.
func (p *Provider) Name() string { return "qwen" }

// qwenEnvKeys are the variables Qwen Code reads to decide which backend
// and which credentials a session uses. They are stripped from the child
// environment unconditionally, so a session's endpoint is whatever the
// workspace configured and never whatever happens to be exported in the
// shell that launched Sirdar.
var qwenEnvKeys = []string{
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"OPENAI_MODEL",
	"QWEN_MODEL",
	"QWEN_OAUTH",
	"QWEN_CODE_SYSTEM_SETTINGS_PATH",
	"QWEN_CODE_SYSTEM_DEFAULTS_PATH",
}

// args builds the command line. The prompt is not on it: it goes in on
// stdin, which has no length limit and is the form --json-schema accepts
// alongside a positional argument.
func args(spec provider.SessionSpec, ep Endpoint, mcpNames []string) []string {
	out := []string{
		"--output-format", "stream-json",
		"--approval-mode", "default",
	}
	// The runner always supplies a schema; an empty one is a caller bug,
	// so it is passed through rather than silently dropped.
	out = append(out, "--json-schema", compactJSON(spec.OutputSchema))

	model := spec.Model
	if model == "" {
		model = ep.Model
	}
	if model != "" {
		out = append(out, "--model", model)
	}
	if ep.complete() {
		out = append(out, "--auth-type", "openai")
	}
	if spec.Budget.MaxTurns > 0 {
		// structured_output is exempt from --max-tool-calls but not from
		// --max-session-turns, so the terminal call needs a turn of its
		// own or a run that has its answer is killed reporting exit 53.
		out = append(out, "--max-session-turns", strconv.Itoa(spec.Budget.MaxTurns+1))
	}
	if spec.Budget.MaxMinutes > 0 {
		out = append(out, "--max-wall-time", strconv.Itoa(spec.Budget.MaxMinutes)+"m")
	}
	if spec.Resume != "" {
		out = append(out, "--resume", spec.Resume)
	}
	if spec.MCPConfig != "" {
		out = append(out, "--mcp-config", spec.MCPConfig)
	}
	// --mcp-config merges with the operator's own settings rather than
	// replacing them, so naming the workspace file is not on its own the
	// restriction mcp.workspaceOnly asks for. Allow-listing the servers
	// that file declares — and nothing else — is.
	for _, name := range mcpNames {
		out = append(out, "--allowed-mcp-server-names", name)
	}
	// A workspace that named no bash patterns gets no shell at all:
	// Qwen Code's headless deny refuses run_shell_command outright, and
	// leaving that in place is the only fail-closed guarantee available,
	// since the permission hook fails open when it cannot be reached.
	if spec.Policy != nil && len(spec.Policy.BashAllow) > 0 {
		out = append(out, "--allowed-tools", shellTool)
	}
	return out
}

// mcpServerNames returns the names the session may load, and reports
// whether the allow-list should be passed at all. With MCPStrict set and
// no config file named, the answer is a sentinel no server matches, which
// is how a session is started with no MCP servers.
func mcpServerNames(spec provider.SessionSpec) ([]string, error) {
	if spec.MCPConfig == "" {
		if spec.MCPStrict {
			return []string{noMCPServer}, nil
		}
		return nil, nil
	}
	b, err := os.ReadFile(spec.MCPConfig)
	if err != nil {
		return nil, fmt.Errorf("read mcp config: %w", err)
	}
	var f struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", spec.MCPConfig, err)
	}
	if !spec.MCPStrict {
		return nil, nil
	}
	if len(f.MCPServers) == 0 {
		return []string{noMCPServer}, nil
	}
	names := make([]string, 0, len(f.MCPServers))
	for name := range f.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// childEnv returns the environment for the child process: the caller's,
// with every variable Qwen Code reads for its backend removed, and the
// configured endpoint put back in their place.
func childEnv(spec provider.SessionSpec, ep Endpoint, settingsPath string) []string {
	base := spec.Env
	if len(base) == 0 {
		base = os.Environ()
	}
	out := make([]string, 0, len(base)+len(qwenEnvKeys))
	for _, e := range base {
		if isQwenEnv(e) {
			continue
		}
		out = append(out, e)
	}
	if ep.APIKey != "" {
		out = append(out, "OPENAI_API_KEY="+ep.APIKey)
	}
	if ep.BaseURL != "" {
		out = append(out, "OPENAI_BASE_URL="+ep.BaseURL)
	}
	model := ep.Model
	if model == "" {
		model = spec.Model
	}
	if model != "" {
		out = append(out, "OPENAI_MODEL="+model)
	}
	if settingsPath != "" {
		out = append(out, "QWEN_CODE_SYSTEM_SETTINGS_PATH="+settingsPath)
	}
	return out
}

func isQwenEnv(entry string) bool {
	name, _, ok := strings.Cut(entry, "=")
	if !ok {
		return false
	}
	for _, k := range qwenEnvKeys {
		if name == k {
			return true
		}
	}
	return false
}

func compactJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// writeSettings writes the private settings file the session runs under
// and returns its path. It carries two things: the PreToolUse hook that
// asks Sirdar about every tool call, and a deny list for the write tools.
// It is pointed at with QWEN_CODE_SYSTEM_SETTINGS_PATH, so neither the
// workspace's .qwen/settings.json nor the operator's ~/.qwen/settings.json
// is read or written.
func writeSettings(dir, hookURL string) (string, error) {
	type hook struct {
		Type    string `json:"type"`
		URL     string `json:"url"`
		Timeout int    `json:"timeout"`
		Name    string `json:"name"`
	}
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": "*",
				"hooks": []hook{{
					Type:    "http",
					URL:     hookURL,
					Timeout: hookTimeoutSeconds,
					Name:    "sirdar-policy",
				}},
			}},
		},
		"permissions": map[string]any{"deny": deniedTools},
	}
	b, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Start launches the CLI, writes the prompt to its stdin, and closes it.
func (p *Provider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	binary := spec.Binary
	if binary == "" {
		binary = p.endpoint.Binary
	}
	if binary == "" {
		binary = defaultBinary
	}
	policy := spec.Policy
	if policy == nil {
		policy = &provider.PermissionPolicy{}
	}
	mcpNames, err := mcpServerNames(spec)
	if err != nil {
		return nil, fmt.Errorf("qwen mcp config: %w", err)
	}

	// The permission hook listens before the child starts, so the first
	// tool call cannot race the listener.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("qwen permission hook: %w", err)
	}
	dir, err := os.MkdirTemp("", "sirdar-qwen-")
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("qwen settings: %w", err)
	}
	hookURL := "http://" + listener.Addr().String() + hookPath
	settingsPath, err := writeSettings(dir, hookURL)
	if err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("qwen settings: %w", err)
	}

	// runCtx is the one cancellation path: Cancel() cancels it, and so
	// does the caller's ctx. cmd.Cancel turns either into SIGINT, and
	// WaitDelay escalates to SIGKILL if the process has not exited.
	runCtx, cancelRun := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, binary, args(spec, p.endpoint, mcpNames)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = interruptGrace
	cmd.Dir = spec.Cwd
	cmd.Env = childEnv(spec, p.endpoint, settingsPath)

	fail := func(err error) (provider.Session, error) {
		cancelRun()
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fail(fmt.Errorf("qwen stdin: %w", err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fail(fmt.Errorf("qwen stdout: %w", err))
	}
	tail := &tailWriter{max: stderrTailLines}
	cmd.Stderr = tail

	s := &session{
		binary:     binary,
		cmd:        cmd,
		cancelRun:  cancelRun,
		stderr:     tail,
		policy:     policy,
		settingsIn: dir,
		events:     make(chan provider.Event, eventBuffer),
		readDone:   make(chan struct{}),
		hookDone:   make(chan struct{}),
		done:       make(chan struct{}),
	}
	s.hook = &http.Server{Handler: s.hookMux(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		defer close(s.hookDone)
		_ = s.hook.Serve(listener)
	}()

	if err := cmd.Start(); err != nil {
		_ = s.hook.Close()
		<-s.hookDone
		return fail(fmt.Errorf("start %s: %w", binary, err))
	}

	// The prompt is the whole of the session's input. Qwen Code reads
	// stdin to EOF before it starts, so it is written and closed here
	// rather than left for CloseInput: a session whose stdin stayed open
	// would never begin.
	writeErr := writePrompt(stdin, spec.Prompt)
	go s.read(stdout)
	if writeErr != nil {
		// The session is never handed to the caller, so reap it here
		// rather than through Cancel/Wait.
		cancelRun()
		go func() {
			<-s.readDone
			_ = cmd.Wait()
			_ = s.hook.Close()
			<-s.hookDone
			_ = os.RemoveAll(dir)
		}()
		return nil, fmt.Errorf("qwen prompt: %w", writeErr)
	}
	return s, nil
}

func writePrompt(stdin io.WriteCloser, prompt string) error {
	if _, err := io.WriteString(stdin, prompt+"\n"); err != nil {
		_ = stdin.Close()
		return err
	}
	return stdin.Close()
}

// Doctor checks that the binary runs and reports which endpoint a session
// would use. The API key is never part of the detail.
func (p *Provider) Doctor(ctx context.Context, binary string) []provider.Check {
	if binary == "" {
		binary = p.endpoint.Binary
	}
	if binary == "" {
		binary = defaultBinary
	}

	version := provider.Check{Name: "qwen --version"}
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

	return []provider.Check{version, p.endpointCheck()}
}

// endpointCheck reports what the session will talk to. A workspace that
// configured nothing is not an error: the child then uses the login the
// operator's own qwen binary holds.
func (p *Provider) endpointCheck() provider.Check {
	c := provider.Check{Name: "qwen endpoint", OK: true}
	e := p.endpoint
	switch {
	case e.complete():
		c.Detail = e.BaseURL + " (" + e.Model + ")"
	case e.BaseURL == "" && e.Model == "" && e.APIKey == "":
		c.Detail = "not configured; using the qwen CLI's own login"
	default:
		c.OK = false
		c.Detail = "qwen.baseUrl, qwen.model and qwen.apiKey are needed together for an " +
			"OpenAI-compatible endpoint; " + incomplete(e) + " missing"
	}
	return c
}

// incomplete names the endpoint fields a partially configured workspace
// left out. It never names a value, only a key.
func incomplete(e Endpoint) string {
	var missing []string
	for _, f := range []struct{ key, value string }{
		{"qwen.baseUrl", e.BaseURL},
		{"qwen.model", e.Model},
		{"qwen.apiKey", e.APIKey},
	} {
		if f.value == "" {
			missing = append(missing, f.key)
		}
	}
	if len(missing) == 0 {
		return "nothing is"
	}
	return strings.Join(missing, ", ") + " is"
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

// session is one running qwen process.
type session struct {
	binary     string
	cmd        *exec.Cmd
	cancelRun  context.CancelFunc
	stderr     *tailWriter
	policy     *provider.PermissionPolicy
	hook       *http.Server
	settingsIn string

	events   chan provider.Event
	readDone chan struct{} // closed when stdout hits EOF
	hookDone chan struct{} // closed when the hook server has stopped
	done     chan struct{} // closed when the process has been reaped

	emitMu sync.RWMutex // held for reading while an event is sent
	closed bool         // set under emitMu before events is closed

	mu      sync.Mutex
	handle  string
	res     provider.Result
	waitErr error
	meter   usageMeter

	waitOnce sync.Once
}

// usageMeter accumulates what the session has spent so far. The CLI
// reports a turn's usage on the last line of each turn and the session
// total on the result line, so a run can show turns and tokens as they
// happen rather than only once it is over. There is no cost figure on the
// wire, so CostUSD stays zero and only the turn and wall-clock budgets
// bite.
type usageMeter struct {
	turns  int
	inTok  int64
	outTok int64
}

// Events returns the activity stream. The channel is buffered; the caller
// must drain it, because the reader goroutine and the permission hook both
// block once the buffer fills and Wait does not return until stdout has
// been read to EOF.
func (s *session) Events() <-chan provider.Event { return s.events }

func (s *session) Handle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handle
}

// Send always fails. Qwen Code rejects --input-format stream-json
// alongside --json-schema, so a session that was started with a schema
// takes exactly one user message and its stdin is already closed. The
// runner answers a failed Send by resuming the handle in a fresh session,
// which is how the schema retry reaches a Qwen run.
func (s *session) Send(context.Context, string) error {
	return errors.New("qwen sessions take one message: --json-schema and --input-format stream-json " +
		"cannot both be passed, so a follow-up turn has to resume the session id")
}

// CloseInput does nothing. The prompt was written and stdin closed when
// the session started, because Qwen Code reads stdin to EOF before it
// begins work.
func (s *session) CloseInput() error { return nil }

// Wait reaps the process and returns the session's Result. It must be
// called after Events has been drained. A non-zero exit is reported in
// Result.ExitErr rather than as an error, so the caller can still read the
// handle and whatever the session produced.
func (s *session) Wait() (provider.Result, error) {
	s.waitOnce.Do(func() {
		<-s.readDone
		err := s.cmd.Wait()
		// Nothing will call the hook once the process has gone.
		_ = s.hook.Close()
		<-s.hookDone
		if s.settingsIn != "" {
			_ = os.RemoveAll(s.settingsIn)
		}
		tail := s.stderr.snapshot()

		s.mu.Lock()
		s.res.Handle = s.handle
		s.res.StderrTail = tail
		if err != nil {
			var exitErr *exec.ExitError
			switch {
			case errors.As(err, &exitErr):
				s.res.ExitErr = fmt.Errorf("%s exited with code %d (%s)%s",
					s.binary, exitErr.ExitCode(), exitReason(exitErr.ExitCode()), formatTail(tail))
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				// Cancel() or the caller's context stopped the session;
				// that is a reported outcome, not a Wait failure.
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

// exitReason names the codes Qwen Code documents, so a run that died on a
// budget says so rather than reporting a bare number. A run that overran
// its turns writes no result line at all, which is why the exit code is
// the only account of it.
func exitReason(code int) string {
	switch code {
	case 1:
		return "the model answered in prose instead of calling structured_output"
	case 53:
		return "max session turns"
	case 55:
		return "wall-clock or tool-call budget"
	case 130:
		return "interrupted"
	default:
		return "see stderr"
	}
}

// Cancel stops the session through the same path as a cancelled context:
// cmd.Cancel delivers SIGINT and cmd.WaitDelay escalates to SIGKILL if the
// process is still alive after interruptGrace. It does not block; the
// outcome shows up in Wait's Result.
func (s *session) Cancel() { s.cancelRun() }

// read consumes stdout until EOF, emitting one or more events per line.
func (s *session) read(stdout io.Reader) {
	defer s.closeEvents()
	defer close(s.readDone)

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		line := append([]byte(nil), raw...)
		for _, ev := range decode(line) {
			s.measure(&ev)
			s.absorb(ev)
			s.emit(ev)
		}
	}
	if err := sc.Err(); err != nil {
		ev := newEvent(provider.EvError, nil)
		ev.Text = "read qwen output: " + err.Error()
		s.emit(ev)
	}
}

// emit publishes an event unless the stream has already been closed. Both
// the stdout reader and the permission hook's HTTP handlers reach the
// channel, so the close has to wait for whoever is mid-send.
func (s *session) emit(ev provider.Event) {
	s.emitMu.RLock()
	defer s.emitMu.RUnlock()
	if s.closed {
		return
	}
	s.events <- ev
}

func (s *session) closeEvents() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.events)
}

// hookMux serves the PreToolUse hook Qwen Code posts every tool call to.
// It is the session's permission mediator: Qwen Code emits no
// control_request in headless mode, so this is the only place a host
// decision reaches a tool call.
func (s *session) hookMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(hookPath, s.decide)
	return mux
}

// hookRequest is the PreToolUse payload. Every other field of the event —
// session id, transcript path, cwd, timestamp — is ignored.
type hookRequest struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
}

func (s *session) decide(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxLineBytes))
	if err != nil {
		writeDecision(w, false, "Sirdar policy: the tool call could not be read")
		return
	}
	var req hookRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ToolName == "" {
		// Qwen Code is waiting for an answer, so a payload that cannot be
		// read is still answered — with a refusal, since nothing about it
		// has been judged.
		ev := newEvent(provider.EvError, body)
		ev.Text = "unparseable permission hook request"
		s.emit(ev)
		writeDecision(w, false, "Sirdar policy: the tool call could not be read")
		return
	}

	decision := s.policy.Decide(policyName(req.ToolName), req.ToolInput)

	// The event goes out before the answer does. The child does not
	// continue until it has the answer, so emitting first is what keeps a
	// permission event ahead of the tool_use line it belongs to; the
	// other order lets the next stdout line overtake it.
	ev := newEvent(provider.EvPermission, body)
	ev.Tool = req.ToolName
	ev.Input = req.ToolInput
	ev.Decision = "deny"
	if decision.Allow {
		ev.Decision = "allow"
	}
	ev.Text = decision.Message
	s.emit(ev)

	writeDecision(w, decision.Allow, decisionReason(decision))
}

// decisionReason is what the model is told. Qwen Code requires a reason on
// every decision, and an allowed call has none to give.
func decisionReason(d provider.Decision) string {
	if d.Message != "" {
		return d.Message
	}
	if d.Allow {
		return "Sirdar policy: allowed"
	}
	return "Sirdar policy: not permitted"
}

func writeDecision(w http.ResponseWriter, allow bool, reason string) {
	verdict := "deny"
	if allow {
		verdict = "allow"
	}
	payload := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       verdict,
			"permissionDecisionReason": reason,
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "encode decision", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// measure turns a usage event into running session totals. A per-turn
// usage event carries that turn's tokens alone and no turn number; the
// result line carries the session's own totals and replaces the running
// count, since it is the figure the run is judged against.
func (s *session) measure(ev *provider.Event) {
	if ev.Kind != provider.EvUsage {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if isResultLine(ev.Raw) {
		s.meter.turns = ev.Turns
		s.meter.inTok = ev.InputTok
		s.meter.outTok = ev.OutputTok
		return
	}
	if isRoundTrip(ev.Raw) {
		s.meter.turns++
	}
	s.meter.inTok += ev.InputTok
	s.meter.outTok += ev.OutputTok
	ev.Turns = s.meter.turns
	ev.InputTok = s.meter.inTok
	ev.OutputTok = s.meter.outTok
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
