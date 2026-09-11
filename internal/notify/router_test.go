package notify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// captured is a Notifier that records what it was asked to post.
type captured struct{ events []Event }

func (c *captured) Notify(_ context.Context, ev Event) error {
	c.events = append(c.events, ev)
	return nil
}

func TestRouterFiltersByState(t *testing.T) {
	c := &captured{}
	r := &Router{Notifier: c, On: []string{"failed", "over_budget"}}
	for _, status := range []string{"completed", "blocked", "failed", "over_budget"} {
		ev := sampleEvent()
		ev.Status = status
		if err := r.Notify(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.events) != 2 || c.events[0].Status != "failed" || c.events[1].Status != "over_budget" {
		t.Fatalf("posted %+v", c.events)
	}
}

func TestRouterDefaultsToEveryTerminalState(t *testing.T) {
	c := &captured{}
	r := &Router{Notifier: c}
	for _, status := range DefaultOn {
		ev := sampleEvent()
		ev.Status = status
		if err := r.Notify(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.events) != len(DefaultOn) {
		t.Fatalf("posted %d of %d", len(c.events), len(DefaultOn))
	}
}

// TestRouterStripsTheTitleByDefault: a ticket subject names the customer,
// so it reaches the channel only when the workspace asked for it.
func TestRouterStripsTheTitleByDefault(t *testing.T) {
	c := &captured{}
	if err := (&Router{Notifier: c}).Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatal(err)
	}
	if c.events[0].Title != "" {
		t.Fatalf("title reached the destination: %q", c.events[0].Title)
	}
	if err := (&Router{Notifier: c, IncludeTitle: true}).Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatal(err)
	}
	if c.events[1].Title != sampleEvent().Title {
		t.Fatalf("title %q", c.events[1].Title)
	}
}

func TestNilRouterNotifiesNothing(t *testing.T) {
	var r *Router
	if err := r.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatal(err)
	}
}

func resolverFor(env map[string]string) config.Resolver {
	return config.Resolver{Env: func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}}
}

func TestFromConfigBuildsEveryDestination(t *testing.T) {
	cfg := &config.Config{Workspace: "oxo"}
	cfg.Notify = &config.NotifyConfig{
		On:           []string{"completed"},
		IncludeTitle: true,
		Slack:        &config.WebhookConfig{WebhookURL: "env:SLACK_WEBHOOK"},
		Teams:        &config.WebhookConfig{WebhookURL: "env:TEAMS_WEBHOOK"},
		Generic: []config.GenericHook{{
			URL:     "https://hooks.example.com/sirdar",
			Headers: map[string]string{"Authorization": "env:HOOK_TOKEN", "X-Env": "staging"},
			Secret:  "env:HOOK_SECRET",
		}},
	}
	creds := resolverFor(map[string]string{
		"SLACK_WEBHOOK": "https://hooks.slack.com/services/T/B/xoxb",
		"TEAMS_WEBHOOK": "https://acme.webhook.office.com/webhookb2/abc",
		"HOOK_TOKEN":    "Bearer token-123",
		"HOOK_SECRET":   "s3cret",
	})

	n, err := FromConfig(cfg, creds)
	if err != nil {
		t.Fatal(err)
	}
	router, ok := n.(*Router)
	if !ok {
		t.Fatalf("built %T", n)
	}
	if !router.IncludeTitle || len(router.On) != 1 || router.On[0] != "completed" {
		t.Fatalf("router %+v", router)
	}
	dests, ok := router.Notifier.(Multi)
	if !ok || len(dests) != 3 {
		t.Fatalf("destinations %T %v", router.Notifier, router.Notifier)
	}
	if dests[0].(*Slack).WebhookURL != "https://hooks.slack.com/services/T/B/xoxb" {
		t.Errorf("slack %+v", dests[0])
	}
	if dests[1].(*Teams).WebhookURL != "https://acme.webhook.office.com/webhookb2/abc" {
		t.Errorf("teams %+v", dests[1])
	}
	g := dests[2].(*Generic)
	if g.Secret != "s3cret" || g.Headers["Authorization"] != "Bearer token-123" || g.Headers["X-Env"] != "staging" {
		t.Errorf("generic %+v", g)
	}
}

func TestFromConfigWithoutANotifyBlock(t *testing.T) {
	n, err := FromConfig(&config.Config{}, config.Resolver{})
	if n != nil || err != nil {
		t.Fatalf("%v %v", n, err)
	}
}

func TestFromConfigHonoursTheKillSwitch(t *testing.T) {
	t.Setenv(DisableEnv, "1")
	cfg := &config.Config{}
	cfg.Notify = &config.NotifyConfig{Slack: &config.WebhookConfig{WebhookURL: "env:SLACK_WEBHOOK"}}
	n, err := FromConfig(cfg, resolverFor(map[string]string{"SLACK_WEBHOOK": "https://hooks.slack.com/x"}))
	if n != nil || err != nil {
		t.Fatalf("SIRDAR_NO_NOTIFY was ignored: %v %v", n, err)
	}
}

// TestFromConfigRejectsAPlaintextWebhook: the ref resolved to something no
// run's metadata should be posted to, and the error must not quote it.
func TestFromConfigRejectsAPlaintextWebhook(t *testing.T) {
	cfg := &config.Config{}
	cfg.Notify = &config.NotifyConfig{Slack: &config.WebhookConfig{WebhookURL: "env:SLACK_WEBHOOK"}}
	_, err := FromConfig(cfg, resolverFor(map[string]string{"SLACK_WEBHOOK": "http://evil.example/xoxb-SECRET"}))
	if err == nil {
		t.Fatal("a plaintext webhook was accepted")
	}
	if strings.Contains(err.Error(), "xoxb-SECRET") {
		t.Fatalf("the error quoted the credential: %v", err)
	}
}

func TestFromConfigReportsAMissingCredential(t *testing.T) {
	cfg := &config.Config{}
	cfg.Notify = &config.NotifyConfig{Teams: &config.WebhookConfig{WebhookURL: "env:TEAMS_WEBHOOK"}}
	_, err := FromConfig(cfg, resolverFor(nil))
	if err == nil || !strings.Contains(err.Error(), "notify.teams.webhookUrl") {
		t.Fatalf("error %v", err)
	}
}

// TestEventCarriesNoNoteText is the guarantee the package exists to keep:
// an Event has no field a note's body could travel in.
func TestEventCarriesNoNoteText(t *testing.T) {
	raw, err := json.Marshal(sampleEvent())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"kind": true, "key": true, "title": true, "status": true, "confidence": true,
		"classification": true, "service": true, "runId": true, "notePath": true,
		"trackerUrl": true, "helpdeskUrl": true, "turns": true, "costUsd": true,
		"minutes": true, "reason": true, "workspace": true,
	}
	for name := range m {
		if !allowed[name] {
			t.Errorf("the event carries an unexpected field %q", name)
		}
	}
}
