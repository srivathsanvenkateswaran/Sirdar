package httpx

import (
	"net/url"
	"testing"
)

func mustTrust(t *testing.T, base string, extra ...HostRule) *Trust {
	t.Helper()
	tr, err := NewTrust(base, extra...)
	if err != nil {
		t.Fatalf("NewTrust(%q): %v", base, err)
	}
	return tr
}

func TestNewTrustRejectsAnUnusableBase(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"", "   ", "/rest/api", "ftp://files.example.com", "://nope"} {
		if _, err := NewTrust(base); err == nil {
			t.Errorf("NewTrust(%q) = nil error, want a failure", base)
		}
	}
}

func TestTrustCheck(t *testing.T) {
	t.Parallel()

	zendesk := []HostRule{{Suffix: ".zendesk.com"}, {Suffix: ".zdusercontent.com"}}
	azdo := []HostRule{{Suffix: "dev.azure.com", SendCredential: true}, {Suffix: "contoso.visualstudio.com", SendCredential: true}}

	tests := []struct {
		name   string
		base   string
		rules  []HostRule
		raw    string
		fetch  bool
		cred   bool
		reason string
	}{
		// The configured host, in the forms one URL can take.
		{name: "same host", base: "https://jira.example.com", raw: "https://jira.example.com/x", fetch: true, cred: true},
		{name: "host case ignored", base: "https://jira.example.com", raw: "https://JIRA.EXAMPLE.COM/x", fetch: true, cred: true},
		{name: "trailing root dot", base: "https://jira.example.com", raw: "https://jira.example.com./x", fetch: true, cred: true},
		{name: "explicit default port", base: "https://jira.example.com", raw: "https://jira.example.com:443/x", fetch: true, cred: true},

		// Lookalikes.
		{name: "suffix lookalike", base: "https://jira.example.com", raw: "https://jira.example.com.attacker.example/x", fetch: false, reason: ReasonNotTrusted},
		{name: "prefix lookalike", base: "https://jira.example.com", raw: "https://xjira.example.com/x", fetch: false, reason: ReasonNotTrusted},
		{name: "unrelated host", base: "https://jira.example.com", raw: "https://attacker.example/x", fetch: false, reason: ReasonNotTrusted},

		// Userinfo: the real destination of this URL is attacker.example.
		{name: "userinfo decoy", base: "https://jira.example.com", raw: "https://jira.example.com@attacker.example/x", fetch: false, reason: ReasonUserinfo},
		{name: "userinfo on the right host", base: "https://jira.example.com", raw: "https://someone@jira.example.com/x", fetch: false, reason: ReasonUserinfo},

		// Transport and ports.
		{name: "downgraded scheme", base: "https://jira.example.com", raw: "http://jira.example.com/x", fetch: false, reason: ReasonScheme},
		{name: "non-http scheme", base: "https://jira.example.com", raw: "ftp://jira.example.com/x", fetch: false, reason: ReasonScheme},
		{name: "file url has no host", base: "https://jira.example.com", raw: "file:///etc/passwd", fetch: false, reason: ReasonNoHost},
		{name: "other port", base: "https://jira.example.com", raw: "https://jira.example.com:8443/x", fetch: false, reason: ReasonNotTrusted},
		{name: "relative", base: "https://jira.example.com", raw: "/rest/api/2/attachment/1", fetch: false, reason: ReasonNoHost},
		{name: "empty", base: "https://jira.example.com", raw: "", fetch: false, reason: ReasonNoHost},
		{name: "unparseable", base: "https://jira.example.com", raw: "://nope", fetch: false, reason: ReasonNoHost},

		// The http exception: a workspace configured over plain HTTP has
		// already made that choice, and https to the same host is fine too.
		{name: "http base, same host and port", base: "http://127.0.0.1:8080", raw: "http://127.0.0.1:8080/x", fetch: true, cred: true},
		{name: "http base upgraded", base: "http://127.0.0.1:8080", raw: "https://127.0.0.1:8080/x", fetch: true, cred: true},
		{name: "http base, default port is another host", base: "http://127.0.0.1:8080", raw: "http://127.0.0.1/x", fetch: false, reason: ReasonNotTrusted},

		// Suffix rules: fetchable, but never credentialed.
		{name: "cdn suffix", base: "https://acme.zendesk.com", rules: zendesk, raw: "https://cdn.zdusercontent.com/x.png", fetch: true, cred: false},
		{name: "suffix apex", base: "https://acme.zendesk.com", rules: zendesk, raw: "https://zendesk.com/x.png", fetch: true, cred: false},
		{name: "suffix lookalike is not a suffix", base: "https://acme.zendesk.com", rules: zendesk, raw: "https://evilzendesk.com/x.png", fetch: false, reason: ReasonNotTrusted},
		{name: "suffix under another domain", base: "https://acme.zendesk.com", rules: zendesk, raw: "https://zendesk.com.attacker.example/x", fetch: false, reason: ReasonNotTrusted},
		{name: "suffix over plain http", base: "https://acme.zendesk.com", rules: zendesk, raw: "http://cdn.zdusercontent.com/x.png", fetch: false, reason: ReasonScheme},

		// Exact-host rules can carry the credential.
		{name: "exact rule with credential", base: "https://dev.azure.com/contoso", rules: azdo, raw: "https://contoso.visualstudio.com/_apis/wit/attachments/g", fetch: true, cred: true},
		{name: "exact rule is not a suffix", base: "https://dev.azure.com/contoso", rules: azdo, raw: "https://evil.dev.azure.com/x", fetch: false, reason: ReasonNotTrusted},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := mustTrust(t, tc.base, tc.rules...)
			fetch, cred, reason := tr.CheckRaw(tc.raw)
			if fetch != tc.fetch || cred != tc.cred {
				t.Errorf("CheckRaw(%q) = (%v, %v), want (%v, %v)", tc.raw, fetch, cred, tc.fetch, tc.cred)
			}
			if reason != tc.reason {
				t.Errorf("CheckRaw(%q) reason = %q, want %q", tc.raw, reason, tc.reason)
			}
		})
	}
}

