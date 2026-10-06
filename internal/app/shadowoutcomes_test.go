package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/shadow"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// gradeJudge replaces shadowJudge for the test. answer is what every call
// returns; err, when set, makes every call fail after spending cost.
type gradeJudge struct {
	mu     sync.Mutex
	calls  int
	answer string
	cost   float64
	err    error
}

func (j *gradeJudge) install(t *testing.T) {
	t.Helper()
	prev := shadowJudge
	shadowJudge = func(ctx context.Context, deps runner.Deps, model, prompt string, schema []byte) (json.RawMessage, float64, error) {
		j.mu.Lock()
		defer j.mu.Unlock()
		j.calls++
		if j.err != nil {
			return nil, j.cost, j.err
		}
		return json.RawMessage(j.answer), j.cost, nil
	}
	t.Cleanup(func() { shadowJudge = prev })
}

func (j *gradeJudge) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.calls
}

const confirmedAnswer = `{"verdict":"confirmed","sameRootCause":true,"reason":"same cause"}`

// shadowRunID is the finished shadow run every collection test grades.
const shadowRunID = "20261006T060000Z-sh01"

// finishedShadow puts a finished shadow run of key in the ledger, with its
// result.json on disk, finished at finishedAt.
func finishedShadow(t *testing.T, f *shadowFixture, key string, finishedAt time.Time) {
	t.Helper()
	started := finishedAt.Add(-10 * time.Minute)
	writeShadowRun(t, f.root, key, shadowRunID, store.State{Status: store.StatusCompleted, StartedAt: started, UpdatedAt: finishedAt}, triageDoc)
	f.append(t, shadow.Entry{At: started, Ev: "seen", Key: key, Title: "Export fails", Type: "bug"})
	f.append(t, shadow.Entry{At: started, Ev: "started", Key: key, RunID: shadowRunID, Attempt: 1})
	v, ok := shadow.VerdictOf(filepath.Join(f.root, ".sirdar", "runs", key, shadowRunID))
	if !ok {
		t.Fatal("the shadow run's result.json has no verdict")
	}
	f.append(t, shadow.Entry{At: finishedAt, Ev: "finished", Key: key, RunID: shadowRunID, Status: "completed", CostUSD: 0.5, Verdict: v})
}

