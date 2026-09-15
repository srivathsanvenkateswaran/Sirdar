// Package cursor adapts the Cursor Agent CLI (`cursor-agent -p` with
// stream-json on stdout) to the provider.Session contract: it starts the
// process, turns each output line into a provider.Event, and enforces what
// read-only means for a CLI that has no permission channel.
//
// Cursor is the odd one out among Sirdar's providers, and three findings
// from docs/research/11-cursor-wire-formats.md shape this file:
//
//   - Print mode is its own approver. `-p`'s own help says it has "access
//     to all tools, including write and shell", and a live default-mode
//     turn wrote a file and ran a shell command with no approval prompt on
//     the stream and no TTY to answer one. There is no control_request
//     channel and no permission hook Sirdar can install without writing
//     into the operator's repository, so PermissionPolicy is never
//     consulted: the read-only guarantee is the tool set and the execution
//     mode instead, both enforced by Cursor's backend rather than here.
//     What Sirdar can still do is notice that the guarantee failed — a
//     completed edit or shell call becomes provider.EvBreach, which ends
//     the run without a note (see breachOf in stream.go).
//   - There is no --json-schema and no structured_output on the result
//     line. The schema goes in the prompt and the answer is read out of the
//     result text, leniently; a text carrying no JSON object is what makes
//     the runner retry.
//   - There is no stdin protocol, so a session takes exactly one user
//     message. Send always fails and the runner carries the schema retry
//     into a --resume of the handle, the way it does for qwen.
//
// Fix mode is refused outright. Cursor's sandbox confines a *shell*
// command to the workspace, but the edit tool takes an absolute path from
// the model and is not sandboxed, and nothing local judges it — so a fix
// session's writes could not be confined to Sirdar's fix worktree. See
// ErrFixUnsupported.
package cursor

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
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

const (
	defaultBinary   = "cursor-agent"
	defaultModel    = "auto"
	defaultMode     = "ask"
	maxLineBytes    = 16 << 20 // a single stream-json line can carry a big tool result
	eventBuffer     = 64
	stderrTailLines = 50
	doctorTimeout   = 10 * time.Second
	interruptGrace  = 10 * time.Second
)

// ErrFixUnsupported is why `sirdar fix` refuses to run against this
// provider. The text is shown to the operator by the CLI and by the
// doctor row, so it says what is missing rather than only that something
// is.
var ErrFixUnsupported = errors.New(
	"provider cursor cannot run a fix session: the Cursor CLI approves its own tool calls in " +
		"print mode, and its sandbox confines a shell command to the workspace but not the edit " +
		"tool, which takes an absolute path — so a fix could write outside Sirdar's worktree with " +
		"nothing to refuse it; use provider claude, codex or openai for `sirdar fix`")

// writeTools are the tool-call names excluded from every session. They are
// the `oneof tool` field names of agent.v1.ToolCall, which is what
// --exclude-tools accepts, and they become the
// x-cursor-agent-exclude-tools request header.
//
// Read off the CLI's bundle, the flag is turned into that header and sent
// to Cursor's backend, so this is a server-side refusal rather than a
// local one — belt to the execution mode's braces, not a guarantee Sirdar
// enforces itself. It is passed anyway because it is the only tool-level
// statement the CLI accepts, and because an ask-mode refusal is the model
// declining rather than a tool being withheld.
//
// switch_mode_tool_call is on the list for a second reason: the model has
// a tool for changing its own execution mode, which is the one path by
// which an ask-mode session could stop being one.
var writeTools = []string{
	"edit_tool_call",
	"delete_tool_call",
	"shell_tool_call",
	"write_shell_stdin_tool_call",
	"apply_agent_diff_tool_call",
	"switch_mode_tool_call",
}

// fetchTools retrieve a URL. They join the exclusion list whenever
// permissions.fetch names no hosts, which is the same rule the Claude
// adapter applies to WebFetch: with no allow-list there is nothing to
// allow, and here there is no policy to ask, so the tool comes off the
// session instead.
//
// WebSearch is deliberately not on the list. It carries a query rather
// than a destination, so permissions.fetch has nothing to judge it
// against, and it stays allowed on every provider.
var fetchTools = []string{"web_fetch_tool_call", "fetch_tool_call"}

