// Package acp adapts any agent that speaks the Agent Client Protocol to
// Sirdar's provider contract. One client covers Gemini CLI, Goose,
// OpenCode, Qwen Code, Kimi CLI, Crush, Junie, Augment, GitHub Copilot CLI
// and the rest of the ACP registry, plus Claude Code and Codex through the
// Zed/ACP adapters — though those two are better driven by Sirdar's own
// claude and codex providers, which speak their native protocols and get a
// cost signal, a rate-limit signal and schema-constrained output that ACP
// has no field for.
//
// What ACP does not give a client, and what this adapter does instead:
//
//   - No structured output. There is no schema field on session/prompt, so
//     the note schema goes into the prompt text and the concatenated
//     agent_message_chunk text is parsed as JSON at end_turn. A fenced
//     ```json block is unwrapped first. Text that is not a JSON object
//     becomes an EvError and an EvFinal carrying the prose, which is what
//     puts the runner's one schema-retry turn on the wire as a second
//     session/prompt.
//   - No cost or token accounting beyond usage_update's context used/size
//     and an optional session cost, which many agents never send, so
//     budget.maxUsd usually never fires.
//   - Almost nothing for budget.maxTurns to count. One ACP turn is a whole
//     prompt turn: the agent may make dozens of model requests and run
//     dozens of tools inside a single session/prompt, and the protocol
//     reports one turn for all of it. A run therefore normally ends at
//     turn one or two, and budget.maxMinutes is the bound that actually
//     stops a runaway agent. Set it as if it were the only one, because
//     for most ACP agents it is.
//   - No rate-limit signal at all, so a spent window looks like an error,
//     not a pause.
//   - No message on a permission denial. The client may only select one of
//     the options the agent offered, so the read-only instruction has to be
//     in the prompt; the reason is still recorded as an EvPermission for the
//     operator.
//
// Sirdar declines the write-file and terminal client capabilities at
// initialize rather than denying each request, and answers the fs and
// terminal methods an agent calls anyway with a JSON-RPC error.
//
// What that does not amount to is a sandbox, and the difference matters
// more here than for the other providers. An ACP agent is a whole CLI with
// its own tools, its own configuration and, in most cases, its own global
// MCP servers, none of which the protocol lets a client see or switch off:
// mcp.workspaceOnly cannot be enforced across this boundary the way
// --strict-mcp-config enforces it for Claude Code. Permission requests are
// the agent's to send, so Sirdar's policy governs what it is asked about
// and nothing else.
//
// Two things narrow that. Where the agent exposes ACP session modes, the
// read-only one is selected before the first prompt (see selectMode), so an
// agent that would otherwise have approved a write without asking refuses
// it in its own process instead. And a write, a command or a sub-agent
// spawn that completes in a read-only session having asked nobody raises an
// EvBreach (see observeUnmediated), which ends the run and files nothing —
// the only move left once the thing has already happened. Note too that the
// workspace's .mcp.json env values are sent to the agent in session/new, so
// any secret in them crosses the wire into that agent's process.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
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
	// protocolVersion is ACP v1, the version every shipped agent speaks.
	protocolVersion = 1
	// clientVersion is reported in initialize.clientInfo.version.
	clientVersion = "0.1.0-dev"
	// shutdownGrace is how long a closed stdin gets to end the agent
	// before its process group is killed.
	shutdownGrace = 5 * time.Second
	// cancelGrace is how long session/cancel gets to wind the turn down
	// before the process group is killed.
	cancelGrace = 5 * time.Second
	// stderrTailLines is how much of the agent's stderr is kept.
	stderrTailLines = 50
	// doctorTimeout bounds the whole Doctor handshake.
	doctorTimeout = 30 * time.Second
	// sessionEnded is the last event of a finished turn. It doubles as the
	// barrier that tells the session the caller has finished with the
	// final event and is not going to answer it; see barrier.
	sessionEnded = "session ended"
)

// Config is the agent to launch. Command is the program — `gemini`,
// `goose`, `npx` — and Args the rest of its command line, e.g.
// `{"--experimental-acp"}`, `{"acp"}`, or
// `{"@zed-industries/claude-code-acp"}`. Env is added to the child's
// environment; it is not the whole of it.
//
// Mode overrides the session mode this adapter would otherwise pick for
// itself (see selectMode). It is one agent's own mode id — kimi's `plan`,
// another agent's `read-only` — for a workspace whose agent names its
// read-only mode something this adapter does not recognise.
type Config struct {
	Command string
	Args    []string
	Env     map[string]string
	Mode    string
}

// Provider starts ACP sessions against the configured agent.
type Provider struct{ cfg Config }

// New returns the ACP provider for one configured agent command.
func New(cfg Config) provider.Provider { return &Provider{cfg: cfg} }

// Name identifies this provider in config and run records. It is the
// protocol, not the agent: which agent ran is in the acp.command setting
// and in the doctor row.
func (p *Provider) Name() string { return "acp" }

// agentCapabilities is the part of the initialize result this adapter acts
// on: whether a session can be resumed, and whether images may be sent.
type agentCapabilities struct {
	LoadSession        bool `json:"loadSession"`
	PromptCapabilities struct {
		Image           bool `json:"image"`
		Audio           bool `json:"audio"`
		EmbeddedContext bool `json:"embeddedContext"`
	} `json:"promptCapabilities"`
	MCPCapabilities struct {
		HTTP bool `json:"http"`
		SSE  bool `json:"sse"`
	} `json:"mcpCapabilities"`

	// SessionCapabilities is the optional block an agent uses to say which
	// session methods it serves. Only setMode is read: its presence is the
	// second way an agent can advertise modes, for one that serves
	// session/set_mode but lists no modes until a session exists.
	SessionCapabilities struct {
		SetMode json.RawMessage `json:"setMode"`
	} `json:"sessionCapabilities"`
}

type initializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities agentCapabilities `json:"agentCapabilities"`
	AgentInfo         struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"agentInfo"`
	AuthMethods []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"authMethods"`

	// Modes is where a few agents put the mode list ACP defines on
	// session/new. It is read here as a fallback, not as the primary
	// source.
	Modes *sessionModes `json:"modes"`
}

// sessionModes is ACP's session-modes block: the ids the session will
// accept on session/set_mode, and which one it is in now. It rides on
// session/new (and session/load) where an agent exposes modes at all.
type sessionModes struct {
	CurrentModeID  string `json:"currentModeId"`
	AvailableModes []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"availableModes"`
}

// modeKey reduces a mode id to the word it is recognised by. ACP mode ids
// are opaque strings, and some agents make them URLs: every one of GitHub
// Copilot's is a link into the protocol's own session-modes page
// (`https://agentclientprotocol.com/protocol/session-modes#plan`), where
// the only part that names the mode is the fragment. So the key is what
// follows the last "#", or failing that the last "/", or the whole id when
// it has neither — which is every agent that names its modes plainly.
func modeKey(id string) string {
	if i := strings.LastIndex(id, "#"); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	return id
}

// match returns the agent's own id for the mode called want, or "" when
// the session advertised no such mode. A whole-id match wins over a key
// one, so an agent that offers both `plan` and a URL ending in `#plan` is
// taken at its plainer word; the id that comes back is always the agent's,
// verbatim, since that is the only string session/set_mode accepts.
func (m *sessionModes) match(want string) string {
	if m == nil {
		return ""
	}
	for _, mode := range m.AvailableModes {
		if mode.ID == want {
			return mode.ID
		}
	}
	for _, mode := range m.AvailableModes {
		if strings.EqualFold(modeKey(mode.ID), want) {
			return mode.ID
		}
	}
	return ""
}

// ids lists the advertised mode ids, for an error that has to say what was
// on offer instead.
func (m *sessionModes) ids() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.AvailableModes))
	for _, mode := range m.AvailableModes {
		out = append(out, mode.ID)
	}
	return out
}

// configOption is one entry of the session config-options block ACP added
// alongside session modes and means to replace them with: a typed choice
// the agent advertises on session/new and the client changes with
// session/set_config_option. OpenCode puts its session mode here and
// advertises no availableModes at all, so this is the only way to get one
// of its sessions out of `build` and into `plan`.
//
// An option's own values carry `value`; `id` is read as well because the
// two spellings appear in different write-ups of the same block and
// nothing is lost by accepting either.
type configOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	Type         string `json:"type"`
	CurrentValue string `json:"currentValue"`
	Options      []struct {
		Value string `json:"value"`
		ID    string `json:"id"`
		Name  string `json:"name"`
	} `json:"options"`
}

// value returns this option's own spelling of want, or "" when it offers
// nothing of the kind. It matches the way sessionModes.match does, down to
// modeKey, since an agent free to name a mode with a URL is just as free
// to name a config value with one.
func (o *configOption) value(want string) string {
	if o == nil {
		return ""
	}
	for _, opt := range o.Options {
		if v := optionValue(opt.Value, opt.ID); v == want {
			return v
		}
	}
	for _, opt := range o.Options {
		v := optionValue(opt.Value, opt.ID)
		if v != "" && strings.EqualFold(modeKey(v), want) {
			return v
		}
	}
	return ""
}

