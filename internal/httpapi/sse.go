package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// events streams the service's fan-out channel as Server-Sent Events. The
// browser transport opens this once with EventSource('/api/events') and
// dispatches on the event name, so every message goes out as
//
//	event: run.updated
//	data: {"kind":"run.updated",...}
//
// with a blank line between messages. A comment line every keepalive
// interval stops an idle connection being dropped by a proxy or by the
// browser's own timeout.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "the connection cannot be streamed")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Tell any reverse proxy in front of us not to buffer the stream.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsubscribe := s.svc.Subscribe()
	defer unsubscribe()

	ticker := newTicker(s.keepalive)
	defer ticker.Stop()

	// A write that cannot be made ends the stream there and then, rather
	// than leaving the handler, the subscription and one of the browser's
	// six connections per host held by a reader that has gone. The
	// deadline is what turns a client that has stopped reading — which
	// blocks the write for as long as the kernel will hold it — into an
	// error this loop can act on.
	rc := http.NewResponseController(w)
	write := func(format string, args ...any) bool {
		// ErrNotSupported from a wrapper with no deadline of its own is
		// not a reason to refuse the stream; the write itself still
		// reports a connection that has gone.
		_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	for {
		select {
		case <-r.Context().Done():
			// The client went away: drop the subscription and let the
			// handler return so the connection is released.
			return
		case <-ticker.C:
			if !write(": keepalive\n\n") {
				return
			}
		case e, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(e)
			if err != nil {
				// An event that will not marshal is this event's problem,
				// not the stream's.
				continue
			}
			if !write("event: %s\ndata: %s\n\n", e.Kind, data) {
				return
			}
		}
	}
}

// writeTimeout is how long one frame may take to reach the client before
// the stream is given up on.
const writeTimeout = 10 * time.Second

// newTicker wraps time.NewTicker so a zero or negative interval, which
// time.NewTicker panics on, means "no keepalives" instead.
func newTicker(d time.Duration) *time.Ticker {
	if d <= 0 {
		t := time.NewTicker(time.Hour)
		t.Stop()
		return t
	}
	return time.NewTicker(d)
}
