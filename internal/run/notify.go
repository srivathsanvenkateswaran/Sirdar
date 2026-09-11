package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/notify"
)

// notifyFinished posts the run's digest to whatever the workspace
// configured, and reports whether it had to record a warning — which the
// caller answers by writing the state again.
//
// A webhook that is down, slow, or misconfigured cannot change what a run
// was worth: the note is already on disk, the register row already written.
// The failure is kept where the rest of a run's degradations are kept, in
// state.json's warnings and on the progress stream.
func (r *Runner) notifyFinished(p *prepared, row note.DigestRow) bool {
	if r.Notifier == nil || p.noNotify {
		return false
	}
	if err := r.Notifier.Notify(context.Background(), r.event(p, row)); err != nil {
		// errors.Join puts one failure per line; the run's warnings and
		// the progress view are both one line each.
		warning := "notify: " + strings.Join(strings.Split(strings.TrimSpace(err.Error()), "\n"), "; ")
		p.state.Warnings = append(p.state.Warnings, warning)
		fmt.Fprintf(r.stderr(), "[%s] %s\n", p.state.Key, warning)
		return true
	}
	return false
}

// event is what the channel is told about a finished run: its identifiers,
// its verdict, its numbers, and where to read the rest. The note's body
// stays on disk. The title is filled in here and dropped again by the
// router unless the workspace set notify.includeTitle.
func (r *Runner) event(p *prepared, row note.DigestRow) notify.Event {
	st := p.state
	ev := notify.Event{
		Kind:           string(st.Kind),
		Key:            st.Key,
		Title:          bundleTitle(p.bundle),
		Status:         string(st.Status),
		Confidence:     row.Confidence,
		Classification: row.Classification,
		Service:        p.service,
		RunID:          st.RunID,
		NotePath:       p.notePath,
		Turns:          st.Usage.Turns,
		CostUSD:        st.Usage.CostUSD,
		Reason:         st.Reason,
	}
	if b := p.bundle; b.Tracker != nil {
		ev.TrackerURL = b.Tracker.URL
	}
	if b := p.bundle; b.Helpdesk != nil {
		ev.HelpdeskURL = b.Helpdesk.URL
	}
	if !st.StartedAt.IsZero() && st.UpdatedAt.After(st.StartedAt) {
		ev.Minutes = st.UpdatedAt.Sub(st.StartedAt).Minutes()
	}
	if r.Config != nil {
		ev.Workspace = r.Config.Workspace
	}
	return ev
}
