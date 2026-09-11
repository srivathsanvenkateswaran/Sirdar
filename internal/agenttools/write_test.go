package agenttools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTools returns the two writing tools by name for a workspace rooted
// at dir.
func writeTools(t *testing.T, dir string) (Tool, Tool) {
	t.Helper()
	set := WriteSet(Options{Root: dir})
	if len(set) != 2 {
		t.Fatalf("WriteSet returned %d tools, want 2", len(set))
	}
	byName := map[string]Tool{}
	for _, tool := range set {
		byName[tool.Spec().Name] = tool
	}
	w, ok := byName["write_file"]
	if !ok {
		t.Fatal("WriteSet has no write_file")
	}
	e, ok := byName["edit_file"]
	if !ok {
		t.Fatal("WriteSet has no edit_file")
	}
	return w, e
}

func callJSON(t *testing.T, tool Tool, args map[string]string) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Call(t.Context(), json.RawMessage(raw))
}

func TestWriteFileWritesInsideTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	write, _ := writeTools(t, dir)

	if _, err := callJSON(t, write, map[string]string{"path": "pkg/thing.go", "content": "package pkg\n"}); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "pkg", "thing.go"))
	if err != nil {
		t.Fatalf("the file was not created: %v", err)
	}
	if string(got) != "package pkg\n" {
		t.Fatalf("content %q", got)
	}
}

// TestWriteFileIsConfinedToTheRoot is the security property of the whole
// set: a path argument comes from the model, so every way out of the
// workspace has to be refused — the obvious climb, an absolute path, and
// the one that is easy to forget, a symlink inside the workspace pointing
// out of it.
func TestWriteFileIsConfinedToTheRoot(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	write, edit := writeTools(t, dir)
	for _, path := range []string{
		"../victim.txt",
		"../../victim.txt",
		victim,
		"escape/victim.txt",
	} {
		if _, err := callJSON(t, write, map[string]string{"path": path, "content": "owned\n"}); err == nil {
			t.Errorf("write_file accepted %q", path)
		}
		if _, err := callJSON(t, edit, map[string]string{"path": path, "old": "original", "new": "owned"}); err == nil {
			t.Errorf("edit_file accepted %q", path)
		}
	}

	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original\n" {
		t.Fatalf("a file outside the workspace was modified: %q", got)
	}
}

func TestEditFileReplacesOneOccurrence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "csv.go")
	if err := os.WriteFile(path, []byte("rows := make([][]string, 0)\nreturn rows\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, edit := writeTools(t, dir)

	if _, err := callJSON(t, edit, map[string]string{
		"path": "csv.go",
		"old":  "rows := make([][]string, 0)",
		"new":  "enc := csv.NewWriter(w)",
	}); err != nil {
		t.Fatalf("edit_file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "enc := csv.NewWriter(w)\nreturn rows\n"; string(got) != want {
		t.Fatalf("content %q, want %q", got, want)
	}
}

// TestEditFileRefusesAnAmbiguousMatch: an edit that could land in two
// places is an edit nobody can review, so it is refused rather than
// applied to the first hit.
func TestEditFileRefusesAnAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.go")
	if err := os.WriteFile(path, []byte("x := 1\ny := 2\nx := 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, edit := writeTools(t, dir)

	_, err := callJSON(t, edit, map[string]string{"path": "dup.go", "old": "x := 1", "new": "x := 2"})
	if err == nil {
		t.Fatal("edit_file applied an ambiguous edit")
	}
	if !strings.Contains(err.Error(), "2 times") {
		t.Errorf("the error does not say how many matches there were: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "x := 1\ny := 2\nx := 1\n" {
		t.Fatalf("the file was modified anyway: %q", got)
	}
}

func TestEditFileRefusesTextThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, edit := writeTools(t, dir)

	if _, err := callJSON(t, edit, map[string]string{"path": "a.go", "old": "package b", "new": "package c"}); err == nil {
		t.Fatal("edit_file accepted text that is not in the file")
	}
}

func TestWriteToolsRejectEmptyArguments(t *testing.T) {
	dir := t.TempDir()
	write, edit := writeTools(t, dir)

	if _, err := callJSON(t, write, map[string]string{"content": "x"}); err == nil {
		t.Error("write_file accepted a missing path")
	}
	if _, err := callJSON(t, edit, map[string]string{"path": "a.go", "old": "", "new": "x"}); err == nil {
		t.Error("edit_file accepted an empty old string")
	}
}

// TestReadOnlySetHasNoWrites: the read-only set is what a triage run gets,
// and it must stay read-only however this file grows.
func TestReadOnlySetHasNoWrites(t *testing.T) {
	for _, tool := range ReadOnlySet(Options{Root: t.TempDir()}) {
		switch tool.Spec().Name {
		case "write_file", "edit_file":
			t.Errorf("%s is in the read-only set", tool.Spec().Name)
		}
	}
}
