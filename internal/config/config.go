// Package config loads and validates a Sirdar workspace's .sirdar/config.yaml.
package config

import (
	"fmt"
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
	Adapter string       `yaml:"adapter"` // "exec" | "zohodesk" | "jira" | "linear" | "azdo" | "rally"
	Command string       `yaml:"command,omitempty"`
	OrgID   string       `yaml:"orgId,omitempty"`
	BaseURL string       `yaml:"baseUrl,omitempty"`
	Token   string       `yaml:"token,omitempty"` // credential ref
	Auth    *OAuthConfig `yaml:"auth,omitempty"`

	// Jira.
	Deployment    string `yaml:"deployment,omitempty"` // cloud | datacenter | auto
	Email         string `yaml:"email,omitempty"`      // Cloud account email, sent with apiToken
	APIToken      string `yaml:"apiToken,omitempty"`   // credential ref (Jira Cloud)
	PAT           string `yaml:"pat,omitempty"`        // credential ref (Jira Data Center, Azure DevOps)
	ProjectKey    string `yaml:"projectKey,omitempty"`
	EpicLinkField string `yaml:"epicLinkField,omitempty"`

	// Linear (APIKey is also Rally's credential).
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
// ("env:NAME" or "keychain:SERVICE"), never literal secrets.
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

// RallyDefaultBaseURL is the subscription host a rally source falls back to
// when it names none: Rally's North American production instance.
const RallyDefaultBaseURL = "https://rally1.rallydev.com"

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
	Concurrency int `yaml:"concurrency"`
	Permissions struct {
		Bash []string `yaml:"bash"`
	} `yaml:"permissions"`
	Playbooks string `yaml:"playbooks"`
	Providers struct {
		Claude struct {
			Path string `yaml:"path"`
		} `yaml:"claude"`
		Codex struct {
			Path string `yaml:"path"`
		} `yaml:"codex"`
	} `yaml:"providers"`

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
		c.Budget.MaxTurns = 60
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
	case "claude", "codex":
	default:
		return fmt.Errorf("config: provider: must be claude or codex, got %q", c.Provider)
	}
	switch c.Billing {
	case "subscription", "api":
	default:
		return fmt.Errorf("config: billing: must be subscription or api, got %q", c.Billing)
	}
	if c.Concurrency < 1 {
		return fmt.Errorf("config: concurrency: must be >= 1, got %d", c.Concurrency)
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
	if err := validateSource("sources.tracker", c.Sources.Tracker, true); err != nil {
		return err
	}
	if err := validateSource("sources.helpdesk", c.Sources.Helpdesk, false); err != nil {
		return err
	}
	return nil
}

// trackerOnlyAdapters are the built-in trackers. They read issues, not
// support conversations, so naming one under sources.helpdesk is a mistake
// worth catching at load time — each of them does serve the conversation on
// its own issues, but the wiring layer picks that view up automatically and
// there is nothing for an operator to configure separately.
var trackerOnlyAdapters = map[string]bool{"jira": true, "linear": true, "azdo": true, "rally": true}

func validateSource(prefix string, s *SourceConfig, isTracker bool) error {
	if s == nil {
		return nil
	}
	if trackerOnlyAdapters[s.Adapter] && !isTracker {
		return fmt.Errorf("config: %s.adapter: %q is a tracker adapter; configure it under sources.tracker", prefix, s.Adapter)
	}
	if s.HelpdeskRef != nil && !isTracker {
		return fmt.Errorf("config: %s.helpdeskRef: is only supported for sources.tracker", prefix)
	}
	switch s.Adapter {
	case "exec":
		if s.Command == "" {
			return fmt.Errorf("config: %s.command: is required for adapter exec", prefix)
		}
		if s.Auth != nil {
			return fmt.Errorf("config: %s.auth: is only supported for adapter zohodesk", prefix)
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
func credentialRef(key, ref string) error {
	if strings.HasPrefix(ref, "env:") || strings.HasPrefix(ref, "keychain:") {
		return nil
	}
	return fmt.Errorf("config: %s: must start with env: or keychain:, got %q", key, ref)
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
