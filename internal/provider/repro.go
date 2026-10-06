package provider

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// CredentialRefusal is the message for typing into a credential-shaped field.
const CredentialRefusal = "Sirdar policy: the agent never types credentials; ask the operator to log in in the browser window"

// credentialField matches an element label, field name or field type that
// names a password, a one-time code or a token, in English or Arabic. It
// errs towards refusing: a search box mislabelled "token" costs the agent
// one refusal, a password typed by the agent costs a credential in the
// transcript.
var credentialField = regexp.MustCompile(`(?i)pass(word|code|phrase)?|otp|one.time|2fa|mfa|totp|pin\b|secret|token|كلمة.?المرور|رمز`)

// reproNotConfined is the reason every repro refusal carries, so the
// transcript says why a browser call that is fine on staging was refused.
const reproNotConfined = "browser not confined to repro hosts"

// reproInteraction names the browser tools that act on whatever page is
// open. They are harmless on a staging host and anything but elsewhere, so
// they are allowed only while the last navigation landed on a repro host.
var reproInteraction = map[string]bool{
	"browser_click":         true,
	"browser_type":          true,
	"browser_press_key":     true,
	"browser_select_option": true,
	"browser_fill_form":     true,
	"browser_hover":         true,
	"browser_drag":          true,
	"browser_handle_dialog": true,
	"browser_wait_for":      true,
}

// ReproGuard confines a reproduction browser to the workspace's staging
// hosts. It is per session: confined says whether the page the browser is on
// was reached by a navigation this guard checked, and it drops to false on
// anything that moves the browser somewhere the guard did not see.
type ReproGuard struct {
	// Server is the MCP server name the browser tools come from, as in
	// mcp__<server>__browser_navigate.
	Server string
	// Hosts are the host globs from repro.hosts, matched as
	// permissions.fetch globs are.
	Hosts []string
	// Evaluate lets browser_evaluate through while confined; it is set
	// only when the server itself is started with --allowed-origins.
	Evaluate bool

	mu       sync.Mutex
	confined bool
	host     string
}

// NewReproGuard returns a guard that starts unconfined, so the agent's first
// browser act has to be a navigation to a repro host.
func NewReproGuard(server string, hosts []string, evaluate bool) *ReproGuard {
	return &ReproGuard{Server: server, Hosts: append([]string(nil), hosts...), Evaluate: evaluate}
}

// Confined reports whether the browser is on a repro host, as far as the
// guard has seen.
func (g *ReproGuard) Confined() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.confined
}

// browserTool returns the tool's name after the repro server's prefix, or
// false when the call is not on the repro server.
func (g *ReproGuard) browserTool(tool string) (string, bool) {
	prefix := mcpPrefix + g.Server + "__"
	if g.Server == "" || !strings.HasPrefix(tool, prefix) {
		return "", false
	}
	return strings.TrimPrefix(tool, prefix), true
}

// decide answers for a call on the repro server, or reports false to fall through.
// ordinary is the decision the workspace's MCP rules give the call; the
// guard defers to it wherever it has nothing to add.
func (g *ReproGuard) decide(tool string, input json.RawMessage, ordinary func() Decision) (Decision, bool) {
	// Codex hands the policy no arguments, so there is no URL to judge and
	// no field to inspect: the guard steps aside and the ordinary rules
	// answer, which is why reproduction is never active on codex.
	if g == nil || len(input) == 0 {
		return Decision{}, false
	}
	name, ok := g.browserTool(tool)
	if !ok {
		return Decision{}, false
	}
	// Before confinement: a credential is refused on a staging host too,
	// because the agent typing one puts it in the transcript.
	if g.credentialCall(tool, input) {
		return Decision{Allow: false, Message: CredentialRefusal}, true
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case name == "browser_navigate":
		return g.navigate(input), true
	case name == "browser_navigate_back" || (name == "browser_tabs" && tabsAction(input) != "list"):
		// The destination is a page the guard never checked, so the
		// browser is unconfined until the next repro navigation.
		d := ordinary()
		g.confined, g.host = false, ""
		return d, true
	case reproInteraction[name] || (name == "browser_evaluate" && g.Evaluate):
		if g.confined {
			return Decision{Allow: true, Message: "repro host " + g.host}, true
		}
		d := ordinary()
		if !d.Allow {
			d.Message += " (" + reproNotConfined + ")"
		}
		return d, true
	}
	return Decision{}, false
}

// navigate judges a browser_navigate, holding g.mu. It overrides a
// permissions.mcp entry for the tool: a workspace that listed
// browser_navigate before reproduction existed listed it for any URL.
func (g *ReproGuard) navigate(input json.RawMessage) Decision {
	var args struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(input, &args)
	raw := strings.TrimSpace(args.URL)
	if DecideFetchURL(g.Hosts, raw).Allow {
		u, _ := url.Parse(raw)
		g.confined, g.host = true, strings.ToLower(u.Hostname())
		return Decision{Allow: true, Message: "repro host " + g.host}
	}
	g.confined, g.host = false, ""
	host := ""
	if u, err := url.Parse(raw); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	if host == "" {
		return Decision{Allow: false, Message: "Sirdar policy: " + reproNotConfined + "; the URL does not parse"}
	}
	return Decision{Allow: false, Message: "Sirdar policy: " + reproNotConfined + "; " + host +
		" is not in repro.hosts (" + strings.Join(g.Hosts, ", ") + ")"}
}

// tabsAction is the action a browser_tabs call names.
func tabsAction(input json.RawMessage) string {
	var args struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(input, &args)
	return args.Action
}

// credentialCall reports whether the call types into a credential-shaped
// field. askFor consults it too, so the refusal is never put to the
// operator: no grant lets the agent type a password.
func (g *ReproGuard) credentialCall(tool string, input json.RawMessage) bool {
	if g == nil || len(input) == 0 {
		return false
	}
	name, ok := g.browserTool(tool)
	if !ok {
		return false
	}
	switch name {
	case "browser_type":
		var args struct {
			Element string `json:"element"`
		}
		_ = json.Unmarshal(input, &args)
		return credentialField.MatchString(args.Element)
	case "browser_fill_form":
		var args struct {
			Fields []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"fields"`
		}
		_ = json.Unmarshal(input, &args)
		for _, f := range args.Fields {
			if credentialField.MatchString(f.Name) || credentialField.MatchString(f.Type) {
				return true
			}
		}
	}
	return false
}
