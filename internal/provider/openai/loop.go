package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/agenttools"
	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

const (
	// DefaultMaxContextTokens is the context window assumed when the
	// workspace does not name one.
	DefaultMaxContextTokens = 128000

	// submitNoteTool is the tool the model finishes with. Its parameters
	// are the run's note schema, so structured output works on servers
	// that support no response_format at all.
	submitNoteTool = "submit_note"

	// trimThreshold is the share of the context window at which old tool
	// results start being dropped.
	trimThreshold = 0.8
	// keepToolResults is how many of the most recent tool results survive
	// a trim. The model is nearly always working from the last few.
	keepToolResults = 6
	// trimmedMarker replaces a dropped tool result.
	trimmedMarker = "[trimmed]"

	// maxToolOutputBytes caps what one tool call contributes to the
	// transcript. agenttools applies its own cap; this one covers MCP
	// servers, whose output Sirdar does not control.
	maxToolOutputBytes = 64 << 10

	// mcpStartTimeout bounds starting one MCP server and listing its
	// tools. mcpclient.Start's context bounds the handshake only, which is
	// the behaviour that lets a server outlive this timeout and serve the
	// whole run.
	mcpStartTimeout = 60 * time.Second

	doctorTimeout = 15 * time.Second

	// sessionEnded is the last event of every session. It doubles as the
	// barrier that tells the loop the caller has finished with the final
	// event and is not going to answer it; see awaitSend.
	sessionEnded = "session ended"
)

// LoopConfig configures the provider: the chat endpoint, the context
// window the loop trims against, the price table cost is computed from
// (both zero means cost is reported as 0), and whether the workspace's own
// MCP servers are connected.
type LoopConfig struct {
	Chat             Config
	MaxContextTokens int

	PriceInputPerMTok  float64
	PriceOutputPerMTok float64

	// MCPWorkspaceOnly restricts MCP to the servers declared in the
	// workspace's own .mcp.json — the only source Sirdar reads, and the
	// one the operator can see in the repository. Setting it false runs
	// the session with no MCP servers at all, on the workspace's local
	// read-only tools alone.
	MCPWorkspaceOnly bool
}

// Provider drives Sirdar's own agent loop against an OpenAI-compatible
// Chat Completions endpoint. Unlike the claude and codex providers it
// starts no agent CLI: the loop, the tool set and the permission checks
// are Sirdar's, and the endpoint only supplies the model.
type Provider struct {
	cfg LoopConfig
	hc  *http.Client
}

// NewProvider returns the openai provider.
//
// It is not called New because this package's Chat Completions client
// already owns that name; the two live together because the loop is the
// only thing that client exists for.
func NewProvider(cfg LoopConfig) provider.Provider {
	if cfg.MaxContextTokens <= 0 {
		cfg.MaxContextTokens = DefaultMaxContextTokens
	}
	return &Provider{cfg: cfg}
}

// Name identifies this provider in config and run records.
func (p *Provider) Name() string { return "openai" }

// Doctor reports whether the configured endpoint answers and which model
// runs will ask for. The binary argument is ignored: there is no CLI to
// find, which is the point of this provider.
func (p *Provider) Doctor(ctx context.Context, _ string) []provider.Check {
	base := strings.TrimSuffix(strings.TrimSpace(p.cfg.Chat.BaseURL), "/")

	endpoint := provider.Check{Name: "openai endpoint"}
	if base == "" {
		endpoint.Detail = "openai.baseUrl is not set"
	} else {
		pingCtx, cancel := context.WithTimeout(ctx, doctorTimeout)
		defer cancel()
		if err := New(p.cfg.Chat, p.hc).Ping(pingCtx); err != nil {
			endpoint.Detail = err.Error()
		} else {
			endpoint.OK = true
			endpoint.Detail = base
		}
	}

	model := provider.Check{Name: "openai model", Detail: p.cfg.Chat.Model}
	if model.Detail == "" {
		model.Detail = "openai.model is not set"
	} else {
		model.OK = true
	}
	return []provider.Check{endpoint, model}
}

