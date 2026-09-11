// Package httpapi serves Sirdar's desktop surface over HTTP: a JSON API
// under /api, a Server-Sent Events stream at /api/events, and the embedded
// single-page UI on every other path. It is the browser-mode half of the
// pair the design spec describes; the Wails shell calls the same Service
// in process.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/webhooks"
)

// New returns the handler for the whole surface: the JSON API, the event
// stream, and the UI in ui. A nil ui serves the "not built" page, which is
// what an unbuilt frontend leaves behind. WithHooks adds the inbound
// webhook endpoints; without it they answer 404.
func New(svc Service, ui fs.FS, opts ...Option) http.Handler { return newServer(svc, ui, opts...) }

type server struct {
	svc   Service
	ui    fs.FS
	mux   *http.ServeMux
	hooks *webhooks.Receiver
	// hooksWS is the workspace id the hook receiver was built for. A
	// delivery naming any other workspace in its path is a 404, whatever
	// secret it carries.
	hooksWS string

	// keepalive is how often an idle event stream writes its comment line.
	// A field rather than a constant so the tests need not wait 15 s.
	keepalive time.Duration

	// logf takes what the caller must not be told. A webhook sender is not
	// the operator, so the reason a delivery failed is written here and
	// only a generic message goes back over the wire.
	logf func(format string, v ...any)
}

func newServer(svc Service, ui fs.FS, opts ...Option) *server {
	if ui == nil {
		ui = emptyFS{}
	}
	s := &server{
		svc: svc, ui: ui, mux: http.NewServeMux(),
		keepalive: 15 * time.Second,
		logf:      log.Printf,
	}
	for _, opt := range opts {
		opt(s)
	}

	s.mux.HandleFunc("GET /api/workspaces", s.listWorkspaces)
	s.mux.HandleFunc("POST /api/workspaces", s.addWorkspace)
	s.mux.HandleFunc("DELETE /api/workspaces/{id}", s.removeWorkspace)
	s.mux.HandleFunc("GET /api/workspaces/{id}/queue", s.queue)
	s.mux.HandleFunc("GET /api/workspaces/{id}/runs", s.runs)
	s.mux.HandleFunc("GET /api/workspaces/{id}/runs/{runId}", s.run)
	s.mux.HandleFunc("GET /api/workspaces/{id}/runs/{runId}/events", s.runEvents)
	s.mux.HandleFunc("GET /api/workspaces/{id}/runs/{runId}/note", s.note)
	s.mux.HandleFunc("GET /api/workspaces/{id}/runs/{runId}/prompt", s.prompt)
	s.mux.HandleFunc("POST /api/workspaces/{id}/triage", s.startTriage)
	s.mux.HandleFunc("POST /api/workspaces/{id}/rca", s.startRCA)
	s.mux.HandleFunc("POST /api/workspaces/{id}/runs/{runId}/resume", s.resume)
	s.mux.HandleFunc("POST /api/jobs/{jobId}/cancel", s.cancel)
	s.mux.HandleFunc("GET /api/workspaces/{id}/register", s.register)
	s.mux.HandleFunc("GET /api/workspaces/{id}/doctor", s.doctor)
	s.mux.HandleFunc("GET /api/quota", s.quota)
	s.mux.HandleFunc("GET /api/events", s.events)

	// Anything else under /api is a client mistake, and answering it with
	// the UI's index.html would hand a JSON caller a page of HTML.
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint: "+r.Method+" "+r.URL.Path)
	})
	// The hook routes are registered whether or not a receiver was given:
	// the handler answers 404 without one, which keeps a disabled hook
	// from falling through to the single-page app and getting a 200.
	s.mux.HandleFunc("POST /hooks/{workspaceId}/{source}", s.hook)
	s.mux.HandleFunc("/hooks/", s.hookDisabled)

	s.mux.HandleFunc("/", s.static)
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// --- workspaces ---

func (s *server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	ws, err := s.svc.Workspaces()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(ws))
}

