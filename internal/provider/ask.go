package provider

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Ask kinds: what sort of call a permission question is about. They decide
// which allow-list a grant widens and how the call is summarised.
const (
	AskBash  = "bash"
	AskMCP   = "mcp"
	AskFetch = "fetch"
	AskTool  = "tool"
)

// Verdicts an operator gives a permission question.
const (
	// VerdictAllow allows the one call that was asked about, once.
	VerdictAllow = "allow"
	// VerdictAllowRun allows that call and any later call in the same run
	// that matches the same tool and command pattern.
	VerdictAllowRun = "allow_run"
	// VerdictDeny refuses it, and every later call matching the same
	// pattern, for the rest of the run.
	VerdictDeny = "deny"
)

// ValidVerdict reports whether v is one of the three verdicts.
func ValidVerdict(v string) bool {
	return v == VerdictAllow || v == VerdictAllowRun || v == VerdictDeny
}

// PermissionAsk is a tool call the policy refused and the operator could
// allow: the question a run blocks on when the workspace has permissions.ask
// on. It is what the blocked state carries as question.decision, and what a
// grant is matched against when the session asks again.
type PermissionAsk struct {
	// Kind is bash, mcp, fetch or tool.
	Kind string `json:"kind"`
	// Tool is the tool name as the provider sent it to the policy:
	// Bash, mcp__grafana__query_loki_logs, WebFetch, Read.
	Tool string `json:"tool"`
	// Summary is the call in one line: the command, the URL, the path, or
	// an MCP tool's arguments.
	Summary string `json:"summary"`
	// Patterns is what "allow for this run" would add: one glob per shell
	// segment the allow-list does not cover, the host of a fetch, the
	// directory of a read, or the tool's own name.
	Patterns []string `json:"patterns,omitempty"`
	// Verdict is the policy's own verdict on the call, which is always deny
	// today: an allowed call is never asked about.
	Verdict string `json:"verdict"`
	// Reason is the policy's own reason for that verdict.
	Reason string `json:"reason"`
}

