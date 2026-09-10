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
}

// Bundle is everything gathered for a ticket: the tracker record, the helpdesk
// record, the conversation thread, its attachments, and any warnings surfaced
// while assembling them.
type Bundle struct {
	Tracker     *TrackerTicket  // nil when the workspace has no tracker
	Helpdesk    *HelpdeskTicket // nil when fetch failed or absent
	Thread      Thread
	Attachments []Attachment
	Warnings    []string // e.g. attachment download failures, surfaced to the prompt
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
