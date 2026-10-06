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
	waitWatching(t, f.svc, f.wsID)
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
		// Ungradable is listed but, as on the scorecard, is not a grade.
		{At: at.Add(-40 * 24 * time.Hour), Ev: "started", Key: "OMNI-4", RunID: "r4", Attempt: 1},
		{At: at.Add(-40 * 24 * time.Hour), Ev: "finished", Key: "OMNI-4", RunID: "r4", Status: "completed"},
		{At: at, Ev: "graded", Key: "OMNI-4", ShadowRunID: "r4", Grade: "ungradable", Final: true},
		// The day before's grade is not this day's news.
		{At: at.Add(-50 * time.Hour), Ev: "started", Key: "OMNI-9", RunID: "r9", Attempt: 1},
		{At: at.Add(-24 * time.Hour), Ev: "graded", Key: "OMNI-9", ShadowRunID: "r9", Grade: "confirmed", Final: true},
	}
	text := digestText(entries, day)
	want := "Sirdar shadow, 2026-10-06: started 3, graded 2, confirmed 50%, cost $2.20\nOMNI-1 confirmed\nOMNI-2 wrong\nOMNI-4 ungradable"
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
	h.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	// The digest goes over https only, so the test server is a TLS one
	// and the default transport is taught to trust it for this test.
	dt := http.DefaultTransport.(*http.Transport)
	prevTLS := dt.TLSClientConfig
	dt.TLSClientConfig = hook.srv.Client().Transport.(*http.Transport).TLSClientConfig
	t.Cleanup(func() { dt.TLSClientConfig = prevTLS })
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
	// Sent on the 6th, about the 5th.
	if !strings.Contains(body, `"text":"Sirdar shadow, 2026-10-05: started 0`) {
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

func TestDigestRefusesHTTP(t *testing.T) {
	t.Setenv("SHADOW_HOOK", "http://hooks.slack.com/services/T/B/x")
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
		t.Errorf("%d posts over plain http", posts)
	}
	if st := f.status(t); st.LastError != "shadow.digest.webhookUrl: must be an https URL" {
		t.Errorf("lastError %q", st.LastError)
	}
}

func TestCollectProvisionalNotRejudgedOnSameTruth(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer, cost: 0.03}
	judge.install(t)
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	now := f.clock.Now()
	finishedShadow(t, f, "OMNI-1", now.Add(-2*time.Hour))
	operatorRun(t, f.root, "OMNI-1", "20261006T090000Z-op01", store.KindTriage, now.Add(-time.Hour), nil, triageDoc)

	f.collect(t)
	f.clock.Add(6 * time.Hour)
	f.collect(t)
	if judge.count() != 1 {
		t.Errorf("the judge was called %d times on unchanged truth, want 1", judge.count())
	}
	if g := gradedLines(f.ledger(t)); len(g) != 1 || g[0].Final {
		t.Errorf("graded:\n%s", dumpLedger(g))
	}
}

func TestJudgeFailureCapReadsLedger(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer, cost: 0.03}
	judge.install(t)
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	now := f.clock.Now()
	finishedShadow(t, f, "OMNI-1", now.Add(-2*time.Hour))
	operatorRun(t, f.root, "OMNI-1", "20261006T090000Z-op01", store.KindRCA, now.Add(-time.Hour),
		&store.RCAInputs{Resolution: "Null tenant on refund"}, "")
	// Another process already failed three times today; the cap is in
	// the ledger, not in the memory of whoever failed.
	for i := 0; i < 3; i++ {
		f.append(t, shadow.Entry{At: now.Add(-time.Duration(i+1) * time.Minute), Ev: "graded", Key: "OMNI-1",
			ShadowRunID: shadowRunID, Warning: "judge failed: boom"})
	}
	f.collect(t)
	if judge.count() != 0 {
		t.Errorf("the judge was called %d times after three failures today", judge.count())
	}
}

func TestJudgeFailureWithoutCostCounts(t *testing.T) {
	judge := &gradeJudge{err: errors.New("no session")}
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
		t.Errorf("the judge was called %d times, want 3: a failure that cost nothing still counts", judge.count())
	}
}

