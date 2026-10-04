package app

import (
	"context"
	"errors"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

func TestQuestionOnTheDetail(t *testing.T) {
	ask := &store.PermissionAsk{Kind: "bash", Tool: "Bash", Summary: "rg -n refund src",
		Patterns: []string{"rg *"}, Verdict: "deny", Reason: "Sirdar policy: not permitted by permissions.bash"}

	d := DetailFor(t.TempDir(), store.State{Status: store.StatusBlocked, Reason: "asking: rg -n refund src", Ask: ask}, config.Identity{})
	if d.Question == nil || d.Question.Text != "rg -n refund src" || d.Question.Decision == nil {
		t.Fatalf("question %+v", d.Question)
	}
	if got := *d.Question.Decision; got.Kind != "bash" || got.Summary != "rg -n refund src" || got.Verdict != "deny" || got.Reason == "" || got.Patterns[0] != "rg *" {
		t.Errorf("decision %+v", got)
	}

	d = DetailFor(t.TempDir(), store.State{Status: store.StatusBlocked, Reason: "agent asked: Which database?"}, config.Identity{})
	if d.Question == nil || d.Question.Text != "Which database?" || d.Question.Decision != nil {
		t.Errorf("free-form question %+v", d.Question)
	}

	for _, s := range []store.State{
		{Status: store.StatusBlocked, Reason: "interrupted"},
		{Status: store.StatusCompleted, Ask: ask},
	} {
		if q := DetailFor(t.TempDir(), s, config.Identity{}).Question; q != nil {
			t.Errorf("%s %q: question %+v", s.Status, s.Reason, q)
		}
	}
}

func TestResumeRefusesADecisionNobodyAskedFor(t *testing.T) {
	root := newWorkspace(t)
	writeRunAt(t, root, "OMNI-1", "r-free", store.StatusBlocked, "")
	svc := unstartedService(t, root)
	ctx := context.Background()

	_, err := svc.Resume(ctx, WorkspaceID(root), "r-free", "", "", &PermissionDecision{Verdict: "allow"})
	if !errors.Is(err, ErrNotAsking) {
		t.Errorf("not asking: %v", err)
	}
	_, err = svc.Resume(ctx, WorkspaceID(root), "r-free", "", "", &PermissionDecision{Verdict: "sure"})
	if !errors.Is(err, ErrBadDecision) {
		t.Errorf("bad verdict: %v", err)
	}
}

// A run that blocks while its page is open reaches that page only as a
// run.updated carrying the summary. Without the question on it, the 2026-10-04
// OMNI-3413 rerun showed a plain "Waiting on you" card with no Allow buttons
// until the page was reloaded.
func TestQuestionOnTheSummary(t *testing.T) {
	ask := &store.PermissionAsk{Kind: "bash", Tool: "Bash", Summary: "gh search code x", Patterns: []string{"gh search *"}, Verdict: "deny"}
	s := SummaryOf(store.State{Status: store.StatusBlocked, Reason: "asking: gh search code x", Ask: ask})
	if s.Question == nil || s.Question.Decision == nil || s.Question.Decision.Summary != "gh search code x" {
		t.Fatalf("question %+v", s.Question)
	}
	if q := SummaryOf(store.State{Status: store.StatusRunning}).Question; q != nil {
		t.Errorf("running: question %+v", q)
	}
}
