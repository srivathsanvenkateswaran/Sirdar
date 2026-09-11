// Package config loads and validates a Sirdar workspace's .sirdar/config.yaml.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Provider selects which agent CLI drives triage and RCA runs.
type Provider string

// SourceConfig configures one ticket data source (tracker or helpdesk).
// It takes no inline catch-all map: one would swallow every unknown key
// under sources.*, which is exactly what KnownFields(true) is there to
// catch, and a typo in a source's settings would then be silently ignored.
type SourceConfig struct {
	Adapter string       `yaml:"adapter"` // "exec" | "zohodesk" | "zendesk" | "freshdesk" | "helpscout" | "intercom" | "hubspot" | "jira" | "linear" | "azdo" | "rally"
	Command string       `yaml:"command,omitempty"`
	OrgID   string       `yaml:"orgId,omitempty"`
	BaseURL string       `yaml:"baseUrl,omitempty"`
	Token   string       `yaml:"token,omitempty"` // credential ref
	Auth    *OAuthConfig `yaml:"auth,omitempty"`

	// Jira (Email and APIToken are also Zendesk's basic-auth credentials).
	Deployment    string `yaml:"deployment,omitempty"` // cloud | datacenter | auto
	Email         string `yaml:"email,omitempty"`      // Cloud/Zendesk account email, sent with apiToken
	APIToken      string `yaml:"apiToken,omitempty"`   // credential ref (Jira Cloud, Zendesk basic auth)
	PAT           string `yaml:"pat,omitempty"`        // credential ref (Jira Data Center, Azure DevOps)
	ProjectKey    string `yaml:"projectKey,omitempty"`
	EpicLinkField string `yaml:"epicLinkField,omitempty"`

	// Linear (APIKey is also Rally's and Freshdesk's credential).
	APIKey  string `yaml:"apiKey,omitempty"` // credential ref
	TeamKey string `yaml:"teamKey,omitempty"`

	// Azure DevOps (HelpdeskField is also Rally's).
	OrgURL             string `yaml:"orgUrl,omitempty"`
	Project            string `yaml:"project,omitempty"`
	HelpdeskLinkDomain string `yaml:"helpdeskLinkDomain,omitempty"`
	HelpdeskField      string `yaml:"helpdeskField,omitempty"`

	// Rally.
	Workspace string   `yaml:"workspace,omitempty"`
	Types     []string `yaml:"types,omitempty"`

	// Zendesk.
	Subdomain  string `yaml:"subdomain,omitempty"`  // account identifier, e.g. "acme" for acme.zendesk.com
	OAuthToken string `yaml:"oauthToken,omitempty"` // credential ref; alternative to email + apiToken

	// Freshdesk.
	Domain string `yaml:"domain,omitempty"` // account host, e.g. "acme.freshdesk.com"

	// Help Scout. Its Mailbox API has no API-key mode: every call carries
	// an OAuth2 token the adapter mints for itself from this pair, so both
	// are credential references and there is nothing else to configure.
	ClientID     string `yaml:"clientId,omitempty"`     // credential ref
	ClientSecret string `yaml:"clientSecret,omitempty"` // credential ref

	// Intercom (workspace access token) and HubSpot Service Hub
	// (private-app access token). Both are a single bearer credential
	// against a single fixed API host, so neither needs a base URL.
	AccessToken string `yaml:"accessToken,omitempty"` // credential ref

	// HelpdeskRef is the tracker-only fallback that reads a helpdesk
	// reference out of the ticket description when the tracker's own data
	// model carries none.
	HelpdeskRef *HelpdeskRefConfig `yaml:"helpdeskRef,omitempty"`
}

// HelpdeskRefConfig configures the description-regex fallback for a
// tracker ticket that carries its helpdesk link only as pasted text. The
// wiring layer applies it after the adapter has had its say, so a tracker
// with native linkage (Jira Service Management, a Linear customer request,
// an Azure DevOps hyperlink) is never overridden.
//
// Pattern is matched against the description and must have exactly one
// capture group: the group, not the whole match, is what gets used, so a
// rule can anchor on surrounding text it does not want to keep. IDPattern
// is optional and narrows that capture further — a URL down to the ticket
// number the helpdesk API expects.
type HelpdeskRefConfig struct {
	Pattern   string `yaml:"pattern"`
	IDPattern string `yaml:"idPattern,omitempty"`
}

