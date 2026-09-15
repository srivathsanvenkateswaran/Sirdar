// Package agy adapts Google's Antigravity CLI (`agy --print` with
// stream-json on both stdin and stdout) to the provider.Session contract.
//
// It is modelled on the Claude adapter — NDJSON in, NDJSON out, a schema on
// the command line, follow-up user messages for the schema retry — but one
// difference runs through the whole file and is worth stating before the
// code: **`agy` offers no way for a parent process to mediate a tool
// call.** There is no control_request channel, no HTTP permission hook, and
// nothing on stdin that answers a prompt. A headless run auto-denies
// anything that would need approval, and everything else is decided by
// files Sirdar does not own — the operator's own
// ~/.gemini/antigravity-cli/settings.json, a project file under
// ~/.gemini/config/projects/, or a .agents/hooks.json inside the customer's
// repository. See docs/research/10-antigravity-wire-formats.md.
//
// Three consequences shape this adapter:
//
//   - The read-only guarantee is `--mode plan` plus the absence of any
//     allow rule, not a tool exclusion list. Plan mode was the only mode
//     observed to refuse a write to an absolute path outside the
//     workspace; the default mode performed one. PermissionPolicy is never
//     consulted, because by the time a tool call is visible on the stream
//     the CLI has already decided it. What the session does instead is
//     watch: a write that actually completes in a triage session is
//     reported as an error rather than passed over in silence.
//   - Fix mode is refused outright. Letting `agy` write needs
//     --dangerously-skip-permissions, which approves every tool including
//     a write into .git/hooks, and provider.FixPolicy's path confinement
//     has nothing to attach to.
//   - mcp.workspaceOnly cannot be enforced: the CLI reads one global
//     mcp_config.json and takes no flag that narrows or replaces it.
//     Doctor warns and the session emits a system event saying so.
package agy

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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

const (
	defaultBinary   = "agy"
	maxLineBytes    = 16 << 20 // a single stream-json line can carry a big tool result
	eventBuffer     = 64
	stderrTailLines = 50
	doctorTimeout   = 20 * time.Second // `agy models` is a network round trip
	interruptGrace  = 10 * time.Second

	// planMode is the execution mode every Sirdar session runs in. It is
	// the strongest read-only lever the CLI has: `--mode plan` refused a
	// write_to_file naming an absolute path outside the workspace, where
	// the default mode performed the same write into the CLI's own
	// scratch directory without asking anyone.
	planMode = "plan"

	// defaultModel is the cheapest tier `agy models` offers: the smallest
	// Flash model at the lowest reasoning effort. A workspace that names
	// no model gets it rather than whatever the account's default is,
	// because the account default is a Pro model on an unknown quota.
	defaultModel = "gemini-3.6-flash-low"

	// stateDirParent and stateDirChild name the CLI's own state directory
	// under the operator's home, ~/.gemini/antigravity-cli.
	stateDirParent = ".gemini"
	stateDirChild  = "antigravity-cli"

	// planDirName is the one directory under the state directory a
	// plan-mode session is expected to write into: brain/<conversation
	// id>/ holds this conversation's implementation-plan artifact, which
	// every plan-mode run produces.
	//
	// The exemption stops there and does not cover the state directory as
	// a whole. Beside brain/ the CLI keeps scratch/, and scratch/ is
	// exactly where the research capture found a default-mode run
	// depositing a file the agent had been asked to write to an absolute
	// path — a real write, performed on the operator's disk, that an
	// exemption for the whole directory would have reported as
	// bookkeeping. See observeWrite.
	planDirName = "brain"
)

// writeTools are the tool names that change a file. A triage session must
// reach none of them outside the CLI's own state directory; one that
// completes anywhere else is reported as an error, because the read-only
// guarantee here rests on the CLI's own refusal and nothing Sirdar can
// impose, so a refusal that did not happen has to be visible.
var writeTools = map[string]bool{
	"write_to_file":              true,
	"replace_file_content":       true,
	"multi_replace_file_content": true,
	"sed_file":                   true,
	"notebook_edit":              true,
}

// execTools are the tool names that run a program. The same reasoning
// applies: a triage session should see every one of these refused.
//
// call_mcp_tool is on the list because an MCP server's own tools are
// opaque: the CLI reports one `call_mcp_tool` step whatever the server
// went on to do, and the session cannot see the servers the operator
// configured globally, let alone which of their tools write. Left off the
// list, an MCP write is the one kind that completes invisibly.
var execTools = map[string]bool{
	"run_command":                true,
	"send_command_input":         true,
	"notebook_execution":         true,
	"browser_subagent":           true,
	"execute_browser_javascript": true,
	"call_mcp_tool":              true,
}

// readTools are the tools that put a file's contents in front of the
// agent. A triage note is a claim about a codebase, and one of these
// completing is the only evidence on this wire that the agent looked at
// the codebase before making it. See observeRead.
//
// `list_dir` and `find_by_name` are deliberately **not** here, and that is
// the distinction round 1 turned on: that run's `find_by_name` completed
// while its `view_file` was refused, so a check that counted any tool
// would have passed a session which had seen a list of filenames and not
// one line of code.
var readTools = map[string]bool{
	"view_file":         true,
	"read_file":         true,
	"grep_search":       true,
	"read_resource":     true,
	"read_url_content":  true,
	"read_browser_page": true,
}

