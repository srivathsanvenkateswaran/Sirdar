package run

import "github.com/srivathsanvenkateswaran/sirdar/internal/store"

// Sink is the live reader of a run this process is hosting. The desktop
// shell and `sirdar serve` both run the executor inside themselves, and
// without a sink every line it appends to events.jsonl reaches the window
// only when the filesystem poller next sweeps the run directories — half a
// second later on average, and in one lump for the whole interval. A sink
// hands the line on as it is written, so a provider writing twenty lines a
// second reads as twenty lines a second.
//
// A run started by another process — the CLI — has no sink here. The
// poller still finds it, which is why the two must agree on what has been
// delivered: the sink owns a per-run cursor over events.jsonl, and the
// poller reads that cursor before it tails, so a line the sink published
// is never published a second time.
type Sink interface {
	// Append writes one line through write — which appends it to the run's
	// event log and returns the bytes it wrote — and publishes it. The
	// sink advances its cursor for dir by those bytes, and holds it
	// against a concurrent tail for as long as the write takes, so a
	// poller either sees the line already accounted for or does not see
	// it on disk yet. A write error is returned unchanged and nothing is
	// published.
	Append(dir string, write func() ([]byte, error)) error

	// Done says the run in dir has closed its event log. The cursor stays
	// until whatever tails the file has caught up with it; nothing may be
	// appended through the sink for dir afterwards without opening the log
	// again.
	Done(dir string)
}

// eventLog is a run's events.jsonl with this process's sink attached when
// there is one. Every line goes through the same call, so no path writes an
// event the live readers do not get.
type eventLog struct {
	log  *store.EventLog
	sink Sink
	dir  string
}

// openEventLog opens rn's event log, wired to the runner's sink.
func (r *Runner) openEventLog(rn store.Run) (*eventLog, error) {
	log, err := rn.OpenEventLog()
	if err != nil {
		return nil, err
	}
	return &eventLog{log: log, sink: r.Sink, dir: rn.Dir}, nil
}

// Append writes one event, and publishes it when this process has a sink.
func (l *eventLog) Append(kind string, payload any) error {
	if l.sink == nil {
		return l.log.Append(kind, payload)
	}
	return l.sink.Append(l.dir, func() ([]byte, error) { return l.log.AppendLine(kind, payload) })
}

// Close closes the file and tells the sink the run is done with it.
func (l *eventLog) Close() error {
	if l.sink != nil {
		l.sink.Done(l.dir)
	}
	return l.log.Close()
}
