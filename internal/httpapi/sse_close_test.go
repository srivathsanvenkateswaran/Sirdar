package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// deadWriter is a ResponseWriter whose body writes fail, the way a socket
// to a reader that has gone does once the kernel gives up on it.
type deadWriter struct {
	head    http.Header
	writes  int
	flushes int
}

func (w *deadWriter) Header() http.Header {
	if w.head == nil {
		w.head = http.Header{}
	}
	return w.head
}

func (w *deadWriter) Write(b []byte) (int, error) {
	w.writes++
	return 0, errors.New("write: broken pipe")
}

func (w *deadWriter) WriteHeader(int) {}

func (w *deadWriter) Flush() { w.flushes++ }

// TestSSEEndsOnAWriteThatFails is the other half of finding #2: a stream
// that cannot be written to has to be given up at once, because until it is
// it holds a subscription and one of the browser's six connections per
// host, and six of those stall every request the page makes.
func TestSSEEndsOnAWriteThatFails(t *testing.T) {
	f := newFake()
	f.ch = make(chan Event, 4)
	f.unsubscribe = make(chan struct{})
	f.ch <- Event{Kind: "run.updated", WorkspaceID: knownWS, Run: &f.runs[0]}

	s := newServer(f, nil)
	s.keepalive = time.Hour

	w := &deadWriter{}
	req := httptest.NewRequest("GET", "/api/events", nil)

	done := make(chan struct{})
	go func() {
		s.events(w, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler held the stream open after the write failed")
	}
	select {
	case <-f.unsubscribe:
	default:
		t.Fatal("the handler did not drop its subscription")
	}
}

// The keepalive is what notices a stream nobody is reading. Six dead ones
// fill a browser's connection pool for the host, so it has to be short
// enough that they do not accumulate over a few page loads.
func TestKeepaliveIsShortEnoughToClearDeadStreams(t *testing.T) {
	s := newServer(newFake(), nil)
	if s.keepalive > 5*time.Second {
		t.Fatalf("keepalive is %s; a dead stream is held that long before it is noticed", s.keepalive)
	}
}
