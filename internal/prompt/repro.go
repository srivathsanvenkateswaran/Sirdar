package prompt

import "strings"

// ReproPrompt is the staging browser a session may reproduce on: the MCP
// server whose tools drive it, and the hosts it may navigate to.
type ReproPrompt struct {
	Server string
	Hosts  []string
}

// reproSection tells the session it can try the ticket on staging, and the
// rules for doing so. The policy that enforces the hosts lives with the
// provider; this is the part the agent reads, so it knows a navigation off
// those hosts will be refused, that a login is the operator's to do, and
// that the reply has to say what reproduced and what did not. "" when the
// run has no browser or no hosts.
func reproSection(r *ReproPrompt) string {
	if r == nil || r.Server == "" || len(r.Hosts) == 0 {
		return ""
	}
	return "# Reproduce on staging\n\n" +
		"You have a browser (the mcp__" + r.Server + "__ tools) and these staging hosts: " + strings.Join(r.Hosts, ", ") + ".\n" +
		"Before you conclude anything about behaviour a user sees, try to reproduce it there.\n\n" +
		"- Navigate only to those hosts. On them, clicking, typing, selecting and running script need no\n" +
		"  permission; navigation anywhere else is refused or put to the operator.\n" +
		"- Use staging data. Production identifiers from the ticket (domain, CompanyID, invoice number)\n" +
		"  may not exist on staging; find the staging equivalent and say which you used.\n" +
		"- If you reach a login page, stop and ask the operator, in your reply, to log in in the browser\n" +
		"  window that opened; the profile keeps the login. Never type a password, code or token, and\n" +
		"  never use one found in the bundle.\n" +
		"- Do not trigger anything that reaches a real person: SMS, email, WhatsApp, payment.\n" +
		"- Report it in your reply under \"Reproduction\": reproduced, not reproduced, or not attempted and\n" +
		"  why; where (URL and tenant); the steps; the evidence (what the page showed, a console or\n" +
		"  network line, a screenshot path). Call a mechanism confirmed only when it reproduced or a test\n" +
		"  showed it."
}
