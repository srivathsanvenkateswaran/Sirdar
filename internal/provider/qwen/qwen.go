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
//     from inside this process. The hook fails open — an unreachable
//     listener means the tool runs — so it is authenticated with a
//     per-session token, probed before the session is handed back, and
//     watched for the rest of the run; and the tools a triage session must
//     never reach are taken off the command line rather than left to it.
//   - --json-schema and --input-format stream-json are mutually exclusive,
//     so a session takes exactly one user message. Send always fails and
//     the runner carries the schema retry into a --resume of the handle.
//   - The result line reports no cost, so a USD budget cannot bite.
package qwen

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
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
	// hook before giving up on it. A timeout is a non-blocking hook
	// failure, so a decision that arrives late is an allow: the answer is
	// therefore written and flushed before anything that can block.
	hookTimeoutSeconds = 15

	// hookPathPrefix is the endpoint the child posts PreToolUse events
	// to; the session's token is the last path segment. probePathPrefix
	// answers Start's own reachability probe and nothing else.
	hookPathPrefix  = "/decide/"
	probePathPrefix = "/probe/"

	// hookTokenBytes is the size of the per-session secret that
	// authenticates the child to the permission hook. It lives in the
	// 0600 settings file and in this process's memory, and is never put
	// in an event, a log line or an error.
	hookTokenBytes = 32

	// hookProbeTimeout bounds the reachability probe Start makes before
	// it hands the session back.
	hookProbeTimeout = 5 * time.Second

	// toolCallsPerTurn converts budget.maxTurns into --max-tool-calls,
	// and defaultMaxToolCalls bounds a session that named no turn budget.
	// structured_output is exempt from the CLI's counter, so the terminal
	// call needs no allowance of its own.
	toolCallsPerTurn    = 4
	defaultMaxToolCalls = 200

	// noMCPServer is the name passed to --allowed-mcp-server-names when a
	// session must see no MCP servers at all. Qwen Code has no
	// --strict-mcp-config: --mcp-config merges with the operator's own
	// settings, and the allow-list by name is the only thing that
	// narrows the set. A name no server has yields an empty set.
	noMCPServer = "__sirdar_none__"

	// shellTool is the one tool whose exclusion Sirdar ever lifts, and
	// only for a workspace that named permissions.bash patterns. The CLI
	// cannot express "shell, but only these commands" — a rule written
	// against the canonical name allows every command, whatever specifier
	// it carries — so the allow-list is enforced by the hook.
	shellTool = "run_shell_command"
)

// qwenCoreTools is every core tool Qwen Code 0.23.3 can register: the
// ToolNames table in packages/core/src/tools/tool-names.ts, plus the
// legacy names ToolNamesMigration still resolves (replace → edit,
// task → agent, search_file_content → grep_search) and read_many_files.
//
// It is written out rather than discovered so that a version bump shows
// up as a diff here. A tool a later Qwen adds is not on this list, so it
// stays registered and is refused by the hook — the safe direction.
var qwenCoreTools = []string{
	"agent", "artifact", "ask_user_question", "create_sub_session",
	"cron_create", "cron_delete", "cron_list", "display_image", "edit",
	"enter_plan_mode", "enter_worktree", "exit_plan_mode", "exit_worktree",
	"get_goal", "glob", "grep_search", "image_gen", "list_agents",
	"list_directory", "loop_wakeup", "lsp", "monitor", "notebook_edit",
	"propose_goal", "read_file", "read_many_files", "read_mcp_resource",
	"record_artifact", "record_source", "replace", "report_findings",
	"request_shutdown", "run_shell_command", "save_memory",
	"search_file_content", "send_message", "skill", "structured_output",
	"task", "task_create", "task_list", "task_stop", "task_update",
	"team_create", "team_delete", "team_plan_approval", "todo_write",
	"tool_search", "update_goal", "web_fetch", "web_search", "workflow",
	"write_file", "zoom_image",
}