// operatorRun writes one of the operator's own runs on key.
func operatorRun(t *testing.T, root, key, runID string, kind store.Kind, startedAt time.Time, inputs *store.RCAInputs, result string) {
	t.Helper()
	dir := filepath.Join(root, ".sirdar", "runs", key, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := store.State{
		RunID: runID, Key: key, Kind: kind, Status: store.StatusCompleted,
		Provider: "claude", StartedAt: startedAt, UpdatedAt: startedAt.Add(time.Minute), RCAInputs: inputs,
	}
	if err := (store.Run{Dir: dir}).WriteState(st); err != nil {
		t.Fatal(err)
	}
	if result != "" {
		if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(result), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *shadowFixture) collect(t *testing.T, keys ...string) []ShadowRow {
	t.Helper()
	ws, err := f.svc.reg.Find(f.wsID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f.root)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.svc.shadowCollectAndGrade(context.Background(), ws, cfg, keys)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rows
}

func gradedLines(entries []shadow.Entry) []shadow.Entry {
	var out []shadow.Entry
	for _, e := range ofEv(entries, "graded") {
		if e.Grade != "" {
			out = append(out, e)
		}
	}
	return out
}

func TestCollectGradesOperatorRCA(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer, cost: 0.03}
	judge.install(t)
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	now := f.clock.Now()
	finishedShadow(t, f, "OMNI-1", now.Add(-2*time.Hour))
	operatorRun(t, f.root, "OMNI-1", "20261006T090000Z-op01", store.KindRCA, now.Add(-time.Hour),
		&store.RCAInputs{Resolution: "Null tenant on refund"}, "")

	rows := f.collect(t)
	entries := f.ledger(t)

	outcomes := ofEv(entries, "outcome")
	if len(outcomes) != 1 || outcomes[0].Source != "operator-rca" || outcomes[0].Resolution != "Null tenant on refund" {
		t.Fatalf("outcome lines:\n%s", dumpLedger(outcomes))
	}
	graded := gradedLines(entries)
	if len(graded) != 1 {
		t.Fatalf("graded lines:\n%s", dumpLedger(entries))
	}
	g := graded[0]
	if g.Grade != "confirmed" || !g.Final || g.Basis != "resolution" || g.CostUSD != 0.03 || g.ShadowRunID != shadowRunID {
		t.Errorf("graded %+v", g)
	}
	if judge.count() != 1 {
		t.Errorf("the judge was called %d times, want 1", judge.count())
	}
	if len(rows) != 1 || rows[0].Key != "OMNI-1" || rows[0].Grade != "confirmed" {
		t.Errorf("rows %+v", rows)
	}

	// Nothing new arrived, so a second pass leaves the final grade alone.
	f.collect(t)
	if n := len(gradedLines(f.ledger(t))); n != 1 || judge.count() != 1 {
		t.Errorf("a second pass with nothing new graded again: %d lines, %d calls", n, judge.count())
	}
}

func TestCollectProvisionalThenFinal(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer, cost: 0.03}
	judge.install(t)
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	now := f.clock.Now()
	finishedShadow(t, f, "OMNI-1", now.Add(-2*time.Hour))
	operatorRun(t, f.root, "OMNI-1", "20261006T090000Z-op01", store.KindTriage, now.Add(-time.Hour), nil, triageDoc)

	f.collect(t)
	entries := f.ledger(t)
	outcomes := ofEv(entries, "outcome")
	if len(outcomes) != 1 || outcomes[0].Source != "operator-triage" {
		t.Fatalf("outcome lines:\n%s", dumpLedger(outcomes))
	}
	graded := gradedLines(entries)
	if len(graded) != 1 || graded[0].Final || graded[0].Basis != "operator-triage" {
		t.Fatalf("want one provisional grade:\n%s", dumpLedger(entries))
	}

	row, err := f.svc.shadowOutcome(context.Background(), f.wsID, "OMNI-1", nil, "Raised the export job's timeout")
	if err != nil {
		t.Fatal(err)
	}
	graded = gradedLines(f.ledger(t))
	if len(graded) != 2 || !graded[1].Final || graded[1].Grade != "confirmed" {
		t.Fatalf("want a second, final grade:\n%s", dumpLedger(graded))
	}
	if row.Key != "OMNI-1" || !row.Final {
		t.Errorf("row %+v", row)
	}
}

func TestCollectUngradable(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer}
	judge.install(t)
	f := newShadowFixture(t, shadowConfig(5, "  outcomes:\n    windowDays: 30\n"), nil, Options{})
	now := f.clock.Now()
	finishedShadow(t, f, "OMNI-1", now.Add(-31*24*time.Hour))

	f.collect(t)
	graded := gradedLines(f.ledger(t))
	if len(graded) != 1 || graded[0].Grade != "ungradable" || !graded[0].Final {
		t.Fatalf("graded:\n%s", dumpLedger(f.ledger(t)))
	}
	if judge.count() != 0 {
		t.Errorf("the judge was called %d times for a key past its window", judge.count())
	}

	// A closed key is not marked again.
	f.collect(t)
	if n := len(gradedLines(f.ledger(t))); n != 1 {
		t.Errorf("%d graded lines after a second pass, want 1", n)
	}
}

