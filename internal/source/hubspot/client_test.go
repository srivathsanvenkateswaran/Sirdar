package hubspot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

const (
	testToken      = "pat-na1-test-token-value"
	cdnHost        = "cdn2.hubspotusercontent-na1.net"
	attachmentBody = "hello world"
)

// --- harness ---

// hostRouter dials the real address of a virtual HubSpot hostname without
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

func addrOf(ts *httptest.Server) string { return strings.TrimPrefix(ts.URL, "http://") }

func newClient(t *testing.T, routes map[string]string) *Client {
	t.Helper()
	hc := &http.Client{Transport: hostRouter{routes: routes}}
	c, err := New(Config{AccessToken: testToken}, hc)
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

func wantAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("%s: Authorization = %q, want the bearer private-app token", r.URL.Path, got)
	}
}

// apiHandler serves the ticket, its conversation thread's two pages, the
// associated contact and company, the account details and the signed URLs
// the attachments are fetched through.
func apiHandler(t *testing.T, ticketFixture string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wantAuth(t, r)
		switch r.URL.Path {
		case "/account-info/v3/details":
			writeFixture(t, w, "account_info.json")
		case "/crm/v3/objects/tickets/7001", "/crm/v3/objects/tickets/7002":
			if got := r.URL.Query().Get("properties"); got != ticketProperties {
				t.Errorf("properties = %q, want %q", got, ticketProperties)
			}
			if got := r.URL.Query().Get("associations"); got != ticketAssociations {
				t.Errorf("associations = %q, want %q", got, ticketAssociations)
			}
			writeFixture(t, w, ticketFixture)
		case "/crm/v3/objects/contacts/501":
			writeFixture(t, w, "contact.json")
		case "/crm/v3/objects/companies/601":
			writeFixture(t, w, "company.json")
		case "/conversations/v3/conversations/threads/8001/messages":
			if r.URL.Query().Get("after") == "" {
				writeFixture(t, w, "messages_page1.json")
			} else {
				writeFixture(t, w, "messages_page2.json")
			}
		case "/files/v3/files/9001/signed-url":
			writeFixture(t, w, "signed_url.json")
		case "/files/v3/files/9002/signed-url":
			writeFixture(t, w, "signed_url_untrusted.json")
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"error","message":"not found"}`))
		}
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

// --- Config / New ---

func TestNew_ValidatesConfig(t *testing.T) {
	if _, err := New(Config{}, nil); err == nil {
		t.Error("New without accessToken: want error")
	}
	c, err := New(Config{AccessToken: "  t  "}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.AccessToken != "t" {
		t.Errorf("AccessToken = %q, want it trimmed", c.cfg.AccessToken)
	}
	if c.baseURL != "https://api.hubapi.com" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
}

// --- Ping ---

func TestPing_SendsBearer(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "ticket.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := cs.count("/account-info/v3/details"); got != 1 {
		t.Errorf("account-info hits = %d, want 1", got)
	}
}

func TestPing_Unauthorized(t *testing.T) {
	cs := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":"error"}`))
	})
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping: want error")
	}
	if code := sourceCode(t, err); code != source.Auth {
		t.Errorf("code = %q, want %q", code, source.Auth)
	}
}

// --- Get ---