// keptTools are the core tools a triage session leaves registered: the
// names policyNames maps onto a read verdict the permission policy
// already knows how to judge, the todo list, and the terminal
// structured_output call. The shell is the one conditional member —
// args excludes it unless the workspace named permissions.bash patterns.
//
// Everything else in qwenCoreTools is excluded on every session, which
// makes the flag a whitelist in exclusion clothing: a core tool is either
// judged by the policy under a name it understands, or never registered.
var keptTools = map[string]bool{
	"read_file":           true,
	"read_many_files":     true,
	"read_mcp_resource":   true,
	"grep_search":         true,
	"search_file_content": true,
	"glob":                true,
	"list_directory":      true,
	"web_fetch":           true,
	"web_search":          true,
	"todo_write":          true,
	"structured_output":   true,
	shellTool:             true,
}

// excludedTools are passed to --exclude-tools on every session. They are
// the settings-proof half of the read-only guarantee.
//
// Qwen Code's own headless deny (denyUnlessAllowed in 0.23.3) refuses
// run_shell_command, monitor, edit and write_file under --approval-mode
// default — but only when isExplicitlyAllowed says no, and that consults
// permissions.allow, tools.allowed and tools.core merged across the
// system, user (~/.qwen/settings.json) and workspace (.qwen/settings.json)
// layers. An operator who once allowed run_shell_command globally, or a
// repository carrying its own .qwen/settings.json, therefore cancels that
// deny. --exclude-tools does not consult any of it: the flag is appended
// to the merged deny list unconditionally, isToolEnabled returns false for
// an excluded name in both its branches, and PermissionManager consults
// deny rules before allow rules, so the tool is never registered and the
// model never sees it.
//
// The agent / skill / task family is on the list for a second reason: it
// is how repository-controlled configuration reaches the permission
// decision. A project skill's frontmatter hooks are registered through
// SessionHooksManager (applySkillHooks) and a declarative subagent's
// through HookRegistry.addAgentHooks, both under source "session", which
// getSourcePriority ranks last and which fireHooks appends after every
// registry hook. The outputs are then merged by mergeWithOrLogic, whose
// hookSpecificOutput loop assigns each field over the previous output's —
// so the last permissionDecision written wins, and a hook that runs after
// Sirdar's can turn a deny into an allow. Taking away the tools that
// register one closes that path; the folder-trust posture (see
// writeTrustedFolders) closes it a second time.
var excludedTools = buildExcludedTools()

// buildExcludedTools is qwenCoreTools minus keptTools, in the order the
// core list is written, so --exclude-tools is stable across runs.
func buildExcludedTools() []string {
	out := make([]string, 0, len(qwenCoreTools))
	for _, name := range qwenCoreTools {
		if !keptTools[name] {
			out = append(out, name)
		}
	}
	return out
}

// deniedTools are pinned into the session's settings file as well. The
// --exclude-tools flag above is the guarantee; this is the belt-and-braces
// half, the way the Claude adapter passes --disallowedTools alongside its
// permission policy.
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

