package azdo

import (
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Work item field reference names. Azure DevOps keys the flat fields map by
// these, and the same names go into the workitemsbatch field list.
const (
	fieldTitle        = "System.Title"
	fieldDescription  = "System.Description"
	fieldReproSteps   = "Microsoft.VSTS.TCM.ReproSteps"
	fieldState        = "System.State"
	fieldReason       = "System.Reason"
	fieldWorkItemType = "System.WorkItemType"
	fieldPriority     = "Microsoft.VSTS.Common.Priority"
	fieldSeverity     = "Microsoft.VSTS.Common.Severity"
	fieldAssignedTo   = "System.AssignedTo"
	fieldCreatedBy    = "System.CreatedBy"
	fieldTags         = "System.Tags"
	fieldParent       = "System.Parent"
	fieldAreaPath     = "System.AreaPath"
	fieldIteration    = "System.IterationPath"
	fieldCreatedDate  = "System.CreatedDate"
	fieldChangedDate  = "System.ChangedDate"
)

// workItem is the subset of the Get Work Item / workitemsbatch payload the
// adapter reads. Fields stays raw so a field can be a string, a number or an
// identity object without a per-field struct.
type workItem struct {
	ID        int                        `json:"id"`
	Rev       int                        `json:"rev"`
	Fields    map[string]json.RawMessage `json:"fields"`
	Relations []relation                 `json:"relations"`
	Links     map[string]link            `json:"_links"`
	URL       string                     `json:"url"`
}

type link struct {
	Href string `json:"href"`
}

// relation is one entry of a work item's relations[]: an attachment
// (rel "AttachedFile"), a hyperlink (rel "Hyperlink"), or a work item link.
type relation struct {
	Rel        string          `json:"rel"`
	URL        string          `json:"url"`
	Attributes json.RawMessage `json:"attributes"`
}

// attributeString reads one attribute of a relation as a string.
func (r relation) attributeString(name string) string {
	if len(r.Attributes) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(r.Attributes, &m); err != nil {
		return ""
	}
	return rawString(m[name])
}

// identity is the Azure DevOps IdentityRef shape used by System.AssignedTo,
// System.CreatedBy and a comment's createdBy.
type identity struct {
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
}

// comment is one entry of the work item comments API.
type comment struct {
	ID           int      `json:"id"`
	Text         string   `json:"text"`
	Format       string   `json:"format"` // "markdown" (default) | "html"
	RenderedText string   `json:"renderedText"`
	CreatedBy    identity `json:"createdBy"`
	CreatedDate  string   `json:"createdDate"`
	IsDeleted    bool     `json:"isDeleted"`
}

type commentsPage struct {
	TotalCount        int       `json:"totalCount"`
	Count             int       `json:"count"`
	Comments          []comment `json:"comments"`
	ContinuationToken string    `json:"continuationToken"`
}

// rawString renders a raw JSON field value as a string: strings come through
// unquoted, numbers keep their integer form where they have one, and
// anything else falls back to its JSON text.
func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		if f == math.Trunc(f) && math.Abs(f) < 1e15 {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return strconv.FormatBool(b)
	}
	return strings.TrimSpace(string(raw))
}

func fieldString(fields map[string]json.RawMessage, name string) string {
	if name == "" {
		return ""
	}
	return rawString(fields[name])
}

// fieldIdentity reads an identity field, tolerating servers that return a
// bare display-name string instead of the IdentityRef object.
func fieldIdentity(fields map[string]json.RawMessage, name string) identity {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 {
		return identity{}
	}
	var id identity
	if err := json.Unmarshal(raw, &id); err == nil && (id.DisplayName != "" || id.UniqueName != "") {
		return id
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return identity{DisplayName: s}
	}
	return identity{}
}

