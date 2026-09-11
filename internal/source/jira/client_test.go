package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

const (
	testEmail    = "ops@acme.example"
	testAPIToken = "cloud-token-secret"
	testPAT      = "dc-pat-secret"

	// fixtureBase is the placeholder host the fixtures use for absolute
	// URLs; it is rewritten to the test server's address as each fixture is
	// served.
	fixtureBase = "https://jira.example.com"
	// fixtureForeign is the placeholder for a host that is not the configured
	// Jira instance; a test that cares rewrites it to a second listener.
	fixtureForeign = "https://attacker.example"
)

// --- test server ---

type testServer struct {
	*httptest.Server
	t *testing.T

	// rewrites is applied to every fixture served, on top of the placeholder
	// host rewrite, so a fixture can point at a second listener too.
	rewrites map[string]string

	mu     sync.Mutex
	counts map[string]int
}

// startServer returns a running server and the mux behind it, counting hits
// per path so a test can assert that a lookup was cached rather than repeated.
func startServer(t *testing.T) (*testServer, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	ts := &testServer{t: t, counts: map[string]int{}}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		ts.counts[r.URL.Path]++
		ts.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, mux
}

func (ts *testServer) hits(path string) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.counts[path]
}

// writeFixture serves a testdata file, rewriting the placeholder host so the
// absolute URLs inside it (attachment content links) point back at this
// server.
func (ts *testServer) writeFixture(w http.ResponseWriter, name string) {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		ts.t.Errorf("read fixture %s: %v", name, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	body := strings.ReplaceAll(string(b), fixtureBase, ts.URL)
	for from, to := range ts.rewrites {
		body = strings.ReplaceAll(body, from, to)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func wantBasicAuth(t *testing.T, r *http.Request) {
	t.Helper()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testEmail+":"+testAPIToken))
	if got := r.Header.Get("Authorization"); got != want {
		t.Errorf("%s: Authorization = %q, want basic auth", r.URL.Path, got)
	}
	if got := r.Header.Get("X-Atlassian-Token"); got != "no-check" {
		t.Errorf("%s: X-Atlassian-Token = %q, want %q", r.URL.Path, got, "no-check")
	}
}

func wantBearerAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got, want := r.Header.Get("Authorization"), "Bearer "+testPAT; got != want {
		t.Errorf("%s: Authorization = %q, want %q", r.URL.Path, got, want)
	}
	if got := r.Header.Get("X-Atlassian-Token"); got != "no-check" {
		t.Errorf("%s: X-Atlassian-Token = %q, want %q", r.URL.Path, got, "no-check")
	}
}

func newClient(t *testing.T, ts *testServer, cfg Config) *Client {
	t.Helper()
	cfg.BaseURL = ts.URL
	c, err := New(cfg, ts.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func cloudConfig() Config {
	return Config{Deployment: DeploymentCloud, Email: testEmail, APIToken: testAPIToken}
}

func dataCenterConfig() Config {
	return Config{Deployment: DeploymentDataCenter, PAT: testPAT}
}

// wantSourceError asserts err is a *source.Error carrying code.
func wantSourceError(t *testing.T, err error, code source.Code) *source.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s error, got nil", code)
	}
	var serr *source.Error
	if !errors.As(err, &serr) {
		t.Fatalf("error %v is not a *source.Error", err)
	}
	if serr.Code != code {
		t.Fatalf("error code = %q, want %q (message %q)", serr.Code, code, serr.Message)
	}
	return serr
}

// --- New ---

func TestNewValidatesConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"cloud basic", Config{BaseURL: "https://acme.atlassian.net", Email: "a@b.c", APIToken: "t"}, false},
		{"data center pat", Config{BaseURL: "https://jira.corp", Deployment: "datacenter", PAT: "p"}, false},
		{"trailing slash", Config{BaseURL: "https://acme.atlassian.net/", PAT: "p"}, false},
		{"no base url", Config{PAT: "p"}, true},
		{"relative base url", Config{BaseURL: "jira.corp", PAT: "p"}, true},
		{"non http scheme", Config{BaseURL: "ftp://jira.corp", PAT: "p"}, true},
		{"unknown deployment", Config{BaseURL: "https://jira.corp", Deployment: "onprem", PAT: "p"}, true},
		{"no credentials", Config{BaseURL: "https://jira.corp"}, true},
		{"email without token", Config{BaseURL: "https://jira.corp", Email: "a@b.c"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := New(tt.cfg, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("New(%+v) = nil error, want an error", tt.cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%+v): %v", tt.cfg, err)
			}
			if strings.HasSuffix(c.base, "/") {
				t.Errorf("base %q keeps its trailing slash", c.base)
			}
		})
	}
}

// --- Get: mapping ---