// searchTools are the tools that name files without opening them. They do
// not count as evidence, but a refusal of one is still a refused read and
// belongs in the count the failure reason carries — a session that was
// refused a `list_dir` and a `view_file` was refused two reads.
var searchTools = map[string]bool{
	"list_dir":     true,
	"find_by_name": true,
	"glob":         true,
}

// toolArgs is every argument name an `agy` tool names its subject with:
// the file a write targets, the command line an exec runs, the server and
// tool an MCP call reaches. The keys are Cascade's PascalCase, not the
// snake_case the other adapters read, which is why this cannot reuse the
// policy's writeArgs.
type toolArgs struct {
	TargetFile   string `json:"TargetFile"`
	FilePath     string `json:"FilePath"`
	NotebookPath string `json:"NotebookPath"`
	AbsolutePath string `json:"AbsolutePath"`

	CommandLine string `json:"CommandLine"`
	Command     string `json:"Command"`

	ServerName string `json:"ServerName"`
	ToolName   string `json:"ToolName"`
}

func (a toolArgs) target() string {
	for _, p := range []string{a.TargetFile, a.FilePath, a.NotebookPath, a.AbsolutePath} {
		if strings.TrimSpace(p) != "" {
			return p
		}
	}
	return ""
}

// subject is what the tool call acted on, for the breach to name: the path
// for a write, the command line for an exec, the server's tool for an MCP
// call. Empty when the arguments named none, which a DONE line with its
// tool_info elided and no ACTIVE line to match leaves it.
func (a toolArgs) subject() string {
	if t := a.target(); t != "" {
		return t
	}
	for _, c := range []string{a.CommandLine, a.Command} {
		if strings.TrimSpace(c) != "" {
			return c
		}
	}
	switch {
	case a.ServerName != "" && a.ToolName != "":
		return a.ServerName + "/" + a.ToolName
	case a.ToolName != "":
		return a.ToolName
	case a.ServerName != "":
		return a.ServerName
	}
	return ""
}

// Config is what a workspace configures under `agy:`. Every field is
// optional: with none of them set the session runs against whatever Google
// account the operator's own `agy` binary is signed in to, the way
// provider: claude runs against their Claude Code login.
type Config struct {
	Binary string
	Model  string
	Effort string
}

// Provider starts Antigravity CLI sessions.
type Provider struct{ cfg Config }

// New returns the Antigravity provider with no configuration: the child
// runs against the login the operator's `agy` binary already holds and the
// cheapest model on the account.
func New() provider.Provider { return &Provider{} }

// NewConfig returns the provider with the workspace's `agy:` block applied.
func NewConfig(c Config) provider.Provider { return &Provider{cfg: c} }

// Name identifies this provider in config and run records.
func (p *Provider) Name() string { return "agy" }

// model is the model a session runs, preferring the per-run override.
func (p *Provider) model(spec provider.SessionSpec) string {
	for _, m := range []string{spec.Model, p.cfg.Model} {
		if strings.TrimSpace(m) != "" {
			return m
		}
	}
	return defaultModel
}

// args builds the command line.
//
// --disable-slash-commands is **not** on it, and its absence is the
// deliberate part. Round 1 passed it — the prompt carries ticket text
// somebody else wrote, and slash-command expansion would let a line of
// that text name a command — and every run came back with
// "warning: --mode plan has no effect while slash command expansion is
// disabled." on stderr. Plan mode is implemented as an expansion in print
// mode, so the two flags cannot both take: disabling expansion disabled
// the one read-only lever this provider has, and the sessions ran in the
// CLI's default mode, which the research capture watched perform a write.
// Nothing in `agy --help` combines them, so the injection surface is
// accepted and narrowed instead:
//
//   - With --input-format stream-json the CLI refuses to answer its own
//     slash commands at all — "/%s is answered by the CLI itself and is
//     unavailable with --input-format stream-json" — so the commands that
//     change the session (/model, /logout, /plugin) are not reachable from
//     the prompt. What remains expandable is a workspace or user skill.
//   - Every write and every command is denied for the session by the
//     project file (project.go), so a skill that expanded into one is
//     refused rather than run.
//   - A write or command that completes anyway ends the run (observeWrite).
//
// --print= is load-bearing and the empty value is not a typo: -p, --print
// and --prompt all take the prompt as the flag's own value, so the bare
// `agy -p --output-format stream-json` form is rejected with "-p took
// \"--output-format\" as its prompt". With --input-format stream-json the
// prompt arrives on stdin instead, so the flag is given an empty value to
// select print mode without swallowing the next argument.
func (p *Provider) args(spec provider.SessionSpec, project string) []string {
	out := []string{
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		// The read-only guarantee, such as the CLI allows one to be
		// made. See planMode.
		"--mode", planMode,
	}
	// The session's own project file carries the permission rules: a
	// read allowed, every write and every command denied. See project.go.
	// Empty when the file could not be written, in which case the session
	// runs on the operator's own settings and says so.
	if project != "" {
		out = append(out, "--project", project)
	}
	// The runner always supplies a schema; an empty one is a caller bug,
	// so it is passed through rather than silently dropped.
	out = append(out, "--json-schema", compactJSON(spec.OutputSchema))
	out = append(out, "--model", p.model(spec))
	if e := strings.TrimSpace(p.cfg.Effort); e != "" {
		out = append(out, "--effort", e)
	}
	// The CLI has no turn or tool-call ceiling — no --max-turns, no
	// --max-tool-calls, no --max-wall-time — so the wall clock is the one
	// budget it can enforce itself and the turn budget is left to the
	// runner counting result events.
	if spec.Budget.MaxMinutes > 0 {
		out = append(out, "--print-timeout", fmt.Sprintf("%dm", spec.Budget.MaxMinutes))
	}
	// Flags are not carried across a resume: a session resumed without
	// --mode plan runs in the default mode. The whole set is therefore
	// passed on every start, resume included, and --conversation only
	// adds to it.
	if spec.Resume != "" {
		out = append(out, "--conversation", spec.Resume)
	}
	return append(out, "--print=")
}

