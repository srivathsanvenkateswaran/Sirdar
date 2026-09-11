package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sampleEvent() Event {
	return Event{
		Kind:           "triage",
		Key:            "OMNI-1",
		Title:          "Export fails for شركة الراقي",
		Status:         "completed",
		Confidence:     "medium",
		Classification: "code",
		Service:        "omni",
		RunID:          "20260910T090000Z-abcd",
		NotePath:       "/w/notes/OMNI-1 export-fails.md",
		TrackerURL:     "https://t/OMNI-1",
		HelpdeskURL:    "https://h/555",
		Turns:          12,
		CostUSD:        0.42,
		Minutes:        3.5,
		Workspace:      "oxo",
	}
}

// recorder is a webhook receiver that keeps every request it was sent and
// answers with a scripted sequence of statuses.
type recorder struct {
	srv      *httptest.Server
	bodies   chan []byte
	headers  chan http.Header
	requests atomic.Int32
}

func newRecorder(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, n int)) *recorder {
	t.Helper()
	rec := &recorder{bodies: make(chan []byte, 8), headers: make(chan http.Header, 8)}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(rec.requests.Add(1))
		body := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			if _, err := r.Body.Read(body); err != nil && err.Error() != "EOF" {
				t.Errorf("read body: %v", err)
			}
		}
		rec.bodies <- body
		rec.headers <- r.Header.Clone()
		if handle != nil {
			handle(w, r, n)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (r *recorder) url(path string) string { return r.srv.URL + path }

// plainJSON re-renders a decoded payload without Go's HTML escaping, so a
// test can assert on the Slack link syntax it was actually sent.
func plainJSON(t *testing.T, v any) string {
	t.Helper()
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func (r *recorder) body(t *testing.T) map[string]any {
	t.Helper()
	select {
	case raw := <-r.bodies:
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("payload is not JSON: %v\n%s", err, raw)
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("the receiver was never called")
		return nil
	}
}

// TestSlackMessageShape pins the Block Kit payload: a header naming the
// ticket and its state, the five fields an operator triages by, both
// links, and the note's path in a context block rather than the body of
// the message.
func TestSlackMessageShape(t *testing.T) {
	rec := newRecorder(t, nil)
	s := &Slack{WebhookURL: rec.url("/services/T000/B000/xoxbSECRET")}
	if err := s.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatal(err)
	}

	msg := rec.body(t)
	blocks, _ := msg["blocks"].([]any)
	if len(blocks) == 0 {
		t.Fatalf("no blocks: %v", msg)
	}
	header := blocks[0].(map[string]any)
	if header["type"] != "header" {
		t.Fatalf("first block %v", header)
	}
	text := header["text"].(map[string]any)
	if text["type"] != "plain_text" || text["text"] != "[OMNI-1] completed" {
		t.Fatalf("header text %v", text)
	}
	if msg["text"] != "[OMNI-1] completed" {
		t.Fatalf("fallback text %v", msg["text"])
	}

	flat := plainJSON(t, msg)
	for _, want := range []string{
		"*Confidence*\\nmedium", "*Classification*\\ncode", "*Service*\\nomni",
		"*Cost*\\n$0.42", "*Duration*\\n3.5 min",
		"<https://t/OMNI-1|Tracker>", "<https://h/555|Helpdesk>",
		"note: `/w/notes/OMNI-1 export-fails.md`",
		"triage · oxo · run 20260910T090000Z-abcd",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("message is missing %q:\n%s", want, flat)
		}
	}

	// A context block holds the note path; no block carries note text,
	// because the event never had any.
	last := blocks[len(blocks)-1].(map[string]any)
	if last["type"] != "context" {
		t.Errorf("last block %v", last)
	}
}

