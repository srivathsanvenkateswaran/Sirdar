package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// writeRunState puts one finished — or unfinished — run on disk, which is what
// the dedupe reads.
func writeRunState(t *testing.T, root, key string, kind store.Kind, status store.Status, at time.Time) {
	t.Helper()
	run, err := store.Create(root, key, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.WriteState(store.State{
		RunID:     filepath.Base(run.Dir),
		Key:       key,
		Kind:      kind,
		Status:    status,
		StartedAt: at,
		UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
}

// withCooldown writes a workspace whose config names one, so the default
// is not what is being tested.
func withCooldown(t *testing.T, cooldown string) string {
	t.Helper()
	root := newWorkspace(t)
	yaml := configYAML + "webhooks:\n  enabled: true\n  cooldown: " + cooldown +
		"\n  sources:\n    generic:\n      secret: env:SIRDAR_TEST_HOOK_SECRET\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTriageIfIdleStartsWhenNothingIsRunning(t *testing.T) {
	root := withCooldown(t, "10m")
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	id, reason, err := svc.TriageIfIdle(context.Background(), WorkspaceID(root), "OMNI-1", TriageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if reason != "" {
		t.Fatalf("skipped an idle key: %s", reason)
	}
	if id == "" {
		t.Fatal("no job id")
	}
	waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
}

// A second delivery while the first run is still going would put two agent
// sessions on one ticket, and the second one's note would overwrite the
// first.
func TestTriageIfIdleSkipsARunningKey(t *testing.T) {
	root := withCooldown(t, "10m")
	writeRunState(t, root, "OMNI-1", store.KindTriage, store.StatusRunning, time.Now().Add(-time.Hour))
	svc := newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, stubHelpdesk{}))

	id, reason, err := svc.TriageIfIdle(context.Background(), WorkspaceID(root), "OMNI-1", TriageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Errorf("started job %s over a running one", id)
	}
	if reason == "" {
		t.Fatal("no reason given")
	}
}

// An RCA of the same key counts too: it is the same run directory.
func TestTriageIfIdleSkipsAPreparingRCA(t *testing.T) {
	root := withCooldown(t, "10m")
	writeRunState(t, root, "OMNI-1", store.KindRCA, store.StatusPreparing, time.Now())
	svc := newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, stubHelpdesk{}))

	_, reason, err := svc.TriageIfIdle(context.Background(), WorkspaceID(root), "OMNI-1", TriageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if reason == "" {
		t.Fatal("a preparing rca run did not hold the key")
	}
}

func TestTriageIfIdleSkipsWithinCooldown(t *testing.T) {
	root := withCooldown(t, "10m")
	writeRunState(t, root, "OMNI-1", store.KindTriage, store.StatusCompleted, time.Now().Add(-2*time.Minute))
	svc := newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, stubHelpdesk{}))

	_, reason, err := svc.TriageIfIdle(context.Background(), WorkspaceID(root), "OMNI-1", TriageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if reason == "" {
		t.Fatal("a key triaged two minutes ago was not on cooldown")
	}
}

// Past the cooldown the same delivery starts a run: the ticket has moved
// on, and a second opinion is the point of the trigger.
func TestTriageIfIdleStartsPastCooldown(t *testing.T) {
	root := withCooldown(t, "1m")
	writeRunState(t, root, "OMNI-1", store.KindTriage, store.StatusCompleted, time.Now().Add(-10*time.Minute))
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	id, reason, err := svc.TriageIfIdle(context.Background(), WorkspaceID(root), "OMNI-1", TriageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if reason != "" || id == "" {
		t.Fatalf("id %q reason %q", id, reason)
	}
	waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
}

// A failed triage holds the key just as a completed one does: without
// that, a workspace whose adapter is down answers every delivery with a
// fresh agent session.
func TestCooldownCoversAFailedTriage(t *testing.T) {
	states := []store.State{{
		Key: "OMNI-1", Kind: store.KindTriage, Status: store.StatusFailed,
		StartedAt: at(1), UpdatedAt: at(1),
	}}
	if busyReason(states, 10*time.Minute, at(2)) == "" {
		t.Fatal("a failed triage did not hold the key")
	}
}

func TestCooldownOfZeroHoldsNothing(t *testing.T) {
	states := []store.State{{
		Key: "OMNI-1", Kind: store.KindTriage, Status: store.StatusCompleted,
		StartedAt: at(1), UpdatedAt: at(1),
	}}
	if reason := busyReason(states, 0, at(1)); reason != "" {
		t.Fatalf("a zero cooldown still skipped: %s", reason)
	}
}

// An RCA that finished a minute ago is not a triage, and does not stop one.
func TestCooldownIgnoresRCARuns(t *testing.T) {
	states := []store.State{{
		Key: "OMNI-1", Kind: store.KindRCA, Status: store.StatusCompleted,
		StartedAt: at(1), UpdatedAt: at(1),
	}}
	if reason := busyReason(states, 10*time.Minute, at(2)); reason != "" {
		t.Fatalf("an rca run held the key: %s", reason)
	}
}

func at(minutes int) time.Time {
	return time.Date(2026, 9, 11, 9, minutes, 0, 0, time.UTC)
}

// Two deliveries for one key that arrive together must start one run. The
// running check and the cooldown are both read off the run directory, and
// the first status a run writes there is written by the job goroutine
// after TriageIfIdle has returned — so without a claim taken before the
// directory is read, every caller in a burst sees an idle key and starts
// its own agent session on the same ticket.
func TestTriageIfIdleStartsOneRunForConcurrentDeliveries(t *testing.T) {
	root := withCooldown(t, "10m")
	// A session that produces nothing and ends only when the service is
	// stopped, so the first run is still in flight while the rest call.
	p := &stubProvider{script: block(make(chan struct{}, 1))}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))

	const callers = 8
	ids := make([]JobID, callers)
	reasons := make([]string, callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup
	release := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-release
			ids[i], reasons[i], errs[i] = svc.TriageIfIdle(
				context.Background(), WorkspaceID(root), "OMNI-1", TriageOptions{})
		}(i)
	}
	close(release)
	wg.Wait()

	runs := 0
	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		switch {
		case ids[i] != "":
			runs++
			if reasons[i] != "" {
				t.Errorf("caller %d started %s and gave a reason too: %s", i, ids[i], reasons[i])
			}
		case reasons[i] == "":
			t.Errorf("caller %d started nothing and said why not", i)
		}
	}
	if runs != 1 {
		t.Fatalf("%d of %d concurrent deliveries started a run, want 1", runs, callers)
	}
}

