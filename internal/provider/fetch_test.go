package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDecideFetchURL is the whole of permissions.fetch in one table: which
// destinations a workspace's list reaches, and which are refused however
// the URL is dressed up.
func TestDecideFetchURL(t *testing.T) {
	allow := []string{
		"docs.example.com", "*.golang.org", "http://localhost:3000", "http://127.0.0.1",
		"http://[::1]:4000",
	}

	cases := []struct {
		name  string
		url   string
		allow []string
		want  bool
		// reason is a fragment the denial message has to carry, so the
		// operator reading a refused run learns why rather than that.
		reason string
	}{
		{name: "allow-listed host", url: "https://docs.example.com/guide", allow: allow, want: true},
		{name: "allow-listed host on a port", url: "https://docs.example.com:8443/guide", allow: allow, want: true},
		{name: "subdomain glob", url: "https://pkg.golang.org/net/http", allow: allow, want: true},
		{name: "deeper subdomain", url: "https://a.b.golang.org/", allow: allow, want: true},
		{
			name: "the bare domain is not a subdomain", url: "https://golang.org/", allow: allow,
			want: false, reason: "not in permissions.fetch",
		},
		{
			name: "a neighbouring host is not a match", url: "https://evil-golang.org/", allow: allow,
			want: false, reason: "not in permissions.fetch",
		},
		{
			name: "a host the list does not name", url: "https://attacker.example/collect?q=secret", allow: allow,
			want: false, reason: `"attacker.example" is not in permissions.fetch`,
		},
		{
			name: "http to an allow-listed host", url: "http://docs.example.com/guide", allow: allow,
			want: false, reason: "must use https",
		},
		{name: "loopback named with its port", url: "http://localhost:3000/health", allow: allow, want: true},
		{name: "loopback named without one", url: "http://127.0.0.1:11434/api/tags", allow: allow, want: true},
		{
			name: "loopback on a port the list does not name", url: "http://localhost:9999/", allow: allow,
			want: false, reason: "points at this machine",
		},
		{
			name: "userinfo in front of an allowed host",
			url:  "https://docs.example.com@attacker.example/", allow: allow,
			want: false, reason: "userinfo",
		},
		{
			name: "an IP literal skips the name that was allowed", url: "https://93.184.216.34/", allow: allow,
			want: false, reason: "IP literal",
		},
		{
			name: "a private address", url: "https://10.1.2.3/admin", allow: allow,
			want: false, reason: "private, link-local",
		},
		{
			name: "the cloud metadata service", url: "http://169.254.169.254/latest/meta-data/", allow: allow,
			want: false, reason: "private, link-local",
		},
		{
			name: "an empty list denies an otherwise ordinary host",
			url:  "https://docs.example.com/guide", allow: nil,
			want: false, reason: "permissions.fetch is empty",
		},
		{
			name: "a scheme that is not http", url: "file:///etc/passwd", allow: allow,
			want: false, reason: "only http and https",
		},
		{
			name: "a relative URL", url: "/etc/passwd", allow: allow,
			want: false, reason: "not absolute",
		},
		{
			name: "an empty URL", url: "   ", allow: allow,
			want: false, reason: "empty URL",
		},
		{
			name: "a trailing dot does not dodge the host match",
			url:  "https://attacker.example./", allow: allow,
			want: false, reason: "not in permissions.fetch",
		},
		{name: "a trailing dot on an allowed host still matches", url: "https://docs.example.com./x", allow: allow, want: true},
		{name: "the host match folds case", url: "https://DOCS.Example.COM/x", allow: allow, want: true},
		{
			name: "an IPv6 loopback entry matches the bracketed form on its port", url: "http://[::1]:4000/health",
			allow: allow, want: true,
		},
		{
			name: "an IPv6 loopback on a port the list does not name", url: "http://[::1]:9999/",
			allow: allow, want: false, reason: "points at this machine",
		},
		{
			name: "a decimal IP literal is refused like any other IP", url: "http://2130706433/admin",
			allow: allow, want: false, reason: "IP literal",
		},
		{
			name: "a hex IP literal is refused like any other IP", url: "http://0x7f000001/admin",
			allow: allow, want: false, reason: "IP literal",
		},
		{
			name: "a two-part dotted IP literal is refused like any other IP", url: "http://127.1/admin",
			allow: allow, want: false, reason: "IP literal",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := DecideFetchURL(c.allow, c.url)
			if d.Allow != c.want {
				t.Fatalf("DecideFetchURL(%q) allow = %v, want %v (%s)", c.url, d.Allow, c.want, d.Message)
			}
			if c.want {
				return
			}
			if !strings.Contains(d.Message, "Sirdar policy:") {
				t.Errorf("denial does not name the policy: %q", d.Message)
			}
			if c.reason != "" && !strings.Contains(d.Message, c.reason) {
				t.Errorf("denial %q does not carry %q", d.Message, c.reason)
			}
		})
	}
}

