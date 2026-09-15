package ticket

import (
	"regexp"
	"strings"
	"time"
)

// RedactionMarker stands in for every pull-request reference an as-of
// assembly removes. It is deliberately visible: the point of the cutoff is
// to take away what the fix was, not to pretend nothing was taken away.
// A silent deletion would leave a sentence that reads as if the customer
// never mentioned anything, which is a different ticket from the one the
// engineer picked up.
const RedactionMarker = "[redacted: pull request]"

// Cutoff records what assembling a bundle as of an instant removed from it:
// the messages that had not been written yet, the attachments that had not
// been uploaded yet, and the pull-request references that gave the answer
// away. It is what `bundle/manifest.json` carries and what a retrospective
// golden entry copies into its `redacted` block.
type Cutoff struct {
	AsOf               time.Time `json:"asOf"`
	CommentsDropped    int       `json:"commentsDropped"`
	AttachmentsDropped int       `json:"attachmentsDropped"`
	PRLinks            int       `json:"prLinks"`
}

// prReferencePatterns are the shapes a pull request is named in: a hosting
// service's URL for one, and the prose mention an engineer writes in a
// comment. They are matched case-insensitively as one alternation, longest
// form first, so a mention inside a URL cannot win over the URL itself.
//
// The URL forms stop at whitespace and at the punctuation that usually
// closes a link in prose, so "(see https://github.com/o/r/pull/7)" keeps
// its closing bracket.
var prReferencePatterns = []string{
	// github.com/<owner>/<repo>/pull/N, and the API's /pulls/N.
	`\bhttps?://[^\s<>"'` + "`" + `\)\]]*?/(?:pull|pulls|pull-requests)/\d+[^\s<>"'` + "`" + `\)\]]*`,
	// GitLab: <host>/<group>/<project>/-/merge_requests/N.
	`\bhttps?://[^\s<>"'` + "`" + `\)\]]*?/merge_requests/\d+[^\s<>"'` + "`" + `\)\]]*`,
	// "PR #482", "pull request 482", "MR!17" and the rest of the shapes
	// the same thing gets written in by hand.
	`\b(?:pull requests?|merge requests?|PRs?|MRs?)\s*[#!]?\s*\d+\b`,
}

var prReference = regexp.MustCompile(`(?i)` + strings.Join(prReferencePatterns, "|"))

// prFieldNames are the tracker fields that hold nothing but a pull request,
// compared after folding case and dropping separators. A field named here
// is replaced whole rather than scanned, because a tracker that renders its
// links as "Fix (#482)" or as a bare branch name would otherwise slip a
// reference past the patterns above.
var prFieldNames = map[string]bool{
	"pr": true, "prs": true, "prurl": true, "prurls": true, "prlink": true, "prlinks": true,
	"pullrequest": true, "pullrequests": true, "pullrequesturl": true, "pullrequesturls": true,
	"mr": true, "mrs": true, "mrurl": true,
	"mergerequest": true, "mergerequests": true, "mergerequesturl": true,
}

// ApplyAsOf cuts b back to how the ticket looked at asOf and records what
// that cost in b.Cutoff. Three things go:
//
//   - every thread message written after asOf;
//   - every attachment that only a dropped message pointed at — an
//     attachment carries no timestamp of its own, so the message that
//     referenced it is the only evidence of when it arrived, and one that
//     no message references at all is kept, since nothing dates it;
//   - every pull-request reference, in the tracker and helpdesk records and
//     in the messages that survived, replaced by RedactionMarker.
//
// The returned attachments are the dropped ones, so the caller can delete
// the files they name: leaving a screenshot of the fix in the bundle
// directory would hand the session exactly what the cutoff took out of the
// thread.
//
// A message with a zero timestamp is kept. An adapter that could not date a
// comment is a gap in the source, and dropping the conversation on the
// strength of a missing field would quietly empty the bundle.
func ApplyAsOf(b *Bundle, asOf time.Time) (Cutoff, []Attachment) {
	c := Cutoff{AsOf: asOf}

	referenced := map[string]bool{} // any message pointed at it
	live := map[string]bool{}       // a message that survived pointed at it
	kept := make(Thread, 0, len(b.Thread))
	for _, m := range b.Thread {
		for _, id := range m.AttachmentIDs {
			referenced[id] = true
		}
		if m.At.After(asOf) {
			c.CommentsDropped++
			continue
		}
		for _, id := range m.AttachmentIDs {
			live[id] = true
		}
		m.Text = redact(m.Text, &c.PRLinks)
		kept = append(kept, m)
	}

	keptAttachments := make([]Attachment, 0, len(b.Attachments))
	var dropped []Attachment
	for _, a := range b.Attachments {
		if referenced[a.ID] && !live[a.ID] {
			c.AttachmentsDropped++
			dropped = append(dropped, a)
			continue
		}
		keptAttachments = append(keptAttachments, a)
	}

	if t := b.Tracker; t != nil {
		t.Title = redact(t.Title, &c.PRLinks)
		t.Description = redact(t.Description, &c.PRLinks)
		redactFields(t.Fields, &c.PRLinks)
	}
	if h := b.Helpdesk; h != nil {
		h.Subject = redact(h.Subject, &c.PRLinks)
		redactFields(h.Fields, &c.PRLinks)
	}

	b.Thread = kept
	if len(keptAttachments) == 0 {
		keptAttachments = nil
	}
	b.Attachments = keptAttachments
	b.Cutoff = &c
	return c, dropped
}

// redact replaces every pull-request reference in s, counting each one.
func redact(s string, n *int) string {
	if s == "" {
		return s
	}
	return prReference.ReplaceAllStringFunc(s, func(string) string {
		*n++
		return RedactionMarker
	})
}

// redactFields redacts an adapter's extra fields in place: a field whose
// name says it holds a pull request is replaced whole, and every other
// field is scanned the way prose is.
func redactFields(fields map[string]string, n *int) {
	for name, value := range fields {
		if value == "" {
			continue
		}
		if prFieldNames[normaliseFieldName(name)] {
			fields[name] = RedactionMarker
			*n++
			continue
		}
		fields[name] = redact(value, n)
	}
}

// normaliseFieldName folds case and drops the separators trackers disagree
// about, so `prs`, `PR_URL` and `pull requests` all compare as one name.
func normaliseFieldName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch r {
		case '_', '-', ' ', '.':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