// strippedEnvKeys are the variables that can point the child at a
// different backend, hand it a different credential, or make it believe it
// is running as an Antigravity sidecar. None has a legitimate role in a
// Sirdar run, and GEMINI_API_KEY in particular is this CLI's equivalent of
// ANTHROPIC_BASE_URL: with it set and modelProvider: "gemini" configured,
// the session stops billing against the operator's Antigravity login and
// starts billing an API key, against a base URL another variable chooses.
//
// The OAuth login itself is not in the environment and not under
// ~/.gemini: it lives in the OS keyring. That is why this list strips
// rather than relocates — moving HOME to isolate the CLI's configuration
// would risk the keyring lookup, and a failed lookup is a dead run.
var strippedEnvKeys = []string{
	// backend and credentials
	"GEMINI_API_KEY",
	"GOOGLE_GEMINI_BASE_URL",
	"GOOGLE_API_KEY",
	"GOOGLE_APPLICATION_CREDENTIALS",
	"GOOGLE_CLOUD_QUOTA_PROJECT",
	"GOOGLE_CLOUD_PROJECT",
	// the sidecar/extension protocol the server injects into its own
	// children; a stray export makes a Sirdar session look like one
	"ANTIGRAVITY_LS_ADDRESS",
	"ANTIGRAVITY_CSRF_TOKEN",
	"ANTIGRAVITY_SIDECAR_WEB_PORT",
	"ANTIGRAVITY_SIDECAR_UI_TOKEN",
	"ANTIGRAVITY_AGENTAPI_EXE",
	"ANTIGRAVITY_EXECUTABLE_DATA_DIR",
	"ANTIGRAVITY_CONVERSATION_ID",
	"ANTIGRAVITY_PROJECT_ID",
	"ANTIGRAVITY_AGENT",
	// internal state and browser-tool wiring
	"JETSKI_APP_DATA_DIR",
	"JETSKI_BROWSER_PORT",
}

// childEnv returns the environment for the child process, plus the
// EvSystem events the caller should see about what was removed.
func childEnv(spec provider.SessionSpec) ([]string, []provider.Event) {
	base := spec.Env
	if len(base) == 0 {
		base = os.Environ()
	}
	out := make([]string, 0, len(base))
	var events []provider.Event
	for _, e := range base {
		if name, ok := strippedName(e); ok {
			events = append(events, systemNotice("removed "+name+
				" from the agent environment: an agy session runs against the operator's own Antigravity login and nothing else"))
			continue
		}
		out = append(out, e)
	}
	return out, events
}

func strippedName(entry string) (string, bool) {
	name, _, ok := strings.Cut(entry, "=")
	if !ok {
		return "", false
	}
	for _, k := range strippedEnvKeys {
		if name == k {
			return name, true
		}
	}
	return "", false
}

func systemNotice(text string) provider.Event {
	raw, _ := json.Marshal(map[string]string{"event": "system", "source": "sirdar", "text": text})
	ev := newEvent(provider.EvSystem, raw)
	ev.Text = text
	return ev
}

func compactJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// ErrFixUnsupported is why `sirdar fix` cannot run on this provider. It is
// returned from Start rather than checked somewhere earlier so that every
// path into a fix session — the CLI, the desktop app, `sirdar serve` —
// hits the same refusal.
var ErrFixUnsupported = errors.New(
	"provider agy: fix mode is refused. The Antigravity CLI gives a parent process no way to " +
		"mediate or even see a tool call before it runs: headless runs auto-deny whatever needs " +
		"approval, and the only way to let the agent write is --dangerously-skip-permissions, " +
		"which approves every tool including a write into .git/hooks. Sirdar's fix policy " +
		"confines a write to the workspace by judging each call, and there is nothing here to " +
		"judge. Run `sirdar fix` under provider: claude, codex or openai")

// SupportsFix is false, and is asked before `sirdar fix` does anything at
// all. Start refuses a fix spec too, but by then the branch has been cut
// and a worktree added for a session that was never going to run.
func (p *Provider) SupportsFix() bool { return false }

// FixRefusal is the reason, the same one Start gives.
func (p *Provider) FixRefusal() error { return ErrFixUnsupported }

