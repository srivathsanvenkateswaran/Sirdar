package zendesk

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

const (
	testEmail    = "agent@example.com"
	testAPIToken = "tok-secret-abc123"
	testOAuth    = "oauth-secret-xyz789"
)

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// wantBasicHeader is the Authorization header value New produces for
// testEmail/testAPIToken.
func wantBasicHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(testEmail+"/token:"+testAPIToken))
}

func checkAuthHeader(t *testing.T, r *http.Request, want string) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization header = %q, want %q (path %s)", got, want, r.URL.Path)
	}
}

// --- New / Config validation ---

func TestNew_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr source.Code
	}{
		{"both basic and bearer", Config{Subdomain: "acme", Email: testEmail, APIToken: testAPIToken, OAuthToken: testOAuth}, source.Auth},
		{"email without token", Config{Subdomain: "acme", Email: testEmail}, source.Auth},
		{"token without email", Config{Subdomain: "acme", APIToken: testAPIToken}, source.Auth},
		{"no credentials", Config{Subdomain: "acme"}, source.Auth},
		{"no subdomain, basic", Config{Email: testEmail, APIToken: testAPIToken}, source.Internal},
		{"no subdomain, bearer", Config{OAuthToken: testOAuth}, source.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg, nil)
			if err == nil {
				t.Fatalf("New() error = nil, want %s", tt.wantErr)
			}
			var serr *source.Error
			if !errors.As(err, &serr) {
				t.Fatalf("New() error is not *source.Error: %v", err)
			}
			if serr.Code != tt.wantErr {
				t.Errorf("New() error code = %q, want %q", serr.Code, tt.wantErr)
			}
		})
	}
}

func TestNew_AuthHeaderVariants(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		c, err := New(Config{Subdomain: "acme", Email: testEmail, APIToken: testAPIToken}, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.authHeader != wantBasicHeader() {
			t.Errorf("authHeader = %q, want %q", c.authHeader, wantBasicHeader())
		}
	})
	t.Run("bearer", func(t *testing.T) {
		c, err := New(Config{Subdomain: "acme", OAuthToken: testOAuth}, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.authHeader != "Bearer "+testOAuth {
			t.Errorf("authHeader = %q, want %q", c.authHeader, "Bearer "+testOAuth)
		}
	})
	t.Run("default base URL from subdomain", func(t *testing.T) {
		c, err := New(Config{Subdomain: "acme", OAuthToken: testOAuth}, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.baseURL != "https://acme.zendesk.com" {
			t.Errorf("baseURL = %q, want %q", c.baseURL, "https://acme.zendesk.com")
		}
	})
	t.Run("default HTTP client", func(t *testing.T) {
		c, err := New(Config{Subdomain: "acme", OAuthToken: testOAuth}, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.hc == nil || c.hc.Timeout != 30*time.Second {
			t.Fatalf("expected default 30s HTTP client, got %+v", c.hc)
		}
	})
}

// --- Ping ---

func TestPing(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v2/users/me.json", func(w http.ResponseWriter, r *http.Request) {
			checkAuthHeader(t, r, wantBasicHeader())
			w.Write([]byte(`{"user":{"id":1,"name":"Agent"}}`))
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := c.Ping(context.Background()); err != nil {
			t.Fatalf("Ping: %v", err)
		}
	})

	t.Run("auth failure", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v2/users/me.json", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"Couldn't authenticate you"}`))
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		err = c.Ping(context.Background())
		assertCode(t, err, source.Auth)
		assertNoSecret(t, err)
	})
}

// --- Get ---

func newTicketServer(t *testing.T) (*httptest.Server, *Client) {
	t.Helper()
	ticketJSON := mustReadFile(t, "testdata/ticket.json")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		checkAuthHeader(t, r, wantBasicHeader())
		w.Header().Set("Content-Type", "application/json")
		w.Write(ticketJSON)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, c
}

