package azdo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	testProject = "Fabrikam"
	testPAT     = "pat-secret"
)

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// checkAuth asserts every request carries the PAT as the basic-auth
// password with an empty user name, and names an api-version.
func checkAuth(t *testing.T, r *http.Request) {
	t.Helper()
	user, pass, ok := r.BasicAuth()
	if !ok {
		t.Errorf("%s: no basic auth header", r.URL.Path)
		return
	}
	if user != "" {
		t.Errorf("%s: basic auth user = %q, want empty", r.URL.Path, user)
	}
	if pass != testPAT {
		t.Errorf("%s: basic auth password = %q, want the PAT", r.URL.Path, pass)
	}
	if r.URL.Query().Get("api-version") == "" {
		t.Errorf("%s: no api-version in query %q", r.URL.Path, r.URL.RawQuery)
	}
}

// fixtureServer serves the work item, its comments (two pages), a WIQL
// query, workitemsbatch, the two attachment payloads and the project probe,
// recording what it was asked for.
type fixtureServer struct {
	*httptest.Server

	mu               sync.Mutex
	batchIDs         [][]int
	wiqlQuery        string
	commentPages     int
	attachmentStatus map[string]int      // guid -> status override
	rewrite          func(string) string // rewrites the work item JSON before it is served
	// redirectAttachmentsTo, when set, answers every attachment request
	// with a 302 to it, the way Azure DevOps answers with the sign-in page
	// instead of a 401.
	redirectAttachmentsTo string
}

