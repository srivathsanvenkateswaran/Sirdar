package shadow

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// shadowRun is one key's started and finished lines, plus a graded line
// when grade is set.
type shadowRun struct {
	key, class, grade string
	final             bool
	startedAgo        time.Duration
	elapsed, cost     float64
	judgeCost         float64
	lead              *float64
	backfill          bool
	operatorFirst     bool
	status            string
}

func (r shadowRun) entries(now time.Time) []Entry {
	at := now.Add(-r.startedAgo)
	status := r.status
	if status == "" {
		status = "completed"
	}
	run := "run-" + r.key
	out := []Entry{
		{At: at.Add(-time.Minute), Ev: "seen", Key: r.key, Title: "title " + r.key},
		{At: at, Ev: "started", Key: r.key, RunID: run, Attempt: 1, Backfill: r.backfill, OperatorFirst: r.operatorFirst},
		{At: at.Add(10 * time.Minute), Ev: "finished", Key: r.key, RunID: run, Status: status, CostUSD: r.cost, ElapsedSec: r.elapsed,
			Verdict: &Verdict{Classification: r.class, Confidence: "medium", Hypothesis: "The cache is stale. It never expires."}},
	}
	if r.grade != "" {
		out = append(out, Entry{At: at.Add(time.Hour), Ev: "graded", Key: r.key, ShadowRunID: run, Grade: r.grade, Final: r.final,
			Reason: "because", CostUSD: r.judgeCost, LeadMinutes: r.lead})
	}
	return out
}

func ledgerOf(now time.Time, runs ...shadowRun) []Entry {
	var out []Entry
	for _, r := range runs {
		out = append(out, r.entries(now)...)
	}
	return out
}

var reportNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func since30() time.Time { return reportNow.Add(-30 * 24 * time.Hour) }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestReportHeadline(t *testing.T) {
	entries := ledgerOf(reportNow,
		shadowRun{key: "A", class: "code", grade: "confirmed", final: true, startedAgo: 6 * time.Hour},
		shadowRun{key: "B", class: "code", grade: "partial", final: true, startedAgo: 5 * time.Hour},
		shadowRun{key: "C", class: "data", grade: "wrong", final: true, startedAgo: 4 * time.Hour},
		shadowRun{key: "D", class: "", grade: "no-answer", final: true, startedAgo: 3 * time.Hour},
		shadowRun{key: "E", class: "code", grade: "partial", final: false, startedAgo: 2 * time.Hour},
		shadowRun{key: "F", class: "code", grade: "confirmed", final: true, startedAgo: time.Hour, backfill: true},
	)
	r := Report(entries, since30())
	h := r.Headline
	if h.Started != 5 || h.Graded != 4 || !near(h.Confirmed, 0.25) || !near(h.ConfirmedOrPartial, 0.5) {
		t.Errorf("headline = %+v", h)
	}
	if len(r.Rows) != 6 {
		t.Fatalf("rows = %d, want 6", len(r.Rows))
	}
	var keys []string
	for _, row := range r.Rows {
		keys = append(keys, row.Key)
	}
	if !reflect.DeepEqual(keys, []string{"F", "E", "D", "C", "B", "A"}) {
		t.Errorf("row order = %v, want newest first", keys)
	}
	f := r.Rows[0]
	if !reflect.DeepEqual(f.Tags, []string{"backfill"}) {
		t.Errorf("backfill row tags = %v", f.Tags)
	}
	if !reflect.DeepEqual(r.Rows[1].Tags, []string{"provisional"}) {
		t.Errorf("provisional row tags = %v", r.Rows[1].Tags)
	}
	if !reflect.DeepEqual(r.Rows[2].Tags, []string{"no answer"}) {
		t.Errorf("no-answer row tags = %v", r.Rows[2].Tags)
	}
	if r.Rows[1].Hypothesis != "The cache is stale." {
		t.Errorf("hypothesis = %q, want the first sentence", r.Rows[1].Hypothesis)
	}
	if r.Rows[1].Status != "completed" || r.Rows[1].RunID != "run-E" || r.Rows[1].Title != "title E" {
		t.Errorf("row E = %+v", r.Rows[1])
	}
	want := []ClassRow{
		{Classification: "code", Graded: 2, Confirmed: 1, Partial: 1},
		{Classification: "data", Graded: 1, Wrong: 1},
		{Classification: "unknown", Graded: 1},
	}
	if !reflect.DeepEqual(r.ByClassification, want) {
		t.Errorf("byClassification = %+v, want %+v", r.ByClassification, want)
	}
}

