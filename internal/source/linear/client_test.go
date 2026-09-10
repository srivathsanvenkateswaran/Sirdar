package linear

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
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

const testKey = "lin_api_secret"

// --- Test harness ---

// gqlRequest is one decoded GraphQL request the fake server received.
type gqlRequest struct {
	Op        string
	Query     string
	Variables map[string]any
}

// fakeLinear is an httptest server that answers GraphQL POSTs by operation
// name and serves Linear upload downloads on any other path. Every request's
// Authorization header is checked against the configured API key.
type fakeLinear struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	requests  []gqlRequest
	downloads []string

	// respond answers one GraphQL operation. Returning false means "no
	// handler for this operation", which fails the test.
	respond func(w http.ResponseWriter, req gqlRequest) bool
	// download answers a file fetch; nil serves a small body with a
	// text/plain content type.
	download func(w http.ResponseWriter, r *http.Request)
}

var opNameRe = regexp.MustCompile(`(?s)query\s+(\w+)`)

func newFakeLinear(t *testing.T, respond func(w http.ResponseWriter, req gqlRequest) bool) *fakeLinear {
	t.Helper()
	f := &fakeLinear{t: t, respond: respond}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLinear) serve(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != testKey {
		f.t.Errorf("Authorization header = %q, want the raw API key %q (path %s)", got, testKey, r.URL.Path)
	}

	if r.Method != http.MethodPost {
		f.mu.Lock()
		f.downloads = append(f.downloads, r.URL.String())
		f.mu.Unlock()
		if f.download != nil {
			f.download(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "file body")
		return
	}

	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("decode request body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	req := gqlRequest{Query: body.Query, Variables: body.Variables}
	if m := opNameRe.FindStringSubmatch(body.Query); m != nil {
		req.Op = m[1]
	}

	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if !f.respond(w, req) {
		f.t.Errorf("unexpected operation %q", req.Op)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (f *fakeLinear) ops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.requests))
	for i, r := range f.requests {
		out[i] = r.Op
	}
	return out
}

func (f *fakeLinear) request(i int) gqlRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.requests) {
		f.t.Fatalf("wanted request %d, only %d were made", i, len(f.requests))
	}
	return f.requests[i]
}

func (f *fakeLinear) downloadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.downloads)
}

// uploadRewrite sends requests aimed at Linear's upload host to the test
// server instead, leaving the URLs in the fixtures exactly as Linear writes
// them.
type uploadRewrite struct{ host string }

func (t uploadRewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.EqualFold(r.URL.Hostname(), uploadHost) {
		r = r.Clone(r.Context())
		r.URL.Scheme = "http"
		r.URL.Host = t.host
	}
	return http.DefaultTransport.RoundTrip(r)
}

func newTestClient(t *testing.T, f *fakeLinear, teamKey string) *Client {
	t.Helper()
	base, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	c, err := New(Config{APIKey: testKey, TeamKey: teamKey}, &http.Client{Transport: uploadRewrite{host: base.Host}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.Endpoint = f.srv.URL + "/graphql"
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

// serveFixtures answers each operation from a fixture file.
func serveFixtures(t *testing.T, byOp map[string]string) func(http.ResponseWriter, gqlRequest) bool {
	return func(w http.ResponseWriter, req gqlRequest) bool {
		name, ok := byOp[req.Op]
		if !ok {
			return false
		}
		w.Write(fixture(t, name))
		return true
	}
}

// dig walks a decoded JSON object, failing the test if the path is absent.
func dig(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for i, key := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("dig %v: %q is not an object", path, strings.Join(path[:i], "."))
		}
		cur, ok = obj[key]
		if !ok {
			t.Fatalf("dig %v: missing key %q", path, key)
		}
	}
	return cur
}

func sourceCode(t *testing.T, err error) source.Code {
	t.Helper()
	var se *source.Error
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *source.Error", err)
	}
	return se.Code
}

// --- New ---

func TestNewRejectsEmptyAPIKey(t *testing.T) {
	if _, err := New(Config{}, nil); err == nil {
		t.Fatal("New with no API key: want an error, got nil")
	} else if got := sourceCode(t, err); got != source.Auth {
		t.Errorf("code = %q, want %q", got, source.Auth)
	}
}

