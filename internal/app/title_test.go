package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// A run directory as the core leaves it: state.json beside a bundle and,
// once the session has filed, a note under the notes directory.
func fakeRunDir(t *testing.T, bundle string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".sirdar", "runs", "OMNI-1", "20260910T090000Z-aaaa")
	if err := os.MkdirAll(filepath.Join(dir, "bundle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if bundle != "" {
		if err := os.WriteFile(filepath.Join(dir, "bundle", "ticket.json"), []byte(bundle), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func writeNote(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "OMNI-1-triage.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSummaryAtReadsTheTrackerTitle(t *testing.T) {
	dir := fakeRunDir(t, `{"Tracker":{"Key":"OMNI-1","Title":"  Statement export times out "},"Helpdesk":{"ID":"h1","Subject":"Export"}}`)
	got := SummaryAt(dir, store.State{RunID: "20260910T090000Z-aaaa", Key: "OMNI-1"})
	if got.Title != "Statement export times out" {
		t.Fatalf("Title = %q, want the tracker title", got.Title)
	}
}

func TestSummaryAtFallsBackToTheHelpdeskSubject(t *testing.T) {
	dir := fakeRunDir(t, `{"Tracker":null,"Helpdesk":{"ID":"h1","Subject":"Export hangs at page 3"}}`)
	got := SummaryAt(dir, store.State{RunID: "20260910T090000Z-aaaa", Key: "OMNI-1"})
	if got.Title != "Export hangs at page 3" {
		t.Fatalf("Title = %q, want the helpdesk subject", got.Title)
	}
}

func TestSummaryAtFallsBackToTheNote(t *testing.T) {
	dir := fakeRunDir(t, "")
	withKey := writeNote(t, "---\nkey: OMNI-1\ntitle: \"Pool exhausted on export\"\n---\n\n# Something else\n")
	got := SummaryAt(dir, store.State{RunID: "r", Key: "OMNI-1", Notes: []string{withKey}})
	if got.Title != "Pool exhausted on export" {
		t.Fatalf("Title = %q, want the frontmatter title", got.Title)
	}

	// The shipped templates put the title in the first heading, not the
	// frontmatter, so that is what a real note yields.
	heading := writeNote(t, "---\nkey: OMNI-1\ntags: [triage]\n---\n\n# Export pool never released\n\nbody\n")
	got = SummaryAt(dir, store.State{RunID: "r", Key: "OMNI-1", Notes: []string{heading}})
	if got.Title != "Export pool never released" {
		t.Fatalf("Title = %q, want the note's heading", got.Title)
	}
}

func TestSummaryAtWithNothingToReadIsEmpty(t *testing.T) {
	dir := fakeRunDir(t, "")
	got := SummaryAt(dir, store.State{RunID: "r", Key: "OMNI-1", Notes: []string{filepath.Join(dir, "missing.md")}})
	if got.Title != "" {
		t.Fatalf("Title = %q, want empty", got.Title)
	}
	// An unreadable bundle is not a title either.
	broken := fakeRunDir(t, "{not json")
	if got := SummaryAt(broken, store.State{RunID: "r", Key: "OMNI-1"}); got.Title != "" {
		t.Fatalf("Title = %q on a broken bundle, want empty", got.Title)
	}
}

func TestDetailOfCarriesTheTitle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sirdar", "runs", "OMNI-1", "r1", "bundle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ticket.json"), []byte(`{"Tracker":{"Key":"OMNI-1","Title":"Login loops"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := DetailOf(root, store.State{RunID: "r1", Key: "OMNI-1"})
	if d.Title != "Login loops" {
		t.Fatalf("Title = %q, want the bundle's", d.Title)
	}
}
