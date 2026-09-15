package httpapi

import "net/http"

// providers are the values a one-off `provider` override may take. The set
// is checked here rather than left to the job, so a typo from a UI is a 400
// with the list in it instead of a job that starts and fails.
var providers = map[string]bool{
	"claude": true, "codex": true, "openai": true, "acp": true, "qwen": true,
}

const providerList = "claude, codex, openai, acp or qwen"

// validProvider reports whether name is one Sirdar drives, writing the 400
// itself when it is not. An empty name is the workspace's own provider and
// is always allowed.
func validProvider(w http.ResponseWriter, name string) bool {
	if name == "" || providers[name] {
		return true
	}
	writeError(w, http.StatusBadRequest, "bad_request", "provider must be "+providerList)
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
func (s *server) startFix(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key             string `json:"key"`
		DryRun          bool   `json:"dryRun"`
		NoPR            bool   `json:"noPr"`
		Base            string `json:"base"`
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
		NoPR:            body.NoPR,
		Base:            body.Base,
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
