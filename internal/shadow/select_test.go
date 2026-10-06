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

	onA := []Listed{bug("A", 0)}

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
			listed:  onA,
			policy:  base,
			start:   []string{"A"},
		},
		{
			name:    "no retry after a not_found failure",
			entries: retried("A", "failed", "not_found: no such issue", time.Hour),
			listed:  onA,
			policy:  base,
		},
		{
			name:    "no retry within one interval",
			entries: retried("A", "failed", "provider exited", time.Minute),
			listed:  onA,
			policy:  base,
		},
		{
			name:    "retry after blocked interrupted",
			entries: retried("A", "blocked", "interrupted", time.Hour),
			listed:  onA,
			policy:  base,
			start:   []string{"A"},
		},
		{
			name:    "retry after blocked on a rate limit",
			entries: retried("A", "blocked", "rate limited until 14:00", time.Hour),
			listed:  onA,
			policy:  base,
			start:   []string{"A"},
		},
		{
			name:    "no retry after blocked on a question",
			entries: retried("A", "blocked", "asked: which tenant?", time.Hour),
			listed:  onA,
			policy:  base,
		},
		{
			name: "no third attempt",
			entries: append(retried("A", "failed", "provider exited", 90*time.Minute),
				started("A", "r2", 2, time.Hour),
				finished("A", "r2", "failed", "provider exited", 30*time.Minute)),
			listed: onA,
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
			listed: []Listed{bug("N", 0), bug("R", 0)},
			policy: base,
			start:  []string{"N", "R"},
		},
		{
			name:    "seen key no longer listed is not started",
			entries: []Entry{seen("A", 10*time.Minute), seen("B", 10*time.Minute)},
			listed:  []Listed{bug("B", 0)},
			policy:  base,
			start:   []string{"B"},
		},
		{
			name:    "failed attempt no longer listed is not retried",
			entries: retried("A", "failed", "provider exited", time.Hour),
			policy:  base,
		},
		{
			name:    "a key moved to an excluded status is not retried",
			entries: retried("A", "failed", "provider exited", time.Hour),
			listed:  []Listed{{Key: "A", Type: "bug", Status: "Done"}},
			policy:  with(func(p *Policy) { p.Statuses = []string{"open"} }),
		},
		{
			name:    "a listed key seen on an earlier tick and never started stays a candidate",
			entries: []Entry{seen("A", 10*time.Minute)},
			listed:  onA,
			policy:  with(func(p *Policy) { p.Concurrency = 1 }),
			start:   []string{"A"},
		},
		{
			name: "run cap named before USD cap when both are reached",
			entries: append(append([]Entry{}, nineToday...),
				seen("K10", 2*time.Hour), started("K10", "rK10", 1, time.Hour),
				Entry{At: ago(30 * time.Minute), Ev: "finished", Key: "K10", RunID: "rK10", Status: "completed", CostUSD: 10}),
			listed: []Listed{bug("A", time.Minute)},
			policy: base,
			seen:   []string{"A"},
			capHit: "daily run cap reached (10 of 10)",
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
	}, []Listed{{Key: "A", Type: "bug"}}, now, Policy{Concurrency: 1, MaxPerDay: 1, MaxUSDPerDay: 1, Interval: time.Minute}, 0, 0)
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

// TestSelectCutsSeenText: a seen line carries the tracker's title, type and
// status as they came, and one huge title would make the line too long for
// the ledger, failing every tick at the same append.
func TestSelectCutsSeenText(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 70<<10)
	listed := []Listed{{Key: "A", Title: long, Type: "Bug", Status: "Open " + long, CreatedAt: now.Add(-time.Minute)}}
	p := Policy{Types: []string{"bug"}, Since: now.Add(-time.Hour), MaxPerDay: 10, MaxUSDPerDay: 10, Concurrency: 1, Interval: 5 * time.Minute}
	plan := Select(nil, listed, now, p, 0, 0)
	if len(plan.Seen) != 1 {
		t.Fatalf("seen %+v", plan.Seen)
	}
	e := plan.Seen[0]
	if n := len([]rune(e.Title)); n != MaxFieldRunes {
		t.Errorf("title is %d runes, want %d", n, MaxFieldRunes)
	}
	if n := len([]rune(e.Status)); n != MaxFieldRunes {
		t.Errorf("status is %d runes, want %d", n, MaxFieldRunes)
	}
	if err := Append(t.TempDir(), e); err != nil {
		t.Errorf("the seen line does not fit the ledger: %v", err)
	}
	if len(plan.Start) != 1 {
		t.Errorf("start %+v, want the ticket started", plan.Start)
	}
}

// TestSelectConcurrencyHoldBack: two new tickets with room for one run
// start one, and the other, already seen, starts on the next tick once the
// first has finished.
func TestSelectConcurrencyHoldBack(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	p := Policy{Types: []string{"bug"}, Since: now.Add(-24 * time.Hour), MaxPerDay: 10, MaxUSDPerDay: 10, Concurrency: 1, Interval: 5 * time.Minute}
	listed := []Listed{
		{Key: "A", Type: "bug", Status: "Open", CreatedAt: now.Add(-2 * time.Hour)},
		{Key: "B", Type: "bug", Status: "Open", CreatedAt: now.Add(-time.Hour)},
	}
	first := Select(nil, listed, now, p, 0, 0)
	if len(first.Seen) != 2 || len(first.Start) != 1 || first.Start[0].Key != "A" {
		t.Fatalf("first tick seen %d, start %+v; want both seen and A started", len(first.Seen), first.Start)
	}
	entries := append([]Entry{}, first.Seen...)
	entries = append(entries, Entry{At: now, Ev: "started", Key: "A", RunID: "rA", Attempt: 1})

	// While A runs, B stays held back.
	later := now.Add(5 * time.Minute)
	if held := Select(entries, listed, later, p, 1, 0); len(held.Start) != 0 || len(held.Seen) != 0 {
		t.Fatalf("with A in flight: seen %d, start %+v; want nothing", len(held.Seen), held.Start)
	}

	entries = append(entries, Entry{At: later, Ev: "finished", Key: "A", RunID: "rA", Status: "completed"})
	next := Select(entries, listed, later.Add(5*time.Minute), p, 0, 0)
	if len(next.Start) != 1 || next.Start[0].Key != "B" {
		t.Fatalf("after A finished: start %+v, want B", next.Start)
	}
}