func TestTrustCheckNilCases(t *testing.T) {
	t.Parallel()
	var nilTrust *Trust
	if fetch, _, reason := nilTrust.Check(&url.URL{Scheme: "https", Host: "example.com"}); fetch || reason != ReasonNoTrust {
		t.Errorf("nil Trust = (%v, %q), want (false, %q)", fetch, reason, ReasonNoTrust)
	}
	tr := mustTrust(t, "https://example.com")
	if fetch, _, reason := tr.Check(nil); fetch || reason != ReasonNoHost {
		t.Errorf("Check(nil) = (%v, %q), want (false, %q)", fetch, reason, ReasonNoHost)
	}
}

func TestNormalizeHost(t *testing.T) {
	t.Parallel()
	tests := []struct{ scheme, host, want string }{
		{"https", "Example.COM", "example.com"},
		{"https", "example.com.", "example.com"},
		{"https", "example.com:443", "example.com"},
		{"http", "example.com:80", "example.com"},
		{"https", "example.com:80", "example.com:80"},
		{"http", "example.com:443", "example.com:443"},
		{"https", "example.com:8443", "example.com:8443"},
		{"https", "[::1]:8443", "[::1]:8443"},
		{"https", "[::1]", "[::1]"},
	}
	for _, tc := range tests {
		if got := NormalizeHost(tc.scheme, tc.host); got != tc.want {
			t.Errorf("NormalizeHost(%q, %q) = %q, want %q", tc.scheme, tc.host, got, tc.want)
		}
	}
}

func TestHostOf(t *testing.T) {
	t.Parallel()
	tests := []struct{ raw, want string }{
		{"https://Example.com:443/x?token=secret", "example.com"},
		{"/relative", "(no host)"},
		{"://nope", "(unparseable)"},
	}
	for _, tc := range tests {
		if got := HostOf(tc.raw); got != tc.want {
			t.Errorf("HostOf(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
