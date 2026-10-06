package shadow

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// writeRun makes a run directory for st under root and writes its state,
// plus a result.json copied from the named fixture when one is given.
func writeRun(t *testing.T, root string, st store.State, fixture string) string {
	t.Helper()
	run, err := store.CreateID(root, st.Key, st.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.WriteState(st); err != nil {
		t.Fatal(err)
	}
	if fixture != "" {
		raw, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(run.Dir, "result.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return run.Dir
}

func TestOperatorOutcomesRCA(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, store.State{
		RunID: "20261006T100000Z-aaaa", Key: "OMNI-1", Kind: store.KindRCA,
		Status: store.StatusCompleted, StartedAt: t0.Add(time.Hour),
		RCAInputs: &store.RCAInputs{PRURL: "https://github.com/o/r/pull/912", Resolution: "Read the void flag."},
	}, "rca-result.json")

	lines, first, err := OperatorOutcomes(root, "OMNI-1", nil, t0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines = %+v, want one", lines)
	}
	l := lines[0]
	if l.Ev != "outcome" || l.Key != "OMNI-1" || l.Source != "operator-rca" || l.RunID != "20261006T100000Z-aaaa" {
		t.Errorf("line = %+v", l)
	}
	if !reflect.DeepEqual(l.PRURLs, []string{"https://github.com/o/r/pull/912"}) || l.Resolution != "Read the void flag." {
		t.Errorf("PRURLs/Resolution = %v / %q", l.PRURLs, l.Resolution)
	}
	if l.Summary != "The refund total summed voided lines because the void flag was never read." || l.Classification != "code" {
		t.Errorf("Summary/Classification = %q / %q", l.Summary, l.Classification)
	}
	if !l.At.Equal(t0.Add(2 * time.Hour)) {
		t.Errorf("At = %v", l.At)
	}
	if !first.Equal(t0.Add(time.Hour)) {
		t.Errorf("firstStart = %v", first)
	}
}

func TestOperatorOutcomesFix(t *testing.T) {
	root := t.TempDir()
	st := store.State{RunID: "20261006T110000Z-bbbb", Key: "OMNI-1", Kind: store.KindFix,
		Status: store.StatusCompleted, StartedAt: t0.Add(2 * time.Hour)}
	st.Fix.PRURL = "https://github.com/o/r/pull/7"
	writeRun(t, root, st, "")
	// A fix run that opened no pull request says nothing about the outcome.
	writeRun(t, root, store.State{RunID: "20261006T090000Z-cccc", Key: "OMNI-1", Kind: store.KindFix,
		Status: store.StatusFailed, StartedAt: t0.Add(time.Hour)}, "")

	lines, first, err := OperatorOutcomes(root, "OMNI-1", nil, t0.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Source != "operator-fix" || lines[0].RunID != st.RunID ||
		!reflect.DeepEqual(lines[0].PRURLs, []string{"https://github.com/o/r/pull/7"}) {
		t.Fatalf("lines = %+v", lines)
	}
	if !first.Equal(t0.Add(time.Hour)) {
		t.Errorf("firstStart = %v, want the failed fix run's start", first)
	}
}

func TestOperatorOutcomesTriage(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, store.State{RunID: "20261006T100000Z-dddd", Key: "OMNI-1", Kind: store.KindTriage,
		Status: store.StatusCompleted, StartedAt: t0}, "triage-result.json")
	// Neither an unfinished triage nor one with no note gives a verdict.
	writeRun(t, root, store.State{RunID: "20261006T110000Z-eeee", Key: "OMNI-1", Kind: store.KindTriage,
		Status: store.StatusRunning, StartedAt: t0.Add(time.Hour)}, "triage-result.json")
	writeRun(t, root, store.State{RunID: "20261006T120000Z-ffff", Key: "OMNI-1", Kind: store.KindTriage,
		Status: store.StatusCompleted, StartedAt: t0.Add(2 * time.Hour)}, "")

	lines, _, err := OperatorOutcomes(root, "OMNI-1", nil, t0.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Source != "operator-triage" || lines[0].RunID != "20261006T100000Z-dddd" {
		t.Fatalf("lines = %+v", lines)
	}
	v := lines[0].Verdict
	if v == nil || v.Hypothesis != "The refund calculator sums voided lines." || v.Classification != "code" ||
		!reflect.DeepEqual(v.CodeRefs, []string{"src/refund.cs:40"}) {
		t.Errorf("Verdict = %+v", v)
	}
}

func TestOperatorOutcomesSkipMeasurements(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, store.State{RunID: "20261006T080000Z-aaaa", Key: "OMNI-1", Kind: store.KindTriage,
		Status: store.StatusCompleted, StartedAt: t0.Add(-time.Hour), Shadow: true}, "triage-result.json")
	writeRun(t, root, store.State{RunID: "20261006T081000Z-bbbb", Key: "OMNI-1", Kind: store.KindRCA,
		Status: store.StatusCompleted, StartedAt: t0.Add(-2 * time.Hour), Eval: true,
		RCAInputs: &store.RCAInputs{PRURL: "https://github.com/o/r/pull/1"}}, "rca-result.json")

	lines, first, err := OperatorOutcomes(root, "OMNI-1", nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 || !first.IsZero() {
		t.Fatalf("lines = %+v, firstStart = %v; want none and zero", lines, first)
	}

	writeRun(t, root, store.State{RunID: "20261006T100000Z-cccc", Key: "OMNI-1", Kind: store.KindTriage,
		Status: store.StatusFailed, StartedAt: t0.Add(time.Hour)}, "")
	_, first, err = OperatorOutcomes(root, "OMNI-1", nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(t0.Add(time.Hour)) {
		t.Errorf("firstStart = %v, want the operator run's start, not a measurement's", first)
	}
}

func TestOperatorOutcomesDedupe(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, store.State{RunID: "20261006T100000Z-aaaa", Key: "OMNI-1", Kind: store.KindRCA,
		Status: store.StatusCompleted, StartedAt: t0,
		RCAInputs: &store.RCAInputs{PRURL: "https://github.com/o/r/pull/912", Resolution: "x"}}, "rca-result.json")
	writeRun(t, root, store.State{RunID: "20261006T110000Z-bbbb", Key: "OMNI-1", Kind: store.KindTriage,
		Status: store.StatusCompleted, StartedAt: t0.Add(time.Hour)}, "triage-result.json")

	first, _, err := OperatorOutcomes(root, "OMNI-1", nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("first call = %+v, want two lines", first)
	}
	second, _, err := OperatorOutcomes(root, "OMNI-1", first, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("second call = %+v, want none", second)
	}
}

func TestManualOutcome(t *testing.T) {
	e, err := ManualOutcome("OMNI-1", []string{"https://github.com/o/r/pull/3"}, "  Fixed the mapper.  ", t0)
	if err != nil {
		t.Fatal(err)
	}
	if e.Ev != "outcome" || e.Key != "OMNI-1" || e.Source != "manual" || e.Resolution != "Fixed the mapper." ||
		!reflect.DeepEqual(e.PRURLs, []string{"https://github.com/o/r/pull/3"}) || !e.At.Equal(t0) {
		t.Errorf("entry = %+v", e)
	}

	if _, err := ManualOutcome("OMNI-1", nil, "only a resolution", t0); err != nil {
		t.Errorf("resolution alone: %v", err)
	}
	if _, err := ManualOutcome("OMNI-1", []string{"notaurl"}, "", t0); err == nil {
		t.Error("--pr notaurl: no error")
	}
	_, err = ManualOutcome("OMNI-1", nil, "   ", t0)
	if err == nil || err.Error() != "shadow: an outcome needs a --pr URL or a resolution" {
		t.Errorf("neither PR nor resolution: err = %v", err)
	}

	long, err := ManualOutcome("OMNI-1", nil, strings.Repeat("é", 9000), t0)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(long.Resolution)); n != 8000 {
		t.Errorf("resolution is %d runes, want 8000", n)
	}
}

type stubOutcomer struct {
	out   ticket.Outcome
	err   error
	calls int
}

func (s *stubOutcomer) Outcome(ctx context.Context, key string) (ticket.Outcome, error) {
	s.calls++
	return s.out, s.err
}

func TestTrackerOutcomeUnsupported(t *testing.T) {
	o := &stubOutcomer{err: &source.Error{Code: source.Unsupported, Message: "no"}}
	_, ok, err := TrackerOutcome(context.Background(), o, "OMNI-1", nil, t0)
	if ok || err != nil {
		t.Errorf("unsupported: ok=%v err=%v, want false nil", ok, err)
	}
	o = &stubOutcomer{err: &source.Error{Code: source.NotFound, Message: "gone"}}
	_, ok, err = TrackerOutcome(context.Background(), o, "OMNI-1", nil, t0)
	if ok || err == nil {
		t.Errorf("not found: ok=%v err=%v, want false and an error", ok, err)
	}
}

func TestTrackerOutcomeNew(t *testing.T) {
	resolved := t0.Add(-time.Hour)
	o := &stubOutcomer{out: ticket.Outcome{Key: "OMNI-1", Status: "Done", Resolution: "Patched the mapper.",
		ResolvedAt: resolved, PullRequests: []string{"https://github.com/o/r/pull/9", "https://github.com/o/r/pull/8"}}}
	line, ok, err := TrackerOutcome(context.Background(), o, "OMNI-1", nil, t0)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if line.Ev != "outcome" || line.Key != "OMNI-1" || line.Source != "tracker" || line.Status != "Done" ||
		line.Resolution != "Patched the mapper." || line.ResolvedAt == nil || !line.ResolvedAt.Equal(resolved) ||
		!reflect.DeepEqual(line.PRURLs, o.out.PullRequests) || !line.At.Equal(t0) {
		t.Errorf("line = %+v", line)
	}

	open := &stubOutcomer{out: ticket.Outcome{Key: "OMNI-1", Status: "In Progress"}}
	line, ok, err = TrackerOutcome(context.Background(), open, "OMNI-1", nil, t0)
	if err != nil || !ok || line.ResolvedAt != nil || line.Status != "In Progress" {
		t.Errorf("open record: line=%+v ok=%v err=%v", line, ok, err)
	}
}

func TestTrackerOutcomeDedupe(t *testing.T) {
	o := &stubOutcomer{out: ticket.Outcome{Key: "OMNI-1", Status: "Done",
		PullRequests: []string{"https://github.com/o/r/pull/9", "https://github.com/o/r/pull/8"}}}
	line, ok, err := TrackerOutcome(context.Background(), o, "OMNI-1", nil, t0)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	// The same PRs listed in another order are the same observation.
	o.out.PullRequests = []string{"https://github.com/o/r/pull/8", "https://github.com/o/r/pull/9"}
	if _, ok, err := TrackerOutcome(context.Background(), o, "OMNI-1", []Entry{line}, t0.Add(time.Hour)); ok || err != nil {
		t.Errorf("repeat: ok=%v err=%v, want false nil", ok, err)
	}
	// A status change is a new observation.
	o.out.Status = "Closed"
	if _, ok, err := TrackerOutcome(context.Background(), o, "OMNI-1", []Entry{line}, t0.Add(time.Hour)); !ok || err != nil {
		t.Errorf("status change: ok=%v err=%v, want true nil", ok, err)
	}
}

func TestOperatorOutcomesTriageListsCapped(t *testing.T) {
	root := t.TempDir()
	dir := writeRun(t, root, store.State{RunID: "20261006T100000Z-dddd", Key: "OMNI-1", Kind: store.KindTriage,
		Status: store.StatusCompleted, StartedAt: t0}, "")
	refs := make([]string, 100)
	for i := range refs {
		refs[i] = `"` + strings.Repeat("r", 5000) + `"`
	}
	doc := `{"classification":"code","rootCause":{"hypothesis":"h","confidence":"low","codeRefs":[` + strings.Join(refs, ",") +
		`]},"openQuestions":[` + strings.Join(refs, ",") + `]}`
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, _, err := OperatorOutcomes(root, "OMNI-1", nil, t0)
	if err != nil || len(lines) != 1 {
		t.Fatalf("lines = %d, err = %v", len(lines), err)
	}
	if err := Append(root, lines[0]); err != nil {
		t.Errorf("the capped line still does not fit the ledger: %v", err)
	}
}

// TestManualOutcomeCapsThePRList: a manual outcome is one ledger line, so
// its URLs are bounded in number and length rather than left to push the
// line past the ledger's size limit.
func TestManualOutcomeCapsThePRList(t *testing.T) {
	pr := func(i int) string { return "https://github.com/o/r/pull/" + strings.Repeat("1", i%3+1) }
	ten := make([]string, MaxManualPRs)
	for i := range ten {
		ten[i] = pr(i)
	}
	if _, err := ManualOutcome("OMNI-1", ten, "", t0); err != nil {
		t.Fatalf("%d URLs: %v", MaxManualPRs, err)
	}
	if _, err := ManualOutcome("OMNI-1", append(ten, pr(0)), "", t0); err == nil || !strings.Contains(err.Error(), "at most 10") {
		t.Errorf("%d URLs: err = %v", MaxManualPRs+1, err)
	}

	base := "https://github.com/o/r/pull/1?"
	fits := base + strings.Repeat("a", MaxManualPRURLLen-len(base))
	if _, err := ManualOutcome("OMNI-1", []string{fits}, "", t0); err != nil {
		t.Fatalf("a %d-character URL: %v", len(fits), err)
	}
	if _, err := ManualOutcome("OMNI-1", []string{fits + "a"}, "", t0); err == nil || !strings.Contains(err.Error(), "2000") {
		t.Errorf("a %d-character URL: err = %v", len(fits)+1, err)
	}
}
