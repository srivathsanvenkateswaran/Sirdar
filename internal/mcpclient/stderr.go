package mcpclient

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// DefaultStderrLines is how many lines a StderrTail keeps when the caller
// does not say. It matches what the provider adapters report in
// provider.Result.StderrTail.
const DefaultStderrLines = 50

// maxStderrLineBytes caps one captured line. A server that writes a
// megabyte-long blob to stderr — several do, on a crash — must not be able
// to hold that megabyte for the rest of the run.
const maxStderrLineBytes = 2000

// StderrTail is a bounded ring of the last lines the workspace's MCP
// servers wrote to stderr. A server's stderr is the only account of why it
// died, so it is captured for every run; it is also unbounded output from
// a subprocess, so it is capped in both directions — lines kept, and bytes
// per line — rather than buffered whole.
//
// It is safe for concurrent use: each server's stderr is copied by a
// goroutine of its own, and the session reads the tail from another.
type StderrTail struct {
	mu      sync.Mutex
	max     int
	lines   []string
	writers []*prefixedTail
}

// NewStderrTail returns a tail keeping the last max lines; max <= 0 means
// DefaultStderrLines.
func NewStderrTail(max int) *StderrTail {
	if max <= 0 {
		max = DefaultStderrLines
	}
	return &StderrTail{max: max}
}

// Writer returns the io.Writer to hand to Start for one server. Every line
// it captures is prefixed with the server's name, so a tail collected from
// several servers still says which one spoke.
func (t *StderrTail) Writer(server string) io.Writer {
	w := &prefixedTail{tail: t, prefix: server + ": "}
	t.mu.Lock()
	t.writers = append(t.writers, w)
	t.mu.Unlock()
	return w
}

// Lines returns the captured tail, including each server's trailing
// unterminated line — which is exactly what a crash looks like.
func (t *StderrTail) Lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]string(nil), t.lines...)
	for _, w := range t.writers {
		if len(w.partial) > 0 {
			out = append(out, w.prefix+string(w.partial))
		}
	}
	if len(out) > t.max {
		out = out[len(out)-t.max:]
	}
	return out
}

// push appends one complete line. The caller holds t.mu.
func (t *StderrTail) push(line string) {
	if len(line) > maxStderrLineBytes {
		line = line[:maxStderrLineBytes] + "…"
	}
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		t.lines = append([]string(nil), t.lines[len(t.lines)-t.max:]...)
	}
}

// prefixedTail is the per-server end of a StderrTail: it splits what one
// server writes into lines and stamps each with that server's name.
type prefixedTail struct {
	tail    *StderrTail
	prefix  string
	partial []byte // guarded by tail.mu
}

func (w *prefixedTail) Write(p []byte) (int, error) {
	t := w.tail
	t.mu.Lock()
	defer t.mu.Unlock()

	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		t.push(w.prefix + strings.TrimRight(string(w.partial[:i]), "\r"))
		w.partial = append([]byte(nil), w.partial[i+1:]...)
	}
	// An unterminated line is kept so it can still be reported, but only
	// up to the per-line cap: a server writing without newlines must not
	// grow this buffer without end.
	if len(w.partial) > maxStderrLineBytes {
		t.push(w.prefix + string(w.partial[:maxStderrLineBytes]) + "…")
		w.partial = nil
	}
	return len(p), nil
}
