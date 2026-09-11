package config

import (
	"strings"
	"testing"
	"time"
)

const enabledHooks = minimal + `
webhooks:
  enabled: true
  cooldown: 30m
  match:
    assignee: sri@acme.com
    statuses: [Open, "In Progress"]
    labels: [support]
  sources:
    jira:
      secret: env:JIRA_HOOK_SECRET
    azdo:
      username: sirdar
      password: keychain:azdo-hook-password
`

func TestWebhooksLoad(t *testing.T) {
	c, err := Load(writeCfg(t, enabledHooks))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Webhooks.Enabled {
		t.Fatal("webhooks.enabled did not survive the load")
	}
	if got := c.WebhookCooldown(); got != 30*time.Minute {
		t.Fatalf("cooldown %s, want 30m", got)
	}
	if c.Webhooks.Sources["jira"].Secret != "env:JIRA_HOOK_SECRET" {
		t.Fatalf("jira source %+v", c.Webhooks.Sources["jira"])
	}
	if c.Webhooks.Sources["azdo"].Username != "sirdar" {
		t.Fatalf("azdo source %+v", c.Webhooks.Sources["azdo"])
	}
	if len(c.Webhooks.Match.Statuses) != 2 || c.Webhooks.Match.Labels[0] != "support" {
		t.Fatalf("match %+v", c.Webhooks.Match)
	}
}

