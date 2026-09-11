// Package httpx holds the HTTP plumbing every built-in source adapter needs:
// deciding which hosts a URL taken out of a response body may be fetched
// from (and which of them may see the credential), a redirect policy that
// applies that decision to every hop, Retry-After parsing, body ceilings,
// attachment downloads, filename sanitising, per-ticket warnings and the
// List bounds from the adapter contract.
//
// It is HTTP plumbing, not an adapter framework: nothing here knows about
// source.Error codes, role mapping or any vendor's response envelope.
package httpx

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// HostRule is one entry in a Trust's host list.
//
// Suffix is either a dot-prefixed suffix (".zendesk.com", which matches
// zendesk.com itself and any label below it, on a dot boundary so
// "evilzendesk.com" does not match) or a bare host that must match exactly
// ("dev.azure.com").
//
// SendCredential says whether the adapter's credential may go to a host the
// rule matched. It is false for a first-party CDN whose URLs are pre-signed:
// the file is fetchable there, the API token has no business being sent.
type HostRule struct {
	Suffix         string
	SendCredential bool
}

// Trust answers the one question every adapter asks about a URL it found
// inside a response body: may this be fetched, and may the credential go
// with it.
//
// Base is the configured base URL. Its host is always trusted with the
// credential, and its scheme is the one plain-HTTP exception: a workspace
// configured over http (a self-hosted collection, a test server) has already
// made that choice, so http is accepted for it and https is required
// everywhere else.
type Trust struct {
	Base  *url.URL
	Hosts []HostRule
}

// NewTrust parses baseURL and returns a Trust that trusts its host with the
// credential, plus any extra rules.
func NewTrust(baseURL string, extra ...HostRule) (*Trust, error) {
	raw := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if raw == "" {
		return nil, fmt.Errorf("httpx: base URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("httpx: invalid base URL %q: %w", baseURL, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("httpx: base URL %q has no host", baseURL)
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return nil, fmt.Errorf("httpx: base URL %q must be http or https", baseURL)
	}
	t := &Trust{Base: u}
	for _, r := range extra {
		if strings.TrimSpace(r.Suffix) == "" {
			continue
		}
		t.Hosts = append(t.Hosts, HostRule{Suffix: strings.ToLower(strings.TrimSpace(r.Suffix)), SendCredential: r.SendCredential})
	}
	return t, nil
}

// Refusal reasons returned by Check. They name the rule that refused,
// never the URL: the rest of an attacker-supplied URL has no business in a
// log line, and the caller already has the host.
const (
	ReasonNoHost     = "no host"
	ReasonUserinfo   = "userinfo in the url"
	ReasonScheme     = "scheme is not https"
	ReasonNotTrusted = "untrusted host"
	ReasonNoTrust    = "no trust configured"
)

// Check reports whether u may be fetched at all (fetch) and whether the
// adapter's credential may be sent there (sendCredential), with a short
// reason when it may not — empty when it may.
//
// The rules, in order: a URL with no host is refused; userinfo is refused
// ("https://jira.example.com@attacker.example/x" parses with the real
// destination in Host and the decoy in User); the scheme must be https, or
// the base URL's own scheme; the base host (normalised — lowercased,
// trailing root dot dropped, default port removed) is trusted with the
// credential; and each host rule is applied on a dot boundary, granting the
// credential only when the rule says so.
func (t *Trust) Check(u *url.URL) (fetch, sendCredential bool, reason string) {
	if t == nil {
		return false, false, ReasonNoTrust
	}
	if u == nil || u.Hostname() == "" {
		return false, false, ReasonNoHost
	}
	if u.User != nil {
		return false, false, ReasonUserinfo
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != t.baseScheme() {
		return false, false, ReasonScheme
	}
	host := NormalizeHost(scheme, u.Host)
	if host == "" {
		return false, false, ReasonNoHost
	}
	if t.Base != nil && host == NormalizeHost(t.baseScheme(), t.Base.Host) {
		return true, true, ""
	}
	for _, r := range t.Hosts {
		if matchHost(r.Suffix, host) {
			return true, r.SendCredential, ""
		}
	}
	return false, false, ReasonNotTrusted
}

// CheckRaw is Check for a URL still in string form. A URL that will not
// parse is refused.
func (t *Trust) CheckRaw(raw string) (fetch, sendCredential bool, reason string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false, false, ReasonNoHost
	}
	return t.Check(u)
}

func (t *Trust) baseScheme() string {
	if t == nil || t.Base == nil {
		return "https"
	}
	if s := strings.ToLower(t.Base.Scheme); s != "" {
		return s
	}
	return "https"
}

// matchHost applies one rule to an already-normalised host. A dot-prefixed
// rule matches the domain itself and anything below it; a bare rule is an
// exact host match.
func matchHost(rule, host string) bool {
	if rule == "" {
		return false
	}
	if !strings.HasPrefix(rule, ".") {
		return host == rule
	}
	return host == strings.TrimPrefix(rule, ".") || strings.HasSuffix(host, rule)
}

// NormalizeHost renders a URL host comparable: lowercased, with the DNS
// root's trailing dot removed and the scheme's default port dropped, so
// "JIRA.Example.com.:443" and "jira.example.com" are recognised as one host.
func NormalizeHost(scheme, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	scheme = strings.ToLower(strings.TrimSpace(scheme))

	name, port, err := net.SplitHostPort(host)
	if err != nil {
		// No port at all, or a bare IPv6 literal.
		return strings.TrimSuffix(host, ".")
	}
	name = strings.TrimSuffix(name, ".")
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return name
	}
	return net.JoinHostPort(name, port)
}

// HostOf returns a URL's normalised host for a warning line, or a
// placeholder when there is none to report. Warnings name the host and
// nothing else: the rest of the URL is attacker-chosen text headed for a
// log.
func HostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "(unparseable)"
	}
	if u.Host == "" {
		return "(no host)"
	}
	return NormalizeHost(u.Scheme, u.Host)
}
