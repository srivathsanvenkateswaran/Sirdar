// Package zohodesk implements source.Helpdesk against the Zoho Desk v1 REST
// API: ticket lookup, threaded conversation retrieval, and attachment
// download.
package zohodesk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Client is a Zoho Desk API client implementing source.Helpdesk.
type Client struct {
	BaseURL string
	OrgID   string
	Tokens  TokenSource
	HTTP    *http.Client

	// mu guards warnings and lastID. One Client serves every run in a
	// batch, so two tickets can be inside Attachments at the same time.
	mu sync.Mutex
	// warnings holds the non-fatal problems (individual attachment
	// download failures) each Attachments call recorded, keyed by the
	// ticket id it was called with, so one ticket's skipped attachment
	// cannot be reported against another's. An entry is written when the
	// call ends and removed when it is read.
	warnings map[string][]string
	// lastID is the ticket whose Attachments call finished most recently,
	// which is what the argument-less source.Warner interface can offer.
	lastID string
}

// New returns a Client configured to talk to baseURL as organization orgID,
// taking each request's access token from ts.
func New(baseURL, orgID string, ts TokenSource) *Client {
	return &Client{
		BaseURL: baseURL,
		OrgID:   orgID,
		Tokens:  ts,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithToken returns a Client that authenticates with one fixed access
// token, which Zoho Desk stops honouring an hour after it was issued.
func NewWithToken(baseURL, orgID, token string) *Client {
	return New(baseURL, orgID, StaticToken(token))
}

// get issues an authenticated GET against path (relative to BaseURL) with
// the given query values, and decodes a 2xx JSON response into out. Non-2xx
// responses are mapped to a *source.Error.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	body, err := c.getRaw(ctx, path, query)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("zoho desk: GET %s: decode: %v", path, err)}
	}
	return nil
}

// getRaw issues an authenticated GET and returns the raw response body for a
// 2xx response, mapping non-2xx responses to a *source.Error.
func (c *Client) getRaw(ctx context.Context, path string, query url.Values) ([]byte, error) {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zoho desk: GET %s: %v", path, err)}
	}

	resp, err := c.send(ctx, req)
	if err != nil {
		var serr *source.Error
		if errors.As(err, &serr) {
			return nil, serr
		}
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zoho desk: GET %s: %v", path, err)}
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusError(http.MethodGet, path, resp.StatusCode, body)
	}
	if readErr != nil {
		return nil, &source.Error{Code: source.Internal, Message: fmt.Sprintf("zoho desk: GET %s: read body: %v", path, readErr)}
	}
	return body, nil
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// setHeaders puts the org id and a current access token on req, returning
// the token it used so a rejection can name it.
func (c *Client) setHeaders(ctx context.Context, req *http.Request) (string, error) {
	if c.Tokens == nil {
		return "", &source.Error{Code: source.Auth, Message: "zoho desk: no token source configured"}
	}
	token, err := c.Tokens.Token(ctx)
	if err != nil {
		return "", err
	}
	req.Header.Set("orgId", c.OrgID)
	req.Header.Set("Authorization", "Zoho-oauthtoken "+token)
	return token, nil
}

// send issues an authenticated request, retrying it once with a freshly
// minted access token when Desk answers 401. An access token can stop
// working before the expiry it was issued with — revoked, or invalidated by
// another client refreshing the same grant — and a whole triage run should
// not fail for the second it takes to mint another. Only one retry: a 401
// that survives a new token is a real authentication failure.
//
// Every request this client makes is a bodyless GET, so replaying one is
// free of the usual re-send problem.
func (c *Client) send(ctx context.Context, req *http.Request) (*http.Response, error) {
	token, err := c.setHeaders(ctx, req)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	refresher, ok := c.Tokens.(Refresher)
	if !ok {
		return resp, nil
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	refresher.Invalidate(token)

	retry := req.Clone(ctx)
	if _, err := c.setHeaders(ctx, retry); err != nil {
		return nil, err
	}
	return c.http().Do(retry)
}

// statusError maps a non-2xx HTTP response to a *source.Error.
func statusError(method, path string, status int, body []byte) *source.Error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &source.Error{Code: source.Auth, Message: fmt.Sprintf("zoho desk: %s %s: %d", method, path, status)}
	case http.StatusNotFound:
		return &source.Error{Code: source.NotFound, Message: fmt.Sprintf("zoho desk: %s %s: %d", method, path, status)}
	case http.StatusTooManyRequests:
		return &source.Error{Code: source.RateLimited, Message: fmt.Sprintf("zoho desk: %s %s: %d", method, path, status)}
	default:
		snippet := body
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return &source.Error{Code: source.Internal, Message: fmt.Sprintf("zoho desk: %s %s: %d: %s", method, path, status, snippet)}
	}
}

// --- Ticket ---

type zohoContact struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type zohoCustomFields struct {
	CompanyID string `json:"cf_company_id"`
}

type zohoTicket struct {
	ID           string           `json:"id"`
	Subject      string           `json:"subject"`
	Status       string           `json:"status"`
	Priority     string           `json:"priority"`
	Channel      string           `json:"channel"`
	Contact      zohoContact      `json:"contact"`
	AccountName  string           `json:"accountName"`
	CF           zohoCustomFields `json:"cf"`
	WebURL       string           `json:"webUrl"`
	CreatedTime  string           `json:"createdTime"`
	ModifiedTime string           `json:"modifiedTime"`
	DepartmentID string           `json:"departmentId"`
	TicketNumber string           `json:"ticketNumber"`
	Email        string           `json:"email"`
	Phone        string           `json:"phone"`
}

