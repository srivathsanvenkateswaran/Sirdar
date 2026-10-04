package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

func TestStartSessionNeedsAnInstruction(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, nil, nil))

	started, err := svc.StartSession(context.Background(), WorkspaceID(root), SessionOptions{Instruction: "   "})
	if !errors.Is(err, ErrBadSession) || !strings.Contains(err.Error(), "type what you want done") {
		t.Fatalf("err = %v, want ErrBadSession with the instruction message", err)
	}
	if started.JobID != "" || len(svc.Jobs()) != 0 {
		t.Fatalf("a bad request started a job: %+v", started)
	}
}

func TestStartSessionWithoutAReference(t *testing.T) {
	root := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvFinal, Text: "The ledger skips zero rows."})}
	svc := newService(t, root, stubBuilder(p, nil, nil))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()
	wsID := WorkspaceID(root)

	started, err := svc.StartSession(context.Background(), wsID, SessionOptions{Instruction: "Why is the refund for order 1234 stuck in pending?"})
	if err != nil {
		t.Fatal(err)
	}
	if started.JobID == "" || started.RunID == "" || !strings.HasPrefix(started.Key, "ASK-") {
		t.Fatalf("started %+v", started)
	}

	done := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished && e.JobID == started.JobID })
	if len(done.Outcomes) != 1 || done.Outcomes[0].RunID != started.RunID || done.Outcomes[0].Status != string(store.StatusCompleted) {
		t.Fatalf("outcomes %+v", done.Outcomes)
	}

	dir := filepath.Join(root, ".sirdar", "runs", started.Key, started.RunID)
	if _, err := os.Stat(filepath.Join(dir, "answer.md")); err != nil {
		t.Fatalf("answer.md: %v", err)
	}
	_, state, err := store.Open(root, started.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != store.KindSession {
		t.Fatalf("kind %q", state.Kind)
	}
}

func TestStartSessionWithAPlainKey(t *testing.T) {
	root := newWorkspace(t)
	p := &stubProvider{script: replay(provider.Event{Kind: provider.EvFinal, Text: "Yes."})}
	tr := stubTracker{}
	svc := newService(t, root, stubBuilder(p, tr, stubHelpdesk{}))

	started, err := svc.StartSession(context.Background(), WorkspaceID(root), SessionOptions{
		Instruction: "Was it the PR?", Reference: "omni-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Key != "OMNI-1" {
		t.Fatalf("key %q, want OMNI-1", started.Key)
	}
}

func TestStartSessionUnresolvedReference(t *testing.T) {
	root := newWorkspace(t)
	bare := linkedHelpdesk{hd: ticket.HelpdeskTicket{ID: "99999", Subject: "refund is late"}}
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{}, bare))

	started, err := svc.StartSession(context.Background(), WorkspaceID(root), SessionOptions{
		Instruction: "Why is the refund stuck?", Reference: "#99999",
	})
	if !errors.Is(err, ErrBadSession) {
		t.Fatalf("err = %v, want ErrBadSession", err)
	}
	if started.JobID != "" || len(svc.Jobs()) != 0 {
		t.Fatalf("an unresolved reference started a job: %+v", started)
	}
	if _, err := os.Stat(filepath.Join(root, ".sirdar", "runs")); !os.IsNotExist(err) {
		t.Errorf(".sirdar/runs exists (err %v)", err)
	}
}

func TestStartSessionRefusesWorktreeForNow(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, nil, nil))

	_, err := svc.StartSession(context.Background(), WorkspaceID(root), SessionOptions{
		Instruction: "Fix the bug", Access: "worktree",
	})
	if !errors.Is(err, ErrBadSession) || !strings.Contains(err.Error(), "worktree sessions are not available yet") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartSessionRefusesAnUnknownAccess(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, nil, nil))

	_, err := svc.StartSession(context.Background(), WorkspaceID(root), SessionOptions{
		Instruction: "Fix the bug", Access: "read-write",
	})
	if !errors.Is(err, ErrBadSession) || !strings.Contains(err.Error(), "access must be read-only or worktree") {
		t.Fatalf("err = %v", err)
	}
}
