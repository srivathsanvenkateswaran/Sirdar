package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseMessage is one message read off the stream: the event name and its
// data line.
type sseMessage struct {
	name string
	data string
}

// readMessage reads lines until a message is complete, skipping the comment
// lines the keepalive writes. It fails the test rather than hanging when
// the stream ends early.
func readMessage(t *testing.T, br *bufio.Reader) sseMessage {
	t.Helper()
	var msg sseMessage
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read event stream: %v (partial %+v)", err, msg)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "":
			if msg.name != "" || msg.data != "" {
				return msg
			}
		case strings.HasPrefix(line, ":"):
			// keepalive comment
		case strings.HasPrefix(line, "event: "):
			msg.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			msg.data = strings.TrimPrefix(line, "data: ")
		default:
			t.Fatalf("unexpected stream line %q", line)
		}
	}
}

// startStream serves an event stream from f and returns the reader, the
// func that disconnects the client, and a channel closed when the handler
// has returned.
func startStream(t *testing.T, f *fake, keepalive time.Duration) (*bufio.Reader, func(), <-chan struct{}) {
	t.Helper()
	s := newServer(f, nil)
	s.keepalive = keepalive

	handled := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ServeHTTP(w, r)
		close(handled)
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The safety net: a stream that never produces anything fails the test
	// instead of hanging it.
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("cache control %q", cc)
	}
	return bufio.NewReader(resp.Body), cancel, handled
}

func TestSSEStreamsEventsThenStopsOnDisconnect(t *testing.T) {
	f := newFake()
	f.ch = make(chan Event, 8)
	f.unsubscribe = make(chan struct{})
	f.ch <- Event{Kind: "run.updated", WorkspaceID: knownWS, Run: &f.runs[0]}
	f.ch <- Event{Kind: "job.finished", JobID: knownJob, WorkspaceID: knownWS,
		Outcomes: []JobOutcome{{Key: "OMNI-2510", Status: "completed", RunID: knownRun}}}

	br, disconnect, handled := startStream(t, f, time.Hour)

	first := readMessage(t, br)
	if first.name != "run.updated" {
		t.Fatalf("first event %q", first.name)
	}
	var got Event
	if err := json.Unmarshal([]byte(first.data), &got); err != nil {
		t.Fatalf("decode %q: %v", first.data, err)
	}
	if got.Kind != "run.updated" || got.Run == nil || got.Run.RunID != knownRun {
		t.Fatalf("payload %+v", got)
	}

	second := readMessage(t, br)
	if second.name != "job.finished" {
		t.Fatalf("second event %q", second.name)
	}
	if !strings.Contains(second.data, `"jobId":"`+knownJob+`"`) {
		t.Fatalf("second data %q", second.data)
	}

	// The client goes away: the handler must return and drop the
	// subscription rather than leaking a goroutine per reload.
	disconnect()
	select {
	case <-handled:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the client disconnected")
	}
	select {
	case <-f.unsubscribe:
	default:
		t.Fatal("handler did not unsubscribe")
	}
}

func TestSSEKeepalive(t *testing.T) {
	f := newFake()
	f.ch = make(chan Event, 1)
	f.unsubscribe = make(chan struct{})

	br, _, _ := startStream(t, f, 10*time.Millisecond)

	// Nothing is on the channel, so the only thing that can arrive is the
	// comment that holds the connection open.
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimRight(line, "\n") != ": keepalive" {
		t.Fatalf("line %q, want a keepalive comment", line)
	}
}

func TestSSEStopsWhenServiceClosesTheChannel(t *testing.T) {
	f := newFake()
	f.ch = make(chan Event, 1)
	f.unsubscribe = make(chan struct{})

	_, _, handled := startStream(t, f, time.Hour)
	close(f.ch)

	select {
	case <-handled:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return when the service closed the channel")
	}
}
