package gorgias

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- wire types ---

// gPerson is Gorgias's user-or-customer reference. The same shape appears
// as a ticket's customer, its assignee_user, and a message's sender and
// receiver; the API does not say which of the two kinds it is, so a
// message's role comes from from_agent and public rather than from here.
type gPerson struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
}

// display renders a person for a byline or a contact field: the name
// Gorgias sent, then the first/last pair, then the email address, then the
// bare id. A person Sirdar cannot resolve is still evidence of who wrote
// the message.
func (p gPerson) display() string {
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	if n := strings.TrimSpace(p.Firstname + " " + p.Lastname); n != "" {
		return n
	}
	if e := strings.TrimSpace(p.Email); e != "" {
		return e
	}
	if p.ID != 0 {
		return strconv.FormatInt(p.ID, 10)
	}
	return ""
}

// gFile is Gorgias's File object as it hangs off a message. It carries no
// id of its own — the URL is the identifier — so this adapter derives a
// stable one from the message and the position.
type gFile struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	URL         string `json:"url"`
}

type gTag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type gTicket struct {
	ID              int64    `json:"id"`
	Subject         string   `json:"subject"`
	Status          string   `json:"status"`
	Channel         string   `json:"channel"`
	Via             string   `json:"via"`
	Language        string   `json:"language"`
	Spam            bool     `json:"spam"`
	Customer        *gPerson `json:"customer"`
	AssigneeUser    *gPerson `json:"assignee_user"`
	Tags            []gTag   `json:"tags"`
	CreatedDatetime string   `json:"created_datetime"`
	UpdatedDatetime string   `json:"updated_datetime"`
	ClosedDatetime  string   `json:"closed_datetime"`
}

type gMessage struct {
	ID              int64    `json:"id"`
	TicketID        int64    `json:"ticket_id"`
	Public          bool     `json:"public"`
	FromAgent       bool     `json:"from_agent"`
	Channel         string   `json:"channel"`
	Via             string   `json:"via"`
	RuleID          *int64   `json:"rule_id"`
	Subject         string   `json:"subject"`
	BodyText        string   `json:"body_text"`
	BodyHTML        string   `json:"body_html"`
	StrippedText    string   `json:"stripped_text"`
	Sender          *gPerson `json:"sender"`
	Attachments     []gFile  `json:"attachments"`
	CreatedDatetime string   `json:"created_datetime"`
	SentDatetime    string   `json:"sent_datetime"`
}

// gMessagePage is Gorgias's list envelope: the resources under data, the
// cursors under meta.
type gMessagePage struct {
	Data []gMessage `json:"data"`
	Meta struct {
		PrevCursor string `json:"prev_cursor"`
		NextCursor string `json:"next_cursor"`
	} `json:"meta"`
}

// gAccount is the record Ping reads. Nothing is mapped from it; it exists
// so the probe decodes something rather than trusting a 200 alone.
type gAccount struct {
	ID     int64  `json:"id"`
	Domain string `json:"domain"`
}

// timeLayouts are the shapes a Gorgias "ISO 8601 datetime" arrives in. The
// documented one is RFC 3339 with an offset, but the API also emits
// microseconds with no zone at all, which is UTC in practice.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
}

// parseTime parses a Gorgias timestamp. An empty or unparseable string
// yields the zero time rather than failing a whole call over one cosmetic
// field.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// --- Get ---

func (c *Client) fetchTicket(ctx context.Context, id string) (gTicket, error) {
	var gt gTicket
	if err := c.apiGET(ctx, "/api/tickets/"+url.PathEscape(id), nil, &gt); err != nil {
		return gTicket{}, err
	}
	return gt, nil
}

// Get fetches a ticket and maps it to ticket.HelpdeskTicket.
//
// Priority is left empty on purpose: the Gorgias Ticket object has no
// priority attribute at all. Teams that track urgency do it with tags or a
// custom field, which have no fixed name, so the tags go into Fields as
// they are rather than one of them being guessed at as a priority.
func (c *Client) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	gt, err := c.fetchTicket(ctx, id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}

	fields := map[string]string{}
	if gt.Via != "" {
		fields["via"] = gt.Via
	}
	if gt.Language != "" {
		fields["language"] = gt.Language
	}
	if gt.Spam {
		fields["spam"] = "true"
	}
	if names := tagNames(gt.Tags); len(names) > 0 {
		fields["tags"] = strings.Join(names, ", ")
	}
	if gt.AssigneeUser != nil {
		if who := gt.AssigneeUser.display(); who != "" {
			fields["assignee"] = who
		}
	}
	if t := parseTime(gt.ClosedDatetime); !t.IsZero() {
		fields["closedAt"] = t.Format(time.RFC3339)
	}

	out := ticket.HelpdeskTicket{
		ID:        id,
		Subject:   gt.Subject,
		Status:    gt.Status,
		Channel:   gt.Channel,
		URL:       c.baseURL + "/app/ticket/" + id,
		CreatedAt: parseTime(gt.CreatedDatetime),
		UpdatedAt: parseTime(gt.UpdatedDatetime),
		Fields:    fields,
	}
	if gt.Customer != nil {
		// Gorgias's customer is a person, not an organisation: there is no
		// company object on the ticket. Customer carries who they are and
		// Contact how to reach them, which is the most either field can
		// honestly say here.
		out.Customer = gt.Customer.display()
		out.Contact = strings.TrimSpace(gt.Customer.Email)
		if out.Contact == "" {
			out.Contact = out.Customer
		}
		if gt.Customer.ID != 0 {
			out.CustomerID = strconv.FormatInt(gt.Customer.ID, 10)
		}
	}
	return out, nil
}

