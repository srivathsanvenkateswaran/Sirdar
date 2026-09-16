package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// writeRun puts one run on disk under root with the given status and
// answers with its directory. The state carries a filed note when
// filedNote is not empty.
func writeRunAt(t *testing.T, root, key, runID string, status store.Status, filedNote string) string {
	t.Helper()
	rn, err := store.CreateID(root, key, runID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	st := store.State{
		RunID: runID, Key: key, Kind: store.KindTriage, Status: status,
		Provider: "claude", Model: "sonnet", StartedAt: now, UpdatedAt: now,
	}
	if filedNote != "" {
		st.Notes = []string{filedNote}
	}
	if err := rn.WriteState(st); err != nil {
		t.Fatal(err)
	}
	return rn.Dir
}

// unstartedService is a service over root whose watcher never runs: these
// tests read and write the run directories themselves.
func unstartedService(t *testing.T, root string) *Service {
	t.Helper()
	return New(newRegistry(t, root), nil, Options{})
}

func TestDeleteRunRemovesTheDirectoryAndSaysSo(t *testing.T) {
	root := newWorkspace(t)
	filed := filepath.Join(t.TempDir(), "OMNI-1-triage.md")
	if err := os.WriteFile(filed, []byte("# Triage OMNI-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeRunAt(t, root, "OMNI-1", "20260910T090000Z-aaaa", store.StatusCompleted, filed)
	svc := unstartedService(t, root)
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	if err := svc.DeleteRun(WorkspaceID(root), "20260910T090000Z-aaaa"); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("run dir still there: %v", err)
	}
	// The filed note is the record and stays.
	if _, err := os.Stat(filed); err != nil {
		t.Fatalf("filed note: %v", err)
	}
	select {
	case e := <-events:
		if e.Kind != KindRunRemoved || e.WorkspaceID != WorkspaceID(root) || e.RunID != "20260910T090000Z-aaaa" {
			t.Fatalf("event %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no run.removed event")
	}
	runs, err := svc.Runs(WorkspaceID(root), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs after delete: %+v", runs)
	}
}

func TestDeleteRunRefusesALiveRunAndUnknownIDs(t *testing.T) {
	root := newWorkspace(t)
	for _, status := range []store.Status{store.StatusPreparing, store.StatusRunning} {
		id := "20260910T090000Z-" + string(status)[:4]
		dir := writeRunAt(t, root, "OMNI-1", id, status, "")
		svc := unstartedService(t, root)
		err := svc.DeleteRun(WorkspaceID(root), id)
		if !errors.Is(err, ErrRunLive) {
			t.Fatalf("%s: err = %v, want ErrRunLive", status, err)
		}
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("%s: run dir was touched: %v", status, err)
		}
	}
	// A blocked run has no session behind it and can go.
	writeRunAt(t, root, "OMNI-2", "20260910T090000Z-bbbb", store.StatusBlocked, "")
	svc := unstartedService(t, root)
	if err := svc.DeleteRun(WorkspaceID(root), "20260910T090000Z-bbbb"); err != nil {
		t.Fatalf("blocked: %v", err)
	}
	if err := svc.DeleteRun(WorkspaceID(root), "nope"); !errors.Is(err, ErrNoSuchRun) {
		t.Fatalf("unknown run: %v", err)
	}
	if err := svc.DeleteRun("nope", "20260910T090000Z-bbbb"); !errors.Is(err, ErrNoSuchWorkspace) {
		t.Fatalf("unknown workspace: %v", err)
	}
	if err := svc.DeleteRun(WorkspaceID(root), "../escape"); !errors.Is(err, ErrNoSuchRun) {
		t.Fatalf("path-like id: %v", err)
	}
}

func TestSearchFindsAnswersAndNotes(t *testing.T) {
	root := newWorkspace(t)
	filed := filepath.Join(t.TempDir(), "OMNI-1-triage.md")
	if err := os.WriteFile(filed, []byte("# Triage OMNI-1\n\nThe Statement Export\ntimes out on page two.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeRunAt(t, root, "OMNI-1", "20260910T090000Z-aaaa", store.StatusCompleted, filed)
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{"classification":"bug","summary":"pool exhausted during export"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second run with nothing matching.
	writeRunAt(t, root, "OMNI-2", "20260910T100000Z-bbbb", store.StatusFailed, "")
	svc := unstartedService(t, root)

	hits, err := svc.Search(WorkspaceID(root), "EXPORT")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits %+v", hits)
	}
	answer, note := hits[0], hits[1]
	if answer.Source != "answer" || answer.RunID != "20260910T090000Z-aaaa" || answer.Key != "OMNI-1" || answer.Kind != "triage" || answer.Status != "completed" {
		t.Fatalf("answer hit %+v", answer)
	}
	if !strings.Contains(answer.Excerpt, "pool exhausted during export") {
		t.Fatalf("answer excerpt %q", answer.Excerpt)
	}
	if note.Source != "note" || note.Path != filed {
		t.Fatalf("note hit %+v", note)
	}
	// Newlines collapse so the excerpt reads as one line.
	if !strings.Contains(note.Excerpt, "The Statement Export times out") {
		t.Fatalf("note excerpt %q", note.Excerpt)
	}

	none, err := svc.Search(WorkspaceID(root), "   ")
	if err != nil || len(none) != 0 {
		t.Fatalf("blank query: %v %+v", err, none)
	}
	if _, err := svc.Search("nope", "export"); !errors.Is(err, ErrNoSuchWorkspace) {
		t.Fatalf("unknown workspace: %v", err)
	}
}

func TestSearchCapsAtTheLimit(t *testing.T) {
	root := newWorkspace(t)
	for i := 0; i < SearchLimit+5; i++ {
		id := time.Date(2026, 9, 1, 0, i, 0, 0, time.UTC).Format("20060102T150405Z") + "-cccc"
		dir := writeRunAt(t, root, "OMNI-9", id, store.StatusCompleted, "")
		if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("needle here"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := unstartedService(t, root)
	hits, err := svc.Search(WorkspaceID(root), "needle")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != SearchLimit {
		t.Fatalf("hits = %d, want %d", len(hits), SearchLimit)
	}
}

func TestExcerptOfCentresAndTrims(t *testing.T) {
	long := strings.Repeat("a ", 200) + "Needle" + strings.Repeat(" b", 200)
	got, ok := excerptOf(long, "needle")
	if !ok {
		t.Fatal("no match")
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("edges not marked: %q", got)
	}
	if !strings.Contains(got, "Needle") {
		t.Fatalf("match missing: %q", got)
	}
	if n := len([]rune(got)); n > excerptLen+2 {
		t.Fatalf("excerpt %d runes", n)
	}
	// A short text is answered whole, with no ellipsis.
	if got, _ := excerptOf("Short  text\nwith needle", "needle"); got != "Short text with needle" {
		t.Fatalf("short: %q", got)
	}
	// Multibyte text: the cut lands on a rune boundary.
	arabic := strings.Repeat("م", 300) + "hit" + strings.Repeat("ن", 300)
	got, ok = excerptOf(arabic, "hit")
	if !ok || !strings.Contains(got, "hit") || !strings.ContainsRune(got, 'م') {
		t.Fatalf("arabic: %q %v", got, ok)
	}
	if _, ok := excerptOf("nothing", "zzz"); ok {
		t.Fatal("false match")
	}
}
