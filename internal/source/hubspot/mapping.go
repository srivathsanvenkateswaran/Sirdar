package hubspot

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// ticketProperties is the property set asked for on a ticket. HubSpot
// returns only the properties a request names, so this list is the whole
// contract: anything missing here is simply absent from the response.
const ticketProperties = "subject,content,hs_pipeline_stage,hs_ticket_priority,createdate,hs_lastmodifieddate,hubspot_owner_id"

// ticketAssociations is the association set asked for alongside the
// ticket: the customer, their company, and the Conversations-inbox thread
// carrying the actual conversation.
const ticketAssociations = "contacts,companies,conversations"

// --- wire types ---

type assocRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type assocList struct {
	Results []assocRef `json:"results"`
}

type crmTicket struct {
	ID           string               `json:"id"`
	Properties   map[string]string    `json:"properties"`
	CreatedAt    string               `json:"createdAt"`
	UpdatedAt    string               `json:"updatedAt"`
	Archived     bool                 `json:"archived"`
	Associations map[string]assocList `json:"associations"`
}

// assocIDs returns the associated object ids under whichever key matches
// kind. HubSpot names the association block after the object type, but
// which spelling comes back — "conversations", "conversation",
// "p_conversations" on some accounts — is the least-documented corner of
// the ticket model, so the lookup matches on a substring rather than
// insisting on one exact key.
func (t crmTicket) assocIDs(kind string) []string {
	var out []string
	for key, list := range t.Associations {
		if !strings.Contains(strings.ToLower(key), kind) {
			continue
		}
		for _, r := range list.Results {
			if r.ID != "" {
				out = append(out, r.ID)
			}
		}
	}
	return out
}

type crmContact struct {
	ID         string `json:"id"`
	Properties struct {
		FirstName string `json:"firstname"`
		LastName  string `json:"lastname"`
		Email     string `json:"email"`
	} `json:"properties"`
}

// name renders a contact as "First Last", falling back to the email
// address and then the bare id.
func (c crmContact) name() string {
	full := strings.TrimSpace(strings.TrimSpace(c.Properties.FirstName) + " " + strings.TrimSpace(c.Properties.LastName))
	if full != "" {
		return full
	}
	if c.Properties.Email != "" {
		return c.Properties.Email
	}
	return c.ID
}

