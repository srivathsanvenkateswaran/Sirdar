package gorgias

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
	testEmail      = "ops@acme.com"
	testAPIKey     = "gorgias-api-key-secret"
	accountHost    = "acme.gorgias.com"
	filesHost      = "files.gorgias.com"
	attackerHost   = "attacker.example"
	attachmentBody = "hello world"
)

// --- harness ---

// hostRouter dials the real address of a virtual Gorgias hostname without
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
	c, err := New(Config{Account: "acme", Email: testEmail, APIKey: testAPIKey}, hc)
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
	if !ok || user != testEmail || pass != testAPIKey {
		t.Errorf("%s: basic auth = (%q, %q, %v), want the configured email and key", r.URL.Path, user, pass, ok)
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
// pagination behaviour.
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

// apiHandler serves the ticket, the message feed and the account record
// from the fixtures, and any attachment path with a fixed body.
func apiHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/account":
			wantBasicAuth(t, r)
			writeFixture(t, w, "account.json")
		case r.URL.Path == "/api/tickets/921":
			wantBasicAuth(t, r)
			writeFixture(t, w, "ticket.json")
		case r.URL.Path == "/api/messages":
			wantBasicAuth(t, r)
			writeFixture(t, w, "messages.json")
		case strings.HasPrefix(r.URL.Path, "/api/attachments/download/"):
			wantBasicAuth(t, r)
			_, _ = w.Write([]byte(attachmentBody))
		default:
			http.NotFound(w, r)
		}
	}
}

// --- Config / New ---

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(Config{Email: testEmail, APIKey: "k"}, nil); err == nil {
		t.Error("New with neither account nor baseUrl: want an error")
	}
	if _, err := New(Config{Account: "acme", APIKey: "k"}, nil); err == nil {
		t.Error("New with no email: want an error")
	}
	if _, err := New(Config{Account: "acme", Email: testEmail}, nil); err == nil {
		t.Error("New with no apiKey: want an error")
	}
	if _, err := New(Config{Account: "acme.example.com", Email: testEmail, APIKey: "k"}, nil); err == nil {
		t.Error("New with a foreign host as the account: want an error")
	}
}

func TestNewDerivesBaseURL(t *testing.T) {
	for _, tc := range []struct{ account, baseURL, want string }{
		{account: "acme", want: "https://acme.gorgias.com"},
		{account: "https://acme.gorgias.com/", want: "https://acme.gorgias.com"},
		{account: "acme.gorgias.com", want: "https://acme.gorgias.com"},
		{account: "acme", baseURL: "https://proxy.internal/gorgias/", want: "https://proxy.internal/gorgias"},
	} {
		c, err := New(Config{Account: tc.account, BaseURL: tc.baseURL, Email: testEmail, APIKey: "k"}, nil)
		if err != nil {
			t.Fatalf("New(%q, %q): %v", tc.account, tc.baseURL, err)
		}
		if c.baseURL != tc.want {
			t.Errorf("New(%q, %q).baseURL = %q, want %q", tc.account, tc.baseURL, c.baseURL, tc.want)
		}
	}
}

// --- Ping ---

func TestPingCallsAccount(t *testing.T) {
	srv := newCountingServer(t, apiHandler(t))
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if srv.count("/api/account") != 1 {
		t.Fatalf("Ping did not call /api/account (hits: %d)", srv.count("/api/account"))
	}
}

// --- Get ---