// Start launches the CLI and sends the prompt as the first user line.
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
	// does the caller's ctx. The CLI starts a language-server sidecar of
	// its own, so the whole process group is signalled rather than just
	// the immediate child.
	env, notices := childEnv(spec)

	// The session's permission rules go in a project file of Sirdar's
	// own, written before the process starts and deleted when it ends.
	// This is what lets a triage read a file at all: without it the CLI
	// auto-denies read_file in a headless run, and round 1 filed a
	// high-confidence note off the ticket text alone. See project.go.
	//
	// A crash between writing the file and reaping the process leaves one
	// behind in the operator's directory, so every start clears the ones
	// old enough to be nobody's.
	if n := sweepStaleProjects(time.Now(), staleProjectAge); n > 0 {
		notices = append(notices, systemNotice(fmt.Sprintf(
			"removed %d stale Sirdar project file(s) from ~/.gemini/config/projects left by an earlier run", n)))
	}
	projectID, projectPath, perr := writeProject(spec.Cwd)
	if perr != nil {
		// Not fatal. An operator whose own settings.json already allows
		// read_file still gets a working run, and one whose settings do
		// not gets a run that reads nothing — which the blind check ends
		// with a reason naming exactly that, rather than a note written
		// out of the ticket text. Saying it here is what connects the two.
		notices = append(notices, systemNotice("could not write this session's agy project file ("+perr.Error()+
			"), so the session runs on the operator's own ~/.gemini/antigravity-cli/settings.json: "+
			"reads will be auto-denied unless that file allows read_file"))
		projectID, projectPath = "", ""
	} else {
		notices = append(notices, systemNotice("granted this session read_file through its own project file "+
			projectPath+"; write_file, command and execute_url are denied, and the file is removed when the run ends"))
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, binary, p.args(spec, projectID)...)
	procgroup.Setup(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = interruptGrace
	cmd.Dir = spec.Cwd
	cmd.Env = env

	// mcp.workspaceOnly asked for a restriction this CLI cannot express.
	// It is said once per session rather than left to doctor alone,
	// because a run's own event log is where an operator looks afterwards
	// to find out what the session could reach.
	if spec.MCPStrict {
		notices = append(notices, systemNotice("mcp.workspaceOnly is not enforceable on provider agy: "+
			"the CLI loads ~/.gemini/config/mcp_config.json for every session and takes no flag that "+
			"narrows or replaces it, so this session sees whatever MCP servers the operator has configured globally"))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancelRun()
		removeProject(projectPath)
		return nil, fmt.Errorf("agy stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelRun()
		removeProject(projectPath)
		return nil, fmt.Errorf("agy stdout: %w", err)
	}
	tail := &tailWriter{max: stderrTailLines}
	cmd.Stderr = tail

	s := &session{
		binary:    binary,
		cmd:       cmd,
		cancelRun: cancelRun,
		stdin:     stdin,
		stderr:    tail,
		stateDir:  stateDir(),
		project:   projectPath,
		events:    make(chan provider.Event, eventBuffer),
		readDone:  make(chan struct{}),
		done:      make(chan struct{}),
		pending:   map[int]toolArgsMemo{},
	}
	if err := cmd.Start(); err != nil {
		cancelRun()
		removeProject(projectPath)
		return nil, fmt.Errorf("start %s: %w", binary, err)
	}
	// Sent before the read goroutine starts, so there is no chance of a
	// send racing its close(s.events) on an already-buffered channel.
	for _, ev := range notices {
		s.events <- ev
	}
	go s.read(stdout)
	if err := s.writeUser(spec.Prompt); err != nil {
		cancelRun()
		go func() {
			<-s.readDone
			_ = cmd.Wait()
			// After the child is reaped, not before: the CLI reads its
			// project at startup and a file pulled out from under it
			// would be a second failure on top of this one.
			removeProject(s.project)
		}()
		return nil, fmt.Errorf("agy prompt: %w", err)
	}
	return s, nil
}

// stateDir is the CLI's own state directory, or "" when the home
// directory cannot be found — in which case observeWrite exempts nothing
// and reports every write, which is the safe direction.
func stateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, stateDirParent, stateDirChild)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// Doctor checks that the binary runs and that the account is signed in.
func (p *Provider) Doctor(ctx context.Context, binary string) []provider.Check {
	return p.DoctorWithConfig(ctx, binary, provider.DoctorConfig{})
}

// DoctorWithConfig reports on the binary, the login, the model, and the
// two guarantees this provider cannot make.
func (p *Provider) DoctorWithConfig(ctx context.Context, binary string, cfg provider.DoctorConfig) []provider.Check {
	if binary == "" {
		binary = p.cfg.Binary
	}
	if binary == "" {
		binary = defaultBinary
	}

	version := provider.Check{Name: "agy --version"}
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

	// There is no `agy auth status`. `agy models` is the cheapest probe
	// that proves a login: it is one authenticated request, it starts no
	// conversation, and it spends no model tokens. Its output is also the
	// only way to tell whether the configured model is one this account
	// may use.
	login := provider.Check{Name: "agy models"}
	models, listErr := listModels(ctx, binary)
	switch {
	case listErr != nil:
		login.Detail = listErr.Error()
	case len(models) == 0:
		login.Detail = "the account lists no models; `agy` may not be signed in"
	default:
		login.OK = true
		login.Detail = fmt.Sprintf("signed in, %d models available", len(models))
	}

	wanted := p.cfg.Model
	if wanted == "" {
		wanted = defaultModel
	}
	model := provider.Check{Name: "agy model", OK: true, Detail: wanted}
	switch {
	case listErr != nil || len(models) == 0:
		model = provider.Warn("agy model", wanted+": cannot be checked until `agy models` answers")
	case !models[wanted]:
		model = provider.Warn("agy model", wanted+" is not on this account's model list; "+
			"run `agy models` and set agy.model to one that is")
	}

	reads := readAccessCheck()
	checks := []provider.Check{version, login, model, reads, settingsCheck(cfg, reads.OK), mcpCheck(cfg), fixCheck()}
	return checks
}