func TestNewDefaults(t *testing.T) {
	c, err := New(Config{APIKey: "  k  ", TeamKey: " ENG "}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Endpoint != DefaultEndpoint {
		t.Errorf("Endpoint = %q, want %q", c.Endpoint, DefaultEndpoint)
	}
	if c.APIKey != "k" || c.TeamKey != "ENG" {
		t.Errorf("APIKey/TeamKey = %q/%q, want %q/%q", c.APIKey, c.TeamKey, "k", "ENG")
	}
	if c.HTTP == nil {
		t.Error("HTTP client is nil")
	}
}

// --- Get ---

func TestGetMapsIssue(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"Issue": "issue.json"}))
	c := newTestClient(t, f, "")

	got, err := c.Get(context.Background(), "ENG-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	req := f.request(0)
	if req.Op != "Issue" {
		t.Errorf("operation = %q, want %q", req.Op, "Issue")
	}
	if id := req.Variables["id"]; id != "ENG-123" {
		t.Errorf("variables.id = %v, want ENG-123 (the human identifier, not a UUID lookup)", id)
	}
	if strings.Contains(req.Query, "Bearer") {
		t.Error("query mentions Bearer; the personal API key goes in Authorization raw")
	}

	want := ticket.TrackerTicket{
		Key:         "ENG-123",
		Title:       "Checkout returns 500 on card retry",
		Priority:    "High",
		Status:      "In Progress",
		Assignee:    "Dana Okoro",
		URL:         "https://linear.app/acme/issue/ENG-123/checkout-returns-500-on-card-retry",
		HelpdeskRef: "https://acme.zendesk.com/agent/tickets/4242",
	}
	if got.Key != want.Key || got.Title != want.Title || got.Priority != want.Priority ||
		got.Status != want.Status || got.Assignee != want.Assignee || got.URL != want.URL {
		t.Errorf("mapped ticket = %+v, want the fixture's fields %+v", got, want)
	}
	if got.HelpdeskRef != want.HelpdeskRef {
		t.Errorf("HelpdeskRef = %q, want the zendesk attachment URL %q", got.HelpdeskRef, want.HelpdeskRef)
	}
	if !strings.HasPrefix(got.Description, "Customer cannot complete checkout") {
		t.Errorf("Description = %q, want the Markdown passed through unchanged", got.Description)
	}
	if want := time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC); !got.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want)
	}
	if want := time.Date(2026, 9, 9, 17, 5, 12, 0, time.UTC); !got.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, want)
	}

	wantFields := map[string]string{
		"team":             "ENG",
		"project":          "Payments hardening",
		"labels":           "bug, payments",
		"priorityLabel":    "High",
		"priority":         "2",
		"stateType":        "started",
		"parent":           "ENG-100",
		"customerRequests": "2",
	}
	for k, v := range wantFields {
		if got.Fields[k] != v {
			t.Errorf("Fields[%q] = %q, want %q", k, got.Fields[k], v)
		}
	}
	if w := c.WarningsFor("ENG-123"); len(w) != 0 {
		t.Errorf("WarningsFor(ENG-123) = %v, want none", w)
	}
}

func TestGetNotFoundWhenIssueIsNull(t *testing.T) {
	f := newFakeLinear(t, func(w http.ResponseWriter, req gqlRequest) bool {
		w.Write([]byte(`{"data":{"issue":null}}`))
		return true
	})
	c := newTestClient(t, f, "")

	_, err := c.Get(context.Background(), "ENG-404")
	if err == nil {
		t.Fatal("Get: want an error for a null issue, got nil")
	}
	if got := sourceCode(t, err); got != source.NotFound {
		t.Errorf("code = %q, want %q", got, source.NotFound)
	}
}

