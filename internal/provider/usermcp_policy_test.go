package provider

import "testing"

// userServerReadGlobs is the read-only permissions.mcp block docs/config.md
// recommends for the three servers the operator opts in with
// mcp.userServers. The test holds the doc's list to what it claims.
var userServerReadGlobs = []string{
	"mcp__slack__slack_read_*",
	"mcp__slack__slack_search_*",
	"mcp__zoho-desk__get*",
	"mcp__zoho-desk__search*",
	"mcp__zoho-desk__resolveTicketNumber",
	"mcp__janus__janus_get_*",
	"mcp__janus__janus_search_*",
	"mcp__janus__janus_list_*",
}

// TestUserServerToolsUnderTheHeuristic: with no permissions.mcp, the user
// servers' writes are refused by name exactly as a workspace server's are.
func TestUserServerToolsUnderTheHeuristic(t *testing.T) {
	refused := []string{
		"mcp__slack__slack_send_message", "mcp__slack__slack_send_message_draft",
		"mcp__slack__slack_add_reaction", "mcp__slack__slack_add_list_record",
		"mcp__slack__slack_create_canvas", "mcp__slack__slack_create_conversation",
		"mcp__slack__slack_update_canvas", "mcp__slack__slack_update_list_record",
		"mcp__slack__slack_schedule_message",
		"mcp__janus__janus_create_ticket", "mcp__janus__janus_update_ticket",
		"mcp__janus__janus_transition_ticket", "mcp__janus__janus_log_worklog",
		"mcp__janus__janus_add_comment", "mcp__janus__janus_delete_link",
		// resolve is a write word, so the heuristic refuses this read; the
		// recommended globs name it outright.
		"mcp__zoho-desk__resolveTicketNumber",
	}
	for _, tool := range refused {
		if DecideMCPTool(tool, nil).Allow {
			t.Errorf("%s: the heuristic allowed a write", tool)
		}
	}
	allowed := []string{
		"mcp__slack__slack_read_thread", "mcp__slack__slack_read_channel",
		"mcp__slack__slack_search_channels", "mcp__zoho-desk__getTicket",
		"mcp__zoho-desk__getThreads", "mcp__janus__janus_get_ticket",
		"mcp__janus__janus_search_tickets", "mcp__janus__janus_list_comments",
	}
	for _, tool := range allowed {
		if v := DecideMCPTool(tool, nil); !v.Allow {
			t.Errorf("%s: the heuristic refused a read (%s)", tool, v.Reason())
		}
	}
}

// TestUserServerReadGlobs: the documented globs allow the reads, including
// resolveTicketNumber, and nothing that writes.
func TestUserServerReadGlobs(t *testing.T) {
	for _, tool := range []string{
		"mcp__slack__slack_read_thread", "mcp__slack__slack_search_public",
		"mcp__zoho-desk__getTicket", "mcp__zoho-desk__searchTickets",
		"mcp__zoho-desk__resolveTicketNumber",
		"mcp__janus__janus_get_ticket", "mcp__janus__janus_search_jql", "mcp__janus__janus_list_projects",
	} {
		if !DecideMCPTool(tool, userServerReadGlobs).Allow {
			t.Errorf("%s: the read globs refused it", tool)
		}
	}
	for _, tool := range []string{
		"mcp__slack__slack_send_message", "mcp__slack__slack_create_canvas",
		"mcp__janus__janus_transition_ticket", "mcp__janus__janus_log_worklog",
		"mcp__zoho-desk__updateTicket",
	} {
		if DecideMCPTool(tool, userServerReadGlobs).Allow {
			t.Errorf("%s: the read globs allowed a write", tool)
		}
	}
}

// TestUserServerReadBecomesAQuestion: with permissions.ask on, a read the
// allow-list does not cover is put to the operator instead of refused.
func TestUserServerReadBecomesAQuestion(t *testing.T) {
	p := &PermissionPolicy{MCPAllow: []string{"mcp__janus__janus_get_*"}, Ask: true}
	d := p.Decide("mcp__slack__slack_read_thread", []byte(`{}`))
	if d.Allow {
		t.Fatal("not in permissions.mcp, so it is not allowed outright")
	}
	if d.Ask == nil {
		t.Fatalf("with permissions.ask on the refusal should be a question: %+v", d)
	}
}
