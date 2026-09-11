// Package codex adapts the OpenAI Codex CLI to Sirdar's provider contract by
// driving `codex app-server`, its line-delimited JSON-RPC 2.0 stdio protocol.
//
// A triage session runs with sandbox "read-only", so the sandbox itself
// refuses writes; a fix session runs with "workspace-write", which confines
// it to the thread's cwd. Both run with approvalPolicy "untrusted", so every
// action Codex would otherwise take unattended is put to Sirdar first. Each
// request is answered from SessionSpec.Policy — the same PermissionPolicy
// Claude Code's permission prompts go through — and every answer is surfaced
// as an EvPermission event, so an operator can see what the agent asked for
// and what it was told.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
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

	// sandboxMode is Codex's own confinement for a triage session: the
	// filesystem is read-only whatever the permission policy says.
	sandboxMode = "read-only"

	// fixSandboxMode is what a fix session runs in instead. A fix has to
	// write the files it is fixing, and workspace-write confines Codex's
	// own writes to the thread's cwd — the workspace root. It is the
	// outer wall; which file inside it may be written is still decided by
	// the policy, one approval at a time (see decideFileChange).
	fixSandboxMode = "workspace-write"

	// approvalPolicy decides which actions Codex asks about instead of
	// deciding for itself, and it is the only reason Sirdar's permission
	// policy reaches a Codex session at all.
	//
	// It used to be "never", which does not mean "nothing is gated": for
	// MCP tool calls it means *refused*. A turn against codex-cli 0.154.0
	// with a workspace MCP server ended in
	//
	//	"error":{"message":"MCP tool call requires approval, but approval
	//	policy is never"}
	//
	// so the servers a workspace declares were being started and then
	// never usable. Under "untrusted" the same call arrives as an
	// approval request — an mcpServer/elicitation/request carrying
	// _meta.codex_approval_kind "mcp_tool_call" — which is what lets
	// permissions.mcp judge it. See docs/research/06-wire-formats.md for
	// both transcripts.
	//
	// The cost is that shell commands are put to Sirdar too, and are
	// judged by permissions.bash. That is what Claude Code sessions have
	// always done; a Codex session was the odd one out.
	approvalPolicy = "untrusted"

	// mcpApprovalKind marks the elicitations that are really MCP tool-call
	// approvals rather than a server asking the user a question.
	mcpApprovalKind = "mcp_tool_call"

	// readOnlyReason is what the agent is told when it asks for a write
	// outside a fix run.
	readOnlyReason = "Sirdar policy: triage runs are read-only"
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

	// mcp.workspaceOnly reaches this provider as SessionSpec.MCPStrict, the
	// field Claude Code turns into --strict-mcp-config. Codex has no such
	// flag: its MCP servers come from config.toml in CODEX_HOME, and both
	// `-c mcp_servers=...` and thread/start's `config` object merge into
	// that table rather than replacing it, so neither can subtract the
	// operator's own servers. A generated home is what can.
	var home *scratchHome
	var warnings []string
	if spec.MCPStrict {
		h, w, err := newScratchHome(specRoot(spec.Cwd, spec.MCPConfig), sessionEnv(spec))
		if err != nil {
			return nil, fmt.Errorf("codex: workspace mcp config: %w", err)
		}
		home, warnings = h, w
	}
	started := false
	defer func() {
		if !started {
			home.remove()
		}
	}()

	cmd := exec.Command(binary, "app-server")
	cmd.Dir = spec.Cwd
	cmd.Env = childEnv(spec, home)

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

	policy := spec.Policy
	if policy == nil {
		policy = &provider.PermissionPolicy{Root: spec.Cwd}
	}
	s := &session{
		cmd:             cmd,
		spec:            spec,
		policy:          policy,
		home:            home,
		pendingMCP:      map[string][]string{},
		pendingChanges:  map[string][]string{},
		approvedChanges: map[string][]string{},
		poisonedChanges: map[string]bool{},
		events:          make(chan provider.Event),
		turnDone:        make(chan struct{}),
		exited:          make(chan struct{}),
		stdoutDone:      make(chan struct{}),
		stderrDone:      make(chan struct{}),
		stopped:         make(chan struct{}),
		pumpDone:        make(chan struct{}),
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

	// The .mcp.json warnings and the resulting server list are reported
	// before the first turn, so an operator reading the events log can see
	// which servers the thread was given and which entries were skipped.
	for _, w := range warnings {
		s.emit(provider.Event{Kind: provider.EvSystem, Text: "mcp: " + w})
	}
	if home != nil {
		s.emit(provider.Event{Kind: provider.EvSystem, Text: "mcp.workspaceOnly: " + serverSummary(home.servers)})
	}

	if err := s.handshake(ctx); err != nil {
		s.abort()
		return nil, err
	}
	started = true
	return s, nil
}

