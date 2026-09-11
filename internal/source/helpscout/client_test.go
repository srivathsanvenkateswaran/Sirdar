package helpscout

import (
	"context"
	"encoding/base64"
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
	testClientID     = "hs-client-id"
	testClientSecret = "hs-client-secret-value"
	testToken        = "hs-access-token-1"
	testToken2       = "hs-access-token-2"
	attachmentBody   = "hello world"
)

// --- harness ---

// hostRouter dials the real address of a virtual Help Scout hostname
// without touching DNS: it keeps the original Host on the wire (so the
// server side can tell which virtual host was targeted, and Go re-sends
// the Authorization header only when a redirect stays on the same host)
// while redirecting the actual TCP connection to an httptest listener.
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
	c, err := New(Config{ClientID: testClientID, ClientSecret: testClientSecret}, hc)
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

// api is a stand-in for api.helpscout.net: it mints tokens, counts hits
// per path, and serves whatever the test's handler answers with.
type api struct {
	*httptest.Server
	t *testing.T

	mu       sync.Mutex
	hits     map[string]int
	token    string   // the token the next mint hands out
	mintSeq  []string // when set, the token each successive mint hands out
	accepted string   // the token requests must carry; "" means whatever was last minted
	mints    int
	auths    []string // Authorization header seen on each non-token request
}

func newAPI(t *testing.T, h http.HandlerFunc) *api {
	t.Helper()
	a := &api{t: t, hits: map[string]int{}, token: testToken}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.hits[r.URL.Path]++
		a.mu.Unlock()

		if r.URL.Path == "/v2/oauth2/token" {
			a.serveToken(w, r)
			return
		}
		a.mu.Lock()
		a.auths = append(a.auths, r.Header.Get("Authorization"))
		want := a.accepted
		if want == "" {
			want = a.token
		}
		a.mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer "+want {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		h(w, r)
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *api) serveToken(w http.ResponseWriter, r *http.Request) {
	a.t.Helper()
	if r.Method != http.MethodPost {
		a.t.Errorf("token endpoint method = %s, want POST", r.Method)
	}
	if err := r.ParseForm(); err != nil {
		a.t.Errorf("token endpoint: parse form: %v", err)
	}
	if got := r.PostFormValue("grant_type"); got != "client_credentials" {
		a.t.Errorf("grant_type = %q, want client_credentials", got)
	}
	if got := r.PostFormValue("client_id"); got != testClientID {
		a.t.Errorf("client_id = %q", got)
	}
	if got := r.PostFormValue("client_secret"); got != testClientSecret {
		a.t.Errorf("client_secret = %q", got)
	}
	a.mu.Lock()
	a.mints++
	tok := a.token
	if n := len(a.mintSeq); n > 0 {
		i := a.mints - 1
		if i >= n {
			i = n - 1
		}
		tok = a.mintSeq[i]
	}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"token_type":"bearer","access_token":%q,"expires_in":172800}`, tok)
}

func (a *api) count(path string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hits[path]
}

func (a *api) mintCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mints
}

func (a *api) accept(tok string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accepted = tok
}

func (a *api) authHeaders() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.auths...)
}

// conversationHandler serves the conversation fixture, its thread pages,
// its attachment data and the mailbox list.
func conversationHandler(t *testing.T, convFixture string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/mailboxes":
			writeFixture(t, w, "mailboxes.json")
		case r.URL.Path == "/v2/conversations/123":
			if got := r.URL.Query().Get("embed"); got != "threads" {
				t.Errorf("embed = %q, want threads", got)
			}
			writeFixture(t, w, convFixture)
		case r.URL.Path == "/v2/conversations/123/threads":
			writeFixture(t, w, "threads_page2.json")
		case strings.HasSuffix(r.URL.Path, "/attachments/900/data"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":%q}`, base64.StdEncoding.EncodeToString([]byte(attachmentBody)))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
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
	if _, err := New(Config{ClientSecret: "s"}, nil); err == nil {
		t.Error("New without clientId: want error")
	}
	if _, err := New(Config{ClientID: "c"}, nil); err == nil {
		t.Error("New without clientSecret: want error")
	}
	c, err := New(Config{ClientID: " c ", ClientSecret: " s "}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.ClientID != "c" || c.cfg.ClientSecret != "s" {
		t.Errorf("credentials not trimmed: %q / %q", c.cfg.ClientID, c.cfg.ClientSecret)
	}
	if c.baseURL != "https://api.helpscout.net" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
}

// --- Ping and token handling ---

