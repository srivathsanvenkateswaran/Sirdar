package shadow

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSelect(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	ptr := func(t time.Time) *time.Time { return &t }
	base := Policy{
		Types:        []string{"bug"},
		Since:        ago(30 * 24 * time.Hour),
		MaxPerDay:    10,
		MaxUSDPerDay: 10,
		Concurrency:  2,
		Interval:     5 * time.Minute,
	}
	with := func(f func(*Policy)) Policy {
		p := base
		f(&p)
		return p
	}
	bug := func(key string, created time.Duration) Listed {
		l := Listed{Key: key, Title: "title " + key, Type: "Bug", Status: "Open"}
		if created > 0 {
			l.CreatedAt = ago(created)
		}
		return l
	}
	seen := func(key string, at time.Duration) Entry {
		return Entry{At: ago(at), Ev: "seen", Key: key, Type: "bug", Status: "Open"}
	}
	started := func(key, run string, attempt int, at time.Duration) Entry {
		return Entry{At: ago(at), Ev: "started", Key: key, RunID: run, Attempt: attempt}
	}
	finished := func(key, run, status, reason string, at time.Duration) Entry {
		return Entry{At: ago(at), Ev: "finished", Key: key, RunID: run, Status: status, Reason: reason}
	}
	retried := func(key, status, reason string, finishedAgo time.Duration) []Entry {
		return []Entry{
			seen(key, 3*time.Hour),
			started(key, "r1", 1, 2*time.Hour),
			finished(key, "r1", status, reason, finishedAgo),
		}
	}

	nineToday := []Entry{}
	for i := 1; i <= 9; i++ {
		k := fmt.Sprintf("K%d", i)
		nineToday = append(nineToday, seen(k, 2*time.Hour), started(k, "r"+k, 1, time.Hour), finished(k, "r"+k, "completed", "", 30*time.Minute))
	}

	cases := []struct {
		name        string
		entries     []Entry
		listed      []Listed
		policy      Policy
		inFlight    int
		inFlightUSD float64
		seen        []string
		baseline    []string
		skipped     []string
		start       []string
		capHit      string
	}{
		{
			name:   "type filter drops a task",
			listed: []Listed{bug("A", time.Hour), {Key: "B", Type: "Task", Status: "Open", CreatedAt: ago(time.Hour)}},
			policy: base,
			seen:   []string{"A"},
			start:  []string{"A"},
		},
		{
			name:   "status filter keeps only Open",
			listed: []Listed{bug("A", time.Hour), {Key: "B", Type: "bug", Status: "In Progress", CreatedAt: ago(time.Hour)}},
			policy: with(func(p *Policy) { p.Statuses = []string{"open"} }),
			seen:   []string{"A"},
			start:  []string{"A"},
		},
		{
			name:   "since drops an older ticket",
			listed: []Listed{bug("A", 2*time.Hour), bug("B", 3*24*time.Hour)},
			policy: with(func(p *Policy) { p.Since = ago(24 * time.Hour) }),
			seen:   []string{"A"},
			start:  []string{"A"},
		},
		{
			name:     "baseline on the first tick after armed",
			entries:  []Entry{{At: ago(time.Minute), Ev: "armed", Since: ptr(ago(time.Minute))}},
			listed:   []Listed{bug("A", 0), bug("B", 30*time.Second)},
			policy:   with(func(p *Policy) { p.ArmedAt = ago(time.Minute); p.Since = ago(time.Minute) }),
			seen:     []string{"A", "B"},
			baseline: []string{"A"},
			start:    []string{"B"},
		},
		{
			name: "no baseline on a later tick",
			entries: []Entry{
				{At: ago(10 * time.Minute), Ev: "armed", Since: ptr(ago(10 * time.Minute))},
				seen("X", 5*time.Minute),
				started("X", "rX", 1, 4*time.Minute),
			},
			listed: []Listed{bug("A", 0)},
			policy: with(func(p *Policy) { p.ArmedAt = ago(10 * time.Minute) }),
			seen:   []string{"A"},
			start:  []string{"A"},
		},
		{
			name:    "stale skip after 73h",
			entries: []Entry{seen("A", 73*time.Hour)},
			policy:  base,
			skipped: []string{"A"},
		},
		{
			name:    "retry after failed provider exited",
			entries: retried("A", "failed", "provider exited", time.Hour),
			policy:  base,
			start:   []string{"A"},
		},
		{
			name:    "no retry after a not_found failure",
			entries: retried("A", "failed", "not_found: no such issue", time.Hour),
			policy:  base,
		},
		{
			name:    "no retry within one interval",
			entries: retried("A", "failed", "provider exited", time.Minute),
			policy:  base,
		},
		{
			name:    "retry after blocked interrupted",
			entries: retried("A", "blocked", "interrupted", time.Hour),
			policy:  base,
			start:   []string{"A"},
		},
		{
			name:    "retry after blocked on a rate limit",
			entries: retried("A", "blocked", "rate limited until 14:00", time.Hour),
			policy:  base,
			start:   []string{"A"},
		},
		{
			name:    "no retry after blocked on a question",
			entries: retried("A", "blocked", "asked: which tenant?", time.Hour),
			policy:  base,
		},
		{
			name: "no third attempt",
			entries: append(retried("A", "failed", "provider exited", 90*time.Minute),
				started("A", "r2", 2, time.Hour),
				finished("A", "r2", "failed", "provider exited", 30*time.Minute)),
			policy: base,
		},
		{
			name:    "count cap",
			entries: nineToday,
			listed:  []Listed{bug("A", 2*time.Minute), bug("B", time.Minute)},
			policy:  with(func(p *Policy) { p.Concurrency = 5 }),
			seen:    []string{"A", "B"},
			start:   []string{"A"},
			capHit:  "daily run cap reached (10 of 10)",
		},
		{
			name: "USD cap",
			entries: []Entry{
				seen("K", 2*time.Hour), started("K", "rK", 1, time.Hour),
				{At: ago(30 * time.Minute), Ev: "finished", Key: "K", RunID: "rK", Status: "completed", CostUSD: 10},
			},
			listed: []Listed{bug("A", time.Minute)},
			policy: base,
			seen:   []string{"A"},
			capHit: "daily USD cap reached ($10.00 of $10.00)",
		},
		{
			name:     "concurrency 1 with one in flight",
			listed:   []Listed{bug("A", time.Minute)},
			policy:   with(func(p *Policy) { p.Concurrency = 1 }),
			inFlight: 1,
			seen:     []string{"A"},
		},
		{
			name:   "oldest CreatedAt first",
			listed: []Listed{bug("C", 0), bug("B", time.Hour), bug("A", 2*time.Hour)},
			policy: with(func(p *Policy) { p.Concurrency = 3 }),
			seen:   []string{"C", "B", "A"},
			start:  []string{"A", "B", "C"},
		},
		{
			name: "first attempts before retries",
			entries: append(retried("R", "failed", "provider exited", time.Hour),
				seen("N", 10*time.Minute)),
			policy: base,
			start:  []string{"N", "R"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := Select(c.entries, c.listed, now, c.policy, c.inFlight, c.inFlightUSD)
			var seenKeys, baseKeys, skippedKeys, startKeys []string
			for _, e := range plan.Seen {
				if e.Ev != "seen" || !e.At.Equal(now) {
					t.Errorf("seen line %+v", e)
				}
				seenKeys = append(seenKeys, e.Key)
				if e.Baseline {
					baseKeys = append(baseKeys, e.Key)
				}
			}
			for _, e := range plan.Skipped {
				if e.Ev != "skipped" {
					t.Errorf("skipped line %+v", e)
				}
				skippedKeys = append(skippedKeys, e.Key)
			}
			for _, s := range plan.Start {
				startKeys = append(startKeys, s.Key)
			}
			check := func(what string, got, want []string) {
				if len(got) == 0 && len(want) == 0 {
					return
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %v, want %v", what, got, want)
				}
			}
			check("seen", seenKeys, c.seen)
			check("baseline", baseKeys, c.baseline)
			check("skipped", skippedKeys, c.skipped)
			check("start", startKeys, c.start)
			if plan.CapHit != c.capHit {
				t.Errorf("CapHit = %q, want %q", plan.CapHit, c.capHit)
			}
		})
	}
}