// Start assembles the session's tool set and starts the loop goroutine.
func (p *Provider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	cfg := p.cfg
	if strings.TrimSpace(cfg.Chat.BaseURL) == "" {
		return nil, errors.New("openai: baseUrl is required")
	}
	if spec.Model != "" {
		cfg.Chat.Model = spec.Model
	}
	if strings.TrimSpace(cfg.Chat.Model) == "" {
		return nil, errors.New("openai: model is required")
	}
	if cfg.MaxContextTokens <= 0 {
		cfg.MaxContextTokens = DefaultMaxContextTokens
	}

	policy := spec.Policy
	if policy == nil {
		policy = &provider.PermissionPolicy{}
	}

	runCtx, cancel := context.WithCancel(ctx)
	s := &session{
		cfg:      cfg,
		spec:     spec,
		policy:   policy,
		client:   New(cfg.Chat, p.hc),
		ctx:      runCtx,
		cancel:   cancel,
		byName:   map[string]*tool{},
		servers:  map[string]mcpclient.ServerConfig{},
		mcp:      map[string]*mcpclient.Client{},
		mcpErr:   map[string]error{},
		tail:     mcpclient.NewStderrTail(mcpclient.DefaultStderrLines),
		events:   make(chan provider.Event),
		sendCh:   make(chan string, 1),
		dead:     make(chan struct{}),
		done:     make(chan struct{}),
		messages: []Message{{Role: "system", Content: System()}},
	}
	s.messages = append(s.messages, Message{Role: "user", Content: UserMessage(spec.Prompt, spec.Images)})

	for _, local := range agenttools.ReadOnlySet(agenttools.Options{Root: spec.Cwd, BashAllow: policy.BashAllow}) {
		ts := local.Spec()
		s.add(&tool{name: ts.Name, description: ts.Description, parameters: ts.Parameters, local: local})
	}

	if cfg.MCPWorkspaceOnly {
		// The session's own environment, not this process's: internal/run
		// strips the workspace's credentials out of it, and a ${VAR} in
		// .mcp.json must not be able to read one back.
		env := spec.Env
		if len(env) == 0 {
			env = os.Environ()
		}
		servers, warnings, err := mcpclient.LoadWorkspaceServersEnv(spec.Cwd, env)
		if err != nil {
			// A workspace that configured MCP servers and cannot be read
			// still gets its run: the local tools are enough for most
			// triage, and failing here would lose the session entirely.
			s.warnings = append(s.warnings, "mcp: "+err.Error())
		}
		s.warnings = append(s.warnings, warnings...)
		for _, server := range servers {
			s.servers[server.Name] = server
			s.serverOrder = append(s.serverOrder, server.Name)
		}
	}

	go s.run()
	return s, nil
}

// tool is one entry in the session's tool set: a local read-only tool, a
// tool on one of the workspace's MCP servers, or submit_note.
type tool struct {
	name        string
	description string
	parameters  json.RawMessage

	local  agenttools.Tool // nil unless local
	server string          // non-empty for an MCP tool
	remote string          // the tool's name on that server
	submit bool
}

// session is one run of the loop.
type session struct {
	cfg    LoopConfig
	spec   provider.SessionSpec
	policy *provider.PermissionPolicy
	client *Client

	ctx    context.Context
	cancel context.CancelFunc

	tools       []*tool
	byName      map[string]*tool
	servers     map[string]mcpclient.ServerConfig
	serverOrder []string
	warnings    []string

	// mcpMu guards the started servers. Servers are started on first use
	// and kept for the session; Cancel and Wait close them.
	mcpMu     sync.Mutex
	mcp       map[string]*mcpclient.Client
	mcpErr    map[string]error
	mcpClosed bool
	tail      *mcpclient.StderrTail

	// messages is the transcript. Only the loop goroutine touches it.
	messages []Message
	nudged   bool
	turns    int
	inTok    int64
	outTok   int64

	events chan provider.Event
	sendCh chan string
	dead   chan struct{}
	done   chan struct{}

	deadOnce sync.Once

	mu           sync.Mutex
	result       provider.Result
	streamClosed bool
	inputClosed  bool
}

func (s *session) add(t *tool) {
	s.tools = append(s.tools, t)
	s.byName[t.name] = t
}

