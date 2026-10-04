package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

func TestSessionKey(t *testing.T) {
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ instruction, want string }{
		{"Why is the refund for order 1234 stuck in pending?", "ASK-20261004-why-is-the-refund-for"},
		{"Check   the TAX rounding.", "ASK-20261004-check-the-tax-rounding"},
		{"Investigate intermittent authentication failures across regional deployments today", "ASK-20261004-investigate-intermittent-authentication"},
		{"لماذا الاسترداد عالق", "ASK-20261004-session"},
	} {
		if got := SessionKey(tc.instruction, at); got != tc.want {
			t.Errorf("SessionKey(%q) = %q, want %q", tc.instruction, got, tc.want)
		}
	}
	// The date is the start's date in UTC, not the operator's: 23:30 in
	// Bengaluru is 18:00Z the same day.
	ist := time.FixedZone("IST", 5*3600+1800)
	if got := SessionKey("Hi", time.Date(2026, 10, 4, 23, 30, 0, 0, ist)); got != "ASK-20261004-hi" {
		t.Errorf("SessionKey at 23:30 +05:30 = %q", got)
	}
}

const sessionReply = "**The refund is stuck** because the ledger skips zero-amount rows.\n\nEvidence: `ledger.go:33`."

const sessionInstruction = "Why is the refund for order 1234 stuck in pending?"

// runSessionWithoutAReference starts a session from an instruction alone
// against a provider that answers it in one prose final.
func runSessionWithoutAReference(t *testing.T) (*config.Config, *stubProvider, Outcome) {
	t.Helper()
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvFinal, Text: sessionReply})}
	r := newRunner(cfg, p, nil, nil)
	out, err := r.Session(context.Background(), "", Options{Instruction: sessionInstruction, NoBundle: true})
	if err != nil {
		t.Fatal(err)
	}
	return cfg, p, out
}

func TestSessionWithoutAReference(t *testing.T) {
	cfg, p, out := runSessionWithoutAReference(t)
	st := out.State
	if out.Key != "ASK-20260910-why-is-the-refund-for" || st.Key != out.Key {
		t.Fatalf("key %q state key %q", out.Key, st.Key)
	}
	if st.Kind != store.KindSession || st.Status != store.StatusCompleted {
		t.Fatalf("kind %q status %q reason %q", st.Kind, st.Status, st.Reason)
	}
	if !st.ReplyFirst || st.Access != store.AccessReadOnly {
		t.Fatalf("replyFirst %v access %q", st.ReplyFirst, st.Access)
	}
	if len(st.Notes) != 0 {
		t.Fatalf("notes %v", st.Notes)
	}
	if out.Digest.Issue != sessionInstruction {
		t.Errorf("digest issue %q", out.Digest.Issue)
	}

	dir := runDir(t, cfg, out)
	if got := readFile(t, filepath.Join(dir, "answer.md")); got != sessionReply+"\n" {
		t.Fatalf("answer.md = %q", got)
	}
	for _, absent := range []string{
		filepath.Join(dir, "note.md"),
		filepath.Join(dir, "result.json"),
		filepath.Join(dir, "bundle"),
		filepath.Join(cfg.Root, ".sirdar", "register.jsonl"),
	} {
		if _, err := os.Stat(absent); !os.IsNotExist(err) {
			t.Errorf("%s exists (err %v)", absent, err)
		}
	}

	spec := p.spec(0)
	if spec.OutputSchema != nil {
		t.Errorf("OutputSchema = %s, want nil", spec.OutputSchema)
	}
	if !strings.Contains(spec.Prompt, "# Task") || !strings.Contains(spec.Prompt, sessionInstruction) ||
		strings.Contains(spec.Prompt, "# Ticket") {
		t.Errorf("prompt:\n%s", spec.Prompt)
	}
	if got := readFile(t, filepath.Join(dir, "prompt.md")); got != spec.Prompt {
		t.Errorf("prompt.md is not the prompt the session was given")
	}
}

func TestSessionEventsCarryNoPhase(t *testing.T) {
	cfg, _, out := runSessionWithoutAReference(t)
	events := readFile(t, filepath.Join(runDir(t, cfg, out), "events.jsonl"))
	if !strings.Contains(events, `"final"`) {
		t.Fatalf("events.jsonl has no final line:\n%s", events)
	}
	if strings.Contains(events, `"phase"`) {
		t.Errorf("events.jsonl carries a phase outside a note turn:\n%s", events)
	}
}