func TestGetPriorityLabels(t *testing.T) {
	labels := map[int]string{0: "No priority", 1: "Urgent", 2: "High", 3: "Medium", 4: "Low"}
	for p := 0; p <= 4; p++ {
		p, label := p, labels[p]
		t.Run(label, func(t *testing.T) {
			f := newFakeLinear(t, func(w http.ResponseWriter, req gqlRequest) bool {
				fmt.Fprintf(w, `{"data":{"issue":{"identifier":"ENG-1","title":"t","priority":%d,"priorityLabel":%q,`+
					`"state":{"name":"Todo","type":"unstarted"},"labels":{"nodes":[]},"attachments":{"nodes":[]}}}}`, p, label)
				return true
			})
			c := newTestClient(t, f, "")

			got, err := c.Get(context.Background(), "ENG-1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Priority != label {
				t.Errorf("Priority = %q, want the label %q", got.Priority, label)
			}
			if want := fmt.Sprint(p); got.Fields["priority"] != want {
				t.Errorf("Fields[priority] = %q, want the raw %q", got.Fields["priority"], want)
			}
		})
	}
}

func TestGetRetriesWithoutCustomerNeeds(t *testing.T) {
	f := newFakeLinear(t, func(w http.ResponseWriter, req gqlRequest) bool {
		if strings.Contains(req.Query, "customerNeeds") {
			w.Write([]byte(`{"errors":[{"message":"Cannot query field \"customerNeeds\" on type \"Issue\".","extensions":{"type":"GRAPHQL_VALIDATION_FAILED"}}]}`))
			return true
		}
		w.Write([]byte(strings.Replace(string(fixture(t, "issue.json")),
			`"customerNeeds": { "nodes": [{ "id": "need-1" }, { "id": "need-2" }] }`, `"x": 1`, 1)))
		return true
	})
	c := newTestClient(t, f, "")

	got, err := c.Get(context.Background(), "ENG-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ops := f.ops(); len(ops) != 2 {
		t.Fatalf("operations = %v, want the query retried once without customerNeeds", ops)
	}
	if _, ok := got.Fields["customerRequests"]; ok {
		t.Errorf("Fields[customerRequests] = %q, want it absent when the field is not available", got.Fields["customerRequests"])
	}
	warns := c.WarningsFor("ENG-123")
	if len(warns) != 1 || !strings.Contains(warns[0], "customerNeeds") {
		t.Errorf("WarningsFor(ENG-123) = %v, want one mentioning customerNeeds", warns)
	}
	if again := c.WarningsFor("ENG-123"); len(again) != 0 {
		t.Errorf("WarningsFor(ENG-123) after reading = %v, want none", again)
	}
	if other := c.WarningsFor("ENG-999"); len(other) != 0 {
		t.Errorf("another ticket inherited the warning: %v", other)
	}
}

// --- List ---

func TestListPaginatesAcrossPages(t *testing.T) {
	page := 0
	f := newFakeLinear(t, func(w http.ResponseWriter, req gqlRequest) bool {
		if req.Op != "Issues" {
			return false
		}
		page++
		if page == 1 {
			w.Write(fixture(t, "issues_page1.json"))
		} else {
			w.Write(fixture(t, "issues_page2.json"))
		}
		return true
	})
	c := newTestClient(t, f, "ENG")

	got, err := c.List(context.Background(), source.ListFilter{Assignee: "me"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d issues, want 3 across two pages", len(got))
	}
	if got[0].Key != "ENG-201" || got[2].Key != "ENG-203" {
		t.Errorf("keys = %q..%q, want ENG-201..ENG-203 in page order", got[0].Key, got[2].Key)
	}

	first := f.request(0)
	if v := dig(t, first.Variables, "filter", "assignee", "isMe", "eq"); v != true {
		t.Errorf("filter.assignee.isMe.eq = %v, want true for the me assignee", v)
	}
	nin := dig(t, first.Variables, "filter", "state", "type", "nin")
	if fmt.Sprint(nin) != "[completed canceled]" {
		t.Errorf("filter.state.type.nin = %v, want [completed canceled]", nin)
	}
	if v := dig(t, first.Variables, "filter", "team", "key", "eq"); v != "ENG" {
		t.Errorf("filter.team.key.eq = %v, want the configured team key ENG", v)
	}
	if v := first.Variables["first"]; fmt.Sprint(v) != "50" {
		t.Errorf("first = %v, want the default page size 50", v)
	}
	if _, ok := first.Variables["after"]; ok {
		t.Error("first page sent an after cursor")
	}

	second := f.request(1)
	if v := second.Variables["after"]; v != "cursor-page-1" {
		t.Errorf("second page after = %v, want cursor-page-1 from pageInfo", v)
	}
}

func TestListStopsAtLimit(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"Issues": "issues_page1.json"}))
	c := newTestClient(t, f, "")

	got, err := c.List(context.Background(), source.ListFilter{Assignee: "dana@example.com", Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d issues, want 1 (Limit)", len(got))
	}
	if n := len(f.ops()); n != 1 {
		t.Errorf("made %d requests, want 1: the limit was reached on the first page", n)
	}
	req := f.request(0)
	if v := req.Variables["first"]; fmt.Sprint(v) != "1" {
		t.Errorf("first = %v, want the page size narrowed to the limit", v)
	}
	if v := dig(t, req.Variables, "filter", "assignee", "email", "eq"); v != "dana@example.com" {
		t.Errorf("filter.assignee.email.eq = %v, want the email-shaped assignee", v)
	}
}

