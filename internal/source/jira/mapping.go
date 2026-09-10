package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- wire types ---

type jiraUser struct {
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
	AccountID    string `json:"accountId"`   // Cloud
	Name         string `json:"name"`        // Data Center username
	AccountType  string `json:"accountType"` // Cloud: atlassian | app | customer
}

type jiraNamed struct {
	Name string `json:"name"`
}

type jiraStatus struct {
	Name           string `json:"name"`
	StatusCategory struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"statusCategory"`
}

type jiraProject struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	ProjectTypeKey string `json:"projectTypeKey"`
}

type jiraParent struct {
	Key string `json:"key"`
}

type jiraAttachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Content  string `json:"content"`
	Created  string `json:"created"`
	Size     int64  `json:"size"`
}

type jiraComment struct {
	ID      string   `json:"id"`
	Author  jiraUser `json:"author"`
	Body    string   `json:"body"`
	Created string   `json:"created"`
	Updated string   `json:"updated"`
	// JSDPublic is present only on Jira Service Management issues: true for a
	// comment the customer can see, false for an agent-only internal note.
	JSDPublic *bool `json:"jsdPublic"`
}

type jiraComments struct {
	Comments   []jiraComment `json:"comments"`
	StartAt    int           `json:"startAt"`
	MaxResults int           `json:"maxResults"`
	Total      int           `json:"total"`
}

type jiraFields struct {
	Summary     string           `json:"summary"`
	Description string           `json:"description"`
	Priority    *jiraNamed       `json:"priority"`
	Status      *jiraStatus      `json:"status"`
	Assignee    *jiraUser        `json:"assignee"`
	Reporter    *jiraUser        `json:"reporter"`
	Resolution  *jiraNamed       `json:"resolution"`
	IssueType   *jiraNamed       `json:"issuetype"`
	Project     *jiraProject     `json:"project"`
	Labels      []string         `json:"labels"`
	Parent      *jiraParent      `json:"parent"`
	Created     string           `json:"created"`
	Updated     string           `json:"updated"`
	Attachment  []jiraAttachment `json:"attachment"`
	Comment     *jiraComments    `json:"comment"`
}

// jiraIssue keeps the raw fields object alongside the decoded one so an
// instance-specific custom field (the Data Center epic link) can be read
// without a struct tag naming a field id that differs per instance.
type jiraIssue struct {
	Key       string
	ID        string
	Fields    jiraFields
	RawFields map[string]json.RawMessage
}

func (i *jiraIssue) UnmarshalJSON(b []byte) error {
	var wire struct {
		Key    string          `json:"key"`
		ID     string          `json:"id"`
		Fields json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	i.Key, i.ID = wire.Key, wire.ID
	if len(wire.Fields) == 0 {
		return nil
	}
	if err := json.Unmarshal(wire.Fields, &i.Fields); err != nil {
		return err
	}
	// A custom field whose shape is unknown must not fail the whole issue.
	_ = json.Unmarshal(wire.Fields, &i.RawFields)
	return nil
}

// --- small helpers ---

// displayName picks the most human of the identifiers Jira offers, which
// differ between Cloud (displayName, accountId) and Data Center
// (displayName, name).
func displayName(u *jiraUser) string {
	if u == nil {
		return ""
	}
	for _, s := range []string{u.DisplayName, u.Name, u.EmailAddress} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// userKey is the stable identity used to tell whether a commenter is the
// same person as the reporter.
func userKey(u *jiraUser) string {
	if u == nil {
		return ""
	}
	for _, s := range []string{u.AccountID, u.Name, u.EmailAddress, u.DisplayName} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

func isServiceDesk(p *jiraProject) bool {
	return p != nil && p.ProjectTypeKey == "service_desk"
}

// jiraTimeLayouts covers Jira's own timestamp format ("+0000", no colon in
// the offset, milliseconds) and the RFC 3339 variants a proxy might hand back
// instead.
var jiraTimeLayouts = []string{
	"2006-01-02T15:04:05.999-0700",
	"2006-01-02T15:04:05-0700",
	time.RFC3339Nano,
	time.RFC3339,
}

// parseTime parses a Jira timestamp, yielding the zero time for an empty or
// unrecognised value rather than failing the whole ticket over a date.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range jiraTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// wikiAttachmentRefs matches the two ways Jira wiki markup names an
// attachment inline: an embedded image (!screenshot.png! or
// !screenshot.png|thumbnail!) and a file link ([^report.pdf]). Collecting
// them is what lets a comment's Message point at the attachments it talks
// about, since Jira's attachment list is per-issue and carries no comment id.
var wikiAttachmentRefs = regexp.MustCompile(`!([^!\r\n|]+?)(?:\|[^!\r\n]*)?!|\[\^([^\]\r\n]+)\]`)

// attachmentRefNames returns the attachment filenames referenced inline in a
// wiki-markup body, in order of first appearance.
func attachmentRefNames(body string) []string {
	if body == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range wikiAttachmentRefs.FindAllStringSubmatch(body, -1) {
		name := strings.TrimSpace(m[1])
		if name == "" {
			name = strings.TrimSpace(m[2])
		}
		if name == "" || strings.ContainsAny(name, " \t") || !strings.Contains(name, ".") {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// attachmentIDsFor maps the filenames a body references onto the ids of the
// issue's attachments, dropping references to files that are not attached.
func attachmentIDsFor(body string, byName map[string]string) []string {
	var ids []string
	for _, name := range attachmentRefNames(body) {
		if id, ok := byName[name]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func attachmentsByName(atts []jiraAttachment) map[string]string {
	byName := make(map[string]string, len(atts))
	for _, a := range atts {
		if a.Filename != "" && a.ID != "" {
			byName[a.Filename] = a.ID
		}
	}
	return byName
}

// --- tracker mapping ---

// mapTracker maps an issue onto ticket.TrackerTicket. HelpdeskRef is set only
// from Jira's own linkage — an issue in a Jira Service Management project is
// its own helpdesk ticket — and left empty otherwise, so the wiring layer's
// configurable description regex stays the single place that guesses.
func (c *Client) mapTracker(iss *jiraIssue) ticket.TrackerTicket {
	f := &iss.Fields
	tt := ticket.TrackerTicket{
		Key:         iss.Key,
		Title:       f.Summary,
		Description: f.Description,
		URL:         c.browseURL(iss.Key),
		CreatedAt:   parseTime(f.Created),
		UpdatedAt:   parseTime(f.Updated),
		Fields:      map[string]string{},
	}
	if f.Priority != nil {
		tt.Priority = f.Priority.Name
	}
	if f.Status != nil {
		tt.Status = f.Status.Name
	}
	tt.Assignee = displayName(f.Assignee)
	if isServiceDesk(f.Project) {
		tt.HelpdeskRef = iss.Key
	}

	if f.IssueType != nil && f.IssueType.Name != "" {
		tt.Fields["issuetype"] = f.IssueType.Name
	}
	if f.Project != nil && f.Project.Key != "" {
		tt.Fields["project"] = f.Project.Key
	}
	if len(f.Labels) > 0 {
		tt.Fields["labels"] = strings.Join(f.Labels, ",")
	}
	if parent := c.parentKey(iss); parent != "" {
		tt.Fields["parent"] = parent
	}
	if r := displayName(f.Reporter); r != "" {
		tt.Fields["reporter"] = r
	}
	if f.Resolution != nil && f.Resolution.Name != "" {
		tt.Fields["resolution"] = f.Resolution.Name
	}
	return tt
}

// parentKey prefers the standard parent field — which on Cloud covers both
// sub-task parents and epics — and falls back to the instance's epic-link
// custom field, which is how Data Center still models epic membership.
func (c *Client) parentKey(iss *jiraIssue) string {
	if iss.Fields.Parent != nil && iss.Fields.Parent.Key != "" {
		return iss.Fields.Parent.Key
	}
	ef := c.cachedEpicField()
	if ef == "" || iss.RawFields == nil {
		return ""
	}
	raw, ok := iss.RawFields[ef]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.TrimSpace(obj.Key)
	}
	return ""
}

// --- helpdesk mapping ---

// mapHelpdesk maps an issue onto ticket.HelpdeskTicket. For a Jira Service
// Management project the reporter is the customer who raised the request, so
// that is what Contact/Customer describe.
func (c *Client) mapHelpdesk(iss *jiraIssue) ticket.HelpdeskTicket {
	f := &iss.Fields
	ht := ticket.HelpdeskTicket{
		ID:        iss.Key,
		Subject:   f.Summary,
		URL:       c.browseURL(iss.Key),
		Channel:   "jira",
		CreatedAt: parseTime(f.Created),
		UpdatedAt: parseTime(f.Updated),
		Fields:    map[string]string{},
	}
	if isServiceDesk(f.Project) {
		ht.Channel = "jira-service-management"
	}
	if f.Status != nil {
		ht.Status = f.Status.Name
	}
	if f.Priority != nil {
		ht.Priority = f.Priority.Name
	}
	if f.Reporter != nil {
		ht.Contact = displayName(f.Reporter)
		ht.Customer = strings.TrimSpace(f.Reporter.EmailAddress)
		ht.CustomerID = userKey(f.Reporter)
	}
	if f.Project != nil {
		if f.Project.Key != "" {
			ht.Fields["project"] = f.Project.Key
		}
		if f.Project.ProjectTypeKey != "" {
			ht.Fields["projectTypeKey"] = f.Project.ProjectTypeKey
		}
	}
	if f.IssueType != nil && f.IssueType.Name != "" {
		ht.Fields["issuetype"] = f.IssueType.Name
	}
	return ht
}

// --- thread ---

const commentPageSize = 100

// maxCommentPages bounds the comment feed the same way maxSearchPages
// bounds search. The loop's own exit conditions rely on the server
// advancing startAt and reporting a truthful total; an instance that
// answers every request with the same full page satisfies neither and
// would otherwise page for ever, one round trip at a time.
const maxCommentPages = 100

// Threads implements source.Helpdesk: the issue description opens the
// conversation and every comment follows, oldest first.
func (c *Client) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	ctx, col := withCollector(ctx)
	defer c.publish(id, col)

	iss, err := c.fetchIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	comments, err := c.allComments(ctx, iss)
	if err != nil {
		return nil, err
	}

	f := &iss.Fields
	jsm := isServiceDesk(f.Project)
	byName := attachmentsByName(f.Attachment)
	reporter := userKey(f.Reporter)

	th := make(ticket.Thread, 0, len(comments)+1)
	if strings.TrimSpace(f.Description) != "" {
		// The description is what the reporter wrote, so on a service desk it
		// is the customer's opening message.
		role := ticket.RoleAgent
		if jsm {
			role = ticket.RoleCustomer
		}
		th = append(th, ticket.Message{
			At:            parseTime(f.Created),
			Author:        displayName(f.Reporter),
			Role:          role,
			Text:          f.Description,
			AttachmentIDs: attachmentIDsFor(f.Description, byName),
		})
	}

	for _, cm := range comments {
		author := displayName(&cm.Author)
		if jsm && cm.JSDPublic != nil && !*cm.JSDPublic {
			author += " (internal)"
		}
		th = append(th, ticket.Message{
			At:            parseTime(cm.Created),
			Author:        author,
			Role:          commentRole(cm, jsm, reporter),
			Text:          cm.Body,
			AttachmentIDs: attachmentIDsFor(cm.Body, byName),
		})
	}

	sort.SliceStable(th, func(i, j int) bool { return th[i].At.Before(th[j].At) })
	return th, nil
}

// commentRole decides who a comment came from. Only a service desk can
// distinguish a customer at all: a public comment from the person who raised
// the request is the customer talking, everything else is an agent, and an
// app account (automation, a connected add-on) is the system.
func commentRole(cm jiraComment, jsm bool, reporter string) ticket.Role {
	if strings.EqualFold(cm.Author.AccountType, "app") {
		return ticket.RoleSystem
	}
	if jsm && cm.JSDPublic != nil && *cm.JSDPublic && reporter != "" && userKey(&cm.Author) == reporter {
		return ticket.RoleCustomer
	}
	return ticket.RoleAgent
}

// allComments returns the issue's comments, using the ones already embedded
// in the issue payload when they are complete and paging
// /rest/api/2/issue/{key}/comment when Jira truncated them.
func (c *Client) allComments(ctx context.Context, iss *jiraIssue) ([]jiraComment, error) {
	if iss.Fields.Comment != nil && len(iss.Fields.Comment.Comments) >= iss.Fields.Comment.Total {
		return iss.Fields.Comment.Comments, nil
	}

	var all []jiraComment
	startAt := 0
	page := 0
	for ; page < maxCommentPages; page++ {
		q := url.Values{}
		q.Set("startAt", strconv.Itoa(startAt))
		q.Set("maxResults", strconv.Itoa(commentPageSize))
		q.Set("orderBy", "created")

		var res jiraComments
		path := "/rest/api/2/issue/" + url.PathEscape(iss.Key) + "/comment"
		if err := c.doJSON(ctx, http.MethodGet, path, q, nil, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Comments...)
		if len(res.Comments) == 0 {
			break
		}
		startAt += len(res.Comments)
		if res.Total > 0 && startAt >= res.Total {
			break
		}
	}
	if page >= maxCommentPages {
		// The thread the agent reads is now a prefix of the real one, and
		// nothing else would say so.
		warnCtx(ctx, "jira: comments for %s stopped after %d pages (%d comments); the thread may be incomplete", iss.Key, maxCommentPages, len(all))
	}
	return all, nil
}
