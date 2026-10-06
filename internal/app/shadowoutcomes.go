package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/eval"
	"github.com/srivathsanvenkateswaran/sirdar/internal/notify"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/shadow"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// ShadowReport and ShadowRow are the scorecard as the API sends it. They
// are the shadow package's types, so the wire shape has one definition.
type (
	ShadowReport = shadow.ShadowReport
	ShadowRow    = shadow.Row
)

var shadowJudge = eval.JudgeOnce                                               // replaced in tests
var digestPost = notify.PostSlackText                                          // replaced in tests
var digestHostOK = func(host string) bool { return host == "hooks.slack.com" } // replaced in tests

// shadowJudgeFailsPerDay is how many failed judge calls one key may cost
// in a local day before collection stops asking about it until the next.
// A judge that keeps failing on the same truth will most likely fail the
// same way again, and every attempt is paid for.
const shadowJudgeFailsPerDay = 3

// shadowCollectAndGrade gathers new outcomes for keys, every ledger key
// when keys is empty, and grades the keys whose truth changed or that have
// no final grade yet (spec §6, §7). A key whose latest attempt is still
// running, or may still be retried, is left for later: its grade would be
// about a run that is not its last.
//
// The window that closes a key as ungradable is applied only on a sweep of
// every key. A key named by the caller is one the operator, or the
// operator's own run, just said something about, and that is worth a
// grade however old the shadow run is.
func (s *Service) shadowCollectAndGrade(ctx context.Context, ws Workspace, cfg *config.Config, keys []string) ([]ShadowRow, error) {
	w := s.shadowWatch(ws.ID, ws.Root)
	// One pass at a time per workspace: the tick, a manual entry and an
	// operator run finishing can all ask at once, and two passes would
	// append the same outcome twice and pay the judge twice.
	w.collect.Lock()
	defer w.collect.Unlock()

	root := ws.Root
	entries, err := shadow.Read(root)
	if err != nil {
		return nil, err
	}
	tickets := shadow.Fold(entries)
	sweep := len(keys) == 0
	if sweep {
		for key := range tickets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
	}

	now := s.now()
	window := time.Duration(cfg.Shadow.Outcomes.WindowDays) * 24 * time.Hour
	var due []*shadow.Ticket
	for _, key := range keys {
		t := tickets[key]
		if t == nil {
			continue
		}
		latest := t.Latest()
		if latest == nil || latest.Finished == nil || shadow.RetryPending(t) {
			continue
		}
		if sweep && now.Sub(latest.Finished.At) > window {
			if t.Grade == nil || !t.Grade.Final {
				if err := shadow.Append(root, shadow.Ungradable(key, latest.Started.RunID, now)); err != nil {
					return nil, err
				}
			}
			continue
		}
		due = append(due, t)
	}
	if len(due) == 0 {
		return nil, nil
	}

	deps, cleanup, err := s.build(cfg, "", "", s.stderr())
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	judge := func(ctx context.Context, prompt string, schema []byte) (json.RawMessage, float64, error) {
		return shadowJudge(ctx, deps, cfg.ShadowJudgeModel(), prompt, schema)
	}

	var problems []error
	touched := map[string]bool{}
	for _, t := range due {
		if ctx.Err() != nil {
			break
		}
		touched[t.Key] = true
		if err := s.shadowCollectOne(ctx, w, root, cfg, deps, judge, t, now); err != nil {
			problems = append(problems, fmt.Errorf("shadow: %s: %w", t.Key, err))
		}
	}
	if len(problems) > 0 {
		s.shadow.mu.Lock()
		w.lastError = errors.Join(problems...).Error()
		s.shadow.mu.Unlock()
	}

	if entries, err = shadow.Read(root); err != nil {
		return nil, err
	}
	var rows []ShadowRow
	for _, r := range shadow.Report(entries, time.Time{}).Rows {
		if touched[r.Key] {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

// shadowCollectOne appends what is new about one key's outcome and grades
// it when there is reason to. A failure to read one source is reported and
// the others still count.
func (s *Service) shadowCollectOne(ctx context.Context, w *shadowWatch, root string, cfg *config.Config, deps runner.Deps, judge shadow.Judge, t *shadow.Ticket, now time.Time) error {
	key := t.Key
	latest := t.Latest()
	var problems []error

	outcomes := append([]shadow.Entry(nil), t.Outcomes...)
	fresh, firstStart, err := shadow.OperatorOutcomes(root, key, outcomes, now)
	if err != nil {
		problems = append(problems, err)
	}
	if o, ok := deps.Tracker.(source.Outcomer); ok && cfg.ShadowOutcomesTracker() {
		line, isNew, err := shadow.TrackerOutcome(ctx, o, key, append(outcomes, fresh...), now)
		if err != nil {
			problems = append(problems, err)
		} else if isNew {
			fresh = append(fresh, line)
		}
	}
	appended := 0
	for _, line := range fresh {
		if err := shadow.Append(root, line); err != nil {
			problems = append(problems, err)
			continue
		}
		outcomes = append(outcomes, line)
		appended++
	}

	if t.Grade != nil && t.Grade.Final && appended == 0 {
		return errors.Join(problems...)
	}
	day := now.Format("2006-01-02")
	failKey := key + "\x00" + day
	s.shadow.mu.Lock()
	fails := w.judgeFails[failKey]
	s.shadow.mu.Unlock()
	if fails >= shadowJudgeFailsPerDay {
		return errors.Join(problems...)
	}

	dir := filepath.Join(root, ".sirdar", "runs", key, latest.Started.RunID)
	if rn, _, err := store.Open(root, latest.Started.RunID); err == nil {
		dir = rn.Dir
	}
	line, err := shadow.Grade(ctx, judge, shadow.GradeInput{
		Run:           shadow.ShadowRun{Dir: dir, Started: latest.Started, Finished: *latest.Finished},
		Outcomes:      outcomes,
		OperatorStart: firstStart,
		JudgeModel:    cfg.ShadowJudgeModel(),
		Now:           now,
	})
	var je *shadow.JudgeError
	switch {
	case errors.Is(err, shadow.ErrNotEnoughTruth):
		// Nothing to grade against yet; the next pass looks again.
	case errors.As(err, &je):
		s.shadow.mu.Lock()
		w.judgeFails[failKey]++
		s.shadow.mu.Unlock()
		problems = append(problems, err)
		// The call was paid for whether or not its answer was usable, so
		// its cost lands on a graded line with no grade: the daily cap and
		// the scorecard count it, and Fold does not take it for a grade.
		if je.CostUSD > 0 {
			failed := shadow.Entry{
				At: now, Ev: "graded", Key: key, ShadowRunID: latest.Started.RunID,
				JudgeModel: cfg.ShadowJudgeModel(), CostUSD: je.CostUSD,
				Warning: cutText("judge failed: " + firstLineOf(err.Error())),
			}
			if err := shadow.Append(root, failed); err != nil {
				problems = append(problems, err)
			}
		}
	case err != nil:
		problems = append(problems, err)
	default:
		if err := shadow.Append(root, line); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// shadowOutcome records an outcome the operator entered by hand and grades
// the key against it at once.
func (s *Service) shadowOutcome(ctx context.Context, wsID, key string, prURLs []string, resolution string) (ShadowRow, error) {
	if err := checkID(ErrNoSuchRun, "key", key); err != nil {
		return ShadowRow{}, err
	}
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return ShadowRow{}, err
	}
	line, err := shadow.ManualOutcome(key, prURLs, resolution, s.now())
	if err != nil {
		return ShadowRow{}, fmt.Errorf("%w: %w", ErrBadInput, err)
	}
	entries, err := shadow.Read(ws.Root)
	if err != nil {
		return ShadowRow{}, err
	}
	if t := shadow.Fold(entries)[key]; t == nil || len(t.Attempts) == 0 {
		return ShadowRow{}, fmt.Errorf("%w: %s has no shadow run", ErrNoSuchRun, key)
	}
	if err := shadow.Append(ws.Root, line); err != nil {
		return ShadowRow{}, err
	}
	if _, err := s.shadowCollectAndGrade(ctx, ws, cfg, []string{key}); err != nil {
		return ShadowRow{}, err
	}
	// The row is read afresh rather than taken from the pass, which leaves
	// out a key whose run is still going: the outcome is recorded either
	// way, and the row says where the key stands.
	report, err := s.shadowReport(wsID, time.Time{})
	if err != nil {
		return ShadowRow{}, err
	}
	for _, r := range report.Rows {
		if r.Key == key {
			return r, nil
		}
	}
	return ShadowRow{}, fmt.Errorf("%w: %s has no shadow run", ErrNoSuchRun, key)
}

// shadowRegrade collects and grades the keys named now, without waiting
// for the outcome tick.
func (s *Service) shadowRegrade(ctx context.Context, wsID string, keys []string) ([]ShadowRow, error) {
	for _, key := range keys {
		if err := checkID(ErrNoSuchRun, "key", key); err != nil {
			return nil, err
		}
	}
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return nil, err
	}
	return s.shadowCollectAndGrade(ctx, ws, cfg, keys)
}

// shadowReport is the scorecard over keys first started at or after since.
func (s *Service) shadowReport(wsID string, since time.Time) (ShadowReport, error) {
	root, err := s.root(wsID)
	if err != nil {
		return ShadowReport{}, err
	}
	entries, err := shadow.Read(root)
	if err != nil {
		return ShadowReport{}, err
	}
	return shadow.Report(entries, since), nil
}

// shadowObserveRun starts a collection pass for the key of an operator RCA
// or fix run that just completed, when the ledger holds a finished shadow
// run of it: that run's result is the truth the shadow run is graded on,
// and it is freshest now.
func (s *Service) shadowObserveRun(e Event) {
	if !s.opts.Shadow || e.Kind != KindRunUpdated || e.Run == nil || e.Run.Shadow {
		return
	}
	if e.Run.Status != string(store.StatusCompleted) || (e.Run.Kind != string(store.KindRCA) && e.Run.Kind != string(store.KindFix)) {
		return
	}
	s.shadow.mu.Lock()
	ctx := s.shadow.ctx
	s.shadow.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	wsID, key := e.WorkspaceID, e.Run.Key
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		ws, cfg, err := s.load(wsID)
		if err != nil || !cfg.Shadow.Enabled {
			return
		}
		entries, err := shadow.Read(ws.Root)
		if err != nil {
			return
		}
		t := shadow.Fold(entries)[key]
		if t == nil || t.Latest() == nil || t.Latest().Finished == nil {
			return
		}
		if _, err := s.shadowCollectAndGrade(ctx, ws, cfg, []string{key}); err != nil {
			s.log(err)
		}
		s.shadowRepublish(ws, cfg)
	}()
}
