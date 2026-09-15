package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

// TestOpenConfigOpensOnlyARegisteredWorkspace pins the one thing the
// method guards: the path handed to the desktop is a registered
// workspace's own config file, and an id nobody registered opens nothing.
func TestOpenConfigOpensOnlyARegisteredWorkspace(t *testing.T) {
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

	if err := b.OpenConfig("no-such-workspace"); err == nil {
		t.Fatal("OpenConfig on an unknown workspace: want an error")
	}
	if len(opened) != 0 {
		t.Fatalf("an unknown workspace opened %v", opened)
	}

	if err := b.OpenConfig(ws.ID); err != nil {
		t.Fatalf("OpenConfig: %v", err)
	}
	want := filepath.Join(ws.Root, ".sirdar", "config.yaml")
	if len(opened) != 1 || opened[0] != want {
		t.Fatalf("opened %v, want [%s]", opened, want)
	}
}
