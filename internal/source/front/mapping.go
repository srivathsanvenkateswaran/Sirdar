package front

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/httpx"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- wire types ---

// frTeammate is Front's teammate shape, used for a conversation's assignee
// and for the author of an outbound message or a comment. Type is the enum
// that separates a person from automation: "user" is a human teammate,
// while "rule", "macro", "api", "integration", "application",
// "bulk_reply", "csat", "smart_csat" and "ai" are Front acting on its own.
type frTeammate struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	First    string `json:"first_name"`
	Last     string `json:"last_name"`
	Type     string `json:"type"`
}

// name renders a teammate for a message byline: "First Last", then the
// email address, then the @username, then the bare id — an author Sirdar
// cannot resolve is still evidence of who wrote the message.
func (t *frTeammate) name() string {
	if t == nil {
		return ""
	}
	full := strings.TrimSpace(strings.TrimSpace(t.First) + " " + strings.TrimSpace(t.Last))
	if full != "" {
		return full
	}
	if e := strings.TrimSpace(t.Email); e != "" {
		return e
	}
	if u := strings.TrimSpace(t.Username); u != "" {
		return u
	}
	return t.ID
}

// frHandle is a contact handle with the part it played: the conversation's
// `recipient`, and each entry in a message's `recipients`. Role is "from",
// "to", "cc", "bcc" or "reply-to".
type frHandle struct {
	Name   string `json:"name"`
	Handle string `json:"handle"`
	Role   string `json:"role"`
}

// name renders a handle for a byline: the display name Front resolved, or
// the raw handle (an email address, a phone number, an @screen_name).
func (h *frHandle) name() string {
	if h == nil {
		return ""
	}
	if n := strings.TrimSpace(h.Name); n != "" {
		return n
	}
	return strings.TrimSpace(h.Handle)
}

// frTag is one of a conversation's tags.
type frTag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// frAttachment is one downloadable file hanging off a message or a comment.
// URL is Front's own authenticated download endpoint, not a pre-signed CDN
// link, so it is fetched with the bearer token like any other call.
type frAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Metadata    struct {
		IsInline bool   `json:"is_inline"`
		CID      string `json:"cid"`
	} `json:"metadata"`
}

// frConversation is the conversation record itself.
//
// Timestamps are unix seconds carried as JSON numbers with a fractional
// part (Front's own examples show "posted_at": 1698943401.378), so they are
// decoded as float64 rather than int64.
type frConversation struct {
	ID             string      `json:"id"`
	Type           string      `json:"type"`
	Subject        string      `json:"subject"`
	Status         string      `json:"status"`
	StatusID       string      `json:"status_id"`
	StatusCategory string      `json:"status_category"`
	TicketIDs      []string    `json:"ticket_ids"`
	Assignee       *frTeammate `json:"assignee"`
	Recipient      *frHandle   `json:"recipient"`
	Tags           []frTag     `json:"tags"`
	IsPrivate      bool        `json:"is_private"`
	CreatedAt      float64     `json:"created_at"`
	UpdatedAt      float64     `json:"updated_at"`
	WaitingSince   float64     `json:"waiting_since"`
}

// frMessage is one entry in the externally-visible half of the thread:
// what was sent to, or received from, the customer.
//
// DraftMode is Front's draft marker and is null on a sent message; IsDraft
// is the older boolean, decoded too so a workspace still being served the
// old shape does not have its drafts read as real replies.
type frMessage struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	IsInbound   bool           `json:"is_inbound"`
	DraftMode   *string        `json:"draft_mode"`
	IsDraft     bool           `json:"is_draft"`
	CreatedAt   float64        `json:"created_at"`
	Subject     string         `json:"subject"`
	Blurb       string         `json:"blurb"`
	Author      *frTeammate    `json:"author"`
	Recipients  []frHandle     `json:"recipients"`
	Body        string         `json:"body"`
	Text        string         `json:"text"`
	Attachments []frAttachment `json:"attachments"`
}

// draft reports whether the message was never sent. An unsent reply is not
// part of the conversation that happened.
func (m frMessage) draft() bool {
	return m.IsDraft || (m.DraftMode != nil && strings.TrimSpace(*m.DraftMode) != "")
}

