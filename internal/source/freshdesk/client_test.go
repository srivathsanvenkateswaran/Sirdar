package freshdesk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

const (
	testAPIKey = "test-api-key-secret"
	testDomain = "acme.freshdesk.com"
	testCDN    = "cdn.freshdesk.com"
)

// --- harness ---

// hostRouter dials the real address of a virtual Freshdesk hostname without
// touching DNS: it keeps the original Host on the wire (so the server side
// can tell which virtual host was targeted, and Go re-sends the
// Authorization header only when a redirect stays on the same host) while
// redirecting the actual TCP connection to an httptest listener.
type hostRouter struct {
	routes map[string]string // virtual host -> real "host:port"
}

func (h hostRouter) RoundTrip(req *http.Request) (*http.Response, error) {
	addr, ok := h.routes[req.URL.Host]
	if !ok {
		return nil, fmt.Errorf("hostRouter: no route for host %q", req.URL.Host)
	}
	r2 := req.Clone(req.Context())
	r2.Host = req.URL.Host
	r2.URL.Scheme = "http"
	r2.URL.Host = addr
	return http.DefaultTransport.RoundTrip(r2)
}

func addrOf(ts *httptest.Server) string {
	return strings.TrimPrefix(ts.URL, "http://")
}

func newClient(t *testing.T, routes map[string]string, domain string) *Client {
	t.Helper()
	hc := &http.Client{Transport: hostRouter{routes: routes}}
	c, err := New(Config{Domain: domain, APIKey: testAPIKey}, hc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func writeFixture(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(fixture(t, name))
}

func wantBasicAuth(t *testing.T, r *http.Request) {
	t.Helper()
	user, pass, ok := r.BasicAuth()
	if !ok || user != testAPIKey || pass != "X" {
		t.Errorf("%s: basic auth = (%q, %q, %v), want (%q, \"X\", true)", r.URL.Path, user, pass, ok, testAPIKey)
	}
}

func sourceCode(t *testing.T, err error) source.Code {
	t.Helper()
	var serr *source.Error
	if !errors.As(err, &serr) {
		t.Fatalf("error is not *source.Error: %v (%T)", err, err)
	}
	return serr.Code
}

// countingServer records how many times each path was hit, for asserting
// caching (agent name lookups) and pagination behaviour.
type countingServer struct {
	*httptest.Server
	mu   sync.Mutex
	hits map[string]int
}

func newCountingServer(t *testing.T, h http.HandlerFunc) *countingServer {
	t.Helper()
	cs := &countingServer{hits: map[string]int{}}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		cs.hits[r.URL.Path]++
		cs.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *countingServer) count(path string) int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.hits[path]
}

// --- Config / New ---

func TestNew_ValidatesConfig(t *testing.T) {
	if _, err := New(Config{Domain: "", APIKey: "k"}, nil); err == nil {
		t.Error("New with empty domain: want error")
	}
	if _, err := New(Config{Domain: "acme.freshdesk.com", APIKey: ""}, nil); err == nil {
		t.Error("New with empty apiKey: want error")
	}
	c, err := New(Config{Domain: "https://Acme.Freshdesk.com/", APIKey: "k"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.Domain != "Acme.Freshdesk.com" {
		t.Errorf("Domain = %q, want scheme and trailing slash trimmed", c.cfg.Domain)
	}
	if c.baseURL != "https://Acme.Freshdesk.com" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
}

// --- Get ---

func mainHandler(t *testing.T, ticketFixture, convFixture string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		switch r.URL.Path {
		case "/api/v2/agents/me":
			writeFixture(t, w, "agent.json")
		case "/api/v2/tickets/123":
			writeFixture(t, w, ticketFixture)
		case "/api/v2/tickets/123/conversations":
			writeFixture(t, w, convFixture)
		case "/api/v2/agents/501":
			writeFixture(t, w, "agent.json")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestGet_MapsFields(t *testing.T) {
	ts := newCountingServer(t, mainHandler(t, "ticket.json", "conversations.json"))
	c := newClient(t, map[string]string{testDomain: addrOf(ts.Server)}, testDomain)

	got, err := c.Get(context.Background(), "123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != "123" {
		t.Errorf("ID = %q, want 123", got.ID)
	}
	if got.Subject != "Checkout fails on payment step" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.Status != "Pending" {
		t.Errorf("Status = %q, want Pending", got.Status)
	}
	if got.Priority != "Urgent" {
		t.Errorf("Priority = %q, want Urgent", got.Priority)
	}
	if got.Channel != "email" {
		t.Errorf("Channel = %q, want email", got.Channel)
	}
	if got.Contact != "Priya Shah" {
		t.Errorf("Contact = %q", got.Contact)
	}
	if got.Customer != "Acme Corp" {
		t.Errorf("Customer = %q", got.Customer)
	}
	if got.CustomerID != "501" {
		t.Errorf("CustomerID = %q", got.CustomerID)
	}
	if got.URL != "https://acme.freshdesk.com/a/tickets/123" {
		t.Errorf("URL = %q", got.URL)
	}
	wantCreated := time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)
	if !got.CreatedAt.Equal(wantCreated) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, wantCreated)
	}
	if got.Fields["tags"] != "billing, urgent" {
		t.Errorf("Fields[tags] = %q", got.Fields["tags"])
	}
	if got.Fields["type"] != "Problem" {
		t.Errorf("Fields[type] = %q", got.Fields["type"])
	}
	if got.Fields["group_id"] != "42" {
		t.Errorf("Fields[group_id] = %q", got.Fields["group_id"])
	}
	if got.Fields["responder_id"] != "501" {
		t.Errorf("Fields[responder_id] = %q", got.Fields["responder_id"])
	}
	if got.Fields["description_text"] != "It just spins forever. See for a screenshot." {
		t.Errorf("Fields[description_text] = %q", got.Fields["description_text"])
	}
}

func TestGet_StatusPriorityChannelMapping(t *testing.T) {
	cases := []struct {
		status, priority, source              int
		wantStatus, wantPriority, wantChannel string
	}{
		{2, 1, 1, "Open", "Low", "email"},
		{3, 2, 2, "Pending", "Medium", "portal"},
		{4, 3, 3, "Resolved", "High", "phone"},
		{5, 4, 7, "Closed", "Urgent", "chat"},
		{9, 4, 9, "status-9", "Urgent", "feedback_widget"},
		{2, 1, 10, "Open", "Low", "outbound_email"},
		{2, 1, 42, "Open", "Low", "source-42"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d/%d/%d", tc.status, tc.priority, tc.source), func(t *testing.T) {
			body := fmt.Sprintf(`{"id":123,"subject":"x","status":%d,"priority":%d,"source":%d,"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`,
				tc.status, tc.priority, tc.source)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantBasicAuth(t, r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(ts.Close)
			c := newClient(t, map[string]string{testDomain: addrOf(ts)}, testDomain)

			got, err := c.Get(context.Background(), "123")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.Priority != tc.wantPriority {
				t.Errorf("Priority = %q, want %q", got.Priority, tc.wantPriority)
			}
			if got.Channel != tc.wantChannel {
				t.Errorf("Channel = %q, want %q", got.Channel, tc.wantChannel)
			}
		})
	}
}

// --- Ping ---

func TestPing_Success(t *testing.T) {
	ts := newCountingServer(t, mainHandler(t, "ticket.json", "conversations.json"))
	c := newClient(t, map[string]string{testDomain: addrOf(ts.Server)}, testDomain)

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := ts.count("/api/v2/agents/me"); got != 1 {
		t.Errorf("hits on agents/me = %d, want 1", got)
	}
}

func TestPing_Unauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)
	c := newClient(t, map[string]string{testDomain: addrOf(ts)}, testDomain)

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping: want error")
	}
	if code := sourceCode(t, err); code != source.Auth {
		t.Errorf("code = %q, want %q", code, source.Auth)
	}
}

