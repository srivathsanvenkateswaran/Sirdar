package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The in-process event path.
//
// A run the desktop shell or `sirdar serve` starts executes inside that
// process, so the line the executor appends to events.jsonl and the reader
// waiting for it are a function call apart. Before this, they were half a
// second apart: the only producer of run.event was the watcher's 500 ms
// poll, and a provider writing twenty lines a second reached the window as
// one lump about once a second.
//
// Service implements run.Sink. Every line the executor writes goes through
// Append, which advances a per-run cursor and publishes the event at once.
// The watcher goes on polling — a run the CLI started in another process
// has no sink here — and reads the same cursor before it tails, so the two
// never deliver the same line twice.

// liveCursor is how far one run's events.jsonl has been published from
// inside this process: the byte offset consumed and the index of the last
// event published, which is the sequence number the UI dedupes on.
//
// Its mutex is the lock the sink and the watcher's tail share. The sink
// holds it across the file write and the publish; the watcher holds it
// across its read of the same file. So the watcher sees either a line that
// is already accounted for in offset, or a file that does not hold it yet.
type liveCursor struct {
	mu     sync.Mutex
	offset int64
	index  int

	// wsID is the workspace the run belongs to, resolved once: the
	// registry is a file read, and this is on the per-line path.
	wsID  string
	runID string

	// closed is set when the run closed its log. The cursor is kept until
	// the watcher has caught up with it, because dropping it earlier would
	// let the flush-grace tail republish the last lines.
	closed bool
}

// liveRuns is the set of runs this process is writing events for.
type liveRuns struct {
	mu sync.Mutex
	m  map[string]*liveCursor
}

func newLiveRuns() *liveRuns { return &liveRuns{m: map[string]*liveCursor{}} }

// lookup returns the cursor for dir, or nil when no run in this process
// owns it.
func (l *liveRuns) lookup(dir string) *liveCursor {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.m[filepath.Clean(dir)]
}

// open returns the cursor for dir, creating it seeded from whatever the
// log already holds. A steer reopens a finished run's log, so a fresh
// cursor that started at zero would republish the whole transcript.
//
// resolve names the workspace the run belongs to and is called only when
// the cursor is created: it reads the registry off disk, which must not
// happen once per event line.
func (l *liveRuns) open(dir string, resolve func() (wsID, runID string)) *liveCursor {
	key := filepath.Clean(dir)

	l.mu.Lock()
	if c := l.m[key]; c != nil {
		l.mu.Unlock()
		return c
	}
	l.mu.Unlock()

	offset, index := logExtent(filepath.Join(key, "events.jsonl"))
	wsID, runID := resolve()

	l.mu.Lock()
	defer l.mu.Unlock()
	if c := l.m[key]; c != nil {
		return c
	}
	c := &liveCursor{offset: offset, index: index, wsID: wsID, runID: runID}
	l.m[key] = c
	return c
}

// release forgets dir outright, for a run directory that is no longer on
// disk.
func (l *liveRuns) release(dir string) {
	key := filepath.Clean(dir)
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}

// releaseIfClosed forgets dir when the run has closed its event log. The
// watcher calls it once it has stopped tailing the run, by which point the
// tail has already taken the cursor's final offset.
func (l *liveRuns) releaseIfClosed(dir string) {
	key := filepath.Clean(dir)
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.m[key]
	if c == nil {
		return
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		delete(l.m, key)
	}
}

// logExtent reports the size of an events.jsonl and how many complete
// lines it holds, which is the index the next line will be given.
func logExtent(path string) (int64, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	var offset int64
	index := 0
	br := bufio.NewReader(f)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			// A partial trailing line is not a line yet: leave the
			// offset before it so it is counted when it is finished.
			return offset, index
		}
		offset += int64(len(line))
		if len(bytes.TrimSpace(line)) > 0 {
			index++
		}
	}
}

// Append implements run.Sink: it writes one line to the run's event log
// and publishes it to every subscriber before the write's lock is let go.
func (s *Service) Append(dir string, write func() ([]byte, error)) error {
	c := s.live.open(dir, func() (string, string) {
		return s.workspaceOf(dir), filepath.Base(filepath.Clean(dir))
	})

	c.mu.Lock()
	defer c.mu.Unlock()
	// A steer appends to a log that was closed once already; the run is
	// live again, so the watcher must not take the cursor away.
	c.closed = false

	line, err := write()
	if err != nil {
		return err
	}
	c.offset += int64(len(line))

	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil
	}
	c.index++
	if c.wsID == "" {
		// The run is not under any registered workspace, so no reader is
		// listening for it. The cursor still advanced, which is what
		// keeps the watcher from publishing it either.
		return nil
	}
	var ev RunEvent
	if err := json.Unmarshal(trimmed, &ev); err != nil {
		return nil
	}
	event := ev
	s.observe(Event{
		Kind:        KindRunEvent,
		WorkspaceID: c.wsID,
		RunID:       c.runID,
		Index:       c.index,
		Event:       &event,
	})
	return nil
}

// Done implements run.Sink: the run has closed its log. The cursor is kept
// until the watcher has read past it; see liveRuns.
func (s *Service) Done(dir string) {
	if c := s.live.lookup(dir); c != nil {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
	}
}

// workspaceOf names the workspace a run directory belongs to, matching on
// the registered roots. It is called once per run, not once per line.
func (s *Service) workspaceOf(dir string) string {
	clean := filepath.Clean(dir)
	workspaces, err := s.reg.List()
	if err != nil {
		return ""
	}
	best := ""
	longest := 0
	for _, ws := range workspaces {
		root := filepath.Clean(ws.Root)
		if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
			continue
		}
		if len(root) > longest {
			best, longest = ws.ID, len(root)
		}
	}
	return best
}