// OAuthConfig configures an OAuth refresh-token grant, so the source mints
// its own short-lived access tokens instead of being handed one. A Zoho
// Desk access token lives an hour; a static token: ref therefore cannot
// carry an unattended run, and auth: is the shape that can.
//
// ClientID, ClientSecret and RefreshToken are credential references
// ("env:NAME", "keychain:SERVICE", "file:PATH" or "cmd:COMMAND"), never
// literal secrets.
type OAuthConfig struct {
	ClientID     string `yaml:"clientId"`
	ClientSecret string `yaml:"clientSecret"`
	RefreshToken string `yaml:"refreshToken"`
	AccountsURL  string `yaml:"accountsUrl,omitempty"`
}

// accountsURLs maps a Zoho Desk API host to the accounts server that issues
// its tokens. Zoho runs one accounts server per data centre, and the
// mapping is not a substring rewrite: desk.zoho.com.au is its own host, not
// desk.zoho.com with a suffix.
var accountsURLs = map[string]string{
	"desk.zoho.in":     "https://accounts.zoho.in",
	"desk.zoho.com":    "https://accounts.zoho.com",
	"desk.zoho.eu":     "https://accounts.zoho.eu",
	"desk.zoho.com.au": "https://accounts.zoho.com.au",
}

// AccountsURLFor returns the Zoho accounts server matching a Desk API base
// URL, or "" when the host is not one of the data centres above — in which
// case the operator has to name sources.*.auth.accountsUrl themselves.
func AccountsURLFor(baseURL string) string {
	u, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return ""
	}
	return accountsURLs[strings.ToLower(u.Hostname())]
}

// ACPConfig configures `provider: acp`, where Sirdar drives any agent that
// speaks the Agent Client Protocol. Command is the program to launch and
// Args the rest of its command line — `gemini --experimental-acp`,
// `goose acp`, `npx @zed-industries/claude-code-acp` — and Command is the
// one required field. Env is added to the agent's environment rather than
// replacing it, since an ACP agent authenticates however its own CLI does.
//
// Values in Env are literal, not credential references: they reach a child
// process's environment, which is exactly what a credential ref exists to
// avoid. Put a key in your shell and let the agent read it from there.
type ACPConfig struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
}

// OpenAIConfig configures `provider: openai`, where Sirdar runs the agent
// loop itself against any OpenAI-compatible Chat Completions endpoint —
// an aggregator, a vendor, or a server on the operator's own machine.
// BaseURL and Model are required; everything else has a default or is
// optional.
//
// APIKey is a credential reference ("env:NAME", "keychain:SERVICE",
// "file:PATH" or "cmd:COMMAND"), never the key itself, and it is optional:
// a local llama.cpp or Ollama server needs none.
type OpenAIConfig struct {
	BaseURL          string            `yaml:"baseUrl"`
	APIKey           string            `yaml:"apiKey,omitempty"`
	Model            string            `yaml:"model"`
	MaxContextTokens int               `yaml:"maxContextTokens,omitempty"`
	Price            *PriceConfig      `yaml:"price,omitempty"`
	Temperature      *float64          `yaml:"temperature,omitempty"`
	ExtraHeaders     map[string]string `yaml:"extraHeaders,omitempty"`
}

// PriceConfig is what a million tokens cost at the configured endpoint. It
// is what turns the response's token counts into the cost the USD budget
// is enforced against; with no price block the cost of a run is reported
// as 0 and only the turn and wall-clock budgets bite.
type PriceConfig struct {
	InputPerMTok  float64 `yaml:"inputPerMTok"`
	OutputPerMTok float64 `yaml:"outputPerMTok"`
}

// DefaultMaxContextTokens is the context window assumed for an
// openai-compatible endpoint that does not name one. The loop starts
// dropping old tool results as the prompt approaches it.
const DefaultMaxContextTokens = 128000

// RallyDefaultBaseURL is the subscription host a rally source falls back to
// when it names none: Rally's North American production instance.
const RallyDefaultBaseURL = "https://rally1.rallydev.com"

// DefaultAttachmentMaxBytes is the size above which a downloaded
// attachment is dropped from the bundle. A 17 MB screen recording is 99%
// of a bundle by bytes and none of it by evidence: the session cannot open
// it, so it is named in a warning instead.
const DefaultAttachmentMaxBytes = 10 << 20