// A workspace that says nothing about webhooks gets them off, and the
// default cooldown for whenever it turns them on.
func TestWebhooksDefaultOff(t *testing.T) {
	c, err := Load(writeCfg(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if c.Webhooks.Enabled {
		t.Fatal("webhooks are on by default")
	}
	if got := c.WebhookCooldown(); got != DefaultWebhookCooldown {
		t.Fatalf("cooldown %s, want %s", got, DefaultWebhookCooldown)
	}
}

// A cooldown written as zero means no cooldown, not "give me the default":
// the distinction needs the pointer, so it is worth a test.
func TestWebhooksZeroCooldownIsNotTheDefault(t *testing.T) {
	c, err := Load(writeCfg(t, minimal+"\nwebhooks:\n  cooldown: 0s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.WebhookCooldown(); got != 0 {
		t.Fatalf("cooldown %s, want 0", got)
	}
}

func TestWebhooksValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			"unknown source",
			"\nwebhooks:\n  enabled: true\n  sources:\n    gitlab:\n      secret: env:X\n",
			"unknown webhook source",
		},
		{
			"a secret that carries the secret",
			"\nwebhooks:\n  enabled: true\n  sources:\n    jira:\n      secret: hunter2\n",
			"must start with env:",
		},
		{
			"a source with no secret",
			"\nwebhooks:\n  enabled: true\n  sources:\n    jira: {}\n",
			"secret: is required",
		},
		{
			"azdo with no password",
			"\nwebhooks:\n  enabled: true\n  sources:\n    azdo:\n      username: sirdar\n",
			"password: is required",
		},
		{
			"azdo with no username",
			"\nwebhooks:\n  enabled: true\n  sources:\n    azdo:\n      password: env:P\n",
			"username: is required",
		},
		{
			"azdo given a secret",
			"\nwebhooks:\n  enabled: true\n  sources:\n    azdo:\n      username: u\n      password: env:P\n      secret: env:S\n",
			"authenticates with username and password",
		},
		{
			"a signed source given basic auth",
			"\nwebhooks:\n  enabled: true\n  sources:\n    linear:\n      username: u\n      password: env:P\n",
			"authenticates with a secret",
		},
		{
			"enabled with no sources",
			"\nwebhooks:\n  enabled: true\n",
			"at least one source is required",
		},
		{
			"a negative cooldown",
			"\nwebhooks:\n  cooldown: -1m\n",
			"must be >= 0",
		},
		{
			"a cooldown that is not a duration",
			"\nwebhooks:\n  cooldown: soon\n",
			"cannot unmarshal",
		},
		{
			"an unknown key under webhooks",
			"\nwebhooks:\n  enabledd: true\n",
			"enabledd",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeCfg(t, minimal+tc.body))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// What is written is checked whether or not the block is enabled, so a
// typo is caught the first time the workspace loads rather than the first
// time a hook fires.
func TestWebhooksValidatedEvenWhenDisabled(t *testing.T) {
	_, err := Load(writeCfg(t, minimal+"\nwebhooks:\n  sources:\n    jira:\n      secret: hunter2\n"))
	if err == nil || !strings.Contains(err.Error(), "must start with env:") {
		t.Fatalf("got %v", err)
	}
}

// --- proxy ------------------------------------------------------------

// The URL is half of what a HubSpot signature proves, and behind a reverse
// proxy the URL this process sees is not the one HubSpot called. The
// operator states the public address; it is not taken off the delivery.
func TestWebhookProxyLoads(t *testing.T) {
	body := minimal + `
webhooks:
  enabled: true
  sources:
    hubspot:
      secret: env:HS_SECRET
      proxy:
        scheme: https
        host: hooks.acme.com
`
	c, err := Load(writeCfg(t, body))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Webhooks.Sources["hubspot"].Proxy
	if p == nil || p.Scheme != "https" || p.Host != "hooks.acme.com" {
		t.Fatalf("proxy %+v", p)
	}
}

func TestWebhookProxyIsChecked(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block string
	}{
		{"a source that does not sign the URL it calls", `
    jira:
      secret: env:JIRA_HOOK_SECRET
      proxy:
        host: hooks.acme.com
`},
		{"a scheme that is not http or https", `
    hubspot:
      secret: env:HS_SECRET
      proxy:
        scheme: ftp
        host: hooks.acme.com
`},
		{"a host written as a URL", `
    hubspot:
      secret: env:HS_SECRET
      proxy:
        host: https://hooks.acme.com/hooks
`},
		{"an empty proxy block", `
    hubspot:
      secret: env:HS_SECRET
      proxy: {}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeCfg(t, minimal+"\nwebhooks:\n  enabled: true\n  sources:"+tc.block)); err == nil {
				t.Fatal("loaded without complaint")
			}
		})
	}
}

// "me" is the account the workspace's own credentials belong to, which is
// the only identity Sirdar can work out for itself.
func TestMatchAssigneeResolvesMe(t *testing.T) {
	body := `
workspace: demo
sources:
  tracker:
    adapter: jira
    baseUrl: https://acme.atlassian.net
    email: sri@acme.com
    apiToken: env:JIRA
webhooks:
  enabled: true
  match:
    assignee: me
  sources:
    jira:
      secret: env:JIRA_HOOK_SECRET
`
	c, err := Load(writeCfg(t, body))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.MatchAssignee()
	if err != nil {
		t.Fatal(err)
	}
	if got != "sri@acme.com" {
		t.Fatalf("assignee %q", got)
	}
}

// A Linear API key or an Azure DevOps PAT names nobody, so "me" has
// nothing to resolve to and the operator has to write the address out.
func TestMatchAssigneeMeNeedsAnAccountEmail(t *testing.T) {
	body := `
workspace: demo
sources:
  tracker:
    adapter: linear
    apiKey: env:LINEAR
webhooks:
  enabled: true
  match:
    assignee: me
  sources:
    linear:
      secret: env:LINEAR_HOOK_SECRET
`
	c, err := Load(writeCfg(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.MatchAssignee(); err == nil {
		t.Fatal("me resolved against a workspace that names no account")
	}
}

func TestMatchAssigneePassesAnAddressThrough(t *testing.T) {
	c, err := Load(writeCfg(t, enabledHooks))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.MatchAssignee()
	if err != nil {
		t.Fatal(err)
	}
	if got != "sri@acme.com" {
		t.Fatalf("assignee %q", got)
	}
}

// The scaffold has to survive its own validation, webhooks block included.
func TestDefaultConfigYAMLMentionsWebhooks(t *testing.T) {
	if !strings.Contains(DefaultConfigYAML, "webhooks:") {
		t.Fatal("the scaffold does not mention webhooks")
	}
}
