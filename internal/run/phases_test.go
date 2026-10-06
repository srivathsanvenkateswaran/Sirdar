package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// phasesYAML is the test workspace with a run model and the given phases
// block appended.
func phasesYAML(phases string) string {
	return configYAML + "model: claude-opus-5-5\n" + phases
}

const noteModelYAML = "phases:\n  note:\n    model: claude-sonnet-5\n"

// triageOne runs a triage of OMNI-1 and returns its outcome.
func triageOne(t *testing.T, r *Runner, o Options) Outcome {
	t.Helper()
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, o)
	if err != nil {
		t.Fatal(err)
	}
	return outs[0]
}

// hasLine reports whether any events.jsonl line contains text.
func hasLine(t *testing.T, dir, text string) bool {
	t.Helper()
	for _, line := range eventLogLines(t, dir) {
		if strings.Contains(line, text) {
			return true
		}
	}
	return false
}

// usage is a usage event reporting turns.
func usage(turns int) provider.Event {
	return provider.Event{Kind: provider.EvUsage, Turns: turns}
}

// untilCancelled plays events and then holds the session open until the
// runner cancels it, as a provider still working would.
func untilCancelled(s *stubSession, events ...provider.Event) {
	defer s.finish()
	for _, ev := range events {
		if !s.emit(ev) {
			return
		}
	}
	<-s.cancelled
}