func TestGetMapsTicket(t *testing.T) {
	srv := newCountingServer(t, apiHandler(t))
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})

	got, err := c.Get(context.Background(), "921")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := ticket.HelpdeskTicket{
		ID:         "921",
		Subject:    "تصدير التقرير لا يعمل",
		Status:     "open",
		Channel:    "email",
		Contact:    "rania@example.com",
		Customer:   "Rania Haddad",
		CustomerID: "4561",
		URL:        "https://acme.gorgias.com/app/ticket/921",
	}
	if got.ID != want.ID || got.Subject != want.Subject || got.Status != want.Status ||
		got.Channel != want.Channel || got.Contact != want.Contact ||
		got.Customer != want.Customer || got.CustomerID != want.CustomerID || got.URL != want.URL {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
	// The Ticket object has no priority attribute; guessing one out of the
	// tags would be worse than leaving it empty.
	if got.Priority != "" {
		t.Errorf("Priority = %q, want empty: Gorgias tickets carry none", got.Priority)
	}
	if got.CreatedAt.Format("2006-01-02T15:04:05") != "2026-09-10T08:30:00" {
		t.Errorf("CreatedAt = %v, want the fractional-second timestamp parsed", got.CreatedAt)
	}
	if got.UpdatedAt.Format("2006-01-02T15:04:05") != "2026-09-10T11:05:00" {
		t.Errorf("UpdatedAt = %v", got.UpdatedAt)
	}
	if got.Fields["tags"] != "billing, urgent" {
		t.Errorf("tags = %q, want the blank tag dropped", got.Fields["tags"])
	}
	if got.Fields["assignee"] != "Sami Nasr" {
		t.Errorf("assignee = %q", got.Fields["assignee"])
	}
	if got.Fields["language"] != "ar" || got.Fields["via"] != "email" {
		t.Errorf("fields = %v", got.Fields)
	}
	if _, ok := got.Fields["closedAt"]; ok {
		t.Errorf("closedAt was set for an open ticket: %v", got.Fields)
	}
}

func TestGetStatusErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   source.Code
	}{
		{http.StatusNotFound, source.NotFound},
		{http.StatusUnauthorized, source.Auth},
		{http.StatusForbidden, source.Auth},
		{http.StatusTooManyRequests, source.RateLimited},
		{http.StatusInternalServerError, source.Internal},
	} {
		srv := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", tc.status)
		})
		c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})
		_, err := c.Get(context.Background(), "921")
		if err == nil {
			t.Fatalf("status %d: want an error", tc.status)
		}
		if got := sourceCode(t, err); got != tc.want {
			t.Errorf("status %d: code = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// TestErrorsNeverCarryTheCredential covers the one thing a helpdesk error
// must never do: quote the key it authenticated with.
func TestErrorsNeverCarryTheCredential(t *testing.T) {
	srv := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad credentials for "+testAPIKey, http.StatusInternalServerError)
	})
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})
	_, err := c.Get(context.Background(), "921")
	if err == nil {
		t.Fatal("want an error")
	}
	// The body is quoted, which is how an operator sees what the API said;
	// what matters is that nothing this client holds is added to it.
	if strings.Contains(err.Error(), testEmail) {
		t.Fatalf("error quotes the account email: %v", err)
	}
}

// --- Threads ---

func TestThreadsRolesAndInternalNotes(t *testing.T) {
	srv := newCountingServer(t, apiHandler(t))
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})

	th, err := c.Threads(context.Background(), "921")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	// The fifth fixture message has neither text nor an attachment and is
	// left out rather than filling the thread with an empty entry.
	if len(th) != 4 {
		t.Fatalf("Threads returned %d messages, want 4:\n%+v", len(th), th)
	}

	want := []struct {
		author string
		role   ticket.Role
	}{
		{"Rania Haddad", ticket.RoleCustomer},
		{"support@acme.com", ticket.RoleSystem},
		{"Sami Nasr (internal)", ticket.RoleAgent},
		{"Sami Nasr", ticket.RoleAgent},
	}
	for i, w := range want {
		if th[i].Author != w.author || th[i].Role != w.role {
			t.Errorf("message %d = (%q, %q), want (%q, %q)", i, th[i].Author, th[i].Role, w.author, w.role)
		}
	}
	if !strings.Contains(th[0].Text, "does **nothing**") {
		t.Errorf("html body was not converted to Markdown: %q", th[0].Text)
	}
	if got := th[0].AttachmentIDs; len(got) != 1 || got[0] != "5001-1" {
		t.Errorf("attachment ids = %v, want [5001-1]", got)
	}
	if !th[0].At.Before(th[3].At) {
		t.Errorf("thread is not in creation order: %v then %v", th[0].At, th[3].At)
	}
}