func TestGetCloudMapsServiceDeskIssue(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		if got := r.URL.Query().Get("fields"); !strings.Contains(got, "summary") || !strings.Contains(got, "attachment") {
			t.Errorf("fields query = %q, want the named field set", got)
		}
		ts.writeFixture(w, "issue_cloud.json")
	})

	c := newClient(t, ts, cloudConfig())
	got, err := c.Get(context.Background(), "SUP-42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Key != "SUP-42" {
		t.Errorf("Key = %q, want SUP-42", got.Key)
	}
	if want := "Payment webhook retries stall after 3 attempts"; got.Title != want {
		t.Errorf("Title = %q, want %q", got.Title, want)
	}
	// v2 hands back wiki markup, which the adapter passes through untouched.
	if !strings.Contains(got.Description, "!screenshot.png|thumbnail!") {
		t.Errorf("Description lost its wiki markup: %q", got.Description)
	}
	if got.Priority != "High" {
		t.Errorf("Priority = %q, want High", got.Priority)
	}
	if got.Status != "In Progress" {
		t.Errorf("Status = %q, want In Progress", got.Status)
	}
	if got.Assignee != "Priya Raman" {
		t.Errorf("Assignee = %q, want Priya Raman", got.Assignee)
	}
	if want := ts.URL + "/browse/SUP-42"; got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
	// A service_desk project means the issue is its own helpdesk ticket.
	if got.HelpdeskRef != "SUP-42" {
		t.Errorf("HelpdeskRef = %q, want SUP-42", got.HelpdeskRef)
	}
	if want := "2026-09-01T09:14:22Z"; got.CreatedAt.UTC().Format(time.RFC3339) != want {
		t.Errorf("CreatedAt = %s, want %s", got.CreatedAt.UTC().Format(time.RFC3339), want)
	}
	if want := "2026-09-08T16:02:11Z"; got.UpdatedAt.UTC().Format(time.RFC3339) != want {
		t.Errorf("UpdatedAt = %s, want %s", got.UpdatedAt.UTC().Format(time.RFC3339), want)
	}

	wantFields := map[string]string{
		"issuetype": "Support",
		"project":   "SUP",
		"labels":    "payments,webhooks",
		"parent":    "SUP-7",
		"reporter":  "Dana Okoro",
	}
	for k, want := range wantFields {
		if got.Fields[k] != want {
			t.Errorf("Fields[%q] = %q, want %q", k, got.Fields[k], want)
		}
	}
	// resolution is null on this issue, so it must not appear at all.
	if _, ok := got.Fields["resolution"]; ok {
		t.Errorf("Fields carries an empty resolution: %q", got.Fields["resolution"])
	}
	// Cloud carries the epic in the standard parent field, so no custom
	// field discovery should have happened.
	if n := ts.hits("/rest/api/2/field"); n != 0 {
		t.Errorf("field discovery ran %d times on Cloud, want 0", n)
	}
	if w := c.WarningsFor("SUP-42"); len(w) != 0 {
		t.Errorf("unexpected warnings: %v", w)
	}
}

func TestGetDataCenterResolvesEpicLinkField(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/field", func(w http.ResponseWriter, r *http.Request) {
		wantBearerAuth(t, r)
		ts.writeFixture(w, "fields_datacenter.json")
	})
	mux.HandleFunc("/rest/api/2/issue/ENG-17", func(w http.ResponseWriter, r *http.Request) {
		wantBearerAuth(t, r)
		if got := r.URL.Query().Get("fields"); !strings.Contains(got, "customfield_10101") {
			t.Errorf("fields query = %q, want it to request the resolved epic-link field", got)
		}
		ts.writeFixture(w, "issue_datacenter.json")
	})

	c := newClient(t, ts, dataCenterConfig())
	got, err := c.Get(context.Background(), "ENG-17")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Data Center has no parent field for epics; the link lives in a custom
	// field whose id is instance-specific and resolved by name.
	if got.Fields["parent"] != "ENG-1" {
		t.Errorf("Fields[parent] = %q, want ENG-1 (from the epic-link custom field)", got.Fields["parent"])
	}
	if got.Fields["resolution"] != "Unresolved" {
		t.Errorf("Fields[resolution] = %q, want Unresolved", got.Fields["resolution"])
	}
	if got.Fields["project"] != "ENG" {
		t.Errorf("Fields[project] = %q, want ENG", got.Fields["project"])
	}
	if got.Assignee != "Sam Iyer" {
		t.Errorf("Assignee = %q, want Sam Iyer", got.Assignee)
	}
	// A software project is not a service desk, so nothing links it to a
	// helpdesk ticket.
	if got.HelpdeskRef != "" {
		t.Errorf("HelpdeskRef = %q, want empty for a software project", got.HelpdeskRef)
	}
	if want := "2026-08-27T21:11:09Z"; got.CreatedAt.UTC().Format(time.RFC3339) != want {
		t.Errorf("CreatedAt = %s, want %s", got.CreatedAt.UTC().Format(time.RFC3339), want)
	}

	if _, err := c.Get(context.Background(), "ENG-17"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if n := ts.hits("/rest/api/2/field"); n != 1 {
		t.Errorf("field discovery ran %d times, want 1 (it should be cached)", n)
	}
}

func TestGetEpicLinkFieldMissingWarnsButSucceeds(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/field", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"summary","name":"Summary","custom":false}]`))
	})
	mux.HandleFunc("/rest/api/2/issue/ENG-17", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_datacenter.json")
	})

	c := newClient(t, ts, dataCenterConfig())
	got, err := c.Get(context.Background(), "ENG-17")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Fields["parent"] != "" {
		t.Errorf("Fields[parent] = %q, want empty when the epic field is unknown", got.Fields["parent"])
	}
	warnings := c.WarningsFor("ENG-17")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Epic Link") {
		t.Errorf("warnings = %v, want one naming the Epic Link field", warnings)
	}
	// Reading them consumes them.
	if w := c.WarningsFor("ENG-17"); len(w) != 0 {
		t.Errorf("warnings survived a read: %v", w)
	}
}

// --- Helpdesk view ---