// Events returns the activity stream. It is unbuffered: the loop is a
// single goroutine with nothing else to do while the caller reads, and an
// unbuffered channel is what makes the end-of-session barrier in
// awaitSend meaningful.
func (s *session) Events() <-chan provider.Event { return s.events }

// Handle returns "": this provider has no resume token. The runner falls
// back to a fresh session, which is the honest answer — the transcript
// lives in this process and nowhere else.
func (s *session) Handle() string { return "" }

// Send appends a follow-up user message and resumes the loop. The runner
// uses it for the schema-retry turn, which arrives while it is still
// draining the events of the final it is retrying.
func (s *session) Send(_ context.Context, userText string) error {
	select {
	case <-s.dead:
		return errors.New("openai: the session was cancelled")
	default:
	}
	s.mu.Lock()
	streamClosed, inputClosed := s.streamClosed, s.inputClosed
	s.mu.Unlock()
	if streamClosed {
		return errors.New("openai: the session's event stream has ended")
	}
	if inputClosed {
		return errors.New("openai: the session's input has been closed")
	}
	select {
	case s.sendCh <- userText:
		return nil
	default:
		return errors.New("openai: a follow-up message is already pending")
	}
}

// CloseInput records that no follow-up message is coming. Unlike the CLI
// providers there is no stdin holding the session open: the loop emits its
// end-of-session event and returns on its own once nothing has been sent.
// What this does is shut the door behind it, so a Send racing the runner's
// endSession is refused rather than resuming a loop the runner has already
// finished with. Calling it more than once is not an error.
func (s *session) CloseInput() error {
	s.mu.Lock()
	s.inputClosed = true
	s.mu.Unlock()
	return nil
}

// Cancel stops the loop and shuts every MCP server down. It does not
// block; the outcome shows up in Wait's Result.
func (s *session) Cancel() {
	s.deadOnce.Do(func() { close(s.dead) })
	s.cancel()
	s.closeMCP()
}

// Wait returns the session's Result once the loop has ended. Like the
// other providers, it must be called after Events has been drained: the
// loop blocks on the event channel, so a caller that waits without reading
// waits forever.
func (s *session) Wait() (provider.Result, error) {
	<-s.done
	s.closeMCP()

	s.mu.Lock()
	defer s.mu.Unlock()
	res := s.result
	res.StderrTail = s.tail.Lines()
	return res, nil
}

// run is the loop goroutine: discover tools, take turns until the session
// reaches an end state, then wait to see whether the caller answers with a
// follow-up before ending the stream.
func (s *session) run() {
	defer s.finish()

	for _, w := range s.warnings {
		if !s.emit(provider.Event{Kind: provider.EvSystem, Text: w, Raw: rawOf(map[string]string{"warning": w})}) {
			return
		}
	}
	if !s.discoverTools() {
		return
	}

	for {
		s.turnLoop()
		if !s.awaitSend() {
			return
		}
	}
}

// finish ends the event stream. Once it has run, Send has nowhere to
// deliver to and says so.
func (s *session) finish() {
	s.mu.Lock()
	s.streamClosed = true
	s.mu.Unlock()
	close(s.events)
	close(s.done)
}

// discoverTools starts each configured MCP server, lists its tools under
// the mcp__<server>__<tool> name the permission policy understands, and
// appends submit_note last. A server that will not start or will not list
// costs a warning, not the run.
func (s *session) discoverTools() bool {
	for _, name := range s.serverOrder {
		client, err := s.ensureClient(name)
		if err != nil {
			if !s.warn(fmt.Sprintf("mcp server %q: %v", name, err)) {
				return false
			}
			continue
		}
		listCtx, cancel := context.WithTimeout(s.ctx, mcpStartTimeout)
		infos, err := client.ListTools(listCtx)
		cancel()
		if err != nil {
			if !s.warn(fmt.Sprintf("mcp server %q: tools/list: %v", name, err)) {
				return false
			}
			continue
		}
		for _, info := range infos {
			s.add(&tool{
				name:        mcpclient.ToolName(name, info.Name),
				description: info.Description,
				parameters:  info.InputSchema,
				server:      name,
				remote:      info.Name,
			})
		}
	}

	schema := s.spec.OutputSchema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	s.add(&tool{
		name:        submitNoteTool,
		description: "Submit the finished note. Call this exactly once, as your last action, with the note as the arguments. This is the only output Sirdar keeps.",
		parameters:  schema,
		submit:      true,
	})
	return true
}