func newFixtureServer(t *testing.T) (*fixtureServer, *Client) {
	t.Helper()

	fs := &fixtureServer{attachmentStatus: map[string]int{}}
	workItemJSON := mustReadFile(t, "testdata/workitem.json")
	page1 := mustReadFile(t, "testdata/comments-page1.json")
	page2 := mustReadFile(t, "testdata/comments-page2.json")
	wiqlJSON := mustReadFile(t, "testdata/wiql.json")

	var batchByID map[string]json.RawMessage
	if err := json.Unmarshal(mustReadFile(t, "testdata/batch.json"), &batchByID); err != nil {
		t.Fatalf("parse batch.json: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/_apis/projects/"+testProject, func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"p1","name":"Fabrikam","state":"wellFormed"}`))
	})
	mux.HandleFunc("/"+testProject+"/_apis/wit/workitems/4242", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		if got := r.URL.Query().Get("$expand"); got != "all" {
			t.Errorf("$expand = %q, want all", got)
		}
		body := strings.ReplaceAll(string(workItemJSON), "{{BASE}}", fs.URL)
		fs.mu.Lock()
		rewrite := fs.rewrite
		fs.mu.Unlock()
		if rewrite != nil {
			body = rewrite(body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
	mux.HandleFunc("/"+testProject+"/_apis/wit/workItems/4242/comments", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		fs.mu.Lock()
		fs.commentPages++
		fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("continuationToken") == "page-2-token" {
			w.Write(page2)
			return
		}
		w.Write(page1)
	})
	mux.HandleFunc("/"+testProject+"/_apis/wit/wiql", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		if r.Method != http.MethodPost {
			t.Errorf("wiql method = %s, want POST", r.Method)
		}
		var req wiqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode wiql body: %v", err)
		}
		fs.mu.Lock()
		fs.wiqlQuery = req.Query
		fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(wiqlJSON)
	})
	mux.HandleFunc("/"+testProject+"/_apis/wit/workitemsbatch", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		var req batchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode batch body: %v", err)
		}
		if req.ErrorPolicy != "Omit" {
			t.Errorf("errorPolicy = %q, want Omit", req.ErrorPolicy)
		}
		if len(req.Fields) == 0 {
			t.Error("batch request carried no field list")
		}
		fs.mu.Lock()
		fs.batchIDs = append(fs.batchIDs, append([]int(nil), req.IDs...))
		fs.mu.Unlock()

		var out []json.RawMessage
		for _, id := range req.IDs {
			if wi, ok := batchByID[itoa(id)]; ok {
				out = append(out, wi)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"count": len(out), "value": out})
	})
	mux.HandleFunc("/"+testProject+"/_apis/wit/attachments/", func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		guid := filepath.Base(r.URL.Path)
		fs.mu.Lock()
		status := fs.attachmentStatus[guid]
		redirectTo := fs.redirectAttachmentsTo
		fs.mu.Unlock()
		if redirectTo != "" {
			http.Redirect(w, r, redirectTo, http.StatusFound)
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			w.Write([]byte("nope"))
			return
		}
		switch guid {
		case "aaaaaaaa-1111-2222-3333-444444444444":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("log line\n"))
		case "bbbbbbbb-2222-3333-4444-555555555555":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("\x89PNG\r\n\x1a\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		w.WriteHeader(http.StatusNotFound)
	})

	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)

	c, err := New(Config{
		OrgURL:             fs.URL,
		Project:            testProject,
		PAT:                testPAT,
		HelpdeskLinkDomain: "support.contoso.com",
	}, fs.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fs, c
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestNormalizeKey(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		wantErr  bool
	}{
		{in: "123", want: "123"},
		{in: "#123", want: "123"},
		{in: "AB-123", want: "123"},
		{in: "  42 ", want: "42"},
		{in: "AB1-234", want: "234"},
		{in: "007", want: "7"},
		{in: "abc", wantErr: true},
		{in: "", wantErr: true},
	} {
		got, err := normalizeKey(tc.in)
		if tc.wantErr {
			var se *source.Error
			if !errors.As(err, &se) || se.Code != source.NotFound {
				t.Errorf("normalizeKey(%q) error = %v, want source.NotFound", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeKey(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGet(t *testing.T) {
	_, c := newFixtureServer(t)

	// A prefixed key must resolve to the same work item as its bare id.
	tk, err := c.Get(context.Background(), "AB-4242")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if tk.Key != "4242" {
		t.Errorf("Key = %q, want 4242", tk.Key)
	}
	if tk.Title != "Login fails for SSO users" {
		t.Errorf("Title = %q", tk.Title)
	}
	if tk.Status != "Active" {
		t.Errorf("Status = %q, want Active", tk.Status)
	}
	if tk.Priority != "2" {
		t.Errorf("Priority = %q, want 2 (severity must not be folded in)", tk.Priority)
	}
	if tk.Assignee != "Ada Lovelace" {
		t.Errorf("Assignee = %q", tk.Assignee)
	}
	if tk.URL != "https://dev.azure.com/contoso/Fabrikam/_workitems/edit/4242" {
		t.Errorf("URL = %q, want the _links.html.href", tk.URL)
	}
	if tk.HelpdeskRef != "https://eu.support.contoso.com/tickets/8899" {
		t.Errorf("HelpdeskRef = %q, want the Hyperlink on the helpdesk domain", tk.HelpdeskRef)
	}
	if want := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC); !tk.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", tk.CreatedAt, want)
	}
	if want := time.Date(2026, 9, 8, 14, 30, 0, 0, time.UTC); !tk.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v", tk.UpdatedAt, want)
	}

	// HTML description, then the Bug's repro steps under their own heading.
	if !strings.Contains(tk.Description, "**SSO**") {
		t.Errorf("Description lost the HTML emphasis: %q", tk.Description)
	}
	if !strings.Contains(tk.Description, "## Repro steps") {
		t.Errorf("Description has no repro steps heading: %q", tk.Description)
	}
	if !strings.Contains(tk.Description, "Open the portal") {
		t.Errorf("Description has no repro steps body: %q", tk.Description)
	}
	if strings.Index(tk.Description, "cannot sign in") > strings.Index(tk.Description, "## Repro steps") {
		t.Errorf("repro steps came before the description: %q", tk.Description)
	}

	want := map[string]string{
		"workItemType":  "Bug",
		"areaPath":      `Fabrikam\Web`,
		"iterationPath": `Fabrikam\Sprint 42`,
		"tags":          "sso, regression, customer",
		"parent":        "4000",
		"severity":      "2 - High",
		"reason":        "New",
	}
	for k, v := range want {
		if got := tk.Fields[k]; got != v {
			t.Errorf("Fields[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestGetHelpdeskRefFallsBackToField(t *testing.T) {
	fs, _ := newFixtureServer(t)
	c, err := New(Config{
		OrgURL:        fs.URL,
		Project:       testProject,
		PAT:           testPAT,
		HelpdeskField: "Custom.HelpdeskId",
	}, fs.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tk, err := c.Get(context.Background(), "4242")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if tk.HelpdeskRef != "ZD-99" {
		t.Errorf("HelpdeskRef = %q, want the custom field value ZD-99", tk.HelpdeskRef)
	}
}

func TestHelpdeskGetAndThreadsPaginate(t *testing.T) {
	fs, c := newFixtureServer(t)
	hd := c.Helpdesk()

	ht, err := hd.Get(context.Background(), "#4242")
	if err != nil {
		t.Fatalf("Helpdesk.Get: %v", err)
	}
	if ht.ID != "4242" || ht.Subject != "Login fails for SSO users" || ht.Status != "Active" {
		t.Errorf("helpdesk ticket = %+v", ht)
	}
	if ht.Contact != "Grace Hopper" {
		t.Errorf("Contact = %q, want the creator", ht.Contact)
	}

	th, err := hd.Threads(context.Background(), "4242")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	fs.mu.Lock()
	pages := fs.commentPages
	fs.mu.Unlock()
	if pages != 2 {
		t.Errorf("comment pages fetched = %d, want 2", pages)
	}
	if len(th) != 3 {
		t.Fatalf("thread has %d messages, want 3: %+v", len(th), th)
	}
	if th[0].Author != "Grace Hopper" || th[0].Role != ticket.RoleAgent {
		t.Errorf("message 0 = %+v", th[0])
	}
	if th[0].Text != "Reproduced on the **staging** tenant." {
		t.Errorf("markdown comment was rewritten: %q", th[0].Text)
	}
	if !strings.Contains(th[1].Text, "`nameid`") {
		t.Errorf("html comment was not converted to markdown: %q", th[1].Text)
	}
	if th[2].Role != ticket.RoleSystem || th[2].Author != "system" {
		t.Errorf("authorless comment = %+v, want the system role", th[2])
	}
	if want := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC); !th[0].At.Equal(want) {
		t.Errorf("message 0 At = %v, want %v", th[0].At, want)
	}
}

func TestListWIQLThenBatchChunks(t *testing.T) {
	fs, c := newFixtureServer(t)
	c.batchSize = 2 // three ids, so the batch call has to chunk

	got, err := c.List(context.Background(), source.ListFilter{Assignee: "me", Parent: "AB-4000", Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	fs.mu.Lock()
	query, chunks := fs.wiqlQuery, fs.batchIDs
	fs.mu.Unlock()

	for _, want := range []string{
		"SELECT [System.Id] FROM WorkItems",
		"[System.TeamProject] = @project",
		"[System.State] NOT IN ('Closed','Done','Removed','Resolved')",
		"[System.AssignedTo] = @Me",
		"[System.Parent] = 4000",
		"ORDER BY [System.ChangedDate] DESC",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("wiql query missing %q:\n%s", want, query)
		}
	}

	if len(chunks) != 2 {
		t.Fatalf("workitemsbatch calls = %d, want 2: %v", len(chunks), chunks)
	}
	if len(chunks[0]) != 2 || chunks[0][0] != 4242 || chunks[0][1] != 4243 {
		t.Errorf("first chunk = %v, want [4242 4243]", chunks[0])
	}
	if len(chunks[1]) != 1 || chunks[1][0] != 4244 {
		t.Errorf("second chunk = %v, want [4244]", chunks[1])
	}

	if len(got) != 3 {
		t.Fatalf("List returned %d tickets, want 3", len(got))
	}
	for i, want := range []string{"4242", "4243", "4244"} {
		if got[i].Key != want {
			t.Errorf("ticket %d key = %q, want %q (the query's order)", i, got[i].Key, want)
		}
	}
	// The batch endpoint returns fields only, so the web URL is constructed.
	if want := fs.URL + "/Fabrikam/_workitems/edit/4242"; got[0].URL != want {
		t.Errorf("URL = %q, want %q", got[0].URL, want)
	}
}

func TestListNamedAssigneeAndStatus(t *testing.T) {
	fs, c := newFixtureServer(t)
	if _, err := c.List(context.Background(), source.ListFilter{Assignee: "ada@contoso.com", Status: "Active"}); err != nil {
		t.Fatalf("List: %v", err)
	}
	fs.mu.Lock()
	query := fs.wiqlQuery
	fs.mu.Unlock()

	if !strings.Contains(query, "[System.AssignedTo] = 'ada@contoso.com'") {
		t.Errorf("wiql query missing the named assignee:\n%s", query)
	}
	if !strings.Contains(query, "[System.State] = 'Active'") {
		t.Errorf("wiql query missing the status filter:\n%s", query)
	}
	if strings.Contains(query, "NOT IN") {
		t.Errorf("an explicit status must replace the open-ish default:\n%s", query)
	}
}

func TestListLimitTruncates(t *testing.T) {
	fs, c := newFixtureServer(t)
	got, err := c.List(context.Background(), source.ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d tickets, want 2", len(got))
	}
	fs.mu.Lock()
	chunks := fs.batchIDs
	fs.mu.Unlock()
	if len(chunks) != 1 || len(chunks[0]) != 2 {
		t.Errorf("batched ids = %v, want one chunk of 2", chunks)
	}
}

func TestEffectiveLimit(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, defaultListLimit},
		{-1, defaultListLimit},
		{50, 50},
		{200, maxListLimit},
		{500, maxListLimit},
	}
	for _, tc := range cases {
		if got, _ := httpx.Limit(tc.in, defaultListLimit, maxListLimit); got != tc.want {
			t.Errorf("Limit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if defaultListLimit != 100 || maxListLimit != 200 {
		t.Errorf("limits = %d/%d, want the contract's 100 default and 200 cap", defaultListLimit, maxListLimit)
	}
}

func TestPing(t *testing.T) {
	_, c := newFixtureServer(t)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestAttachments(t *testing.T) {
	_, c := newFixtureServer(t)
	dir := filepath.Join(t.TempDir(), "attachments")

	atts, err := c.Helpdesk().Attachments(context.Background(), "4242", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 2 {
		t.Fatalf("got %d attachments, want 2 (one relation, one inline image): %+v", len(atts), atts)
	}

	// "../../evil log.txt" must not escape the destination directory.
	if atts[0].Name != "evil log.txt" {
		t.Errorf("name = %q, want the sanitised base name", atts[0].Name)
	}
	if atts[0].Path != "attachments/1-evil log.txt" {
		t.Errorf("path = %q", atts[0].Path)
	}
	if atts[0].ID != "aaaaaaaa-1111-2222-3333-444444444444" {
		t.Errorf("id = %q, want the attachment guid", atts[0].ID)
	}
	if atts[0].MIME != "text/plain" {
		t.Errorf("mime = %q", atts[0].MIME)
	}
	body, err := os.ReadFile(filepath.Join(dir, "1-evil log.txt"))
	if err != nil || string(body) != "log line\n" {
		t.Errorf("downloaded body = %q, err = %v", body, err)
	}

	if atts[1].Name != "screen shot.png" {
		t.Errorf("inline image name = %q", atts[1].Name)
	}
	if _, err := os.Stat(filepath.Join(dir, "2-screen shot.png")); err != nil {
		t.Errorf("inline image not written: %v", err)
	}
	if w := c.WarningsFor("4242"); len(w) != 0 {
		t.Errorf("unexpected warnings: %v", w)
	}
}

func TestAttachmentsPartialFailureWarns(t *testing.T) {
	fs, c := newFixtureServer(t)
	fs.mu.Lock()
	fs.attachmentStatus["bbbbbbbb-2222-3333-4444-555555555555"] = http.StatusInternalServerError
	fs.mu.Unlock()

	dir := filepath.Join(t.TempDir(), "attachments")
	atts, err := c.Helpdesk().Attachments(context.Background(), "4242", dir)
	if err != nil {
		t.Fatalf("one bad download must not fail the call: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("got %d attachments, want 1", len(atts))
	}
	// Warnings read back under any accepted spelling of the same key, and
	// are consumed once read.
	w := c.WarningsFor("#4242")
	if len(w) != 1 || !strings.Contains(w[0], "bbbbbbbb") {
		t.Errorf("warnings = %v, want one naming the failed attachment", w)
	}
	if again := c.WarningsFor("4242"); len(again) != 0 {
		t.Errorf("warnings were not consumed: %v", again)
	}
}

// A work item's bundle is Get, then Threads, then Attachments, with a single
// WarningsFor at the end (internal/run's fetchBundle). Warnings accumulate
// under the work item across those calls and are cleared by the read, not by
// the next call — so a later clean call cannot erase what an earlier one
// recorded, and a line already recorded is not repeated.
func TestWarningsAccumulateAcrossTheBundleAndClearOnRead(t *testing.T) {
	fs, c := newFixtureServer(t)
	fs.mu.Lock()
	fs.attachmentStatus["bbbbbbbb-2222-3333-4444-555555555555"] = http.StatusInternalServerError
	fs.mu.Unlock()

	ctx := context.Background()
	if _, err := c.Get(ctx, "4242"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := c.Helpdesk().Threads(ctx, "4242"); err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if _, err := c.Helpdesk().Attachments(ctx, "4242", filepath.Join(t.TempDir(), "a")); err != nil {
		t.Fatalf("Attachments: %v", err)
	}

	// A second pass — a retry, say — that hits the same failure adds no
	// second copy of the line, and a third that runs clean does not erase
	// what the first recorded.
	if _, err := c.Helpdesk().Attachments(ctx, "4242", filepath.Join(t.TempDir(), "b")); err != nil {
		t.Fatalf("Attachments (retry): %v", err)
	}
	fs.mu.Lock()
	delete(fs.attachmentStatus, "bbbbbbbb-2222-3333-4444-555555555555")
	fs.mu.Unlock()
	if _, err := c.Helpdesk().Attachments(ctx, "4242", filepath.Join(t.TempDir(), "c")); err != nil {
		t.Fatalf("Attachments (clean): %v", err)
	}

	w := c.WarningsFor("4242")
	if len(w) != 1 || !strings.Contains(w[0], "bbbbbbbb") {
		t.Fatalf("WarningsFor(4242) = %v, want exactly one line naming the failed attachment", w)
	}
	if again := c.WarningsFor("4242"); len(again) != 0 {
		t.Errorf("warnings survived being read: %v", again)
	}
}

func TestAttachmentsAllFail(t *testing.T) {
	fs, c := newFixtureServer(t)
	fs.mu.Lock()
	fs.attachmentStatus["aaaaaaaa-1111-2222-3333-444444444444"] = http.StatusInternalServerError
	fs.attachmentStatus["bbbbbbbb-2222-3333-4444-555555555555"] = http.StatusInternalServerError
	fs.mu.Unlock()

	dir := filepath.Join(t.TempDir(), "attachments")
	atts, err := c.Helpdesk().Attachments(context.Background(), "4242", dir)
	if err == nil {
		t.Fatal("every download failed but Attachments returned no error")
	}
	if len(atts) != 0 {
		t.Errorf("got %d attachments, want none", len(atts))
	}
	// Every failure is already in the returned error, so nothing is left
	// behind for the next caller to pick up.
	if w := c.WarningsFor("4242"); len(w) != 0 {
		t.Errorf("warnings = %v, want none when the error carries them all", w)
	}
	for _, guid := range []string{"aaaaaaaa", "bbbbbbbb"} {
		if !strings.Contains(err.Error(), guid) {
			t.Errorf("error does not name attachment %s: %v", guid, err)
		}
	}
}

// errorServer answers every request with one canned response.
func errorServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{OrgURL: srv.URL, Project: testProject, PAT: testPAT}, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func wantCode(t *testing.T, err error, code source.Code) {
	t.Helper()
	var se *source.Error
	if !errors.As(err, &se) {
		t.Fatalf("error = %v (%T), want *source.Error", err, err)
	}
	if se.Code != code {
		t.Fatalf("code = %q, want %q (message %q)", se.Code, code, se.Message)
	}
	if strings.Contains(se.Message, testPAT) {
		t.Fatalf("error message leaked the credential: %q", se.Message)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Run("401", func(t *testing.T) {
		c := errorServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"denied"}`))
		})
		_, err := c.Get(context.Background(), "1")
		wantCode(t, err, source.Auth)
	})

	t.Run("403", func(t *testing.T) {
		c := errorServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
		_, err := c.Get(context.Background(), "1")
		wantCode(t, err, source.Auth)
	})

	t.Run("203 sign-in page by content type", func(t *testing.T) {
		c := errorServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNonAuthoritativeInfo)
			w.Write([]byte("<html><body>Sign in to your account</body></html>"))
		})
		_, err := c.Get(context.Background(), "1")
		wantCode(t, err, source.Auth)
	})

	t.Run("203 sign-in page sniffed from the body", func(t *testing.T) {
		c := errorServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNonAuthoritativeInfo)
			w.Write([]byte("<!DOCTYPE html><html><body>Sign in</body></html>"))
		})
		_, err := c.Get(context.Background(), "1")
		wantCode(t, err, source.Auth)
	})

	t.Run("404", func(t *testing.T) {
		c := errorServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		_, err := c.Get(context.Background(), "1")
		wantCode(t, err, source.NotFound)
	})

	t.Run("500 quotes a bounded body snippet", func(t *testing.T) {
		c := errorServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(strings.Repeat("x", 5000)))
		})
		_, err := c.Get(context.Background(), "1")
		wantCode(t, err, source.Internal)
		var se *source.Error
		errors.As(err, &se)
		if len(se.Message) > 400 {
			t.Errorf("message is %d bytes, want the body snippet capped", len(se.Message))
		}
	})

	t.Run("429 waits out one short Retry-After", func(t *testing.T) {
		var mu sync.Mutex
		calls := 0
		c := errorServer(t, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			mu.Unlock()
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("TF400733: throttled"))
		})
		err := c.Ping(context.Background())
		wantCode(t, err, source.RateLimited)
		mu.Lock()
		defer mu.Unlock()
		if calls != 2 {
			t.Errorf("requests = %d, want 2 (one retry after Retry-After)", calls)
		}
	})

	t.Run("429 refuses a Retry-After beyond the cap", func(t *testing.T) {
		var mu sync.Mutex
		calls := 0
		c := errorServer(t, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			mu.Unlock()
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
		})
		err := c.Ping(context.Background())
		wantCode(t, err, source.RateLimited)
		mu.Lock()
		defer mu.Unlock()
		if calls != 1 {
			t.Errorf("requests = %d, want 1 (a 1h wait must not be honoured)", calls)
		}
	})

	t.Run("bad json", func(t *testing.T) {
		c := errorServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":`))
		})
		_, err := c.Get(context.Background(), "1")
		wantCode(t, err, source.Internal)
	})
}

