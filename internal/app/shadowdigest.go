package app

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/shadow"
)

// digestHostError is what the status shows for a digest webhook that does
// not point at Slack. The digest is the one message shadow sends out, and
// it may only go to a Slack incoming webhook.
const digestHostError = "shadow.digest.webhookUrl: host must be hooks.slack.com"

// digestText is the day's digest: counts, cost, and each key graded that
// day with its grade. It carries no title, hypothesis or ticket text, the
// same metadata-only rule run notifications keep, because it leaves the
// machine.
func digestText(entries []shadow.Entry, day time.Time) string {
	y, m, d := day.Date()
	sameDay := func(t time.Time) bool {
		ty, tm, td := t.In(day.Location()).Date()
		return ty == y && tm == m && td == d
	}
	grades := map[string]string{}
	for _, e := range entries {
		// A graded line with no grade is a judge call that failed; its
		// cost is in the total below, but it graded nothing.
		if e.Ev == "graded" && e.Grade != "" && e.Key != "" && sameDay(e.At) {
			grades[e.Key] = e.Grade // the day's newest grade for the key wins
		}
	}
	keys := make([]string, 0, len(grades))
	confirmed := 0
	for key, g := range grades {
		keys = append(keys, key)
		if g == "confirmed" {
			confirmed++
		}
	}
	sort.Strings(keys)
	pct := 0
	if len(keys) > 0 {
		pct = int(math.Round(100 * float64(confirmed) / float64(len(keys))))
	}
	today := shadow.Today(entries, 0, day)

	var b strings.Builder
	fmt.Fprintf(&b, "Sirdar shadow, %s: started %d, graded %d, confirmed %d%%, cost $%.2f",
		day.Format("2006-01-02"), today.Runs, len(keys), pct, today.USD)
	for _, key := range keys {
		fmt.Fprintf(&b, "\n%s %s", key, grades[key])
	}
	return b.String()
}

// maybeSendDigest posts the day's digest once, at or after digest.at local
// time, when the digest is enabled. The digest line it appends on success
// is what keeps a restart, or a second tick, from sending it again; a
// failed post leaves no line, so the next tick tries again.
func (s *Service) maybeSendDigest(ctx context.Context, ws Workspace, cfg *config.Config, now time.Time) {
	if !cfg.Shadow.Digest.Enabled {
		return
	}
	hour, minute := cfg.ShadowDigestAt()
	if now.Before(time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())) {
		return
	}
	day := now.Format("2006-01-02")
	entries, err := shadow.Read(ws.Root)
	if err != nil {
		s.shadowDigestError(ws, err.Error())
		return
	}
	for _, e := range entries {
		if e.Ev == "digest" && e.Day == day {
			return
		}
	}
	hook, err := config.Resolver{Keychain: KeychainFor()}.Resolve(cfg.Shadow.Digest.WebhookURL)
	if err != nil {
		s.shadowDigestError(ws, "shadow.digest.webhookUrl: "+err.Error())
		return
	}
	if u, err := url.Parse(hook); err != nil || !digestHostOK(u.Hostname()) {
		s.shadowDigestError(ws, digestHostError)
		return
	}
	if err := digestPost(ctx, hook, digestText(entries, now)); err != nil {
		s.shadowDigestError(ws, "shadow digest: "+err.Error())
		return
	}
	if err := shadow.Append(ws.Root, shadow.Entry{At: now, Ev: "digest", Day: day}); err != nil {
		s.shadowDigestError(ws, err.Error())
	}
}

func (s *Service) shadowDigestError(ws Workspace, msg string) {
	w := s.shadowWatch(ws.ID, ws.Root)
	s.shadow.mu.Lock()
	w.lastError = msg
	s.shadow.mu.Unlock()
}
