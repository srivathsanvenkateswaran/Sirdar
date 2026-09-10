package rally

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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

const (
	testAPIKey = "_abc-secret-key"
	// fixtureBase is the host the testdata _ref values point at; the
	// fixture server rewrites it to its own URL so refs resolve.
	fixtureBase = "https://rally.example.com"
	wsPrefix    = "/slm/webservice/v2.0/"
)

// --- harness ---

// fixtureServer is an httptest server that asserts the ZSESSIONID header on
// every request and records the URLs it was called with.
type fixtureServer struct {
	*httptest.Server

	mu   sync.Mutex
	reqs []url.URL
}

// handler is the per-test request handler. base is the server's own URL, so
// a fixture's refs can be rewritten to point back at it.
type handler func(t *testing.T, w http.ResponseWriter, r *http.Request, base string)

func newServer(t *testing.T, h handler) *fixtureServer {
	t.Helper()
	fs := &fixtureServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("ZSESSIONID"); got != testAPIKey {
			t.Errorf("ZSESSIONID header = %q, want %q (path %s)", got, testAPIKey, r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET (path %s)", r.Method, r.URL.Path)
		}
		fs.mu.Lock()
		fs.reqs = append(fs.reqs, *r.URL)
		fs.mu.Unlock()
		h(t, w, r, fs.Server.URL)
	}))
	t.Cleanup(fs.Close)
	return fs
}

// collection is the WSAPI collection a request addressed, e.g. "defect" or
// "portfolioitem/feature".
func collection(r *http.Request) string {
	return strings.TrimPrefix(r.URL.Path, wsPrefix)
}

func (fs *fixtureServer) requests() []url.URL {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]url.URL(nil), fs.reqs...)
}

// collections returns the collection path of every request in order.
func (fs *fixtureServer) collections() []string {
	var out []string
	for _, u := range fs.requests() {
		out = append(out, strings.TrimPrefix(u.Path, wsPrefix))
	}
	return out
}

// queriesFor returns the decoded query values of every request against a
// collection, in order.
func (fs *fixtureServer) queriesFor(coll string) []url.Values {
	var out []url.Values
	for _, u := range fs.requests() {
		if strings.TrimPrefix(u.Path, wsPrefix) == coll {
			out = append(out, u.Query())
		}
	}
	return out
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// serveFixture writes a fixture with its refs rewritten to the test
// server's own URL.
func serveFixture(t *testing.T, w http.ResponseWriter, base, name string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(strings.ReplaceAll(fixture(t, name), fixtureBase, base))); err != nil {
		t.Errorf("write fixture %s: %v", name, err)
	}
}

