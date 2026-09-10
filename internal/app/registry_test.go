package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryMissingFileIsEmpty(t *testing.T) {
	reg := &Registry{Path: filepath.Join(t.TempDir(), "workspaces.json")}
	list, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("workspaces: %d, want 0", len(list))
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	root := newWorkspace(t)
	path := filepath.Join(t.TempDir(), "workspaces.json")
	reg := &Registry{Path: path}

	ws, err := reg.Add(root)
	if err != nil {
		t.Fatal(err)
	}
	if ws.ID != WorkspaceID(root) || len(ws.ID) != 12 {
		t.Fatalf("id %q", ws.ID)
	}
	if ws.Root != root {
		t.Fatalf("root %q want %q", ws.Root, root)
	}
	if ws.Name != "test" || ws.Provider != "claude" {
		t.Fatalf("workspace %+v", ws)
	}
	if ws.NotesDir != filepath.Join(root, "notes") {
		t.Fatalf("notesDir %q", ws.NotesDir)
	}
	// Settings shows the billing mode read-only, so it has to come back
	// from the workspace's own config rather than being left blank.
	if ws.Billing != "subscription" {
		t.Fatalf("billing %q", ws.Billing)
	}

	// The file holds roots and nothing else, so a workspace's provider is
	// always whatever its own config currently says.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	if len(onDisk.Workspaces) != 1 || onDisk.Workspaces[0]["root"] != root {
		t.Fatalf("on disk: %s", data)
	}
	if len(onDisk.Workspaces[0]) != 1 {
		t.Fatalf("on disk entry carries more than a root: %s", data)
	}

	// A fresh registry over the same file reads the workspace back.
	again := &Registry{Path: path}
	list, err := again.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0] != ws {
		t.Fatalf("list %+v want %+v", list, ws)
	}

	if err := again.Remove(ws.ID); err != nil {
		t.Fatal(err)
	}
	if list, err = again.List(); err != nil || len(list) != 0 {
		t.Fatalf("after remove: %+v %v", list, err)
	}
	if err := again.Remove(ws.ID); err == nil {
		t.Fatal("removing an unknown id should fail")
	}
}

func TestRegistryRefusesDuplicateAndBadRoot(t *testing.T) {
	root := newWorkspace(t)
	reg := &Registry{Path: filepath.Join(t.TempDir(), "workspaces.json")}
	if _, err := reg.Add(root); err != nil {
		t.Fatal(err)
	}
	_, err := reg.Add(root)
	if err == nil {
		t.Fatal("adding the same root twice should fail")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("error %v", err)
	}
	if list, _ := reg.List(); len(list) != 1 {
		t.Fatalf("duplicate was stored: %+v", list)
	}

	// A directory with no .sirdar/config.yaml is not a workspace.
	if _, err := reg.Add(t.TempDir()); err == nil {
		t.Fatal("adding a non-workspace should fail")
	}
}
