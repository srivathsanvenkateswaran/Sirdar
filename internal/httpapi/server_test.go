package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// do issues one request against a handler built on f and returns the
// recorded response. A nil body sends no body at all.
func do(t *testing.T, f *fake, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	New(f, emptyFS{}).ServeHTTP(w, r)
	return w
}

// decodeJSON asserts the response is JSON with the expected status and
// unmarshals it into v.
func decodeJSON(t *testing.T, w *httptest.ResponseRecorder, want int, v any) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d; body %s", w.Code, want, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	if v == nil {
		return
	}
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
}

// assertError checks the error envelope the spec fixes for every failure.
func assertError(t *testing.T, w *httptest.ResponseRecorder, want int, code string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, w, want, &body)
	if body.Error.Code != code {
		t.Fatalf("code %q, want %q", body.Error.Code, code)
	}
	if body.Error.Message == "" {
		t.Fatal("error message is empty")
	}
}

func TestListWorkspaces(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces", "")

	var got []map[string]any
	decodeJSON(t, w, 200, &got)
	if len(got) != 1 {
		t.Fatalf("got %d workspaces", len(got))
	}
	// The frontend reads camelCase; a Go-cased key here would break it.
	for _, k := range []string{"id", "name", "root", "provider", "model", "notesDir"} {
		if _, ok := got[0][k]; !ok {
			t.Errorf("workspace has no %q: %v", k, got[0])
		}
	}
}

func TestListWorkspacesEmptyIsArray(t *testing.T) {
	f := newFake()
	f.workspaces = nil
	w := do(t, f, "GET", "/api/workspaces", "")
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("body %q, want an empty array", got)
	}
}

func TestAddWorkspace(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/workspaces", `{"root":"/repos/other"}`)

	var got Workspace
	decodeJSON(t, w, 200, &got)
	if got.Root != "/repos/other" {
		t.Fatalf("root %q", got.Root)
	}
	if f.gotRoot != "/repos/other" {
		t.Fatalf("service got root %q", f.gotRoot)
	}
}

func TestAddWorkspaceBadBodies(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":     `{"root":`,
		"unknown field": `{"root":"/r","depth":3}`,
		"missing root":  `{}`,
		"empty body":    "",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			var w *httptest.ResponseRecorder
			if body == "" {
				w = httptest.NewRecorder()
				New(f, nil).ServeHTTP(w, httptest.NewRequest("POST", "/api/workspaces", nil))
			} else {
				w = do(t, f, "POST", "/api/workspaces", body)
			}
			assertError(t, w, 400, "bad_request")
		})
	}
}

func TestRemoveWorkspace(t *testing.T) {
	f := newFake()
	w := do(t, f, "DELETE", "/api/workspaces/"+knownWS, "")
	if w.Code != 204 {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	if f.gotRemoved != knownWS {
		t.Fatalf("removed %q", f.gotRemoved)
	}
}

func TestRemoveUnknownWorkspace(t *testing.T) {
	assertError(t, do(t, newFake(), "DELETE", "/api/workspaces/nope", ""), 404, "not_found")
}

func TestQueue(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/queue?assignee=sri&status=Open&limit=20", "")

	var got []Ticket
	decodeJSON(t, w, 200, &got)
	if len(got) != 1 || got[0].Key != "OMNI-2510" {
		t.Fatalf("tickets %+v", got)
	}
	if want := (QueueFilter{Assignee: "sri", Status: "Open", Limit: 20}); f.gotFilter != want {
		t.Fatalf("filter %+v, want %+v", f.gotFilter, want)
	}
}

func TestQueueBadLimit(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/queue?limit=soon", ""), 400, "bad_request")
}

func TestQueueUnsupported(t *testing.T) {
	f := newFake()
	f.queueUnsupported = true
	// A workspace with no tracker is a 501, not an error the UI should
	// show as a failure.
	assertError(t, do(t, f, "GET", "/api/workspaces/"+knownWS+"/queue", ""), 501, "unsupported")
}

func TestQueueUnknownWorkspace(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/nope/queue", ""), 404, "not_found")
}

func TestQueueAdapterFailureIsServerError(t *testing.T) {
	f := newFake()
	// A tracker adapter that is not installed reaches the handler as a
	// file error. It is not a missing resource, and answering 404 would
	// have the UI show an empty queue instead of the breakage.
	f.queueErr = fmt.Errorf("sources.tracker: start adapter: %w", fs.ErrNotExist)
	assertError(t, do(t, f, "GET", "/api/workspaces/"+knownWS+"/queue", ""), 500, "internal")
}

func TestRuns(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs?key=OMNI-2510", "")

	var got []map[string]any
	decodeJSON(t, w, 200, &got)
	if len(got) != 1 || got[0]["runId"] != knownRun {
		t.Fatalf("runs %+v", got)
	}
	if usage, ok := got[0]["usage"].(map[string]any); !ok || usage["costUsd"] != 0.42 {
		t.Fatalf("usage %+v", got[0]["usage"])
	}
	if f.gotKey != "OMNI-2510" {
		t.Fatalf("key filter %q", f.gotKey)
	}
}

func TestRunDetail(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun, "")

	var got map[string]any
	decodeJSON(t, w, 200, &got)
	for _, k := range []string{"runId", "status", "promptPath", "bundleDir", "warnings", "handle", "budget"} {
		if _, ok := got[k]; !ok {
			t.Errorf("detail has no %q", k)
		}
	}
	if b, ok := got["budget"].(map[string]any); !ok || b["maxUsd"] != float64(5) {
		t.Fatalf("budget %+v", got["budget"])
	}
}