// turnLoop runs chat turns until the session produces a note, fails, or
// runs out of budget.
func (s *session) turnLoop() {
	for {
		if max := s.spec.Budget.MaxTurns; max > 0 && s.turns >= max {
			// The runner reads "over budget" off a usage event whose
			// turn count is past the budget, and it never sees one from
			// here: the loop stops on the turn that reaches the limit,
			// so the last usage it emitted said exactly max. Emitting
			// the turn that would have run says the same thing in the
			// vocabulary the runner has, which is the difference
			// between the run being recorded as over_budget and being
			// recorded as a plain failure.
			s.turns = max + 1
			s.emitUsage()
			s.fail(fmt.Sprintf("stopped after %d turns: the turn budget is spent and no note was submitted", max))
			return
		}

		resp, err := s.client.Chat(s.ctx, Request{
			Messages:   s.messages,
			Tools:      s.toolSpecs(),
			ToolChoice: "auto",
		})
		if err != nil {
			if s.ctx.Err() != nil {
				return // cancelled: the caller already knows why
			}
			var limited *RateLimitError
			if errors.As(err, &limited) {
				// The endpoint is rate limiting us and the one retry did
				// not clear it. That is a blocked run, not a failed one:
				// the runner pauses its queue until ResetsAt and the
				// ticket can be picked up again afterwards.
				s.rateLimited(limited)
				return
			}
			s.fail(err.Error())
			return
		}

		s.turns++
		s.inTok += int64(resp.Usage.PromptTokens)
		s.outTok += int64(resp.Usage.CompletionTokens)
		if !s.emitUsage() {
			return
		}
		if max := s.spec.Budget.MaxUSD; max > 0 && s.priced() && s.cost() > max {
			s.fail(fmt.Sprintf("stopped at $%.4f: over the $%.2f budget for this run", s.cost(), max))
			return
		}

		message := resp.Message
		message.Role = "assistant"
		s.messages = append(s.messages, message)

		if len(message.ToolCalls) == 0 {
			if !s.handleProse(message.Content) {
				return
			}
			continue
		}
		if text := strings.TrimSpace(message.Content); text != "" {
			if !s.emit(provider.Event{
				Kind: provider.EvAssistantText,
				Text: text,
				Raw:  rawOf(map[string]string{"assistant": text}),
			}) {
				return
			}
		}
		if !s.runToolCalls(message.ToolCalls) {
			return
		}
		if !s.trim(resp.Usage.PromptTokens) {
			return
		}
	}
}

// handleProse deals with a reply that called no tool. A JSON object is
// taken as the note — some models answer that way however firmly they are
// told not to — and anything else earns one reminder, then ends the
// session. It reports whether the loop should take another turn.
func (s *session) handleProse(content string) bool {
	text := strings.TrimSpace(content)
	if isJSONObject(text) {
		s.final(json.RawMessage(text), "")
		return false
	}
	if text != "" {
		if !s.emit(provider.Event{
			Kind: provider.EvAssistantText,
			Text: text,
			Raw:  rawOf(map[string]string{"assistant": text}),
		}) {
			return false
		}
		s.mu.Lock()
		s.result.Text = text
		s.mu.Unlock()
	}
	if s.nudged {
		s.fail("the model answered in prose twice instead of calling " + submitNoteTool)
		return false
	}
	s.nudged = true
	s.messages = append(s.messages, Message{Role: "user", Content: Nudge()})
	return s.emit(provider.Event{
		Kind: provider.EvSystem,
		Text: "no tool call: reminded the model to call " + submitNoteTool,
		Raw:  rawOf(map[string]string{"nudge": Nudge()}),
	})
}

