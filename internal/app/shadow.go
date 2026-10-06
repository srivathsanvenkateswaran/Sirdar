package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/shadow"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// ShadowStatus is what the shadow watcher is doing for one workspace, as
// the CLI, the API and the shadow.status event report it.
type ShadowStatus struct {
	Enabled           bool        `json:"enabled"`
	State             string      `json:"state"` // watching paused gated locked-elsewhere stopped disabled
	Reason            string      `json:"reason"`
	PausedUntil       string      `json:"pausedUntil,omitempty"`
	LastPoll          string      `json:"lastPoll,omitempty"`
	NextPoll          string      `json:"nextPoll,omitempty"`
	LastError         string      `json:"lastError,omitempty"`
	ConsecutiveErrors int         `json:"consecutiveErrors"`
	Today             ShadowToday `json:"today"`
	InFlight          []string    `json:"inFlight"` // run ids
	HasLedger         bool        `json:"hasLedger"`
	Breaches          int         `json:"breaches"` // finished lines today whose reason starts "read-only breach" or names a blind run
}

// ShadowToday is today's use of the daily caps.
type ShadowToday struct {
	Runs         int     `json:"runs"`
	MaxPerDay    int     `json:"maxPerDay"`
	USD          float64 `json:"usd"`
	MaxUSDPerDay float64 `json:"maxUSDPerDay"`
}

// ShadowStarted is what `shadow run` did with one key.
type ShadowStarted struct {
	Key     string `json:"key"`
	RunID   string `json:"runId,omitempty"`
	JobID   JobID  `json:"jobId,omitempty"`
	Skipped string `json:"skipped,omitempty"` // why it did not start
}

// ErrShadowLocked is a poll refused because another process holds the
// workspace's shadow lock. The HTTP layer answers 403.
var ErrShadowLocked = errors.New("app: shadow is watched by another process")

// shadowNoAnswer is the answer a shadow run gets to a question in words.
// Nobody is there to give a real one, and leaving the run blocked would
// measure nothing.
const shadowNoAnswer = "Nobody is available to answer. Continue with your best judgement and list this question under openQuestions."

// shadowFirstTick delays the first poll after start, so a serve that is
// restarted in a loop does not poll the tracker on every restart.
const shadowFirstTick = 30 * time.Second

// shadowSupervise is how often the supervisor rereads the registry and
// every workspace's config, which is how enabling, disabling or retuning
// shadow takes effect without a restart.
const shadowSupervise = 30 * time.Second

// shadowRatePause is how long a rate limit with no stated reset pauses
// the watcher.
const shadowRatePause = 30 * time.Minute

// shadowModelPause is how long a per-model limit pauses the watcher. A
// model limit names no reset, and an hour is long enough not to hammer it.
const shadowModelPause = time.Hour

// shadowAskedPrefix is the runner's reason prefix for a run blocked on a
// question in words (run.askedPrefix, which is unexported).
const shadowAskedPrefix = "agent asked: "

// shadowMaxText bounds one question or denied-call summary on a finished
// line, so a long one cannot push the line over the ledger's 64 KiB limit
// and lose the line that the daily caps count.
const shadowMaxText = 500

// shadowMaxItems and shadowMaxItemRunes bound a verdict's codeRefs and
// openQuestions, and the questions, on a finished line: the same caps an
// operator triage's outcome line keeps, for the same reason as above.
const (
	shadowMaxItems     = 20
	shadowMaxItemRunes = 300
)

// shadowState is this process's watcher state for every workspace, guarded
// by mu. Each workspace's ticks are serialised by its own tick mutex.
type shadowState struct {
	mu        sync.Mutex
	ws        map[string]*shadowWatch
	startedAt time.Time
	cancel    context.CancelFunc
	done      chan struct{}
}

func newShadowState() *shadowState {
	return &shadowState{ws: map[string]*shadowWatch{}}
}

// shadowWatch is one workspace's watcher state. Every field but tick is
// guarded by shadowState.mu.
type shadowWatch struct {
	tick sync.Mutex

	root     string
	locked   bool   // this process holds .sirdar/shadow.lock
	watching bool   // the supervisor holds it, rather than a one-off poll
	by       string // what the lock says holds it

	state, reason      string
	lastPoll, nextPoll time.Time
	errors             int
	lastError          string

	// authHold is set when the tracker rejected the credentials, with the
	// config file's mtime then: the watcher stays gated until it changes,
	// because asking again with the same credentials gets the same answer.
	authHold  bool
	authMtime time.Time

	inFlight  map[string]bool // run ids
	published *ShadowStatus
}

func (s *Service) shadowWatch(wsID, root string) *shadowWatch {
	s.shadow.mu.Lock()
	defer s.shadow.mu.Unlock()
	w := s.shadow.ws[wsID]
	if w == nil {
		w = &shadowWatch{inFlight: map[string]bool{}}
		s.shadow.ws[wsID] = w
	}
	w.root = root
	return w
}

// shadowBy is what this process's watcher writes as the lock's holder.
func (s *Service) shadowBy() string {
	if s.opts.ShadowBy != "" {
		return s.opts.ShadowBy
	}
	return "serve"
}

// --- status -----------------------------------------------------------

func (s *Service) shadowStatus(wsID string) (ShadowStatus, error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return ShadowStatus{}, err
	}
	return s.shadowStatusOf(ws, cfg), nil
}