// TestDeniedURLNeverLeaksItsQuery: a denial message goes back to the model
// and into events.jsonl, so it must never carry a query string or fragment
// off the raw URL — that is exactly where a token or a session id travels.
// Every branch that used to interpolate the raw URL is exercised here with
// one carrying a token, whatever else about the URL got it denied.
func TestDeniedURLNeverLeaksItsQuery(t *testing.T) {
	const secret = "token=super-secret-value"
	allow := []string{"docs.example.com", "http://localhost:3000"}

	urls := []string{
		"http://docs.example.com/guide?" + secret,               // http to an allowed host
		"https://docs.example.com@attacker.example/x?" + secret, // userinfo
		"https://93.184.216.34/x?" + secret,                     // IP literal
		"http://localhost:9999/x?" + secret,                     // loopback, wrong port
		"file:///etc/passwd?" + secret,                          // bad scheme
		"https://attacker.example/collect?" + secret,            // not in the allow-list
	}
	for _, u := range urls {
		t.Run(u, func(t *testing.T) {
			d := DecideFetchURL(allow, u)
			if d.Allow {
				t.Fatalf("DecideFetchURL(%q) was allowed; the case is meant to be denied", u)
			}
			if strings.Contains(d.Message, secret) {
				t.Errorf("denial for %q leaked the query string into the message: %q", u, d.Message)
			}
		})
	}
}

