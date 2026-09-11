package servicenow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

const (
	testUser     = "sirdar.integration"
	testPassword = "pw-secret-abc123"
	testOAuth    = "oauth-secret-xyz789"
	testSysID    = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	testNumber   = "INC0010023"
)

func wantBasicHeader() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(testUser+":"+testPassword))
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

// fixture returns a fixture with {{base}} replaced by the test server's
// own URL, which is how a download_link can point at the instance under
// test.
func fixture(t *testing.T, name, base string) []byte {
	t.Helper()
	return []byte(strings.ReplaceAll(string(mustRead(t, name)), "{{base}}", base))
}

// listEnvelope wraps a single-record fixture as the Table API's list form,
// which is what a lookup by number gets back.
func listEnvelope(t *testing.T, single []byte) []byte {
	t.Helper()
	var one singleResponse
	if err := json.Unmarshal(single, &one); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	out, err := json.Marshal(map[string]any{"result": []record{one.Result}})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return out
}

// newTestClient starts a server and returns a client pointed at it with
// basic auth.
func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{Instance: "acme", BaseURL: srv.URL, Username: testUser, Password: testPassword}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

// --- New / config validation ---

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr source.Code
	}{
		{"basic and bearer", Config{Instance: "acme", Username: testUser, Password: testPassword, OAuthToken: testOAuth}, source.Auth},
		{"username without password", Config{Instance: "acme", Username: testUser}, source.Auth},
		{"password without username", Config{Instance: "acme", Password: testPassword}, source.Auth},
		{"no credentials", Config{Instance: "acme"}, source.Auth},
		{"no instance", Config{Username: testUser, Password: testPassword}, source.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg, nil)
			var serr *source.Error
			if !errors.As(err, &serr) {
				t.Fatalf("New() error = %v, want a *source.Error", err)
			}
			if serr.Code != tt.wantErr {
				t.Errorf("New() code = %q, want %q", serr.Code, tt.wantErr)
			}
		})
	}
}

