package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

func TestValidateID(t *testing.T) {
	bad := []string{
		"",
		".",
		"..",
		"../..",
		"a/b",
		`a\b`,
		"/etc/passwd",
		"*",
		"20260910T090000Z-*",
		"?",
		"[a-z]",
		"OMNI-1\n",
		"OMNI\x00-1",
	}
	for _, s := range bad {
		if err := validateID(s); err == nil {
			t.Errorf("validateID(%q) allowed it", s)
		} else if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("validateID(%q) = %v, want ErrInvalidArgument", s, err)
		}
	}

	good := []string{
		"OMNI-1",
		"20260910T090000Z-aaaa",
		"deadbeef1234",
		"a.b",
		"...",
		"ticket_42",
		"مفتاح",
	}
	for _, s := range good {
		if err := validateID(s); err != nil {
			t.Errorf("validateID(%q) = %v, want nil", s, err)
		}
	}
}

// TestServiceRejectsTraversalIDs walks every method that takes an id
// straight from an HTTP route or query string. ServeMux unescapes each
// segment before the handler sees it, so `..%2F..` arrives as `../..` and
// `%2A` as `*`; neither may reach internal/store, where they would join
// into a path or match as a glob.
func TestServiceRejectsTraversalIDs(t *testing.T) {
	root := newWorkspace(t)
	writeState(t, root, "OMNI-1", "20260910T090000Z-eeee", store.StatusCompleted)
	svc := New(newRegistry(t, root), stubBuilder(&stubProvider{script: replay()}, stubTracker{}, stubHelpdesk{}), Options{})
	ws := WorkspaceID(root)
	ctx := context.Background()

	hostile := []string{"", "..", "../../etc", "a/b", "*", "20260910T090000Z-*"}
	for _, id := range hostile {
		checks := map[string]error{
			"RemoveWorkspace": svc.RemoveWorkspace(id),
			"Runs(workspace)": errOf2(svc.Runs(id, "OMNI-1")),
			"Run(workspace)":  errOfRun(svc.Run(id, "20260910T090000Z-eeee")),
			"Run(runId)":      errOfRun(svc.Run(ws, id)),
			"Note(runId)":     errOfString(svc.Note(ws, id, "triage")),
			"Prompt(runId)":   errOfString(svc.Prompt(ws, id)),
			"Register":        errOfRows(svc.Register(id)),
			"StartRCA(key)":   mustErr(svc.StartRCA(ctx, ws, id, RCAOptions{})),
			"Resume(runId)":   mustErr(svc.Resume(ctx, ws, id, "")),
		}
		if _, _, err := svc.Events(ws, id, 0); true {
			checks["Events(runId)"] = err
		}
		if _, err := svc.Queue(ctx, id, QueueFilter{}); true {
			checks["Queue(workspace)"] = err
		}
		if _, err := svc.Doctor(ctx, id); true {
			checks["Doctor(workspace)"] = err
		}
		if _, err := svc.StartTriage(ctx, ws, []string{id}, TriageOptions{}); true {
			checks["StartTriage(key)"] = err
		}
		// An empty key means "every key", so only a non-empty hostile
		// one is an error there.
		if id != "" {
			checks["Runs(key)"] = errOf2(svc.Runs(ws, id))
		}

		for what, err := range checks {
			if err == nil {
				t.Errorf("%s accepted %q", what, id)
				continue
			}
			if !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("%s(%q) = %v, want ErrInvalidArgument", what, id, err)
			}
			// The HTTP layer maps the not-found sentinels to 404; an
			// id that cannot name anything must land there rather than
			// on a 500.
			if !errors.Is(err, ErrNoSuchWorkspace) && !errors.Is(err, ErrNoSuchRun) {
				t.Errorf("%s(%q) = %v, which httpapi would answer 500", what, id, err)
			}
		}
	}

	// Nothing above created a run directory outside the workspace, and the
	// legitimate ids still work.
	if _, err := svc.Run(ws, "20260910T090000Z-eeee"); err != nil {
		t.Fatalf("a valid run id was rejected: %v", err)
	}
	if runs, err := svc.Runs(ws, "OMNI-1"); err != nil || len(runs) != 1 {
		t.Fatalf("a valid key was rejected: %+v %v", runs, err)
	}
}