func TestGet_MapsFields(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "ticket.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	got, err := c.Get(context.Background(), "7001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "7001" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Subject != "Checkout returns 500" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.Status != "2" {
		t.Errorf("Status = %q, want the pipeline stage", got.Status)
	}
	if got.Priority != "HIGH" {
		t.Errorf("Priority = %q", got.Priority)
	}
	if got.Contact != "Ada Lovelace" {
		t.Errorf("Contact = %q", got.Contact)
	}
	if got.Customer != "Acme Inc" || got.CustomerID != "601" {
		t.Errorf("Customer = %q / %q", got.Customer, got.CustomerID)
	}
	if got.Fields["ownerId"] != "77" {
		t.Errorf("ownerId = %q", got.Fields["ownerId"])
	}
	if got.Fields["contactEmail"] != "ada@example.com" {
		t.Errorf("contactEmail = %q", got.Fields["contactEmail"])
	}
	if got.URL != "https://app.hubspot.com/contacts/1234567/ticket/7001" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() || got.CreatedAt.After(got.UpdatedAt) {
		t.Errorf("timestamps = %v / %v", got.CreatedAt, got.UpdatedAt)
	}
	if w := c.WarningsFor("7001"); w != nil {
		t.Errorf("warnings = %v, want none", w)
	}

	// The portal id, the contact and the company are each read once.
	if _, err := c.Get(context.Background(), "7001"); err != nil {
		t.Fatalf("Get again: %v", err)
	}
	for path, want := range map[string]int{
		"/account-info/v3/details":      1,
		"/crm/v3/objects/contacts/501":  1,
		"/crm/v3/objects/companies/601": 1,
	} {
		if got := cs.count(path); got != want {
			t.Errorf("%s hits = %d, want %d (the lookup is cached)", path, got, want)
		}
	}
}

// --- Threads ---

func TestThreads_ContentFirstThenMessagesAcrossPages(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "ticket.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	th, err := c.Threads(context.Background(), "7001")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 5 {
		t.Fatalf("len(thread) = %d, want 5: %+v", len(th), th)
	}
	want := []struct {
		author string
		role   ticket.Role
		text   string
	}{
		{"Ada Lovelace", ticket.RoleCustomer, "Every checkout returns a **500**."},
		{"ada@example.com", ticket.RoleCustomer, "Every checkout returns a 500."},
		{"Grace Hopper", ticket.RoleAgent, "Looking into it **now**."},
		{"Grace Hopper (internal)", ticket.RoleAgent, "Processor is rate limiting us."},
		{"S-1", ticket.RoleSystem, "Thanks for contacting Acme support."},
	}
	for i, w := range want {
		if th[i].Author != w.author {
			t.Errorf("message %d author = %q, want %q", i, th[i].Author, w.author)
		}
		if th[i].Role != w.role {
			t.Errorf("message %d role = %q, want %q", i, th[i].Role, w.role)
		}
		if strings.TrimSpace(th[i].Text) != w.text {
			t.Errorf("message %d text = %q, want %q", i, th[i].Text, w.text)
		}
	}
	if got := cs.count("/conversations/v3/conversations/threads/8001/messages"); got != 2 {
		t.Errorf("message page hits = %d, want 2", got)
	}
	if w := c.WarningsFor("7001"); w != nil {
		t.Errorf("warnings = %v, want none", w)
	}
}

func TestThreads_MessagePagesCapped(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "ticket.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	old := maxMessagePages
	maxMessagePages = 1
	defer func() { maxMessagePages = old }()

	th, err := c.Threads(context.Background(), "7001")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 3 {
		t.Errorf("len(thread) = %d, want 3 (the ticket content plus page one)", len(th))
	}
	warnings := c.WarningsFor("7001")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "capped at 1") {
		t.Fatalf("warnings = %v, want one page-cap warning", warnings)
	}
	if got := c.WarningsFor("7001"); got != nil {
		t.Errorf("warnings after read = %v, want nil", got)
	}
}

func TestThreads_NoConversationAssociationWarnsAndKeepsContent(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "ticket_no_conversation.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	th, err := c.Threads(context.Background(), "7002")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 1 {
		t.Fatalf("len(thread) = %d, want 1 (the ticket's own content)", len(th))
	}
	if th[0].Role != ticket.RoleCustomer || !strings.Contains(th[0].Text, "Which card was charged?") {
		t.Errorf("message = %+v", th[0])
	}
	warnings := c.WarningsFor("7002")
	if len(warnings) != 1 || warnings[0] != "hubspot: ticket has no associated conversation" {
		t.Errorf("warnings = %v, want the no-conversation warning", warnings)
	}
}

