// Package prompt assembles the prompt sent to a provider session: the fixed
// preamble, the workspace's playbooks, the ticket bundle, and the note
// schema the session must answer against.
package prompt

import (
	_ "embed"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// preambleMD is the fixed instructions every prompt opens with: the agent's
// role, the read-only run, the citation and absence rules.
//
//go:embed preamble.md
var preambleMD string

// TriageSchema is the draft-07 JSON Schema a triage session's JSON output
// must satisfy.
//
//go:embed schemas/triage.json
var TriageSchema []byte

// RCASchema is the draft-07 JSON Schema an rca session's JSON output must
// satisfy: a top-level object with "rca" and "resolution".
//
//go:embed schemas/rca.json
var RCASchema []byte

// TriageInput is everything Triage needs to assemble a triage prompt.
type TriageInput struct {
	Bundle     ticket.Bundle
	BundleDir  string // absolute
	Playbooks  []Playbook
	ThreadHead string // thread.md, or its first 40 lines
	// ThreadHeadTruncated says whether ThreadHead is only the head of a
	// longer conversation, so the prompt's heading can say which it is.
	ThreadHeadTruncated bool
	// NotesLanguage is the language code the note itself is written in
	// (config language.notes); empty means "en".
	NotesLanguage string
	// CustomerLanguage is the language code customer-facing text is
	// written in (config language.customer), or "auto" for the language
	// of the ticket's first customer message; empty means "auto".
	CustomerLanguage string
}

// RCAInput is everything RCA needs to assemble an rca prompt: the same
// ticket context as a triage run, plus the triage note being reviewed, the
// human-reported resolution, and the merged pull request when there is one.
type RCAInput struct {
	TriageInput

	TriageNote string // full markdown of the triage note
	Resolution string // human text

	PRTitle, PRBody, PRDiff string // may be empty
	PRURL                   string // may be empty
}

// DefaultNotesLanguage and CustomerLanguageAuto mirror the config
// defaults, so a prompt assembled without them says the same thing a
// default workspace's prompt says rather than saying nothing.
const (
	DefaultNotesLanguage = "en"
	CustomerLanguageAuto = "auto"
)

const auditRuleLine = "Audit rule: fill only what the PR or the resolution text supports; leave anything else null."

// triageFieldGuidance gives one sentence of guidance per top-level field of
// the triage schema, drawn from the spec's field descriptions.
var triageFieldGuidance = []string{
	"ticket identifies the record: key, title, tracker and helpdesk URLs, priority, service, and customer.",
	"title is a one-line summary of the issue.",
	"complaint is the customer's complaint translated faithfully into the note's language, preserving tone and urgency.",
	"complaintOriginal is that same complaint verbatim in the language the customer wrote it in, unedited and untranslated; omit it only when the complaint was already written in the note's language.",
	"customerReplyDraft is a short, polite status update the engineer could send the customer, as {language, text} in the customer's language: it acknowledges the issue and says it is being investigated, and it promises no fix, no cause and no date.",

	"timeline lists each event with its time, role, and summary, including what L1 already told the customer.",
	"reproSteps lists the steps that reproduce the issue.",
	"rootCause states the hypothesis, a confidence level (high, medium, low, or unknown), the evidence for it, and any code references.",
	"blastRadius describes who or what is affected.",
	"classification is one of code, data, config, not-a-bug, or unknown.",
	"proposedFix describes the fix, the files it touches, any remediation SQL, and its risks.",
	"openQuestions lists anything left unresolved rather than guessed.",
}

// rcaFieldGuidance gives one sentence of guidance per field one level under
// the RCA schema's "rca" and "resolution" objects, drawn from the spec's
// field descriptions.
var rcaFieldGuidance = []string{
	"rca.title is a one-line title for the root cause analysis.",
	"rca.summary is 3 to 5 sentences a manager can read alone.",
	"rca.customerSummary is what happened and what was done, as {language, text} in the customer's language, for the support agent to relay; it states what is already true and promises nothing further.",

	"rca.impact states customers affected, records affected, financial impact, first occurrence, detection, and time to detect.",
	"rca.timeline lists each event with its time and the evidence for it.",
	"rca.rootCause describes the cause, the offending code, the mechanism, and cites code references.",
	"rca.contributingFactors lists the factors that contributed to the issue.",
	"rca.evidence groups the supporting queries and results (or references and notes) by source: database, logs, apm, code, and attachments.",
	"rca.blastRadius states the query used, the count it returned, whether the scope is one-off or systemic, and the reasoning.",
	"rca.whyNotCaughtEarlier explains why the issue wasn't caught sooner.",
	"rca.prevention lists follow-up actions with a type (code, test, monitoring, or process), an owner, and a tracking ticket.",
	"rca.openQuestions lists anything left unresolved rather than guessed.",
	"rca.classification is one of code, data, config, or not-a-bug.",
	"rca.severity is one of high, medium, or low.",
	"rca.confidence is one of high, medium, low, or unknown.",
	"rca.origin is the workspace-defined origin, such as omni, legacy, pos, or integration.",
	"rca.triageReview scores the original triage note with a verdict (confirmed, partial, or wrong), what it got right, what it missed, and why.",
	"rca.lessons lists what should be remembered from this incident.",
	"rca.playbookSuggestions lists ready-to-paste additions for a named playbook, with a reason.",
	"resolution.title is a one-line title for the resolution.",
	"resolution.resolutionType is one of code-fix, data-fix, config-change, guidance, wont-fix, or duplicate.",
	"resolution.whatWasWrong is 2 to 3 sentences on what was wrong, without repeating the RCA.",
	"resolution.whatWeChanged describes what was changed to fix it.",
	"resolution.codeChange records the PR, its status, merge time, files changed, reviewer, and whether it's deployed, or null when there is no code change.",
	"resolution.dataChange records who authorised and executed the data change, the verification queries and outputs before and after, the rollback plan, and side effects, or null when there is no data change.",
	"resolution.verification lists each check performed, its environment, result, date, and who ran it.",
	"resolution.customerOutcome states what the customer was told, whether they confirmed the fix, and the helpdesk and tracker status.",
	"resolution.residualRisk lists risks that remain.",
	"resolution.lessons is what to remember from the resolution.",
}

// Triage assembles the prompt for a triage session.
func Triage(in TriageInput) string {
	sections := []string{
		strings.TrimRight(preambleMD, "\n"),
		languageSection(in.NotesLanguage, in.CustomerLanguage),
		playbooksSection(in.Playbooks),
		ticketSection(in.Bundle, in.BundleDir),
		conversationSection(in.ThreadHead, in.ThreadHeadTruncated),
	}
	if len(in.Bundle.Warnings) > 0 {
		sections = append(sections, warningsSection(in.Bundle.Warnings))
	}
	sections = append(sections, outputSection(triageFieldGuidance, TriageSchema))

	sections = append(sections, "Respond with the JSON object only.")
	return strings.Join(sections, "\n\n") + "\n"
}

// RCA assembles the prompt for an rca session.
func RCA(in RCAInput) string {
	sections := []string{
		strings.TrimRight(preambleMD, "\n"),
		languageSection(in.NotesLanguage, in.CustomerLanguage),
		playbooksSection(in.Playbooks),
		ticketSection(in.Bundle, in.BundleDir),
		conversationSection(in.ThreadHead, in.ThreadHeadTruncated),
	}
	if len(in.Bundle.Warnings) > 0 {
		sections = append(sections, warningsSection(in.Bundle.Warnings))
	}
	sections = append(sections, triageNoteSection(in.TriageNote))

	sections = append(sections, resolutionSection(in.Resolution))
	if pr := pullRequestSection(in.PRTitle, in.PRURL, in.PRBody, in.PRDiff); pr != "" {
		sections = append(sections, pr)
	}
	sections = append(sections, outputSection(rcaFieldGuidance, RCASchema))
	sections = append(sections, auditRuleLine)
	sections = append(sections, "Respond with the JSON object only.")
	return strings.Join(sections, "\n\n") + "\n"
}

// languageSection states both languages a run writes in: the one the note
// is written in, and the one anything the customer reads is written in.
// The session has to be told both, because it is the same session that
// translates the complaint into the first and drafts the reply in the
// second.
func languageSection(notes, customer string) string {
	if notes == "" {
		notes = DefaultNotesLanguage
	}
	if customer == "" {
		customer = CustomerLanguageAuto
	}
	var b strings.Builder
	b.WriteString("# Language\n\n")
	b.WriteString("- Write the note in " + notes + " (language.notes: " + notes + "). Every field is in that language except the ones named below.\n")
	if customer == CustomerLanguageAuto {
		b.WriteString("- Write customer-facing text in the language of the ticket's first customer message (language.customer: auto), and set its `language` field to that language's code.\n")
	} else {
		b.WriteString("- Write customer-facing text in " + customer + " (language.customer: " + customer + "), whatever language the ticket is in, and set its `language` field to " + customer + ".\n")
	}
	b.WriteString("- Keep the customer's original wording as well as the translation, verbatim, in the field the schema gives it.")
	return b.String()
}

func playbooksSection(playbooks []Playbook) string {

	var b strings.Builder
	b.WriteString("# Playbooks")
	for _, p := range playbooks {
		b.WriteString("\n\n## " + p.Name + "\n\n" + strings.TrimRight(p.Body, "\n"))
	}
	return b.String()
}

func ticketSection(bundle ticket.Bundle, bundleDir string) string {
	var b strings.Builder
	b.WriteString("# Ticket\n\n")
	b.WriteString("Key: " + bundle.Key() + "\n")
	b.WriteString("Title: " + ticketTitle(bundle) + "\n")
	b.WriteString("Priority: " + ticketPriority(bundle) + "\n")
	b.WriteString("Tracker URL: " + trackerURL(bundle) + "\n")
	b.WriteString("Helpdesk URL: " + helpdeskURL(bundle) + "\n")
	b.WriteString("Customer: " + customerLine(bundle) + "\n")
	b.WriteString("Bundle directory: " + bundleDir + "\n")
	b.WriteString("\nFiles:")
	if len(bundle.Attachments) == 0 {
		b.WriteString("\n(none)")
	}
	for _, a := range bundle.Attachments {
		path := a.Path
		if path == "" {
			path = a.Name
		}
		b.WriteString("\n- " + path)
	}
	return b.String()
}

func ticketTitle(bundle ticket.Bundle) string {
	if bundle.Tracker != nil && bundle.Tracker.Title != "" {
		return bundle.Tracker.Title
	}
	if bundle.Helpdesk != nil {
		return bundle.Helpdesk.Subject
	}
	return ""
}

func ticketPriority(bundle ticket.Bundle) string {
	if bundle.Tracker != nil && bundle.Tracker.Priority != "" {
		return bundle.Tracker.Priority
	}
	if bundle.Helpdesk != nil {
		return bundle.Helpdesk.Priority
	}
	return ""
}

func trackerURL(bundle ticket.Bundle) string {
	if bundle.Tracker != nil {
		return bundle.Tracker.URL
	}
	return ""
}

func helpdeskURL(bundle ticket.Bundle) string {
	if bundle.Helpdesk != nil {
		return bundle.Helpdesk.URL
	}
	return ""
}

func customerLine(bundle ticket.Bundle) string {
	if bundle.Helpdesk == nil {
		return ""
	}
	if bundle.Helpdesk.CustomerID == "" {
		return bundle.Helpdesk.Customer
	}
	return bundle.Helpdesk.Customer + " (" + bundle.Helpdesk.CustomerID + ")"
}

func conversationSection(threadHead string, truncated bool) string {
	heading := "## Conversation"
	if truncated {
		heading += " (first lines; the rest is in thread.md)"
	}
	return heading + "\n\n" + fenceBlock("", threadHead)
}

func warningsSection(warnings []string) string {
	var b strings.Builder
	b.WriteString("## Warnings\n\n")
	for i, w := range warnings {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("- " + w)
	}
	return b.String()
}

func triageNoteSection(note string) string {
	return "# Triage note\n\n" + fenceBlock("", note)
}

func resolutionSection(resolution string) string {
	return "# Resolution as reported by the engineer\n\n" + fenceBlock("", resolution)
}

func pullRequestSection(title, url, body, diff string) string {
	if title == "" && url == "" && body == "" && diff == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Merged pull request")
	if title != "" {
		b.WriteString("\n\nTitle: " + title)
	}
	if url != "" {
		b.WriteString("\n\nURL: " + url)
	}
	if body != "" {
		b.WriteString("\n\n" + fenceBlock("", body))
	}
	if diff != "" {
		b.WriteString("\n\n" + fenceBlock("diff", diff))
	}
	return b.String()
}

func outputSection(guidance []string, schema []byte) string {
	var b strings.Builder
	b.WriteString("# Output\n\n")
	for i, g := range guidance {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("- " + g)
	}
	b.WriteString("\n\n")
	b.WriteString(fenceBlock("json", string(schema)))
	return b.String()
}

// fenceBlock wraps body in a fenced code block, trimming any trailing
// newlines from body first so the closing fence always lands on its own
// line regardless of how the caller's string ends.
func fenceBlock(lang, body string) string {
	body = strings.TrimRight(body, "\n")
	if lang == "" {
		return "```\n" + body + "\n```"
	}
	return "```" + lang + "\n" + body + "\n```"
}
