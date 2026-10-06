package shadow

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// Day is what today's shadow runs have used against the daily caps.
type Day struct {
	Runs int     // started lines with attempt 1 whose At is in today's local calendar day
	USD  float64 // today's finished.costUSD + inFlightUSD + today's graded.costUSD
}

// Today totals the ledger for now's local calendar day. Only first attempts
// count as runs, because the run cap is about how many tickets shadow takes
// on, and a retry finishes a ticket already counted. Money counts whatever
// spent it: retries, the runs still going, and the judge.
func Today(entries []Entry, inFlightUSD float64, now time.Time) Day {
	d := Day{USD: inFlightUSD}
	y, m, dd := now.Date()
	for _, e := range entries {
		ey, em, ed := e.At.In(now.Location()).Date()
		if ey != y || em != m || ed != dd {
			continue
		}
		switch e.Ev {
		case "started":
			if e.Attempt == 1 {
				d.Runs++
			}
		case "finished", "graded":
			d.USD += e.CostUSD
		}
	}
	return d
}

// Listed is one ticket from the tracker's list, as much as selection needs.
type Listed struct {
	Key, Title, Type, Status string
	CreatedAt                time.Time // zero when the adapter gives none
}

// Policy is the shadow config one tick selects under.
type Policy struct {
	Types        []string // already folded
	Statuses     []string // case-folded match when non-empty
	Since        time.Time
	ArmedAt      time.Time // At of the armed line; zero when since is a fixed time
	MaxPerDay    int
	MaxUSDPerDay float64
	Concurrency  int
	Interval     time.Duration
}

// StaleAfter is how long a seen ticket may wait for a start. Past it the
// operator has almost certainly worked the ticket, so a shadow verdict
// could no longer be ready first and would be graded against their work.
const StaleAfter = 72 * time.Hour

// MaxAttempts caps the runs one ticket gets: the first and one retry.
const MaxAttempts = 2

// Start is one run the watcher should begin.
type Start struct {
	Key     string
	Attempt int    // 1, or 2 for a retry
	RetryOf string // the previous attempt's RunID on a retry
}

// Plan is one tick's decision. The caller appends Seen and Skipped, then
// starts Start in order.
type Plan struct {
	Seen    []Entry // new seen lines, in listed order
	Skipped []Entry // new skipped lines
	Start   []Start // in start order, already cut to the caps
	CapHit  string  // "daily run cap reached (10 of 10)" or "daily USD cap reached ($10.00 of $10.00)" or ""
}

