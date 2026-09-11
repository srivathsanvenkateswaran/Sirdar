package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// ErrUnsupported is returned by Queue when the workspace has no tracker, or
// its adapter does not implement listing. The HTTP layer answers 501.
var ErrUnsupported = errors.New("app: unsupported by this workspace")

// ErrNoSuchJob is returned when a job id is not one this process started,
// or the job has already finished.
var ErrNoSuchJob = errors.New("app: no such job")

// ErrNoSuchRun is returned for a run id no workspace holds.
var ErrNoSuchRun = errors.New("app: no such run")

// DepsBuilder assembles a runner's dependencies for one workspace. It is
// injected so tests can drive a stub provider, and so the desktop shell and
// the CLI share BuildDeps in production.
type DepsBuilder func(cfg *config.Config, provider, model string, stderr io.Writer) (runner.Deps, func(), error)

// Options configure a Service.
type Options struct {
	// Interval is the watcher's poll period; zero means DefaultInterval.
	Interval time.Duration
	// Buffer is each subscriber's channel depth; zero means DefaultBuffer.
	Buffer int
	// Stderr receives runner progress lines and adapter output.
	Stderr io.Writer
	// Now is the clock the quota history scan reads; zero means time.Now.
	Now func() time.Time
}

// DefaultBuffer is how many events a subscriber may fall behind by before
// the oldest is dropped.
const DefaultBuffer = 256

// Service is the application layer both desktop shells call: it owns the
// workspace registry, the run watcher, the derived quota, and the table of
// runs this process started.
type Service struct {
	reg     *Registry
	build   DepsBuilder
	opts    Options
	watcher *Watcher
	quota   *quotaTracker

	mu      sync.Mutex
	subs    map[int]chan Event
	nextSub int
	jobs    map[JobID]context.CancelFunc
	nextJob int
	// starting holds the workspace-and-key pairs a webhook delivery has a
	// triage in flight for, from before the run directory is read until the
	// job ends. See TriageIfIdle.
	starting map[string]struct{}

	// running counts the job goroutines Stop waits for.
	running sync.WaitGroup
}

// stopTimeout is how long Stop waits for the jobs it cancelled to end
// before giving up on them and shutting the watcher down anyway.
const stopTimeout = 10 * time.Second

// New returns a Service over reg. Nothing is watched until Start is called.
func New(reg *Registry, build DepsBuilder, opts Options) *Service {
	if opts.Buffer <= 0 {
		opts.Buffer = DefaultBuffer
	}
	s := &Service{
		reg:      reg,
		build:    build,
		opts:     opts,
		quota:    newQuotaTracker(),
		subs:     map[int]chan Event{},
		jobs:     map[JobID]context.CancelFunc{},
		starting: map[string]struct{}{},
	}
	s.quota.nowFunc = opts.Now
	s.watcher = NewWatcher(reg, opts.Interval, s.observe)
	return s
}

// Start reads the quota already recorded in the workspaces' event logs and
// begins watching. Watching stops when ctx is cancelled or Stop is called.
func (s *Service) Start(ctx context.Context) {
	s.scanQuota()
	s.watcher.Start(ctx)
}

// Stop cancels every job this process started, waits for their agent
// sessions to end, and then halts the watcher. A shell that exits without
// calling it leaves the provider subprocesses behind.
func (s *Service) Stop() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.jobs))
	for _, cancel := range s.jobs {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}

	finished := make(chan struct{})
	go func() {
		s.running.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(stopTimeout):
		// A run that will not end in ten seconds is not worth holding
		// the shutdown for; its state.json already says it was blocked.
	}

	s.watcher.Stop()
}

func (s *Service) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

func (s *Service) stderr() io.Writer {
	if s.opts.Stderr == nil {
		return io.Discard
	}
	return s.opts.Stderr
}

// scanQuota seeds the quota meter from the last day of recorded events.
func (s *Service) scanQuota() {
	workspaces, err := s.reg.List()
	if err != nil {
		return
	}
	roots := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		roots = append(roots, ws.Root)
	}
	for _, q := range s.quota.scanRoots(roots, s.now().Add(-quotaHistory)) {
		quota := q
		s.publish(Event{Kind: KindQuotaUpdate, Quota: &quota})
	}
}

