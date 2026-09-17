package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// liveService returns a started service over root with a watcher slow
// enough that anything arriving promptly came from the sink and not from a
// poll, plus the subscriber to read it on.
func liveService(t *testing.T, root string, interval time.Duration) (*Service, <-chan Event) {
	t.Helper()
	svc := New(newRegistry(t, root), nil, Options{Interval: interval})
	events, unsubscribe := svc.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		svc.Stop()
		unsubscribe()
	})
	svc.Start(ctx)
	return svc, events
}

func TestSinkPublishesEachLineAsItIsWritten(t *testing.T) {
	root := newWorkspace(t)
	// A poll this slow cannot be what delivered a line inside 200ms.
	svc, events := liveService(t, root, 5*time.Second)

	const key, runID = "SBX-1", "20260917T090000Z-aaaa"
	writeState(t, root, key, runID, store.StatusRunning)
	dir := filepath.Join(root, ".sirdar", "runs", key, runID)

	log, err := (store.Run{Dir: dir}).OpenEventLog()
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	for i := 1; i <= 3; i++ {
		if err := svc.Append(dir, func() ([]byte, error) {
			return log.AppendLine("tool_started", map[string]string{"tool": "Bash"})
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	for want := 1; want <= 3; want++ {
		select {
		case e := <-events:
			if e.Kind != KindRunEvent {
				t.Fatalf("event %d is %s, want %s", want, e.Kind, KindRunEvent)
			}
			if e.Index != want {
				t.Fatalf("index %d, want %d", e.Index, want)
			}
			if e.RunID != runID || e.WorkspaceID != WorkspaceID(root) {
				t.Fatalf("event %d names run %q in workspace %q", want, e.RunID, e.WorkspaceID)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("line %d did not reach the subscriber", want)
		}
	}
}

func TestWatcherDoesNotRepublishWhatTheSinkDelivered(t *testing.T) {
	root := newWorkspace(t)
	svc, events := liveService(t, root, 20*time.Millisecond)

	const key, runID = "SBX-1", "20260917T091500Z-bbbb"
	writeState(t, root, key, runID, store.StatusRunning)
	dir := filepath.Join(root, ".sirdar", "runs", key, runID)

	log, err := (store.Run{Dir: dir}).OpenEventLog()
	if err != nil {
		t.Fatal(err)
	}

	const lines = 8
	for i := 0; i < lines; i++ {
		if err := svc.Append(dir, func() ([]byte, error) {
			return log.AppendLine("assistant_text", map[string]string{"text": "hello"})
		}); err != nil {
			t.Fatal(err)
		}
		// Spread the writes across several polls, so a watcher that meant
		// to duplicate them had every chance to.
		time.Sleep(5 * time.Millisecond)
	}
	svc.Done(dir)
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	writeState(t, root, key, runID, store.StatusCompleted)

	// Long enough for the poll, the flush grace and the sweep that
	// releases the cursor.
	deadline := time.After(3 * time.Second)
	seen := map[int]int{}
	count := 0
collect:
	for {
		select {
		case e := <-events:
			if e.Kind != KindRunEvent || e.RunID != runID {
				continue
			}
			seen[e.Index]++
			count++
		case <-deadline:
			break collect
		}
	}

	if count != lines {
		t.Fatalf("subscriber saw %d run events, want %d (indexes %v)", count, lines, seen)
	}
	for i := 1; i <= lines; i++ {
		if seen[i] != 1 {
			t.Fatalf("index %d delivered %d times, want once (all: %v)", i, seen[i], seen)
		}
	}
}

func TestSinkCursorCarriesOnWhenTheLogIsReopened(t *testing.T) {
	root := newWorkspace(t)
	svc, events := liveService(t, root, 5*time.Second)

	const key, runID = "SBX-1", "20260917T092000Z-cccc"
	writeState(t, root, key, runID, store.StatusRunning)
	dir := filepath.Join(root, ".sirdar", "runs", key, runID)

	write := func() {
		log, err := (store.Run{Dir: dir}).OpenEventLog()
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Append(dir, func() ([]byte, error) {
			return log.AppendLine("assistant_text", map[string]string{"text": "hello"})
		}); err != nil {
			t.Fatal(err)
		}
		svc.Done(dir)
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
	}

	write()
	// The cursor is dropped as if the sweep had reclaimed it, which is
	// what a steer hours later would find.
	svc.live.release(dir)
	write()

	var indexes []int
	for len(indexes) < 2 {
		select {
		case e := <-events:
			if e.Kind == KindRunEvent && e.RunID == runID {
				indexes = append(indexes, e.Index)
			}
		case <-time.After(time.Second):
			t.Fatalf("only saw indexes %v", indexes)
		}
	}
	if indexes[0] != 1 || indexes[1] != 2 {
		t.Fatalf("indexes %v, want 1 then 2: a reopened log must not renumber the transcript", indexes)
	}
}