func TestPing_MintsTokenOnceAndSendsBearer(t *testing.T) {
	a := newAPI(t, conversationHandler(t, "conversation.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	for i := 0; i < 3; i++ {
		if err := c.Ping(context.Background()); err != nil {
			t.Fatalf("Ping %d: %v", i, err)
		}
	}
	if got := a.mintCount(); got != 1 {
		t.Errorf("token mints = %d, want 1 (the token is cached until it expires)", got)
	}
	for _, h := range a.authHeaders() {
		if h != "Bearer "+testToken {
			t.Errorf("Authorization = %q, want the minted bearer token", h)
		}
	}
	if got := a.count("/v2/mailboxes"); got != 3 {
		t.Errorf("mailbox hits = %d, want 3", got)
	}
}

func TestToken_RefreshedOnceAfter401(t *testing.T) {
	a := newAPI(t, conversationHandler(t, "conversation.json"))
	// The account accepts only the second token: the first one behaves
	// like a token revoked at the Help Scout console before its stated
	// expiry, which is the case a plain expiry check cannot catch.
	a.mintSeq = []string{testToken, testToken2}
	a.accept(testToken2)
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := a.mintCount(); got != 2 {
		t.Errorf("token mints = %d, want 2 (the 401 re-mints exactly once)", got)
	}
	if got := a.count("/v2/mailboxes"); got != 2 {
		t.Errorf("mailbox hits = %d, want 2", got)
	}
	headers := a.authHeaders()
	if len(headers) != 2 || headers[0] != "Bearer "+testToken || headers[1] != "Bearer "+testToken2 {
		t.Errorf("Authorization headers = %v, want the stale token then the fresh one", headers)
	}

	// The refreshed token is what the next call reuses: no third mint.
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping after refresh: %v", err)
	}
	if got := a.mintCount(); got != 2 {
		t.Errorf("token mints = %d after a second Ping, want 2", got)
	}
}

func TestToken_PersistentlyRejectedIsAuthError(t *testing.T) {
	a := newAPI(t, conversationHandler(t, "conversation.json"))
	a.accept("some-other-token")
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping: want error")
	}
	if code := sourceCode(t, err); code != source.Auth {
		t.Errorf("code = %q, want %q", code, source.Auth)
	}
	if got := a.count("/v2/mailboxes"); got != 2 {
		t.Errorf("mailbox hits = %d, want 2 (the call is retried exactly once)", got)
	}
}

func TestToken_GrantRejectedIsAuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer ts.Close()
	c := newClient(t, map[string]string{apiHost: addrOf(ts)})

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
	a := newAPI(t, conversationHandler(t, "conversation.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	got, err := c.Get(context.Background(), "123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "123" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Subject != "Checkout fails with a 500" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q", got.Status)
	}
	if got.Channel != "email" {
		t.Errorf("Channel = %q", got.Channel)
	}
	if got.Contact != "Ada Lovelace" {
		t.Errorf("Contact = %q", got.Contact)
	}
	if got.CustomerID != "555" {
		t.Errorf("CustomerID = %q", got.CustomerID)
	}
	if got.URL != "https://secure.helpscout.net/conversation/123" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Fields["mailboxId"] != "99" {
		t.Errorf("mailboxId = %q", got.Fields["mailboxId"])
	}
	if got.Fields["tags"] != "billing, urgent" {
		t.Errorf("tags = %q", got.Fields["tags"])
	}
	if got.Fields["customerEmail"] != "ada@example.com" {
		t.Errorf("customerEmail = %q", got.Fields["customerEmail"])
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps not parsed: %v / %v", got.CreatedAt, got.UpdatedAt)
	}
	if got.CreatedAt.After(got.UpdatedAt) {
		t.Errorf("CreatedAt %v after UpdatedAt %v", got.CreatedAt, got.UpdatedAt)
	}
}

// --- Threads ---