// observe is the watcher's sink: every run event is offered to the quota
// tracker before it is fanned out.
func (s *Service) observe(e Event) {
	s.publish(e)
	if e.Kind != KindRunEvent || e.Event == nil {
		return
	}
	if q, ok := s.quota.observe(*e.Event); ok {
		quota := q
		s.publish(Event{Kind: KindQuotaUpdate, Quota: &quota})
	}
}

// Subscribe returns a channel of events and the func that closes it. A
// subscriber that stops reading loses its oldest events rather than
// stalling the watcher.
func (s *Service) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, s.opts.Buffer)

	s.mu.Lock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	s.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if c, ok := s.subs[id]; ok {
				delete(s.subs, id)
				close(c)
			}
		})
	}
}

// publish fans one event out. Every send is non-blocking: a full
// subscriber loses its oldest event, and the caller — the watcher's
// polling goroutine — is never held up by a slow reader.
func (s *Service) publish(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- e:
		default:
			select {
			case <-ch: // drop the oldest
			default:
			}
			select {
			case ch <- e:
			default:
			}
		}
	}
}

// --- workspaces -------------------------------------------------------

// Workspaces lists every registered workspace.
func (s *Service) Workspaces() ([]Workspace, error) { return s.reg.List() }

// AddWorkspace registers root after validating its configuration.
func (s *Service) AddWorkspace(root string) (Workspace, error) { return s.reg.Add(root) }

// RemoveWorkspace drops a workspace from the registry. Nothing on disk is
// touched.
func (s *Service) RemoveWorkspace(id string) error {
	if err := checkID(ErrNoSuchWorkspace, "workspace", id); err != nil {
		return err
	}
	return s.reg.Remove(id)
}

// load resolves a workspace id to its registration and its configuration.
func (s *Service) load(wsID string) (Workspace, *config.Config, error) {
	if err := checkID(ErrNoSuchWorkspace, "workspace", wsID); err != nil {
		return Workspace{}, nil, err
	}
	ws, err := s.reg.Find(wsID)
	if err != nil {
		return Workspace{}, nil, err
	}
	cfg, err := config.Load(ws.Root)
	if err != nil {
		return Workspace{}, nil, err
	}
	return ws, cfg, nil
}

// root resolves a workspace id to its root directory.
func (s *Service) root(wsID string) (string, error) {
	if err := checkID(ErrNoSuchWorkspace, "workspace", wsID); err != nil {
		return "", err
	}
	ws, err := s.reg.Find(wsID)
	if err != nil {
		return "", err
	}
	return ws.Root, nil
}

// --- reading runs -----------------------------------------------------

// Runs lists a workspace's runs, newest first. An empty key lists all.
func (s *Service) Runs(wsID, key string) ([]RunSummary, error) {
	root, err := s.root(wsID)
	if err != nil {
		return nil, err
	}
	// An empty key means every key, which is not a lookup and not a path.
	if key != "" {
		if err := checkID(ErrNoSuchRun, "key", key); err != nil {
			return nil, err
		}
	}
	states, err := store.List(root, key)
	if err != nil {
		return nil, err
	}
	out := make([]RunSummary, 0, len(states))
	for _, st := range states {
		out = append(out, SummaryOf(st))
	}
	return out, nil
}

// Run returns one run's full detail.
func (s *Service) Run(wsID, runID string) (RunDetail, error) {
	root, err := s.root(wsID)
	if err != nil {
		return RunDetail{}, err
	}
	if err := checkID(ErrNoSuchRun, "run", runID); err != nil {
		return RunDetail{}, err
	}
	_, state, err := store.Open(root, runID)
	if err != nil {
		return RunDetail{}, fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
	}
	return DetailOf(root, state), nil
}