func TestListStatusFilter(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"Issues": "issues_page2.json"}))
	c := newTestClient(t, f, "")

	if _, err := c.List(context.Background(), source.ListFilter{Assignee: "Dana Okoro", Status: "In Review"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	req := f.request(0)
	if v := dig(t, req.Variables, "filter", "state", "name", "eq"); v != "In Review" {
		t.Errorf("filter.state.name.eq = %v, want In Review", v)
	}
	if v := dig(t, req.Variables, "filter", "assignee", "name", "eq"); v != "Dana Okoro" {
		t.Errorf("filter.assignee.name.eq = %v, want the plain-name assignee", v)
	}
}

func TestListResolvesParentIdentifier(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{
		"IssueID": "issue_parent.json",
		"Issues":  "issues_page2.json",
	}))
	c := newTestClient(t, f, "")

	if _, err := c.List(context.Background(), source.ListFilter{Parent: "ENG-100"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if ops := f.ops(); len(ops) != 2 || ops[0] != "IssueID" || ops[1] != "Issues" {
		t.Fatalf("operations = %v, want the identifier resolved before listing", ops)
	}
	if v := f.request(0).Variables["id"]; v != "ENG-100" {
		t.Errorf("IssueID id = %v, want ENG-100", v)
	}
	want := "bbbb2222-0000-4000-8000-000000000099"
	if v := dig(t, f.request(1).Variables, "filter", "parent", "id", "eq"); v != want {
		t.Errorf("filter.parent.id.eq = %v, want the resolved UUID %s", v, want)
	}
}

func TestListPassesUUIDParentThrough(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"Issues": "issues_page2.json"}))
	c := newTestClient(t, f, "")

	parent := "bbbb2222-0000-4000-8000-000000000099"
	if _, err := c.List(context.Background(), source.ListFilter{Parent: parent}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if ops := f.ops(); len(ops) != 1 {
		t.Fatalf("operations = %v, want no lookup for a parent that is already a UUID", ops)
	}
	if v := dig(t, f.request(0).Variables, "filter", "parent", "id", "eq"); v != parent {
		t.Errorf("filter.parent.id.eq = %v, want %s", v, parent)
	}
}

// --- Helpdesk view ---

func TestHelpdeskGet(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"Issue": "issue.json"}))
	c := newTestClient(t, f, "")

	got, err := c.Helpdesk().Get(context.Background(), "ENG-123")
	if err != nil {
		t.Fatalf("Helpdesk().Get: %v", err)
	}
	if got.ID != "ENG-123" || got.Subject != "Checkout returns 500 on card retry" {
		t.Errorf("ID/Subject = %q/%q, want ENG-123/the issue title", got.ID, got.Subject)
	}
	if got.Status != "In Progress" || got.Priority != "High" || got.Channel != "linear" {
		t.Errorf("Status/Priority/Channel = %q/%q/%q, want In Progress/High/linear", got.Status, got.Priority, got.Channel)
	}
	if got.Fields["team"] != "ENG" {
		t.Errorf("Fields[team] = %q, want ENG", got.Fields["team"])
	}
}

