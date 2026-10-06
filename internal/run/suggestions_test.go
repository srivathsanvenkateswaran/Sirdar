package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// rcaDocTwoSuggestions is rcaDoc carrying two playbook additions instead
// of one.
var rcaDocTwoSuggestions = strings.Replace(rcaDoc,
	`"playbookSuggestions": [{"playbook":"exports","addition":"Check the row count first.","reason":"It is the fastest signal."}]`,
	`"playbookSuggestions": [{"playbook":"20-exports.md","addition":"- Check the row count first.","reason":"It is the fastest signal."},{"playbook":"30-logs.md","addition":"- Loki keeps 30 days.","reason":"Older logs are gone."}]`,
	1)

// rcaAfterTriage runs a triage and then an rca of OMNI-1 with opts.
func rcaAfterTriage(t *testing.T, opts Options) (Outcome, string) {
	t.Helper()
	if !strings.Contains(rcaDocTwoSuggestions, "30-logs.md") {
		t.Fatal("the rca document was not rewritten with two suggestions")
	}
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	if outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	} else if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("triage status %q", outs[0].State.Status)
	}
	p.script = replay(finalEvent(rcaDocTwoSuggestions))
	out, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{Options: opts, Resolution: "Streamed the export."})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("rca status %q reason %q", out.State.Status, out.State.Reason)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "playbook-suggestions.md")); err != nil {
		t.Fatalf("playbook-suggestions.md: %v", err)
	}
	return out, cfg.Root
}

// TestRCAAppendsSuggestions: each of an rca's playbook additions is queued
// as a pending suggestion, beside the markdown block it always wrote.
func TestRCAAppendsSuggestions(t *testing.T) {
	out, root := rcaAfterTriage(t, Options{})
	lines, err := store.ReadSuggestions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("%d suggestion lines, want 2: %+v", len(lines), lines)
	}
	want := []struct{ playbook, addition, reason string }{
		{"20-exports.md", "- Check the row count first.", "It is the fastest signal."},
		{"30-logs.md", "- Loki keeps 30 days.", "Older logs are gone."},
	}
	for i, l := range lines {
		if l.Ev != "proposed" || l.Source != "rca" || l.RunID != out.State.RunID || l.Key != "OMNI-1" {
			t.Errorf("line %d %+v: want proposed, source rca, this run and key", i, l)
		}
		if l.Playbook != want[i].playbook || l.Addition != want[i].addition || l.Reason != want[i].reason {
			t.Errorf("line %d %+v: want %+v", i, l, want[i])
		}
		if l.ID != store.SuggestionID(l.Playbook, l.Addition) {
			t.Errorf("line %d id %q", i, l.ID)
		}
	}
}

// TestEvalRCAAppendsNoSuggestions: an eval replays a ticket to measure a
// prompt, and what it proposes is not the operator's to act on.
func TestEvalRCAAppendsNoSuggestions(t *testing.T) {
	_, root := rcaAfterTriage(t, Options{Eval: true})
	if _, err := os.Stat(store.SuggestionsPath(root)); !os.IsNotExist(err) {
		t.Fatalf("suggestions file: %v, want none", err)
	}
}