func TestSessionWithAReference(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvFinal, Text: "Yes: the PR dropped the guard."})}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out, err := r.Session(context.Background(), "OMNI-1", Options{Instruction: "Was it the PR?"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Key != "OMNI-1" || out.State.Status != store.StatusCompleted || out.State.Kind != store.KindSession {
		t.Fatalf("key %q status %q kind %q reason %q", out.Key, out.State.Status, out.State.Kind, out.State.Reason)
	}
	spec := p.spec(0)
	if !strings.Contains(spec.Prompt, "Key: OMNI-1") || !strings.Contains(spec.Prompt, "Was it the PR?") {
		t.Errorf("prompt:\n%s", spec.Prompt)
	}
	if spec.OutputSchema != nil {
		t.Errorf("OutputSchema = %s, want nil", spec.OutputSchema)
	}
	dir := runDir(t, cfg, out)
	if _, err := os.Stat(filepath.Join(dir, "bundle", "ticket.json")); err != nil {
		t.Errorf("bundle: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "answer.md")); got != "Yes: the PR dropped the guard.\n" {
		t.Errorf("answer.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); !os.IsNotExist(err) {
		t.Errorf("note.md exists (err %v)", err)
	}
	if len(out.State.Notes) != 0 {
		t.Errorf("notes %v", out.State.Notes)
	}
}

func TestSessionNeedsAnInstruction(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay()}
	r := newRunner(cfg, p, nil, nil)

	_, err := r.Session(context.Background(), "", Options{NoBundle: true})
	if err == nil || err.Error() != "run: a session needs an instruction" {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, ".sirdar", "runs")); !os.IsNotExist(err) {
		t.Errorf(".sirdar/runs exists (err %v)", err)
	}
	if p.startCount() != 0 {
		t.Errorf("a session started")
	}
}

func TestSessionHonoursTheRunID(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvFinal, Text: "Done."})}
	r := newRunner(cfg, p, nil, nil)

	const id = "20261004T101500Z-ab12"
	out, err := r.Session(context.Background(), "", Options{RunID: id, Instruction: "Check the TAX rounding.", NoBundle: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.RunID != id {
		t.Fatalf("run id %q", out.State.RunID)
	}
	want := filepath.Join(cfg.Root, ".sirdar", "runs", "ASK-20260910-check-the-tax-rounding", id)
	if _, err := os.Stat(filepath.Join(want, "state.json")); err != nil {
		t.Fatalf("run directory: %v", err)
	}
}

func TestSessionWithoutAReplyFails(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay()}
	r := newRunner(cfg, p, nil, nil)

	out, err := r.Session(context.Background(), "", Options{Instruction: sessionInstruction, NoBundle: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusFailed || out.State.Reason != "the session ended without a reply" {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "answer.md")); !os.IsNotExist(err) {
		t.Errorf("answer.md exists (err %v)", err)
	}
}

func TestResumedSessionAsksForAnAnswer(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvQuestion, Text: "Which ledger?"})}
	r := newRunner(cfg, p, nil, nil)

	out, err := r.Session(context.Background(), "", Options{Instruction: sessionInstruction, NoBundle: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusBlocked {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	// A question puts its words in the reason, and a resume answers
	// those with the operator's reply. The nudge to finish is for every
	// other blocked run — an interrupt, a rate limit — so the reason is
	// set to one of those.
	rn, st, err := store.Open(cfg.Root, out.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	st.Reason = "interrupted"
	if err := rn.WriteState(st); err != nil {
		t.Fatal(err)
	}

	p.script = replay(provider.Event{Kind: provider.EvFinal, Text: "The main ledger."})
	resumed, err := r.Resume(context.Background(), out.State.RunID, ResumeOptions{Answer: ""})
	if err != nil {
		t.Fatal(err)
	}
	spec := p.spec(1)
	if spec.Prompt != "Continue where you left off and answer the operator." {
		t.Errorf("resumed prompt %q", spec.Prompt)
	}
	if spec.OutputSchema != nil {
		t.Errorf("resumed OutputSchema = %s, want nil", spec.OutputSchema)
	}
	if spec.Resume != "handle-abc" {
		t.Errorf("resume handle %q", spec.Resume)
	}
	if resumed.State.Status != store.StatusCompleted {
		t.Errorf("resumed status %q reason %q", resumed.State.Status, resumed.State.Reason)
	}
}

// TestLiveSteerOnASessionIsConversation is a follow-up typed while a session
// answers: it goes in as conversation rather than as a request for the
// document again, and the answer to it replaces the earlier one in
// answer.md.
func TestLiveSteerOnASessionIsConversation(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, nil, nil)
	r.SteerPoll = 5 * time.Millisecond

	var delivered string
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if _, err := (store.Run{Dir: spec.RunDir}).QueueSteer("Did you test it?", "", time.Now()); err != nil {
			t.Error(err)
			return
		}
		if !s.emit(provider.Event{Kind: provider.EvFinal, Text: sessionReply}) {
			return
		}
		select {
		case delivered = <-s.sendCh:
		case <-time.After(5 * time.Second):
			t.Error("the follow-up was never sent into the session")
			return
		}
		s.emit(provider.Event{Kind: provider.EvFinal, Text: "No: read from code only. A zero-amount refund on staging would confirm it."})
	}

	out, err := r.Session(context.Background(), "", Options{Instruction: sessionInstruction, NoBundle: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if delivered != conversationPrompt("Did you test it?") {
		t.Fatalf("sent %q", delivered)
	}
	if strings.Contains(delivered, "JSON") || strings.Contains(delivered, "schema") {
		t.Errorf("the follow-up asks for a document: %q", delivered)
	}
	got := readFile(t, filepath.Join(runDir(t, cfg, out), "answer.md"))
	if !strings.HasPrefix(got, "No: read from code only.") {
		t.Errorf("answer.md = %q, want the follow-up's answer", got)
	}
}