func tagNames(tags []gTag) []string {
	var names []string
	for _, t := range tags {
		if n := strings.TrimSpace(t.Name); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// --- Threads ---

// listMessages fetches every message on a ticket, paging with the cursor
// Gorgias returns in meta.next_cursor until the feed ends or
// maxMessagePages is reached. In the latter case the messages fetched so
// far are returned with a warning: a ticket that long is unusual enough
// that Sirdar would rather triage it with a truncated thread and a visible
// note than fail outright.
//
// The feed is asked for in creation order, so a ticket that runs past the
// page cap keeps its opening messages — the customer's complaint — rather
// than its most recent ones.
func (c *Client) listMessages(ctx context.Context, id string) ([]gMessage, []string, error) {
	var all []gMessage
	cursor := ""
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("ticket_id", id)
		q.Set("limit", strconv.Itoa(messagesPerPage))
		q.Set("order_by", "created_datetime:asc")
		if cursor != "" {
			q.Set("cursor", cursor)
		}

		var pg gMessagePage
		if err := c.apiGET(ctx, "/api/messages", q, &pg); err != nil {
			return nil, nil, err
		}
		all = append(all, pg.Data...)

		next := strings.TrimSpace(pg.Meta.NextCursor)
		if next == "" || next == cursor {
			// An unchanged cursor is a server that would page forever.
			return all, nil, nil
		}
		if page >= maxMessagePages {
			return all, []string{fmt.Sprintf("gorgias: message pages capped at %d", maxMessagePages)}, nil
		}
		cursor = next
	}
}

// messageRole maps a message to a Sirdar role and says whether it is an
// internal note.
//
// Gorgias has two booleans rather than an author-type enum, and they mean
// different things: public is the customer-facing split ("whether the
// message was sent/received by a customer; internal notes are not
// public"), and from_agent is the direction ("whether the message was sent
// by your company to a customer, or the opposite"). An internal note is
// therefore a non-public message from the company — it is checked first,
// because a note is agent-to-agent and reading one as a customer's words
// would be the worst of the available mistakes.
//
// A message a rule sent is automation rather than a person, so it maps to
// system. Anything else from the company is an agent, and anything not
// from the company is the customer.
func messageRole(m gMessage) (ticket.Role, bool) {
	if !m.Public || strings.EqualFold(m.Channel, "internal-note") {
		return ticket.RoleAgent, true
	}
	if !m.FromAgent {
		return ticket.RoleCustomer, false
	}
	if m.RuleID != nil {
		return ticket.RoleSystem, false
	}
	return ticket.RoleAgent, false
}

// messageText renders a message body: Gorgias's own plain-text rendering
// when it sent one, the HTML converted to Markdown otherwise. stripped_text
// is deliberately not preferred — it drops quoted history and signatures,
// and a triage note is read by someone who wants the thread as the customer
// wrote it.
func messageText(m gMessage) string {
	if t := strings.TrimSpace(m.BodyText); t != "" {
		return m.BodyText
	}
	text, _ := htmltext.ToMarkdown(m.BodyHTML)
	return text
}

// messageAuthor names the sender, with an " (internal)" suffix on a note
// so the thread shows at a glance what the customer never saw.
func messageAuthor(m gMessage, internal bool) string {
	author := ""
	if m.Sender != nil {
		author = m.Sender.display()
	}
	if author == "" {
		author = "unknown"
	}
	if internal {
		author += " (internal)"
	}
	return author
}

// messageTime is when a message happened: created_datetime, which every
// message has, falling back to sent_datetime for one Gorgias dated only by
// its delivery.
func messageTime(m gMessage) time.Time {
	if t := parseTime(m.CreatedDatetime); !t.IsZero() {
		return t
	}
	return parseTime(m.SentDatetime)
}

// Threads returns a ticket's messages as an ordered thread. A message with
// neither text nor an attachment is left out rather than filling the
// thread with an empty entry.
//
// The message feed is a filtered collection, not a ticket sub-resource, so
// a ticket id that does not exist yields an empty thread rather than a
// not-found error. That is not worth a second round trip to disguise: a
// bundle calls Get first, and Get does answer not-found.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	msgs, warnings, err := c.listMessages(ctx, id)
	if err != nil {
		return nil, err
	}

	out := make(ticket.Thread, 0, len(msgs))
	for _, m := range msgs {
		text := messageText(m)
		if strings.TrimSpace(text) == "" && len(m.Attachments) == 0 {
			continue
		}
		role, internal := messageRole(m)
		out = append(out, ticket.Message{
			At:            messageTime(m),
			Author:        messageAuthor(m, internal),
			Role:          role,
			Text:          text,
			AttachmentIDs: attachmentIDs(m),
		})
	}

	// The API was asked for creation order; sorting again costs nothing
	// and keeps a feed that ignored order_by from reaching the note out of
	// sequence.
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	c.addWarnings(id, warnings)
	return out, nil
}
