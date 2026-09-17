package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// The sentence the Claude CLI answers with when the login has spent one
// model's allowance, as the owner's 2026-09-17 screenshot recorded it.
const limitSentence = "You've reached your Fable limit. Switch to another model, or manage usage credits at claude.ai/settings/usage."

func modelLimitEvent() provider.Event {
	return provider.Event{
		Kind:  provider.EvModelLimit,
		Model: "Fable",
		Text:  limitSentence,
		Raw:   json.RawMessage(`{"type":"assistant"}`),
	}
}

// emptyFinal is the result line that follows the refusal: the CLI ends the
// turn, and the turn carries no document. Before EvModelLimit existed this
// was the whole of what the run layer saw, which is why it re-asked.
func emptyFinal() provider.Event {
	return provider.Event{Kind: provider.EvFinal, Text: limitSentence, Raw: json.RawMessage(`{"type":"result"}`)}
}

// withFallbacks is the workspace configuration plus a fallback list.
func withFallbacks(t *testing.T, models ...string) *config.Config {
	t.Helper()
	cfg := newWorkspace(t)
	cfg.Providers.Claude.FallbackModels = models
	return cfg
}

// TestModelLimitBlocksInsteadOfRetrying is the finding this branch exists
// for. Sirdar read the CLI's "You've reached your Fable limit" as a turn
// that ended without an answer, spent the empty-turn retries re-asking the
// same model, and failed the run. One session, one refusal, and the run
// stops blocked with the model in its reason.
func TestModelLimitBlocksInsteadOfRetrying(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(modelLimitEvent(), emptyFinal())}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusBlocked {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if out.State.Reason != "model limit: Fable" {
		t.Fatalf("reason %q, want the model named", out.State.Reason)
	}
	if got := ModelLimited(out.State.Reason); got != "Fable" {
		t.Fatalf("ModelLimited(%q) = %q", out.State.Reason, got)
	}
	// One session, and nothing sent back into it: the revision retry is
	// what re-asked the same model three times.
	if n := p.startCount(); n != 1 {
		t.Fatalf("started %d sessions; a model limit must not buy a retry", n)
	}
	if sent := p.session(0).sentTexts(); len(sent) != 0 {
		t.Fatalf("the session was re-asked: %q", sent)
	}
	// The handle is kept, which is what makes the run resumable under
	// another model rather than a failure to start again.
	if out.State.Handle == "" {
		t.Fatal("a blocked run must keep the handle it can be continued from")
	}
	// And the refusal is not left behind as an answer a later steer would
	// prime a fresh session with.
	raw, err := os.ReadFile(filepath.Join(runDir(t, cfg, out), "result.raw.txt"))
	if err == nil && strings.Contains(string(raw), "reached your Fable limit") {
		t.Fatalf("the refusal was kept as the run's answer: %q", raw)
	}
}

// The transcript says which model ran out and on whose login, because a
// reason of five words is not enough to act on a week later.
func TestModelLimitIsOnTheEventLog(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(modelLimitEvent(), emptyFinal())}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	log := readFile(t, filepath.Join(runDir(t, cfg, outs[0]), "events.jsonl"))
	for _, want := range []string{"model limit: Fable", "on the claude login"} {
		if !strings.Contains(log, want) {
			t.Fatalf("events.jsonl does not say %q:\n%s", want, log)
		}
	}
}

// TestFallbackModelsAreTriedInOrder: with providers.claude.fallbackModels
// set, a per-model limit moves the run on by itself. The second session
// asks for the first fallback, resumes the same session handle so the
// transcript is kept, and the transcript records the switch.
func TestFallbackModelsAreTriedInOrder(t *testing.T) {
	cfg := withFallbacks(t, "claude-opus-5", "claude-sonnet-5")
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if spec.Model == "claude-opus-5" {
			s.emit(finalEvent(triageDoc))
			return
		}
		s.emit(modelLimitEvent())
		s.emit(emptyFinal())
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if n := p.startCount(); n != 2 {
		t.Fatalf("started %d sessions, want the limited one and its fallback", n)
	}
	second := p.spec(1)
	if second.Model != "claude-opus-5" {
		t.Fatalf("the fallback session asked for %q, want the first configured fallback", second.Model)
	}
	if second.Resume == "" {
		t.Fatal("the fallback must resume the session the first model built")
	}
	// The run now reads as the model that finished it, and says what put
	// it there.
	if out.State.Model != "claude-opus-5" {
		t.Fatalf("the run's model is %q, want the one that wrote the note", out.State.Model)
	}
	if n := len(out.State.ModelSegments); n != 1 {
		t.Fatalf("model segments %+v", out.State.ModelSegments)
	}
	seg := out.State.ModelSegments[0]
	if seg.Model != "claude-opus-5" || seg.Why != "model limit: Fable" {
		t.Fatalf("segment %+v", seg)
	}
	log := readFile(t, filepath.Join(runDir(t, cfg, out), "events.jsonl"))
	if !strings.Contains(log, "Switched to claude-opus-5") {
		t.Fatalf("the switch is not in the transcript:\n%s", log)
	}
}