// values lists what this option offers, for a notice that has to say so.
func (o *configOption) values() []string {
	if o == nil {
		return nil
	}
	out := make([]string, 0, len(o.Options))
	for _, opt := range o.Options {
		if v := optionValue(opt.Value, opt.ID); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func optionValue(value, id string) string {
	if value != "" {
		return value
	}
	return id
}

// configOptionsOf reads the config-options block out of a session/new or
// session/load result. Nothing falls back to initialize here: unlike
// modes, config options exist only once a session does.
func configOptionsOf(result json.RawMessage) []configOption {
	var opened struct {
		ConfigOptions []configOption `json:"configOptions"`
	}
	if err := json.Unmarshal(result, &opened); err != nil {
		return nil
	}
	return opened.ConfigOptions
}

// modeOption picks the config option that is the session mode, by the id
// OpenCode uses and by the category the protocol defines for it. A config
// option that is neither is the model, the thinking tier, or whatever else
// the agent exposes, and none of Sirdar's business.
func modeOption(options []configOption) *configOption {
	for i := range options {
		if options[i].ID == "mode" || options[i].Category == "mode" {
			return &options[i]
		}
	}
	return nil
}

// Start launches the agent, initializes it, opens (or loads) a session and
// puts the first session/prompt on the wire. It returns once the session
// exists; the turn's activity arrives on the event channel.
func (p *Provider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	command, args := p.cfg.Command, p.cfg.Args
	if spec.Binary != "" {
		command = spec.Binary
	}
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("acp: no agent command configured (set acp.command)")
	}

	servers, warnings, err := mcpclient.LoadWorkspaceServers(spec.Cwd)
	if err != nil {
		return nil, fmt.Errorf("acp: %w", err)
	}

	cmd := exec.Command(command, args...)
	cmd.Dir = spec.Cwd
	cmd.Env = childEnv(spec.Env, p.cfg.Env)
	setpgid(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stderr pipe: %w", err)
	}

	s := &session{
		cmd:        cmd,
		spec:       spec,
		servers:    servers,
		modeWanted: strings.TrimSpace(p.cfg.Mode),
		events:     make(chan provider.Event),
		sendCh:     make(chan string, 1),
		turnCh:     make(chan string),
		turnDone:   make(chan struct{}),
		exited:     make(chan struct{}),
		stdoutDone: make(chan struct{}),
		stderrDone: make(chan struct{}),
		stopped:    make(chan struct{}),
		closing:    make(chan struct{}),
		pumpDone:   make(chan struct{}),
		driverDone: make(chan struct{}),
		calls:      map[string]*trackedCall{},
	}
	s.queue.cond = sync.NewCond(&s.queue.mu)
	s.conn = newConn(stdout, stdin)
	s.conn.onNotify = s.onNotify
	s.conn.onRequest = s.onRequest

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: start %s: %w", command, err)
	}

	go s.pump()
	go func() {
		defer close(s.stdoutDone)
		s.conn.run()
		// stdout is at EOF, so the agent can send nothing more: end the
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

	for _, w := range warnings {
		s.emit(provider.Event{Kind: provider.EvSystem, Text: "mcp: " + w, Raw: rawOf(map[string]any{"warning": w})})
	}

	if err := s.handshake(ctx); err != nil {
		s.abort()
		return nil, err
	}
	go s.drive()
	return s, nil
}

// childEnv is the agent's environment: the caller's, plus the acp.env
// entries from config. It is additive because an ACP agent authenticates
// however its own CLI does — a login file, a keychain, an API key already
// in the operator's shell — and stripping the environment would break
// every one of those.
func childEnv(base []string, extra map[string]string) []string {
	if len(base) == 0 {
		base = os.Environ()
	}
	if len(extra) == 0 {
		return base
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(base)+len(keys))
	out = append(out, base...)
	for _, k := range keys {
		out = append(out, k+"="+extra[k])
	}
	return out
}

// handshake runs initialize → session/new (or session/load).
func (s *session) handshake(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	raw, err := s.conn.call("initialize", map[string]any{
		"protocolVersion":    protocolVersion,
		"clientCapabilities": clientCapabilities(),
		"clientInfo": map[string]string{
			"name":    "sirdar",
			"title":   "Sirdar",
			"version": clientVersion,
		},
	})
	if err != nil {
		return fmt.Errorf("acp: initialize: %w", err)
	}
	var init initializeResult
	if err := json.Unmarshal(raw, &init); err != nil {
		return fmt.Errorf("acp: initialize result: %w", err)
	}
	s.mu.Lock()
	s.caps = init.AgentCapabilities
	s.setModeServed = len(init.AgentCapabilities.SessionCapabilities.SetMode) > 0 &&
		!isJSONNull(init.AgentCapabilities.SessionCapabilities.SetMode)
	s.mu.Unlock()
	s.emit(provider.Event{Kind: provider.EvSystem, Text: "acp agent " + agentLabel(init), Raw: raw})

	if s.spec.Resume != "" {
		if !init.AgentCapabilities.LoadSession {
			return fmt.Errorf("acp: this agent cannot resume a session (no loadSession capability); start a fresh run")
		}
		// The id is recorded before the call, not after: session/load
		// replays the whole prior conversation as a burst of
		// session/update notifications while the call is still open, and
		// the session filter has to recognise them as this session's.
		s.mu.Lock()
		s.sessionID = s.spec.Resume
		s.mu.Unlock()
		loaded, err := s.conn.call("session/load", map[string]any{
			"sessionId":  s.spec.Resume,
			"cwd":        s.spec.Cwd,
			"mcpServers": mcpServerParams(s.servers),
		})
		if err != nil {
			return fmt.Errorf("acp: session/load: %w", err)
		}
		// A resumed session carries the mode it was left in, which is the
		// previous run's, not this one's: it is selected again here for
		// the same reason it was selected the first time.
		s.selectMode(modesOf(loaded, init), configOptionsOf(loaded), loaded)
		return nil
	}

	raw, err = s.conn.call("session/new", map[string]any{
		"cwd":        s.spec.Cwd,
		"mcpServers": mcpServerParams(s.servers),
	})
	if err != nil {
		return fmt.Errorf("acp: session/new: %w", err)
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return fmt.Errorf("acp: session/new result: %w", err)
	}
	if created.SessionID == "" {
		return errors.New("acp: session/new returned no sessionId")
	}
	s.mu.Lock()
	s.sessionID = created.SessionID
	s.mu.Unlock()
	s.selectMode(modesOf(raw, init), configOptionsOf(raw), raw)
	return nil
}

// modesOf reads the session-modes block out of a session/new or
// session/load result, falling back to the one a few agents put on the
// initialize result instead.
func modesOf(result json.RawMessage, init initializeResult) *sessionModes {
	var opened struct {
		Modes *sessionModes `json:"modes"`
	}
	if err := json.Unmarshal(result, &opened); err == nil && opened.Modes != nil &&
		(len(opened.Modes.AvailableModes) > 0 || opened.Modes.CurrentModeID != "") {
		return opened.Modes
	}
	return init.Modes
}

// readOnlyModeIDs are the words agents use for a mode that refuses writes,
// in the order they are preferred. kimi and Gemini CLI call it `plan`; the
// Copilot and Cursor spikes each had their own spelling, which is why more
// than one is matched and why acp.mode exists for an agent that matches
// none of them. These are matched against an agent's mode ids by modeKey,
// not only whole, so Copilot's URL ids resolve too.
var readOnlyModeIDs = []string{"plan", "read-only", "readonly", "read_only", "ask"}

// editModeIDs are the words for the mode a `sirdar fix` session needs: one
// that may actually write, with Sirdar's permission policy still deciding
// each call. OpenCode's is `build`, and it is last because an agent that
// offers `default` or `edit` as well means one of those.
var editModeIDs = []string{"default", "edit", "build"}

// selectMode puts the session into the mode the run needs, before the
// first session/prompt goes out.
//
// This is defence in depth, not the guarantee. The guarantee is that every
// session/request_permission is answered by the run's PermissionPolicy;
// what a mode adds is the agent's own in-process refusal of writes it
// would otherwise have approved without asking — kimi's plan mode vetoes
// Write and Edit before its permission chain is consulted at all, and its
// `default` mode approves an in-workspace write before a request is even
// built. An agent that offers a read-only mode and is not put in one is
// the case the tool_call_update breach check exists to catch after the
// fact; selecting the mode is how it is caught before.
//
// The chosen id is sent even when the agent says it is already current: it
// costs one round trip and it means the mode the run ran under was set by
// this client rather than inferred from the agent's own report.
func (s *session) selectMode(modes *sessionModes, options []configOption, raw json.RawMessage) {
	s.mu.Lock()
	sessionID, served := s.sessionID, s.setModeServed
	s.mu.Unlock()

	offered := len(modes.ids()) > 0
	option := modeOption(options)
	if !offered && option == nil && !served {
		s.emit(provider.Event{
			Kind: provider.EvSystem,
			Text: "acp: this agent offers no session modes, so there is no read-only mode to select; " +
				"the run's posture rests on Sirdar's permission policy and on the breach check for a " +
				"tool call that completes without asking",
			Raw: raw,
		})
		return
	}

	fix := s.isFix()
	want := readOnlyModeIDs
	posture := "read-only"
	if fix {
		want = editModeIDs
		posture = "fix"
	}

	// An agent that exposes its mode both ways is taken at session/set_mode,
	// which is the older call and the one every capture has confirmed. The
	// config option is what an agent like OpenCode leaves instead of a mode
	// list, not a second opinion about one.
	if !offered && option != nil {
		s.selectModeOption(sessionID, option, want, posture, raw)
		return
	}

	chosen := ""
	switch {
	case s.modeWanted != "":
		chosen = s.modeWanted
		if offered {
			// acp.mode is written by a person, so it is the bare word
			// even where the agent's own id is a URL; what goes back on
			// the wire has to be the agent's id.
			resolved := modes.match(chosen)
			if resolved == "" {
				s.emit(provider.Event{
					Kind: provider.EvError,
					Text: fmt.Sprintf("acp: acp.mode is %q, which this agent does not offer (it offers %s); "+
						"no mode was selected and the session runs in %q",
						chosen, strings.Join(modes.ids(), ", "), modes.CurrentModeID),
					Raw: raw,
				})
				return
			}
			chosen = resolved
		}
	case offered:
		for _, id := range want {
			if resolved := modes.match(id); resolved != "" {
				chosen = resolved
				break
			}
		}
	}

	if chosen == "" {
		s.emit(provider.Event{
			Kind: provider.EvSystem,
			Text: fmt.Sprintf("acp: none of this agent's modes (%s) is a %s mode Sirdar recognises; "+
				"the session runs in %q — set acp.mode if one of them is the right one",
				strings.Join(modes.ids(), ", "), posture, modes.CurrentModeID),
			Raw: raw,
		})
		return
	}

	if _, err := s.conn.call("session/set_mode", map[string]any{
		"sessionId": sessionID,
		"modeId":    chosen,
	}); err != nil {
		s.emit(provider.Event{
			Kind: provider.EvError,
			Text: fmt.Sprintf("acp: session/set_mode %q was refused: %v; the session runs in whatever "+
				"mode it opened in", chosen, err),
			Raw: raw,
		})
		return
	}

	s.emit(provider.Event{
		Kind: provider.EvSystem,
		Text: fmt.Sprintf("acp mode %s selected for this %s session", chosen, posture),
		Raw:  rawOf(map[string]any{"modeId": chosen, "mode": posture, "available": modes.ids()}),
	})
}

// selectModeOption is selectMode for an agent whose session mode is a
// config option rather than a mode list — OpenCode is the one that has
// been captured doing it. The call is session/set_config_option, which
// takes `configId` (not `optionId`: the kimi capture has the agent
// refusing that spelling by name) and the option's own value.
func (s *session) selectModeOption(sessionID string, option *configOption, want []string, posture string, raw json.RawMessage) {
	chosen := ""
	switch {
	case s.modeWanted != "":
		chosen = option.value(s.modeWanted)
		if chosen == "" {
			s.emit(provider.Event{
				Kind: provider.EvError,
				Text: fmt.Sprintf("acp: acp.mode is %q, which this agent's %q config option does not offer "+
					"(it offers %s); no mode was selected and the session runs in %q",
					s.modeWanted, option.ID, strings.Join(option.values(), ", "), option.CurrentValue),
				Raw: raw,
			})
			return
		}
	default:
		for _, id := range want {
			if v := option.value(id); v != "" {
				chosen = v
				break
			}
		}
	}

	if chosen == "" {
		s.emit(provider.Event{
			Kind: provider.EvSystem,
			Text: fmt.Sprintf("acp: none of this agent's %q config option values (%s) is a %s mode Sirdar "+
				"recognises; the session runs in %q — set acp.mode if one of them is the right one",
				option.ID, strings.Join(option.values(), ", "), posture, option.CurrentValue),
			Raw: raw,
		})
		return
	}

	if _, err := s.conn.call("session/set_config_option", map[string]any{
		"sessionId": sessionID,
		"configId":  option.ID,
		"value":     chosen,
	}); err != nil {
		s.emit(provider.Event{
			Kind: provider.EvError,
			Text: fmt.Sprintf("acp: session/set_config_option %s=%q was refused: %v; the session runs in "+
				"whatever mode it opened in", option.ID, chosen, err),
			Raw: raw,
		})
		return
	}

	s.emit(provider.Event{
		Kind: provider.EvSystem,
		Text: fmt.Sprintf("acp mode %s selected for this %s session", chosen, posture),
		Raw: rawOf(map[string]any{
			"configId": option.ID, "value": chosen, "mode": posture, "available": option.values(),
		}),
	})
}

// isFix reports whether this session may write. The spec's Mode is the
// field that says so; the policy is asked too, since a caller that builds
// a fix policy without setting Mode still means a fix.
func (s *session) isFix() bool { return s.spec.Mode.IsFix() || s.spec.Policy.IsFix() }

// clientCapabilities is what Sirdar tells an agent it can be asked for.
// Reading a text file is allowed — it is cheaper for the agent to ask than
// to shell out, and the handler confines it to the workspace — while
// writing files and terminals are declined here so a well-behaved agent
// never asks. ACP's own wording is that an agent MUST NOT call a method
// whose capability is false.
func clientCapabilities() map[string]any {
	return map[string]any{
		"fs": map[string]any{
			"readTextFile":  true,
			"writeTextFile": false,
		},
		"terminal": false,
	}
}

// mcpServerParams renders the workspace's stdio MCP servers in ACP's
// session/new shape. ACP takes env as name/value pairs rather than an
// object, and sorting them keeps a session/new line stable between runs.
func mcpServerParams(servers []mcpclient.ServerConfig) []map[string]any {
	out := make([]map[string]any, 0, len(servers))
	for _, srv := range servers {
		args := srv.Args
		if args == nil {
			args = []string{}
		}
		env := make([]map[string]string, 0, len(srv.Env))
		keys := make([]string, 0, len(srv.Env))
		for k := range srv.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = append(env, map[string]string{"name": k, "value": srv.Env[k]})
		}
		out = append(out, map[string]any{
			"name":    srv.Name,
			"command": srv.Command,
			"args":    args,
			"env":     env,
		})
	}
	return out
}

