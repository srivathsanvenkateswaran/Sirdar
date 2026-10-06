package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
// when keys is empty, and grades the keys with no grade yet or whose truth
// grew since their grade (spec §6, §7). A key whose latest attempt is
// still running, or may still be retried, is left for later: its grade
// would be about a run that is not its last.
//
// The window that closes a key as ungradable is applied on a sweep of
// every key, and not when the operator names keys by hand: a manual
// outcome or a regrade is the operator saying the key is worth a grade
// however old the shadow run is.
func (s *Service) shadowCollectAndGrade(ctx context.Context, ws Workspace, cfg *config.Config, keys []string) ([]ShadowRow, error) {
	return s.shadowCollect(ctx, ws, cfg, keys, len(keys) == 0)
}

// shadowCollect is shadowCollectAndGrade with the window rule chosen by
// the caller. A key past its window whose latest attempt never got a final
// grade is closed as ungradable, even one still waiting for a retry that
// may never come because the ticket left the operator's queue.
func (s *Service) shadowCollect(ctx context.Context, ws Workspace, cfg *config.Config, keys []string, windowed bool) ([]ShadowRow, error) {
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
	if len(keys) == 0 {
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
		if latest == nil || latest.Finished == nil {
			continue
		}
		if windowed && now.Sub(latest.Finished.At) > window {
			if t.Grade == nil || !t.Grade.Final {
				if err := shadow.Append(root, shadow.Ungradable(key, latest.Started.RunID, now)); err != nil {
					return nil, err
				}
			}
			continue
		}
		if shadow.RetryPending(t) {
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
		if err := s.shadowCollectOne(ctx, root, cfg, deps, judge, t, entries, now); err != nil {
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
func (s *Service) shadowCollectOne(ctx context.Context, root string, cfg *config.Config, deps runner.Deps, judge shadow.Judge, t *shadow.Ticket, entries []shadow.Entry, now time.Time) error {
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

	// A grade stands until the truth behind it grows. The truth grew when
	// this pass appended a line, or when an outcome line already sits
	// after the grade because the regrade it called for failed.
	if t.Grade != nil && appended == 0 && !outcomeAfterGrade(entries, key, latest.Started.RunID) {
		return errors.Join(problems...)
	}
	if judgeFailsToday(entries, key, now) >= shadowJudgeFailsPerDay {
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
		problems = append(problems, err)
		// A failed call lands on a graded line with no grade. Its cost,
		// paid whether or not the answer was usable, reaches the daily cap
		// and the scorecard; the line itself is what the per-day failure
		// cap counts, so the cap holds across restarts and processes; and
		// Fold does not take it for a grade.
		failed := shadow.Entry{
			At: now, Ev: "graded", Key: key, ShadowRunID: latest.Started.RunID,
			JudgeModel: cfg.ShadowJudgeModel(), CostUSD: je.CostUSD,
			Warning: cutText(judgeFailedPrefix + firstLineOf(err.Error())),
		}
		if err := shadow.Append(root, failed); err != nil {
			problems = append(problems, err)
		}
	case err != nil:
		problems = append(problems, err)
	default:
		if err := appendGraded(shadow.Append, root, line); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// appendGraded appends a graded line, and when the ledger refuses it, the
// same line without its warning and overlap fractions. If that is refused
// too, it appends the failed-judge line carrying the call's cost: the
// judge was paid either way, and with no line at all the cost would miss
// the daily cap and the call would run again on every pass, since the
// per-day failure cap counts only lines that landed. add is shadow.Append,
// passed in so a test can make it refuse.
func appendGraded(add func(string, shadow.Entry) error, root string, line shadow.Entry) error {
	err := add(root, line)
	if err == nil {
		return nil
	}
	slim := line
	slim.Warning, slim.PRFilesHit, slim.CodeRefsPathOverlap = "", nil, nil
	if add(root, slim) == nil {
		return nil
	}
	failed := shadow.Entry{
		At: line.At, Ev: "graded", Key: line.Key, ShadowRunID: line.ShadowRunID,
		JudgeModel: line.JudgeModel, CostUSD: line.CostUSD,
		Warning: cutText(judgeFailedPrefix + "the graded line could not be recorded: " + firstLineOf(err.Error())),
	}
	if ferr := add(root, failed); ferr != nil {
		return errors.Join(err, ferr)
	}
	return err
}

// judgeFailedPrefix starts the warning on a failed judge call's line.
const judgeFailedPrefix = "judge failed: "

// judgeFailsToday counts key's failed judge calls in now's local day.
func judgeFailsToday(entries []shadow.Entry, key string, now time.Time) int {
	y, m, d := now.Date()
	n := 0
	for _, e := range entries {
		if e.Ev != "graded" || e.Key != key || e.Grade != "" || !strings.HasPrefix(e.Warning, judgeFailedPrefix) {
			continue
		}
		if ey, em, ed := e.At.In(now.Location()).Date(); ey == y && em == m && ed == d {
			n++
		}
	}
	return n
}

// outcomeAfterGrade reports whether an outcome line for key comes after
// the newest grade of runID in the ledger.
func outcomeAfterGrade(entries []shadow.Entry, key, runID string) bool {
	lastOutcome, lastGrade := -1, -1
	for i, e := range entries {
		if e.Key != key {
			continue
		}
		switch {
		case e.Ev == "outcome":
			lastOutcome = i
		case e.Ev == "graded" && e.Grade != "" && e.ShadowRunID == runID:
			lastGrade = i
		}
	}
	return lastOutcome > lastGrade
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
// or fix run that has just completed, when the ledger holds a finished
// shadow run of it: that run's result is the truth the shadow run is
// graded on, and it is freshest now.
//
// Only the move into completed counts. The watcher reports every run on
// disk when it starts, and again whenever a finished run's state file is
// rewritten; neither is news, and each would cost a pass and perhaps a
// judge call. A run first seen after that initial sweep is new, so one
// first seen completed has just finished.
func (s *Service) shadowObserveRun(e Event) {
	if !s.opts.Shadow || e.Kind != KindRunUpdated || e.Run == nil {
		return
	}
	completed := string(store.StatusCompleted)
	id := e.WorkspaceID + "\x00" + e.Run.Key + "\x00" + e.Run.RunID
	s.shadow.mu.Lock()
	prev, known := s.shadow.runStatus[id]
	s.shadow.runStatus[id] = e.Run.Status
	swept, ctx := s.shadow.swept, s.shadow.ctx
	s.shadow.mu.Unlock()
	if !swept || (known && prev == completed) || e.Run.Status != completed || e.Run.Shadow {
		return
	}
	if e.Run.Kind != string(store.KindRCA) && e.Run.Kind != string(store.KindFix) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	wsID, key, runID := e.WorkspaceID, e.Run.Key, e.Run.RunID
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		ws, cfg, err := s.load(wsID)
		if err != nil || !cfg.Shadow.Enabled {
			return
		}
		// An eval run is a measurement like a shadow run, and says nothing
		// about how the ticket ended.
		if _, st, err := store.Open(ws.Root, runID); err != nil || st.Measurement() {
			return
		}
		entries, err := shadow.Read(ws.Root)
		if err != nil {
			return
		}
		// A pause or a quota near its limit holds back the judge as it
		// holds back runs; the next outcome tick after it lifts collects.
		if _, _, paused := shadow.PausedUntil(entries, s.now()); paused || s.shadowQuotaReason(cfg) != "" {
			return
		}
		t := shadow.Fold(entries)[key]
		if t == nil || t.Latest() == nil || t.Latest().Finished == nil {
			return
		}
		if _, err := s.shadowCollect(ctx, ws, cfg, []string{key}, true); err != nil {
			s.log(err)
		}
		s.shadowRepublish(ws, cfg)
	}()
}