// --- error mapping ---

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantCode source.Code
	}{
		{"unauthorized", http.StatusUnauthorized, source.Auth},
		{"forbidden", http.StatusForbidden, source.Auth},
		{"not_found", http.StatusNotFound, source.NotFound},
		{"rate_limited_no_retry_after", http.StatusTooManyRequests, source.RateLimited},
		{"server_error", http.StatusInternalServerError, source.Internal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("boom, key=" + testAPIKey))
			}))
			t.Cleanup(ts.Close)
			c := newClient(t, map[string]string{testDomain: addrOf(ts)}, testDomain)

			_, err := c.Get(context.Background(), "123")
			if err == nil {
				t.Fatal("Get: want error")
			}
			if code := sourceCode(t, err); code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if tc.wantCode == source.Auth && strings.Contains(err.Error(), testAPIKey) {
				t.Errorf("auth error leaked the api key: %v", err)
			}
		})
	}
}

func TestRetryAfterHonoured(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		hits++
		if hits == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeFixture(t, w, "ticket.json")
	}))
	t.Cleanup(ts.Close)
	c := newClient(t, map[string]string{testDomain: addrOf(ts)}, testDomain)

	if _, err := c.Get(context.Background(), "123"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if hits != 2 {
		t.Errorf("hits = %d, want 2 (one 429, one retry)", hits)
	}
}

