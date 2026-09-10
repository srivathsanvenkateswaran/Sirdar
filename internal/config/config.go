// Package config loads and validates a Sirdar workspace's .sirdar/config.yaml.
package config

import (
	"fmt"
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
	Adapter string `yaml:"adapter"` // "exec" | "zohodesk"
	Command string `yaml:"command,omitempty"`
	OrgID   string `yaml:"orgId,omitempty"`
	BaseURL string `yaml:"baseUrl,omitempty"`
	Token   string `yaml:"token,omitempty"` // credential ref
}

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
	case "zohodesk":
		if s.OrgID == "" {
			return fmt.Errorf("config: %s.orgId: is required for adapter zohodesk", prefix)
		}
		if s.BaseURL == "" {
			return fmt.Errorf("config: %s.baseUrl: is required for adapter zohodesk", prefix)
		}
		if s.Token == "" {
			return fmt.Errorf("config: %s.token: is required for adapter zohodesk", prefix)
		}
	case "":
		return fmt.Errorf("config: %s.adapter: is required", prefix)
	default:
		return fmt.Errorf("config: %s.adapter: unknown adapter %q", prefix, s.Adapter)
	}
	if s.Token != "" && !strings.HasPrefix(s.Token, "env:") && !strings.HasPrefix(s.Token, "keychain:") {
		return fmt.Errorf("config: %s.token: must start with env: or keychain:, got %q", prefix, s.Token)
	}
	return nil
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
