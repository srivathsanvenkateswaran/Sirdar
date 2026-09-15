package app

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidProvider(t *testing.T) {
	for name, want := range map[string]bool{
		"":        true, // the workspace's own
		"claude":  true,
		"codex":   true,
		"openai":  true,
		"acp":     true,
		"qwen":    true,
		"Claude":  false,
		"cluade":  false,
		"gemini":  false,
		" claude": false,
	} {
		if got := ValidProvider(name); got != want {
			t.Errorf("ValidProvider(%q) = %v, want %v", name, got, want)
		}
	}
}

// The check lives here rather than in internal/httpapi because the Wails
// bridge calls this Service directly: a typo from the desktop shell has to
// be refused by the same rule as one from the browser, and before a job is
// started rather than by the agent failing to launch.
func TestEveryStartRefusesAnUnknownProvider(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, stubHelpdesk{}))
	ws := WorkspaceID(root)
	ctx := context.Background()

	starts := map[string]func() error{
		"triage": func() error {
			_, err := svc.StartTriage(ctx, ws, []string{"OMNI-1"}, TriageOptions{Provider: "cluade"})
			return err
		},
		"rca": func() error {
			_, err := svc.StartRCA(ctx, ws, "OMNI-1", RCAOptions{Provider: "cluade"})
			return err
		},
		"fix": func() error {
			_, err := svc.StartFix(ctx, ws, "OMNI-1", FixOptions{Provider: "cluade"})
			return err
		},
		"eval": func() error {
			_, err := svc.StartEval(ctx, ws, []string{"OMNI-1"}, EvalOptions{Provider: "cluade"})
			return err
		},
	}
	for name, start := range starts {
		t.Run(name, func(t *testing.T) {
			err := start()
			if err == nil {
				t.Fatal("an unknown provider started a job")
			}
			if !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("error %v is not an ErrInvalidArgument", err)
			}
			// The reader has to be able to see what they should have
			// typed, not just that they were wrong.
			if !strings.Contains(err.Error(), ProviderList) {
				t.Errorf("error %v does not list the providers", err)
			}
		})
	}
}

// The empty override is the workspace's own provider and must keep working.
func TestStartAcceptsTheWorkspacesOwnProvider(t *testing.T) {
	root := newWorkspace(t)
	svc := newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, stubHelpdesk{}))
	if _, err := svc.StartTriage(context.Background(), WorkspaceID(root), []string{"OMNI-1"}, TriageOptions{}); err != nil {
		t.Fatalf("StartTriage with no override: %v", err)
	}
}