func TestGet_MapsFields(t *testing.T) {
	_, c := newTicketServer(t)

	got, err := c.Get(context.Background(), "555")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != "555" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Subject != "Export fails" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.Status != "open" {
		t.Errorf("Status = %q", got.Status)
	}
	if got.Priority != "high" {
		t.Errorf("Priority = %q", got.Priority)
	}
	if got.Channel != "web" {
		t.Errorf("Channel = %q", got.Channel)
	}
	if got.Contact != "John Doe" {
		t.Errorf("Contact = %q", got.Contact)
	}
	if got.Customer != "Acme Co" {
		t.Errorf("Customer = %q", got.Customer)
	}
	if got.CustomerID != "200" {
		t.Errorf("CustomerID = %q", got.CustomerID)
	}
	// URL is built from the configured Subdomain, not the BaseURL override
	// used to reach the test server.
	if got.URL != "https://acme.zendesk.com/agent/tickets/555" {
		t.Errorf("URL = %q", got.URL)
	}
	wantCreated := time.Date(2026, 9, 10, 6, 0, 0, 0, time.UTC)
	if !got.CreatedAt.Equal(wantCreated) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, wantCreated)
	}
	wantUpdated := time.Date(2026, 9, 10, 6, 30, 0, 0, time.UTC)
	if !got.UpdatedAt.Equal(wantUpdated) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, wantUpdated)
	}
	if got.Fields["tags"] != "billing,urgent" {
		t.Errorf("Fields[tags] = %q", got.Fields["tags"])
	}
	if got.Fields["type"] != "incident" {
		t.Errorf("Fields[type] = %q", got.Fields["type"])
	}
	if got.Fields["group_id"] != "300" {
		t.Errorf("Fields[group_id] = %q", got.Fields["group_id"])
	}
	if got.Fields["assignee"] != "Agent Carol" {
		t.Errorf("Fields[assignee] = %q", got.Fields["assignee"])
	}
}

// --- Threads ---

// newThreadsServer serves ticket.json and a two-page comments feed, with
// every "PLACEHOLDER" in the fixtures replaced by the server's own URL so
// attachment content_url and next_page values resolve back to it. It uses
// TLS, not plain HTTP, because trustedNextPage requires next_page to be
// https — a real multi-page fetch has to satisfy that to prove pagination
// still works end to end under the stricter check.
func newThreadsServer(t *testing.T) (*httptest.Server, *Client) {
	t.Helper()
	ticketJSON := mustReadFile(t, "testdata/ticket.json")
	page1Raw := string(mustReadFile(t, "testdata/comments_page1.json"))
	page2JSON := mustReadFile(t, "testdata/comments_page2.json")

	mux := http.NewServeMux()
	var srvURL string // set once the server is up, read by the handler closures below
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		checkAuthHeader(t, r, wantBasicHeader())
		w.Header().Set("Content-Type", "application/json")
		w.Write(ticketJSON)
	})
	mux.HandleFunc("/api/v2/tickets/555/comments.json", func(w http.ResponseWriter, r *http.Request) {
		checkAuthHeader(t, r, wantBasicHeader())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			w.Write(page2JSON)
			return
		}
		w.Write([]byte(strings.ReplaceAll(page1Raw, "PLACEHOLDER", srvURL)))
	})
	mux.HandleFunc("/attachments/11/screenshot.png", func(w http.ResponseWriter, r *http.Request) {
		checkAuthHeader(t, r, wantBasicHeader())
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA"))
	})
	mux.HandleFunc("/inline/note.jpg", func(w http.ResponseWriter, r *http.Request) {
		checkAuthHeader(t, r, wantBasicHeader())
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("JPGDATA"))
	})

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, c
}