func TestSelectDetails(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	created := now.Add(-time.Hour)
	plan := Select(nil, []Listed{{Key: "A", Title: "Login fails", Type: "Bug", Status: "Open", CreatedAt: created}}, now,
		Policy{Types: []string{"bug"}, MaxPerDay: 1, MaxUSDPerDay: 1, Concurrency: 1}, 0, 0)
	if len(plan.Seen) != 1 {
		t.Fatalf("seen = %+v", plan.Seen)
	}
	s := plan.Seen[0]
	if s.Title != "Login fails" || s.Type != "Bug" || s.Status != "Open" || s.CreatedAt == nil || !s.CreatedAt.Equal(created) || s.Baseline {
		t.Errorf("seen line = %+v", s)
	}
	if len(plan.Start) != 1 || plan.Start[0] != (Start{Key: "A", Attempt: 1}) {
		t.Errorf("start = %+v", plan.Start)
	}

	stale := Select([]Entry{{At: now.Add(-73 * time.Hour), Ev: "seen", Key: "A"}}, nil, now, Policy{Concurrency: 1, MaxPerDay: 1, MaxUSDPerDay: 1}, 0, 0)
	if len(stale.Skipped) != 1 || stale.Skipped[0].Reason != "stale: seen 73h ago and never started" {
		t.Errorf("stale skip = %+v", stale.Skipped)
	}
	again := Select(append([]Entry{{At: now.Add(-73 * time.Hour), Ev: "seen", Key: "A"}}, stale.Skipped...), nil, now, Policy{Concurrency: 1, MaxPerDay: 1, MaxUSDPerDay: 1}, 0, 0)
	if len(again.Skipped) != 0 || len(again.Start) != 0 {
		t.Errorf("a skipped key was skipped or started again: %+v", again)
	}

	retry := Select([]Entry{
		{At: now.Add(-3 * time.Hour), Ev: "seen", Key: "A"},
		{At: now.Add(-2 * time.Hour), Ev: "started", Key: "A", RunID: "r1", Attempt: 1},
		{At: now.Add(-time.Hour), Ev: "finished", Key: "A", RunID: "r1", Status: "blocked", Reason: "model limit: weekly"},
	}, nil, now, Policy{Concurrency: 1, MaxPerDay: 1, MaxUSDPerDay: 1, Interval: time.Minute}, 0, 0)
	if len(retry.Start) != 1 || retry.Start[0] != (Start{Key: "A", Attempt: 2, RetryOf: "r1"}) {
		t.Errorf("retry start = %+v", retry.Start)
	}
}