type convSender struct {
	ActorID            string `json:"actorId"`
	Name               string `json:"name"`
	DeliveryIdentifier struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"deliveryIdentifier"`
}

type convAttachment struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	FileID string `json:"fileId"`
	Name   string `json:"name"`
}

type convMessage struct {
	ID          string           `json:"id"`
	Type        string           `json:"type"`
	Text        string           `json:"text"`
	RichText    string           `json:"richText"`
	Subject     string           `json:"subject"`
	Direction   string           `json:"direction"`
	CreatedAt   string           `json:"createdAt"`
	Senders     []convSender     `json:"senders"`
	Attachments []convAttachment `json:"attachments"`
}

type convMessagePage struct {
	Results []convMessage `json:"results"`
	Paging  struct {
		Next struct {
			After string `json:"after"`
			Link  string `json:"link"`
		} `json:"next"`
	} `json:"paging"`
}

// parseTime parses a HubSpot timestamp. Object properties come back as
// ISO-8601 ("2026-09-10T08:30:00.000Z") in the v3 CRM API and as epoch
// milliseconds in a few older shapes, so both are accepted; anything else
// yields the zero time rather than failing the whole call over one
// cosmetic field.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms).UTC()
	}
	return time.Time{}
}

// --- portal id, contacts, companies ---

// portalIDOnce resolves the account's hub id, which only the account-info
// endpoint carries and which the HubSpot UI keys every record URL by. It
// is fetched at most once per Client — including when the account has none
// to give, since a second call would fail the same way.
func (c *Client) portalIDOnce(ctx context.Context) string {
	c.mu.Lock()
	if c.portalIDSet {
		id := c.portalID
		c.mu.Unlock()
		return id
	}
	c.mu.Unlock()

	var details struct {
		PortalID int64 `json:"portalId"`
	}
	id := ""
	if err := c.apiGET(ctx, "/account-info/v3/details", nil, &details); err == nil && details.PortalID != 0 {
		id = strconv.FormatInt(details.PortalID, 10)
	}

	c.mu.Lock()
	c.portalID, c.portalIDSet = id, true
	c.mu.Unlock()
	return id
}

// lookupContact resolves a contact id to its record, caching the result.
func (c *Client) lookupContact(ctx context.Context, id string) (crmContact, error) {
	c.mu.Lock()
	if ct, ok := c.contacts[id]; ok {
		c.mu.Unlock()
		return ct, nil
	}
	c.mu.Unlock()

	q := url.Values{}
	q.Set("properties", "firstname,lastname,email")
	var ct crmContact
	if err := c.apiGET(ctx, "/crm/v3/objects/contacts/"+url.PathEscape(id), q, &ct); err != nil {
		return crmContact{}, err
	}

	c.mu.Lock()
	if c.contacts == nil || len(c.contacts) > maxObjectCacheEntries {
		c.contacts = map[string]crmContact{}
	}
	c.contacts[id] = ct
	c.mu.Unlock()
	return ct, nil
}

// lookupCompany resolves a company id to its name, caching the result.
func (c *Client) lookupCompany(ctx context.Context, id string) (string, error) {
	c.mu.Lock()
	if n, ok := c.companies[id]; ok {
		c.mu.Unlock()
		return n, nil
	}
	c.mu.Unlock()

	q := url.Values{}
	q.Set("properties", "name")
	var co struct {
		Properties struct {
			Name string `json:"name"`
		} `json:"properties"`
	}
	if err := c.apiGET(ctx, "/crm/v3/objects/companies/"+url.PathEscape(id), q, &co); err != nil {
		return "", err
	}

	c.mu.Lock()
	if c.companies == nil || len(c.companies) > maxObjectCacheEntries {
		c.companies = map[string]string{}
	}
	c.companies[id] = co.Properties.Name
	c.mu.Unlock()
	return co.Properties.Name, nil
}

// --- Get ---

func (c *Client) fetchTicket(ctx context.Context, id string) (crmTicket, error) {
	q := url.Values{}
	q.Set("properties", ticketProperties)
	q.Set("associations", ticketAssociations)

	var t crmTicket
	if err := c.apiGET(ctx, "/crm/v3/objects/tickets/"+url.PathEscape(id), q, &t); err != nil {
		return crmTicket{}, err
	}
	return t, nil
}

// Get fetches a ticket and maps it to ticket.HelpdeskTicket. The contact
// and company behind the ticket are resolved through their own object
// endpoints, since an association carries only ids; a lookup that fails is
// recorded as a warning rather than failing the fetch, because the ticket
// itself is the evidence Sirdar came for.
func (c *Client) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	t, err := c.fetchTicket(ctx, id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}

	var warnings []string
	props := t.Properties
	if props == nil {
		props = map[string]string{}
	}
	fields := map[string]string{}
	if v := props["hubspot_owner_id"]; v != "" {
		fields["ownerId"] = v
	}
	if v := props["hs_pipeline_stage"]; v != "" {
		fields["pipelineStage"] = v
	}

	out := ticket.HelpdeskTicket{
		ID:        id,
		Subject:   props["subject"],
		Status:    props["hs_pipeline_stage"],
		Priority:  props["hs_ticket_priority"],
		CreatedAt: parseTime(firstNonEmpty(props["createdate"], t.CreatedAt)),
		UpdatedAt: parseTime(firstNonEmpty(props["hs_lastmodifieddate"], t.UpdatedAt)),
		Fields:    fields,
	}

	if ids := t.assocIDs("contact"); len(ids) > 0 {
		fields["contactId"] = ids[0]
		contact, lerr := c.lookupContact(ctx, ids[0])
		if lerr != nil {
			warnings = append(warnings, fmt.Sprintf("hubspot: contact %s: %v", ids[0], lerr))
			out.Contact = ids[0]
		} else {
			out.Contact = contact.name()
			if contact.Properties.Email != "" {
				fields["contactEmail"] = contact.Properties.Email
			}
		}
	}
	if ids := t.assocIDs("compan"); len(ids) > 0 {
		out.CustomerID = ids[0]
		name, lerr := c.lookupCompany(ctx, ids[0])
		if lerr != nil {
			warnings = append(warnings, fmt.Sprintf("hubspot: company %s: %v", ids[0], lerr))
		} else {
			out.Customer = name
		}
	}

	if portal := c.portalIDOnce(ctx); portal != "" {
		out.URL = "https://app.hubspot.com/contacts/" + portal + "/ticket/" + id
	} else {
		warnings = append(warnings, "hubspot: portal id unavailable, ticket URL omitted")
	}

	c.addWarnings(id, warnings)
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// --- Threads ---

// listMessages fetches every message on one conversation thread, paging
// with the cursor HubSpot returns until the feed ends or maxMessagePages
// is reached. Each page URL is built here from the configured host and the
// cursor, never followed as a server-supplied link, so the cap exists only
// to stop a pathological feed from hanging a run; reaching it is warned
// about, not treated as fatal.
func (c *Client) listMessages(ctx context.Context, threadID string) ([]convMessage, []string, error) {
	var all []convMessage
	after := ""
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(messagesPerPage))
		if after != "" {
			q.Set("after", after)
		}

		var pg convMessagePage
		path := "/conversations/v3/conversations/threads/" + url.PathEscape(threadID) + "/messages"
		if err := c.apiGET(ctx, path, q, &pg); err != nil {
			return nil, nil, err
		}
		all = append(all, pg.Results...)

		next := pg.Paging.Next.After
		if next == "" || next == after {
			return all, nil, nil
		}
		if page >= maxMessagePages {
			return all, []string{fmt.Sprintf("hubspot: message pages capped at %d", maxMessagePages)}, nil
		}
		after = next
	}
}

// messageRole maps a conversation message to a role and says whether it is
// internal. HubSpot has no explicit author-type field: the sender's
// actorId prefix encodes it — "V-" is a visitor (the customer), "A-" an
// agent, "S-" the system, "I-" an integration, "E-" a bare email address —
// and the message's own type is the public/private split, "COMMENT" being
// the internal note that never reaches the visitor.
func messageRole(m convMessage) (ticket.Role, bool) {
	if strings.EqualFold(m.Type, "COMMENT") {
		return ticket.RoleAgent, true
	}
	actor := ""
	if len(m.Senders) > 0 {
		actor = strings.ToUpper(strings.TrimSpace(m.Senders[0].ActorID))
	}
	switch {
	case strings.HasPrefix(actor, "V-"), strings.HasPrefix(actor, "E-"):
		return ticket.RoleCustomer, false
	case strings.HasPrefix(actor, "A-"):
		return ticket.RoleAgent, false
	case strings.HasPrefix(actor, "S-"), strings.HasPrefix(actor, "I-"):
		return ticket.RoleSystem, false
	default:
		// An unrecognised prefix is treated as staff, which is the safer
		// default: a message wrongly attributed to the customer would read
		// as the customer's own words.
		return ticket.RoleAgent, false
	}
}

// messageAuthor names the sender: the delivery identifier (an email
// address, usually) when there is one, then the sender's name, then the
// bare actor id — a sender Sirdar cannot resolve is still evidence of who
// wrote the message.
func messageAuthor(m convMessage) string {
	if len(m.Senders) == 0 {
		return ""
	}
	s := m.Senders[0]
	if v := strings.TrimSpace(s.DeliveryIdentifier.Value); v != "" {
		return v
	}
	if n := strings.TrimSpace(s.Name); n != "" {
		return n
	}
	return s.ActorID
}

// messageText prefers the plain text HubSpot already extracted, falling
// back to converting the rich-text HTML.
func messageText(m convMessage) string {
	if t := strings.TrimSpace(m.Text); t != "" {
		return t
	}
	text, _ := htmltext.ToMarkdown(m.RichText)
	return text
}

// threadMessages fetches every associated conversation thread's messages,
// in association order. A ticket with no conversation association — the
// association shape is the weakest-documented part of HubSpot's support
// model, and plenty of tickets are created without one — yields no
// messages and a warning saying so, rather than an error: the ticket's own
// content is still worth triaging.
func (c *Client) threadMessages(ctx context.Context, t crmTicket) ([]convMessage, []string, error) {
	threadIDs := t.assocIDs("conversation")
	if len(threadIDs) == 0 {
		return nil, []string{"hubspot: ticket has no associated conversation"}, nil
	}

	var msgs []convMessage
	var warnings []string
	for _, tid := range threadIDs {
		batch, warns, err := c.listMessages(ctx, tid)
		if err != nil {
			return nil, nil, err
		}
		msgs = append(msgs, batch...)
		warnings = append(warnings, warns...)
	}
	return msgs, warnings, nil
}

// Threads returns the ticket's conversation: the ticket's own content as
// the first message (the customer's original description, which HubSpot
// keeps on the CRM record rather than in the inbox thread), then every
// message on each associated conversation thread.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	t, err := c.fetchTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	msgs, warnings, err := c.threadMessages(ctx, t)
	if err != nil {
		return nil, err
	}

	props := t.Properties
	if props == nil {
		props = map[string]string{}
	}
	out := make(ticket.Thread, 0, len(msgs)+1)

	content := strings.TrimSpace(props["content"])
	if content != "" {
		text, _ := htmltext.ToMarkdown(content)
		author := ""
		if ids := t.assocIDs("contact"); len(ids) > 0 {
			if contact, lerr := c.lookupContact(ctx, ids[0]); lerr == nil {
				author = contact.name()
			} else {
				author = ids[0]
			}
		}
		out = append(out, ticket.Message{
			At:     parseTime(firstNonEmpty(props["createdate"], t.CreatedAt)),
			Author: author,
			Role:   ticket.RoleCustomer,
			Text:   text,
		})
	}

	for _, m := range msgs {
		text := messageText(m)
		if text == "" && len(m.Attachments) == 0 {
			continue
		}
		role, internal := messageRole(m)
		author := messageAuthor(m)
		if internal {
			author += " (internal)"
		}
		out = append(out, ticket.Message{
			At:            parseTime(m.CreatedAt),
			Author:        author,
			Role:          role,
			Text:          text,
			AttachmentIDs: attachmentIDs(m),
		})
	}

	c.addWarnings(id, warnings)
	return out, nil
}
