package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

// toServer sends every request the client makes to srv instead of the host
// it names, keeping the URL the client built — slack.com — as the one its
// redirect policy and host pin judge. That is how these tests reach an
// httptest server without the client's base being configurable.
type toServer struct{ srv *httptest.Server }

func (t toServer) RoundTrip(req *http.Request) (*http.Response, error) {
	target, _ := url.Parse(t.srv.URL)
	out := req.Clone(req.Context())
	out.URL.Scheme = target.Scheme
	out.URL.Host = target.Host
	out.Host = req.URL.Host
	return http.DefaultTransport.RoundTrip(out)
}

type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) add(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r)
}

func newClient(t *testing.T, h http.HandlerFunc) (*Client, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.add(r.Clone(context.Background()))
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New("xoxp-test-token")
	c.HTTP = &http.Client{Transport: toServer{srv}}
	return c, got
}

func msg(ts, user, text string) map[string]any {
	return map[string]any{"ts": ts, "user": user, "text": text, "user_profile": map[string]any{"display_name": user + "-name"}}
}

func TestFindLink(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		ok         bool
		want       Link
	}{
		{"a message", "see https://acme.slack.com/archives/C0123ABCD/p1712345678901234 please", true,
			Link{URL: "https://acme.slack.com/archives/C0123ABCD/p1712345678901234", Team: "acme", Channel: "C0123ABCD", TS: "1712345678.901234"}},
		{"a reply in a thread", "https://acme.slack.com/archives/C0123ABCD/p1712345699000100?thread_ts=1712345678.901234&cid=C0123ABCD", true,
			Link{URL: "https://acme.slack.com/archives/C0123ABCD/p1712345699000100?thread_ts=1712345678.901234&cid=C0123ABCD", Team: "acme", Channel: "C0123ABCD", TS: "1712345699.000100", ThreadTS: "1712345678.901234"}},
		{"an enterprise org", "https://acme.enterprise.slack.com/archives/G0123ABCD/p1712345678901234", true,
			Link{URL: "https://acme.enterprise.slack.com/archives/G0123ABCD/p1712345678901234", Team: "acme.enterprise", Channel: "G0123ABCD", TS: "1712345678.901234"}},
		{"Arabic around the link", "شوف https://acme.slack.com/archives/C0123ABCD/p1712345678901234 لو سمحت", true,
			Link{URL: "https://acme.slack.com/archives/C0123ABCD/p1712345678901234", Team: "acme", Channel: "C0123ABCD", TS: "1712345678.901234"}},
		{"a look-alike host", "https://acme.slack.com.example.org/archives/C0123ABCD/p1712345678901234", false, Link{}},
		{"plain http", "http://acme.slack.com/archives/C0123ABCD/p1712345678901234", false, Link{}},
		{"no link", "triage SBX-1", false, Link{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FindLink(tc.text)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("FindLink = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestReadThreadByThreadTS(t *testing.T) {
	c, got := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/conversations.replies" {
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []any{
			msg("1712345678.901234", "U1", "customer says the export is empty, #28310"),
			map[string]any{"ts": "1712345699.000100", "username": "deskbot", "text": "", "attachments": []any{map[string]any{"title": "SBX-1 export is empty"}}},
		}})
	})
	l, _ := FindLink("https://acme.slack.com/archives/C0123ABCD/p1712345699000100?thread_ts=1712345678.901234")
	th, err := c.Read(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	if !th.IsThread || len(th.Messages) != 2 || th.Messages[0].Author != "U1-name" || th.Messages[1].Author != "deskbot" {
		t.Fatalf("thread %+v", th)
	}
	if texts := th.Texts(); texts[0] != "" || !strings.Contains(strings.Join(texts, "\n"), "SBX-1 export is empty") {
		t.Fatalf("Texts = %q, want the linked message first and the attachment title in", texts)
	}
	r := got.reqs[0]
	if r.Header.Get("Authorization") != "Bearer xoxp-test-token" {
		t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
	}
	if r.Host != "slack.com" {
		t.Errorf("request went to host %q, want slack.com", r.Host)
	}
	q := r.URL.Query()
	if q.Get("channel") != "C0123ABCD" || q.Get("ts") != "1712345678.901234" || q.Get("limit") != "50" {
		t.Errorf("query %v", q)
	}
}