// shadowStatusOf assembles the status from the ledger, the lock file and
// what this process knows. It works in a process that is not watching,
// which is how `sirdar shadow status` reports on a serve running elsewhere.
func (s *Service) shadowStatusOf(ws Workspace, cfg *config.Config) ShadowStatus {
	now := s.now()
	entries, _ := shadow.Read(ws.Root)
	w := s.shadowWatch(ws.ID, ws.Root)

	s.shadow.mu.Lock()
	st := ShadowStatus{
		Enabled:           cfg.Shadow.Enabled,
		LastPoll:          wireTime(w.lastPoll),
		NextPoll:          wireTime(w.nextPoll),
		LastError:         w.lastError,
		ConsecutiveErrors: w.errors,
		InFlight:          inFlightIDs(w),
	}
	state, reason, locked := w.state, w.reason, w.locked
	s.shadow.mu.Unlock()

	day := shadow.Today(entries, shadowInFlightUSD(ws.Root, st.InFlight), now)
	st.Today = ShadowToday{Runs: day.Runs, MaxPerDay: cfg.Shadow.MaxPerDay, USD: day.USD, MaxUSDPerDay: cfg.Shadow.MaxUSDPerDay}
	if _, err := os.Stat(shadow.LedgerPath(ws.Root)); err == nil {
		st.HasLedger = true
	}
	st.Breaches = shadowBreaches(entries, now)

	// An operator's pause is in the ledger, so every process reports it,
	// whoever is watching and whether or not shadow is enabled: the
	// operator who just paused it expects to see that it is paused.
	if until, _, paused := shadow.PausedUntil(entries, now); paused {
		st.State, st.Reason, st.PausedUntil = "paused", shadowPausedReason(until), wireTime(until)
		return st
	}
	switch {
	case !cfg.Shadow.Enabled:
		st.State, st.Reason = "disabled", "shadow.enabled is false"
	case locked && state != "":
		st.State, st.Reason = state, reason
	default:
		if info, mtime, ok, _ := readShadowLock(ws.Root); ok && !locked && !shadowLockStale(info, mtime, now, cfg.ShadowInterval()) {
			st.State, st.Reason = "locked-elsewhere", shadowLockHeldElsewhere(info)
		} else if state != "" {
			st.State, st.Reason = state, reason
		} else {
			st.State, st.Reason = "stopped", "not watching; run sirdar serve or sirdar shadow watch"
		}
	}
	return st
}

// shadowPublish sends the status on the event stream when it changed since
// the last one sent for the workspace, and returns it.
func (s *Service) shadowPublish(ws Workspace, cfg *config.Config) ShadowStatus {
	st := s.shadowStatusOf(ws, cfg)
	w := s.shadowWatch(ws.ID, ws.Root)
	s.shadow.mu.Lock()
	changed := w.published == nil || !reflect.DeepEqual(*w.published, st)
	if changed {
		kept := st
		w.published = &kept
	}
	s.shadow.mu.Unlock()
	if changed {
		sent := st
		s.publish(Event{Kind: KindShadowStatus, WorkspaceID: ws.ID, Shadow: &sent})
	}
	return st
}

// shadowRepublish publishes after a job ends, with the config as it is now.
func (s *Service) shadowRepublish(ws Workspace, fallback *config.Config) {
	cfg := fallback
	if _, fresh, err := s.load(ws.ID); err == nil {
		cfg = fresh
	}
	s.shadowPublish(ws, cfg)
}

func inFlightIDs(w *shadowWatch) []string {
	out := make([]string, 0, len(w.inFlight))
	for id := range w.inFlight {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// shadowInFlightUSD is what the runs still going have spent so far, read
// from their state files, so the daily cap counts money already gone.
func shadowInFlightUSD(root string, runIDs []string) float64 {
	var usd float64
	for _, id := range runIDs {
		if _, st, err := store.Open(root, id); err == nil {
			usd += st.Usage.CostUSD
		}
	}
	return usd
}

// shadowBreaches counts today's runs that failed on a read-only breach or
// on a blind answer. They are findings about the agent, reported apart
// from ordinary failures.
func shadowBreaches(entries []shadow.Entry, now time.Time) int {
	y, m, d := now.Date()
	n := 0
	for _, e := range entries {
		if e.Ev != "finished" {
			continue
		}
		if ey, em, ed := e.At.In(now.Location()).Date(); ey != y || em != m || ed != d {
			continue
		}
		if strings.HasPrefix(e.Reason, "read-only breach") || strings.HasPrefix(e.Reason, "the agent could read nothing") {
			n++
		}
	}
	return n
}

func shadowPausedReason(until time.Time) string {
	return "paused until " + until.Local().Format("15:04")
}

// --- operator actions -------------------------------------------------

// shadowPause stops new shadow starts for d, 24 hours when d is not
// positive. Runs already going carry on.
func (s *Service) shadowPause(wsID string, d time.Duration) (ShadowStatus, error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return ShadowStatus{}, err
	}
	if d <= 0 {
		d = 24 * time.Hour
	}
	now := s.now()
	until := now.Add(d)
	if err := shadow.Append(ws.Root, shadow.Entry{At: now, Ev: "paused", Until: &until, Reason: "paused by the operator"}); err != nil {
		return ShadowStatus{}, err
	}
	return s.shadowPublish(ws, cfg), nil
}

// shadowResume lifts a pause early, whoever wrote it.
func (s *Service) shadowResume(wsID string) (ShadowStatus, error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return ShadowStatus{}, err
	}
	if err := shadow.Append(ws.Root, shadow.Entry{At: s.now(), Ev: "resumed", By: "operator"}); err != nil {
		return ShadowStatus{}, err
	}
	return s.shadowPublish(ws, cfg), nil
}

