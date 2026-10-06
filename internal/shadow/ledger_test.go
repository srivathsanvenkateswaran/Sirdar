package shadow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func TestAppendRead(t *testing.T) {
	root := t.TempDir()
	in := []Entry{
		{At: t0, Ev: "armed", Since: &t0, By: "serve"},
		{At: t0.Add(time.Minute), Ev: "seen", Key: "OMNI-1", Title: "Login fails", Type: "bug", Status: "Open"},
		{At: t0.Add(2 * time.Minute), Ev: "started", Key: "OMNI-1", RunID: "r1", Attempt: 1, Model: "m"},
	}
	for _, e := range in {
		if err := Append(root, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("read %d entries, want 3", len(got))
	}
	for i, e := range got {
		if e.V != 1 {
			t.Errorf("entry %d: v = %d, want 1", i, e.V)
		}
		if e.Ev != in[i].Ev || e.Key != in[i].Key || !e.At.Equal(in[i].At) {
			t.Errorf("entry %d = %+v, want %+v", i, e, in[i])
		}
	}
	if got[0].Since == nil || !got[0].Since.Equal(t0) || got[0].By != "serve" {
		t.Errorf("armed line lost its fields: %+v", got[0])
	}
	if got[2].RunID != "r1" || got[2].Attempt != 1 {
		t.Errorf("started line lost its fields: %+v", got[2])
	}
	if LedgerPath(root) != filepath.Join(root, ".sirdar", "shadow.jsonl") {
		t.Errorf("LedgerPath = %s", LedgerPath(root))
	}
}

func TestAppendStampsAt(t *testing.T) {
	root := t.TempDir()
	before := time.Now()
	if err := Append(root, Entry{Ev: "resumed", By: "operator"}); err != nil {
		t.Fatal(err)
	}
	got, _ := Read(root)
	if len(got) != 1 || got[0].At.Before(before.Add(-time.Second)) {
		t.Fatalf("At was not stamped: %+v", got)
	}
}

func TestReadMissing(t *testing.T) {
	got, err := Read(t.TempDir())
	if err != nil || got != nil {
		t.Fatalf("Read on a missing ledger = %v, %v; want nil, nil", got, err)
	}
}

func TestReadSkipsTornLine(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Entry{At: t0, Ev: "seen", Key: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, Entry{At: t0, Ev: "seen", Key: "B"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(LedgerPath(root), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"at":"2026`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "A" || got[1].Key != "B" {
		t.Fatalf("got %+v, want the two good lines", got)
	}
}

func TestAppendRefusesHugeLine(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Entry{At: t0, Ev: "seen", Key: "A"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(LedgerPath(root))
	err := Append(root, Entry{At: t0, Ev: "outcome", Key: "A", Resolution: strings.Repeat("x", 70000)})
	if err == nil || !strings.Contains(err.Error(), "over the 64 KiB limit") {
		t.Fatalf("err = %v, want the 64 KiB refusal", err)
	}
	after, _ := os.ReadFile(LedgerPath(root))
	if string(before) != string(after) {
		t.Fatal("the ledger changed after a refused line")
	}
}

func TestFoldSupersedesGrade(t *testing.T) {
	entries := []Entry{
		{At: t0, Ev: "seen", Key: "A", Title: "t", Type: "bug"},
		{At: t0.Add(time.Minute), Ev: "started", Key: "A", RunID: "r1", Attempt: 1},
		{At: t0.Add(2 * time.Minute), Ev: "finished", Key: "A", RunID: "r1", Status: "completed"},
		{At: t0.Add(3 * time.Minute), Ev: "graded", Key: "A", ShadowRunID: "r1", Grade: "partial", Final: false},
		{At: t0.Add(4 * time.Minute), Ev: "graded", Key: "A", ShadowRunID: "r1", Grade: "confirmed", Final: true},
	}
	tk := Fold(entries)["A"]
	if tk == nil || tk.Grade == nil {
		t.Fatalf("no grade folded: %+v", tk)
	}
	if tk.Grade.Grade != "confirmed" {
		t.Errorf("grade = %q, want confirmed", tk.Grade.Grade)
	}
	if tk.Latest() == nil || tk.Latest().Finished == nil || tk.Latest().Finished.Status != "completed" {
		t.Errorf("latest attempt = %+v", tk.Latest())
	}
	if tk.Title != "t" || !tk.SeenAt.Equal(t0) {
		t.Errorf("seen fields not folded: %+v", tk)
	}
}

// TestFoldSkipsFailedJudgeLine: a judge call that failed leaves a graded
// line with its cost and no grade. It must not stand in for the grade
// before it, and its cost still reaches the scorecard and the daily cap.
func TestFoldSkipsFailedJudgeLine(t *testing.T) {
	entries := []Entry{
		{At: t0, Ev: "seen", Key: "A"},
		{At: t0.Add(time.Minute), Ev: "started", Key: "A", RunID: "r1", Attempt: 1},
		{At: t0.Add(2 * time.Minute), Ev: "finished", Key: "A", RunID: "r1", Status: "completed"},
		{At: t0.Add(3 * time.Minute), Ev: "graded", Key: "A", ShadowRunID: "r1", Grade: "partial", Basis: "operator-triage", CostUSD: 0.03},
		{At: t0.Add(4 * time.Minute), Ev: "graded", Key: "A", ShadowRunID: "r1", Warning: "judge failed: timeout", CostUSD: 0.02},
	}
	tk := Fold(entries)["A"]
	if tk.Grade == nil || tk.Grade.Grade != "partial" {
		t.Fatalf("grade = %+v, want the partial grade before the failed call", tk.Grade)
	}
	row := Report(entries, time.Time{}).Rows[0]
	if row.CostUSD < 0.0499 || row.CostUSD > 0.0501 {
		t.Errorf("row cost $%v, want both judge calls ($0.05)", row.CostUSD)
	}
	if d := Today(entries, 0, t0); d.USD < 0.0499 || d.USD > 0.0501 {
		t.Errorf("today $%v, want both judge calls ($0.05)", d.USD)
	}
}

func TestFoldGradeFollowsLatestAttempt(t *testing.T) {
	entries := []Entry{
		{At: t0, Ev: "seen", Key: "A"},
		{At: t0.Add(time.Minute), Ev: "started", Key: "A", RunID: "r1", Attempt: 1},
		{At: t0.Add(2 * time.Minute), Ev: "finished", Key: "A", RunID: "r1", Status: "failed"},
		{At: t0.Add(3 * time.Minute), Ev: "graded", Key: "A", ShadowRunID: "r1", Grade: "no-answer", Final: true},
		{At: t0.Add(4 * time.Minute), Ev: "started", Key: "A", RunID: "r2", Attempt: 2},
	}
	tk := Fold(entries)["A"]
	if tk.Grade != nil {
		t.Errorf("grade = %+v, want nil once a second attempt started", tk.Grade)
	}
	if len(tk.Attempts) != 2 || tk.Latest().Started.RunID != "r2" || tk.Latest().Finished != nil {
		t.Errorf("attempts = %+v", tk.Attempts)
	}
}

func TestFoldNeverStarted(t *testing.T) {
	tk := Fold([]Entry{{At: t0, Ev: "seen", Key: "A"}})["A"]
	if tk.Latest() != nil {
		t.Errorf("Latest = %+v, want nil", tk.Latest())
	}
}

func TestArmed(t *testing.T) {
	if _, ok := Armed(nil); ok {
		t.Fatal("armed on an empty ledger")
	}
	later := t0.Add(-24 * time.Hour)
	since, ok := Armed([]Entry{
		{At: t0, Ev: "armed", Since: &t0},
		{At: t0.Add(time.Hour), Ev: "armed", Since: &later},
	})
	if !ok || !since.Equal(later) {
		t.Fatalf("Armed = %v, %v; want the newest armed line's since", since, ok)
	}
}

func TestPausedUntil(t *testing.T) {
	now := t0
	hour := now.Add(time.Hour)
	paused := []Entry{{At: now.Add(-time.Minute), Ev: "paused", Until: &hour, Reason: "rate limited"}}
	until, reason, ok := PausedUntil(paused, now)
	if !ok || !until.Equal(hour) || reason != "rate limited" {
		t.Errorf("PausedUntil = %v, %q, %v; want paused until +1h", until, reason, ok)
	}
	resumed := append(append([]Entry{}, paused...), Entry{At: now, Ev: "resumed", By: "operator"})
	if _, _, ok := PausedUntil(resumed, now); ok {
		t.Error("paused after a later resumed line")
	}
	past := now.Add(-time.Minute)
	if _, _, ok := PausedUntil([]Entry{{At: now.Add(-time.Hour), Ev: "paused", Until: &past}}, now); ok {
		t.Error("paused after the pause expired")
	}
}

func TestAppendAfterTornLine(t *testing.T) {
	root := t.TempDir()
	if err := Append(root, Entry{At: t0, Ev: "seen", Key: "A"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(LedgerPath(root), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"v":1,"at":"2026`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := Append(root, Entry{At: t0, Ev: "finished", Key: "A", RunID: "r1", Status: "blocked", Reason: "interrupted"}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Ev != "finished" || got[1].RunID != "r1" {
		t.Fatalf("got %+v, want the seen line and the finished line appended after the torn one", got)
	}
}

// TestPausedUntilOperatorAndLimit: the operator's newest pause sets the
// pause, and a limit's pause written after it can only lengthen it.
func TestPausedUntilOperatorAndLimit(t *testing.T) {
	now := t0
	at := func(d time.Duration) *time.Time { u := now.Add(d); return &u }
	op := func(written, d time.Duration) Entry {
		return Entry{At: now.Add(written), Ev: "paused", Until: at(d), Reason: "paused by the operator", By: "operator"}
	}
	limit := func(written, d time.Duration) Entry {
		return Entry{At: now.Add(written), Ev: "paused", Until: at(d), Reason: "rate limited", By: PauseByLimit}
	}
	cases := []struct {
		name    string
		entries []Entry
		until   time.Duration
		reason  string
	}{
		{"operator 24h then operator 1h", []Entry{op(-2*time.Minute, 24*time.Hour), op(-time.Minute, time.Hour)}, time.Hour, "paused by the operator"},
		{"operator 24h then limit 30m", []Entry{op(-2*time.Minute, 24*time.Hour), limit(-time.Minute, 30*time.Minute)}, 24 * time.Hour, "paused by the operator"},
		{"operator 1h then limit 3h", []Entry{op(-2*time.Minute, time.Hour), limit(-time.Minute, 3*time.Hour)}, 3 * time.Hour, "rate limited"},
		{"limit 3h before the operator's 1h does not count", []Entry{limit(-2*time.Minute, 3*time.Hour), op(-time.Minute, time.Hour)}, time.Hour, "paused by the operator"},
		{"an old line without by is the operator's", []Entry{op(-2*time.Minute, 24*time.Hour), {At: now.Add(-time.Minute), Ev: "paused", Until: at(time.Hour), Reason: "paused by the operator"}}, time.Hour, "paused by the operator"},
	}
	for _, c := range cases {
		until, reason, ok := PausedUntil(c.entries, now)
		if !ok || !until.Equal(now.Add(c.until)) || reason != c.reason {
			t.Errorf("%s: PausedUntil = %v, %q, %v; want +%v %q", c.name, until.Sub(now), reason, ok, c.until, c.reason)
		}
	}
	resumed := []Entry{op(-2*time.Minute, 24*time.Hour), limit(-time.Minute, 30*time.Minute), {At: now, Ev: "resumed", By: "operator"}}
	if _, _, ok := PausedUntil(resumed, now); ok {
		t.Error("paused after a later resumed line")
	}
}