// Select decides one tick: which listed tickets are new, which seen ones
// have gone stale, and which to start now within the caps. It reads the
// ledger and the list and writes nothing, so the watcher's whole policy can
// be tested without a tracker or a provider.
func Select(entries []Entry, listed []Listed, now time.Time, p Policy, inFlight int, inFlightUSD float64) Plan {
	var plan Plan
	tickets := Fold(entries)

	// The first tick after arming is the one whose seen lines are the queue
	// as it stood when shadow was switched on. A ticket with no creation
	// time cannot be placed before or after since, so on that tick it is
	// taken as already there and never started.
	firstTick := false
	if !p.ArmedAt.IsZero() {
		firstTick = true
		for _, e := range entries {
			if e.Ev == "seen" && !e.At.Before(p.ArmedAt) {
				firstTick = false
				break
			}
		}
	}

	type candidate struct {
		key       string
		createdAt time.Time
	}
	var firsts []candidate
	added := map[string]bool{}
	for _, t := range listed {
		if !config.MatchQueueType(p.Types, t.Type) || !statusMatches(p.Statuses, t.Status) {
			continue
		}
		if !t.CreatedAt.IsZero() && t.CreatedAt.Before(p.Since) {
			continue
		}
		if tickets[t.Key] != nil || added[t.Key] {
			continue
		}
		added[t.Key] = true
		e := Entry{At: now, Ev: "seen", Key: t.Key, Title: t.Title, Type: t.Type, Status: t.Status}
		if !t.CreatedAt.IsZero() {
			created := t.CreatedAt
			e.CreatedAt = &created
		} else if firstTick {
			e.Baseline = true
		}
		plan.Seen = append(plan.Seen, e)
		if !e.Baseline {
			firsts = append(firsts, candidate{t.Key, t.CreatedAt})
		}
	}

	type retry struct {
		start      Start
		finishedAt time.Time
	}
	var retries []retry
	for key, t := range tickets {
		if len(t.Attempts) == 0 {
			if t.SeenAt.IsZero() || t.Baseline || t.Skipped != "" {
				continue
			}
			if age := now.Sub(t.SeenAt); age > StaleAfter {
				plan.Skipped = append(plan.Skipped, Entry{At: now, Ev: "skipped", Key: key,
					Reason: fmt.Sprintf("stale: seen %dh ago and never started", int(age.Hours()))})
				continue
			}
			firsts = append(firsts, candidate{key, t.CreatedAt})
			continue
		}
		if len(t.Attempts) >= MaxAttempts {
			continue
		}
		last := t.Latest()
		if last.Finished == nil || now.Sub(last.Finished.At) < p.Interval || !retryable(last.Finished) {
			continue
		}
		retries = append(retries, retry{
			start:      Start{Key: key, Attempt: len(t.Attempts) + 1, RetryOf: last.Started.RunID},
			finishedAt: last.Finished.At,
		})
	}
	sort.Slice(plan.Skipped, func(i, j int) bool { return plan.Skipped[i].Key < plan.Skipped[j].Key })

	sort.SliceStable(firsts, func(i, j int) bool {
		a, b := firsts[i], firsts[j]
		if a.createdAt.IsZero() != b.createdAt.IsZero() {
			return !a.createdAt.IsZero()
		}
		if !a.createdAt.Equal(b.createdAt) {
			return a.createdAt.Before(b.createdAt)
		}
		return a.key < b.key
	})
	sort.Slice(retries, func(i, j int) bool {
		if !retries[i].finishedAt.Equal(retries[j].finishedAt) {
			return retries[i].finishedAt.Before(retries[j].finishedAt)
		}
		return retries[i].start.Key < retries[j].start.Key
	})
	queue := make([]Start, 0, len(firsts)+len(retries))
	for _, c := range firsts {
		queue = append(queue, Start{Key: c.key, Attempt: 1})
	}
	for _, r := range retries {
		queue = append(queue, r.start)
	}

	day := Today(entries, inFlightUSD, now)
	newFirsts := 0
	for _, s := range queue {
		if inFlight+len(plan.Start) >= p.Concurrency {
			break
		}
		if day.USD >= p.MaxUSDPerDay {
			plan.CapHit = fmt.Sprintf("daily USD cap reached ($%.2f of $%.2f)", day.USD, p.MaxUSDPerDay)
			break
		}
		if s.Attempt == 1 {
			if day.Runs+newFirsts >= p.MaxPerDay {
				plan.CapHit = fmt.Sprintf("daily run cap reached (%d of %d)", day.Runs+newFirsts, p.MaxPerDay)
				break
			}
			newFirsts++
		}
		plan.Start = append(plan.Start, s)
	}
	return plan
}

func statusMatches(statuses []string, status string) bool {
	if len(statuses) == 0 {
		return true
	}
	for _, s := range statuses {
		if strings.EqualFold(s, status) {
			return true
		}
	}
	return false
}

// retryable reports whether a finished attempt ended for a reason another
// run could get past: a provider failure, a crash that interrupted it, or a
// rate or model limit that has since had time to lift. A not_found failure
// will fail the same way again, and every other ending is an answer.
func retryable(f *Entry) bool {
	switch f.Status {
	case "failed":
		return !strings.Contains(f.Reason, "not_found")
	case "blocked":
		return f.Reason == "interrupted" ||
			strings.HasPrefix(f.Reason, "rate limited") ||
			strings.HasPrefix(f.Reason, "model limit: ")
	}
	return false
}
