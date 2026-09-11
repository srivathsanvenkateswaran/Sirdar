package helpscout

import (
	"context"
	"encoding/json"
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

// hsPerson is Help Scout's person shape, used for primaryCustomer,
// createdBy, and a thread's customer. Type is "user" for a Help Scout
// agent and "customer" for the person who wrote in.
type hsPerson struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	First string `json:"first"`
	Last  string `json:"last"`
	Email string `json:"email"`
}

// name renders a person as "First Last", falling back to the email
// address, then to the numeric id: a name Sirdar cannot resolve is still
// evidence of who wrote the message.
func (p *hsPerson) name() string {
	if p == nil {
		return ""
	}
	full := strings.TrimSpace(strings.TrimSpace(p.First) + " " + strings.TrimSpace(p.Last))
	if full != "" {
		return full
	}
	if p.Email != "" {
		return p.Email
	}
	if p.ID != 0 {
		return strconv.FormatInt(p.ID, 10)
	}
	return ""
}

// hsTag is one of a conversation's tags. Help Scout returns tags as
// objects ({"id":1,"tag":"urgent"}), but a bare string is accepted too so
// a shape change does not fail the whole fetch over a cosmetic field.
type hsTag struct{ Tag string }

func (t *hsTag) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		t.Tag = s
		return nil
	}
	var obj struct {
		Tag  string `json:"tag"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	if obj.Tag != "" {
		t.Tag = obj.Tag
	} else {
		t.Tag = obj.Name
	}
	return nil
}

type hsAttachment struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	Links    struct {
		Data struct {
			Href string `json:"href"`
		} `json:"data"`
	} `json:"_links"`
}

type hsThread struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	State     string    `json:"state"`
	Status    string    `json:"status"`
	Body      string    `json:"body"`
	CreatedAt string    `json:"createdAt"`
	CreatedBy *hsPerson `json:"createdBy"`
	Customer  *hsPerson `json:"customer"`
	Embedded  struct {
		Attachments []hsAttachment `json:"attachments"`
	} `json:"_embedded"`
}

// hsLinks is the HAL link block Help Scout puts on a collection. Only
// "next" is read: every other link either repeats a URL this client built
// itself or points at a page it has already seen.
type hsLinks struct {
	Next struct {
		Href string `json:"href"`
	} `json:"next"`
}

// hsThreadPage is one page of the dedicated thread-list endpoint
// (`GET /v2/conversations/{id}/threads?page=N`), the only place a "next"
// link or a page count for the thread feed appears: the single-conversation
// `?embed=threads` response carries neither.
type hsThreadPage struct {
	Embedded struct {
		Threads []hsThread `json:"threads"`
	} `json:"_embedded"`
	Links hsLinks `json:"_links"`
	Page  struct {
		TotalPages int `json:"totalPages"`
	} `json:"page"`
}

type hsConversation struct {
	ID              int64     `json:"id"`
	Number          int64     `json:"number"`
	Subject         string    `json:"subject"`
	Status          string    `json:"status"`
	State           string    `json:"state"`
	Type            string    `json:"type"`
	MailboxID       int64     `json:"mailboxId"`
	Tags            []hsTag   `json:"tags"`
	CreatedAt       string    `json:"createdAt"`
	UserUpdatedAt   string    `json:"userUpdatedAt"`
	ClosedAt        string    `json:"closedAt"`
	PrimaryCustomer *hsPerson `json:"primaryCustomer"`
	Assignee        *hsPerson `json:"assignee"`
	Embedded        struct {
		Threads []hsThread `json:"threads"`
	} `json:"_embedded"`
}

// parseTime parses a Help Scout timestamp (RFC3339, e.g.
// "2026-09-10T08:30:00Z"). An empty or unparseable value yields the zero
// time rather than failing the whole call over one cosmetic field.
func parseTime(s string) time.Time {
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

// --- Get ---

// fetchConversation fetches a conversation with its threads embedded, the
// one call Get, Threads and Attachments all start from.
func (c *Client) fetchConversation(ctx context.Context, id string) (hsConversation, error) {
	q := url.Values{}
	q.Set("embed", "threads")

	var conv hsConversation
	if err := c.apiGET(ctx, "/v2/conversations/"+url.PathEscape(id), q, &conv); err != nil {
		return hsConversation{}, err
	}
	return conv, nil
}

// mapConversation converts a decoded conversation into
// ticket.HelpdeskTicket. Help Scout has no priority field — it leans on
// status, tags and custom fields instead — so Priority is left empty
// rather than guessed at from a tag.
func mapConversation(id string, conv hsConversation) ticket.HelpdeskTicket {
	fields := map[string]string{}
	if conv.MailboxID != 0 {
		fields["mailboxId"] = strconv.FormatInt(conv.MailboxID, 10)
	}
	if len(conv.Tags) > 0 {
		names := make([]string, 0, len(conv.Tags))
		for _, t := range conv.Tags {
			if t.Tag != "" {
				names = append(names, t.Tag)
			}
		}
		if len(names) > 0 {
			fields["tags"] = strings.Join(names, ", ")
		}
	}
	if conv.Number != 0 {
		fields["number"] = strconv.FormatInt(conv.Number, 10)
	}
	if conv.State != "" {
		fields["state"] = conv.State
	}
	if a := conv.Assignee.name(); a != "" {
		fields["assignee"] = a
	}

	out := ticket.HelpdeskTicket{
		ID:        id,
		Subject:   conv.Subject,
		Status:    conv.Status,
		Channel:   conv.Type,
		Contact:   conv.PrimaryCustomer.name(),
		CreatedAt: parseTime(conv.CreatedAt),
		UpdatedAt: parseTime(conv.UserUpdatedAt),
		Fields:    fields,
	}
	if conv.PrimaryCustomer != nil {
		if conv.PrimaryCustomer.Email != "" {
			fields["customerEmail"] = conv.PrimaryCustomer.Email
		}
		if conv.PrimaryCustomer.ID != 0 {
			out.CustomerID = strconv.FormatInt(conv.PrimaryCustomer.ID, 10)
		}
	}
	// The web URL is built from the id this client was asked for, not from
	// anything the API returned: a "view in Help Scout" link taken out of a
	// response body would be a link an operator clicks, pointed wherever
	// the response said.
	convID := id
	if conv.ID != 0 {
		convID = strconv.FormatInt(conv.ID, 10)
	}
	out.URL = "https://secure.helpscout.net/conversation/" + convID
	return out
}

// Get fetches a conversation and maps it to ticket.HelpdeskTicket.
func (c *Client) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	conv, err := c.fetchConversation(ctx, id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	return mapConversation(id, conv), nil
}

// --- Threads ---

// listThreads returns every thread on a conversation: the page embedded on
// the conversation itself (via `?embed=threads`), plus whatever the
// dedicated thread-list endpoint has beyond it.
//
// The embedded response carries no pagination link of its own — a
// realistic Help Scout response has only `_links.self` on it — so a full
// page (threadsPageSize entries) is the only signal that more threads might
// exist. When it is, the rest is fetched from
// `GET /v2/conversations/{id}/threads?page=N` starting at page 2, following
// that endpoint's own `_links.next` and `page.totalPages` until the feed
// ends or maxThreadPages total pages (the embedded one included) have been
// read. A "next" link arrives inside a response body, so it is checked
// against trust.Check before it is followed — an untrusted one stops
// pagination with a warning rather than being fetched.
func (c *Client) listThreads(ctx context.Context, id string, conv hsConversation) ([]hsThread, []string, error) {
	all := append([]hsThread(nil), conv.Embedded.Threads...)
	var warnings []string

	if len(conv.Embedded.Threads) < threadsPageSize {
		// A short first page is the whole feed: Help Scout would not hand
		// back fewer than a full page unless there was nothing left.
		return all, warnings, nil
	}

	pagesSeen := 1 // the embedded page already counts as page 1
	next := fmt.Sprintf("%s/v2/conversations/%s/threads?page=2", c.baseURL, url.PathEscape(id))
	for next != "" {
		u, err := url.Parse(next)
		if err != nil {
			warnings = append(warnings, "helpscout: thread page link is not a valid url")
			break
		}
		fetch, _, _ := c.trust.Check(u)
		if !fetch {
			warnings = append(warnings, fmt.Sprintf("helpscout: thread page host not trusted: %s", u.Hostname()))
			break
		}
		if pagesSeen >= maxThreadPages {
			warnings = append(warnings, fmt.Sprintf("helpscout: thread pages capped at %d", maxThreadPages))
			break
		}

		var pg hsThreadPage
		if err := c.apiGETAbsolute(ctx, next, &pg); err != nil {
			return nil, nil, err
		}
		all = append(all, pg.Embedded.Threads...)
		pagesSeen++
		if pg.Links.Next.Href == next {
			// A feed whose next link points at the page just read would
			// loop for ever; stop rather than trust it.
			warnings = append(warnings, "helpscout: thread pagination stopped on a self-referential next link")
			break
		}
		if pg.Page.TotalPages != 0 && pagesSeen >= pg.Page.TotalPages {
			break
		}
		next = pg.Links.Next.Href
	}
	return all, warnings, nil
}

// threadRole maps a thread's type to a role and says whether the message
// is an internal note. Help Scout's type field answers both of Sirdar's
// questions on its own, which no other vendor's model does:
//
//	customer            -> the person who wrote in
//	message, reply      -> a published staff reply
//	note                -> staff, internal only
//	lineitem            -> a state change with no body
//
// Anything else (chat, beaconchat, phone, forwardchild, forwardparent)
// falls back to who wrote it: createdBy.type "customer" is the customer,
// everyone else is staff.
func threadRole(th hsThread) (ticket.Role, bool) {
	switch strings.ToLower(th.Type) {
	case "customer":
		return ticket.RoleCustomer, false
	case "message", "reply":
		return ticket.RoleAgent, false
	case "note":
		return ticket.RoleAgent, true
	case "lineitem":
		return ticket.RoleSystem, false
	default:
		if th.CreatedBy != nil && strings.EqualFold(th.CreatedBy.Type, "customer") {
			return ticket.RoleCustomer, false
		}
		return ticket.RoleAgent, false
	}
}

// threadAuthor names the sender of a thread: whoever created it, falling
// back to the thread's customer for an inbound message that carries no
// createdBy.
func threadAuthor(th hsThread) string {
	if n := th.CreatedBy.name(); n != "" {
		return n
	}
	return th.Customer.name()
}

// Threads returns the conversation's ordered thread list. Drafts are left
// out: an unsent reply is not part of the conversation that happened.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	conv, err := c.fetchConversation(ctx, id)
	if err != nil {
		return nil, err
	}
	threads, warnings, err := c.listThreads(ctx, id, conv)
	if err != nil {
		return nil, err
	}

	msgs := make(ticket.Thread, 0, len(threads))
	for _, th := range threads {
		if strings.EqualFold(th.State, "draft") {
			continue
		}
		role, internal := threadRole(th)
		author := threadAuthor(th)
		if internal {
			author += " (internal)"
		}
		text, _ := htmltext.ToMarkdown(th.Body)

		var ids []string
		for _, a := range th.Embedded.Attachments {
			ids = append(ids, strconv.FormatInt(a.ID, 10))
		}
		msgs = append(msgs, ticket.Message{
			At:            parseTime(th.CreatedAt),
			Author:        author,
			Role:          role,
			Text:          text,
			AttachmentIDs: ids,
		})
	}

	// Help Scout returns threads newest first; Sirdar's thread is oldest
	// first, and a stable sort keeps same-second entries in API order.
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].At.Before(msgs[j].At) })
	c.addWarnings(id, warnings)
	return msgs, nil
}
