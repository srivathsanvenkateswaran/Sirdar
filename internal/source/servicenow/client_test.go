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

// newTestClient starts a TLS server — New refuses a non-https base URL,
// same as it refuses one for a live instance — and returns a client
// pointed at it with basic auth, using the server's own client so its
// self-signed certificate is trusted.
func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
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
		got, err := ResolveBaseURL(tt.instance, tt.base)
		if err != nil {
			t.Errorf("ResolveBaseURL(%q, %q) unexpected error: %v", tt.instance, tt.base, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ResolveBaseURL(%q, %q) = %q, want %q", tt.instance, tt.base, got, tt.want)
		}
	}
}

// TestResolveBaseURLRejectsHostileForms is the round-1 finding: every
// credentialed request goes to exactly the host ResolveBaseURL names, so a
// resolved URL carrying userinfo, a path, a query or a fragment, or a
// non-bare instance name, must be refused rather than silently used —
// otherwise "acme.service-now.com@evil.com" sends the live Authorization
// header to evil.com, and "acme/x" glues a path onto the service-now.com
// suffix as if it were a host.
func TestResolveBaseURLRejectsHostileForms(t *testing.T) {
	tests := []struct{ name, instance, base string }{
		{"userinfo smuggles the credential to another host", "acme.service-now.com@evil.com", ""},
		{"userinfo in an explicit scheme", "https://acme.service-now.com@evil.com", ""},
		{"userinfo in a baseUrl override", "acme", "https://user:pass@proxy.example.com"},
		{"bare instance carries a path separator", "acme/x", ""},
		{"bare instance carries a colon", "acme:8443", ""},
		{"resolved URL carries a path", "https://acme.service-now.com/api/now", ""},
		{"baseUrl carries a path", "acme", "https://proxy.example.com/table"},
		{"resolved URL carries a query", "https://acme.service-now.com?x=1", ""},
		{"resolved URL carries a fragment", "https://acme.service-now.com#frag", ""},
		{"non-https scheme", "http://acme.service-now.com", ""},
		{"baseUrl is non-https", "acme", "http://proxy.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveBaseURL(tt.instance, tt.base)
			if err == nil {
				t.Fatalf("ResolveBaseURL(%q, %q) = %q, want an error", tt.instance, tt.base, got)
			}
		})
	}
}

// TestNewRejectsHostileInstance proves the same protection holds where a
// client is actually built, not only inside ResolveBaseURL's own tests:
// New must refuse a credentialed client rather than build one that would
// send the configured password to a host the operator did not name.
func TestNewRejectsHostileInstance(t *testing.T) {
	_, err := New(Config{Instance: "acme.service-now.com@evil.com", Username: testUser, Password: testPassword}, nil)
	if err == nil {
		t.Fatal("New with a userinfo-carrying instance must fail")
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
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// TestGetByNumberRejectsInvalidCharacters is the round-1 finding at
// client.go ~444: a record number carrying the encoded-query separator
// "^" or the IN-list separator "," used to be silently stripped before the
// lookup, which risks matching a different record than the one the caller
// typed. It must be rejected instead, naming the character, and the
// request must never reach the wire.
func TestGetByNumberRejectsInvalidCharacters(t *testing.T) {
	for _, tt := range []struct{ id, char string }{
		{"INC001^activeSELECT=true", "'^'"},
		{"INC001,INC002", "','"},
	} {
		t.Run(tt.id, func(t *testing.T) {
			var called bool
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.Write([]byte(`{"result":[]}`))
			})
			_, err := c.Get(context.Background(), tt.id)
			if err == nil {
				t.Fatal("want an error for a record id carrying an encoded-query control character")
			}
			if !strings.Contains(err.Error(), tt.char) {
				t.Errorf("error should name the invalid character %s: %v", tt.char, err)
			}
			if called {
				t.Error("an invalid id must not reach the wire")
			}
		})
	}
}