func TestCollectUngradableWhileRetryPending(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer}
	judge.install(t)
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	finished := f.clock.Now().Add(-31 * 24 * time.Hour)
	f.append(t, shadow.Entry{At: finished.Add(-time.Minute), Ev: "started", Key: "OMNI-1", RunID: shadowRunID, Attempt: 1})
	f.append(t, shadow.Entry{At: finished, Ev: "finished", Key: "OMNI-1", RunID: shadowRunID, Status: "blocked", Reason: "interrupted"})

	f.collect(t)
	graded := gradedLines(f.ledger(t))
	if len(graded) != 1 || graded[0].Grade != "ungradable" {
		t.Fatalf("a retry that never came left the key open:\n%s", dumpLedger(f.ledger(t)))
	}
}

// newObservedService starts a service with the shadow watcher over a
// workspace already set up by prepare, the way serve starts over a
// workspace with history.
func newObservedService(t *testing.T, prepare func(root string, now time.Time)) *shadowFixture {
	t.Helper()
	root := newWorkspace(t)
	writeConfig(t, root, shadowConfig(5, ""))
	clock := &shadowClock{t: time.Date(2026, 10, 6, 10, 0, 0, 0, time.Local)}
	prepare(root, clock.Now())
	p := &stubProvider{script: completes()}
	svc := New(newRegistry(t, root), stubBuilder(p, &shadowTracker{}, stubHelpdesk{}),
		Options{Interval: 20 * time.Millisecond, Now: clock.Now, Shadow: true})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	t.Cleanup(svc.Stop)
	return &shadowFixture{root: root, wsID: WorkspaceID(root), svc: svc, clock: clock, p: p}
}

// shadowLedgerAt writes a finished shadow run of key straight to root.
func shadowLedgerAt(t *testing.T, root, key string, finishedAt time.Time) {
	t.Helper()
	f := &shadowFixture{root: root}
	finishedShadow(t, f, key, finishedAt)
}

func writeRCAState(t *testing.T, root, runID string, status store.Status, startedAt time.Time) {
	t.Helper()
	dir := filepath.Join(root, ".sirdar", "runs", "OMNI-1", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := store.State{RunID: runID, Key: "OMNI-1", Kind: store.KindRCA, Status: status, Provider: "claude",
		StartedAt: startedAt, UpdatedAt: time.Now(), RCAInputs: &store.RCAInputs{Resolution: "Null tenant on refund"}}
	if err := (store.Run{Dir: dir}).WriteState(st); err != nil {
		t.Fatal(err)
	}
}

// waitRunStatus waits until the watcher has reported runID at status.
func waitRunStatus(t *testing.T, events <-chan Event, runID string, status store.Status) {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Kind == KindRunUpdated && e.Run != nil && e.Run.RunID == runID && e.Run.Status == string(status) {
				return
			}
		case <-timeout:
			t.Fatalf("the watcher never reported %s %s", runID, status)
		}
	}
}

func TestObserveOnlyOnTransition(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer, cost: 0.03}
	judge.install(t)
	f := newObservedService(t, func(root string, now time.Time) {
		shadowLedgerAt(t, root, "OMNI-1", now.Add(-2*time.Hour))
		// The operator's RCA finished before serve started.
		writeRCAState(t, root, "20261006T090000Z-op01", store.StatusCompleted, now.Add(-time.Hour))
	})
	waitWatching(t, f.svc, f.wsID)
	events, unsubscribe := f.svc.Subscribe()
	defer unsubscribe()

	// Rewriting the finished run's state is a change on disk, not news.
	time.Sleep(100 * time.Millisecond)
	writeRCAState(t, f.root, "20261006T090000Z-op01", store.StatusCompleted, f.clock.Now().Add(-time.Hour))
	time.Sleep(300 * time.Millisecond)
	if judge.count() != 0 || len(ofEv(f.ledger(t), "outcome")) != 0 {
		t.Fatalf("a run already completed at start set off a pass: %d judge calls\n%s", judge.count(), dumpLedger(f.ledger(t)))
	}

	// A run that goes from running to completed while serve watches is.
	writeRCAState(t, f.root, "20261006T095000Z-op02", store.StatusRunning, f.clock.Now().Add(-10*time.Minute))
	waitRunStatus(t, events, "20261006T095000Z-op02", store.StatusRunning)
	writeRCAState(t, f.root, "20261006T095000Z-op02", store.StatusCompleted, f.clock.Now().Add(-10*time.Minute))
	deadline := time.Now().Add(15 * time.Second)
	for len(gradedLines(f.ledger(t))) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no pass after a live completion:\n%s", dumpLedger(f.ledger(t)))
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if judge.count() != 1 {
		t.Errorf("the judge was called %d times, want 1", judge.count())
	}
}

