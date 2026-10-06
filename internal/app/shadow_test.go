package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/shadow"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// shadowClock is a settable clock for the watcher's view of time. The
// runner keeps its own clock, so only the ledger, the gate and the poll
// schedule move with it.
type shadowClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *shadowClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *shadowClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// shadowTracker is a tracker whose list and errors a test can change
// between ticks.
type shadowTracker struct {
	mu        sync.Mutex
	list      []ticket.TrackerTicket
	errs      []error // one is handed out per List call, oldest first
	noCreated map[string]bool
}

func (s *shadowTracker) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tt := *sampleBundle().Tracker
	tt.Key = key
	if s.noCreated[key] {
		tt.CreatedAt = time.Time{}
	}
	return tt, nil
}

func (s *shadowTracker) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return nil, err
	}
	return append([]ticket.TrackerTicket(nil), s.list...), nil
}

func (s *shadowTracker) set(list ...ticket.TrackerTicket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = list
}

// shadowConfig is the test workspace's config with a tracker, a budget
// and a shadow block. extra is indented under shadow:.
func shadowConfig(maxUSD float64, extra string) string {
	body := strings.Replace(configYAML, "maxUsd: 5", fmt.Sprintf("maxUsd: %v", maxUSD), 1)
	return body + `sources:
  tracker:
    adapter: exec
    command: /bin/true
shadow:
  enabled: true
` + extra
}

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	path := filepath.Join(root, ".sirdar", "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(root); err != nil {
		t.Fatalf("config does not load: %v", err)
	}
}

type shadowFixture struct {
	root  string
	wsID  string
	svc   *Service
	clock *shadowClock
	tr    *shadowTracker
	p     *stubProvider
}

func newShadowFixture(t *testing.T, cfgBody string, p *stubProvider, opts Options) *shadowFixture {
	t.Helper()
	root := newWorkspace(t)
	writeConfig(t, root, cfgBody)
	clock := &shadowClock{t: time.Date(2026, 10, 6, 10, 0, 0, 0, time.Local)}
	tr := &shadowTracker{}
	if p == nil {
		p = &stubProvider{script: completes()}
	}
	opts.Interval = 20 * time.Millisecond
	opts.Now = clock.Now
	svc := New(newRegistry(t, root), stubBuilder(p, tr, stubHelpdesk{}), opts)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	t.Cleanup(svc.Stop)
	return &shadowFixture{root: root, wsID: WorkspaceID(root), svc: svc, clock: clock, tr: tr, p: p}
}

// completes is a provider script whose every run answers and files a note.
func completes() func(provider.SessionSpec, *stubSession) {
	return replyThenNote([]provider.Event{
		{Kind: provider.EvUsage, Turns: 3, CostUSD: 0.42},
		{Kind: provider.EvFinal, Text: "The export job times out."},
	}, finalEvent(triageDoc))
}

func (f *shadowFixture) tick(t *testing.T) {
	t.Helper()
	ws, err := f.svc.reg.Find(f.wsID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f.root)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.svc.shadowTick(context.Background(), ws, cfg)
}