// agySettings is the part of the CLI's own settings file that decides what
// a Sirdar session will be allowed to do. Sirdar neither writes this file
// nor passes a flag that overrides it, so reporting it is the only thing
// doctor can do about it.
type agySettings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
		Ask   []string `json:"ask"`
	} `json:"permissions"`
	TrustedWorkspaces []string `json:"trustedWorkspaces"`
}

// settingsCheck reports ~/.gemini/antigravity-cli/settings.json: the
// permission rules in it and whether this workspace is trusted.
//
// This is the file the read-only guarantee actually rests on. Plan mode
// refuses a write because nothing in permissions.allow approves one; a
// single allow rule the operator added months ago for their own
// interactive use turns that refusal into a completed write, which a
// Sirdar run can then only report after the fact (see observeWrite). The
// row exists so an operator reads the rules before a run trips over them.
//
// It is always a warning, never a failure: a permissive rule is the
// operator's own decision about their own machine, and doctor's job here
// is to make it visible rather than to veto it.
// readAccessCheck is the row that says whether a triage on this provider
// will be able to read anything.
//
// It is a **failure**, not a warning, when the answer is no. Every other
// gap this adapter reports — no MCP scoping, no fix mode, an operator's
// own allow rules — is a permanent property of the CLI that an operator
// can read and decide about. This one is different: a session that cannot
// read is not a degraded triage, it is an agent answering a ticket from
// its description, and round 1 produced exactly that and called it high
// confidence. A run in that state should not start.
//
// What the check tests is the one thing Sirdar controls: whether it can
// write its own project file under ~/.gemini/config/projects, which is
// where the read_file grant goes (project.go). The grant itself is
// verified on a live run rather than here; doctor cannot spend a model
// turn.
func readAccessCheck() provider.Check {
	const name = "agy read access"
	dir, err := projectsDir()
	if err != nil {
		return provider.Check{Name: name, Detail: "the home directory could not be found, so Sirdar cannot " +
			"write the project file that grants this session read_file. A headless agy run auto-denies a read " +
			"it has no rule for, so a triage would answer out of the ticket text alone. " +
			"Add `read_file(*)` under permissions.allow in ~/.gemini/antigravity-cli/settings.json"}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return provider.Check{Name: name, Detail: dir + " could not be created (" + err.Error() +
			"), so Sirdar cannot grant this session read_file and a triage would read nothing. " +
			"Add `read_file(*)` under permissions.allow in ~/.gemini/antigravity-cli/settings.json"}
	}
	probe, err := os.CreateTemp(dir, projectIDPrefix+"doctor-*.json")
	if err != nil {
		return provider.Check{Name: name, Detail: dir + " is not writable (" + err.Error() +
			"), so Sirdar cannot grant this session read_file and a triage would read nothing. " +
			"Add `read_file(*)` under permissions.allow in ~/.gemini/antigravity-cli/settings.json"}
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)

	return provider.Check{Name: name, OK: true, Detail: "Sirdar writes one project file per session under " +
		dir + " granting read_file and denying write_file, command and execute_url, passes it as --project, " +
		"and deletes it when the run ends. Project rules outrank " + filepath.Join("~", stateDirParent, stateDirChild, "settings.json")}
}

func settingsCheck(cfg provider.DoctorConfig, readsGranted bool) provider.Check {
	const name = "agy settings"
	path, err := settingsPath()
	if err != nil {
		return provider.Warn(name, "the home directory could not be found, so "+
			"the CLI's settings.json cannot be read; Sirdar cannot tell what this session will be allowed to do")
	}
	// A settings file that cannot be read or parsed is treated the same
	// way as one with no read rule: with Sirdar's project file
	// unavailable, nothing has been shown to grant a read, and a triage
	// that reads nothing is the failure readAccessCheck describes.
	unreadable := func(detail string) provider.Check {
		if readsGranted {
			return provider.Warn(name, detail)
		}
		return provider.Check{Name: name, Detail: detail + ". Sirdar cannot write its own project file either, " +
			"so nothing is known to grant this session a read and a triage would read nothing"}
	}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return unreadable(path + " does not exist: the CLI will run on its defaults, " +
			"and this workspace is not in trustedWorkspaces")
	case err != nil:
		return unreadable(path + " could not be read: " + err.Error() +
			"; Sirdar cannot tell what this session will be allowed to do")
	}
	var s agySettings
	if err := json.Unmarshal(b, &s); err != nil {
		return unreadable(path + " is not valid JSON (" + err.Error() +
			"); the CLI reads this file for every session and Sirdar cannot tell what it will allow")
	}

	parts := []string{
		"permissions.allow: " + ruleList(s.Permissions.Allow),
		"permissions.deny: " + ruleList(s.Permissions.Deny),
	}
	if len(s.Permissions.Ask) > 0 {
		// Headless runs cannot prompt, so an ask rule is an auto-deny
		// rather than a question, which is worth saying plainly.
		parts = append(parts, "permissions.ask: "+ruleList(s.Permissions.Ask)+
			" (a headless run cannot prompt, so these are auto-denied)")
	}
	trust := "this workspace is not in trustedWorkspaces"
	if trustsWorkspace(s.TrustedWorkspaces, cfg.Root) {
		trust = "this workspace is in trustedWorkspaces"
	}
	parts = append(parts, trust)

	detail := path + " — " + strings.Join(parts, "; ")
	if len(s.Permissions.Allow) > 0 {
		detail += ". An allow rule is what turns agy's refusal into a completed write, " +
			"which a triage run can only report afterwards and then fail on"
	}
	// With Sirdar's own project file unavailable, this file is the only
	// thing that can grant a read, and a run without one answers out of
	// the ticket text. That is a failure rather than a warning, for the
	// reason readAccessCheck gives.
	if !readsGranted && !allowsRead(s.Permissions.Allow) {
		return provider.Check{Name: name, Detail: detail + ". Sirdar cannot write its own project file, and " +
			"this one allows no read either, so a triage would read nothing: add \"read_file(*)\" to permissions.allow"}
	}
	return provider.Warn(name, detail)
}

