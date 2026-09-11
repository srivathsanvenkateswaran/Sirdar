package prompt

import (
	_ "embed"
	"strings"
)

// fixPreambleMD is the fixed instruction a fix session opens with: the human
// gate that has already happened, the scope rule, and the standing refusal
// to commit or push.
//
//go:embed preamble-fix.md
var fixPreambleMD string

// FixSchema is the draft-07 JSON Schema a fix session's JSON output must
// satisfy.
//
//go:embed schemas/fix.json
var FixSchema []byte

// FixInput is everything Fix needs to assemble a fix prompt: the workspace's
// playbooks, the approved triage note, and the RCA note when the ticket has
// one (a fix run after an RCA has a confirmed cause to work from, not just a
// hypothesis).
type FixInput struct {
	Key        string
	Branch     string
	Playbooks  []Playbook
	TriageNote string
	RCANote    string // may be empty
}

// fixFieldGuidance gives one sentence of guidance per field of the fix
// schema.
var fixFieldGuidance = []string{
	"summary says what you changed; its first line becomes the commit subject, so write that line as a subject.",
	"filesChanged lists every workspace file you created, edited or deleted, relative to the workspace root.",
	"testsRun lists each build or test command you ran and what it reported.",
	"risks says what a reviewer should look at hardest, or \"none\".",
	"deviationFromNote is empty when you implemented the note's Proposed Fix as written, and otherwise says what you did differently and why.",
}

// Fix assembles the prompt for a fix session.
func Fix(in FixInput) string {
	sections := []string{
		strings.TrimRight(fixPreambleMD, "\n"),
		playbooksSection(in.Playbooks),
		fixTicketSection(in),
		triageNoteSection(in.TriageNote),
	}
	if strings.TrimSpace(in.RCANote) != "" {
		sections = append(sections, "# RCA note\n\n"+fenceBlock("", in.RCANote))
	}
	sections = append(sections, outputSection(fixFieldGuidance, FixSchema))
	sections = append(sections, "Respond with the JSON object only.")
	return strings.Join(sections, "\n\n") + "\n"
}

// fixTicketSection names the ticket and the branch the session is standing
// on, so a model that runs `git status` recognises where it is.
func fixTicketSection(in FixInput) string {
	var b strings.Builder
	b.WriteString("# Fix\n\n")
	b.WriteString("Ticket: " + in.Key + "\n")
	if in.Branch != "" {
		b.WriteString("Branch: " + in.Branch + " (already checked out, cut from the default branch)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
