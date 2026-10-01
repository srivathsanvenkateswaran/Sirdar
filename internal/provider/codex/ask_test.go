package codex

import (
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

func askPolicy(spec *provider.SessionSpec, grants ...provider.Grant) {
	spec.Policy = &provider.PermissionPolicy{
		BashAllow: []string{"rg *"},
		MCPAllow:  []string{"mcp__notes__notes_lookup", "mcp__grafana__query_*"},
		Root:      spec.Cwd,
		Ask:       true,
		Grants:    provider.NewGrants(grants...),
	}
}

// TestAskMapsToCancel: a question is answered "cancel", which declines the
// call and interrupts the turn, on both the command and the MCP approval;
// the event carries the question.
func TestAskMapsToCancel(t *testing.T) {
	sess := startSession(t, "script-approvals.jsonl", func(spec *provider.SessionSpec) { askPolicy(spec) })
	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := replyTo(t, res, 102); got != `{"decision":"cancel"}` {
		t.Errorf("command reply = %s", got)
	}
	if got := replyTo(t, res, 104); got != `{"action":"cancel"}` {
		t.Errorf("mcp reply = %s", got)
	}
	// A file change in a triage run is the read-only guarantee: never asked.
	if got := replyTo(t, res, 107); got != `{"decision":"decline"}` {
		t.Errorf("file change reply = %s", got)
	}
	ev, ok := permissionFor(evs, "mcp__notes__delete_everything")
	if !ok || ev.Decision != "ask" || ev.Ask == nil || ev.Ask.Kind != provider.AskMCP {
		t.Errorf("mcp event %+v", ev)
	}
	ok = false
	for _, e := range evs {
		if e.Kind == provider.EvPermission && e.Tool == "commandExecution" && e.Decision == "ask" {
			ev, ok = e, true
		}
	}
	if !ok || ev.Ask == nil || ev.Ask.Summary != "curl -X POST https://example.invalid" {
		t.Errorf("command event %+v", ev)
	}
}

// TestGrantsMapToAcceptAndDecline: an allowed command is accepted, an
// allowed MCP tool is accepted with empty content, and a denied one is
// declined with the reason on the event.
func TestGrantsMapToAcceptAndDecline(t *testing.T) {
	cmd := provider.PermissionAsk{Kind: provider.AskBash, Tool: "Bash", Summary: "curl -X POST https://example.invalid", Patterns: []string{"curl *"}}
	mcp := provider.PermissionAsk{Kind: provider.AskMCP, Tool: "mcp__notes__delete_everything", Patterns: []string{"mcp__notes__delete_everything"}}
	sess := startSession(t, "script-approvals.jsonl", func(spec *provider.SessionSpec) {
		askPolicy(spec, provider.GrantFor(cmd, provider.VerdictAllow, ""), provider.GrantFor(mcp, provider.VerdictDeny, "never in triage"))
	})
	evs := drain(sess)
	res, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := replyTo(t, res, 102); got != `{"decision":"accept"}` {
		t.Errorf("command reply = %s", got)
	}
	if got := replyTo(t, res, 104); got != `{"action":"decline"}` {
		t.Errorf("mcp reply = %s", got)
	}
	if ev, ok := permissionFor(evs, "mcp__notes__delete_everything"); !ok || ev.Decision != "deny" || ev.Text != "Sirdar policy: the operator denied mcp__notes__delete_everything for this run: never in triage" {
		t.Errorf("mcp event %+v", ev)
	}
}
