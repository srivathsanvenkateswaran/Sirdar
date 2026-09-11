package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
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
	if c.Budget.MaxTurns != 120 || c.Budget.MaxMinutes != 25 || c.Budget.MaxUSD != 5 {
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

// --- provider: openai ----------------------------------------------------

const openaiConfig = `
workspace: demo
provider: openai
openai:
  baseUrl: https://openrouter.ai/api/v1
  apiKey: env:OPENROUTER_API_KEY
  model: qwen/qwen3-coder
  price:
    inputPerMTok: 0.2
    outputPerMTok: 0.8
  temperature: 0
  extraHeaders:
    HTTP-Referer: https://github.com/srivathsanvenkateswaran/Sirdar
`

func TestLoadOpenAIBlock(t *testing.T) {
	c, err := Load(writeCfg(t, openaiConfig))
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "openai" {
		t.Fatalf("provider = %q", c.Provider)
	}
	o := c.OpenAI
	if o == nil {
		t.Fatal("openai block was not loaded")
	}
	if o.BaseURL != "https://openrouter.ai/api/v1" || o.Model != "qwen/qwen3-coder" {
		t.Fatalf("openai = %+v", o)
	}
	if o.MaxContextTokens != DefaultMaxContextTokens {
		t.Errorf("maxContextTokens = %d, want the %d default", o.MaxContextTokens, DefaultMaxContextTokens)
	}
	if o.Price == nil || o.Price.InputPerMTok != 0.2 || o.Price.OutputPerMTok != 0.8 {
		t.Errorf("price = %+v", o.Price)
	}
	if o.Temperature == nil || *o.Temperature != 0 {
		t.Errorf("temperature = %v, want an explicit 0 rather than unset", o.Temperature)
	}
	if o.ExtraHeaders["HTTP-Referer"] == "" {
		t.Errorf("extraHeaders = %v", o.ExtraHeaders)
	}
}

func TestValidateOpenAI(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // substring of the expected error; "" means it must load
	}{
		{"complete", openaiConfig, ""},
		{"no block", "workspace: demo\nprovider: openai\n", "openai:"},
		{
			"no baseUrl",
			"workspace: demo\nprovider: openai\nopenai:\n  model: m\n",
			"openai.baseUrl",
		},
		{
			"no model",
			"workspace: demo\nprovider: openai\nopenai:\n  baseUrl: https://api.example/v1\n",
			"openai.model",
		},
		{
			"relative baseUrl",
			"workspace: demo\nprovider: openai\nopenai:\n  baseUrl: /v1\n  model: m\n",
			"openai.baseUrl",
		},
		{
			"a literal key instead of a reference",
			"workspace: demo\nprovider: openai\nopenai:\n  baseUrl: https://api.example/v1\n  model: m\n  apiKey: sk-live-1234\n",
			"openai.apiKey",
		},
		{
			"a negative price",
			"workspace: demo\nprovider: openai\nopenai:\n  baseUrl: https://api.example/v1\n  model: m\n  price:\n    inputPerMTok: -1\n",
			"openai.price.inputPerMTok",
		},
		{
			// An openai block left behind while the workspace runs on
			// claude is not an error: only its own fields are checked.
			"unused block on another provider",
			"workspace: demo\nprovider: claude\nopenai:\n  baseUrl: https://api.example/v1\n",
			"",
		},
		{
			"an unknown provider",
			"workspace: demo\nprovider: gemini\n",
			"claude, codex, openai or acp",
		},
		{
			"acp with no block",
			"workspace: demo\nprovider: acp\n",
			"acp: is required",
		},
		{
			"acp with no command",
			"workspace: demo\nprovider: acp\nacp:\n  args: [\"--experimental-acp\"]\n",
			"acp.command",
		},
		{
			"acp configured",
			"workspace: demo\nprovider: acp\nacp:\n  command: gemini\n  args: [\"--experimental-acp\"]\n  env:\n    GEMINI_ACP: \"1\"\n",
			"",
		},
		{
			// An acp block left behind while the workspace runs on claude
			// is not an error, the same as an unused openai block.
			"unused acp block on another provider",
			"workspace: demo\nprovider: claude\nacp:\n  command: goose\n",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, c.body))
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("Load: %v", err)
			case c.want == "":
			case err == nil:
				t.Fatalf("want an error mentioning %q, got none", c.want)
			case !strings.Contains(err.Error(), c.want):
				t.Fatalf("error = %v, want it to mention %q", err, c.want)
			}
		})
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