func agentLabel(init initializeResult) string {
	name := init.AgentInfo.Name
	if name == "" {
		name = init.AgentInfo.Title
	}
	if name == "" {
		name = "(unnamed)"
	}
	if init.AgentInfo.Version != "" {
		name += " " + init.AgentInfo.Version
	}
	return fmt.Sprintf("%s, protocol v%d", name, init.ProtocolVersion)
}

// ------------------------------------------------------------------ session

type session struct {
	cmd     *exec.Cmd
	spec    provider.SessionSpec
	conn    *conn
	tail    stderrTail
	servers []mcpclient.ServerConfig

	events chan provider.Event
	queue  eventQueue

	// sendCh carries the runner's follow-up message to the barrier;
	// turnCh carries it on to the turn driver.
	sendCh chan string
	turnCh chan string

	// modeWanted is acp.mode from the workspace config: the mode id to
	// select instead of the one selectMode would pick. Empty means pick.
	modeWanted string

	mu            sync.Mutex
	caps          agentCapabilities
	setModeServed bool
	sessionID     string
	chunks        strings.Builder
	finalJSON     json.RawMessage
	finalText     string
	turns         int
	// emptyTurns counts the turns that ended with no assistant text at
	// all. See finishAnswer and unanswered.
	emptyTurns int
	usage      struct {
		in, out int64
		cost    float64
	}
	calls map[string]*trackedCall // toolCallId → what is known of it

	// preOpenDropped counts session-scoped traffic that arrived before
	// session/new (or session/load) gave this side a session id to check
	// ownership against. See owns and preOpenTraffic.
	preOpenDropped int

	turnDone   chan struct{}
	turnClosed bool
	turnErr    error

	streamClosed bool
	inputClosed  bool
	exitErr      error

	exited     chan struct{}
	stdoutDone chan struct{}
	stderrDone chan struct{}

	// stopped is closed by abort to release a pump with no reader;
	// closing is closed by closeStream so a pump parked on the
	// end-of-turn barrier gives up; pumpDone and driverDone close when
	// those goroutines have returned.
	stopped    chan struct{}
	stopOnce   sync.Once
	closing    chan struct{}
	closeOnce  sync.Once
	pumpDone   chan struct{}
	driverDone chan struct{}

	cancelOnce sync.Once
	waitOnce   sync.Once
	result     provider.Result
	waitErr    error
}

func (s *session) Events() <-chan provider.Event { return s.events }

// pumpExited closes when the goroutine feeding Events() has returned. It
// is what the package's own tests assert against; nothing outside this
// package can reach it, which is the point — a session's teardown is
// observable per session rather than through a package variable every
// concurrent Start would race on.
func (s *session) pumpExited() <-chan struct{} { return s.pumpDone }

// Handle returns the ACP sessionId. A later run resumes it with
// session/load, which only works against an agent advertising
// loadSession — Start says so plainly when it does not, and the runner
// falls back to a fresh session.
func (s *session) Handle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// Send hands the session a follow-up user message — in practice the
// runner's schema-retry turn — which the barrier picks up and the driver
// puts on the wire as another session/prompt.
func (s *session) Send(ctx context.Context, userText string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	streamClosed, inputClosed := s.streamClosed, s.inputClosed
	s.mu.Unlock()
	if streamClosed {
		return errors.New("acp: the session's event stream has ended")
	}
	if inputClosed {
		return errors.New("acp: the session's input has been closed")
	}
	select {
	case s.sendCh <- userText:
		return nil
	default:
		return errors.New("acp: a follow-up message is already pending")
	}
}

// CloseInput records that no follow-up message is coming. There is no
// stdin line an ACP agent is waiting on — a prompt turn ends with the
// session/prompt response, not with EOF — so nothing is written here. What
// it does is shut the door behind the runner, so a Send racing its
// endSession is refused rather than starting a turn nobody will read.
func (s *session) CloseInput() error {
	s.mu.Lock()
	s.inputClosed = true
	s.mu.Unlock()
	return nil
}

// Cancel asks the agent to wind the turn down and kills its process group
// if it has not by cancelGrace. ACP's session/cancel is a notification: the
// agent acknowledges it by resolving the open session/prompt with
// stopReason "cancelled", and an agent that does not is not going to.
func (s *session) Cancel() {
	s.mu.Lock()
	sessionID := s.sessionID
	s.mu.Unlock()
	if sessionID == "" {
		// There is no session to cancel: the agent is still starting, or
		// it never got as far as answering session/new. Nothing it is
		// doing is work this run asked for, so it goes now rather than
		// after a grace period spent waiting for a reply to a
		// notification naming no session.
		killGroup(s.cmd)
		return
	}
	_ = s.conn.notify("session/cancel", map[string]any{"sessionId": sessionID})
	s.cancelOnce.Do(func() {
		time.AfterFunc(cancelGrace, func() {
			select {
			case <-s.exited:
			default:
				killGroup(s.cmd)
			}
		})
	})
}

// Wait blocks until the current turn ends, shuts the agent down and
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

	s.shutdown()

	s.mu.Lock()
	res := provider.Result{
		Final:      s.finalJSON,
		Text:       s.finalText,
		Handle:     s.sessionID,
		ExitErr:    s.exitErr,
		StderrTail: s.tail.lines(),
	}
	res.Usage.Turns = s.turns
	res.Usage.InputTok = s.usage.in
	res.Usage.OutputTok = s.usage.out
	res.Usage.CostUSD = s.usage.cost
	err := s.turnErr
	if err == nil {
		err = s.exitErr
	}
	s.mu.Unlock()

	return res, err
}

// shutdown closes stdin, waits for the process (killing its group after
// the grace period), then ends the event stream.
func (s *session) shutdown() {
	_ = s.conn.closeWrite()

	if !waitFor(s.exited, shutdownGrace) {
		killGroup(s.cmd)
		if !waitFor(s.exited, shutdownGrace) {
			s.mu.Lock()
			if s.exitErr == nil {
				s.exitErr = errors.New("acp: the agent did not exit")
			}
			s.mu.Unlock()
		}
	}

	s.closeStream()
}

// closeStream ends Events() for good: no further turn may be started, and
// a pump parked on the end-of-turn barrier is released. It is safe to call
// more than once.
func (s *session) closeStream() {
	s.mu.Lock()
	s.streamClosed = true
	s.mu.Unlock()
	s.closeOnce.Do(func() { close(s.closing) })
	s.queue.close()
}

// abort tears down a session whose handshake failed. Start returns an
// error in that case, so the caller never receives the session and never
// reads its events: release the pump before shutting the queue down.
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

// beginTurn arms the turn barrier Wait blocks on.
func (s *session) beginTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks.Reset()
	if s.turnClosed {
		s.turnDone = make(chan struct{})
		s.turnClosed = false
		s.turnErr = nil
	}
}

// endTurn records a turn's outcome and releases Wait.
func (s *session) endTurn(err error) {
	s.mu.Lock()
	if s.turnErr == nil {
		s.turnErr = err
	}
	if !s.turnClosed {
		s.turnClosed = true
		close(s.turnDone)
	}
	s.mu.Unlock()
	s.queue.endTurn()
}

// ---------------------------------------------------------------- turn loop

// drive runs prompt turns one after another: the run's own prompt first,
// then whatever the barrier hands over from Send.
func (s *session) drive() {
	text, images := promptText(s.spec), s.spec.Images
	for {
		s.runTurn(text, images)
		select {
		case next := <-s.turnCh:
			text, images = next, nil
		case <-s.driverDone:
			return
		}
	}
}

// runTurn sends one session/prompt and blocks for the whole turn, which is
// how ACP works: the response carries the stop reason, and everything the
// agent did on the way arrives as session/update notifications.
func (s *session) runTurn(text string, images []string) {
	s.beginTurn()

	s.mu.Lock()
	sessionID, allowImages := s.sessionID, s.caps.PromptCapabilities.Image
	s.mu.Unlock()

	blocks := []map[string]any{{"type": "text", "text": text}}
	if allowImages {
		for _, path := range images {
			block, err := imageBlock(path)
			if err != nil {
				s.emit(provider.Event{
					Kind: provider.EvSystem,
					Text: fmt.Sprintf("acp: attachment %s was not sent: %v", filepath.Base(path), err),
					Raw:  rawOf(map[string]any{"attachment": path}),
				})
				continue
			}
			blocks = append(blocks, block)
		}
	} else if len(images) > 0 {
		s.emit(provider.Event{
			Kind: provider.EvSystem,
			Text: fmt.Sprintf("acp: the agent accepts no image prompts; %d attachment(s) were named in the prompt instead", len(images)),
			Raw:  rawOf(map[string]any{"attachments": images}),
		})
	}

	raw, err := s.conn.callLong("session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    blocks,
	})
	if err != nil {
		s.emit(provider.Event{Kind: provider.EvError, Text: err.Error(), Raw: rawOf(map[string]any{"error": err.Error()})})
		s.endTurn(err)
		return
	}

	var res struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(raw, &res)
	s.finishTurn(res.StopReason, raw)
}

