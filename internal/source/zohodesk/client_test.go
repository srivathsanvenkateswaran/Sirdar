package zohodesk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
)

const (
	testOrgID = "org1"
	testToken = "tok-secret"
)

// checkHeaders asserts the two required headers are present on every request.
func checkHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("orgId"); got != testOrgID {
		t.Errorf("orgId header = %q, want %q (path %s)", got, testOrgID, r.URL.Path)
	}
	if got := r.Header.Get("Authorization"); got != "Zoho-oauthtoken "+testToken {
		t.Errorf("Authorization header = %q, want %q (path %s)", got, "Zoho-oauthtoken "+testToken, r.URL.Path)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// serverOpts controls status overrides for the happy-path fixture server.
type serverOpts struct {
	ticketStatus int // override status code for GET /api/v1/tickets/555 (0 = 200)
}

// newFixtureServer serves ticket.json, conversations.json, thread.json (keyed by
// thread id) and the three attachment download paths referenced by those
// fixtures. It asserts the auth headers on every request.
func newFixtureServer(t *testing.T, opts serverOpts) (*httptest.Server, *Client) {
	t.Helper()

	ticketJSON := mustReadFile(t, "testdata/ticket.json")
	conversationsJSON := mustReadFile(t, "testdata/conversations.json")
	threadJSON := mustReadFile(t, "testdata/thread.json")
	var threadsByID map[string]json.RawMessage
	if err := json.Unmarshal(threadJSON, &threadsByID); err != nil {
		t.Fatalf("parse thread.json: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tickets/555", func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		if opts.ticketStatus != 0 {
			w.WriteHeader(opts.ticketStatus)
			w.Write([]byte(`{"message":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(ticketJSON)
	})
	mux.HandleFunc("/api/v1/tickets/555/conversations", func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write(conversationsJSON)
	})
	mux.HandleFunc("/api/v1/tickets/555/threads/", func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/555/threads/")
		body, ok := threadsByID[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	mux.HandleFunc("/api/v1/tickets/555/attachments/a1/content", func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA-a1"))
	})
	mux.HandleFunc("/supportapi/x/inlineattachments/i9", func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA-i9"))
	})
	mux.HandleFunc("/api/v1/im/sessions/s1/attachments/c3/content", func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("PDFDATA-c3"))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewWithToken(srv.URL, testOrgID, testToken)
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL
	return srv, c
}

func TestNewWithToken(t *testing.T) {
	c := NewWithToken("https://example.zohodesk.com", "org1", "tok")
	if c.BaseURL != "https://example.zohodesk.com" || c.OrgID != "org1" {
		t.Fatalf("unexpected client: %+v", c)
	}
	got, err := c.Tokens.Token(context.Background())
	if err != nil || got != "tok" {
		t.Fatalf("Tokens.Token() = %q, %v; want %q", got, err, "tok")
	}
	if c.HTTP == nil || c.HTTP.Timeout != 30*time.Second {
		t.Fatalf("expected default 30s HTTP client, got %+v", c.HTTP)
	}
}

func TestGet_MapsFields(t *testing.T) {
	_, c := newFixtureServer(t, serverOpts{})

	got, err := c.Get(context.Background(), "555")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != "555" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Subject != "Export fails" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.Status != "Open" {
		t.Errorf("Status = %q", got.Status)
	}
	if got.Priority != "High" {
		t.Errorf("Priority = %q", got.Priority)
	}
	if got.Channel != "EMAIL" {
		t.Errorf("Channel = %q", got.Channel)
	}
	if got.Contact != "John Doe" {
		t.Errorf("Contact = %q", got.Contact)
	}
	if got.Customer != "Acme Co" {
		t.Errorf("Customer = %q", got.Customer)
	}
	if got.CustomerID != "4561" {
		t.Errorf("CustomerID = %q", got.CustomerID)
	}
	if got.URL != "https://desk.zoho.com/support/555" {
		t.Errorf("URL = %q", got.URL)
	}
	wantCreated := time.Date(2026, 9, 10, 6, 0, 0, 0, time.UTC)
	if !got.CreatedAt.Equal(wantCreated) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, wantCreated)
	}
	wantUpdated := time.Date(2026, 9, 10, 6, 30, 0, 0, time.UTC)
	if !got.UpdatedAt.Equal(wantUpdated) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, wantUpdated)
	}
	if got.Fields["departmentId"] != "d1" {
		t.Errorf("Fields[departmentId] = %q", got.Fields["departmentId"])
	}
	if got.Fields["ticketNumber"] != "1001" {
		t.Errorf("Fields[ticketNumber] = %q", got.Fields["ticketNumber"])
	}
	if got.Fields["email"] != "john@example.com" {
		t.Errorf("Fields[email] = %q", got.Fields["email"])
	}
	if got.Fields["phone"] != "+1234567890" {
		t.Errorf("Fields[phone] = %q", got.Fields["phone"])
	}
}

func TestThreads_OrderRoleAndAttachmentIDs(t *testing.T) {
	_, c := newFixtureServer(t, serverOpts{})

	got, err := c.Threads(context.Background(), "555")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(Threads) = %d, want 3: %+v", len(got), got)
	}

	// Ascending by At: comment c1 (07:00) < thread t1 (08:00) < thread t2 (09:00),
	// even though t1 precedes c1 in the conversations.json array.
	if got[0].Author != "Bob" || got[0].Text != "Working on it" {
		t.Errorf("msg0 = %+v", got[0])
	}
	if got[0].Role != "agent" {
		t.Errorf("msg0.Role = %q, want agent", got[0].Role)
	}
	if got[1].Author != "Alice" || got[1].Text != "Hello from thread one" {
		t.Errorf("msg1 = %+v", got[1])
	}
	if got[1].Role != "customer" {
		t.Errorf("msg1.Role = %q, want customer (direction in)", got[1].Role)
	}
	if got[2].Author != "Agent Carol" || got[2].Text != "Thread two text" {
		t.Errorf("msg2 = %+v", got[2])
	}
	if got[2].Role != "agent" {
		t.Errorf("msg2.Role = %q, want agent (direction out)", got[2].Role)
	}

	for i := 1; i < len(got); i++ {
		if got[i].At.Before(got[i-1].At) {
			t.Fatalf("messages not sorted ascending by At: %+v", got)
		}
	}

	// t1's message should reference both its direct attachment (a1) and its
	// inline image (i9); t2's should reference its IM-session attachment (c3).
	if !containsAll(got[1].AttachmentIDs, "a1", "i9") {
		t.Errorf("msg1.AttachmentIDs = %v, want a1 and i9", got[1].AttachmentIDs)
	}
	if !containsAll(got[2].AttachmentIDs, "c3") {
		t.Errorf("msg2.AttachmentIDs = %v, want c3", got[2].AttachmentIDs)
	}
	if len(got[0].AttachmentIDs) != 0 {
		t.Errorf("msg0.AttachmentIDs = %v, want none", got[0].AttachmentIDs)
	}
}

func containsAll(haystack []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, h := range haystack {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestThreads_CommentRoleMapping(t *testing.T) {
	// Ad hoc server for role-mapping edge cases not covered by the shared
	// fixture: a CONTACT commenter maps to customer, and a non-public comment
	// gets " (internal)" appended to its Author.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tickets/9/conversations", func(w http.ResponseWriter, r *http.Request) {
		checkHeaders(t, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[
			{"id":"c1","type":"comment","commenter":{"name":"Dana"},"commenterType":"CONTACT","isPublic":true,"content":"help","commentedTime":"2026-09-10T07:00:00.000Z"},
			{"id":"c2","type":"comment","commenter":{"name":"Eve"},"commenterType":"AGENT","isPublic":false,"content":"note to self","commentedTime":"2026-09-10T08:00:00.000Z"}
		]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewWithToken(srv.URL, testOrgID, testToken)
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL

	got, err := c.Threads(context.Background(), "9")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].Role != "customer" {
		t.Errorf("CONTACT commenter role = %q, want customer", got[0].Role)
	}
	if got[1].Role != "agent" {
		t.Errorf("AGENT commenter role = %q, want agent", got[1].Role)
	}
	if got[1].Author != "Eve (internal)" {
		t.Errorf("non-public comment author = %q, want suffix (internal)", got[1].Author)
	}
}

func TestAttachments_DownloadsThreeInOrder(t *testing.T) {
	_, c := newFixtureServer(t, serverOpts{})
	dir := t.TempDir()

	got, err := c.Attachments(context.Background(), "555", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(Attachments) = %d, want 3: %+v", len(got), got)
	}
	if w := c.WarningsFor("555"); len(w) != 0 {
		t.Errorf("WarningsFor(555) = %v, want none", w)
	}

	wantPrefixes := []string{"1-", "2-", "3-"}
	for i, a := range got {
		if !strings.HasPrefix(filepath.Base(a.Path), wantPrefixes[i]) {
			t.Errorf("attachment %d Path = %q, want prefix %q", i, a.Path, wantPrefixes[i])
		}
		// a.Path is "<base(dir)>/<index>-<name>"; the file lives directly
		// under dir with that same "<index>-<name>" basename.
		full := filepath.Join(dir, filepath.Base(a.Path))
		if _, err := os.Stat(full); err != nil {
			t.Errorf("attachment %d not found on disk at %s: %v", i, full, err)
		}
	}

	if got[0].ID != "a1" || got[0].Name != "shot.png" || got[0].MIME != "image/png" {
		t.Errorf("attachment 0 = %+v", got[0])
	}
	if got[1].ID != "i9" || got[1].MIME != "image/png" {
		t.Errorf("attachment 1 = %+v", got[1])
	}
	if got[2].ID != "c3" || got[2].Name != "doc.pdf" || got[2].MIME != "application/pdf" {
		t.Errorf("attachment 2 = %+v", got[2])
	}
}

func TestAttachments_PartialFailureRecordsWarning(t *testing.T) {
	ticketJSON := mustReadFile(t, "testdata/ticket.json")
	conversationsJSON := mustReadFile(t, "testdata/conversations.json")
	threadJSON := mustReadFile(t, "testdata/thread.json")
	var threadsByID map[string]json.RawMessage
	if err := json.Unmarshal(threadJSON, &threadsByID); err != nil {
		t.Fatalf("parse thread.json: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tickets/555", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(ticketJSON)
	})
	mux.HandleFunc("/api/v1/tickets/555/conversations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(conversationsJSON)
	})
	mux.HandleFunc("/api/v1/tickets/555/threads/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/555/threads/")
		w.Header().Set("Content-Type", "application/json")
		w.Write(threadsByID[id])
	})
	// a1 fails, i9 and c3 succeed.
	mux.HandleFunc("/api/v1/tickets/555/attachments/a1/content", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/supportapi/x/inlineattachments/i9", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA-i9"))
	})
	mux.HandleFunc("/api/v1/im/sessions/s1/attachments/c3/content", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("PDFDATA-c3"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewWithToken(srv.URL, testOrgID, testToken)
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL

	dir := t.TempDir()
	got, err := c.Attachments(context.Background(), "555", dir)
	if err != nil {
		t.Fatalf("Attachments returned error on partial failure: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(Attachments) = %d, want 2: %+v", len(got), got)
	}
	if w := c.WarningsFor("555"); len(w) != 1 {
		t.Fatalf("WarningsFor(555) = %v, want 1 entry", w)
	}
	// Reading them consumes them: a second read must not report the same
	// missing attachment against whatever runs next.
	if w := c.WarningsFor("555"); len(w) != 0 {
		t.Fatalf("WarningsFor(555) after reading = %v, want none", w)
	}

	// A second call records its own warnings rather than adding to the
	// first call's.
	if _, err := c.Attachments(context.Background(), "555", t.TempDir()); err != nil {
		t.Fatalf("second Attachments call: %v", err)
	}
	if w := c.WarningsFor("555"); len(w) != 1 {
		t.Fatalf("warnings not reset between calls: %v", w)
	}
}

func TestAttachments_AllFail(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tickets/555/conversations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"t1","type":"thread","direction":"in","author":{"name":"Alice"},"createdTime":"2026-09-10T08:00:00.000Z"}]}`)
	})
	mux.HandleFunc("/api/v1/tickets/555/threads/t1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plainText":"hi","content":"<p>no images</p>","attachments":[{"id":"a1","name":"shot.png","href":"/api/v1/tickets/555/attachments/a1/content"}]}`)
	})
	mux.HandleFunc("/api/v1/tickets/555/attachments/a1/content", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewWithToken(srv.URL, testOrgID, testToken)
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL

	got, err := c.Attachments(context.Background(), "555", t.TempDir())
	if err == nil {
		t.Fatal("expected error when every attachment download fails")
	}
	if len(got) != 0 {
		t.Errorf("got %d attachments, want 0", len(got))
	}
}

func TestGet_AuthError(t *testing.T) {
	_, c := newFixtureServer(t, serverOpts{ticketStatus: http.StatusUnauthorized})
	_, err := c.Get(context.Background(), "555")
	assertSourceError(t, err, source.Auth)
}

func TestGet_Forbidden(t *testing.T) {
	_, c := newFixtureServer(t, serverOpts{ticketStatus: http.StatusForbidden})
	_, err := c.Get(context.Background(), "555")
	assertSourceError(t, err, source.Auth)
}

func TestGet_NotFound(t *testing.T) {
	_, c := newFixtureServer(t, serverOpts{ticketStatus: http.StatusNotFound})
	_, err := c.Get(context.Background(), "555")
	assertSourceError(t, err, source.NotFound)
}

func TestGet_RateLimited(t *testing.T) {
	_, c := newFixtureServer(t, serverOpts{ticketStatus: http.StatusTooManyRequests})
	_, err := c.Get(context.Background(), "555")
	assertSourceError(t, err, source.RateLimited)
}

func TestGet_InternalError(t *testing.T) {
	_, c := newFixtureServer(t, serverOpts{ticketStatus: http.StatusInternalServerError})
	_, err := c.Get(context.Background(), "555")
	assertSourceError(t, err, source.Internal)
}

func assertSourceError(t *testing.T, err error, want source.Code) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(*source.Error)
	if !ok {
		t.Fatalf("error type = %T, want *source.Error: %v", err, err)
	}
	if se.Code != want {
		t.Fatalf("code = %q, want %q", se.Code, want)
	}
}

func TestConversations_Pagination(t *testing.T) {
	var gotFrom []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tickets/9/conversations", func(w http.ResponseWriter, r *http.Request) {
		from := r.URL.Query().Get("from")
		gotFrom = append(gotFrom, from)
		w.Header().Set("Content-Type", "application/json")
		if from == "" || from == "0" {
			entries := make([]string, 100)
			for i := range entries {
				entries[i] = fmt.Sprintf(`{"id":"c%d","type":"comment","commenter":{"name":"Bob"},"commenterType":"AGENT","isPublic":true,"content":"msg %d","commentedTime":"2026-09-10T07:%02d:00.000Z"}`, i, i, i%60)
			}
			fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(entries, ","))
			return
		}
		entries := make([]string, 20)
		for i := range entries {
			entries[i] = fmt.Sprintf(`{"id":"d%d","type":"comment","commenter":{"name":"Bob"},"commenterType":"AGENT","isPublic":true,"content":"msg2 %d","commentedTime":"2026-09-10T09:%02d:00.000Z"}`, i, i, i%60)
		}
		fmt.Fprintf(w, `{"data":[%s]}`, strings.Join(entries, ","))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewWithToken(srv.URL, testOrgID, testToken)
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL

	got, err := c.Threads(context.Background(), "9")
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(got) != 120 {
		t.Fatalf("len(Threads) = %d, want 120", len(got))
	}
	if len(gotFrom) != 2 {
		t.Fatalf("requests made = %d, want 2: %v", len(gotFrom), gotFrom)
	}
	if gotFrom[0] != "" && gotFrom[0] != "0" {
		t.Errorf("first request from = %q, want empty or 0", gotFrom[0])
	}
	if gotFrom[1] != "100" {
		t.Errorf("second request from = %q, want 100", gotFrom[1])
	}
}

func TestResolveURL_RelativePath(t *testing.T) {
	c := NewWithToken("https://desk.example.com", testOrgID, testToken)

	got := c.resolveURL("api/v1/tickets/555/attachments/a7/content")
	want := "https://desk.example.com/api/v1/tickets/555/attachments/a7/content"
	if got != want {
		t.Errorf("resolveURL(no leading slash) = %q, want %q", got, want)
	}

	got = c.resolveURL("/api/v1/tickets/555/attachments/a7/content")
	if got != want {
		t.Errorf("resolveURL(leading slash) = %q, want %q", got, want)
	}

	abs := "https://other.example.com/x"
	if got := c.resolveURL(abs); got != abs {
		t.Errorf("resolveURL(absolute) = %q, want unchanged %q", got, abs)
	}
}

// TestAttachments_SanitizesPathTraversalName covers a thread attachment whose
// API-supplied name is a path-traversal payload: the file must land inside
// dir (as "<index>-evil.txt"), and nothing must be written outside dir.
func TestAttachments_SanitizesPathTraversalName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tickets/555/conversations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"t1","type":"thread","direction":"in","author":{"name":"Alice"},"createdTime":"2026-09-10T08:00:00.000Z"}]}`)
	})
	mux.HandleFunc("/api/v1/tickets/555/threads/t1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plainText":"hi","content":"<p>no images</p>","attachments":[{"id":"a1","name":"../../evil.txt","href":"/api/v1/tickets/555/attachments/a1/content"}]}`)
	})
	mux.HandleFunc("/api/v1/tickets/555/attachments/a1/content", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("evil payload"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewWithToken(srv.URL, testOrgID, testToken)
	c.HTTP = srv.Client()
	c.BaseURL = srv.URL

	base := t.TempDir()
	dir := filepath.Join(base, "out")

	got, err := c.Attachments(context.Background(), "555", dir)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Attachments) = %d, want 1: %+v", len(got), got)
	}

	wantPath := filepath.Base(dir) + "/1-evil.txt"
	if got[0].Path != wantPath {
		t.Errorf("Path = %q, want %q", got[0].Path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(dir, "1-evil.txt")); err != nil {
		t.Errorf("expected file at %s: %v", filepath.Join(dir, "1-evil.txt"), err)
	}

	// Nothing must have escaped dir: no "evil.txt" anywhere in base other
	// than inside dir, and no writes above base either.
	if _, err := os.Stat(filepath.Join(base, "evil.txt")); !os.IsNotExist(err) {
		t.Errorf("file escaped into %s: %v", base, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(base), "evil.txt")); !os.IsNotExist(err) {
		t.Errorf("file escaped above %s: %v", base, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(dir): %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "1-evil.txt" {
		t.Errorf("dir contents = %v, want exactly [1-evil.txt]", entries)
	}
}

// warningsServer serves two tickets, each with one attachment that fails to
// download, so a concurrent Attachments call for each has something to
// record. Ticket ids are "a" and "b"; the failing attachment's id names the
// ticket, which is how a leak between them shows up.
func newWarningsServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for _, id := range []string{"a", "b"} {
		id := id
		mux.HandleFunc("/api/v1/tickets/"+id+"/conversations", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"t1","type":"thread","direction":"in","author":{"name":"Alice"},"createdTime":"2026-09-10T08:00:00.000Z"}]}`)
		})
		mux.HandleFunc("/api/v1/tickets/"+id+"/threads/t1", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"plainText":"hi","content":"<p>x</p>","attachments":[`+
				`{"id":"ok-%s","name":"ok-%s.txt","href":"/api/v1/tickets/%s/attachments/ok/content"},`+
				`{"id":"gone-%s","name":"gone-%s.txt","href":"/api/v1/tickets/%s/attachments/gone/content"}]}`,
				id, id, id, id, id, id)
		})
		mux.HandleFunc("/api/v1/tickets/"+id+"/attachments/ok/content", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("fine"))
		})
		mux.HandleFunc("/api/v1/tickets/"+id+"/attachments/gone/content", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "gone", http.StatusNotFound)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestAttachments_ConcurrentCallsKeepWarningsApart covers the shape a batch
// run has: one Client, several tickets in flight. Under -race this also
// catches the unsynchronised write the warnings used to be. A ticket must
// never be told an attachment is missing when the missing one belongs to
// another ticket.
func TestAttachments_ConcurrentCallsKeepWarningsApart(t *testing.T) {
	srv := newWarningsServer(t)
	c := NewWithToken(srv.URL, testOrgID, testToken)
	c.HTTP = srv.Client()

	var wg sync.WaitGroup
	warnings := map[string][]string{}
	var mu sync.Mutex
	for _, id := range []string{"a", "b"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Attachments(context.Background(), id, t.TempDir()); err != nil {
				t.Errorf("Attachments(%s): %v", id, err)
				return
			}
			w := c.WarningsFor(id)
			mu.Lock()
			warnings[id] = w
			mu.Unlock()
		}()
	}
	wg.Wait()

	for _, id := range []string{"a", "b"} {
		got := warnings[id]
		if len(got) != 1 {
			t.Fatalf("ticket %s warnings = %v, want exactly 1", id, got)
		}
		if !strings.Contains(got[0], "gone-"+id) {
			t.Errorf("ticket %s got another ticket's warning: %q", id, got[0])
		}
	}
}

// TestAttachments_ClearsWarningsOnAnEarlyReturn covers a ticket with no
// attachments at all following one that had a failure: the earlier call's
// warning must not be served against it.
func TestAttachments_ClearsWarningsOnAnEarlyReturn(t *testing.T) {
	mux := http.NewServeMux()
	calls := 0
	mux.HandleFunc("/api/v1/tickets/555/conversations", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"t1","type":"thread","direction":"in","author":{"name":"Alice"},"createdTime":"2026-09-10T08:00:00.000Z"}]}`)
	})
	mux.HandleFunc("/api/v1/tickets/555/threads/t1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			fmt.Fprint(w, `{"plainText":"hi","content":"<p>x</p>","attachments":[`+
				`{"id":"ok","name":"ok.txt","href":"/api/v1/tickets/555/attachments/ok/content"},`+
				`{"id":"gone","name":"gone.txt","href":"/api/v1/tickets/555/attachments/gone/content"}]}`)
			return
		}
		// The second time round the ticket has no attachments at all.
		fmt.Fprint(w, `{"plainText":"hi","content":"<p>x</p>"}`)
	})
	mux.HandleFunc("/api/v1/tickets/555/attachments/ok/content", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("fine"))
	})
	mux.HandleFunc("/api/v1/tickets/555/attachments/gone/content", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewWithToken(srv.URL, testOrgID, testToken)
	c.HTTP = srv.Client()

	if _, err := c.Attachments(context.Background(), "555", t.TempDir()); err != nil {
		t.Fatalf("first Attachments: %v", err)
	}
	// Deliberately not read: the point is that the next call discards it.
	got, err := c.Attachments(context.Background(), "555", t.TempDir())
	if err != nil {
		t.Fatalf("second Attachments: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("second call returned %d attachments, want 0", len(got))
	}
	if w := c.WarningsFor("555"); len(w) != 0 {
		t.Fatalf("a ticket with no attachments inherited warnings: %v", w)
	}
}

// TestAttachments_AllFailDoesNotAlsoWarn covers the duplicate report: when
// every attachment fails, the caller already has each failure in the error,
// and repeating them as warnings put the same line in the prompt twice.
func TestAttachments_AllFailDoesNotAlsoWarn(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tickets/555/conversations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"t1","type":"thread","direction":"in","author":{"name":"Alice"},"createdTime":"2026-09-10T08:00:00.000Z"}]}`)
	})
	mux.HandleFunc("/api/v1/tickets/555/threads/t1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plainText":"hi","content":"<p>x</p>","attachments":[`+
			`{"id":"gone","name":"gone.txt","href":"/api/v1/tickets/555/attachments/gone/content"}]}`)
	})
	mux.HandleFunc("/api/v1/tickets/555/attachments/gone/content", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewWithToken(srv.URL, testOrgID, testToken)
	c.HTTP = srv.Client()

	_, err := c.Attachments(context.Background(), "555", t.TempDir())
	if err == nil {
		t.Fatal("want an error when every attachment fails")
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Errorf("error does not name the failure: %v", err)
	}
	if w := c.WarningsFor("555"); len(w) != 0 {
		t.Fatalf("failures were reported twice, as an error and as warnings: %v", w)
	}
}