func TestThreads_OrderRolesAndInternalNotes(t *testing.T) {
	a := newAPI(t, conversationHandler(t, "conversation.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	th, err := c.Threads(context.Background(), "123")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 4 {
		t.Fatalf("len(thread) = %d, want 4 (the draft reply is left out): %+v", len(th), th)
	}
	want := []struct {
		author string
		role   ticket.Role
		text   string
	}{
		{"Ada Lovelace", ticket.RoleCustomer, "Every checkout returns a **500**."},
		{"Grace Hopper", ticket.RoleAgent, "Thanks for the report — looking now."},
		{"Grace Hopper (internal)", ticket.RoleAgent, "Refunded the duplicate charge already."},
		{"Grace Hopper", ticket.RoleSystem, ""},
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
	for i := 1; i < len(th); i++ {
		if th[i].At.Before(th[i-1].At) {
			t.Errorf("message %d is out of order", i)
		}
	}
	if len(th[0].AttachmentIDs) != 2 {
		t.Errorf("first message attachment ids = %v, want two", th[0].AttachmentIDs)
	}
}

func TestThreads_FollowsNextPageLink(t *testing.T) {
	a := newAPI(t, conversationHandler(t, "conversation_paged.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	oldSize := threadsPageSize
	threadsPageSize = 1 // the fixture embeds one thread, which must read as a full first page
	defer func() { threadsPageSize = oldSize }()
	old := maxThreadPages
	maxThreadPages = 2
	defer func() { maxThreadPages = old }()

	th, err := c.Threads(context.Background(), "123")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 2 {
		t.Fatalf("len(thread) = %d, want 2 (one from each page)", len(th))
	}
	if !strings.Contains(th[1].Text, "Page two reply") {
		t.Errorf("second message = %q, want the page-two reply", th[1].Text)
	}
	warnings := c.WarningsFor("123")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "capped at 2") {
		t.Fatalf("warnings = %v, want one page-cap warning", warnings)
	}
	if got := c.WarningsFor("123"); got != nil {
		t.Errorf("warnings after read = %v, want nil", got)
	}
}

func TestThreads_UntrustedNextPageStopsWithWarning(t *testing.T) {
	// The embedded page is trusted by construction (api.helpscout.net), but
	// its own "next" link — read from the dedicated threads endpoint's
	// response, not the conversation's — points off-host; that page must be
	// skipped rather than fetched.
	a := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/conversations/123":
			writeFixture(t, w, "conversation_untrusted_page.json")
		case r.URL.Path == "/v2/conversations/123/threads":
			writeFixture(t, w, "threads_untrusted_next.json")
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	})
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("untrusted thread page host was called: %s", r.URL.Path)
	}))
	defer evil.Close()
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server), "threads.evil.example": addrOf(evil)})

	old := threadsPageSize
	threadsPageSize = 1 // both fixtures embed one thread, which must read as a full page
	defer func() { threadsPageSize = old }()

	th, err := c.Threads(context.Background(), "123")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 2 {
		t.Fatalf("len(thread) = %d, want 2 (the embedded page plus the one trusted follow-up page)", len(th))
	}
	warnings := c.WarningsFor("123")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "threads.evil.example") {
		t.Errorf("warnings = %v, want one naming the untrusted host", warnings)
	}
}

func TestThreads_StopsAtTotalPages(t *testing.T) {
	// The dedicated endpoint's own page.totalPages says the feed ends at
	// page 2, even though that page's "next" link (unrealistically) still
	// points further: pagination must stop on totalPages without needing
	// the maxThreadPages cap to kick in.
	a := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/conversations/123":
			writeFixture(t, w, "conversation_paged.json")
		case r.URL.Path == "/v2/conversations/123/threads":
			writeFixture(t, w, "threads_page2_final.json")
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		}
	})
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	old := threadsPageSize
	threadsPageSize = 1
	defer func() { threadsPageSize = old }()

	th, err := c.Threads(context.Background(), "123")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 2 {
		t.Fatalf("len(thread) = %d, want 2", len(th))
	}
	if got := a.count("/v2/conversations/123/threads"); got != 1 {
		t.Errorf("threads endpoint hits = %d, want 1 (totalPages stops it before a third page)", got)
	}
	if w := c.WarningsFor("123"); w != nil {
		t.Errorf("warnings = %v, want none", w)
	}
}

// --- Attachments ---

func TestAttachments_DownloadSanitizeAndUntrustedHostSkipped(t *testing.T) {
	a := newAPI(t, conversationHandler(t, "conversation.json"))
	var evilHits int
	var mu sync.Mutex
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		evilHits++
		mu.Unlock()
		if r.Header.Get("Authorization") != "" {
			t.Errorf("credential sent to untrusted host")
		}
	}))
	defer evil.Close()
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server), "files.evil.example": addrOf(evil)})

	dir := filepath.Join(t.TempDir(), "TCK-1")
	atts, err := c.Attachments(context.Background(), "123", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("len(attachments) = %d, want 1: %+v", len(atts), atts)
	}
	if atts[0].Name != "evil report.png" {
		t.Errorf("Name = %q, want the directory traversal stripped", atts[0].Name)
	}
	if atts[0].Path != "TCK-1/1-evil report.png" {
		t.Errorf("Path = %q", atts[0].Path)
	}
	if atts[0].MIME != "image/png" {
		t.Errorf("MIME = %q", atts[0].MIME)
	}
	body, err := os.ReadFile(filepath.Join(dir, "1-evil report.png"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(body) != attachmentBody {
		t.Errorf("downloaded body = %q, want %q", body, attachmentBody)
	}
	mu.Lock()
	hits := evilHits
	mu.Unlock()
	if hits != 0 {
		t.Errorf("untrusted host was called %d times, want 0", hits)
	}
	warnings := c.WarningsFor("123")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "files.evil.example") {
		t.Errorf("warnings = %v, want one naming the untrusted attachment host", warnings)
	}
}

