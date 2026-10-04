package run

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// failingNote is a note turn that answers with an invalid note twice, so the
// schema retry is spent and the note is not filed.
func failingNote(spec provider.SessionSpec, s *stubSession) {
	if len(spec.OutputSchema) == 0 {
		replay(provider.Event{Kind: provider.EvFinal, Text: "The export job times out."})(spec, s)
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
}

// replyFirstTriage runs a reply-first triage of OMNI-1 under script and
// returns its outcome.
func replyFirstTriage(t *testing.T, cfg *config.Config, r *Runner, p *stubProvider, script func(provider.SessionSpec, *stubSession)) Outcome {
	t.Helper()
	p.script = script
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("triage ended %q: %s", outs[0].State.Status, outs[0].State.Reason)
	}
	return outs[0]
}

func TestUpdateNoteFilesTheNoteAgain(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := replyFirstTriage(t, cfg, r, p, failingNote)
	if !strings.HasPrefix(first.State.NoteWarning, "note not filed:") {
		t.Fatalf("the first note was filed: warning %q", first.State.NoteWarning)
	}

	p.script = replay(finalEvent(triageDoc))
	out, err := r.UpdateNote(context.Background(), first.State.RunID, UpdateNoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" || out.State.Phase != "" {
		t.Fatalf("status %q reason %q warning %q phase %q", out.State.Status, out.State.Reason, out.State.NoteWarning, out.State.Phase)
	}
	dir := runDir(t, cfg, out)
	if _, err := os.Stat(filepath.Join(dir, "note.md")); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("register rows %d, want 1", len(rows))
	}
	last := p.spec(p.startCount() - 1)
	if last.Resume != "handle-abc" {
		t.Errorf("resume %q", last.Resume)
	}
	if !bytes.Equal(last.OutputSchema, prompt.TriageSchema) {
		t.Error("the note turn was not held to the triage schema")
	}
	if last.Prompt != r.notePrompt(&prepared{kind: store.KindTriage}) {
		t.Errorf("prompt:\n%s", last.Prompt)
	}

	// The note turn's lines, the one that opens it included, carry the
	// phase; the reply's answer.md is untouched.
	kinds, phases := eventPhases(t, dir)
	at := -1
	for i, line := range eventKinds(t, dir) {
		if line == "system:"+filingAgain {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no %q line: %v", filingAgain, eventKinds(t, dir))
	}
	for i := at; i < len(kinds); i++ {
		if phases[i] != "note" {
			t.Errorf("line %d (%s) phase %q, want note", i, kinds[i], phases[i])
		}
	}
	if got := readFile(t, filepath.Join(dir, "answer.md")); got != "The export job times out.\n" {
		t.Errorf("answer.md = %q", got)
	}
}

func TestUpdateNoteReplacesTheNote(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := replyFirstTriage(t, cfg, r, p, replyThenNote("The export job times out.", finalEvent(triageDoc)))

	retitled := strings.Replace(triageDoc, `"title": "Export fails for large orders"`, `"title": "Export drops the last page"`, 1)
	p.script = replay(finalEvent(retitled))
	out, err := r.UpdateNote(context.Background(), first.State.RunID, UpdateNoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
		t.Fatalf("status %q reason %q warning %q", out.State.Status, out.State.Reason, out.State.NoteWarning)
	}
	dir := runDir(t, cfg, out)
	if got := readFile(t, filepath.Join(dir, "note.md")); !strings.Contains(got, "Export drops the last page") {
		t.Fatalf("note.md was not replaced:\n%s", got)
	}
	if len(out.State.Notes) != 2 {
		t.Fatalf("notes %v, want the run's note and its filed copy", out.State.Notes)
	}
	for _, path := range out.State.Notes {
		if !strings.Contains(readFile(t, path), "Export drops the last page") {
			t.Errorf("%s does not hold the new note", path)
		}
	}
	// The register is a log: the note filed again is a second row for the
	// same run, beside the first, as a steer that changes the note adds one.
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].RunID != first.State.RunID || rows[1].RunID != first.State.RunID ||
		rows[0].Title != "Export fails for large orders" || rows[1].Title != "Export drops the last page" {
		t.Errorf("register rows %+v, want the first note's and the new one's", rows)
	}
}