// allowsRead reports whether any of the operator's own allow rules grants
// a read. The rule syntax is <permission>(<target>), and read_file is the
// permission every read tool on this CLI goes through — `view_file`,
// `grep_search`, `list_dir` and `find_by_name` alike, which is what the
// headless refusal names when it asks for one.
func allowsRead(allow []string) bool {
	for _, rule := range allow {
		if strings.HasPrefix(strings.TrimSpace(rule), "read_file(") {
			return true
		}
	}
	return false
}

func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, stateDirParent, stateDirChild, "settings.json"), nil
}

func ruleList(rules []string) string {
	if len(rules) == 0 {
		return "none"
	}
	out := append([]string(nil), rules...)
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// trustsWorkspace reports whether root is one of the trusted workspaces.
// Both sides are resolved before they are compared, so a workspace reached
// through a symlink is not reported as untrusted.
func trustsWorkspace(trusted []string, root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	want := resolvePath(root)
	for _, t := range trusted {
		if t = strings.TrimSpace(t); t != "" && resolvePath(t) == want {
			return true
		}
	}
	return false
}

func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

// mcpCheck reports what MCP servers the session will actually see, which
// is never the workspace's .mcp.json: the CLI reads one global file and
// takes no flag that narrows it. The row is a warning whatever the
// workspace configured, because the gap is the same either way — it just
// matters more to a workspace that asked for the restriction.
func mcpCheck(cfg provider.DoctorConfig) provider.Check {
	servers := globalMCPServers()
	detail := "the CLI loads ~/.gemini/config/mcp_config.json for every session and takes no " +
		"flag that narrows or replaces it"
	switch {
	case len(servers) == 0:
		detail += "; it declares no servers, so this session sees none"
	default:
		detail += "; this session will see " + strings.Join(servers, ", ")
	}
	if cfg.MCPWorkspaceOnly {
		return provider.Warn("agy mcp scope", "mcp.workspaceOnly cannot be enforced: "+detail+
			". Remove servers from that file, or drive this workspace with provider: claude or codex")
	}
	return provider.Warn("agy mcp scope", detail)
}

// globalMCPServers reads the one MCP file the CLI consults, sorted so the
// row is stable. A missing or unparseable file means no servers, which is
// what the CLI itself does with it.
func globalMCPServers() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(home, stateDirParent, "config", "mcp_config.json"))
	if err != nil {
		return nil
	}
	var f struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil
	}
	names := make([]string, 0, len(f.MCPServers))
	for name := range f.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// fixCheck states the refusal up front, so an operator reads it in
// `sirdar doctor` rather than discovering it when a fix run dies.
func fixCheck() provider.Check {
	return provider.Warn("agy fix mode", "`sirdar fix` is refused on provider agy: the CLI gives Sirdar "+
		"no way to mediate a tool call, so a write session could only be started with "+
		"--dangerously-skip-permissions, which approves every tool including a write into .git/hooks. "+
		"Triage and rca are unaffected")
}

// listModels runs `agy models` and returns the model ids it names. The
// output is two tab-separated columns, id then display name.
func listModels(ctx context.Context, binary string) (map[string]bool, error) {
	out, err := runWithTimeout(ctx, binary, "models")
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, firstLine(out))
	}
	models := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		// A real row is "<id>\t<display name>". The progress line
		// ("Fetching available models...") and any stray output carry no
		// tab, which is the only thing that separates them reliably: a
		// model id has dots in it too (gemini-3.6-flash-low).
		id, _, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found || id == "" {
			continue
		}
		models[id] = true
	}
	return models, nil
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

// session is one running agy process.
type session struct {
	binary    string
	cmd       *exec.Cmd
	cancelRun context.CancelFunc
	stdin     io.WriteCloser
	stderr    *tailWriter
	stateDir  string

	// project is the session's own project file under
	// ~/.gemini/config/projects, deleted when the session ends. Empty
	// when it could not be written, in which case the session ran on the
	// operator's own settings.
	project string

	events   chan provider.Event
	readDone chan struct{}
	done     chan struct{}

	// pending carries a tool call's arguments from the ACTIVE line that
	// started the step to the DONE line that ended it, keyed by
	// step_index. It is touched only by the read goroutine, so it needs no
	// lock of its own.
	pending map[int]toolArgsMemo

	// reads and readsDenied are what the session saw of the codebase, and
	// share pending's confinement to the read goroutine. See observeRead.
	reads       int
	readsDenied int
	blindSent   bool

	writeMu   sync.Mutex
	stdinShut bool

	mu      sync.Mutex
	handle  string
	res     provider.Result
	waitErr error
	meter   usageMeter

	waitOnce sync.Once
}

// usageMeter accumulates what the session has spent. Step lines carry one
// step's tokens and result lines carry the conversation's own totals, so
// the running count is added to by the first and replaced by the second.
// There is no cost figure on this wire at all, so CostUSD stays zero and
// budget.maxUsd never bites.
type usageMeter struct {
	turns  int
	inTok  int64
	outTok int64
}