// TestThreadsAsksForCreationOrder proves the feed is requested oldest
// first: a ticket long enough to hit the page cap must keep the customer's
// opening complaint, not its most recent replies.
func TestThreadsAsksForCreationOrder(t *testing.T) {
	var mu sync.Mutex
	var gotQuery url.Values
	srv := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotQuery = r.URL.Query()
		mu.Unlock()
		writeFixture(t, w, "messages.json")
	})
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})
	if _, err := c.Threads(context.Background(), "921"); err != nil {
		t.Fatalf("Threads: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := gotQuery.Get("order_by"); got != "created_datetime:asc" {
		t.Errorf("order_by = %q, want created_datetime:asc", got)
	}
	if got := gotQuery.Get("ticket_id"); got != "921" {
		t.Errorf("ticket_id = %q, want 921", got)
	}
	if got := gotQuery.Get("limit"); got != "100" {
		t.Errorf("limit = %q, want the API maximum", got)
	}
}

// pagedMessages serves a cursor-paginated feed: each request answers with
// one message and a next_cursor, for pages pages, then stops.
func pagedMessages(t *testing.T, pages int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		n := 1
		if cursor != "" {
			fmt.Sscanf(cursor, "page-%d", &n)
		}
		next := ""
		if n < pages {
			next = fmt.Sprintf(`"page-%d"`, n+1)
		} else {
			next = "null"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"id":%d,"ticket_id":921,"public":true,"from_agent":false,
			"body_text":"message %d","sender":{"name":"Rania"},
			"created_datetime":"2026-09-10T08:0%d:00+00:00"}],
			"meta":{"next_cursor":%s}}`, 6000+n, n, n%10, next)
	}
}

func TestThreadsFollowsCursor(t *testing.T) {
	srv := newCountingServer(t, pagedMessages(t, 3))
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})

	th, err := c.Threads(context.Background(), "921")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 3 {
		t.Fatalf("Threads returned %d messages, want one per page", len(th))
	}
	if srv.count("/api/messages") != 3 {
		t.Fatalf("/api/messages was hit %d times, want 3", srv.count("/api/messages"))
	}
	if w := c.WarningsFor("921"); len(w) != 0 {
		t.Fatalf("a feed that ended on its own warned: %v", w)
	}
}

// TestThreadsPageCapWarns: a feed that never stops is cut off at the page
// cap, with the truncation said out loud rather than a thread that quietly
// stops.
func TestThreadsPageCapWarns(t *testing.T) {
	orig := maxMessagePages
	maxMessagePages = 2
	t.Cleanup(func() { maxMessagePages = orig })

	srv := newCountingServer(t, pagedMessages(t, 1000))
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})

	th, err := c.Threads(context.Background(), "921")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 2 {
		t.Fatalf("Threads returned %d messages, want the cap's worth", len(th))
	}
	if srv.count("/api/messages") != 2 {
		t.Fatalf("/api/messages was hit %d times, want the cap", srv.count("/api/messages"))
	}
	warnings := c.WarningsFor("921")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "capped at 2") {
		t.Fatalf("warnings = %v, want one naming the page cap", warnings)
	}
	if len(c.WarningsFor("921")) != 0 {
		t.Fatal("WarningsFor did not consume the warnings")
	}
}

// TestThreadsStopsOnRepeatedCursor: a server that keeps handing back the
// cursor it was given would page forever; the client stops instead.
func TestThreadsStopsOnRepeatedCursor(t *testing.T) {
	srv := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":1,"public":true,"body_text":"hi","created_datetime":"2026-09-10T08:00:00+00:00"}],"meta":{"next_cursor":"same"}}`)
	})
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})
	if _, err := c.Threads(context.Background(), "921"); err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if got := srv.count("/api/messages"); got != 2 {
		t.Fatalf("/api/messages was hit %d times, want 2 (the repeat is what stops it)", got)
	}
}