func errOf2(_ []RunSummary, err error) error     { return err }
func errOfRun(_ RunDetail, err error) error      { return err }
func errOfString(_ string, err error) error      { return err }
func errOfRows(_ []RegisterRow, err error) error { return err }

// writeRun writes a run of a given kind with the note files a run of that
// kind would have produced.
func writeRun(t *testing.T, root, key, runID string, kind store.Kind, notes map[string]string) {
	t.Helper()
	dir := filepath.Join(root, ".sirdar", "runs", key, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := store.State{
		RunID: runID, Key: key, Kind: kind, Status: store.StatusCompleted,
		Provider: "claude", StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := (store.Run{Dir: dir}).WriteState(state); err != nil {
		t.Fatal(err)
	}
	for name, body := range notes {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestNoteKindMatchesTheRun keeps note.md from being served as whichever
// note the caller asked for: it means the triage note on a triage run and
// the rca note on an rca run, and a resolution only ever comes from
// note-resolution.md.
func TestNoteKindMatchesTheRun(t *testing.T) {
	root := newWorkspace(t)
	writeRun(t, root, "OMNI-1", "run-triage", store.KindTriage, map[string]string{"note.md": "the triage note"})
	writeRun(t, root, "OMNI-1", "run-rca", store.KindRCA, map[string]string{
		"note.md":            "the rca note",
		"note-resolution.md": "the resolution draft",
	})
	svc := New(newRegistry(t, root), nil, Options{})
	ws := WorkspaceID(root)

	for _, tc := range []struct {
		runID, kind, want string
	}{
		{"run-triage", "triage", "the triage note"},
		{"run-triage", "", "the triage note"},
		{"run-rca", "rca", "the rca note"},
		{"run-rca", "resolution", "the resolution draft"},
		{"run-rca", "", "the rca note"},
	} {
		got, err := svc.Note(ws, tc.runID, tc.kind)
		if err != nil {
			t.Errorf("Note(%s, %q): %v", tc.runID, tc.kind, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Note(%s, %q) = %q, want %q", tc.runID, tc.kind, got, tc.want)
		}
	}

	for _, tc := range []struct{ runID, kind string }{
		{"run-triage", "rca"},
		{"run-triage", "resolution"},
		{"run-rca", "triage"},
		{"run-triage", "sideways"},
	} {
		got, err := svc.Note(ws, tc.runID, tc.kind)
		if err == nil {
			t.Errorf("Note(%s, %q) returned %q, want an error", tc.runID, tc.kind, got)
			continue
		}
		if !errors.Is(err, ErrNoSuchRun) {
			t.Errorf("Note(%s, %q) = %v, which httpapi would answer 500", tc.runID, tc.kind, err)
		}
	}
}

// TestStopCancelsRunningJobs proves a shell shutting down does not orphan
// an agent subprocess.
func TestStopCancelsRunningJobs(t *testing.T) {
	root := newWorkspace(t)
	started := make(chan struct{}, 1)
	p := &stubProvider{script: block(started)}
	svc := New(newRegistry(t, root), stubBuilder(p, stubTracker{}, stubHelpdesk{}), Options{Interval: 20 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	if _, err := svc.StartTriage(context.Background(), WorkspaceID(root), []string{"OMNI-1"}, TriageOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent session never started")
	}

	begin := time.Now()
	svc.Stop()
	if elapsed := time.Since(begin); elapsed >= stopTimeout {
		t.Fatalf("Stop took %s, which means it gave up waiting rather than cancelling", elapsed)
	}

	if !p.session(t, 0).wasCancelled() {
		t.Fatal("Stop left the agent session running")
	}
	if jobs := svc.Jobs(); len(jobs) != 0 {
		t.Fatalf("jobs still registered after Stop: %v", jobs)
	}
	runs, err := svc.Runs(WorkspaceID(root), "OMNI-1")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %+v err %v", runs, err)
	}
	if runs[0].Status != string(store.StatusBlocked) {
		t.Fatalf("the cancelled run ended %q, want blocked", runs[0].Status)
	}
	if !strings.Contains(runs[0].Reason, "interrupt") {
		t.Fatalf("reason %q", runs[0].Reason)
	}

	// Stopping twice is harmless.
	svc.Stop()
}