func TestHelpdeskGetDistinguishesServiceDesk(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud.json")
	})
	mux.HandleFunc("/rest/api/2/issue/ENG-17", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_datacenter.json")
	})
	mux.HandleFunc("/rest/api/2/field", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "fields_datacenter.json")
	})

	c := newClient(t, ts, cloudConfig())
	hd := c.Helpdesk()

	jsm, err := hd.Get(context.Background(), "SUP-42")
	if err != nil {
		t.Fatalf("Helpdesk().Get(SUP-42): %v", err)
	}
	if jsm.ID != "SUP-42" {
		t.Errorf("ID = %q, want SUP-42", jsm.ID)
	}
	if jsm.Channel != "jira-service-management" {
		t.Errorf("Channel = %q, want jira-service-management", jsm.Channel)
	}
	if jsm.Contact != "Dana Okoro" {
		t.Errorf("Contact = %q, want Dana Okoro", jsm.Contact)
	}
	if jsm.Customer != "dana@customer.example" {
		t.Errorf("Customer = %q, want dana@customer.example", jsm.Customer)
	}
	if jsm.CustomerID != "acc-dana" {
		t.Errorf("CustomerID = %q, want acc-dana", jsm.CustomerID)
	}
	if jsm.Fields["projectTypeKey"] != "service_desk" {
		t.Errorf("Fields[projectTypeKey] = %q, want service_desk", jsm.Fields["projectTypeKey"])
	}

	plain, err := hd.Get(context.Background(), "ENG-17")
	if err != nil {
		t.Fatalf("Helpdesk().Get(ENG-17): %v", err)
	}
	if plain.Channel != "jira" {
		t.Errorf("Channel = %q, want jira for a non-service-desk project", plain.Channel)
	}
}

// --- Threads ---

func TestThreadsMapsRolesAndAttachmentRefs(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud.json")
	})

	c := newClient(t, ts, cloudConfig())
	th, err := c.Threads(context.Background(), "SUP-42")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 4 {
		t.Fatalf("got %d messages, want 4 (description plus three comments)", len(th))
	}

	want := []struct {
		author string
		role   ticket.Role
		atts   []string
	}{
		// The description is what the reporter raised the request with.
		{"Dana Okoro", ticket.RoleCustomer, []string{"9001", "9003"}},
		{"Dana Okoro", ticket.RoleCustomer, []string{"9001"}},
		// An agent-only note keeps the marker in the author.
		{"Priya Raman (internal)", ticket.RoleAgent, []string{"9003"}},
		{"Automation for Jira", ticket.RoleSystem, nil},
	}
	for i, w := range want {
		if th[i].Author != w.author {
			t.Errorf("message %d Author = %q, want %q", i, th[i].Author, w.author)
		}
		if th[i].Role != w.role {
			t.Errorf("message %d Role = %q, want %q", i, th[i].Role, w.role)
		}
		if strings.Join(th[i].AttachmentIDs, ",") != strings.Join(w.atts, ",") {
			t.Errorf("message %d AttachmentIDs = %v, want %v", i, th[i].AttachmentIDs, w.atts)
		}
	}

	for i := 1; i < len(th); i++ {
		if th[i].At.Before(th[i-1].At) {
			t.Errorf("messages are not in ascending time order at index %d", i)
		}
	}
}

func TestThreadsPagesTruncatedComments(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-45", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud_truncated.json")
	})
	mux.HandleFunc("/rest/api/2/issue/SUP-45/comment", func(w http.ResponseWriter, r *http.Request) {
		switch got := r.URL.Query().Get("startAt"); got {
		case "0":
			ts.writeFixture(w, "comments_page1.json")
		case "2":
			ts.writeFixture(w, "comments_page2.json")
		default:
			t.Errorf("unexpected startAt %q", got)
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	c := newClient(t, ts, cloudConfig())
	th, err := c.Threads(context.Background(), "SUP-45")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	// The issue payload carried only one of three comments, so the client
	// must page the comment endpoint rather than serve a short thread.
	if len(th) != 4 {
		t.Fatalf("got %d messages, want 4 (description plus three comments)", len(th))
	}
	if n := ts.hits("/rest/api/2/issue/SUP-45/comment"); n != 2 {
		t.Errorf("comment endpoint hit %d times, want 2", n)
	}
	if !strings.Contains(th[3].Author, "(internal)") {
		t.Errorf("last message Author = %q, want the internal marker", th[3].Author)
	}
}

// TestThreadsCapsCommentPagination covers an instance that never stops
// paginating: it answers every request with the same full page and a total
// it does not honour. Both of the loop's own exits depend on the server
// being truthful, so without a page cap this is an endless run of round
// trips.
func TestThreadsCapsCommentPagination(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-45", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud_truncated.json")
	})
	mux.HandleFunc("/rest/api/2/issue/SUP-45/comment", func(w http.ResponseWriter, r *http.Request) {
		// total 0 means "not reported", and startAt is ignored: the same
		// two comments come back for ever.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"startAt":0,"maxResults":2,"total":0,"comments":[
			{"id":"9001","author":{"displayName":"Loop One","accountId":"acc-1"},"body":"again","created":"2026-09-02T09:00:00.000+0000"},
			{"id":"9002","author":{"displayName":"Loop Two","accountId":"acc-2"},"body":"and again","created":"2026-09-02T09:01:00.000+0000"}
		]}`))
	})

	c := newClient(t, ts, cloudConfig())
	th, err := c.Threads(context.Background(), "SUP-45")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if n := ts.hits("/rest/api/2/issue/SUP-45/comment"); n != maxCommentPages {
		t.Errorf("comment endpoint hit %d times, want the %d page cap", n, maxCommentPages)
	}
	// The description, plus two comments for every page fetched.
	if want := 1 + 2*maxCommentPages; len(th) != want {
		t.Errorf("got %d messages, want %d", len(th), want)
	}

	warnings := c.WarningsFor("SUP-45")
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "stopped after") && strings.Contains(w, "pages") {
			found = true
		}
	}
	if !found {
		t.Errorf("WarningsFor(SUP-45) = %v, want one saying pagination stopped early", warnings)
	}
}

// TestWarningsSurviveTheWholeBundle runs the call sequence
// internal/run/prepare.go's fetchBundle runs — Get, then Threads, then
// Attachments, then one WarningsFor — with Threads hitting the comment page
// cap and Attachments finding nothing to complain about.
//
// Each call files its warnings under the same ticket when it ends. While
// publish replaced that entry instead of appending to it, the clean
// Attachments wiped the pagination warning Threads had just recorded, and
// the agent read a thread that stops at page 100 with nothing saying so.
func TestWarningsSurviveTheWholeBundle(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-45", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud_truncated.json")
	})
	mux.HandleFunc("/rest/api/2/issue/SUP-45/comment", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"startAt":0,"maxResults":1,"total":0,"comments":[
			{"id":"9001","author":{"displayName":"Loop One","accountId":"acc-1"},"body":"again","created":"2026-09-02T09:00:00.000+0000"}
		]}`))
	})

	c := newClient(t, ts, cloudConfig())
	ctx := context.Background()

	if _, err := c.Get(ctx, "SUP-45"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := c.Threads(ctx, "SUP-45"); err != nil {
		t.Fatalf("Threads: %v", err)
	}
	// The fixture carries no attachments, so this call records nothing —
	// the case that used to erase everything before it.
	atts, err := c.Attachments(ctx, "SUP-45", filepath.Join(t.TempDir(), "att"))
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 0 {
		t.Fatalf("attachments = %+v, want none", atts)
	}

	warnings := c.WarningsFor("SUP-45")
	found := 0
	for _, w := range warnings {
		if strings.Contains(w, "stopped after") && strings.Contains(w, "pages") {
			found++
		}
	}
	if found != 1 {
		t.Errorf("WarningsFor(SUP-45) = %v, want exactly one pagination warning after Get/Threads/Attachments", warnings)
	}
	if again := c.WarningsFor("SUP-45"); len(again) != 0 {
		t.Errorf("warnings survived being read: %v", again)
	}
}