// finishTurn turns a stop reason into the run's terminal events.
func (s *session) finishTurn(stopReason string, raw json.RawMessage) {
	s.mu.Lock()
	s.turns++
	turns := s.turns
	in, cost := s.usage.in, s.usage.cost
	text := strings.TrimSpace(s.chunks.String())
	s.mu.Unlock()

	// ACP reports no per-turn token usage, so the turn counter is the one
	// meter the runner's budget can be enforced against. It is emitted for
	// every stop reason, including the ones that end the run.
	s.emit(provider.Event{
		Kind:     provider.EvUsage,
		Turns:    turns,
		InputTok: in,
		CostUSD:  cost,
		Raw:      raw,
	})

	switch stopReason {
	case "end_turn":
		s.finishAnswer(text, raw)

	case "cancelled":
		s.emit(provider.Event{Kind: provider.EvSystem, Text: "acp: the prompt turn was cancelled", Raw: raw})
		s.endTurn(nil)

	case "max_tokens", "max_turn_requests":
		msg := "acp: the agent stopped early: " + stopReason
		s.emit(provider.Event{Kind: provider.EvError, Text: msg, Raw: raw})
		s.endTurn(errors.New(msg))

	case "refusal":
		const msg = "acp: the agent refused the prompt"
		s.emit(provider.Event{Kind: provider.EvError, Text: msg, Raw: raw})
		s.endTurn(errors.New(msg))

	default:
		msg := fmt.Sprintf("acp: session/prompt returned an unknown stop reason %q", stopReason)
		s.emit(provider.Event{Kind: provider.EvError, Text: msg, Raw: raw})
		s.endTurn(errors.New(msg))
	}
}

// finishAnswer parses the turn's assistant text as the run's JSON note.
//
// Prose earns two events, not one. The EvError says what went wrong, in the
// event log and in the operator's progress view, since a prose answer from
// an ACP agent is a real failure mode ACP gives no way to prevent. The
// EvFinal that follows carries the prose so the runner's schema check sees
// it, fails it, and spends its one retry turn — which reaches this session
// as a Send, and the wire as a second session/prompt.
func (s *session) finishAnswer(text string, raw json.RawMessage) {
	doc := jsonObject(text)
	if doc != nil {
		s.mu.Lock()
		s.finalJSON = doc
		s.finalText = ""
		s.mu.Unlock()
		s.emit(provider.Event{Kind: provider.EvFinal, Final: doc, Raw: raw})
		s.endTurn(nil)
		return
	}

	s.mu.Lock()
	s.finalText = text
	empty := strings.TrimSpace(text) == ""
	if empty {
		s.emptyTurns++
	}
	s.mu.Unlock()

	// An empty turn and a prose turn are different failures and the
	// operator has to be able to tell them apart: one agent answered the
	// wrong way, the other stopped without answering at all — which is
	// what a turn spent entirely on tool calls and thinking looks like.
	//
	// The prose answer is an error where it stands, because the turn was
	// spent and something wrong came back. An empty turn is only a
	// warning here: Copilot ends two of them before a good third often
	// enough that a run which goes on to file a valid note should not be
	// carrying error lines about the turns it took to get there. If no
	// answer ever arrives, the session says so on the way out — see
	// unanswered.
	kind := provider.EvError
	reason := "acp: the agent did not return a JSON note"
	if empty {
		kind = provider.EvSystem
		reason = emptyTurnText
	}
	s.emit(provider.Event{
		Kind: kind,
		Text: reason,
		Raw:  raw,
	})
	s.emit(provider.Event{Kind: provider.EvFinal, Text: text, Raw: raw})
	s.endTurn(nil)
}

// emptyTurnText is what a turn that produced no assistant text at all is
// called, as a warning while the run may still recover and as the run's
// error once it has not.
const emptyTurnText = "acp: the agent ended the turn without an answer"

// unanswered is the error the empty-turn warnings were held back from. It
// goes out at the end of the session — after the final event and after the
// caller has had its chance to send a retry — and only when the run really
// did end with nothing: no JSON note, and no prose answer either (a prose
// answer earned its own error when it arrived).
func (s *session) unanswered() {
	s.mu.Lock()
	empty := s.emptyTurns
	answered := len(s.finalJSON) > 0 || strings.TrimSpace(s.finalText) != ""
	s.mu.Unlock()
	if empty == 0 || answered {
		return
	}

	ev := provider.Event{
		Kind: provider.EvError,
		At:   time.Now(),
		Text: emptyTurnText,
		Raw:  rawOf(map[string]any{"emptyTurns": empty}),
	}
	select {
	case s.events <- ev:
	case <-s.stopped:
	case <-s.closing:
	}
}

// promptText is the run's prompt plus the only structured-output mechanism
// ACP has: an instruction. There is no schema field on session/prompt and
// no response_format equivalent, so the schema goes in the text and the
// answer is parsed out of the assistant's own message.
func promptText(spec provider.SessionSpec) string {
	var b strings.Builder
	b.WriteString(spec.Prompt)
	b.WriteString("\n\n---\n\nFinish by replying with one JSON object and nothing else: " +
		"no prose before or after it, no commentary, no summary, no code fence. " +
		"Anything else is discarded and you will be asked again.")
	if len(spec.OutputSchema) > 0 {
		b.WriteString("\n\nThe object must match this JSON Schema:\n\n```json\n")
		b.Write(compactJSON(spec.OutputSchema))
		b.WriteString("\n```")
		b.WriteString("\n\nThat schema describes the shape of your answer; it is not the answer. " +
			"Reply with the JSON object only: no `$schema`, no `title`, no surrounding text or code fence.")
	}
	if spec.Policy.IsFix() {
		// A fix session is here to edit, and telling it otherwise is how
		// the read-only wording used to turn a fix run into a turn spent
		// explaining that it could not make the change.
		b.WriteString("\n\nYou may edit files in this workspace. Every write has to land inside " +
			"the workspace directory, and writes to .git/, .sirdar/ and the repository's hooks " +
			"directory are refused by the harness, which cannot tell you why at the moment it " +
			"refuses. Shell commands are limited to the workspace's allow-list.")
		return b.String()
	}
	b.WriteString("\n\nThis run is read-only. Do not write, edit, move or delete any file, " +
		"and do not run a command that changes anything: such a request is refused by the harness, " +
		"which cannot tell you why at the moment it refuses.")
	return b.String()
}

func compactJSON(raw []byte) []byte {
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return raw
	}
	return out.Bytes()
}

// jsonObject returns the run's note from the turn's assistant text, or nil
// when there is none.
//
// A fence is unwrapped first, and then the *last* top-level JSON object in
// what remains is taken. Agents that were asked for JSON and nothing else
// routinely lead with a sentence anyway ("Here is the triage note:"), and
// an answer that is otherwise perfectly good should not be thrown away and
// retried over a prefix. Taking the last one rather than the first is what
// makes that safe: an agent that quotes an example object and then answers
// is answering with the second.
//
// Only an object counts. The note schemas are objects, and accepting a
// bare string or array here would hand the runner something that can never
// validate.
func jsonObject(text string) json.RawMessage {
	return lastJSONObject(stripFence(text))
}

// lastJSONObject scans for balanced top-level { } spans, ignoring braces
// inside string literals, and returns the last one that parses.
func lastJSONObject(text string) json.RawMessage {
	var (
		depth int
		start = -1
		inStr bool
		esc   bool
		best  string
	)
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				if candidate := text[start : i+1]; json.Valid([]byte(candidate)) {
					best = candidate
				}
				start = -1
			}
		}
	}
	if best == "" {
		return nil
	}
	return json.RawMessage(best)
}

// stripFence removes one surrounding ```json (or ```) code fence, which is
// how most agents present a JSON answer however plainly they are asked not
// to.
func stripFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	rest := strings.TrimPrefix(trimmed, "```")
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		// Anything on the opening line is the language tag.
		if tag := strings.TrimSpace(rest[:i]); tag == "" || !strings.ContainsAny(tag, "{ \t") {
			rest = rest[i+1:]
		}
	}
	if i := strings.LastIndex(rest, "```"); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// imageBlock reads a local attachment into the only image shape ACP has:
// inline base64 plus a mime type. There is no local-path content block.
func imageBlock(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mime := mimeByExtension(path)
	if mime == "" {
		return nil, fmt.Errorf("unsupported image type %q", filepath.Ext(path))
	}
	return map[string]any{
		"type":     "image",
		"mimeType": mime,
		"data":     base64.StdEncoding.EncodeToString(data),
	}, nil
}

func mimeByExtension(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return ""
	}
}

// -------------------------------------------------------------- event stream

// queue states returned by next.
const (
	itemReady = iota
	turnEnded
	queueClosed
)

// eventQueue is an unbounded buffer between the connection's reader
// goroutine and the consumer of Events(). It keeps notification handling
// from blocking on a slow — or absent — consumer, which would deadlock the
// JSON-RPC reader. It also carries the end-of-turn mark, so the pump knows
// when everything the turn produced has been handed over.
type eventQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	items   []provider.Event
	turnEnd bool
	closed  bool
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

func (q *eventQueue) endTurn() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.turnEnd = true
	q.cond.Signal()
}

func (q *eventQueue) clearTurnEnd() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.turnEnd = false
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

// next blocks until there is an event to deliver, the turn has ended and
// everything it produced has been drained, or the queue is closed.
func (q *eventQueue) next() (provider.Event, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed && !q.turnEnd {
		q.cond.Wait()
	}
	if len(q.items) > 0 {
		ev := q.items[0]
		q.items = q.items[1:]
		return ev, itemReady
	}
	if q.closed {
		return provider.Event{}, queueClosed
	}
	return provider.Event{}, turnEnded
}

// pump moves queued events onto the Events channel, and runs the
// end-of-turn barrier between turns. It gives up as soon as abort closes
// s.stopped: a session whose Start failed is handed to nobody, so there
// will never be a reader and a blocked send would leak this goroutine.
func (s *session) pump() {
	defer close(s.events)
	defer close(s.driverDone)
	defer close(s.pumpDone)
	for {
		ev, state := s.queue.next()
		switch state {
		case itemReady:
			select {
			case s.events <- ev:
			case <-s.stopped:
				return
			}
		case turnEnded:
			if !s.barrier() {
				return
			}
		default:
			return
		}
	}
}

