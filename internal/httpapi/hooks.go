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

// WithHooks serves the inbound webhook endpoints against rc, for the one
// workspace id the receiver was built for. A nil receiver — which is what a
// workspace with webhooks.enabled false builds — leaves the routes
// answering 404, so a hook that is off is indistinguishable from one that
// was never configured.
//
// The id matters: rc holds one workspace's secrets, and without it the
// {workspaceId} in the path would be nothing but a label. Whoever holds the
// secret for the workspace being served could then name any other
// registered workspace in the path and start runs in it.
func WithHooks(workspaceID string, rc *webhooks.Receiver) Option {
	return func(s *server) {
		s.hooks = rc
		s.hooksWS = workspaceID
	}
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
	if s.hooks == nil || wsID != s.hooksWS || !s.hooks.Has(source) {
		// One 404 for all three: the hooks are off, the path names a
		// workspace other than the one this receiver was built for, or the
		// source is not enabled. An endpoint that answered differently
		// would tell an unauthenticated caller which workspace it is
		// serving and which trackers that workspace is wired to.
		//
		// The workspace check is what keeps a secret to one workspace.
		// This process serves the workspace it was started in; a delivery
		// that verifies against its secret may not name another
		// registered workspace in the path and start runs there.
		writeError(w, http.StatusNotFound, "not_found", "no such webhook endpoint")
		return
	}

	triggers, err := s.hooks.Accept(r, source)
	if err != nil {
		s.svc.HookReceived(source, "", outcomeRejected)
		s.failHook(w, source, err)
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
			// The sender is told only that the start failed. A Service
			// error names what it was working on — the workspace root, an
			// adapter's command line — and the sender of a webhook is not
			// the operator: the detail goes to the log instead.
			s.logf("sirdar: hook %s: starting a triage of %s: %v", source, t.Key, err)
			s.svc.HookReceived(source, t.Key, outcomeRejected)
			if len(keys) == 0 {
				writeError(w, http.StatusInternalServerError, "internal", startFailed)
				return
			}
			// Some of the delivery's tickets started; the rest of it is
			// reported as skipped rather than losing the jobs that did.
			skipped = append(skipped, t.Key+": "+startFailed)
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

// startFailed is what a sender is told when a verified delivery could not
// start a run. It says nothing about why: see the server's log for that.
const startFailed = "the delivery was verified but the triage could not be started"

// failHook maps a receiver error onto the status the sender should see.
// The messages that reach the sender are the webhooks package's own, which
// describe the delivery; anything else is generic and logged, because an
// error from deeper in can carry the workspace's path.
func (s *server) failHook(w http.ResponseWriter, source string, err error) {
	switch {
	case errors.Is(err, webhooks.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "the delivery could not be verified")
	case errors.Is(err, webhooks.ErrUnknownSource):
		writeError(w, http.StatusNotFound, "not_found", "no such webhook endpoint")
	case errors.Is(err, webhooks.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
	case errors.Is(err, webhooks.ErrBadPayload):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		s.logf("sirdar: hook %s: %v", source, err)
		writeError(w, http.StatusInternalServerError, "internal", "the delivery could not be read")
	}
}