// runToolCalls executes one assistant message's tool calls in order,
// stopping at submit_note. It reports whether the loop should continue.
func (s *session) runToolCalls(calls []ToolCall) bool {
	for i, call := range calls {
		name := call.Function.Name
		args := json.RawMessage(strings.TrimSpace(call.Function.Arguments))
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}

		if t := s.byName[name]; t != nil && t.submit {
			s.answerTool(call.ID, name, "note received")
			// A model that puts submit_note in the middle of a batch
			// leaves the calls after it unanswered, and an unanswered
			// tool_call_id is a 400 from most endpoints on the next
			// request. The schema retry is exactly that request, so the
			// remaining calls are closed out here rather than leaving
			// the retry to fail on a transcript this loop wrote.
			for _, rest := range calls[i+1:] {
				s.answerTool(rest.ID, rest.Function.Name, "skipped: the note was already submitted")
			}
			s.finalFromArgs(args)
			return false
		}

		decision := s.policy.Decide(name, args)
		verdict := "deny"
		if decision.Allow {
			verdict = "allow"
		}
		if !s.emit(provider.Event{
			Kind:     provider.EvPermission,
			Tool:     name,
			Input:    args,
			Decision: verdict,
			Text:     decision.Message,
			Raw:      rawOf(map[string]any{"tool": name, "decision": verdict, "message": decision.Message}),
		}) {
			return false
		}
		if !decision.Allow {
			s.answerTool(call.ID, name, "denied: "+decision.Message)
			continue
		}

		if !s.emit(provider.Event{
			Kind:  provider.EvToolStarted,
			Tool:  name,
			Input: args,
			Raw:   rawOf(map[string]any{"tool": name, "arguments": args}),
		}) {
			return false
		}
		output := s.callTool(name, args)
		if !s.emit(provider.Event{
			Kind:  provider.EvToolFinished,
			Tool:  name,
			Input: args,
			Text:  output,
			Raw:   rawOf(map[string]any{"tool": name, "output": output}),
		}) {
			return false
		}
		s.answerTool(call.ID, name, output)
	}
	return true
}

// callTool runs one allowed tool call and returns what the model sees.
// Every failure comes back as tool output rather than as an error: a tool
// that broke is something the model can work around, and a loop that dies
// on it loses the whole run.
func (s *session) callTool(name string, args json.RawMessage) string {
	t := s.byName[name]
	switch {
	case t == nil:
		return "error: unknown tool " + name
	case t.local != nil:
		out, err := t.local.Call(s.ctx, args)
		if err != nil {
			return "error: " + err.Error()
		}
		return capOutput(out)
	case t.server != "":
		client, err := s.ensureClient(t.server)
		if err != nil {
			return "error: " + err.Error()
		}
		out, isErr, err := client.CallTool(s.ctx, t.remote, args)
		if err != nil {
			return "error: " + err.Error()
		}
		if isErr {
			return "error: " + capOutput(out)
		}
		return capOutput(out)
	default:
		return "error: tool " + name + " cannot be called"
	}
}

// answerTool appends the tool message the next request has to carry: an
// assistant message with tool calls is only valid when every call has an
// answer.
func (s *session) answerTool(id, name, content string) {
	s.messages = append(s.messages, Message{
		Role:       "tool",
		ToolCallID: id,
		Name:       name,
		Content:    content,
	})
}

// finalFromArgs records the note the model submitted. The arguments are
// passed through as they arrived — the runner validates them against the
// schema and buys a retry when they do not fit — but only when they parse
// as JSON at all; anything else is reported as text, which the runner
// treats the same way.
func (s *session) finalFromArgs(args json.RawMessage) {
	if isJSONObject(strings.TrimSpace(string(args))) {
		s.final(args, "")
		return
	}
	s.final(nil, string(args))
}

func (s *session) final(doc json.RawMessage, text string) {
	s.mu.Lock()
	s.result.Final = doc
	if text != "" {
		s.result.Text = text
	}
	s.result.ExitErr = nil
	s.mu.Unlock()

	s.emit(provider.Event{
		Kind:      provider.EvFinal,
		Final:     doc,
		Text:      text,
		Turns:     s.turns,
		InputTok:  s.inTok,
		OutputTok: s.outTok,
		CostUSD:   s.cost(),
		Raw:       rawOf(map[string]any{"final": true, "turns": s.turns}),
	})
}

