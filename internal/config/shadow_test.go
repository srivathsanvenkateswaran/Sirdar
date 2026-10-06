package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// shadowBase is a minimal workspace that also has a tracker, so a shadow
// block that turns itself on can load.
const shadowBase = `
workspace: demo
sources:
  tracker:
    adapter: jira
    baseUrl: https://jira.acme.internal
    deployment: datacenter
    pat: env:JIRA_PAT
`

func shadowCfg(t *testing.T, block string) (*Config, error) {
	t.Helper()
	return Load(writeCfg(t, shadowBase+block))
}

func TestShadowDefaults(t *testing.T) {
	c, err := Load(writeCfg(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	s := c.Shadow
	if s.Enabled {
		t.Error("shadow must default to off")
	}
	if s.Interval != "15m" || c.ShadowInterval() != 15*time.Minute {
		t.Errorf("interval %q / %v", s.Interval, c.ShadowInterval())
	}
	if s.Assignee != "me" {
		t.Errorf("assignee %q", s.Assignee)
	}
	if s.Since != "armed" {
		t.Errorf("since %q", s.Since)
	}
	if since, armed := c.ShadowSince(); !armed || !since.IsZero() {
		t.Errorf("ShadowSince = %v, %v; want zero, armed", since, armed)
	}
	if s.MaxPerDay != 10 || s.MaxUSDPerDay != 10 || s.Concurrency != 1 {
		t.Errorf("caps %d / %v / %d", s.MaxPerDay, s.MaxUSDPerDay, s.Concurrency)
	}
	if s.PauseAtQuota == nil || *s.PauseAtQuota != 0.85 || c.ShadowPauseAtQuota() != 0.85 {
		t.Errorf("pauseAtQuota %v / %v", s.PauseAtQuota, c.ShadowPauseAtQuota())
	}
	if s.YieldToOperator == nil || !*s.YieldToOperator || !c.ShadowYield() {
		t.Error("yieldToOperator must default to true")
	}
	if s.Reproduce == nil || !*s.Reproduce || !c.ShadowReproduce() {
		t.Error("reproduce must default to true")
	}
	if s.Outcomes.Interval != "6h" || c.ShadowOutcomesInterval() != 6*time.Hour {
		t.Errorf("outcomes.interval %q / %v", s.Outcomes.Interval, c.ShadowOutcomesInterval())
	}
	if s.Outcomes.WindowDays != 30 {
		t.Errorf("outcomes.windowDays %d", s.Outcomes.WindowDays)
	}
	if s.Outcomes.Tracker == nil || !*s.Outcomes.Tracker || !c.ShadowOutcomesTracker() {
		t.Error("outcomes.tracker must default to true")
	}
	if s.Digest.Enabled || s.Digest.At != "09:00" || s.Digest.WebhookURL != "" {
		t.Errorf("digest %+v", s.Digest)
	}
	if h, m := c.ShadowDigestAt(); h != 9 || m != 0 {
		t.Errorf("ShadowDigestAt = %d:%d", h, m)
	}
	if s.Types != nil {
		t.Errorf("types must stay absent, got %v", *s.Types)
	}
	if !reflect.DeepEqual(c.ShadowTypes(), c.QueueTypes()) {
		t.Errorf("ShadowTypes %v, QueueTypes %v", c.ShadowTypes(), c.QueueTypes())
	}
	if c.ShadowModel() != "" {
		t.Errorf("ShadowModel %q", c.ShadowModel())
	}
	if c.ShadowJudgeModel() != c.Model {
		t.Errorf("ShadowJudgeModel %q, Model %q", c.ShadowJudgeModel(), c.Model)
	}
}

func TestShadowNilConfig(t *testing.T) {
	var c *Config
	if c.ShadowInterval() != 15*time.Minute || c.ShadowOutcomesInterval() != 6*time.Hour {
		t.Error("a nil config must read the default intervals")
	}
	if c.ShadowPauseAtQuota() != 0.85 || !c.ShadowYield() || !c.ShadowReproduce() || !c.ShadowOutcomesTracker() {
		t.Error("a nil config must read the defaults")
	}
	if _, armed := c.ShadowSince(); !armed {
		t.Error("a nil config must read since: armed")
	}
	if h, m := c.ShadowDigestAt(); h != 9 || m != 0 {
		t.Errorf("ShadowDigestAt = %d:%d", h, m)
	}
	if !reflect.DeepEqual(c.ShadowTypes(), []string{"bug"}) {
		t.Errorf("ShadowTypes %v", c.ShadowTypes())
	}
	if c.ShadowModel() != "" || c.ShadowJudgeModel() != "" {
		t.Error("a nil config names no model")
	}
}

func TestShadowTypesFallBack(t *testing.T) {
	c, err := shadowCfg(t, "    queue:\n      types: [bug, incident]\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ShadowTypes(); !reflect.DeepEqual(got, []string{"bug", "incident"}) {
		t.Fatalf("ShadowTypes = %v, want [bug incident]", got)
	}
}

func TestShadowAccessors(t *testing.T) {
	c, err := shadowCfg(t, `
model: workspace-model
shadow:
  enabled: true
  types: [Defect, Incident]
  model: "  shadow-model "
  since: 2026-10-01
  pauseAtQuota: 0
  yieldToOperator: false
  reproduce: false
  outcomes:
    interval: 7d
    tracker: false
  judge:
    model: " judge-model "
  digest:
    at: "17:45"
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ShadowTypes(); !reflect.DeepEqual(got, []string{"bug", "incident"}) {
		t.Errorf("ShadowTypes = %v", got)
	}
	if c.ShadowModel() != "shadow-model" || c.ShadowJudgeModel() != "judge-model" {
		t.Errorf("models %q / %q", c.ShadowModel(), c.ShadowJudgeModel())
	}
	since, armed := c.ShadowSince()
	if armed || since.Year() != 2026 || since.Month() != time.October || since.Day() != 1 {
		t.Errorf("ShadowSince = %v, %v", since, armed)
	}
	if c.ShadowPauseAtQuota() != 0 || c.ShadowYield() || c.ShadowReproduce() || c.ShadowOutcomesTracker() {
		t.Error("explicit false and zero values must survive the defaults")
	}
	if c.ShadowOutcomesInterval() != 168*time.Hour {
		t.Errorf("outcomes interval %v", c.ShadowOutcomesInterval())
	}
	if h, m := c.ShadowDigestAt(); h != 17 || m != 45 {
		t.Errorf("ShadowDigestAt = %d:%d", h, m)
	}

	c2, err := shadowCfg(t, "shadow:\n  judge: {model: \"\"}\nmodel: ws\n")
	if err != nil {
		t.Fatal(err)
	}
	if c2.ShadowJudgeModel() != "ws" {
		t.Errorf("an empty judge model must fall back to the workspace model, got %q", c2.ShadowJudgeModel())
	}

	c3, err := shadowCfg(t, "shadow:\n  since: \"2026-10-01T08:30:00Z\"\n")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC)
	if since, armed := c3.ShadowSince(); armed || !since.Equal(want) {
		t.Errorf("ShadowSince = %v, %v; want %v", since, armed, want)
	}
}

func TestShadowValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		base  string // shadowBase when empty
		block string
		want  string // "" means the config must load
	}{
		{name: "enabled without a tracker", base: minimal, block: "shadow:\n  enabled: true\n",
			want: "config: shadow.enabled: requires sources.tracker"},
		{name: "enabled with a tracker", block: "shadow:\n  enabled: true\n"},

		{name: "interval below 5m", block: "shadow:\n  interval: 4m\n",
			want: `config: shadow.interval: must be a duration from 5m to 24h, got "4m"`},
		{name: "interval above 24h", block: "shadow:\n  interval: 25h\n",
			want: `config: shadow.interval: must be a duration from 5m to 24h, got "25h"`},
		{name: "interval not a duration", block: "shadow:\n  interval: soon\n",
			want: `config: shadow.interval: must be a duration from 5m to 24h, got "soon"`},
		{name: "interval 5m", block: "shadow:\n  interval: 5m\n"},
		{name: "interval 24h", block: "shadow:\n  interval: 24h\n"},
		{name: "interval 1d", block: "shadow:\n  interval: 1d\n"},

		{name: "assignee blank", block: "shadow:\n  assignee: \"  \"\n",
			want: "config: shadow.assignee: must not be empty"},
		{name: "assignee named", block: "shadow:\n  assignee: someone@acme.com\n"},

		{name: "types empty", block: "shadow:\n  types: []\n",
			want: `config: shadow.types: must name at least one type (or "*")`},
		{name: "types only blanks", block: "shadow:\n  types: [\" \", \"\"]\n",
			want: `config: shadow.types: must name at least one type (or "*")`},
		{name: "types wildcard", block: "shadow:\n  types: [\"*\"]\n"},
		{name: "types one", block: "shadow:\n  types: [bug]\n"},

		{name: "statuses blank entry", block: "shadow:\n  statuses: [Open, \" \"]\n",
			want: "config: shadow.statuses: entry 1 is empty"},
		{name: "statuses named", block: "shadow:\n  statuses: [Open, In Progress]\n"},

		{name: "since nonsense", block: "shadow:\n  since: yesterday\n",
			want: `config: shadow.since: must be armed, a date (2006-01-02) or an RFC 3339 time, got "yesterday"`},
		{name: "since date", block: "shadow:\n  since: 2026-10-01\n"},
		{name: "since rfc3339", block: "shadow:\n  since: \"2026-10-01T08:30:00+04:00\"\n"},
		{name: "since armed", block: "shadow:\n  since: armed\n"},

		{name: "maxPerDay above 100", block: "shadow:\n  maxPerDay: 101\n",
			want: "config: shadow.maxPerDay: must be from 1 to 100, got 101"},
		{name: "maxPerDay negative", block: "shadow:\n  maxPerDay: -1\n",
			want: "config: shadow.maxPerDay: must be from 1 to 100, got -1"},
		{name: "maxPerDay 1", block: "shadow:\n  maxPerDay: 1\n"},
		{name: "maxPerDay 100", block: "shadow:\n  maxPerDay: 100\n"},

		{name: "maxUSDPerDay above 500", block: "shadow:\n  maxUSDPerDay: 500.5\n",
			want: "config: shadow.maxUSDPerDay: must be > 0 and <= 500, got 500.5"},
		{name: "maxUSDPerDay negative", block: "shadow:\n  maxUSDPerDay: -1\n",
			want: "config: shadow.maxUSDPerDay: must be > 0 and <= 500, got -1"},
		{name: "maxUSDPerDay 500", block: "shadow:\n  maxUSDPerDay: 500\n"},
		{name: "maxUSDPerDay 0.01", block: "shadow:\n  maxUSDPerDay: 0.01\n"},
		{name: "maxUSDPerDay below concurrency x budget", block: "budget:\n  maxUsd: 5\nshadow:\n  concurrency: 3\n  maxUSDPerDay: 14\n",
			want: "config: shadow.maxUSDPerDay: must be at least concurrency × budget.maxUsd (15) when concurrency > 1"},
		{name: "maxUSDPerDay equal to concurrency x budget", block: "budget:\n  maxUsd: 5\nshadow:\n  concurrency: 3\n  maxUSDPerDay: 15\n"},
		{name: "maxUSDPerDay under budget at concurrency 1", block: "budget:\n  maxUsd: 5\nshadow:\n  concurrency: 1\n  maxUSDPerDay: 2\n"},

		{name: "concurrency above 4", block: "shadow:\n  concurrency: 5\n  maxUSDPerDay: 100\n",
			want: "config: shadow.concurrency: must be from 1 to 4, got 5"},
		{name: "concurrency negative", block: "shadow:\n  concurrency: -1\n",
			want: "config: shadow.concurrency: must be from 1 to 4, got -1"},
		{name: "concurrency 4", block: "shadow:\n  concurrency: 4\n  maxUSDPerDay: 20\n"},

		{name: "pauseAtQuota above 1", block: "shadow:\n  pauseAtQuota: 1.1\n",
			want: "config: shadow.pauseAtQuota: must be 0 (off) or in (0, 1], got 1.1"},
		{name: "pauseAtQuota negative", block: "shadow:\n  pauseAtQuota: -0.1\n",
			want: "config: shadow.pauseAtQuota: must be 0 (off) or in (0, 1], got -0.1"},
		{name: "pauseAtQuota 0", block: "shadow:\n  pauseAtQuota: 0\n"},
		{name: "pauseAtQuota 1", block: "shadow:\n  pauseAtQuota: 1\n"},

		{name: "outcomes.interval below 15m", block: "shadow:\n  outcomes:\n    interval: 14m\n",
			want: `config: shadow.outcomes.interval: must be a duration from 15m to 7d, got "14m"`},
		{name: "outcomes.interval above 7d", block: "shadow:\n  outcomes:\n    interval: 8d\n",
			want: `config: shadow.outcomes.interval: must be a duration from 15m to 7d, got "8d"`},
		{name: "outcomes.interval 15m", block: "shadow:\n  outcomes:\n    interval: 15m\n"},
		{name: "outcomes.interval 7d", block: "shadow:\n  outcomes:\n    interval: 7d\n"},

		{name: "outcomes.windowDays above 180", block: "shadow:\n  outcomes:\n    windowDays: 181\n",
			want: "config: shadow.outcomes.windowDays: must be from 1 to 180, got 181"},
		{name: "outcomes.windowDays 1", block: "shadow:\n  outcomes:\n    windowDays: 1\n"},
		{name: "outcomes.windowDays 180", block: "shadow:\n  outcomes:\n    windowDays: 180\n"},

		{name: "digest.at hour 24", block: "shadow:\n  digest:\n    at: \"24:00\"\n",
			want: `config: shadow.digest.at: must be HH:MM, got "24:00"`},
		{name: "digest.at single digit", block: "shadow:\n  digest:\n    at: \"9:00\"\n",
			want: `config: shadow.digest.at: must be HH:MM, got "9:00"`},
		{name: "digest.at minute 60", block: "shadow:\n  digest:\n    at: \"09:60\"\n",
			want: `config: shadow.digest.at: must be HH:MM, got "09:60"`},
		{name: "digest.at 00:00", block: "shadow:\n  digest:\n    at: \"00:00\"\n"},
		{name: "digest.at 23:59", block: "shadow:\n  digest:\n    at: \"23:59\"\n"},

		{name: "digest enabled without a webhook", block: "shadow:\n  digest:\n    enabled: true\n",
			want: "config: shadow.digest.webhookUrl: is required when digest.enabled is true"},
		{name: "digest webhook carries the URL", block: "shadow:\n  digest:\n    webhookUrl: https://hooks.slack.com/services/T/B/x\n",
			want: "config: shadow.digest.webhookUrl: must start with"},
		{name: "digest webhook names a reference", block: "shadow:\n  digest:\n    enabled: true\n    webhookUrl: env:SHADOW_DIGEST\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base
			if base == "" {
				base = shadowBase
			}
			_, err := Load(writeCfg(t, base+tc.block))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted; want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestShadowUnknownKey(t *testing.T) {
	_, err := shadowCfg(t, "shadow: {intervall: 5m}\n")
	if err == nil {
		t.Fatal("a misspelled shadow key was accepted")
	}
	if !strings.Contains(err.Error(), "intervall") {
		t.Fatalf("error %v does not name the key", err)
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"7d":    168 * time.Hour,
		"1d":    24 * time.Hour,
		"90m":   90 * time.Minute,
		"6h":    6 * time.Hour,
		"1h30m": 90 * time.Minute,
	} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"d", "1.5d", "", "-1d", "7 d", "x7d", "99999999999999999999d"} {
		if got, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %v, want an error", in, got)
		}
	}
}
