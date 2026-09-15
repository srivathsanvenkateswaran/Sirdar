// Package ticket defines the tracker/helpdesk/thread types that make up a support
// ticket bundle, and the writer that persists a bundle to disk.
package ticket

import "time"

// Role identifies who sent a message in a Thread.
type Role string

const (
	RoleCustomer Role = "customer"
	RoleAgent    Role = "agent"
	RoleSystem   Role = "system"
)

// TrackerTicket is the issue-tracker side of a ticket (e.g. a Jira issue).
type TrackerTicket struct {
	Key         string
	Title       string
	Description string
	Priority    string
	Status      string
	Assignee    string
	URL         string
	HelpdeskRef string // helpdesk ticket id parsed from the tracker record; "" if none

	CreatedAt time.Time
	UpdatedAt time.Time

	Fields map[string]string // adapter-specific extras (e.g. epic, company)
}

// HelpdeskTicket is the customer-support side of a ticket (e.g. a Zoho Desk ticket).
type HelpdeskTicket struct {
	ID         string
	Subject    string
	Status     string
	Priority   string
	Channel    string
	Contact    string
	Customer   string
	CustomerID string
	URL        string

	CreatedAt time.Time
	UpdatedAt time.Time

	Fields map[string]string
}

// Message is one entry in a Thread.
type Message struct {
	At            time.Time
	Author        string
	Role          Role
	Text          string
	AttachmentIDs []string
}

// Thread is the ordered conversation for a ticket.
type Thread []Message

// Attachment is a file referenced from a Thread message.
type Attachment struct {
	ID   string
	Name string
	MIME string
	Path string

	// Transcript is the bundle-relative path of the text transcription of
	// an audio attachment, written beside the file itself. Empty when the
	// attachment is not audio, or when the workspace configured no
	// transcription command, or when the command could not read it.
	Transcript string `json:",omitempty"`

	// TranscriptLanguage is the language the transcription tool reported
	// or was told to use, when it said; empty otherwise.
	TranscriptLanguage string `json:",omitempty"`
}

// Transcribed reports whether this attachment has a transcript in the
// bundle — which, for an audio file, is the difference between evidence
// the session can read and evidence it cannot.
func (a Attachment) Transcribed() bool { return a.Transcript != "" }

// SkippedAttachment is an attachment the bundle's manifest listed but could
// not make available to a session — over the size cap, or a type it cannot
// open — recorded so a note can tell a reader what evidence went unread
// rather than only surfacing it as a run warning. An attachment that was
// transcribed instead is not one of these: its contents did reach the
// session, as text.
type SkippedAttachment struct {
	Name   string
	Type   string // MIME type, best effort
	Size   string // human-readable, e.g. "17.0 MiB"; "size unknown" when unknown
	Reason string // e.g. "over the 15.0 MiB limit", "cannot be opened in this session"
}

// Bundle is everything gathered for a ticket: the tracker record, the helpdesk
// record, the conversation thread, its attachments, and any warnings surfaced
// while assembling them.
type Bundle struct {
	Tracker            *TrackerTicket  // nil when the workspace has no tracker
	Helpdesk           *HelpdeskTicket // nil when fetch failed or absent
	Thread             Thread
	Attachments        []Attachment
	SkippedAttachments []SkippedAttachment // from the manifest, but dropped: too large, unreadable, or unrecoverably untranscribed
	Warnings           []string            // e.g. attachment download failures, surfaced to the prompt

	// Cutoff is set when this bundle was assembled as of an instant
	// rather than as the ticket stands today: it says what ApplyAsOf
	// removed. Nil on an ordinary live bundle.
	Cutoff *Cutoff `json:",omitempty"`
}

// Key returns Tracker.Key if present else Helpdesk.ID.
func (b Bundle) Key() string {
	if b.Tracker != nil {
		return b.Tracker.Key
	}
	if b.Helpdesk != nil {
		return b.Helpdesk.ID
	}
	return ""
}
