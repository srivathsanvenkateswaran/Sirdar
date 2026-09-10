package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// DefaultInterval is how often the watcher polls the run directories.
const DefaultInterval = 500 * time.Millisecond

// flushGrace is how long a run's event log is still tailed after the run
// left preparing/running, so the last lines the runner wrote — the final
// event, the usage tick — are not lost to the poll interval.
const flushGrace = 2 * time.Second

// Watcher polls every registered workspace's run directories and turns what
// changed on disk into events: one run.updated per state.json that moved,
// one run.event per new line in an active run's events.jsonl. Workspaces
// added while it runs are picked up on the next tick. Standard library
// only: no filesystem notification API is involved.
type Watcher struct {
	reg      *Registry
	interval time.Duration
	sink     func(Event)

	// runs is touched only by the polling goroutine.
	runs map[string]*watchedRun

	// swept is set once the initial sweep has finished, so a run found
	// after it can be told from one that was already on disk.
	swept bool

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	done    chan struct{}
}

// watchedRun is what the watcher remembers between ticks about one run.
type watchedRun struct {
	dir   string
	wsID  string
	runID string

	mtime time.Time
	size  int64

	status     string
	offset     int64 // bytes of events.jsonl already emitted
	index      int   // 1-based index of the last emitted event
	flushUntil time.Time
}

// NewWatcher returns a watcher over reg that hands every event to sink. An
// interval of zero means DefaultInterval.
func NewWatcher(reg *Registry, interval time.Duration, sink func(Event)) *Watcher {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Watcher{reg: reg, interval: interval, sink: sink, runs: map[string]*watchedRun{}}
}

// Start begins polling. It returns immediately; polling stops when ctx is
// cancelled or Stop is called. Calling Start twice is a no-op.
func (w *Watcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	stop, done := w.stop, w.done
	w.mu.Unlock()

	// The initial sweep runs before Start returns. What it finds was
	// written before this process existed, so its event logs are adopted
	// silently: the UI backfills that history from Service.Events, and
	// replaying it here would only duplicate it. A run that appears after
	// this point is new, and is tailed from the top.
	w.tick()
	go w.loop(ctx, stop, done)
}

// Stop halts polling and waits for the polling goroutine to finish, so no
// event arrives after it returns.
func (w *Watcher) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	w.started = false
	stop, done := w.stop, w.done
	w.mu.Unlock()

	close(stop)
	<-done
}

func (w *Watcher) loop(ctx context.Context, stop, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			w.tick()
		}
	}
}

// active reports whether a run is one the watcher tails.
func active(status string) bool {
	return status == string(store.StatusPreparing) || status == string(store.StatusRunning)
}

// tick sweeps every workspace once.
func (w *Watcher) tick() {
	workspaces, err := w.reg.List()
	if err != nil {
		return
	}
	now := time.Now()
	seen := make(map[string]bool)

	for _, ws := range workspaces {
		matches, err := filepath.Glob(filepath.Join(ws.Root, ".sirdar", "runs", "*", "*", "state.json"))
		if err != nil {
			continue
		}
		for _, path := range matches {
			dir := filepath.Dir(path)
			id := ws.ID + "\x00" + dir
			seen[id] = true

			r := w.runs[id]
			if r == nil {
				r = &watchedRun{dir: dir, wsID: ws.ID, runID: filepath.Base(dir)}
				w.runs[id] = r
				if w.swept {
					// A run first seen now may already be finished —
					// it can start and end inside one interval — so
					// it is tailed for the grace period whatever its
					// status says, rather than only from an
					// active-to-inactive transition it never makes.
					r.flushUntil = now.Add(flushGrace)
				} else {
					w.adopt(r)
				}
			}
			w.check(r, path, now)
			if active(r.status) || now.Before(r.flushUntil) {
				w.tail(r)
			}
		}
	}

	w.swept = true

	// A workspace that was removed, or a run directory that was deleted,
	// stops being tracked so the map does not grow without bound.
	for id := range w.runs {
		if !seen[id] {
			delete(w.runs, id)
		}
	}
}

// check re-reads state.json when its mtime or size moved and publishes the
// new summary.
func (w *Watcher) check(r *watchedRun, path string, now time.Time) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	if fi.ModTime().Equal(r.mtime) && fi.Size() == r.size {
		return
	}
	r.mtime, r.size = fi.ModTime(), fi.Size()

	state, err := (store.Run{Dir: r.dir}).ReadState()
	if err != nil {
		// A half-written state.json is read again on the next tick,
		// because mtime and size have not been accepted yet.
		r.mtime, r.size = time.Time{}, -1
		return
	}
	previous := r.status
	r.status = string(state.Status)
	if state.RunID != "" {
		r.runID = state.RunID
	}
	if active(previous) && !active(r.status) {
		r.flushUntil = now.Add(flushGrace)
	}
	summary := SummaryOf(state)
	w.sink(Event{Kind: KindRunUpdated, WorkspaceID: r.wsID, Run: &summary})
}

// adopt reads a run's existing events.jsonl without publishing any of it,
// so the watcher's index carries on from the end of the file rather than
// restarting at 1 and colliding with the history the UI already has.
func (w *Watcher) adopt(r *watchedRun) { w.read(r, false) }

// tail emits every complete line appended to the run's events.jsonl since
// the last tick. A trailing partial line is left for the next one.
func (w *Watcher) tail(r *watchedRun) { w.read(r, true) }

// read advances the run's offset and index over every complete line it has
// not consumed yet, publishing each one when emit is set.
func (w *Watcher) read(r *watchedRun, emit bool) {
	f, err := os.Open(filepath.Join(r.dir, "events.jsonl"))
	if err != nil {
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return
	}
	if fi.Size() < r.offset {
		// The log was truncated or replaced, so read it from the top
		// again. The index keeps counting: it is the sequence number of
		// the events this subscriber has been sent, and rewinding it
		// would make the UI treat new events as ones it already has.
		r.offset = 0
	}
	if fi.Size() == r.offset {
		return
	}
	if _, err := f.Seek(r.offset, io.SeekStart); err != nil {
		return
	}

	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return // incomplete tail: re-read from r.offset next tick
		}
		r.offset += int64(len(line))

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev RunEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		r.index++
		if !emit {
			continue
		}
		event := ev
		w.sink(Event{
			Kind:        KindRunEvent,
			WorkspaceID: r.wsID,
			RunID:       r.runID,
			Index:       r.index,
			Event:       &event,
		})
	}
}