// barrier holds the session open after a turn ends, so the runner's schema
// retry has somewhere to land, and reports whether a follow-up arrived.
//
// The problem it solves is the runner's shape: it drains Events() to its
// close before it calls Wait, and it calls Send from inside that drain,
// while handling the final event. Ending the stream the moment the turn
// completes would therefore refuse every retry — which is what the Codex
// adapter does, and why it has to resume a whole fresh session instead —
// and never ending it would hang the run.
//
// So the session sends one last event and treats it as a barrier. The
// channel is unbuffered, so that send can only complete once the caller has
// come back for another event, which is strictly after it finished with the
// final and therefore after any Send it was going to make. A send that
// arrives in that window is picked up here; the re-check after the barrier
// covers the case where both were ready at once.
func (s *session) barrier() bool {
	if text, ok := s.takeSend(); ok {
		return s.resume(text)
	}

	s.mu.Lock()
	turns := s.turns
	s.mu.Unlock()
	ended := provider.Event{
		Kind: provider.EvSystem,
		At:   time.Now(),
		Text: sessionEnded,
		Raw:  rawOf(map[string]any{"sessionEnded": true, "turns": turns}),
	}
	select {
	case text := <-s.sendCh:
		return s.resume(text)
	case s.events <- ended:
	case <-s.stopped:
		return false
	case <-s.closing:
		return false
	}

	if text, ok := s.takeSend(); ok {
		return s.resume(text)
	}
	// Past the last retry: the session is over, and whatever the empty
	// turns were held back for is now certain either way.
	s.unanswered()
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

// resume hands a follow-up message to the turn driver and re-arms the
// queue for the turn it starts.
func (s *session) resume(text string) bool {
	s.queue.clearTurnEnd()
	select {
	case s.turnCh <- text:
		return true
	case <-s.stopped:
		return false
	case <-s.closing:
		return false
	}
}

func (s *session) emit(ev provider.Event) {
	ev.At = time.Now()
	if ev.Raw == nil {
		ev.Raw = json.RawMessage(`{}`)
	}
	s.queue.push(ev)
}

// ------------------------------------------------------------ wire → events

// sessionUpdate is the subset of an ACP session/update notification this
// adapter reads. Every variant is discriminated by sessionUpdate.
type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       contentBlock    `json:"content"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"`
	RawInput      json.RawMessage `json:"rawInput"`
	RawOutput     json.RawMessage `json:"rawOutput"`
	Locations     json.RawMessage `json:"locations"`
	Entries       json.RawMessage `json:"entries"`
	ModeID        string          `json:"modeId"`

	// available_commands_update's payload: the agent's whole slash-command
	// catalogue, re-sent in full every time any of it changes. Only the
	// names are read; see onAvailableCommands for why none of it is kept.
	AvailableCommands []struct {
		Name string `json:"name"`
	} `json:"availableCommands"`

	// usage_update, whose shipped shape carries context used/size and an
	// optional session cost. Some agents nest it under "usage"; both are
	// read because the RFD's own examples show the flat form and the
	// schema page was not explicit.
	Used  int64      `json:"used"`
	Size  int64      `json:"size"`
	Cost  *costBlock `json:"cost"`
	Usage *struct {
		Used int64      `json:"used"`
		Size int64      `json:"size"`
		Cost *costBlock `json:"cost"`
	} `json:"usage"`
}

// trackedCall is what the session remembers about one tool call between
// its tool_call, its permission request and its tool_call_update.
type trackedCall struct {
	name  string
	kind  string
	title string
	asked bool
	// paths is every destination the call has named so far. A
	// tool_call_update carries only what changed, so a write's path
	// usually arrives once, on the tool_call, and the completion that has
	// to be judged on it carries nothing.
	paths []string
}

type costBlock struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *session) onNotify(method string, params json.RawMessage) {
	raw := envelope(nil, method, params)
	if method != "session/update" {
		s.emit(provider.Event{Kind: provider.EvSystem, Text: method, Raw: raw})
		return
	}

	var payload struct {
		SessionID string        `json:"sessionId"`
		Update    sessionUpdate `json:"update"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}
	if !s.owns(payload.SessionID) {
		if !s.sessionKnown() {
			// Arrived before session/new returned: this side has no
			// session id yet to check the notification against, so
			// nothing about it can be trusted as belonging to the run
			// that was asked for.
			s.preOpenTraffic("session/update", raw)
			return
		}
		// A nested subagent session's traffic, which belongs to a turn
		// this run did not ask for and must not be folded into its
		// transcript — least of all its agent_message_chunks, which are
		// what the run's answer is parsed out of.
		return
	}
	u := payload.Update

	switch u.SessionUpdate {
	case "agent_message_chunk":
		if u.Content.Text == "" {
			return
		}
		s.mu.Lock()
		s.chunks.WriteString(u.Content.Text)
		s.mu.Unlock()
		s.emit(provider.Event{Kind: provider.EvAssistantText, Text: u.Content.Text, Raw: raw})

	case "agent_thought_chunk":
		if u.Content.Text == "" {
			return
		}
		s.emit(provider.Event{Kind: provider.EvSystem, Text: u.Content.Text, Raw: raw})

	case "tool_call":
		name := displayName(u.Kind, u.Title)
		s.track(u.ToolCallID, name, u.Kind, u.Title, updatePaths(u))
		s.emit(provider.Event{
			Kind:  provider.EvToolStarted,
			Tool:  name,
			Input: toolInput(u),
			Raw:   raw,
		})

	case "tool_call_update":
		if u.Status != "completed" && u.Status != "failed" {
			return
		}
		call := s.track(u.ToolCallID, displayName(u.Kind, u.Title), u.Kind, u.Title, updatePaths(u))
		s.emit(provider.Event{
			Kind:  provider.EvToolFinished,
			Tool:  call.name,
			Input: toolInput(u),
			Raw:   raw,
		})
		if u.Status == "completed" {
			if ev := s.observeUnmediated(call, u, raw); ev != nil {
				s.emit(*ev)
			}
		}

	case "plan":
		s.emit(provider.Event{Kind: provider.EvSystem, Text: "acp plan", Input: u.Entries, Raw: raw})

	case "usage_update":
		s.onUsage(u, raw)

	case "available_commands_update":
		s.onAvailableCommands(u)

	case "current_mode_update":
		s.emit(provider.Event{Kind: provider.EvSystem, Text: "acp mode " + u.ModeID, Raw: raw})

	default:
		s.emit(provider.Event{Kind: provider.EvSystem, Text: "acp " + u.SessionUpdate, Raw: raw})
	}
}

// onAvailableCommands records that the agent's slash-command catalogue
// arrived, and nothing else about it.
//
// The notification carries the whole catalogue every time, descriptions
// included — OpenCode's is the host's 31 skills, some 15 KiB a copy, and it
// re-sends the lot on every update. Kept verbatim, one run's event log
// reached 1300-odd system events made almost entirely of the same text.
// None of it is anything the run acts on: Sirdar never invokes a
// slash-command, and what an operator needs from this line is that the
// catalogue is there and roughly what is in it.
func (s *session) onAvailableCommands(u sessionUpdate) {
	const named = 3
	names := make([]string, 0, named)
	for _, cmd := range u.AvailableCommands {
		if len(names) == named {
			break
		}
		if cmd.Name != "" {
			names = append(names, cmd.Name)
		}
	}

	text := fmt.Sprintf("acp available commands: %d", len(u.AvailableCommands))
	if len(names) > 0 {
		listed := strings.Join(names, ", ")
		if len(u.AvailableCommands) > len(names) {
			listed += ", …"
		}
		text += " (" + listed + ")"
	}
	s.emit(provider.Event{
		Kind: provider.EvSystem,
		Text: text,
		Raw:  rawOf(map[string]any{"availableCommands": len(u.AvailableCommands), "first": names}),
	})
}

// onUsage records what little ACP reports. "used" is the tokens currently
// in the agent's context, not the run's cumulative input, and it is the
// only token figure the protocol has; it is reported as InputTok because
// that is the field a Sirdar run displays, and the run's own budget is
// enforced on turns and minutes regardless. A cost is only believed in
// USD, since budget.maxUsd is a dollar figure.
func (s *session) onUsage(u sessionUpdate, raw json.RawMessage) {
	used, size, cost := u.Used, u.Size, u.Cost
	if u.Usage != nil {
		used, size, cost = u.Usage.Used, u.Usage.Size, u.Usage.Cost
	}

	s.mu.Lock()
	if used > 0 {
		s.usage.in = used
	}
	if cost != nil && strings.EqualFold(cost.Currency, "USD") {
		s.usage.cost = cost.Amount
	}
	turns, in, spent := s.turns, s.usage.in, s.usage.cost
	s.mu.Unlock()

	_ = size
	s.emit(provider.Event{
		Kind:     provider.EvUsage,
		Turns:    turns,
		InputTok: in,
		CostUSD:  spent,
		Raw:      raw,
	})
}

// owns reports whether a session-scoped message belongs to this session.
// An empty id on the wire is taken as this session's, since an agent that
// omits the field has only one — but only once this side has a session id
// of its own to check against. Before that (between sending session/new and
// its response carrying the id) nothing legitimate is session-scoped yet:
// session/load is the one case that starts sooner, and it records the id
// before making the call, so it is unaffected. An empty s.sessionID here
// therefore means every session-scoped message is refused or dropped, not
// waved through.
func (s *session) owns(sessionID string) bool {
	s.mu.Lock()
	mine := s.sessionID
	s.mu.Unlock()
	if mine == "" {
		return false
	}
	return sessionID == "" || mine == sessionID
}

// sessionKnown reports whether this side has recorded a session id yet.
// Used alongside owns to tell "not open yet" apart from "belongs to some
// other session" when deciding whether to count and warn about dropped
// traffic.
func (s *session) sessionKnown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID != ""
}

// preOpenTraffic records session-scoped agent traffic that arrived before
// this side had a session id to check ownership against — traffic that, in
// ACP, has no legitimate source, since nothing session-scoped is supposed
// to arrive before session/new returns. The first occurrence is surfaced as
// an EvError so it is visible in events.jsonl; later ones are only counted,
// so a chatty or misbehaving agent cannot flood the log.
func (s *session) preOpenTraffic(method string, raw json.RawMessage) {
	s.mu.Lock()
	s.preOpenDropped++
	first := s.preOpenDropped == 1
	s.mu.Unlock()
	if !first {
		return
	}
	s.emit(provider.Event{
		Kind: provider.EvError,
		Text: "acp: the agent sent " + method + " before session/new returned; " +
			"session-scoped traffic cannot be verified until this side has a session id, " +
			"so it is refused or dropped",
		Raw: raw,
	})
}

// track records what is known about a tool call and returns the merged
// state. A tool_call_update carries only the fields that changed, so the
// kind and the title usually arrive once, on the tool_call, and everything
// after that has to be looked up by id.
func (s *session) track(id, name, kind, title string, paths []string) trackedCall {
	if id == "" {
		return trackedCall{name: name, kind: kind, title: title, paths: paths}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	call, ok := s.calls[id]
	if !ok {
		call = &trackedCall{}
		s.calls[id] = call
	}
	if kind != "" {
		call.kind = kind
	}
	if title != "" {
		call.title = title
	}
	if name != "" && (call.name == "" || call.name == "tool") {
		call.name = name
	}
	if call.name == "" {
		call.name = name
	}
	call.paths = mergePaths(call.paths, paths)
	return *call
}

// mergePaths adds the paths a later update named to the ones already
// known, without repeating one.
func mergePaths(have, add []string) []string {
	for _, p := range add {
		seen := false
		for _, h := range have {
			if h == p {
				seen = true
				break
			}
		}
		if !seen {
			have = append(have, p)
		}
	}
	return have
}

// markAsked records that a tool call went through
// session/request_permission, so a completed write that did not can be
// told apart from one that did.
func (s *session) markAsked(id, kind string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	call, ok := s.calls[id]
	if !ok {
		call = &trackedCall{kind: kind}
		s.calls[id] = call
	}
	call.asked = true
}

// displayName is what the event log and the progress view call a tool.
// ACP's kind is the coarse, portable label (read, edit, execute); the title
// is agent-authored prose and is the fallback.
func displayName(kind, title string) string {
	if kind != "" {
		return kind
	}
	if title != "" {
		return title
	}
	return "tool"
}

// toolInput is what the event carries as the call's arguments: rawInput
// where the agent sent it, and the file locations it named otherwise, which
// is all several agents report for a read.
func toolInput(u sessionUpdate) json.RawMessage {
	if len(u.RawInput) > 0 && !isJSONNull(u.RawInput) {
		return u.RawInput
	}
	if len(u.Locations) > 0 && !isJSONNull(u.Locations) {
		return u.Locations
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// ------------------------------------------------------- agent → client RPC

// permissionRequest is ACP's session/request_permission payload.
type permissionRequest struct {
	SessionID string `json:"sessionId"`
	ToolCall  struct {
		ToolCallID string          `json:"toolCallId"`
		Title      string          `json:"title"`
		Kind       string          `json:"kind"`
		RawInput   json.RawMessage `json:"rawInput"`
		Locations  []struct {
			Path string `json:"path"`
		} `json:"locations"`
	} `json:"toolCall"`
	Options []struct {
		OptionID string `json:"optionId"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
	} `json:"options"`
}