func newClient(t *testing.T, fs *fixtureServer, cfg Config) *Client {
	t.Helper()
	cfg.BaseURL = fs.URL
	cfg.APIKey = testAPIKey
	c, err := New(cfg, fs.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// wantsID reports whether a query filters on a particular FormattedID.
func wantsID(r *http.Request, id string) bool {
	return strings.Contains(r.URL.Query().Get("query"), `"`+id+`"`)
}

// artifactHandler serves the defect and story fixtures from their own
// collections, and an empty result for anything else. It is the happy-path
// server most tests use.
func artifactHandler(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
	switch collection(r) {
	case "user":
		serveFixture(t, w, base, "user.json")
	case "defect":
		if wantsID(r, "DE1234") {
			serveFixture(t, w, base, "defect.json")
			return
		}
		serveFixture(t, w, base, "empty.json")
	case "hierarchicalrequirement":
		if wantsID(r, "US777") {
			serveFixture(t, w, base, "story.json")
			return
		}
		serveFixture(t, w, base, "empty.json")
	default:
		serveFixture(t, w, base, "empty.json")
	}
}

func sourceCode(t *testing.T, err error) source.Code {
	t.Helper()
	var se *source.Error
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *source.Error", err)
	}
	return se.Code
}

// --- New / config ---

func TestNewDefaultsAndValidation(t *testing.T) {
	if _, err := New(Config{}, nil); err == nil {
		t.Fatal("New without an apiKey succeeded, want an error")
	}

	c, err := New(Config{APIKey: "k"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.BaseURL != defaultBaseURL {
		t.Errorf("BaseURL = %q, want the rally1 default %q", c.cfg.BaseURL, defaultBaseURL)
	}
	if got, want := strings.Join(c.cfg.Types, ","), "Defect,HierarchicalRequirement"; got != want {
		t.Errorf("Types = %q, want %q", got, want)
	}

	c, err = New(Config{APIKey: "k", BaseURL: "https://eu1.rallydev.com/"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.BaseURL != "https://eu1.rallydev.com" {
		t.Errorf("BaseURL = %q, want the trailing slash trimmed", c.cfg.BaseURL)
	}
}

// --- Get: mapping ---

func TestGetDefect(t *testing.T) {
	fs := newServer(t, artifactHandler)
	c := newClient(t, fs, Config{Workspace: "/workspace/111", HelpdeskField: "c_ZendeskTicketID"})

	got, err := c.Get(context.Background(), "DE1234")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Key != "DE1234" {
		t.Errorf("Key = %q, want DE1234", got.Key)
	}
	if got.Title != "Payment webhook retries stall after the third attempt" {
		t.Errorf("Title = %q", got.Title)
	}
	// Defects carry State, not ScheduleState.
	if got.Status != "Submitted" {
		t.Errorf("Status = %q, want the defect's State (Submitted)", got.Status)
	}
	if got.Priority != "High Attention" {
		t.Errorf("Priority = %q", got.Priority)
	}
	if got.Assignee != "Ann Agent" {
		t.Errorf("Assignee = %q, want the Owner display name", got.Assignee)
	}
	if want := fs.URL + "/#/detail/defect/12345"; got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
	if got.HelpdeskRef != "8891" {
		t.Errorf("HelpdeskRef = %q, want the configured custom field's value", got.HelpdeskRef)
	}
	if want := time.Date(2026, 9, 1, 9, 15, 0, 0, time.UTC); !got.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want)
	}
	if want := time.Date(2026, 9, 9, 16, 40, 0, 0, time.UTC); !got.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, want)
	}
	if !strings.Contains(got.Description, "Webhook delivery") {
		t.Errorf("Description lost the body: %q", got.Description)
	}
	if !strings.Contains(got.Description, "Notes") || !strings.Contains(got.Description, "reproduces on staging") {
		t.Errorf("Description did not append Notes: %q", got.Description)
	}
	if strings.Contains(got.Description, "<p>") {
		t.Errorf("Description still carries HTML: %q", got.Description)
	}

	wantFields := map[string]string{
		"type":      "Defect",
		"project":   "Payments",
		"iteration": "Sprint 42",
		"release":   "2026.09",
		"tags":      "regression, payments",
		"parent":    "F42",
		"severity":  "Major Problem",
		"objectId":  "12345",
		"_ref":      fixtureBase + "/slm/webservice/v2.0/defect/12345",
	}
	for k, want := range wantFields {
		if k == "_ref" {
			want = fs.URL + "/slm/webservice/v2.0/defect/12345"
		}
		if got.Fields[k] != want {
			t.Errorf("Fields[%q] = %q, want %q", k, got.Fields[k], want)
		}
	}

	// The workspace scope and the explicit fetch list must be on the wire.
	qs := fs.queriesFor("defect")
	if len(qs) != 1 {
		t.Fatalf("defect queried %d times, want once", len(qs))
	}
	if got, want := qs[0].Get("query"), `(FormattedID = "DE1234")`; got != want {
		t.Errorf("query = %q, want %q", got, want)
	}
	if got := qs[0].Get("workspace"); got != "/workspace/111" {
		t.Errorf("workspace = %q, want the configured ref", got)
	}
	if got := qs[0].Get("pagesize"); got != "1" {
		t.Errorf("pagesize = %q, want 1", got)
	}
	if fetch := qs[0].Get("fetch"); !strings.Contains(fetch, "c_ZendeskTicketID") || strings.Contains(fetch, "true") {
		t.Errorf("fetch = %q, want the named field list including the helpdesk field", fetch)
	}
}

func TestGetStoryWithInlineImage(t *testing.T) {
	fs := newServer(t, artifactHandler)
	c := newClient(t, fs, Config{})

	got, err := c.Get(context.Background(), "US777")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Key != "US777" {
		t.Errorf("Key = %q, want US777", got.Key)
	}
	// Stories carry ScheduleState, not State.
	if got.Status != "In-Progress" {
		t.Errorf("Status = %q, want the story's ScheduleState", got.Status)
	}
	if got.Fields["type"] != "HierarchicalRequirement" {
		t.Errorf("Fields[type] = %q", got.Fields["type"])
	}
	if want := fs.URL + "/#/detail/hierarchicalrequirement/77700"; got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
	if got.HelpdeskRef != "" {
		t.Errorf("HelpdeskRef = %q, want empty with no helpdesk field configured", got.HelpdeskRef)
	}
	if want := "/slm/attachment/55501/screenshot.png"; got.Fields["inlineImages"] != want {
		t.Errorf("Fields[inlineImages] = %q, want %q", got.Fields["inlineImages"], want)
	}
	if !strings.Contains(got.Description, "List every attempt") {
		t.Errorf("Description lost the list items: %q", got.Description)
	}
	if !strings.Contains(got.Description, "/slm/attachment/55501/screenshot.png") {
		t.Errorf("Description lost the inline image: %q", got.Description)
	}

	// The US prefix routes straight at the story collection.
	if colls := fs.collections(); len(colls) != 1 || colls[0] != "hierarchicalrequirement" {
		t.Errorf("collections queried = %v, want only hierarchicalrequirement", colls)
	}
}

// --- Get: routing ---

func TestGetPrefixRoutingAndFallback(t *testing.T) {
	// TA55 has a Task prefix, so task is tried first; it is not a
	// configured type and holds nothing, so the sweep falls through the
	// configured types until the story collection answers.
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		if collection(r) == "hierarchicalrequirement" {
			serveFixture(t, w, base, "story.json")
			return
		}
		serveFixture(t, w, base, "empty.json")
	})
	c := newClient(t, fs, Config{})

	if _, err := c.Get(context.Background(), "TA55"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := []string{"task", "defect", "hierarchicalrequirement"}
	got := fs.collections()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("collections queried = %v, want %v", got, want)
	}
}