func TestNoteTurnRunsOnThePhaseModel(t *testing.T) {
	cfg := newWorkspaceWith(t, phasesYAML(noteModelYAML))
	p := &stubProvider{script: replyThenNote("The ledger skips zero rows.", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out := triageOne(t, r, Options{})
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if got := p.spec(0).Model; got != "claude-opus-5-5" {
		t.Errorf("reply model %q", got)
	}
	if got := p.spec(1).Model; got != "claude-sonnet-5" {
		t.Errorf("note model %q", got)
	}
	if got := out.State.RequestedModel(); got != "claude-opus-5-5" {
		t.Errorf("RequestedModel %q, want the investigating model", got)
	}
	segs := out.State.ModelSegments
	if len(segs) == 0 {
		t.Fatal("no model segment for the note turn")
	}
	if last := segs[len(segs)-1]; last.Model != "claude-sonnet-5" || last.Why != "phases.note.model" {
		t.Errorf("last segment %+v", last)
	}
	if !hasLine(t, runDir(t, cfg, out), "Note turn on claude-sonnet-5") {
		t.Error("no system line naming the note model")
	}
}

func TestReplyPhaseModelIsTheRunModel(t *testing.T) {
	cfg := newWorkspaceWith(t, phasesYAML("phases:\n  reply:\n    model: claude-sonnet-5\n"))
	p := &stubProvider{script: replyThenNote("Answer.", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out := triageOne(t, r, Options{})
	if out.State.Model != "claude-sonnet-5" || p.spec(0).Model != "claude-sonnet-5" {
		t.Errorf("state model %q, spec model %q, want the reply model", out.State.Model, p.spec(0).Model)
	}

	p2 := &stubProvider{script: replyThenNote("Answer.", finalEvent(triageDoc))}
	r2 := newRunner(cfg, p2, stubTracker{}, stubHelpdesk{})
	out = triageOne(t, r2, Options{Model: "x"})
	if out.State.Model != "x" || p2.spec(0).Model != "x" {
		t.Errorf("state model %q, spec model %q, want the explicit model", out.State.Model, p2.spec(0).Model)
	}
}

func TestNoteCapIsToldToTheProvider(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyEventsThenNote(
		[]provider.Event{usage(5), {Kind: provider.EvFinal, Text: "Answer."}}, finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	triageOne(t, r, Options{})
	if got := p.spec(1).Budget.MaxTurns; got != config.DefaultNoteMaxTurns {
		t.Errorf("note turn MaxTurns %d, want %d", got, config.DefaultNoteMaxTurns)
	}

	cfg = newWorkspaceWith(t, configYAML+"phases:\n  note:\n    maxTurns: 0\n")
	p = &stubProvider{script: replyEventsThenNote(
		[]provider.Event{usage(5), {Kind: provider.EvFinal, Text: "Answer."}}, finalEvent(triageDoc))}
	r = newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	triageOne(t, r, Options{})
	if got, want := p.spec(1).Budget.MaxTurns, cfg.Budget.MaxTurns-5; got != want {
		t.Errorf("note turn MaxTurns %d with the cap off, want the remaining budget %d", got, want)
	}
}

func TestNoteCapEndsTheNoteTurnWithAWarning(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		if len(spec.OutputSchema) == 0 {
			replay(provider.Event{Kind: provider.EvFinal, Text: "Answer."})(spec, s)
			return
		}
		untilCancelled(s, usage(9))
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out := triageOne(t, r, Options{})
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if want := "note not filed: the note turn used its 8-turn cap"; out.State.NoteWarning != want {
		t.Errorf("NoteWarning %q, want %q", out.State.NoteWarning, want)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "answer.md")); err != nil {
		t.Error(err)
	}
}

func TestNoteFiledOnTheEighthTurnIsKept(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyThenNote("Answer.", usage(8), finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out := triageOne(t, r, Options{})
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "note.md")); err != nil {
		t.Error(err)
	}
}

const wrapUpMarker = "You have used this turn's cap of 3 tool turns"

// cappedReply is a reply session that reaches a 3-turn cap and keeps
// working until cancelled, a wrap-up that answers wrapUp, and a note turn
// that files the note. The costs are Claude's: the wrap-up resumes the
// reply's conversation and reports its running total.
func cappedReply(wrapUp string) func(provider.SessionSpec, *stubSession) {
	return func(spec provider.SessionSpec, s *stubSession) {
		switch {
		case len(spec.OutputSchema) > 0:
			replay(finalEvent(triageDoc))(spec, s)
		case strings.Contains(spec.Prompt, wrapUpMarker):
			replay(provider.Event{Kind: provider.EvUsage, Turns: 1, CostUSD: 0.15},
				provider.Event{Kind: provider.EvFinal, Text: wrapUp})(spec, s)
		default:
			untilCancelled(s, provider.Event{Kind: provider.EvUsage, Turns: 3, CostUSD: 0.10})
		}
	}
}

func wrapUpSpec(t *testing.T, p *stubProvider) provider.SessionSpec {
	t.Helper()
	for i := 0; i < p.startCount(); i++ {
		if strings.Contains(p.spec(i).Prompt, wrapUpMarker) {
			return p.spec(i)
		}
	}
	t.Fatal("no wrap-up session was started")
	return provider.SessionSpec{}
}

func TestReplyCapWrapsUp(t *testing.T) {
	cfg := newWorkspaceWith(t, configYAML+"phases:\n  reply:\n    maxTurns: 3\n")
	p := &stubProvider{script: cappedReply("Partial answer.")}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out := triageOne(t, r, Options{})
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if got := p.spec(0).Budget.MaxTurns; got != 3 {
		t.Errorf("reply MaxTurns %d, want the reply cap", got)
	}
	dir := runDir(t, cfg, out)
	spec := wrapUpSpec(t, p)
	if spec.Budget.MaxTurns != 2 {
		t.Errorf("wrap-up MaxTurns %d", spec.Budget.MaxTurns)
	}
	if pol := spec.Policy; pol == nil || len(pol.ReadRoots) != 1 || pol.ReadRoots[0] != dir || len(pol.BashAllow) != 0 {
		t.Errorf("wrap-up policy %+v", spec.Policy)
	}
	if spec.MCPConfig != "" || spec.OutputSchema != nil {
		t.Errorf("wrap-up MCPConfig %q schema %s", spec.MCPConfig, spec.OutputSchema)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dir, "answer.md"))); got != "Partial answer." {
		t.Errorf("answer.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); err != nil {
		t.Error(err)
	}
	if !hasLine(t, dir, "Reply turn reached its 3-turn cap") {
		t.Error("no system line marking the cap")
	}
	// The reply's three turns and the wrap-up's one are both the run's,
	// and the conversation's running total is counted once.
	if out.State.Usage.Turns != 4 {
		t.Errorf("usage turns %d, want the reply's and the wrap-up's", out.State.Usage.Turns)
	}
	if c := out.State.Usage.CostUSD; c < 0.149 || c > 0.151 {
		t.Errorf("usage cost %.4f, want the conversation's total 0.15", c)
	}
}

func TestEmptyWrapUpIsOverBudget(t *testing.T) {
	cfg := newWorkspaceWith(t, configYAML+"phases:\n  reply:\n    maxTurns: 3\n")
	p := &stubProvider{script: cappedReply("")}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out := triageOne(t, r, Options{})
	if out.State.Status != store.StatusOverBudget {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if out.State.Reason != "reply turn reached its 3-turn cap" {
		t.Errorf("reason %q", out.State.Reason)
	}
	if sent := wrapUpSessionSends(p); len(sent) != 0 {
		t.Errorf("the empty wrap-up was nudged: %q", sent)
	}
}

func wrapUpSessionSends(p *stubProvider) []string {
	for i := 0; i < p.startCount(); i++ {
		if strings.Contains(p.spec(i).Prompt, wrapUpMarker) {
			return p.session(i).sentTexts()
		}
	}
	return nil
}

func TestNoteModelLimitRetriesOnTheReplyModel(t *testing.T) {
	cfg := newWorkspaceWith(t, phasesYAML(noteModelYAML))
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		switch {
		case len(spec.OutputSchema) == 0:
			replay(provider.Event{Kind: provider.EvFinal, Text: "Answer."})(spec, s)
		case spec.Model == "claude-sonnet-5":
			untilCancelled(s, modelLimitEvent(), emptyFinal())
		default:
			replay(finalEvent(triageDoc))(spec, s)
		}
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out := triageOne(t, r, Options{})
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if n := p.startCount(); n != 3 {
		t.Fatalf("started %d sessions, want reply, note, note retry", n)
	}
	if got := p.spec(2).Model; got != "claude-opus-5-5" {
		t.Errorf("retry model %q, want the run model", got)
	}
	if !hasLine(t, runDir(t, cfg, out), "hit a model limit; filing the note on") {
		t.Error("no system line for the retry")
	}
}

func TestEvalIgnoresPhases(t *testing.T) {
	cfg := newWorkspaceWith(t, phasesYAML("phases:\n  reply:\n    model: claude-sonnet-5\n    maxTurns: 3\n  note:\n    model: claude-haiku-5\n"))
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out := triageOne(t, r, Options{Eval: true, NoteOnly: true})
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if n := p.startCount(); n != 1 {
		t.Fatalf("started %d sessions", n)
	}
	if got := p.spec(0).Model; got != cfg.Model {
		t.Errorf("model %q, want %q", got, cfg.Model)
	}
	if got := p.spec(0).Budget.MaxTurns; got != cfg.Budget.MaxTurns {
		t.Errorf("MaxTurns %d, want %d", got, cfg.Budget.MaxTurns)
	}
}

func TestUpdateNoteUsesTheNoteModel(t *testing.T) {
	cfg := newWorkspaceWith(t, phasesYAML(noteModelYAML))
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := replyFirstTriage(t, cfg, r, p, replyThenNote("Answer.", finalEvent(triageDoc)))

	p.script = replay(finalEvent(triageDoc))
	out, err := r.UpdateNote(context.Background(), first.State.RunID, UpdateNoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if got := p.spec(p.startCount() - 1).Model; got != "claude-sonnet-5" {
		t.Errorf("Update note model %q", got)
	}
	if got := out.State.RequestedModel(); got != "claude-opus-5-5" {
		t.Errorf("RequestedModel %q", got)
	}
	again := false
	for _, line := range eventLogLines(t, runDir(t, cfg, out)) {
		if strings.Contains(line, filingAgain) {
			again = true
		}
		if again && strings.Contains(line, "Note turn on claude-sonnet-5") {
			return
		}
	}
	t.Error("no Note turn on line after the Update note began")
}

func TestUpdateNoteExplicitModelBeatsTheNoteModel(t *testing.T) {
	cfg := newWorkspaceWith(t, phasesYAML(noteModelYAML))
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := replyFirstTriage(t, cfg, r, p, replyThenNote("Answer.", finalEvent(triageDoc)))

	p.script = replay(finalEvent(triageDoc))
	if _, err := r.UpdateNote(context.Background(), first.State.RunID, UpdateNoteOptions{Model: "claude-haiku-5"}); err != nil {
		t.Fatal(err)
	}
	if got := p.spec(p.startCount() - 1).Model; got != "claude-haiku-5" {
		t.Errorf("Update note model %q, want the one the operator named", got)
	}
}

func TestFixRunIgnoresPhases(t *testing.T) {
	cfg := newWorkspaceWith(t, phasesYAML("phases:\n  reply:\n    model: claude-sonnet-5\n    maxTurns: 3\n  note:\n    model: claude-haiku-5\n"))
	const report = `{"summary":"Stream the export","filesChanged":["export/csv.go"],"testsRun":[],"risks":"none","deviationFromNote":""}`
	p := &stubProvider{script: replay(finalEvent(report))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out, err := r.Fix(context.Background(), "OMNI-1", FixOptions{Prompt: "implement the fix"})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if got := p.spec(0).Model; got != "claude-opus-5-5" {
		t.Errorf("fix model %q", got)
	}
	if got := p.spec(0).Budget.MaxTurns; got != cfg.Budget.MaxTurns {
		t.Errorf("fix MaxTurns %d, want %d", got, cfg.Budget.MaxTurns)
	}
}