func TestThreads_TwoPagesRolesAndText(t *testing.T) {
	_, c := newThreadsServer(t)

	got, err := c.Threads(context.Background(), "555")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(Threads) = %d, want 3: %+v", len(got), got)
	}

	// Comment 1: author_id 100 == ticket requester_id -> customer, public.
	if got[0].Role != ticket.RoleCustomer {
		t.Errorf("msg0.Role = %q, want customer", got[0].Role)
	}
	if got[0].Author != "John Doe" {
		t.Errorf("msg0.Author = %q, want %q (no internal suffix, public)", got[0].Author, "John Doe")
	}
	if got[0].Text != "Hello from customer" {
		t.Errorf("msg0.Text = %q", got[0].Text)
	}
	if len(got[0].AttachmentIDs) != 1 || got[0].AttachmentIDs[0] != "11" {
		t.Errorf("msg0.AttachmentIDs = %v, want [11]", got[0].AttachmentIDs)
	}

	// Comment 2: author_id 400, side-loaded role "agent", not the
	// requester -> agent; public:false -> " (internal)" suffix; empty
	// plain_body falls back to htmltext.ToMarkdown(html_body); its inline
	// <img> must NOT appear in AttachmentIDs (only attachments[].id do).
	if got[1].Author != "Agent Carol (internal)" {
		t.Errorf("msg1.Author = %q", got[1].Author)
	}
	if got[1].Role != "agent" {
		t.Errorf("msg1.Role = %q, want agent", got[1].Role)
	}
	if !strings.Contains(got[1].Text, "Internal note") {
		t.Errorf("msg1.Text = %q, want markdown fallback containing %q", got[1].Text, "Internal note")
	}
	if len(got[1].AttachmentIDs) != 0 {
		t.Errorf("msg1.AttachmentIDs = %v, want none (inline images aren't attachments[])", got[1].AttachmentIDs)
	}

	// Comment 3 (page 2): author_id 999, side-loaded role "agent", not the
	// requester -> agent.
	if got[2].Author != "Dave" {
		t.Errorf("msg2.Author = %q", got[2].Author)
	}
	if got[2].Role != "agent" {
		t.Errorf("msg2.Role = %q, want agent", got[2].Role)
	}
	if got[2].Text != "Follow-up from agent" {
		t.Errorf("msg2.Text = %q", got[2].Text)
	}

	// Order is the API's own order across pages (creation order); no
	// re-sort should have happened.
	for i := 1; i < len(got); i++ {
		if got[i].At.Before(got[i-1].At) {
			t.Fatalf("messages not in ascending API order: %+v", got)
		}
	}
}

func TestThreads_RequesterLookupPropagatesNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"RecordNotFound"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Threads(context.Background(), "555")
	assertCode(t, err, source.NotFound)
}