// --- Attachments ---

func TestAttachmentsHostTrust(t *testing.T) {
	api := newCountingServer(t, apiHandler(t))
	// The Gorgias file host is fetched from but must never be given the
	// API key: its URLs carry their own signature.
	files := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Errorf("%s: the API key was sent to the file host", r.URL.Path)
		}
		_, _ = w.Write([]byte(attachmentBody))
	})
	attacker := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the untrusted host was contacted at all: %s", r.URL.Path)
	})

	c := newClient(t, map[string]string{
		accountHost:  addrOf(api.Server),
		filesHost:    addrOf(files.Server),
		attackerHost: addrOf(attacker.Server),
	})

	dir := filepath.Join(t.TempDir(), "attachments")
	got, err := c.Attachments(context.Background(), "921", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Attachments returned %d files, want the two trusted ones:\n%+v", len(got), got)
	}
	if got[0].ID != "5001-1" || got[0].Name != "screenshot.png" || got[0].MIME != "image/png" {
		t.Errorf("first attachment = %+v", got[0])
	}
	if got[0].Path != "attachments/1-screenshot.png" {
		t.Errorf("path = %q, want the index prefix and the dir base", got[0].Path)
	}
	// The second file's name field is empty, so the name comes from the
	// URL's path with the signature query dropped.
	if got[1].ID != "5003-1" || got[1].Name != "worker-log.txt" {
		t.Errorf("second attachment = %+v", got[1])
	}
	for _, a := range got {
		b, rerr := os.ReadFile(filepath.Join(filepath.Dir(dir), a.Path))
		if rerr != nil {
			t.Fatalf("read %s: %v", a.Path, rerr)
		}
		if string(b) != attachmentBody {
			t.Errorf("%s content = %q", a.Path, b)
		}
	}

	warnings := c.WarningsFor("921")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one for the untrusted host", warnings)
	}
	if !strings.Contains(warnings[0], attackerHost) {
		t.Errorf("warning does not name the refused host: %q", warnings[0])
	}
	// The warning reaches the agent and the operator; the rest of an
	// attacker-chosen URL has no business there.
	if strings.Contains(warnings[0], "leak-me") || strings.Contains(warnings[0], "/evil.png") {
		t.Errorf("warning quotes the untrusted URL beyond its host: %q", warnings[0])
	}
}

// TestAttachmentsRedirectRefused: Gorgias answers a download with a 307 to
// a signed URL. A redirect Location arrives inside a server response, so a
// hop off the trusted hosts is refused before the request is made, and the
// failure names the host alone.
func TestAttachmentsRedirectRefused(t *testing.T) {
	attacker := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect was followed to the untrusted host: %s", r.URL.Path)
	})
	api := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/attachments/download/") {
			http.Redirect(w, r, "https://"+attackerHost+"/signed/screenshot.png?sig=leak-me", http.StatusTemporaryRedirect)
			return
		}
		apiHandler(t)(w, r)
	})
	files := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(attachmentBody))
	})

	c := newClient(t, map[string]string{
		accountHost:  addrOf(api.Server),
		filesHost:    addrOf(files.Server),
		attackerHost: addrOf(attacker.Server),
	})

	dir := filepath.Join(t.TempDir(), "attachments")
	got, err := c.Attachments(context.Background(), "921", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	// The account-host attachment redirected off-host and was refused; the
	// files-host one still arrived, so this is a warning, not a failure.
	if len(got) != 1 || got[0].ID != "5003-1" {
		t.Fatalf("Attachments returned %+v, want only the file that did not redirect", got)
	}
	warnings := c.WarningsFor("921")
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "redirect to untrusted host "+attackerHost) {
		t.Fatalf("warnings = %v, want one naming the refused redirect host", warnings)
	}
	if strings.Contains(joined, "leak-me") || strings.Contains(joined, "/signed/") {
		t.Fatalf("a warning quotes the redirect target beyond its host: %v", warnings)
	}
}

