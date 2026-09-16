package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// TestOpenNoteOpensOnlyARecordedNote pins the guard: the only file the
// desktop is handed is one the run itself recorded in its state, and a
// path the run never wrote — however plausible — opens nothing.
func TestOpenNoteOpensOnlyARecordedNote(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "workspace: omni\nprovider: claude\nmodel: sonnet\nnotes:\n  dir: notes\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	var opened []string
	previous := openWithDesktop
	openWithDesktop = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	t.Cleanup(func() { openWithDesktop = previous })

	reg := &app.Registry{Path: filepath.Join(t.TempDir(), "workspaces.json")}
	b := NewBridge(app.New(reg, app.BuildDeps, app.Options{}))
	ws, err := b.AddWorkspace(root)
	if err != nil {
		t.Fatalf("AddWorkspace: %v", err)
	}

	const runID = "20260910T090000Z-aaaa"
	run, err := store.CreateID(ws.Root, "OMNI-1", runID)
	if err != nil {
		t.Fatalf("CreateID: %v", err)
	}
	filed := filepath.Join(ws.Root, "notes", "OMNI-1 triage.md")
	now := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	err = run.WriteState(store.State{
		RunID: runID, Key: "OMNI-1", Kind: store.KindTriage, Status: store.StatusCompleted,
		Provider: "claude", Model: "sonnet", StartedAt: now, UpdatedAt: now,
		Notes: []string{filepath.Join(run.Dir, "note.md"), filed},
	})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	stray := filepath.Join(ws.Root, ".sirdar", "config.yaml")
	if err := b.OpenNote(ws.ID, runID, stray); err == nil {
		t.Fatal("OpenNote on a path the run never recorded: want an error")
	}
	if err := b.OpenNote(ws.ID, "no-such-run", filed); err == nil {
		t.Fatal("OpenNote on an unknown run: want an error")
	}
	if len(opened) != 0 {
		t.Fatalf("a refused OpenNote opened %v", opened)
	}

	if err := b.OpenNote(ws.ID, runID, filed); err != nil {
		t.Fatalf("OpenNote: %v", err)
	}
	if len(opened) != 1 || opened[0] != filed {
		t.Fatalf("opened %v, want [%s]", opened, filed)
	}
}