// sessionEnv is the environment the session is configured from: the
// runner's, or this process's when the caller passed none.
func sessionEnv(spec provider.SessionSpec) []string {
	if len(spec.Env) > 0 {
		return spec.Env
	}
	return os.Environ()
}

// childEnv is the environment the app-server is started with. Without a
// generated home it is the caller's, unchanged — nil means the child
// inherits this process's environment, which is what a spec with no Env
// asks for.
func childEnv(spec provider.SessionSpec, home *scratchHome) []string {
	if home == nil {
		if len(spec.Env) == 0 {
			return nil
		}
		return spec.Env
	}
	return home.apply(sessionEnv(spec))
}

// serverSummary names the MCP servers a workspace-only thread will see.
func serverSummary(names []string) string {
	if len(names) == 0 {
		return "no workspace .mcp.json servers, so the thread has no MCP tools"
	}
	return "the thread sees " + strings.Join(names, ", ") + " and no other MCP server"
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

	// A triage thread runs in Codex's read-only sandbox; a fix thread has
	// to write the files it is fixing, so it gets workspace-write, which
	// confines it to the cwd the thread was started in. approvalPolicy is
	// "untrusted" either way, so Codex asks before it runs a command, calls
	// an MCP tool or writes a file, and Sirdar's policy answers. In a fix
	// thread that approval is the gate that decides which file inside the
	// workspace may be written; in a triage thread it is what refuses the
	// write the sandbox would have refused anyway.
	sandbox := sandboxMode
	if s.spec.Mode.IsFix() {
		sandbox = fixSandboxMode
	}
	method := "thread/start"
	params := map[string]any{
		"sandbox":        sandbox,
		"approvalPolicy": approvalPolicy,
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

	// policy decides every approval request. It is never nil: a spec that
	// names none gets an empty policy, which allows the read-only tool
	// names, denies write-shaped MCP tools by name, and allows no shell
	// command at all — the same reading an unconfigured Claude session
	// gets.
	policy *provider.PermissionPolicy
	// home is the generated CODEX_HOME this session runs against, nil
	// when mcp.workspaceOnly is off. It is removed once the process has
	// exited, not before: the app-server reads it for the life of the
	// session.
	home *scratchHome

	events chan provider.Event
	queue  eventQueue

	mu       sync.Mutex
	threadID string
	turnID   string
	// pendingMCP is, per server, the FIFO of tool names whose item/started
	// has arrived but whose approval has not yet been decided. An MCP
	// tool-call approval arrives as an elicitation, which names the server
	// in a field and the tool only inside a sentence meant for a person;
	// the item/started that precedes it carries both as data. A FIFO
	// rather than one name per server because a turn can have several
	// calls to the same server open at once — two item/started before
	// either is approved — and a single remembered name would have the
	// second call's start overwrite the first's before its elicitation
	// arrived, judging call one's approval under call two's name.
	pendingMCP map[string][]string
	// pendingChanges is, per fileChange item id, the paths that item's
	// patch would touch. A file-change approval carries only the item's
	// id (FileChangeRequestApprovalParams is threadId, turnId, itemId,
	// startedAtMs and an optional reason and grantRoot — no paths), and
	// the item/started that precedes it is what carries changes[].path.
	// Without the paths there is nothing for the fix policy to confine,
	// so the entry recorded here is what makes the approval decidable.
	pendingChanges map[string][]string
	// approvedChanges is, per fileChange item id Sirdar has already
	// accepted, the destinations that acceptance was judged against. It is
	// what a later patchUpdated is compared to: Codex applies whatever the
	// patch looks like when the item completes, not what it looked like at
	// approval time, so an item is only as trustworthy as the assumption
	// that its destinations never move again after the accept.
	approvedChanges map[string][]string
	// poisonedChanges marks a fileChange item whose patchUpdated changed its
	// destinations after Sirdar had already approved a different set. Once
	// poisoned, every requestApproval naming that item is declined outright
	// — the item's own bookkeeping can no longer be trusted to say what it
	// will actually write.
	poisonedChanges map[string]bool
	finalText       string
	usage           struct{ in, out int64 }
	turns           int
	turnDone        chan struct{}
	turnClosed      bool
	turnErr         error

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
	// Cancel does not reap the process, so the generated home is removed
	// on a bounded wait for it to go. A Cancel that is followed by Wait —
	// what the runner does — removes it there instead; whichever gets
	// there first wins, and the other is a no-op.
	if s.home != nil {
		go func() {
			if waitFor(s.exited, shutdownGrace) {
				s.home.remove()
			}
		}()
	}
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

	// Reap the child first: Codex holds the generated home open — and may
	// still rewrite its auth.json — until it has gone. Only then is the
	// home taken down. That is why this is not the single call to shutdown
	// it used to be.
	//
	// What the login write-back has to say goes in the stderr tail, not on
	// the event stream: a turn's completion has already closed the stream
	// by the time Wait runs, so an event emitted here would be pushed to a
	// queue nobody can read. The tail is carried into the run's state
	// whatever the outcome, which is where an operator would go looking
	// for a refresh that could not be returned.
	s.reap()
	for _, w := range s.home.remove() {
		s.tail.add("sirdar: " + w)
	}
	s.closeStream()

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

// shutdown reaps the app-server and closes the event stream.
func (s *session) shutdown() {
	s.reap()
	s.closeStream()
}

// reap closes stdin and waits for the process, killing it after the grace
// period and recording the failure if it will not go even then.
func (s *session) reap() {
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
	Changes   []fileChange    `json:"changes"`
	Questions json.RawMessage `json:"questions"`
}

// fileChange is one entry of a fileChange item's patch. path is where the
// entry already lives; kind.move_path, when the app-server's FileChange enum
// sends an "update" with a rename, is the second location the patch writes
// to. Both are destinations the fix policy has to judge — a change whose own
// path is innocuous can still relocate the file to somewhere path never
// names, such as .git/hooks.
type fileChange struct {
	Path string `json:"path"`
	Kind struct {
		Type     string `json:"type"`
		MovePath string `json:"move_path"`
	} `json:"kind"`
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
			// An MCP call announces itself here, with its server and tool
			// as separate fields, before Codex asks whether it may run.
			// Remembering it is what lets the approval be decided under
			// the tool's real name. item/completed also drops it: a call
			// Codex auto-approves raises no elicitation to pop it off the
			// FIFO, and a stale entry left behind would wrongly name a
			// later call's approval.
			if it.Type == "mcpToolCall" && it.Server != "" && it.Tool != "" {
				s.mu.Lock()
				if kind == provider.EvToolStarted {
					s.pendingMCP[it.Server] = append(s.pendingMCP[it.Server], it.Tool)
				} else {
					s.dropPending(it.Server, it.Tool)
				}
				s.mu.Unlock()
			}
			// A file change announces its paths here, the same way an MCP
			// call announces its tool, and its approval names only the
			// item id. The entry is dropped once the item completes, so a
			// later approval cannot be decided against a finished patch.
			if it.Type == "fileChange" && it.ID != "" {
				s.mu.Lock()
				if kind == provider.EvToolStarted {
					s.pendingChanges[it.ID] = changePaths(it.Changes)
				} else {
					delete(s.pendingChanges, it.ID)
				}
				s.mu.Unlock()
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

	case "item/fileChange/patchUpdated":
		// Codex revises a patch before it asks about it, and the revision
		// can name files the item/started did not. The approval must be
		// judged against what would actually be written, so the tracked
		// paths are replaced by the ones this notification carries.
		//
		// But a revision can also arrive *after* Sirdar has already
		// accepted the item — Codex applies whatever the patch looks like
		// when the item completes, not what it looked like at approval
		// time. If the destinations changed since the accept, the accept
		// no longer means anything: the item is poisoned, and any further
		// approval naming it is declined regardless of what it asks for.
		var payload struct {
			ItemID  string       `json:"itemId"`
			Changes []fileChange `json:"changes"`
		}
		if err := json.Unmarshal(params, &payload); err != nil || payload.ItemID == "" {
			return
		}
		newPaths := changePaths(payload.Changes)
		s.mu.Lock()
		approved, wasApproved := s.approvedChanges[payload.ItemID]
		if wasApproved && !samePaths(approved, newPaths) {
			s.poisonedChanges[payload.ItemID] = true
			s.mu.Unlock()
			s.emit(provider.Event{
				Kind: provider.EvError,
				Text: fmt.Sprintf("codex: fileChange %s changed after it was approved; declining any further approval for it", payload.ItemID),
				Raw:  raw,
			})
			return
		}
		s.pendingChanges[payload.ItemID] = newPaths
		s.mu.Unlock()

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
	// A fileChange item's bookkeeping belongs to the turn it was raised in:
	// item ids do not carry across turns, and a straggler an item/completed
	// never arrived for (the turn ended some other way) should not linger
	// and be judged against a future turn's approval.
	s.pendingChanges = map[string][]string{}
	s.approvedChanges = map[string][]string{}
	s.poisonedChanges = map[string]bool{}
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

// onRequest answers the server-to-client requests Codex can raise. Every
// approval is decided by the session's PermissionPolicy and reported as an
// EvPermission event, whichever way it went.
func (s *session) onRequest(id json.RawMessage, method string, params json.RawMessage) {
	raw := envelope(id, method, params)

	switch method {
	case "item/commandExecution/requestApproval":
		s.decideCommand(id, params, raw)

	case "item/fileChange/requestApproval":
		s.decideFileChange(id, params, raw)

	case "item/permissions/requestApproval":
		// A request to widen the sandbox: more filesystem, or network.
		// The answer is a granted profile rather than a decision, and an
		// empty one grants nothing — replying {"decision":"decline"} here
		// was not a shape this request has.
		_ = s.conn.reply(id, map[string]any{"permissions": map[string]any{}, "scope": "turn"})
		s.denied(approvalTool(method), params, raw, readOnlyReason)

	case "item/tool/requestUserInput":
		_ = s.conn.reply(id, map[string]any{"answers": map[string]any{}})
		s.emit(provider.Event{Kind: provider.EvQuestion, Text: "codex asked for user input", Input: params, Raw: raw})

	case "mcpServer/elicitation/request":
		s.decideElicitation(id, params, raw)

	default:
		if strings.HasSuffix(method, "/requestApproval") {
			// An approval-shaped request Sirdar does not recognise —
			// a future app-server version, most likely. Replying {}
			// to a method this package has never seen would answer
			// with whatever shape happens to look like an accept to
			// that version; only a refusal is safe to guess at, and
			// it is worth an EvError rather than the quiet EvSystem
			// line an ordinary unknown notification gets, since a
			// human should know an approval request went unhandled.
			_ = s.conn.reply(id, map[string]string{"action": "decline"})
			s.emit(provider.Event{Kind: provider.EvError, Text: "codex: unrecognised approval request " + method, Raw: raw})
			return
		}
		_ = s.conn.reply(id, map[string]any{})
		s.emit(provider.Event{Kind: provider.EvSystem, Text: method, Raw: raw})
	}
}

// decideCommand puts a shell command to permissions.bash, the same
// allow-list a Claude session's Bash calls go through.
func (s *session) decideCommand(id, params, raw json.RawMessage) {
	var req struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(params, &req)

	input, _ := json.Marshal(map[string]string{"command": req.Command})
	d := s.policy.Decide("Bash", input)
	if d.Allow {
		_ = s.conn.reply(id, map[string]string{"decision": "accept"})
		s.emit(provider.Event{
			Kind: provider.EvPermission, Decision: "allow",
			Tool: "commandExecution", Input: params, Text: req.Command, Raw: raw,
		})
		return
	}
	// "decline" rather than "cancel": the agent is told no and carries on
	// with the turn, which is how it learns to reach for something the
	// allow-list covers instead of dying on the first refusal.
	_ = s.conn.reply(id, map[string]string{"decision": "decline"})
	s.denied("commandExecution", params, raw, d.Message)
}

// fileChangeTool is the tool name a file-change approval is decided under.
// Codex's own name for the operation is the item type, which no permission
// rule is written against; "Edit" is the name the same write carries on the
// Claude path and in PermissionPolicy's fixAllowed set, so deciding under it
// is what puts a Codex patch through exactly the rules a Claude fix goes
// through.
const fileChangeTool = "Edit"

// decideFileChange answers item/fileChange/requestApproval.
//
// Outside a fix run there is nothing to weigh: no permission setting makes a
// triage session a writer, and the read-only sandbox would have refused the
// write anyway. Inside one, the patch is the point of the run, and the
// question is only which files it may touch — so each path the item would
// write goes through the same policy that judges a Claude fix's Edit calls,
// which resolves it through symlinks, refuses anything outside the workspace
// root and refuses .git/, .sirdar/ and the repository's hooks directory.
//
// The paths come from the fileChange item, not from the request: the request
// carries only the item's id (see pendingChanges). An approval whose paths
// are not known is declined rather than guessed at — an accept would hand
// Codex a patch nobody checked the destination of.
func (s *session) decideFileChange(id, params, raw json.RawMessage) {
	tool := approvalTool("item/fileChange/requestApproval")
	if !s.policy.IsFix() {
		_ = s.conn.reply(id, map[string]string{"decision": "decline"})
		s.denied(tool, params, raw, readOnlyReason)
		return
	}

	var req struct {
		ItemID    string `json:"itemId"`
		GrantRoot string `json:"grantRoot"`
	}
	_ = json.Unmarshal(params, &req)
	if strings.TrimSpace(req.GrantRoot) != "" {
		// Not this patch but a standing permission to write under a root
		// for the rest of the session, which would take the decision away
		// from the policy for every change after it. Sirdar answers one
		// patch at a time.
		_ = s.conn.reply(id, map[string]string{"decision": "decline"})
		s.denied(tool, params, raw, "Sirdar policy: a fix decides one file change at a time, "+
			"so writes are not granted for a whole root")
		return
	}

	s.mu.Lock()
	poisoned := s.poisonedChanges[req.ItemID]
	paths := s.pendingChanges[req.ItemID]
	s.mu.Unlock()
	if poisoned {
		// patchUpdated changed this item's destinations after Sirdar had
		// already approved a different set (see onNotify). Nothing this
		// request asks for is trusted any more.
		_ = s.conn.reply(id, map[string]string{"decision": "decline"})
		s.denied(tool, params, raw, "Sirdar policy: this file change's patch changed after it was "+
			"already approved, so it is declined")
		return
	}
	if len(paths) == 0 {
		_ = s.conn.reply(id, map[string]string{"decision": "decline"})
		s.denied(tool, params, raw, "Sirdar policy: the file change named no path, "+
			"so where it would write cannot be checked")
		return
	}

	// Every path has to pass: a patch is applied whole, so one file the
	// policy refuses refuses the patch.
	for _, path := range paths {
		input, _ := json.Marshal(map[string]string{"file_path": path})
		if d := s.policy.Decide(fileChangeTool, input); !d.Allow {
			_ = s.conn.reply(id, map[string]string{"decision": "decline"})
			s.denied(tool, params, raw, d.Message)
			return
		}
	}
	s.mu.Lock()
	s.approvedChanges[req.ItemID] = paths
	s.mu.Unlock()
	_ = s.conn.reply(id, map[string]string{"decision": "accept"})
	s.emit(provider.Event{
		Kind: provider.EvPermission, Decision: "allow",
		Tool: tool, Input: params, Text: strings.Join(paths, ", "), Raw: raw,
	})
}

// samePaths reports whether a and b name the same destinations, independent
// of order: a patch revision is only compared for what it touches, not the
// order the app-server happened to list it in.
func samePaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

// changePaths lifts every destination out of a fileChange item's changes:
// each entry's own path, plus its move_path when the change renames the
// file. Entries that name neither are dropped.
func changePaths(changes []fileChange) []string {
	var out []string
	for _, c := range changes {
		if strings.TrimSpace(c.Path) != "" {
			out = append(out, c.Path)
		}
		if strings.TrimSpace(c.Kind.MovePath) != "" {
			out = append(out, c.Kind.MovePath)
		}
	}
	return out
}

// elicitation is the subset of mcpServer/elicitation/request Sirdar reads.
// Codex carries MCP tool-call approvals on this channel: _meta marks them,
// and the tool's own name appears only inside the message written for a
// person.
type elicitation struct {
	ServerName string `json:"serverName"`
	Message    string `json:"message"`
	Meta       struct {
		ApprovalKind string `json:"codex_approval_kind"`
	} `json:"_meta"`
}

// decideElicitation answers an elicitation. An MCP tool-call approval goes
// to permissions.mcp under the tool's namespaced name, so the rules a
// workspace already wrote for Claude mean the same thing here. A genuine
// elicitation — a server asking the operator a question — is declined: an
// unattended run has nobody to ask.
func (s *session) decideElicitation(id, params, raw json.RawMessage) {
	var e elicitation
	_ = json.Unmarshal(params, &e)
	if e.Meta.ApprovalKind != mcpApprovalKind {
		_ = s.conn.reply(id, map[string]string{"action": "decline"})
		s.emit(provider.Event{Kind: provider.EvSystem, Text: "mcpServer/elicitation/request", Raw: raw})
		return
	}

	name, mismatch := s.toolInFlight(e.ServerName, e.Message)
	if name == "" {
		// Neither the item/started bookkeeping nor the message named a
		// tool. Deciding under mcp__<server>__ — the empty tool name —
		// would run it through permissions.mcp's write-verb heuristic,
		// which sees no words in an empty name and allows; with an
		// explicit allow-list, mcp__<server>__* matches it too. An
		// unresolved name is refused outright rather than let either
		// reading fail open.
		_ = s.conn.reply(id, map[string]string{"action": "decline"})
		s.denied(mcpclient.ToolName(e.ServerName, ""), params, raw, "tool name unknown")
		return
	}
	tool := mcpclient.ToolName(e.ServerName, name)
	if mismatch {
		// The message's own quoted name disagrees with the oldest
		// started-but-unapproved call Sirdar was tracking for this
		// server. The two bookkeeping paths should never disagree;
		// when they do, neither is trusted enough to decide under.
		_ = s.conn.reply(id, map[string]string{"action": "decline"})
		s.denied(tool, params, raw, "tool name mismatch")
		return
	}
	d := s.policy.Decide(tool, nil)
	if d.Allow {
		_ = s.conn.reply(id, map[string]any{"action": "accept", "content": map[string]any{}})
		s.emit(provider.Event{
			Kind: provider.EvPermission, Decision: "allow",
			Tool: tool, Input: params, Raw: raw,
		})
		return
	}
	_ = s.conn.reply(id, map[string]string{"action": "decline"})
	s.denied(tool, params, raw, d.Message)
}

// toolInFlight names the tool an MCP approval is about, and reports whether
// the message's own claim disagrees with what Sirdar was tracking. The
// message Codex writes for a person always quotes the tool's name (see
// docs/research/06-wire-formats.md), so it is preferred; the server's FIFO
// of started-but-unapproved calls is the fallback for a message that does
// not quote one, and it is also popped whenever the message does, so the
// two can be cross-checked — a disagreement is reported as mismatch=true
// rather than silently preferring one. An empty name — which
// permissions.mcp will not match — is what is left when neither source has
// one.
func (s *session) toolInFlight(server, message string) (name string, mismatch bool) {
	s.mu.Lock()
	var oldest string
	if q := s.pendingMCP[server]; len(q) > 0 {
		oldest, s.pendingMCP[server] = q[0], q[1:]
	}
	s.mu.Unlock()

	quoted := quotedToolName(message)
	switch {
	case quoted != "" && oldest != "" && quoted != oldest:
		return quoted, true
	case quoted != "":
		return quoted, false
	default:
		return oldest, false
	}
}

// dropPending removes one occurrence of tool from server's FIFO of
// started-but-unapproved calls, for a call that completed without ever
// raising an elicitation — Codex auto-approved it, so nothing popped the
// entry toolInFlight would otherwise have consumed. Must be called with
// s.mu held.
func (s *session) dropPending(server, tool string) {
	q := s.pendingMCP[server]
	for i, t := range q {
		if t == tool {
			s.pendingMCP[server] = append(q[:i], q[i+1:]...)
			return
		}
	}
}

// quotedToolName lifts the tool name out of a message such as
//
//	Allow the probe MCP server to run tool "delete_everything"?
func quotedToolName(message string) string {
	start := strings.IndexByte(message, '"')
	if start < 0 {
		return ""
	}
	rest := message[start+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// denied reports a refusal to the operator, with the policy's own reason.
func (s *session) denied(tool string, params, raw json.RawMessage, reason string) {
	if reason == "" {
		reason = readOnlyReason
	}
	s.emit(provider.Event{
		Kind:     provider.EvPermission,
		Decision: "deny",
		Tool:     tool,
		Input:    params,
		Text:     reason,
		Raw:      raw,
	})
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
// app-server starts and reports its MCP servers. With no workspace
// configuration to hand, the MCP row falls back to the workspace it can
// find from the working directory; DoctorWithConfig is the accurate one.
func (p codexProvider) Doctor(ctx context.Context, binary string) []provider.Check {
	return p.doctor(ctx, binary, nil)
}

// DoctorWithConfig is Doctor told which workspace it is reporting on, and
// under which mcp.workspaceOnly setting. internal/app calls this one, so
// the row is right from the desktop app and `sirdar serve` too, whose
// working directories have nothing to do with the workspace.
func (p codexProvider) DoctorWithConfig(ctx context.Context, binary string, cfg provider.DoctorConfig) []provider.Check {
	return p.doctor(ctx, binary, &cfg)
}

func (codexProvider) doctor(ctx context.Context, binary string, cfg *provider.DoctorConfig) []provider.Check {
	if binary == "" {
		binary = defaultBinary
	}
	checks := []provider.Check{versionCheck(ctx, binary), loginCheck(ctx, binary)}
	return append(checks, mcpCheck(ctx, binary, cfg))
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

// mcpCheck starts the app-server, initializes it and lists the MCP servers a
// thread would see under the mcp.workspaceOnly setting in force: against the
// generated home when it is on, and against the operator's own Codex config
// when it is off.
func mcpCheck(ctx context.Context, binary string, cfg *provider.DoctorConfig) provider.Check {
	const name = "codex mcp servers"

	var home *scratchHome
	root, only := doctorSetting(cfg)
	prefix := "mcp.workspaceOnly off, the operator's own servers: "
	if only {
		h, _, err := newScratchHome(root, os.Environ())
		if err != nil {
			return provider.Check{Name: name, Detail: err.Error()}
		}
		defer h.remove()
		home = h
		prefix = "mcp.workspaceOnly on, from " + filepath.Join(root, ".mcp.json") + ": "
	}

	cmd := exec.Command(binary, "app-server")
	cmd.Env = childEnv(provider.SessionSpec{}, home)
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
		return provider.Check{Name: name, OK: true, Detail: prefix + "none"}
	}
	return provider.Check{Name: name, OK: true, Detail: prefix + strings.Join(names, ", ")}
}

// workingDir is the directory Doctor was run from, "" when it cannot be
// determined (a deleted cwd), in which case no workspace is found and the
// check reports on a home with no servers.
func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// mcpServerNames reads server names out of a mcpServerStatus/list result.
// codex-cli 0.154.0 answers {"data":[{"name":...}],"nextCursor":null}; the
// older {"servers":[...]} shape and a name-keyed object are still read, so
// the row does not go blank against a different build.
//
// codex_apps, Codex's own plugin runtime, is listed whatever the config
// says — it is part of the CLI, not a configured server — so it is
// reported as it comes rather than filtered out.
func mcpServerNames(raw json.RawMessage) []string {
	var listed struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(raw, &listed); err == nil {
		entries := listed.Data
		if len(entries) == 0 {
			entries = listed.Servers
		}
		if len(entries) > 0 {
			names := make([]string, 0, len(entries))
			for _, srv := range entries {
				if srv.Name != "" {
					names = append(names, srv.Name)
				}
			}
			sort.Strings(names)
			return names
		}
	}

	var keyed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return nil
	}
	if _, ok := keyed["servers"]; ok {
		return nil
	}
	if _, ok := keyed["data"]; ok {
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