// --- Search ---

func TestBuildJQL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		projectKey string
		filter     source.ListFilter
		want       string
	}{
		{
			name:   "default is open-ish",
			filter: source.ListFilter{},
			want:   "statusCategory != Done ORDER BY updated DESC",
		},
		{
			name:   "me becomes currentUser",
			filter: source.ListFilter{Assignee: "me"},
			want:   "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC",
		},
		{
			name:   "explicit assignee is quoted",
			filter: source.ListFilter{Assignee: "priya@acme.example"},
			want:   `assignee = "priya@acme.example" AND statusCategory != Done ORDER BY updated DESC`,
		},
		{
			name:   "explicit status replaces the default",
			filter: source.ListFilter{Status: "In Progress"},
			want:   `status = "In Progress" ORDER BY updated DESC`,
		},
		{
			name:   "parent",
			filter: source.ListFilter{Parent: "SUP-7"},
			want:   `statusCategory != Done AND parent = "SUP-7" ORDER BY updated DESC`,
		},
		{
			name:       "project key scopes the search",
			projectKey: "SUP",
			filter:     source.ListFilter{Assignee: "me"},
			want:       `project = "SUP" AND assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC`,
		},
		{
			name:   "quotes in a value cannot end the literal",
			filter: source.ListFilter{Status: `we"ird`},
			want:   `status = "we\"ird" ORDER BY updated DESC`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &Client{cfg: Config{ProjectKey: tt.projectKey}}
			if got := c.buildJQL(tt.filter); got != tt.want {
				t.Errorf("buildJQL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListCloudPaginatesWithNextPageToken(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var body struct {
			JQL           string   `json:"jql"`
			Fields        []string `json:"fields"`
			MaxResults    int      `json:"maxResults"`
			NextPageToken string   `json:"nextPageToken"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode search body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if want := "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"; body.JQL != want {
			t.Errorf("jql = %q, want %q", body.JQL, want)
		}
		if len(body.Fields) == 0 {
			t.Errorf("fields is empty, want the named field set")
		}
		switch body.NextPageToken {
		case "":
			if body.MaxResults != 3 {
				t.Errorf("page 1 maxResults = %d, want 3 (the caller's limit)", body.MaxResults)
			}
			ts.writeFixture(w, "search_cloud_page1.json")
		case "cursor-page-2":
			if body.MaxResults != 1 {
				t.Errorf("page 2 maxResults = %d, want 1 (what is left of the limit)", body.MaxResults)
			}
			ts.writeFixture(w, "search_cloud_page2.json")
		default:
			t.Errorf("unexpected nextPageToken %q", body.NextPageToken)
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	c := newClient(t, ts, cloudConfig())
	got, err := c.List(context.Background(), source.ListFilter{Assignee: "me", Limit: 3})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	keys := make([]string, len(got))
	for i, tt := range got {
		keys[i] = tt.Key
	}
	if want := "SUP-42,SUP-43,SUP-44"; strings.Join(keys, ",") != want {
		t.Errorf("keys = %v, want %s", keys, want)
	}
	if got[0].HelpdeskRef != "SUP-42" {
		t.Errorf("listed service desk issue lost its HelpdeskRef: %q", got[0].HelpdeskRef)
	}
	if n := ts.hits("/rest/api/3/search/jql"); n != 2 {
		t.Errorf("search hit %d times, want 2", n)
	}
}

func TestListDataCenterPaginatesWithStartAt(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/field", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "fields_datacenter.json")
	})
	mux.HandleFunc("/rest/api/2/search", func(w http.ResponseWriter, r *http.Request) {
		wantBearerAuth(t, r)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		q := r.URL.Query()
		if want := `project = "ENG" AND assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC`; q.Get("jql") != want {
			t.Errorf("jql = %q, want %q", q.Get("jql"), want)
		}
		switch q.Get("startAt") {
		case "0":
			if q.Get("maxResults") != "3" {
				t.Errorf("page 1 maxResults = %q, want 3", q.Get("maxResults"))
			}
			ts.writeFixture(w, "search_datacenter_page1.json")
		case "2":
			if q.Get("maxResults") != "1" {
				t.Errorf("page 2 maxResults = %q, want 1", q.Get("maxResults"))
			}
			ts.writeFixture(w, "search_datacenter_page2.json")
		default:
			t.Errorf("unexpected startAt %q", q.Get("startAt"))
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	cfg := dataCenterConfig()
	cfg.ProjectKey = "ENG"
	c := newClient(t, ts, cfg)

	got, err := c.List(context.Background(), source.ListFilter{Assignee: "me", Limit: 3})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	keys := make([]string, len(got))
	for i, tt := range got {
		keys[i] = tt.Key
	}
	if want := "ENG-17,ENG-18,ENG-19"; strings.Join(keys, ",") != want {
		t.Errorf("keys = %v, want %s", keys, want)
	}
	// The epic-link custom field is resolved for List too, so a listed issue
	// carries its epic.
	if got[0].Fields["parent"] != "ENG-1" {
		t.Errorf("Fields[parent] = %q, want ENG-1", got[0].Fields["parent"])
	}
	if n := ts.hits("/rest/api/2/search"); n != 2 {
		t.Errorf("search hit %d times, want 2", n)
	}
}

func TestListStopsOnRepeatedCursorAndCapsLimit(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxResults int `json:"maxResults"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode search body: %v", err)
		}
		if body.MaxResults > maxPageSize {
			t.Errorf("maxResults = %d, want no more than %d", body.MaxResults, maxPageSize)
		}
		// Always hand back the same cursor, the documented Cloud failure mode.
		ts.writeFixture(w, "search_cloud_page1.json")
	})

	c := newClient(t, ts, cloudConfig())
	got, err := c.List(context.Background(), source.ListFilter{Limit: 500})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("got %d issues, want 4 (two pages before the cursor repeated)", len(got))
	}

	warnings := c.WarningsFor("")
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want one for the capped limit and one for the repeated cursor", warnings)
	}
	if !strings.Contains(warnings[0], "capped at 200") {
		t.Errorf("first warning = %q, want it to name the cap", warnings[0])
	}
	if !strings.Contains(warnings[1], "repeated nextPageToken") {
		t.Errorf("second warning = %q, want it to name the repeated cursor", warnings[1])
	}
}