// TestAttachmentsAllFailingIsAnError: when nothing could be downloaded the
// caller gets the failures as an error rather than an empty slice that
// looks like a ticket with no attachments.
func TestAttachmentsAllFailingIsAnError(t *testing.T) {
	api := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/attachments/download/") {
			http.Error(w, "gone", http.StatusNotFound)
			return
		}
		apiHandler(t)(w, r)
	})
	files := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})
	c := newClient(t, map[string]string{
		accountHost:  addrOf(api.Server),
		filesHost:    addrOf(files.Server),
		attackerHost: addrOf(api.Server),
	})

	dir := filepath.Join(t.TempDir(), "attachments")
	got, err := c.Attachments(context.Background(), "921", dir)
	if err == nil {
		t.Fatal("want an error when every attachment failed")
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want nothing", got)
	}
	if w := c.WarningsFor("921"); len(w) != 0 {
		t.Fatalf("failures were recorded twice, as warnings and as the error: %v", w)
	}
}

// TestAttachmentsSizeCap: a file past the ceiling is a failure, and its
// partial output never survives as something that looks like the file.
func TestAttachmentsSizeCap(t *testing.T) {
	orig := maxAttachmentBytes
	// Wide enough for the account-host file (11 bytes), narrow enough to
	// refuse the 4 KiB one.
	maxAttachmentBytes = 20
	t.Cleanup(func() { maxAttachmentBytes = orig })

	api := newCountingServer(t, apiHandler(t))
	files := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	})
	c := newClient(t, map[string]string{
		accountHost:  addrOf(api.Server),
		filesHost:    addrOf(files.Server),
		attackerHost: addrOf(api.Server),
	})

	dir := filepath.Join(t.TempDir(), "attachments")
	if _, err := c.Attachments(context.Background(), "921", dir); err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "worker-log") {
			t.Fatalf("an over-size download was left on disk: %s", e.Name())
		}
	}
	warnings := strings.Join(c.WarningsFor("921"), "\n")
	if !strings.Contains(warnings, "exceeds the 20 byte limit") {
		t.Fatalf("warnings = %q, want the size cap named", warnings)
	}
}

// TestAttachmentsNoneIsNotAnError covers a ticket whose messages carry no
// files at all.
func TestAttachmentsNoneIsNotAnError(t *testing.T) {
	srv := newCountingServer(t, pagedMessages(t, 1))
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})
	got, err := c.Attachments(context.Background(), "921", filepath.Join(t.TempDir(), "attachments"))
	if err != nil || got != nil {
		t.Fatalf("Attachments = (%+v, %v), want (nil, nil)", got, err)
	}
}

// --- warnings ---

// TestWarningsAccumulatePerTicket: a bundle is assembled from Threads and
// Attachments, and the caller reads once at the end, so an earlier call's
// warning has to survive a later one.
func TestWarningsAccumulatePerTicket(t *testing.T) {
	orig := maxMessagePages
	maxMessagePages = 1
	t.Cleanup(func() { maxMessagePages = orig })

	srv := newCountingServer(t, pagedMessages(t, 100))
	c := newClient(t, map[string]string{accountHost: addrOf(srv.Server)})

	if _, err := c.Threads(context.Background(), "921"); err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if _, err := c.Attachments(context.Background(), "921", filepath.Join(t.TempDir(), "a")); err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	warnings := c.WarningsFor("921")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want the page-cap line once and only once", warnings)
	}
	if len(c.WarningsFor("922")) != 0 {
		t.Fatal("warnings leaked onto another ticket")
	}
}
