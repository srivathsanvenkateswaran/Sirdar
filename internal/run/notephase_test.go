package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// replyThenNote answers a reply turn (no schema) with reply and a note turn (schema) with the
// note events.
func replyThenNote(reply string, note ...provider.Event) func(provider.SessionSpec, *stubSession) {
	return replyEventsThenNote([]provider.Event{{Kind: provider.EvFinal, Text: reply}}, note...)
}

// replyEventsThenNote is replyThenNote for a reply turn that does more than
// answer: it plays the reply events in the turn with no schema, and the
// note events in the turn held to one.
func replyEventsThenNote(reply []provider.Event, note ...provider.Event) func(provider.SessionSpec, *stubSession) {
	return func(spec provider.SessionSpec, s *stubSession) {
		if len(spec.OutputSchema) == 0 {
			replay(reply...)(spec, s)
			return
		}
		replay(note...)(spec, s)
	}
}

// eventPhases reads events.jsonl and returns each line's kind and phase.
func eventPhases(t *testing.T, dir string) (kinds, phases []string) {
	t.Helper()
	for _, line := range eventLogLines(t, dir) {
		var ev struct {
			Kind    string `json:"kind"`
			Payload struct {
				Phase string `json:"phase"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("events.jsonl line %q: %v", line, err)
		}
		kinds = append(kinds, ev.Kind)
		phases = append(phases, ev.Payload.Phase)
	}
	return kinds, phases
}

func TestTriageRepliesThenFilesTheNote(t *testing.T) {
	cfg := newWorkspace(t)
	const reply = "The refund is stuck: the ledger skips zero rows."
	p := &stubProvider{script: replyThenNote(reply, finalEvent(triageDoc))}
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
		t.Fatalf("started %d sessions, want the reply and the note", n)
	}
	if p.spec(0).OutputSchema != nil {
		t.Error("the reply turn was given a schema")
	}
	noteSpec := p.spec(1)
	if !bytes.Equal(noteSpec.OutputSchema, prompt.TriageSchema) {
		t.Error("the note turn was not given the triage schema")
	}
	if noteSpec.Resume != "handle-abc" {
		t.Errorf("note turn resume %q, want the reply session's handle", noteSpec.Resume)
	}
	if !strings.Contains(noteSpec.Prompt, "File the note for this investigation from what you found; add nothing you did not find.") {
		t.Errorf("note turn prompt:\n%s", noteSpec.Prompt)
	}
	dir := runDir(t, cfg, out)
	if pol := noteSpec.Policy; pol == nil || len(pol.BashAllow) != 0 || pol.Ask ||
		len(pol.ReadRoots) != 1 || pol.ReadRoots[0] != dir {
		t.Errorf("note turn policy %+v, want reads of %s and nothing else", noteSpec.Policy, dir)
	}

	if got := readFile(t, filepath.Join(dir, "answer.md")); got != reply+"\n" {
		t.Errorf("answer.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md")); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("register rows %d, want 1", len(rows))
	}
	st, err := (store.Run{Dir: dir}).ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != store.StatusCompleted || st.Phase != "" || st.NoteWarning != "" || !st.ReplyFirst {
		t.Errorf("state status=%q phase=%q noteWarning=%q replyFirst=%v", st.Status, st.Phase, st.NoteWarning, st.ReplyFirst)
	}

	kinds, phases := eventPhases(t, dir)
	first := -1
	for i, k := range kinds {
		if k == "final" {
			first = i
			break
		}
	}
	if first < 0 {
		t.Fatalf("no final in events.jsonl: %v", kinds)
	}
	if first == len(kinds)-1 {
		t.Fatalf("nothing was logged after the reply: %v", kinds)
	}
	for i := range kinds {
		want := ""
		if i > first {
			want = "note"
		}
		if phases[i] != want {
			t.Errorf("line %d (%s) phase %q, want %q", i, kinds[i], phases[i], want)
		}
	}
}

func TestPhaseIsNoteWhileTheNoteIsFiled(t *testing.T) {
	cfg := newWorkspace(t)
	seen := make(chan store.State, 1)
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		if len(spec.OutputSchema) == 0 {
			replay(provider.Event{Kind: provider.EvFinal, Text: "The ledger skips zero rows."})(spec, s)
			return
		}
		defer s.finish()
		st, err := (store.Run{Dir: spec.RunDir}).ReadState()
		if err != nil {
			t.Error(err)
		}
		seen <- st
		s.emit(finalEvent(triageDoc))
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	select {
	case st := <-seen:
		if st.Status != store.StatusRunning || st.Phase != store.PhaseNote {
			t.Fatalf("during the note turn: status %q phase %q", st.Status, st.Phase)
		}
	default:
		t.Fatal("the note turn never started")
	}
}

func TestNoteFailureLeavesTheReplyCompleted(t *testing.T) {
	cfg := newWorkspace(t)
	const reply = "The ledger skips zero rows."
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		if len(spec.OutputSchema) == 0 {
			replay(provider.Event{Kind: provider.EvFinal, Text: reply})(spec, s)
			return
		}
		defer s.finish()
		if !s.emit(finalEvent(`{"title":1}`)) {
			return
		}
		select {
		case <-s.sendCh:
		case <-s.cancelled:
			return
		}
		s.emit(finalEvent(`{"title":1}`))
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
	if !strings.HasPrefix(out.State.NoteWarning, "note not filed: schema validation failed twice") {
		t.Errorf("note warning %q", out.State.NoteWarning)
	}
	if out.State.Phase != "" {
		t.Errorf("phase %q after the run settled", out.State.Phase)
	}
	dir := runDir(t, cfg, out)
	if got := readFile(t, filepath.Join(dir, "answer.md")); got != reply+"\n" {
		t.Errorf("answer.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); !os.IsNotExist(err) {
		t.Errorf("note.md exists after a failed note turn: %v", err)
	}
	if rows, _ := store.ReadRegister(cfg.Root); len(rows) != 0 {
		t.Errorf("register rows %+v, want none", rows)
	}
	if ExitCode(outs) != 0 {
		t.Errorf("exit code %d for a run that answered", ExitCode(outs))
	}
}

func TestNoteTurnRefusedToolFailsTheNoteNotTheRun(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyThenNote("The ledger skips zero rows.",
		provider.Event{Kind: provider.EvPermission, Decision: "deny", Tool: "Bash"},
		finalEvent(triageDoc),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !strings.Contains(out.State.NoteWarning, "Bash") {
		t.Errorf("note warning %q, want the refused tool named", out.State.NoteWarning)
	}
	if out.State.Ask != nil {
		t.Errorf("the note turn left a question on the run: %+v", out.State.Ask)
	}
}

func TestPrimedNoteTurnWhenTheProviderCannotResume(t *testing.T) {
	cfg := newWorkspace(t)
	const reply = "The ledger skips zero rows."
	p := &stubProvider{script: replyThenNote(reply, finalEvent(triageDoc))}
	r := newRunner(cfg, steerable{stubProvider: p, c: provider.ContinuePrimed}, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted || outs[0].State.NoteWarning != "" {
		t.Fatalf("status %q reason %q note warning %q", outs[0].State.Status, outs[0].State.Reason, outs[0].State.NoteWarning)
	}
	if p.startCount() != 2 {
		t.Fatalf("started %d sessions", p.startCount())
	}
	noteSpec := p.spec(1)
	if noteSpec.Resume != "" {
		t.Errorf("a primed note turn resumed %q", noteSpec.Resume)
	}
	original := strings.TrimSpace(p.spec(0).Prompt)
	for _, want := range []string{original, "## Your reply", reply, "File the note for this investigation"} {
		if !strings.Contains(noteSpec.Prompt, want) {
			t.Errorf("primed note prompt lacks %q", firstLine(want))
		}
	}
}

func TestRCARepliesThenFilesTheNote(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyThenNote("The export fails for large orders.", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	if outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil || outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("triage: %v %+v", err, outs)
	}

	p.script = replyThenNote("Caused by the ledger change.", finalEvent(rcaDoc))
	before := p.startCount()
	out, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q note warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if n := p.startCount() - before; n != 2 {
		t.Fatalf("the rca started %d sessions, want the reply and the note", n)
	}
	if p.spec(before).OutputSchema != nil {
		t.Error("the rca reply turn was given a schema")
	}
	noteSpec := p.spec(before + 1)
	if !bytes.Equal(noteSpec.OutputSchema, prompt.RCASchema) {
		t.Error("the rca note turn was not given the rca schema")
	}
	if !strings.Contains(noteSpec.Prompt, "Audit rule:") {
		t.Errorf("the rca note turn prompt lacks the audit rule:\n%s", noteSpec.Prompt)
	}
	if got := readFile(t, filepath.Join(runDir(t, cfg, out), "answer.md")); got != "Caused by the ledger change.\n" {
		t.Errorf("answer.md = %q", got)
	}
	if len(out.State.Notes) < 2 {
		t.Fatalf("notes %v, want the rca and the resolution", out.State.Notes)
	}
	for _, path := range out.State.Notes {
		if _, err := os.Stat(path); err != nil {
			t.Error(err)
		}
	}
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	var rca, res int
	for _, row := range rows {
		switch row.Kind {
		case "rca":
			rca++
		case "resolution":
			res++
		}
	}
	if rca != 1 || res != 1 {
		t.Errorf("register rca rows %d resolution rows %d, want one each", rca, res)
	}
}

func TestNoteOnlyTriageKeepsOneSchemaSession(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{NoteOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if p.startCount() != 1 {
		t.Fatalf("started %d sessions, want one", p.startCount())
	}
	if !bytes.Equal(p.spec(0).OutputSchema, prompt.TriageSchema) {
		t.Error("the one session was not given the triage schema")
	}
	if out.State.ReplyFirst {
		t.Error("a note-only triage is marked reply-first")
	}
	dir := runDir(t, cfg, out)
	if _, err := os.Stat(filepath.Join(dir, "answer.md")); !os.IsNotExist(err) {
		t.Errorf("answer.md exists on a note-only run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); err != nil {
		t.Error(err)
	}
}

func TestOverBudgetInTheNoteTurnKeepsTheReply(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyThenNote("The ledger skips zero rows.",
		provider.Event{Kind: provider.EvUsage, Turns: 4, CostUSD: 6},
		finalEvent(triageDoc),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !strings.Contains(out.State.NoteWarning, "exceeded the $5.00 budget") {
		t.Errorf("note warning %q", out.State.NoteWarning)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "answer.md")); err != nil {
		t.Error(err)
	}
}

// TestSteerDuringTheNoteTurnIsHeld: a follow-up typed while the note is
// filed is not sent into the note turn, which writes down a finished
// investigation; it is held for the settled run, and the lines that say so
// are marked as the note turn's.
func TestSteerDuringTheNoteTurnIsHeld(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		if len(spec.OutputSchema) == 0 {
			replay(provider.Event{Kind: provider.EvFinal, Text: "The ledger skips zero rows."})(spec, s)
			return
		}
		defer s.finish()
		if _, err := (store.Run{Dir: spec.RunDir}).QueueSteer("Also check the refunds table", "", time.Now()); err != nil {
			t.Error(err)
		}
		s.emit(finalEvent(triageDoc))
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.SteerPoll = 5 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q note warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if sent := p.session(1).sentTexts(); len(sent) != 0 {
		t.Fatalf("the note turn was sent %q", sent)
	}
	if q := out.State.QueuedSteers; len(q) != 1 || q[0].Status != store.SteerHeld {
		t.Fatalf("queued steers %+v, want one held", q)
	}
	kinds, phases := eventPhases(t, runDir(t, cfg, out))
	var held bool
	for i, k := range kinds {
		if k == "steer_queued" || k == "steer_held" {
			if phases[i] != "note" {
				t.Errorf("%s line phase %q, want note", k, phases[i])
			}
			held = held || k == "steer_held"
		}
	}
	if !held {
		t.Errorf("no steer_held line: %v", kinds)
	}
}

func TestBreachInTheNoteTurnFailsTheRunAndClearsThePhase(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyThenNote("The ledger skips zero rows.",
		breachEvent("the session wrote src/ledger.go"), finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	st, err := (store.Run{Dir: runDir(t, cfg, out)}).ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != "" || out.State.Phase != "" {
		t.Errorf("phase %q on disk, %q returned, after a breach ended the run", st.Phase, out.State.Phase)
	}
}

// TestBudgetCoversBothTurnsTogether: each turn alone is under the $5 cap,
// the two together are over it. The run is held to the total, the note
// session is told what is left, and the run's usage is the sum.
func TestBudgetCoversBothTurnsTogether(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyEventsThenNote([]provider.Event{
		{Kind: provider.EvUsage, Turns: 2, CostUSD: 3},
		{Kind: provider.EvFinal, Text: "The ledger skips zero rows."},
	}, provider.Event{Kind: provider.EvUsage, Turns: 2, CostUSD: 3}, finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !strings.Contains(out.State.NoteWarning, "cost $6.00 exceeded the $5.00 budget") {
		t.Errorf("note warning %q", out.State.NoteWarning)
	}
	if u := out.State.Usage; u.CostUSD != 6 || u.Turns != 4 {
		t.Errorf("usage %+v, want the two turns' sum", u)
	}
	if b := p.spec(1).Budget; b.MaxUSD != 2 || b.MaxTurns != 58 {
		t.Errorf("note session budget %+v, want what the reply turn left", b)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "answer.md")); err != nil {
		t.Error(err)
	}
	if rows, _ := store.ReadRegister(cfg.Root); len(rows) != 0 {
		t.Errorf("register rows %+v after the budget refused the note", rows)
	}
}

func TestRunUsageIsTheSumOfBothTurns(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyEventsThenNote([]provider.Event{
		{Kind: provider.EvUsage, Turns: 2, InputTok: 100, OutputTok: 10, CostUSD: 1},
		{Kind: provider.EvFinal, Text: "The ledger skips zero rows."},
	}, provider.Event{Kind: provider.EvUsage, Turns: 3, InputTok: 50, OutputTok: 20, CostUSD: 1.5}, finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q note warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if u := out.State.Usage; u.Turns != 5 || u.InputTokens != 150 || u.OutputTokens != 30 || u.CostUSD != 2.5 {
		t.Errorf("usage %+v, want the two turns' sum", u)
	}
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil || len(rows) != 1 || rows[0].CostUSD != 2.5 || rows[0].Turns != 5 {
		t.Errorf("register %+v, %v", rows, err)
	}
}

// TestRepeatedReplyResultLineDoesNotDisturbTheNoteTurn: a CLI that repeats
// its result line on the way out does so after the note turn has begun.
// That line belongs to the reply; it must not be judged as a note, spend
// the schema retry, or start a session that replaces the note turn's.
func TestRepeatedReplyResultLineDoesNotDisturbTheNoteTurn(t *testing.T) {
	cfg := newWorkspace(t)
	const reply = "The ledger skips zero rows."
	p := &stubProvider{sendErr: errors.New("the session has exited")}
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if len(spec.OutputSchema) > 0 {
			s.emit(finalEvent(triageDoc))
			return
		}
		if !s.emit(provider.Event{Kind: provider.EvFinal, Text: reply}) {
			return
		}
		for p.startCount() < 2 {
			time.Sleep(time.Millisecond)
		}
		s.emit(provider.Event{Kind: provider.EvFinal, Text: reply})
		s.emit(provider.Event{Kind: provider.EvPermission, Decision: "deny", Tool: "Bash"})
	}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q note warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if n := p.startCount(); n != 2 {
		t.Fatalf("started %d sessions, want the reply and the note", n)
	}
	for i := 0; i < 2; i++ {
		if p.session(i).waitCount() == 0 {
			t.Errorf("session %d was never reaped", i)
		}
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "note.md")); err != nil {
		t.Error(err)
	}
}

// TestNoteTurnPrimedWhenTheProviderCannotContinue: cursor and agy refuse
// to continue a run, because a follow-up could lead to tool calls nothing
// judges first. The note turn leads to none, so it runs primed in a fresh
// session under the note policy and files the note.
func TestNoteTurnPrimedWhenTheProviderCannotContinue(t *testing.T) {
	cfg := newWorkspace(t)
	const reply = "The ledger skips zero rows."
	stub := &stubProvider{script: replyThenNote(reply, finalEvent(triageDoc))}
	p := steerable{stubProvider: stub, c: provider.ContinueNone, err: errors.New("cannot continue")}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q note warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	noteSpec := stub.spec(1)
	if noteSpec.Resume != "" || !strings.Contains(noteSpec.Prompt, "## Your reply") || !strings.Contains(noteSpec.Prompt, reply) {
		t.Errorf("note turn resume %q, prompt not primed", noteSpec.Resume)
	}
	if pol := noteSpec.Policy; pol == nil || len(pol.BashAllow) != 0 || len(pol.ReadRoots) != 1 {
		t.Errorf("note turn policy %+v", noteSpec.Policy)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "note.md")); err != nil {
		t.Error(err)
	}
}

// TestNoNoteTurnAfterTheWallClockRanOut: the wall-clock timer fires once,
// on whichever session is live. One that fired while the reply was on its
// way would not fire again for the note session, so no note turn starts;
// the run keeps its reply and says why it filed no note.
func TestNoNoteTurnAfterTheWallClockRanOut(t *testing.T) {
	cfg := newWorkspace(t)
	const reply = "The ledger skips zero rows."
	p := &stubProvider{}
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if len(spec.OutputSchema) > 0 {
			s.emit(finalEvent(triageDoc))
			return
		}
		// The reply lands after the timer has cancelled this session:
		// it was already in the stream.
		<-s.cancelled
		s.events <- provider.Event{Kind: provider.EvFinal, Text: reply}
	}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	ctx := context.Background()
	prep, err := r.prepare(ctx, "OMNI-1", store.KindTriage, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// All but a moment of the 25 minute budget is already spent, so the
	// timer fires a second into the run.
	prep.usageBase.ElapsedSeconds = 25*60 - 0.1
	out := r.execute(ctx, prep, "", newPool(nil))

	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if n := p.startCount(); n != 1 {
		t.Fatalf("started %d sessions, want no note turn", n)
	}
	if !strings.Contains(out.State.NoteWarning, "wall-clock budget of 25 minutes exceeded") {
		t.Errorf("note warning %q", out.State.NoteWarning)
	}
	if got := readFile(t, filepath.Join(runDir(t, cfg, out), "answer.md")); got != reply+"\n" {
		t.Errorf("answer.md = %q", got)
	}
}

// TestUnwrittenReplyFailsTheRun: a reply that could not be written to
// answer.md is no reply to file a note from, so the run fails as a
// session run does and no note turn starts.
func TestUnwrittenReplyFailsTheRun(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		if len(spec.OutputSchema) == 0 {
			// A directory where the file should go makes the write fail.
			if err := os.Mkdir(filepath.Join(spec.RunDir, answerFile), 0o755); err != nil {
				t.Error(err)
			}
		}
		replyThenNote("The ledger skips zero rows.", finalEvent(triageDoc))(spec, s)
	}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusFailed || !strings.Contains(out.State.Reason, answerFile) {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if n := p.startCount(); n != 1 {
		t.Errorf("started %d sessions, want no note turn", n)
	}
	if out.State.Phase != "" || out.State.NoteWarning != "" {
		t.Errorf("phase %q note warning %q", out.State.Phase, out.State.NoteWarning)
	}
}

func TestNoteTurnNamesWhyItStopped(t *testing.T) {
	resets := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		ev   provider.Event
		want string
	}{
		{"rate limit", provider.Event{Kind: provider.EvRateLimited, ResetsAt: resets},
			"note not filed: rate limited, resets at 2026-09-10T12:00:00Z"},
		{"permission question", askEvent(), "note not filed: the note turn tried Bash, which it may not use"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newWorkspace(t)
			p := &stubProvider{script: replyThenNote("The ledger skips zero rows.", tc.ev)}
			r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
			r.StallTimeout = -1

			outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			out := outs[0]
			if out.State.Status != store.StatusCompleted {
				t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
			}
			if out.State.NoteWarning != tc.want {
				t.Errorf("note warning %q, want %q", out.State.NoteWarning, tc.want)
			}
		})
	}
}
