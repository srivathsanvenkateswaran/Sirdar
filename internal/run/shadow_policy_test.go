package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// askingWorkspace is a workspace that says outright to put a refusal to the
// operator, so a shadow run that does not ask is not asking because of a
// default.
func askingWorkspace(t *testing.T) *config.Config {
	t.Helper()
	return newWorkspaceWith(t, strings.Replace(configYAML, "permissions:\n", "permissions:\n  ask: true\n", 1))
}

func TestShadowPolicyNeverAsks(t *testing.T) {
	cfg := askingWorkspace(t)
	p := &stubProvider{script: replyThenNote("reply", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{Shadow: true})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	spec := p.spec(0)
	if spec.Policy == nil {
		t.Fatal("the shadow reply turn has no policy")
	}
	if spec.Policy.Ask {
		t.Error("a shadow run's policy puts refusals to the operator")
	}
	if spec.Policy.Grants != nil {
		t.Errorf("a shadow run's policy carries grants: %v", spec.Policy.Grants)
	}

	p = &stubProvider{script: replyThenNote("reply", finalEvent(triageDoc))}
	r = newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	if !p.spec(0).Policy.Ask {
		t.Error("an attended run in a workspace with permissions.ask: true does not ask")
	}
}

// deniedPsql is the refusal the denied-call tests play: a database query
// the read-only policy turns down.
func deniedPsql() provider.Event {
	return provider.Event{
		Kind: provider.EvPermission, Decision: "deny", Tool: "Bash",
		Input: json.RawMessage(`{"command":"psql -c 'select 1'"}`),
	}
}

func deniedTriage(t *testing.T, o Options, denies int) store.State {
	t.Helper()
	cfg := askingWorkspace(t)
	reply := make([]provider.Event, 0, denies+1)
	for i := 0; i < denies; i++ {
		reply = append(reply, deniedPsql())
	}
	reply = append(reply, provider.Event{Kind: provider.EvFinal, Text: "The refund is stuck."})
	p := &stubProvider{script: replyEventsThenNote(reply, finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, o)
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	return outs[0].State
}

func TestShadowRecordsDeniedCalls(t *testing.T) {
	st := deniedTriage(t, Options{Shadow: true}, 1)
	if len(st.Denied) != 1 {
		t.Fatalf("denied calls: %+v", st.Denied)
	}
	if st.Denied[0].Tool != "Bash" || !strings.Contains(st.Denied[0].Summary, "psql") {
		t.Errorf("denied call %+v", st.Denied[0])
	}
	if st.Denied[0].At.IsZero() {
		t.Error("the denied call carries no time")
	}
}

func TestShadowDeniedCallsCapped(t *testing.T) {
	st := deniedTriage(t, Options{Shadow: true}, 25)
	if len(st.Denied) != store.MaxDenied {
		t.Fatalf("denied calls: %d, want %d", len(st.Denied), store.MaxDenied)
	}
}

func TestNonShadowDoesNotRecordDenied(t *testing.T) {
	st := deniedTriage(t, Options{}, 1)
	if len(st.Denied) != 0 {
		t.Fatalf("an attended run recorded denied calls: %+v", st.Denied)
	}
}

func TestShadowQuestionRecorded(t *testing.T) {
	cfg := askingWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvQuestion, Text: "Which tenant?"})}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{Shadow: true})
	if err != nil {
		t.Fatal(err)
	}
	st := outs[0].State
	if st.Status != store.StatusBlocked || st.Reason != "agent asked: Which tenant?" {
		t.Fatalf("status %q reason %q", st.Status, st.Reason)
	}
	if !reflect.DeepEqual(st.Questions, []string{"Which tenant?"}) {
		t.Fatalf("questions %q", st.Questions)
	}

	p.script = replay(provider.Event{Kind: provider.EvQuestion, Text: "Which env?"})
	resumed, err := r.Resume(context.Background(), st.RunID, ResumeOptions{Answer: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State.Status != store.StatusBlocked {
		t.Fatalf("resumed status %q reason %q", resumed.State.Status, resumed.State.Reason)
	}
	if !reflect.DeepEqual(resumed.State.Questions, []string{"Which tenant?", "Which env?"}) {
		t.Fatalf("questions after resume %q", resumed.State.Questions)
	}
	if got := p.spec(1).Prompt; got != "x" {
		t.Fatalf("the resumed session's first prompt was %q", got)
	}
}

// shadowReproTriage runs a shadow triage in a workspace whose repro hosts
// are set, with the given .mcp.json ("" for none).
func shadowReproTriage(t *testing.T, mcpJSON string, o Options) (store.State, provider.SessionSpec) {
	t.Helper()
	cfg := reproWorkspace(t, mcpJSON)
	p := &stubProvider{script: replyThenNote("reply", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, o)
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	return outs[0].State, p.spec(0)
}

const reproOffWarning = "staging reproduction off: shadow.reproduce is false"

// TestShadowNoReproduce: shadow.reproduce false takes the reproduction
// section out of the prompt and says so, but keeps the guard, which only
// narrows where the browser may go.
func TestShadowNoReproduce(t *testing.T) {
	st, spec := shadowReproTriage(t, reproMCPJSON, Options{Shadow: true, NoReproduce: true})
	if strings.Contains(spec.Prompt, "# Reproduce on staging") {
		t.Error("a shadow run with shadow.reproduce false carries the reproduction section")
	}
	if spec.Policy.Repro == nil {
		t.Error("a shadow run with shadow.reproduce false lost its repro guard")
	}
	if !contains(st.Warnings, reproOffWarning) {
		t.Errorf("warnings %q", st.Warnings)
	}

	// The same shadow run without NoReproduce reproduces as usual.
	st, spec = shadowReproTriage(t, reproMCPJSON, Options{Shadow: true})
	if !strings.Contains(spec.Prompt, "# Reproduce on staging") {
		t.Error("a shadow run that may reproduce has no reproduction section")
	}
	if contains(st.Warnings, reproOffWarning) {
		t.Errorf("warnings %q on a shadow run that may reproduce", st.Warnings)
	}
}

// TestShadowNoReproduceWhenReproInactive: reproduction that was not
// going to happen anyway is not reported as switched off.
func TestShadowNoReproduceWhenReproInactive(t *testing.T) {
	st, spec := shadowReproTriage(t, "", Options{Shadow: true, NoReproduce: true})
	if strings.Contains(spec.Prompt, "# Reproduce on staging") {
		t.Error("the prompt carries the reproduction section with no browser server")
	}
	if contains(st.Warnings, reproOffWarning) {
		t.Errorf("warnings %q on a run that could not reproduce", st.Warnings)
	}
	if !hasReproWarning(st.Warnings) {
		t.Errorf("warnings %q lack the reason reproduction is not active", st.Warnings)
	}
}

// TestRCARecordsInputs: what an rca was handed is kept on the run, the
// resolution cut to its cap, and the PR goes on the register's RCA row
// only. gh is a stand-in that fails, so the PR is never fetched.
func TestRCARecordsInputs(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}

	const pr = "https://github.com/o/r/pull/9"
	resolution := strings.Repeat("é", 5000)
	p.script = replay(finalEvent(rcaDoc))
	out, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{PRURL: pr, Resolution: resolution})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("rca status %q reason %q", out.State.Status, out.State.Reason)
	}
	in := out.State.RCAInputs
	if in == nil {
		t.Fatal("the rca run kept no inputs")
	}
	if in.PRURL != pr {
		t.Errorf("PRURL %q", in.PRURL)
	}
	if n := utf8.RuneCountInString(in.Resolution); n != store.RCAResolutionMax {
		t.Errorf("resolution has %d runes, want %d", n, store.RCAResolutionMax)
	}

	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	var rca, res *store.RegisterRow
	for i := range rows {
		switch rows[i].Kind {
		case "rca":
			rca = &rows[i]
		case "resolution":
			res = &rows[i]
		}
	}
	if rca == nil || res == nil {
		t.Fatalf("register rows %+v", rows)
	}
	if rca.PRURL != pr {
		t.Errorf("the RCA row's PRURL is %q", rca.PRURL)
	}
	if res.PRURL != "" {
		t.Errorf("the resolution row carries PRURL %q", res.PRURL)
	}
}