// mcpTools are excluded when the workspace asked for workspace-only MCP
// and declared no servers. Cursor merges ~/.cursor/mcp.json with the
// project's own and has no flag that narrows the set, so "only the
// workspace's servers" is not expressible; "no servers at all" is, by
// taking the tools away. See Doctor for the case this cannot cover.
var mcpTools = []string{
	"mcp_tool_call",
	"list_mcp_resources_tool_call",
	"read_mcp_resource_tool_call",
	"get_mcp_tools_tool_call",
	"mcp_auth_tool_call",
}

// cursorEnvKeys are the variables the CLI reads to decide which backend a
// session talks to and which credential pays for it. They are stripped
// from the child environment unconditionally, so a session runs against
// the login the operator's own binary holds rather than whatever is
// exported in the shell that launched Sirdar.
//
// CURSOR_API_ENDPOINT is the one that matters most: it is the documented
// endpoint override, and leaving it in place is the configuration that
// ships the operator's Cursor login to whatever host it names — the same
// failure ANTHROPIC_BASE_URL is stripped for under subscription billing.
// Sirdar offers no way to configure it back, because there is no
// Cursor-compatible endpoint for it to legitimately point at.
var cursorEnvKeys = []string{
	"CURSOR_API_KEY",
	"CURSOR_AUTH_TOKEN",
	"CURSOR_API_ENDPOINT",
	"CURSOR_API_URL",
	"CURSOR_DATA_DIR",
	"CURSOR_STATSIG_OVERRIDES",
}

// proxyEnvKeys are the variables that decide how the CLI's HTTPS traffic
// leaves the machine and which certificates it will trust. They are NOT
// stripped: on a corporate network they are the only way the CLI reaches
// api2.cursor.sh at all, and removing them would turn a working workspace
// into a session that fails before its first token.
//
// They are said out loud instead, once per session and once in `sirdar
// doctor`, because what they mean is that a proxy the operator configured
// terminates the session's TLS: the prompt Sirdar built out of a ticket,
// and the agent's answer, are both readable there. That is a reasonable
// thing to have set up and an unreasonable thing to discover afterwards.
// The value is never reported — a proxy URL routinely carries credentials.
var proxyEnvKeys = []string{
	"HTTPS_PROXY",
	"HTTP_PROXY",
	"ALL_PROXY",
	"NODE_EXTRA_CA_CERTS",
}

// Config is what a workspace configured under `cursor:`. Every field is
// optional: with none set the session runs `cursor-agent` off PATH against
// the login it already holds, in ask mode, on the Auto model.
type Config struct {
	// Binary is where the CLI lives; empty means look it up on PATH.
	Binary string
	// Model is the model id. Empty means "auto", which is also the only
	// model a free plan may select.
	Model string
	// Mode is the execution mode a read-only session runs in: "ask" or
	// "plan". Empty means "ask".
	Mode string
}

// Provider starts Cursor Agent sessions.
type Provider struct{ cfg Config }

// New returns the Cursor provider with no configuration.
func New() provider.Provider { return &Provider{} }

// NewConfig returns the Cursor provider with the workspace's `cursor:`
// block applied.
func NewConfig(cfg Config) provider.Provider { return &Provider{cfg: cfg} }

// Name identifies this provider in config and run records.
func (p *Provider) Name() string { return "cursor" }

// mode is the execution mode a session runs in.
func (p *Provider) mode(spec provider.SessionSpec) string {
	switch strings.TrimSpace(strings.ToLower(p.cfg.Mode)) {
	case "plan":
		return "plan"
	default:
		_ = spec
		return defaultMode
	}
}

// model is the model id a session runs on: the one-off override the caller
// named, else the workspace's, else Auto.
func (p *Provider) model(spec provider.SessionSpec) string {
	if spec.Model != "" {
		return spec.Model
	}
	if p.cfg.Model != "" {
		return p.cfg.Model
	}
	return defaultModel
}

