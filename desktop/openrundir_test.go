package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// TestOpenRunDirOpensTheRunsOwnDirectory pins what the desktop is handed:
// the run directory the service resolved for the id, and nothing for an id
// no workspace holds.
func TestOpenRunDirOpensTheRunsOwnDirectory(t *testing.T) {
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
	now := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	err = run.WriteState(store.State{
		RunID: runID, Key: "OMNI-1", Kind: store.KindTriage, Status: store.StatusCompleted,
		Provider: "claude", Model: "sonnet", StartedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	if err := b.OpenRunDir(ws.ID, "no-such-run"); err == nil {
		t.Fatal("OpenRunDir on an unknown run: want an error")
	}
	if len(opened) != 0 {
		t.Fatalf("opened %v before a valid call", opened)
	}
	if err := b.OpenRunDir(ws.ID, runID); err != nil {
		t.Fatalf("OpenRunDir: %v", err)
	}
	if len(opened) != 1 || opened[0] != run.Dir {
		t.Fatalf("opened %v, want [%s]", opened, run.Dir)
	}
}