func TestJudgeFailureCappedPerDay(t *testing.T) {
	judge := &gradeJudge{err: errors.New("judge fell over"), cost: 0.02}
	judge.install(t)
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	now := f.clock.Now()
	finishedShadow(t, f, "OMNI-1", now.Add(-2*time.Hour))
	operatorRun(t, f.root, "OMNI-1", "20261006T090000Z-op01", store.KindRCA, now.Add(-time.Hour),
		&store.RCAInputs{Resolution: "Null tenant on refund"}, "")

	for i := 0; i < 4; i++ {
		f.collect(t)
	}
	if judge.count() != 3 {
		t.Errorf("the judge was called %d times in one day, want 3", judge.count())
	}
	entries := f.ledger(t)
	if g := gradedLines(entries); len(g) != 0 {
		t.Errorf("a failed judge call left a grade:\n%s", dumpLedger(g))
	}

	// Each failed call was paid for, so each left a line the daily cap counts.
	failed := ofEv(entries, "graded")
	if len(failed) != 3 {
		t.Fatalf("%d cost lines for 3 failed judge calls:\n%s", len(failed), dumpLedger(entries))
	}
	for _, e := range failed {
		if e.CostUSD != 0.02 || !strings.HasPrefix(e.Warning, "judge failed: ") || e.Final {
			t.Errorf("failed judge line %+v", e)
		}
	}
	day := shadow.Today(entries, 0, now)
	if want := 0.5 + 3*0.02; day.USD < want-1e-9 || day.USD > want+1e-9 {
		t.Errorf("today's spend $%v, want $%v counting the failed judge calls", day.USD, want)
	}
	rows := shadow.Report(entries, time.Time{}).Rows
	if len(rows) != 1 || rows[0].Grade != "" {
		t.Errorf("a failed judge call reads as a grade: %+v", rows)
	}
	if st := f.status(t); !strings.Contains(st.LastError, "judge fell over") {
		t.Errorf("lastError %q, want the judge's failure", st.LastError)
	}

	// The next day the key is judged again.
	f.clock.Add(24 * time.Hour)
	f.collect(t)
	if judge.count() != 4 {
		t.Errorf("the judge was called %d times after the day turned, want 4", judge.count())
	}
}

func TestObserveTriggersCollection(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer, cost: 0.03}
	judge.install(t)
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{Shadow: true})
	now := f.clock.Now()
	finishedShadow(t, f, "OMNI-1", now.Add(-2*time.Hour))
	operatorRun(t, f.root, "OMNI-1", "20261006T090000Z-op01", store.KindRCA, now.Add(-time.Hour),
		&store.RCAInputs{Resolution: "Null tenant on refund"}, "")

	f.svc.observe(Event{Kind: KindRunUpdated, WorkspaceID: f.wsID, Run: &RunSummary{
		RunID: "20261006T090000Z-op01", Key: "OMNI-1", Kind: "rca", Status: "completed",
	}})

	deadline := time.Now().Add(15 * time.Second)
	for len(gradedLines(f.ledger(t))) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no graded line after the operator's RCA completed:\n%s", dumpLedger(f.ledger(t)))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if g := gradedLines(f.ledger(t))[0]; g.Grade != "confirmed" || g.Key != "OMNI-1" {
		t.Errorf("graded %+v", g)
	}
}

func TestManualOutcomeBadInput(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	finishedShadow(t, f, "OMNI-1", f.clock.Now().Add(-time.Hour))
	_, err := f.svc.shadowOutcome(context.Background(), f.wsID, "OMNI-1", []string{"notaurl"}, "")
	if !errors.Is(err, ErrBadInput) {
		t.Fatalf("err = %v, want ErrBadInput", err)
	}
	_, err = f.svc.shadowOutcome(context.Background(), f.wsID, "OMNI-1", nil, "  ")
	if !errors.Is(err, ErrBadInput) {
		t.Errorf("empty outcome: err = %v, want ErrBadInput", err)
	}
	if n := len(ofEv(f.ledger(t), "outcome")); n != 0 {
		t.Errorf("a refused outcome left %d outcome lines", n)
	}
}