func TestReportRowsNeverNil(t *testing.T) {
	r := Report(ledgerOf(reportNow, shadowRun{key: "A", startedAgo: time.Hour}), since30())
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"denied":[]`, `"questions":[]`, `"tags":[]`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("report JSON lacks %s: %s", field, raw)
		}
	}
	empty, _ := json.Marshal(Report(nil, since30()))
	if !strings.Contains(string(empty), `"rows":[]`) || !strings.Contains(string(empty), `"byClassification":[]`) {
		t.Errorf("empty report JSON = %s", empty)
	}
}

func TestReportRunning(t *testing.T) {
	entries := []Entry{{At: reportNow, Ev: "started", Key: "A", RunID: "r1", Attempt: 1}}
	r := Report(entries, since30())
	if len(r.Rows) != 1 || r.Rows[0].Status != "running" || r.Headline.Started != 1 {
		t.Errorf("report = %+v", r)
	}
}

func TestReportMedians(t *testing.T) {
	entries := ledgerOf(reportNow,
		shadowRun{key: "A", startedAgo: 4 * time.Hour, elapsed: 100, cost: 1, grade: "confirmed", final: true, judgeCost: 0.5},
		shadowRun{key: "B", startedAgo: 3 * time.Hour, elapsed: 300, cost: 2},
		shadowRun{key: "C", startedAgo: 2 * time.Hour, elapsed: 200, cost: 3},
		shadowRun{key: "D", startedAgo: time.Hour, elapsed: 400, cost: 4},
		shadowRun{key: "E", startedAgo: 30 * time.Minute, elapsed: 9000, cost: 0.5, status: "failed"},
	)
	h := Report(entries, since30()).Headline
	if !near(h.MedianElapsedSec, 250) {
		t.Errorf("median elapsed = %v, want 250 (completed runs only)", h.MedianElapsedSec)
	}
	if !near(h.CostUSD, 11) {
		t.Errorf("cost = %v, want 11 including the judge", h.CostUSD)
	}
	if !near(h.MedianCostUSD, 2) {
		t.Errorf("median cost = %v, want 2", h.MedianCostUSD)
	}
}

func TestReportCostSumsAttemptsAndGrades(t *testing.T) {
	entries := []Entry{
		{At: reportNow.Add(-3 * time.Hour), Ev: "started", Key: "A", RunID: "r1", Attempt: 1},
		{At: reportNow.Add(-170 * time.Minute), Ev: "finished", Key: "A", RunID: "r1", Status: "failed", CostUSD: 0.5},
		{At: reportNow.Add(-2 * time.Hour), Ev: "started", Key: "A", RunID: "r2", Attempt: 2},
		{At: reportNow.Add(-time.Hour), Ev: "finished", Key: "A", RunID: "r2", Status: "completed", CostUSD: 1},
		{At: reportNow.Add(-50 * time.Minute), Ev: "graded", Key: "A", ShadowRunID: "r2", Grade: "partial", CostUSD: 0.1},
		{At: reportNow.Add(-40 * time.Minute), Ev: "graded", Key: "A", ShadowRunID: "r2", Grade: "confirmed", Final: true, CostUSD: 0.2},
	}
	r := Report(entries, since30())
	row := r.Rows[0]
	if !near(row.CostUSD, 1.8) || row.RunID != "r2" || row.Grade != "confirmed" || !row.Final {
		t.Errorf("row = %+v", row)
	}
}

func TestReportLead(t *testing.T) {
	plus, minus := 47.0, -5.0
	entries := ledgerOf(reportNow,
		shadowRun{key: "A", startedAgo: 3 * time.Hour, grade: "confirmed", final: true, lead: &plus},
		shadowRun{key: "B", startedAgo: 2 * time.Hour, grade: "wrong", final: true, lead: &minus},
		shadowRun{key: "C", startedAgo: time.Hour, grade: "partial", final: true},
	)
	h := Report(entries, since30()).Headline
	if h.ReadyBefore != 1 || h.ReadyOf != 2 {
		t.Errorf("ready = %d of %d, want 1 of 2", h.ReadyBefore, h.ReadyOf)
	}
}

func TestReportOperatorFirstExcluded(t *testing.T) {
	entries := ledgerOf(reportNow,
		shadowRun{key: "A", startedAgo: 2 * time.Hour, grade: "wrong", final: true, cost: 1},
		shadowRun{key: "B", startedAgo: time.Hour, grade: "confirmed", final: true, cost: 5, operatorFirst: true},
	)
	r := Report(entries, since30())
	h := r.Headline
	if h.Started != 1 || h.Graded != 1 || h.Confirmed != 0 || !near(h.CostUSD, 1) {
		t.Errorf("headline = %+v, want the operator-first row left out", h)
	}
	if len(r.Rows) != 2 || !reflect.DeepEqual(r.Rows[0].Tags, []string{"operator first"}) {
		t.Errorf("rows = %+v", r.Rows)
	}
}

func TestReportSince(t *testing.T) {
	entries := ledgerOf(reportNow,
		shadowRun{key: "OLD", startedAgo: 40 * 24 * time.Hour, grade: "confirmed", final: true},
		shadowRun{key: "NEW", startedAgo: time.Hour},
	)
	r := Report(entries, since30())
	if len(r.Rows) != 1 || r.Rows[0].Key != "NEW" || r.Headline.Started != 1 {
		t.Errorf("rows = %+v", r.Rows)
	}
	if r.Since != since30().Format(time.RFC3339) {
		t.Errorf("since = %q", r.Since)
	}
}

func TestFirstSentence(t *testing.T) {
	cases := map[string]string{
		"One. Two.":              "One.",
		"No stop at all":         "No stop at all",
		"Version 2.5 broke it":   "Version 2.5 broke it",
		"Line one\nline two":     "Line one",
		strings.Repeat("a", 200): strings.Repeat("a", 160),
	}
	for in, want := range cases {
		if got := firstSentence(in); got != want {
			t.Errorf("firstSentence(%q) = %q, want %q", in, got, want)
		}
	}
}
