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

// digestText is one day's digest: runs started, grades given and money
// spent that day, and each key graded that day with its grade. Graded and
// confirmed are the scorecard headline's numbers over the keys graded that
// day, so the two never disagree about what counts as a grade. It carries
// no title, hypothesis or ticket text, the same metadata-only rule run
// notifications keep, because it leaves the machine.
func digestText(entries []shadow.Entry, day time.Time) string {
	y, m, d := day.Date()
	sameDay := func(t time.Time) bool {
		ty, tm, td := t.In(day.Location()).Date()
		return ty == y && tm == m && td == d
	}
	gradedThatDay := map[string]bool{}
	for key, t := range shadow.Fold(entries) {
		if t.Grade != nil && sameDay(t.Grade.At) {
			gradedThatDay[key] = true
		}
	}
	var theirs []shadow.Entry
	for _, e := range entries {
		if gradedThatDay[e.Key] {
			theirs = append(theirs, e)
		}
	}
	report := shadow.Report(theirs, time.Time{})
	rows := append([]shadow.Row(nil), report.Rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	spent := shadow.Today(entries, 0, day)

	var b strings.Builder
	fmt.Fprintf(&b, "Sirdar shadow, %s: started %d, graded %d, confirmed %d%%, cost $%.2f",
		day.Format("2006-01-02"), spent.Runs, report.Headline.Graded,
		int(math.Round(100*report.Headline.Confirmed)), spent.USD)
	for _, r := range rows {
		fmt.Fprintf(&b, "\n%s %s", r.Key, r.Grade)
		if !r.Final {
			b.WriteString(" (provisional)")
		}
	}
	return b.String()
}

// maybeSendDigest posts yesterday's digest once, at or after digest.at
// local time, when the digest is enabled. A morning digest of the day
// before covers a whole day; one of the day it is sent on would cover
// only the night. The digest line it appends on success
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
	if msg := digestURLProblem(hook); msg != "" {
		s.shadowDigestError(ws, msg)
		return
	}
	if err := digestPost(ctx, hook, digestText(entries, now.AddDate(0, 0, -1))); err != nil {
		s.shadowDigestError(ws, "shadow digest: "+err.Error())
		return
	}
	if err := shadow.Append(ws.Root, shadow.Entry{At: now, Ev: "digest", Day: day}); err != nil {
		s.shadowDigestError(ws, err.Error())
	}
}

// digestHTTPSError is what the status shows for a digest webhook that is
// not https: the URL is a bearer credential, and must not cross the
// network in the clear.
const digestHTTPSError = "shadow.digest.webhookUrl: must be an https URL"

// digestURLProblem is why the digest may not be posted to hook, or "".
// The send and the doctor row both ask it.
func digestURLProblem(hook string) string {
	u, err := url.Parse(hook)
	if err != nil || !digestHostOK(u.Hostname()) {
		return digestHostError
	}
	if u.Scheme != "https" {
		return digestHTTPSError
	}
	return ""
}

func (s *Service) shadowDigestError(ws Workspace, msg string) {
	w := s.shadowWatch(ws.ID, ws.Root)
	s.shadow.mu.Lock()
	w.lastError = msg
	s.shadow.mu.Unlock()
}
