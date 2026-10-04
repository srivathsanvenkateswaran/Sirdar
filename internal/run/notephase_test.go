package run

import (
	"bytes"
	"context"
	"encoding/json"
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