func TestPrefixType(t *testing.T) {
	cases := map[string]string{
		"DE1234": "defect",
		"DS7":    "defectsuite",
		"US777":  "hierarchicalrequirement",
		"TA55":   "task",
		"TC9":    "testcase",
		"F42":    "portfolioitem/feature",
		"de1234": "defect",
	}
	for key, want := range cases {
		got, ok := prefixType(key)
		if !ok {
			t.Errorf("prefixType(%q) found nothing, want %q", key, want)
			continue
		}
		if got.Path != want {
			t.Errorf("prefixType(%q) = %q, want %q", key, got.Path, want)
		}
	}
	// A key whose prefix is not followed by a digit is not a type hint.
	for _, key := range []string{"FOO", "X1", "", "DE"} {
		if got, ok := prefixType(key); ok {
			t.Errorf("prefixType(%q) = %q, want no match", key, got.Path)
		}
	}
}

func TestGetNotFound(t *testing.T) {
	fs := newServer(t, artifactHandler)
	c := newClient(t, fs, Config{})

	_, err := c.Get(context.Background(), "DE9999")
	if err == nil {
		t.Fatal("Get of a missing key succeeded, want NotFound")
	}
	if code := sourceCode(t, err); code != source.NotFound {
		t.Errorf("code = %q, want %q", code, source.NotFound)
	}
}

// --- error mapping ---