// Events returns the run's recorded events after index `after`, and the
// index a following call should pass. Indexes are 1-based, matching what
// the watcher publishes.
func (s *Service) Events(wsID, runID string, after int) ([]RunEvent, int, error) {
	root, err := s.root(wsID)
	if err != nil {
		return nil, after, err
	}
	if err := checkID(ErrNoSuchRun, "run", runID); err != nil {
		return nil, after, err
	}
	rn, _, err := store.Open(root, runID)
	if err != nil {
		return nil, after, fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
	}

	f, err := os.Open(filepath.Join(rn.Dir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return []RunEvent{}, after, nil
		}
		return nil, after, fmt.Errorf("app: read events: %w", err)
	}
	defer f.Close()

	out := []RunEvent{}
	index := 0
	br := bufio.NewReader(f)
	for {
		line, readErr := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var ev RunEvent
			if json.Unmarshal(line, &ev) == nil {
				index++
				if index > after {
					out = append(out, ev)
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	return out, index, nil
}

// noteFile maps a note kind to the file the run wrote it to. A triage run
// holds only a triage note and an rca run only the rca pair, so asking a
// run for a note it never wrote is an error rather than the other note:
// note.md means something different in each.
func noteFile(kind string, runKind store.Kind) (string, error) {
	switch kind {
	case "": // whichever primary note this run's own kind produced
		return "note.md", nil
	case "triage":
		if runKind != store.KindTriage {
			return "", noteKindMismatch(kind, runKind)
		}
		return "note.md", nil
	case "rca":
		if runKind != store.KindRCA {
			return "", noteKindMismatch(kind, runKind)
		}
		return "note.md", nil
	case "resolution":
		if runKind != store.KindRCA {
			return "", noteKindMismatch(kind, runKind)
		}
		return "note-resolution.md", nil
	default:
		return "", fmt.Errorf("%w: note kind %q: %w", ErrNoSuchRun, kind, ErrInvalidArgument)
	}
}

func noteKindMismatch(kind string, runKind store.Kind) error {
	return fmt.Errorf("%w: a %s run has no %s note", ErrNoSuchRun, runKind, kind)
}

// Note returns the markdown of one of a run's notes.
func (s *Service) Note(wsID, runID, kind string) (string, error) {
	rn, state, err := s.openRun(wsID, runID)
	if err != nil {
		return "", err
	}
	name, err := noteFile(kind, state.Kind)
	if err != nil {
		return "", err
	}
	return readRunFile(rn.Dir, name)
}

// Prompt returns the prompt the run sent the agent.
func (s *Service) Prompt(wsID, runID string) (string, error) {
	rn, _, err := s.openRun(wsID, runID)
	if err != nil {
		return "", err
	}
	return readRunFile(rn.Dir, "prompt.md")
}

// openRun resolves a workspace and run id to the run on disk.
func (s *Service) openRun(wsID, runID string) (store.Run, store.State, error) {
	root, err := s.root(wsID)
	if err != nil {
		return store.Run{}, store.State{}, err
	}
	if err := checkID(ErrNoSuchRun, "run", runID); err != nil {
		return store.Run{}, store.State{}, err
	}
	rn, state, err := store.Open(root, runID)
	if err != nil {
		return store.Run{}, store.State{}, fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
	}
	return rn, state, nil
}

func readRunFile(dir, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", fmt.Errorf("app: read %s: %w", name, err)
	}
	return string(data), nil
}

// Register returns the workspace's run register, oldest line first.
func (s *Service) Register(wsID string) ([]RegisterRow, error) {
	root, err := s.root(wsID)
	if err != nil {
		return nil, err
	}
	rows, err := store.ReadRegister(root)
	if err != nil {
		return nil, err
	}
	out := make([]RegisterRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, RegisterRowOf(r))
	}
	return out, nil
}

// Doctor runs the workspace's health checks.
func (s *Service) Doctor(ctx context.Context, wsID string) ([]Check, error) {
	_, cfg, err := s.load(wsID)
	if err != nil {
		return nil, err
	}
	return RunDoctor(ctx, cfg), nil
}

// Quota returns the newest rate-limit reading per provider.
func (s *Service) Quota() []Quota { return s.quota.snapshot() }

// --- the queue --------------------------------------------------------

// Queue lists the workspace's tracker tickets, each decorated with its
// newest run. It returns ErrUnsupported when the workspace configures no
// tracker or the adapter cannot list.
func (s *Service) Queue(ctx context.Context, wsID string, f QueueFilter) ([]Ticket, error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return nil, err
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

	tickets, err := deps.Tracker.List(ctx, source.ListFilter{
		Assignee: f.Assignee,
		Status:   f.Status,
		Limit:    f.Limit,
	})
	if err != nil {
		var serr *source.Error
		if errors.As(err, &serr) && serr.Code == source.Unsupported {
			return nil, ErrUnsupported
		}
		return nil, err
	}

	out := make([]Ticket, 0, len(tickets))
	for _, t := range tickets {
		row := Ticket{
			Key:         t.Key,
			Title:       t.Title,
			Priority:    t.Priority,
			Status:      t.Status,
			Assignee:    t.Assignee,
			URL:         t.URL,
			HelpdeskRef: t.HelpdeskRef,
			UpdatedAt:   wireTime(t.UpdatedAt),
		}
		if states, err := store.List(ws.Root, t.Key); err == nil && len(states) > 0 {
			latest := SummaryOf(states[0])
			row.LatestRun = &latest
		}
		out = append(out, row)
	}
	return out, nil
}

// --- jobs -------------------------------------------------------------

// StartTriage triages every key in the background and returns the job id
// that can cancel it. The runs themselves surface through the watcher.
func (s *Service) StartTriage(ctx context.Context, wsID string, keys []string, o TriageOptions) (JobID, error) {
	return s.startTriage(ctx, wsID, keys, o, nil)
}

// startTriage is StartTriage with a hook the webhook path uses: done runs
// when the job ends, however it ends, and releases the in-flight claim
// TriageIfIdle took on the key.
func (s *Service) startTriage(ctx context.Context, wsID string, keys []string, o TriageOptions, done func()) (JobID, error) {
	if len(keys) == 0 {
		return "", fmt.Errorf("%w: no keys to triage", ErrInvalidArgument)
	}
	for _, key := range keys {
		if err := checkID(ErrNoSuchRun, "key", key); err != nil {
			return "", err
		}
	}
	return s.startJob(ctx, wsID, o.Provider, o.Model, done, func(jctx context.Context, deps runner.Deps) []JobOutcome {
		r := &runner.Runner{Deps: deps}
		outs, err := r.Triage(jctx, keys, runner.Options{
			Model:       o.Model,
			Concurrency: deps.Config.Concurrency,
			DryRun:      o.DryRun,
		})
		if err != nil {
			return s.failed(keys, err)
		}
		return outcomesOf(outs)
	}, func(err error) []JobOutcome { return s.failed(keys, err) })
}

// StartRCA produces the RCA note and resolution draft for one key.
func (s *Service) StartRCA(ctx context.Context, wsID, key string, o RCAOptions) (JobID, error) {
	if err := checkID(ErrNoSuchRun, "key", key); err != nil {
		return "", err
	}
	return s.start(ctx, wsID, "", "", func(jctx context.Context, deps runner.Deps) []JobOutcome {
		r := &runner.Runner{Deps: deps}
		out, err := r.RCA(jctx, key, runner.RCAOptions{
			PRURL:      o.PRURL,
			Resolution: o.Resolution,
		})
		if err != nil {
			s.log(err)
			if out.State.RunID == "" {
				return s.failed([]string{key}, nil)
			}
		}
		return outcomesOf([]runner.Outcome{out})
	}, func(err error) []JobOutcome { return s.failed([]string{key}, err) })
}

// Resume continues a blocked run, answering the agent's question with
// answer when it asked one.
func (s *Service) Resume(ctx context.Context, wsID, runID, answer string) (JobID, error) {
	if err := checkID(ErrNoSuchRun, "run", runID); err != nil {
		return "", err
	}
	return s.start(ctx, wsID, "", "", func(jctx context.Context, deps runner.Deps) []JobOutcome {
		// The runner reads the operator's answer from Stdin; a desktop
		// session has none, so the answer is handed over as the input.
		deps.Stdin = strings.NewReader(answer + "\n")
		r := &runner.Runner{Deps: deps}
		out, err := r.Resume(jctx, runID)
		if err != nil {
			s.log(err)
			if out.State.RunID == "" {
				return []JobOutcome{{Key: out.Key, Status: string(store.StatusFailed), RunID: runID}}
			}
		}
		return outcomesOf([]runner.Outcome{out})
	}, func(err error) []JobOutcome {
		s.log(err)
		return []JobOutcome{{Status: string(store.StatusFailed), RunID: runID}}
	})
}

// start registers a job, builds the workspace's dependencies, and runs work
// in the background. The job's context is detached from the caller's, so a
// request that returns does not cancel the run it started; only Cancel and
// Stop do.
func (s *Service) start(
	ctx context.Context,
	wsID, providerName, model string,
	work func(context.Context, runner.Deps) []JobOutcome,
	onBuildError func(error) []JobOutcome,
) (JobID, error) {
	return s.startJob(ctx, wsID, providerName, model, nil, work, onBuildError)
}

// startJob is start with done: a callback the job goroutine runs last,
// whatever the job did. It is how the webhook path holds a key from before
// the run directory is read until the run that read it has finished.
func (s *Service) startJob(
	ctx context.Context,
	wsID, providerName, model string,
	done func(),
	work func(context.Context, runner.Deps) []JobOutcome,
	onBuildError func(error) []JobOutcome,
) (JobID, error) {
	ws, cfg, err := s.load(wsID)
	if err != nil {
		return "", err
	}

	jctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	id := s.addJob(cancel)

	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer cancel()
		if done != nil {
			defer done()
		}

		var outcomes []JobOutcome
		deps, cleanup, err := s.build(cfg, providerName, model, Synced(s.stderr()))
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			outcomes = onBuildError(err)
		} else {
			// A job started from a UI has no terminal behind it, so it
			// must never inherit the process's stdin: a runner that
			// asked for an answer would hang on it. Resume, the one
			// flow with an answer, supplies its own reader.
			deps.Stdin = strings.NewReader("")
			outcomes = work(jctx, deps)
		}

		s.dropJob(id)
		s.publish(Event{Kind: KindJobFinished, JobID: id, WorkspaceID: ws.ID, Outcomes: outcomes})
	}()
	return id, nil
}