// qwenEnvKeys are the variables Qwen Code reads to decide which backend a
// session talks to, which credentials it uses, where it keeps its settings
// and state, and what system prompt it runs under. They are stripped from
// the child environment unconditionally, so a session's endpoint is
// whatever the workspace configured and its settings are the ones Sirdar
// wrote — never whatever happens to be exported in the shell that launched
// Sirdar. The ones Sirdar sets itself go back in afterwards.
var qwenEnvKeys = []string{
	// backend and credentials
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"OPENAI_MODEL",
	"QWEN_API_KEY",
	"QWEN_BASE_URL",
	"QWEN_MODEL",
	"QWEN_CODE_MODEL",
	"QWEN_OAUTH",
	"QWEN_DEFAULT_AUTH_TYPE",
	"GEMINI_API_KEY",
	"GEMINI_MODEL",
	// settings, state and trust locations
	"QWEN_HOME",
	"QWEN_DIR",
	"QWEN_CODE_SYSTEM_SETTINGS_PATH",
	"QWEN_CODE_SYSTEM_DEFAULTS_PATH",
	"QWEN_CODE_TRUSTED_FOLDERS_PATH",
	"QWEN_CODE_MCP_APPROVALS_PATH",
	// system prompt overrides; QWEN_WRITE_SYSTEM_MD also writes a file
	"QWEN_SYSTEM_MD",
	"QWEN_WRITE_SYSTEM_MD",
	"QWEN_SYSTEM_IDENTITY_MD",
	// transport safety: equivalent to --insecure
	"QWEN_TLS_INSECURE",
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
	// --max-tool-calls is the third bound, and the one that stops a
	// session looping on a tool the policy keeps refusing. A turn can
	// carry several calls, so the budget is scaled rather than mapped one
	// to one; a session that named no turn budget still gets a ceiling.
	out = append(out, "--max-tool-calls", strconv.Itoa(maxToolCalls(spec.Budget)))
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
	// The read-only guarantee. Every write-capable tool is excluded on
	// every session, whatever the operator's or the repository's own
	// settings say, because --exclude-tools is merged into the deny list
	// without consulting them and deny beats allow (see excludedTools).
	for _, name := range excludedTools {
		out = append(out, "--exclude-tools", name)
	}
	// The shell is excluded the same way unless the workspace named
	// permissions.bash patterns. When it did, the hook is the gate: it
	// decides every command, and a hook that cannot be reached aborts the
	// session rather than letting one through (see session.watchHook).
	if spec.Policy != nil && len(spec.Policy.BashAllow) > 0 {
		out = append(out, "--allowed-tools", shellTool)
	} else {
		out = append(out, "--exclude-tools", shellTool)
	}
	return out
}