func (s *session) Events() <-chan provider.Event { return s.events }

func (s *session) Handle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handle
}

// Send delivers a follow-up user message. Unlike Qwen Code, this CLI
// accepts --json-schema and --input-format stream-json together, so the
// schema retry is one more line on stdin rather than a fresh --resume.
func (s *session) Send(ctx context.Context, userText string) error {
	select {
	case <-s.done:
		return errors.New("agy session has exited")
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.writeUser(userText)
}

// CloseInput closes the CLI's stdin. Under --input-format stream-json the
// process runs one turn per line and then waits for the next, holding
// stdout open, so EOF is what lets a run that has its answer finish.
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

// Wait reaps the process and returns the session's Result.
func (s *session) Wait() (provider.Result, error) {
	s.waitOnce.Do(func() {
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
				s.res.ExitErr = fmt.Errorf("%s cancelled: %w%s", s.binary, err, formatTail(tail))
			default:
				s.res.ExitErr = err
				s.waitErr = err
			}
		}
		s.mu.Unlock()
		s.cancelRun()
		// The child is reaped by now, so the permission file it was
		// started under has done its job. It lives in the operator's own
		// directory and is removed on every exit path: here, in Cancel,
		// and from the failed-Start paths above.
		removeProject(s.project)
		close(s.done)
	})
	<-s.done

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.res, s.waitErr
}

// Cancel stops the session through the same path as a cancelled context.
// stdin is marked shut without being closed, so the Wait that follows does
// not hand a process being interrupted an EOF it would exit cleanly on.
func (s *session) Cancel() {
	s.writeMu.Lock()
	s.stdinShut = true
	s.writeMu.Unlock()
	s.cancelRun()
	// Cancel is the path a breach and a budget take, and neither is
	// guaranteed to be followed by a Wait that gets as far as reaping the
	// child. The project file is removed here too; Wait's own removal is
	// then a no-op.
	removeProject(s.project)
}

// read consumes stdout until EOF, emitting one or more events per line.
// There is no control channel to answer: every line is output.
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
		for _, ev := range decode(line) {
			s.rememberTool(&ev)
			s.measure(&ev)
			s.absorb(ev)
			s.observeRead(ev)
			// Ahead of the answer it judges, because the run layer files
			// the note the moment the final event reaches it.
			if blind := s.blindBefore(ev); blind != nil {
				s.events <- *blind
			}
			s.events <- ev
			if breach := s.observeWrite(ev); breach != nil {
				s.events <- *breach
			}
		}
	}
	if err := sc.Err(); err != nil {
		ev := newEvent(provider.EvError, nil)
		ev.Text = "read agy output: " + err.Error()
		s.events <- ev
	}
}

// toolArgsMemo is what an ACTIVE line said about a tool call, kept until
// its DONE line arrives.
type toolArgsMemo struct {
	name string
	args json.RawMessage
}

// rememberTool carries a tool call's name and arguments from the line that
// started the step to the line that ended it.
//
// The exemption and the breach both need the path, and the capture in
// docs/research/10-antigravity-wire-formats.md shows the terminal line
// eliding it: the ACTIVE line spells out `"tool_info":{"name":...,
// "parameters":{"TargetFile":"/…/scratch/a.txt"}}` and the DONE line that
// follows carries `"tool_info":{…}`. Reading the path off the DONE line
// alone therefore gets nothing, which would exempt nothing and name
// nothing. step_index is what pairs the two lines.
//
// The enrichment happens before the event is emitted, so the run's event
// log and the operator's progress line get the path too, not just the
// breach check.
func (s *session) rememberTool(ev *provider.Event) {
	idx, ok := stepIndex(ev.Raw)
	if !ok {
		return
	}
	switch ev.Kind {
	case provider.EvToolStarted:
		if ev.Tool != "" || len(ev.Input) > 0 {
			s.pending[idx] = toolArgsMemo{name: ev.Tool, args: ev.Input}
		}
	case provider.EvToolFinished, provider.EvPermission:
		memo, held := s.pending[idx]
		if !held {
			return
		}
		delete(s.pending, idx)
		if ev.Tool == "" {
			ev.Tool = memo.name
		}
		if emptyArgs(ev.Input) {
			ev.Input = memo.args
		}
	}
}

// emptyArgs reports whether a tool call's arguments say nothing, which is
// what an elided tool_info leaves behind.
func emptyArgs(input json.RawMessage) bool {
	s := strings.TrimSpace(string(input))
	return s == "" || s == "null" || s == "{}"
}

// observeRead counts what the session managed to read and what it was
// refused. Both counters are touched only by the read goroutine, like
// pending, so neither needs a lock.
//
// A read counts when its step ends without an error message: a DONE line
// for one of the readTools. An ERROR line — a refusal, a missing file, a
// binary file the CLI declined to decode — is not a read, whatever the
// reason, because none of them put a line of the codebase in the prompt.
func (s *session) observeRead(ev provider.Event) {
	switch ev.Kind {
	case provider.EvToolFinished:
		if readTools[ev.Tool] && ev.Text == "" {
			s.reads++
		}
	case provider.EvPermission:
		if ev.Decision == "deny" && (readTools[ev.Tool] || searchTools[ev.Tool]) {
			s.readsDenied++
		}
	}
}

