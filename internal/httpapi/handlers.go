package httpapi

import (
	"encoding/json"
	"errors"
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

// runDiff is GET /api/workspaces/{id}/runs/{runId}/diff: one fix run's
// change, file by file, with the unified patch under it.
//
// It starts nothing and writes nothing — the commit was made when the fix
// ran — so it carries no gate beyond the ones every read route has. A run
// with no change to show is a 404 naming the reason: a triage run, or a fix
// whose worktree is gone and which never recorded a commit.
func (s *server) runDiff(w http.ResponseWriter, r *http.Request) {
	d, err := s.svc.RunDiff(r.PathValue("id"), r.PathValue("runId"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// dropHunk is POST /api/workspaces/{id}/runs/{runId}/diff/drop: revert one
// hunk out of the fix commit and amend it in place, then answer with the
// change as it stands afterwards.
//
// It is refused on a listener other machines can reach, for the reason
// startFix is: this rewrites a commit in the operator's repository, and a
// server with no authentication of any kind must not offer that to whoever
// turns out to be able to reach the port.
//
// The refusals that are not about the listener are 409s with a reason: the
// run is still live, its worktree is gone, its branch is already pushed, or
// the etag says the patch the hunk index was read from is not the patch
// that is there now.
func (s *server) dropHunk(w http.ResponseWriter, r *http.Request) {
	if !s.loopbackOnly {
		writeError(w, http.StatusForbidden, "forbidden",
			"dropping a hunk rewrites a commit in the operator's repository, so it is refused on a listener"+
				" other machines can reach; use `sirdar runs diff --drop` or a server bound to loopback")
		return
	}
	var body struct {
		Path string `json:"path"`
		Hunk int    `json:"hunk"`
		ETag string `json:"etag"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if body.Path == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "path is required")
		return
	}
	if body.ETag == "" {
		writeError(w, http.StatusBadRequest, "bad_request",
			"etag is required: it is the etag of the diff the hunk index was read from")
		return
	}
	d, err := s.svc.DropHunk(r.Context(), r.PathValue("id"), r.PathValue("runId"), body.Path, body.Hunk, body.ETag)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
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

// mcpServers is GET /api/workspaces/{id}/mcp: the MCP servers a run in
// this workspace would be offered. With ?connect=1 each is started and its
// tools counted, which is slow enough that it is never the default.
//
// Nothing here carries a credential value: an entry's env and headers
// cross as key names, and its command line crosses as configured rather
// than expanded.
func (s *server) mcpServers(w http.ResponseWriter, r *http.Request) {
	connect, ok := boolParam(w, r, "connect")
	if !ok {
		return
	}
	inv, err := s.svc.MCPServers(r.Context(), r.PathValue("id"), connect)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// mcpTools is GET /api/workspaces/{id}/mcp/{server}/tools: every tool the
// server lists, with the verdict a run would get for it and the rule that
// settled it.
func (s *server) mcpTools(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.MCPTools(r.Context(), r.PathValue("id"), r.PathValue("server"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// mcpCall is POST /api/workspaces/{id}/mcp/{server}/call: run one tool by
// hand. A tool the workspace's permissions would refuse a run is refused
// here too — 403, with the same reason, and nothing started.
func (s *server) mcpCall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if body.Tool == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "tool is required")
		return
	}
	res, err := s.svc.MCPCall(r.Context(), r.PathValue("id"), r.PathValue("server"), body.Tool, body.Args)
	switch {
	case errors.Is(err, ErrMCPDenied):
		writeJSON(w, http.StatusForbidden, res)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// boolParam reads a query parameter that is on when present and not "0" or
// "false", refusing anything else so a typo is a 400 rather than an
// expensive default nobody asked for.
func boolParam(w http.ResponseWriter, r *http.Request, name string) (bool, bool) {
	raw := r.URL.Query().Get(name)
	switch raw {
	case "":
		return false, true
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return false, true
	default:
		writeError(w, http.StatusBadRequest, "bad_request", name+" must be 1 or 0")
		return false, false
	}
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