// onRequest answers the agent-to-client requests an ACP agent can raise.
func (s *session) onRequest(id json.RawMessage, method string, params json.RawMessage) {
	raw := envelope(id, method, params)

	switch {
	case method == "session/request_permission":
		s.onPermission(id, params, raw)

	case method == "fs/read_text_file":
		s.onReadTextFile(id, params, raw)

	case method == "fs/write_text_file":
		s.refuse(id, method, "writeTextFile", params, raw)

	case strings.HasPrefix(method, "terminal/"):
		s.refuse(id, method, "terminal", params, raw)

	default:
		_ = s.conn.replyError(id, codeMethodNotFound, "sirdar does not implement "+method)
		s.emit(provider.Event{Kind: provider.EvSystem, Text: "acp: refused " + method, Raw: raw})
	}
}

// refuse declines a method whose client capability Sirdar set to false at
// initialize. A well-behaved agent never calls these; one that does is told
// so, and the attempt is recorded where an operator will see it.
func (s *session) refuse(id json.RawMessage, method, capability string, params, raw json.RawMessage) {
	_ = s.conn.replyError(id, codeMethodNotFound,
		"sirdar declined the "+capability+" capability: triage runs are read-only")
	s.emit(provider.Event{
		Kind:     provider.EvPermission,
		Decision: "deny",
		Tool:     method,
		Input:    params,
		Text:     "Sirdar policy: triage runs are read-only",
		Raw:      raw,
	})
}

// refuseSession declines a request that names a session this client did
// not open — a nested subagent session, which ACP lets an agent create for
// itself. Sirdar's policy, its workspace root and its budget describe the
// session it asked for, and none of them can be honestly applied to
// another one, so the request is answered with an error rather than
// guessed at.
func (s *session) refuseSession(id json.RawMessage, method, sessionID string, raw json.RawMessage) {
	_ = s.conn.replyError(id, codeInvalidParams,
		"sirdar serves only the session it opened; "+sessionID+" is not it")
	s.emit(provider.Event{
		Kind: provider.EvSystem,
		Text: "acp: refused " + method + " for another session (" + sessionID + ")",
		Raw:  raw,
	})
}

// onPermission answers session/request_permission from the run's
// PermissionPolicy. ACP gives the client no field to attach a reason to its
// answer — it may only pick one of the agent's own options — so the policy's
// message goes into the event log and the progress view instead, and the
// read-only instruction is already in the prompt.
func (s *session) onPermission(id json.RawMessage, params, raw json.RawMessage) {
	var req permissionRequest
	if err := json.Unmarshal(params, &req); err != nil {
		_ = s.conn.replyError(id, codeInvalidParams, "malformed session/request_permission")
		return
	}

	if !s.owns(req.SessionID) {
		if !s.sessionKnown() {
			s.preOpenTraffic("session/request_permission", raw)
		}
		s.refuseSession(id, "session/request_permission", req.SessionID, raw)
		return
	}
	s.markAsked(req.ToolCall.ToolCallID, req.ToolCall.Kind)

	tool := policyTool(req.ToolCall.Kind, req.ToolCall.Title)
	decision := s.decide(tool, req)

	want := []string{"reject_once", "reject_always"}
	verdict := "deny"
	if decision.Allow {
		want = []string{"allow_once", "allow_always"}
		verdict = "allow"
	}

	optionID := ""
	for _, kind := range want {
		for _, opt := range req.Options {
			if opt.Kind == kind {
				optionID = opt.OptionID
				break
			}
		}
		if optionID != "" {
			break
		}
	}

	if optionID == "" {
		// The agent offered nothing that matches the verdict. Cancelling
		// the request is the protocol's own way of saying "no answer",
		// and it is the safe reading of a missing reject option.
		_ = s.conn.reply(id, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
		verdict = "deny"
		if decision.Message == "" {
			decision.Message = "the agent offered no option matching Sirdar's decision"
		}
	} else {
		_ = s.conn.reply(id, map[string]any{
			"outcome": map[string]any{"outcome": "selected", "optionId": optionID},
		})
	}

	s.emit(provider.Event{
		Kind:     provider.EvPermission,
		Decision: verdict,
		Tool:     tool,
		Input:    req.ToolCall.RawInput,
		Text:     decision.Message,
		Raw:      raw,
	})
}

// writeKinds are the ACP kinds that change a file. In a fix run each one
// is decided on the destinations it named rather than on its raw
// arguments, which ACP does not standardise (see decideFixWrite).
var writeKinds = map[string]bool{"edit": true, "delete": true, "move": true}

// ------------------------------------------------- tool calls nobody approved

// observeUnmediated decides what a completed tool call that never produced
// a session/request_permission means for the run, and returns the event
// that says so — or nil when there is nothing to report.
//
// ACP leaves it to the agent to decide what is worth asking about, so this
// is the harness finding out after the fact. Nothing can be undone at that
// point. What can be decided is whether the run carries on.
//
// A read-only run — triage or rca — that sees a write, a shell command or
// a sub-agent spawn it was never asked about has had its guarantee fail,
// not bent: that is an EvBreach, which the run layer answers by cancelling
// the session and filing nothing, exactly as provider agy does when the
// Antigravity CLI reports a completed write in a plan-mode session. The
// difference between the two providers is only where the failure comes
// from — agy cannot be asked, an ACP agent could have asked and did not.
//
// A fix run is allowed to write and to run commands, so the same call is a
// warning there — but only inside the session root. A write that lands
// outside it, or one that names no destination at all, is a breach in a
// fix run too: the whole point of running a fix in a linked worktree is
// that nothing outside it is touched, and a write nobody looked at cannot
// be shown to have stayed inside.
//
// fetch stays a warning in both. It is neither a write nor a command: what
// it costs is that permissions.fetch never judged the destination, which
// is worth an operator's attention and is not the read-only guarantee
// failing.
func (s *session) observeUnmediated(call trackedCall, u sessionUpdate, raw json.RawMessage) *provider.Event {
	if call.asked {
		return nil
	}
	subagent := indicatesSubagent(call.kind, call.title) || indicatesSubagent(call.kind, call.name)
	write := writeKinds[call.kind]
	if !subagent && !write && !authoritativeKinds[call.kind] {
		return nil
	}

	fix := s.isFix()
	switch {
	case subagent:
		// Never a warning. A sub-agent is a second agent loop with its own
		// permission state, and every agent whose bundle has been read
		// starts it in the permissive mode: nothing it goes on to do
		// produces a permission request, and nothing Sirdar configured
		// reaches it. A fix run is no more able to vouch for that than a
		// triage run.
	case call.kind == "fetch":
		return s.unmediatedWarning(call, u, raw)
	case fix && !write:
		return s.unmediatedWarning(call, u, raw)
	case fix && write && s.writesWithinRoot(call):
		return s.unmediatedWarning(call, u, raw)
	case !fix:
		// Every unasked write and command in a read-only run is a breach.
	}

	subject := unmediatedSubject(call, u)
	headline := "read-only breach: " + call.name
	if subject != "" {
		headline += " " + oneLine(subject)
	}
	detail := fmt.Sprintf("the agent completed a %q tool call (%s) without ever sending "+
		"session/request_permission, so Sirdar's permission policy never saw it. ", call.kind, call.name)
	switch {
	case subagent:
		headline = "read-only breach: sub-agent spawn " + oneLine(firstNonEmpty(call.title, call.name))
		detail = "the agent spawned a sub-agent without asking. A sub-agent runs its own loop with " +
			"its own permission state — every agent whose behaviour has been captured starts one in " +
			"a mode that approves everything — so nothing it does will reach this client as a " +
			"permission request and nothing Sirdar configured applies to it. "
	case fix:
		detail += "A fix session may write, but only inside its own worktree, and this call " +
			"cannot be shown to have stayed there. "
	}
	detail += "Check the agent's own permission configuration — Sirdar can neither see nor override it."

	return &provider.Event{
		Kind:  provider.EvBreach,
		Text:  headline + "\n" + detail,
		Tool:  call.name,
		Input: toolInput(u),
		Raw:   raw,
	}
}

// unmediatedWarning is the report for a completed call that asked nobody
// but did not break the run's guarantee: a fetch, or a fix session's own
// in-worktree write.
func (s *session) unmediatedWarning(call trackedCall, u sessionUpdate, raw json.RawMessage) *provider.Event {
	named := call.name
	if subject := unmediatedSubject(call, u); subject != "" {
		named += " " + oneLine(subject)
	}
	return &provider.Event{
		Kind: provider.EvError,
		Text: fmt.Sprintf("acp: the agent completed a %q tool call (%s) without asking permission; "+
			"this agent does not route that kind through session/request_permission, "+
			"so Sirdar's permission policy could not be applied to it", call.kind, named),
		Tool:  call.name,
		Input: toolInput(u),
		Raw:   raw,
	}
}

// writesWithinRoot reports whether every destination a write named sits
// inside the session root. A write that named none answers false: where it
// went is unknown, and unknown is not inside.
func (s *session) writesWithinRoot(call trackedCall) bool {
	if len(call.paths) == 0 {
		return false
	}
	root := s.spec.Cwd
	if strings.TrimSpace(root) == "" {
		return false
	}
	for _, path := range call.paths {
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, abs)
		}
		if !withinRoot(root, abs) {
			return false
		}
	}
	return true
}