// args builds the command line. The prompt is the last positional
// argument: there is no --input-format and no stdin protocol.
func (p *Provider) args(spec provider.SessionSpec, prompt string) []string {
	out := []string{
		"-p",
		"--output-format", "stream-json",
		"--model", p.model(spec),
		"--mode", p.mode(spec),
		// Without --trust a directory the operator has never opened
		// interactively fails before any API call, exit 1, with nothing
		// on stdout. Trust is persisted per directory, so the flag is
		// passed unconditionally rather than relied on being unnecessary
		// on the one machine it was tried.
		"--trust",
		// The sandbox is what confines a shell command: it sets
		// TYPE_WORKSPACE_READWRITE with networkAccess false and the
		// workspace as the only writable path. It does nothing for the
		// edit tool, which is why the edit tool is excluded instead.
		"--sandbox", "enabled",
		// Keeps the repository's own .cursor/cli.json, sandbox policies
		// and rules out of the session's configuration, so a checkout
		// cannot widen what its own triage run may do.
		"--disable-project-configs",
	}
	for _, name := range p.excluded(spec) {
		out = append(out, "--exclude-tools", name)
	}
	if spec.Resume != "" {
		out = append(out, "--resume", spec.Resume)
	}
	// --stream-partial-output is deliberately not passed: with it every
	// token becomes its own assistant line of the same shape as the
	// complete one, and the complete line arrives as well, so the answer
	// would be reported dozens of times over.
	return append(out, prompt)
}

// excluded is the tool list a session refuses, in a stable order.
func (p *Provider) excluded(spec provider.SessionSpec) []string {
	out := append([]string(nil), writeTools...)
	if spec.Policy == nil || len(spec.Policy.FetchAllow) == 0 {
		out = append(out, fetchTools...)
	}
	// MCPStrict with no config named means the workspace asked for
	// workspace-only MCP and declared no servers, so the session should
	// see none. With a config named, Cursor merges it with the
	// operator's own ~/.cursor/mcp.json and there is no flag that
	// narrows the result; the doctor row says so.
	if spec.MCPStrict && spec.MCPConfig == "" {
		out = append(out, mcpTools...)
	}
	return out
}

// schemaPrompt is the prompt a session is started with: the caller's, plus
// the instruction that carries the output schema, because there is no
// --json-schema flag to carry it on the command line.
func schemaPrompt(spec provider.SessionSpec) string {
	schema := strings.TrimSpace(compactJSON(spec.OutputSchema))
	if schema == "" || schema == "null" {
		return spec.Prompt
	}
	return spec.Prompt + "\n\n" +
		"Reply with one JSON object and nothing else: no prose before or after it, and no " +
		"code fence. It must validate against this JSON Schema:\n" + schema
}

