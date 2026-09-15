package httpapi

import (
	"net/http"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

// validProvider reports whether name is one Sirdar drives, writing the 400
// itself when it is not. An empty name is the workspace's own provider and
// is always allowed.
//
// The set itself is internal/app's. The Wails bridge calls the Service with
// no HTTP layer in front of it, so a set kept here would let the desktop
// shell start a job over a typo the browser one refuses. This is only the
// early 400 with the list in it; app.Service checks the same name again
// before it starts anything.
func validProvider(w http.ResponseWriter, name string) bool {
	if app.ValidProvider(name) {
		return true
	}
	writeError(w, http.StatusBadRequest, "bad_request", "provider must be "+app.ProviderList)
	return false
}

// startFix is POST /api/workspaces/{id}/fix: the one flow that writes.
//
// The gate is not here. A fix runs from a triage note a human read and
// approved, and internal/fix refuses one whose status says otherwise; this
// route only carries the same flags `sirdar fix` takes. acceptDeviation is
// the second half of that gate: a run whose agent reported deviating from
// the note commits locally and stops, and a rerun with it set pushes the
// commit that was reviewed rather than starting another session.
//
// The one gate that is here is the listener's: every other route reads or
// starts a session that writes a note, but this one writes code to the
// operator's repository and opens a pull request under their GitHub login.
// On a listener other machines can reach — `sirdar serve --allow-remote`,
// which has no authentication of any kind — that is refused outright. A
// remote reader of somebody's triage notes is bad; a remote author of their
// commits is a different category.
func (s *server) startFix(w http.ResponseWriter, r *http.Request) {
	if !s.loopbackOnly {
		writeError(w, http.StatusForbidden, "forbidden",
			"a fix writes code and opens a pull request, so it is refused on a listener other machines can reach;"+
				" start it from `sirdar fix` or from a server bound to loopback")
		return
	}
	var body struct {
		Key             string `json:"key"`
		DryRun          bool   `json:"dryRun"`
		Local           bool   `json:"local"`
		NoPR            bool   `json:"noPr"`
		Base            string `json:"base"`
		At              string `json:"at"`
		AcceptDeviation bool   `json:"acceptDeviation"`
		Provider        string `json:"provider"`
		Model           string `json:"model"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if body.Key == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "key is required")
		return
	}
	if !validProvider(w, body.Provider) {
		return
	}
	id, err := s.svc.StartFix(r.Context(), r.PathValue("id"), body.Key, FixOptions{
		DryRun:          body.DryRun,
		Local:           body.Local,
		NoPR:            body.NoPR,
		Base:            body.Base,
		At:              body.At,
		AcceptDeviation: body.AcceptDeviation,
		Provider:        body.Provider,
		Model:           body.Model,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobResponse{id})
}

// startEval is POST /api/workspaces/{id}/eval: replay the golden set and
// score it. An empty keys list means every key in the set.
//
// The golden set itself is not a parameter. It holds real customers'
// conversations, and the directory is the one the shell was started with.
func (s *server) startEval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keys        []string `json:"keys"`
		Provider    string   `json:"provider"`
		Model       string   `json:"model"`
		Concurrency int      `json:"concurrency"`
		// Retro replays each key at the commit its fix branched from
		// and scores it against the pull request that fixed it.
		Retro   bool `json:"retro"`
		WithRCA bool `json:"withRca"`
		Rubric  bool `json:"rubric"`
	}
	// An eval of the whole golden set carries no body at all.
	if !decode(w, r, &body, true) {
		return
	}
	if !validProvider(w, body.Provider) {
		return
	}
	if body.Concurrency < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "concurrency must be a non-negative integer")
		return
	}
	id, err := s.svc.StartEval(r.Context(), r.PathValue("id"), body.Keys, EvalOptions{
		Provider: body.Provider, Model: body.Model, Concurrency: body.Concurrency,
		Retro: body.Retro, WithRCA: body.WithRCA, Rubric: body.Rubric,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobResponse{id})
}

// evalReports is GET /api/workspaces/{id}/eval: the reports recorded under
// .sirdar/eval, newest first.
func (s *server) evalReports(w http.ResponseWriter, r *http.Request) {
	reports, err := s.svc.EvalReports(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(reports))
}

// latestRetro is GET /api/workspaces/{id}/eval/retro/latest: the newest
// retro report, or null when the workspace has run none. A workspace with
// no retro is not an error — most have none — so the body is a null rather
// than a 404 the screen would have to tell apart from a bad id.
func (s *server) latestRetro(w http.ResponseWriter, r *http.Request) {
	report, err := s.svc.LatestRetro(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// golden is GET /api/workspaces/{id}/golden: the keys in the golden set.
func (s *server) golden(w http.ResponseWriter, r *http.Request) {
	entries, err := s.svc.Golden(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nonNil(entries))
}

// addGolden is POST /api/workspaces/{id}/golden: copy a completed run's
// bundle into the golden set. A body naming only a run id takes the key
// from that run.
func (s *server) addGolden(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		RunID string `json:"runId"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if body.Key == "" && body.RunID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "key or runId is required")
		return
	}
	entry, err := s.svc.AddGolden(r.PathValue("id"), body.Key, body.RunID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// configSummary is GET /api/workspaces/{id}/config/summary: the notify and
// webhooks blocks with every credential reference cut back to its scheme.
func (s *server) configSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := s.svc.ConfigSummary(r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}