// --- Attachments ---

// fileServer serves attachment bytes and records whether any request
// arrived carrying an Authorization header.
type fileServer struct {
	*httptest.Server
	mu       sync.Mutex
	hits     int
	sawAuth  bool
	redirect string
}

func newFileServer(t *testing.T) *fileServer {
	t.Helper()
	fs := &fileServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		fs.hits++
		if r.Header.Get("Authorization") != "" {
			fs.sawAuth = true
		}
		redirect := fs.redirect
		fs.mu.Unlock()
		if redirect != "" {
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(attachmentBody))
	}))
	t.Cleanup(fs.Close)
	return fs
}

func (fs *fileServer) setRedirect(raw string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.redirect = raw
}

func (fs *fileServer) state() (hits int, sawAuth bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.hits, fs.sawAuth
}

func TestAttachments_SignedURLDownloadAndTrust(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "ticket.json"))
	cdn := newFileServer(t)
	evil := newFileServer(t)
	c := newClient(t, map[string]string{
		apiHost:              addrOf(cs.Server),
		cdnHost:              addrOf(cdn.Server),
		"files.evil.example": addrOf(evil.Server),
	})

	dir := filepath.Join(t.TempDir(), "TCK-1")
	atts, err := c.Attachments(context.Background(), "7001", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("len(attachments) = %d, want 1: %+v", len(atts), atts)
	}
	if atts[0].ID != "9001" {
		t.Errorf("ID = %q, want the file id", atts[0].ID)
	}
	// The signed-url response carries the name and the extension
	// separately; a file written without its extension is one an operator
	// has to guess at.
	if atts[0].Name != "screenshot.png" || atts[0].Path != "TCK-1/1-screenshot.png" {
		t.Errorf("attachment = %+v", atts[0])
	}
	body, err := os.ReadFile(filepath.Join(dir, "1-screenshot.png"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(body) != attachmentBody {
		t.Errorf("downloaded body = %q", body)
	}

	hits, sawAuth := cdn.state()
	if hits != 1 {
		t.Errorf("cdn hits = %d, want 1", hits)
	}
	if sawAuth {
		t.Error("the private-app token was sent to the file CDN, which needs none")
	}
	if hits, _ := evil.state(); hits != 0 {
		t.Errorf("untrusted host was called %d times, want 0", hits)
	}

	warnings := c.WarningsFor("7001")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "files.evil.example") {
		t.Errorf("warnings = %v, want one naming the untrusted attachment host", warnings)
	}
}

func TestAttachments_RedirectToForeignHostRefused(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "ticket.json"))
	cdn := newFileServer(t)
	elsewhere := newFileServer(t)
	cdn.setRedirect("https://elsewhere.example/file")
	c := newClient(t, map[string]string{
		apiHost:              addrOf(cs.Server),
		cdnHost:              addrOf(cdn.Server),
		"elsewhere.example":  addrOf(elsewhere.Server),
		"files.evil.example": addrOf(elsewhere.Server),
	})

	dir := filepath.Join(t.TempDir(), "TCK-1")
	_, err := c.Attachments(context.Background(), "7001", dir)
	if err == nil {
		t.Fatal("Attachments: want an error when every attachment fails")
	}
	if !strings.Contains(err.Error(), "elsewhere.example") {
		t.Errorf("error = %v, want it to name the refused redirect host", err)
	}
	if hits, _ := elsewhere.state(); hits != 0 {
		t.Errorf("redirect target was called %d times, want 0", hits)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files left on disk: %v", entries)
	}
}