func TestReadOneMessageThenItsThread(t *testing.T) {
	c, got := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/conversations.history":
			m := msg("1712345678.901234", "U1", "is SBX-1 fixed?")
			m["reply_count"] = 2
			m["thread_ts"] = "1712345678.901234"
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []any{m}})
		case "/api/conversations.replies":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []any{
				msg("1712345678.901234", "U1", "is SBX-1 fixed?"),
				msg("1712345690.000001", "U2", "not yet"),
				msg("1712345691.000001", "U1", "thanks"),
			}})
		}
	})
	l, _ := FindLink("https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	th, err := c.Read(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	if !th.IsThread || len(th.Messages) != 3 {
		t.Fatalf("thread %+v", th)
	}
	h := got.reqs[0].URL.Query()
	if got.reqs[0].URL.Path != "/api/conversations.history" || h.Get("latest") != "1712345678.901234" || h.Get("inclusive") != "true" || h.Get("limit") != "1" {
		t.Errorf("history query %s %v", got.reqs[0].URL.Path, h)
	}
}

func TestReadOneMessageWithNoThread(t *testing.T) {
	c, got := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": []any{msg("1712345678.901234", "U1", "#28310 again")}})
	})
	l, _ := FindLink("https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	th, err := c.Read(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	if th.IsThread || len(th.Messages) != 1 || len(got.reqs) != 1 {
		t.Fatalf("thread %+v after %d calls", th, len(got.reqs))
	}
}

func TestReadTruncatesAtFifty(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		var msgs []any
		for i := 0; i < 60; i++ {
			msgs = append(msgs, msg(fmt.Sprintf("17123456%02d.000001", i), "U1", "line"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": msgs})
	})
	th, err := c.Read(context.Background(), Link{Channel: "C0123ABCD", TS: "1712345600.000001", ThreadTS: "1712345600.000001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Messages) != MaxMessages || !th.Truncated {
		t.Fatalf("%d messages, truncated %v", len(th.Messages), th.Truncated)
	}
}

func TestSlackErrorsAreClassified(t *testing.T) {
	for code, want := range map[string]source.Code{
		"missing_scope":     source.Auth,
		"invalid_auth":      source.Auth,
		"channel_not_found": source.NotFound,
		"fatal_error":       source.Internal,
	} {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
		})
		_, err := c.Read(context.Background(), Link{Channel: "C0123ABCD", TS: "1712345678.901234"})
		var serr *source.Error
		if !errors.As(err, &serr) || serr.Code != want || !strings.Contains(serr.Message, code) {
			t.Errorf("%s: err %v, want code %s", code, err, want)
		}
	}
}

// TestRedirectOffSlackIsRefused is the host pin: a response that points the
// client at any other host is refused rather than followed with the token.
func TestRedirectOffSlackIsRefused(t *testing.T) {
	c, got := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://collector.example.org/steal", http.StatusFound)
	})
	_, err := c.AuthTest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "untrusted host collector.example.org") {
		t.Fatalf("err %v, want the redirect refused by host", err)
	}
	if len(got.reqs) != 1 {
		t.Fatalf("%d requests reached the server, want only the first", len(got.reqs))
	}
	if strings.Contains(err.Error(), "steal") || strings.Contains(err.Error(), "xoxp") {
		t.Fatalf("error carries the redirect path or the token: %v", err)
	}
}

func TestAuthTest(t *testing.T) {
	c, got := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": "sam", "team": "Acme"})
	})
	who, err := c.AuthTest(context.Background())
	if err != nil || who.User != "sam" || who.Team != "Acme" {
		t.Fatalf("%+v %v", who, err)
	}
	if got.reqs[0].URL.Path != "/api/auth.test" {
		t.Errorf("path %s", got.reqs[0].URL.Path)
	}
}

func TestNoTokenIsAnAuthError(t *testing.T) {
	c := New("")
	_, err := c.AuthTest(context.Background())
	var serr *source.Error
	if !errors.As(err, &serr) || serr.Code != source.Auth {
		t.Fatalf("err %v", err)
	}
}
