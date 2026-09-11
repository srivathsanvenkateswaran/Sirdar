package servicenow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- wire types ---

// record is one Table API row, decoded field by field rather than into a
// struct because the API's field shape depends on how it was asked: with
// sysparm_display_value=true and sysparm_exclude_reference_link=true every
// field is a plain string, with either of them off a reference field is an
// object carrying "value" and "link". Decoding lazily means a proxy or an
// instance that answers in the other shape is read correctly instead of
// silently yielding empty strings.
type record map[string]json.RawMessage

// str reads one field as text, accepting either shape and preferring the
// display value — the name a human reads — when both are present.
func (r record) str(key string) string {
	raw, ok := r[key]
	if !ok || len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		DisplayValue string `json:"display_value"`
		Value        string `json:"value"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if v := strings.TrimSpace(obj.DisplayValue); v != "" {
			return v
		}
		return strings.TrimSpace(obj.Value)
	}
	// A number or a boolean: keep the literal rather than losing the field.
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

// timeLayouts covers the timestamp formats a ServiceNow instance serves.
// The first is the platform's own "yyyy-MM-dd HH:mm:ss"; the RFC 3339
// variants cover a proxy or a scoped API that answers in the web format.
var timeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z07:00",
	time.RFC3339Nano,
	time.RFC3339,
}

// parseTime reads a ServiceNow timestamp, yielding the zero time for an
// empty or unrecognised value rather than failing a whole ticket over a
// date. A naive timestamp is read as UTC: with
// sysparm_display_value=true the instance renders it in the integration
// user's display timezone and names no offset, so an instance whose
// integration user is not on UTC will be off by that offset — which is why
// the adapter docs ask for a UTC integration user.
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

// asText renders a field that may carry HTML. A journal entry is normally
// plain text, but an instance with a rich-text comment plugin, or an
// incident opened by an inbound email action, stores markup; running plain
// text through the HTML converter would collapse its line breaks, so the
// conversion is attempted only when there is markup to convert and the
// original is kept when the conversion yields nothing.
func asText(h string) string {
	if !strings.Contains(h, "<") {
		return h
	}
	text, _ := htmltext.ToMarkdown(h)
	if strings.TrimSpace(text) == "" {
		return h
	}
	return text
}

// --- shared record mapping ---

// fieldsOf gathers the record's secondary fields for the bundle's Fields
// map: the ones an agent reading the note would want and the note template
// does not have a column for. Empty values are left out rather than
// written as empty strings.
func fieldsOf(r record) map[string]string {
	fields := map[string]string{}
	for _, key := range []string{
		"number", "category", "subcategory", "urgency", "impact",
		"assignment_group", "opened_by", "close_notes", "resolved_at", "active",
	} {
		if v := r.str(key); v != "" {
			fields[key] = v
		}
	}
	if v := r.str("caller_id.email"); v != "" {
		fields["callerEmail"] = v
	}
	return fields
}

// Get implements source.Helpdesk: it resolves id — a record number
// (INC0010023) or a sys_id — and maps the record to a helpdesk ticket.
// The caller is the customer, the company they belong to is the account.
func (c *Client) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	rec, err := c.fetchRecord(ctx, id)
	if err != nil {
		return ticket.HelpdeskTicket{}, err
	}
	return c.mapHelpdesk(rec), nil
}

func (c *Client) mapHelpdesk(r record) ticket.HelpdeskTicket {
	sysID := r.str("sys_id")
	return ticket.HelpdeskTicket{
		ID:       firstNonEmpty(r.str("number"), sysID),
		Subject:  r.str("short_description"),
		Status:   r.str("state"),
		Priority: r.str("priority"),
		Channel:  r.str("contact_type"),
		Contact:  r.str("caller_id"),
		Customer: r.str("company"),
		// The company's sys_id is dot-walked rather than read off the
		// reference field: with sysparm_display_value=true the field
		// itself carries the name a human reads and no id at all, so an
		// instance that will not serve the dot-walk leaves this empty
		// rather than repeating the name as if it were an identifier.
		CustomerID: r.str("company.sys_id"),
		URL:        c.recordURL(sysID),
		CreatedAt:  parseTime(firstNonEmpty(r.str("opened_at"), r.str("sys_created_on"))),
		UpdatedAt:  parseTime(r.str("sys_updated_on")),
		Fields:     fieldsOf(r),
	}
}

// getTracker is Tracker.Get: the same record read as the work item rather
// than as the customer's complaint.
func (c *Client) getTracker(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	rec, err := c.fetchRecord(ctx, key)
	if err != nil {
		return ticket.TrackerTicket{}, err
	}
	return c.mapTracker(rec), nil
}

func (c *Client) mapTracker(r record) ticket.TrackerTicket {
	sysID := r.str("sys_id")
	return ticket.TrackerTicket{
		Key:         firstNonEmpty(r.str("number"), sysID),
		Title:       r.str("short_description"),
		Description: asText(r.str("description")),
		Priority:    r.str("priority"),
		Status:      r.str("state"),
		Assignee:    r.str("assigned_to"),
		URL:         c.recordURL(sysID),
		// A ServiceNow incident is both records at once, so the helpdesk
		// reference is the incident's own number: a workspace with
		// ServiceNow under sources.tracker and nothing under
		// sources.helpdesk still gets the conversation, because the wiring
		// layer picks up this client's helpdesk view for the same id.
		HelpdeskRef: firstNonEmpty(r.str("number"), sysID),
		CreatedAt:   parseTime(firstNonEmpty(r.str("opened_at"), r.str("sys_created_on"))),
		UpdatedAt:   parseTime(r.str("sys_updated_on")),
		Fields:      fieldsOf(r),
	}
}

// --- Threads ---

// journalEntry is one row of sys_journal_field: a single comment or work
// note, with the field it belongs to in element and the record it hangs
// off in element_id.
type journalEntry struct {
	sysID     string
	element   string
	value     string
	createdOn string
	createdBy string
}

// journalFields are the two journal fields a support conversation lives
// in: comments are what the caller sees, work notes are internal.
const (
	fieldComments  = "comments"
	fieldWorkNotes = "work_notes"
)

// Threads implements source.Helpdesk: the record's own description first —
// the caller's original words — then every journal entry in the order it
// was written.
//
// Roles follow ServiceNow's own visibility split rather than a guess: a
// work note never reaches the caller, so it is staff content and carries
// the " (internal)" author suffix; a comment is customer-visible, and is
// the caller's own words when the entry's author is the caller. An
// unrecognised author is treated as staff, which is the safer default —
// a message wrongly attributed to the customer reads as the customer's
// own words.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	rec, err := c.fetchRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	sysID := rec.str("sys_id")

	var warnings []string
	entries, jerr := c.journalEntries(ctx, sysID, &warnings)
	if jerr != nil {
		// sys_journal_field is ACL-restricted on plenty of instances, and
		// an integration user who can read incidents cannot always read
		// it. Losing the conversation is worth a warning, not a failed
		// bundle: the record itself is still evidence, and the record's
		// own comments/work_notes fields are not a substitute (they are
		// the same text without authors or times).
		warnings = append(warnings, fmt.Sprintf("servicenow: journal entries unavailable: %v", jerr))
	}

	caller := rec.str("caller_id")
	callerUser := rec.str("caller_id.user_name")

	msgs := make(ticket.Thread, 0, len(entries)+1)
	if body := asText(firstNonEmpty(rec.str("description"), rec.str("short_description"))); strings.TrimSpace(body) != "" {
		author := firstNonEmpty(caller, rec.str("opened_by"))
		// A record opened without a caller is staff content — an
		// engineer's own incident — not a customer's words.
		role := ticket.RoleAgent
		if caller != "" {
			role = ticket.RoleCustomer
		}
		msgs = append(msgs, ticket.Message{
			At:     parseTime(firstNonEmpty(rec.str("opened_at"), rec.str("sys_created_on"))),
			Author: author,
			Role:   role,
			Text:   body,
		})
	}

	for _, e := range entries {
		text := asText(e.value)
		if strings.TrimSpace(text) == "" {
			continue
		}
		author := e.createdBy
		role := ticket.RoleAgent
		if e.element == fieldWorkNotes {
			author += " (internal)"
		} else if isCaller(e.createdBy, caller, callerUser) {
			role = ticket.RoleCustomer
		}
		msgs = append(msgs, ticket.Message{
			At:     parseTime(e.createdOn),
			Author: author,
			Role:   role,
			Text:   text,
		})
	}

	c.addWarnings(id, warnings)
	return msgs, nil
}

// isCaller reports whether a journal entry's author is the record's
// caller. sys_created_by is a login name, so the dot-walked
// caller_id.user_name is the match that should fire; the display name is
// tried too, for an instance that will not serve the dot-walk.
func isCaller(createdBy, callerName, callerUser string) bool {
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		return false
	}
	if callerUser != "" && strings.EqualFold(createdBy, callerUser) {
		return true
	}
	return callerName != "" && strings.EqualFold(createdBy, callerName)
}

// journalEntries reads every comment and work note on a record, oldest
// first, paging with sysparm_offset until a short page ends the feed or
// the page cap stops it.
func (c *Client) journalEntries(ctx context.Context, sysID string, warnings *[]string) ([]journalEntry, error) {
	if sysID == "" {
		return nil, nil
	}
	var out []journalEntry
	offset := 0
	for page := 0; ; page++ {
		if page >= maxJournalPages {
			*warnings = append(*warnings, fmt.Sprintf("servicenow: journal stopped at the %d-page cap after %d entries; the thread may be incomplete", maxJournalPages, len(out)))
			break
		}
		q := url.Values{}
		q.Set("sysparm_display_value", "true")
		q.Set("sysparm_exclude_reference_link", "true")
		q.Set("sysparm_fields", "sys_id,element,element_id,value,sys_created_on,sys_created_by")
		q.Set("sysparm_query", "element_id="+encodedValue(sysID)+"^elementIN"+fieldComments+","+fieldWorkNotes+"^ORDERBYsys_created_on")
		q.Set("sysparm_limit", strconv.Itoa(journalPageSize))
		q.Set("sysparm_offset", strconv.Itoa(offset))

		var resp tableResponse
		if err := c.get(ctx, "/api/now/table/"+journalTable, q, &resp); err != nil {
			if len(out) > 0 {
				// Some of the feed is in hand; keep it and say so rather
				// than throwing away what was read.
				*warnings = append(*warnings, fmt.Sprintf("servicenow: journal page %d: %v", page+1, err))
				return out, nil
			}
			return nil, err
		}
		for _, r := range resp.Result {
			out = append(out, journalEntry{
				sysID:     r.str("sys_id"),
				element:   r.str("element"),
				value:     r.str("value"),
				createdOn: r.str("sys_created_on"),
				createdBy: r.str("sys_created_by"),
			})
		}
		if len(resp.Result) < journalPageSize {
			break
		}
		offset += len(resp.Result)
	}
	return out, nil
}

// --- List ---

// List implements source.Tracker, translating the filter into a
// ServiceNow encoded query and paging the Table API with
// sysparm_offset/sysparm_limit.
func (c *Client) list(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	// List is about no one ticket, so its warnings file under the empty
	// key: WarningsFor("") is how a caller reads them back.
	var warnings []string
	limit, capped := limitOf(f.Limit)
	if capped {
		warnings = append(warnings, fmt.Sprintf("servicenow: list limit %d capped at %d", f.Limit, maxListResults))
	}

	query, dropped := c.buildQuery(f)
	if dropped {
		warnings = append(warnings, "servicenow: a list filter contained the encoded-query separator ^ and it was dropped from the value")
	}

	var out []ticket.TrackerTicket
	offset := 0
	for page := 0; len(out) < limit; page++ {
		if page >= maxListPages {
			warnings = append(warnings, fmt.Sprintf("servicenow: list stopped at the %d-page cap after %d records", maxListPages, len(out)))
			break
		}
		want := pageSize(limit - len(out))
		q := recordParams()
		q.Set("sysparm_query", query)
		q.Set("sysparm_limit", strconv.Itoa(want))
		q.Set("sysparm_offset", strconv.Itoa(offset))

		var resp tableResponse
		if err := c.get(ctx, "/api/now/table/"+url.PathEscape(c.table), q, &resp); err != nil {
			c.addWarnings("", warnings)
			return nil, err
		}
		for _, r := range resp.Result {
			out = append(out, c.mapTracker(r))
		}
		// Offset pagination has no end-of-results signal of its own: a
		// page shorter than the one asked for is the end.
		if len(resp.Result) < want {
			break
		}
		offset += len(resp.Result)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	c.addWarnings("", warnings)
	return out, nil
}

// limitOf applies the adapter contract's List bounds.
func limitOf(requested int) (int, bool) {
	if requested <= 0 {
		return defaultListResults, false
	}
	if requested > maxListResults {
		return maxListResults, true
	}
	return requested, false
}

// pageSize is how many records to ask for next: never more than the page
// this adapter reads in, never more than the caller still wants.
func pageSize(want int) int {
	if want <= 0 || want > listPageSize {
		return listPageSize
	}
	return want
}

// buildQuery turns a ListFilter into a ServiceNow encoded query. An unset
// status means the open-ish default, expressed as active=true so it holds
// whatever the instance called its workflow states. It reports whether a
// value had the query separator stripped out of it, which the caller turns
// into a warning: a silently narrowed filter is how a run reads the wrong
// working set.
func (c *Client) buildQuery(f source.ListFilter) (query string, dropped bool) {
	var clauses []string
	add := func(field, value string) {
		clean := encodedValue(value)
		if clean != strings.TrimSpace(value) {
			dropped = true
		}
		if clean == "" {
			return
		}
		clauses = append(clauses, field+"="+clean)
	}

	switch a := strings.TrimSpace(f.Assignee); {
	case a == "":
	case strings.EqualFold(a, "me"):
		// The instance resolves the credential's own user, so "me" means
		// whoever the integration user is.
		clauses = append(clauses, "assigned_to=javascript:gs.getUserID()")
	default:
		add("assigned_to.user_name", a)
	}
	if s := strings.TrimSpace(f.Status); s != "" {
		add("state", s)
	} else {
		clauses = append(clauses, "active=true")
	}
	if p := strings.TrimSpace(f.Parent); p != "" {
		add("parent", p)
	}
	clauses = append(clauses, "ORDERBYDESCsys_updated_on")
	return strings.Join(clauses, "^"), dropped
}