func TestAttachments_OversizeIsRefusedAndNotLeftOnDisk(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "ticket.json"))
	cdn := newFileServer(t)
	evil := newFileServer(t)
	c := newClient(t, map[string]string{
		apiHost:              addrOf(cs.Server),
		cdnHost:              addrOf(cdn.Server),
		"files.evil.example": addrOf(evil.Server),
	})

	old := maxAttachmentBytes
	maxAttachmentBytes = 4
	defer func() { maxAttachmentBytes = old }()

	dir := filepath.Join(t.TempDir(), "TCK-1")
	atts, err := c.Attachments(context.Background(), "7001", dir)
	if err == nil {
		t.Fatal("Attachments: want an error when every attachment fails")
	}
	if len(atts) != 0 {
		t.Errorf("attachments = %+v, want none", atts)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("oversize attachment left on disk: %v", entries)
	}
}

func TestSanitizeName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"report.png", "report.png"},
		{"../../etc/passwd", "passwd"},
		{"", "attachment"},
		{"..", "attachment"},
		{"a\x00b.txt", "ab.txt"},
		{strings.Repeat("x", 300) + ".png", strings.Repeat("x", 116) + ".png"},
	} {
		if got := sanitizeName(tc.in); got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- trust ---

func TestURLTrust(t *testing.T) {
	c, err := New(Config{AccessToken: "t"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range []struct {
		raw               string
		trusted, sendAuth bool
	}{
		{"https://api.hubapi.com/crm/v3/objects/tickets/1", true, true},
		{"https://API.HubAPI.com.:443/x", true, true},
		{"https://cdn2.hubspotusercontent-na1.net/hubfs/1/x.png", true, false},
		{"https://hubspotusercontent20.net/x", true, false},
		{"https://app.hubspot.com/x", true, false},
		{"https://hubspot.com/x", true, false},
		{"http://api.hubapi.com/x", false, false},
		{"https://api.hubapi.com@attacker.example/x", false, false},
		{"https://hubspotusercontent-na1.net.evil.example/x", false, false},
		{"https://hubspot.com.evil.example/x", false, false},
		{"https://files.evil.example/x", false, false},
	} {
		u, perr := url.Parse(tc.raw)
		if perr != nil {
			t.Fatalf("parse %q: %v", tc.raw, perr)
		}
		_, trusted, sendAuth := c.urlTrust(u)
		if trusted != tc.trusted || sendAuth != tc.sendAuth {
			t.Errorf("urlTrust(%q) = (%v, %v), want (%v, %v)", tc.raw, trusted, sendAuth, tc.trusted, tc.sendAuth)
		}
	}
}

// --- error mapping ---

func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		header map[string]string
		want   source.Code
	}{
		{http.StatusNotFound, nil, source.NotFound},
		{http.StatusUnauthorized, nil, source.Auth},
		{http.StatusForbidden, nil, source.Auth},
		{http.StatusTooManyRequests, map[string]string{"Retry-After": "3600"}, source.RateLimited},
		{http.StatusTooManyRequests, nil, source.RateLimited},
		{http.StatusInternalServerError, nil, source.Internal},
	} {
		cs := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
			for k, v := range tc.header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"status":"error","message":"nope"}`))
		})
		c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

		_, err := c.Get(context.Background(), "7001")
		if err == nil {
			t.Fatalf("status %d: want error", tc.status)
		}
		if code := sourceCode(t, err); code != tc.want {
			t.Errorf("status %d: code = %q, want %q", tc.status, code, tc.want)
		}
		cs.Close()
	}
}

func TestRetryAfter_HonouredOnce(t *testing.T) {
	var calls int
	var mu sync.Mutex
	cs := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		apiHandler(t, "ticket.json")(w, r)
	})
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	if _, err := c.Get(context.Background(), "7001"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Errorf("calls = %d, want at least 2 (one 429 honoured, then the real answer)", calls)
	}
}

func TestErrorMessages_NeverContainTheAccessToken(t *testing.T) {
	cs := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","message":"boom"}`))
	})
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	_, err := c.Get(context.Background(), "7001")
	if err == nil {
		t.Fatal("Get: want error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error repeats the access token: %s", err)
	}
}