// unmediatedSubject is the path or command a breach headline names, so an
// operator reading the run's terminal reason sees what was done and not
// only that something was.
func unmediatedSubject(call trackedCall, u sessionUpdate) string {
	if len(call.paths) > 0 {
		return strings.Join(call.paths, ", ")
	}
	var args struct {
		Command  json.RawMessage `json:"command"`
		Commands []string        `json:"commands"`
	}
	if len(u.RawInput) > 0 && !isJSONNull(u.RawInput) {
		_ = json.Unmarshal(u.RawInput, &args)
	}
	if len(args.Command) > 0 && !isJSONNull(args.Command) {
		if command, err := provider.UnwrapCommand(args.Command); err == nil {
			return command
		}
	}
	if len(args.Commands) > 0 {
		return strings.Join(args.Commands, "; ")
	}
	return firstNonEmpty(call.title, "")
}

// subagentIdents are the tool names agents give the call that starts a
// second agent loop: Claude Code's Task, kimi's Agent and AgentSwarm, and
// the spawn_* family other agents use.
var subagentIdents = map[string]bool{
	"agent": true, "agentswarm": true, "agent_swarm": true,
	"task": true, "subagent": true, "subagenttask": true,
}

// indicatesSubagent reports whether a tool call starts a sub-agent.
//
// ACP's kind enum has no value for this — a sub-agent spawn arrives as
// `other`, or as `execute`, or with no kind at all — so the agent's own
// title is the only signal, and a title is agent-authored prose. Matching
// it on a substring would fail a run over "update the task list", so only
// the leading identifier is compared: "Task", "Agent(subagent_type=…)",
// "AgentSwarm", "spawn_worker", and the mcp__server__task spelling, but
// not a sentence that happens to contain one of those words.
func indicatesSubagent(kind, title string) bool {
	for _, field := range []string{kind, title} {
		id := leadingIdent(field)
		if id == "" {
			continue
		}
		if subagentIdents[id] || strings.HasPrefix(id, "spawn") {
			return true
		}
	}
	return false
}

// leadingIdent lowercases a tool name and returns its first identifier:
// everything up to the first character that is not a letter, digit or
// underscore. An mcp__ name is reduced to its last segment first, since
// that is the tool's own name within its server.
func leadingIdent(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	for i, r := range name {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return name[:i]
	}
	return name
}

