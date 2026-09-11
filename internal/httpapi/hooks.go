package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/webhooks"
)

// Option configures the server. Options exist so the hook routes can be
// handed a receiver without every caller that serves only the UI having to
// know webhooks exist.
type Option func(*server)

// WithHooks serves the inbound webhook endpoints against rc. A nil
// receiver — which is what a workspace with webhooks.enabled false builds
// — leaves the routes answering 404, so a hook that is off is
// indistinguishable from one that was never configured.
func WithHooks(rc *webhooks.Receiver) Option {
	return func(s *server) { s.hooks = rc }
}

// hookResponse is what a delivery gets back. A delivery that started a run
// gets the job and the keys; one that started nothing gets the reason, and
// still gets a 2xx: a tracker told its delivery failed will send it again,
// and "I have already triaged this" is not a failure worth retrying.
//
// JobIDs appears only when one delivery named several tickets, since each
// ticket gets its own job.
type hookResponse struct {
	JobID   JobID    `json:"jobId,omitempty"`
	JobIDs  []JobID  `json:"jobIds,omitempty"`
	Keys    []string `json:"keys,omitempty"`
	Skipped string   `json:"skipped,omitempty"`
}

// The outcomes a hook.received event reports.
const (
	outcomeStarted  = "started"
	outcomeSkipped  = "skipped"
	outcomeFiltered = "filtered"
	outcomeIgnored  = "ignored"
	outcomeRejected = "rejected"
)

// hook is POST /hooks/{workspaceId}/{source}: verify the delivery, read
// the tickets it names, drop the ones the workspace's filter does not
// want, and start a triage of what is left.
func (s *server) hook(w http.ResponseWriter, r *http.Request) {
	source := r.PathValue("source")
	wsID := r.PathValue("workspaceId")
	if s.hooks == nil || !s.hooks.Has(source) {
		// A source that is off and a source that does not exist are the
		// same 404 on purpose: an endpoint that answers differently for a
		// configured source tells an unauthenticated caller which
		// trackers this workspace is wired to.
		writeError(w, http.StatusNotFound, "not_found", "no webhook source "+source+" is enabled")
		return
	}

	triggers, err := s.hooks.Accept(r, source)
	if err != nil {
		s.svc.HookReceived(source, "", outcomeRejected)
		s.failHook(w, err)
		return
	}
	if len(triggers) == 0 {
		s.svc.HookReceived(source, "", outcomeIgnored)
		writeJSON(w, http.StatusAccepted, hookResponse{Skipped: "the delivery named no ticket to triage"})
		return
	}

	var (
		keys    []string
		jobs    []JobID
		skipped []string
	)
	for _, t := range triggers {
		if ok, why := s.hooks.Allows(t); !ok {
			s.svc.HookReceived(source, t.Key, outcomeFiltered)
			skipped = append(skipped, t.Key+": "+why)
			continue
		}
		id, why, err := s.svc.TriageIfIdle(r.Context(), wsID, t.Key, TriageOptions{})
		switch {
		case err != nil:
			s.svc.HookReceived(source, t.Key, outcomeRejected)
			if len(keys) == 0 {
				s.fail(w, err)
				return
			}
			// Some of the delivery's tickets are already running; the
			// rest of it is reported as skipped rather than losing the
			// jobs that did start.
			skipped = append(skipped, t.Key+": "+err.Error())
		case why != "":
			s.svc.HookReceived(source, t.Key, outcomeSkipped)
			skipped = append(skipped, t.Key+": "+why)
		default:
			s.svc.HookReceived(source, t.Key, outcomeStarted)
			keys = append(keys, t.Key)
			jobs = append(jobs, id)
		}
	}

	if len(keys) == 0 {
		writeJSON(w, http.StatusAccepted, hookResponse{Skipped: strings.Join(skipped, "; ")})
		return
	}
	resp := hookResponse{JobID: jobs[0], Keys: keys}
	if len(jobs) > 1 {
		resp.JobIDs = jobs
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// hookDisabled answers everything else under /hooks. Without it an
// unmatched hook path would fall through to the single-page app and a
// tracker would get 200 and a page of HTML.
func (s *server) hookDisabled(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "no such webhook endpoint: "+r.Method+" "+r.URL.Path)
}

// failHook maps a receiver error onto the status the sender should see.
func (s *server) failHook(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, webhooks.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "the delivery could not be verified")
	case errors.Is(err, webhooks.ErrUnknownSource):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, webhooks.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
	case errors.Is(err, webhooks.ErrBadPayload):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}
