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

// --- built-in tracker adapters ---

// trackerCfg builds a workspace whose only source is the tracker block
// given, so a validation error can only have come from that block.
func trackerCfg(block string) string {
	return "workspace: demo\nsources:\n  tracker:\n" + block
}

func TestValidateBuiltinTrackers(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  string // substring of the expected error; "" means it must load
	}{
		{
			name:  "jira cloud",
			block: "    adapter: jira\n    baseUrl: https://acme.atlassian.net\n    email: you@acme.com\n    apiToken: env:JIRA_TOKEN\n",
		},
		{
			name:  "jira data center pat",
			block: "    adapter: jira\n    baseUrl: https://jira.acme.internal\n    deployment: datacenter\n    pat: env:JIRA_PAT\n",
		},
		{
			name:  "jira without baseUrl",
			block: "    adapter: jira\n    pat: env:JIRA_PAT\n",
			want:  "sources.tracker.baseUrl",
		},
		{
			name:  "jira with an email but no apiToken",
			block: "    adapter: jira\n    baseUrl: https://acme.atlassian.net\n    email: you@acme.com\n",
			want:  "email and apiToken",
		},
		{
			name:  "jira with an unknown deployment",
			block: "    adapter: jira\n    baseUrl: https://acme.atlassian.net\n    pat: env:JIRA_PAT\n    deployment: onprem\n",
			want:  "sources.tracker.deployment",
		},
		{
			name:  "linear",
			block: "    adapter: linear\n    apiKey: env:LINEAR_KEY\n    teamKey: ENG\n",
		},
		{
			name:  "linear without an apiKey",
			block: "    adapter: linear\n    teamKey: ENG\n",
			want:  "sources.tracker.apiKey",
		},
		{
			name:  "azdo",
			block: "    adapter: azdo\n    orgUrl: https://dev.azure.com/acme\n    project: Payments\n    pat: env:AZDO_PAT\n",
		},
		{
			name:  "azdo without a project",
			block: "    adapter: azdo\n    orgUrl: https://dev.azure.com/acme\n    pat: env:AZDO_PAT\n",
			want:  "sources.tracker.project",
		},
		{
			name:  "azdo without a pat",
			block: "    adapter: azdo\n    orgUrl: https://dev.azure.com/acme\n    project: Payments\n",
			want:  "sources.tracker.pat",
		},
		{
			name:  "rally",
			block: "    adapter: rally\n    apiKey: env:RALLY_KEY\n    workspace: \"12345\"\n    types: [Defect]\n",
		},
		{
			name:  "rally without a workspace",
			block: "    adapter: rally\n    apiKey: env:RALLY_KEY\n",
			want:  "sources.tracker.workspace",
		},
		{
			name:  "an unknown adapter is still an error",
			block: "    adapter: shortcut\n",
			want:  `unknown adapter "shortcut"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, trackerCfg(tc.block)))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("want the config to load, got %v", err)
			case tc.want != "" && err == nil:
				t.Fatalf("want an error containing %q, got none", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("error %v does not contain %q", err, tc.want)
			}
		})
	}
}

// TestValidateBuiltinCredentialRefs covers the rule that matters most: a
// tracker credential names a secret, it never carries one.
func TestValidateBuiltinCredentialRefs(t *testing.T) {
	for key, block := range map[string]string{
		"apiToken": "    adapter: jira\n    baseUrl: https://acme.atlassian.net\n    email: you@acme.com\n    apiToken: shhh\n",
		"pat":      "    adapter: azdo\n    orgUrl: https://dev.azure.com/acme\n    project: Payments\n    pat: shhh\n",
		"apiKey":   "    adapter: linear\n    apiKey: lin_api_shhh\n",
	} {
		_, err := Load(writeCfg(t, trackerCfg(block)))
		if err == nil || !strings.Contains(err.Error(), "sources.tracker."+key) {
			t.Errorf("%s: want an error naming the key, got %v", key, err)
		}
	}
}

// TestRallyBaseURLDefault: a rally source that names no host gets Rally's
// production one, so the wiring and doctor both report the same endpoint.
func TestRallyBaseURLDefault(t *testing.T) {
	c, err := Load(writeCfg(t, trackerCfg("    adapter: rally\n    apiKey: env:RALLY_KEY\n    workspace: \"12345\"\n")))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Sources.Tracker.BaseURL; got != RallyDefaultBaseURL {
		t.Fatalf("baseUrl = %q, want %q", got, RallyDefaultBaseURL)
	}
}

// TestBuiltinTrackerUnderHelpdeskIsRejected: the four built-in adapters read
// issues. Their conversation view is picked up automatically by the wiring,
// so naming one under sources.helpdesk is a mistake, not a second source.
func TestBuiltinTrackerUnderHelpdeskIsRejected(t *testing.T) {
	body := "workspace: demo\nsources:\n  helpdesk:\n    adapter: linear\n    apiKey: env:LINEAR_KEY\n"
	_, err := Load(writeCfg(t, body))
	if err == nil || !strings.Contains(err.Error(), "sources.tracker") {
		t.Fatalf("want an error pointing at sources.tracker, got %v", err)
	}
}

// --- helpdeskRef fallback ---

func TestValidateHelpdeskRef(t *testing.T) {
	base := "    adapter: linear\n    apiKey: env:LINEAR_KEY\n    helpdeskRef:\n"
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{
			name:  "the Zoho URL rule",
			block: "      pattern: 'Zoho Ticket URL:\\s*(\\S+)'\n      idPattern: '(\\d+)$'\n",
		},
		{
			name:  "idPattern is optional",
			block: "      pattern: 'ticket #(\\d+)'\n",
		},
		{
			name:  "a pattern that does not compile",
			block: "      pattern: '([0-9'\n",
			want:  "helpdeskRef.pattern",
		},
		{
			name:  "a pattern with no capture group",
			block: "      pattern: 'ticket #\\d+'\n",
			want:  "exactly one capture group",
		},
		{
			name:  "a pattern with two capture groups",
			block: "      pattern: '(ticket) #(\\d+)'\n",
			want:  "exactly one capture group",
		},
		{
			name:  "an idPattern with no capture group",
			block: "      pattern: 'ticket #(\\d+)'\n      idPattern: '\\d+'\n",
			want:  "helpdeskRef.idPattern",
		},
		{
			name:  "helpdeskRef without a pattern",
			block: "      idPattern: '(\\d+)'\n",
			want:  "helpdeskRef.pattern",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, trackerCfg(base+tc.block)))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("want the config to load, got %v", err)
			case tc.want != "" && err == nil:
				t.Fatalf("want an error containing %q, got none", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("error %v does not contain %q", err, tc.want)
			}
		})
	}
}

// TestDefaultConfigYAMLLoads: every commented example in the scaffold is
// still YAML, and the file `sirdar init` writes loads as it stands.
func TestDefaultConfigYAMLLoads(t *testing.T) {
	body := strings.Replace(DefaultConfigYAML, "<name>", "demo", 1)
	if _, err := Load(writeCfg(t, body)); err != nil {
		t.Fatalf("the scaffold does not load: %v", err)
	}
	for _, want := range []string{
		"# adapter: jira", "# adapter: linear", "# adapter: azdo", "# adapter: rally",
		"# helpdeskRef:", `#   pattern: 'Zoho Ticket URL:\s*(\S+)'`, `#   idPattern: '(\d+)$'`,
	} {
		if !strings.Contains(DefaultConfigYAML, want) {
			t.Errorf("DefaultConfigYAML is missing %q", want)
		}
	}
}