func TestNewAuthModes(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		c, err := New(Config{Instance: "acme", Username: testUser, Password: testPassword}, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.authHeader != wantBasicHeader() {
			t.Errorf("authHeader = %q, want the basic header", c.authHeader)
		}
		if c.authWho != testUser {
			t.Errorf("authWho = %q, want %q", c.authWho, testUser)
		}
	})
	t.Run("bearer", func(t *testing.T) {
		c, err := New(Config{Instance: "acme", OAuthToken: testOAuth}, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.authHeader != "Bearer "+testOAuth {
			t.Errorf("authHeader = %q, want the bearer header", c.authHeader)
		}
		if strings.Contains(c.authWho, testOAuth) {
			t.Errorf("authWho leaks the token: %q", c.authWho)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		c, err := New(Config{Instance: "acme", OAuthToken: testOAuth}, nil)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if c.baseURL != "https://acme.service-now.com" {
			t.Errorf("baseURL = %q", c.baseURL)
		}
		if c.table != DefaultTable {
			t.Errorf("table = %q, want %q", c.table, DefaultTable)
		}
		if c.hc == nil || c.hc.Timeout != 30*time.Second {
			t.Fatalf("expected a default 30s client, got %+v", c.hc)
		}
	})
}

func TestResolveBaseURL(t *testing.T) {
	tests := []struct{ instance, base, want string }{
		{"acme", "", "https://acme.service-now.com"},
		{"acme.service-now.com", "", "https://acme.service-now.com"},
		{"acme", "https://proxy.example.com/", "https://proxy.example.com"},
		{"", "", ""},
		{"https://acme.service-now.com", "", "https://acme.service-now.com"},
	}
	for _, tt := range tests {
		if got := ResolveBaseURL(tt.instance, tt.base); got != tt.want {
			t.Errorf("ResolveBaseURL(%q, %q) = %q, want %q", tt.instance, tt.base, got, tt.want)
		}
	}
}

// --- auth on the wire, both modes ---

func TestAuthHeaderReachesTheWire(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"basic", Config{Instance: "acme", Username: testUser, Password: testPassword}, wantBasicHeader()},
		{"bearer", Config{Instance: "acme", OAuthToken: testOAuth}, "Bearer " + testOAuth},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				w.Write([]byte(`{"result":[]}`))
			}))
			defer srv.Close()

			cfg := tt.cfg
			cfg.BaseURL = srv.URL
			c, err := New(cfg, srv.Client())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := c.Ping(context.Background()); err != nil {
				t.Fatalf("Ping: %v", err)
			}
			if got != tt.want {
				t.Errorf("Authorization = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Get ---

func TestGetBySysID(t *testing.T) {
	var gotPath, gotDisplay, gotFields string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotDisplay = r.URL.Query().Get("sysparm_display_value")
		gotFields = r.URL.Query().Get("sysparm_fields")
		w.Write(mustRead(t, "incident.json"))
	})

	hd, err := c.Get(context.Background(), testSysID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotPath != "/api/now/table/incident/"+testSysID {
		t.Errorf("path = %q", gotPath)
	}
	if gotDisplay != "true" {
		t.Errorf("sysparm_display_value = %q, want true", gotDisplay)
	}
	if !strings.Contains(gotFields, "caller_id.user_name") {
		t.Errorf("sysparm_fields does not dot-walk the caller's login name: %q", gotFields)
	}
	if hd.ID != testNumber {
		t.Errorf("ID = %q, want %q", hd.ID, testNumber)
	}
	if hd.Subject != "Export to CSV fails" {
		t.Errorf("Subject = %q", hd.Subject)
	}
	if hd.Status != "In Progress" || hd.Priority != "2 - High" || hd.Channel != "Email" {
		t.Errorf("status/priority/channel = %q/%q/%q", hd.Status, hd.Priority, hd.Channel)
	}
	if hd.Contact != "Abel Tuter" {
		t.Errorf("Contact = %q", hd.Contact)
	}
	// company arrives in the {display_value,value} shape, which the lazy
	// decoder has to read as the display name.
	if hd.Customer != "ACME Corp" {
		t.Errorf("Customer = %q, want the display value", hd.Customer)
	}
	if hd.CustomerID != "31bea3d53790200044e0bfc8bcbe5dec" {
		t.Errorf("CustomerID = %q, want the dot-walked sys_id", hd.CustomerID)
	}
	if !strings.Contains(hd.URL, "nav_to.do") || !strings.Contains(hd.URL, testSysID) {
		t.Errorf("URL = %q, want the record's UI page", hd.URL)
	}
	if want := time.Date(2026, 9, 10, 8, 29, 0, 0, time.UTC); !hd.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %s, want %s", hd.CreatedAt, want)
	}
	if hd.Fields["number"] != testNumber || hd.Fields["callerEmail"] != "abel.tuter@example.com" {
		t.Errorf("Fields = %v", hd.Fields)
	}
}

func TestGetByNumberQueriesTheTable(t *testing.T) {
	var gotPath, gotQuery, gotLimit string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("sysparm_query")
		gotLimit = r.URL.Query().Get("sysparm_limit")
		w.Write(listEnvelope(t, mustRead(t, "incident.json")))
	})

	hd, err := c.Get(context.Background(), testNumber)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotPath != "/api/now/table/incident" {
		t.Errorf("path = %q, want the table list form", gotPath)
	}
	if gotQuery != "number="+testNumber {
		t.Errorf("sysparm_query = %q", gotQuery)
	}
	if gotLimit != "1" {
		t.Errorf("sysparm_limit = %q, want 1", gotLimit)
	}
	if hd.ID != testNumber {
		t.Errorf("ID = %q", hd.ID)
	}
}

func TestGetMissingRecordIsNotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":[]}`))
	})
	_, err := c.Get(context.Background(), testNumber)
	var serr *source.Error
	if !errors.As(err, &serr) || serr.Code != source.NotFound {
		t.Fatalf("Get() error = %v, want not_found", err)
	}
}