// The claim covers the start, not the key forever: once the job has ended
// the key is free again, or a workspace would answer one delivery per key
// per process. The cooldown is zero here, so a claim left behind is the
// only thing that could hold the second delivery back.
func TestTriageIfIdleReleasesTheKeyWhenTheJobEnds(t *testing.T) {
	root := withCooldown(t, "0s")
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	svc := newService(t, root, stubBuilder(p, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	_, reason, err := svc.TriageIfIdle(context.Background(), WorkspaceID(root), "OMNI-1", TriageOptions{})
	if err != nil || reason != "" {
		t.Fatalf("first delivery: reason %q err %v", reason, err)
	}
	waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })

	id, reason, err := svc.TriageIfIdle(context.Background(), WorkspaceID(root), "OMNI-1", TriageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatalf("the key was still held after its job finished: %s", reason)
	}
	waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
}

func TestTriageIfIdleRejectsAnUnsafeKey(t *testing.T) {
	root := withCooldown(t, "10m")
	svc := newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, stubHelpdesk{}))
	if _, _, err := svc.TriageIfIdle(context.Background(), WorkspaceID(root), "../../etc", TriageOptions{}); err == nil {
		t.Fatal("a key with a path separator was accepted")
	}
}

// " OMNI-1" and "OMNI-1" are two keys to the run store: a padded one would
// get its own run directory and miss both the running check and the
// cooldown. The receiver trims what it reads out of a payload; a key that
// reaches here still padded is a caller's mistake and is refused.
func TestTriageIfIdleRejectsAPaddedKey(t *testing.T) {
	root := withCooldown(t, "10m")
	writeRunState(t, root, "OMNI-1", store.KindTriage, store.StatusRunning, time.Now())
	svc := newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, stubHelpdesk{}))
	if _, _, err := svc.TriageIfIdle(context.Background(), WorkspaceID(root), " OMNI-1", TriageOptions{}); err == nil {
		t.Fatal("a key padded with a space was accepted, so it would have run beside the running one")
	}
}

func TestHookReceivedPublishes(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, stubHelpdesk{}))
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	svc.HookReceived("jira", "OMNI-1", "started")
	got := waitFor(t, events, "hook.received", func(e Event) bool { return e.Kind == KindHookReceived })
	if got.Source != "jira" || got.Key != "OMNI-1" || got.Outcome != "started" {
		t.Fatalf("event %+v", got)
	}
}

// --- BuildReceiver ----------------------------------------------------

func TestBuildReceiverResolvesSecrets(t *testing.T) {
	t.Setenv("SIRDAR_TEST_HOOK_SECRET", "from-the-environment")
	root := withCooldown(t, "10m")
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := BuildReceiver(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rc == nil || !rc.Has("generic") {
		t.Fatalf("receiver %+v", rc)
	}
}

// A hook endpoint that is up but cannot verify anything is worse than one
// that never started, so an unresolvable secret stops the command.
func TestBuildReceiverFailsOnAMissingSecret(t *testing.T) {
	t.Setenv("SIRDAR_TEST_HOOK_SECRET", "")
	root := withCooldown(t, "10m")
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildReceiver(cfg); err == nil {
		t.Fatal("a missing secret built a receiver anyway")
	}
}

func TestBuildReceiverIsNilWhenDisabled(t *testing.T) {
	cfg, err := config.Load(newWorkspace(t))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := BuildReceiver(cfg)
	if err != nil || rc != nil {
		t.Fatalf("receiver %+v, err %v", rc, err)
	}
}