func TestThreadsOrdersAndFlattensReplies(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"IssueConversation": "conversation.json"}))
	c := newTestClient(t, f, "")

	th, err := c.Helpdesk().Threads(context.Background(), "ENG-123")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(th) != 4 {
		t.Fatalf("got %d messages, want 4", len(th))
	}

	// The fixture lists the comments out of order and puts the reply in the
	// middle: c0 (bot, 08:55) is last in the payload, and c2 is a reply to
	// c1 that must land directly after it.
	// Linear draws no customer/agent line on a comment, so the guest author
	// stays an agent; only the bot is anything else.
	wantAuthors := []string{"Sentry", "Dana Okoro", "Kai Mensah", "Priya Raman"}
	wantRoles := []ticket.Role{ticket.RoleSystem, ticket.RoleAgent, ticket.RoleAgent, ticket.RoleAgent}
	wantAt := []time.Time{
		time.Date(2026, 9, 2, 8, 55, 0, 0, time.UTC),
		time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 2, 9, 5, 0, 0, time.UTC),
		time.Date(2026, 9, 2, 9, 10, 0, 0, time.UTC),
	}
	for i := range th {
		if th[i].Author != wantAuthors[i] {
			t.Errorf("message %d author = %q, want %q", i, th[i].Author, wantAuthors[i])
		}
		if th[i].Role != wantRoles[i] {
			t.Errorf("message %d role = %q, want %q", i, th[i].Role, wantRoles[i])
		}
		if !th[i].At.Equal(wantAt[i]) {
			t.Errorf("message %d at = %v, want %v", i, th[i].At, wantAt[i])
		}
	}
	if want := "\u21b3 Only on the second retry, not the first."; th[2].Text != want {
		t.Errorf("reply text = %q, want the flattened %q", th[2].Text, want)
	}
	if want := "Reproduced on staging with a declined Visa."; th[1].Text != want {
		t.Errorf("top-level comment text = %q, want it unprefixed: %q", th[1].Text, want)
	}
	if ids := th[3].AttachmentIDs; len(ids) != 1 || ids[0] != "4444dddd-0000-4000-8000-000000000004" {
		t.Errorf("guest message AttachmentIDs = %v, want the inline upload id", ids)
	}
	for i, m := range th {
		if m.Role == ticket.RoleCustomer {
			t.Errorf("message %d role = customer; Linear comments are workspace discussion, "+
				"the customer voice arrives through the helpdesk adapter", i)
		}
	}
	if w := c.WarningsFor("ENG-123"); len(w) != 0 {
		t.Errorf("WarningsFor(ENG-123) = %v, want none", w)
	}
}

func TestThreadsRetriesWithoutCommentActors(t *testing.T) {
	f := newFakeLinear(t, func(w http.ResponseWriter, req gqlRequest) bool {
		if req.Op != "IssueConversation" {
			return false
		}
		if strings.Contains(req.Query, "externalUser") {
			w.Write([]byte(`{"errors":[{"message":"Cannot query field \"externalUser\" on type \"Comment\".",` +
				`"extensions":{"type":"GRAPHQL_VALIDATION_FAILED"}}]}`))
			return true
		}
		if strings.Contains(req.Query, "botActor") {
			t.Error("the retry still asked for botActor")
		}
		w.Write(fixture(t, "conversation_no_actors.json"))
		return true
	})
	c := newTestClient(t, f, "")

	th, err := c.Helpdesk().Threads(context.Background(), "ENG-123")
	if err != nil {
		t.Fatalf("Threads: a workspace without externalUser must still yield a thread: %v", err)
	}
	if ops := f.ops(); len(ops) != 2 {
		t.Fatalf("operations = %v, want the query retried once without the actor fields", ops)
	}
	if len(th) != 4 {
		t.Fatalf("got %d messages, want all 4 from the reduced query", len(th))
	}
	for i, m := range th {
		if m.Role != ticket.RoleAgent {
			t.Errorf("message %d role = %q, want agent when the actor fields are unavailable", i, m.Role)
		}
	}
	if th[1].Author != "Dana Okoro" {
		t.Errorf("message 1 author = %q, want the user author still mapped", th[1].Author)
	}
	if th[0].Author != "" {
		t.Errorf("message 0 author = %q, want it empty: the bot author was not returned", th[0].Author)
	}
	if want := "\u21b3 Only on the second retry, not the first."; th[2].Text != want {
		t.Errorf("reply text = %q, want the flattening still applied: %q", th[2].Text, want)
	}

	warns := c.WarningsFor("ENG-123")
	if len(warns) != 1 || !strings.Contains(warns[0], "externalUser") {
		t.Errorf("WarningsFor(ENG-123) = %v, want one naming the unavailable fields", warns)
	}
}