// from returns the handle that sent an inbound message. Front leaves
// `author` null on inbound mail — there is no teammate who wrote it — and
// puts the sender in `recipients` with role "from".
func (m frMessage) from() *frHandle {
	for i := range m.Recipients {
		if strings.EqualFold(m.Recipients[i].Role, "from") {
			return &m.Recipients[i]
		}
	}
	return nil
}

// frComment is one entry in the internal half of the thread: a teammate
// note that, in Front's own words, is never sent and cannot be shared
// outside of Front.
type frComment struct {
	ID          string         `json:"id"`
	Author      *frTeammate    `json:"author"`
	Body        string         `json:"body"`
	PostedAt    float64        `json:"posted_at"`
	IsPinned    bool           `json:"is_pinned"`
	Attachments []frAttachment `json:"attachments"`
}

// frPagination is the block Front puts on every list response. Next is a
// ready-to-use full URL or null; Front's own guidance is to follow it and
// never to infer the end of a feed from a short page.
type frPagination struct {
	Next string `json:"next"`
}

type frMessagePage struct {
	Pagination frPagination `json:"_pagination"`
	Results    []frMessage  `json:"_results"`
}

type frCommentPage struct {
	Pagination frPagination `json:"_pagination"`
	Results    []frComment  `json:"_results"`
}

// unixTime converts one of Front's fractional unix-seconds timestamps.
// Zero means "not set" and maps to the zero time rather than 1970.
func unixTime(secs float64) time.Time {
	if secs <= 0 {
		return time.Time{}
	}
	whole, frac := int64(secs), secs-float64(int64(secs))
	return time.Unix(whole, int64(frac*float64(time.Second))).UTC()
}

// bodyText renders a body Front returned. Front's message bodies are HTML,
// so they go through htmltext; a body with no markup in it at all is kept
// verbatim instead, because the converter collapses whitespace the way a
// browser does and that would run the lines of a genuinely plain body
// together.
func bodyText(body string) string {
	if !strings.Contains(body, "<") {
		return strings.TrimSpace(body)
	}
	text, _ := htmltext.ToMarkdown(body)
	return text
}

// --- Get ---

func (c *Client) fetchConversation(ctx context.Context, id string) (frConversation, error) {
	var conv frConversation
	if err := c.apiGET(ctx, "/conversations/"+url.PathEscape(id), nil, &conv); err != nil {
		return frConversation{}, err
	}
	return conv, nil
}

// mapConversation converts a decoded conversation into
// ticket.HelpdeskTicket. Front has no priority field — urgency is modelled
// with tags and custom fields instead — so Priority is left empty rather
// than guessed at from a tag.
func mapConversation(id string, conv frConversation) ticket.HelpdeskTicket {
	fields := map[string]string{}
	if conv.StatusCategory != "" {
		fields["statusCategory"] = conv.StatusCategory
	}
	if conv.StatusID != "" {
		fields["statusId"] = conv.StatusID
	}
	if len(conv.Tags) > 0 {
		names := make([]string, 0, len(conv.Tags))
		for _, t := range conv.Tags {
			if t.Name != "" {
				names = append(names, t.Name)
			}
		}
		if len(names) > 0 {
			fields["tags"] = strings.Join(names, ", ")
		}
	}
	if a := conv.Assignee.name(); a != "" {
		fields["assignee"] = a
	}
	if len(conv.TicketIDs) > 0 {
		fields["ticketIds"] = strings.Join(conv.TicketIDs, ", ")
	}
	if conv.IsPrivate {
		fields["isPrivate"] = strconv.FormatBool(conv.IsPrivate)
	}
	if h := conv.Recipient; h != nil && strings.TrimSpace(h.Handle) != "" {
		fields["contactHandle"] = strings.TrimSpace(h.Handle)
	}

	// Front's conversation carries updated_at on the shape this adapter is
	// written against; waiting_since stands in when it does not, since it
	// moves with the conversation and an empty UpdatedAt reads as "never
	// touched".
	updated := conv.UpdatedAt
	if updated <= 0 {
		updated = conv.WaitingSince
	}

	return ticket.HelpdeskTicket{
		ID:        id,
		Subject:   conv.Subject,
		Status:    conv.Status,
		Channel:   conv.Type,
		Contact:   conv.Recipient.name(),
		CreatedAt: unixTime(conv.CreatedAt),
		UpdatedAt: unixTime(updated),
		// The web URL is built from the id this client was asked for, not
		// from anything the API returned: a "view in Front" link taken out
		// of a response body would be a link an operator clicks, pointed
		// wherever the response said.
		URL:    appURLPrefix + id,
		Fields: fields,
	}
}

