package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// Steers typed while the run works.
//
// A person who types into a running session's composer is not refused. The
// instruction goes into the run's steers.jsonl inbox (store.Run.QueueSteer)
// from whichever process took it, and the executor drains that inbox at the
// run's next turn boundary: the moment a provider turn has ended with an
// answer. If the session takes another user message there — Claude Code on
// stream-json stdin, an ACP agent's next session/prompt, the openai loop —
// the instruction is sent as that message, framed the way a steer on a
// finished run is, and the run goes on in the same session. If it does not,
// the instruction is held, and is applied as an ordinary steer the moment
// the run settles (see the app layer's ApplyHeldSteers).

// ContinueLive is the continuation a steer delivered into the running
// session records: neither a resumed session nor a primed one, but the
// same session reading one more user message.
const ContinueLive = "live"

// steerPollInterval is how often a working run looks at its inbox between
// provider events, so "steer queued" reaches the transcript while the
// agent is still inside a long tool call. Delivery does not depend on it:
// the turn boundary reads the inbox itself.
const steerPollInterval = 500 * time.Millisecond

func (r *Runner) steerPoll() time.Duration {
	if r.SteerPoll > 0 {
		return r.SteerPoll
	}
	return steerPollInterval
}

// pickUpSteers takes every instruction the inbox holds that the run has not
// seen yet into its state as queued, writes a `steer_queued` line for each,
// and saves the state when anything arrived.
func (r *Runner) pickUpSteers(p *prepared, log *eventLog) {
	added, err := p.run.MergeSteerInbox(&p.state)
	if err != nil {
		fmt.Fprintf(r.stderr(), "[%s] steer inbox: %v\n", p.state.Key, err)
		return
	}
	if len(added) == 0 {
		return
	}
	for _, q := range added {
		if err := log.Append("steer_queued", eventPayload{Text: q.Text, Phase: p.state.Phase}); err != nil {
			fmt.Fprintf(r.stderr(), "[%s] event log: %v\n", p.state.Key, err)
		}
		fmt.Fprintf(r.stderr(), "[%s] steer queued: %s\n", p.state.Key, firstLine(q.Text))
	}
	p.state.UpdatedAt = r.now()
	if err := p.run.WriteState(p.state); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] state: %v\n", p.state.Key, err)
	}
}

// pendingSteer reports whether q is still waiting for a session to read it.
func pendingSteer(q store.QueuedSteer) bool {
	return q.Status == store.SteerQueued || q.Status == store.SteerHeld
}

// liveDeliverable says whether q can go into the running session: it is
// still pending, the run is not filing its note, and it asks for no model
// other than the one the session is on — a different model is a different
// session, which only a steer after the run settles can start. The note
// turn writes down a finished investigation, so a follow-up typed during it
// is held and answered once the run settles.
func liveDeliverable(q store.QueuedSteer, s store.State) bool {
	if !pendingSteer(q) || s.Phase == store.PhaseNote {
		return false
	}
	m := strings.TrimSpace(q.Model)
	return m == "" || strings.EqualFold(m, s.Model) || strings.EqualFold(m, s.RequestedModel())
}

// joinSteers is the one instruction several queued steers make together,
// in the order they were typed.
func joinSteers(qs []store.QueuedSteer) string {
	texts := make([]string, 0, len(qs))
	for _, q := range qs {
		texts = append(texts, strings.TrimSpace(q.Text))
	}
	return strings.Join(texts, "\n\n")
}

