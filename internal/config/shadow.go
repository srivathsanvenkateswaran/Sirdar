package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Defaults for the shadow block (spec §10). The accessors fall back to the
// same values, so a Config built by hand reads as a workspace that set none
// of these keys.
const (
	defaultShadowInterval         = "15m"
	defaultShadowAssignee         = "me"
	defaultShadowSince            = "armed"
	defaultShadowMaxPerDay        = 10
	defaultShadowMaxUSDPerDay     = 10
	defaultShadowConcurrency      = 1
	defaultShadowPauseAtQuota     = 0.85
	defaultShadowOutcomesInterval = "6h"
	defaultShadowWindowDays       = 30
	defaultShadowDigestAt         = "09:00"
)

// ShadowConfig is the shadow block: an unattended watcher that triages the
// operator's own tickets in the background and leaves its work only under
// .sirdar/, so its answers can be compared with what the operator did.
//
// The pointer fields default to true (or 0.85) and are pointers so an
// explicit false or 0 survives the defaults. Types is a pointer so an absent
// list can fall back to the tracker queue's types while an empty one is an
// error.
type ShadowConfig struct {
	Enabled         bool           `yaml:"enabled"`
	Interval        string         `yaml:"interval"`
	Assignee        string         `yaml:"assignee"`
	Types           *[]string      `yaml:"types"`
	Statuses        []string       `yaml:"statuses"`
	Since           string         `yaml:"since"`
	MaxPerDay       int            `yaml:"maxPerDay"`
	MaxUSDPerDay    float64        `yaml:"maxUSDPerDay"`
	Concurrency     int            `yaml:"concurrency"`
	Model           string         `yaml:"model"`
	PauseAtQuota    *float64       `yaml:"pauseAtQuota"`
	YieldToOperator *bool          `yaml:"yieldToOperator"`
	Reproduce       *bool          `yaml:"reproduce"`
	Outcomes        ShadowOutcomes `yaml:"outcomes"`
	Judge           ShadowJudge    `yaml:"judge"`
	Digest          ShadowDigest   `yaml:"digest"`
}

// ShadowOutcomes configures how often, and for how long after a shadow run,
// Sirdar looks for what actually happened to the ticket.
type ShadowOutcomes struct {
	Interval   string `yaml:"interval"`
	WindowDays int    `yaml:"windowDays"`
	Tracker    *bool  `yaml:"tracker"`
}

// ShadowJudge names the model that grades a shadow answer against the
// outcome; empty means the workspace model.
type ShadowJudge struct {
	Model string `yaml:"model"`
}

// ShadowDigest is the opt-in daily summary. WebhookURL is a credential
// reference, never the URL itself, because a Slack webhook URL is a secret.
type ShadowDigest struct {
	Enabled    bool   `yaml:"enabled"`
	At         string `yaml:"at"`
	WebhookURL string `yaml:"webhookUrl"`
}

func applyShadowDefaults(c *Config) {
	s := &c.Shadow
	if s.Interval == "" {
		s.Interval = defaultShadowInterval
	}
	// An absent assignee reads as "me"; one written as only spaces is
	// trimmed after the default, so validation reports it rather than
	// quietly reading it as the operator.
	if s.Assignee == "" {
		s.Assignee = defaultShadowAssignee
	}
	s.Assignee = strings.TrimSpace(s.Assignee)
	if s.Since == "" {
		s.Since = defaultShadowSince
	}
	if s.MaxPerDay == 0 {
		s.MaxPerDay = defaultShadowMaxPerDay
	}
	if s.MaxUSDPerDay == 0 {
		s.MaxUSDPerDay = defaultShadowMaxUSDPerDay
	}
	if s.Concurrency == 0 {
		s.Concurrency = defaultShadowConcurrency
	}
	s.Model = strings.TrimSpace(s.Model)
	if s.PauseAtQuota == nil {
		q := defaultShadowPauseAtQuota
		s.PauseAtQuota = &q
	}
	if s.YieldToOperator == nil {
		yes := true
		s.YieldToOperator = &yes
	}
	if s.Reproduce == nil {
		yes := true
		s.Reproduce = &yes
	}
	if s.Outcomes.Interval == "" {
		s.Outcomes.Interval = defaultShadowOutcomesInterval
	}
	if s.Outcomes.WindowDays == 0 {
		s.Outcomes.WindowDays = defaultShadowWindowDays
	}
	if s.Outcomes.Tracker == nil {
		yes := true
		s.Outcomes.Tracker = &yes
	}
	s.Judge.Model = strings.TrimSpace(s.Judge.Model)
	if s.Digest.At == "" {
		s.Digest.At = defaultShadowDigestAt
	}
}