// TestBuiltinHelpdeskUnderTrackerIsRejected: the helpdesk adapters read
// support conversations and have no issue list to sweep, so naming one under
// sources.tracker leaves a workspace that loads and then fails on its first
// run.
func TestBuiltinHelpdeskUnderTrackerIsRejected(t *testing.T) {
	for _, block := range []string{
		"    adapter: zendesk\n    subdomain: acme\n    oauthToken: env:ZENDESK_OAUTH\n",
		"    adapter: freshdesk\n    domain: acme.freshdesk.com\n    apiKey: env:FRESHDESK_KEY\n",
		"    adapter: zohodesk\n    orgId: \"1\"\n    baseUrl: https://desk.zoho.com\n    token: env:ZOHO\n",
		"    adapter: helpscout\n    clientId: env:HS_ID\n    clientSecret: env:HS_SECRET\n",
		"    adapter: intercom\n    accessToken: env:INTERCOM_TOKEN\n",
		"    adapter: hubspot\n    accessToken: env:HUBSPOT_TOKEN\n",
	} {
		_, err := Load(writeCfg(t, trackerCfg(block)))
		if err == nil || !strings.Contains(err.Error(), "sources.helpdesk") {
			t.Errorf("%s: want an error pointing at sources.helpdesk, got %v", strings.TrimSpace(block), err)
		}
	}
}

// TestAuthIsRejectedOnNonZohoAdapters: `auth:` is the Zoho OAuth refresh
// grant and nothing else understands it. An adapter that quietly ignored the
// block would read as "my OAuth config is live" right up until the first run
// failed for want of a credential.
func TestAuthIsRejectedOnNonZohoAdapters(t *testing.T) {
	const auth = "    auth:\n      clientId: env:ID\n      clientSecret: env:SECRET\n      refreshToken: env:REFRESH\n"
	for name, body := range map[string]string{
		"jira":      trackerCfg("    adapter: jira\n    baseUrl: https://acme.atlassian.net\n    pat: env:JIRA_PAT\n" + auth),
		"linear":    trackerCfg("    adapter: linear\n    apiKey: env:LINEAR_KEY\n" + auth),
		"zendesk":   helpdeskCfg("    adapter: zendesk\n    subdomain: acme\n    oauthToken: env:ZENDESK_OAUTH\n" + auth),
		"helpscout": helpdeskCfg("    adapter: helpscout\n    clientId: env:HS_ID\n    clientSecret: env:HS_SECRET\n" + auth),
		"intercom":  helpdeskCfg("    adapter: intercom\n    accessToken: env:INTERCOM_TOKEN\n" + auth),
		"hubspot":   helpdeskCfg("    adapter: hubspot\n    accessToken: env:HUBSPOT_TOKEN\n" + auth),
		"freshdesk": helpdeskCfg("    adapter: freshdesk\n    domain: acme.freshdesk.com\n    apiKey: env:FRESHDESK_KEY\n" + auth),
		"exec":      helpdeskCfg("    adapter: exec\n    command: ./tickets.sh\n" + auth),
	} {
		_, err := Load(writeCfg(t, body))
		if err == nil || !strings.Contains(err.Error(), ".auth: is only supported for adapter zohodesk") {
			t.Errorf("%s: want the auth block refused, got %v", name, err)
		}
	}

	// zohodesk itself still accepts it.
	ok := helpdeskCfg("    adapter: zohodesk\n    orgId: \"1\"\n    baseUrl: https://desk.zoho.com\n" + auth)
	if _, err := Load(writeCfg(t, ok)); err != nil {
		t.Errorf("zohodesk with an auth block: %v", err)
	}
}

// --- built-in helpdesk adapters (zendesk, freshdesk) ---

// helpdeskCfg builds a workspace whose only source is the helpdesk block
// given, so a validation error can only have come from that block.
func helpdeskCfg(block string) string {
	return "workspace: demo\nsources:\n  helpdesk:\n" + block
}