// Grant is an operator's answer to a permission question, kept on the run
// so the policy can consult it before it asks again. A grant never reaches
// config.yaml: it lives as long as the run does.
type Grant struct {
	Verdict  string   `json:"verdict"`
	Kind     string   `json:"kind"`
	Tool     string   `json:"tool"`
	Summary  string   `json:"summary"`
	Patterns []string `json:"patterns,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// GrantFor turns an answer to ask into the grant the policy will consult.
func GrantFor(ask PermissionAsk, verdict, reason string) Grant {
	return Grant{
		Verdict:  verdict,
		Kind:     ask.Kind,
		Tool:     ask.Tool,
		Summary:  ask.Summary,
		Patterns: append([]string(nil), ask.Patterns...),
		Reason:   strings.TrimSpace(reason),
	}
}

// Label is the call as one short phrase for an event line or a progress
// line: the program a shell grant covers, an MCP tool, a host.
func (g Grant) Label() string {
	if len(g.Patterns) > 0 {
		labels := make([]string, 0, len(g.Patterns))
		for _, p := range g.Patterns {
			labels = append(labels, strings.TrimSuffix(p, " *"))
		}
		return strings.Join(labels, ", ")
	}
	return g.Tool
}

// Grants is the set of answers a session's policy consults. An allow-once
// grant is spent by the first call it matches; the others last the run.
// It is safe for concurrent use: providers decide on their own goroutines.
type Grants struct {
	mu   sync.Mutex
	list []Grant
	used []bool
}

// NewGrants holds gs for one session.
func NewGrants(gs ...Grant) *Grants {
	return &Grants{list: append([]Grant(nil), gs...), used: make([]bool, len(gs))}
}

// find returns the grant that answers this call, spending an allow-once
// grant it uses. Deny grants are looked at first, so a refusal the operator
// gave is never undone by a broader allow.
func (g *Grants) find(p *PermissionPolicy, ask PermissionAsk, tool string, input json.RawMessage) (Grant, bool) {
	if g == nil {
		return Grant{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	// A denial covers any call that touches what it denied: the same
	// call, or one sharing a pattern with it (`rg x | wc -l` after rg was
	// denied).
	for _, gr := range g.list {
		if gr.Verdict != VerdictDeny || !sameCall(gr, ask) {
			continue
		}
		if gr.Summary == ask.Summary || overlaps(gr.Patterns, ask.Patterns) {
			return gr, true
		}
	}

	// Allow-for-this-run grants are pooled: a pipeline whose segments were
	// granted one question at a time is covered by all of them together.
	var pooled []string
	var first *Grant
	for i, gr := range g.list {
		if gr.Verdict != VerdictAllowRun || !sameCall(gr, ask) {
			continue
		}
		if gr.Summary == ask.Summary {
			return gr, true
		}
		pooled = append(pooled, gr.Patterns...)
		if first == nil {
			first = &g.list[i]
		}
	}
	if first != nil && p.allowsWith(ask.Kind, tool, input, pooled) {
		return *first, true
	}

	for i, gr := range g.list {
		if gr.Verdict == VerdictAllow && !g.used[i] && sameCall(gr, ask) && gr.Summary == ask.Summary {
			g.used[i] = true
			return gr, true
		}
	}
	return Grant{}, false
}

// runPatterns is every pattern an allow-for-this-run grant of this kind has
// added, so a new question names only what is still not covered.
func (g *Grants) runPatterns(kind string) []string {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, gr := range g.list {
		if gr.Verdict == VerdictAllowRun && gr.Kind == kind {
			out = append(out, gr.Patterns...)
		}
	}
	return out
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// sameCall reports whether a grant and a question are about the same tool:
// any shell tool for a bash grant, since Bash, bash and Codex's command
// execution all reach the policy as one allow-list, and the very tool name
// for everything else.
func sameCall(g Grant, ask PermissionAsk) bool {
	if g.Kind != ask.Kind {
		return false
	}
	return ask.Kind == AskBash || g.Tool == ask.Tool
}

// subcommandPrograms are the programs whose first argument names what they
// do, so a grant for `git log` is not a grant for `git push`.
var subcommandPrograms = map[string]bool{
	"git": true, "go": true, "npm": true, "npx": true, "yarn": true, "pnpm": true,
	"docker": true, "kubectl": true, "cargo": true, "dotnet": true, "make": true,
	"gh": true, "pip": true, "brew": true, "helm": true, "terraform": true,
	"aws": true, "gcloud": true, "az": true, "mvn": true, "gradle": true,
}

// commandPattern is the allow-list glob "allow for this run" adds for one
// shell segment: the program, and its subcommand when the program is one
// that takes one.
func commandPattern(segment string) string {
	tokens := argTokens(segment)
	for len(tokens) > 0 {
		if _, _, ok := envAssignment(tokens[0]); !ok {
			break
		}
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return ""
	}
	program := tokens[0]
	if subcommandPrograms[filepath.Base(program)] && len(tokens) > 1 && plainWord(tokens[1]) {
		return program + " " + tokens[1] + " *"
	}
	return program + " *"
}

// plainWord is an argument that reads as a subcommand: letters, digits and
// dashes inside, no leading dash, no path.
func plainWord(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	for _, r := range s {
		ok := r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// summaryMax caps a summary's length; the full call is in the event log.
const summaryMax = 400

func clip(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " "))
	if len(s) > summaryMax {
		return s[:summaryMax] + "…"
	}
	return s
}

func compactArgs(input json.RawMessage) string {
	var buf bytes.Buffer
	if len(input) == 0 || json.Compact(&buf, input) != nil {
		return ""
	}
	s := buf.String()
	if s == "{}" || s == "null" {
		return ""
	}
	return s
}

// askFor works out whether a refused call is one the operator could allow,
// and if it is, the question to put to them. A write in a triage run and a
// fix write outside the tree are never asked about: those are the read-only
// guarantee and the fix's confinement, which no answer widens. Neither is a
// call that no grant could let through — a shell construct, a path outside
// the workspace, an IP literal — since asking would only offer a button
// that does nothing.
func (p *PermissionPolicy) askFor(tool string, input json.RawMessage) (PermissionAsk, bool) {
	if AlwaysDenied[tool] || fixAllowed[tool] || writeTools[tool] {
		return PermissionAsk{}, false
	}
	ask := PermissionAsk{Tool: tool, Verdict: VerdictDeny}
	switch {
	case strings.HasPrefix(tool, mcpPrefix):
		ask.Kind = AskMCP
		ask.Summary = clip(compactArgs(input))
		ask.Patterns = []string{tool}
		return ask, true
	case FetchTools[tool]:
		ask.Kind = AskFetch
		var args fetchArgs
		if json.Unmarshal(input, &args) != nil {
			return PermissionAsk{}, false
		}
		targets := args.URLs
		if strings.TrimSpace(args.URL) != "" {
			targets = append([]string{args.URL}, targets...)
		}
		targets = append(targets, urlInText.FindAllString(args.Prompt, -1)...)
		var denied []string
		for _, t := range targets {
			if strings.TrimSpace(t) == "" || DecideFetchURL(p.FetchAllow, t).Allow {
				continue
			}
			u, err := url.Parse(strings.TrimSpace(t))
			if err != nil || u.Hostname() == "" {
				return PermissionAsk{}, false
			}
			denied = append(denied, t)
			ask.Patterns = appendNew(ask.Patterns, strings.ToLower(u.Hostname()))
		}
		ask.Summary = clip(strings.Join(denied, " "))
	case IsReadTool(tool):
		ask.Kind = AskTool
		targets, ok := readTargets(tool, input)
		if !ok {
			return PermissionAsk{}, false
		}
		scope := p.ReadScope()
		var denied []string
		for _, t := range targets {
			if _, err := scope.Resolve(t); err == nil {
				continue
			}
			denied = append(denied, t)
			ask.Patterns = appendNew(ask.Patterns, readDir(t))
		}
		ask.Summary = clip(strings.Join(denied, " "))
	case tool == "Bash" || tool == "bash":
		ask.Kind = AskBash
		var args struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(input, &args)
		command := strings.TrimSpace(args.Command)
		if command == "" {
			return PermissionAsk{}, false
		}
		covered := append(append([]string(nil), p.BashAllow...), p.Grants.runPatterns(AskBash)...)
		for _, seg := range SplitCommand(command) {
			norm := normaliseGitFlags(seg)
			if ok, _ := p.matchBash(covered, norm); ok {
				continue
			}
			if pat := commandPattern(norm); pat != "" {
				ask.Patterns = appendNew(ask.Patterns, pat)
			}
		}
		ask.Summary = clip(command)
	default:
		ask.Kind = AskTool
		ask.Summary = clip(compactArgs(input))
		ask.Patterns = []string{tool}
		return ask, true
	}
	return ask, true
}

// askable reports whether answering ask could let the call through: it
// names something to grant, and granting it — on top of what this run has
// already granted — satisfies every other rule.
func (p *PermissionPolicy) askable(ask PermissionAsk, tool string, input json.RawMessage) bool {
	if len(ask.Patterns) == 0 {
		return false
	}
	withGranted := append(append([]string(nil), ask.Patterns...), p.Grants.runPatterns(ask.Kind)...)
	return p.allowsWith(ask.Kind, tool, input, withGranted)
}

// readDir is the directory a read grant covers: the path itself when it is
// a directory, else the one it sits in.
func readDir(target string) string {
	t := strings.TrimSpace(target)
	if fi, err := os.Stat(t); err == nil && fi.IsDir() {
		return filepath.Clean(t)
	}
	return filepath.Dir(t)
}

func appendNew(list []string, s string) []string {
	for _, have := range list {
		if have == s {
			return list
		}
	}
	return append(list, s)
}

// allowsWith reports whether the policy, with patterns added to the list
// kind names, would allow the call. It is how a grant covers "any later call
// matching the same pattern": the pattern joins the allow-list for this run
// only, and every other rule — the shell constructs, the root escape, the
// fetch address guard — still applies.
func (p *PermissionPolicy) allowsWith(kind, tool string, input json.RawMessage, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	switch kind {
	case AskMCP, AskTool:
		if !IsReadTool(tool) {
			for _, pat := range patterns {
				if pat == tool {
					return true
				}
			}
			return false
		}
	}
	w := *p
	w.Grants, w.Ask = nil, false
	switch kind {
	case AskBash:
		w.BashAllow = append(append([]string(nil), p.BashAllow...), patterns...)
	case AskFetch:
		w.FetchAllow = append(append([]string(nil), p.FetchAllow...), patterns...)
	case AskTool:
		w.ReadAlso = append(append([]string(nil), p.ReadAlso...), patterns...)
	}
	return w.decide(tool, input).Allow
}

// grantedMessage is the reason an allowed-by-grant call carries in the
// event log.
func grantedMessage(g Grant) string {
	if g.Verdict == VerdictAllow {
		return "allowed once by you"
	}
	return "allowed by you for this run"
}

// deniedMessage is the refusal a call the operator denied carries back to
// the agent, with the operator's reason when they gave one.
func deniedMessage(g Grant) string {
	msg := "Sirdar policy: the operator denied " + g.Label() + " for this run"
	if g.Reason != "" {
		msg += ": " + g.Reason
	}
	return msg
}

// AskPending is the reason a provider gives the agent for a call that is
// now waiting on the operator. The run stops here and resumes with the
// operator's answer, so the agent is told not to work around it.
const AskPending = "Sirdar policy: this call is waiting on the operator's decision; the run pauses here and resumes with their answer"