func TestTodayCounts(t *testing.T) {
	dubai := time.FixedZone("GST", 4*60*60)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, dubai)
	// 01:00 local on the 6th is still the 5th in UTC; the local date is what counts.
	earlyLocal := time.Date(2026, 10, 5, 21, 0, 0, 0, time.UTC)
	yesterday := now.Add(-24 * time.Hour)
	entries := []Entry{
		{At: earlyLocal, Ev: "started", Key: "A", RunID: "a1", Attempt: 1},
		{At: now.Add(-time.Hour), Ev: "started", Key: "B", RunID: "b2", Attempt: 2},
		{At: yesterday, Ev: "started", Key: "C", RunID: "c1", Attempt: 1},
		{At: now.Add(-2 * time.Hour), Ev: "finished", Key: "A", RunID: "a1", CostUSD: 1.5},
		{At: yesterday, Ev: "finished", Key: "C", RunID: "c1", CostUSD: 3},
		{At: now.Add(-time.Minute), Ev: "graded", Key: "A", ShadowRunID: "a1", CostUSD: 0.25},
		{At: yesterday, Ev: "graded", Key: "C", ShadowRunID: "c1", CostUSD: 0.5},
	}
	d := Today(entries, 0.5, now)
	if d.Runs != 1 {
		t.Errorf("Runs = %d, want 1", d.Runs)
	}
	if math.Abs(d.USD-2.25) > 1e-9 {
		t.Errorf("USD = %v, want 2.25", d.USD)
	}
}

func TestStaleReasonUsesWholeHours(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	plan := Select([]Entry{{At: now.Add(-(100*time.Hour + 40*time.Minute)), Ev: "seen", Key: "A"}}, nil, now, Policy{Concurrency: 1}, 0, 0)
	if len(plan.Skipped) != 1 || !strings.Contains(plan.Skipped[0].Reason, "seen 100h ago") {
		t.Errorf("skipped = %+v", plan.Skipped)
	}
}