func TestQueryResultErrorsMapping(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    source.Code
	}{
		{"generic", "errors.json", source.Internal},
		{"unauthorised", "errors-auth.json", source.Auth},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
				serveFixture(t, w, base, tc.fixture)
			})
			c := newClient(t, fs, Config{})

			_, err := c.Get(context.Background(), "DE1234")
			if err == nil {
				t.Fatal("Get succeeded, want an error from QueryResult.Errors")
			}
			if code := sourceCode(t, err); code != tc.want {
				t.Errorf("code = %q, want %q", code, tc.want)
			}
		})
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	tests := []struct {
		status int
		want   source.Code
	}{
		{http.StatusUnauthorized, source.Auth},
		{http.StatusForbidden, source.Auth},
		{http.StatusNotFound, source.NotFound},
		{http.StatusTooManyRequests, source.RateLimited},
		{http.StatusInternalServerError, source.Internal},
	}
	for _, tc := range tests {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"OperationResult":{"Errors":["%d and the key _abc-secret-key was rejected"]}}`, tc.status)
			})
			c := newClient(t, fs, Config{})

			_, err := c.Get(context.Background(), "DE1234")
			if err == nil {
				t.Fatalf("Get succeeded on %d, want an error", tc.status)
			}
			if code := sourceCode(t, err); code != tc.want {
				t.Errorf("code = %q, want %q", code, tc.want)
			}
			if tc.status == http.StatusInternalServerError && !strings.Contains(err.Error(), "500") {
				t.Errorf("500 error lost the status: %v", err)
			}
		})
	}
}

func TestErrorMessageKeepsPathAndDropsQuery(t *testing.T) {
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", 500)))
	})
	c := newClient(t, fs, Config{})

	_, err := c.Get(context.Background(), "DE1234")
	if err == nil {
		t.Fatal("Get succeeded, want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/slm/webservice/v2.0/defect") {
		t.Errorf("error lost the path: %v", msg)
	}
	if strings.Contains(msg, testAPIKey) {
		t.Errorf("error leaked the credential: %v", msg)
	}
	if strings.Contains(msg, "FormattedID") {
		t.Errorf("error quoted the query string: %v", msg)
	}
	if n := strings.Count(msg, "x"); n > 200 {
		t.Errorf("error quoted %d body bytes, want at most 200", n)
	}
}

func TestRateLimitedHonoursOneRetryAfter(t *testing.T) {
	var calls int
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		serveFixture(t, w, base, "defect.json")
	})
	c := newClient(t, fs, Config{})

	// Retry-After: 0 is not a wait worth honouring, so the call reports
	// the rate limit rather than hammering the API.
	if _, err := c.Get(context.Background(), "DE1234"); err == nil {
		t.Fatal("Get succeeded, want RateLimited")
	} else if code := sourceCode(t, err); code != source.RateLimited {
		t.Errorf("code = %q, want %q", code, source.RateLimited)
	}

	if got := parseRetryAfter("5"); got != 5*time.Second {
		t.Errorf("parseRetryAfter(5) = %v, want 5s", got)
	}
	if got := parseRetryAfter("nonsense"); got != 0 {
		t.Errorf("parseRetryAfter(nonsense) = %v, want 0", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf("parseRetryAfter empty = %v, want 0", got)
	}
}

// --- Ping / me ---

func TestPingCachesCurrentUser(t *testing.T) {
	fs := newServer(t, artifactHandler)
	c := newClient(t, fs, Config{})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if c.userName != "ann@example.com" {
		t.Errorf("cached UserName = %q", c.userName)
	}
	if !strings.HasSuffix(c.userRef, "/user/9001") {
		t.Errorf("cached user ref = %q", c.userRef)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("second Ping: %v", err)
	}
	if n := len(fs.queriesFor("user")); n != 1 {
		t.Errorf("user endpoint hit %d times, want 1 (cached after the first)", n)
	}
}

func TestPingAuthFailure(t *testing.T) {
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := newClient(t, fs, Config{})

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping succeeded on 401, want an error")
	}
	if code := sourceCode(t, err); code != source.Auth {
		t.Errorf("code = %q, want %q", code, source.Auth)
	}
}

func TestListResolvesMe(t *testing.T) {
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		switch collection(r) {
		case "user":
			serveFixture(t, w, base, "user.json")
		case "defect":
			serveFixture(t, w, base, "defect.json")
		default:
			serveFixture(t, w, base, "story.json")
		}
	})
	c := newClient(t, fs, Config{Project: "/project/301"})

	got, err := c.List(context.Background(), source.ListFilter{Assignee: "me", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d tickets, want 2 (one per configured type)", len(got))
	}
	// Newest updated first, across types.
	if got[0].Key != "US777" || got[1].Key != "DE1234" {
		t.Errorf("List order = %s,%s, want US777,DE1234 (newest LastUpdateDate first)", got[0].Key, got[1].Key)
	}

	qs := fs.queriesFor("defect")
	if len(qs) == 0 {
		t.Fatal("defect was never queried")
	}
	q := qs[0].Get("query")
	if !strings.Contains(q, `(Owner.UserName = "ann@example.com")`) {
		t.Errorf("query %q did not resolve me to the current user", q)
	}
	if !strings.Contains(q, `(State != "Closed")`) {
		t.Errorf("query %q lost the open-ish default for a defect", q)
	}
	if got := qs[0].Get("order"); got != "LastUpdateDate DESC" {
		t.Errorf("order = %q, want LastUpdateDate DESC", got)
	}
	if got := qs[0].Get("project"); got != "/project/301" {
		t.Errorf("project = %q, want the configured project scope on List", got)
	}

	// Stories are filtered on ScheduleState, not State.
	sq := fs.queriesFor("hierarchicalrequirement")
	if len(sq) == 0 {
		t.Fatal("hierarchicalrequirement was never queried")
	}
	// Stock Rally has no Closed ScheduleState; a story finishes at
	// Accepted, so that is what the open-ish default excludes.
	if q := sq[0].Get("query"); !strings.Contains(q, `(ScheduleState != "Accepted")`) {
		t.Errorf("story query %q did not exclude the story terminal state", q)
	}
}

func TestListFilters(t *testing.T) {
	tests := []struct {
		name   string
		filter source.ListFilter
		typ    artifactType
		want   []string
	}{
		{
			name:   "explicit status",
			filter: source.ListFilter{Status: "Fixed"},
			typ:    artifactType{"Defect", "defect"},
			want:   []string{`(State = "Fixed")`},
		},
		{
			name:   "named assignee",
			filter: source.ListFilter{Assignee: "bo@example.com"},
			typ:    artifactType{"Defect", "defect"},
			want:   []string{`(Owner.UserName = "bo@example.com")`, `(State != "Closed")`},
		},
		{
			name:   "story parent",
			filter: source.ListFilter{Parent: "US100"},
			typ:    artifactType{"HierarchicalRequirement", "hierarchicalrequirement"},
			want:   []string{`(Parent.FormattedID = "US100")`},
		},
		{
			name:   "feature parent on a story uses Feature",
			filter: source.ListFilter{Parent: "F42"},
			typ:    artifactType{"HierarchicalRequirement", "hierarchicalrequirement"},
			want:   []string{`(Feature.FormattedID = "F42")`},
		},
		{
			name:   "feature parent on a defect stays Parent",
			filter: source.ListFilter{Parent: "F42"},
			typ:    artifactType{"Defect", "defect"},
			want:   []string{`(Parent.FormattedID = "F42")`},
		},
		{
			name:   "story open-ish default excludes Accepted",
			filter: source.ListFilter{},
			typ:    artifactType{"HierarchicalRequirement", "hierarchicalrequirement"},
			want:   []string{`(ScheduleState != "Accepted")`},
		},
	}
	c, err := New(Config{APIKey: "k"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := c.listQuery(tc.typ, tc.filter, "")
			if err != nil {
				t.Fatalf("listQuery: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(q, want) {
					t.Errorf("listQuery = %q, want it to contain %q", q, want)
				}
			}
		})
	}
}

func TestListPaginatesAndStopsAtLimit(t *testing.T) {
	// 5 defects exist; a Limit of 3 must stop the sweep there.
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		if collection(r) != "defect" {
			serveFixture(t, w, base, "empty.json")
			return
		}
		writeGeneratedDefects(t, w, r, 5)
	})
	c := newClient(t, fs, Config{Types: []string{"Defect"}})

	got, err := c.List(context.Background(), source.ListFilter{Limit: 3})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List returned %d tickets, want 3", len(got))
	}
	qs := fs.queriesFor("defect")
	if got := qs[0].Get("pagesize"); got != "3" {
		t.Errorf("pagesize = %q, want the limit (3)", got)
	}
	if got := qs[0].Get("start"); got != "1" {
		t.Errorf("start = %q, want 1 (WSAPI start is 1-based)", got)
	}
}

// writeGeneratedDefects serves a page of synthetic defects honouring the
// request's start and pagesize, so paging behaviour can be asserted.
func writeGeneratedDefects(t *testing.T, w http.ResponseWriter, r *http.Request, total int) {
	t.Helper()
	start, _ := strconv.Atoi(r.URL.Query().Get("start"))
	if start < 1 {
		start = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("pagesize"))
	if size < 1 {
		size = 20
	}
	var results []map[string]any
	for i := start; i < start+size && i <= total; i++ {
		results = append(results, map[string]any{
			"_ref":           fmt.Sprintf("%s/slm/webservice/v2.0/defect/%d", r.Host, i),
			"_type":          "Defect",
			"ObjectID":       i,
			"FormattedID":    fmt.Sprintf("DE%d", i),
			"Name":           fmt.Sprintf("defect %d", i),
			"State":          "Open",
			"CreationDate":   "2026-09-01T00:00:00.000Z",
			"LastUpdateDate": fmt.Sprintf("2026-09-%02dT00:00:00.000Z", 30-i),
		})
	}
	body, err := json.Marshal(map[string]any{"QueryResult": map[string]any{
		"Errors": []string{}, "Warnings": []string{},
		"TotalResultCount": total, "StartIndex": start, "PageSize": size,
		"Results": results,
	}})
	if err != nil {
		t.Fatalf("marshal generated page: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		t.Errorf("write generated page: %v", err)
	}
}

// --- discussion ---

func TestThreadsOrdering(t *testing.T) {
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		if collection(r) == "conversationpost" {
			serveFixture(t, w, base, "conversationposts.json")
			return
		}
		artifactHandler(t, w, r, base)
	})
	c := newClient(t, fs, Config{})

	thread, err := c.Helpdesk().Threads(context.Background(), "DE1234")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(thread) != 3 {
		t.Fatalf("Threads returned %d messages, want 3", len(thread))
	}
	// The fixture is deliberately out of order; the thread is not.
	wantAuthors := []string{"Ann Agent", "Cy Reviewer", "Bo Builder"}
	for i, want := range wantAuthors {
		if thread[i].Author != want {
			t.Errorf("message %d author = %q, want %q", i, thread[i].Author, want)
		}
		if i > 0 && thread[i].At.Before(thread[i-1].At) {
			t.Errorf("message %d (%v) precedes message %d (%v)", i, thread[i].At, i-1, thread[i-1].At)
		}
		// Rally cannot tell a customer from an agent on a post.
		if thread[i].Role != "agent" {
			t.Errorf("message %d role = %q, want agent", i, thread[i].Role)
		}
	}
	if !strings.Contains(thread[0].Text, "reproduced on staging") {
		t.Errorf("message text lost the body: %q", thread[0].Text)
	}
	if strings.Contains(thread[2].Text, "<strong>") {
		t.Errorf("message text still carries HTML: %q", thread[2].Text)
	}

	q := fs.queriesFor("conversationpost")[0]
	if want := `(Artifact = "` + fs.URL + `/slm/webservice/v2.0/defect/12345")`; q.Get("query") != want {
		t.Errorf("query = %q, want %q", q.Get("query"), want)
	}
	if q.Get("order") != "CreationDate ASC" {
		t.Errorf("order = %q, want CreationDate ASC", q.Get("order"))
	}
	if q.Get("fetch") != "Text,User,CreationDate" {
		t.Errorf("fetch = %q, want Text,User,CreationDate", q.Get("fetch"))
	}
	if q.Get("pagesize") != "200" {
		t.Errorf("pagesize = %q, want 200 (the WSAPI ceiling)", q.Get("pagesize"))
	}
}

func TestThreadsPagination(t *testing.T) {
	const total = 250
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		if collection(r) != "conversationpost" {
			artifactHandler(t, w, r, base)
			return
		}
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		size, _ := strconv.Atoi(r.URL.Query().Get("pagesize"))
		if size != maxPageSize {
			t.Errorf("pagesize = %d, want %d", size, maxPageSize)
		}
		var results []map[string]any
		for i := start; i < start+size && i <= total; i++ {
			results = append(results, map[string]any{
				"_type":        "ConversationPost",
				"ObjectID":     i,
				"Text":         fmt.Sprintf("<p>post %d</p>", i),
				"User":         map[string]any{"_refObjectName": "Ann Agent"},
				"CreationDate": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			})
		}
		body, _ := json.Marshal(map[string]any{"QueryResult": map[string]any{
			"Errors": []string{}, "Warnings": []string{},
			"TotalResultCount": total, "StartIndex": start, "PageSize": size,
			"Results": results,
		}})
		_, _ = w.Write(body)
	})
	c := newClient(t, fs, Config{})

	thread, err := c.Helpdesk().Threads(context.Background(), "DE1234")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(thread) != total {
		t.Fatalf("Threads returned %d messages, want %d", len(thread), total)
	}
	starts := []string{}
	for _, q := range fs.queriesFor("conversationpost") {
		starts = append(starts, q.Get("start"))
	}
	if want := "1,201"; strings.Join(starts, ",") != want {
		t.Errorf("start values = %v, want %s", starts, want)
	}
}

// --- attachments ---

func attachmentHandler(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
	coll := collection(r)
	switch {
	case coll == "attachment":
		serveFixture(t, w, base, "attachments.json")
	case strings.HasPrefix(coll, "attachmentcontent/"):
		if got := r.URL.Query().Get("fetch"); got != "Content" {
			t.Errorf("attachmentcontent fetch = %q, want Content", got)
		}
		var byID map[string]json.RawMessage
		if err := json.Unmarshal([]byte(strings.ReplaceAll(fixture(t, "attachmentcontent.json"), fixtureBase, base)), &byID); err != nil {
			t.Fatalf("parse attachmentcontent.json: %v", err)
		}
		body, ok := byID[strings.TrimPrefix(coll, "attachmentcontent/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	default:
		artifactHandler(t, w, r, base)
	}
}

func TestAttachments(t *testing.T) {
	fs := newServer(t, attachmentHandler)
	c := newClient(t, fs, Config{})
	dir := filepath.Join(t.TempDir(), "attachments")

	got, err := c.Helpdesk().Attachments(context.Background(), "DE1234", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Attachments returned %d files, want 2 (the third has no content)", len(got))
	}

	if got[0].Name != "screenshot.png" || got[0].Path != "attachments/1-screenshot.png" || got[0].MIME != "image/png" {
		t.Errorf("attachment 0 = %+v", got[0])
	}
	// "../../etc/passwd" must not escape the destination directory.
	if got[1].Name != "passwd" || got[1].Path != "attachments/2-passwd" {
		t.Errorf("attachment 1 = %+v, want the traversal stripped", got[1])
	}

	body, err := os.ReadFile(filepath.Join(dir, "1-screenshot.png"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(body) != "hello rally" {
		t.Errorf("decoded content = %q, want the base64 payload decoded", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "2-passwd")); err != nil {
		t.Errorf("second attachment not written: %v", err)
	}

	// The attachment that carried no Content reference is a warning, not
	// a failure, and it is filed under the ticket it belongs to.
	warnings := c.WarningsFor("DE1234")
	if len(warnings) != 1 {
		t.Fatalf("WarningsFor(DE1234) = %v, want 1 entry", warnings)
	}
	if !strings.Contains(warnings[0], "55503") {
		t.Errorf("warning does not name the failing attachment: %q", warnings[0])
	}
	if again := c.WarningsFor("DE1234"); len(again) != 0 {
		t.Errorf("warnings survived being read: %v", again)
	}
	if other := c.WarningsFor("US777"); len(other) != 0 {
		t.Errorf("another ticket inherited the warnings: %v", other)
	}
}

func TestAttachmentsAllFail(t *testing.T) {
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		coll := collection(r)
		switch {
		case coll == "attachment":
			serveFixture(t, w, base, "attachments.json")
		case strings.HasPrefix(coll, "attachmentcontent/"):
			w.WriteHeader(http.StatusInternalServerError)
		default:
			artifactHandler(t, w, r, base)
		}
	})
	c := newClient(t, fs, Config{})

	got, err := c.Helpdesk().Attachments(context.Background(), "DE1234", filepath.Join(t.TempDir(), "a"))
	if err == nil {
		t.Fatal("Attachments succeeded with every download failing, want an error")
	}
	if len(got) != 0 {
		t.Errorf("Attachments returned %d files, want none", len(got))
	}
	// The failures are in the error, so they are not repeated as warnings.
	if w := c.WarningsFor("DE1234"); len(w) != 0 {
		t.Errorf("WarningsFor = %v, want none when the error already carries them", w)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"screenshot.png":       "screenshot.png",
		"../../etc/passwd":     "passwd",
		`..\..\windows\me.txt`: "me.txt",
		"":                     "attachment",
		"..":                   "attachment",
		"/":                    "attachment",
		"a\x00b.txt":           "ab.txt",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("n", 300) + ".png"
	got := sanitizeName(long)
	if len(got) > 120 {
		t.Errorf("sanitizeName kept %d bytes, want at most 120", len(got))
	}
	if !strings.HasSuffix(got, ".png") {
		t.Errorf("sanitizeName lost the extension: %q", got)
	}
}

func TestDecodeBase64(t *testing.T) {
	b, err := decodeBase64("aGVsbG8g\ncmFsbHk=", maxAttachmentBytes)
	if err != nil {
		t.Fatalf("decodeBase64: %v", err)
	}
	if string(b) != "hello rally" {
		t.Errorf("decodeBase64 = %q", b)
	}
	if _, err := decodeBase64("!!!not base64!!!", maxAttachmentBytes); err == nil {
		t.Error("decodeBase64 accepted a non-base64 payload")
	}
	// The size check happens on the encoded length, before decoding.
	if _, err := decodeBase64("aGVsbG8gcmFsbHk=", 4); err == nil {
		t.Error("decodeBase64 accepted a payload past its limit")
	}
}

// --- helpdesk view ---

func TestHelpdeskGet(t *testing.T) {
	fs := newServer(t, artifactHandler)
	c := newClient(t, fs, Config{})

	got, err := c.Helpdesk().Get(context.Background(), "DE1234")
	if err != nil {
		t.Fatalf("Helpdesk().Get: %v", err)
	}
	if got.ID != "DE1234" || got.Subject == "" || got.Status != "Submitted" {
		t.Errorf("helpdesk ticket = %+v", got)
	}
	if got.Customer != "Payments" {
		t.Errorf("Customer = %q, want the project name", got.Customer)
	}
}

// refURL keeps a payload from steering an authenticated request at another
// host: only the path survives.
func TestRefURLPinsTheConfiguredHost(t *testing.T) {
	c, err := New(Config{APIKey: "k", BaseURL: "https://rally1.rallydev.com"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.refURL("https://evil.example.com/slm/webservice/v2.0/attachmentcontent/1")
	if err != nil {
		t.Fatalf("refURL: %v", err)
	}
	if want := "https://rally1.rallydev.com/slm/webservice/v2.0/attachmentcontent/1"; got != want {
		t.Errorf("refURL = %q, want %q", got, want)
	}
}

func TestInterfaces(t *testing.T) {
	c, err := New(Config{APIKey: "k"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var _ source.Tracker = c
	var _ source.Warner = c
	var _ source.Helpdesk = c.Helpdesk()
	if _, ok := c.Helpdesk().(source.Warner); !ok {
		t.Error("the helpdesk view does not implement source.Warner")
	}
}

// An unbounded List is not on offer: no Limit means defaultListLimit, and
// an outsized Limit is cut to maxListLimit.
func TestListLimitBounds(t *testing.T) {
	tests := []struct {
		name         string
		limit        int
		wantResults  int
		wantPageSize string
	}{
		{"zero means the default", 0, defaultListLimit, strconv.Itoa(defaultListLimit)},
		{"oversized is capped", 5000, maxListLimit, strconv.Itoa(maxListLimit)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
				if collection(r) != "defect" {
					serveFixture(t, w, base, "empty.json")
					return
				}
				writeGeneratedDefects(t, w, r, 1000)
			})
			c := newClient(t, fs, Config{Types: []string{"Defect"}})

			got, err := c.List(context.Background(), source.ListFilter{Limit: tc.limit})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got) != tc.wantResults {
				t.Errorf("List returned %d tickets, want %d", len(got), tc.wantResults)
			}
			qs := fs.queriesFor("defect")
			if got := qs[0].Get("pagesize"); got != tc.wantPageSize {
				t.Errorf("pagesize = %q, want %q", got, tc.wantPageSize)
			}
			if got := qs[0].Get("start"); got != "1" {
				t.Errorf("start = %q, want 1 (WSAPI start is 1-based)", got)
			}
		})
	}
}

// One Client serves every ticket in a run. Two tickets fetched at the same
// time must not be handed each other's warnings — which is what keying them
// by id buys, and what -race here checks.
func TestWarningsAreKeyedPerTicketUnderConcurrency(t *testing.T) {
	fs := newServer(t, attachmentHandler)
	c := newClient(t, fs, Config{})

	ids := []string{"DE1234", "US777"}
	warnings := map[string][]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			dir := filepath.Join(t.TempDir(), "attachments")
			if _, err := c.Helpdesk().Attachments(context.Background(), id, dir); err != nil {
				t.Errorf("Attachments(%s): %v", id, err)
				return
			}
			w := c.WarningsFor(id)
			mu.Lock()
			warnings[id] = w
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	for _, id := range ids {
		if got := warnings[id]; len(got) != 1 {
			t.Errorf("ticket %s warnings = %v, want exactly 1", id, got)
		}
	}
}

// --- query-value validation ---

// A value interpolated into query=(...) that carries a parenthesis, a
// quote, a backslash or a control character cannot be made safe — WSAPI
// documents no escape sequence inside a quoted literal — so it is refused
// rather than silently rewritten.
func TestQueryValuesAreRejectedNotStripped(t *testing.T) {
	rejected := map[string]string{
		"open paren":       `DE1234") OR (1 = 1`,
		"close paren":      `ann)`,
		"double quote":     `ann" OR "x`,
		"backslash":        `ann\x`,
		"newline":          "ann\nx",
		"carriage return":  "ann\rx",
		"null":             "ann\x00x",
		"delete character": "ann\x7fx",
	}
	for name, value := range rejected {
		t.Run(name, func(t *testing.T) {
			if err := checkQueryValue("assignee", value); err == nil {
				t.Fatalf("checkQueryValue(%q) accepted it, want a rejection", value)
			} else if code := sourceCode(t, err); code != source.Internal {
				t.Errorf("code = %q, want %q", code, source.Internal)
			}
		})
	}
	for _, ok := range []string{"ann@example.com", "Ann Agent", "https://rally.example.com/slm/webservice/v2.0/defect/1", "In-Progress"} {
		if err := checkQueryValue("value", ok); err != nil {
			t.Errorf("checkQueryValue(%q) = %v, want it accepted", ok, err)
		}
	}
}