// parseTime parses an Azure DevOps timestamp (ISO 8601 UTC, sometimes with
// fractional seconds). An empty or unparseable value yields the zero time.
func parseTime(s string) time.Time {
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

// description renders System.Description, and for a Bug appends the
// Bug-specific repro steps under their own heading — the default Bug form
// puts the report in ReproSteps rather than Description, so a Bug that only
// filled one of them still comes out with its text.
func description(fields map[string]json.RawMessage) string {
	desc, _ := htmltext.ToMarkdown(fieldString(fields, fieldDescription))
	if strings.EqualFold(fieldString(fields, fieldWorkItemType), "Bug") {
		repro, _ := htmltext.ToMarkdown(fieldString(fields, fieldReproSteps))
		if strings.TrimSpace(repro) != "" {
			desc = strings.TrimRight(desc, "\n") + "\n\n## Repro steps\n\n" + repro
		}
	}
	return strings.TrimSpace(desc)
}

// tags normalises the semicolon-delimited System.Tags string.
func tags(raw string) string {
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

// helpdeskRef derives the helpdesk ticket reference from the work item's own
// linkage: first a Hyperlink relation pointing at the configured helpdesk
// domain, then the configured custom field. It returns "" when neither is
// configured or neither matches — the generic description-regex fallback is
// the wiring layer's job, not the adapter's.
func (c *Client) helpdeskRef(wi workItem) string {
	if d := c.cfg.HelpdeskLinkDomain; d != "" {
		for _, rel := range wi.Relations {
			if !strings.EqualFold(rel.Rel, "Hyperlink") || rel.URL == "" {
				continue
			}
			u, err := url.Parse(rel.URL)
			if err != nil {
				continue
			}
			if hostMatches(u.Hostname(), d) {
				return rel.URL
			}
		}
	}
	return fieldString(wi.Fields, c.cfg.HelpdeskField)
}

// hostMatches reports whether host is domain or a subdomain of it. The dot
// boundary is deliberate: a plain suffix test would treat
// "nothelpdesk.example.com" as a match for "helpdesk.example.com".
//
// Both sides are lowercased. The configured domain is typed by hand into a
// YAML file, so "Helpdesk.Example.com" there is an ordinary thing to write
// and used to match nothing at all, silently.
func hostMatches(host, domain string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// mapTracker maps a work item onto the tracker side of a ticket bundle.
func (c *Client) mapTracker(wi workItem) ticket.TrackerTicket {
	f := wi.Fields
	key := strconv.Itoa(wi.ID)

	webURL := wi.Links["html"].Href
	if webURL == "" {
		webURL = c.webURL(key)
	}

	fields := map[string]string{}
	setIf := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			fields[k] = v
		}
	}
	setIf("workItemType", fieldString(f, fieldWorkItemType))
	setIf("areaPath", fieldString(f, fieldAreaPath))
	setIf("iterationPath", fieldString(f, fieldIteration))
	setIf("tags", tags(fieldString(f, fieldTags)))
	setIf("parent", fieldString(f, fieldParent))
	// Severity is Azure DevOps' second priority axis on Bugs. It stays out
	// of Priority — which is the numeric Microsoft.VSTS.Common.Priority and
	// nothing else — so callers comparing priorities across trackers are not
	// handed a compound string.
	setIf("severity", fieldString(f, fieldSeverity))
	setIf("reason", fieldString(f, fieldReason))
	if len(fields) == 0 {
		fields = nil
	}

	return ticket.TrackerTicket{
		Key:         key,
		Title:       fieldString(f, fieldTitle),
		Description: description(f),
		Priority:    fieldString(f, fieldPriority),
		Status:      fieldString(f, fieldState),
		Assignee:    fieldIdentity(f, fieldAssignedTo).DisplayName,
		URL:         webURL,
		HelpdeskRef: c.helpdeskRef(wi),
		CreatedAt:   parseTime(fieldString(f, fieldCreatedDate)),
		UpdatedAt:   parseTime(fieldString(f, fieldChangedDate)),
		Fields:      fields,
	}
}

// mapHelpdesk maps a work item onto the helpdesk side of a bundle: the
// record the comment thread belongs to.
func (c *Client) mapHelpdesk(wi workItem) ticket.HelpdeskTicket {
	f := wi.Fields
	key := strconv.Itoa(wi.ID)

	webURL := wi.Links["html"].Href
	if webURL == "" {
		webURL = c.webURL(key)
	}

	fields := map[string]string{}
	if v := strings.TrimSpace(fieldString(f, fieldWorkItemType)); v != "" {
		fields["workItemType"] = v
	}
	if v := strings.TrimSpace(fieldString(f, fieldAreaPath)); v != "" {
		fields["areaPath"] = v
	}
	if len(fields) == 0 {
		fields = nil
	}

	createdBy := fieldIdentity(f, fieldCreatedBy)
	return ticket.HelpdeskTicket{
		ID:        key,
		Subject:   fieldString(f, fieldTitle),
		Status:    fieldString(f, fieldState),
		Priority:  fieldString(f, fieldPriority),
		Channel:   "azure-devops",
		Contact:   createdBy.DisplayName,
		Customer:  c.cfg.Project,
		URL:       webURL,
		CreatedAt: parseTime(fieldString(f, fieldCreatedDate)),
		UpdatedAt: parseTime(fieldString(f, fieldChangedDate)),
		Fields:    fields,
	}
}

// mapComment maps one work item comment to a thread message. Azure DevOps
// has no customer-facing comment concept — every commenter is an
// engineer on the work item — so the role is always agent, except for a
// comment with no author, which is automation.
func mapComment(cm comment) ticket.Message {
	text := cm.Text
	if strings.EqualFold(cm.Format, "html") {
		src := cm.Text
		if strings.TrimSpace(src) == "" {
			src = cm.RenderedText
		}
		text, _ = htmltext.ToMarkdown(src)
	}

	role := ticket.RoleAgent
	author := cm.CreatedBy.DisplayName
	if strings.TrimSpace(author) == "" {
		author = strings.TrimSpace(cm.CreatedBy.UniqueName)
	}
	if author == "" {
		role = ticket.RoleSystem
		author = "system"
	}

	return ticket.Message{
		At:     parseTime(cm.CreatedDate),
		Author: author,
		Role:   role,
		Text:   strings.TrimSpace(text),
	}
}