func TestValidateZendesk(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  string // substring of the expected error; "" means it must load
	}{
		{
			name:  "basic auth",
			block: "    adapter: zendesk\n    subdomain: acme\n    email: you@acme.com\n    apiToken: env:ZENDESK_TOKEN\n",
		},
		{
			name:  "oauth",
			block: "    adapter: zendesk\n    subdomain: acme\n    oauthToken: env:ZENDESK_OAUTH\n",
		},
		{
			name:  "an optional baseUrl override",
			block: "    adapter: zendesk\n    subdomain: acme\n    baseUrl: https://support.acme.com\n    oauthToken: env:ZENDESK_OAUTH\n",
		},
		{
			name:  "missing subdomain",
			block: "    adapter: zendesk\n    oauthToken: env:ZENDESK_OAUTH\n",
			want:  "sources.helpdesk.subdomain",
		},
		{
			name:  "no credentials at all",
			block: "    adapter: zendesk\n    subdomain: acme\n",
			want:  "one of email + apiToken or oauthToken",
		},
		{
			name:  "both auth forms at once",
			block: "    adapter: zendesk\n    subdomain: acme\n    email: you@acme.com\n    apiToken: env:ZENDESK_TOKEN\n    oauthToken: env:ZENDESK_OAUTH\n",
			want:  "not both",
		},
		{
			name:  "email without apiToken",
			block: "    adapter: zendesk\n    subdomain: acme\n    email: you@acme.com\n",
			want:  "both email and apiToken",
		},
		{
			name:  "apiToken without email",
			block: "    adapter: zendesk\n    subdomain: acme\n    apiToken: env:ZENDESK_TOKEN\n",
			want:  "both email and apiToken",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, helpdeskCfg(tc.block)))
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

func TestValidateZendeskCredentialRefsAreNotLiterals(t *testing.T) {
	for key, block := range map[string]string{
		"apiToken":   "    adapter: zendesk\n    subdomain: acme\n    email: you@acme.com\n    apiToken: shhh\n",
		"oauthToken": "    adapter: zendesk\n    subdomain: acme\n    oauthToken: shhh\n",
	} {
		_, err := Load(writeCfg(t, helpdeskCfg(block)))
		if err == nil || !strings.Contains(err.Error(), "sources.helpdesk."+key) {
			t.Errorf("%s: want an error naming the key, got %v", key, err)
		}
	}
}

func TestValidateFreshdesk(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{
			name:  "valid",
			block: "    adapter: freshdesk\n    domain: acme.freshdesk.com\n    apiKey: env:FRESHDESK_KEY\n",
		},
		{
			name:  "missing domain",
			block: "    adapter: freshdesk\n    apiKey: env:FRESHDESK_KEY\n",
			want:  "sources.helpdesk.domain",
		},
		{
			name:  "missing apiKey",
			block: "    adapter: freshdesk\n    domain: acme.freshdesk.com\n",
			want:  "sources.helpdesk.apiKey",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, helpdeskCfg(tc.block)))
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

func TestValidateFreshdeskCredentialRefIsNotALiteral(t *testing.T) {
	block := "    adapter: freshdesk\n    domain: acme.freshdesk.com\n    apiKey: shhh\n"
	_, err := Load(writeCfg(t, helpdeskCfg(block)))
	if err == nil || !strings.Contains(err.Error(), "sources.helpdesk.apiKey") {
		t.Fatalf("want an error naming the key, got %v", err)
	}
}

// --- Help Scout, Intercom, HubSpot ---

// TestValidateFixedHostHelpdesks covers the three adapters that talk to one
// fixed vendor host and so have nothing to configure but their credentials:
// what each cannot work without, and that a missing one is named.
func TestValidateFixedHostHelpdesks(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{
			name:  "helpscout valid",
			block: "    adapter: helpscout\n    clientId: env:HS_ID\n    clientSecret: env:HS_SECRET\n",
		},
		{
			name:  "helpscout missing clientId",
			block: "    adapter: helpscout\n    clientSecret: env:HS_SECRET\n",
			want:  "sources.helpdesk.clientId",
		},
		{
			name:  "helpscout missing clientSecret",
			block: "    adapter: helpscout\n    clientId: env:HS_ID\n",
			want:  "sources.helpdesk.clientSecret",
		},
		{
			name:  "intercom valid",
			block: "    adapter: intercom\n    accessToken: env:INTERCOM_TOKEN\n",
		},
		{
			name:  "intercom missing accessToken",
			block: "    adapter: intercom\n",
			want:  "sources.helpdesk.accessToken",
		},
		{
			name:  "hubspot valid",
			block: "    adapter: hubspot\n    accessToken: keychain:hubspot-token\n",
		},
		{
			name:  "hubspot missing accessToken",
			block: "    adapter: hubspot\n",
			want:  "sources.helpdesk.accessToken",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, helpdeskCfg(tc.block)))
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