func TestEffectiveLimit(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, defaultListResults},
		{-1, defaultListResults},
		{50, 50},
		{200, maxListResults},
		{500, maxListResults},
	}
	for _, tc := range cases {
		if got, _ := httpx.Limit(tc.in, defaultListResults, maxListResults); got != tc.want {
			t.Errorf("Limit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if defaultListResults != 100 || maxListResults != 200 {
		t.Errorf("limits = %d/%d, want the contract's 100 default and 200 cap", defaultListResults, maxListResults)
	}
}

// --- Ping and deployment detection ---

func TestPingDetectsDeployment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cfg      Config
		fixture  string
		wantAuth string
		want     string
	}{
		{
			name:     "cloud credentials on a cloud instance",
			cfg:      Config{Deployment: DeploymentAuto, Email: testEmail, APIToken: testAPIToken},
			fixture:  "serverinfo_cloud.json",
			wantAuth: "Basic",
			want:     DeploymentCloud,
		},
		{
			name:     "pat on a data center instance",
			cfg:      Config{Deployment: DeploymentAuto, PAT: testPAT},
			fixture:  "serverinfo_datacenter.json",
			wantAuth: "Bearer",
			want:     DeploymentDataCenter,
		},
		{
			// The probe is what decides, not the shape of the credentials:
			// a data center instance fronted by basic auth still reports
			// itself as Server.
			name:     "probe overrides the credential guess",
			cfg:      Config{Deployment: DeploymentAuto, Email: testEmail, APIToken: testAPIToken},
			fixture:  "serverinfo_datacenter.json",
			wantAuth: "Basic",
			want:     DeploymentDataCenter,
		},
		{
			// An explicit setting is the operator's call and survives a probe
			// that disagrees.
			name:     "explicit config wins over the probe",
			cfg:      Config{Deployment: DeploymentCloud, Email: testEmail, APIToken: testAPIToken},
			fixture:  "serverinfo_datacenter.json",
			wantAuth: "Basic",
			want:     DeploymentCloud,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts, mux := startServer(t)
			mux.HandleFunc("/rest/api/2/serverInfo", func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, tt.wantAuth+" ") {
					t.Errorf("Authorization = %q, want a %s credential", got, tt.wantAuth)
				}
				ts.writeFixture(w, tt.fixture)
			})

			c := newClient(t, ts, tt.cfg)
			if err := c.Ping(context.Background()); err != nil {
				t.Fatalf("Ping: %v", err)
			}
			if got := c.cachedDeployment(); got != tt.want {
				t.Errorf("deployment = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPingReportsAuthFailure(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/serverInfo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errorMessages":["Client must be authenticated"]}`))
	})

	c := newClient(t, ts, cloudConfig())
	serr := wantSourceError(t, c.Ping(context.Background()), source.Auth)
	if strings.Contains(serr.Message, testAPIToken) {
		t.Errorf("error message leaks the credential: %q", serr.Message)
	}
}

func TestListRoutesByDetectedDeployment(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/serverInfo", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "serverinfo_datacenter.json")
	})
	mux.HandleFunc("/rest/api/2/field", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "fields_datacenter.json")
	})
	mux.HandleFunc("/rest/api/2/search", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "search_datacenter_page2.json")
	})
	mux.HandleFunc("/rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("Cloud search endpoint used against a Data Center instance")
		w.WriteHeader(http.StatusGone)
	})

	// No deployment configured and a hostname that gives nothing away: the
	// serverInfo probe is what routes the search.
	c := newClient(t, ts, Config{Deployment: DeploymentAuto, PAT: testPAT})
	got, err := c.List(context.Background(), source.ListFilter{Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Key != "ENG-19" {
		t.Fatalf("got %d issues (%v), want ENG-19", len(got), got)
	}
	if n := ts.hits("/rest/api/2/serverInfo"); n != 1 {
		t.Errorf("serverInfo probed %d times, want 1 (it should be cached)", n)
	}
}

// --- Error mapping ---

func TestErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   source.Code
	}{
		{"unauthorized", http.StatusUnauthorized, `{"errorMessages":["nope"]}`, source.Auth},
		{"forbidden", http.StatusForbidden, `{"errorMessages":["no permission"]}`, source.Auth},
		{"not found", http.StatusNotFound, `{"errorMessages":["Issue does not exist"]}`, source.NotFound},
		{"server error", http.StatusInternalServerError, `{"errorMessages":["boom"]}`, source.Internal},
		{"gone", http.StatusGone, `deprecated endpoint`, source.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts, mux := startServer(t)
			mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})

			c := newClient(t, ts, cloudConfig())
			_, err := c.Get(context.Background(), "SUP-42")
			serr := wantSourceError(t, err, tt.want)
			if !strings.Contains(serr.Message, "/rest/api/2/issue/SUP-42") {
				t.Errorf("message %q does not name the path", serr.Message)
			}
			if strings.Contains(serr.Message, testAPIToken) {
				t.Errorf("message leaks the credential: %q", serr.Message)
			}
		})
	}
}

func TestInternalErrorTruncatesBody(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	long := strings.Repeat("x", 5000)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(long))
	})

	c := newClient(t, ts, cloudConfig())
	_, err := c.Get(context.Background(), "SUP-42")
	serr := wantSourceError(t, err, source.Internal)
	if n := strings.Count(serr.Message, "x"); n != 200 {
		t.Errorf("body snippet is %d bytes, want 200", n)
	}
}

func TestRateLimitedHonoursOneRetryAfter(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errorMessages":["rate limit"]}`))
	})

	c := newClient(t, ts, cloudConfig())
	_, err := c.Get(context.Background(), "SUP-42")
	wantSourceError(t, err, source.RateLimited)

	// One wait, one retry, then give up: a triage run must not sit in a
	// retry loop behind a quota it cannot spend down.
	if n := ts.hits("/rest/api/2/issue/SUP-42"); n != 2 {
		t.Errorf("issue fetched %d times, want 2 (the request and one retry)", n)
	}
}