// fail ends the session with an error the operator will see as the run's
// reason.
func (s *session) fail(text string) {
	s.mu.Lock()
	s.result.ExitErr = errors.New(text)
	s.mu.Unlock()
	s.emit(provider.Event{
		Kind: provider.EvError,
		Text: text,
		Raw:  rawOf(map[string]string{"error": text}),
	})
}

// rateLimited ends the session on a 429 the chat client's retry could not
// get past. The event carries the reset time so the runner can hold the
// whole queue until the window reopens, rather than spending the rest of
// it on tickets that will be refused the same way.
func (s *session) rateLimited(err *RateLimitError) {
	s.mu.Lock()
	s.result.ExitErr = err
	s.mu.Unlock()
	s.emit(provider.Event{
		Kind:     provider.EvRateLimited,
		Text:     err.Error(),
		ResetsAt: err.ResetsAt,
		Raw:      rawOf(map[string]any{"rateLimited": true, "resetsAt": err.ResetsAt}),
	})
}

func (s *session) warn(text string) bool {
	return s.emit(provider.Event{
		Kind: provider.EvSystem,
		Text: text,
		Raw:  rawOf(map[string]string{"warning": text}),
	})
}

func (s *session) emitUsage() bool {
	cost := s.cost()
	s.mu.Lock()
	s.result.Usage.Turns = s.turns
	s.result.Usage.InputTok = s.inTok
	s.result.Usage.OutputTok = s.outTok
	s.result.Usage.CostUSD = cost
	s.mu.Unlock()

	return s.emit(provider.Event{
		Kind:      provider.EvUsage,
		Turns:     s.turns,
		InputTok:  s.inTok,
		OutputTok: s.outTok,
		CostUSD:   cost,
		Raw:       rawOf(map[string]any{"turns": s.turns, "inputTokens": s.inTok, "outputTokens": s.outTok, "costUsd": cost}),
	})
}

// trim drops the oldest tool results once the prompt approaches the
// context window, so a long investigation degrades instead of failing at
// the server's limit. The most recent results are kept: they are what the
// model is reasoning from.
func (s *session) trim(promptTokens int) bool {
	if promptTokens <= 0 || s.cfg.MaxContextTokens <= 0 {
		return true
	}
	if float64(promptTokens) < trimThreshold*float64(s.cfg.MaxContextTokens) {
		return true
	}

	var indexes []int
	for i, m := range s.messages {
		if m.Role == "tool" && m.Content != trimmedMarker {
			indexes = append(indexes, i)
		}
	}
	if len(indexes) <= keepToolResults {
		return true
	}
	dropped := indexes[:len(indexes)-keepToolResults]
	for _, i := range dropped {
		s.messages[i].Content = trimmedMarker
	}
	return s.warn(fmt.Sprintf("context at %d tokens: trimmed %d older tool results", promptTokens, len(dropped)))
}

func (s *session) toolSpecs() []ToolSpec {
	out := make([]ToolSpec, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, ToolSpec{Name: t.name, Description: t.description, Parameters: t.parameters})
	}
	return out
}

func (s *session) priced() bool {
	return s.cfg.PriceInputPerMTok > 0 || s.cfg.PriceOutputPerMTok > 0
}

// cost is what the run has spent so far, per the workspace's price table.
// With no prices configured it is 0, and the runner's turn and wall-clock
// budgets are the only ones that bite.
func (s *session) cost() float64 {
	return float64(s.inTok)/1e6*s.cfg.PriceInputPerMTok + float64(s.outTok)/1e6*s.cfg.PriceOutputPerMTok
}

// emit delivers one event, or reports false once the session has been
// cancelled and nobody is reading any more.
func (s *session) emit(ev provider.Event) bool {
	ev.At = time.Now()
	if ev.Raw == nil {
		ev.Raw = json.RawMessage(`{}`)
	}
	select {
	case s.events <- ev:
		return true
	case <-s.dead:
		return false
	}
}

