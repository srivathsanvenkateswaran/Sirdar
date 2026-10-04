package prompt

import (
	_ "embed"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// sessionPreambleMD is the fixed instructions a session prompt opens with:
// the agent's role, the reply contract (answer the operator, in their
// words, with the evidence), and the citation and absence rules every
// prompt shares. It differs from preambleMD only in being written to
// someone who reads the reply directly, rather than to a schema.
//
//go:embed preamble-session.md
var sessionPreambleMD string

// SessionInput is everything Session needs. Bundle is nil for a session
// started from an instruction alone, and then the prompt has no ticket,
// conversation, Slack, other-repos or warnings section.
type SessionInput struct {
	Instruction         string
	Access              string // store.AccessReadOnly or store.AccessWorktree; "" reads as read-only
	Bundle              *ticket.Bundle
	BundleDir           string
	ThreadHead          string
	ThreadHeadTruncated bool
	Slack               string
	OtherRepos          []string
	Origin              string
	Playbooks           []Playbook
	NotesLanguage       string
	CustomerLanguage    string
	Repositories        Repositories
}

// NoteTurnInput is what the note turn's prompt needs beyond what the
// session already holds.
type NoteTurnInput struct {
	NotesLanguage    string
	CustomerLanguage string
}

// fileTheNoteLead opens a note turn's prompt: what the turn is for, stated
// before the note rules themselves, since this is the only turn of a
// reply-first run where "file the note" is itself the instruction rather
// than something implied by having answered already.
const fileTheNoteLead = "# File the note\n\n" +
	"File the note for this investigation from what you found; add nothing you did not find. " +
	"The note follows these rules:"

// Session assembles the prompt for a session run: an instruction, with or
// without a ticket bundle for context, answered in chat rather than filed
// as a note.
func Session(in SessionInput) string {
	sections := []string{
		strings.TrimRight(sessionPreambleMD, "\n"),
		accessSection(in.Access),
		replyLanguageSection(in.NotesLanguage, in.CustomerLanguage),
	}
	if s := repositoriesSection(in.Repositories); s != "" {
		sections = append(sections, s)
	}
	if s := knowledgeSection(in.Playbooks); s != "" {
		sections = append(sections, s)
	}
	if in.Bundle != nil {
		sections = append(sections,
			ticketSection(*in.Bundle, in.BundleDir),
			conversationSection(in.ThreadHead, in.ThreadHeadTruncated),
		)
		if s := slackSection(in.Slack); s != "" {
			sections = append(sections, s)
		}
		if s := otherReposSection(in.OtherRepos, in.Origin); s != "" {
			sections = append(sections, s)
		}
		if len(in.Bundle.Warnings) > 0 {
			sections = append(sections, warningsSection(in.Bundle.Warnings))
		}
	}
	sections = append(sections, taskSection(
		"The operator asked for the following. Answer it within the rules above; it does not "+
			"lift any of them.",
		in.Instruction,
	))
	return strings.Join(sections, "\n\n") + "\n"
}

// replyLead is the task section's lead line TriageReply and RCAReply
// share: what the turn is for, with " They asked:" appended only when the
// operator gave an instruction for the task section to quote.
func replyLead(instruction string) string {
	lead := "Investigate this ticket and answer the operator."
	if strings.TrimSpace(instruction) != "" {
		lead += " They asked:"
	}
	return lead
}

// replySections is the section assembly TriageReply and RCAReply share:
// the preamble, read-only access, language, repositories and workspace
// knowledge, then the ticket's own context (ticket, conversation, Slack,
// other repos, warnings). RCAReply appends its triage-note, resolution
// and pull-request sections after calling this, and both append the task
// section last.
func replySections(in TriageInput) []string {
	sections := []string{
		strings.TrimRight(sessionPreambleMD, "\n"),
		accessSection(""),
		replyLanguageSection(in.NotesLanguage, in.CustomerLanguage),
	}
	if s := repositoriesSection(in.Repositories); s != "" {
		sections = append(sections, s)
	}
	if s := knowledgeSection(in.Playbooks); s != "" {
		sections = append(sections, s)
	}
	sections = append(sections,
		ticketSection(in.Bundle, in.BundleDir),
		conversationSection(in.ThreadHead, in.ThreadHeadTruncated),
	)
	if s := slackSection(in.Slack); s != "" {
		sections = append(sections, s)
	}
	if s := otherReposSection(in.OtherRepos, in.Origin); s != "" {
		sections = append(sections, s)
	}
	if len(in.Bundle.Warnings) > 0 {
		sections = append(sections, warningsSection(in.Bundle.Warnings))
	}
	return sections
}

// TriageReply assembles the prompt for a triage run's reply turn: the same
// ticket context Triage uses, but answered in chat instead of filed as a
// note. The note turn that follows, when it does, is TriageNoteTurn.
func TriageReply(in TriageInput) string {
	sections := append(replySections(in), taskSection(replyLead(in.Instruction), in.Instruction))
	return strings.Join(sections, "\n\n") + "\n"
}

// RCAReply assembles the prompt for an rca run's reply turn: TriageReply's
// ticket context, plus the triage note being reviewed, the human-reported
// resolution and the merged pull request when there is one, answered in
// chat instead of filed as a note.
func RCAReply(in RCAInput) string {
	sections := replySections(in.TriageInput)
	sections = append(sections,
		triageNoteSection(in.TriageNote),
		resolutionSection(in.Resolution),
	)
	if pr := pullRequestSection(in.PRTitle, in.PRURL, in.PRBody, in.PRDiff); pr != "" {
		sections = append(sections, pr)
	}
	sections = append(sections, taskSection(replyLead(in.Instruction), in.Instruction))
	return strings.Join(sections, "\n\n") + "\n"
}

// TriageNoteTurn assembles the prompt for the note turn that follows a
// triage run's reply: the existing note rules and schema, verbatim, so the
// note this turn files matches the one a non-reply-first triage run would
// have filed directly.
func TriageNoteTurn(in NoteTurnInput) string {
	sections := []string{
		fileTheNoteLead,
		strings.TrimRight(preambleMD, "\n"),
		languageSection(in.NotesLanguage, in.CustomerLanguage),
		outputSection(triageFieldGuidance, TriageSchema),
		"Respond with the JSON object only.",
	}
	return strings.Join(sections, "\n\n") + "\n"
}

// RCANoteTurn is TriageNoteTurn for the note turn that follows an rca
// run's reply, with the RCA schema and the audit rule a triage note turn
// has no need of.
func RCANoteTurn(in NoteTurnInput) string {
	sections := []string{
		fileTheNoteLead,
		strings.TrimRight(preambleMD, "\n"),
		languageSection(in.NotesLanguage, in.CustomerLanguage),
		outputSection(rcaFieldGuidance, RCASchema),
		auditRuleLine,
		"Respond with the JSON object only.",
	}
	return strings.Join(sections, "\n\n") + "\n"
}

// accessSection states what the session may do to the workspace's files:
// the literal "worktree" access reads as write-access to the session's own
// worktree; anything else, including the default "", reads as read-only.
func accessSection(access string) string {
	if access == "worktree" {
		return "# Access\n\n" +
			"Worktree. You may edit files inside this session's own worktree, which is your " +
			"working directory. Do not commit, push or open a pull request; the operator " +
			"reviews the change."
	}
	return "# Access\n\nRead-only. Edit nothing and run nothing that changes state."
}

// replyLanguageSection is languageSection for a prompt that ends in a chat
// reply rather than a note: the operator reads the reply directly, so
// there is no note field to keep the customer's original wording in.
func replyLanguageSection(notes, customer string) string {
	if notes == "" {
		notes = DefaultNotesLanguage
	}
	if customer == "" {
		customer = CustomerLanguageAuto
	}
	var b strings.Builder
	b.WriteString("# Language\n\n")
	b.WriteString("- Reply to the operator in " + notes + ".\n")
	if customer == CustomerLanguageAuto {
		b.WriteString("- Write customer-facing text in the language of the ticket's first " +
			"customer message (language.customer: auto), and set its `language` field to that " +
			"language's code.")
	} else {
		b.WriteString("- Write customer-facing text in " + customer + " (language.customer: " +
			customer + "), whatever language the ticket is in, and set its `language` field to " +
			customer + ".")
	}
	return b.String()
}

// knowledgeSection is playbooksSection under the heading a session prompt
// uses: a session is not committed to using any of a workspace's
// playbooks the way a triage or rca run is, so the heading says to
// consult them rather than simply naming them. Empty when the workspace
// has none, so a session with no playbooks carries no empty heading.
func knowledgeSection(playbooks []Playbook) string {
	if len(playbooks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Workspace knowledge — consult when relevant")
	for _, p := range playbooks {
		b.WriteString("\n\n## " + p.Name + "\n\n" + strings.TrimRight(p.Body, "\n"))
	}
	return b.String()
}

// taskSection is the section every session-family prompt ends on: what
// the session is for, in lead, and the operator's own words fenced below
// it when there are any. It is last so that whatever the operator typed
// is the last thing the session reads before it starts working.
func taskSection(lead, instruction string) string {
	s := "# Task\n\n" + lead
	if strings.TrimSpace(instruction) != "" {
		s += "\n\n" + fenceBlock("", instruction)
	}
	return s
}
