package run

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// steeredDoc is the triage note after a steer that re-checked the cause:
// a different hypothesis under the same title.
var steeredDoc = strings.Replace(triageDoc,
	`"hypothesis":"The export job times out."`,
	`"hypothesis":"The partial-return path drops the last page."`, 1)

// steerable is the stub provider with a continuation of its own.
type steerable struct {
	*stubProvider
	c   provider.Continuation
	err error
}

func (s steerable) Continuation() provider.Continuation { return s.c }
func (s steerable) SteerRefusal() error                 { return s.err }

// triageThen runs one triage to completion and returns its outcome, so
// the steer tests start from a run that actually finished.
func triageThen(t *testing.T, cfg *config.Config, r *Runner, p *stubProvider) Outcome {
	t.Helper()
	p.script = replay(
		provider.Event{Kind: provider.EvUsage, Turns: 3, InputTok: 100, OutputTok: 20, CostUSD: 0.42},
		finalEvent(triageDoc),
	)
	// Note-only, so the steers below continue a run whose answer is its
	// note: a steer on a reply-first run is conversation, which is a
	// different path with tests of its own.
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{NoteOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("triage ended %q: %s", outs[0].State.Status, outs[0].State.Reason)
	}
	return outs[0]
}

// eventKinds reads the kind of every line in a run's events.jsonl, with
// the text of a steer or system line beside it.
func eventKinds(t *testing.T, dir string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var line struct {
			Kind    string `json:"kind"`
			Payload struct {
				Text         string `json:"text"`
				Continuation string `json:"continuation"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		switch line.Kind {
		case "steer":
			out = append(out, "steer:"+line.Payload.Continuation+":"+line.Payload.Text)
		case "system":
			out = append(out, "system:"+line.Payload.Text)
		default:
			out = append(out, line.Kind)
		}
	}
	return out
}

// TestSteerResumesTheRunInPlace is the harness-engine core: a completed
// run takes an instruction, goes back to running against the session it
// recorded, and ends completed again with the transcript, the usage, the
// note and the register all carried forward on the same run.
func TestSteerResumesTheRunInPlace(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	// A clock that moves, so the wall-clock time the run spends is not
	// zero and can be seen to accumulate.
	tick := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { tick = tick.Add(30 * time.Second); return tick }
	first := triageThen(t, cfg, r, p)
	dir := runDir(t, cfg, first)
	noteBefore := readFile(t, filepath.Join(dir, "note.md"))

	// The steer's session is watched for the moment the run is running
	// again: the state on disk must say so while the session is live.
	var statusDuringSteer store.Status
	p.script = func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		// The channel is unbuffered, so once the first event has been
		// taken the runner is past writing the running state.
		// The resumed session reports the conversation's running cost,
		// $0.42 before it and $0.10 of its own, and its own turns and
		// tokens.
		s.emit(provider.Event{Kind: provider.EvUsage, Turns: 2, InputTok: 50, OutputTok: 10, CostUSD: 0.52})
		st, _ := (store.Run{Dir: dir}).ReadState()
		statusDuringSteer = st.Status
		s.emit(finalEvent(steeredDoc))
	}

	out, err := r.Steer(context.Background(), first.State.RunID, "Re-check the partial-return path", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.RunID != first.State.RunID {
		t.Fatalf("a steer made a new run: %s", out.State.RunID)
	}
	if statusDuringSteer != store.StatusRunning {
		t.Fatalf("status during the steer %q, want running", statusDuringSteer)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}

	// The provider was asked to resume the handle the first session
	// recorded, with the instruction as the prompt and the same policy.
	spec := p.spec(1)
	if spec.Resume != "handle-abc" {
		t.Fatalf("resume %q, want the run's handle", spec.Resume)
	}
	if !strings.Contains(spec.Prompt, "Re-check the partial-return path") || strings.Contains(spec.Prompt, "Key: OMNI-1") {
		t.Fatalf("a resumed steer carries the instruction, not the whole prompt:\n%s", spec.Prompt)
	}
	if spec.Mode != provider.ModeTriage || spec.Policy == nil || spec.Policy.Root != cfg.Root {
		t.Fatalf("a steer widened the session: mode %q policy %+v", spec.Mode, spec.Policy)
	}
	// The turn budget handed to the provider is what the run has left.
	if spec.Budget.MaxTurns != cfg.Budget.MaxTurns-3 {
		t.Fatalf("max turns %d, want the remaining %d", spec.Budget.MaxTurns, cfg.Budget.MaxTurns-3)
	}

	// Usage accumulates on the run: 3 + 2 turns, $0.42 + $0.10.
	u := out.State.Usage
	if u.Turns != 5 || u.InputTokens != 150 || u.OutputTokens != 30 || u.CostUSD < 0.519 || u.CostUSD > 0.521 {
		t.Fatalf("usage did not accumulate: %+v", u)
	}
	if u.ElapsedSeconds <= first.State.Usage.ElapsedSeconds || first.State.Usage.ElapsedSeconds <= 0 {
		t.Fatalf("elapsed did not accumulate: first %v, steered %v", first.State.Usage.ElapsedSeconds, u.ElapsedSeconds)
	}

	// The steer is on the record.
	if len(out.State.Steers) != 1 || out.State.Steers[0].Text != "Re-check the partial-return path" ||
		out.State.Steers[0].Continuation != "resume" {
		t.Fatalf("steers: %+v", out.State.Steers)
	}
	kinds := eventKinds(t, dir)
	want := []string{"usage", "final", "steer:resume:Re-check the partial-return path", "usage", "final"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("events.jsonl:\n got %v\nwant %v", kinds, want)
	}

	// The answer changed, so the note was rendered again in both places
	// and the register got a second row for the same run.
	noteAfter := readFile(t, filepath.Join(dir, "note.md"))
	if noteAfter == noteBefore || !strings.Contains(noteAfter, "partial-return path") {
		t.Fatalf("note.md was not re-rendered:\n%s", noteAfter)
	}
	filed := readFile(t, filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md"))
	if !strings.Contains(filed, "partial-return path") {
		t.Fatalf("the filed note was not re-rendered:\n%s", filed)
	}
	if len(out.State.Notes) != 2 {
		t.Fatalf("notes: %v", out.State.Notes)
	}
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1].RunID != first.State.RunID || rows[1].Turns != 5 {
		t.Fatalf("register rows: %+v", rows)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "result.json")), "partial-return path") {
		t.Fatal("result.json was not replaced")
	}
}

// TestSteerUnchangedAnswerLeavesTheNote: the same document back means
// nothing to render and nothing to index; the steer is still recorded.
func TestSteerUnchangedAnswerLeavesTheNote(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := triageThen(t, cfg, r, p)
	dir := runDir(t, cfg, first)
	noteBefore := readFile(t, filepath.Join(dir, "note.md"))

	// Same value, different formatting: still the same answer.
	p.script = replay(
		provider.Event{Kind: provider.EvUsage, Turns: 1, CostUSD: 0.05},
		finalEvent(strings.ReplaceAll(triageDoc, "\n", "")),
	)
	out, err := r.Steer(context.Background(), first.State.RunID, "Are you sure?", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if readFile(t, filepath.Join(dir, "note.md")) != noteBefore {
		t.Fatal("note.md was rewritten for an unchanged answer")
	}
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("register rows: %d, want the one from the first run", len(rows))
	}
	if len(out.State.Steers) != 1 || out.State.Usage.Turns != 4 {
		t.Fatalf("state: steers %+v usage %+v", out.State.Steers, out.State.Usage)
	}
	if strings.Join(out.State.Notes, ",") != strings.Join(first.State.Notes, ",") {
		t.Fatalf("notes changed: %v", out.State.Notes)
	}
	if out.Digest.Confidence != "medium" || out.Digest.Issue == "" {
		t.Fatalf("digest: %+v", out.Digest)
	}
}

// TestSteerPrimedOpensAFreshSession is the acp shape, and the fallback for
// a run that recorded no handle: no resume, the original prompt plus the
// earlier answer plus the instruction, and the transcript says a new
// session is answering.
func TestSteerPrimedOpensAFreshSession(t *testing.T) {
	cfg := newWorkspace(t)
	stub := &stubProvider{}
	p := steerable{stubProvider: stub, c: provider.ContinuePrimed}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := triageThen(t, cfg, r, stub)
	dir := runDir(t, cfg, first)
	original := readFile(t, filepath.Join(dir, "prompt.md"))

	stub.script = replay(finalEvent(steeredDoc))
	out, err := r.Steer(context.Background(), first.State.RunID, "Now write it up properly", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	spec := stub.spec(1)
	if spec.Resume != "" {
		t.Fatalf("a primed steer resumed %q", spec.Resume)
	}
	for _, want := range []string{strings.TrimRight(original, "\n"), "## Earlier answer", `"The export job times out."`, "Now write it up properly"} {
		if !strings.Contains(spec.Prompt, want) {
			t.Fatalf("primed prompt is missing %q:\n%s", want, spec.Prompt)
		}
	}
	if out.State.Steers[0].Continuation != "primed" {
		t.Fatalf("steers: %+v", out.State.Steers)
	}
	kinds := eventKinds(t, dir)
	if len(kinds) < 4 || kinds[2] != "steer:primed:Now write it up properly" || kinds[3] != "system:"+continuedInNewSession {
		t.Fatalf("events.jsonl: %v", kinds)
	}

	// The same fallback when a resume provider has no handle to resume.
	second := &stubProvider{}
	r2 := newRunner(cfg, second, stubTracker{}, stubHelpdesk{})
	run := triageThen(t, cfg, r2, second)
	rn, state, err := store.Open(cfg.Root, run.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	state.Handle = ""
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}
	second.script = replay(finalEvent(steeredDoc))
	out, err = r2.Steer(context.Background(), run.State.RunID, "Go on", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := second.spec(1); got.Resume != "" || !strings.Contains(got.Prompt, "## Earlier answer") {
		t.Fatalf("a handle-less run was not primed: resume %q", got.Resume)
	}
	if out.State.Steers[0].Continuation != "primed" {
		t.Fatalf("steers: %+v", out.State.Steers)
	}
}

// TestSteerBudgetCapsApplyToTheTotal: the turn cap is the run's, not the
// session's. Three turns already spent and three more under a cap of five
// ends over budget, exactly as six turns in one session would.
func TestSteerBudgetCapsApplyToTheTotal(t *testing.T) {
	cfg := newWorkspaceWith(t, strings.Replace(configYAML, "maxTurns: 60", "maxTurns: 5", 1))
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := triageThen(t, cfg, r, p)

	p.script = replay(
		provider.Event{Kind: provider.EvUsage, Turns: 3, CostUSD: 0.01},
		finalEvent(steeredDoc),
	)
	out, err := r.Steer(context.Background(), first.State.RunID, "Keep going", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusOverBudget || !strings.Contains(out.State.Reason, "6 turns exceeded the 5 turn budget") {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if p.spec(1).Budget.MaxTurns != 2 {
		t.Fatalf("the provider was given %d turns, want the remaining 2", p.spec(1).Budget.MaxTurns)
	}

	// Once the cap is reached, a further steer is refused before anything
	// is written.
	before := readFile(t, filepath.Join(runDir(t, cfg, out), "state.json"))
	if _, err := r.Steer(context.Background(), first.State.RunID, "Again", SteerOptions{}); err == nil || !strings.Contains(err.Error(), "over budget") {
		t.Fatalf("a steer on an over-budget run: %v", err)
	}
	if readFile(t, filepath.Join(runDir(t, cfg, out), "state.json")) != before {
		t.Fatal("a refused steer touched state.json")
	}
}

// TestSteerRefusals covers every run-level refusal, and that each leaves
// the run exactly as it was.
func TestSteerRefusals(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := triageThen(t, cfg, r, p)
	rn, base, err := store.Open(cfg.Root, first.State.RunID)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		mutate func(*store.State)
		runner *Runner
		text   string
		want   string
		live   bool
	}{
		{name: "empty instruction", text: "  ", want: "instruction is empty"},
		{name: "running", mutate: func(s *store.State) { s.Status = store.StatusRunning }, want: "is running", live: true},
		{name: "preparing", mutate: func(s *store.State) { s.Status = store.StatusPreparing }, want: "is preparing", live: true},
		{name: "over budget", mutate: func(s *store.State) { s.Status = store.StatusOverBudget; s.Reason = "cost" }, want: "ran over budget"},
		{name: "eval", mutate: func(s *store.State) { s.Eval = true }, want: "eval replay"},
		{name: "turns spent", mutate: func(s *store.State) { s.Usage.Turns = cfg.Budget.MaxTurns }, want: "turns"},
		{name: "dollars spent", mutate: func(s *store.State) { s.Usage.CostUSD = cfg.Budget.MaxUSD }, want: "budget"},
		{name: "minutes spent", mutate: func(s *store.State) { s.Usage.ElapsedSeconds = float64(cfg.Budget.MaxMinutes * 60) }, want: "wall-clock"},
		{name: "other provider", mutate: func(s *store.State) { s.Provider = "codex" }, want: "made under provider codex"},
		{name: "worktree gone", mutate: func(s *store.State) { s.At = "0123456789abcdef0123456789abcdef01234567" }, want: "worktree is gone"},
		{
			name:   "provider refuses",
			runner: newRunner(cfg, steerable{stubProvider: p, err: errors.New("no continuation on this provider")}, stubTracker{}, stubHelpdesk{}),
			want:   "no continuation on this provider",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := base
			if tc.mutate != nil {
				tc.mutate(&state)
			}
			if err := rn.WriteState(state); err != nil {
				t.Fatal(err)
			}
			before := readFile(t, filepath.Join(rn.Dir, "state.json"))
			starts := p.startCount()
			rr := r
			if tc.runner != nil {
				rr = tc.runner
			}
			text := "Go on"
			if tc.text != "" {
				text = tc.text
			}
			_, err := rr.Steer(context.Background(), first.State.RunID, text, SteerOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Steer = %v, want %q", err, tc.want)
			}
			if tc.live != errors.Is(err, ErrSteerLive) {
				t.Fatalf("ErrSteerLive = %v for %v", errors.Is(err, ErrSteerLive), err)
			}
			if p.startCount() != starts {
				t.Fatal("a refused steer started a session")
			}
			if readFile(t, filepath.Join(rn.Dir, "state.json")) != before {
				t.Fatal("a refused steer touched state.json")
			}
		})
	}

	if _, err := r.Steer(context.Background(), "20260101T000000Z-0000", "Go on", SteerOptions{}); err == nil {
		t.Fatal("a steer on an unknown run succeeded")
	}
}

// TestSteerBlockedRunTakesTheInstructionAsTheAnswer: a run blocked on a
// question is finished, from the operator's side; the instruction is
// what the agent gets, and the run ends completed on the same id.
func TestSteerBlockedRunTakesTheInstructionAsTheAnswer(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvQuestion, Text: "Which database?"})}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusBlocked {
		t.Fatalf("status %q", outs[0].State.Status)
	}
	p.script = replay(finalEvent(triageDoc))
	out, err := r.Steer(context.Background(), outs[0].State.RunID, "The orders database", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.Reason != "" {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !strings.Contains(p.spec(1).Prompt, "The orders database") || p.spec(1).Resume != "handle-abc" {
		t.Fatalf("spec: %+v", p.spec(1))
	}
	if len(out.State.Notes) != 2 {
		t.Fatalf("notes: %v", out.State.Notes)
	}
}

// followUpAnswer is what a session says when asked whether it tested its claim.
const followUpAnswer = "No — read from code only. Unverified."

// finalCount is how many final lines a run's events.jsonl holds.
func finalCount(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	for _, k := range eventKinds(t, dir) {
		if k == "final" {
			n++
		}
	}
	return n
}

// TestSteerOnASessionIsConversation is a follow-up on a settled session
// started from an instruction alone. It has no bundle, which a steer used to
// read and refuse on, and its answer is a reply, not a document.
func TestSteerOnASessionIsConversation(t *testing.T) {
	cfg, p, first := runSessionWithoutAReference(t)
	r := newRunner(cfg, p, nil, nil)
	p.script = replay(provider.Event{Kind: provider.EvFinal, Text: followUpAnswer})

	out, err := r.Steer(context.Background(), first.State.RunID, "Did you test it?", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.RunID != first.State.RunID {
		t.Fatalf("status %q reason %q run %s", out.State.Status, out.State.Reason, out.State.RunID)
	}
	spec := p.spec(1)
	if spec.OutputSchema != nil {
		t.Errorf("the follow-up was held to a schema: %s", spec.OutputSchema)
	}
	if spec.Prompt != conversationPrompt("Did you test it?") {
		t.Errorf("prompt %q", spec.Prompt)
	}
	if spec.Resume != "handle-abc" {
		t.Errorf("resume %q", spec.Resume)
	}
	dir := runDir(t, cfg, out)
	if got := readFile(t, filepath.Join(dir, "answer.md")); got != followUpAnswer+"\n" {
		t.Errorf("answer.md = %q", got)
	}
	if s := out.State.Steers; len(s) != 1 || s[0].Continuation != "resume" {
		t.Errorf("steers %+v", s)
	}
	if n := finalCount(t, dir); n != 2 {
		t.Errorf("events.jsonl holds %d final lines, want both replies", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); !os.IsNotExist(err) {
		t.Errorf("a session follow-up filed a note: %v", err)
	}
}

// TestSteerOnASessionWithAReferenceIsConversation: a session that has a
// bundle is still a session. Its follow-up is not sent the triage steer and
// schema, and files no triage note.
func TestSteerOnASessionWithAReferenceIsConversation(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvFinal, Text: "Yes: the PR dropped the guard."})}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first, err := r.Session(context.Background(), "OMNI-1", Options{Instruction: "Was it the PR?"})
	if err != nil || first.State.Status != store.StatusCompleted {
		t.Fatalf("session: %v %q %q", err, first.State.Status, first.State.Reason)
	}

	p.script = replay(provider.Event{Kind: provider.EvFinal, Text: followUpAnswer})
	out, err := r.Steer(context.Background(), first.State.RunID, "Did you test it?", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	spec := p.spec(1)
	if spec.OutputSchema != nil || spec.Prompt != conversationPrompt("Did you test it?") {
		t.Errorf("schema %v prompt %q", spec.OutputSchema != nil, spec.Prompt)
	}
	dir := runDir(t, cfg, out)
	if got := readFile(t, filepath.Join(dir, "answer.md")); got != followUpAnswer+"\n" {
		t.Errorf("answer.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); !os.IsNotExist(err) {
		t.Errorf("a session follow-up filed a note: %v", err)
	}
	if rows, _ := store.ReadRegister(cfg.Root); len(rows) != 0 {
		t.Errorf("register rows %+v", rows)
	}
	if len(out.State.Notes) != 0 {
		t.Errorf("notes %v", out.State.Notes)
	}
}

// TestSteerAfterTheTriageNoteIsConversation: once a reply-first triage has
// replied and filed its note, a follow-up is answered in chat and the note,
// its filed copy and the register are left exactly as they were.
func TestSteerAfterTheTriageNoteIsConversation(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyThenNote("The export job times out.", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil || outs[0].State.Status != store.StatusCompleted || outs[0].State.NoteWarning != "" {
		t.Fatalf("triage: %v %+v", err, outs)
	}
	first := outs[0]
	dir := runDir(t, cfg, first)
	notePath := filepath.Join(dir, "note.md")
	filedPath := filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md")
	registerPath := filepath.Join(cfg.Root, ".sirdar", "register.jsonl")
	noteBefore, filedBefore, registerBefore := readFile(t, notePath), readFile(t, filedPath), readFile(t, registerPath)

	// Answers in prose whatever it is asked; a schema'd session would
	// file nothing from this, and a note turn would show as a third start.
	p.script = replay(provider.Event{Kind: provider.EvFinal, Text: followUpAnswer})
	out, err := r.Steer(context.Background(), first.State.RunID, "Did you test it?", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.Phase != "" || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q phase %q warning %q", out.State.Status, out.State.Reason, out.State.Phase, out.State.NoteWarning)
	}
	if n := p.startCount(); n != 3 {
		t.Fatalf("started %d sessions, want the reply, the note and one follow-up", n)
	}
	spec := p.spec(2)
	if spec.OutputSchema != nil || spec.Prompt != conversationPrompt("Did you test it?") {
		t.Errorf("schema %v prompt %q", spec.OutputSchema != nil, spec.Prompt)
	}
	if got := readFile(t, filepath.Join(dir, "answer.md")); got != followUpAnswer+"\n" {
		t.Errorf("answer.md = %q", got)
	}
	if readFile(t, notePath) != noteBefore || readFile(t, filedPath) != filedBefore || readFile(t, registerPath) != registerBefore {
		t.Error("a follow-up rewrote the note, its filed copy or the register")
	}
	if strings.Join(out.State.Notes, ",") != strings.Join(first.State.Notes, ",") {
		t.Errorf("notes %v, were %v", out.State.Notes, first.State.Notes)
	}
}

// TestSteerOnANoteOnlyTriageKeepsTheSchema: a run whose answer is its note
// is steered as it always was, for the document again.
func TestSteerOnANoteOnlyTriageKeepsTheSchema(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := triageThen(t, cfg, r, p)

	p.script = replay(finalEvent(steeredDoc))
	if _, err := r.Steer(context.Background(), first.State.RunID, "Re-check it", SteerOptions{}); err != nil {
		t.Fatal(err)
	}
	spec := p.spec(1)
	if !bytes.Equal(spec.OutputSchema, prompt.TriageSchema) {
		t.Error("a note-only steer lost the triage schema")
	}
	if spec.Prompt != steerPrompt("Re-check it", store.KindTriage) {
		t.Errorf("prompt %q", spec.Prompt)
	}
}

// TestPrimedSteerOnASessionCarriesTheEarlierReply: a provider that cannot
// resume opens a fresh session with the original prompt, the reply it is
// standing in for, and the follow-up.
func TestPrimedSteerOnASessionCarriesTheEarlierReply(t *testing.T) {
	cfg, stub, first := runSessionWithoutAReference(t)
	r := newRunner(cfg, steerable{stubProvider: stub, c: provider.ContinuePrimed}, nil, nil)
	stub.script = replay(provider.Event{Kind: provider.EvFinal, Text: followUpAnswer})

	out, err := r.Steer(context.Background(), first.State.RunID, "Did you test it?", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	spec := stub.spec(1)
	if spec.Resume != "" || spec.OutputSchema != nil {
		t.Errorf("resume %q schema %v", spec.Resume, spec.OutputSchema != nil)
	}
	for _, want := range []string{strings.TrimSpace(stub.spec(0).Prompt), "## Your earlier reply", sessionReply, conversationPrompt("Did you test it?")} {
		if !strings.Contains(spec.Prompt, want) {
			t.Errorf("primed prompt lacks %q", firstLine(want))
		}
	}
	if out.State.Steers[0].Continuation != "primed" {
		t.Errorf("steers %+v", out.State.Steers)
	}
}

// TestSteerQueuedDuringTheNoteTurnIsHeld: a follow-up typed while Update note
// files the note is not sent into that turn; it waits for the run to settle.
func TestSteerQueuedDuringTheNoteTurnIsHeld(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyThenNote("The export job times out.", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.SteerPoll = 5 * time.Millisecond
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil || outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("triage: %v %+v", err, outs)
	}

	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if _, err := (store.Run{Dir: spec.RunDir}).QueueSteer("Also check the refunds table", "", time.Now()); err != nil {
			t.Error(err)
		}
		// Long enough for the inbox poll to take it while the turn is open.
		time.Sleep(30 * time.Millisecond)
		s.emit(finalEvent(triageDoc))
	}
	out, err := r.UpdateNote(context.Background(), outs[0].State.RunID, UpdateNoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	if sent := p.session(2).sentTexts(); len(sent) != 0 {
		t.Fatalf("the note turn was sent %q", sent)
	}
	if q := out.State.QueuedSteers; len(q) != 1 || q[0].Status != store.SteerHeld {
		t.Fatalf("queued steers %+v, want one held", q)
	}
}

// TestSteerOnATriageWithoutItsBundleIsRefused: only a session may have no
// bundle. A triage whose ticket.json is gone is refused as it always was,
// rather than steered with an empty ticket.
func TestSteerOnATriageWithoutItsBundleIsRefused(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyThenNote("The export job times out.", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil || outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("triage: %v %+v", err, outs)
	}
	dir := runDir(t, cfg, outs[0])
	if err := os.Remove(filepath.Join(dir, "bundle", "ticket.json")); err != nil {
		t.Fatal(err)
	}
	starts := p.startCount()
	if _, err := r.Steer(context.Background(), outs[0].State.RunID, "Did you test it?", SteerOptions{}); err == nil || !strings.Contains(err.Error(), "read bundle") {
		t.Fatalf("Steer = %v, want the missing bundle", err)
	}
	if p.startCount() != starts {
		t.Error("a refused steer started a session")
	}
}

// TestSteerBeforeTheReplyRepliesThenFilesTheNote: a reply-first triage that
// blocked in its reply turn has neither reply nor note. A steer carries on
// the way the run began: the follow-up as conversation, the reply into
// answer.md, then the note turn.
func TestSteerBeforeTheReplyRepliesThenFilesTheNote(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvQuestion, Text: "Which database?"})}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	first := outs[0]
	if first.State.Status != store.StatusBlocked || !first.State.ReplyFirst {
		t.Fatalf("status %q replyFirst %v", first.State.Status, first.State.ReplyFirst)
	}
	dir := runDir(t, cfg, first)

	const reply = "The orders database drops rows past 500."
	p.script = replyThenNote(reply, finalEvent(triageDoc))
	out, err := r.Steer(context.Background(), first.State.RunID, "The orders database", SteerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" || out.State.Phase != "" {
		t.Fatalf("status %q reason %q warning %q phase %q", out.State.Status, out.State.Reason, out.State.NoteWarning, out.State.Phase)
	}
	if n := p.startCount(); n != 3 {
		t.Fatalf("started %d sessions, want the blocked one, the reply and the note", n)
	}
	if spec := p.spec(1); spec.OutputSchema != nil || spec.Prompt != conversationPrompt("The orders database") {
		t.Errorf("reply turn: schema %v prompt %q", spec.OutputSchema != nil, spec.Prompt)
	}
	if !bytes.Equal(p.spec(2).OutputSchema, prompt.TriageSchema) {
		t.Error("the note turn was not held to the triage schema")
	}
	if got := readFile(t, filepath.Join(dir, "answer.md")); got != reply+"\n" {
		t.Errorf("answer.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); err != nil {
		t.Error(err)
	}
	if rows, _ := store.ReadRegister(cfg.Root); len(rows) != 1 {
		t.Errorf("register rows %d, want 1", len(rows))
	}
}