func TestStatusErrorNeverQuotesTheBodyOnAuthFailure(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"detail":"User Not Authenticated ` + testPassword + `"}}`))
	})
	_, err := c.Get(context.Background(), testSysID)
	var serr *source.Error
	if !errors.As(err, &serr) || serr.Code != source.Auth {
		t.Fatalf("error = %v, want auth", err)
	}
	if strings.Contains(serr.Message, testPassword) {
		t.Fatalf("the auth error quoted the response body: %q", serr.Message)
	}
}

// --- Tracker view ---

func TestTrackerGet(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(mustRead(t, "incident.json"))
	})
	tr, err := c.Tracker().Get(context.Background(), testSysID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if tr.Key != testNumber || tr.Title != "Export to CSV fails" {
		t.Errorf("key/title = %q/%q", tr.Key, tr.Title)
	}
	if tr.Assignee != "Beth Anglin" || tr.Status != "In Progress" {
		t.Errorf("assignee/status = %q/%q", tr.Assignee, tr.Status)
	}
	// The description arrives as HTML and has to reach the agent as text.
	if strings.Contains(tr.Description, "<p>") || !strings.Contains(tr.Description, "returns a 500") {
		t.Errorf("Description = %q, want the HTML converted", tr.Description)
	}
	if tr.HelpdeskRef != testNumber {
		t.Errorf("HelpdeskRef = %q, want the incident's own number", tr.HelpdeskRef)
	}
}

// --- Threads ---

func TestThreadsRolesAndOrder(t *testing.T) {
	var gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/now/table/sys_journal_field"):
			gotQuery = r.URL.Query().Get("sysparm_query")
			w.Write(mustRead(t, "journal.json"))
		default:
			w.Write(mustRead(t, "incident.json"))
		}
	})

	th, err := c.Threads(context.Background(), testSysID)
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if !strings.Contains(gotQuery, "element_id="+testSysID) || !strings.Contains(gotQuery, "elementINcomments,work_notes") {
		t.Errorf("sysparm_query = %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "ORDERBYsys_created_on") {
		t.Errorf("journal query is not ordered: %q", gotQuery)
	}
	// The description, two comments and one work note; the blank comment
	// is dropped.
	if len(th) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(th), th)
	}
	if th[0].Role != ticket.RoleCustomer || th[0].Author != "Abel Tuter" {
		t.Errorf("opening message = %+v, want the caller's own words", th[0])
	}
	if strings.Contains(th[0].Text, "<p>") {
		t.Errorf("opening message still carries HTML: %q", th[0].Text)
	}
	if th[1].Role != ticket.RoleCustomer || th[1].Author != "abel.tuter" {
		t.Errorf("caller comment = %+v, want role customer", th[1])
	}
	if th[2].Role != ticket.RoleAgent || !strings.HasSuffix(th[2].Author, "(internal)") {
		t.Errorf("work note = %+v, want an internal agent message", th[2])
	}
	if th[3].Role != ticket.RoleAgent || th[3].Author != "beth.anglin" {
		t.Errorf("agent comment = %+v, want role agent", th[3])
	}
	for i := 1; i < len(th); i++ {
		if th[i].At.Before(th[i-1].At) {
			t.Fatalf("thread is out of order at %d: %s before %s", i, th[i].At, th[i-1].At)
		}
	}
}

func TestThreadsJournalForbiddenWarnsRatherThanFails(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/now/table/sys_journal_field") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write(mustRead(t, "incident.json"))
	})

	th, err := c.Threads(context.Background(), testSysID)
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 1 {
		t.Fatalf("want the record's own description to survive, got %d messages", len(th))
	}
	warnings := c.WarningsFor(testSysID)
	if len(warnings) == 0 || !strings.Contains(warnings[0], "journal entries unavailable") {
		t.Fatalf("warnings = %v, want one naming the unreadable journal", warnings)
	}
}

// TestThreadsPageCap proves an instance that keeps handing back full pages
// stops at the cap and says so, rather than paging without bound.
func TestThreadsPageCap(t *testing.T) {
	var pages int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/now/table/sys_journal_field") {
			w.Write(mustRead(t, "incident.json"))
			return
		}
		atomic.AddInt32(&pages, 1)
		offset, _ := strconv.Atoi(r.URL.Query().Get("sysparm_offset"))
		rows := make([]map[string]string, 0, journalPageSize)
		for i := 0; i < journalPageSize; i++ {
			rows = append(rows, map[string]string{
				"sys_id":         fmt.Sprintf("j%d", offset+i),
				"element":        "comments",
				"element_id":     testSysID,
				"value":          fmt.Sprintf("entry %d", offset+i),
				"sys_created_on": "2026-09-10 09:00:00",
				"sys_created_by": "abel.tuter",
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"result": rows})
	})

	th, err := c.Threads(context.Background(), testSysID)
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if got := int(atomic.LoadInt32(&pages)); got != maxJournalPages {
		t.Fatalf("fetched %d journal pages, want the %d-page cap", got, maxJournalPages)
	}
	if len(th) != maxJournalPages*journalPageSize+1 {
		t.Fatalf("got %d messages", len(th))
	}
	warnings := c.WarningsFor(testSysID)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "page cap") {
		t.Fatalf("warnings = %v, want one naming the page cap", warnings)
	}
}

// --- Attachments ---

func TestAttachments(t *testing.T) {
	var srvURL string
	var mu sync.Mutex
	var downloaded []string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/now/attachment":
			w.Write(fixture(t, "attachments.json", srvURL))
		case strings.HasPrefix(r.URL.Path, "/api/now/attachment/"):
			mu.Lock()
			downloaded = append(downloaded, r.URL.Path)
			mu.Unlock()
			if r.Header.Get("Authorization") == "" {
				t.Error("attachment download went out without the credential")
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte("file-bytes"))
		default:
			w.Write(mustRead(t, "incident.json"))
		}
	})
	srvURL = srv.URL

	dir := filepath.Join(t.TempDir(), "attachments")
	atts, err := c.Attachments(context.Background(), testSysID, dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("got %d attachments, want 2: %+v", len(atts), atts)
	}
	if atts[0].Name != "screenshot.png" || atts[0].MIME != "image/png" {
		t.Errorf("first attachment = %+v", atts[0])
	}
	if atts[0].Path != "attachments/1-screenshot.png" {
		t.Errorf("path = %q", atts[0].Path)
	}
	// A traversal-shaped filename must not escape the directory.
	if strings.Contains(atts[1].Path, "..") {
		t.Fatalf("traversal name survived sanitising: %q", atts[1].Path)
	}
	for _, a := range atts {
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), a.Path)); err != nil {
			t.Errorf("attachment not on disk: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	// The second row carries no download_link, so the URL is derived from
	// the attachment's own sys_id.
	if len(downloaded) != 2 || downloaded[1] != "/api/now/attachment/att2/file" {
		t.Errorf("downloads = %v", downloaded)
	}
}

func TestAttachmentsForeignHostIsRefused(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/now/attachment" {
			w.Write(mustRead(t, "attachments_foreign.json"))
			return
		}
		w.Write(mustRead(t, "incident.json"))
	})

	dir := filepath.Join(t.TempDir(), "attachments")
	_, err := c.Attachments(context.Background(), testSysID, dir)
	if err == nil {
		t.Fatal("want an error when every attachment is on an untrusted host")
	}
	if !strings.Contains(err.Error(), "attacker.example.com") {
		t.Errorf("error should name the host: %v", err)
	}
	if strings.Contains(err.Error(), "leak-me") {
		t.Errorf("error quoted the attacker-chosen URL: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused attachment left %d files on disk", len(entries))
	}
}

// TestAttachmentRedirectOffHostIsRefused covers the SSO case: an instance
// that answers an attachment request with a redirect to a login host must
// not have that followed, and the file must not be written.
func TestAttachmentRedirectOffHostIsRefused(t *testing.T) {
	var srvURL string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/now/attachment":
			w.Write(fixture(t, "attachments.json", srvURL))
		case strings.HasPrefix(r.URL.Path, "/api/now/attachment/"):
			http.Redirect(w, r, "https://sso.attacker.example.com/login?RelayState=secret-relay", http.StatusFound)
		default:
			w.Write(mustRead(t, "incident.json"))
		}
	})
	srvURL = srv.URL

	dir := filepath.Join(t.TempDir(), "attachments")
	_, err := c.Attachments(context.Background(), testSysID, dir)
	if err == nil {
		t.Fatal("want an error when every download is redirected off the instance")
	}
	if !strings.Contains(err.Error(), "sso.attacker.example.com") {
		t.Errorf("error should name the redirect target's host: %v", err)
	}
	if strings.Contains(err.Error(), "RelayState") || strings.Contains(err.Error(), "secret-relay") {
		t.Errorf("error quoted the redirect's query string: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused download left %d files on disk", len(entries))
	}
}

func TestAttachmentHTMLBodyIsRefused(t *testing.T) {
	var srvURL string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/now/attachment":
			w.Write(fixture(t, "attachments.json", srvURL))
		case strings.HasPrefix(r.URL.Path, "/api/now/attachment/"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte("<html><body>sign in</body></html>"))
		default:
			w.Write(mustRead(t, "incident.json"))
		}
	})
	srvURL = srv.URL

	dir := filepath.Join(t.TempDir(), "attachments")
	if _, err := c.Attachments(context.Background(), testSysID, dir); err == nil {
		t.Fatal("want an error when the instance serves a login page instead of the file")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a login page was written to disk as an attachment")
	}
}

// --- List ---

func TestListPagesAndCapsTheLimit(t *testing.T) {
	var mu sync.Mutex
	var queries []url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query())
		offset, _ := strconv.Atoi(r.URL.Query().Get("sysparm_offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("sysparm_limit"))
		mu.Unlock()

		rows := make([]map[string]string, 0, limit)
		for i := 0; i < limit; i++ {
			rows = append(rows, map[string]string{
				"sys_id":            fmt.Sprintf("%032d", offset+i),
				"number":            fmt.Sprintf("INC%07d", offset+i),
				"short_description": "row",
				"state":             "New",
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"result": rows})
	})

	got, err := c.Tracker().List(context.Background(), source.ListFilter{Limit: 500})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != maxListResults {
		t.Fatalf("got %d records, want the %d cap", len(got), maxListResults)
	}
	warnings := c.WarningsFor("")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "capped at 200") {
		t.Fatalf("warnings = %v, want one naming the cap", warnings)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 2 {
		t.Fatalf("made %d requests, want 2 pages of %d", len(queries), listPageSize)
	}
	if queries[1].Get("sysparm_offset") != strconv.Itoa(listPageSize) {
		t.Errorf("second page offset = %q", queries[1].Get("sysparm_offset"))
	}
	if q := queries[0].Get("sysparm_query"); !strings.Contains(q, "active=true") || !strings.Contains(q, "ORDERBYDESCsys_updated_on") {
		t.Errorf("sysparm_query = %q", q)
	}
}

func TestListFilterDropsTheQuerySeparator(t *testing.T) {
	var gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("sysparm_query")
		w.Write([]byte(`{"result":[]}`))
	})

	if _, err := c.Tracker().List(context.Background(), source.ListFilter{Assignee: "beth^state=7"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if strings.Contains(gotQuery, "beth^state=7") {
		t.Fatalf("a filter value smuggled a condition into the query: %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "assigned_to.user_name=bethstate=7") {
		t.Errorf("sysparm_query = %q", gotQuery)
	}
	warnings := c.WarningsFor("")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "separator") {
		t.Fatalf("warnings = %v, want one naming the dropped separator", warnings)
	}
}

func TestListAssigneeMe(t *testing.T) {
	var gotQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("sysparm_query")
		w.Write([]byte(`{"result":[]}`))
	})
	if _, err := c.Tracker().List(context.Background(), source.ListFilter{Assignee: "me", Status: "2"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(gotQuery, "assigned_to=javascript:gs.getUserID()") {
		t.Errorf("sysparm_query = %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "state=2") || strings.Contains(gotQuery, "active=true") {
		t.Errorf("an explicit status must replace the open-ish default: %q", gotQuery)
	}
}

// --- transport ---

func TestRateLimitRetriesOnceThenReports(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write(mustRead(t, "incident.json"))
	})
	if _, err := c.Get(context.Background(), testSysID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("made %d calls, want a retry after Retry-After", calls)
	}
}

func TestRateLimitWithoutRetryAfterIsReported(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.Get(context.Background(), testSysID)
	var serr *source.Error
	if !errors.As(err, &serr) || serr.Code != source.RateLimited {
		t.Fatalf("error = %v, want rate_limited", err)
	}
}

func TestOversizedBodyIsRefused(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"sys_id":"`))
		blob := strings.Repeat("a", 1<<20)
		for i := 0; i < 10; i++ {
			w.Write([]byte(blob))
		}
		w.Write([]byte(`"}}`))
	})
	_, err := c.Get(context.Background(), testSysID)
	var serr *source.Error
	if !errors.As(err, &serr) || serr.Code != source.Internal {
		t.Fatalf("error = %v, want internal", err)
	}
	if !strings.Contains(serr.Message, "read body") {
		t.Errorf("error = %v, want the body ceiling", serr)
	}
}

func TestIsSysID(t *testing.T) {
	if !isSysID(testSysID) {
		t.Error("a 32-hex string is a sys_id")
	}
	for _, s := range []string{"", testNumber, strings.Repeat("z", 32), testSysID + "0"} {
		if isSysID(s) {
			t.Errorf("%q is not a sys_id", s)
		}
	}
}