func TestRetryAfterTooLongNotHonoured(t *testing.T) {
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(ts.Close)
	c := newClient(t, map[string]string{testDomain: addrOf(ts)}, testDomain)

	_, err := c.Get(context.Background(), "123")
	if err == nil {
		t.Fatal("Get: want error")
	}
	if code := sourceCode(t, err); code != source.RateLimited {
		t.Errorf("code = %q, want %q", code, source.RateLimited)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1 (a Retry-After over the cap is not honoured)", hits)
	}
}

// --- Threads ---

func TestThreads_DescriptionFirstThenConversationsInOrder(t *testing.T) {
	ts := newCountingServer(t, mainHandler(t, "ticket.json", "conversations.json"))
	c := newClient(t, map[string]string{testDomain: addrOf(ts.Server)}, testDomain)

	thread, err := c.Threads(context.Background(), "123")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(thread) != 4 {
		t.Fatalf("len(thread) = %d, want 4", len(thread))
	}

	desc := thread[0]
	if got, want := string(desc.Role), "customer"; got != want {
		t.Errorf("description role = %q, want %q", got, want)
	}
	if desc.Author != "Priya Shah" {
		t.Errorf("description author = %q", desc.Author)
	}
	if desc.Text != "It just spins forever. See for a screenshot." {
		t.Errorf("description text = %q", desc.Text)
	}
	if len(desc.AttachmentIDs) != 2 || desc.AttachmentIDs[0] != "900001" || desc.AttachmentIDs[1] != "inline-1" {
		t.Errorf("description attachment ids = %v, want [900001 inline-1]", desc.AttachmentIDs)
	}

	customerReply := thread[1]
	if string(customerReply.Role) != "customer" {
		t.Errorf("customer reply role = %q, want customer", customerReply.Role)
	}
	if customerReply.Author != "priya@example.com" {
		t.Errorf("customer reply author = %q", customerReply.Author)
	}
	if customerReply.Text != "Still broken, tried on Chrome and Safari." {
		t.Errorf("customer reply text = %q", customerReply.Text)
	}

	privateNote := thread[2]
	if string(privateNote.Role) != "agent" {
		t.Errorf("private note role = %q, want agent", privateNote.Role)
	}
	if privateNote.Author != "Sam Agent (internal)" {
		t.Errorf("private note author = %q, want %q", privateNote.Author, "Sam Agent (internal)")
	}
	if privateNote.Text != "Checking the payment provider logs now." {
		t.Errorf("private note text = %q", privateNote.Text)
	}

	publicReply := thread[3]
	if string(publicReply.Role) != "agent" {
		t.Errorf("public reply role = %q, want agent", publicReply.Role)
	}
	if publicReply.Author != "Sam Agent" {
		t.Errorf("public reply author = %q, want %q (no internal suffix)", publicReply.Author, "Sam Agent")
	}
	if !strings.Contains(publicReply.Text, "Found it") || !strings.Contains(publicReply.Text, "a stale API key on our side") {
		t.Errorf("public reply text = %q, want it to contain the message body", publicReply.Text)
	}
	if len(publicReply.AttachmentIDs) != 2 || publicReply.AttachmentIDs[0] != "900002" || publicReply.AttachmentIDs[1] != "inline-2" {
		t.Errorf("public reply attachment ids = %v, want [900002 inline-2]", publicReply.AttachmentIDs)
	}

	// The agent (id 501) authored two entries but should only have been
	// looked up once: lookupAgent caches.
	if got := ts.count("/api/v2/agents/501"); got != 1 {
		t.Errorf("agent lookups = %d, want 1 (cached)", got)
	}
}