func TestValidateFixedHostHelpdeskCredentialRefsAreNotLiterals(t *testing.T) {
	for key, block := range map[string]string{
		"clientId":     "    adapter: helpscout\n    clientId: shhh\n    clientSecret: env:HS_SECRET\n",
		"clientSecret": "    adapter: helpscout\n    clientId: env:HS_ID\n    clientSecret: shhh\n",
		"accessToken":  "    adapter: intercom\n    accessToken: shhh\n",
	} {
		_, err := Load(writeCfg(t, helpdeskCfg(block)))
		if err == nil || !strings.Contains(err.Error(), "sources.helpdesk."+key) {
			t.Errorf("%s: want an error naming the key, got %v", key, err)
		}
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

// TestDefaultConfigYAMLDocumentsOpenAI keeps the scaffold and the
// implementation in step: `sirdar init` has to show the block, and the
// file it writes has to load.
func TestDefaultConfigYAMLDocumentsOpenAI(t *testing.T) {
	for _, want := range []string{"claude | codex | openai", "# openai:", "#   baseUrl:", "#   model:"} {
		if !strings.Contains(DefaultConfigYAML, want) {
			t.Errorf("DefaultConfigYAML does not mention %q", want)
		}
	}
	body := strings.ReplaceAll(DefaultConfigYAML, "<name>", "demo")
	body = strings.ReplaceAll(body, "<tracker-adapter>", "adapter")
	body = strings.ReplaceAll(body, "<org-id>", "1")
	if _, err := Load(writeCfg(t, body)); err != nil {
		t.Fatalf("the scaffolded config does not load: %v", err)
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
		"# adapter: zendesk", "# adapter: freshdesk",
		"# adapter: helpscout", "# adapter: intercom", "# adapter: hubspot",
		"# helpdeskRef:", `#   pattern: 'Zoho Ticket URL:\s*(\S+)'`, `#   idPattern: '(\d+)$'`,
	} {
		if !strings.Contains(DefaultConfigYAML, want) {
			t.Errorf("DefaultConfigYAML is missing %q", want)
		}
	}
}

// TestMCPAndAttachmentDefaults pins the two settings D2 and D7 added: MCP
// servers are restricted to the workspace unless the operator says
// otherwise, and an attachment cap exists even in a config that predates
// it.
func TestMCPAndAttachmentDefaults(t *testing.T) {
	root := writeCfg(t, minimal)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.WorkspaceOnlyMCP() {
		t.Error("mcp.workspaceOnly must default to true")
	}
	if cfg.AttachmentMaxBytes() != DefaultAttachmentMaxBytes {
		t.Errorf("attachments.maxBytes %d", cfg.AttachmentMaxBytes())
	}
	if got := cfg.MCPConfigPath(); got != "" {
		t.Errorf("with no .mcp.json in the workspace there is nothing to point the session at, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cfg.MCPConfigPath(); got != filepath.Join(root, ".mcp.json") {
		t.Errorf("MCPConfigPath %q", got)
	}

	root = writeCfg(t, minimal+`
mcp:
  workspaceOnly: false
attachments:
  maxBytes: 2048
permissions:
  mcp:
    - "mcp__grafana__query_*"
`)
	cfg, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkspaceOnlyMCP() {
		t.Error("mcp.workspaceOnly: false must be honoured")
	}
	if cfg.MCPConfigPath() != "" {
		t.Error("with workspaceOnly off, no config is forced on the session")
	}
	if cfg.AttachmentMaxBytes() != 2048 {
		t.Errorf("attachments.maxBytes %d", cfg.AttachmentMaxBytes())
	}
	if len(cfg.Permissions.MCP) != 1 {
		t.Errorf("permissions.mcp %v", cfg.Permissions.MCP)
	}
}

func TestFixBashDefaultsAndOverride(t *testing.T) {
	cfg, err := Load(writeCfg(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Permissions.FixBash) != len(DefaultFixBash) {
		t.Fatalf("fixBash = %v, want the default list", cfg.Permissions.FixBash)
	}
	for _, want := range []string{"git status*", "git diff*", "git log*", "git show*", "git grep*", "git blame*",
		"dotnet build*", "dotnet test*", "npm test*", "go build*", "go test*", "make *"} {
		var found bool
		for _, got := range cfg.Permissions.FixBash {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the default fixBash list is missing %q: %v", want, cfg.Permissions.FixBash)
		}
	}

	// The git entries are the read-only ones. Sirdar makes the branch, the
	// commit and the push itself, so a default that hands the agent
	// `git commit`, `git push` or `git config` gives away reach the flow
	// never needed.
	for _, pattern := range cfg.Permissions.FixBash {
		if pattern == "git *" {
			t.Errorf("the default fixBash list still carries a blanket %q", pattern)
		}
	}
	for _, banned := range []string{"git commit -m x", "git push origin main", "git config user.email x@y",
		"git reset --hard HEAD~1", "git checkout -B other"} {
		if ok, _ := provider.MatchCommand("", cfg.Permissions.FixBash, banned); ok {
			t.Errorf("the default fixBash list allows %q", banned)
		}
	}
	for _, wanted := range []string{"git status --porcelain", "git diff HEAD", "git log --oneline -20",
		"git show HEAD", "git grep -n rows", "git blame export/csv.go", "go test ./..."} {
		if ok, reason := provider.MatchCommand("", cfg.Permissions.FixBash, wanted); !ok {
			t.Errorf("the default fixBash list refuses %q: %s", wanted, reason)
		}
	}

	// A workspace that names its own list gets exactly that list: the
	// default is a starting point, not a floor.
	cfg, err = Load(writeCfg(t, minimal+`permissions:
  fixBash:
    - "just *"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Permissions.FixBash) != 1 || cfg.Permissions.FixBash[0] != "just *" {
		t.Fatalf("fixBash = %v", cfg.Permissions.FixBash)
	}
	// The read-only list stays its own thing.
	if len(cfg.Permissions.Bash) != 0 {
		t.Errorf("permissions.bash was filled in from fixBash: %v", cfg.Permissions.Bash)
	}
}

// --- language ---

func TestLoadAppliesLanguageDefaults(t *testing.T) {
	c, err := Load(writeCfg(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if c.NotesLanguage() != "en" {
		t.Errorf("language.notes default = %q, want en", c.NotesLanguage())
	}
	if c.CustomerLanguage() != "auto" {
		t.Errorf("language.customer default = %q, want auto", c.CustomerLanguage())
	}
	if !c.RTLMarkup() {
		t.Error("language.rtlMarkup default = false, want true")
	}
}

func TestLoadReadsLanguageBlock(t *testing.T) {
	c, err := Load(writeCfg(t, minimal+"\nlanguage:\n  notes: en\n  customer: ar\n  rtlMarkup: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.NotesLanguage() != "en" || c.CustomerLanguage() != "ar" {
		t.Fatalf("language = %+v", c.Language)
	}
	if c.RTLMarkup() {
		t.Error("rtlMarkup: false was not applied")
	}
}

// A Config built by hand — in a test, or by the desktop wiring — reads as
// the defaults rather than as "no language, no markup".
func TestZeroConfigReadsAsTheLanguageDefaults(t *testing.T) {
	var c Config
	if c.NotesLanguage() != "en" || c.CustomerLanguage() != "auto" || !c.RTLMarkup() {
		t.Fatalf("zero Config: notes=%q customer=%q rtl=%v", c.NotesLanguage(), c.CustomerLanguage(), c.RTLMarkup())
	}
}

func TestLoadRejectsBadLanguageCodes(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"notes is a sentence", "\nlanguage:\n  notes: English please\n", "language.notes"},
		{"notes cannot be auto", "\nlanguage:\n  notes: auto\n", "language.notes"},
		{"customer is a sentence", "\nlanguage:\n  customer: whatever the ticket is\n", "language.customer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, minimal+c.body))
			if err == nil {
				t.Fatalf("want an error naming %s, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error does not name %s: %v", c.want, err)
			}
		})
	}
}

func TestLoadAcceptsARegionalLanguageTag(t *testing.T) {
	c, err := Load(writeCfg(t, minimal+"\nlanguage:\n  customer: ar-SA\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.CustomerLanguage() != "ar-SA" {
		t.Fatalf("customer = %q", c.CustomerLanguage())
	}
}

// TestDefaultConfigYAMLDocumentsLanguage: `sirdar init` has to write the
// language block, and it has to carry the values a fresh workspace runs
// with rather than leaving the operator to discover them in the docs.
func TestDefaultConfigYAMLDocumentsLanguage(t *testing.T) {
	for _, want := range []string{"language:", "notes: en", "customer: auto", "rtlMarkup: true"} {
		if !strings.Contains(DefaultConfigYAML, want) {
			t.Errorf("DefaultConfigYAML does not mention %q", want)
		}
	}
	body := strings.Replace(DefaultConfigYAML, "<name>", "demo", 1)
	c, err := Load(writeCfg(t, body))
	if err != nil {
		t.Fatalf("the scaffold does not load: %v", err)
	}
	if c.NotesLanguage() != "en" || c.CustomerLanguage() != "auto" || !c.RTLMarkup() {
		t.Fatalf("scaffolded language block: notes=%q customer=%q rtl=%v", c.NotesLanguage(), c.CustomerLanguage(), c.RTLMarkup())
	}
}
