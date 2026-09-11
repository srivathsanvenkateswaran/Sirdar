package provider

import (
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// FetchTools are the tool names whose argument is a URL the session would
// retrieve: Claude Code's WebFetch, the lower-case web_fetch that Sirdar's
// own agent loop offers and that the qwen adapter maps its tool onto, and
// the ACP "fetch" kind, which arrives under the WebFetch name too.
//
// They are deliberately not in AlwaysAllowed. A read tool pulls attacker
// text into the session all the time — a ticket comment, a page, a file —
// and a fetch approved without looking at the destination is how that text
// gets to send what the session has read to a host of its choosing. The
// destination is therefore judged on every call, against permissions.fetch.
var FetchTools = map[string]bool{
	"WebFetch":  true,
	"web_fetch": true,
}

// fetchArgs is every argument name a fetch tool names its destination
// with. Claude Code's WebFetch takes url plus a prompt; Sirdar's own
// web_fetch takes url alone; Qwen Code's and gemini-cli's take a prompt
// with the URLs written into the text, and an ACP agent names whatever its
// own tool names. Each is read, and every URL found in any of them has to
// be allowed — a call naming two hosts is not half-approved.
type fetchArgs struct {
	URL    string   `json:"url"`
	URLs   []string `json:"urls"`
	Prompt string   `json:"prompt"`
}

// urlInText finds the http and https URLs written into a free-text
// argument, which is the only place a Qwen or gemini-style web_fetch puts
// them. The run of characters is deliberately generous: it is being used
// to find a destination to judge, not to validate one, and url.Parse has
// the final word on each.
var urlInText = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>)\]}]+`)

// loopbackHosts are the names that mean this machine. They are refused
// like any other private destination unless permissions.fetch names one
// explicitly in its http://host[:port] form.
var loopbackHosts = map[string]bool{
	"localhost":     true,
	"localhost.":    true,
	"ip6-localhost": true,
	"ip6-loopback":  true,
}

// decideFetch judges one fetch tool call: it pulls every URL out of the
// arguments and puts each through DecideFetchURL. A call naming none is
// refused rather than approved unseen, the same way a write naming no path
// is.
func (p *PermissionPolicy) decideFetch(tool string, input json.RawMessage) Decision {
	var args fetchArgs
	if err := json.Unmarshal(input, &args); err != nil {
		// A malformed call is refused rather than partially trusted: Go's
		// json.Unmarshal keeps decoding after an UnmarshalTypeError (a
		// urls field sent as an object, say) and duplicate keys silently
		// take the last value, either of which could leave args holding
		// something other than what the caller actually sent.
		return Decision{Allow: false, Message: "Sirdar policy: " + tool +
			" arguments do not parse, so where it would fetch from cannot be checked"}
	}

	targets := args.URLs
	if strings.TrimSpace(args.URL) != "" {
		targets = append([]string{args.URL}, targets...)
	}
	targets = append(targets, urlInText.FindAllString(args.Prompt, -1)...)

	found := false
	for _, target := range targets {
		if strings.TrimSpace(target) == "" {
			continue
		}
		found = true
		if d := DecideFetchURL(p.FetchAllow, target); !d.Allow {
			return d
		}
	}
	if !found {
		return Decision{Allow: false, Message: "Sirdar policy: " + tool +
			" named no URL, so where it would fetch from cannot be checked"}
	}
	return Decision{Allow: true}
}

// DecideFetchURL reports whether one URL may be fetched under a
// permissions.fetch allow-list. It is the single entry point the permission
// policy and Sirdar's own web_fetch tool (internal/agenttools) both go
// through, so a destination the policy would refuse cannot be reached by
// asking the loop's tool instead.
//
// The rules, in the order they are applied:
//
//   - the URL has to parse, be absolute, and carry a host;
//   - no userinfo: https://docs.example.com@attacker.example is a request to
//     attacker.example that reads as a request to the documentation host;
//   - a loopback destination is refused unless permissions.fetch names it
//     in its http://host[:port] form, which is how a workspace allows a
//     local service it runs itself;
//   - an IP literal is refused: permissions.fetch names hosts, and an
//     address that skips DNS skips the name the operator allowed. Private,
//     link-local (169.254.169.254, the cloud metadata service), carrier-NAT
//     and multicast addresses are refused by BlockedIP whatever else says;
//   - the scheme has to be https, the loopback entries above excepted;
//   - and the host has to match one of the allow-list's globs.
//
// An empty allow-list — the default — denies everything. The message names
// the host and the setting, so an operator reading a refused run knows
// which line to add.
//
// What this does not do is resolve the host. A name in the allow-list that
// points at an internal address is allowed here; the address guard on
// Sirdar's own fetch dialer (internal/agenttools) is what refuses that
// connection, and a provider CLI doing its own fetching has whatever guard
// it has. See docs/config.md.
func DecideFetchURL(allow []string, rawURL string) Decision {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return fetchDenial("a fetch named an empty URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		// No quoted raw text here: it failed to parse, so there is no
		// scheme or host to show in its place, and the raw string may
		// still carry a query fragment worth not echoing.
		return fetchDenial("a fetch URL does not parse")
	}
	scheme := strings.ToLower(u.Scheme)
	display := fetchDisplay(u)
	if scheme == "" {
		return fetchDenial(withDisplay(display, "is not absolute; a fetch needs a scheme and a host"))
	}
	if scheme != "http" && scheme != "https" {
		return fetchDenial(withDisplay(display, "uses scheme "+quote(scheme)+"; a fetch may use only http and https"))
	}
	if u.Host == "" {
		return fetchDenial(withDisplay(display, "is not absolute; a fetch needs a scheme and a host"))
	}
	if u.User != nil {
		return fetchDenial(withDisplay(display,
			"carries userinfo before the host, which makes a request to one host read as a request to another"))
	}

	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return fetchDenial(withDisplay(display, "names no host"))
	}
	hostPort := host
	if port := u.Port(); port != "" {
		hostPort = net.JoinHostPort(host, port)
	}

	ip := net.ParseIP(host)
	if ip == nil && looksLikeIPLiteral(host) {
		// "127.1", "2130706433", "0x7f000001": net.ParseIP does not
		// recognise these, but a resolver or an HTTP client still might,
		// so treating them as an ordinary hostname would let one skip
		// both the allow-list (host globs are written against names) and
		// BlockedIP. permissions.fetch names hosts; an address in any
		// spelling skips the name it allows.
		return fetchDenial(withDisplay(display, "names "+quote(host)+
			", which is an IP literal written in a non-canonical form; permissions.fetch names hosts, "+
			"and an address skips the name it allows"))
	}
	loopback := loopbackHosts[host] || strings.HasSuffix(host, ".localhost") ||
		(ip != nil && ip.IsLoopback())

	// A loopback entry is the one shape that permits http, an IP literal
	// and an address BlockedIP refuses: a workspace that runs its own
	// service on 127.0.0.1 has to be able to name it.
	if loopback && matchesLoopback(allow, host, hostPort) {
		return Decision{Allow: true}
	}
	if loopback {
		return fetchDenial("the URL " + quote(display) + " points at this machine; permissions.fetch allows a " +
			"loopback destination only when it names it, as http://" + hostPort)
	}
	if ip != nil {
		if BlockedIP(ip) {
			return fetchDenial("the address " + quote(host) +
				" is private, link-local or otherwise not routed on the public internet, and is never fetchable")
		}
		return fetchDenial("the URL " + quote(display) + " names the IP literal " + quote(host) +
			"; permissions.fetch names hosts, and an address skips the name it allows")
	}
	if scheme != "https" {
		return fetchDenial("the URL " + quote(display) +
			" is http; a fetch must use https unless permissions.fetch names a loopback host as http://host")
	}
	if len(allow) == 0 {
		return fetchDenial("permissions.fetch is empty, so no host is fetchable; " +
			quote(host) + " needs a line there before a session may fetch it")
	}
	for _, pattern := range allow {
		if matchFetchHost(pattern, host) {
			return Decision{Allow: true}
		}
	}
	return fetchDenial(quote(host) + " is not in permissions.fetch (" + strings.Join(allow, ", ") + ")")
}

func fetchDenial(reason string) Decision {
	return Decision{Allow: false, Message: "Sirdar policy: " + reason}
}

// fetchDisplay is what a denial reason may show back to the model, and what
// lands in events.jsonl, in place of the raw URL: scheme and host, never a
// query or fragment. A query string is exactly where a token or a session
// id travels, and both the message that goes back to the model and the run
// record on disk are places that should not be echoing it.
func fetchDisplay(u *url.URL) string {
	switch {
	case u == nil:
		return ""
	case u.Scheme != "" && u.Host != "":
		return u.Scheme + "://" + u.Host
	case u.Host != "":
		return u.Host
	default:
		return u.Scheme
	}
}

// withDisplay builds a denial reason around a scheme+host display, or a
// display-free generic reason when there is neither a scheme nor a host to
// show (a relative URL, say).
func withDisplay(display, reason string) string {
	if display == "" {
		return "a fetch URL " + reason
	}
	return "the URL " + quote(display) + " " + reason
}

// looksLikeIPLiteral reports whether host, which net.ParseIP did not
// recognise, is still an IP address written in a form some resolvers and
// HTTP clients accept anyway: a bare decimal number (2130706433), a hex
// literal (0x7f000001), or a dotted form whose rightmost label is one of
// those (127.1, 127.0.0x1). No real hostname's last label is ever all
// digits or hex-prefixed — ICANN does not allow an all-numeric TLD — so
// this never misjudges an ordinary name.
func looksLikeIPLiteral(host string) bool {
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	if last == "" {
		return false
	}
	if strings.HasPrefix(last, "0x") || strings.HasPrefix(last, "0X") {
		return true
	}
	for _, r := range last {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// matchFetchHost applies one permissions.fetch entry to a host. An entry
// carrying a scheme is a loopback entry and is judged by matchesLoopback
// instead, never here.
//
// "*.example.com" matches a subdomain and not the bare domain, which is
// what the leading "." in the pattern already says: MatchGlob anchors both
// ends, so example.com has nothing to put in front of the dot. An entry
// with no wildcard is an exact host.
func matchFetchHost(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" || strings.Contains(pattern, "://") {
		return false
	}
	return MatchGlob(pattern, host)
}

// matchesLoopback reports whether the allow-list names this loopback
// destination. A entry written without a port (http://localhost) matches
// that host on any port; one written with a port matches only that port,
// so http://127.0.0.1:11434 does not open every local service.
func matchesLoopback(allow []string, host, hostPort string) bool {
	for _, entry := range allow {
		entryHost, ok := loopbackEntry(entry)
		if !ok {
			continue
		}
		if entryHost == host || entryHost == hostPort {
			return true
		}
	}
	return false
}

// loopbackEntry reports the host[:port] of a permissions.fetch entry
// written in the http://host[:port] form, and whether the entry is in that
// form at all. The host side is normalised the same way DecideFetchURL's
// own host is — brackets stripped off an IPv6 literal, then put back by
// net.JoinHostPort only when a port is present — so "http://[::1]" and
// "http://[::1]:8080" compare equal to a request's host and hostPort
// however either was written. Without this, "http://[::1]" never matched a
// call to it: u.Hostname() strips the brackets net/url requires around an
// IPv6 host, but this function, read directly off the config string, used
// to keep them.
func loopbackEntry(entry string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(entry)), "http://")
	if !ok || rest == "" {
		return "", false
	}
	rest = strings.TrimSuffix(rest, "/")
	host, port := rest, ""
	if h, p, err := net.SplitHostPort(rest); err == nil {
		host, port = h, p
	}
	host = strings.Trim(host, "[]")
	if port != "" {
		return net.JoinHostPort(host, port), true
	}
	return host, true
}

// BlockedIP reports whether an address falls in one of the ranges a fetch
// refuses: loopback, the unspecified address, RFC1918 and IPv6 unique-local
// (both covered by net.IP.IsPrivate), link-local — which is where cloud
// metadata services live, notably 169.254.169.254 — multicast, and
// carrier-grade NAT.
//
// It lives here rather than beside the dialer it started on because two
// callers need the same answer: this package's permissions.fetch check,
// which runs before any provider fetches anything, and the dial guard in
// internal/agenttools, which runs on each resolved address of Sirdar's own
// fetch so that a public name pointed at an internal address is refused
// too.
func BlockedIP(ip net.IP) bool {
	switch {
	case ip.IsLoopback(), ip.IsUnspecified(), ip.IsPrivate():
		return true
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return true
	}
	// 100.64.0.0/10, carrier-grade NAT: not routed on the public internet.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}

// hostLabel is one label of a hostname: letters, digits and hyphens, not
// starting or ending with a hyphen. Underscores are out — no public host
// needs one, and allowing them widens what a pattern can be written
// against for nothing.
var hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateFetchEntry checks one permissions.fetch entry and returns the
// reason it is unusable, or "" when it is fine. It runs at config load, so
// a typo is a failed load rather than a run that quietly fetches nothing —
// or, worse, one that fetches more than the operator meant.
//
// Accepted:
//
//	example.com            exact host, https only
//	*.example.com          any subdomain of it, https only, not the bare domain
//	http://localhost:3000  a loopback service this machine runs, http or https
//
// Refused: a path or query (the allow-list judges hosts, not URLs), any
// userinfo, a bare "*" or a wildcard anywhere but a leading "*.", an
// https:// prefix (entries are hosts, and the scheme is already implied),
// and http:// in front of anything that is not loopback.
func ValidateFetchEntry(entry string) string {
	raw := strings.TrimSpace(entry)
	if raw == "" {
		return "is empty"
	}
	if raw == "*" || raw == "*.*" {
		return "is a bare wildcard, which allows every host and is the same as having no allow-list at all"
	}
	if strings.Contains(raw, "@") {
		return "carries userinfo; an entry names a host"
	}
	lower := strings.ToLower(raw)

	if rest, ok := strings.CutPrefix(lower, "https://"); ok {
		return "starts with https://; an entry is a bare host glob such as " + quote(strings.TrimSuffix(rest, "/"))
	}
	if rest, ok := strings.CutPrefix(lower, "http://"); ok {
		rest = strings.TrimSuffix(rest, "/")
		host, port := rest, ""
		if h, p, err := net.SplitHostPort(rest); err == nil {
			host, port = h, p
		}
		if strings.ContainsAny(rest, "/?#") {
			return "names a path; an entry is a host, optionally with a port"
		}
		if !loopbackHosts[host] && !strings.HasSuffix(host, ".localhost") {
			if ip := net.ParseIP(strings.Trim(host, "[]")); ip == nil || !ip.IsLoopback() {
				return "uses http:// for " + quote(host) +
					", which is not loopback; http is allowed only for a service on this machine"
			}
		}
		if port != "" && strings.Trim(port, "0123456789") != "" {
			return "has a port that is not a number"
		}
		return ""
	}

	if strings.Contains(lower, "://") {
		return "carries a scheme; an entry is a bare host glob, or http://host for a loopback service"
	}
	if strings.ContainsAny(lower, "/?# ") {
		return "names a path or contains a space; an entry is a host glob such as \"*.example.com\""
	}
	if strings.Contains(lower, ":") {
		return "names a port; a host entry matches the host on any port"
	}
	trimmed := strings.TrimSuffix(lower, ".")
	if looksLikeIPLiteral(trimmed) {
		return "is an IP literal, not a hostname; permissions.fetch names hosts, and an address skips the name it allows"
	}
	labels := strings.Split(trimmed, ".")
	for i, label := range labels {
		if i == 0 && label == "*" {
			// "*.com" or "*.io" would let a session fetch any host under
			// a whole top-level domain; a wildcard needs a real domain
			// under it, so at least two labels have to follow the "*.".
			if len(labels) < 3 {
				return "is a wildcard directly on a top-level domain (" + quote(raw) +
					"), which allows every host under it; an entry needs at least two labels after " +
					"\"*.\", such as \"*.example.com\""
			}
			continue
		}
		if strings.Contains(label, "*") || strings.Contains(label, "?") {
			return "uses a wildcard outside a leading \"*.\"; " +
				"an entry is either an exact host or \"*.\" in front of one"
		}
		if !hostLabel.MatchString(label) {
			return "is not a hostname: " + quote(label) + " is not a valid label"
		}
	}
	if len(labels) < 2 {
		return "is a single label; an entry names a host such as \"docs.example.com\""
	}
	return ""
}