func TestThreads_Pagination(t *testing.T) {
	orig := conversationsPerPage
	conversationsPerPage = 2
	t.Cleanup(func() { conversationsPerPage = orig })

	ts := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		switch {
		case r.URL.Path == "/api/v2/tickets/123":
			writeFixture(t, w, "ticket.json")
		case r.URL.Path == "/api/v2/tickets/123/conversations" && r.URL.Query().Get("page") == "1":
			writeFixture(t, w, "conversations_page1.json")
		case r.URL.Path == "/api/v2/tickets/123/conversations" && r.URL.Query().Get("page") == "2":
			writeFixture(t, w, "conversations_page2.json")
		case r.URL.Path == "/api/v2/agents/501":
			writeFixture(t, w, "agent.json")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c := newClient(t, map[string]string{testDomain: addrOf(ts.Server)}, testDomain)

	thread, err := c.Threads(context.Background(), "123")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	// description + 2 (page 1) + 1 (page 2) = 4.
	if len(thread) != 4 {
		t.Fatalf("len(thread) = %d, want 4", len(thread))
	}
	if got := ts.count("/api/v2/tickets/123/conversations"); got != 2 {
		t.Errorf("conversations requests = %d, want 2 (page 1 full, page 2 short)", got)
	}
}

// --- Attachments ---

func TestAttachments_DownloadSanitizeAndHostTrust(t *testing.T) {
	primary := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		switch r.URL.Path {
		case "/api/v2/tickets/123":
			writeFixture(t, w, "ticket.json")
		case "/api/v2/tickets/123/conversations":
			writeFixture(t, w, "conversations.json")
		case "/api/v2/agents/501":
			writeFixture(t, w, "agent.json")
		case "/api/v2/attachments/900001/order-1234.pdf":
			_, _ = w.Write([]byte("%PDF-fake-order-bytes"))
		case "/api/v2/attachments/900002/changelog.txt":
			_, _ = w.Write([]byte("v2: fixed the stale api key"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	var cdnAuthSeen bool
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			cdnAuthSeen = true
		}
		switch r.URL.Path {
		case "/data/helpdesk/attachments/production/700001/original/spinner.png":
			_, _ = w.Write([]byte("spinner-png-bytes"))
		case "/data/helpdesk/attachments/production/700002/original/fix.png":
			_, _ = w.Write([]byte("fix-png-bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cdn.Close)

	c := newClient(t, map[string]string{
		testDomain: addrOf(primary.Server),
		testCDN:    addrOf(cdn),
	}, testDomain)

	dir := filepath.Join(t.TempDir(), "123")
	atts, err := c.Attachments(context.Background(), "123", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 4 {
		t.Fatalf("len(atts) = %d, want 4: %+v", len(atts), atts)
	}

	want := []struct {
		id, name, path string
	}{
		{"900001", "order-1234.pdf", "123/1-order-1234.pdf"},
		{"inline-1", "spinner.png", "123/2-spinner.png"},
		{"900002", "changelog.txt", "123/3-changelog.txt"},
		{"inline-2", "fix.png", "123/4-fix.png"},
	}
	for i, w := range want {
		got := atts[i]
		if got.ID != w.id || got.Name != w.name || got.Path != w.path {
			t.Errorf("atts[%d] = %+v, want id=%s name=%s path=%s", i, got, w.id, w.name, w.path)
		}
	}

	if cdnAuthSeen {
		t.Error("the CDN (pre-signed, not the configured domain) received an Authorization header")
	}
	if warnings := c.WarningsFor("123"); len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}

	// Spot-check file contents landed under the right names.
	b, err := os.ReadFile(filepath.Join(dir, "1-order-1234.pdf"))
	if err != nil || string(b) != "%PDF-fake-order-bytes" {
		t.Errorf("attachment 1 contents = %q, %v", b, err)
	}
}

func TestAttachments_UntrustedHostSkippedWithWarning(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		switch r.URL.Path {
		case "/api/v2/tickets/123":
			writeFixture(t, w, "ticket_untrusted.json")
		case "/api/v2/tickets/123/conversations":
			writeFixture(t, w, "conversations_empty.json")
		case "/api/v2/attachments/900001/order-1234.pdf":
			_, _ = w.Write([]byte("%PDF-fake-order-bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(primary.Close)
	c := newClient(t, map[string]string{testDomain: addrOf(primary)}, testDomain)

	dir := filepath.Join(t.TempDir(), "123")
	atts, err := c.Attachments(context.Background(), "123", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 || atts[0].ID != "900001" {
		t.Fatalf("atts = %+v, want the one trusted attachment", atts)
	}

	warnings := c.WarningsFor("123")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1", warnings)
	}
	if !strings.Contains(warnings[0], "freshdesk: attachment host not trusted: evil.example.com") {
		t.Errorf("warning = %q, want it to name the untrusted host", warnings[0])
	}
}

func TestAttachments_AllFail(t *testing.T) {
	ticket := `{"id":123,"subject":"x","status":2,"priority":1,"source":1,
		"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z",
		"attachments":[{"id":1,"name":"a.txt","content_type":"text/plain","attachment_url":"https://evil.example.com/a.txt"}]}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/tickets/123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(ticket))
		case "/api/v2/tickets/123/conversations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	c := newClient(t, map[string]string{testDomain: addrOf(ts)}, testDomain)

	dir := filepath.Join(t.TempDir(), "123")
	atts, err := c.Attachments(context.Background(), "123", dir)
	if err == nil {
		t.Fatal("Attachments: want an error when every attachment fails")
	}
	if len(atts) != 0 {
		t.Errorf("atts = %+v, want none", atts)
	}
	if !strings.Contains(err.Error(), "host not trusted") {
		t.Errorf("error = %v, want it to mention the untrusted host", err)
	}
	// The failure surfaced through the returned error, so it must not also
	// sit in WarningsFor waiting to be reported a second time.
	if warnings := c.WarningsFor("123"); len(warnings) != 0 {
		t.Errorf("warnings = %v, want none (already reported via the error)", warnings)
	}
}

func TestAttachments_NoAttachments(t *testing.T) {
	ticket := `{"id":123,"subject":"x","status":2,"priority":1,"source":1,
		"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/tickets/123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(ticket))
		case "/api/v2/tickets/123/conversations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	c := newClient(t, map[string]string{testDomain: addrOf(ts)}, testDomain)

	atts, err := c.Attachments(context.Background(), "123", filepath.Join(t.TempDir(), "123"))
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if atts != nil {
		t.Errorf("atts = %+v, want nil", atts)
	}
}

// TestAttachments_PlainHTTPAndUserinfoRefused: the host is only half the
// question. An attachment_url arrives inside an API response body, so a
// hostile instance can put "http://" in front of a real Freshworks host, or
// hide the destination behind userinfo. Neither URL is fetched at all — the
// listener behind both records zero hits.
func TestAttachments_PlainHTTPAndUserinfoRefused(t *testing.T) {
	cdn := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("cdn-bytes"))
	})

	ticket := `{"id":123,"subject":"x","status":2,"priority":1,"source":1,
		"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z",
		"attachments":[
			{"id":1,"name":"plain.png","content_type":"image/png",
			 "attachment_url":"http://cdn.freshdesk.com/plain.png"},
			{"id":2,"name":"decoy.png","content_type":"image/png",
			 "attachment_url":"https://acme.freshdesk.com@cdn.freshdesk.com/decoy.png"}
		]}`
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/tickets/123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(ticket))
		case "/api/v2/tickets/123/conversations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(primary.Close)

	c := newClient(t, map[string]string{
		testDomain: addrOf(primary),
		testCDN:    addrOf(cdn.Server),
	}, testDomain)

	atts, err := c.Attachments(context.Background(), "123", filepath.Join(t.TempDir(), "123"))
	if err == nil {
		t.Fatal("Attachments: want an error, neither URL is trusted")
	}
	if len(atts) != 0 {
		t.Errorf("atts = %+v, want none", atts)
	}
	if got := cdn.count("/plain.png") + cdn.count("/decoy.png"); got != 0 {
		t.Errorf("attachment host hits = %d, want 0", got)
	}
	if !strings.Contains(err.Error(), "host not trusted") {
		t.Errorf("error = %v, want it to say the host was not trusted", err)
	}
}

// TestAttachments_OversizeFileIsRefusedAndNotLeftOnDisk covers the download
// limit now that attachments stream to disk rather than through a buffer: a
// file past the ceiling fails, and its partial output is removed instead of
// being left there looking complete.
func TestAttachments_OversizeFileIsRefusedAndNotLeftOnDisk(t *testing.T) {
	orig := maxAttachmentBytes
	maxAttachmentBytes = 16
	t.Cleanup(func() { maxAttachmentBytes = orig })

	ticket := `{"id":123,"subject":"x","status":2,"priority":1,"source":1,
		"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z",
		"attachments":[
			{"id":1,"name":"big.bin","content_type":"application/octet-stream",
			 "attachment_url":"https://acme.freshdesk.com/api/v2/attachments/1/big.bin"},
			{"id":2,"name":"small.txt","content_type":"text/plain",
			 "attachment_url":"https://acme.freshdesk.com/api/v2/attachments/2/small.txt"}
		]}`
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/tickets/123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(ticket))
		case "/api/v2/tickets/123/conversations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		case "/api/v2/attachments/1/big.bin":
			_, _ = w.Write([]byte(strings.Repeat("A", 1024)))
		case "/api/v2/attachments/2/small.txt":
			_, _ = w.Write([]byte("small"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(primary.Close)
	c := newClient(t, map[string]string{testDomain: addrOf(primary)}, testDomain)

	dir := filepath.Join(t.TempDir(), "123")
	atts, err := c.Attachments(context.Background(), "123", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 || atts[0].ID != "2" {
		t.Fatalf("atts = %+v, want only the small attachment", atts)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "1-big.bin")); !os.IsNotExist(statErr) {
		t.Errorf("the oversize attachment was left on disk (stat err = %v)", statErr)
	}
	warnings := c.WarningsFor("123")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "exceeds") {
		t.Errorf("warnings = %v, want one saying the attachment exceeded the limit", warnings)
	}
}

// --- pure-function coverage ---

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"order-1234.pdf", "order-1234.pdf"},
		{"../../evil.sh", "evil.sh"},
		{"", "attachment"},
		{".", "attachment"},
		{"..", "attachment"},
		{"a/b/c.png", "c.png"},
		{"bad\x00name.txt", "badname.txt"},
	}
	for _, tc := range cases {
		if got := sanitizeName(tc.in); got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAttachmentTrust(t *testing.T) {
	c, err := New(Config{Domain: testDomain, APIKey: "k"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		host     string
		trusted  bool
		sendAuth bool
	}{
		{"acme.freshdesk.com", true, true},
		{"cdn.freshdesk.com", true, false},
		{"foo.freshcloud.io", true, false},
		{"foo.freshworksapi.com", true, false},
		{"freshdesk.com", true, false},
		{"evilfreshdesk.com", false, false},
		{"evil.example.com", false, false},
	}
	for _, tc := range cases {
		trusted, sendAuth := c.attachmentTrust(tc.host)
		if trusted != tc.trusted || sendAuth != tc.sendAuth {
			t.Errorf("attachmentTrust(%q) = (%v, %v), want (%v, %v)", tc.host, trusted, sendAuth, tc.trusted, tc.sendAuth)
		}
	}
}

// --- redirects and pagination trust ---

// TestCheckRedirect_RefusesOffDomainRedirect covers an API call (the ticket
// GET, which every one of Get/Threads/Attachments starts with) answered
// with a redirect to a host outside the configured domain and the trusted
// Freshdesk suffixes. The credential must never reach that host, and — the
// stronger property CheckRedirect exists for — the hop must never even be
// taken: a foreign listener wired into the same hostRouter records zero
// hits.
func TestCheckRedirect_RefusesOffDomainRedirect(t *testing.T) {
	foreign := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("stolen"))
	})

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/tickets/123" {
			http.Redirect(w, r, "https://evil.example.com/steal", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(primary.Close)

	c := newClient(t, map[string]string{
		testDomain:         addrOf(primary),
		"evil.example.com": addrOf(foreign.Server),
	}, testDomain)

	if _, err := c.Get(context.Background(), "123"); err == nil {
		t.Fatal("Get: want an error, the redirect target is off the trusted hosts")
	}
	if got := foreign.count("/steal"); got != 0 {
		t.Errorf("foreign host hits = %d, want 0: an off-domain redirect must never be followed", got)
	}
}

// TestAttachments_RedirectToForeignHostRefused covers the download path
// specifically: an attachment URL on the configured domain that itself
// redirects off it. The pre-signed-URL trust check only looks at the URL's
// own host, so without CheckRedirect this would still leak the credential
// (Go strips Authorization on a cross-host hop by default, but would still
// follow the redirect and write whatever the foreign host served to disk
// under the attachment's name) and reach a host never checked for trust.
func TestAttachments_RedirectToForeignHostRefused(t *testing.T) {
	foreign := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("stolen-bytes"))
	})

	ticketJSON := `{"id":123,"subject":"x","status":2,"priority":1,"source":1,
		"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z",
		"attachments":[{"id":1,"name":"a.txt","content_type":"text/plain",
		"attachment_url":"https://acme.freshdesk.com/api/v2/attachments/1/a.txt"}]}`
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/tickets/123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(ticketJSON))
		case "/api/v2/tickets/123/conversations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		case "/api/v2/attachments/1/a.txt":
			http.Redirect(w, r, "https://evil.example.com/steal", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(primary.Close)

	c := newClient(t, map[string]string{
		testDomain:         addrOf(primary),
		"evil.example.com": addrOf(foreign.Server),
	}, testDomain)

	dir := filepath.Join(t.TempDir(), "123")
	atts, err := c.Attachments(context.Background(), "123", dir)
	if err == nil {
		t.Fatal("Attachments: want an error, the only attachment redirects off-domain")
	}
	if len(atts) != 0 {
		t.Errorf("atts = %+v, want none", atts)
	}
	if got := foreign.count("/steal"); got != 0 {
		t.Errorf("foreign host hits = %d, want 0: an off-domain redirect must never be followed", got)
	}
}

// TestAttachments_TrustedHostRedirectSkippedOthersStillDownload covers the
// case where a *trusted* attachment host (the CDN suffix, not the
// configured domain itself) answers with a redirect off the trusted set —
// an expired pre-signed link falling back to a generic host, say. That one
// attachment must be skipped with a warning, not treated as fatal: a
// ticket's other, unrelated attachments still come back.
func TestAttachments_TrustedHostRedirectSkippedOthersStillDownload(t *testing.T) {
	foreign := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("stolen-bytes"))
	})
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example.com/steal", http.StatusFound)
	}))
	t.Cleanup(cdn.Close)

	ticketJSON := `{"id":123,"subject":"x","status":2,"priority":1,"source":1,
		"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z",
		"attachments":[
			{"id":1,"name":"redirected.png","content_type":"image/png",
			 "attachment_url":"https://cdn.freshdesk.com/redirected.png"},
			{"id":2,"name":"fine.txt","content_type":"text/plain",
			 "attachment_url":"https://acme.freshdesk.com/api/v2/attachments/2/fine.txt"}
		]}`
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/tickets/123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(ticketJSON))
		case "/api/v2/tickets/123/conversations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
		case "/api/v2/attachments/2/fine.txt":
			_, _ = w.Write([]byte("still here"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(primary.Close)

	c := newClient(t, map[string]string{
		testDomain:         addrOf(primary),
		testCDN:            addrOf(cdn),
		"evil.example.com": addrOf(foreign.Server),
	}, testDomain)

	dir := filepath.Join(t.TempDir(), "123")
	atts, err := c.Attachments(context.Background(), "123", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 || atts[0].ID != "2" || atts[0].Name != "fine.txt" {
		t.Fatalf("atts = %+v, want only the non-redirecting attachment", atts)
	}
	if got := foreign.count("/steal"); got != 0 {
		t.Errorf("foreign host hits = %d, want 0: the redirect must never be followed", got)
	}

	warnings := c.WarningsFor("123")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "freshdesk: redirect to untrusted host evil.example.com") {
		t.Errorf("warnings = %v, want one naming the untrusted redirect target", warnings)
	}
}

// --- pagination cap ---

func TestThreads_ConversationPaginationCapped(t *testing.T) {
	origPer, origMax := conversationsPerPage, maxConversationPages
	conversationsPerPage = 1
	maxConversationPages = 3
	t.Cleanup(func() { conversationsPerPage, maxConversationPages = origPer, origMax })

	ts := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		switch {
		case r.URL.Path == "/api/v2/tickets/123":
			writeFixture(t, w, "ticket.json")
		case r.URL.Path == "/api/v2/tickets/123/conversations":
			// Every page is full (1 of 1), so pagination would run
			// forever without the cap.
			body := fmt.Sprintf(`[{"id":%s,"body":"<p>x</p>","body_text":"x","incoming":true,"private":false,"user_id":9001,"from_email":"priya@example.com","created_at":"2026-09-01T09:00:00Z","attachments":[]}]`,
				r.URL.Query().Get("page"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c := newClient(t, map[string]string{testDomain: addrOf(ts.Server)}, testDomain)

	thread, err := c.Threads(context.Background(), "123")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	// description + 3 capped pages of 1 entry each.
	if len(thread) != 4 {
		t.Fatalf("len(thread) = %d, want 4 (stopped at the page cap)", len(thread))
	}
	if got := ts.count("/api/v2/tickets/123/conversations"); got != maxConversationPages {
		t.Errorf("conversations requests = %d, want %d (the cap, not an unbounded sweep)", got, maxConversationPages)
	}

	warnings := c.WarningsFor("123")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "freshdesk: conversation pages capped at 3") {
		t.Errorf("warnings = %v, want one naming the page cap", warnings)
	}
}