// NotifyConfig posts a short digest of every finished run to a chat
// channel or a webhook receiver. Every destination is optional and any
// number may be configured at once; a workspace with no notify block posts
// nothing.
//
// On lists the terminal states worth hearing about; empty means all four.
// IncludeTitle is off because a support ticket's subject routinely names
// the customer who filed it, and a chat channel is a wider audience than
// the notes directory. The note's body is never sent, whatever it is set to.
//
// Slack.WebhookURL and Teams.WebhookURL are credential references
// ("env:NAME" or "keychain:SERVICE"), never the URL itself: an incoming
// webhook URL carries its own authorisation in its path, so it is a secret.
// A generic hook's url is a plain URL — it identifies a receiver that
// authenticates with the headers instead — and its headers' values and
// secret are credential references when they carry one.
type NotifyConfig struct {
	On           []string       `yaml:"on,omitempty"`
	IncludeTitle bool           `yaml:"includeTitle,omitempty"`
	Slack        *WebhookConfig `yaml:"slack,omitempty"`
	Teams        *WebhookConfig `yaml:"teams,omitempty"`
	Generic      []GenericHook  `yaml:"generic,omitempty"`
}

// WebhookConfig is one chat destination: the credential reference holding
// its incoming-webhook URL.
type WebhookConfig struct {
	WebhookURL string `yaml:"webhookUrl"`
}

// GenericHook is one receiver that takes the run event as JSON.
type GenericHook struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Secret  string            `yaml:"secret,omitempty"`
}

// NotifyStates are the terminal run states a notify.on list may name.
var NotifyStates = map[string]bool{"completed": true, "failed": true, "over_budget": true, "blocked": true}

// LanguageConfig names the two languages a run writes in.
//
// Notes is the language of the engineer's note — the whole body of it,
// including the translated complaint. Customer is the language of anything
// the customer will read: the reply draft on a triage note, the customer
// summary on an RCA. "auto" means the language of the ticket's first
// customer message, which is what a helpdesk that serves one country
// mostly wants; a fixed code ("ar", "en", "fr") pins it instead.
//
// RTLMarkup wraps a right-to-left paragraph the default templates emit in
// a <div dir="rtl"> block. Obsidian renders that HTML, so an Arabic
// complaint reads the way the customer wrote it instead of being laid out
// left to right. It applies only to the embedded templates: a workspace
// with its own notes.templates owns its markup, and Sirdar does not add
// any to it.
type LanguageConfig struct {
	Notes     string `yaml:"notes"`
	Customer  string `yaml:"customer"`
	RTLMarkup *bool  `yaml:"rtlMarkup"`
}

// DefaultNotesLanguage is the language an engineer's note is written in
// when the workspace names none.
const DefaultNotesLanguage = "en"

// CustomerLanguageAuto is the customer: value meaning "whatever language
// the ticket's first customer message is in".
const CustomerLanguageAuto = "auto"

// languageCode matches a BCP 47-shaped tag loose enough for the codes a
// helpdesk deals in ("ar", "en", "ar-SA", "zh-Hant") and strict enough to
// reject a sentence typed into the field.
var languageCode = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// Config is a fully loaded, defaulted, and validated workspace configuration.

type Config struct {
	Workspace string   `yaml:"workspace"`
	Provider  Provider `yaml:"provider"`
	Model     string   `yaml:"model"`
	Billing   string   `yaml:"billing"` // "subscription" | "api"
	Sources   struct {
		Tracker  *SourceConfig `yaml:"tracker"`
		Helpdesk *SourceConfig `yaml:"helpdesk"`
	} `yaml:"sources"`
	Notes struct {
		Dir       string `yaml:"dir"`
		Templates string `yaml:"templates"`
		Filenames struct {
			Triage     string `yaml:"triage"`
			RCA        string `yaml:"rca"`
			Resolution string `yaml:"resolution"`
		} `yaml:"filenames"`
	} `yaml:"notes"`
	Budget struct {
		MaxTurns   int     `yaml:"maxTurns"`
		MaxMinutes int     `yaml:"maxMinutes"`
		MaxUSD     float64 `yaml:"maxUsd"`
	} `yaml:"budget"`
	// Language says which language the engineer's note is written in and
	// which language anything shown to a customer is written in. They are
	// rarely the same: the workspace this was built for reads Arabic
	// tickets, keeps its notes in English, and replies to the customer in
	// Arabic again.
	Language    LanguageConfig `yaml:"language"`
	Concurrency int            `yaml:"concurrency"`
	Permissions struct {
		Bash []string `yaml:"bash"`
		// MCP is a list of glob patterns matched against a tool's full
		// name (e.g. "mcp__grafana__query_*"). When it is non-empty it
		// is the whole of the MCP allow-list: a tool that matches none
		// of the patterns is denied. When it is empty, read-shaped MCP
		// tools are allowed and write-shaped ones are denied by the
		// heuristic in internal/provider.
		MCP []string `yaml:"mcp"`
	} `yaml:"permissions"`
	// MCP controls which MCP servers the agent session can see at all.
	// WorkspaceOnly (default true) starts the session with
	// --strict-mcp-config against <root>/.mcp.json, so the operator's own
	// global connectors are not loaded into a triage run.
	MCP struct {
		WorkspaceOnly *bool `yaml:"workspaceOnly"`
	} `yaml:"mcp"`
	// Attachments caps what a helpdesk download may put in the bundle.
	Attachments struct {
		MaxBytes int64 `yaml:"maxBytes"`
	} `yaml:"attachments"`
	Playbooks string `yaml:"playbooks"`
	Providers struct {
		Claude struct {
			Path string `yaml:"path"`
		} `yaml:"claude"`
		Codex struct {
			Path string `yaml:"path"`
		} `yaml:"codex"`
	} `yaml:"providers"`
	OpenAI *OpenAIConfig `yaml:"openai,omitempty"`
	ACP    *ACPConfig    `yaml:"acp,omitempty"`

	// Webhooks configures the inbound trigger endpoints `sirdar serve`
	// exposes. They are off unless enabled, and exposing them off the
	// loopback interface needs `serve --allow-remote` and a TLS proxy.
	Webhooks WebhooksConfig `yaml:"webhooks"`
	Notify   *NotifyConfig  `yaml:"notify,omitempty"`

	Root string `yaml:"-"` // workspace root (directory containing .sirdar), set by Load
}