// Get fetches a conversation and maps it to ticket.HelpdeskTicket.
func (c *Client) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	conv, err := c.fetchConversation(ctx, id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	return mapConversation(id, conv), nil
}

// --- feeds ---

// automationTypes are the teammate types that are Front acting on its own
// rather than a person: an auto-reply, a rule, an API integration. They map
// to ticket.RoleSystem so an agent reading the thread does not take a rule's
// output for something a human chose to say.
var automationTypes = map[string]bool{
	"rule":        true,
	"macro":       true,
	"api":         true,
	"application": true,
	"integration": true,
	"bulk_reply":  true,
	"csat":        true,
	"smart_csat":  true,
	"ai":          true,
}

// messageRole maps a message to a Sirdar role.
//
// `is_inbound` is the only field that says which side of the conversation a
// message came from, and it is Front's own definition of received-from-
// outside, so an inbound message is the customer's. Everything else was
// sent by the team: a human teammate is an agent, and one of Front's
// automation author types is system. An outbound message is never mapped to
// the customer, whatever its author says, because a message wrongly
// attributed to the customer reads as the customer's own words.
func messageRole(m frMessage) ticket.Role {
	if m.IsInbound {
		return ticket.RoleCustomer
	}
	if m.Author != nil && automationTypes[strings.ToLower(m.Author.Type)] {
		return ticket.RoleSystem
	}
	return ticket.RoleAgent
}

// messageAuthor names the sender of a message: the teammate who wrote an
// outbound one, and the "from" handle of an inbound one, falling back to
// the other in either direction so a byline is never blank when Front gave
// something to print.
func messageAuthor(m frMessage) string {
	if m.IsInbound {
		if n := m.from().name(); n != "" {
			return n
		}
		return m.Author.name()
	}
	if n := m.Author.name(); n != "" {
		return n
	}
	return m.from().name()
}

// entry is one item of the merged thread: a message or a comment, reduced
// to what both Threads and Attachments need. Keeping them in one ordered
// slice is what makes an attachment's position in the thread the same in
// both calls, so the AttachmentIDs on a message match the files Attachments
// writes.
type entry struct {
	id     string
	at     time.Time
	author string
	role   ticket.Role
	text   string
	atts   []frAttachment
}