func TestEffectiveLimit(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, defaultLimit},
		{-5, defaultLimit},
		{1, 1},
		{99, 99},
		{maxLimit, maxLimit},
		{maxLimit + 1, maxLimit},
		{100000, maxLimit},
	}
	for _, tc := range cases {
		if got := effectiveLimit(tc.in); got != tc.want {
			t.Errorf("effectiveLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if defaultLimit != 100 || maxLimit != 200 {
		t.Errorf("limits = %d/%d, want the contract's 100 default and 200 cap", defaultLimit, maxLimit)
	}
}

func TestAttachmentsDownloadsLinearUploads(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"IssueConversation": "conversation.json"}))
	f.download = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprintf(w, "bytes of %s", r.URL.Path)
	}
	c := newTestClient(t, f, "")
	dir := filepath.Join(t.TempDir(), "attachments")

	got, err := c.Helpdesk().Attachments(context.Background(), "ENG-123", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d attachments, want 3 (issue upload, description image, comment image)", len(got))
	}
	if n := f.downloadCount(); n != 3 {
		t.Errorf("made %d downloads, want 3: the zendesk and github attachments are links, not files", n)
	}

	wantNames := []string{"crash log.txt", "evil.png", "repro.png"}
	for i, a := range got {
		if a.Name != wantNames[i] {
			t.Errorf("attachment %d name = %q, want the sanitised %q", i, a.Name, wantNames[i])
		}
		wantPath := fmt.Sprintf("attachments/%d-%s", i+1, wantNames[i])
		if a.Path != wantPath {
			t.Errorf("attachment %d path = %q, want %q", i, a.Path, wantPath)
		}
		if a.MIME != "image/png" {
			t.Errorf("attachment %d MIME = %q, want image/png", i, a.MIME)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.Base(wantPath))); err != nil {
			t.Errorf("attachment %d not written: %v", i, err)
		}
	}

	// The description image's URL escapes a traversal; nothing may be written
	// outside dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("wrote %d files, want exactly 3 inside the destination dir", len(entries))
	}
	if w := c.WarningsFor("ENG-123"); len(w) != 0 {
		t.Errorf("WarningsFor(ENG-123) = %v, want none", w)
	}
}

func TestAttachmentsRecordsPerFileFailures(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"IssueConversation": "conversation.json"}))
	f.download = func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "repro.png") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}
	c := newTestClient(t, f, "")

	got, err := c.Helpdesk().Attachments(context.Background(), "ENG-123", filepath.Join(t.TempDir(), "att"))
	if err != nil {
		t.Fatalf("Attachments: one failure among several must not fail the call: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d attachments, want the 2 that downloaded", len(got))
	}

	warns := c.WarningsFor("ENG-123")
	if len(warns) != 1 {
		t.Fatalf("WarningsFor(ENG-123) = %v, want 1 entry for the skipped file", warns)
	}
	if !strings.Contains(warns[0], "4444dddd-0000-4000-8000-000000000004") {
		t.Errorf("warning = %q, want it to name the attachment that failed", warns[0])
	}
	if w := c.WarningsFor("ENG-123"); len(w) != 0 {
		t.Errorf("WarningsFor(ENG-123) after reading = %v, want none", w)
	}
}

func TestAttachmentsAllFailuresBecomeAnError(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"IssueConversation": "conversation.json"}))
	f.download = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	c := newTestClient(t, f, "")

	got, err := c.Helpdesk().Attachments(context.Background(), "ENG-123", filepath.Join(t.TempDir(), "att"))
	if err == nil {
		t.Fatal("Attachments: want an error when every download fails, got nil")
	}
	if len(got) != 0 {
		t.Errorf("got %d attachments, want none", len(got))
	}
	if w := c.WarningsFor("ENG-123"); len(w) != 0 {
		t.Errorf("failures were reported twice, as an error and as warnings: %v", w)
	}
}