func TestRateLimitedSkipsALongRetryAfter(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	c := newClient(t, ts, cloudConfig())
	start := time.Now()
	_, err := c.Get(context.Background(), "SUP-42")
	wantSourceError(t, err, source.RateLimited)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Get waited %s, want it to give up rather than honour an hour-long Retry-After", elapsed)
	}
	if n := ts.hits("/rest/api/2/issue/SUP-42"); n != 1 {
		t.Errorf("issue fetched %d times, want 1 (no retry)", n)
	}
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		header  string
		wantOK  bool
		wantDur time.Duration
	}{
		{"", false, 0},
		{"5", true, 5 * time.Second},
		{"30", true, 30 * time.Second},
		{"31", false, 0},
		{"-1", false, 0},
		{"soon", false, 0},
	}
	for _, tt := range tests {
		t.Run("header="+tt.header, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			if tt.header != "" {
				h.Set("Retry-After", tt.header)
			}
			got, ok := httpx.RetryAfter(h, maxRetryAfter)
			if ok != tt.wantOK {
				t.Fatalf("RetryAfter(%q) ok = %v, want %v", tt.header, ok, tt.wantOK)
			}
			if ok && got != tt.wantDur {
				t.Errorf("RetryAfter(%q) = %s, want %s", tt.header, got, tt.wantDur)
			}
		})
	}
}

// --- Attachments ---

// attachmentBodies is what the fixture server serves for each attachment id.
var attachmentBodies = map[string]string{
	"9001": "\x89PNG\r\n\x1a\nfake png bytes",
	"9002": "root:x:0:0",
	"9003": "retry budget exhausted at 03:14:02",
}

