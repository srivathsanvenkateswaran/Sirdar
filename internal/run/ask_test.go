package run

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

var rgAsk = &provider.PermissionAsk{
	Kind: provider.AskBash, Tool: "Bash", Summary: "rg -n refund src",
	Patterns: []string{"rg *"}, Verdict: provider.VerdictDeny,
	Reason: "Sirdar policy: not permitted by permissions.bash",
}

func askEvent() provider.Event {
	return provider.Event{Kind: provider.EvPermission, Decision: "ask", Ask: rgAsk, Tool: "Bash",
		Input: json.RawMessage(`{"command":"rg -n refund src"}`), Text: rgAsk.Reason}
}

// blockOnAsk runs a triage whose session stops on rgAsk.
func blockOnAsk(t *testing.T, body string) (*Runner, *stubProvider, store.State) {
	t.Helper()
	cfg := newWorkspaceWith(t, body)
	p := &stubProvider{script: replay(askEvent(), finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return r, p, outs[0].State
}

func TestBlockedOnPermission(t *testing.T) {
	_, p, state := blockOnAsk(t, configYAML)
	if state.Status != store.StatusBlocked {
		t.Fatalf("status %q reason %q", state.Status, state.Reason)
	}
	if state.Reason != "asking: rg -n refund src" {
		t.Errorf("reason %q", state.Reason)
	}
	if state.Ask == nil || state.Ask.Summary != "rg -n refund src" || state.Ask.Patterns[0] != "rg *" {
		t.Errorf("ask %+v", state.Ask)
	}
	if p.session(0).cancelCount() == 0 {
		t.Error("the session was not stopped on the question")
	}
	if len(state.Notes) != 0 {
		t.Errorf("a note was filed past the question: %v", state.Notes)
	}
	if !p.spec(0).Policy.Ask {
		t.Error("permissions.ask defaults on, but the policy does not ask")
	}
	if got := notifyReason(state.Reason); got != "agent asked for permission" {
		t.Errorf("notify reason %q", got)
	}
}

func TestAskOffInConfig(t *testing.T) {
	cfg := newWorkspaceWith(t, strings.Replace(configYAML, "permissions:\n", "permissions:\n  ask: false\n", 1))
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	if p.spec(0).Policy.Ask {
		t.Error("permissions.ask: false still asks")
	}
}

func resumeWith(t *testing.T, r *Runner, p *stubProvider, runID string, o ResumeOptions) (Outcome, provider.SessionSpec) {
	t.Helper()
	p.script = replay(finalEvent(triageDoc))
	first := p.startCount()
	out, err := r.Resume(context.Background(), runID, o)
	if err != nil {
		t.Fatal(err)
	}
	// The session that carries the answer is the first the resume starts;
	// a reply-first triage starts its note turn after it, under a policy
	// of its own.
	return out, p.spec(first)
}

func rgInput(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func TestResumeAllowRunKeepsAGrant(t *testing.T) {
	r, p, state := blockOnAsk(t, configYAML)
	r.Config.Permissions.Bash = []string{"cat *"} // rg is not on the list
	out, spec := resumeWith(t, r, p, state.RunID, ResumeOptions{Decision: &Decision{Verdict: provider.VerdictAllowRun}})
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if out.State.Ask != nil {
		t.Errorf("the answered question is still on the run: %+v", out.State.Ask)
	}
	if len(out.State.Grants) != 1 || out.State.Grants[0].Verdict != provider.VerdictAllowRun {
		t.Fatalf("grants %+v", out.State.Grants)
	}
	if !strings.Contains(spec.Prompt, "allowed `rg -n refund src`") || !strings.Contains(spec.Prompt, "`rg *`") {
		t.Errorf("prompt %q", spec.Prompt)
	}
	for _, cmd := range []string{"rg -n refund src", "rg other"} {
		if d := spec.Policy.Decide("Bash", rgInput(cmd)); !d.Allow {
			t.Errorf("%q not allowed by the grant: %+v", cmd, d)
		}
	}
	rn, _, err := store.Open(r.Config.Root, state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var logged bool
	for _, line := range eventLogLines(t, rn.Dir) {
		if strings.Contains(line, `"grant"`) && strings.Contains(line, "you allowed rg for this run") {
			logged = true
		}
	}
	if !logged {
		t.Error("the grant is not in events.jsonl")
	}
}

func TestResumeAllowOnceIsNotKept(t *testing.T) {
	r, p, state := blockOnAsk(t, configYAML)
	r.Config.Permissions.Bash = []string{"cat *"}
	out, spec := resumeWith(t, r, p, state.RunID, ResumeOptions{Decision: &Decision{Verdict: provider.VerdictAllow}, Answer: "only src/"})
	if len(out.State.Grants) != 0 {
		t.Errorf("an allow-once was kept on the run: %+v", out.State.Grants)
	}
	if !strings.Contains(spec.Prompt, "this once") || !strings.Contains(spec.Prompt, "The operator adds: only src/") {
		t.Errorf("prompt %q", spec.Prompt)
	}
	if d := spec.Policy.Decide("Bash", rgInput("rg -n refund src")); !d.Allow {
		t.Fatalf("the allowed call: %+v", d)
	}
	if d := spec.Policy.Decide("Bash", rgInput("rg -n refund src")); d.Allow {
		t.Errorf("allow once was spent twice: %+v", d)
	}
}

func TestResumeDenyRefusesWithTheReason(t *testing.T) {
	r, p, state := blockOnAsk(t, configYAML)
	r.Config.Permissions.Bash = []string{"cat *"}
	out, spec := resumeWith(t, r, p, state.RunID, ResumeOptions{Decision: &Decision{Verdict: provider.VerdictDeny, Reason: "the index is faster"}})
	if len(out.State.Grants) != 1 || out.State.Grants[0].Verdict != provider.VerdictDeny {
		t.Fatalf("grants %+v", out.State.Grants)
	}
	if !strings.Contains(spec.Prompt, "denied `rg -n refund src`: the index is faster") {
		t.Errorf("prompt %q", spec.Prompt)
	}
	d := spec.Policy.Decide("Bash", rgInput("rg x"))
	if d.Allow || d.Ask != nil || !strings.Contains(d.Message, "the index is faster") {
		t.Errorf("decision %+v", d)
	}
}

func TestResumeDecisionNeedsAQuestion(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvQuestion, Text: "Which database?"})}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Resume(context.Background(), outs[0].State.RunID, ResumeOptions{Decision: &Decision{Verdict: provider.VerdictAllow}})
	if err == nil || !strings.Contains(err.Error(), "not waiting on a permission question") {
		t.Fatalf("err %v", err)
	}

	r2, _, state := blockOnAsk(t, configYAML)
	if _, err := r2.Resume(context.Background(), state.RunID, ResumeOptions{Decision: &Decision{Verdict: "maybe"}}); err == nil {
		t.Fatal("an unknown verdict was accepted")
	}
}

func TestResumeReadsADecisionFromTheTerminal(t *testing.T) {
	r, p, state := blockOnAsk(t, configYAML)
	r.Stdin = strings.NewReader("r\n")
	out, _ := resumeWith(t, r, p, state.RunID, ResumeOptions{})
	if len(out.State.Grants) != 1 || out.State.Grants[0].Verdict != provider.VerdictAllowRun {
		t.Fatalf("grants %+v", out.State.Grants)
	}
}

func TestResumeFreeAnswerToAPermission(t *testing.T) {
	r, p, state := blockOnAsk(t, configYAML)
	out, spec := resumeWith(t, r, p, state.RunID, ResumeOptions{Answer: "grep the logs instead"})
	if len(out.State.Grants) != 0 {
		t.Errorf("a free answer left a grant: %+v", out.State.Grants)
	}
	want := "Your call `rg -n refund src` was not run: the operator replied in words instead of allowing it, and only an allow lets it through. The operator's reply: grep the logs instead\n\n" +
		"Do not retry it, or anything matching `rg *`, on your own: each attempt stops the run and asks the operator again. " +
		"If the reply asks for that call, make exactly that call once more so the operator can allow it; otherwise carry on without it."
	if spec.Prompt != want {
		t.Errorf("prompt %q\nwant   %q", spec.Prompt, want)
	}
}

func TestEvalNeverAsks(t *testing.T) {
	cfg := newWorkspace(t)
	r := newRunner(cfg, &stubProvider{}, stubTracker{}, stubHelpdesk{})
	p := &prepared{state: store.State{Eval: true}, run: store.Run{Dir: t.TempDir()}}
	if r.sessionSpec(p, "").Policy.Ask {
		t.Error("an eval run asks")
	}
}
