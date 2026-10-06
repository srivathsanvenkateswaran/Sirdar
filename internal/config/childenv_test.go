package config

import (
	"slices"
	"testing"
)

// TestChildEnvStripsCredentials: every "env:" credential reference the
// workspace configures is taken out of the environment, everything else
// is kept, and SIRDAR_BILLING is added, as a run's own child environment
// does.
func TestChildEnvStripsCredentials(t *testing.T) {
	c := &Config{Billing: "subscription"}
	c.Sources.Helpdesk = &SourceConfig{Token: "env:DESK_TOKEN", Auth: &OAuthConfig{RefreshToken: "env:DESK_REFRESH"}}
	c.Sources.Tracker = &SourceConfig{APIToken: "env:TRACKER_TOKEN"}
	c.Notify = &NotifyConfig{Slack: &WebhookConfig{WebhookURL: "env:SLACK_HOOK"}}

	got := c.ChildEnv([]string{"PATH=/bin", "DESK_TOKEN=a", "DESK_REFRESH=b", "TRACKER_TOKEN=c", "SLACK_HOOK=d", "HOME=/h"})
	want := []string{"PATH=/bin", "HOME=/h", "SIRDAR_BILLING=subscription"}
	if !slices.Equal(got, want) {
		t.Fatalf("ChildEnv = %q, want %q", got, want)
	}
}