// digestAt is HH:MM on a 24-hour clock, both fields two digits.
var digestAt = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

func validateShadow(c *Config) error {
	s := &c.Shadow
	if s.Enabled && c.Sources.Tracker == nil {
		return errors.New("config: shadow.enabled: requires sources.tracker")
	}
	if d, err := ParseDuration(s.Interval); err != nil || d < 5*time.Minute || d > 24*time.Hour {
		return fmt.Errorf("config: shadow.interval: must be a duration from 5m to 24h, got %q", s.Interval)
	}
	if strings.TrimSpace(s.Assignee) == "" {
		return errors.New("config: shadow.assignee: must not be empty")
	}
	if s.Types != nil && len(NormalizeQueueTypes(*s.Types)) == 0 {
		return errors.New(`config: shadow.types: must name at least one type (or "*")`)
	}
	for i, st := range s.Statuses {
		if strings.TrimSpace(st) == "" {
			return fmt.Errorf("config: shadow.statuses: entry %d is empty", i)
		}
	}
	if _, _, ok := parseShadowSince(s.Since); !ok {
		return fmt.Errorf("config: shadow.since: must be armed, a date (2006-01-02) or an RFC 3339 time, got %q", s.Since)
	}
	if s.MaxPerDay < 1 || s.MaxPerDay > 100 {
		return fmt.Errorf("config: shadow.maxPerDay: must be from 1 to 100, got %d", s.MaxPerDay)
	}
	if !(s.MaxUSDPerDay > 0 && s.MaxUSDPerDay <= 500) {
		return fmt.Errorf("config: shadow.maxUSDPerDay: must be > 0 and <= 500, got %v", s.MaxUSDPerDay)
	}
	// Concurrency is range-checked before the spend floor that depends on
	// it, so an out-of-range value is reported as itself rather than as a
	// spend cap that is too low for it.
	if s.Concurrency < 1 || s.Concurrency > 4 {
		return fmt.Errorf("config: shadow.concurrency: must be from 1 to 4, got %d", s.Concurrency)
	}
	// With several runs in flight at once, a daily cap below what they can
	// spend together would be crossed by runs already started, so the cap
	// has to cover one full budget for each of them.
	if floor := float64(s.Concurrency) * c.Budget.MaxUSD; s.Concurrency > 1 && s.MaxUSDPerDay < floor {
		return fmt.Errorf("config: shadow.maxUSDPerDay: must be at least concurrency × budget.maxUsd (%v) when concurrency > 1", floor)
	}
	if q := s.PauseAtQuota; q != nil && *q != 0 && !(*q > 0 && *q <= 1) {
		return fmt.Errorf("config: shadow.pauseAtQuota: must be 0 (off) or in (0, 1], got %v", *q)
	}
	if d, err := ParseDuration(s.Outcomes.Interval); err != nil || d < 15*time.Minute || d > 168*time.Hour {
		return fmt.Errorf("config: shadow.outcomes.interval: must be a duration from 15m to 7d, got %q", s.Outcomes.Interval)
	}
	if s.Outcomes.WindowDays < 1 || s.Outcomes.WindowDays > 180 {
		return fmt.Errorf("config: shadow.outcomes.windowDays: must be from 1 to 180, got %d", s.Outcomes.WindowDays)
	}
	if !digestAt.MatchString(s.Digest.At) {
		return fmt.Errorf("config: shadow.digest.at: must be HH:MM, got %q", s.Digest.At)
	}
	if s.Digest.Enabled && s.Digest.WebhookURL == "" {
		return errors.New("config: shadow.digest.webhookUrl: is required when digest.enabled is true")
	}
	if s.Digest.WebhookURL != "" {
		if err := credentialRef("shadow.digest.webhookUrl", s.Digest.WebhookURL); err != nil {
			return err
		}
	}
	return nil
}

// maxDurationDays keeps a day count inside what time.Duration can hold.
const maxDurationDays = int64(1<<63-1) / int64(24*time.Hour)

