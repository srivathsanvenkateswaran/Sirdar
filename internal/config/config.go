// Package config loads and validates a Sirdar workspace's .sirdar/config.yaml.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	Adapter string       `yaml:"adapter"` // "exec" | "zohodesk"
	Command string       `yaml:"command,omitempty"`
	OrgID   string       `yaml:"orgId,omitempty"`
	BaseURL string       `yaml:"baseUrl,omitempty"`
	Token   string       `yaml:"token,omitempty"` // credential ref
	Auth    *OAuthConfig `yaml:"auth,omitempty"`
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

// DefaultAttachmentMaxBytes is the size above which a downloaded
// attachment is dropped from the bundle. A 17 MB screen recording is 99%
// of a bundle by bytes and none of it by evidence: the session cannot open
// it, so it is named in a warning instead.
const DefaultAttachmentMaxBytes = 10 << 20

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
	if c.MCP.WorkspaceOnly == nil {
		yes := true
		c.MCP.WorkspaceOnly = &yes
	}
	if c.Attachments.MaxBytes == 0 {
		c.Attachments.MaxBytes = DefaultAttachmentMaxBytes
	}
	for _, s := range []*SourceConfig{c.Sources.Tracker, c.Sources.Helpdesk} {
		if s != nil && s.Auth != nil && s.Auth.AccountsURL == "" {
			s.Auth.AccountsURL = AccountsURLFor(s.BaseURL)
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
	if c.Attachments.MaxBytes < 0 {
		return fmt.Errorf("config: attachments.maxBytes: must be >= 0, got %d", c.Attachments.MaxBytes)
	}
	if err := validateSource("sources.tracker", c.Sources.Tracker); err != nil {
		return err
	}
	if err := validateSource("sources.helpdesk", c.Sources.Helpdesk); err != nil {
		return err
	}
	return nil
}

func validateSource(prefix string, s *SourceConfig) error {
	if s == nil {
		return nil
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
	case "":
		return fmt.Errorf("config: %s.adapter: is required", prefix)
	default:
		return fmt.Errorf("config: %s.adapter: unknown adapter %q", prefix, s.Adapter)
	}
	if s.Token != "" {
		return credentialRef(prefix+".token", s.Token)
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

// WorkspaceOnlyMCP reports whether an agent session should see only the
// workspace's own .mcp.json. It is the default, and a Config built by hand
// (in a test, say) reads as the default rather than as "off".
func (c *Config) WorkspaceOnlyMCP() bool {
	return c.MCP.WorkspaceOnly == nil || *c.MCP.WorkspaceOnly
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
