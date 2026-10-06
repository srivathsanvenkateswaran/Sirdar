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

// reproArgs decodes a call's arguments the way the browser server reads
// them: exact keys, and the last of a duplicated key. Decoding into a
// struct would not do: encoding/json matches field names without regard to
// case and keeps whichever spelling comes last, so {"url": <evil>, "URL":
// <staging>} would be judged on the staging URL while Playwright navigates
// to the evil one. False means the input is not a JSON object.
func reproArgs(input json.RawMessage) (map[string]json.RawMessage, bool) {
	var args map[string]json.RawMessage
	if len(input) == 0 || json.Unmarshal(input, &args) != nil || args == nil {
		return nil, false
	}
	return args, true
}

// stringArg is args[key] when it is a JSON string, and "" otherwise.
func stringArg(args map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := args[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// urlHost is the host a URL names, lower-cased and without a trailing dot,
// or "" when it names none.
func urlHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// unconfine drops confinement, holding g.mu.
func (g *ReproGuard) unconfine() { g.confined, g.host = false, "" }

// decide answers for a call on the repro server, or reports false to fall through.
// ordinary is the decision the workspace's MCP rules give the call; the
// guard defers to it wherever it has nothing to add.
func (g *ReproGuard) decide(tool string, input json.RawMessage, ordinary func() Decision) (Decision, bool) {
	if g == nil {
		return Decision{}, false
	}
	name, ok := g.browserTool(tool)
	if !ok {
		return Decision{}, false
	}
	args, ok := reproArgs(input)
	if !ok {
		// Codex hands the policy no arguments, and an ACP agent may leave
		// rawInput out: there is no URL to judge and no field to inspect,
		// so the ordinary rules answer. The call may still move the
		// browser, which is why confinement drops: nothing the guard could
		// not see is ever auto-allowed afterwards.
		g.mu.Lock()
		g.unconfine()
		g.mu.Unlock()
		return Decision{}, false
	}
	// Before confinement: a credential is refused on a staging host too,
	// because the agent typing one puts it in the transcript.
	if credentialArgs(name, args) {
		return Decision{Allow: false, Message: CredentialRefusal}, true
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case name == "browser_navigate":
		return g.navigate(args), true
	case name == "browser_navigate_back" || name == "browser_run_code" ||
		(name == "browser_tabs" && stringArg(args, "action") != "list"):
		// The destination is a page the guard never checked — run_code
		// can navigate as freely as it likes — so the browser is
		// unconfined until the next repro navigation, whoever allowed
		// this call.
		d := ordinary()
		g.unconfine()
		return d, true
	case name == "browser_evaluate":
		if g.Evaluate && g.confined {
			return Decision{Allow: true, Message: "repro host " + g.host}, true
		}
		// Script the guard did not vouch for can move the page as well
		// as read it, so after it the guard trusts nothing it saw before.
		d := ordinary()
		if g.Evaluate && !d.Allow {
			d.Message += " (" + reproNotConfined + ")"
		}
		g.unconfine()
		return d, true
	case reproInteraction[name]:
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
func (g *ReproGuard) navigate(args map[string]json.RawMessage) Decision {
	raw := strings.TrimSpace(stringArg(args, "url"))
	host := urlHost(raw)
	fetch := DecideFetchURL(g.Hosts, raw)
	if fetch.Allow && host != "" {
		g.confined, g.host = true, host
		return Decision{Allow: true, Message: "repro host " + host}
	}
	g.unconfine()
	switch {
	case host == "":
		return Decision{Allow: false, Message: "Sirdar policy: " + reproNotConfined + "; the URL does not parse"}
	case g.listed(host):
		// A repro host refused anyway — plain http, userinfo, an address
		// spelled oddly — gets the fetch matcher's own reason, since "not
		// in repro.hosts" would be false.
		return Decision{Allow: false, Message: "Sirdar policy: " + reproNotConfined + "; " +
			strings.TrimPrefix(fetch.Message, "Sirdar policy: ")}
	}
	return Decision{Allow: false, Message: "Sirdar policy: " + reproNotConfined + "; " + host +
		" is not in repro.hosts (" + strings.Join(g.Hosts, ", ") + ")"}
}

// listed reports whether host matches one of repro.hosts.
func (g *ReproGuard) listed(host string) bool {
	for _, pattern := range g.Hosts {
		if matchFetchHost(pattern, host) {
			return true
		}
	}
	return false
}

// navigateHost is the pure half of a navigate judgement, for the grant
// machinery: whether the guard governs this call as a repro navigate, and
// the host it names ("" when none). It reads no guard state and changes
// none, so a dry run (allowsWith, askable, a grant lookup) cannot move
// confinement.
func (g *ReproGuard) navigateHost(tool string, input json.RawMessage) (string, bool) {
	if g == nil {
		return "", false
	}
	if name, ok := g.browserTool(tool); !ok || name != "browser_navigate" {
		return "", false
	}
	args, ok := reproArgs(input)
	if !ok {
		return "", false
	}
	return urlHost(stringArg(args, "url")), true
}

// navigatePattern is what an operator's grant on an off-host repro
// navigate covers: that tool to that one host. A bare tool name would turn
// "allow evil.example for this run" into "allow every host for this run",
// which is the confinement the guard exists for.
func navigatePattern(tool, host string) string { return tool + " host:" + host }

// credentialCall reports whether the call types into a credential-shaped
// field. askFor consults it too, so the refusal is never put to the
// operator: no grant lets the agent type a password.
func (g *ReproGuard) credentialCall(tool string, input json.RawMessage) bool {
	if g == nil {
		return false
	}
	name, ok := g.browserTool(tool)
	if !ok {
		return false
	}
	args, ok := reproArgs(input)
	return ok && credentialArgs(name, args)
}

// credentialArgs is credentialCall on decoded arguments: browser_type's
// element, and every browser_fill_form field's name and type.
func credentialArgs(name string, args map[string]json.RawMessage) bool {
	switch name {
	case "browser_type":
		return credentialField.MatchString(stringArg(args, "element"))
	case "browser_fill_form":
		// Element by element, so one malformed field does not hide the
		// others from the check.
		var fields []json.RawMessage
		_ = json.Unmarshal(args["fields"], &fields)
		for _, raw := range fields {
			f, _ := reproArgs(raw)
			if credentialField.MatchString(stringArg(f, "name")) || credentialField.MatchString(stringArg(f, "type")) {
				return true
			}
		}
	}
	return false
}