func TestAttachmentsClearsStaleWarnings(t *testing.T) {
	fail := true
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"IssueConversation": "conversation.json"}))
	f.download = func(w http.ResponseWriter, r *http.Request) {
		if fail && strings.Contains(r.URL.Path, "repro.png") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}
	c := newTestClient(t, f, "")

	if _, err := c.Helpdesk().Attachments(context.Background(), "ENG-123", filepath.Join(t.TempDir(), "a")); err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	fail = false
	if _, err := c.Helpdesk().Attachments(context.Background(), "ENG-123", filepath.Join(t.TempDir(), "b")); err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if w := c.WarningsFor("ENG-123"); len(w) != 0 {
		t.Errorf("warnings not reset between calls: %v", w)
	}
}

func TestErrorsArrayMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   source.Code
	}{
		{
			name:   "rate limited",
			status: http.StatusBadRequest,
			body:   `{"errors":[{"message":"Rate limit exceeded","extensions":{"type":"RATELIMITED","userPresentableMessage":"Slow down"}}]}`,
			want:   source.RateLimited,
		},
		{
			name:   "authentication",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"Authentication required","extensions":{"type":"AUTHENTICATION_ERROR"}}]}`,
			want:   source.Auth,
		},
		{
			name:   "forbidden",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"nope","extensions":{"type":"FORBIDDEN"}}]}`,
			want:   source.Auth,
		},
		{
			name:   "entity not found",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"Entity not found: Issue","extensions":{"type":"invalid input"}}]}`,
			want:   source.NotFound,
		},
		{
			name:   "numeric extension code",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"boom","extensions":{"code":500}}]}`,
			want:   source.Internal,
		},
		{
			name:   "unauthorized status with no errors array",
			status: http.StatusUnauthorized,
			body:   `not json`,
			want:   source.Auth,
		},
		{
			name:   "server error with no errors array",
			status: http.StatusBadGateway,
			body:   `gateway down`,
			want:   source.Internal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLinear(t, func(w http.ResponseWriter, req gqlRequest) bool {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
				return true
			})
			c := newTestClient(t, f, "")

			_, err := c.Get(context.Background(), "ENG-123")
			if err == nil {
				t.Fatal("Get: want an error, got nil")
			}
			if got := sourceCode(t, err); got != tc.want {
				t.Errorf("code = %q, want %q (error: %v)", got, tc.want, err)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Error("error message leaks the API key")
			}
		})
	}
}

func TestRateLimitedHonoursOneRetryAfter(t *testing.T) {
	calls := 0
	f := newFakeLinear(t, func(w http.ResponseWriter, req gqlRequest) bool {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"errors":[{"message":"slow down","extensions":{"type":"RATELIMITED"}}]}`)
			return true
		}
		w.Write(fixture(t, "issue.json"))
		return true
	})
	c := newTestClient(t, f, "")

	if _, err := c.Get(context.Background(), "ENG-123"); err != nil {
		t.Fatalf("Get after a honoured Retry-After: %v", err)
	}
	if calls != 2 {
		t.Errorf("made %d requests, want the 429 retried exactly once", calls)
	}
}

func TestRateLimitedWithoutRetryAfter(t *testing.T) {
	calls := 0
	f := newFakeLinear(t, func(w http.ResponseWriter, req gqlRequest) bool {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{}`)
		return true
	})
	c := newTestClient(t, f, "")

	_, err := c.Get(context.Background(), "ENG-123")
	if err == nil {
		t.Fatal("Get: want an error, got nil")
	}
	if got := sourceCode(t, err); got != source.RateLimited {
		t.Errorf("code = %q, want %q", got, source.RateLimited)
	}
	if calls != 1 {
		t.Errorf("made %d requests, want no retry without a Retry-After header", calls)
	}
}

// --- Ping ---

func TestPing(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"Ping": "viewer.json"}))
	c := newTestClient(t, f, "")

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if req := f.request(0); !strings.Contains(req.Query, "viewer") {
		t.Errorf("ping query = %q, want a viewer query", req.Query)
	}
}

