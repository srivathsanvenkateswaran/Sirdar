package openai

import "strings"

// systemPrompt is the standing instruction for a Sirdar-driven session. It
// is deliberately short: the run's own prompt (the ticket bundle, the
// playbooks, the note schema) is far longer, and a model that has to weigh
// two long sets of instructions follows neither well. Everything here is
// about the loop's contract — read-only tools, one submit_note call, no
// prose answer — because that is what the harness enforces and what a model
// most often gets wrong.
const systemPrompt = `You are running inside Sirdar, a support-triage harness, in a checkout of the workspace you are investigating.

Your tools are read-only: read files, list directories, search, run the workspace's allow-listed shell commands, fetch a URL, and call the workspace's MCP servers. Nothing you can call changes the repository, the tracker, or the helpdesk, and a command outside the allow-list is refused rather than run.

Gather evidence with those tools first. Then finish by calling submit_note exactly once, with the note as its arguments, matching that tool's schema. The note is the only output that counts: do not answer in prose, and do not paste the JSON into a message instead of calling the tool.`

// System returns the system message text.
func System() string { return systemPrompt }

// nudgeText is sent once when the model replies with prose instead of
// calling a tool. A second prose-only reply ends the session: a model that
// ignores this is not going to produce a note, and every further turn
// spends the run's budget for nothing.
const nudgeText = "Call submit_note with the JSON note now. A prose answer is discarded; the tool call is the only way to finish."

// Nudge returns the one reminder a prose-only reply earns.
func Nudge() string { return nudgeText }

// UserMessage renders the run's prompt as the session's first user message.
// Image attachments are named by path rather than sent as content parts:
// most OpenAI-compatible servers Sirdar targets are text-only, and a model
// that can read a file can still be told where the screenshots are.
func UserMessage(prompt string, images []string) string {
	if len(images) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\nAttached images (read them with your tools if you need them):\n")
	for _, path := range images {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteString("\n")
	}
	return b.String()
}