// deliverSteers is the turn boundary. The session has just ended a turn
// with a valid answer, which is already filed; if instructions are queued
// and the session takes another user message, they are sent as one steer
// and the run carries on in the same session, its answer to be validated
// and filed again. It reports whether it sent one — false leaves the
// session to be ended as usual and the instructions queued, to be held.
func (r *Runner) deliverSteers(ctx context.Context, p *prepared, sess provider.Session, log *eventLog, ex *execution) bool {
	// The note turn's boundary is the end of the run's work, not a turn a
	// follow-up can join: what is queued is held for the settled run.
	if p.state.Phase == store.PhaseNote {
		return false
	}
	r.pickUpSteers(p, log)
	var idx []int
	var batch []store.QueuedSteer
	for i, q := range p.state.QueuedSteers {
		if liveDeliverable(q, p.state) {
			idx = append(idx, i)
			batch = append(batch, q)
		}
	}
	if len(batch) == 0 {
		return false
	}
	text := joinSteers(batch)
	// A run that answers in chat takes a follow-up as conversation: the
	// operator gets an answer to what they asked, not the document again.
	msg := steerPrompt(text, p.kind)
	if p.reply {
		msg = conversationPrompt(text)
	}
	if err := sess.Send(ctx, msg); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] the session takes no message mid-run (%v); the steer waits for the run to settle\n",
			p.state.Key, firstLine(err.Error()))
		return false
	}

	now := r.now()
	turn := p.state.Usage.Turns
	for _, i := range idx {
		q := &p.state.QueuedSteers[i]
		q.Status, q.Turn, q.Done, q.Reason = store.SteerDelivered, turn, now, ""
	}
	s := store.Steer{At: now, Text: text, Continuation: ContinueLive}
	p.state.Steers = append(p.state.Steers, s)
	p.steer = &s
	// The answer just filed is what the steered turn is measured
	// against: the same document back leaves the note as it stands.
	p.previousFinal = append([]byte(nil), ex.final...)
	if err := log.Append("steer", eventPayload{Text: text, Continuation: ContinueLive, Turns: turn, Phase: p.state.Phase}); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] event log: %v\n", p.state.Key, err)
	}
	fmt.Fprintf(r.stderr(), "[%s] steer delivered at turn %d: %s\n", p.state.Key, turn, firstLine(text))

	// A new turn, owed a new answer: everything that judged the last one
	// starts again.
	ex.final, ex.reply = nil, ""
	ex.row, ex.completeErr = note.DigestRow{}, nil
	ex.retried, ex.emptyTurns, ex.schemaError = false, 0, ""
	ex.finalNarration = ""
	p.state.UpdatedAt = now
	if err := p.run.WriteState(p.state); err != nil {
		fmt.Fprintf(r.stderr(), "[%s] state: %v\n", p.state.Key, err)
	}
	ex.stall.reset()
	return true
}

// holdSteers marks every instruction still queued when the session is over
// as held: no turn boundary is coming in this session, so each waits for
// the run to settle and is applied then.
func (r *Runner) holdSteers(p *prepared, log *eventLog) {
	r.pickUpSteers(p, log)
	held := false
	for i := range p.state.QueuedSteers {
		q := &p.state.QueuedSteers[i]
		if q.Status != store.SteerQueued {
			continue
		}
		q.Status = store.SteerHeld
		held = true
		if err := log.Append("steer_held", eventPayload{Text: q.Text, Phase: p.state.Phase}); err != nil {
			fmt.Fprintf(r.stderr(), "[%s] event log: %v\n", p.state.Key, err)
		}
	}
	if held {
		fmt.Fprintf(r.stderr(), "[%s] steer held until the run settles\n", p.state.Key)
	}
}

// HeldSteers is what a settled run still owes the person who steered it
// while it worked: every instruction queued on it, in its inbox or its
// state, that no session has read. The state passed in gets the inbox's
// unseen lines merged into it.
func HeldSteers(rn store.Run, s *store.State) ([]store.QueuedSteer, error) {
	if _, err := rn.MergeSteerInbox(s); err != nil {
		return nil, err
	}
	var out []store.QueuedSteer
	for _, q := range s.QueuedSteers {
		if pendingSteer(q) {
			out = append(out, q)
		}
	}
	return out, nil
}

// JoinSteers is joinSteers for the app layer, which applies held steers as
// one follow-up.
func JoinSteers(qs []store.QueuedSteer) string { return joinSteers(qs) }

// ResolveSteers records what became of the queued steers named by ids, on
// the state passed in.
func ResolveSteers(s *store.State, ids map[string]bool, status, reason string, at time.Time) {
	for i := range s.QueuedSteers {
		q := &s.QueuedSteers[i]
		if ids[q.ID] {
			q.Status, q.Reason, q.Done = status, reason, at
		}
	}
}