func TestDigestTextIsMetadataOnly(t *testing.T) {
	day := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	at := day.Add(-time.Hour)
	secret := "Customer Ali"
	v := &shadow.Verdict{Classification: "code", Hypothesis: secret + " refunds hit a null tenant"}
	entries := []shadow.Entry{
		{At: at, Ev: "seen", Key: "OMNI-1", Title: secret + " cannot refund"},
		{At: at, Ev: "started", Key: "OMNI-1", RunID: "r1", Attempt: 1},
		{At: at, Ev: "finished", Key: "OMNI-1", RunID: "r1", Status: "completed", CostUSD: 1.5, Verdict: v},
		{At: at, Ev: "outcome", Key: "OMNI-1", Source: "manual", Resolution: secret + " was refunded by hand"},
		{At: at, Ev: "graded", Key: "OMNI-1", ShadowRunID: "r1", Grade: "confirmed", Final: true, Reason: secret, CostUSD: 0.1},
		{At: at, Ev: "seen", Key: "OMNI-2", Title: secret},
		{At: at, Ev: "started", Key: "OMNI-2", RunID: "r2", Attempt: 1},
		{At: at, Ev: "finished", Key: "OMNI-2", RunID: "r2", Status: "completed", CostUSD: 0.5},
		{At: at, Ev: "graded", Key: "OMNI-2", ShadowRunID: "r2", Grade: "wrong", Final: true, CostUSD: 0.1},
		{At: at, Ev: "started", Key: "OMNI-3", RunID: "r3", Attempt: 1},
		// Yesterday's grade is not today's news.
		{At: at.Add(-24 * time.Hour), Ev: "graded", Key: "OMNI-9", ShadowRunID: "r9", Grade: "confirmed", Final: true},
	}
	text := digestText(entries, day)
	want := "Sirdar shadow, 2026-10-06: started 3, graded 2, confirmed 50%, cost $2.20\nOMNI-1 confirmed\nOMNI-2 wrong"
	if text != want {
		t.Errorf("digest:\n%s\nwant:\n%s", text, want)
	}
	if strings.Contains(text, secret) {
		t.Errorf("the digest carries ticket text: %s", text)
	}
}

// hookServer counts the posts it receives and keeps the last body.
type hookServer struct {
	mu    sync.Mutex
	posts int
	body  []byte
	srv   *httptest.Server
}

func newHookServer(t *testing.T) *hookServer {
	h := &hookServer{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		h.mu.Lock()
		h.posts++
		h.body = buf[:n]
		h.mu.Unlock()
	}))
	t.Cleanup(h.srv.Close)
	return h
}

const digestConfig = "  digest:\n    enabled: true\n    at: \"09:00\"\n    webhookUrl: env:SHADOW_HOOK\n"

func TestDigestOncePerDay(t *testing.T) {
	hook := newHookServer(t)
	t.Setenv("SHADOW_HOOK", hook.srv.URL+"/services/T/B/x")
	prev := digestHostOK
	digestHostOK = func(string) bool { return true }
	t.Cleanup(func() { digestHostOK = prev })

	f := newShadowFixture(t, shadowConfig(5, digestConfig), nil, Options{})
	f.tick(t)
	f.clock.Add(time.Minute)
	f.tick(t)

	hook.mu.Lock()
	posts, body := hook.posts, string(hook.body)
	hook.mu.Unlock()
	if posts != 1 {
		t.Fatalf("%d posts in one day, want 1", posts)
	}
	if !strings.Contains(body, `"text":"Sirdar shadow, 2026-10-06: started 0`) {
		t.Errorf("posted %s", body)
	}
	digests := ofEv(f.ledger(t), "digest")
	if len(digests) != 1 || digests[0].Day != "2026-10-06" {
		t.Errorf("digest lines:\n%s", dumpLedger(digests))
	}
}

func TestDigestRefusesOtherHost(t *testing.T) {
	t.Setenv("SHADOW_HOOK", "https://example.com/x")
	posts := 0
	prev := digestPost
	digestPost = func(ctx context.Context, url, text string) error {
		posts++
		return nil
	}
	t.Cleanup(func() { digestPost = prev })

	f := newShadowFixture(t, shadowConfig(5, digestConfig), nil, Options{})
	f.tick(t)
	if posts != 0 {
		t.Errorf("%d posts to a host that is not hooks.slack.com", posts)
	}
	if st := f.status(t); st.LastError != "shadow.digest.webhookUrl: host must be hooks.slack.com" {
		t.Errorf("lastError %q", st.LastError)
	}
	if n := len(ofEv(f.ledger(t), "digest")); n != 0 {
		t.Errorf("%d digest lines for a digest never sent", n)
	}
}