// Load reads <root>/.sirdar/config.yaml, applies defaults, and validates the result.
func Load(root string) (*Config, error) {
	path := filepath.Join(root, ".sirdar", "config.yaml")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	defer f.Close()

	var c Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	c.Root = root
	applyDefaults(&c)
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func applyDefaults(c *Config) {
	if c.Provider == "" {
		c.Provider = "claude"
	}
	if c.Billing == "" {
		c.Billing = "subscription"
	}
	if c.Concurrency == 0 {
		c.Concurrency = 1
	}
	if c.Budget.MaxTurns == 0 {
		// A turn is one model round-trip — one assistant message that
		// calls a tool or answers — which is the unit the Claude CLI
		// reports as num_turns. Real triages of a busy ticket run to
		// 40-60 of them, so the default leaves room for a hard one.
		c.Budget.MaxTurns = 120
	}
	if c.Budget.MaxMinutes == 0 {
		c.Budget.MaxMinutes = 25
	}
	if c.Budget.MaxUSD == 0 {
		c.Budget.MaxUSD = 5
	}
	if c.Notes.Dir == "" {
		c.Notes.Dir = ".sirdar/notes"
	}
	if c.Notes.Filenames.Triage == "" {
		c.Notes.Filenames.Triage = "{key} {slug}.md"
	}
	if c.Notes.Filenames.RCA == "" {
		c.Notes.Filenames.RCA = "{key} RCA {slug}.md"
	}
	if c.Notes.Filenames.Resolution == "" {
		c.Notes.Filenames.Resolution = "{key} RES {slug}.md"
	}
	if c.Playbooks == "" {
		c.Playbooks = ".sirdar/playbooks"
	}
	if c.Language.Notes == "" {
		c.Language.Notes = DefaultNotesLanguage
	}
	if c.Language.Customer == "" {
		c.Language.Customer = CustomerLanguageAuto
	}
	if c.Language.RTLMarkup == nil {
		yes := true
		c.Language.RTLMarkup = &yes
	}

	if c.OpenAI != nil && c.OpenAI.MaxContextTokens == 0 {
		c.OpenAI.MaxContextTokens = DefaultMaxContextTokens
	}
	if c.MCP.WorkspaceOnly == nil {
		yes := true
		c.MCP.WorkspaceOnly = &yes
	}
	if c.Attachments.MaxBytes == 0 {
		c.Attachments.MaxBytes = DefaultAttachmentMaxBytes
	}
	for _, s := range []*SourceConfig{c.Sources.Tracker, c.Sources.Helpdesk} {
		if s == nil {
			continue
		}
		if s.Auth != nil && s.Auth.AccountsURL == "" {
			s.Auth.AccountsURL = AccountsURLFor(s.BaseURL)
		}
		if s.Adapter == "rally" && s.BaseURL == "" {
			s.BaseURL = RallyDefaultBaseURL
		}
	}
}

// FindRoot walks up from dir to the first directory containing .sirdar/config.yaml.
func FindRoot(dir string) (string, error) {
	d := dir
	for {
		if _, err := os.Stat(filepath.Join(d, ".sirdar", "config.yaml")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("config: no .sirdar/config.yaml found above %s", dir)
		}
		d = parent
	}
}

// Validate checks that c satisfies the rules a run depends on, returning the
// first violation found. Each error names the offending key.
func (c *Config) Validate() error {
	switch c.Provider {
	case "claude", "codex", "openai", "acp":
	default:
		return fmt.Errorf("config: provider: must be claude, codex, openai or acp, got %q", c.Provider)
	}
	if err := validateOpenAI(c); err != nil {
		return err
	}
	if err := validateACP(c); err != nil {
		return err
	}
	switch c.Billing {
	case "subscription", "api":
	default:
		return fmt.Errorf("config: billing: must be subscription or api, got %q", c.Billing)
	}
	if c.Concurrency < 1 {
		return fmt.Errorf("config: concurrency: must be >= 1, got %d", c.Concurrency)
	}
	if err := validateLanguage(&c.Language); err != nil {
		return err
	}

	if c.Budget.MaxTurns <= 0 {
		return fmt.Errorf("config: budget.maxTurns: must be > 0, got %d", c.Budget.MaxTurns)
	}
	if c.Budget.MaxMinutes <= 0 {
		return fmt.Errorf("config: budget.maxMinutes: must be > 0, got %d", c.Budget.MaxMinutes)
	}
	if c.Budget.MaxUSD <= 0 {
		return fmt.Errorf("config: budget.maxUsd: must be > 0, got %v", c.Budget.MaxUSD)
	}
	if c.Attachments.MaxBytes < 0 {
		return fmt.Errorf("config: attachments.maxBytes: must be >= 0, got %d", c.Attachments.MaxBytes)
	}
	if err := validateSource("sources.tracker", c.Sources.Tracker, true); err != nil {
		return err
	}
	if err := validateSource("sources.helpdesk", c.Sources.Helpdesk, false); err != nil {
		return err
	}
	if err := validateWebhooks(&c.Webhooks); err != nil {
		return err
	}
	return validateNotify(c.Notify)
}

// validateNotify checks the notify block: the states named are states a run
// can end in, the chat webhooks name a credential rather than carry one,
// and every URL is one Sirdar will post a run's metadata to.
func validateNotify(n *NotifyConfig) error {
	if n == nil {
		return nil
	}
	for _, state := range n.On {
		if !NotifyStates[state] {
			return fmt.Errorf("config: notify.on: must be completed, failed, over_budget or blocked, got %q", state)
		}
	}
	for _, f := range []struct {
		key string
		w   *WebhookConfig
	}{{"notify.slack", n.Slack}, {"notify.teams", n.Teams}} {
		if f.w == nil {
			continue
		}
		if f.w.WebhookURL == "" {
			return fmt.Errorf("config: %s.webhookUrl: is required", f.key)
		}
		if err := credentialRef(f.key+".webhookUrl", f.w.WebhookURL); err != nil {
			return err
		}
	}
	for i, g := range n.Generic {
		key := fmt.Sprintf("notify.generic[%d]", i)
		if err := ValidateWebhookURL(key+".url", g.URL); err != nil {
			return err
		}
		for name, value := range g.Headers {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("config: %s.headers: a header name is empty", key)
			}
			if value == "" {
				return fmt.Errorf("config: %s.headers.%s: is empty", key, name)
			}
			if credentialShapedHeader(name) && !IsCredentialRef(value) {
				return fmt.Errorf("config: %s.headers.%s: looks like a credential and must start with env: or keychain:", key, name)
			}
		}
		if g.Secret != "" {
			if err := credentialRef(key+".secret", g.Secret); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateLanguage checks the language block. notes: has to be a language
// code — there is no "auto" for it, since the engineer's note is written
// for one team and that team reads one language. customer: is a code or
// "auto".
func validateLanguage(l *LanguageConfig) error {
	if !languageCode.MatchString(l.Notes) {
		return fmt.Errorf("config: language.notes: must be a language code such as en or ar, got %q", l.Notes)
	}
	if l.Customer != CustomerLanguageAuto && !languageCode.MatchString(l.Customer) {
		return fmt.Errorf("config: language.customer: must be auto or a language code such as ar, got %q", l.Customer)
	}
	return nil
}

// validateACP checks the acp block. The agent's launch command is the only
// thing the adapter cannot work out for itself, and it is required only
// when the workspace actually selects the provider — an acp block left in
// place while running on claude is not an error.
func validateACP(c *Config) error {
	if c.Provider == "acp" && c.ACP == nil {
		return fmt.Errorf("config: acp: is required when provider is acp")
	}
	if c.ACP == nil {
		return nil
	}
	if c.Provider == "acp" && strings.TrimSpace(c.ACP.Command) == "" {
		return fmt.Errorf("config: acp.command: is required when provider is acp")
	}
	return nil
}

// ValidateWebhookURL accepts an absolute https URL, or an http one on the
// loopback interface for a receiver running on this machine. Plain http to
// anywhere else would put the run's metadata, and any token the headers
// carry, on the wire in the clear.
func ValidateWebhookURL(key, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("config: %s: must be an absolute http or https URL, got %q", key, raw)
	}
	if u.User != nil {
		// The value may itself be a credential straight out of a
		// resolved reference, so the error names the problem without
		// echoing anything the URL carried.
		return fmt.Errorf("config: %s: must not carry userinfo (a username or password in the URL)", key)
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return fmt.Errorf("config: %s: must be https unless the host is loopback, got %q", key, raw)
	}
	return nil
}

// credentialShapedHeader reports whether name is the kind of header that
// carries a credential: Authorization, or anything ending in -Token, -Key
// or -Secret (case-insensitive). config.go requires those to be an env:/
// keychain: reference, the same way it already requires one for
// notify.slack.webhookUrl and notify.generic[].secret.
func credentialShapedHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "authorization" {
		return true
	}
	for _, suffix := range []string{"-token", "-key", "-secret"} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}

// isLoopback reports whether host names this machine, by name or by
// address.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validateOpenAI checks the openai block. baseUrl and model are required

// only when the workspace actually selects the provider — an openai block
// left in place while running on claude is not an error — but the fields
// that are set are checked either way, so a bad value is caught at load
// time rather than mid-run.
func validateOpenAI(c *Config) error {
	o := c.OpenAI
	if c.Provider == "openai" && o == nil {
		return fmt.Errorf("config: openai: is required when provider is openai")
	}
	if o == nil {
		return nil
	}
	if c.Provider == "openai" {
		if strings.TrimSpace(o.BaseURL) == "" {
			return fmt.Errorf("config: openai.baseUrl: is required when provider is openai")
		}
		if strings.TrimSpace(o.Model) == "" {
			return fmt.Errorf("config: openai.model: is required when provider is openai")
		}
	}
	if o.BaseURL != "" {
		u, err := url.Parse(o.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: openai.baseUrl: must be an absolute http or https URL, got %q", o.BaseURL)
		}
	}
	if o.APIKey != "" {
		if err := credentialRef("openai.apiKey", o.APIKey); err != nil {
			return err
		}
	}
	if o.MaxContextTokens < 0 {
		return fmt.Errorf("config: openai.maxContextTokens: must be > 0, got %d", o.MaxContextTokens)
	}
	if o.Price != nil {
		if o.Price.InputPerMTok < 0 {
			return fmt.Errorf("config: openai.price.inputPerMTok: must be >= 0, got %v", o.Price.InputPerMTok)
		}
		if o.Price.OutputPerMTok < 0 {
			return fmt.Errorf("config: openai.price.outputPerMTok: must be >= 0, got %v", o.Price.OutputPerMTok)
		}
	}
	return nil
}

// trackerOnlyAdapters are the built-in trackers. They read issues, not
// support conversations, so naming one under sources.helpdesk is a mistake
// worth catching at load time — each of them does serve the conversation on
// its own issues, but the wiring layer picks that view up automatically and
// there is nothing for an operator to configure separately.
var trackerOnlyAdapters = map[string]bool{"jira": true, "linear": true, "azdo": true, "rally": true}

// helpdeskOnlyAdapters are the built-in helpdesks. They read support
// conversations and have no issue list to sweep, so naming one under
// sources.tracker leaves a workspace that loads and then fails on its first
// run — worth catching at load time instead.
var helpdeskOnlyAdapters = map[string]bool{
	"zendesk":   true,
	"freshdesk": true,
	"zohodesk":  true,
	"helpscout": true,
	"intercom":  true,
	"hubspot":   true,
}

func validateSource(prefix string, s *SourceConfig, isTracker bool) error {
	if s == nil {
		return nil
	}
	if trackerOnlyAdapters[s.Adapter] && !isTracker {
		return fmt.Errorf("config: %s.adapter: %q is a tracker adapter; configure it under sources.tracker", prefix, s.Adapter)
	}
	if helpdeskOnlyAdapters[s.Adapter] && isTracker {
		return fmt.Errorf("config: %s.adapter: %q is a helpdesk adapter; configure it under sources.helpdesk", prefix, s.Adapter)
	}
	if s.HelpdeskRef != nil && !isTracker {
		return fmt.Errorf("config: %s.helpdeskRef: is only supported for sources.tracker", prefix)
	}
	// Hoisted out of the exec case so it covers every adapter: an `auth:`
	// block on a jira or zendesk source used to be read, ignored, and never
	// mentioned, which reads as "my OAuth config is live" right up until
	// the first run fails on a missing token.
	if s.Auth != nil && s.Adapter != "zohodesk" {
		return fmt.Errorf("config: %s.auth: is only supported for adapter zohodesk", prefix)
	}
	switch s.Adapter {
	case "exec":
		if s.Command == "" {
			return fmt.Errorf("config: %s.command: is required for adapter exec", prefix)
		}
	case "zohodesk":
		if s.OrgID == "" {
			return fmt.Errorf("config: %s.orgId: is required for adapter zohodesk", prefix)
		}
		if s.BaseURL == "" {
			return fmt.Errorf("config: %s.baseUrl: is required for adapter zohodesk", prefix)
		}
		switch {
		case s.Token == "" && s.Auth == nil:
			return fmt.Errorf("config: %s: one of token or auth is required for adapter zohodesk", prefix)
		case s.Token != "" && s.Auth != nil:
			return fmt.Errorf("config: %s: set token or auth, not both", prefix)
		}
		if err := validateOAuth(prefix+".auth", s.Auth); err != nil {
			return err
		}
	case "zendesk":
		if s.Subdomain == "" {
			return fmt.Errorf("config: %s.subdomain: is required for adapter zendesk", prefix)
		}
		basic := s.Email != "" || s.APIToken != ""
		bearer := s.OAuthToken != ""
		switch {
		case basic && bearer:
			return fmt.Errorf("config: %s: set email and apiToken, or oauthToken, not both", prefix)
		case basic && (s.Email == "" || s.APIToken == ""):
			return fmt.Errorf("config: %s: both email and apiToken are required for basic auth", prefix)
		case !basic && !bearer:
			return fmt.Errorf("config: %s: one of email + apiToken or oauthToken is required for adapter zendesk", prefix)
		}
	case "freshdesk":
		if s.Domain == "" {
			return fmt.Errorf("config: %s.domain: is required for adapter freshdesk", prefix)
		}
		if s.APIKey == "" {
			return fmt.Errorf("config: %s.apiKey: is required for adapter freshdesk", prefix)
		}
	case "helpscout":
		if s.ClientID == "" {
			return fmt.Errorf("config: %s.clientId: is required for adapter helpscout", prefix)
		}
		if s.ClientSecret == "" {
			return fmt.Errorf("config: %s.clientSecret: is required for adapter helpscout", prefix)
		}
	case "intercom":
		if s.AccessToken == "" {
			return fmt.Errorf("config: %s.accessToken: is required for adapter intercom", prefix)
		}
	case "hubspot":
		if s.AccessToken == "" {
			return fmt.Errorf("config: %s.accessToken: is required for adapter hubspot", prefix)
		}
	case "jira":
		if s.BaseURL == "" {
			return fmt.Errorf("config: %s.baseUrl: is required for adapter jira", prefix)
		}
		switch s.Deployment {
		case "", "auto", "cloud", "datacenter":
		default:
			return fmt.Errorf("config: %s.deployment: must be cloud, datacenter or auto, got %q", prefix, s.Deployment)
		}
		hasCloud := s.Email != "" && s.APIToken != ""
		if !hasCloud && s.PAT == "" {
			return fmt.Errorf("config: %s: adapter jira needs email and apiToken (Cloud) or pat (Data Center)", prefix)
		}
	case "linear":
		if s.APIKey == "" {
			return fmt.Errorf("config: %s.apiKey: is required for adapter linear", prefix)
		}
	case "azdo":
		if s.OrgURL == "" {
			return fmt.Errorf("config: %s.orgUrl: is required for adapter azdo", prefix)
		}
		if s.Project == "" {
			return fmt.Errorf("config: %s.project: is required for adapter azdo", prefix)
		}
		if s.PAT == "" {
			return fmt.Errorf("config: %s.pat: is required for adapter azdo", prefix)
		}
	case "rally":
		if s.APIKey == "" {
			return fmt.Errorf("config: %s.apiKey: is required for adapter rally", prefix)
		}
		if s.Workspace == "" {
			return fmt.Errorf("config: %s.workspace: is required for adapter rally", prefix)
		}
	case "":
		return fmt.Errorf("config: %s.adapter: is required", prefix)
	default:
		return fmt.Errorf("config: %s.adapter: unknown adapter %q", prefix, s.Adapter)
	}
	for _, f := range []struct{ key, ref string }{
		{"token", s.Token},
		{"apiToken", s.APIToken},
		{"pat", s.PAT},
		{"apiKey", s.APIKey},
		{"oauthToken", s.OAuthToken},
		{"clientId", s.ClientID},
		{"clientSecret", s.ClientSecret},
		{"accessToken", s.AccessToken},
	} {
		if f.ref == "" {
			continue
		}
		if err := credentialRef(prefix+"."+f.key, f.ref); err != nil {
			return err
		}
	}
	return validateHelpdeskRef(prefix+".helpdeskRef", s.HelpdeskRef)
}

// validateHelpdeskRef compiles the fallback's patterns and insists each has
// exactly one capture group. A pattern with none, or with several, has no
// single answer to "which part is the reference", and finding that out at
// load time beats finding it out halfway through a batch.
func validateHelpdeskRef(prefix string, h *HelpdeskRefConfig) error {
	if h == nil {
		return nil
	}
	if h.Pattern == "" {
		return fmt.Errorf("config: %s.pattern: is required", prefix)
	}
	for _, f := range []struct{ key, expr string }{
		{"pattern", h.Pattern},
		{"idPattern", h.IDPattern},
	} {
		if f.expr == "" {
			continue
		}
		re, err := regexp.Compile(f.expr)
		if err != nil {
			return fmt.Errorf("config: %s.%s: %w", prefix, f.key, err)
		}
		if n := re.NumSubexp(); n != 1 {
			return fmt.Errorf("config: %s.%s: must have exactly one capture group, got %d", prefix, f.key, n)
		}
	}
	return nil
}

// validateOAuth checks an auth block: all three credential references are
// required and each must name a credential rather than carry one, and the
// accounts server must be known, either derived from baseUrl by
// applyDefaults or named outright.
func validateOAuth(prefix string, a *OAuthConfig) error {
	if a == nil {
		return nil
	}
	for _, f := range []struct{ key, ref string }{
		{"clientId", a.ClientID},
		{"clientSecret", a.ClientSecret},
		{"refreshToken", a.RefreshToken},
	} {
		if f.ref == "" {
			return fmt.Errorf("config: %s.%s: is required", prefix, f.key)
		}
		if err := credentialRef(prefix+"."+f.key, f.ref); err != nil {
			return err
		}
	}
	if a.AccountsURL == "" {
		return fmt.Errorf("config: %s.accountsUrl: is required when baseUrl is not a known Zoho data centre", prefix)
	}
	return nil
}

// credentialRef rejects a value that carries a secret instead of naming one.
// The set of schemes lives in creds.go, with the resolver that reads them.
// The error names the key and the accepted prefixes only, never the value: a
// misconfigured field is as likely to hold the secret itself as a typo, and
// that must never end up in a log line or a warning.
func credentialRef(key, ref string) error {
	if IsCredentialRef(ref) {
		return nil
	}
	return fmt.Errorf("config: %s: must start with %s", key, credSchemeList)
}

// WorkspaceOnlyMCP reports whether an agent session should see only the
// workspace's own .mcp.json. It is the default, and a Config built by hand
// (in a test, say) reads as the default rather than as "off".
func (c *Config) WorkspaceOnlyMCP() bool {
	return c.MCP.WorkspaceOnly == nil || *c.MCP.WorkspaceOnly
}

// RTLMarkup reports whether the embedded note templates should wrap a
// right-to-left paragraph in a <div dir="rtl"> block. It is the default,
// and a Config built by hand (in a test, say) reads as the default rather
// than as "off".
func (c *Config) RTLMarkup() bool {
	return c.Language.RTLMarkup == nil || *c.Language.RTLMarkup
}

// NotesLanguage is the language the engineer's note is written in, or the
// default when the workspace named none.
func (c *Config) NotesLanguage() string {
	if c.Language.Notes == "" {
		return DefaultNotesLanguage
	}
	return c.Language.Notes
}

// CustomerLanguage is the language customer-facing text is written in, or
// "auto" when the workspace named none.
func (c *Config) CustomerLanguage() string {
	if c.Language.Customer == "" {
		return CustomerLanguageAuto
	}
	return c.Language.Customer
}

// AttachmentMaxBytes is the configured attachment size cap, or the default

// when the workspace did not set one.
func (c *Config) AttachmentMaxBytes() int64 {
	if c.Attachments.MaxBytes <= 0 {
		return DefaultAttachmentMaxBytes
	}
	return c.Attachments.MaxBytes
}

// MCPConfigPath is the workspace's .mcp.json when it exists and MCP
// servers are restricted to it, else "". A provider that gets a path
// starts its session against that file alone.
func (c *Config) MCPConfigPath() string {
	if !c.WorkspaceOnlyMCP() {
		return ""
	}
	path := filepath.Join(c.Root, ".mcp.json")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// ExpandPath expands a leading ~ and makes relative paths relative to c.Root.
func (c *Config) ExpandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Root, p)
}