// TestSlackReasonAndMissingFields is the failed run: the reason is on the
// card, and the fields a failed run never filled in are left out rather
// than rendered empty.
func TestSlackReasonAndMissingFields(t *testing.T) {
	rec := newRecorder(t, nil)
	ev := Event{Kind: "triage", Key: "OMNI-2", Status: "failed", Reason: "the session ended without a JSON note"}
	if err := (&Slack{WebhookURL: rec.url("/hook")}).Notify(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	flat := plainJSON(t, rec.body(t))
	if !strings.Contains(flat, "the session ended without a JSON note") {
		t.Errorf("reason missing:\n%s", flat)
	}
	for _, unwanted := range []string{"Confidence", "Cost", "Duration", "Tracker"} {
		if strings.Contains(flat, unwanted) {
			t.Errorf("empty field %q was rendered:\n%s", unwanted, flat)
		}
	}
}

// TestTeamsAdaptiveCard pins the card envelope Teams accepts from both a
// Workflows URL and an old connector.
func TestTeamsAdaptiveCard(t *testing.T) {
	rec := newRecorder(t, nil)
	if err := (&Teams{WebhookURL: rec.url("/workflow/SECRET")}).Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatal(err)
	}

	msg := rec.body(t)
	if msg["type"] != "message" {
		t.Fatalf("envelope %v", msg)
	}
	atts := msg["attachments"].([]any)
	att := atts[0].(map[string]any)
	if att["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Fatalf("contentType %v", att["contentType"])
	}
	card := att["content"].(map[string]any)
	if card["type"] != "AdaptiveCard" || card["version"] != "1.4" {
		t.Fatalf("card %v", card)
	}

	var facts map[string]string
	for _, el := range card["body"].([]any) {
		e := el.(map[string]any)
		if e["type"] != "FactSet" {
			continue
		}
		facts = map[string]string{}
		for _, f := range e["facts"].([]any) {
			fact := f.(map[string]any)
			facts[fact["title"].(string)] = fact["value"].(string)
		}
	}
	want := map[string]string{
		"Confidence": "medium", "Classification": "code", "Service": "omni",
		"Cost": "$0.42", "Duration": "3.5 min", "Turns": "12",
	}
	for k, v := range want {
		if facts[k] != v {
			t.Errorf("fact %s = %q, want %q", k, facts[k], v)
		}
	}

	flat := plainJSON(t, card)
	for _, s := range []string{"[OMNI-1] completed", "note: /w/notes/OMNI-1 export-fails.md", `"url":"https://t/OMNI-1"`, `"url":"https://h/555"`} {
		if !strings.Contains(flat, s) {
			t.Errorf("card is missing %q:\n%s", s, flat)
		}
	}
}

// TestGenericPayloadHeadersAndSignature checks the three things a receiver
// depends on: the event as JSON, the configured headers, and a signature it
// can recompute.
func TestGenericPayloadHeadersAndSignature(t *testing.T) {
	rec := newRecorder(t, nil)
	g := &Generic{
		URL:     rec.url("/hooks/sirdar"),
		Headers: map[string]string{"Authorization": "Bearer token-123", "X-Env": "staging"},
		Secret:  "s3cret",
	}
	if err := g.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatal(err)
	}

	raw := <-rec.bodies
	headers := <-rec.headers
	var back Event
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("payload: %v\n%s", err, raw)
	}
	if back.Key != "OMNI-1" || back.Status != "completed" || back.NotePath == "" || back.Minutes != 3.5 {
		t.Fatalf("round trip %+v", back)
	}
	if headers.Get("Authorization") != "Bearer token-123" || headers.Get("X-Env") != "staging" {
		t.Fatalf("headers %v", headers)
	}
	if headers.Get("Content-Type") != "application/json" {
		t.Fatalf("content type %q", headers.Get("Content-Type"))
	}
	if got, want := headers.Get(SignatureHeader), Sign("s3cret", raw); got != want {
		t.Fatalf("signature %q, want %q", got, want)
	}
	if !strings.HasPrefix(headers.Get(SignatureHeader), "sha256=") {
		t.Fatalf("signature format %q", headers.Get(SignatureHeader))
	}
	if Sign("other", raw) == Sign("s3cret", raw) {
		t.Fatal("the signature does not depend on the secret")
	}
}

// TestGenericWithoutSecretIsUnsigned: a receiver that verifies nothing must
// not be sent a header implying it should.
func TestGenericWithoutSecretIsUnsigned(t *testing.T) {
	rec := newRecorder(t, nil)
	if err := (&Generic{URL: rec.url("/h")}).Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatal(err)
	}
	<-rec.bodies
	if sig := (<-rec.headers).Get(SignatureHeader); sig != "" {
		t.Fatalf("unsigned post carried %q", sig)
	}
}

// TestRetryAfter429 is the rate-limited channel: one retry, after the delay
// the receiver asked for.
func TestRetryAfter429(t *testing.T) {
	rec := newRecorder(t, func(w http.ResponseWriter, _ *http.Request, n int) {
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := (&Slack{WebhookURL: rec.url("/hook")}).Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("the retry did not carry the message: %v", err)
	}
	if got := rec.requests.Load(); got != 2 {
		t.Fatalf("%d requests, want 2", got)
	}
}

// TestRetryOnceOnServerError: a 5xx is retried once and once only — a
// finished run must not be held open by a webhook that keeps failing.
func TestRetryOnceOnServerError(t *testing.T) {
	rec := newRecorder(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusBadGateway)
	})
	err := (&Generic{URL: rec.url("/h")}).Notify(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("a failing receiver reported success")
	}
	if got := rec.requests.Load(); got != 2 {
		t.Fatalf("%d requests, want 2", got)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error %v", err)
	}
}