func TestPingAuthFailure(t *testing.T) {
	f := newFakeLinear(t, func(w http.ResponseWriter, req gqlRequest) bool {
		w.Write([]byte(`{"errors":[{"message":"Authentication failed","extensions":{"type":"AUTHENTICATION_ERROR"}}]}`))
		return true
	})
	c := newTestClient(t, f, "")

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping: want an error, got nil")
	}
	if got := sourceCode(t, err); got != source.Auth {
		t.Errorf("code = %q, want %q", got, source.Auth)
	}
}

// --- Pure mapping ---

func TestHelpdeskRefDerivation(t *testing.T) {
	cases := []struct {
		name string
		atts []linearAttachment
		want string
	}{
		{
			name: "zendesk source type wins over an earlier github link",
			atts: []linearAttachment{
				{URL: "https://github.com/acme/api/pull/1", SourceType: "github"},
				{URL: "https://acme.zendesk.com/agent/tickets/9", SourceType: "zendesk"},
			},
			want: "https://acme.zendesk.com/agent/tickets/9",
		},
		{
			name: "intercom",
			atts: []linearAttachment{{URL: "https://app.intercom.com/a/apps/x/conversations/7", SourceType: "intercom"}},
			want: "https://app.intercom.com/a/apps/x/conversations/7",
		},
		{
			name: "front",
			atts: []linearAttachment{{URL: "https://app.frontapp.com/open/msg_1", SourceType: "front"}},
			want: "https://app.frontapp.com/open/msg_1",
		},
		{
			name: "unknown source type with a helpdesk host",
			atts: []linearAttachment{{URL: "https://desk.zoho.com/agent/acme/tickets/details/555", SourceType: "unknown"}},
			want: "https://desk.zoho.com/agent/acme/tickets/details/555",
		},
		{
			name: "empty source type with a helpdesk host",
			atts: []linearAttachment{{URL: "https://acme.freshdesk.com/a/tickets/12", SourceType: ""}},
			want: "https://acme.freshdesk.com/a/tickets/12",
		},
		{
			name: "unknown source type with an unrelated host",
			atts: []linearAttachment{{URL: "https://example.com/notes/1", SourceType: "unknown"}},
			want: "",
		},
		{
			name: "slack and figma are not helpdesks",
			atts: []linearAttachment{
				{URL: "https://acme.slack.com/archives/C1/p1", SourceType: "slack"},
				{URL: "https://figma.com/file/x", SourceType: "figma"},
			},
			want: "",
		},
		{name: "no attachments", atts: nil, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := helpdeskRef(tc.atts); got != tc.want {
				t.Errorf("helpdeskRef = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"screenshot.png", "screenshot.png"},
		{"crash log.txt", "crash log.txt"},
		{"../../etc/passwd", "passwd"},
		{`..\..\windows\evil.exe`, "evil.exe"},
		{"", "attachment"},
		{"..", "attachment"},
		{".", "attachment"},
		{"/", "attachment"},
		{"a\x00b.png", "ab.png"},
		{strings.Repeat("x", 200) + ".png", strings.Repeat("x", 116) + ".png"},
	}
	for _, tc := range cases {
		if got := sanitizeName(tc.in); got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCollectUploadsFindsInlineImages(t *testing.T) {
	md := "before ![a](https://uploads.linear.app/w/1111/one.png) and " +
		"![b](https://uploads.linear.app/w/2222/two%20shot.jpg) and " +
		"![c](https://example.com/three.png) and a link [d](https://uploads.linear.app/w/3333/four.png)"

	got := collectUploads(md)
	if len(got) != 2 {
		t.Fatalf("got %d uploads, want the 2 Linear-hosted images: %+v", len(got), got)
	}
	if got[0].Name != "one.png" || got[1].Name != "two shot.jpg" {
		t.Errorf("names = %q, %q, want one.png and the unescaped two shot.jpg", got[0].Name, got[1].Name)
	}
	if got[0].ID != "1111" {
		t.Errorf("id = %q, want the upload's storage segment 1111", got[0].ID)
	}
}