func TestAttachmentsDownloadsAndSanitisesNames(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud.json")
	})
	mux.HandleFunc("/rest/api/2/attachment/content/", func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		id := strings.TrimPrefix(r.URL.Path, "/rest/api/2/attachment/content/")
		body, ok := attachmentBodies[id]
		if !ok {
			t.Errorf("unexpected attachment id %q", id)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(body))
	})

	c := newClient(t, ts, cloudConfig())
	dir := filepath.Join(t.TempDir(), "attachments")
	got, err := c.Attachments(context.Background(), "SUP-42", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d attachments, want 3", len(got))
	}

	want := []ticket.Attachment{
		{ID: "9001", Name: "screenshot.png", MIME: "image/png", Path: "attachments/1-screenshot.png"},
		// "../../etc/passwd" must not escape the destination directory.
		{ID: "9002", Name: "passwd", MIME: "text/plain", Path: "attachments/2-passwd"},
		{ID: "9003", Name: "payments.log", MIME: "text/plain", Path: "attachments/3-payments.log"},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("attachment %d = %+v, want %+v", i, got[i], w)
		}
	}

	for i, w := range want {
		body, err := os.ReadFile(filepath.Join(dir, filepath.Base(w.Path)))
		if err != nil {
			t.Errorf("read attachment %d: %v", i, err)
			continue
		}
		if string(body) != attachmentBodies[w.ID] {
			t.Errorf("attachment %d body = %q, want %q", i, body, attachmentBodies[w.ID])
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("wrote %d files, want 3", len(entries))
	}
	if w := c.WarningsFor("SUP-42"); len(w) != 0 {
		t.Errorf("unexpected warnings: %v", w)
	}
}

func TestAttachmentsWarnsOnLoginRedirectAndHTML(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud.json")
	})
	mux.HandleFunc("/rest/api/2/attachment/content/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/rest/api/2/attachment/content/") {
		case "9001":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(attachmentBodies["9001"]))
		case "9002":
			// Data Center's attachment URL is served by the web layer, which
			// bounces an unauthenticated request to the SSO login page.
			http.Redirect(w, r, "https://sso.example.com/login?RelayState=jira", http.StatusFound)
		case "9003":
			// Some proxies answer with the login page directly, 200 and all.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><body><form id=\"login\"></form></body></html>"))
		}
	})

	c := newClient(t, ts, cloudConfig())
	dir := filepath.Join(t.TempDir(), "attachments")
	got, err := c.Attachments(context.Background(), "SUP-42", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 1 || got[0].ID != "9001" {
		t.Fatalf("got %v, want only attachment 9001", got)
	}

	// Neither login page may be written to disk under an attachment's name.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("wrote %v, want only the one real attachment", names)
	}

	warnings := c.WarningsFor("SUP-42")
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want one per skipped attachment", warnings)
	}
	if !strings.Contains(warnings[0], "sso.example.com") {
		t.Errorf("redirect warning = %q, want it to name the host it was sent to", warnings[0])
	}
	if strings.Contains(warnings[0], "RelayState") {
		t.Errorf("redirect warning leaks the redirect query: %q", warnings[0])
	}
	if !strings.Contains(warnings[1], "text/html") {
		t.Errorf("html warning = %q, want it to name the content type", warnings[1])
	}
}

func TestAttachmentsRefusesAContentURLOnAnotherHost(t *testing.T) {
	t.Parallel()

	// A second listener standing in for wherever a hostile instance would
	// like the credential sent. Any request at all is a failure.
	var foreignHits int
	var foreignMu sync.Mutex
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignMu.Lock()
		foreignHits++
		foreignMu.Unlock()
		t.Errorf("credentialed request reached a foreign host: %s %s (Authorization %q)",
			r.Method, r.URL.Path, r.Header.Get("Authorization"))
	}))
	t.Cleanup(foreign.Close)

	ts, mux := startServer(t)
	ts.rewrites = map[string]string{fixtureForeign: foreign.URL}
	mux.HandleFunc("/rest/api/2/issue/SUP-46", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_foreign_attachment.json")
	})
	mux.HandleFunc("/rest/api/2/attachment/content/", func(w http.ResponseWriter, r *http.Request) {
		wantBasicAuth(t, r)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("real attachment bytes"))
	})

	c := newClient(t, ts, cloudConfig())
	dir := filepath.Join(t.TempDir(), "attachments")
	got, err := c.Attachments(context.Background(), "SUP-46", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}

	foreignMu.Lock()
	hits := foreignHits
	foreignMu.Unlock()
	if hits != 0 {
		t.Errorf("foreign host received %d requests, want 0", hits)
	}

	// The sibling on the configured host is unaffected.
	if len(got) != 1 || got[0].ID != "9101" {
		t.Fatalf("got %v, want only the same-host attachment 9101", got)
	}
	if got[0].Path != "attachments/1-same-host.png" {
		t.Errorf("Path = %q, want attachments/1-same-host.png", got[0].Path)
	}

	warnings := c.WarningsFor("SUP-46")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one for the untrusted host", warnings)
	}
	foreignHost := strings.TrimPrefix(foreign.URL, "http://")
	if want := "jira: attachment host not trusted: " + foreignHost; warnings[0] != want {
		t.Errorf("warning = %q, want %q", warnings[0], want)
	}
	if strings.Contains(warnings[0], "/rest/api/2/attachment") {
		t.Errorf("warning carries the URL, not just the host: %q", warnings[0])
	}
}

func TestTrustedURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		baseURL string
		raw     string
		want    bool
	}{
		{"same host", "https://jira.example.com", "https://jira.example.com/rest/api/2/attachment/content/1", true},
		{"host case is ignored", "https://jira.example.com", "https://JIRA.EXAMPLE.COM/x", true},
		{"trailing dot is the same host", "https://jira.example.com", "https://jira.example.com./x", true},
		{"explicit default port", "https://jira.example.com", "https://jira.example.com:443/x", true},

		{"lookalike suffix", "https://jira.example.com", "https://jira.example.com.attacker.example/x", false},
		{"lookalike prefix", "https://jira.example.com", "https://xjira.example.com/x", false},
		{"unrelated host", "https://jira.example.com", "https://attacker.example/x", false},
		// The real destination of this URL is attacker.example; the
		// configured host is only the userinfo.
		{"userinfo decoy", "https://jira.example.com", "https://jira.example.com@attacker.example/x", false},
		{"userinfo on the right host", "https://jira.example.com", "https://someone@jira.example.com/x", false},
		{"downgraded scheme", "https://jira.example.com", "http://jira.example.com/x", false},
		{"other port", "https://jira.example.com", "https://jira.example.com:8443/x", false},
		{"relative", "https://jira.example.com", "/rest/api/2/attachment/content/1", false},
		{"empty", "https://jira.example.com", "", false},
		{"not a url", "https://jira.example.com", "://nope", false},

		// A plain-HTTP instance (a test server, an on-prem host behind a
		// terminating proxy) trusts its own scheme and https to itself.
		{"http base, same host and port", "http://127.0.0.1:8080", "http://127.0.0.1:8080/x", true},
		{"http base upgraded", "http://127.0.0.1:8080", "https://127.0.0.1:8080/x", true},
		{"http base, default port is a different host", "http://127.0.0.1:8080", "http://127.0.0.1/x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := New(Config{BaseURL: tt.baseURL, PAT: "p"}, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, _, _ := c.trust.CheckRaw(tt.raw)
			if got != tt.want {
				t.Errorf("trust.CheckRaw(%q) with base %q = %v, want %v", tt.raw, tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestAttachmentsFailsWhenEveryDownloadFails(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud.json")
	})
	mux.HandleFunc("/rest/api/2/attachment/content/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	c := newClient(t, ts, cloudConfig())
	got, err := c.Attachments(context.Background(), "SUP-42", filepath.Join(t.TempDir(), "attachments"))
	if err == nil {
		t.Fatalf("Attachments returned %v and no error, want an error when nothing downloaded", got)
	}
	if got != nil {
		t.Errorf("attachments = %v, want nil", got)
	}
	// Every failure is already in the error; repeating it as a warning would
	// only double it up in the bundle.
	if w := c.WarningsFor("SUP-42"); len(w) != 0 {
		t.Errorf("warnings = %v, want none when the error carries them all", w)
	}
}

func TestAttachmentsOnIssueWithNone(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-45", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud_truncated.json")
	})

	c := newClient(t, ts, cloudConfig())
	dir := filepath.Join(t.TempDir(), "attachments")
	got, err := c.Attachments(context.Background(), "SUP-45", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if got != nil {
		t.Errorf("attachments = %v, want nil", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("created %s for an issue with no attachments", dir)
	}
}

func TestSanitizeName(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"screenshot.png", "screenshot.png"},
		{"../../etc/passwd", "passwd"},
		{"/absolute/path/log.txt", "log.txt"},
		// The shared helper treats a backslash as a separator rather than
		// stripping it, which is what Rally and Linear already did: a
		// Windows-shaped name is input like any other, and splitting on it
		// is the safer of the two readings.
		{`windows\path\note.txt`, "note.txt"},
		{"", "attachment"},
		{".", "attachment"},
		{"..", "attachment"},
		{"tab\there.txt", "tabhere.txt"},
		{strings.Repeat("é", 200) + ".png", strings.Repeat("é", 58) + ".png"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got := httpx.SanitizeName(tt.in)
			if got != tt.want {
				t.Errorf("SanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if len(got) > 120 {
				t.Errorf("SanitizeName(%q) is %d bytes, want no more than 120", tt.in, len(got))
			}
		})
	}
}

func TestAttachmentRefNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want []string
	}{
		{"embedded image", "see !screenshot.png!", []string{"screenshot.png"}},
		{"image with options", "see !screenshot.png|thumbnail!", []string{"screenshot.png"}},
		{"file link", "log is [^payments.log]", []string{"payments.log"}},
		{"both, deduplicated", "!a.png! and [^a.png] and !b.png!", []string{"a.png", "b.png"}},
		{"emphasis is not an attachment", "this is !important! work", nil},
		{"no refs", "plain text", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := attachmentRefNames(tt.body)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("attachmentRefNames(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

// --- warnings are per ticket ---

func TestWarningsAreKeyedByTicket(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud.json")
	})
	mux.HandleFunc("/rest/api/2/issue/SUP-45", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud_truncated.json")
	})
	mux.HandleFunc("/rest/api/2/issue/SUP-45/comment", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("startAt") {
		case "0":
			ts.writeFixture(w, "comments_page1.json")
		default:
			ts.writeFixture(w, "comments_page2.json")
		}
	})
	mux.HandleFunc("/rest/api/2/attachment/content/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "9002") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("ok"))
	})

	c := newClient(t, ts, cloudConfig())
	ctx := context.Background()

	// Two tickets in flight at once must not be handed each other's
	// warnings, which is the whole reason source.Warner is keyed by id.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := c.Attachments(ctx, "SUP-42", filepath.Join(t.TempDir(), "a42")); err != nil {
			t.Errorf("Attachments(SUP-42): %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := c.Threads(ctx, "SUP-45"); err != nil {
			t.Errorf("Threads(SUP-45): %v", err)
		}
	}()
	wg.Wait()

	if w := c.WarningsFor("SUP-45"); len(w) != 0 {
		t.Errorf("SUP-45 warnings = %v, want none", w)
	}
	w := c.WarningsFor("SUP-42")
	if len(w) != 1 || !strings.Contains(w[0], "9002") {
		t.Errorf("SUP-42 warnings = %v, want one naming attachment 9002", w)
	}
}