// Get fetches a ticket and maps it to ticket.HelpdeskTicket.
func (c *Client) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	var zt zohoTicket
	if err := c.get(ctx, "/api/v1/tickets/"+id, nil, &zt); err != nil {
		return ticket.HelpdeskTicket{}, err
	}

	fields := map[string]string{}
	if zt.DepartmentID != "" {
		fields["departmentId"] = zt.DepartmentID
	}
	if zt.TicketNumber != "" {
		fields["ticketNumber"] = zt.TicketNumber
	}
	if zt.Email != "" {
		fields["email"] = zt.Email
	}
	if zt.Phone != "" {
		fields["phone"] = zt.Phone
	}

	return ticket.HelpdeskTicket{
		ID:         zt.ID,
		Subject:    zt.Subject,
		Status:     zt.Status,
		Priority:   zt.Priority,
		Channel:    zt.Channel,
		Contact:    strings.TrimSpace(zt.Contact.FirstName + " " + zt.Contact.LastName),
		Customer:   zt.AccountName,
		CustomerID: zt.CF.CompanyID,
		URL:        zt.WebURL,
		CreatedAt:  parseZohoTime(zt.CreatedTime),
		UpdatedAt:  parseZohoTime(zt.ModifiedTime),
		Fields:     fields,
	}, nil
}

// parseZohoTime parses a Zoho Desk timestamp, which is RFC3339 with
// fractional seconds (e.g. "2026-09-10T08:30:00.000Z"), falling back to
// plain RFC3339. An empty or unparseable string yields the zero time.
func parseZohoTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// --- Conversations / threads ---

type zohoAttachmentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Href string `json:"href"`
}

type zohoNamed struct {
	Name string `json:"name"`
}

type zohoConversationEntry struct {
	ID            string              `json:"id"`
	Type          string              `json:"type"` // "thread" | "comment"
	Direction     string              `json:"direction"`
	Author        zohoNamed           `json:"author"`
	CreatedTime   string              `json:"createdTime"`
	Commenter     zohoNamed           `json:"commenter"`
	CommenterType string              `json:"commenterType"`
	IsPublic      *bool               `json:"isPublic"`
	Content       string              `json:"content"`
	CommentedTime string              `json:"commentedTime"`
	Attachments   []zohoAttachmentRef `json:"attachments"`
}

type zohoConversationsPage struct {
	Data []zohoConversationEntry `json:"data"`
}

type zohoThreadDetail struct {
	ID          string              `json:"id"`
	PlainText   string              `json:"plainText"`
	Summary     string              `json:"summary"`
	Content     string              `json:"content"`
	Attachments []zohoAttachmentRef `json:"attachments"`
}

const conversationsPageSize = 100

// listConversations fetches every conversation entry for a ticket, paging
// with the from offset until a page returns fewer than
// conversationsPageSize entries.
func (c *Client) listConversations(ctx context.Context, id string) ([]zohoConversationEntry, error) {
	var all []zohoConversationEntry
	from := 0
	for {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(conversationsPageSize))
		q.Set("from", strconv.Itoa(from))

		var page zohoConversationsPage
		if err := c.get(ctx, "/api/v1/tickets/"+id+"/conversations", q, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Data...)
		if len(page.Data) < conversationsPageSize {
			break
		}
		from += conversationsPageSize
	}
	return all, nil
}

// getThread fetches a single thread's detail.
func (c *Client) getThread(ctx context.Context, ticketID, threadID string) (zohoThreadDetail, error) {
	q := url.Values{}
	q.Set("include", "plainText")

	var td zohoThreadDetail
	if err := c.get(ctx, "/api/v1/tickets/"+ticketID+"/threads/"+threadID, q, &td); err != nil {
		return zohoThreadDetail{}, err
	}
	return td, nil
}

// Threads fetches the full conversation for a ticket, resolving each thread
// entry's detail and mapping comment entries directly, sorted ascending by
// message time.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	entries, err := c.listConversations(ctx, id)
	if err != nil {
		return nil, err
	}

	inlineCounter := 0
	msgs := make(ticket.Thread, 0, len(entries))
	for _, e := range entries {
		switch e.Type {
		case "thread":
			td, err := c.getThread(ctx, id, e.ID)
			if err != nil {
				return nil, err
			}
			text := td.PlainText
			if text == "" {
				text = td.Summary
			}
			if text == "" {
				text = td.Content
			}
			role := ticket.RoleAgent
			if e.Direction == "in" {
				role = ticket.RoleCustomer
			}
			refs := c.collectEntryAttachments(td.Attachments, td.Content, &inlineCounter)
			msgs = append(msgs, ticket.Message{
				At:            parseZohoTime(e.CreatedTime),
				Author:        e.Author.Name,
				Role:          role,
				Text:          text,
				AttachmentIDs: attachmentIDs(refs),
			})
		case "comment":
			// Both CONTACT and END_USER commenter types identify the customer
			// side of the conversation; everything else defaults to agent.
			role := ticket.RoleAgent
			if e.CommenterType == "CONTACT" || e.CommenterType == "END_USER" {
				role = ticket.RoleCustomer
			}
			author := e.Commenter.Name
			if e.IsPublic != nil && !*e.IsPublic {
				author += " (internal)"
			}
			refs := c.collectEntryAttachments(e.Attachments, e.Content, &inlineCounter)
			msgs = append(msgs, ticket.Message{
				At:            parseZohoTime(e.CommentedTime),
				Author:        author,
				Role:          role,
				Text:          e.Content,
				AttachmentIDs: attachmentIDs(refs),
			})
		}
	}

	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].At.Before(msgs[j].At) })
	return msgs, nil
}

func attachmentIDs(refs []attachmentRef) []string {
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, len(refs))
	for i, r := range refs {
		ids[i] = r.ID
	}
	return ids
}
