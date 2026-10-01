package qwen

import (
	"encoding/json"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

type hookAnswer struct {
	Continue           *bool  `json:"continue"`
	StopReason         string `json:"stopReason"`
	HookSpecificOutput struct {
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func askHook(t *testing.T, policy *provider.PermissionPolicy) (hookAnswer, provider.Event) {
	t.Helper()
	s := newTestSession("tok")
	s.policy = policy
	rec := &recorder{}
	s.decide(rec, hookPost(t, s, `{"tool_name":"run_shell_command","tool_input":{"command":"rg -n refund src"},"tool_call_id":"c1"}`))
	var out hookAnswer
	if err := json.Unmarshal(rec.body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.body.String(), err)
	}
	select {
	case ev := <-s.events:
		return out, ev
	default:
		t.Fatal("no permission event")
	}
	return out, provider.Event{}
}

// TestAskMapsToStoppingDeny: the hook answers a question with a deny whose
// reason says the operator is being asked, and continue false so the CLI
// stops the turn.
func TestAskMapsToStoppingDeny(t *testing.T) {
	out, ev := askHook(t, &provider.PermissionPolicy{BashAllow: []string{"cat *"}, Root: t.TempDir(), Ask: true})
	if out.HookSpecificOutput.PermissionDecision != "deny" || out.HookSpecificOutput.PermissionDecisionReason != provider.AskPending {
		t.Errorf("hook answer %+v", out)
	}
	if out.Continue == nil || *out.Continue {
		t.Errorf("continue = %v, want false", out.Continue)
	}
	if ev.Decision != "ask" || ev.Ask == nil || ev.Ask.Kind != provider.AskBash || ev.Ask.Summary != "rg -n refund src" {
		t.Errorf("event %+v", ev)
	}
}

// TestGrantMapsToHookAllow: an allowed call is answered allow, and a denied
// one deny with the operator's reason — neither stops the turn.
func TestGrantMapsToHookAllow(t *testing.T) {
	ask := provider.PermissionAsk{Kind: provider.AskBash, Tool: "Bash", Summary: "rg -n refund src", Patterns: []string{"rg *"}}
	out, ev := askHook(t, &provider.PermissionPolicy{BashAllow: []string{"cat *"}, Root: t.TempDir(), Ask: true,
		Grants: provider.NewGrants(provider.GrantFor(ask, provider.VerdictAllowRun, ""))})
	if out.HookSpecificOutput.PermissionDecision != "allow" || out.Continue != nil || ev.Decision != "allow" {
		t.Errorf("allow: answer %+v, event %+v", out, ev)
	}

	out, ev = askHook(t, &provider.PermissionPolicy{BashAllow: []string{"cat *"}, Root: t.TempDir(), Ask: true,
		Grants: provider.NewGrants(provider.GrantFor(ask, provider.VerdictDeny, "use the index"))})
	if out.HookSpecificOutput.PermissionDecision != "deny" || out.Continue != nil || ev.Decision != "deny" {
		t.Errorf("deny: answer %+v, event %+v", out, ev)
	}
	if want := "Sirdar policy: the operator denied rg for this run: use the index"; out.HookSpecificOutput.PermissionDecisionReason != want {
		t.Errorf("deny reason %q", out.HookSpecificOutput.PermissionDecisionReason)
	}
}
