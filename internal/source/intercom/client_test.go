package intercom

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
	testToken      = "ic-access-token-value"
	cdnHost        = "acme.intercomcdn.com"
	uploadHost     = "acme.intercom-attachments-7.com"
	attachmentBody = "hello world"
)

// --- harness ---

// hostRouter dials the real address of a virtual Intercom hostname without
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

// countingServer records how many times each path was hit, for asserting
// caching (the contact lookup, the app id) and pagination behaviour.
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
		t.Errorf("%s: Authorization = %q, want the bearer access token", r.URL.Path, got)
	}
	if got := r.Header.Get("Intercom-Version"); got != apiVersion {
		t.Errorf("%s: Intercom-Version = %q, want %q", r.URL.Path, got, apiVersion)
	}
}

// apiHandler serves the conversation, the contact behind it and /me.
func apiHandler(t *testing.T, convFixture string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wantAuth(t, r)
		switch r.URL.Path {
		case "/me":
			writeFixture(t, w, "me.json")
		case "/conversations/1001", "/conversations/1002":
			if got := r.URL.Query().Get("display_as"); got != "plaintext" {
				t.Errorf("display_as = %q, want plaintext", got)
			}
			writeFixture(t, w, convFixture)
		case "/contacts/c-1":
			writeFixture(t, w, "contact.json")
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"error.list","errors":[{"code":"not_found"}]}`))
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
	if c.baseURL != "https://api.intercom.io" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
}

// --- Ping ---

func TestPing_SendsBearerAndVersion(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "conversation.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := cs.count("/me"); got != 1 {
		t.Errorf("/me hits = %d, want 1", got)
	}
}

func TestPing_Unauthorized(t *testing.T) {
	cs := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error.list"}`))
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
	cs := newCountingServer(t, apiHandler(t, "conversation.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	got, err := c.Get(context.Background(), "1001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "1001" {
		t.Errorf("ID = %q", got.ID)
	}
	// The source message has no subject, so the first line of the opening
	// message stands in for one, the way the Intercom inbox itself shows a
	// chat.
	if got.Subject != "Payments are failing on checkout." {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.Status != "open" {
		t.Errorf("Status = %q", got.Status)
	}
	if got.Priority != "priority" {
		t.Errorf("Priority = %q", got.Priority)
	}
	if got.Contact != "Ada Lovelace" {
		t.Errorf("Contact = %q", got.Contact)
	}
	if got.Customer != "Acme Inc" || got.CustomerID != "co-1" {
		t.Errorf("Customer = %q / %q, want the contact's company", got.Customer, got.CustomerID)
	}
	if got.Fields["contactEmail"] != "ada@example.com" {
		t.Errorf("contactEmail = %q", got.Fields["contactEmail"])
	}
	if got.URL != "https://app.intercom.com/a/inbox/abc1234/inbox/conversation/1001" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.CreatedAt.Unix() != 1788000000 || got.UpdatedAt.Unix() != 1788003600 {
		t.Errorf("timestamps = %v / %v, want the unix seconds from the payload", got.CreatedAt, got.UpdatedAt)
	}
	if w := c.WarningsFor("1001"); w != nil {
		t.Errorf("warnings = %v, want none", w)
	}

	// The app id and the contact are each read once and cached.
	if _, err := c.Get(context.Background(), "1001"); err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if got := cs.count("/me"); got != 1 {
		t.Errorf("/me hits = %d, want 1 (the app id is cached)", got)
	}
	if got := cs.count("/contacts/c-1"); got != 1 {
		t.Errorf("contact hits = %d, want 1 (the contact is cached)", got)
	}
}

func TestGet_ContactLookupFailureIsAWarningNotAnError(t *testing.T) {
	cs := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		wantAuth(t, r)
		switch r.URL.Path {
		case "/me":
			writeFixture(t, w, "me.json")
		case "/conversations/1001":
			writeFixture(t, w, "conversation.json")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	got, err := c.Get(context.Background(), "1001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Contact != "c-1" {
		t.Errorf("Contact = %q, want the bare contact id when the lookup failed", got.Contact)
	}
	warnings := c.WarningsFor("1001")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "contact c-1") {
		t.Errorf("warnings = %v, want one naming the contact", warnings)
	}
}

// --- Threads ---

func TestThreads_OrderRolesAndInternalNotes(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "conversation.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	th, err := c.Threads(context.Background(), "1001")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	// Five entries: the source message and four parts. The assignment part
	// carries neither text nor an attachment and is left out.
	if len(th) != 5 {
		t.Fatalf("len(thread) = %d, want 5: %+v", len(th), th)
	}
	want := []struct {
		author string
		role   ticket.Role
		text   string
	}{
		{"Ada Lovelace", ticket.RoleCustomer, "Payments are failing on **checkout**."},
		{"Grace Hopper", ticket.RoleAgent, "Looking into it now."},
		{"Grace Hopper (internal)", ticket.RoleAgent, "Card processor is rate limiting us."},
		{"Operator", ticket.RoleSystem, "Auto-reply: someone will be with you shortly."},
		{"Ada Lovelace", ticket.RoleCustomer, "Here is the screenshot."},
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
	if len(th[0].AttachmentIDs) != 1 || th[0].AttachmentIDs[0] != "9001-1" {
		t.Errorf("source attachment ids = %v", th[0].AttachmentIDs)
	}
	if len(th[4].AttachmentIDs) != 2 {
		t.Errorf("last message attachment ids = %v, want two", th[4].AttachmentIDs)
	}

	// The payload says six parts and sent five, which is the API's own cap
	// truncating the thread: that has to reach the operator.
	warnings := c.WarningsFor("1001")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "truncated at 5 of 6") {
		t.Fatalf("warnings = %v, want one naming the truncation", warnings)
	}
	if got := c.WarningsFor("1001"); got != nil {
		t.Errorf("warnings after read = %v, want nil", got)
	}
}

func TestThreads_EmptySourceBodySkipped(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "conversation_empty_source.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	th, err := c.Threads(context.Background(), "1002")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	// The source message carries neither text nor an attachment (a bot
	// automation start), so only the one real part survives the filter.
	if len(th) != 1 {
		t.Fatalf("len(thread) = %d, want 1: %+v", len(th), th)
	}
	if th[0].Author != "Ada Lovelace" || th[0].Role != ticket.RoleCustomer {
		t.Errorf("message = %+v", th[0])
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

// setRedirect makes every later request answer with a redirect to raw.
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

func TestAttachments_DownloadSanitizeAndTrust(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "conversation.json"))
	cdn := newFileServer(t)
	uploads := newFileServer(t)
	evil := newFileServer(t)
	c := newClient(t, map[string]string{
		apiHost:              addrOf(cs.Server),
		cdnHost:              addrOf(cdn.Server),
		uploadHost:           addrOf(uploads.Server),
		"files.evil.example": addrOf(evil.Server),
	})

	dir := filepath.Join(t.TempDir(), "TCK-1")
	atts, err := c.Attachments(context.Background(), "1001", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("len(attachments) = %d, want 2: %+v", len(atts), atts)
	}
	if atts[0].Path != "TCK-1/1-trace.log" {
		t.Errorf("first Path = %q", atts[0].Path)
	}
	if atts[1].Name != "shot.png" || atts[1].Path != "TCK-1/2-shot.png" {
		t.Errorf("second attachment = %+v, want the directory traversal stripped", atts[1])
	}
	if atts[1].MIME != "image/png" {
		t.Errorf("second MIME = %q", atts[1].MIME)
	}
	body, err := os.ReadFile(filepath.Join(dir, "2-shot.png"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(body) != attachmentBody {
		t.Errorf("downloaded body = %q", body)
	}

	// Neither attachment host may be handed the workspace token: their
	// URLs are pre-signed and need none.
	for name, fs := range map[string]*fileServer{"intercomcdn": cdn, "intercom-attachments": uploads} {
		hits, sawAuth := fs.state()
		if hits != 1 {
			t.Errorf("%s hits = %d, want 1", name, hits)
		}
		if sawAuth {
			t.Errorf("%s was sent the access token", name)
		}
	}
	if hits, _ := evil.state(); hits != 0 {
		t.Errorf("untrusted host was called %d times, want 0", hits)
	}

	warnings := c.WarningsFor("1001")
	if len(warnings) == 0 {
		t.Fatal("warnings = none, want one naming the untrusted attachment host")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "files.evil.example") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one naming files.evil.example", warnings)
	}
}

func TestAttachments_RedirectToForeignHostRefused(t *testing.T) {
	cs := newCountingServer(t, apiHandler(t, "conversation.json"))
	cdn := newFileServer(t)
	uploads := newFileServer(t)
	elsewhere := newFileServer(t)
	cdn.setRedirect("https://elsewhere.example/file")
	uploads.setRedirect("https://elsewhere.example/file")
	c := newClient(t, map[string]string{
		apiHost:              addrOf(cs.Server),
		cdnHost:              addrOf(cdn.Server),
		uploadHost:           addrOf(uploads.Server),
		"elsewhere.example":  addrOf(elsewhere.Server),
		"files.evil.example": addrOf(elsewhere.Server),
	})

	dir := filepath.Join(t.TempDir(), "TCK-1")
	_, err := c.Attachments(context.Background(), "1001", dir)
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
	cs := newCountingServer(t, apiHandler(t, "conversation.json"))
	cdn := newFileServer(t)
	uploads := newFileServer(t)
	evil := newFileServer(t)
	c := newClient(t, map[string]string{
		apiHost:              addrOf(cs.Server),
		cdnHost:              addrOf(cdn.Server),
		uploadHost:           addrOf(uploads.Server),
		"files.evil.example": addrOf(evil.Server),
	})

	old := maxAttachmentBytes
	maxAttachmentBytes = 4
	defer func() { maxAttachmentBytes = old }()

	dir := filepath.Join(t.TempDir(), "TCK-1")
	atts, err := c.Attachments(context.Background(), "1001", dir)
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
		{"https://api.intercom.io/conversations/1", true, true},
		{"https://API.Intercom.io.:443/conversations/1", true, true},
		{"https://acme.intercomcdn.com/i/o/x.png", true, false},
		{"https://intercomcdn.com/i/o/x.png", true, false},
		{"https://acme.intercom-attachments-7.com/i/o/x.png", true, false},
		{"https://intercom-attachments-1.com/x", true, false},
		{"https://uploads.intercom.io/x", true, false},
		{"http://api.intercom.io/x", false, false},
		{"https://api.intercom.io@attacker.example/x", false, false},
		{"https://intercom-attachments-1.com.evil.example/x", false, false},
		{"https://intercomcdn.com.evil.example/x", false, false},
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
			_, _ = w.Write([]byte(`{"type":"error.list","errors":[{"code":"nope"}]}`))
		})
		c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

		_, err := c.Get(context.Background(), "1001")
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
		apiHandler(t, "conversation.json")(w, r)
	})
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	if _, err := c.Threads(context.Background(), "1001"); err != nil {
		t.Fatalf("Threads: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one 429 honoured, then the real answer)", calls)
	}
}

func TestErrorMessages_NeverContainTheAccessToken(t *testing.T) {
	cs := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error.list","errors":[{"code":"boom"}]}`))
	})
	c := newClient(t, map[string]string{apiHost: addrOf(cs.Server)})

	_, err := c.Get(context.Background(), "1001")
	if err == nil {
		t.Fatal("Get: want error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error repeats the access token: %s", err)
	}
}
