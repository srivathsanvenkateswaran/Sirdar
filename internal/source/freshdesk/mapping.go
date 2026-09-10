package freshdesk

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

type fdRequester struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type fdCompany struct {
	Name string `json:"name"`
}

type fdAttachment struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	ContentType   string `json:"content_type"`
	AttachmentURL string `json:"attachment_url"`
}

type fdTicket struct {
	ID              int64          `json:"id"`
	Subject         string         `json:"subject"`
	Status          int            `json:"status"`
	Priority        int            `json:"priority"`
	Source          int            `json:"source"`
	RequesterID     int64          `json:"requester_id"`
	CompanyID       *int64         `json:"company_id"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
	Description     string         `json:"description"`
	DescriptionText string         `json:"description_text"`
	Tags            []string       `json:"tags"`
	Type            string         `json:"type"`
	GroupID         *int64         `json:"group_id"`
	ResponderID     *int64         `json:"responder_id"`
	Requester       *fdRequester   `json:"requester"`
	Company         *fdCompany     `json:"company"`
	Attachments     []fdAttachment `json:"attachments"`
}

type fdConversation struct {
	ID          int64          `json:"id"`
	Body        string         `json:"body"`
	BodyText    string         `json:"body_text"`
	Incoming    bool           `json:"incoming"`
	Private     bool           `json:"private"`
	UserID      *int64         `json:"user_id"`
	FromEmail   string         `json:"from_email"`
	CreatedAt   string         `json:"created_at"`
	Attachments []fdAttachment `json:"attachments"`
}

// --- status / priority / channel enums ---

// statusName maps Freshdesk's numeric ticket status. Values outside the
// four documented ones (a workspace's custom statuses, which Freshdesk
// numbers starting at 6) fall back to "status-<n>" rather than being
// silently blanked.
func statusName(n int) string {
	switch n {
	case 2:
		return "Open"
	case 3:
		return "Pending"
	case 4:
		return "Resolved"
	case 5:
		return "Closed"
	default:
		return "status-" + strconv.Itoa(n)
	}
}

// priorityName maps Freshdesk's numeric ticket priority.
func priorityName(n int) string {
	switch n {
	case 1:
		return "Low"
	case 2:
		return "Medium"
	case 3:
		return "High"
	case 4:
		return "Urgent"
	default:
		return "priority-" + strconv.Itoa(n)
	}
}

// channelName maps Freshdesk's numeric ticket source (creation channel).
// Only the values documented on Freshdesk's own API reference are named;
// anything else falls back to "source-<n>".
func channelName(n int) string {
	switch n {
	case 1:
		return "email"
	case 2:
		return "portal"
	case 3:
		return "phone"
	case 7:
		return "chat"
	case 9:
		return "feedback_widget"
	case 10:
		return "outbound_email"
	default:
		return "source-" + strconv.Itoa(n)
	}
}

// parseFreshdeskTime parses a Freshdesk timestamp (RFC3339, e.g.
// "2026-09-10T08:30:00Z"). An empty or unparseable string yields the zero
// time rather than failing the whole call over one cosmetic field.
func parseFreshdeskTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}

// mapTicket converts a decoded ticket into ticket.HelpdeskTicket. domain is
// the configured account domain (not necessarily the host requests were
// actually sent to in a test), used to build the human-facing ticket URL.
func mapTicket(domain string, ft fdTicket) ticket.HelpdeskTicket {
	fields := map[string]string{}
	if len(ft.Tags) > 0 {
		fields["tags"] = strings.Join(ft.Tags, ", ")
	}
	if ft.Type != "" {
		fields["type"] = ft.Type
	}
	if ft.GroupID != nil {
		fields["group_id"] = strconv.FormatInt(*ft.GroupID, 10)
	}
	if ft.ResponderID != nil {
		fields["responder_id"] = strconv.FormatInt(*ft.ResponderID, 10)
	}
	if ft.DescriptionText != "" {
		fields["description_text"] = ft.DescriptionText
	}

	var customerID string
	if ft.CompanyID != nil {
		customerID = strconv.FormatInt(*ft.CompanyID, 10)
	}
	var customer string
	if ft.Company != nil {
		customer = ft.Company.Name
	}
	var contact string
	if ft.Requester != nil {
		contact = ft.Requester.Name
	}

	return ticket.HelpdeskTicket{
		ID:         strconv.FormatInt(ft.ID, 10),
		Subject:    ft.Subject,
		Status:     statusName(ft.Status),
		Priority:   priorityName(ft.Priority),
		Channel:    channelName(ft.Source),
		Contact:    contact,
		Customer:   customer,
		CustomerID: customerID,
		URL:        fmt.Sprintf("https://%s/a/tickets/%d", domain, ft.ID),
		CreatedAt:  parseFreshdeskTime(ft.CreatedAt),
		UpdatedAt:  parseFreshdeskTime(ft.UpdatedAt),
		Fields:     fields,
	}
}

// fetchTicket fetches a ticket with its requester, company and stats
// side-loaded, for both Get and Threads/Attachments (which need the
// description as the thread's first message and the ticket-level
// attachments).
func (c *Client) fetchTicket(ctx context.Context, id string) (fdTicket, error) {
	q := url.Values{}
	q.Set("include", "requester,company,stats")

	var ft fdTicket
	if err := c.apiGET(ctx, "/api/v2/tickets/"+id, q, &ft); err != nil {
		return fdTicket{}, err
	}
	return ft, nil
}

// Get fetches a ticket and maps it to ticket.HelpdeskTicket.
func (c *Client) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	ft, err := c.fetchTicket(ctx, id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	return mapTicket(c.cfg.Domain, ft), nil
}

// maxConversationPages bounds how many pages listConversations will fetch.
// Every page URL is built by this client from the configured domain and a
// page number it counts itself — never from a server-supplied "next" link
// or Location header — so this cap exists only to stop an unbounded sweep
// against a ticket with a pathological number of conversation entries from
// hanging a triage run; a page count above it is warned about, not treated
// as a fatal error. It is a var, not a const, so a test can shrink it
// without needing a 100-page fixture.
var maxConversationPages = 100

// listConversations fetches every conversation entry for a ticket, paging
// with page/per_page until a page returns fewer than conversationsPerPage
// entries or maxConversationPages is reached. In the latter case the
// entries fetched so far are returned along with a warning: a ticket this
// long is unusual enough that Sirdar would rather triage it with a
// truncated thread and a visible note than fail outright.
func (c *Client) listConversations(ctx context.Context, id string) ([]fdConversation, []string, error) {
	var all []fdConversation
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(conversationsPerPage))

		var batch []fdConversation
		if err := c.apiGET(ctx, "/api/v2/tickets/"+id+"/conversations", q, &batch); err != nil {
			return nil, nil, err
		}
		all = append(all, batch...)
		if len(batch) < conversationsPerPage {
			return all, nil, nil
		}
		if page >= maxConversationPages {
			warning := fmt.Sprintf("freshdesk: conversation pages capped at %d", maxConversationPages)
			return all, []string{warning}, nil
		}
	}
}

// resolveAuthor names the sender of a conversation entry: the raw email
// address when Freshdesk gave one (customer-originated entries always
// carry from_email), otherwise the resolved agent name for user_id, falling
// back to the bare id when the lookup fails — a name Sirdar cannot resolve
// is still evidence of who sent the message.
func (c *Client) resolveAuthor(ctx context.Context, cv fdConversation) string {
	if cv.FromEmail != "" {
		return cv.FromEmail
	}
	if cv.UserID != nil {
		if name, err := c.lookupAgent(ctx, *cv.UserID); err == nil && name != "" {
			return name
		}
		return strconv.FormatInt(*cv.UserID, 10)
	}
	return "unknown"
}

// Threads fetches the full conversation for a ticket: the ticket's own
// description as the first message (from the requester), followed by every
// conversation entry, ordered by timestamp.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	// What an earlier call in the same bundle recorded stays where it is:
	// warnings accumulate under the ticket and the reader clears them.
	ft, err := c.fetchTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	convs, warnings, err := c.listConversations(ctx, id)
	if err != nil {
		return nil, err
	}

	inline := 0
	msgs := make(ticket.Thread, 0, len(convs)+1)

	descText := ft.DescriptionText
	if descText == "" {
		descText, _ = htmltext.ToMarkdown(ft.Description)
	}
	var contact string
	if ft.Requester != nil {
		contact = ft.Requester.Name
	}
	msgs = append(msgs, ticket.Message{
		At:            parseFreshdeskTime(ft.CreatedAt),
		Author:        contact,
		Role:          ticket.RoleCustomer,
		Text:          descText,
		AttachmentIDs: refIDs(collectRefs(ft.Attachments, ft.Description, &inline)),
	})

	for _, cv := range convs {
		role := ticket.RoleAgent
		if cv.Incoming || (cv.UserID != nil && ft.RequesterID != 0 && *cv.UserID == ft.RequesterID) {
			role = ticket.RoleCustomer
		}

		author := c.resolveAuthor(ctx, cv)
		if cv.Private {
			author += " (internal)"
		}

		text := cv.BodyText
		if text == "" {
			text, _ = htmltext.ToMarkdown(cv.Body)
		}

		msgs = append(msgs, ticket.Message{
			At:            parseFreshdeskTime(cv.CreatedAt),
			Author:        author,
			Role:          role,
			Text:          text,
			AttachmentIDs: refIDs(collectRefs(cv.Attachments, cv.Body, &inline)),
		})
	}

	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].At.Before(msgs[j].At) })
	c.addWarnings(id, warnings)
	return msgs, nil
}
