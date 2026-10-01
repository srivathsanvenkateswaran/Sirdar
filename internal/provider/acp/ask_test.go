package acp

import (
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

func runAskScript(t *testing.T, grants ...provider.Grant) (provider.Event, string) {
	t.Helper()
	cwd := workspace(t)
	sess := spawn(t, "script-ask.jsonl", cwd, func(spec *provider.SessionSpec) {
		spec.Policy = &provider.PermissionPolicy{
			BashAllow: []string{"cat *"},
			Root:      cwd,
			Ask:       true,
			Grants:    provider.NewGrants(grants...),
		}
	})
	evs := drain(sess)
	res, _ := sess.Wait()
	perms := only(evs, provider.EvPermission)
	if len(perms) != 1 {
		t.Fatalf("permission events = %+v", perms)
	}
	return perms[0], findAnswer(t, res, "session/request_permission")
}

// TestAskMapsToCancelledOutcome: ACP has no field for a reason, so a
// question is answered with the protocol's "cancelled" outcome — no option
// chosen — and the event carries the question.
func TestAskMapsToCancelledOutcome(t *testing.T) {
	ev, answer := runAskScript(t)
	if !strings.Contains(answer, `"outcome":"cancelled"`) {
		t.Errorf("answer = %s", answer)
	}
	if ev.Decision != "ask" || ev.Ask == nil || ev.Ask.Kind != provider.AskBash || ev.Ask.Summary != "rg -n refund src" {
		t.Errorf("event %+v", ev)
	}
}

// TestGrantsMapToOptions: an allowed call picks allow_once — never
// allow_always, which an agent may keep beyond the run — and a denied one
// reject_once.
func TestGrantsMapToOptions(t *testing.T) {
	ask := provider.PermissionAsk{Kind: provider.AskBash, Tool: "Bash", Summary: "rg -n refund src", Patterns: []string{"rg *"}}
	for verdict, want := range map[string]string{
		provider.VerdictAllow:    `"optionId":"yes"`,
		provider.VerdictAllowRun: `"optionId":"yes"`,
		provider.VerdictDeny:     `"optionId":"no"`,
	} {
		ev, answer := runAskScript(t, provider.GrantFor(ask, verdict, ""))
		if !strings.Contains(answer, want) {
			t.Errorf("%s: answer = %s, want %s", verdict, answer, want)
		}
		if ev.Ask != nil || ev.Decision == "ask" {
			t.Errorf("%s: event %+v", verdict, ev)
		}
	}
}
