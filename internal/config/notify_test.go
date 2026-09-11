package config

import (
	"strings"
	"testing"
)

// notifyCfg is the minimal workspace with a notify block appended.
func notifyCfg(t *testing.T, block string) (*Config, error) {
	t.Helper()
	return Load(writeCfg(t, minimal+block))
}

func TestNotifyLoads(t *testing.T) {
	c, err := notifyCfg(t, `
notify:
  on: [completed, failed]
  includeTitle: true
  slack:
    webhookUrl: env:SLACK_WEBHOOK
  teams:
    webhookUrl: keychain:sirdar-teams
  generic:
    - url: https://hooks.example.com/sirdar
      headers:
        Authorization: env:HOOK_TOKEN
        X-Env: staging
      secret: env:HOOK_SECRET
    - url: http://127.0.0.1:9000/hook
`)
	if err != nil {
		t.Fatal(err)
	}
	n := c.Notify
	if n == nil {
		t.Fatal("notify block was dropped")
	}
	if len(n.On) != 2 || !n.IncludeTitle {
		t.Fatalf("notify %+v", n)
	}
	if n.Slack.WebhookURL != "env:SLACK_WEBHOOK" || n.Teams.WebhookURL != "keychain:sirdar-teams" {
		t.Fatalf("webhooks %+v %+v", n.Slack, n.Teams)
	}
	if len(n.Generic) != 2 || n.Generic[0].Headers["X-Env"] != "staging" || n.Generic[0].Secret != "env:HOOK_SECRET" {
		t.Fatalf("generic %+v", n.Generic)
	}
}

func TestNotifyDefaultsToOff(t *testing.T) {
	c, err := Load(writeCfg(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if c.Notify != nil {
		t.Fatalf("a workspace with no notify block got %+v", c.Notify)
	}
}

func TestNotifyValidation(t *testing.T) {
	for _, tc := range []struct {
		name, block, want string
	}{
		{
			"unknown state",
			"\nnotify:\n  on: [finished]\n  slack:\n    webhookUrl: env:W\n",
			"notify.on",
		},
		{
			"slack webhook carries the URL instead of naming it",
			"\nnotify:\n  slack:\n    webhookUrl: https://hooks.slack.com/services/T/B/xoxb\n",
			"notify.slack.webhookUrl",
		},
		{
			"teams webhook carries the URL instead of naming it",
			"\nnotify:\n  teams:\n    webhookUrl: https://acme.webhook.office.com/x\n",
			"notify.teams.webhookUrl",
		},
		{
			"slack block with no webhook",
			"\nnotify:\n  slack: {}\n",
			"notify.slack.webhookUrl",
		},
		{
			"generic url is plain http off the loopback",
			"\nnotify:\n  generic:\n    - url: http://hooks.example.com/x\n",
			"must be https unless the host is loopback",
		},
		{
			"generic url is not a URL",
			"\nnotify:\n  generic:\n    - url: hooks.example.com\n",
			"must be an absolute http or https URL",
		},
		{
			"generic secret carries the secret instead of naming it",
			"\nnotify:\n  generic:\n    - url: https://hooks.example.com/x\n      secret: hunter2\n",
			"notify.generic[0].secret",
		},
		{
			"empty header value",
			"\nnotify:\n  generic:\n    - url: https://hooks.example.com/x\n      headers:\n        Authorization: \"\"\n",
			"notify.generic[0].headers.Authorization",
		},
		{
			"literal Authorization header instead of a reference",
			"\nnotify:\n  generic:\n    - url: https://hooks.example.com/x\n      headers:\n        Authorization: \"Bearer hunter2\"\n",
			"notify.generic[0].headers.Authorization",
		},
		{
			"literal header ending in -Token instead of a reference",
			"\nnotify:\n  generic:\n    - url: https://hooks.example.com/x\n      headers:\n        X-Api-Token: hunter2\n",
			"notify.generic[0].headers.X-Api-Token",
		},
		{
			"generic url carries userinfo",
			"\nnotify:\n  generic:\n    - url: https://user:hunter2@hooks.example.com/x\n",
			"notify.generic[0].url",
		},
		{
			"unknown key",
			"\nnotify:\n  channel: \"#support\"\n",
			"field channel not found",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := notifyCfg(t, tc.block)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestNotifyAcceptsLoopbackOverHTTP(t *testing.T) {
	for _, host := range []string{"http://localhost:9000/hook", "http://127.0.0.1:9000/hook", "http://[::1]:9000/hook"} {
		if err := ValidateWebhookURL("notify.generic[0].url", host); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
	for _, host := range []string{"http://10.0.0.5/hook", "ftp://example.com/hook", "", "/hook"} {
		if err := ValidateWebhookURL("notify.generic[0].url", host); err == nil {
			t.Errorf("%s was accepted", host)
		}
	}
}