// updatePaths lifts the destinations a session/update named, from
// locations first and the file-naming arguments agents use otherwise —
// the same places requestPaths reads them from on a permission request.
func updatePaths(u sessionUpdate) []string {
	var out []string
	if len(u.Locations) > 0 && !isJSONNull(u.Locations) {
		var locations []struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(u.Locations, &locations); err == nil {
			for _, loc := range locations {
				if strings.TrimSpace(loc.Path) != "" {
					out = append(out, loc.Path)
				}
			}
		}
	}
	if len(out) > 0 {
		return out
	}

	var args struct {
		FileName     string `json:"fileName"`
		FilePath     string `json:"file_path"`
		FilePathCC   string `json:"filePath"`
		Path         string `json:"path"`
		NotebookPath string `json:"notebook_path"`
		OldPath      string `json:"oldPath"`
		NewPath      string `json:"newPath"`
	}
	if len(u.RawInput) > 0 && !isJSONNull(u.RawInput) {
		_ = json.Unmarshal(u.RawInput, &args)
	}
	for _, p := range []string{
		args.FileName, args.FilePath, args.FilePathCC, args.Path,
		args.NotebookPath, args.OldPath, args.NewPath,
	} {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// subjectMax is how much of a path or command a one-line report carries.
const subjectMax = 120

// oneLine collapses a subject onto one line and caps it, so a breach
// headline stays the single line the run records as its reason. A subject
// too long to fit loses its middle rather than its tail: a temporary
// worktree path is mostly prefix, and the file name at the end is the part
// an operator is reading for.
func oneLine(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " "))
	if len(text) <= subjectMax {
		return text
	}
	head := subjectMax / 3
	tail := subjectMax - head - 3
	return text[:head] + "..." + text[len(text)-tail:]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// decide judges one permission request.
//
// Two kinds carry arguments in a shape PermissionPolicy cannot read off
// the wire, so they are unpacked first: an execute's command may be an
// argv or a shell wrapper, and a write names its destination in
// toolCall.locations, which no ACP agent is obliged to repeat under a
// field name the policy knows. Everything else goes to the policy as it
// arrived, which is also what a triage run does with a write: the kind
// maps to Write, Write is AlwaysDenied outside a fix, and the answer is
// the read-only refusal.
func (s *session) decide(tool string, req permissionRequest) provider.Decision {
	if s.spec.Policy == nil {
		return provider.Decision{Allow: false, Message: "Sirdar policy: no permission policy is configured"}
	}
	switch {
	case req.ToolCall.Kind == "execute":
		return s.decideExecute(tool, req)
	case writeKinds[req.ToolCall.Kind] && s.spec.Policy.IsFix():
		return s.decideFixWrite(tool, req)
	}
	return s.spec.Policy.Decide(tool, req.ToolCall.RawInput)
}

// decideExecute judges a shell call on the command it would actually run.
// The agent may send "command" as a line or as an argv, and either may be
// a login-shell wrapper around the real script, so it goes through the
// same UnwrapCommand the Codex path uses; a "commands" list without a
// "command" is judged entry by entry, and every entry has to pass, because
// the approval covers the lot.
func (s *session) decideExecute(tool string, req permissionRequest) provider.Decision {
	var args struct {
		Command  json.RawMessage `json:"command"`
		Commands []string        `json:"commands"`
	}
	if len(req.ToolCall.RawInput) > 0 {
		_ = json.Unmarshal(req.ToolCall.RawInput, &args)
	}

	fields := []json.RawMessage{}
	if len(args.Command) > 0 && !isJSONNull(args.Command) {
		fields = append(fields, args.Command)
	} else {
		for _, line := range args.Commands {
			raw, err := json.Marshal(line)
			if err != nil {
				continue
			}
			fields = append(fields, raw)
		}
	}
	if len(fields) == 0 {
		// Nothing this reader recognises as a command. The raw arguments
		// go to the policy, which refuses a call naming no command at all
		// and says so in its own words.
		return s.spec.Policy.Decide(tool, req.ToolCall.RawInput)
	}

	for _, field := range fields {
		command, err := provider.UnwrapCommand(field)
		if err != nil {
			return provider.Decision{Allow: false, Message: "Sirdar policy: " + err.Error()}
		}
		input, _ := json.Marshal(map[string]string{"command": command})
		if d := s.spec.Policy.Decide(tool, input); !d.Allow {
			return d
		}
	}
	return provider.Decision{Allow: true}
}

// decideFixWrite judges a fix run's edit, delete or move on where it would
// write, through the same PermissionPolicy.decideWrite a Claude fix's Edit
// goes through: the path is resolved through symlinks, anything outside
// the run's worktree is refused, and so is anything inside .git/,
// .sirdar/ or the repository's hooks directory.
//
// The destinations come from toolCall.locations, which is the one place
// ACP defines for them, and from the argument names agents use when they
// send locations with the tool_call but not with the approval. A request
// that names none is denied: an accept would be a write nobody looked at.
// Every path has to pass, since one approval covers the whole call.
func (s *session) decideFixWrite(tool string, req permissionRequest) provider.Decision {
	paths := requestPaths(req)
	if len(paths) == 0 {
		kind := req.ToolCall.Kind
		return provider.Decision{Allow: false, Message: "Sirdar policy: this " + kind +
			" named no path, in its locations or in its arguments, so where it would write cannot be checked"}
	}
	for _, path := range paths {
		input, _ := json.Marshal(map[string]string{"file_path": path})
		if d := s.spec.Policy.Decide(tool, input); !d.Allow {
			return d
		}
	}
	return provider.Decision{Allow: true}
}

// requestPaths lifts the destinations out of a permission request:
// toolCall.locations first, and the file-naming arguments agents send
// otherwise. rawInput that is not an object — apply_patch sends the patch
// text itself — yields nothing, which decideFixWrite turns into a denial.
func requestPaths(req permissionRequest) []string {
	var out []string
	for _, loc := range req.ToolCall.Locations {
		if strings.TrimSpace(loc.Path) != "" {
			out = append(out, loc.Path)
		}
	}
	if len(out) > 0 {
		return out
	}

	var args struct {
		FileName     string `json:"fileName"`
		FilePath     string `json:"file_path"`
		FilePathCC   string `json:"filePath"`
		Path         string `json:"path"`
		NotebookPath string `json:"notebook_path"`
		OldPath      string `json:"oldPath"`
		NewPath      string `json:"newPath"`
	}
	if len(req.ToolCall.RawInput) > 0 {
		_ = json.Unmarshal(req.ToolCall.RawInput, &args)
	}
	for _, p := range []string{
		args.FileName, args.FilePath, args.FilePathCC, args.Path,
		args.NotebookPath, args.OldPath, args.NewPath,
	} {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// kindTools maps ACP's portable tool kinds onto the names
// PermissionPolicy already judges, so one workspace's permissions.bash and
// permissions.mcp rules mean the same thing whichever agent is driving.
// ACP does not name the agent's own tool, only what sort of thing it is,
// so this is the whole of what a policy has to go on.
var kindTools = map[string]string{
	"read":    "Read",
	"edit":    "Write",
	"delete":  "Write",
	"move":    "Write",
	"search":  "Grep",
	"execute": "Bash",
	"fetch":   "WebFetch",
	"think":   "TodoWrite",
}

// authoritativeKinds are the kinds whose ACP kind decides the name the
// policy judges, and which cannot be talked out of by a title. The title is
// agent-authored text, so an agent that titles an edit
// "mcp__editor__apply_diff" would otherwise route a write through
// permissions.mcp, where a read-shaped name is allowed by default — a
// self-chosen string deciding whether a write is permitted.
//
// "fetch" is on the list for the same reason as the four writes. A fetch
// titled "mcp__browser__get_page" would otherwise be judged by
// permissions.mcp, whose heuristic reads "get" as a read and allows it,
// and the destination would never be looked at. Under the kind it goes to
// WebFetch and through permissions.fetch, which is the whole point of
// having the list.
var authoritativeKinds = map[string]bool{
	"edit": true, "delete": true, "move": true, "execute": true, "fetch": true,
}

// policyTool is the name the permission policy judges.
//
// A kind whose meaning is settled by the protocol (authoritativeKinds: the
// four writes and fetch) wins outright. Otherwise an MCP tool names itself
// in full and is passed through, so mcp__ rules apply; then the kind's
// Sirdar equivalent; and a kind with no equivalent (switch_mode, other, or
// none at all) falls back to the agent's title, which the policy denies —
// the read-only posture applied to a tool call whose nature the protocol
// did not state.
func policyTool(kind, title string) string {
	if authoritativeKinds[kind] {
		return kindTools[kind]
	}
	if strings.HasPrefix(title, "mcp__") {
		return title
	}
	if name, ok := kindTools[kind]; ok {
		return name
	}
	if title != "" {
		return title
	}
	if kind != "" {
		return kind
	}
	return "unknown"
}

// onReadTextFile serves fs/read_text_file, the one filesystem capability
// Sirdar advertises, for files inside the workspace only. A path outside it
// is refused: the agent is investigating this workspace, and an agent that
// asks the client to read ~/.aws/credentials is asking the client to hand
// it over.
func (s *session) onReadTextFile(id json.RawMessage, params, raw json.RawMessage) {
	var req struct {
		SessionID string `json:"sessionId"`
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Limit     int    `json:"limit"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		_ = s.conn.replyError(id, codeInvalidParams, "malformed fs/read_text_file")
		return
	}
	if !s.owns(req.SessionID) {
		if !s.sessionKnown() {
			s.preOpenTraffic("fs/read_text_file", raw)
		}
		s.refuseSession(id, "fs/read_text_file", req.SessionID, raw)
		return
	}

	if !withinRoot(s.spec.Cwd, req.Path) {
		_ = s.conn.replyError(id, codeInvalidParams,
			"sirdar serves fs/read_text_file inside the workspace only: "+req.Path+" is outside it")
		s.emit(provider.Event{
			Kind:     provider.EvPermission,
			Decision: "deny",
			Tool:     "fs/read_text_file",
			Input:    params,
			Text:     "Sirdar policy: " + req.Path + " is outside the workspace root",
			Raw:      raw,
		})
		return
	}

	// Off the reader goroutine: this handler runs on the one goroutine
	// reading the agent's stdout, and a read off a slow disk — or a
	// network mount — would stall every notification behind it, including
	// the response to the session/prompt that is open at the time.
	go s.serveReadTextFile(id, req.Path, req.Line, req.Limit, params, raw)
}

// serveReadTextFile answers one in-workspace read. Writes are serialised by
// the connection, so replying from here is safe.
func (s *session) serveReadTextFile(id json.RawMessage, path string, line, limit int, params, raw json.RawMessage) {
	content, err := readTextFile(path)
	if err != nil {
		_ = s.conn.replyError(id, codeInvalidParams, err.Error())
		return
	}
	_ = s.conn.reply(id, map[string]any{"content": sliceLines(content, line, limit)})
	s.emit(provider.Event{
		Kind:  provider.EvToolFinished,
		Tool:  "fs/read_text_file",
		Input: params,
		Raw:   raw,
	})
}

// maxReadBytes caps what one fs/read_text_file may return. The response is
// a single JSON-RPC line the agent has to hold in memory and then feed to a
// model, so a multi-gigabyte log answered in full helps nobody and can take
// the agent down with it. The refusal names the size so the agent can ask
// again with line and limit — which this handler honours.
const maxReadBytes = 8 << 20

func readTextFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// One byte over the cap is read on purpose: it is the difference
	// between a file that fits and one that was truncated.
	data, err := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxReadBytes {
		return "", fmt.Errorf("%s is larger than the %d MiB fs/read_text_file limit; ask again with line and limit",
			path, maxReadBytes>>20)
	}
	return string(data), nil
}

// sliceLines applies ACP's optional line (1-based start) and limit (max
// lines) arguments. Zero means "from the beginning" and "all of it".
func sliceLines(content string, line, limit int) string {
	if line <= 1 && limit <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if line > 1 {
		if line-1 >= len(lines) {
			return ""
		}
		lines = lines[line-1:]
	}
	if limit > 0 && limit < len(lines) {
		lines = lines[:limit]
	}
	return strings.Join(lines, "\n")
}

// withinRoot reports whether path is an absolute path inside root, with
// both sides resolved through symlinks first.
//
// Resolving is the answer, not the fallback. A lexical comparison says yes
// to <cwd>/link/id_rsa where link is a symlink the agent itself could have
// created pointing at the home directory, which is the whole of what this
// check exists to refuse. Resolving also settles the honest disagreements
// in the other direction — macOS's /var is a symlink to /private/var, so a
// workspace under a temporary directory has two names — and admits those.
//
// The lexical comparison is only reached when a path cannot be resolved at
// all (a root that has gone away, a candidate with no existing ancestor),
// where refusing outright would be the wrong answer for the common case of
// a file the agent is about to be told does not exist.
func withinRoot(root, path string) bool {
	if root == "" || path == "" || !filepath.IsAbs(path) {
		return false
	}
	realRoot, rootErr := filepath.EvalSymlinks(root)
	realPath, pathErr := resolveCandidate(path)
	if rootErr == nil && pathErr == nil {
		return lexicallyWithin(realRoot, realPath)
	}
	return lexicallyWithin(root, path)
}

// resolveCandidate resolves path through symlinks. A file that does not
// exist yet cannot be resolved, so the nearest existing ancestor is
// resolved instead and the remainder rebuilt on top of it — which still
// follows every symlink in the part of the path that does exist.
func resolveCandidate(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	dir, rest := filepath.Clean(path), ""
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("acp: no existing ancestor of %s", path)
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest), nil
		}
	}
}

func lexicallyWithin(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// envelope rebuilds a message as it arrived, for Event.Raw.
func envelope(id json.RawMessage, method string, params json.RawMessage) json.RawMessage {
	if len(params) == 0 {
		params = json.RawMessage("null")
	}
	if hasID(id) {
		return json.RawMessage(fmt.Sprintf(`{"id":%s,"method":%q,"params":%s}`,
			strings.TrimSpace(string(id)), method, params))
	}
	return json.RawMessage(fmt.Sprintf(`{"method":%q,"params":%s}`, method, params))
}

func rawOf(v any) json.RawMessage {
	body, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return body
}

// ------------------------------------------------------------- stderr buffer

// stderrTail keeps the last stderrTailLines lines the agent wrote to
// stderr. It is the only account of why an agent died badly — an npx
// package that does not exist, a CLI that is not logged in — so it is kept
// whether or not the session failed.
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

// ------------------------------------------------------------------- doctor

// Doctor starts the configured agent, initializes it, and reports what it
// said about itself: its name and version, the protocol version it agreed
// to, and the capabilities that decide what a run can do with it —
// loadSession for resume, and image prompts for screenshot attachments.
// Then it shuts the agent down again.
func (p *Provider) Doctor(ctx context.Context, binary string) []provider.Check {
	command, args := p.cfg.Command, p.cfg.Args
	if binary != "" {
		command = binary
	}
	if strings.TrimSpace(command) == "" {
		return []provider.Check{{Name: "acp agent", Detail: "acp.command is not set"}}
	}

	label := strings.TrimSpace(command + " " + strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()

	init, err := probe(ctx, command, args, childEnv(nil, p.cfg.Env), workingDir())
	if err != nil {
		return []provider.Check{{Name: "acp agent", Detail: label + ": " + err.Error()}}
	}

	caps := []string{fmt.Sprintf("loadSession=%t (resume)", init.AgentCapabilities.LoadSession)}
	caps = append(caps, fmt.Sprintf("image prompts=%t", init.AgentCapabilities.PromptCapabilities.Image))
	if len(init.AuthMethods) > 0 {
		names := make([]string, 0, len(init.AuthMethods))
		for _, m := range init.AuthMethods {
			names = append(names, m.ID)
		}
		caps = append(caps, "authMethods="+strings.Join(names, "|"))
	}

	return []provider.Check{
		{Name: "acp agent", OK: true, Detail: label + ": " + agentLabel(init)},
		{Name: "acp capabilities", OK: true, Detail: strings.Join(caps, ", ")},
	}
}

// probe runs one initialize exchange against a freshly spawned agent and
// kills it again.
func probe(ctx context.Context, command string, args []string, env []string, dir string) (initializeResult, error) {
	var init initializeResult

	cmd := exec.Command(command, args...)
	// The same environment and working directory a run would give the
	// agent, so doctor answers the question a run will ask rather than a
	// neighbouring one: an agent that finds its login from acp.env, or its
	// config from the directory it starts in, must be probed with both.
	cmd.Env = env
	cmd.Dir = dir
	setpgid(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return init, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return init, err
	}
	var tail stderrTail
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return init, err
	}
	if err := cmd.Start(); err != nil {
		return init, err
	}

	c := newConn(stdout, stdin)
	c.onNotify = func(string, json.RawMessage) {}
	c.onRequest = func(id json.RawMessage, method string, _ json.RawMessage) {
		_ = c.replyError(id, codeMethodNotFound, "sirdar does not implement "+method)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.run()
	}()
	go tail.readFrom(stderr)

	defer func() {
		_ = c.closeWrite()
		reaped := make(chan struct{})
		go func() {
			<-done
			_ = cmd.Wait()
			close(reaped)
		}()
		if !waitFor(reaped, shutdownGrace) {
			killGroup(cmd)
			waitFor(reaped, shutdownGrace)
		}
	}()

	type outcome struct {
		raw json.RawMessage
		err error
	}
	res := make(chan outcome, 1)
	go func() {
		raw, err := c.call("initialize", map[string]any{
			"protocolVersion":    protocolVersion,
			"clientCapabilities": clientCapabilities(),
			"clientInfo": map[string]string{
				"name":    "sirdar",
				"title":   "Sirdar",
				"version": clientVersion,
			},
		})
		res <- outcome{raw, err}
	}()

	select {
	case got := <-res:
		if got.err != nil {
			return init, withTail(got.err, &tail)
		}
		if err := json.Unmarshal(got.raw, &init); err != nil {
			return init, fmt.Errorf("initialize result: %w", err)
		}
		return init, nil
	case <-ctx.Done():
		return init, withTail(errors.New("the agent did not answer initialize"), &tail)
	}
}

// workingDir is the directory doctor starts the agent in. Provider.Doctor
// is not handed the workspace root — the provider contract gives it only a
// binary — so this is Sirdar's own directory, which is the workspace root
// for a doctor run invoked there.
func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// withTail adds the agent's last stderr line to an error, which is where
// "command not found" and "not logged in" actually appear.
func withTail(err error, tail *stderrTail) error {
	lines := tail.lines()
	if len(lines) == 0 {
		return err
	}
	return fmt.Errorf("%w (%s)", err, lines[len(lines)-1])
}
