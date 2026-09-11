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

	for {
		select {
		case <-r.Context().Done():
			// The client went away: drop the subscription and let the
			// handler return so the connection is released.
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
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
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

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