// shadowPoll runs one tick now. It needs the lock: when this process is
// already watching it ticks under the lock it holds; otherwise it takes the
// lock for the length of the tick, and a live holder elsewhere refuses it.
// A tick that fails is reported in the status rather than as an error.
func (s *Service) shadowPoll(ctx context.Context, wsID string) (ShadowStatus, error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return ShadowStatus{}, err
	}
	if !cfg.Shadow.Enabled {
		return s.shadowPublish(ws, cfg), nil
	}
	w := s.shadowWatch(ws.ID, ws.Root)
	s.shadow.mu.Lock()
	locked := w.locked
	s.shadow.mu.Unlock()
	if !locked {
		held, holder, err := takeShadowLock(ws.Root, "poll", s.now(), cfg.ShadowInterval())
		if err != nil {
			return ShadowStatus{}, err
		}
		if !held {
			return s.shadowStatusOf(ws, cfg), fmt.Errorf("%w: %s", ErrShadowLocked, shadowLockHeldElsewhere(holder))
		}
		s.shadow.mu.Lock()
		w.locked, w.by = true, "poll"
		s.shadow.mu.Unlock()
		defer func() {
			s.shadow.mu.Lock()
			w.locked = false
			// Nobody watches once the poll lets go; a gate or pause the
			// tick found is still worth showing, a plain "watching" is not.
			if w.state == "watching" {
				w.state, w.reason = "", ""
			}
			s.shadow.mu.Unlock()
			releaseShadowLock(ws.Root)
		}()
	}
	_ = s.shadowTick(ctx, ws, cfg)
	return s.shadowStatusOf(ws, cfg), nil
}

// --- the supervisor ---------------------------------------------------

// startShadow runs the supervisor: one goroutine that, every
// shadowSupervise, rereads the registry and each workspace's config, keeps
// the lock of every workspace with shadow enabled, and ticks those whose
// next poll has come.
func (s *Service) startShadow(ctx context.Context) {
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.shadow.mu.Lock()
	s.shadow.startedAt = s.now()
	s.shadow.cancel = cancel
	s.shadow.done = done
	s.shadow.mu.Unlock()

	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer close(done)
		s.shadowSupervisePass(sctx)
		t := time.NewTicker(shadowSupervise)
		defer t.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-t.C:
				s.shadowSupervisePass(sctx)
			}
		}
	}()
}

