package claude

import (
	"context"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// runAsk drives the ask fixture under policy and returns the permission
// event and the control_response line the CLI was sent.
func runAsk(t *testing.T, policy *provider.PermissionPolicy) (provider.Event, string) {
	t.Helper()
	spec := fakeSpec(t, "testdata/script-ask.jsonl")
	policy.Root = spec.Cwd
	spec.Policy = policy
	s, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var perm provider.Event
	for ev := range s.Events() {
		if ev.Kind == provider.EvPermission {
			perm = ev
		}
	}
	res, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range res.StderrTail {
		if strings.Contains(line, `"request_id":"r1"`) {
			return perm, line
		}
	}
	t.Fatalf("no control_response for r1: %v", res.StderrTail)
	return perm, ""
}

// TestAskMapsToInterruptingDeny: a question goes back to Claude Code as a
// deny that interrupts the turn, and the event carries the question.
func TestAskMapsToInterruptingDeny(t *testing.T) {
	ev, line := runAsk(t, &provider.PermissionPolicy{BashAllow: []string{"cat *"}, Ask: true})
	if ev.Decision != "ask" || ev.Ask == nil {
		t.Fatalf("event %+v", ev)
	}
	if ev.Ask.Kind != provider.AskBash || ev.Ask.Summary != "rg -n refund src" || ev.Ask.Patterns[0] != "rg *" {
		t.Errorf("ask %+v", *ev.Ask)
	}
	for _, want := range []string{`"behavior":"deny"`, `"interrupt":true`, "waiting on the operator"} {
		if !strings.Contains(line, want) {
			t.Errorf("control_response %s lacks %s", line, want)
		}
	}
}

// TestGrantMapsToAllow: once the operator has allowed the call, the same
// request is answered allow with the input passed through.
func TestGrantMapsToAllow(t *testing.T) {
	ask := provider.PermissionAsk{Kind: provider.AskBash, Tool: "Bash", Summary: "rg -n refund src", Patterns: []string{"rg *"}}
	for _, verdict := range []string{provider.VerdictAllow, provider.VerdictAllowRun} {
		ev, line := runAsk(t, &provider.PermissionPolicy{
			BashAllow: []string{"cat *"}, Ask: true,
			Grants: provider.NewGrants(provider.GrantFor(ask, verdict, "")),
		})
		if ev.Decision != "allow" || ev.Ask != nil {
			t.Errorf("%s: event %+v", verdict, ev)
		}
		if !strings.Contains(line, `"behavior":"allow"`) || !strings.Contains(line, `"command":"rg -n refund src"`) {
			t.Errorf("%s: control_response %s", verdict, line)
		}
	}
}

// TestDenyGrantMapsToDenyWithReason: a call the operator denied is refused
// with their reason, and does not interrupt or ask again.
func TestDenyGrantMapsToDenyWithReason(t *testing.T) {
	ask := provider.PermissionAsk{Kind: provider.AskBash, Tool: "Bash", Summary: "rg x", Patterns: []string{"rg *"}}
	ev, line := runAsk(t, &provider.PermissionPolicy{
		BashAllow: []string{"cat *"}, Ask: true,
		Grants: provider.NewGrants(provider.GrantFor(ask, provider.VerdictDeny, "use the index instead")),
	})
	if ev.Decision != "deny" || ev.Ask != nil {
		t.Errorf("event %+v", ev)
	}
	if !strings.Contains(line, "use the index instead") || strings.Contains(line, "interrupt") {
		t.Errorf("control_response %s", line)
	}
}