func (s *server) addWorkspace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Root string `json:"root"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if body.Root == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "root is required")
		return
	}
	ws, err := s.svc.AddWorkspace(body.Root)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

func (s *server) removeWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RemoveWorkspace(r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- queue and runs ---

func (s *server) queue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := QueueFilter{Assignee: q.Get("assignee"), Status: q.Get("status")}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be a non-negative integer")
			return
		}
		f.Limit = n
	}
	tickets, err := s.svc.Queue(r.Context(), r.PathValue("id"), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(tickets))
}

func (s *server) runs(w http.ResponseWriter, r *http.Request) {
	rs, err := s.svc.Runs(r.PathValue("id"), r.URL.Query().Get("key"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(rs))
}

func (s *server) run(w http.ResponseWriter, r *http.Request) {
	d, err := s.svc.Run(r.PathValue("id"), r.PathValue("runId"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) runEvents(w http.ResponseWriter, r *http.Request) {
	after := 0
	if raw := r.URL.Query().Get("after"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "after must be a non-negative integer")
			return
		}
		after = n
	}
	events, next, err := s.svc.Events(r.PathValue("id"), r.PathValue("runId"), after)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Events []RunEvent `json:"events"`
		Next   int        `json:"next"`
	}{nonNil(events), next})
}

// noteKinds are the note kinds the spec allows on the note route.
var noteKinds = map[string]bool{"triage": true, "rca": true, "resolution": true}

func (s *server) note(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if !noteKinds[kind] {
		writeError(w, http.StatusBadRequest, "bad_request", "kind must be one of triage, rca, resolution")
		return
	}
	md, err := s.svc.Note(r.PathValue("id"), r.PathValue("runId"), kind)
	if err != nil {
		s.failDocument(w, err)
		return
	}
	writeMarkdown(w, md)
}

func (s *server) prompt(w http.ResponseWriter, r *http.Request) {
	md, err := s.svc.Prompt(r.PathValue("id"), r.PathValue("runId"))
	if err != nil {
		s.failDocument(w, err)
		return
	}
	writeMarkdown(w, md)
}

// --- jobs ---

// jobResponse is the body every async start returns: the caller polls or
// watches the event stream for what happens next.
type jobResponse struct {
	JobID JobID `json:"jobId"`
}

func (s *server) startTriage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keys     []string `json:"keys"`
		Provider string   `json:"provider"`
		Model    string   `json:"model"`
		DryRun   bool     `json:"dryRun"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if len(body.Keys) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "keys must list at least one ticket key")
		return
	}
	id, err := s.svc.StartTriage(r.Context(), r.PathValue("id"), body.Keys, TriageOptions{
		Provider: body.Provider, Model: body.Model, DryRun: body.DryRun,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobResponse{id})
}

func (s *server) startRCA(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key        string `json:"key"`
		PRURL      string `json:"prUrl"`
		Resolution string `json:"resolution"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if body.Key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "key is required")
		return
	}
	id, err := s.svc.StartRCA(r.Context(), r.PathValue("id"), body.Key, RCAOptions{
		PRURL: body.PRURL, Resolution: body.Resolution,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobResponse{id})
}

func (s *server) resume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Answer string `json:"answer"`
	}
	// A resume that is not answering a question carries no body at all.
	if !decode(w, r, &body, true) {
		return
	}
	id, err := s.svc.Resume(r.Context(), r.PathValue("id"), r.PathValue("runId"), body.Answer)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobResponse{id})
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Cancel(JobID(r.PathValue("jobId"))); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// --- workspace reports ---

func (s *server) register(w http.ResponseWriter, r *http.Request) {
	rows, err := s.svc.Register(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(rows))
}

func (s *server) doctor(w http.ResponseWriter, r *http.Request) {
	checks, err := s.svc.Doctor(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(checks))
}

func (s *server) quota(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, nonNil(s.svc.Quota()))
}

// --- static UI ---

// notBuiltPage is what a binary built without the frontend serves, so the
// operator sees the missing step rather than a blank 404.
const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Sirdar</title></head>
<body style="font:14px/1.5 system-ui,sans-serif;margin:3rem">
<h1>Sirdar UI not built</h1>
<p>Run <code>make ui</code>, then start <code>sirdar serve</code> again.</p>
<p>The JSON API under <code>/api</code> is serving normally.</p>
</body></html>
`

// static serves the embedded single-page app. Unknown paths fall back to
// index.html because the client router owns them; /api never reaches here.
func (s *server) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	if s.exists(name) {
		http.ServeFileFS(w, r, s.ui, name)
		return
	}
	if s.exists("index.html") {
		http.ServeFileFS(w, r, s.ui, "index.html")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, notBuiltPage)
}

func (s *server) exists(name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	f, err := s.ui.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	return err == nil && !info.IsDir()
}

// emptyFS stands in for a UI that was never embedded.
type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

// --- wire helpers ---

// errorBody is the one error shape the API uses, as the spec requires.
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var b errorBody
	b.Error.Code = code
	b.Error.Message = message
	writeJSON(w, status, b)
}

// fail turns a Service error into the status the spec gives it: 501 for a
// capability the workspace does not have, 404 for an id nobody knows, 500
// for everything else.
func (s *server) fail(w http.ResponseWriter, err error) {
	status, code := classify(err)
	writeError(w, status, code, err.Error())
}

// failDocument is fail for the two routes that return a file the run may
// simply not have written yet: an RCA note before the RCA has run, say. A
// missing file there is a 404, which the UI shows as an empty tab.
func (s *server) failDocument(w http.ResponseWriter, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	s.fail(w, err)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	// The header is already out; a failed write is the client's departure,
	// not something the caller can be told about.
	_ = enc.Encode(v)
}

func writeMarkdown(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, text)
}

// decode reads a JSON request body into dst, rejecting unknown fields so a
// typo in the UI is a 400 here rather than a silently ignored option. When
// allowEmpty, a request with no body at all leaves dst at its zero value.
func decode(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	switch {
	case errors.Is(err, io.EOF):
		if allowEmpty {
			return true
		}
		writeError(w, http.StatusBadRequest, "bad_request", "a JSON body is required")
		return false
	case err != nil:
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("invalid JSON body: %v", err))
		return false
	}
	// One object per request: trailing content means the caller sent
	// something other than what this endpoint accepts.
	if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "the body must be a single JSON object")
		return false
	}
	return true
}

// nonNil keeps an empty result an empty JSON array rather than null, which
// the frontend would have to guard against on every list.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