func TestRunDetailUnknownRun(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/runs/nope", ""), 404, "not_found")
}

func TestRunEvents(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/events?after=1", "")

	var got struct {
		Events []RunEvent `json:"events"`
		Next   int        `json:"next"`
	}
	decodeJSON(t, w, 200, &got)
	if len(got.Events) != 1 || got.Next != 2 {
		t.Fatalf("events %+v next %d", got.Events, got.Next)
	}
	if f.gotAfter != 1 {
		t.Fatalf("after %d", f.gotAfter)
	}
}

func TestRunEventsDefaultsToStart(t *testing.T) {
	f := newFake()
	var got struct {
		Events []RunEvent `json:"events"`
		Next   int        `json:"next"`
	}
	decodeJSON(t, do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/events", ""), 200, &got)
	if len(got.Events) != 2 {
		t.Fatalf("events %+v", got.Events)
	}
}

func TestRunEventsBadAfter(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/events?after=-1", ""), 400, "bad_request")
}

func TestNote(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/note?kind=rca", "")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	if w.Body.String() != f.note {
		t.Fatalf("body %q", w.Body.String())
	}
	if f.gotNoteKind != "rca" {
		t.Fatalf("kind %q", f.gotNoteKind)
	}
}

func TestNoteBadKind(t *testing.T) {
	for _, kind := range []string{"", "summary"} {
		assertError(t, do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/note?kind="+kind, ""), 400, "bad_request")
	}
}

func TestNoteMissingFileIsNotFound(t *testing.T) {
	f := newFake()
	f.noteMissing = true
	assertError(t, do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/note?kind=triage", ""), 404, "not_found")
}

func TestPrompt(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/prompt", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Fatalf("status %d, content type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w.Body.String() != f.prompt {
		t.Fatalf("body %q", w.Body.String())
	}
}

func TestStartTriage(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/workspaces/"+knownWS+"/triage",
		`{"keys":["OMNI-1","OMNI-2"],"provider":"codex","model":"gpt","dryRun":true}`)

	var got jobResponse
	decodeJSON(t, w, 202, &got)
	if got.JobID != knownJob {
		t.Fatalf("jobId %q", got.JobID)
	}
	if len(f.gotKeys) != 2 || f.gotKeys[0] != "OMNI-1" {
		t.Fatalf("keys %v", f.gotKeys)
	}
	if want := (TriageOptions{Provider: "codex", Model: "gpt", DryRun: true}); f.gotTriage != want {
		t.Fatalf("options %+v", f.gotTriage)
	}
}

func TestStartTriageNoKeys(t *testing.T) {
	assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/triage", `{"keys":[]}`), 400, "bad_request")
}

func TestStartTriageUnknownField(t *testing.T) {
	assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/triage", `{"keys":["A"],"concurrency":3}`), 400, "bad_request")
}

func TestStartTriageUnknownWorkspace(t *testing.T) {
	assertError(t, do(t, newFake(), "POST", "/api/workspaces/nope/triage", `{"keys":["A"]}`), 404, "not_found")
}

func TestStartRCA(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/workspaces/"+knownWS+"/rca",
		`{"key":"OMNI-2510","prUrl":"https://gh/pr/1","resolution":"revert"}`)

	var got jobResponse
	decodeJSON(t, w, 202, &got)
	if f.gotRCAKey != "OMNI-2510" {
		t.Fatalf("key %q", f.gotRCAKey)
	}
	if want := (RCAOptions{PRURL: "https://gh/pr/1", Resolution: "revert"}); f.gotRCA != want {
		t.Fatalf("options %+v", f.gotRCA)
	}
}

func TestStartRCANoKey(t *testing.T) {
	assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/rca", `{"prUrl":"x"}`), 400, "bad_request")
}

