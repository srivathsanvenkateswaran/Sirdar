package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/gather"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// gatherRunWith writes a session run and, when index is non-nil, its
// bundle/context/index.json. It answers with the service, the workspace
// root, its id and the run id, so a test can call Service.Run or
// Service.Gathered on it, or reopen the run directly through the store.
func gatherRunWith(t *testing.T, index []gather.Entry) (svc *Service, root, ws, runID string) {
	t.Helper()
	root = newWorkspace(t)
	at := time.Date(2026, 10, 4, 10, 15, 0, 0, time.UTC)
	rn, err := store.Create(root, "OMNI-1", at)
	if err != nil {
		t.Fatal(err)
	}
	state := store.State{
		RunID: filepath.Base(rn.Dir), Key: "OMNI-1", Kind: store.KindTriage,
		Status: store.StatusCompleted, StartedAt: at, UpdatedAt: at,
	}
	if err := rn.WriteState(state); err != nil {
		t.Fatal(err)
	}
	if index != nil {
		contextDir := filepath.Join(rn.Dir, "bundle", "context")
		if err := os.MkdirAll(contextDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body, err := json.MarshalIndent(index, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(contextDir, gather.IndexFile), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc = New(newRegistry(t, root), BuildDeps, Options{})
	return svc, root, WorkspaceID(root), state.RunID
}

func TestRunDetailCarriesGathered(t *testing.T) {
	index := []gather.Entry{
		{Name: "pr-oxo-1", File: "context/pr-oxo-1.md", Kind: "pr", Status: "ok",
			Call: "gh pr view 1", Bytes: 120, TookMs: 80, Summary: "Fix the export"},
		{Name: "company", Kind: "command", Status: "ok", Call: "company -id x", Bytes: 40, TookMs: 10},
		// A denied row is on the manifest gather wrote, but nobody reading
		// gathered context has use for a door that stayed shut.
		{Name: "secret-step", Kind: "command", Status: gather.StatusDenied, Reason: "write word"},
	}
	svc, _, ws, runID := gatherRunWith(t, index)

	detail, err := svc.Run(ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Gathered) != 2 {
		t.Fatalf("Gathered = %+v, want 2 entries (the denied row dropped)", detail.Gathered)
	}
	if detail.Gathered[0].Name != "pr-oxo-1" || detail.Gathered[0].File != "context/pr-oxo-1.md" ||
		detail.Gathered[0].Kind != "pr" || detail.Gathered[0].Bytes != 120 || detail.Gathered[0].TookMs != 80 {
		t.Errorf("Gathered[0] = %+v", detail.Gathered[0])
	}
	if detail.Gathered[1].Name != "company" {
		t.Errorf("Gathered[1] = %+v", detail.Gathered[1])
	}

	got, err := svc.Gathered(ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Service.Gathered = %+v, want 2 entries", got)
	}
}

func TestGatheredIsEmptyRatherThanNilWithNoIndex(t *testing.T) {
	svc, _, ws, runID := gatherRunWith(t, nil)

	got, err := svc.Gathered(ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("Gathered with no index = %#v, want an empty slice", got)
	}

	detail, err := svc.Run(ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Gathered != nil {
		t.Errorf("RunDetail.Gathered with no index = %#v, want nil (omitted on the wire)", detail.Gathered)
	}
}

func writeResultJSON(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunDetailCarriesReproduction(t *testing.T) {
	svc, root, ws, runID := gatherRunWith(t, nil)
	rn, _, err := store.Open(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	writeResultJSON(t, rn.Dir, `{"reproduction":{"attempted":true,"outcome":"reproduced","where":"https://staging.oxo.example (tenant dama)","steps":[],"evidence":[]}}`)

	detail, err := svc.Run(ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Reproduction == nil || detail.Reproduction.Outcome != "reproduced" ||
		detail.Reproduction.Where != "https://staging.oxo.example (tenant dama)" {
		t.Fatalf("Reproduction = %+v", detail.Reproduction)
	}

	// A null reproduction — the ordinary case for a run with no browser or
	// no staging hosts — carries nothing.
	writeResultJSON(t, rn.Dir, `{"reproduction":null}`)
	detail, err = svc.Run(ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Reproduction != nil {
		t.Errorf("Reproduction on a null result = %+v, want nil", detail.Reproduction)
	}

	// An rca document has no reproduction key at all, the same as if
	// result.json were missing: neither is an error.
	writeResultJSON(t, rn.Dir, `{"classification":"bug","summary":"pool exhausted during export"}`)
	detail, err = svc.Run(ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Reproduction != nil {
		t.Errorf("Reproduction on an rca document = %+v, want nil", detail.Reproduction)
	}
}

// TestContextFileIsServed proves Service.AttachmentFile serves a gathered
// file under the same symlink and traversal guard as an attachment:
// context/ and attachments/ are the bundle's only two servable
// directories, and everything else is refused with ErrNoSuchRun.
func TestContextFileIsServed(t *testing.T) {
	svc, root, ws, runID := gatherRunWith(t, nil)
	rn, _, err := store.Open(root, runID)
	if err != nil {
		t.Fatal(err)
	}
	contextDir := filepath.Join(rn.Dir, "bundle", "context")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, "a.md"), []byte("gathered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rn.Dir, "bundle", "ticket.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	full, _, err := svc.AttachmentFile(ws, runID, "context/a.md")
	if err != nil {
		t.Fatalf("AttachmentFile: %v", err)
	}
	if filepath.Base(full) != "a.md" {
		t.Fatalf("resolved to %q", full)
	}

	for name, path := range map[string]string{
		"parent traversal": "context/../ticket.json",
		"absolute path":    "/etc/passwd",
		"the directory":    "context",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := svc.AttachmentFile(ws, runID, path); !errors.Is(err, ErrNoSuchRun) {
				t.Fatalf("AttachmentFile(%q) = %v, want ErrNoSuchRun", path, err)
			}
		})
	}

	// A symlink pointing out of the bundle is refused the same way, on
	// every platform that can make one.
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("not yours"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(contextDir, "leak.txt")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, _, err := svc.AttachmentFile(ws, runID, "context/leak.txt"); !errors.Is(err, ErrNoSuchRun) {
		t.Fatalf("a symlink pointing out of the bundle was served: %v", err)
	}
}
