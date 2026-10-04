package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

var saveAt = time.Date(2026, 9, 10, 9, 30, 0, 0, time.UTC)

func TestSaveSessionNote(t *testing.T) {
	cfg, _, out := runSessionWithoutAReference(t)
	if out.State.Key != "ASK-20260910-why-is-the-refund-for" {
		t.Fatalf("key %q", out.State.Key)
	}
	path, err := SaveSessionNote(cfg, out.State.RunID, saveAt)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cfg.Root, "notes", "Sessions", "ASK-20260910-why-is-the-refund-for why-is-the-refund-for-order-1234-stuck-in-pending.md")
	if path != want {
		t.Fatalf("path %s, want %s", path, want)
	}
	body := readFile(t, path)
	head := "---\nkey: ASK-20260910-why-is-the-refund-for\nrun: " + out.State.RunID +
		"\ncreated: 2026-09-10T09:00:00Z\ninstruction: " + sessionInstruction + "\n---\n\n"
	if !strings.HasPrefix(body, head) {
		t.Errorf("note does not open with the frontmatter:\n%s", body)
	}
	answer := readFile(t, filepath.Join(runDir(t, cfg, out), "answer.md"))
	if body != head+answer {
		t.Errorf("note body after the frontmatter is not answer.md:\n%s", body)
	}

	_, state, err := store.Open(cfg.Root, out.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Notes) != 1 || state.Notes[0] != path {
		t.Fatalf("notes %v", state.Notes)
	}

	// Saving again overwrites the file and names it once.
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again, err := SaveSessionNote(cfg, out.State.RunID, saveAt); err != nil || again != path {
		t.Fatalf("second save: %s %v", again, err)
	}
	if readFile(t, path) != body {
		t.Error("the second save did not overwrite the file")
	}
	if _, state, _ = store.Open(cfg.Root, out.State.RunID); len(state.Notes) != 1 {
		t.Errorf("notes after a second save %v", state.Notes)
	}
}

func TestSaveSessionNoteHonoursThePattern(t *testing.T) {
	cfg, _, out := runSessionWithoutAReference(t)
	cfg.Notes.Filenames.Session = "Asks/{key}.md"
	path, err := SaveSessionNote(cfg, out.State.RunID, saveAt)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfg.Root, "notes", "Asks", out.State.Key+".md"); path != want {
		t.Fatalf("path %s, want %s", path, want)
	}
}

func TestSaveSessionNoteQuotesAnInstructionWithAColon(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvFinal, Text: sessionReply})}
	r := newRunner(cfg, p, nil, nil)
	out, err := r.Session(context.Background(), "", Options{Instruction: "Why: the refund", NoBundle: true})
	if err != nil {
		t.Fatal(err)
	}
	path, err := SaveSessionNote(cfg, out.State.RunID, saveAt)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(readFile(t, path), "---\n", 3)
	if len(parts) != 3 {
		t.Fatalf("no frontmatter in %s", path)
	}
	var meta struct {
		Instruction string `yaml:"instruction"`
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
		t.Fatalf("frontmatter does not parse: %v\n%s", err, parts[1])
	}
	if meta.Instruction != "Why: the refund" {
		t.Errorf("instruction read back as %q", meta.Instruction)
	}
}

func TestSaveSessionNoteRefusesATriageRun(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	out := replyFirstTriage(t, cfg, r, p, replyThenNote("The export job times out.", finalEvent(triageDoc)))
	if _, err := SaveSessionNote(cfg, out.State.RunID, saveAt); !errors.Is(err, ErrNoNote) {
		t.Fatalf("SaveSessionNote = %v, want ErrNoNote", err)
	}
}

func TestSaveSessionNoteRefusesALiveRun(t *testing.T) {
	cfg, _, out := runSessionWithoutAReference(t)
	rn, state, err := store.Open(cfg.Root, out.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = store.StatusRunning
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveSessionNote(cfg, out.State.RunID, saveAt); !errors.Is(err, ErrNoNote) || !strings.Contains(err.Error(), "still working") {
		t.Fatalf("SaveSessionNote = %v", err)
	}
}