// maxToolCalls converts a turn budget into the CLI's cumulative tool-call
// ceiling.
func maxToolCalls(b provider.Budget) int {
	if b.MaxTurns > 0 {
		return b.MaxTurns * toolCallsPerTurn
	}
	return defaultMaxToolCalls
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
func childEnv(spec provider.SessionSpec, ep Endpoint, settingsPath, trustedPath string) []string {
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
	if trustedPath != "" {
		out = append(out, "QWEN_CODE_TRUSTED_FOLDERS_PATH="+trustedPath)
	}
	return out
}

// workspaceDir is the directory the session will run in, absolute, which
// is what the trusted-folders rule has to name.
func workspaceDir(cwd string) (string, error) {
	if cwd == "" {
		return os.Getwd()
	}
	return filepath.Abs(cwd)
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
// and returns its path. It carries three things: the PreToolUse hook that
// asks Sirdar about every tool call, a deny list for the write tools, and
// the folder-trust switch.
//
// QWEN_CODE_SYSTEM_SETTINGS_PATH replaces the *system* settings layer and
// nothing else: the user's ~/.qwen/settings.json and the workspace's
// .qwen/settings.json are still loaded and merged on top of it. This file
// is therefore where Sirdar's hook is registered, not an isolation
// boundary — the guarantees that have to hold whatever those layers say
// are on the command line instead (see excludedTools).
//
// folderTrust is the one exception, and it is an isolation boundary for
// the workspace layer specifically. security.folderTrust.enabled turns
// Qwen Code's trust check on (isFolderTrustEnabled defaults it to false,
// which means every folder is trusted); the trust decision itself is then
// taken from the system and user layers merged with the trusted-folders
// file, never from the workspace layer, so a repository cannot vote
// itself trusted. With the folder untrusted, mergeSettings substitutes an
// empty object for the whole workspace layer — .qwen/settings.json stops
// being read at all — and the project agents, project skills and MCP
// discovery that hang off isTrustedFolder() are skipped with it.
//
// The URL carries the session's token as its last path segment. The file
// is written 0600 and removed when the session ends, and the token appears
// nowhere else.
func writeSettings(dir, hookURL string, folderTrust bool) (string, error) {
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
		"security": map[string]any{
			"folderTrust": map[string]any{"enabled": folderTrust},
		},
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

// doNotTrust is the trusted-folders verdict that makes a directory, and
// everything under it, untrusted. Qwen Code resolves the deepest matching
// rule and breaks ties in favour of an untrusted one.
const doNotTrust = "DO_NOT_TRUST"

// writeTrustedFolders writes the session's own trusted-folders file,
// naming the workspace as untrusted, and returns its path.
//
// QWEN_CODE_TRUSTED_FOLDERS_PATH replaces the file the trust decision is
// read from (getTrustedFoldersPath), so this is what the CLI consults
// rather than the operator's ~/.qwen/trustedFolders.json — a folder the
// operator once trusted interactively does not carry that trust into a
// Sirdar run. The symlink-resolved path is written alongside the plain
// one because the CLI canonicalises the workspace before matching.
//
// The file is 0600 and lives beside the settings file, so it goes when
// the session's directory does.
func writeTrustedFolders(dir, workspace string) (string, error) {
	rules := map[string]string{workspace: doNotTrust}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		rules[resolved] = doNotTrust
	}
	b, err := json.Marshal(rules)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "trustedFolders.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// trustNeededForMCP reports whether the session has to run in a trusted
// folder to do what it was asked to do.
//
// Qwen Code skips MCP discovery outright in an untrusted folder —
// discoverAllMcpTools, discoverAllMcpToolsIncremental and
// readMcpResource all return early on isTrustedFolder() === false — so a
// session that was configured to load MCP servers would quietly get none
// of them. A workspace that named servers therefore keeps trust, and
// pays for it with a live .qwen/settings.json layer; one that named none
// (mcp.workspaceOnly with no .mcp.json, which is the default) runs
// untrusted.
func trustNeededForMCP(spec provider.SessionSpec) bool {
	return spec.MCPConfig != "" || !spec.MCPStrict
}

// userSettingsHooks reports whether the operator's own user-scope
// settings file registers hooks, and names the file when it does.
//
// This decides whether Sirdar's hook reaches the child at all. The CLI
// hands Config a userHooks value of `settings.getUserHooks() ?? merged`,
// and getUserHooks reads the *user scope alone* — so with no user hooks
// the merged settings (which carry Sirdar's system-layer hook) are used
// and the hook is registered ungated, but with user hooks present
// Sirdar's hook falls through to getProjectHooks(), which returns nothing
// in an untrusted folder. The session would then run with no mediator at
// all, which reconcile would catch at the first tool result and abort.
// Refusing up front says so in one place instead.
func userSettingsHooks(env []string) (string, bool) {
	home := envValue(env, "HOME")
	if home == "" {
		home = envValue(env, "USERPROFILE")
	}
	if home == "" {
		return "", false
	}
	path := filepath.Join(home, ".qwen", "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var f struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		// An unreadable user settings file is the CLI's problem to
		// report, not a reason to refuse a session here.
		return "", false
	}
	return path, len(f.Hooks) > 0
}

func envValue(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], name+"="); ok {
			return v
		}
	}
	return ""
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
	env := spec.Env
	if len(env) == 0 {
		env = os.Environ()
	}
	// A user-scope hooks block displaces Sirdar's own (see
	// userSettingsHooks), which would leave the session unmediated. That
	// is a configuration Sirdar cannot make safe, so it is refused here
	// rather than discovered at the first tool call.
	if path, ok := userSettingsHooks(env); ok {
		return nil, fmt.Errorf("qwen permission hook: %s registers hooks of its own, "+
			"which take the place of Sirdar's PreToolUse hook and would leave the session "+
			"with no permission mediator; move those hooks elsewhere to run qwen under Sirdar", path)
	}
	workspace, err := workspaceDir(spec.Cwd)
	if err != nil {
		return nil, fmt.Errorf("qwen workspace: %w", err)
	}

	// The permission hook listens before the child starts, so the first
	// tool call cannot race the listener.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("qwen permission hook: %w", err)
	}
	token, err := newHookToken()
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("qwen permission hook: %w", err)
	}
	dir, err := os.MkdirTemp("", "sirdar-qwen-")
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("qwen settings: %w", err)
	}
	base := "http://" + listener.Addr().String()
	// Trust is off unless the session was configured to load MCP servers,
	// which an untrusted folder would silently give it none of.
	folderTrust := !trustNeededForMCP(spec)
	settingsPath, err := writeSettings(dir, base+hookPathPrefix+token, folderTrust)
	if err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("qwen settings: %w", err)
	}
	trustedPath, err := writeTrustedFolders(dir, workspace)
	if err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("qwen settings: %w", err)
	}

	// runCtx is the one cancellation path: Cancel() cancels it, and so
	// does the caller's ctx. cmd.Cancel turns either into SIGINT, and the
	// escalation it schedules kills the whole process group.
	runCtx, cancelRun := context.WithCancel(ctx)
	reaped := make(chan struct{})
	cmd := exec.CommandContext(runCtx, binary, args(spec, p.endpoint, mcpNames)...)
	cmd.Cancel = func() error {
		// SIGINT first: Qwen Code writes its result line on the way out.
		err := cmd.Process.Signal(os.Interrupt)
		// Go's own WaitDelay escalation signals the process, not the
		// group, so a tool the CLI spawned would survive it. This one
		// takes the group, and stands down once the child is reaped so a
		// recycled pid is never signalled.
		time.AfterFunc(interruptGrace, func() {
			select {
			case <-reaped:
			default:
				_ = procgroup.Kill(cmd)
			}
		})
		return err
	}
	// Later than the group kill above, which is the escalation that
	// should get there first.
	cmd.WaitDelay = interruptGrace + 2*time.Second
	cmd.Dir = spec.Cwd
	cmd.Env = childEnv(spec, p.endpoint, settingsPath, trustedPath)
	// Its own process group, so an abort can take the whole tree down
	// rather than leaving a tool the CLI spawned running behind it.
	procgroup.Setup(cmd)

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
		token:      token,
		decided:    map[string]struct{}{},
		started:    map[string]string{},
		events:     make(chan provider.Event, eventBuffer),
		readDone:   make(chan struct{}),
		hookDone:   make(chan struct{}),
		done:       make(chan struct{}),
		reaped:     reaped,
	}
	s.hook = &http.Server{Handler: s.hookMux(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		defer close(s.hookDone)
		_ = s.hook.Serve(listener)
	}()

	if err := cmd.Start(); err != nil {
		s.stopHook()
		return fail(fmt.Errorf("start %s: %w", binary, err))
	}

	// A hook that cannot be reached is not a degraded session, it is an
	// unmediated one: Qwen Code treats a connection failure, a timeout
	// and a non-2xx alike as a non-blocking hook failure and runs the
	// tool. So the listener is proved to be answering before the session
	// is handed back, and watched for the rest of the run.
	if err := probeHook(base + probePathPrefix + token); err != nil {
		s.abort()
		_ = cmd.Wait()
		s.noteReaped()
		s.stopHook()
		return fail(fmt.Errorf("qwen permission hook: %w", err))
	}
	go s.watchHook()

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
			s.noteReaped()
			s.stopHook()
			s.removeSettings()
		}()
		return nil, fmt.Errorf("qwen prompt: %w", writeErr)
	}
	return s, nil
}

