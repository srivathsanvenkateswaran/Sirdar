package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// The secrets the summary must never repeat, in the shapes a workspace
// writes them in: the reference, and what a reference resolves to.
const (
	slackRef  = "env:SIRDAR_SLACK_WEBHOOK"
	hookRef   = "keychain:sirdar-hook"
	secretRef = "env:JIRA_HOOK_SECRET"
)

func summaryFixture() *config.Config {
	cooldown := 5 * time.Minute
	cfg := &config.Config{
		Notify: &config.NotifyConfig{
			On:    []string{"completed", "failed"},
			Slack: &config.WebhookConfig{WebhookURL: slackRef},
			Generic: []config.GenericHook{{
				URL:     "https://hooks.example.com/sirdar/t/9f3b-secret-path?token=abcdef",
				Headers: map[string]string{"X-Api-Key": "env:RECEIVER_KEY", "X-Team": "support"},
				Secret:  hookRef,
			}},
		},
	}
	cfg.Webhooks = config.WebhooksConfig{
		Enabled:  true,
		Cooldown: &cooldown,
		Match:    config.WebhookMatch{Assignee: "me", Statuses: []string{"Open"}},
		Sources: map[string]*config.WebhookSource{
			"jira":    {Secret: secretRef},
			"hubspot": {Secret: "file:/etc/sirdar/hubspot", Proxy: &config.WebhookProxy{Scheme: "https", Host: "hooks.example.com"}},
		},
	}
	return cfg
}

func TestConfigSummaryReportsShapeNotSecrets(t *testing.T) {
	got := SummariseConfig(summaryFixture())

	if !got.Notify.Enabled || len(got.Notify.Destinations) != 2 {
		t.Fatalf("notify %+v", got.Notify)
	}
	slack := got.Notify.Destinations[0]
	if slack.Type != "slack" || slack.Credential != "env" {
		t.Fatalf("slack destination %+v", slack)
	}
	generic := got.Notify.Destinations[1]
	if generic.Type != "generic" || generic.Target != "https://hooks.example.com" {
		t.Fatalf("generic destination %+v", generic)
	}
	if !generic.Signed || generic.Credential != "keychain" {
		t.Fatalf("generic signing %+v", generic)
	}
	// Header names say what the receiver expects; the values are where a
	// token goes, so only the names cross.
	if strings.Join(generic.Headers, ",") != "X-Api-Key,X-Team" {
		t.Fatalf("headers %v", generic.Headers)
	}

	if !got.Webhooks.Enabled || got.Webhooks.Cooldown != "5m0s" {
		t.Fatalf("webhooks %+v", got.Webhooks)
	}
	if got.Webhooks.Match.Assignee != "me" {
		t.Fatalf("match %+v", got.Webhooks.Match)
	}
	if len(got.Webhooks.Sources) != 2 {
		t.Fatalf("sources %+v", got.Webhooks.Sources)
	}
	// Sorted by name, so the screen reads the same way twice.
	if got.Webhooks.Sources[0].Name != "hubspot" || got.Webhooks.Sources[1].Name != "jira" {
		t.Fatalf("sources %+v", got.Webhooks.Sources)
	}
	if got.Webhooks.Sources[0].Credential != "file" || got.Webhooks.Sources[0].Proxy != "https://hooks.example.com" {
		t.Fatalf("hubspot %+v", got.Webhooks.Sources[0])
	}
	if got.Webhooks.Sources[1].Auth != "secret" || got.Webhooks.Sources[1].Credential != "env" {
		t.Fatalf("jira %+v", got.Webhooks.Sources[1])
	}

	// The whole serialised summary is what crosses to the UI: no
	// reference's name, and nothing that could be read as one.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		slackRef, hookRef, secretRef,
		"SIRDAR_SLACK_WEBHOOK", "JIRA_HOOK_SECRET", "RECEIVER_KEY",
		"/etc/sirdar/hubspot", "9f3b-secret-path", "token=abcdef",
	} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("the summary carries %q: %s", forbidden, data)
		}
	}
}

// A workspace that configures neither block reports both as off rather
// than as missing, so the screen has something to say.
func TestConfigSummaryWithNothingConfigured(t *testing.T) {
	got := SummariseConfig(&config.Config{})

	if got.Notify.Enabled {
		t.Error("a workspace with no notify block posts nothing")
	}
	if got.Webhooks.Enabled {
		t.Error("a workspace with no webhooks block serves none")
	}
	if got.Webhooks.Cooldown != config.DefaultWebhookCooldown.String() {
		t.Errorf("cooldown %q, want the default", got.Webhooks.Cooldown)
	}
	// Empty lists, not nulls: the frontend maps over them.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("the summary carries a null: %s", data)
	}
}

// notify.on defaulting to every terminal state is a fact about the
// workspace, so the summary says which states rather than leaving a blank.
func TestConfigSummarySpellsOutTheDefaultStates(t *testing.T) {
	got := SummariseConfig(&config.Config{Notify: &config.NotifyConfig{}})

	if strings.Join(got.Notify.On, ",") != "blocked,completed,failed,over_budget" {
		t.Fatalf("on %v", got.Notify.On)
	}
}

func TestConfigSummaryNeedsAKnownWorkspace(t *testing.T) {
	svc := newService(t, newWorkspace(t), stubBuilder(nil, nil, nil))
	if _, err := svc.ConfigSummary("nosuch"); err == nil {
		t.Fatal("an unknown workspace: want an error")
	}
}