// TestThreads_NextPageUntrustedHostStopsPaginationWithWarning proves
// fetchComments does not blindly follow next_page — which would carry this
// client's live Authorization header wherever it points — to a second
// server the account's own Zendesk instance never named. Page 1's
// next_page here points at a second, independent httptest listener; since
// it is not the client's configured host over https, it must be refused:
// zero requests reach that second listener, a warning names the problem,
// and Threads still returns page 1's comments rather than failing outright.
func TestThreads_NextPageUntrustedHostStopsPaginationWithWarning(t *testing.T) {
	var secondListenerHits int
	secondMux := http.NewServeMux()
	secondMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		secondListenerHits++
		w.Write([]byte(`{"comments":[],"next_page":null}`))
	})
	secondSrv := httptest.NewServer(secondMux)
	t.Cleanup(secondSrv.Close)

	ticketJSON := mustReadFile(t, "testdata/ticket.json")
	mainMux := http.NewServeMux()
	mainMux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write(ticketJSON)
	})
	mainMux.HandleFunc("/api/v2/tickets/555/comments.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{
			"comments": [{
				"id": 1, "author_id": 100, "public": true,
				"plain_body": "page one", "created_at": "2026-09-10T07:00:00Z"
			}],
			"users": [{"id": 100, "name": "John Doe", "role": "end-user"}],
			"next_page": %q
		}`, secondSrv.URL+"/api/v2/tickets/555/comments.json?page=2")
	})
	mainSrv := httptest.NewServer(mainMux)
	t.Cleanup(mainSrv.Close)

	c, err := New(Config{Subdomain: "acme", BaseURL: mainSrv.URL, Email: testEmail, APIToken: testAPIToken}, mainSrv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := c.Threads(context.Background(), "555")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(got) != 1 || got[0].Text != "page one" {
		t.Fatalf("Threads = %+v, want only page 1's comment", got)
	}
	if secondListenerHits != 0 {
		t.Errorf("second listener hits = %d, want 0", secondListenerHits)
	}

	warns := c.WarningsFor("555")
	if len(warns) != 1 || !strings.Contains(warns[0], "zendesk: next_page host not trusted:") {
		t.Fatalf("WarningsFor(555) = %v, want a single next_page-not-trusted warning", warns)
	}
}

// TestThreads_PaginationCappedAtMaxPages proves fetchComments cannot be
// looped forever by a feed whose next_page keeps validly pointing back at
// the same trusted host: it stops after maxCommentPages requests, with a
// warning, rather than following next_page without bound.
func TestThreads_PaginationCappedAtMaxPages(t *testing.T) {
	ticketJSON := mustReadFile(t, "testdata/ticket.json")
	var srvURL string
	var requests int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write(ticketJSON)
	})
	mux.HandleFunc("/api/v2/tickets/555/comments.json", func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprintf(w, `{
			"comments": [{"id": %d, "author_id": 999, "public": true, "plain_body": "c", "created_at": "2026-09-10T07:00:00Z"}],
			"users": [],
			"next_page": %q
		}`, requests, srvURL+"/api/v2/tickets/555/comments.json?page=next")
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := c.Threads(context.Background(), "555")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(got) != maxCommentPages {
		t.Fatalf("len(Threads) = %d, want %d (capped)", len(got), maxCommentPages)
	}
	if requests != maxCommentPages {
		t.Errorf("requests = %d, want %d", requests, maxCommentPages)
	}
	warns := c.WarningsFor("555")
	if len(warns) != 1 || !strings.Contains(warns[0], "pagination stopped after") {
		t.Fatalf("WarningsFor(555) = %v, want a pagination-cap warning", warns)
	}
}

// TestThreads_NextPageRedirectOffHostRefused: next_page is checked before it
// is followed, but the page it points at can still answer with a redirect,
// and that Location is server output like any other. The live Authorization
// header goes on every one of these requests, so the hop is refused rather
// than followed with the credential stripped — and the warning names the
// host without the target's query.
func TestThreads_NextPageRedirectOffHostRefused(t *testing.T) {
	ticketJSON := mustReadFile(t, "testdata/ticket.json")

	var foreignHits int
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignHits++
		t.Errorf("credentialed request reached a foreign host: %s %s (Authorization %q)",
			r.Method, r.URL.Path, r.Header.Get("Authorization"))
	}))
	t.Cleanup(foreign.Close)

	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write(ticketJSON)
	})
	mux.HandleFunc("/api/v2/tickets/555/comments.json", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			http.Redirect(w, r, foreign.URL+"/steal?token=abc", http.StatusFound)
			return
		}
		fmt.Fprintf(w, `{
			"comments": [{"id": 1, "author_id": 999, "public": true, "plain_body": "c", "created_at": "2026-09-10T07:00:00Z"}],
			"users": [],
			"next_page": %q
		}`, srvURL+"/api/v2/tickets/555/comments.json?page=2")
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	// One client for both listeners: each server's certificate is its own,
	// so the transport has to trust the pair.
	hc := srv.Client()
	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, hc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.Threads(context.Background(), "555"); err == nil {
		t.Fatal("Threads followed a next_page that redirected off-host, want an error")
	} else {
		if strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "/steal") {
			t.Errorf("error carries the redirect target's path or query: %v", err)
		}
		if !strings.Contains(err.Error(), "untrusted host") {
			t.Errorf("error = %v, want it to name the refused hop", err)
		}
	}
	if foreignHits != 0 {
		t.Errorf("foreign host received %d requests, want 0", foreignHits)
	}
}

// --- Attachments ---

func TestAttachments_DownloadsAndSanitizesNames(t *testing.T) {
	dir := t.TempDir()
	_, c := newThreadsServer(t)

	got, err := c.Attachments(context.Background(), "555", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(Attachments) = %d, want 2: %+v", len(got), got)
	}

	base := filepath.Base(dir)
	if got[0].ID != "11" || got[0].Name != "screenshot.png" || got[0].MIME != "image/png" {
		t.Errorf("attachment0 = %+v", got[0])
	}
	if got[0].Path != base+"/1-screenshot.png" {
		t.Errorf("attachment0.Path = %q", got[0].Path)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "1-screenshot.png")); err != nil || string(b) != "PNGDATA" {
		t.Errorf("downloaded file content = %q, %v", b, err)
	}

	// The inline image from comment 2 is collected too, named from the
	// image URL's basename.
	if got[1].ID != "inline-1" || got[1].Name != "note.jpg" {
		t.Errorf("attachment1 = %+v", got[1])
	}
	if got[1].Path != base+"/2-note.jpg" {
		t.Errorf("attachment1.Path = %q", got[1].Path)
	}

	if warns := c.WarningsFor("555"); len(warns) != 0 {
		t.Errorf("WarningsFor(555) = %v, want none", warns)
	}
}

func TestAttachments_UntrustedHostSkippedWithWarning(t *testing.T) {
	ticketJSON := mustReadFile(t, "testdata/ticket.json")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(ticketJSON)
	})
	mux.HandleFunc("/attachments/22/ok.png", func(w http.ResponseWriter, r *http.Request) {
		checkAuthHeader(t, r, wantBasicHeader())
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("OKDATA"))
	})
	var srvURL string
	mux.HandleFunc("/api/v2/tickets/555/comments.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"comments": [{
				"id": 1, "author_id": 100, "public": true,
				"plain_body": "hi", "created_at": "2026-09-10T07:00:00Z",
				"attachments": [
					{"id": 11, "file_name": "evil.png", "content_type": "image/png", "content_url": "http://evil.example.com/x.png"},
					{"id": 22, "file_name": "ok.png", "content_type": "image/png", "content_url": "%s/attachments/22/ok.png"}
				]
			}],
			"users": [{"id": 100, "name": "John Doe", "role": "end-user"}],
			"next_page": null
		}`, srvURL)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	got, err := c.Attachments(context.Background(), "555", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 1 || got[0].ID != "22" {
		t.Fatalf("Attachments = %+v, want only the trusted-host attachment (22) downloaded", got)
	}
	warns := c.WarningsFor("555")
	if len(warns) != 1 || !strings.Contains(warns[0], "attachment host not trusted: evil.example.com") {
		t.Fatalf("WarningsFor(555) = %v, want a not-trusted warning naming evil.example.com", warns)
	}
}