func TestObserveAppliesWindowAndPause(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer, cost: 0.03}
	judge.install(t)
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{Shadow: true})
	waitWatching(t, f.svc, f.wsID)
	now := f.clock.Now()
	until := now.Add(time.Hour)
	f.append(t, shadow.Entry{At: now, Ev: "paused", Until: &until, Reason: "paused by the operator"})
	finishedShadow(t, f, "OMNI-1", now.Add(-31*24*time.Hour))
	operatorRun(t, f.root, "OMNI-1", "20261006T090000Z-op01", store.KindRCA, now.Add(-time.Hour),
		&store.RCAInputs{Resolution: "Null tenant on refund"}, "")
	done := func() {
		f.svc.observe(Event{Kind: KindRunUpdated, WorkspaceID: f.wsID, Run: &RunSummary{
			RunID: "20261006T090000Z-op01", Key: "OMNI-1", Kind: "rca", Status: "completed",
		}})
	}

	done()
	time.Sleep(200 * time.Millisecond)
	if n := len(ofEv(f.ledger(t), "graded")); n != 0 {
		t.Fatalf("a pass ran while paused:\n%s", dumpLedger(f.ledger(t)))
	}

	// Once the pause lifts, the window still closes the old key unjudged.
	f.append(t, shadow.Entry{At: now, Ev: "resumed", By: "operator"})
	f.svc.shadow.mu.Lock()
	delete(f.svc.shadow.runStatus, f.wsID+"\x00OMNI-1\x0020261006T090000Z-op01")
	f.svc.shadow.mu.Unlock()
	done()
	deadline := time.Now().Add(15 * time.Second)
	for len(gradedLines(f.ledger(t))) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no line after the pause lifted:\n%s", dumpLedger(f.ledger(t)))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if g := gradedLines(f.ledger(t)); g[0].Grade != "ungradable" || judge.count() != 0 {
		t.Errorf("graded %+v after %d judge calls, want ungradable and none", g, judge.count())
	}
}

