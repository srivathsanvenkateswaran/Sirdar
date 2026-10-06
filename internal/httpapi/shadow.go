package httpapi

import (
	"fmt"
	"net/http"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

/*
The shadow and suggestions routes: the Shadow screen's status, scorecard
and controls, and the queue of playbook additions Settings reviews.

	GET  /api/workspaces/{id}/shadow/status              the watcher's state
	GET  /api/workspaces/{id}/shadow/report?since=       the scorecard
	POST /api/workspaces/{id}/shadow/poll                one tick now
	POST /api/workspaces/{id}/shadow/pause               {for?}
	POST /api/workspaces/{id}/shadow/resume
	POST /api/workspaces/{id}/shadow/outcomes            {key, prUrls, resolution}
	POST /api/workspaces/{id}/shadow/regrade             {key}
	POST /api/workspaces/{id}/runs/{runId}/remember      {index}
	GET  /api/workspaces/{id}/suggestions
	POST /api/workspaces/{id}/suggestions/{sid}/accept   {playbook?, addition?}
	POST /api/workspaces/{id}/suggestions/{sid}/dismiss

Nothing here starts a shadow run: those come from the watcher or from
`sirdar shadow run`, so the page can watch shadow mode but never spend on it
by hand.
*/

func (s *server) shadowStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.ShadowStatus(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) shadowReport(w http.ResponseWriter, r *http.Request) {
	rep, err := s.svc.ShadowReport(r.PathValue("id"), r.URL.Query().Get("since"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// shadowPoll is POST …/shadow/poll. Another process watching the workspace
// is a 403 whose message names it; a tick that fails is not an error here,
// it is in the status the route answers with.
func (s *server) shadowPoll(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.ShadowPoll(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) shadowPause(w http.ResponseWriter, r *http.Request) {
	var body struct {
		For string `json:"for"`
	}
	if !decode(w, r, &body, true) {
		return
	}
	st, err := s.svc.ShadowPause(r.PathValue("id"), body.For)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) shadowResume(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.ShadowResume(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) shadowOutcome(w http.ResponseWriter, r *http.Request) {
	var body app.ShadowOutcomeInput
	if !decode(w, r, &body, false) {
		return
	}
	row, err := s.svc.ShadowOutcome(r.Context(), r.PathValue("id"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// shadowRegrade is POST …/shadow/regrade for one key. The service grades
// any number; the screen asks about the row it shows, so the route takes
// one key and answers with its row.
func (s *server) shadowRegrade(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if body.Key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "key is required")
		return
	}
	rows, err := s.svc.ShadowRegrade(r.Context(), r.PathValue("id"), []string{body.Key})
	if err != nil {
		s.fail(w, err)
		return
	}
	// A key with no finished shadow run, or one still running, comes back
	// with no row: there is nothing yet to show as graded.
	if len(rows) == 0 {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("%s has no shadow row to grade", body.Key))
		return
	}
	writeJSON(w, http.StatusOK, rows[0])
}

func (s *server) remember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index int `json:"index"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	sug, err := s.svc.Remember(r.Context(), r.PathValue("id"), r.PathValue("runId"), body.Index)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sug)
}

func (s *server) suggestions(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.Suggestions(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(list))
}

// acceptSuggestion is POST …/suggestions/{sid}/accept. An empty body
// accepts the proposal as it stands.
func (s *server) acceptSuggestion(w http.ResponseWriter, r *http.Request) {
	var body app.AcceptSuggestionInput
	if !decode(w, r, &body, true) {
		return
	}
	sug, err := s.svc.AcceptSuggestion(r.PathValue("id"), r.PathValue("sid"), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sug)
}

func (s *server) dismissSuggestion(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DismissSuggestion(r.PathValue("id"), r.PathValue("sid")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
