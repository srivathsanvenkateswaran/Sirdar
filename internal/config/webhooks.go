package config

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/webhooks"
)

// DefaultWebhookCooldown is how long a key stays untouchable after a
// triage of it finished. A tracker that fires on every field change will
// send the same ticket several times a minute while somebody is editing
// it, and each delivery would otherwise be a fresh agent session.
const DefaultWebhookCooldown = 10 * time.Minute

// WebhooksConfig is the webhooks block of a workspace configuration.
type WebhooksConfig struct {
	// Enabled decides whether `sirdar serve` registers the hook routes at
	// all. With it false there is no /hooks endpoint to find, which is
	// the state every workspace starts in.
	Enabled bool `yaml:"enabled"`
	// Sources maps a source name — jira, linear, azdo, rally, zendesk,
	// freshdesk, intercom, hubspot, generic — to its credentials. A
	// source that is not listed is not served.
	Sources map[string]*WebhookSource `yaml:"sources,omitempty"`
	// Match narrows which of the deliveries that verify are worth a run.
	Match WebhookMatch `yaml:"match,omitempty"`
	// Cooldown is how recently a key may have been triaged before a
	// delivery naming it is skipped. Nil means DefaultWebhookCooldown;
	// zero means no cooldown at all.
	Cooldown *time.Duration `yaml:"cooldown,omitempty"`
}

// WebhookSource is one source's credentials. Secret and Password are
// credential references ("env:NAME" or "keychain:SERVICE"), never the
// secret itself; Username is not a secret and is written literally.
type WebhookSource struct {
	Secret   string `yaml:"secret,omitempty"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

// WebhookMatch is the filter a verified delivery has to pass.
//
// Assignee is "me" — the account the workspace's own tracker or helpdesk
// credentials belong to — or an address or account id written out. It is
// the filter that matters: a tracker fires on every change to every
// ticket, and this is what makes a hook mean "something landed on my
// plate" rather than "something happened".
type WebhookMatch struct {
	Assignee string   `yaml:"assignee,omitempty"`
	Statuses []string `yaml:"statuses,omitempty"`
	Labels   []string `yaml:"labels,omitempty"`
}

// SelfAssignee is the value webhooks.match.assignee takes to mean the
// account this workspace is already configured with.
const SelfAssignee = "me"

// WebhookCooldown is the configured cooldown, or the default when the
// workspace named none.
func (c *Config) WebhookCooldown() time.Duration {
	if c.Webhooks.Cooldown == nil {
		return DefaultWebhookCooldown
	}
	return *c.Webhooks.Cooldown
}

// SelfIdentity is the address the workspace's own credentials belong to:
// the tracker's account email, else the helpdesk's. It is what
// webhooks.match.assignee: me resolves to, and it is empty for a workspace
// whose sources authenticate with a token that names nobody — a Linear API
// key, an Azure DevOps PAT — in which case the operator has to write the
// address out.
func (c *Config) SelfIdentity() string {
	for _, s := range []*SourceConfig{c.Sources.Tracker, c.Sources.Helpdesk} {
		if s != nil && s.Email != "" {
			return s.Email
		}
	}
	return ""
}

// MatchAssignee is webhooks.match.assignee with "me" resolved. It errors
// when the workspace asked for "me" and nothing says who that is.
func (c *Config) MatchAssignee() (string, error) {
	want := strings.TrimSpace(c.Webhooks.Match.Assignee)
	if !strings.EqualFold(want, SelfAssignee) {
		return want, nil
	}
	if self := c.SelfIdentity(); self != "" {
		return self, nil
	}
	return "", fmt.Errorf("config: webhooks.match.assignee: %q needs an account email on sources.tracker or sources.helpdesk; write the address out instead", SelfAssignee)
}

// validateWebhooks checks the webhooks block. What is written is checked
// whether or not the block is enabled, so a typo is caught the first time
// the workspace loads rather than the first time a hook fires; only the
// "there has to be at least one source" rule waits for enabled.
func validateWebhooks(w *WebhooksConfig) error {
	if w.Cooldown != nil && *w.Cooldown < 0 {
		return fmt.Errorf("config: webhooks.cooldown: must be >= 0, got %s", *w.Cooldown)
	}
	if w.Enabled && len(w.Sources) == 0 {
		return fmt.Errorf("config: webhooks.sources: at least one source is required when webhooks.enabled is true")
	}
	names := make([]string, 0, len(w.Sources))
	for name := range w.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := validateWebhookSource(name, w.Sources[name]); err != nil {
			return err
		}
	}
	return nil
}

func validateWebhookSource(name string, s *WebhookSource) error {
	prefix := "webhooks.sources." + name
	if !webhooks.Known(name) {
		return fmt.Errorf("config: %s: unknown webhook source; one of %s", prefix, strings.Join(webhooks.KnownSources(), ", "))
	}
	if s == nil {
		return fmt.Errorf("config: %s: is empty", prefix)
	}
	if webhooks.UsesBasicAuth(name) {
		if s.Secret != "" {
			return fmt.Errorf("config: %s.secret: %s authenticates with username and password, not a secret", prefix, name)
		}
		if s.Username == "" {
			return fmt.Errorf("config: %s.username: is required for %s", prefix, name)
		}
		if s.Password == "" {
			return fmt.Errorf("config: %s.password: is required for %s", prefix, name)
		}
		return credentialRef(prefix+".password", s.Password)
	}
	if s.Username != "" || s.Password != "" {
		return fmt.Errorf("config: %s: %s authenticates with a secret, not username and password", prefix, name)
	}
	if s.Secret == "" {
		return fmt.Errorf("config: %s.secret: is required for %s", prefix, name)
	}
	return credentialRef(prefix+".secret", s.Secret)
}