// awaitSend holds the session open after an end state, so the runner's
// schema retry has somewhere to land, and reports whether a follow-up
// arrived.
//
// The problem it solves: the runner drains Events() to its close before it
// calls Wait, and it calls Send from inside that drain, while handling the
// final event. Ending the stream as soon as the note is submitted would
// therefore refuse every retry (which is what the Codex adapter does, and
// why it has to resume a fresh session instead); never ending it would
// hang the run.
//
// So the loop sends one last event and treats it as a barrier. The channel
// is unbuffered, so that send can only complete once the caller has come
// back for another event — which is strictly after it finished with the
// final, and therefore after any Send it was going to make. A send that
// arrives in that window is picked up here; the re-check after the barrier
// covers the case where both were ready at once.
func (s *session) awaitSend() bool {
	if text, ok := s.takeSend(); ok {
		s.resume(text)
		return true
	}

	ended := provider.Event{
		Kind: provider.EvSystem,
		At:   time.Now(),
		Text: sessionEnded,
		Raw:  rawOf(map[string]any{"sessionEnded": true, "turns": s.turns}),
	}
	select {
	case text := <-s.sendCh:
		s.resume(text)
		return true
	case s.events <- ended:
	case <-s.dead:
		return false
	}

	if text, ok := s.takeSend(); ok {
		s.resume(text)
		return true
	}
	return false
}

func (s *session) takeSend() (string, bool) {
	select {
	case text := <-s.sendCh:
		return text, true
	default:
		return "", false
	}
}

// resume appends the caller's follow-up and lets the loop take more turns.
// The prose reminder is re-armed: the follow-up is a new instruction, and
// the model deserves the same one reminder against it.
func (s *session) resume(text string) {
	s.messages = append(s.messages, Message{Role: "user", Content: text})
	s.nudged = false
}

// ensureClient starts an MCP server the first time the session needs it
// and keeps it for the rest of the run. A server that failed to start
// stays failed: retrying it on every tool call would spend the run's time
// re-spawning something broken.
func (s *session) ensureClient(name string) (*mcpclient.Client, error) {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()

	if s.mcpClosed {
		return nil, errors.New("the session has ended")
	}
	if c, ok := s.mcp[name]; ok {
		return c, nil
	}
	if err, ok := s.mcpErr[name]; ok {
		return nil, err
	}
	cfg, ok := s.servers[name]
	if !ok {
		return nil, fmt.Errorf("no such server %q", name)
	}

	startCtx, cancel := context.WithTimeout(s.ctx, mcpStartTimeout)
	defer cancel()
	client, err := mcpclient.Start(startCtx, cfg, s.tail.Writer(name))
	if err != nil {
		s.mcpErr[name] = err
		return nil, err
	}
	s.mcp[name] = client
	return client, nil
}

// closeMCP shuts every started server down. mcpclient.Start deliberately
// outlives the context it was given, so this is the only thing that stops
// them: both Cancel and Wait call it, and it is safe to call twice.
func (s *session) closeMCP() {
	s.mcpMu.Lock()
	clients := make([]*mcpclient.Client, 0, len(s.mcp))
	for _, c := range s.mcp {
		clients = append(clients, c)
	}
	s.mcp = map[string]*mcpclient.Client{}
	s.mcpClosed = true
	s.mcpMu.Unlock()

	for _, c := range clients {
		_ = c.Close()
	}
}

// isJSONObject reports whether s is a JSON object, the shape every note
// schema has.
func isJSONObject(s string) bool {
	if !strings.HasPrefix(s, "{") {
		return false
	}
	return json.Valid([]byte(s))
}

// capOutput bounds one tool result. MCP servers answer with whatever they
// like, and an unbounded result would be sent back to the model on every
// subsequent turn.
func capOutput(s string) string {
	if len(s) <= maxToolOutputBytes {
		return s
	}
	cut := s[:maxToolOutputBytes]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return fmt.Sprintf("%s\n[truncated %d bytes]", cut, len(s)-len(cut))
}

// rawOf renders an event's provider-line equivalent. This provider has no
// wire lines of its own — the loop is Sirdar's — so each event carries a
// small object describing what happened, which is what events.jsonl keeps.
func rawOf(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
