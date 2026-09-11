package intercom

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- wire types ---

// icAuthor is Intercom's author/contact reference. Type is the role
// enum: "user" and "lead" are the customer, "admin" and "team" are staff,
// "bot" is automation.
type icAuthor struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// name renders an author for a message byline: the name when Intercom
// sent one, the email address when it did not, then the bare id — an
// author Sirdar cannot resolve is still evidence of who wrote the
// message.
func (a icAuthor) name() string {
	if n := strings.TrimSpace(a.Name); n != "" {
		return n
	}
	if e := strings.TrimSpace(a.Email); e != "" {
		return e
	}
	return a.ID
}

type icAttachment struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Filesize    int64  `json:"filesize"`
}

// icSource is the message the conversation started with.
type icSource struct {
	Type        string         `json:"type"`
	ID          string         `json:"id"`
	DeliveredAs string         `json:"delivered_as"`
	Subject     string         `json:"subject"`
	Body        string         `json:"body"`
	Author      icAuthor       `json:"author"`
	Attachments []icAttachment `json:"attachments"`
}

type icPart struct {
	ID          string         `json:"id"`
	PartType    string         `json:"part_type"`
	Body        string         `json:"body"`
	CreatedAt   int64          `json:"created_at"`
	Author      icAuthor       `json:"author"`
	Attachments []icAttachment `json:"attachments"`
}