// refusedNote checks that UpdateNote refused with ErrNoNote and left the
// run's state as it was.
func refusedNote(t *testing.T, r *Runner, p *stubProvider, dir, runID string) {
	t.Helper()
	before := readFile(t, filepath.Join(dir, "state.json"))
	starts := p.startCount()
	_, err := r.UpdateNote(context.Background(), runID, UpdateNoteOptions{})
	if !errors.Is(err, ErrNoNote) {
		t.Fatalf("UpdateNote = %v, want ErrNoNote", err)
	}
	if readFile(t, filepath.Join(dir, "state.json")) != before {
		t.Error("a refused Update note touched state.json")
	}
	if p.startCount() != starts {
		t.Error("a refused Update note started a session")
	}
}

func TestUpdateNoteRefusesASessionRun(t *testing.T) {
	cfg, p, out := runSessionWithoutAReference(t)
	refusedNote(t, newRunner(cfg, p, nil, nil), p, runDir(t, cfg, out), out.State.RunID)
}

func TestUpdateNoteRefusesARunWithoutAReply(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := triageThen(t, cfg, r, p)
	refusedNote(t, r, p, runDir(t, cfg, first), first.State.RunID)
}

func TestUpdateNoteRefusesALiveRun(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := replyFirstTriage(t, cfg, r, p, replyThenNote("The export job times out.", finalEvent(triageDoc)))
	rn, state, err := store.Open(cfg.Root, first.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = store.StatusRunning
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}
	refusedNote(t, r, p, rn.Dir, first.State.RunID)
}

// TestUpdateNoteThatCannotStartLeavesTheRunCompleted: a note turn whose
// session never starts is a warning on a run that already answered, not a
// failed run. With no note filed before, the warning is the run's
// NoteWarning.
func TestUpdateNoteThatCannotStartLeavesTheRunCompleted(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	first := replyFirstTriage(t, cfg, r, p, failingNote)

	r.Provider = failingStart{p}
	out, err := r.UpdateNote(context.Background(), first.State.RunID, UpdateNoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted || out.State.Reason != "" || out.State.Phase != "" {
		t.Fatalf("status %q reason %q phase %q", out.State.Status, out.State.Reason, out.State.Phase)
	}
	if !strings.HasPrefix(out.State.NoteWarning, "note not filed: ") || !strings.Contains(out.State.NoteWarning, "no session today") {
		t.Errorf("note warning %q", out.State.NoteWarning)
	}
}

// TestFailedUpdateNoteKeepsTheNoteItWasReplacing: an Update note that files
// nothing, because its note fails validation twice or its session never
// starts, leaves the run naming the note still on disk, with the warning it
// had, and says the update failed among the run's warnings.
func TestFailedUpdateNoteKeepsTheNoteItWasReplacing(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(r *Runner, p *stubProvider)
		want string
	}{
		{"validation", func(_ *Runner, p *stubProvider) { p.script = failingNote }, "schema validation failed twice"},
		{"start", func(r *Runner, p *stubProvider) { r.Provider = failingStart{p} }, "no session today"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newWorkspace(t)
			p := &stubProvider{}
			r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
			first := replyFirstTriage(t, cfg, r, p, replyThenNote("The export job times out.", finalEvent(triageDoc)))
			dir := runDir(t, cfg, first)
			noteBefore := readFile(t, filepath.Join(dir, "note.md"))

			tc.fail(r, p)
			out, err := r.UpdateNote(context.Background(), first.State.RunID, UpdateNoteOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if out.State.Status != store.StatusCompleted || out.State.NoteWarning != "" {
				t.Fatalf("status %q warning %q", out.State.Status, out.State.NoteWarning)
			}
			if strings.Join(out.State.Notes, ",") != strings.Join(first.State.Notes, ",") || len(out.State.Notes) != 2 {
				t.Errorf("notes %v, want the note still on disk %v", out.State.Notes, first.State.Notes)
			}
			var warned bool
			for _, w := range out.State.Warnings {
				warned = warned || (strings.HasPrefix(w, "note not updated: ") && strings.Contains(w, tc.want))
			}
			if !warned {
				t.Errorf("warnings %q, want the failed update named", out.State.Warnings)
			}
			if readFile(t, filepath.Join(dir, "note.md")) != noteBefore {
				t.Error("a failed Update note changed note.md")
			}
		})
	}
}

// failingStart is a provider whose sessions never start.
type failingStart struct{ *stubProvider }

func (failingStart) Start(context.Context, provider.SessionSpec) (provider.Session, error) {
	return nil, errors.New("no session today")
}
