package app

import (
	"encoding/json"
	"path/filepath"
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

// The pages that read config.yaml without being able to write it get the
// resolved values, with every default spelled out and every list a list.
func TestConfigSummaryCarriesTheReadOnlyPages(t *testing.T) {
	stall := 0
	cfg := &config.Config{
		Workspace: "omni",
		Provider:  "claude",
		Model:     "sonnet",
		Billing:   "subscription",
		Root:      "/repos/omni",
	}
	cfg.Notes.Dir = "notes"
	cfg.Notes.Filenames.Triage = "{{key}}-triage.md"
	cfg.Budget.MaxTurns = 20
	cfg.Budget.MaxUSD = 2.5
	cfg.Budget.StallMinutes = &stall
	cfg.Permissions.Bash = []string{"git status*"}
	cfg.Permissions.MCP = []string{"mcp__grafana__query_*"}

	got := SummariseConfig(cfg)

	if got.General.Workspace != "omni" || got.General.Provider != "claude" || got.General.Model != "sonnet" {
		t.Fatalf("general %+v", got.General)
	}
	// The summary's paths are filesystem paths the desktop opens, built
	// with filepath.Join from cfg.Root, so they come back separated the
	// way the platform separates them. filepath.FromSlash keeps the
	// expectation spelled as a path while letting it mean the
	// backslash-separated one on Windows.
	if got.General.ConfigPath != filepath.FromSlash("/repos/omni/.sirdar/config.yaml") {
		t.Fatalf("config path %q", got.General.ConfigPath)
	}
	if got.General.NotesLanguage != "en" || got.General.CustomerLanguage != "auto" || !got.General.RTLMarkup {
		t.Fatalf("language defaults %+v", got.General)
	}
	if got.Budget.MaxTurns != 20 || got.Budget.MaxUSD != 2.5 || got.Budget.StallMinutes != 0 {
		t.Fatalf("budget %+v", got.Budget)
	}
	if got.Notes.Dir != filepath.FromSlash("/repos/omni/notes") || got.Notes.Templates != "" || got.Notes.Filenames.Triage != "{{key}}-triage.md" {
		t.Fatalf("notes %+v", got.Notes)
	}
	if !got.MCP.WorkspaceOnly {
		t.Fatalf("mcp %+v", got.MCP)
	}
	if strings.Join(got.Permissions.Bash, ",") != "git status*" || strings.Join(got.Permissions.MCP, ",") != "mcp__grafana__query_*" {
		t.Fatalf("permissions %+v", got.Permissions)
	}
	// Unset lists are empty lists: the frontend maps over every one.
	if got.Permissions.Fetch == nil || got.Permissions.ReadAlso == nil || got.Permissions.FixBash == nil {
		t.Fatalf("a nil list reached the summary: %+v", got.Permissions)
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

func TestConfigSummaryCarriesTheReadersIdentity(t *testing.T) {
	cfg := &config.Config{}
	cfg.Me = config.MeConfig{
		Email:   "srivathsan.v@silq.net",
		Names:   []string{"Srivathsan V"},
		Aliases: []string{"sriv"},
	}
	got := SummariseConfig(cfg).Me
	if got.Email != "srivathsan.v@silq.net" || got.Source != config.IdentityFromMe {
		t.Fatalf("me %+v", got)
	}
	if strings.Join(got.Names, ",") != "Srivathsan V,sriv" {
		t.Fatalf("names %v", got.Names)
	}

	// A workspace that can name nobody says so with an empty source, and
	// still sends a list the frontend can map over.
	none := SummariseConfig(&config.Config{}).Me
	if none.Email != "" || none.Source != "" {
		t.Fatalf("nobody: %+v", none)
	}
	if none.Names == nil {
		t.Fatal("a nil name list reached the summary")
	}
}

func TestConfigSummaryNeedsAKnownWorkspace(t *testing.T) {
	svc := newService(t, newWorkspace(t), stubBuilder(nil, nil, nil))
	if _, err := svc.ConfigSummary("nosuch"); err == nil {
		t.Fatal("an unknown workspace: want an error")
	}
}