type icConversation struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Open      bool   `json:"open"`
	Priority  string `json:"priority"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	Source    icSource
	Contacts  struct {
		Contacts []icAuthor `json:"contacts"`
	} `json:"contacts"`
	ConversationParts struct {
		Parts      []icPart `json:"conversation_parts"`
		TotalCount int      `json:"total_count"`
	} `json:"conversation_parts"`
}

// icContact is the contact record behind a conversation's contact
// reference, which carries only an id and a type.
type icContact struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Companies struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	} `json:"companies"`
}

// unixTime converts Intercom's unix-seconds timestamps. Zero means "not
// set" and maps to the zero time rather than 1970.
func unixTime(secs int64) time.Time {
	if secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0).UTC()
}

// --- app id and contacts ---

type icMe struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Email string `json:"email"`
	App   struct {
		IDCode string `json:"id_code"`
		ID     string `json:"id"`
		Name   string `json:"name"`
	} `json:"app"`
}

// appIDOnce resolves the workspace's app id, which only appears on /me and
// is what the Intercom inbox URL is keyed by. It is fetched at most once
// per Client — including when the workspace has none to give, since a
// second call would fail the same way.
func (c *Client) appIDOnce(ctx context.Context) string {
	c.mu.Lock()
	if c.appIDSet {
		id := c.appID
		c.mu.Unlock()
		return id
	}
	c.mu.Unlock()

	var me icMe
	id := ""
	if err := c.apiGET(ctx, "/me", nil, &me); err == nil {
		if id = me.App.IDCode; id == "" {
			id = me.App.ID
		}
	}

	c.mu.Lock()
	c.appID, c.appIDSet = id, true
	c.mu.Unlock()
	return id
}

// lookupContact resolves a contact id to its record, caching the result:
// the same contact authors most of the parts in one conversation.
func (c *Client) lookupContact(ctx context.Context, id string) (icContact, error) {
	if id == "" {
		return icContact{}, nil
	}
	c.mu.Lock()
	if ct, ok := c.contacts[id]; ok {
		c.mu.Unlock()
		return ct, nil
	}
	c.mu.Unlock()

	var ct icContact
	if err := c.apiGET(ctx, "/contacts/"+url.PathEscape(id), nil, &ct); err != nil {
		return icContact{}, err
	}

	c.mu.Lock()
	if c.contacts == nil || len(c.contacts) > maxContactCacheEntries {
		// A run's contact set is small in practice; a cache that grew past
		// this is more likely a pathologically long-lived client than a
		// workload worth holding onto, so it is dropped wholesale rather
		// than turned into an LRU for a case that should not arise.
		c.contacts = map[string]icContact{}
	}
	c.contacts[id] = ct
	c.mu.Unlock()
	return ct, nil
}

// --- Get ---

func (c *Client) fetchConversation(ctx context.Context, id string) (icConversation, error) {
	// display_as=plaintext asks Intercom to render part bodies as text
	// rather than HTML where it can. Bodies still arrive as HTML often
	// enough that every one of them goes through htmltext anyway.
	q := url.Values{}
	q.Set("display_as", "plaintext")

	var conv icConversation
	if err := c.apiGET(ctx, "/conversations/"+url.PathEscape(id), q, &conv); err != nil {
		return icConversation{}, err
	}
	return conv, nil
}

// subjectOf names a conversation: the source message's subject when it has
// one (an email-originated conversation does), the conversation title
// next, and otherwise the first line of the opening message, which is what
// the Intercom inbox itself shows for a chat.
func subjectOf(conv icConversation) string {
	if s := strings.TrimSpace(conv.Source.Subject); s != "" {
		return s
	}
	if t := strings.TrimSpace(conv.Title); t != "" {
		return t
	}
	text := strings.TrimSpace(htmltext.ToPlain(conv.Source.Body))
	if text == "" {
		return ""
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return firstRunes(strings.TrimSpace(text), 120)
}

// firstRunes truncates on a rune boundary, so a multi-byte character is
// never cut in half.
func firstRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// Get fetches a conversation and maps it to ticket.HelpdeskTicket. The
// contact behind the conversation is resolved through /contacts, since a
// conversation carries only contact ids; a contact lookup that fails is
// recorded as a warning rather than failing the fetch, because the
// conversation itself is the evidence Sirdar came for.
func (c *Client) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	conv, err := c.fetchConversation(ctx, id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}

	var warnings []string
	fields := map[string]string{}
	out := ticket.HelpdeskTicket{
		ID:        id,
		Subject:   subjectOf(conv),
		Status:    conv.State,
		Priority:  conv.Priority,
		Channel:   conv.Source.Type,
		CreatedAt: unixTime(conv.CreatedAt),
		UpdatedAt: unixTime(conv.UpdatedAt),
		Fields:    fields,
	}
	if conv.Source.DeliveredAs != "" {
		fields["deliveredAs"] = conv.Source.DeliveredAs
	}

	if len(conv.Contacts.Contacts) > 0 {
		ref := conv.Contacts.Contacts[0]
		contact, lerr := c.lookupContact(ctx, ref.ID)
		if lerr != nil {
			warnings = append(warnings, fmt.Sprintf("intercom: contact %s: %v", ref.ID, lerr))
			out.Contact = ref.name()
		} else {
			out.Contact = strings.TrimSpace(contact.Name)
			if out.Contact == "" {
				out.Contact = contact.Email
			}
			if out.Contact == "" {
				out.Contact = ref.name()
			}
			if contact.Email != "" {
				fields["contactEmail"] = contact.Email
			}
			if len(contact.Companies.Data) > 0 {
				out.Customer = contact.Companies.Data[0].Name
				out.CustomerID = contact.Companies.Data[0].ID
			}
		}
		if ref.ID != "" {
			fields["contactId"] = ref.ID
		}
	}

	if appID := c.appIDOnce(ctx); appID != "" {
		out.URL = "https://app.intercom.com/a/inbox/" + appID + "/inbox/conversation/" + id
	} else {
		warnings = append(warnings, "intercom: workspace app id unavailable, ticket URL omitted")
	}

	c.addWarnings(id, warnings)
	return out, nil
}

// --- Threads ---

// authorRole maps an author type to a Sirdar role. "user" and "lead" are
// the customer side; "admin" and "team" are staff; "bot" is automation and
// maps to system. An unrecognised type is treated as staff, which is the
// safer default: a message wrongly attributed to the customer would read
// as the customer's own words.
func authorRole(t string) ticket.Role {
	switch strings.ToLower(t) {
	case "user", "lead", "contact":
		return ticket.RoleCustomer
	case "bot":
		return ticket.RoleSystem
	default:
		return ticket.RoleAgent
	}
}

// Threads returns the conversation as an ordered thread: the source
// message first, then each conversation part that carries text or an
// attachment. Parts with neither — Intercom emits one for every
// assignment, close and reopen — are left out rather than filling the
// thread with empty entries.
//
// Intercom caps an inline part list at 500 entries; when the conversation
// says it has more than it sent, that is recorded as a warning, since
// there is no parts-pagination endpoint to follow.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	conv, err := c.fetchConversation(ctx, id)
	if err != nil {
		return nil, err
	}

	var warnings []string
	msgs := make(ticket.Thread, 0, len(conv.ConversationParts.Parts)+1)

	sourceText, _ := htmltext.ToMarkdown(conv.Source.Body)
	msgs = append(msgs, ticket.Message{
		At:            unixTime(conv.CreatedAt),
		Author:        conv.Source.Author.name(),
		Role:          authorRole(conv.Source.Author.Type),
		Text:          sourceText,
		AttachmentIDs: attachmentIDs(conv.Source.ID, "source", conv.Source.Attachments),
	})

	for _, p := range conv.ConversationParts.Parts {
		text, _ := htmltext.ToMarkdown(p.Body)
		if strings.TrimSpace(text) == "" && len(p.Attachments) == 0 {
			continue
		}
		author := p.Author.name()
		role := authorRole(p.Author.Type)
		if strings.EqualFold(p.PartType, "note") {
			// A note is admin-to-admin and never reaches the customer.
			author += " (internal)"
			role = ticket.RoleAgent
		}
		msgs = append(msgs, ticket.Message{
			At:            unixTime(p.CreatedAt),
			Author:        author,
			Role:          role,
			Text:          text,
			AttachmentIDs: attachmentIDs(p.ID, "part", p.Attachments),
		})
	}

	if total := conv.ConversationParts.TotalCount; total > len(conv.ConversationParts.Parts) {
		warnings = append(warnings, fmt.Sprintf("intercom: conversation parts truncated at %d of %d by the API", len(conv.ConversationParts.Parts), total))
	}
	c.addWarnings(id, warnings)
	return msgs, nil
}

// attachmentIDs builds the synthetic ids Attachments uses for the same
// files. Intercom's part attachments carry no id of their own, so one is
// derived from the part it hangs off and its position, which is stable for
// as long as the part is.
func attachmentIDs(partID, kind string, atts []icAttachment) []string {
	if len(atts) == 0 {
		return nil
	}
	ids := make([]string, len(atts))
	for i := range atts {
		ids[i] = attachmentID(partID, kind, i)
	}
	return ids
}

func attachmentID(partID, kind string, i int) string {
	if partID == "" {
		partID = kind
	}
	return fmt.Sprintf("%s-%d", partID, i+1)
}