// TestDecideFetchRefusesMalformedArguments: an arguments payload that does
// not unmarshal cleanly is refused outright rather than judged on whatever
// json.Unmarshal managed to decode before failing — an UnmarshalTypeError
// leaves the fields it reached at their zero value and keeps decoding the
// rest, so a urls field sent as an object could otherwise be silently
// treated as "no urls named" instead of "the call could not be read".
func TestDecideFetchRefusesMalformedArguments(t *testing.T) {
	p := &PermissionPolicy{FetchAllow: []string{"docs.example.com"}}

	cases := []struct {
		name  string
		input string
	}{
		{name: "urls sent as an object", input: `{"urls":{"a":"https://docs.example.com/x"}}`},
		{name: "url sent as a number", input: `{"url":12345}`},
		{name: "not an object at all", input: `["https://docs.example.com/x"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := p.Decide("WebFetch", json.RawMessage(c.input))
			if d.Allow {
				t.Fatalf("Decide(WebFetch, %s) was allowed; malformed arguments should be refused", c.input)
			}
			if d.Message == "" {
				t.Error("a refused fetch carries no message")
			}
		})
	}
}

// TestDecideReadsEveryFetchArgumentShape: a fetch tool names its
// destination differently on every provider — url on Claude and on
// Sirdar's own loop, a free-text prompt on the Qwen and gemini lineage,
// whatever an ACP agent's own tool uses. Each shape has to be read, and a
// call naming several hosts is not half-approved.
func TestDecideReadsEveryFetchArgumentShape(t *testing.T) {
	p := &PermissionPolicy{FetchAllow: []string{"docs.example.com"}}

	cases := []struct {
		name  string
		tool  string
		input string
		want  bool
	}{
		{name: "url", tool: "WebFetch", input: `{"url":"https://docs.example.com/x"}`, want: true},
		{name: "url denied", tool: "WebFetch", input: `{"url":"https://attacker.example/x"}`, want: false},
		{name: "lower-case name", tool: "web_fetch", input: `{"url":"https://docs.example.com/x"}`, want: true},
		{
			name: "url in a prompt", tool: "web_fetch",
			input: `{"prompt":"summarise https://docs.example.com/x for me"}`, want: true,
		},
		{
			name: "a denied host in a prompt", tool: "web_fetch",
			input: `{"prompt":"summarise https://attacker.example/x for me"}`, want: false,
		},
		{
			name: "one allowed and one denied host", tool: "WebFetch",
			input: `{"url":"https://docs.example.com/x","prompt":"and http://attacker.example/y"}`, want: false,
		},
		{name: "a urls array", tool: "web_fetch", input: `{"urls":["https://docs.example.com/a"]}`, want: true},
		{name: "no url at all", tool: "WebFetch", input: `{"question":"what is up"}`, want: false},
		{name: "no arguments at all", tool: "WebFetch", input: `{}`, want: false},
		{name: "unparseable arguments", tool: "WebFetch", input: `not json`, want: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := p.Decide(c.tool, json.RawMessage(c.input))
			if d.Allow != c.want {
				t.Fatalf("Decide(%s, %s) = %v, want %v (%s)", c.tool, c.input, d.Allow, c.want, d.Message)
			}
			if !d.Allow && d.Message == "" {
				t.Error("a refused fetch carries no message")
			}
		})
	}
}

// TestFetchIsNotAlwaysAllowed pins the change this task exists for: the
// fetch tools are not approved by name any more, on either naming
// convention, and WebSearch — which carries a query and no destination —
// still is.
func TestFetchIsNotAlwaysAllowed(t *testing.T) {
	for _, tool := range []string{"WebFetch", "web_fetch"} {
		if AlwaysAllowed[tool] {
			t.Errorf("%s is in AlwaysAllowed, which approves it without looking at the URL", tool)
		}
		if !FetchTools[tool] {
			t.Errorf("%s is not in FetchTools, so Decide would fall through to \"not permitted\"", tool)
		}
	}
	if !AlwaysAllowed["WebSearch"] {
		t.Error("WebSearch should still be allowed: it carries a query, not a destination")
	}
	// A fix session is no more entitled to choose a destination than a
	// triage one.
	fix := FixPolicy(t.TempDir(), nil, nil, nil)
	if d := fix.Decide("WebFetch", json.RawMessage(`{"url":"https://attacker.example/"}`)); d.Allow {
		t.Error("a fix policy approved a fetch with no permissions.fetch entries")
	}
}

func TestValidateFetchEntry(t *testing.T) {
	ok := []string{
		"example.com",
		"docs.example.com",
		"*.example.com",
		"*.internal.example.com",
		"xn--80ak6aa92e.com",
		"http://localhost",
		"http://localhost:3000",
		"http://127.0.0.1:11434",
		"HTTP://LocalHost:8080",
	}
	for _, entry := range ok {
		if reason := ValidateFetchEntry(entry); reason != "" {
			t.Errorf("ValidateFetchEntry(%q) = %q, want accepted", entry, reason)
		}
	}

	bad := map[string]string{
		"":                       "empty",
		"   ":                    "empty",
		"*":                      "wildcard",
		"*.*":                    "wildcard",
		"example.com/docs":       "path",
		"example.com/":           "path",
		"https://example.com":    "https://",
		"user@example.com":       "userinfo",
		"ev*l.example.com":       "wildcard outside",
		"example.*":              "wildcard outside",
		"http://example.com":     "not loopback",
		"example.com:8443":       "port",
		"ftp://example.com":      "scheme",
		"localhost":              "single label",
		"exa mple.com":           "space",
		"-example.com":           "not a hostname",
		"http://localhost:http":  "port that is not a number",
		"http://127.0.0.1/admin": "path",
		"*.com":                  "top-level domain",
		"*.io":                   "top-level domain",
		"127.1":                  "IP literal",
		"2130706433":             "IP literal",
		"0x7f000001":             "IP literal",
	}
	for entry, want := range bad {
		reason := ValidateFetchEntry(entry)
		if reason == "" {
			t.Errorf("ValidateFetchEntry(%q) accepted it", entry)
			continue
		}
		if !strings.Contains(reason, want) {
			t.Errorf("ValidateFetchEntry(%q) = %q, want a reason carrying %q", entry, reason, want)
		}
	}
}