// stopShadow ends the supervisor and waits for it, so no tick starts a
// job after Stop has cancelled the ones running.
func (s *Service) stopShadow() {
	s.shadow.mu.Lock()
	cancel, done := s.shadow.cancel, s.shadow.done
	s.shadow.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// releaseShadowLocks lets go of every lock this process holds.
func (s *Service) releaseShadowLocks() {
	s.shadow.mu.Lock()
	var roots []string
	for _, w := range s.shadow.ws {
		if w.locked {
			roots = append(roots, w.root)
			w.locked, w.watching = false, false
		}
	}
	s.shadow.mu.Unlock()
	for _, root := range roots {
		releaseShadowLock(root)
	}
}

func (s *Service) shadowSupervisePass(ctx context.Context) {
	workspaces, err := s.reg.List()
	if err != nil {
		return
	}
	for _, ws := range workspaces {
		if ctx.Err() != nil {
			return
		}
		_, cfg, err := s.load(ws.ID)
		if err != nil {
			continue
		}
		s.shadowSuperviseOne(ctx, ws, cfg)
	}
}

func (s *Service) shadowSuperviseOne(ctx context.Context, ws Workspace, cfg *config.Config) {
	w := s.shadowWatch(ws.ID, ws.Root)
	now := s.now()
	if !cfg.Shadow.Enabled {
		s.shadow.mu.Lock()
		held := w.locked && w.watching
		if held {
			w.locked, w.watching = false, false
		}
		w.state, w.reason, w.nextPoll = "disabled", "shadow.enabled is false", time.Time{}
		s.shadow.mu.Unlock()
		if held {
			releaseShadowLock(ws.Root)
		}
		s.shadowPublish(ws, cfg)
		return
	}

	interval := cfg.ShadowInterval()
	s.shadow.mu.Lock()
	watching := w.watching
	s.shadow.mu.Unlock()
	// A lock that is no longer this process's was taken over while this
	// one could not touch it, and watching on would mean two pollers.
	if watching && !ownsShadowLock(ws.Root) {
		s.shadow.mu.Lock()
		w.locked, w.watching = false, false
		s.shadow.mu.Unlock()
		watching = false
	}
	if watching {
		_ = touchShadowLock(ws.Root, now)
	} else {
		held, holder, err := takeShadowLock(ws.Root, s.shadowBy(), now, interval)
		s.shadow.mu.Lock()
		switch {
		case err != nil:
			w.lastError = err.Error()
		case !held:
			w.state, w.reason = "locked-elsewhere", shadowLockHeldElsewhere(holder)
		default:
			w.locked, w.watching, w.by = true, true, s.shadowBy()
			if w.state == "" || w.state == "locked-elsewhere" || w.state == "disabled" || w.state == "stopped" {
				w.state, w.reason = "watching", ""
			}
		}
		s.shadow.mu.Unlock()
		if err != nil || !held {
			s.shadowPublish(ws, cfg)
			return
		}
	}

	s.shadow.mu.Lock()
	if w.nextPoll.IsZero() {
		w.nextPoll = s.shadow.startedAt.Add(shadowFirstTick)
	}
	due := !now.Before(w.nextPoll)
	s.shadow.mu.Unlock()
	if due {
		_ = s.shadowTick(ctx, ws, cfg)
	}
	s.shadowPublish(ws, cfg)
}

// --- one tick ---------------------------------------------------------

// shadowTick is one poll of one workspace (spec §4): recover runs a crash
// left behind, arm, gate, list, record, start.
func (s *Service) shadowTick(ctx context.Context, ws Workspace, cfg *config.Config) error {
	w := s.shadowWatch(ws.ID, ws.Root)
	w.tick.Lock()
	defer w.tick.Unlock()
	defer s.shadowPublish(ws, cfg)

	now := s.now()
	interval := cfg.ShadowInterval()
	root := ws.Root

	s.shadow.mu.Lock()
	locked, by := w.locked, w.by
	s.shadow.mu.Unlock()
	if locked {
		_ = touchShadowLock(root, now)
	}
	if by == "" {
		by = "poll"
	}

	settle := func(state, reason string) {
		s.shadow.mu.Lock()
		w.state, w.reason = state, reason
		w.lastPoll, w.nextPoll = now, now.Add(interval)
		s.shadow.mu.Unlock()
	}
	fail := func(err error) error {
		s.shadow.mu.Lock()
		w.lastError = err.Error()
		w.lastPoll, w.nextPoll = now, now.Add(interval)
		s.shadow.mu.Unlock()
		return err
	}

	entries, err := shadow.Read(root)
	if err != nil {
		return fail(err)
	}

	// 1. Recover what a crash left started and never finished. It runs on
	// every tick, not only the first: a run is orphaned only once its
	// process is gone or it has gone quiet for long enough, which may be
	// several ticks after this process started.
	if s.shadowRecover(w, root, entries, now, interval) {
		if entries, err = shadow.Read(root); err != nil {
			return fail(err)
		}
	}

	// 2. Arm.
	since, armed := cfg.ShadowSince()
	var armedAt time.Time
	if armed {
		at, from, ok := armedLine(entries)
		if !ok {
			e := shadow.Entry{At: now, Ev: "armed", Since: &now, By: by}
			if err := shadow.Append(root, e); err != nil {
				return fail(err)
			}
			entries = append(entries, e)
			at, from = now, now
		}
		since, armedAt = from, at
	}

	// 3. Gate.
	s.shadow.mu.Lock()
	ids := inFlightIDs(w)
	authHold, authMtime := w.authHold, w.authMtime
	s.shadow.mu.Unlock()
	if authHold {
		if configMtime(root).Equal(authMtime) {
			settle("gated", "tracker credentials rejected")
			return nil
		}
		s.shadow.mu.Lock()
		w.authHold = false
		s.shadow.mu.Unlock()
	}
	usd := shadowInFlightUSD(root, ids)
	if state, reason := s.shadowGate(root, cfg, entries, now, usd); state != "" {
		settle(state, reason)
		return nil
	}

	// 4. List.
	deps, cleanup, err := s.build(cfg, "", "", s.stderr())
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return fail(err)
	}
	if deps.Tracker == nil {
		settle("disabled", "sources.tracker is not set")
		return nil
	}
	tickets, err := deps.Tracker.List(ctx, source.ListFilter{
		Assignee: queueAssignee(cfg, cfg.Shadow.Assignee),
		Order:    source.OrderNewest,
		Limit:    200,
	})
	if err != nil {
		var serr *source.Error
		isSource := errors.As(err, &serr)
		s.shadow.mu.Lock()
		defer s.shadow.mu.Unlock()
		w.lastError, w.lastPoll = err.Error(), now
		switch {
		case isSource && serr.Code == source.Auth:
			w.state, w.reason = "gated", "tracker credentials rejected"
			w.authHold, w.authMtime = true, configMtime(root)
			w.nextPoll = now.Add(interval)
		case isSource && serr.Code == source.Unsupported:
			w.state, w.reason = "disabled", err.Error()
			w.nextPoll = now.Add(interval)
		default:
			// Back off while the tracker keeps failing: interval × 2^errors,
			// never more than four intervals, so it is noticed again soon
			// after it recovers.
			w.errors++
			backoff := 4 * interval
			if w.errors < 2 {
				backoff = interval << w.errors
			}
			w.nextPoll = now.Add(backoff)
		}
		return err
	}
	s.shadow.mu.Lock()
	w.errors, w.lastError = 0, ""
	s.shadow.mu.Unlock()

	// 5. Record and select.
	listed := make([]shadow.Listed, 0, len(tickets))
	for _, t := range tickets {
		listed = append(listed, shadow.Listed{Key: t.Key, Title: t.Title, Type: t.Type, Status: t.Status, CreatedAt: t.CreatedAt})
	}
	policy := shadow.Policy{
		Types:        cfg.ShadowTypes(),
		Statuses:     cfg.Shadow.Statuses,
		Since:        since,
		ArmedAt:      armedAt,
		MaxPerDay:    cfg.Shadow.MaxPerDay,
		MaxUSDPerDay: cfg.Shadow.MaxUSDPerDay,
		Concurrency:  cfg.Shadow.Concurrency,
		Interval:     interval,
	}
	plan := shadow.Select(entries, listed, now, policy, s.gate.InFlight(ws.ID, ActionShadowTriage), usd)
	// Every line is written before anything starts: a run whose seen line
	// failed to land would be one the next tick takes as new again.
	for _, e := range append(plan.Seen, plan.Skipped...) {
		if err := shadow.Append(root, e); err != nil {
			return fail(err)
		}
	}

	// 6. Start.
	spent := shadow.Today(entries, usd, now).USD
	folded := shadow.Fold(entries)
	started := 0
	for _, st := range plan.Start {
		release, ok := s.gate.TryAcquire(ws.ID, ActionShadowTriage, cfg.Shadow.Concurrency)
		if !ok {
			break
		}
		sp := shadowLaunchSpec{Key: st.Key, Attempt: st.Attempt, RetryOf: st.RetryOf}
		if st.RetryOf != "" {
			sp.Backfill, sp.AsOf = retryCutoff(folded[st.Key], st.RetryOf)
		}
		if _, err := s.shadowLaunch(ctx, ws, cfg, sp, release, spent); err != nil {
			return fail(err)
		}
		started++
	}
	if plan.CapHit != "" && started == 0 {
		settle("gated", plan.CapHit)
		return nil
	}
	settle("watching", "")
	return nil
}