// newHookToken mints the secret that authenticates the child to the
// permission hook.
func newHookToken() (string, error) {
	b := make([]byte, hookTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// probeHook confirms the permission listener is answering on the address
// the child was given. The error it returns never names the URL, because
// the URL carries the session's token.
func probeHook(probeURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), hookProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, probeURL, strings.NewReader("{}"))
	if err != nil {
		return errors.New("the probe request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("unreachable: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("the probe was answered with %s", resp.Status)
	}
	return nil
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
	token      string // authenticates the child to the permission hook

	events   chan provider.Event
	readDone chan struct{} // closed when stdout hits EOF
	hookDone chan struct{} // closed when the hook server has stopped
	done     chan struct{} // closed when the process has been reaped
	reaped   chan struct{} // closed as soon as cmd.Wait has returned

	hookClosing atomic.Bool // set before the hook is shut down on purpose

	emitMu sync.RWMutex // held for reading while an event is sent
	closed bool         // set under emitMu before events is closed

	mu      sync.Mutex
	handle  string
	res     provider.Result
	waitErr error
	meter   usageMeter

	// decided holds the ids of the tool calls the hook answered, and
	// started the ids of the tool calls the stream announced but has not
	// reported a result for yet. A result for a call that ran without a
	// decision is a bypassed hook (see reconcile).
	decided map[string]struct{}
	started map[string]string

	// aborted is why the session was stopped from inside the adapter,
	// which is only ever the permission hook going away.
	aborted string

	waitOnce     sync.Once
	settingsOnce sync.Once
	cleanupOnce  sync.Once
	reapOnce     sync.Once
}

// noteReaped records that the child has been waited on, which stands the
// scheduled process-group kill down: after this point the pid may belong
// to something else.
func (s *session) noteReaped() { s.reapOnce.Do(func() { close(s.reaped) }) }

// stopHook shuts the permission listener down on purpose and waits for
// Serve to return, so watchHook can tell a deliberate close from a server
// that stopped under the session's feet.
func (s *session) stopHook() {
	s.hookClosing.Store(true)
	_ = s.hook.Close()
	<-s.hookDone
}

// removeSettings deletes the 0600 settings file and the directory holding
// it. It is safe to call more than once and from more than one goroutine.
func (s *session) removeSettings() {
	s.settingsOnce.Do(func() {
		if s.settingsIn != "" {
			_ = os.RemoveAll(s.settingsIn)
		}
	})
}

// abort stops the child now. SIGINT and the WaitDelay escalation are the
// polite path; this one is for a session that has lost its mediator, where
// anything the CLI already spawned has to go too.
func (s *session) abort() {
	s.cancelRun()
	if s.cmd == nil {
		return
	}
	select {
	case <-s.reaped:
		return // the pid may have been recycled by now
	default:
	}
	_ = procgroup.Kill(s.cmd)
}

// watchHook aborts the session if the permission listener stops serving
// before the process has been reaped. An early Serve return — a listener
// closed under it, an accept loop that failed — would otherwise leave the
// run going with every tool call failing open.
//
// The reason is recorded rather than emitted from here. emit blocks while
// the caller is slow to drain, and nothing should be allowed to run while
// this goroutine waits for a reader; the stdout reader publishes the
// notice on its way out, and Wait carries it in the Result either way.
func (s *session) watchHook() {
	select {
	case <-s.done:
		return
	case <-s.hookDone:
	}
	if s.hookClosing.Load() {
		return
	}
	s.setAborted("the Sirdar permission hook stopped serving; the session was aborted rather " +
		"than left running with every tool call failing open")
	s.abort()
}

func (s *session) setAborted(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.aborted == "" {
		s.aborted = reason
	}
}

func (s *session) abortedReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aborted
}