// A fallback that is refused too moves on to the next one, and a list that
// runs out leaves the run blocked on the last model refused. No model is
// tried twice: the configured model repeated in the list costs nothing.
func TestFallbackListIsWalkedOnceAndThenStops(t *testing.T) {
	cfg := withFallbacks(t, "claude-fable-5-1", "claude-opus-5", "claude-sonnet-5")
	cfg.Model = "claude-fable-5-1"
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		ev := modelLimitEvent()
		ev.Model = spec.Model
		s.emit(ev)
		s.emit(emptyFinal())
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusBlocked {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	var models []string
	for i := 0; i < p.startCount(); i++ {
		models = append(models, p.spec(i).Model)
	}
	want := []string{"claude-fable-5-1", "claude-opus-5", "claude-sonnet-5"}
	if strings.Join(models, ",") != strings.Join(want, ",") {
		t.Fatalf("models asked for %v, want %v", models, want)
	}
	if out.State.Reason != "model limit: claude-sonnet-5" {
		t.Fatalf("reason %q, want the last model refused", out.State.Reason)
	}
}

// TestResumeWithAModelAsksForIt is the operator's way past a blocked run:
// `sirdar resume RUN --model NAME`. The continued session asks for the
// named model, and so does the run from then on.
func TestResumeWithAModelAsksForIt(t *testing.T) {
	cfg := newWorkspace(t)
	cfg.Model = "claude-fable-5-1"
	answered := false
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if answered {
			s.emit(finalEvent(triageDoc))
			return
		}
		answered = true
		s.emit(modelLimitEvent())
		s.emit(emptyFinal())
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusBlocked {
		t.Fatalf("status %q", outs[0].State.Status)
	}

	out, err := r.Resume(context.Background(), outs[0].State.RunID, ResumeOptions{Model: "claude-opus-5"})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if got := p.spec(p.startCount() - 1).Model; got != "claude-opus-5" {
		t.Fatalf("the resumed session asked for %q", got)
	}
	if out.State.RequestedModel() != "claude-opus-5" {
		t.Fatalf("the run still asks for %q", out.State.RequestedModel())
	}
	if n := len(out.State.ModelSegments); n != 1 || out.State.ModelSegments[0].Why != "resume --model" {
		t.Fatalf("model segments %+v", out.State.ModelSegments)
	}
}

// A steer can name a model too, for a finished run a reader wants a second
// opinion on.
func TestSteerWithAModelAsksForIt(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Steer(context.Background(), outs[0].State.RunID, "Check the pager too", SteerOptions{Model: "claude-opus-5"})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.spec(p.startCount() - 1).Model; got != "claude-opus-5" {
		t.Fatalf("the steered session asked for %q", got)
	}
	if n := len(out.State.ModelSegments); n != 1 || out.State.ModelSegments[0].Why != "steer --model" {
		t.Fatalf("model segments %+v", out.State.ModelSegments)
	}
}

// A per-model limit is not a rate limit, so it must not park the other
// runs in the same invocation behind a clock that is not coming.
func TestModelLimitDoesNotPauseThePool(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if strings.Contains(spec.Prompt, "Key: OMNI-1\n") {
			s.emit(modelLimitEvent())
			s.emit(emptyFinal())
			return
		}
		s.emit(finalEvent(triageDoc))
	}}
	paused := 0
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.onPause = func(_ time.Time) { paused++ }

	outs, err := r.Triage(context.Background(), []string{"OMNI-1", "OMNI-2"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if paused != 0 {
		t.Fatalf("the pool was paused %d times by a per-model limit", paused)
	}
	if outs[1].State.Status != store.StatusCompleted {
		t.Fatalf("the second run ended %q: %q", outs[1].State.Status, outs[1].State.Reason)
	}
}