func TestNewValidates(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"no org", Config{Project: "p", PAT: "t"}},
		{"relative org", Config{OrgURL: "dev.azure.com/contoso", Project: "p", PAT: "t"}},
		{"no project", Config{OrgURL: "https://dev.azure.com/contoso", PAT: "t"}},
		{"no pat", Config{OrgURL: "https://dev.azure.com/contoso", Project: "p"}},
	} {
		if _, err := New(tc.cfg, nil); err == nil {
			t.Errorf("New(%s) = nil error, want a validation error", tc.name)
		}
	}
	c, err := New(Config{OrgURL: "https://dev.azure.com/contoso/", Project: "p", PAT: "t", HelpdeskLinkDomain: ".Support.Contoso.com"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.base != "https://dev.azure.com/contoso" {
		t.Errorf("base = %q, want the trailing slash trimmed", c.base)
	}
	if c.cfg.HelpdeskLinkDomain != "support.contoso.com" {
		t.Errorf("helpdesk domain = %q, want it lowercased and de-dotted", c.cfg.HelpdeskLinkDomain)
	}
}

func TestHostMatches(t *testing.T) {
	for _, tc := range []struct {
		host, domain string
		want         bool
	}{
		{"support.contoso.com", "support.contoso.com", true},
		{"eu.support.contoso.com", "support.contoso.com", true},
		{"EU.Support.Contoso.com", "support.contoso.com", true},
		{"mysupport.contoso.com", "support.contoso.com", false},
		{"contoso.com", "support.contoso.com", false},
		// The domain side is typed by hand into a YAML file, so it is
		// lowercased too rather than silently matching nothing.
		{"support.contoso.com", "Support.Contoso.com", true},
		{"eu.support.contoso.com", "SUPPORT.CONTOSO.COM", true},
		{"support.contoso.com", " support.contoso.com. ", true},
	} {
		if got := hostMatches(tc.host, tc.domain); got != tc.want {
			t.Errorf("hostMatches(%q, %q) = %v, want %v", tc.host, tc.domain, got, tc.want)
		}
	}
}

// foreignServer stands in for a host an attacker put in a work item's
// description or relations. Any request that reaches it means the PAT was
// sent somewhere it should never go.
func foreignServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		t.Errorf("credentialed request reached a foreign host: %s %s (Authorization %q)", r.Method, r.URL.Path, auth)
		w.Write([]byte("pwned"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAttachmentsSkipsUntrustedRelationHost(t *testing.T) {
	fs, c := newFixtureServer(t)
	foreign := foreignServer(t)

	// Repoint only the AttachedFile relation at the foreign host; the
	// inline image stays on the organisation's own host.
	fs.mu.Lock()
	fs.rewrite = func(body string) string {
		return strings.Replace(body,
			fs.URL+"/Fabrikam/_apis/wit/attachments/aaaaaaaa",
			foreign.URL+"/Fabrikam/_apis/wit/attachments/aaaaaaaa", 1)
	}
	fs.mu.Unlock()

	dir := filepath.Join(t.TempDir(), "attachments")
	atts, err := c.Helpdesk().Attachments(context.Background(), "4242", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 || atts[0].Name != "screen shot.png" {
		t.Fatalf("attachments = %+v, want only the inline image on the trusted host", atts)
	}
	w := c.WarningsFor("4242")
	if len(w) != 1 || !strings.Contains(w[0], "attachment host not trusted") {
		t.Fatalf("warnings = %v, want one about the untrusted host", w)
	}
	if strings.Contains(w[0], "aaaaaaaa") {
		t.Errorf("warning quoted the URL, not just the host: %q", w[0])
	}
}

func TestAttachmentsSkipsUntrustedInlineImageHost(t *testing.T) {
	fs, c := newFixtureServer(t)
	foreign := foreignServer(t)

	// Repoint only the inline <img src> — the attacker-editable one — at
	// the foreign host.
	fs.mu.Lock()
	fs.rewrite = func(body string) string {
		return strings.Replace(body,
			fs.URL+"/Fabrikam/_apis/wit/attachments/bbbbbbbb",
			foreign.URL+"/Fabrikam/_apis/wit/attachments/bbbbbbbb", 1)
	}
	fs.mu.Unlock()

	dir := filepath.Join(t.TempDir(), "attachments")
	atts, err := c.Helpdesk().Attachments(context.Background(), "4242", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(atts) != 1 || atts[0].Name != "evil log.txt" {
		t.Fatalf("attachments = %+v, want only the relation on the trusted host", atts)
	}
	w := c.WarningsFor("4242")
	if len(w) != 1 || !strings.Contains(w[0], "attachment host not trusted") {
		t.Fatalf("warnings = %v, want one about the untrusted host", w)
	}
}

// TestDownloadRefusesAnOffHostRedirect: Azure DevOps answers an
// unauthenticated or expired attachment request with a redirect to the
// sign-in page rather than a 401, and a Location header is server output
// like any other. Following one off the org's hosts would put the request on
// a host that was never trust-checked and write whatever came back to disk
// under the attachment's name.
func TestDownloadRefusesAnOffHostRedirect(t *testing.T) {
	fs, c := newFixtureServer(t)
	foreign := foreignServer(t)

	fs.mu.Lock()
	fs.redirectAttachmentsTo = foreign.URL + "/signin"
	fs.mu.Unlock()

	dir := filepath.Join(t.TempDir(), "attachments")
	atts, err := c.Helpdesk().Attachments(context.Background(), "4242", dir)
	if err == nil {
		t.Fatal("every download redirected off-host but Attachments returned no error")
	}
	if len(atts) != 0 {
		t.Errorf("attachments = %+v, want none", atts)
	}
	// foreignServer fails the test itself if it is ever called, so the
	// only thing left to check is that nothing was written.
	entries, rerr := os.ReadDir(dir)
	if rerr == nil && len(entries) != 0 {
		t.Errorf("files were written for the refused downloads: %v", entries)
	}
}

func TestDownloadRefusesUntrustedHostDirectly(t *testing.T) {
	_, c := newFixtureServer(t)
	foreign := foreignServer(t)

	dest := filepath.Join(t.TempDir(), "out.bin")
	if _, err := c.download(context.Background(), foreign.URL+"/x", dest); err == nil {
		t.Fatal("download of an untrusted URL returned no error")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("download wrote a file for an untrusted URL")
	}
}

func TestTrusted(t *testing.T) {
	for _, tc := range []struct {
		name, base, target string
		want               bool
	}{
		{"services same host", "https://dev.azure.com/contoso", "https://dev.azure.com/contoso/Fabrikam/_apis/wit/attachments/g", true},
		{"services legacy host for the same org", "https://dev.azure.com/contoso", "https://contoso.visualstudio.com/_apis/wit/attachments/g", true},
		{"legacy base trusts dev.azure.com", "https://contoso.visualstudio.com", "https://dev.azure.com/contoso/_apis/wit/attachments/g", true},
		{"another org's legacy host", "https://dev.azure.com/contoso", "https://fabrikam.visualstudio.com/_apis/wit/attachments/g", false},
		{"foreign host", "https://dev.azure.com/contoso", "https://attacker.example/x", false},
		{"lookalike host", "https://dev.azure.com/contoso", "https://dev.azure.com.attacker.example/x", false},
		{"case and trailing dot", "https://dev.azure.com/contoso", "https://DEV.AZURE.COM./contoso/x", true},
		{"non-http scheme", "https://dev.azure.com/contoso", "file:///etc/passwd", false},
		{"server collection, same host", "https://tfs.corp:8080/DefaultCollection", "https://tfs.corp:8080/DefaultCollection/_apis/wit/attachments/g", true},
		{"server collection, different port", "https://tfs.corp:8080/DefaultCollection", "https://tfs.corp:9999/x", false},
		{"server collection does not trust dev.azure.com", "https://tfs.corp:8080/DefaultCollection", "https://dev.azure.com/contoso/x", false},
		{"http allowed only on the base scheme", "http://tfs.corp/DefaultCollection", "http://tfs.corp/DefaultCollection/x", true},
		{"http foreign host", "http://tfs.corp/DefaultCollection", "http://attacker.example/x", false},
		{"https base rejects http", "https://dev.azure.com/contoso", "http://dev.azure.com/contoso/x", false},
	} {
		c, err := New(Config{OrgURL: tc.base, Project: testProject, PAT: testPAT}, nil)
		if err != nil {
			t.Fatalf("%s: New: %v", tc.name, err)
		}
		u, err := url.Parse(tc.target)
		if err != nil {
			t.Fatalf("%s: parse target: %v", tc.name, err)
		}
		if got := c.trusted(u); got != tc.want {
			t.Errorf("%s: trusted(%q) with base %q = %v, want %v", tc.name, tc.target, tc.base, got, tc.want)
		}
	}
}