// hostRemapClient returns an *http.Client whose Transport dials addr,
// wherever the request thinks it's going, at hosts[hostname] instead. It
// lets a test use real-looking hostnames (an account's own subdomain, a
// *.zdusercontent.com CDN host) that this package's host-trust logic can
// tell apart, while every request still lands on a local httptest server.
func hostRemapClient(hosts map[string]string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err == nil {
					if target, ok := hosts[host]; ok {
						addr = target
					}
				}
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
}

func TestAttachments_TrustedZendeskHostGetsNoAuthorizationHeader(t *testing.T) {
	// The main server plays the account's own configured Zendesk host: it
	// serves the ticket, comments, and expects the client's credentials.
	mainMux := http.NewServeMux()
	mainMux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		checkAuthHeader(t, r, wantBasicHeader())
		w.Write(mustReadFile(t, "testdata/ticket.json"))
	})
	mainMux.HandleFunc("/api/v2/tickets/555/comments.json", func(w http.ResponseWriter, r *http.Request) {
		checkAuthHeader(t, r, wantBasicHeader())
		fmt.Fprint(w, `{
			"comments": [{
				"id": 1, "author_id": 100, "public": true,
				"plain_body": "hi", "created_at": "2026-09-10T07:00:00Z",
				"attachments": [{"id": 11, "file_name": "receipt.pdf", "content_type": "application/pdf", "content_url": "http://cdn.zdusercontent.com/files/receipt.pdf"}]
			}],
			"users": [{"id": 100, "name": "John Doe", "role": "end-user"}],
			"next_page": null
		}`)
	})
	mainSrv := httptest.NewServer(mainMux)
	t.Cleanup(mainSrv.Close)

	// The CDN server plays cdn.zdusercontent.com: trusted to download from
	// (its host ends in .zdusercontent.com) but must never see this
	// client's Authorization header, since a zdusercontent.com URL already
	// carries its own token.
	var sawAuth string
	var sawAuthSet bool
	cdnMux := http.NewServeMux()
	cdnMux.HandleFunc("/files/receipt.pdf", func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawAuthSet = true
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("PDFDATA"))
	})
	cdnSrv := httptest.NewServer(cdnMux)
	t.Cleanup(cdnSrv.Close)

	mainAddr := strings.TrimPrefix(mainSrv.URL, "http://")
	cdnAddr := strings.TrimPrefix(cdnSrv.URL, "http://")
	hc := hostRemapClient(map[string]string{
		"acme.zendesk.com":      mainAddr,
		"cdn.zdusercontent.com": cdnAddr,
	})

	c, err := New(Config{Subdomain: "acme", BaseURL: "http://acme.zendesk.com", Email: testEmail, APIToken: testAPIToken}, hc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	got, err := c.Attachments(context.Background(), "555", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Attachments = %+v, want 1 downloaded from the trusted CDN host", got)
	}
	if !sawAuthSet {
		t.Fatal("CDN handler was never hit")
	}
	if sawAuth != "" {
		t.Errorf("Authorization header sent to zdusercontent.com host = %q, want none", sawAuth)
	}
}