// TestAppendGradedKeepsTheCost: a judge call that answered is paid for
// whether or not its line lands. When the full line is refused it goes in
// without its warning and fractions; when even that is refused, a failed
// judge line carries the cost, so the daily cap sees the money and the
// per-day failure cap stops the call repeating on every pass.
func TestAppendGradedKeepsTheCost(t *testing.T) {
	half := &shadow.Fraction{Matched: 1, Total: 2, Score: 0.5}
	line := shadow.Entry{
		At: time.Date(2026, 10, 6, 10, 0, 0, 0, time.Local), Ev: "graded", Key: "OMNI-1", ShadowRunID: "r1",
		Grade: "confirmed", Basis: "pr", Final: true, JudgeModel: "judge", CostUSD: 0.07,
		Warning: "PR URLs dropped as invalid: x", PRFilesHit: half, CodeRefsPathOverlap: half,
	}
	refuse := func(n int) (func(string, shadow.Entry) error, *[]shadow.Entry) {
		var landed []shadow.Entry
		calls := 0
		return func(_ string, e shadow.Entry) error {
			calls++
			if calls <= n {
				return errors.New("shadow: ledger line is 70000 bytes, over the 64 KiB limit")
			}
			landed = append(landed, e)
			return nil
		}, &landed
	}

	add, landed := refuse(1)
	if err := appendGraded(add, "root", line); err != nil {
		t.Fatal(err)
	}
	if len(*landed) != 1 {
		t.Fatalf("landed %+v", *landed)
	}
	if got := (*landed)[0]; got.Grade != "confirmed" || got.CostUSD != 0.07 || got.Warning != "" || got.PRFilesHit != nil || got.CodeRefsPathOverlap != nil {
		t.Errorf("retry landed %+v, want the grade and cost without the warning and fractions", got)
	}

	add, landed = refuse(2)
	if err := appendGraded(add, "root", line); err == nil {
		t.Error("no error reported when the graded line could not land")
	}
	if len(*landed) != 1 {
		t.Fatalf("landed %+v", *landed)
	}
	got := (*landed)[0]
	if got.Grade != "" || got.CostUSD != 0.07 || got.Ev != "graded" || got.ShadowRunID != "r1" || !strings.HasPrefix(got.Warning, judgeFailedPrefix) {
		t.Errorf("fallback landed %+v, want a failed judge line with the cost", got)
	}
	if n := judgeFailsToday(*landed, "OMNI-1", line.At); n != 1 {
		t.Errorf("judgeFailsToday = %d, want the fallback counted", n)
	}
	if usd := shadow.Today(*landed, 0, line.At).USD; usd != 0.07 {
		t.Errorf("Today USD = %v, want the judge's cost", usd)
	}
}

// waitWatching waits until svc's supervisor holds the workspace's lock.
func waitWatching(t *testing.T, svc *Service, wsID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		svc.shadow.mu.Lock()
		w := svc.shadow.ws[wsID]
		held := w != nil && w.locked && w.watching
		svc.shadow.mu.Unlock()
		if held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the supervisor never took the workspace's lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestObserveOnlyInTheWatchingProcess: a second serve on the workspace
// reports locked-elsewhere, and collecting there too would append the same
// outcomes twice and pay the judge twice.
func TestObserveOnlyInTheWatchingProcess(t *testing.T) {
	judge := &gradeJudge{answer: confirmedAnswer, cost: 0.03}
	judge.install(t)
	// Everything is on disk before either process starts, so neither run
	// watcher takes the RCA for news; the test sends the completion itself.
	f := newObservedService(t, func(root string, now time.Time) {
		shadowLedgerAt(t, root, "OMNI-1", now.Add(-2*time.Hour))
		operatorRun(t, root, "OMNI-1", "20261006T090000Z-op01", store.KindRCA, now.Add(-time.Hour),
			&store.RCAInputs{Resolution: "Null tenant on refund"}, "")
	})
	waitWatching(t, f.svc, f.wsID)

	other := New(newRegistry(t, f.root), stubBuilder(f.p, &shadowTracker{}, stubHelpdesk{}),
		Options{Interval: 20 * time.Millisecond, Now: f.clock.Now, Shadow: true, ShadowBy: "second serve"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	other.Start(ctx)
	t.Cleanup(other.Stop)
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, err := other.shadowStatus(f.wsID)
		if err != nil {
			t.Fatal(err)
		}
		if st.State == "locked-elsewhere" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the second service is %s, want locked-elsewhere", st.State)
		}
		time.Sleep(5 * time.Millisecond)
	}

	other.shadow.mu.Lock()
	delete(other.shadow.runStatus, f.wsID+"\x00OMNI-1\x0020261006T090000Z-op01")
	other.shadow.mu.Unlock()
	other.observe(Event{Kind: KindRunUpdated, WorkspaceID: f.wsID, Run: &RunSummary{
		RunID: "20261006T090000Z-op01", Key: "OMNI-1", Kind: "rca", Status: "completed",
	}})
	time.Sleep(300 * time.Millisecond)
	if n := judge.count(); n != 0 || len(ofEv(f.ledger(t), "outcome")) != 0 {
		t.Fatalf("a process not watching the workspace collected: %d judge calls\n%s", n, dumpLedger(f.ledger(t)))
	}
}