func TestFormattedIDsAreValidated(t *testing.T) {
	for _, ok := range []string{"DE1234", "US777", "F42", "de1234", "TC9", "ABCD123456789"} {
		if err := checkFormattedID("ticket key", ok); err != nil {
			t.Errorf("checkFormattedID(%q) = %v, want it accepted", ok, err)
		}
	}
	for _, bad := range []string{"", "DE", "1234", `DE1234") OR (1 = 1`, "DE 1234", "TOOLONG1", "DE1234567890", "DE-12"} {
		if err := checkFormattedID("ticket key", bad); err == nil {
			t.Errorf("checkFormattedID(%q) accepted it, want a rejection", bad)
		}
	}
}

// A rejected value must never reach the wire.
func TestGetRejectsAMalformedKeyBeforeCallingOut(t *testing.T) {
	fs := newServer(t, artifactHandler)
	c := newClient(t, fs, Config{})

	_, err := c.Get(context.Background(), `DE1234") OR (FormattedID != "`)
	if err == nil {
		t.Fatal("Get accepted an injected key, want a rejection")
	}
	if code := sourceCode(t, err); code != source.Internal {
		t.Errorf("code = %q, want %q", code, source.Internal)
	}
	if !strings.Contains(err.Error(), "invalid query value") {
		t.Errorf("error = %v, want it to name the invalid value", err)
	}
	if n := len(fs.requests()); n != 0 {
		t.Errorf("%d requests were made, want none", n)
	}
}