func (f *shadowFixture) ledger(t *testing.T) []shadow.Entry {
	t.Helper()
	entries, err := shadow.Read(f.root)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func (f *shadowFixture) append(t *testing.T, e shadow.Entry) {
	t.Helper()
	if err := shadow.Append(f.root, e); err != nil {
		t.Fatal(err)
	}
}

// waitLedger waits until the ledger holds n lines of event ev.
func (f *shadowFixture) waitLedger(t *testing.T, ev string, n int) []shadow.Entry {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		entries := f.ledger(t)
		if len(ofEv(entries, ev)) >= n {
			return entries
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d %s lines; ledger: %s", n, ev, dumpLedger(entries))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitIdle waits until no shadow run of this workspace is in flight.
func (f *shadowFixture) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for f.svc.gate.InFlight(f.wsID, ActionShadowTriage) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("shadow runs still in flight")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *shadowFixture) status(t *testing.T) ShadowStatus {
	t.Helper()
	st, err := f.svc.shadowStatus(f.wsID)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func ofEv(entries []shadow.Entry, ev string) []shadow.Entry {
	var out []shadow.Entry
	for _, e := range entries {
		if e.Ev == ev {
			out = append(out, e)
		}
	}
	return out
}

func dumpLedger(entries []shadow.Entry) string {
	var b strings.Builder
	for _, e := range entries {
		raw, _ := json.Marshal(e)
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

func bug(key string, created time.Time) ticket.TrackerTicket {
	return ticket.TrackerTicket{Key: key, Title: "Export fails", Type: "bug", Status: "Open", CreatedAt: created}
}

func TestShadowClonedBudget(t *testing.T) {
	var mu sync.Mutex
	var budgets []float64
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		mu.Lock()
		budgets = append(budgets, spec.Budget.MaxUSD)
		mu.Unlock()
		completes()(spec, s)
	}}
	f := newShadowFixture(t, shadowConfig(8, "  maxUSDPerDay: 10\n"), p, Options{})
	now := f.clock.Now()
	f.append(t, shadow.Entry{At: now.Add(-time.Hour), Ev: "finished", Key: "OMNI-9", RunID: "r9", Status: "completed", CostUSD: 7})

	f.tick(t) // arms
	f.clock.Add(time.Minute)
	f.tr.set(bug("OMNI-1", f.clock.Now()))
	f.tick(t)
	f.waitLedger(t, "finished", 2)
	f.waitIdle(t)

	mu.Lock()
	defer mu.Unlock()
	if len(budgets) == 0 {
		t.Fatal("no session started")
	}
	if budgets[0] != 3 {
		t.Errorf("session budget $%v, want $3 (the $10 day less $7 spent)", budgets[0])
	}
}

func TestShadowTickStartsNewTicket(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	f.tick(t)
	if len(ofEv(f.ledger(t), "armed")) != 1 {
		t.Fatalf("the first tick did not arm: %s", dumpLedger(f.ledger(t)))
	}

	f.clock.Add(time.Minute)
	f.tr.set(bug("OMNI-1", f.clock.Now()))
	f.tick(t)
	entries := f.waitLedger(t, "finished", 1)
	f.waitIdle(t)

	for _, ev := range []string{"armed", "seen", "started", "finished"} {
		if n := len(ofEv(entries, ev)); n != 1 {
			t.Errorf("%d %s lines, want 1:\n%s", n, ev, dumpLedger(entries))
		}
	}
	started := ofEv(entries, "started")[0]
	if started.Key != "OMNI-1" || started.Attempt != 1 || started.RunID == "" {
		t.Errorf("started %+v", started)
	}
	fin := ofEv(entries, "finished")[0]
	if fin.Status != "completed" || fin.Verdict == nil || fin.RunID != started.RunID {
		t.Errorf("finished %+v", fin)
	}

	_, st, err := store.Open(f.root, started.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Shadow {
		t.Error("the run's state is not marked shadow")
	}
	if names, _ := os.ReadDir(filepath.Join(f.root, "notes")); len(names) > 0 {
		t.Errorf("a shadow run wrote into the notes dir: %v", names)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".sirdar", "register.jsonl")); !os.IsNotExist(err) {
		t.Errorf("a shadow run touched the register: %v", err)
	}
}

func TestShadowTickYields(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	f.tick(t)
	writeTriageState(t, f.root, "OMNI-7", "20261006T090000Z-op01", store.StatusRunning, f.clock.Now(), false)
	f.clock.Add(time.Minute)
	f.tr.set(bug("OMNI-1", f.clock.Now()))
	f.tick(t)

	st := f.status(t)
	if st.State != "gated" || !strings.HasPrefix(st.Reason, "deferring to your run") {
		t.Errorf("status %s %q, want gated deferring to your run", st.State, st.Reason)
	}
	if n := len(ofEv(f.ledger(t), "started")); n != 0 {
		t.Errorf("%d runs started while the operator's run was going", n)
	}
}

func TestShadowTickQuotaGate(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	resets := time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)
	f.svc.quota.mu.Lock()
	f.svc.quota.newest["claude"] = Quota{Provider: "claude", SevenDay: &QuotaWindow{Utilization: 0.88, ResetsAt: wireTime(resets)}}
	f.svc.quota.mu.Unlock()
	f.tr.set(bug("OMNI-1", f.clock.Now().Add(time.Minute)))
	f.tick(t)

	st := f.status(t)
	want := "seven-day quota at 88% (pause at 85%), resets 2026-10-09 14:00"
	if st.State != "paused" || st.Reason != want {
		t.Errorf("status %s %q, want paused %q", st.State, st.Reason, want)
	}
	if n := len(ofEv(f.ledger(t), "started")); n != 0 {
		t.Errorf("%d runs started over the quota", n)
	}
}

func TestShadowTickPaused(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	f.tick(t)
	until := f.clock.Now().Add(time.Hour)
	f.append(t, shadow.Entry{At: f.clock.Now(), Ev: "paused", Until: &until, Reason: "paused by the operator"})
	f.clock.Add(time.Minute)
	f.tr.set(bug("OMNI-1", f.clock.Now()))
	f.tick(t)
	if st := f.status(t); st.State != "paused" {
		t.Errorf("state %s while paused", st.State)
	}
	if n := len(ofEv(f.ledger(t), "started")); n != 0 {
		t.Fatalf("%d runs started while paused", n)
	}

	if _, err := f.svc.shadowResume(f.wsID); err != nil {
		t.Fatal(err)
	}
	f.tick(t)
	f.waitLedger(t, "finished", 1)
	f.waitIdle(t)
	if n := len(ofEv(f.ledger(t), "started")); n != 1 {
		t.Errorf("%d runs started after resume, want 1", n)
	}
}

// askThen is a script whose first `asks` sessions ask a question in words,
// and whose later ones complete.
func askThen(asks int, prompts *[]string, mu *sync.Mutex) func(provider.SessionSpec, *stubSession) {
	n := 0
	return func(spec provider.SessionSpec, s *stubSession) {
		mu.Lock()
		n++
		i := n
		*prompts = append(*prompts, spec.Prompt)
		mu.Unlock()
		if i <= asks {
			replay(provider.Event{Kind: provider.EvQuestion, Text: fmt.Sprintf("Which tenant? (%d)", i)})(spec, s)
			return
		}
		completes()(spec, s)
	}
}

func TestShadowAutoAnswer(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	f := newShadowFixture(t, shadowConfig(5, ""), &stubProvider{script: askThen(1, &prompts, &mu)}, Options{})
	f.tick(t)
	f.clock.Add(time.Minute)
	f.tr.set(bug("OMNI-1", f.clock.Now()))
	f.tick(t)
	entries := f.waitLedger(t, "finished", 1)
	f.waitIdle(t)

	if n := len(ofEv(entries, "started")); n != 1 {
		t.Errorf("%d started lines, want 1", n)
	}
	fin := ofEv(entries, "finished")[0]
	if fin.Status != "completed" {
		t.Errorf("finished %s %q, want completed", fin.Status, fin.Reason)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) < 2 || prompts[1] != shadowNoAnswer {
		t.Errorf("the resume's first prompt is %q, want the fixed no-answer text", prompts)
	}
}

func TestShadowAskedTwice(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	f := newShadowFixture(t, shadowConfig(5, ""), &stubProvider{script: askThen(2, &prompts, &mu)}, Options{})
	f.tick(t)
	f.clock.Add(time.Minute)
	f.tr.set(bug("OMNI-1", f.clock.Now()))
	f.tick(t)
	entries := f.waitLedger(t, "finished", 1)
	f.waitIdle(t)

	fin := ofEv(entries, "finished")[0]
	if fin.Status != "blocked" || fin.Reason != "asked twice" {
		t.Errorf("finished %s %q, want blocked asked twice", fin.Status, fin.Reason)
	}
	if len(fin.Questions) != 2 {
		t.Errorf("questions %q", fin.Questions)
	}
}

func TestShadowRateLimitPauses(t *testing.T) {
	var mu sync.Mutex
	n := 0
	var resets time.Time
	p := &stubProvider{}
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if first {
			replay(provider.Event{Kind: provider.EvRateLimited, ResetsAt: resets})(spec, s)
			return
		}
		completes()(spec, s)
	}
	f := newShadowFixture(t, shadowConfig(5, ""), p, Options{})
	resets = f.clock.Now().Add(2 * time.Hour).Truncate(time.Second)
	f.tick(t)
	f.clock.Add(time.Minute)
	f.tr.set(bug("OMNI-1", f.clock.Now()))
	f.tick(t)
	entries := f.waitLedger(t, "paused", 1)
	f.waitIdle(t)

	paused := ofEv(entries, "paused")[0]
	if paused.Until == nil || !paused.Until.Equal(resets) || paused.Reason != "rate limited" {
		t.Fatalf("paused %+v, want until %s", paused, resets)
	}
	first := ofEv(entries, "started")[0]

	f.clock.Add(time.Hour)
	f.tick(t)
	if n := len(ofEv(f.ledger(t), "started")); n != 1 {
		t.Fatalf("%d started lines inside the pause, want 1", n)
	}

	f.clock.Add(time.Hour + time.Minute)
	f.tick(t)
	entries = f.waitLedger(t, "finished", 2)
	f.waitIdle(t)
	starts := ofEv(entries, "started")
	if len(starts) != 2 {
		t.Fatalf("%d started lines after the pause, want 2:\n%s", len(starts), dumpLedger(entries))
	}
	retry := starts[1]
	if retry.Attempt != 2 || !retry.Resume || retry.RunID != first.RunID {
		t.Errorf("retry %+v, want attempt 2 resuming %s", retry, first.RunID)
	}
}

func TestShadowRecoverInterrupted(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	now := f.clock.Now()
	const runID = "20261006T060000Z-dead"
	created := now.Add(-time.Hour)
	f.append(t, shadow.Entry{At: now.Add(-2 * time.Hour), Ev: "armed", Since: &created})
	f.append(t, shadow.Entry{At: now.Add(-time.Hour), Ev: "seen", Key: "OMNI-1", Type: "bug", CreatedAt: &created})
	f.append(t, shadow.Entry{At: now.Add(-time.Hour), Ev: "started", Key: "OMNI-1", RunID: runID, Attempt: 1})
	writeTriageState(t, f.root, "OMNI-1", runID, store.StatusRunning, now.Add(-time.Hour), true)
	f.tr.set(bug("OMNI-1", created))

	f.tick(t)
	_, st, err := store.Open(f.root, runID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != store.StatusBlocked || st.Reason != "interrupted" {
		t.Errorf("recovered state %s %q, want blocked interrupted", st.Status, st.Reason)
	}
	entries := f.ledger(t)
	fins := ofEv(entries, "finished")
	if len(fins) != 1 || fins[0].Status != "blocked" || fins[0].Reason != "interrupted" {
		t.Fatalf("finished lines %s", dumpLedger(fins))
	}
	if n := len(ofEv(entries, "started")); n != 1 {
		t.Fatalf("retried before an interval passed: %d started", n)
	}

	f.clock.Add(16 * time.Minute)
	f.tick(t)
	entries = f.waitLedger(t, "finished", 2)
	f.waitIdle(t)
	starts := ofEv(entries, "started")
	if len(starts) != 2 || starts[1].Attempt != 2 {
		t.Fatalf("started lines %s", dumpLedger(starts))
	}

	// One retry is all a ticket gets.
	f.clock.Add(time.Hour)
	f.tick(t)
	if n := len(ofEv(f.ledger(t), "started")); n != 2 {
		t.Errorf("%d started lines after the retry, want 2", n)
	}
}

func writeShadowLock(t *testing.T, root string, pid int, by string, mtime time.Time) {
	t.Helper()
	host, _ := os.Hostname()
	raw, _ := json.Marshal(shadowLockInfo{PID: pid, Host: host, By: by, StartedAt: mtime.Format(time.RFC3339)})
	path := shadowLockPath(root)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func readShadowLockFile(t *testing.T, root string) (shadowLockInfo, bool) {
	t.Helper()
	raw, err := os.ReadFile(shadowLockPath(root))
	if os.IsNotExist(err) {
		return shadowLockInfo{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	// A lock created a moment ago may not have its contents yet.
	var info shadowLockInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return shadowLockInfo{}, false
	}
	return info, true
}

func TestShadowLockTakeover(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	writeShadowLock(t, f.root, 999999, "serve", f.clock.Now())
	f.svc.shadowSupervisePass(context.Background())

	info, ok := readShadowLockFile(t, f.root)
	if !ok || info.PID != os.Getpid() {
		t.Fatalf("lock %+v (present %v), want this process's", info, ok)
	}
	if st := f.status(t); st.State != "watching" {
		t.Errorf("state %s %q, want watching", st.State, st.Reason)
	}
}

func TestShadowLockedElsewhere(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	ppid := os.Getppid()
	writeShadowLock(t, f.root, ppid, "serve", f.clock.Now())

	f.svc.shadowSupervisePass(context.Background())
	st := f.status(t)
	want := fmt.Sprintf("shadow: watched by pid %d (serve)", ppid)
	if st.State != "locked-elsewhere" || st.Reason != want {
		t.Errorf("status %s %q, want locked-elsewhere %q", st.State, st.Reason, want)
	}
	if _, err := f.svc.shadowPoll(context.Background(), f.wsID); !errors.Is(err, ErrShadowLocked) {
		t.Errorf("shadowPoll = %v, want ErrShadowLocked", err)
	}
	if info, _ := readShadowLockFile(t, f.root); info.PID != ppid {
		t.Errorf("the other process's lock was replaced: %+v", info)
	}
}

func TestShadowAdapterBackoff(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	interval := 15 * time.Minute
	f.tr.errs = []error{errors.New("tracker down"), errors.New("tracker down")}
	f.tick(t)
	if st := f.status(t); st.ConsecutiveErrors != 1 || st.LastError == "" {
		t.Fatalf("after one failure: %+v", st)
	}
	f.clock.Add(2 * interval)
	f.tick(t)
	st := f.status(t)
	if st.ConsecutiveErrors != 2 {
		t.Fatalf("consecutive errors %d, want 2", st.ConsecutiveErrors)
	}
	last, _ := time.Parse(time.RFC3339Nano, st.LastPoll)
	next, _ := time.Parse(time.RFC3339Nano, st.NextPoll)
	if got := next.Sub(last); got != 4*interval {
		t.Errorf("next poll %s after the last, want %s", got, 4*interval)
	}

	f.tr.errs = []error{&source.Error{Code: source.Auth, Message: "401"}}
	f.clock.Add(4 * interval)
	f.tick(t)
	st = f.status(t)
	if st.State != "gated" || st.Reason != "tracker credentials rejected" {
		t.Errorf("status %s %q after an auth error", st.State, st.Reason)
	}
	// Held until the config changes, without asking the tracker again.
	f.tr.set(bug("OMNI-1", f.clock.Now()))
	f.clock.Add(interval)
	f.tick(t)
	if st := f.status(t); st.State != "gated" {
		t.Errorf("the auth gate lifted without a config change: %s", st.State)
	}
}

func TestShadowConfigReload(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	f.svc.shadowSupervisePass(context.Background())
	if st := f.status(t); st.State != "watching" {
		t.Fatalf("state %s %q, want watching", st.State, st.Reason)
	}
	if _, ok := readShadowLockFile(t, f.root); !ok {
		t.Fatal("no lock while watching")
	}

	writeConfig(t, f.root, strings.Replace(shadowConfig(5, ""), "enabled: true", "enabled: false", 1))
	f.svc.shadowSupervisePass(context.Background())
	if st := f.status(t); st.State != "disabled" {
		t.Errorf("state %s, want disabled", st.State)
	}
	if _, ok := readShadowLockFile(t, f.root); ok {
		t.Error("the lock outlived the disable")
	}
}

func TestShadowRunBackfill(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	f.tr.noCreated = map[string]bool{"OMNI-2": true}

	started, err := f.svc.shadowRun(context.Background(), f.wsID, []string{"OMNI-1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0].RunID == "" || started[0].Skipped != "" {
		t.Fatalf("started %+v", started)
	}
	entries := f.waitLedger(t, "finished", 1)
	f.waitIdle(t)
	st := ofEv(entries, "started")[0]
	if !st.Backfill {
		t.Errorf("started %+v, want backfill", st)
	}
	raw, err := os.ReadFile(filepath.Join(f.root, ".sirdar", "runs", "OMNI-1", st.RunID, "bundle", "manifest.json"))
	if err != nil {
		t.Fatalf("no bundle manifest: %v", err)
	}
	if !strings.Contains(string(raw), `"asOf"`) {
		t.Errorf("manifest has no cutoff: %s", raw)
	}

	started, err = f.svc.shadowRun(context.Background(), f.wsID, []string{"OMNI-2"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0].Skipped != "no creation time; cannot backfill" {
		t.Errorf("started %+v", started)
	}
}

func TestShadowStatusWithoutWatcher(t *testing.T) {
	f := newShadowFixture(t, shadowConfig(5, ""), nil, Options{})
	st := f.status(t)
	if st.State != "stopped" || st.Reason != "not watching; run sirdar serve or sirdar shadow watch" {
		t.Errorf("status %s %q, want stopped", st.State, st.Reason)
	}
	if st.HasLedger {
		t.Error("HasLedger with no ledger")
	}
	if st.InFlight == nil {
		t.Error("InFlight is nil; the wire wants []")
	}

	f.append(t, shadow.Entry{At: f.clock.Now(), Ev: "resumed", By: "operator"})
	writeConfig(t, f.root, strings.Replace(shadowConfig(5, ""), "enabled: true", "enabled: false", 1))
	st = f.status(t)
	if st.State != "disabled" || st.Enabled {
		t.Errorf("status %s enabled %v, want disabled", st.State, st.Enabled)
	}
	if !st.HasLedger {
		t.Error("HasLedger false with a ledger on disk")
	}
}

// TestShadowSupervisorLifecycle: a Service started with Options.Shadow takes
// the lock on its first supervise pass, and Stop gives it back.
func TestShadowSupervisorLifecycle(t *testing.T) {
	root := newWorkspace(t)
	writeConfig(t, root, shadowConfig(5, ""))
	svc := New(newRegistry(t, root), stubBuilder(&stubProvider{script: completes()}, &shadowTracker{}, stubHelpdesk{}),
		Options{Interval: 20 * time.Millisecond, Shadow: true, ShadowBy: "watch"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if info, ok := readShadowLockFile(t, root); ok && info.PID == os.Getpid() {
			if info.By != "watch" {
				t.Errorf("lock by %q, want watch", info.By)
			}
			break
		}
		if time.Now().After(deadline) {
			svc.Stop()
			t.Fatal("the supervisor never took the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	svc.Stop()
	if _, ok := readShadowLockFile(t, root); ok {
		t.Error("Stop left the lock behind")
	}
}