// listMessages returns the conversation's message feed, following
// `_pagination.next` until it is null or maxFeedPages have been read. A
// next link arrives inside a response body, so it is checked against
// trust.Check before it is followed — an untrusted one stops pagination
// with a warning rather than being fetched.
func (c *Client) listMessages(ctx context.Context, id string) ([]frMessage, []string, error) {
	var out []frMessage
	next := fmt.Sprintf("%s/conversations/%s/messages?limit=%d", c.baseURL, url.PathEscape(id), pageSize)
	warnings, err := c.walk(ctx, "messages", next, func(raw string) (string, error) {
		var pg frMessagePage
		if err := c.apiGETAbsolute(ctx, raw, &pg); err != nil {
			return "", err
		}
		out = append(out, pg.Results...)
		return pg.Pagination.Next, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return out, warnings, nil
}

// listComments returns the conversation's comment feed, on the same terms
// as listMessages.
func (c *Client) listComments(ctx context.Context, id string) ([]frComment, []string, error) {
	var out []frComment
	next := fmt.Sprintf("%s/conversations/%s/comments?limit=%d", c.baseURL, url.PathEscape(id), pageSize)
	warnings, err := c.walk(ctx, "comments", next, func(raw string) (string, error) {
		var pg frCommentPage
		if err := c.apiGETAbsolute(ctx, raw, &pg); err != nil {
			return "", err
		}
		out = append(out, pg.Results...)
		return pg.Pagination.Next, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return out, warnings, nil
}

// walk drives one paginated feed: it calls fetch with each page URL and
// follows whatever next link fetch returns, refusing an untrusted host,
// stopping on a link that points back at the page just read, and capping
// the sweep at maxFeedPages with a warning naming the feed.
func (c *Client) walk(ctx context.Context, feed, first string, fetch func(raw string) (string, error)) ([]string, error) {
	var warnings []string
	pages := 0
	for next := first; next != ""; {
		u, err := url.Parse(next)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("front: %s page link is not a valid url", feed))
			break
		}
		if ok, _, _ := c.trust.Check(u); !ok {
			warnings = append(warnings, fmt.Sprintf("front: %s page host not trusted: %s", feed, httpx.HostOf(next)))
			break
		}
		if pages >= maxFeedPages {
			warnings = append(warnings, fmt.Sprintf("front: %s pages capped at %d", feed, maxFeedPages))
			break
		}
		link, err := fetch(next)
		if err != nil {
			return nil, err
		}
		pages++
		if link == next {
			// A feed whose next link points at the page just read would
			// loop for ever; stop rather than trust it.
			warnings = append(warnings, fmt.Sprintf("front: %s pagination stopped on a self-referential next link", feed))
			break
		}
		next = strings.TrimSpace(link)
	}
	return warnings, nil
}

// entries merges the two feeds into one ordered thread: messages with
// their public roles, comments as internal agent notes, sorted oldest
// first. A stable sort keeps same-timestamp entries in the order the API
// returned them, and messages are appended before comments so a note
// written in the same second as the reply it is about still reads after it.
func (c *Client) entries(ctx context.Context, id string) ([]entry, []string, error) {
	msgs, msgWarnings, err := c.listMessages(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	comments, commentWarnings, err := c.listComments(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	warnings := append(msgWarnings, commentWarnings...)

	out := make([]entry, 0, len(msgs)+len(comments))
	for _, m := range msgs {
		if m.draft() {
			continue
		}
		text := bodyText(m.Body)
		if strings.TrimSpace(text) == "" {
			// Front sends `text` alongside `body` for exactly this case.
			text = strings.TrimSpace(m.Text)
		}
		if strings.TrimSpace(text) == "" && len(m.Attachments) == 0 {
			continue
		}
		out = append(out, entry{
			id:     m.ID,
			at:     unixTime(m.CreatedAt),
			author: messageAuthor(m),
			role:   messageRole(m),
			text:   text,
			atts:   m.Attachments,
		})
	}
	for _, cm := range comments {
		text := bodyText(cm.Body)
		if strings.TrimSpace(text) == "" && len(cm.Attachments) == 0 {
			continue
		}
		author := cm.Author.name()
		// Every comment is teammate-to-teammate and never reaches the
		// customer, so it is an agent message carrying the same internal
		// marker the other adapters use.
		out = append(out, entry{
			id:     cm.ID,
			at:     unixTime(cm.PostedAt),
			author: strings.TrimSpace(author + " (internal)"),
			role:   ticket.RoleAgent,
			text:   text,
			atts:   cm.Attachments,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out, warnings, nil
}

// --- Threads ---

// Threads returns the conversation as one ordered thread, merging Front's
// two feeds: `messages`, the externally sent and received content, and
// `comments`, the teammate notes Front keeps internal. Drafts are left out
// — an unsent reply is not part of the conversation that happened — and so
// is an entry with neither text nor an attachment.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	items, warnings, err := c.entries(ctx, id)
	if err != nil {
		return nil, err
	}
	msgs := make(ticket.Thread, 0, len(items))
	for _, e := range items {
		msgs = append(msgs, ticket.Message{
			At:            e.at,
			Author:        e.author,
			Role:          e.role,
			Text:          e.text,
			AttachmentIDs: attachmentIDs(e),
		})
	}
	c.addWarnings(id, warnings)
	return msgs, nil
}

// attachmentIDs lists the ids Attachments will use for the same files.
func attachmentIDs(e entry) []string {
	if len(e.atts) == 0 {
		return nil
	}
	ids := make([]string, len(e.atts))
	for i := range e.atts {
		ids[i] = attachmentID(e.id, e.atts[i], i)
	}
	return ids
}

// attachmentID is the attachment's own Front id, or one derived from the
// message or comment it hangs off when Front sent none — stable for as long
// as that parent is.
func attachmentID(parentID string, a frAttachment, i int) string {
	if id := strings.TrimSpace(a.ID); id != "" {
		return id
	}
	if parentID == "" {
		parentID = "attachment"
	}
	return fmt.Sprintf("%s-%d", parentID, i+1)
}
