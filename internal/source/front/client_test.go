package front

import (
	"context"
	"encoding/json"
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
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

const (
	testToken      = "front-api-token-value"
	companyHost    = "acme.api.frontapp.com"
	evilHost       = "files.evil.example"
	attachmentBody = "hello world"
)

// --- harness ---

// hostRouter dials the real address of a virtual Front hostname without
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
	c, err := New(Config{Token: testToken}, hc)
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

// apiServer is the Front API host: it serves the conversation, both feeds,
// the teammate probe and the attachment bytes, and records what it saw.
type apiServer struct {
	*httptest.Server
	mu       sync.Mutex
	hits     map[string]int
	noAuth   []string // paths that arrived without a bearer header
	redirect map[string]string
}

func newAPIServer(t *testing.T) *apiServer {
	t.Helper()
	as := &apiServer{hits: map[string]int{}, redirect: map[string]string{}}
	as.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		as.mu.Lock()
		as.hits[r.URL.Path]++
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			as.noAuth = append(as.noAuth, r.URL.Path)
		}
		to := as.redirect[r.URL.Path]
		as.mu.Unlock()
		if to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}

		switch {
		case r.URL.Path == "/teammates":
			writeFixture(t, w, "teammates.json")
		case r.URL.Path == "/conversations/cnv_1":
			writeFixture(t, w, "conversation.json")
		case r.URL.Path == "/conversations/cnv_2":
			writeFixture(t, w, "conversation_no_updated.json")
		case r.URL.Path == "/conversations/cnv_1/messages":
			if r.URL.Query().Get("page_token") == "PAGE2" {
				writeFixture(t, w, "messages_page2.json")
				return
			}
			if got := r.URL.Query().Get("limit"); got != "100" {
				t.Errorf("messages limit = %q, want 100", got)
			}
			writeFixture(t, w, "messages_page1.json")
		case r.URL.Path == "/conversations/cnv_2/messages":
			_, _ = w.Write([]byte(`{"_pagination":{"next":null},"_results":[]}`))
		case r.URL.Path == "/conversations/cnv_1/comments":
			writeFixture(t, w, "comments.json")
		case r.URL.Path == "/conversations/cnv_2/comments":
			writeFixture(t, w, "comments_empty.json")
		case strings.HasPrefix(r.URL.Path, "/download/"):
			_, _ = w.Write([]byte(attachmentBody))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"_error":{"status":404,"title":"Not found"}}`))
		}
	}))
	t.Cleanup(as.Close)
	return as
}

func (as *apiServer) count(path string) int {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.hits[path]
}

func (as *apiServer) unauthenticated() []string {
	as.mu.Lock()
	defer as.mu.Unlock()
	return append([]string(nil), as.noAuth...)
}

func (as *apiServer) setRedirect(path, to string) {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.redirect[path] = to
}

// hitCounter is a bare server that must never be called.
type hitCounter struct {
	*httptest.Server
	mu   sync.Mutex
	hits int
}

func newHitCounter(t *testing.T) *hitCounter {
	t.Helper()
	hc := &hitCounter{}
	hc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hc.mu.Lock()
		hc.hits++
		hc.mu.Unlock()
		_, _ = w.Write([]byte(attachmentBody))
	}))
	t.Cleanup(hc.Close)
	return hc
}

func (hc *hitCounter) count() int {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	return hc.hits
}

// fullRoutes wires the API host, the per-company host and the untrusted
// host a fixture points at.
func fullRoutes(as *apiServer, evil *hitCounter) map[string]string {
	return map[string]string{
		apiHost:     addrOf(as.Server),
		companyHost: addrOf(as.Server),
		evilHost:    addrOf(evil.Server),
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
		t.Error("New without a token: want error")
	}
	c, err := New(Config{Token: "  t  "}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.Token != "t" {
		t.Errorf("Token = %q, want it trimmed", c.cfg.Token)
	}
	if c.baseURL != "https://api2.frontapp.com" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
}

// --- Ping ---

func TestPing_SendsBearer(t *testing.T) {
	as := newAPIServer(t)
	c := newClient(t, map[string]string{apiHost: addrOf(as.Server)})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := as.count("/teammates"); got != 1 {
		t.Errorf("/teammates hits = %d, want 1", got)
	}
	if got := as.unauthenticated(); len(got) != 0 {
		t.Errorf("requests without the bearer header: %v", got)
	}
}

func TestPing_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"_error":{"status":401}}`))
	}))
	defer srv.Close()
	c := newClient(t, map[string]string{apiHost: addrOf(srv)})

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
	as := newAPIServer(t)
	c := newClient(t, map[string]string{apiHost: addrOf(as.Server)})

	got, err := c.Get(context.Background(), "cnv_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"ID", got.ID, "cnv_1"},
		{"Subject", got.Subject, "Payments are failing on checkout"},
		{"Status", got.Status, "assigned"},
		{"Channel", got.Channel, "conversation"},
		{"Contact", got.Contact, "Ada Lovelace"},
		{"URL", got.URL, "https://app.frontapp.com/open/cnv_1"},
		{"statusCategory", got.Fields["statusCategory"], "open"},
		{"statusId", got.Fields["statusId"], "sts_1"},
		{"tags", got.Fields["tags"], "billing, urgent"},
		{"assignee", got.Fields["assignee"], "Grace Hopper"},
		{"ticketIds", got.Fields["ticketIds"], "OMNI-3217"},
		{"contactHandle", got.Fields["contactHandle"], "ada@example.com"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	// Front models urgency with tags and custom fields; guessing a
	// priority from a tag would be inventing one.
	if got.Priority != "" {
		t.Errorf("Priority = %q, want empty: Front has no priority field", got.Priority)
	}
	if got.CreatedAt.Unix() != 1788000000 || got.UpdatedAt.Unix() != 1788001500 {
		t.Errorf("timestamps = %v / %v", got.CreatedAt, got.UpdatedAt)
	}
	if w := c.WarningsFor("cnv_1"); w != nil {
		t.Errorf("warnings = %v, want none", w)
	}
}

// TestGet_FallsBackToWaitingSince covers a conversation payload with no
// updated_at: waiting_since stands in, because an empty UpdatedAt reads as
// "never touched".
func TestGet_FallsBackToWaitingSince(t *testing.T) {
	as := newAPIServer(t)
	c := newClient(t, map[string]string{apiHost: addrOf(as.Server)})

	got, err := c.Get(context.Background(), "cnv_2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UpdatedAt.Unix() != 1788009000 {
		t.Errorf("UpdatedAt = %v, want waiting_since", got.UpdatedAt)
	}
	if got.Contact != "+15551234567" {
		t.Errorf("Contact = %q, want the bare handle when Front resolved no name", got.Contact)
	}
	if got.Fields["isPrivate"] != "true" {
		t.Errorf("isPrivate = %q", got.Fields["isPrivate"])
	}
}

// --- Threads ---

// TestThreads_MergesFeedsOrderAndRoles is the table-driven mapping test: it
// walks both Front feeds merged into one thread and pins the byline, the
// role and the text of every entry.
func TestThreads_MergesFeedsOrderAndRoles(t *testing.T) {
	as := newAPIServer(t)
	c := newClient(t, map[string]string{apiHost: addrOf(as.Server)})

	th, err := c.Threads(context.Background(), "cnv_1")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	want := []struct {
		author string
		role   ticket.Role
		text   string
		atts   []string
	}{
		{"Ada Lovelace", ticket.RoleCustomer, "Payments are failing on **checkout**.", []string{"fil_trace"}},
		{"Grace Hopper", ticket.RoleAgent, "Looking into it now.", nil},
		{"Grace Hopper (internal)", ticket.RoleAgent, "Card processor is rate limiting us.", []string{"fil_note"}},
		{"office-hours", ticket.RoleSystem, "Auto-reply: we are outside office hours.", nil},
		{"Ada Lovelace", ticket.RoleCustomer, "Here is the screenshot.", []string{"fil_shot", "fil_evil"}},
	}
	if len(th) != len(want) {
		t.Fatalf("len(thread) = %d, want %d: %+v", len(th), len(want), th)
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
		if strings.Join(th[i].AttachmentIDs, ",") != strings.Join(w.atts, ",") {
			t.Errorf("message %d attachment ids = %v, want %v", i, th[i].AttachmentIDs, w.atts)
		}
	}
	// The draft on page 1 never reaches the thread: an unsent reply is not
	// part of the conversation that happened.
	for _, m := range th {
		if strings.Contains(m.Text, "never sent") {
			t.Errorf("draft message leaked into the thread: %+v", m)
		}
	}
	// The comment is timestamped with a fractional second; it has to land
	// between the reply before it and the auto-reply after it.
	if !th[1].At.Before(th[2].At) || !th[2].At.Before(th[3].At) {
		t.Errorf("merged order is wrong: %v / %v / %v", th[1].At, th[2].At, th[3].At)
	}
	if th[2].At.Nanosecond() == 0 {
		t.Errorf("fractional posted_at was truncated: %v", th[2].At)
	}
	if w := c.WarningsFor("cnv_1"); w != nil {
		t.Errorf("warnings = %v, want none", w)
	}
	// Page 1 said there was a page 2, and it was followed.
	if got := as.count("/conversations/cnv_1/messages"); got != 2 {
		t.Errorf("message page fetches = %d, want 2", got)
	}
}

func TestMessageRole(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  frMessage
		want ticket.Role
	}{
		{"inbound is the customer", frMessage{IsInbound: true}, ticket.RoleCustomer},
		{"outbound teammate is an agent", frMessage{Author: &frTeammate{Type: "user"}}, ticket.RoleAgent},
		{"outbound rule is system", frMessage{Author: &frTeammate{Type: "rule"}}, ticket.RoleSystem},
		{"outbound api is system", frMessage{Author: &frTeammate{Type: "API"}}, ticket.RoleSystem},
		{"outbound csat is system", frMessage{Author: &frTeammate{Type: "smart_csat"}}, ticket.RoleSystem},
		{"outbound with no author is an agent", frMessage{}, ticket.RoleAgent},
		// An unrecognised author type is staff, not the customer: a
		// message wrongly attributed to the customer reads as the
		// customer's own words.
		{"outbound unknown type is an agent", frMessage{Author: &frTeammate{Type: "something_new"}}, ticket.RoleAgent},
		// "visitor" on an outbound message is still outbound: the team
		// sent it, so it is never the customer's words.
		{"outbound visitor is an agent", frMessage{Author: &frTeammate{Type: "visitor"}}, ticket.RoleAgent},
		{"inbound beats an automation author", frMessage{IsInbound: true, Author: &frTeammate{Type: "rule"}}, ticket.RoleCustomer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := messageRole(tc.msg); got != tc.want {
				t.Errorf("messageRole = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDraftDetection(t *testing.T) {
	shared := "shared"
	blank := ""
	for _, tc := range []struct {
		name string
		msg  frMessage
		want bool
	}{
		{"sent", frMessage{}, false},
		{"draft_mode set", frMessage{DraftMode: &shared}, true},
		{"draft_mode empty string", frMessage{DraftMode: &blank}, false},
		{"legacy is_draft", frMessage{IsDraft: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.msg.draft(); got != tc.want {
				t.Errorf("draft() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBodyText(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"html becomes markdown", "<p>Hello <strong>there</strong></p>", "Hello **there**"},
		// A genuinely plain body keeps its line breaks: the HTML
		// converter would collapse them the way a browser does.
		{"plain text keeps its lines", "line one\nline two", "line one\nline two"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bodyText(tc.in); got != tc.want {
				t.Errorf("bodyText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// --- pagination ---

// pageServer serves one message page per request, with a next link the test
// controls, so the page cap and the trust check on a next link can be
// exercised without a 100-page fixture.
func pageServer(t *testing.T, next func(page int) string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`{"_pagination":{"next":null},"_results":[]}`))
		case strings.HasSuffix(r.URL.Path, "/messages"):
			mu.Lock()
			page++
			n := page
			mu.Unlock()
			body, _ := json.Marshal(map[string]any{
				"_pagination": map[string]any{"next": next(n)},
				"_results": []map[string]any{{
					"id":         fmt.Sprintf("msg_p%d", n),
					"is_inbound": true,
					"created_at": 1788000000 + n,
					"text":       fmt.Sprintf("page %d", n),
					"recipients": []map[string]any{{"handle": "ada@example.com", "role": "from"}},
				}},
			})
			_, _ = w.Write(body)
		default:
			writeFixture(t, w, "conversation.json")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestThreads_PageCapWarns proves an endlessly paginating feed stops at the
// cap and says so, rather than sweeping without bound or stopping silently.
func TestThreads_PageCapWarns(t *testing.T) {
	srv := pageServer(t, func(page int) string {
		return fmt.Sprintf("https://%s/conversations/cnv_1/messages?page_token=P%d", apiHost, page+1)
	})
	c := newClient(t, map[string]string{apiHost: addrOf(srv)})

	old := maxFeedPages
	maxFeedPages = 3
	defer func() { maxFeedPages = old }()

	th, err := c.Threads(context.Background(), "cnv_1")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 3 {
		t.Fatalf("len(thread) = %d, want 3 (one per page up to the cap)", len(th))
	}
	warnings := c.WarningsFor("cnv_1")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "messages pages capped at 3") {
		t.Fatalf("warnings = %v, want one naming the cap", warnings)
	}
}

// TestThreads_UntrustedNextLinkRefused covers a `_pagination.next` pointing
// off Front: the link arrives inside a response body, so it is input. It
// must stop pagination with a warning naming the host alone, never be
// fetched.
func TestThreads_UntrustedNextLinkRefused(t *testing.T) {
	evil := newHitCounter(t)
	srv := pageServer(t, func(page int) string {
		if page == 1 {
			return "https://" + evilHost + "/conversations/cnv_1/messages?page_token=steal&secret=abc123"
		}
		return ""
	})
	c := newClient(t, map[string]string{apiHost: addrOf(srv), evilHost: addrOf(evil.Server)})

	th, err := c.Threads(context.Background(), "cnv_1")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 1 {
		t.Fatalf("len(thread) = %d, want 1", len(th))
	}
	if got := evil.count(); got != 0 {
		t.Errorf("untrusted next link was fetched %d times, want 0", got)
	}
	warnings := c.WarningsFor("cnv_1")
	if len(warnings) != 1 || !strings.Contains(warnings[0], evilHost) {
		t.Fatalf("warnings = %v, want one naming the untrusted host", warnings)
	}
	if strings.Contains(warnings[0], "secret") || strings.Contains(warnings[0], "?") {
		t.Errorf("warning repeats the refused link's query: %q", warnings[0])
	}
}

// TestThreads_SelfReferentialNextLinkStops covers a feed whose next link
// points at the page just read, which would otherwise loop for ever.
func TestThreads_SelfReferentialNextLinkStops(t *testing.T) {
	first := fmt.Sprintf("https://%s/conversations/cnv_1/messages?limit=%d", apiHost, pageSize)
	srv := pageServer(t, func(page int) string { return first })
	c := newClient(t, map[string]string{apiHost: addrOf(srv)})

	th, err := c.Threads(context.Background(), "cnv_1")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 1 {
		t.Fatalf("len(thread) = %d, want 1", len(th))
	}
	warnings := c.WarningsFor("cnv_1")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "self-referential") {
		t.Fatalf("warnings = %v, want one naming the self-referential link", warnings)
	}
}

// TestThreads_CompanyHostNextLinkFollowed covers the per-company API host
// Front's own examples show in `_links`: it is Front-operated, so a next
// link pointing at it is followed rather than refused.
func TestThreads_CompanyHostNextLinkFollowed(t *testing.T) {
	srv := pageServer(t, func(page int) string {
		if page == 1 {
			return "https://" + companyHost + "/conversations/cnv_1/messages?page_token=P2"
		}
		return ""
	})
	c := newClient(t, map[string]string{apiHost: addrOf(srv), companyHost: addrOf(srv)})

	th, err := c.Threads(context.Background(), "cnv_1")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 2 {
		t.Fatalf("len(thread) = %d, want 2 (the company-host page was followed)", len(th))
	}
	if w := c.WarningsFor("cnv_1"); w != nil {
		t.Errorf("warnings = %v, want none", w)
	}
}

// --- Attachments ---

func TestAttachments_DownloadSanitizeAndTrust(t *testing.T) {
	as := newAPIServer(t)
	evil := newHitCounter(t)
	c := newClient(t, fullRoutes(as, evil))

	dir := filepath.Join(t.TempDir(), "TCK-1")
	atts, err := c.Attachments(context.Background(), "cnv_1", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	want := []ticket.Attachment{
		{ID: "fil_trace", Name: "trace.log", MIME: "text/plain", Path: "TCK-1/1-trace.log"},
		{ID: "fil_note", Name: "fil_note", MIME: "text/plain", Path: "TCK-1/2-fil_note"},
		{ID: "fil_shot", Name: "shot.png", MIME: "image/png", Path: "TCK-1/3-shot.png"},
	}
	if len(atts) != len(want) {
		t.Fatalf("len(attachments) = %d, want %d: %+v", len(atts), len(want), atts)
	}
	for i, w := range want {
		if atts[i] != w {
			t.Errorf("attachment %d = %+v, want %+v", i, atts[i], w)
		}
	}
	body, err := os.ReadFile(filepath.Join(dir, "3-shot.png"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(body) != attachmentBody {
		t.Errorf("downloaded body = %q", body)
	}
	// fil_note carried no url of its own; the documented download path was
	// built from its id instead.
	if got := as.count("/download/fil_note"); got != 1 {
		t.Errorf("/download/fil_note hits = %d, want 1", got)
	}
	// Front's download endpoint is authenticated like every other call, so
	// every download must have carried the bearer token.
	if got := as.unauthenticated(); len(got) != 0 {
		t.Errorf("requests without the bearer header: %v", got)
	}
	if got := evil.count(); got != 0 {
		t.Errorf("untrusted attachment host was called %d times, want 0", got)
	}

	warnings := c.WarningsFor("cnv_1")
	if len(warnings) != 1 || !strings.Contains(warnings[0], evilHost) {
		t.Fatalf("warnings = %v, want one naming the untrusted attachment host", warnings)
	}
	if got := c.WarningsFor("cnv_1"); got != nil {
		t.Errorf("warnings after read = %v, want nil", got)
	}
}

// TestAttachments_RedirectOffFrontRefused covers a download answered with a
// redirect off the trusted hosts. The hop must be refused before the
// request is made, and the warning must name the host alone: Go wraps a
// CheckRedirect failure in a *url.Error carrying the full target, and a
// login redirect routinely carries a token in its query.
func TestAttachments_RedirectOffFrontRefused(t *testing.T) {
	as := newAPIServer(t)
	evil := newHitCounter(t)
	as.setRedirect("/download/fil_trace", "https://"+evilHost+"/steal?token=abc123")
	c := newClient(t, fullRoutes(as, evil))

	dir := filepath.Join(t.TempDir(), "TCK-1")
	atts, err := c.Attachments(context.Background(), "cnv_1", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	for _, a := range atts {
		if a.ID == "fil_trace" {
			t.Fatalf("the redirected attachment came back anyway: %+v", a)
		}
	}
	if got := evil.count(); got != 0 {
		t.Errorf("redirect target was called %d times, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "1-trace.log")); !os.IsNotExist(err) {
		t.Errorf("the refused download left a file on disk")
	}

	var redirectWarning string
	for _, w := range c.WarningsFor("cnv_1") {
		if strings.Contains(w, "redirect to untrusted host") {
			redirectWarning = w
		}
	}
	if redirectWarning == "" || !strings.Contains(redirectWarning, evilHost) {
		t.Fatalf("want a warning naming the refused redirect host, got %q", redirectWarning)
	}
	if strings.Contains(redirectWarning, "token") || strings.Contains(redirectWarning, "?") {
		t.Errorf("warning carries the redirect target's query: %q", redirectWarning)
	}
}

func TestAttachments_OversizeIsRefusedAndNotLeftOnDisk(t *testing.T) {
	as := newAPIServer(t)
	evil := newHitCounter(t)
	c := newClient(t, fullRoutes(as, evil))

	old := maxAttachmentBytes
	maxAttachmentBytes = 4
	defer func() { maxAttachmentBytes = old }()

	dir := filepath.Join(t.TempDir(), "TCK-1")
	atts, err := c.Attachments(context.Background(), "cnv_1", dir)
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

func TestAttachments_None(t *testing.T) {
	as := newAPIServer(t)
	c := newClient(t, map[string]string{apiHost: addrOf(as.Server)})

	atts, err := c.Attachments(context.Background(), "cnv_2", filepath.Join(t.TempDir(), "TCK-2"))
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 0 {
		t.Errorf("attachments = %+v, want none", atts)
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
		if got := httpx.SanitizeName(tc.in); got != tc.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- trust ---

func TestTrust(t *testing.T) {
	c, err := New(Config{Token: "t"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range []struct {
		raw             string
		fetch, sendCred bool
	}{
		{"https://api2.frontapp.com/conversations/cnv_1", true, true},
		{"https://API2.FrontApp.com.:443/conversations/cnv_1", true, true},
		{"https://api2.frontapp.com/download/fil_1", true, true},
		{"https://acme.api.frontapp.com/download/fil_1", true, true},
		{"https://api.frontapp.com/download/fil_1", true, true},
		// Front-branded but outside the API host family, so not a place
		// this token goes.
		{"https://app.frontapp.com/open/cnv_1", false, false},
		{"https://frontapp.com/x", false, false},
		{"http://api2.frontapp.com/x", false, false},
		{"https://api2.frontapp.com@attacker.example/x", false, false},
		{"https://api.frontapp.com.evil.example/x", false, false},
		{"https://files.evil.example/x", false, false},
	} {
		u, perr := url.Parse(tc.raw)
		if perr != nil {
			t.Fatalf("parse %q: %v", tc.raw, perr)
		}
		fetch, sendCred, _ := c.trust.Check(u)
		if fetch != tc.fetch || sendCred != tc.sendCred {
			t.Errorf("Check(%q) = (%v, %v), want (%v, %v)", tc.raw, fetch, sendCred, tc.fetch, tc.sendCred)
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
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range tc.header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"_error":{"status":0,"title":"nope"}}`))
		}))
		c := newClient(t, map[string]string{apiHost: addrOf(srv)})

		_, err := c.Get(context.Background(), "cnv_1")
		if err == nil {
			t.Fatalf("status %d: want error", tc.status)
		}
		if code := sourceCode(t, err); code != tc.want {
			t.Errorf("status %d: code = %q, want %q", tc.status, code, tc.want)
		}
		srv.Close()
	}
}

func TestRetryAfter_HonouredOnce(t *testing.T) {
	var mu sync.Mutex
	var calls int
	as := newAPIServer(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		as.Config.Handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	c := newClient(t, map[string]string{apiHost: addrOf(srv)})

	if _, err := c.Get(context.Background(), "cnv_1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one 429 honoured, then the real answer)", calls)
	}
}

func TestErrorMessages_NeverContainTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"_error":{"status":500,"title":"boom"}}`))
	}))
	defer srv.Close()
	c := newClient(t, map[string]string{apiHost: addrOf(srv)})

	_, err := c.Get(context.Background(), "cnv_1")
	if err == nil {
		t.Fatal("Get: want error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error repeats the token: %s", err)
	}
}

// TestLogPath drops the query string, so an opaque page token never reaches
// a log line or a warning.
func TestLogPath(t *testing.T) {
	got := logPath("https://api2.frontapp.com/conversations/cnv_1/messages?page_token=secret")
	if got != "/conversations/cnv_1/messages" {
		t.Errorf("logPath = %q", got)
	}
	if strings.Contains(got, "secret") {
		t.Errorf("logPath kept the query: %q", got)
	}
}