// retryCutoff is the backfill flag and cutoff a retry inherits from the
// attempt it retries: a backfilled ticket run again at HEAD's bundle would
// be graded as a backfill while seeing what happened after it. A line
// from before started lines carried the cutoff falls back to the ticket's
// creation time, which is what a backfill cuts at.
func retryCutoff(t *shadow.Ticket, runID string) (backfill bool, asOf time.Time) {
	if t == nil {
		return false, time.Time{}
	}
	for _, a := range t.Attempts {
		if a.Started.RunID != runID || !a.Started.Backfill {
			continue
		}
		if a.Started.AsOf != nil {
			return true, *a.Started.AsOf
		}
		return true, t.CreatedAt
	}
	return false, time.Time{}
}

// armedLine finds the newest armed line: when it was written, and the time
// it arms from.
func armedLine(entries []shadow.Entry) (at, since time.Time, ok bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Ev != "armed" {
			continue
		}
		since = e.At
		if e.Since != nil {
			since = *e.Since
		}
		return e.At, since, true
	}
	return time.Time{}, time.Time{}, false
}

func configMtime(root string) time.Time {
	fi, err := os.Stat(filepath.Join(root, ".sirdar", "config.yaml"))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// shadowGate is the first reason no shadow run may start now, as a state
// and a reason, or two empty strings. The watcher's tick and `shadow run`
// both ask it.
func (s *Service) shadowGate(root string, cfg *config.Config, entries []shadow.Entry, now time.Time, inFlightUSD float64) (state, reason string) {
	if until, _, paused := shadow.PausedUntil(entries, now); paused {
		return "paused", shadowPausedReason(until)
	}
	if reason := s.shadowQuotaReason(cfg); reason != "" {
		return "paused", reason
	}
	if cfg.ShadowYield() {
		if runID := operatorRunGoing(root); runID != "" {
			return "gated", "deferring to your run " + runID
		}
	}
	day := shadow.Today(entries, inFlightUSD, now)
	if day.Runs >= cfg.Shadow.MaxPerDay {
		return "gated", fmt.Sprintf("daily run cap reached (%d of %d)", day.Runs, cfg.Shadow.MaxPerDay)
	}
	if day.USD >= cfg.Shadow.MaxUSDPerDay {
		return "gated", fmt.Sprintf("daily USD cap reached ($%.2f of $%.2f)", day.USD, cfg.Shadow.MaxUSDPerDay)
	}
	return "", ""
}

// operatorRunGoing names a run someone asked for that is preparing or
// running in the workspace, or returns "".
func operatorRunGoing(root string) string {
	states, err := store.List(root, "")
	if err != nil {
		return ""
	}
	for _, st := range states {
		if !st.Measurement() && (st.Status == store.StatusPreparing || st.Status == store.StatusRunning) {
			return st.RunID
		}
	}
	return ""
}

// shadowQuotaReason is why the provider's quota pauses shadow, or "". The
// reason names the fullest window, since that is the one that has to
// reset before shadow runs again.
func (s *Service) shadowQuotaReason(cfg *config.Config) string {
	limit := cfg.ShadowPauseAtQuota()
	if limit <= 0 {
		return ""
	}
	type window struct {
		name   string
		util   float64
		resets string
	}
	for _, q := range s.Quota() {
		if q.Provider != string(cfg.Provider) {
			continue
		}
		var windows []window
		if q.SevenDay != nil {
			windows = append(windows, window{"seven-day", q.SevenDay.Utilization, q.SevenDay.ResetsAt})
		}
		if q.FiveHour != nil {
			windows = append(windows, window{"five-hour", q.FiveHour.Utilization, q.FiveHour.ResetsAt})
		}
		if q.UsedPercent != nil {
			windows = append(windows, window{"quota", *q.UsedPercent / 100, q.ResetsAt})
		}
		var top *window
		for i := range windows {
			if top == nil || windows[i].util > top.util {
				top = &windows[i]
			}
		}
		if top == nil || top.util < limit {
			return ""
		}
		reason := fmt.Sprintf("%s quota at %d%% (pause at %d%%)", top.name, int(math.Round(top.util*100)), int(math.Round(limit*100)))
		if at, err := time.Parse(time.RFC3339Nano, top.resets); err == nil {
			reason += ", resets " + at.Local().Format("2006-01-02 15:04")
		}
		return reason
	}
	return ""
}

// shadowRecover writes the finished line of every started run that has
// none, that this process is not running, and whose process is gone (see
// shadowOrphaned). A run its state says ended gets its line as it ended;
// one still preparing or running, or never given a directory, is written
// blocked "interrupted", which a later tick may retry once. A run whose
// recovery fails is reported and left for the next tick, so it cannot
// hold up the others or the tick. It reports whether it wrote anything.
func (s *Service) shadowRecover(w *shadowWatch, root string, entries []shadow.Entry, now time.Time, interval time.Duration) bool {
	s.shadow.mu.Lock()
	ours := map[string]bool{}
	for id := range w.inFlight {
		ours[id] = true
	}
	s.shadow.mu.Unlock()

	tickets := shadow.Fold(entries)
	keys := make([]string, 0, len(tickets))
	for key := range tickets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	wrote := false
	for _, key := range keys {
		for _, a := range tickets[key].Attempts {
			if a.Finished != nil || ours[a.Started.RunID] {
				continue
			}
			rn, st, err := store.Open(root, a.Started.RunID)
			haveDir := err == nil
			if !shadowOrphaned(a.Started, rn, st, haveDir, now, interval) {
				continue
			}
			dir := rn.Dir
			switch {
			case !haveDir:
				// The started line landed and its process died before the
				// run directory was made: nothing ran, and a retry is what
				// the ticket is owed.
				st = store.State{RunID: a.Started.RunID, Key: key, Status: store.StatusBlocked, Reason: "interrupted"}
				dir = ""
			case st.Status == store.StatusPreparing || st.Status == store.StatusRunning:
				st.Status, st.Reason, st.UpdatedAt = store.StatusBlocked, "interrupted", now
				if err := rn.WriteState(st); err != nil {
					s.logText(fmt.Sprintf("shadow: recover %s %s: %v", key, a.Started.RunID, err))
					continue
				}
			}
			fin := shadowFinished(key, st, dir, now)
			fin.CostUSD = max(0, fin.CostUSD-shadowPriorCost(entries, a.Started.RunID))
			if err := appendFinished(root, fin); err != nil {
				s.logText(fmt.Sprintf("shadow: recover %s %s: %v", key, a.Started.RunID, err))
				continue
			}
			wrote = true
		}
	}
	return wrote
}

// shadowOrphaned reports whether a started run with no finished line has
// lost the process that was driving it. A started line that names a
// process on this host is answered by whether that process is alive. One
// from another host, or from before started lines named a process, is
// orphaned once nothing about it has moved for as long as makes a lock
// stale: its started line, its state, and its event log.
func shadowOrphaned(started shadow.Entry, rn store.Run, st store.State, haveDir bool, now time.Time, interval time.Duration) bool {
	if started.PID > 0 && started.Host == hostName() {
		return !pidAlive(started.PID)
	}
	last := started.At
	if haveDir {
		if st.UpdatedAt.After(last) {
			last = st.UpdatedAt
		}
		if fi, err := os.Stat(filepath.Join(rn.Dir, "events.jsonl")); err == nil && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
	}
	return now.Sub(last) > shadowLockStaleAfter*interval
}

// shadowPriorCost is what the ledger's finished lines already counted for
// runID. A retry that resumes a run carries its spending forward, so its
// finished line records only what it added.
func shadowPriorCost(entries []shadow.Entry, runID string) float64 {
	var usd float64
	for _, e := range entries {
		if e.Ev == "finished" && e.RunID == runID {
			usd += e.CostUSD
		}
	}
	return usd
}

// appendFinished appends a finished line, and when the ledger refuses it,
// the same line without its verdict, questions and denied calls. The
// status and the cost must land whatever else does not: the daily cap
// counts the cost, and recovery would otherwise try the run again on
// every tick.
func appendFinished(root string, e shadow.Entry) error {
	if err := shadow.Append(root, e); err == nil {
		return nil
	}
	e.Verdict, e.Questions, e.Denied = nil, nil, nil
	return shadow.Append(root, e)
}

// --- starting a run ---------------------------------------------------

type shadowLaunchSpec struct {
	Key      string
	Attempt  int
	RetryOf  string // the previous attempt's run id on a retry
	Backfill bool
	AsOf     time.Time
}

// shadowLaunch appends the started line and starts the job, holding the
// gate slot release gives back when the job ends. The started line goes
// first so a crash between the two leaves a run recovery can see, never
// one that ran without counting against the caps.
func (s *Service) shadowLaunch(ctx context.Context, ws Workspace, cfg *config.Config, sp shadowLaunchSpec, release func(), spentToday float64) (ShadowStarted, error) {
	now := s.now()
	root := ws.Root
	key := sp.Key
	runID, resume := store.NewRunID(now), false
	// A retry carries on the session it had when there is one to resume;
	// without a handle there is nothing to continue and it starts afresh.
	if sp.RetryOf != "" {
		if _, prev, err := store.Open(root, sp.RetryOf); err == nil && prev.Handle != "" {
			runID, resume = sp.RetryOf, true
		}
	}
	model := cfg.ShadowModel()
	if model == "" {
		model = cfg.Model
	}
	started := shadow.Entry{
		At: now, Ev: "started", Key: key, RunID: runID, Attempt: sp.Attempt, Model: model,
		OperatorFirst: operatorStartedFirst(root, key, now), Backfill: sp.Backfill, Resume: resume,
		PID: os.Getpid(), Host: hostName(),
	}
	if !sp.AsOf.IsZero() {
		asOf := sp.AsOf
		started.AsOf = &asOf
	}
	// A resumed run's budget is checked against everything the run has
	// spent, and what it spent before is already in today's total, so it
	// may go that much further than what is left today.
	var prior float64
	if resume {
		if entries, err := shadow.Read(root); err == nil {
			prior = shadowPriorCost(entries, runID)
		}
	}
	if err := shadow.Append(root, started); err != nil {
		release()
		return ShadowStarted{Key: key}, err
	}

	w := s.shadowWatch(ws.ID, root)
	s.shadow.mu.Lock()
	w.inFlight[runID] = true
	s.shadow.mu.Unlock()
	done := func() {
		s.shadow.mu.Lock()
		delete(w.inFlight, runID)
		s.shadow.mu.Unlock()
		release()
		s.shadowRepublish(ws, cfg)
	}

	remaining := cfg.Shadow.MaxUSDPerDay - spentToday + prior
	work := func(jctx context.Context, deps runner.Deps) []JobOutcome {
		// The daily cap is enforced as this run's own budget: the session
		// and the executor read the budget off the config, so a clone
		// with the lower figure needs no change in the runner.
		cc := *deps.Config
		cc.Budget.MaxUSD = min(cc.Budget.MaxUSD, remaining)
		deps.Config = &cc
		r := &runner.Runner{Deps: deps}

		var out runner.Outcome
		var err error
		if resume {
			out, err = r.Resume(jctx, runID, runner.ResumeOptions{})
		} else {
			var outs []runner.Outcome
			outs, err = r.Triage(jctx, []string{key}, runner.Options{
				Shadow:      true,
				NoNotify:    true,
				Model:       cfg.ShadowModel(),
				RunID:       runID,
				AsOf:        sp.AsOf,
				NoReproduce: !cfg.ShadowReproduce(),
				Concurrency: 1,
			})
			if len(outs) > 0 {
				out = outs[0]
			}
		}
		s.log(err)
		// A question in words is answered once, with the fixed text, so
		// the run finishes on its own judgement rather than waiting.
		if st := out.State; st.Status == store.StatusBlocked && strings.HasPrefix(st.Reason, shadowAskedPrefix) && len(st.Questions) < 2 {
			again, aerr := r.Resume(jctx, runID, runner.ResumeOptions{Answer: shadowNoAnswer})
			s.log(aerr)
			if aerr == nil {
				out = again
			}
		}
		s.shadowRecordFinished(root, key, runID, out.State, err)
		if out.State.RunID == "" {
			return []JobOutcome{{Key: key, Status: string(store.StatusFailed), RunID: runID}}
		}
		return outcomesOf([]runner.Outcome{out})
	}
	onBuildError := func(err error) []JobOutcome {
		s.shadowRecordFinished(root, key, runID, store.State{}, err)
		return s.failed([]string{key}, err)
	}

	jobID, err := s.startJob(ctx, ws.ID, "", cfg.ShadowModel(), done, work, onBuildError)
	if err != nil {
		s.shadowRecordFinished(root, key, runID, store.State{}, err)
		done()
		return ShadowStarted{Key: key}, err
	}
	return ShadowStarted{Key: key, RunID: runID, JobID: jobID}, nil
}

// operatorStartedFirst reports whether someone started a run of their own
// on key before now. Such a ticket's bundle may already carry their
// findings, which is why the scorecard keeps it out of the headline.
func operatorStartedFirst(root, key string, now time.Time) bool {
	states, err := store.List(root, key)
	if err != nil {
		return false
	}
	for _, st := range states {
		if !st.Measurement() && !st.StartedAt.IsZero() && st.StartedAt.Before(now) {
			return true
		}
	}
	return false
}

// shadowRecordFinished writes a run's finished line from its state on
// disk, and the pause a rate or model limit calls for. A run that never
// got a state is recorded failed with the error that stopped it.
func (s *Service) shadowRecordFinished(root, key, runID string, st store.State, cause error) {
	dir := ""
	if rn, disk, err := store.Open(root, runID); err == nil {
		st, dir = disk, rn.Dir
	} else if st.RunID == "" {
		reason := "the run never started"
		if cause != nil {
			reason = cause.Error()
		}
		st = store.State{RunID: runID, Key: key, Status: store.StatusFailed, Reason: reason}
	}
	now := s.now()
	fin := shadowFinished(key, st, dir, now)
	if entries, err := shadow.Read(root); err == nil {
		fin.CostUSD = max(0, fin.CostUSD-shadowPriorCost(entries, runID))
	}
	if err := appendFinished(root, fin); err != nil {
		s.log(err)
	}
	if p, ok := shadowPauseFor(st, now); ok {
		if err := shadow.Append(root, p); err != nil {
			s.log(err)
		}
	}
}

// shadowFinished is the finished line for a run in state st whose
// directory is dir ("" when it has none).
func shadowFinished(key string, st store.State, dir string, now time.Time) shadow.Entry {
	e := shadow.Entry{
		At: now, Ev: "finished", Key: key, RunID: st.RunID,
		Status: string(st.Status), Reason: st.Reason,
		CostUSD: st.Usage.CostUSD, Turns: st.Usage.Turns, ElapsedSec: st.Usage.ElapsedSeconds,
		Reproduction: "unknown",
	}
	if e.ElapsedSec == 0 && !st.StartedAt.IsZero() && st.UpdatedAt.After(st.StartedAt) {
		e.ElapsedSec = st.UpdatedAt.Sub(st.StartedAt).Seconds()
	}
	if st.Status == store.StatusBlocked && strings.HasPrefix(st.Reason, shadowAskedPrefix) && len(st.Questions) >= 2 {
		e.Reason = "asked twice"
	}
	if dir != "" {
		// A note turn that did not file leaves no verdict to grade, and a
		// result.json left from an earlier turn is not this run's answer.
		if st.NoteWarning == "" {
			if v, ok := shadow.VerdictOf(dir); ok {
				v.CodeRefs = capItems(v.CodeRefs)
				v.OpenQuestions = capItems(v.OpenQuestions)
				e.Verdict = v
			}
		}
		e.Reproduction = shadow.ReproductionOf(dir)
		if st.Status == store.StatusCompleted && st.NoteWarning == "" {
			if _, err := os.Stat(filepath.Join(dir, "note.md")); err == nil {
				e.NoteFiled = true
			}
		}
	}
	for _, d := range st.Denied {
		e.Denied = append(e.Denied, shadow.Denied{Tool: d.Tool, Summary: cutText(d.Summary)})
	}
	for _, q := range st.Questions[:min(len(st.Questions), shadowMaxItems)] {
		e.Questions = append(e.Questions, cutText(q))
	}
	return e
}

// capItems keeps the first shadowMaxItems of list, each cut to
// shadowMaxItemRunes, and leaves an empty list as it was.
func capItems(list []string) []string {
	if len(list) == 0 {
		return list
	}
	out := make([]string, 0, min(len(list), shadowMaxItems))
	for _, item := range list[:min(len(list), shadowMaxItems)] {
		if r := []rune(item); len(r) > shadowMaxItemRunes {
			item = string(r[:shadowMaxItemRunes])
		}
		out = append(out, item)
	}
	return out
}

func cutText(s string) string {
	if r := []rune(s); len(r) > shadowMaxText {
		return string(r[:shadowMaxText])
	}
	return s
}

// shadowPauseFor is the pause a blocked run's limit calls for: until a
// rate limit's stated reset, else half an hour; an hour for a per-model
// limit.
func shadowPauseFor(st store.State, now time.Time) (shadow.Entry, bool) {
	if st.Status != store.StatusBlocked {
		return shadow.Entry{}, false
	}
	switch {
	case strings.HasPrefix(st.Reason, "rate limited"):
		until := now.Add(shadowRatePause)
		if _, at, ok := strings.Cut(st.Reason, "resets at "); ok {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(at)); err == nil && t.After(now) {
				until = t
			}
		}
		return shadow.Entry{At: now, Ev: "paused", Until: &until, Reason: "rate limited"}, true
	case runner.ModelLimited(st.Reason) != "":
		until := now.Add(shadowModelPause)
		return shadow.Entry{At: now, Ev: "paused", Until: &until, Reason: "model limit"}, true
	}
	return shadow.Entry{}, false
}