func (s *Service) addJob(cancel context.CancelFunc) JobID {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextJob++
	id := JobID(fmt.Sprintf("job-%d", s.nextJob))
	s.jobs[id] = cancel
	return id
}

func (s *Service) dropJob(id JobID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, id)
}

// Cancel stops a job this process started. A run it had already begun ends
// as blocked, so it can be resumed later.
func (s *Service) Cancel(id JobID) error {
	s.mu.Lock()
	cancel, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoSuchJob, id)
	}
	cancel()
	return nil
}

// Jobs lists the ids of the jobs still running.
func (s *Service) Jobs() []JobID {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JobID, 0, len(s.jobs))
	for id := range s.jobs {
		out = append(out, id)
	}
	return out
}

// outcomesOf reduces the runner's outcomes to what the UI needs.
func outcomesOf(outs []runner.Outcome) []JobOutcome {
	out := make([]JobOutcome, 0, len(outs))
	for _, o := range outs {
		out = append(out, JobOutcome{
			Key:    o.Key,
			Status: string(o.State.Status),
			RunID:  o.State.RunID,
		})
	}
	return out
}

// failed is the outcome list for a job that never reached the runner.
func (s *Service) failed(keys []string, err error) []JobOutcome {
	out := make([]JobOutcome, 0, len(keys))
	for _, key := range keys {
		out = append(out, JobOutcome{Key: key, Status: string(store.StatusFailed)})
	}
	s.log(err)
	return out
}

// log publishes an error as a log event for the UI's activity pane.
func (s *Service) log(err error) {
	if err == nil {
		return
	}
	s.publish(Event{Kind: KindLog, Text: err.Error()})
}