func TestResume(t *testing.T) {
	f := newFake()
	var got jobResponse
	decodeJSON(t, do(t, f, "POST", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/resume", `{"answer":"yes"}`), 202, &got)
	if got.JobID != knownJob || f.gotAnswer != "yes" {
		t.Fatalf("job %q answer %q", got.JobID, f.gotAnswer)
	}
}

func TestResumeWithoutBody(t *testing.T) {
	// Resuming an interrupted run answers no question, so the UI sends
	// nothing at all.
	f := newFake()
	w := httptest.NewRecorder()
	New(f, nil).ServeHTTP(w, httptest.NewRequest("POST", "/api/workspaces/"+knownWS+"/runs/"+knownRun+"/resume", nil))
	var got jobResponse
	decodeJSON(t, w, 202, &got)
	if got.JobID != knownJob {
		t.Fatalf("jobId %q", got.JobID)
	}
}

func TestResumeUnknownRun(t *testing.T) {
	assertError(t, do(t, newFake(), "POST", "/api/workspaces/"+knownWS+"/runs/nope/resume", `{}`), 404, "not_found")
}

func TestCancel(t *testing.T) {
	f := newFake()
	w := do(t, f, "POST", "/api/jobs/"+knownJob+"/cancel", "")
	if w.Code != 202 {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	if f.gotCancelled != knownJob {
		t.Fatalf("cancelled %q", f.gotCancelled)
	}
}

func TestCancelUnknownJob(t *testing.T) {
	assertError(t, do(t, newFake(), "POST", "/api/jobs/nope/cancel", ""), 404, "not_found")
}

func TestRegister(t *testing.T) {
	f := newFake()
	w := do(t, f, "GET", "/api/workspaces/"+knownWS+"/register", "")

	var got []map[string]any
	decodeJSON(t, w, 200, &got)
	if len(got) != 1 {
		t.Fatalf("rows %+v", got)
	}
	for _, k := range []string{"key", "runId", "costUsd", "triageVerdict", "notePath"} {
		if _, ok := got[0][k]; !ok {
			t.Errorf("row has no %q: %v", k, got[0])
		}
	}
}

func TestDoctor(t *testing.T) {
	var got []Check
	decodeJSON(t, do(t, newFake(), "GET", "/api/workspaces/"+knownWS+"/doctor", ""), 200, &got)
	if len(got) != 1 || !got[0].OK {
		t.Fatalf("checks %+v", got)
	}
}

func TestDoctorUnknownWorkspace(t *testing.T) {
	assertError(t, do(t, newFake(), "GET", "/api/workspaces/nope/doctor", ""), 404, "not_found")
}

func TestQuota(t *testing.T) {
	var got []map[string]any
	decodeJSON(t, do(t, newFake(), "GET", "/api/quota", ""), 200, &got)
	if len(got) != 1 {
		t.Fatalf("quota %+v", got)
	}
	five, ok := got[0]["fiveHour"].(map[string]any)
	if !ok || five["utilization"] != 0.31 {
		t.Fatalf("fiveHour %+v", got[0]["fiveHour"])
	}
	if _, ok := got[0]["usedPercent"]; ok {
		t.Error("usedPercent should be omitted for a Claude reading")
	}
}

func TestUnknownAPIPath(t *testing.T) {
	// Unknown /api paths must not fall through to the UI's index.html.
	assertError(t, do(t, newFake(), "GET", "/api/nope", ""), 404, "not_found")
	assertError(t, do(t, newFake(), "PUT", "/api/workspaces", `{}`), 404, "not_found")
}

// --- static UI ---

func uiFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>Sirdar</title>")},
		"assets/app.js": {Data: []byte("console.log('sirdar')")},
	}
}

func TestStaticServesEmbeddedFiles(t *testing.T) {
	h := New(newFake(), uiFS())
	for path, want := range map[string]string{
		"/":              "<!doctype html><title>Sirdar</title>",
		"/assets/app.js": "console.log('sirdar')",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.String() != want {
			t.Errorf("%s: status %d body %q", path, w.Code, w.Body.String())
		}
	}
}

func TestStaticIndexHTMLRedirectsToRoot(t *testing.T) {
	// net/http canonicalises an explicit /index.html to /; the UI's own
	// links never use it, but a bookmark might.
	h := New(newFake(), uiFS())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/index.html", nil))
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status %d, want 301", w.Code)
	}
}

func TestStaticFallsBackToIndex(t *testing.T) {
	// The client router owns /runs/<id>; a reload of that URL must still
	// get the app rather than a 404.
	h := New(newFake(), uiFS())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/runs/"+knownRun, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>Sirdar</title>") {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
}

func TestStaticNotBuiltPage(t *testing.T) {
	// A binary built without `make ui` has an empty dist tree.
	h := New(newFake(), fstest.MapFS{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "not built") || !strings.Contains(body, "make ui") {
		t.Fatalf("body %q", body)
	}
}

func TestStaticNilFSStillServesAPI(t *testing.T) {
	h := New(newFake(), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/workspaces", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), knownWS) {
		t.Fatalf("body %q", body)
	}
}