// ParseDuration reads a Go duration ("90m", "1h30m") or a whole number of
// days with a "d" suffix ("7d"), which Go's own syntax lacks and which is
// the natural unit for an outcome window.
func ParseDuration(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		if n == "" || strings.Trim(n, "0123456789") != "" {
			return 0, fmt.Errorf("invalid duration %q: days must be a whole number", s)
		}
		days, err := strconv.ParseInt(n, 10, 64)
		if err != nil || days > maxDurationDays {
			return 0, fmt.Errorf("invalid duration %q: too many days", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// parseShadowSince reads shadow.since. A bare date is midnight local time,
// which is what an operator writing "2026-10-01" means by "from that day".
func parseShadowSince(s string) (since time.Time, armed, ok bool) {
	if s == defaultShadowSince {
		return time.Time{}, true, true
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, false, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, false, true
	}
	return time.Time{}, false, false
}

// shadowDuration parses a configured duration, falling back to the default
// for a Config built by hand that never went through Load.
func shadowDuration(v, fallback string) time.Duration {
	if d, err := ParseDuration(v); err == nil && d > 0 {
		return d
	}
	d, _ := ParseDuration(fallback)
	return d
}

// ShadowInterval is how often the watcher polls the tracker.
func (c *Config) ShadowInterval() time.Duration {
	if c == nil {
		return shadowDuration("", defaultShadowInterval)
	}
	return shadowDuration(c.Shadow.Interval, defaultShadowInterval)
}

// ShadowOutcomesInterval is how often outcomes are collected.
func (c *Config) ShadowOutcomesInterval() time.Duration {
	if c == nil {
		return shadowDuration("", defaultShadowOutcomesInterval)
	}
	return shadowDuration(c.Shadow.Outcomes.Interval, defaultShadowOutcomesInterval)
}

// ShadowSince is the earliest ticket time the watcher considers. armed is
// true for "armed", in which case the time is the armed ledger line's and
// the caller reads it from there; since is then zero.
func (c *Config) ShadowSince() (since time.Time, armed bool) {
	if c == nil {
		return time.Time{}, true
	}
	since, armed, ok := parseShadowSince(c.Shadow.Since)
	if !ok {
		return time.Time{}, true
	}
	return since, armed
}

// ShadowTypes is the shadow type filter: shadow.types folded onto the
// canonical vocabulary when the block names any, else the tracker queue's.
func (c *Config) ShadowTypes() []string {
	if c == nil || c.Shadow.Types == nil {
		return c.QueueTypes()
	}
	return NormalizeQueueTypes(*c.Shadow.Types)
}

// ShadowPauseAtQuota is the provider quota utilization at which the
// watcher stops starting runs; 0 means it never pauses for quota.
func (c *Config) ShadowPauseAtQuota() float64 {
	if c == nil || c.Shadow.PauseAtQuota == nil {
		return defaultShadowPauseAtQuota
	}
	return *c.Shadow.PauseAtQuota
}

// ShadowYield reports whether the watcher defers new shadow starts while
// the operator has a run of their own preparing or running.
func (c *Config) ShadowYield() bool {
	return c == nil || c.Shadow.YieldToOperator == nil || *c.Shadow.YieldToOperator
}

// ShadowReproduce reports whether shadow runs may reproduce on staging.
func (c *Config) ShadowReproduce() bool {
	return c == nil || c.Shadow.Reproduce == nil || *c.Shadow.Reproduce
}

// ShadowOutcomesTracker reports whether outcome collection may ask the
// tracker for a ticket's outcome, when its adapter can answer.
func (c *Config) ShadowOutcomesTracker() bool {
	return c == nil || c.Shadow.Outcomes.Tracker == nil || *c.Shadow.Outcomes.Tracker
}

// ShadowModel is the model shadow runs use; "" means the workspace model.
func (c *Config) ShadowModel() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Shadow.Model)
}

// ShadowJudgeModel is the model that grades shadow answers: judge.model,
// else the workspace model.
func (c *Config) ShadowJudgeModel() string {
	if c == nil {
		return ""
	}
	if m := strings.TrimSpace(c.Shadow.Judge.Model); m != "" {
		return m
	}
	return c.Model
}

// ShadowDigestAt is the local time of day the digest is sent.
func (c *Config) ShadowDigestAt() (hour, minute int) {
	at := defaultShadowDigestAt
	if c != nil && digestAt.MatchString(c.Shadow.Digest.At) {
		at = c.Shadow.Digest.At
	}
	m := digestAt.FindStringSubmatch(at)
	hour, _ = strconv.Atoi(m[1])
	minute, _ = strconv.Atoi(m[2])
	return hour, minute
}
