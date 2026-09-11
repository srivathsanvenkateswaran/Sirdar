package run

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/notify"
)

// notifyTimeout is the hard ceiling on one finished run's notification,
// every destination and any retry included. A run that has already
// written its note and register row owes the channel a message, not an
// open-ended wait on a slow or rate-limiting webhook, so the ceiling holds
// regardless of what Retry-After asks for.
const notifyTimeout = 15 * time.Second

// notifyFinished posts the run's digest to whatever the workspace
// configured, and reports whether it had to record a warning — which the
// caller answers by writing the state again.
//
// A webhook that is down, slow, or misconfigured cannot change what a run
// was worth: the note is already on disk, the register row already written.
// The failure is kept where the rest of a run's degradations are kept, in
// state.json's warnings and on the progress stream.
//
// The post runs against ctx with its cancellation removed: an interrupted
// run has already produced everything the message reports, so Ctrl-C must
// not cut the channel off mid-post. It still gets a context, so it inherits
// no more than notifyTimeout regardless of what the run's own context might
// otherwise have granted it.
func (r *Runner) notifyFinished(ctx context.Context, p *prepared, row note.DigestRow) bool {
	if r.Notifier == nil || p.noNotify {
		return false
	}
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
	defer cancel()
	if err := r.Notifier.Notify(postCtx, r.event(p, row)); err != nil {
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
		NotePath:       relNotePath(r.Config, p.notePath),
		Turns:          st.Usage.Turns,
		CostUSD:        st.Usage.CostUSD,
		Reason:         notifyReason(st.Reason),
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

// maxNotifyReason is how much of a run's reason reaches a channel. The
// longest ordinary reason ("wall-clock budget of N minutes exceeded",
// "rate limited, resets at ...") is well under this; it exists for the
// reasons that are not Sirdar's own words, like a provider's exit error.
const maxNotifyReason = 200

// notifyReason is what a channel is told about a run that did not simply
// complete. state.Reason keeps the agent's actual question — resumeText
// needs it to put back to the operator — but the question may quote the
// ticket, and docs/notifications.md promises that nothing a session said
// reaches the wire; a blocked-on-question run reports a fixed phrase
// instead. Every other reason is Sirdar's own text and is sent as given,
// capped so nothing unbounded (a long provider error, say) reaches a chat
// message.
func notifyReason(reason string) string {
	if strings.HasPrefix(reason, askedPrefix) {
		return "agent asked a question"
	}
	r := []rune(reason)
	if len(r) <= maxNotifyReason {
		return reason
	}
	return string(r[:maxNotifyReason]) + "…"
}

// relNotePath reports notePath relative to the workspace root. The
// absolute path carries this machine's home directory and the operator's
// username, which have no business in a chat channel.
func relNotePath(cfg *config.Config, notePath string) string {
	if cfg == nil || notePath == "" || !filepath.IsAbs(notePath) {
		return notePath
	}
	rel, err := filepath.Rel(cfg.Root, notePath)
	if err != nil {
		return notePath
	}
	return rel
}
