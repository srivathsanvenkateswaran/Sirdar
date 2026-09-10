package zendesk

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

// --- Ticket ---

type zendeskUser struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"` // "end-user" | "agent" | "admin"
}

type zendeskOrganization struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type zendeskVia struct {
	Channel string `json:"channel"`
}

type zendeskTicket struct {
	ID             int64      `json:"id"`
	Subject        string     `json:"subject"`
	Status         string     `json:"status"`
	Priority       string     `json:"priority"`
	Via            zendeskVia `json:"via"`
	RequesterID    int64      `json:"requester_id"`
	OrganizationID int64      `json:"organization_id"`
	GroupID        int64      `json:"group_id"`
	AssigneeID     int64      `json:"assignee_id"`
	Tags           []string   `json:"tags"`
	Type           string     `json:"type"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type ticketEnvelope struct {
	Ticket        zendeskTicket         `json:"ticket"`
	Users         []zendeskUser         `json:"users"`
	Organizations []zendeskOrganization `json:"organizations"`
}

// Get fetches a ticket, with its requester and organization side-loaded,
// and maps it to ticket.HelpdeskTicket.
func (c *Client) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	q := url.Values{}
	q.Set("include", "users,organizations")

	var env ticketEnvelope
	if err := c.getJSON(ctx, "/api/v2/tickets/"+id+".json", q, &env); err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	t := env.Ticket

	usersByID := make(map[int64]zendeskUser, len(env.Users))
	for _, u := range env.Users {
		usersByID[u.ID] = u
	}

	contact := ""
	if u, ok := usersByID[t.RequesterID]; ok {
		contact = u.Name
	}
	customer := ""
	for _, o := range env.Organizations {
		if o.ID == t.OrganizationID {
			customer = o.Name
			break
		}
	}
	var customerID string
	if t.OrganizationID != 0 {
		customerID = strconv.FormatInt(t.OrganizationID, 10)
	}

	fields := map[string]string{}
	if len(t.Tags) > 0 {
		fields["tags"] = strings.Join(t.Tags, ",")
	}
	if t.Type != "" {
		fields["type"] = t.Type
	}
	if t.GroupID != 0 {
		fields["group_id"] = strconv.FormatInt(t.GroupID, 10)
	}
	if t.AssigneeID != 0 {
		assignee := strconv.FormatInt(t.AssigneeID, 10)
		if u, ok := usersByID[t.AssigneeID]; ok && u.Name != "" {
			assignee = u.Name
		}
		fields["assignee"] = assignee
	}

	ticketID := id
	if t.ID != 0 {
		ticketID = strconv.FormatInt(t.ID, 10)
	}

	return ticket.HelpdeskTicket{
		ID:         ticketID,
		Subject:    t.Subject,
		Status:     t.Status,
		Priority:   t.Priority,
		Channel:    t.Via.Channel,
		Contact:    contact,
		Customer:   customer,
		CustomerID: customerID,
		URL:        fmt.Sprintf("https://%s.zendesk.com/agent/tickets/%s", c.subdomain, ticketID),
		CreatedAt:  t.CreatedAt,
		UpdatedAt:  t.UpdatedAt,
		Fields:     fields,
	}, nil
}

// --- Comments / threads ---

type zendeskCommentAttachment struct {
	ID          int64  `json:"id"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	ContentURL  string `json:"content_url"`
}

type zendeskComment struct {
	ID          int64                      `json:"id"`
	Type        string                     `json:"type"`
	AuthorID    int64                      `json:"author_id"`
	Body        string                     `json:"body"`
	HTMLBody    string                     `json:"html_body"`
	PlainBody   string                     `json:"plain_body"`
	Public      bool                       `json:"public"`
	CreatedAt   time.Time                  `json:"created_at"`
	Attachments []zendeskCommentAttachment `json:"attachments"`
}

type commentsPage struct {
	Comments []zendeskComment `json:"comments"`
	Users    []zendeskUser    `json:"users"`
	NextPage *string          `json:"next_page"`
}

