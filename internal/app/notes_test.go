package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// settledSession writes a completed session run with a reply straight into
// the store, the way a session that answered leaves it.
func settledSession(t *testing.T, root string) store.State {
	t.Helper()
	at := time.Date(2026, 10, 4, 10, 15, 0, 0, time.UTC)
	key := "ASK-20261004-why-is-the-refund-for"
	rn, err := store.Create(root, key, at)
	if err != nil {
		t.Fatal(err)
	}
	state := store.State{
		RunID: filepath.Base(rn.Dir), Key: key, Kind: store.KindSession, Status: store.StatusCompleted,
		StartedAt: at, UpdatedAt: at, ReplyFirst: true, Access: store.AccessReadOnly,
		Instruction: "Why is the refund for order 1234 stuck in pending?",
	}
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rn.Dir, "answer.md"), []byte("The ledger skips zero rows.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestUpdateNoteRefusesASessionRun(t *testing.T) {
	root := newWorkspace(t)
	p := &stubProvider{script: replay()}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	state := settledSession(t, root)

	job, err := svc.UpdateNote(context.Background(), WorkspaceID(root), state.RunID)
	if !errors.Is(err, ErrNoteRefused) || !strings.Contains(err.Error(), "only triage and RCA runs file a note") {
		t.Fatalf("UpdateNote = %q, %v; want ErrNoteRefused", job, err)
	}
	if job != "" || len(svc.Jobs()) != 0 {
		t.Fatalf("a refused Update note started job %q", job)
	}
}

// TestUpdateNoteRefusesARunOverItsCaps: a replied triage that has spent its USD
// budget, or was made under another provider, is refused before a job exists,
// with the reason, since the note turn would be refused inside the job anyway.
func TestUpdateNoteRefusesARunOverItsCaps(t *testing.T) {
	root := newWorkspace(t)
	p := &stubProvider{script: replay()}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	state := settledSession(t, root)
	state.Kind = store.KindTriage
	state.Provider = "claude"
	state.Usage.CostUSD = 5.10
	rn, _, err := store.Open(root, state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}

	job, err := svc.UpdateNote(context.Background(), WorkspaceID(root), state.RunID)
	if !errors.Is(err, ErrNoteRefused) || !strings.Contains(err.Error(), "has spent $5.10 of its $5.00 budget") {
		t.Fatalf("UpdateNote = %q, %v; want ErrNoteRefused with the budget reason", job, err)
	}
	if job != "" || len(svc.Jobs()) != 0 {
		t.Fatalf("a refused Update note started job %q", job)
	}

	state.Usage.CostUSD = 0.40
	state.Provider = "codex"
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}
	job, err = svc.UpdateNote(context.Background(), WorkspaceID(root), state.RunID)
	if !errors.Is(err, ErrNoteRefused) || !strings.Contains(err.Error(), "made under provider codex") {
		t.Fatalf("UpdateNote = %q, %v; want ErrNoteRefused with the provider reason", job, err)
	}
	if job != "" || len(svc.Jobs()) != 0 {
		t.Fatalf("a refused Update note started job %q", job)
	}
}

// TestUpdateNoteRunsAJob: a reply-first triage files its note again in a
// job, and the run ends completed on its own id.
func TestUpdateNoteRunsAJob(t *testing.T) {
	root := newWorkspace(t)
	p := &stubProvider{script: replyThenNote([]provider.Event{{Kind: provider.EvFinal, Text: "The export job times out."}}, finalEvent(triageDoc))}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()
	wsID := WorkspaceID(root)
	if _, err := svc.StartTriage(context.Background(), wsID, []string{"OMNI-1"}, TriageOptions{}); err != nil {
		t.Fatal(err)
	}
	first := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
	runID := first.Outcomes[0].RunID

	p.script = replay(finalEvent(triageDoc))
	job, err := svc.UpdateNote(context.Background(), wsID, runID)
	if err != nil || job == "" {
		t.Fatalf("UpdateNote = %q, %v", job, err)
	}
	done := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished && e.JobID == job })
	if len(done.Outcomes) != 1 || done.Outcomes[0].RunID != runID || done.Outcomes[0].Status != string(store.StatusCompleted) {
		t.Fatalf("outcomes %+v", done.Outcomes)
	}
	detail, err := svc.Run(wsID, runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.NoteWarning != "" || detail.Phase != "" {
		t.Fatalf("note warning %q phase %q", detail.NoteWarning, detail.Phase)
	}
}

func TestSaveNoteWritesThePath(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{}, stubHelpdesk{}))
	state := settledSession(t, root)

	path, err := svc.SaveNote(WorkspaceID(root), state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) || filepath.Base(filepath.Dir(path)) != "Sessions" ||
		!strings.HasPrefix(filepath.Base(path), state.Key+" ") {
		t.Fatalf("path %s", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "---\nkey: "+state.Key+"\n") || !strings.HasSuffix(string(body), "The ledger skips zero rows.\n") {
		t.Fatalf("note:\n%s", body)
	}

	// A triage run is refused as a conflict, not written.
	triage := state
	triage.Kind = store.KindTriage
	rn, _, err := store.Open(root, state.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rn.WriteState(triage); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveNote(WorkspaceID(root), state.RunID); !errors.Is(err, ErrNoteRefused) {
		t.Fatalf("SaveNote on a triage run = %v", err)
	}
}

func TestSaveNoteUnknownRun(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{}, stubHelpdesk{}))
	if _, err := svc.SaveNote(WorkspaceID(root), "20260101T000000Z-0000"); !errors.Is(err, ErrNoSuchRun) {
		t.Fatalf("SaveNote = %v, want ErrNoSuchRun", err)
	}
}