func TestAttachments_AllFail_ReturnsJoinedErrorNoWarnings(t *testing.T) {
	ticketJSON := mustReadFile(t, "testdata/ticket.json")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write(ticketJSON)
	})
	mux.HandleFunc("/api/v2/tickets/555/comments.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"comments": [{
				"id": 1, "author_id": 100, "public": true,
				"plain_body": "hi", "created_at": "2026-09-10T07:00:00Z",
				"attachments": [
					{"id": 11, "file_name": "a.png", "content_url": "http://evil-a.example.com/x.png"},
					{"id": 12, "file_name": "b.png", "content_url": "http://evil-b.example.com/y.png"}
				]
			}],
			"users": [{"id": 100, "name": "John Doe", "role": "end-user"}],
			"next_page": null
		}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	got, err := c.Attachments(context.Background(), "555", dir)
	if err == nil {
		t.Fatal("Attachments() error = nil, want a joined error when every attachment fails")
	}
	if len(got) != 0 {
		t.Errorf("Attachments = %+v, want none", got)
	}
	if warns := c.WarningsFor("555"); len(warns) != 0 {
		t.Errorf("WarningsFor(555) = %v, want none (failures are in the returned error, not warnings)", warns)
	}
}

// TestAttachments_RedirectToUntrustedHostRefused proves a download does not
// blindly follow a redirect: the attachment's content_url is on the
// account's own trusted host, which then 302s to an untrusted host. The
// redirect target must be refused before it is ever fetched, so the
// attachment fails (rather than being silently retrieved from wherever the
// redirect pointed).
func TestAttachments_RedirectToUntrustedHostRefused(t *testing.T) {
	ticketJSON := mustReadFile(t, "testdata/ticket.json")
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write(ticketJSON)
	})
	mux.HandleFunc("/api/v2/tickets/555/comments.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{
			"comments": [{
				"id": 1, "author_id": 100, "public": true,
				"plain_body": "hi", "created_at": "2026-09-10T07:00:00Z",
				"attachments": [{"id": 11, "file_name": "a.png", "content_url": %q}]
			}],
			"users": [{"id": 100, "name": "John Doe", "role": "end-user"}],
			"next_page": null
		}`, srvURL+"/redirect/a.png")
	})
	mux.HandleFunc("/redirect/a.png", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example.com/final.png", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	srvURL = srv.URL

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	dir := t.TempDir()
	got, err := c.Attachments(context.Background(), "555", dir)
	if err == nil {
		t.Fatal("Attachments() error = nil, want a failure: the only attachment's redirect target is untrusted")
	}
	if len(got) != 0 {
		t.Errorf("Attachments = %+v, want none", got)
	}
	if !strings.Contains(err.Error(), "untrusted host") {
		t.Errorf("error = %v, want it to say the redirect was refused for an untrusted host", err)
	}
}

// TestHostTrust_RequiresHTTPSAndRejectsUserinfo: the host is only half the
// question. A content_url arrives inside an API response body, so a hostile
// instance can put "http://" in front of a legitimate Zendesk host and watch
// the file cross the network in the clear, or hide the real destination
// behind userinfo. Neither is trusted — unless the workspace's own baseUrl
// is http, which is a choice it already made.
func TestHostTrust_RequiresHTTPSAndRejectsUserinfo(t *testing.T) {
	httpsClient, err := New(Config{Subdomain: "acme", OAuthToken: testOAuth}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	httpClient, err := New(Config{Subdomain: "acme", BaseURL: "http://acme.zendesk.com", OAuthToken: testOAuth}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		raw             string
		trusted         bool
		sendAuth        bool
		httpBaseTrusted bool
	}{
		{raw: "https://acme.zendesk.com/x.png", trusted: true, sendAuth: true, httpBaseTrusted: true},
		{raw: "https://cdn.zdusercontent.com/x.png", trusted: true, httpBaseTrusted: true},
		// The right host over the wrong transport.
		{raw: "http://acme.zendesk.com/x.png", httpBaseTrusted: true},
		{raw: "http://cdn.zdusercontent.com/x.png", httpBaseTrusted: true},
		// The real destination is attacker.example; acme.zendesk.com is
		// only the userinfo.
		{raw: "https://acme.zendesk.com@attacker.example/x.png"},
		// Userinfo on an otherwise legitimate URL is still refused.
		{raw: "https://user:pass@acme.zendesk.com/x.png"},
		{raw: "https://evil.example.com/x.png"},
		{raw: "ftp://acme.zendesk.com/x.png"},
	}
	for _, tc := range cases {
		u, perr := url.Parse(tc.raw)
		if perr != nil {
			t.Fatalf("parse %q: %v", tc.raw, perr)
		}
		trusted, sendAuth, _ := httpsClient.trust.Check(u)
		if trusted != tc.trusted || sendAuth != tc.sendAuth {
			t.Errorf("trust.Check(%q) = (%v, %v), want (%v, %v)", tc.raw, trusted, sendAuth, tc.trusted, tc.sendAuth)
		}
		if trusted, _, _ := httpClient.trust.Check(u); trusted != tc.httpBaseTrusted {
			t.Errorf("trust.Check(%q) with an http baseUrl = %v, want %v", tc.raw, trusted, tc.httpBaseTrusted)
		}
	}
}

// TestAttachments_PlainHTTPUploadURLRefused is the same rule seen from the
// outside: an https workspace never fetches an http attachment, and the
// listener serving it is never called.
func TestAttachments_PlainHTTPUploadURLRefused(t *testing.T) {
	var cdnHits int32
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&cdnHits, 1)
		w.Write([]byte("PNGDATA"))
	}))
	t.Cleanup(cdn.Close)

	mainMux := http.NewServeMux()
	mainMux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write(mustReadFile(t, "testdata/ticket.json"))
	})
	mainMux.HandleFunc("/api/v2/tickets/555/comments.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"comments": [{
				"id": 1, "author_id": 100, "public": true,
				"plain_body": "hi", "created_at": "2026-09-10T07:00:00Z",
				"attachments": [
					{"id": 11, "file_name": "plain.png", "content_url": "http://cdn.zdusercontent.com/files/plain.png"},
					{"id": 12, "file_name": "decoy.png", "content_url": "https://acme.zendesk.com@cdn.zdusercontent.com/files/decoy.png"}
				]
			}],
			"users": [{"id": 100, "name": "John Doe", "role": "end-user"}],
			"next_page": null
		}`)
	})
	mainSrv := httptest.NewServer(mainMux)
	t.Cleanup(mainSrv.Close)

	hc := hostRemapClient(map[string]string{
		"acme.zendesk.com":      strings.TrimPrefix(mainSrv.URL, "http://"),
		"cdn.zdusercontent.com": strings.TrimPrefix(cdn.URL, "http://"),
	})
	// The workspace is configured over https, so http is not its choice.
	c, err := New(Config{Subdomain: "acme", BaseURL: "https://acme.zendesk.com", Email: testEmail, APIToken: testAPIToken}, hc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The API calls themselves go over https to a host the remap dials
	// locally; only the attachment URLs above are the point of the test.
	c.baseURL = mainSrv.URL

	got, err := c.Attachments(context.Background(), "555", t.TempDir())
	if err == nil {
		t.Fatal("Attachments succeeded with both URLs untrusted, want an error")
	}
	if len(got) != 0 {
		t.Errorf("Attachments = %+v, want none", got)
	}
	if n := atomic.LoadInt32(&cdnHits); n != 0 {
		t.Errorf("the attachment host was called %d times, want 0", n)
	}
	if !strings.Contains(err.Error(), "not trusted") {
		t.Errorf("error = %v, want it to say the host was not trusted", err)
	}
}