func compactJSON(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// childEnv returns the environment for the child process, plus any
// EvSystem events the caller should see about what it did.
//
// Everything in cursorEnvKeys is removed. The login itself is not an
// environment variable: on macOS the CLI keeps it in the login keychain,
// reached through /usr/bin/security, and on Linux and Windows in a file
// under the home directory — so HOME, PATH, USER and the rest of the
// inherited environment are left alone. Relocating HOME to isolate the
// CLI's configuration would take the login with it.
func childEnv(spec provider.SessionSpec) ([]string, []provider.Event) {
	base := spec.Env
	if len(base) == 0 {
		base = os.Environ()
	}
	out := make([]string, 0, len(base))
	var events []provider.Event
	var proxied []string
	for _, e := range base {
		if name, ok := cursorEnvName(e); ok {
			events = append(events, systemNotice("removed "+name+" from the agent environment: "+
				"a Cursor session runs against the login the CLI already holds"))
			continue
		}
		if name, ok := proxyEnvName(e); ok {
			proxied = append(proxied, name)
		}
		out = append(out, e)
	}
	if len(proxied) > 0 {
		events = append(events, systemNotice(proxyNotice(proxied)))
	}
	return out, events
}

// proxyNotice is what the operator is told about the proxy variables that
// survived into the child. It names them and not their values.
func proxyNotice(names []string) string {
	return strings.Join(names, ", ") + " reached the agent unchanged: the Cursor CLI sends this " +
		"session through that proxy and trusts those certificates, so whatever terminates the " +
		"connection can read the prompt built from the ticket and the answer that comes back. " +
		"Sirdar does not strip them, because on a network that needs them the session would " +
		"otherwise not reach Cursor at all"
}

// proxyEnvName reports whether env entry e sets one of proxyEnvKeys,
// returning the name as it was spelled. The lower-case spellings are
// matched too: Node and the fetch stack inside the CLI read both, so
// `https_proxy` is the same configuration under a different name.
func proxyEnvName(e string) (string, bool) {
	i := strings.IndexByte(e, '=')
	if i <= 0 {
		return "", false
	}
	name := e[:i]
	for _, want := range proxyEnvKeys {
		if strings.EqualFold(name, want) {
			return name, true
		}
	}
	return "", false
}

// cursorEnvName reports whether env entry e sets one of cursorEnvKeys,
// returning its name.
func cursorEnvName(e string) (string, bool) {
	for _, name := range cursorEnvKeys {
		if strings.HasPrefix(e, name+"=") {
			return name, true
		}
	}
	return "", false
}

// systemNotice builds an EvSystem event carrying an operator-facing
// message from the adapter rather than from the CLI.
func systemNotice(text string) provider.Event {
	raw, _ := json.Marshal(map[string]string{"type": "system", "subtype": "sirdar", "text": text})
	ev := newEvent(provider.EvSystem, raw)
	ev.Text = text
	return ev
}

// SupportsFix is false, and is asked before `sirdar fix` does anything at
// all. Start refuses a fix spec too, but by then the branch has been cut
// and a worktree added for a session that was never going to run.
func (p *Provider) SupportsFix() bool { return false }

// FixRefusal is the reason, the same one Start gives.
func (p *Provider) FixRefusal() error { return ErrFixUnsupported }

// ErrSteerUnsupported is why `sirdar steer` refuses to continue a run on
// this provider. The CLI does have --resume, so the refusal is not about
// the handle: it is that print mode approves its own tool calls, and the
// read-only guarantee on a triage run rests on the tool set excluded at
// the start plus a breach noticed after the fact. A follow-up instruction
// is open-ended in a way the triage prompt is not — "fix it", "run the
// migration" — and there is nothing here that would refuse the call it
// leads to before it runs.
var ErrSteerUnsupported = errors.New(
	"provider cursor cannot continue a run: the Cursor CLI approves its own tool calls in " +
		"print mode, so a follow-up instruction cannot be held to the read-only guarantee " +
		"before a tool runs; re-run the triage, or use provider claude, codex, openai, qwen " +
		"or acp for `sirdar steer`")

// Continuation is ContinueNone: see ErrSteerUnsupported.
func (p *Provider) Continuation() provider.Continuation { return provider.ContinueNone }

// SteerRefusal is the reason `sirdar steer` is refused.
func (p *Provider) SteerRefusal() error { return ErrSteerUnsupported }

// Start launches the CLI with the prompt as its positional argument.
func (p *Provider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	if spec.Mode.IsFix() {
		return nil, ErrFixUnsupported
	}
	binary := spec.Binary
	if binary == "" {
		binary = p.cfg.Binary
	}
	if binary == "" {
		binary = defaultBinary
	}
	// runCtx is the one cancellation path: Cancel() cancels it, and so
	// does the caller's ctx.
	//
	// The whole process group is killed rather than the immediate child
	// interrupted, because of what Cancel is now used for: a breach is a
	// read-only guarantee that has already failed, and the session has to
	// stop before the next tool call, not after a grace period the shell
	// commands `cursor-agent` spawned would go on running through.
	runCtx, cancelRun := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, binary, p.args(spec, schemaPrompt(spec))...)
	procgroup.Setup(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = interruptGrace
	cmd.Dir = spec.Cwd
	env, envNotices := childEnv(spec)
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelRun()
		return nil, fmt.Errorf("cursor stdout: %w", err)
	}
	tail := &tailWriter{max: stderrTailLines}
	cmd.Stderr = tail

	s := &session{
		binary:    binary,
		cmd:       cmd,
		cancelRun: cancelRun,
		stderr:    tail,
		events:    make(chan provider.Event, eventBuffer),
		readDone:  make(chan struct{}),
		done:      make(chan struct{}),
		seenCalls: map[string]bool{},
	}
	if err := cmd.Start(); err != nil {
		cancelRun()
		return nil, fmt.Errorf("start %s: %w", binary, err)
	}
	// Sent before the read goroutine starts, so there is no chance of a
	// send racing its close(s.events) on an already-buffered channel.
	for _, ev := range envNotices {
		s.events <- ev
	}
	if spec.Budget.MaxUSD > 0 {
		s.events <- systemNotice("the Cursor CLI reports no cost, so budget.maxUsd cannot stop this run")
	}
	go s.read(stdout)
	return s, nil
}

// Doctor checks that the binary runs, that a login is present, and that
// the configured model is one the account may actually select.
func (p *Provider) Doctor(ctx context.Context, binary string) []provider.Check {
	return p.doctor(ctx, binary, provider.DoctorConfig{}, false)
}

// DoctorWithConfig adds the rows that depend on the workspace: which MCP
// servers a session will see, which is the one restriction this provider
// cannot honour.
func (p *Provider) DoctorWithConfig(ctx context.Context, binary string, cfg provider.DoctorConfig) []provider.Check {
	return p.doctor(ctx, binary, cfg, true)
}