func TestAttachments_RedirectToForeignHostRefused(t *testing.T) {
	var elsewhereHits int
	var mu sync.Mutex
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		elsewhereHits++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":"aGVsbG8="}`))
	}))
	defer elsewhere.Close()

	a := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/conversations/123":
			writeFixture(t, w, "conversation.json")
		case strings.HasSuffix(r.URL.Path, "/data"):
			http.Redirect(w, r, "https://elsewhere.example/file", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server), "elsewhere.example": addrOf(elsewhere)})

	dir := filepath.Join(t.TempDir(), "TCK-1")
	_, err := c.Attachments(context.Background(), "123", dir)
	if err == nil {
		t.Fatal("Attachments: want an error when every attachment fails")
	}
	if !strings.Contains(err.Error(), "elsewhere.example") {
		t.Errorf("error = %v, want it to name the refused redirect host", err)
	}
	mu.Lock()
	hits := elsewhereHits
	mu.Unlock()
	if hits != 0 {
		t.Errorf("redirect target was called %d times, want 0", hits)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files left on disk: %v", entries)
	}
}

func TestAttachments_OversizeIsRefusedAndNotLeftOnDisk(t *testing.T) {
	a := newAPI(t, conversationHandler(t, "conversation.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	old := maxAttachmentBytes
	maxAttachmentBytes = 4
	defer func() { maxAttachmentBytes = old }()

	dir := filepath.Join(t.TempDir(), "TCK-1")
	atts, err := c.Attachments(context.Background(), "123", dir)
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
	c, err := New(Config{ClientID: "c", ClientSecret: "s"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range []struct {
		raw               string
		trusted, sendAuth bool
	}{
		{"https://api.helpscout.net/v2/x", true, true},
		{"https://API.HelpScout.net.:443/v2/x", true, true},
		{"http://api.helpscout.net/v2/x", false, false},
		{"https://api.helpscout.net@attacker.example/x", false, false},
		{"https://secure.helpscout.net/v2/x", false, false},
		{"https://helpscout.net.evil.example/x", false, false},
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
		{http.StatusForbidden, nil, source.Auth},
		{http.StatusTooManyRequests, map[string]string{"Retry-After": "3600"}, source.RateLimited},
		{http.StatusInternalServerError, nil, source.Internal},
		{http.StatusBadGateway, nil, source.Internal},
	} {
		a := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
			for k, v := range tc.header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":"nope"}`))
		})
		c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

		_, err := c.Get(context.Background(), "123")
		if err == nil {
			t.Fatalf("status %d: want error", tc.status)
		}
		if code := sourceCode(t, err); code != tc.want {
			t.Errorf("status %d: code = %q, want %q", tc.status, code, tc.want)
		}
		a.Close()
	}
}

func TestRetryAfter_HonouredOnce(t *testing.T) {
	var calls int
	var mu sync.Mutex
	a := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeFixture(t, w, "conversation.json")
	})
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	if _, err := c.Get(context.Background(), "123"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one 429 honoured, then the real answer)", calls)
	}
}

func TestErrorMessages_NeverContainCredentials(t *testing.T) {
	// Every error this client builds names the method, the path and the
	// status; the credentials travel in a header and a form body and must
	// appear in none of it.
	a := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	_, err := c.Get(context.Background(), "123")
	if err == nil {
		t.Fatal("Get: want error")
	}
	msg := err.Error()
	if strings.Contains(msg, testClientSecret) {
		t.Errorf("error repeats the client secret: %s", msg)
	}
	if strings.Contains(msg, testToken) {
		t.Errorf("error repeats the access token: %s", msg)
	}

	// And the same for the token endpoint's own failure.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer ts.Close()
	c2 := newClient(t, map[string]string{apiHost: addrOf(ts)})
	err = c2.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping: want error")
	}
	if strings.Contains(err.Error(), testClientSecret) || strings.Contains(err.Error(), testClientID) {
		t.Errorf("token error repeats a credential: %s", err)
	}
}

func TestWarningsAreKeyedByTicketAndClearedOnRead(t *testing.T) {
	a := newAPI(t, conversationHandler(t, "conversation.json"))
	c := newClient(t, map[string]string{apiHost: addrOf(a.Server)})

	dir := filepath.Join(t.TempDir(), "TCK-1")
	if _, err := c.Attachments(context.Background(), "123", dir); err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if got := c.WarningsFor("other"); got != nil {
		t.Errorf("warnings for a different ticket = %v, want nil", got)
	}
	if got := c.WarningsFor("123"); len(got) != 1 {
		t.Fatalf("warnings = %v, want one", got)
	}
	if got := c.WarningsFor("123"); got != nil {
		t.Errorf("warnings after read = %v, want nil", got)
	}
}
