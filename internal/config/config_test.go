package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755)
	os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644)
	return root
}

const minimal = `
workspace: demo
sources:
  helpdesk:
    adapter: zohodesk
    orgId: "1"
    baseUrl: https://desk.example
    token: env:ZOHO
`

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := Load(writeCfg(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "claude" || c.Billing != "subscription" || c.Concurrency != 1 {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if c.Budget.MaxTurns != 60 || c.Budget.MaxMinutes != 25 || c.Budget.MaxUSD != 5 {
		t.Fatalf("budget defaults: %+v", c.Budget)
	}
	if c.Notes.Filenames.RCA != "{key} RCA {slug}.md" {
		t.Fatalf("filename default %q", c.Notes.Filenames.RCA)
	}
	if c.Playbooks != ".sirdar/playbooks" {
		t.Fatalf("playbooks default %q", c.Playbooks)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"\nbogus: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}

// TestLoadRejectsUnknownSourceKey covers a typo inside a source block. An
// inline catch-all map used to swallow these, so a misspelled setting was
// simply never applied and nothing said so.
func TestLoadRejectsUnknownSourceKey(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"    baseurl: https://typo.example\n"))
	if err == nil {
		t.Fatal("want an error for an unknown key under sources.helpdesk")
	}
	if !strings.Contains(err.Error(), "baseurl") {
		t.Fatalf("the error must name the offending key, got %v", err)
	}
}

func TestValidateExecNeedsCommand(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"  tracker:\n    adapter: exec\n"))
	if err == nil || !strings.Contains(err.Error(), "sources.tracker.command") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateBadProvider(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"provider: gemini\n"))
	if err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateTokenRef(t *testing.T) {
	bad := strings.Replace(minimal, "env:ZOHO", "plaintext-token", 1)
	_, err := Load(writeCfg(t, bad))
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("got %v", err)
	}
}

func TestFindRoot(t *testing.T) {
	root := writeCfg(t, minimal)
	nested := filepath.Join(root, "a", "b")
	os.MkdirAll(nested, 0o755)
	got, err := FindRoot(nested)
	if err != nil || got != root {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := FindRoot(t.TempDir()); err == nil {
		t.Fatal("expected error outside a workspace")
	}
}

func TestExpandPath(t *testing.T) {
	c := &Config{Root: "/ws"}
	home, _ := os.UserHomeDir()
	if got := c.ExpandPath("~/x"); got != filepath.Join(home, "x") {
		t.Fatal(got)
	}
	if got := c.ExpandPath("rel/y"); got != "/ws/rel/y" {
		t.Fatal(got)
	}
	if got := c.ExpandPath("/abs"); got != "/abs" {
		t.Fatal(got)
	}
}

// authConfig is a helpdesk that authenticates with a refresh-token grant
// rather than a static access token.
const authConfig = `
workspace: demo
sources:
  helpdesk:
    adapter: zohodesk
    orgId: "1"
    baseUrl: https://desk.zoho.in
    auth:
      clientId: keychain:zoho-desk-client-id
      clientSecret: keychain:zoho-desk-client-secret
      refreshToken: env:ZOHO_REFRESH_TOKEN
`

func TestLoadAuthDerivesAccountsURL(t *testing.T) {
	c, err := Load(writeCfg(t, authConfig))
	if err != nil {
		t.Fatal(err)
	}
	auth := c.Sources.Helpdesk.Auth
	if auth == nil {
		t.Fatal("auth block was not loaded")
	}
	if auth.AccountsURL != "https://accounts.zoho.in" {
		t.Fatalf("accountsUrl = %q, want the India accounts server", auth.AccountsURL)
	}
	if auth.ClientID != "keychain:zoho-desk-client-id" || auth.RefreshToken != "env:ZOHO_REFRESH_TOKEN" {
		t.Fatalf("auth refs: %+v", auth)
	}
}

func TestAccountsURLFor(t *testing.T) {
	for base, want := range map[string]string{
		"https://desk.zoho.in":       "https://accounts.zoho.in",
		"https://desk.zoho.com":      "https://accounts.zoho.com",
		"https://desk.zoho.eu":       "https://accounts.zoho.eu",
		"https://desk.zoho.com.au":   "https://accounts.zoho.com.au",
		"https://desk.zoho.com/":     "https://accounts.zoho.com",
		"https://desk.example.local": "",
	} {
		if got := AccountsURLFor(base); got != want {
			t.Errorf("AccountsURLFor(%q) = %q, want %q", base, got, want)
		}
	}
}

// TestValidateAuthNeedsAccountsURL covers a data centre the table does not
// know: the operator has to name the accounts server, because guessing it
// would send the refresh token to the wrong host.
func TestValidateAuthNeedsAccountsURL(t *testing.T) {
	body := strings.Replace(authConfig, "https://desk.zoho.in", "https://desk.example.local", 1)
	_, err := Load(writeCfg(t, body))
	if err == nil || !strings.Contains(err.Error(), "accountsUrl") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateAuthNeedsEveryRef(t *testing.T) {
	body := strings.Replace(authConfig, "      clientSecret: keychain:zoho-desk-client-secret\n", "", 1)
	_, err := Load(writeCfg(t, body))
	if err == nil || !strings.Contains(err.Error(), "auth.clientSecret") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateAuthRefsAreNotLiterals(t *testing.T) {
	body := strings.Replace(authConfig, "env:ZOHO_REFRESH_TOKEN", "1000.actual-refresh-token", 1)
	_, err := Load(writeCfg(t, body))
	if err == nil || !strings.Contains(err.Error(), "auth.refreshToken") {
		t.Fatalf("got %v", err)
	}
}

// TestValidateTokenAndAuthAreExclusive covers a config that sets both: one
// of them would silently lose, and which one is not something an operator
// should have to work out from the source.
func TestValidateTokenAndAuthAreExclusive(t *testing.T) {
	body := authConfig + "    token: env:ZOHO\n"
	_, err := Load(writeCfg(t, body))
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateZohoNeedsTokenOrAuth(t *testing.T) {
	body := strings.Replace(minimal, "    token: env:ZOHO\n", "", 1)
	_, err := Load(writeCfg(t, body))
	if err == nil || !strings.Contains(err.Error(), "one of token or auth") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadRejectsUnknownAuthKey(t *testing.T) {
	_, err := Load(writeCfg(t, authConfig+"      clientid: nope\n"))
	if err == nil || !strings.Contains(err.Error(), "clientid") {
		t.Fatalf("want an error naming the misspelled key, got %v", err)
	}
}

// TestDefaultConfigYAMLShowsTheAuthBlock covers the template `sirdar init`
// writes: the recommended shape is the refresh grant, with the static token
// left commented out beside it.
func TestDefaultConfigYAMLShowsTheAuthBlock(t *testing.T) {
	for _, want := range []string{"auth:", "clientId:", "clientSecret:", "refreshToken:", "# token: keychain:<service>"} {
		if !strings.Contains(DefaultConfigYAML, want) {
			t.Errorf("DefaultConfigYAML is missing %q", want)
		}
	}
}