func (p *Provider) doctor(ctx context.Context, binary string, cfg provider.DoctorConfig, withConfig bool) []provider.Check {
	if binary == "" {
		binary = p.cfg.Binary
	}
	if binary == "" {
		binary = defaultBinary
	}

	version := provider.Check{Name: "cursor-agent --version"}
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

	checks := []provider.Check{version, p.authCheck(ctx, binary), p.modelCheck(ctx, binary)}
	if row, ok := proxyCheck(os.Environ()); ok {
		checks = append(checks, row)
	}
	checks = append(checks, provider.Warn("cursor fix",
		"`sirdar fix` is refused on this provider: "+shortFixReason()))
	if withConfig {
		checks = append(checks, mcpCheck(cfg))
	}
	return checks
}

// authCheck reports whether the CLI holds a login. The account's email
// address is deliberately not reported: doctor's output gets pasted into
// tickets, and `cursor-agent status` prints nothing else.
func (p *Provider) authCheck(ctx context.Context, binary string) provider.Check {
	check := provider.Check{Name: "cursor-agent status"}
	out, err := runWithTimeout(ctx, binary, "status")
	text := firstLine(out)
	switch {
	case err != nil:
		// The error only. `cursor-agent status` prints the account's
		// email address on its success path, and a failing invocation is
		// no guarantee it printed nothing before failing — so the
		// combined output never reaches a report that gets pasted into a
		// ticket.
		check.Detail = err.Error()
	case strings.Contains(strings.ToLower(text), "logged in as"):
		check.OK = true
		check.Detail = "logged in"
	default:
		check.Detail = text
	}
	return check
}

// modelCheck reports which model a session will ask for, against what the
// account may select. A free plan refuses every named model with an
// ActionRequiredError before the first token, and the run dies with no
// result line at all, so it is worth catching here rather than mid-run.
func (p *Provider) modelCheck(ctx context.Context, binary string) provider.Check {
	model := p.cfg.Model
	if model == "" {
		model = defaultModel
	}
	check := provider.Check{Name: "cursor model", OK: true, Detail: model}

	out, err := runWithTimeout(ctx, binary, "about")
	if err != nil {
		return provider.Warn(check.Name, model+"; could not read the account tier: "+firstLine(out))
	}
	tier := fieldValue(string(out), "Subscription Tier")
	if tier == "" {
		return check
	}
	check.Detail = model + " on the " + tier + " plan"
	if strings.EqualFold(tier, "free") && model != defaultModel {
		return provider.Check{
			Name: check.Name,
			Detail: "model " + model + " cannot be used on the Free plan, which allows only " +
				defaultModel + "; a run would fail before its first token",
		}
	}
	return check
}

// mcpCheck says what a session will actually see, which is not what
// mcp.workspaceOnly asks for. Cursor merges ~/.cursor/mcp.json with the
// project's own and offers no flag that narrows the set, so the setting is
// honoured only in its emptiest case — no workspace servers means the MCP
// tools come off the session entirely.
func mcpCheck(cfg provider.DoctorConfig) provider.Check {
	const name = "cursor mcp"
	if !cfg.MCPWorkspaceOnly {
		return provider.Warn(name, "mcp.workspaceOnly is off: every server in ~/.cursor/mcp.json "+
			"and the workspace's .cursor/mcp.json is visible to the agent")
	}
	return provider.Warn(name, "mcp.workspaceOnly cannot be honoured on this provider: the Cursor "+
		"CLI always merges ~/.cursor/mcp.json with the workspace's .cursor/mcp.json and has no "+
		"flag that narrows the set; a workspace that declares no servers gets the MCP tools "+
		"excluded instead")
}

// proxyCheck warns when the environment `sirdar doctor` runs in routes the
// CLI's HTTPS traffic through a proxy or widens the certificates it
// trusts. There is no row when none is set: a warning about a proxy that
// is not there would be noise on every report.
func proxyCheck(environ []string) (provider.Check, bool) {
	var names []string
	seen := map[string]bool{}
	for _, e := range environ {
		name, ok := proxyEnvName(e)
		if !ok || seen[strings.ToUpper(name)] {
			continue
		}
		seen[strings.ToUpper(name)] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return provider.Check{}, false
	}
	return provider.Warn("cursor proxy", strings.Join(names, ", ")+" is set and is passed through "+
		"to the agent: the session's traffic to Cursor goes through that proxy, which can read "+
		"the ticket in the prompt and the answer. Sirdar leaves them alone because removing them "+
		"would break a workspace that needs them"), true
}