// TestNoRetryWhenRetryAfterIsTooLong: half a minute is the limit; a
// receiver asking for more is given up on immediately.
func TestNoRetryWhenRetryAfterIsTooLong(t *testing.T) {
	rec := newRecorder(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if err := (&Slack{WebhookURL: rec.url("/hook")}).Notify(context.Background(), sampleEvent()); err == nil {
		t.Fatal("want an error")
	}
	if got := rec.requests.Load(); got != 1 {
		t.Fatalf("%d requests, want 1", got)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   time.Duration
		ok     bool
	}{
		{"", defaultRetryAfter, true},
		{"0", 0, true},
		{"5", 5 * time.Second, true},
		{"31", 0, false},
		{"-1", 0, false},
		{"soon", 0, false},
	} {
		got, ok := retryAfter(tc.header)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("retryAfter(%q) = %v, %v; want %v, %v", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}

// TestTimeout: a receiver that never answers costs the run one timeout, not
// the rest of its life.
func TestTimeout(t *testing.T) {
	rec := newRecorder(t, func(_ http.ResponseWriter, r *http.Request, _ int) {
		<-r.Context().Done()
	})
	s := &Slack{WebhookURL: rec.url("/hook"), timeout: 50 * time.Millisecond}
	start := time.Now()
	err := s.Notify(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("want a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the post took %s", took)
	}
}

// TestErrorsNeverCarryTheWebhookURL: an incoming webhook URL is a bearer
// credential in its path, and errors end up in state.json's warnings and on
// the operator's terminal.
func TestErrorsNeverCarryTheWebhookURL(t *testing.T) {
	const secret = "xoxb-THE-SECRET-PATH"
	rec := newRecorder(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		http.Error(w, "no_service", http.StatusNotFound)
	})
	dest := rec.url("/services/T000/B000/" + secret)

	errs := []error{
		(&Slack{WebhookURL: dest}).Notify(context.Background(), sampleEvent()),
		(&Teams{WebhookURL: dest}).Notify(context.Background(), sampleEvent()),
		(&Generic{URL: dest, Secret: "hmac-secret", Headers: map[string]string{"Authorization": "Bearer token-123"}}).
			Notify(context.Background(), sampleEvent()),
		// A destination nothing is listening on: the transport error is
		// the one that quotes the whole URL back.
		(&Slack{WebhookURL: "https://127.0.0.1:1/services/" + secret}).Notify(context.Background(), sampleEvent()),
	}
	for i, err := range errs {
		if err == nil {
			t.Fatalf("error %d: want a failure", i)
		}
		for _, leak := range []string{secret, "hmac-secret", "token-123"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error %d leaked %q: %v", i, leak, err)
			}
		}
		if !strings.Contains(err.Error(), "127.0.0.1") {
			t.Errorf("error %d does not say which host failed: %v", i, err)
		}
	}
}

// TestMultiKeepsGoingAfterOneFailure: one webhook that is down must not
// cost the others their message.
func TestMultiKeepsGoingAfterOneFailure(t *testing.T) {
	good := newRecorder(t, nil)
	bad := newRecorder(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		http.Error(w, "invalid_payload", http.StatusBadRequest)
	})
	m := Multi{
		&Slack{WebhookURL: bad.url("/dead")},
		&Generic{URL: good.url("/live")},
	}
	err := m.Notify(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("the failure was swallowed")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error %v", err)
	}
	if good.requests.Load() != 1 || bad.requests.Load() != 1 {
		t.Fatalf("requests: good %d bad %d", good.requests.Load(), bad.requests.Load())
	}
	var back Event
	if err := json.Unmarshal(<-good.bodies, &back); err != nil || back.Key != "OMNI-1" {
		t.Fatalf("the surviving post did not carry the event: %v %+v", err, back)
	}
}

func TestMultiReportsEveryFailure(t *testing.T) {
	bad := newRecorder(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		http.Error(w, "nope", http.StatusBadRequest)
	})
	err := Multi{&Slack{WebhookURL: bad.url("/a")}, &Teams{WebhookURL: bad.url("/b")}}.
		Notify(context.Background(), sampleEvent())
	if err == nil || strings.Count(err.Error(), "400") != 2 {
		t.Fatalf("error %v", err)
	}
}