// --- shadow run -------------------------------------------------------

// shadowRun starts a shadow run of each key now, through the same gate the
// watcher uses but without its lock or shadow.enabled: it is the operator
// asking. backfill triages each ticket as it stood when it was created.
func (s *Service) shadowRun(ctx context.Context, wsID string, keys []string, backfill bool) ([]ShadowStarted, error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return nil, err
	}
	if cfg.Sources.Tracker == nil {
		return nil, fmt.Errorf("%w: shadow run needs sources.tracker", ErrUnsupported)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no keys to shadow", ErrInvalidArgument)
	}
	for _, key := range keys {
		if err := checkID(ErrNoSuchRun, "key", key); err != nil {
			return nil, err
		}
	}
	deps, cleanup, err := s.build(cfg, "", "", s.stderr())
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	if deps.Tracker == nil {
		return nil, ErrUnsupported
	}
	out := make([]ShadowStarted, 0, len(keys))
	for _, key := range keys {
		res, err := s.shadowRunOne(ctx, ws, cfg, deps.Tracker, key, backfill)
		if err != nil {
			return out, err
		}
		out = append(out, res)
	}
	return out, nil
}

func (s *Service) shadowRunOne(ctx context.Context, ws Workspace, cfg *config.Config, tr source.Tracker, key string, backfill bool) (ShadowStarted, error) {
	res := ShadowStarted{Key: key}
	sp := shadowLaunchSpec{Key: key, Backfill: backfill}
	var tt ticket.TrackerTicket
	fetched := false
	if backfill {
		got, err := tr.Get(ctx, key)
		if err != nil {
			res.Skipped = err.Error()
			return res, nil
		}
		if got.CreatedAt.IsZero() {
			res.Skipped = "no creation time; cannot backfill"
			return res, nil
		}
		tt, fetched, sp.AsOf = got, true, got.CreatedAt
	}

	now := s.now()
	entries, err := shadow.Read(ws.Root)
	if err != nil {
		return res, err
	}
	w := s.shadowWatch(ws.ID, ws.Root)
	s.shadow.mu.Lock()
	ids := inFlightIDs(w)
	s.shadow.mu.Unlock()
	usd := shadowInFlightUSD(ws.Root, ids)
	if _, reason := s.shadowGate(ws.Root, cfg, entries, now, usd); reason != "" {
		res.Skipped = reason
		return res, nil
	}
	release, ok := s.gate.TryAcquire(ws.ID, ActionShadowTriage, cfg.Shadow.Concurrency)
	if !ok {
		res.Skipped = fmt.Sprintf("shadow.concurrency reached (%d in flight)", cfg.Shadow.Concurrency)
		return res, nil
	}

	t := shadow.Fold(entries)[key]
	if t == nil || t.SeenAt.IsZero() {
		if !fetched {
			if got, err := tr.Get(ctx, key); err == nil {
				tt, fetched = got, true
			}
		}
		seen := shadow.Entry{At: now, Ev: "seen", Key: key}
		if fetched {
			seen.Title, seen.Type, seen.Status = tt.Title, tt.Type, tt.Status
			if !tt.CreatedAt.IsZero() {
				created := tt.CreatedAt
				seen.CreatedAt = &created
			}
		}
		if err := shadow.Append(ws.Root, seen); err != nil {
			release()
			return res, err
		}
	}
	sp.Attempt = 1
	if t != nil {
		sp.Attempt = len(t.Attempts) + 1
	}
	return s.shadowLaunch(ctx, ws, cfg, sp, release, shadow.Today(entries, usd, now).USD)
}