// blindBefore is the check that stops a triage note being written out of
// the ticket text alone.
//
// Round 1 of this provider completed a run in 36 seconds and filed a
// high-confidence note. Every `view_file` in it had been auto-denied,
// because a headless `agy` cannot prompt for a permission and nothing had
// granted one: the agent answered from the ticket description, and the
// note that reached the register was indistinguishable from one built on
// the code. The project file (project.go) is what fixes the cause; this is
// what makes the failure visible when it does not.
//
// It fires on the final event, which is where the run layer would file,
// and only once — a schema retry produces a second final, and the verdict
// on the session does not change between them. The reason is a fact about
// the session rather than a judgement about the answer: how many reads
// were refused, or that none was attempted.
func (s *session) blindBefore(ev provider.Event) *provider.Event {
	if ev.Kind != provider.EvFinal || s.blindSent || s.reads > 0 {
		return nil
	}
	s.blindSent = true

	reason := "the agent could read nothing (no read tool was called)"
	if s.readsDenied > 0 {
		reason = fmt.Sprintf("the agent could read nothing (%d reads denied)", s.readsDenied)
	}
	out := newEvent(provider.EvBlind, ev.Raw)
	out.Text = reason + "\n" +
		"this session completed no read of a file, so its answer was written out of " +
		"the ticket text alone and is not a triage. agy decides permissions from files rather than from " +
		"a channel Sirdar can answer, so a refused read is reported after the fact: check the permission " +
		"events in this run, and `sirdar doctor` for whether this session got the project file that grants " +
		"read_file"
	return &out
}

// observeWrite is this adapter's substitute for a permission policy.
//
// Sirdar cannot judge an `agy` tool call: by the time one is on the
// stream, the CLI has already allowed or refused it. What Sirdar can do is
// notice that a triage session, which is supposed to write nothing,
// finished a write or ran a command — and end the run over it. A read-only
// guarantee that has already failed is not something a run can carry a
// warning about and go on to file a note under.
//
// Plan mode's own implementation-plan artifact is the one exemption, and
// it is drawn as narrowly as the conversation id allows:
// <state dir>/brain/<conversation id>/. That directory is this session's
// own and is written on every plan-mode run. Everything else under the
// state directory — scratch/ above all, where the research capture caught
// a real write landing — is a breach like any other.
func (s *session) observeWrite(ev provider.Event) *provider.Event {
	if ev.Kind != provider.EvToolFinished {
		return nil
	}
	isWrite := writeTools[ev.Tool]
	if !isWrite && !execTools[ev.Tool] {
		return nil
	}
	if isWrite && s.withinPlanDir(ev.Input) {
		note := systemNotice("agy wrote " + targetOf(ev.Input) + " inside this conversation's own plan directory; " +
			"plan mode keeps its implementation-plan artifact there, outside the workspace")
		return &note
	}

	subject := subjectOf(ev.Input)
	headline := "read-only breach: " + ev.Tool
	if subject != "" {
		headline += " " + oneLine(subject)
	}
	breach := newEvent(provider.EvBreach, ev.Raw)
	breach.Tool = ev.Tool
	breach.Input = ev.Input
	breach.Text = headline + "\na triage session completed " + ev.Tool +
		", which agy should have refused. Sirdar cannot mediate an agy tool call, so this run is " +
		"ended rather than filed. Check ~/.gemini/antigravity-cli/settings.json for a " +
		"permissions.allow rule that approves it — Sirdar can neither see nor override that file"
	return &breach
}

// withinPlanDir reports whether a tool call's target path resolves inside
// <state dir>/brain/<conversation id>/. With no state directory known, or
// before any line has named the conversation, nothing is exempt — which is
// the safe direction: an unexempted write is reported, not hidden.
func (s *session) withinPlanDir(input json.RawMessage) bool {
	if s.stateDir == "" {
		return false
	}
	conversation := s.Handle()
	if conversation == "" {
		return false
	}
	target := targetOf(input)
	if target == "" || !filepath.IsAbs(target) {
		return false
	}
	real, err := provider.EvalNearest(target)
	if err != nil {
		real = filepath.Clean(target)
	}
	planDir := filepath.Join(s.stateDir, planDirName, conversation)
	if resolved, err := filepath.EvalSymlinks(planDir); err == nil {
		planDir = resolved
	}
	rel, err := filepath.Rel(planDir, real)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func targetOf(input json.RawMessage) string {
	var args toolArgs
	_ = json.Unmarshal(input, &args)
	return args.target()
}

func subjectOf(input json.RawMessage) string {
	var args toolArgs
	_ = json.Unmarshal(input, &args)
	return args.subject()
}

// oneLine flattens a multi-line command into something a run's terminal
// reason can carry on one line.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 120
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// measure turns a usage event into running session totals.
func (s *session) measure(ev *provider.Event) {
	if ev.Kind != provider.EvUsage {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if isResultLine(ev.Raw) {
		// The result line's figures are the conversation's own totals, so
		// they replace the running count rather than adding to it.
		s.meter.turns = ev.Turns
		s.meter.inTok = ev.InputTok
		s.meter.outTok = ev.OutputTok
		return
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

	if id := conversationID(ev.Raw); id != "" {
		s.handle = id
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

// writeUser sends one user turn. The shape is the CLI's own, not Claude
// Code's: a top-level "event" discriminator, and a message object whose
// content is an array of blocks. A line missing either is rejected before
// any model turn runs.
func (s *session) writeUser(text string) error {
	payload := map[string]any{
		"event": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": text}},
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
	if s.stdinShut {
		return errors.New("agy session input is closed")
	}
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