// --- Error mapping ---

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		status int
		want   source.Code
	}{
		{http.StatusUnauthorized, source.Auth},
		{http.StatusForbidden, source.Auth},
		{http.StatusNotFound, source.NotFound},
		{http.StatusInternalServerError, source.Internal},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.status), func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(`{"error":"boom","detail":"nope"}`))
			})
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = c.Get(context.Background(), "555")
			assertCode(t, err, tt.want)
			assertNoSecret(t, err)
		})
	}
}

func TestErrorMapping_RateLimited_NoRetryAfter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(context.Background(), "555")
	assertCode(t, err, source.RateLimited)
}

func TestErrorMapping_RateLimited_ExceedsMaxRetryAfterReturnsImmediately(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	_, err = c.Get(context.Background(), "555")
	elapsed := time.Since(start)

	assertCode(t, err, source.RateLimited)
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry past the 30s cap)", calls)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Get took %v, want an immediate return with no wait", elapsed)
	}
}

func TestRetryAfter_HonouredOnce(t *testing.T) {
	var calls int
	ticketJSON := mustReadFile(t, "testdata/ticket.json")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write(ticketJSON)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.Get(context.Background(), "555")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "555" {
		t.Errorf("Get after retry returned %+v", got)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one 429, one retry)", calls)
	}
}

func TestErrorMessages_NeverContainCredentials(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/555.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Run("basic", func(t *testing.T) {
		c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, Email: testEmail, APIToken: testAPIToken}, srv.Client())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = c.Get(context.Background(), "555")
		assertNoSecret(t, err)
	})
	t.Run("bearer", func(t *testing.T) {
		c, err := New(Config{Subdomain: "acme", BaseURL: srv.URL, OAuthToken: testOAuth}, srv.Client())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = c.Get(context.Background(), "555")
		assertNoSecret(t, err)
	})
}

func assertCode(t *testing.T, err error, want source.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %s", want)
	}
	var serr *source.Error
	if !errors.As(err, &serr) {
		t.Fatalf("error is not *source.Error: %v", err)
	}
	if serr.Code != want {
		t.Errorf("error code = %q, want %q (message: %s)", serr.Code, want, serr.Message)
	}
}

func assertNoSecret(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	msg := err.Error()
	if strings.Contains(msg, testAPIToken) {
		t.Errorf("error message leaks the API token: %s", msg)
	}
	if strings.Contains(msg, testOAuth) {
		t.Errorf("error message leaks the OAuth token: %s", msg)
	}
}