// requesterID fetches the ticket's requester_id, the primary signal
// Threads uses to tell a customer's comment from an agent's.
func (c *Client) requesterID(ctx context.Context, id string) (int64, error) {
	var env ticketEnvelope
	if err := c.getJSON(ctx, "/api/v2/tickets/"+id+".json", nil, &env); err != nil {
		return 0, err
	}
	return env.Ticket.RequesterID, nil
}

// fetchComments returns every comment on ticket id, in the order the
// Zendesk API returns them (creation order), following next_page until
// exhausted, along with every side-loaded user keyed by id.
//
// Every request this makes carries the live Authorization header, so
// next_page is not followed blindly: it must parse as https and name this
// client's own configured host, or pagination stops where it is (returning
// everything collected so far, no error) and a warning names the untrusted
// host. Pagination also stops, with a warning, after maxCommentPages pages,
// so a misbehaving or malicious feed cannot loop this call forever.
func (c *Client) fetchComments(ctx context.Context, id string) ([]zendeskComment, map[int64]zendeskUser, []string, error) {
	q := url.Values{}
	q.Set("include", "users")
	next := c.baseURL + "/api/v2/tickets/" + id + "/comments.json?" + q.Encode()

	var comments []zendeskComment
	var warnings []string
	usersByID := map[int64]zendeskUser{}

	for pages := 0; next != ""; pages++ {
		var page commentsPage
		if err := c.getJSONAbsolute(ctx, next, &page); err != nil {
			return nil, nil, nil, err
		}
		comments = append(comments, page.Comments...)
		for _, u := range page.Users {
			usersByID[u.ID] = u
		}

		next = ""
		if page.NextPage == nil || *page.NextPage == "" {
			continue
		}
		if pages+1 >= maxCommentPages {
			warnings = append(warnings, fmt.Sprintf("zendesk: comments pagination stopped after %d pages", maxCommentPages))
			continue
		}
		trustedURL, host, trusted := c.trustedNextPage(*page.NextPage)
		if !trusted {
			warnings = append(warnings, fmt.Sprintf("zendesk: next_page host not trusted: %s", host))
			continue
		}
		next = trustedURL
	}
	return comments, usersByID, warnings, nil
}

// Threads fetches the full, ordered comment thread for a ticket. Role is
// customer when the comment's author is the ticket's requester or the
// side-loaded author's role is "end-user", else agent; a non-public
// comment's Author is suffixed " (internal)". Text prefers plain_body,
// falling back to htmltext.ToMarkdown(html_body).
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	requesterID, err := c.requesterID(ctx, id)
	if err != nil {
		return nil, err
	}
	comments, usersByID, warnings, err := c.fetchComments(ctx, id)
	if err != nil {
		return nil, err
	}
	c.addWarnings(id, warnings)

	msgs := make(ticket.Thread, 0, len(comments))
	for _, cm := range comments {
		role := ticket.RoleAgent
		if cm.AuthorID == requesterID {
			role = ticket.RoleCustomer
		} else if u, ok := usersByID[cm.AuthorID]; ok && u.Role == "end-user" {
			role = ticket.RoleCustomer
		}

		author := ""
		if u, ok := usersByID[cm.AuthorID]; ok {
			author = u.Name
		}
		if author == "" {
			author = strconv.FormatInt(cm.AuthorID, 10)
		}
		if !cm.Public {
			author += " (internal)"
		}

		text := cm.PlainBody
		if text == "" {
			text, _ = htmltext.ToMarkdown(cm.HTMLBody)
		}

		var attachmentIDs []string
		for _, a := range cm.Attachments {
			attachmentIDs = append(attachmentIDs, strconv.FormatInt(a.ID, 10))
		}

		msgs = append(msgs, ticket.Message{
			At:            cm.CreatedAt,
			Author:        author,
			Role:          role,
			Text:          text,
			AttachmentIDs: attachmentIDs,
		})
	}
	return msgs, nil
}