// shortFixReason is the one-line form of ErrFixUnsupported, for a doctor
// row that has to fit on a terminal line.
func shortFixReason() string {
	return "the CLI approves its own tool calls in print mode and does not sandbox the edit tool, " +
		"so a fix could write outside the worktree"
}

// fieldValue reads a "Label   value" line out of `cursor-agent about`.
func fieldValue(out, label string) string {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, label) {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(trimmed, label))
	}
	return ""
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

// session is one running cursor-agent process.
type session struct {
	binary    string
	cmd       *exec.Cmd
	cancelRun context.CancelFunc
	stderr    *tailWriter

	events   chan provider.Event
	readDone chan struct{} // closed when stdout hits EOF
	done     chan struct{} // closed when the process has been reaped

	// seenCalls is the set of model_call_ids that have already been
	// counted as a turn. It is touched only by the read goroutine.
	seenCalls map[string]bool

	mu      sync.Mutex
	handle  string
	res     provider.Result
	waitErr error
	meter   usageMeter

	waitOnce sync.Once
}

// usageMeter accumulates what the session has spent so far. Only the
// result line reports usage, so the meter is a total rather than a running
// sum, except for the turn count, which is Sirdar's own.
type usageMeter struct {
	turns  int
	inTok  int64
	outTok int64
}

// Events returns the activity stream. The channel is buffered; the caller
// must drain it, because the reader goroutine blocks once the buffer fills
// and Wait does not return until stdout has been read to EOF.
func (s *session) Events() <-chan provider.Event { return s.events }

func (s *session) Handle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handle
}

// Send always fails. `cursor-agent -p` takes its one user message as a
// positional argument and reads nothing from stdin, so there is no second
// turn to send: the runner carries the schema retry into a fresh session
// against Handle() instead.
func (s *session) Send(ctx context.Context, userText string) error {
	return errors.New("the Cursor CLI takes one message per session; resume the handle instead")
}

// CloseInput does nothing. There is no stdin protocol to close, and the
// CLI exits on its own once the turn is over.
func (s *session) CloseInput() error { return nil }

// Wait reaps the process and returns the session's Result. It must be
// called after Events has been drained. A non-zero exit is reported in
// Result.ExitErr rather than as an error, so the caller can still read the
// handle and whatever the session produced.
//
// Exit 1 with no result line is a normal outcome here — a workspace the
// CLI will not trust, or a named model a free plan refuses — and in both
// cases the stderr tail is the only account of what happened.
func (s *session) Wait() (provider.Result, error) {
	s.waitOnce.Do(func() {
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
// cmd.Cancel kills the process group, so the shell commands the CLI
// spawned go with it, and cmd.WaitDelay releases Wait from the pipes if
// anything is still holding them after interruptGrace. It does not block;
// the outcome shows up in Wait's Result.
func (s *session) Cancel() { s.cancelRun() }

// read consumes stdout until EOF, emitting one or more events per line.
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
		roundTrip := isRoundTrip(line, s.seenCalls)
		for _, ev := range decode(line) {
			s.measure(&ev, roundTrip)
			s.absorb(ev)
			s.events <- ev
		}
		if roundTrip {
			s.countTurn()
		}
	}
	if err := sc.Err(); err != nil {
		ev := newEvent(provider.EvError, nil)
		ev.Text = "read cursor output: " + err.Error()
		s.events <- ev
	}
}

// countTurn advances the turn meter for a line that completed one model
// round-trip.
func (s *session) countTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meter.turns++
}

// measure fills a usage or final event with the session's totals. The
// result line is the only line carrying token counts, and it carries the
// session's own totals rather than a turn's, so it replaces the meter
// instead of adding to it.
func (s *session) measure(ev *provider.Event, roundTrip bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := s.meter.turns
	if roundTrip {
		turns++
	}
	switch ev.Kind {
	case provider.EvUsage, provider.EvFinal:
		s.meter.inTok = ev.InputTok
		s.meter.outTok = ev.OutputTok
		ev.Turns = turns
	default:
		return
	}
}

// absorb records the parts of an event that belong to the terminal Result.
func (s *session) absorb(ev provider.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.handle == "" {
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