// publishAbort puts the abort notice on the event stream. It runs on the
// stdout reader's way out, the one goroutine that can be sure the channel
// is still open.
func (s *session) publishAbort() {
	reason := s.abortedReason()
	if reason == "" {
		return
	}
	ev := newEvent(provider.EvError, []byte(`{"type":"sirdar_session_aborted"}`))
	ev.Text = reason
	s.emit(ev)
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
		s.noteReaped()
		// Nothing will call the hook once the process has gone.
		s.stopHook()
		s.removeSettings()
		tail := s.stderr.snapshot()

		s.mu.Lock()
		s.res.Handle = s.handle
		s.res.StderrTail = tail
		// An adapter-side abort is the reason the run ended, whatever the
		// signal that carried it out looks like, so it replaces the exit
		// account rather than hiding behind it.
		if s.aborted != "" {
			s.res.ExitErr = fmt.Errorf("%s: %s%s", s.binary, s.aborted, formatTail(tail))
			s.mu.Unlock()
			s.cancelRun()
			close(s.done)
			return
		}
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
//
// Wait owns the cleanup on the normal path. Cancel arranges its own only
// for the caller that cancels and never Waits, which would otherwise leave
// the 0600 settings file on disk. The file is removed once the child's
// stdout has closed, not straight away: Qwen Code re-reads its settings,
// and taking the hook registration away from a process that is still alive
// is exactly the unmediated run this file exists to prevent.
func (s *session) Cancel() {
	s.cancelRun()
	s.cleanupOnce.Do(func() {
		go func() {
			select {
			case <-s.done: // Wait reaped the session and cleaned up
			case <-s.readDone:
				s.removeSettings()
			}
		}()
	})
}

// read consumes stdout until EOF, emitting one or more events per line.
func (s *session) read(stdout io.Reader) {
	defer s.closeEvents()
	defer s.publishAbort()
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
		for _, ev := range s.reconcile(line) {
			s.emit(ev)
		}
		// An aborted session stops being read here. The child is being
		// killed, but whatever it had already written is still in the
		// pipe, and a result line out of that buffer would file a note
		// from a session that had lost its mediator.
		if s.abortedReason() != "" {
			break
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
//
// Both routes are subtree patterns ending in the session's token, and both
// compare it in constant time. Serving the subtree rather than the exact
// token path is deliberate: a request that guesses wrong reaches this code
// and is refused, instead of falling through to a 404 that says nothing.
func (s *session) hookMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(hookPathPrefix, s.decide)
	mux.HandleFunc(probePathPrefix, s.probe)
	return mux
}

// probe answers Start's reachability check. It shares the listener, the
// mux and the token with the hook, so an answer here is proof that a
// PreToolUse post would be served too — and it judges nothing, so the
// probe leaves no permission record behind.
func (s *session) probe(w http.ResponseWriter, r *http.Request) {
	if !s.authentic(r, probePathPrefix) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authentic reports whether a request carries this session's token in its
// path and looks like the CLI's own fetch rather than a browser's.
//
// Without it any process on the machine — or any page the operator happens
// to have open, since the listener speaks plain HTTP on loopback — could
// post forged PreToolUse records into the run's event log, or flood the
// endpoint until a genuine decision missed Qwen Code's hook timeout, which
// turns a deny into an allow.
func (s *session) authentic(r *http.Request, prefix string) bool {
	if r.Method != http.MethodPost {
		return false
	}
	// Qwen Code's HTTP hook is a server-side fetch and never sends an
	// Origin; a request that does is a browser's, whatever it claims.
	if r.Header.Get("Origin") != "" {
		return false
	}
	got := strings.TrimPrefix(r.URL.Path, prefix)
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

// hookRequest is the PreToolUse payload. Every other field of the event —
// session id, transcript path, cwd, timestamp — is ignored.
//
// tool_call_id is the id the stdout stream uses for the same call
// (tool_use.id / tool_result.tool_use_id); tool_use_id is the CLI's own
// internal id. Both are recorded, because the first is what reconcile
// matches against and the second is what older payloads carry.
type hookRequest struct {
	ToolName   string          `json:"tool_name"`
	ToolInput  json.RawMessage `json:"tool_input"`
	ToolUseID  string          `json:"tool_use_id"`
	ToolCallID string          `json:"tool_call_id"`
}

func (s *session) decide(w http.ResponseWriter, r *http.Request) {
	if !s.authentic(r, hookPathPrefix) {
		// No event and no decision payload: a caller that cannot prove it
		// is this session's child gets nothing to work with, and forging a
		// permission record into events.jsonl is not possible.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !isJSONRequest(r) {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}

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
		writeDecision(w, false, "Sirdar policy: the tool call could not be read")
		ev := newEvent(provider.EvError, body)
		ev.Text = "unparseable permission hook request"
		s.emit(ev)
		return
	}

	decision := s.policy.Decide(policyName(req.ToolName), req.ToolInput)
	// The verdict is recorded before the child is answered, so that a
	// tool result for a call with no recorded decision is proof the hook
	// was bypassed rather than a race with this handler.
	s.noteDecision(req.ToolCallID, req.ToolUseID)

	// The answer goes out, and is flushed, before the event does. emit
	// blocks while the caller is slow to drain the channel, and a
	// decision that misses the CLI's hook timeout is a non-blocking hook
	// failure — which is to say a deny that arrives late is an allow.
	writeDecision(w, decision.Allow, decisionReason(decision))

	ev := newEvent(provider.EvPermission, body)
	ev.Tool = req.ToolName
	ev.Input = req.ToolInput
	ev.Decision = "deny"
	if decision.Allow {
		ev.Decision = "allow"
	}
	ev.Text = decision.Message
	s.emit(ev)
}

// isJSONRequest reports whether the body is declared as JSON, which Qwen
// Code's hook always does.
func isJSONRequest(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	return err == nil && mediaType == "application/json"
}

// noteDecision records that the hook judged a tool call.
func (s *session) noteDecision(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if id != "" {
			s.decided[id] = struct{}{}
		}
	}
}

// reconcile matches the stdout stream against the decisions the hook made.
// Qwen Code prints the assistant's tool_use line before it consults the
// PreToolUse hook, so the check cannot be made when the call is announced:
// the first moment a missing decision means anything is the call's result.
//
// The first such result ends the session. A tool that ran without a
// Sirdar decision is a tool that ran unmediated — the CLI's own hook
// failure path is `shouldProceed: true`, so every later call would run the
// same way — and a run whose permission mediator has been cut out is worth
// less than no run at all. So the process group is killed, the reason is
// recorded as the session's abort, and Wait reports a failure.
//
// A result that reports an error is passed over. The CLI refuses an
// excluded tool itself, before any hook is consulted, and those refusals
// arrive as errored results — counting them would bury the one case that
// matters under a steady stream of expected ones.
func (s *session) reconcile(raw []byte) []provider.Event {
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil
	}
	switch l.Type {
	case "assistant":
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, b := range blocksOf(l.Message.Content) {
			if b.Type == "tool_use" && b.ID != "" {
				s.started[b.ID] = b.Name
			}
		}
		return nil
	case "user":
		var out []provider.Event
		for _, b := range blocksOf(l.Message.Content) {
			if b.Type != "tool_result" || b.ToolUseID == "" {
				continue
			}
			s.mu.Lock()
			tool, started := s.started[b.ToolUseID]
			delete(s.started, b.ToolUseID)
			_, decided := s.decided[b.ToolUseID]
			delete(s.decided, b.ToolUseID)
			s.mu.Unlock()

			if !started || decided || b.IsError {
				continue
			}
			reason := tool + " ran without a Sirdar decision; session aborted: " +
				"the permission hook was not asked about the call, so the session " +
				"was no longer mediated"
			ev := newEvent(provider.EvError, raw)
			ev.Tool = tool
			ev.Text = "tool ran without a Sirdar decision; session aborted: " + tool +
				" ran without the permission hook being asked about it"
			out = append(out, ev)
			s.setAborted(reason)
			s.abort()
		}
		return out
	}
	return nil
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
	// net/http buffers the response until the handler returns, and the
	// handler goes on to emit an event that can block. Flushing here is
	// what actually puts the decision on the wire first.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
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
