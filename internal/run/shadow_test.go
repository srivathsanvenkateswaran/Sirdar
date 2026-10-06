package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/notify"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// countingNotifier records how many times a run told the channel anything.
type countingNotifier struct {
	mu    sync.Mutex
	calls int
}

func (n *countingNotifier) Notify(context.Context, notify.Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	return nil
}

func (n *countingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

// TestShadowTriageFilesNothing: a shadow triage runs the same reply and
// note turns an attended one does, and leaves its note in the run
// directory, but files nothing into the notes directory, appends no
// register row and sends no notification.
func TestShadowTriageFilesNothing(t *testing.T) {
	cfg := newWorkspace(t)
	notesDir := cfg.ExpandPath(cfg.Notes.Dir)
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &stubProvider{script: replyThenNote("reply", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	n := &countingNotifier{}
	r.Notifier = n

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{Shadow: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	dir := runDir(t, cfg, out)
	for _, name := range []string{"note.md", "result.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	entries, err := os.ReadDir(notesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the notes directory has %d entries, want none", len(entries))
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, ".sirdar", "register.jsonl")); !os.IsNotExist(err) {
		t.Errorf("register.jsonl exists (err %v)", err)
	}
	if c := n.count(); c != 0 {
		t.Errorf("the notifier was called %d times, want 0", c)
	}
	st, err := (store.Run{Dir: dir}).ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Shadow || !st.ReplyFirst {
		t.Errorf("state shadow=%v replyFirst=%v, want both true", st.Shadow, st.ReplyFirst)
	}
}

// TestShadowResumeDoesNotNotify: the shadow flag lives on the run's state,
// so a shadow run resumed after an interrupt stays silent when it finishes.
func TestShadowResumeDoesNotNotify(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replyThenNote("reply", finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{Shadow: true})
	if err != nil {
		t.Fatal(err)
	}
	rn, st, err := store.Open(cfg.Root, outs[0].State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	st.Status, st.Reason, st.Handle = store.StatusBlocked, "interrupted", "handle-abc"
	if err := rn.WriteState(st); err != nil {
		t.Fatal(err)
	}

	n := &countingNotifier{}
	r.Notifier = n
	p.script = replyThenNote("reply", finalEvent(triageDoc))
	resumed, err := r.Resume(context.Background(), st.RunID, ResumeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State.Status != store.StatusCompleted {
		t.Fatalf("resumed status %q reason %q", resumed.State.Status, resumed.State.Reason)
	}
	if c := n.count(); c != 0 {
		t.Errorf("the notifier was called %d times, want 0", c)
	}
}

func TestRefuseSteerShadow(t *testing.T) {
	err := RefuseSteer(120, 25, 8, store.State{RunID: "r1", Status: store.StatusCompleted, Shadow: true})
	if err == nil || !strings.Contains(err.Error(), "is a shadow run") {
		t.Fatalf("RefuseSteer = %v, want a shadow-run refusal", err)
	}
}