// TestFetchRecordIsCachedForTheClientsLifetime is the round-1 finding at
// client.go ~10 in the original report: Get, Threads and Attachments are
// called one after another for the same ticket, and without a cache each
// one re-fetches the identical record.
func TestFetchRecordIsCachedForTheClientsLifetime(t *testing.T) {
	var recordFetches int
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/now/table/sys_journal_field"):
			w.Write([]byte(`{"result":[]}`))
		case strings.HasPrefix(r.URL.Path, "/api/now/attachment"):
			w.Write([]byte(`{"result":[]}`))
		default:
			recordFetches++
			w.Write(mustRead(t, "incident.json"))
		}
	})

	if _, err := c.Get(context.Background(), testSysID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := c.Threads(context.Background(), testSysID); err != nil {
		t.Fatalf("Threads: %v", err)
	}
	dir := t.TempDir()
	if _, err := c.Attachments(context.Background(), testSysID, dir); err != nil {
		t.Fatalf("Attachments: %v", err)
	}

	if recordFetches != 1 {
		t.Errorf("record was fetched %d times across Get+Threads+Attachments, want 1", recordFetches)
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

// TestThreadsWorkNoteWithNoAuthorFallsBackToAgent is the round-1 finding at
// mapping.go ~266: a work note with an empty sys_created_by used to render
// as " (internal)" — no name before the suffix — which reads as a
// rendering bug rather than an anonymous internal note.
func TestThreadsWorkNoteWithNoAuthorFallsBackToAgent(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/now/table/sys_journal_field"):
			w.Write([]byte(`{"result":[
				{"sys_id":"j1","element":"work_notes","element_id":"` + testSysID + `","value":"internal note","sys_created_on":"2026-09-10 09:00:00","sys_created_by":""}
			]}`))
		default:
			w.Write(mustRead(t, "incident.json"))
		}
	})

	th, err := c.Threads(context.Background(), testSysID)
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	var note *ticket.Message
	for i := range th {
		if th[i].Text == "internal note" {
			note = &th[i]
		}
	}
	if note == nil {
		t.Fatalf("the work note is missing from the thread: %+v", th)
	}
	if note.Author != "agent (internal)" {
		t.Errorf("author = %q, want %q", note.Author, "agent (internal)")
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

// TestDownloadHonoursSendCredential is the round-1 finding at
// attachments.go ~67: Attachments checked Trust.CheckRaw's fetch result
// before downloading but discarded sendCredential, so download always sent
// the Authorization header to any fetchable host. This adapter's own Trust
// never actually grants fetch without also granting sendCredential — it
// has no fetch-only CDN tier — but download itself has to honour the flag
// it is given, in case that ever changes.
func TestDownloadHonoursSendCredential(t *testing.T) {
	var gotAuth string
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		sawAuth = r.Header.Get("Authorization") != ""
		w.Write([]byte("file-bytes"))
	}))
	t.Cleanup(srv.Close)

	c := &Client{authHeader: wantBasicHeader(), hc: srv.Client()}

	dir := t.TempDir()
	if _, err := c.download(context.Background(), srv.URL+"/f", filepath.Join(dir, "f"), false); err != nil {
		t.Fatalf("download (sendCredential=false): %v", err)
	}
	if sawAuth {
		t.Errorf("Authorization header %q reached the wire despite sendCredential=false", gotAuth)
	}

	if _, err := c.download(context.Background(), srv.URL+"/f", filepath.Join(dir, "f2"), true); err != nil {
		t.Fatalf("download (sendCredential=true): %v", err)
	}
	if !sawAuth || gotAuth != wantBasicHeader() {
		t.Errorf("Authorization = %q, want it sent when sendCredential=true", gotAuth)
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

// TestJournalSweepTruncatesWhenTheRetryBudgetIsExhausted is the round-1
// finding at client.go ~331: each page's own 429 retry is already capped
// at maxRetryAfter, but a paginated sweep reading many pages could still
// spend pages * maxRetryAfter waiting in total. The first journal page
// here is retried once and succeeds; the retry budget is sized so the
// second page's own Retry-After would overspend it, so that page must not
// be retried at all — the sweep truncates with a warning naming the page,
// keeping what the first page already returned.
func TestJournalSweepTruncatesWhenTheRetryBudgetIsExhausted(t *testing.T) {
	orig := maxSweepRetryWait
	maxSweepRetryWait = 1500 * time.Millisecond
	t.Cleanup(func() { maxSweepRetryWait = orig })

	var mu sync.Mutex
	attempts := map[string]int{}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/now/table/sys_journal_field") {
			w.Write(mustRead(t, "incident.json"))
			return
		}
		offset := r.URL.Query().Get("sysparm_offset")
		mu.Lock()
		attempts[offset]++
		n := attempts[offset]
		mu.Unlock()

		if n == 1 {
			// Every page 429s once — the budget decides whether it gets
			// retried.
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if offset != "0" {
			t.Errorf("offset %s was retried; the budget should have refused it", offset)
			w.Write([]byte(`{"result":[]}`))
			return
		}
		var entries []string
		for i := 0; i < journalPageSize; i++ {
			entries = append(entries, fmt.Sprintf(
				`{"sys_id":"j%d","element":"comments","element_id":"%s","value":"note %d","sys_created_on":"2026-09-10 09:00:00","sys_created_by":"agent"}`,
				i, testSysID, i))
		}
		w.Write([]byte(`{"result":[` + strings.Join(entries, ",") + `]}`))
	})

	start := time.Now()
	th, err := c.Threads(context.Background(), testSysID)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("Threads took %s; the exhausted budget should stop the sweep well short of a second real wait", elapsed)
	}
	// The description, plus the one full journal page that got its retry.
	if len(th) != journalPageSize+1 {
		t.Fatalf("got %d messages, want %d (description + one full journal page)", len(th), journalPageSize+1)
	}
	warnings := c.WarningsFor(testSysID)
	var truncated bool
	for _, w := range warnings {
		if strings.Contains(w, "journal page 2") {
			truncated = true
		}
	}
	if !truncated {
		t.Errorf("warnings = %v, want one naming the truncated page", warnings)
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

// --- dateFormat / timestamp parsing ---

// TestParseTimeUnambiguousLayouts covers the round-1 additions to
// baseTimeLayouts — none of them need a dateFormat to read correctly.
func TestParseTimeUnambiguousLayouts(t *testing.T) {
	c, _ := New(Config{Instance: "acme", Username: testUser, Password: testPassword}, nil)
	tp := c.newTimeParser()
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2024-03-04 10:00:00", time.Date(2024, 3, 4, 10, 0, 0, 0, time.UTC)},
		{"2024/03/04", time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)},
		{"04.03.2024 10:00:00", time.Date(2024, 3, 4, 10, 0, 0, 0, time.UTC)},
		{"2024-03-04T10:00:00Z", time.Date(2024, 3, 4, 10, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		got := tp.parse(tt.in)
		if !got.Equal(tt.want) {
			t.Errorf("parse(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
	if tp.failed != "" {
		t.Errorf("unambiguous layouts must not fail: %q", tp.failed)
	}
}

// TestDateFormatResolvesTheAmbiguousDashedDate is the round-1 finding:
// "03-04-2024" is the 3rd of April read one way and the 4th of March the
// other, and dateFormat is what picks.
func TestDateFormatResolvesTheAmbiguousDashedDate(t *testing.T) {
	const ambiguous = "03-04-2024 10:00:00"

	unset, err := New(Config{Instance: "acme", Username: testUser, Password: testPassword}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := unset.newTimeParser().parse(ambiguous); !got.IsZero() {
		t.Errorf("with no dateFormat, a dashed date must not parse: got %s", got)
	}

	mdy, err := New(Config{Instance: "acme", Username: testUser, Password: testPassword, DateFormat: DateFormatMDY}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if want := time.Date(2024, 3, 4, 10, 0, 0, 0, time.UTC); !mdy.newTimeParser().parse(ambiguous).Equal(want) {
		t.Errorf("mdy: parse(%q) = %s, want %s (March 4th)", ambiguous, mdy.newTimeParser().parse(ambiguous), want)
	}

	dmy, err := New(Config{Instance: "acme", Username: testUser, Password: testPassword, DateFormat: DateFormatDMY}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if want := time.Date(2024, 4, 3, 10, 0, 0, 0, time.UTC); !dmy.newTimeParser().parse(ambiguous).Equal(want) {
		t.Errorf("dmy: parse(%q) = %s, want %s (April 3rd)", ambiguous, dmy.newTimeParser().parse(ambiguous), want)
	}
}

// TestNewRejectsUnknownDateFormat covers config.go's dateFormat enum at
// the adapter boundary too, so a typo fails at wiring time rather than
// silently falling back to the unambiguous-only default.
func TestNewRejectsUnknownDateFormat(t *testing.T) {
	_, err := New(Config{Instance: "acme", Username: testUser, Password: testPassword, DateFormat: "ymd"}, nil)
	if err == nil {
		t.Fatal("New with an unknown dateFormat must fail")
	}
}

// TestUnparseableTimestampWarnsOncePerCall is the round-1 finding at
// mapping.go ~70-81: parseTime used to fail silently, so a display-value
// date in a layout the adapter does not know about vanished with no trace.
// It must now warn — but only once per call, even when every record on a
// List page carries the same bad format.
func TestUnparseableTimestampWarnsOncePerCall(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":[
			{"sys_id":"a1b2c3d4e5f60718293a4b5c6d7e8f91","number":"INC0000001","opened_at":"not-a-date","sys_updated_on":"not-a-date"},
			{"sys_id":"a1b2c3d4e5f60718293a4b5c6d7e8f92","number":"INC0000002","opened_at":"not-a-date","sys_updated_on":"not-a-date"}
		]}`))
	})
	got, err := c.list(context.Background(), source.ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tickets, want 2", len(got))
	}
	for _, tk := range got {
		if !tk.CreatedAt.IsZero() {
			t.Errorf("%s: CreatedAt = %s, want the zero time", tk.Key, tk.CreatedAt)
		}
	}
	warnings := c.WarningsFor("")
	count := 0
	for _, w := range warnings {
		if strings.Contains(w, "not-a-date") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("got %d timestamp warnings across %d records, want exactly 1: %v", count, len(got), warnings)
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