func TestListRejectsMalformedFilters(t *testing.T) {
	tests := map[string]source.ListFilter{
		"assignee": {Assignee: `ann") OR (1 = 1`},
		"status":   {Status: `Open") OR (1 = 1`},
		"parent":   {Parent: `US1") OR (1 = 1`},
	}
	for name, filter := range tests {
		t.Run(name, func(t *testing.T) {
			fs := newServer(t, artifactHandler)
			c := newClient(t, fs, Config{})

			if _, err := c.List(context.Background(), filter); err == nil {
				t.Fatal("List accepted an injected filter, want a rejection")
			} else if code := sourceCode(t, err); code != source.Internal {
				t.Errorf("code = %q, want %q", code, source.Internal)
			}
			for _, u := range fs.requests() {
				if q := u.Query().Get("query"); strings.Contains(q, "1 = 1") {
					t.Errorf("an injected clause reached the wire: %q", q)
				}
			}
		})
	}
}

// --- body ceilings ---

func TestReadLimitedFailsClosed(t *testing.T) {
	got, err := readLimited(strings.NewReader("hello"), 16)
	if err != nil {
		t.Fatalf("readLimited: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("readLimited = %q, want the whole body", got)
	}
	if _, err := readLimited(strings.NewReader("hello"), 4); err == nil {
		t.Error("readLimited accepted a body past its limit, want an error")
	}
	// A body exactly at the limit is not over it.
	if _, err := readLimited(strings.NewReader("hello"), 5); err != nil {
		t.Errorf("readLimited at exactly the limit = %v, want no error", err)
	}
}

func TestOversizedJSONResponseIsRefused(t *testing.T) {
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		// A well-formed envelope whose padding pushes it past the
		// JSON ceiling: it must not be decoded as a short result.
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"QueryResult":{"Errors":[],"Warnings":[],"TotalResultCount":1,"Results":[{"FormattedID":"DE1234","Name":"%s"}]}}`,
			strings.Repeat("x", maxJSONBody+1024))
	})
	c := newClient(t, fs, Config{})

	_, err := c.Get(context.Background(), "DE1234")
	if err == nil {
		t.Fatal("Get accepted an oversized body, want an error")
	}
	if code := sourceCode(t, err); code != source.Internal {
		t.Errorf("code = %q, want %q", code, source.Internal)
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %v, want it to name the limit", err)
	}
}

func TestOversizedAttachmentContentIsRefused(t *testing.T) {
	fs := newServer(t, func(t *testing.T, w http.ResponseWriter, r *http.Request, base string) {
		coll := collection(r)
		switch {
		case coll == "attachment":
			serveFixture(t, w, base, "attachments.json")
		case strings.HasPrefix(coll, "attachmentcontent/"):
			// Base64 well past what a 50 MB attachment can encode
			// to would exceed the decoded ceiling; the encoded
			// length is what gets checked, before any allocation.
			fmt.Fprintf(w, `{"AttachmentContent":{"Content":"%s"}}`, strings.Repeat("QQ==", 1))
		default:
			artifactHandler(t, w, r, base)
		}
	})
	c := newClient(t, fs, Config{})

	// Sanity: the small payload still works, so the ceiling below is the
	// only thing the oversized case trips.
	if _, err := c.Helpdesk().Attachments(context.Background(), "DE1234", filepath.Join(t.TempDir(), "a")); err != nil {
		t.Fatalf("Attachments with a small payload: %v", err)
	}

	// The AttachmentContent request reads under its own, larger ceiling,
	// and that read fails closed the same way — exercised here with a
	// small limit rather than a 64 MiB fixture.
	var out attachmentContentResult
	err := c.getLimited(context.Background(), fs.URL+wsPrefix+"attachmentcontent/55501", 8, &out)
	if err == nil {
		t.Fatal("getLimited accepted a body past its limit, want an error")
	}
	if code := sourceCode(t, err); code != source.Internal {
		t.Errorf("code = %q, want %q", code, source.Internal)
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %v, want it to name the limit", err)
	}
	if maxAttachmentBody <= maxAttachmentBytes {
		t.Errorf("maxAttachmentBody (%d) must exceed the decoded ceiling (%d) to allow for base64 inflation", maxAttachmentBody, maxAttachmentBytes)
	}
}
