package run

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// TestLiveSteerDeliveredAtTheTurnBoundary is a steer typed while the run
// works, on a session that takes another user message: it is queued, sent
// as the next message once the turn ends with an answer, and the run goes on
// in the same session to file the steered answer.
func TestLiveSteerDeliveredAtTheTurnBoundary(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.SteerPoll = 5 * time.Millisecond

	var delivered string
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		s.emit(provider.Event{Kind: provider.EvUsage, Turns: 3, InputTok: 100, OutputTok: 20})
		if _, err := (store.Run{Dir: spec.RunDir}).QueueSteer("Re-check the partial-return path", "", time.Now()); err != nil {
			t.Error(err)
			return
		}
		if !s.emit(finalEvent(triageDoc)) {
			return
		}
		select {
		case delivered = <-s.sendCh:
		case <-time.After(5 * time.Second):
			t.Error("the steer was never sent into the session")
			return
		}
		s.emit(provider.Event{Kind: provider.EvUsage, Turns: 5, InputTok: 160, OutputTok: 40})
		s.emit(finalEvent(steeredDoc))
	}

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{NoteOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("ended %q: %s", out.State.Status, out.State.Reason)
	}
	if p.startCount() != 1 {
		t.Fatalf("%d sessions started, want the one session to carry the steer", p.startCount())
	}
	if !strings.Contains(delivered, "Re-check the partial-return path") || !strings.Contains(delivered, "Follow-up instruction") {
		t.Fatalf("sent %q, want the instruction in the steer framing", delivered)
	}

	dir := runDir(t, cfg, out)
	st, err := (store.Run{Dir: dir}).ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.QueuedSteers) != 1 || st.QueuedSteers[0].Status != store.SteerDelivered || st.QueuedSteers[0].Turn != 3 {
		t.Fatalf("queued steers = %+v, want one delivered at turn 3", st.QueuedSteers)
	}
	if len(st.Steers) != 1 || st.Steers[0].Continuation != ContinueLive {
		t.Fatalf("steers = %+v, want one live", st.Steers)
	}
	if note := readFile(t, filepath.Join(dir, "note.md")); !strings.Contains(note, "partial-return path drops the last page") {
		t.Fatalf("the note was not refiled from the steered answer:\n%s", note)
	}
	kinds := strings.Join(eventKinds(t, dir), "\n")
	queued := strings.Index(kinds, "steer_queued")
	steer := strings.Index(kinds, "steer:live:Re-check the partial-return path")
	if queued < 0 || steer < queued {
		t.Fatalf("events want steer_queued then the live steer:\n%s", kinds)
	}
}

// TestLiveSteerHeldWhenTheSessionTakesNoMessage is the other provider: a
// session that cannot take a message mid-run (Cursor, Qwen, a Codex turn
// whose stream has closed). The steer is never refused — it is held, the run
// finishes on its own answer, and the held steer is what HeldSteers hands
// whoever applies it once the run has settled.
func TestLiveSteerHeldWhenTheSessionTakesNoMessage(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{sendErr: errors.New("one message per session")}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		s.emit(provider.Event{Kind: provider.EvUsage, Turns: 2})
		if _, err := (store.Run{Dir: spec.RunDir}).QueueSteer("Also check the export worker", "", time.Now()); err != nil {
			t.Error(err)
		}
		s.emit(finalEvent(triageDoc))
	}
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{NoteOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("ended %q: %s", out.State.Status, out.State.Reason)
	}
	if sent := p.session(0).sentTexts(); len(sent) != 1 {
		t.Fatalf("sends = %q, want the one attempt", sent)
	}
	dir := runDir(t, cfg, out)
	rn := store.Run{Dir: dir}
	st, _ := rn.ReadState()
	if len(st.QueuedSteers) != 1 || st.QueuedSteers[0].Status != store.SteerHeld {
		t.Fatalf("queued steers = %+v, want one held", st.QueuedSteers)
	}
	if len(st.Steers) != 0 {
		t.Fatalf("steers = %+v, want none until it is applied", st.Steers)
	}
	kinds := strings.Join(eventKinds(t, dir), "\n")
	if !strings.Contains(kinds, "steer_queued") || !strings.Contains(kinds, "steer_held") {
		t.Fatalf("events want steer_queued and steer_held:\n%s", kinds)
	}
	held, err := HeldSteers(rn, &st)
	if err != nil || len(held) != 1 || held[0].Text != "Also check the export worker" {
		t.Fatalf("held = %+v, %v", held, err)
	}
}

// TestLiveSteerOnAnotherModelWaitsForTheRunToSettle: a model is a session,
// so a queued steer that names a different one is not sent into this one.
func TestLiveSteerOnAnotherModelWaitsForTheRunToSettle(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if _, err := (store.Run{Dir: spec.RunDir}).QueueSteer("Try it on the bigger model", "some-other-model", time.Now()); err != nil {
			t.Error(err)
		}
		s.emit(finalEvent(triageDoc))
	}
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{NoteOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if sent := p.session(0).sentTexts(); len(sent) != 0 {
		t.Fatalf("sends = %q, want none", sent)
	}
	st, _ := (store.Run{Dir: runDir(t, cfg, outs[0])}).ReadState()
	if len(st.QueuedSteers) != 1 || st.QueuedSteers[0].Status != store.SteerHeld {
		t.Fatalf("queued steers = %+v, want one held", st.QueuedSteers)
	}
}
