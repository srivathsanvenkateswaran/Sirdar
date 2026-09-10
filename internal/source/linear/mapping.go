package linear

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- Response shapes ---

type namedRef struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Key   string `json:"key"`
}

type labelConn struct {
	Nodes []struct {
		Name string `json:"name"`
	} `json:"nodes"`
}

type linearAttachment struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle"`
	SourceType string `json:"sourceType"`
}

type attachmentConn struct {
	Nodes []linearAttachment `json:"nodes"`
}

type customerNeedConn struct {
	Nodes []struct {
		ID string `json:"id"`
	} `json:"nodes"`
}

type linearIssue struct {
	ID            string `json:"id"`
	Identifier    string `json:"identifier"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Priority      *int   `json:"priority"`
	PriorityLabel string `json:"priorityLabel"`
	State         *struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Assignee      *namedRef         `json:"assignee"`
	Team          *namedRef         `json:"team"`
	Project       *namedRef         `json:"project"`
	Parent        *linearIssue      `json:"parent"`
	Labels        labelConn         `json:"labels"`
	URL           string            `json:"url"`
	CreatedAt     string            `json:"createdAt"`
	UpdatedAt     string            `json:"updatedAt"`
	Attachments   attachmentConn    `json:"attachments"`
	CustomerNeeds *customerNeedConn `json:"customerNeeds"`
}

type linearComment struct {
	ID           string    `json:"id"`
	Body         string    `json:"body"`
	CreatedAt    string    `json:"createdAt"`
	URL          string    `json:"url"`
	User         *namedRef `json:"user"`
	ExternalUser *namedRef `json:"externalUser"`
	BotActor     *namedRef `json:"botActor"`
	Parent       *struct {
		ID string `json:"id"`
	} `json:"parent"`
}

// --- Tracker mapping ---

// mapIssue projects a Linear issue onto Sirdar's tracker record. Description
// and comment bodies are already Markdown, so nothing is converted.
func mapIssue(iss *linearIssue) ticket.TrackerTicket {
	t := ticket.TrackerTicket{
		Key:         iss.Identifier,
		Title:       iss.Title,
		Description: iss.Description,
		Priority:    iss.PriorityLabel,
		URL:         iss.URL,
		HelpdeskRef: helpdeskRef(iss.Attachments.Nodes),
		CreatedAt:   parseTime(iss.CreatedAt),
		UpdatedAt:   parseTime(iss.UpdatedAt),
		Fields:      map[string]string{},
	}
	if iss.State != nil {
		t.Status = iss.State.Name
		if iss.State.Type != "" {
			t.Fields["stateType"] = iss.State.Type
		}
	}
	if iss.Assignee != nil {
		t.Assignee = iss.Assignee.Name
	}
	if iss.Team != nil && iss.Team.Key != "" {
		t.Fields["team"] = iss.Team.Key
	}
	if iss.Project != nil && iss.Project.Name != "" {
		t.Fields["project"] = iss.Project.Name
	}
	if labels := labelNames(iss.Labels); labels != "" {
		t.Fields["labels"] = labels
	}
	if iss.PriorityLabel != "" {
		t.Fields["priorityLabel"] = iss.PriorityLabel
	}
	if iss.Priority != nil {
		t.Fields["priority"] = strconv.Itoa(*iss.Priority)
	}
	if iss.Parent != nil && iss.Parent.Identifier != "" {
		t.Fields["parent"] = iss.Parent.Identifier
	}
	if iss.CustomerNeeds != nil {
		t.Fields["customerRequests"] = strconv.Itoa(len(iss.CustomerNeeds.Nodes))
	}
	if len(t.Fields) == 0 {
		t.Fields = nil
	}
	return t
}

// mapHelpdeskTicket projects the same issue onto the helpdesk record, for the
// case where the Linear issue itself is the conversation Sirdar reads.
func mapHelpdeskTicket(iss *linearIssue) ticket.HelpdeskTicket {
	h := ticket.HelpdeskTicket{
		ID:        iss.Identifier,
		Subject:   iss.Title,
		Priority:  iss.PriorityLabel,
		Channel:   "linear",
		URL:       iss.URL,
		CreatedAt: parseTime(iss.CreatedAt),
		UpdatedAt: parseTime(iss.UpdatedAt),
		Fields:    map[string]string{},
	}
	if iss.State != nil {
		h.Status = iss.State.Name
		if iss.State.Type != "" {
			h.Fields["stateType"] = iss.State.Type
		}
	}
	if iss.Team != nil && iss.Team.Key != "" {
		h.Fields["team"] = iss.Team.Key
	}
	if iss.Project != nil && iss.Project.Name != "" {
		h.Fields["project"] = iss.Project.Name
	}
	if labels := labelNames(iss.Labels); labels != "" {
		h.Fields["labels"] = labels
	}
	if len(h.Fields) == 0 {
		h.Fields = nil
	}
	return h
}

func labelNames(l labelConn) string {
	names := make([]string, 0, len(l.Nodes))
	for _, n := range l.Nodes {
		if n.Name != "" {
			names = append(names, n.Name)
		}
	}
	return strings.Join(names, ", ")
}

// helpdeskSourceTypes are the Linear attachment source types that mean "this
// link points at a support conversation".
var helpdeskSourceTypes = map[string]bool{"zendesk": true, "intercom": true, "front": true}

// helpdeskHostFragments identify a support-tool URL when Linear could not
// classify the attachment itself (sourceType empty or "unknown"), which is the
// case for a plain link pasted onto the issue.
var helpdeskHostFragments = []string{"zendesk", "intercom", "frontapp", "desk.zoho", "freshdesk", "helpscout", "helpshift", "zohodesk"}

// helpdeskRef returns the URL of the first attachment that points at a
// helpdesk conversation, which is how Linear records a Customer Request's
// source link. Attachments Linear has classified as another integration
// (github, slack, figma...) are never considered.
func helpdeskRef(atts []linearAttachment) string {
	for _, a := range atts {
		if a.URL == "" {
			continue
		}
		st := strings.ToLower(strings.TrimSpace(a.SourceType))
		switch {
		case helpdeskSourceTypes[st]:
			return a.URL
		case st == "" || st == "unknown":
			if looksLikeHelpdeskURL(a.URL) {
				return a.URL
			}
		}
	}
	return ""
}

func looksLikeHelpdeskURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, frag := range helpdeskHostFragments {
		if strings.Contains(host, frag) {
			return true
		}
	}
	return false
}

// parseTime parses a Linear ISO 8601 timestamp; an empty or unparseable value
// yields the zero time rather than failing the whole fetch.
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

// --- Thread mapping ---

// replyPrefix marks a threaded reply once the tree is flattened into Sirdar's
// linear thread.
const replyPrefix = "↳ "

// buildThread flattens Linear's one-level comment tree into an ordered thread:
// top-level comments in createdAt order, each immediately followed by its
// replies in createdAt order, with the reply text prefixed by replyPrefix so
// the nesting survives the flattening.
func buildThread(cs []linearComment) ticket.Thread {
	idx := make([]int, len(cs))
	for i := range cs {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return parseTime(cs[idx[a]].CreatedAt).Before(parseTime(cs[idx[b]].CreatedAt))
	})

	known := make(map[string]bool, len(cs))
	for _, c := range cs {
		if c.ID != "" {
			known[c.ID] = true
		}
	}

	var roots []int
	children := make(map[string][]int)
	for _, i := range idx {
		c := cs[i]
		if c.Parent != nil && c.Parent.ID != "" && c.Parent.ID != c.ID && known[c.Parent.ID] {
			children[c.Parent.ID] = append(children[c.Parent.ID], i)
			continue
		}
		roots = append(roots, i)
	}

	out := make(ticket.Thread, 0, len(cs))
	seen := make(map[int]bool, len(cs))
	var walk func(i int, depth int)
	walk = func(i int, depth int) {
		if seen[i] {
			return
		}
		seen[i] = true
		out = append(out, mapComment(cs[i], depth > 0))
		for _, child := range children[cs[i].ID] {
			walk(child, depth+1)
		}
	}
	for _, i := range roots {
		walk(i, 0)
	}
	// A reply whose parent is missing from the page still belongs in the
	// thread; append it in createdAt order rather than dropping it.
	for _, i := range idx {
		if !seen[i] {
			seen[i] = true
			out = append(out, mapComment(cs[i], true))
		}
	}
	return out
}

// mapComment turns one comment into a thread message. Linear carries no
// customer/agent distinction on a comment — the thread is the workspace's own
// discussion, and a customer's own words reach Sirdar through the helpdesk
// adapter, not through here — so the role is agent unless the comment was
// written by a bot, which is automation.
func mapComment(c linearComment, reply bool) ticket.Message {
	m := ticket.Message{
		At:   parseTime(c.CreatedAt),
		Role: ticket.RoleAgent,
		Text: c.Body,
	}
	switch {
	case c.User != nil && c.User.Name != "":
		m.Author = c.User.Name
	case c.User == nil && c.BotActor != nil:
		m.Role = ticket.RoleSystem
		m.Author = c.BotActor.Name
	case c.User == nil && c.ExternalUser != nil:
		// An external user is a guest commenting in Linear, not the
		// customer voice: take the name, leave the role alone.
		m.Author = c.ExternalUser.Name
		if m.Author == "" {
			m.Author = c.ExternalUser.Email
		}
	}
	if reply {
		m.Text = replyPrefix + m.Text
	}
	m.AttachmentIDs = uploadIDs(collectUploads(c.Body))
	return m
}

// --- Attachment refs ---

// uploadHost is Linear's private file storage. Files there are fetched with
// the same Authorization header as the GraphQL API; every other attachment URL
// points at a third-party system that needs its own credential, so Sirdar
// links to it instead of downloading it.
const uploadHost = "uploads.linear.app"

// inlineUploadRe matches a Markdown image whose target is a Linear upload,
// e.g. ![screenshot](https://uploads.linear.app/<uuid>/<uuid>/shot.png).
var inlineUploadRe = regexp.MustCompile(`!\[[^\]]*\]\(\s*(https://uploads\.linear\.app/[^)\s]+)`)

// uploadRef is a downloadable Linear-hosted file.
type uploadRef struct {
	ID   string
	Name string
	URL  string
}

// collectUploads finds the Linear-hosted images embedded in a Markdown body.
func collectUploads(markdown string) []uploadRef {
	var refs []uploadRef
	for _, m := range inlineUploadRe.FindAllStringSubmatch(markdown, -1) {
		raw := strings.TrimRight(m[1], ".,;")
		refs = append(refs, uploadRef{ID: uploadID(raw), Name: uploadName(raw), URL: raw})
	}
	return refs
}

// uploadAttachments keeps the issue attachments that are Linear-hosted files
// rather than links into another system.
func uploadAttachments(atts []linearAttachment) []uploadRef {
	var refs []uploadRef
	for _, a := range atts {
		if !isUploadURL(a.URL) {
			continue
		}
		name := uploadName(a.URL)
		if ext := path.Ext(name); ext == "" && a.Title != "" {
			name = a.Title
		}
		refs = append(refs, uploadRef{ID: uploadID(a.URL), Name: name, URL: a.URL})
	}
	return refs
}

func isUploadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), uploadHost)
}

// dedupeUploads drops repeats, keeping first-seen order. A Linear upload URL
// is unique per file, and the same image often appears both as an issue
// attachment and inline in the description.
func dedupeUploads(refs []uploadRef) []uploadRef {
	seen := make(map[string]bool, len(refs))
	out := refs[:0:0]
	for _, r := range refs {
		if r.URL == "" || seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		out = append(out, r)
	}
	return out
}

func uploadIDs(refs []uploadRef) []string {
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, r := range refs {
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		ids = append(ids, r.ID)
	}
	return ids
}

// uploadID identifies a Linear upload by the storage UUID that precedes the
// filename, which is stable and unique where the filename alone is not.
func uploadID(raw string) string {
	p := urlPath(raw)
	if p == "" {
		return "attachment"
	}
	if dir := path.Base(path.Dir(p)); dir != "" && dir != "." && dir != "/" {
		return dir
	}
	return path.Base(p)
}

// uploadName is the file's name as Linear stored it: the URL's last segment.
func uploadName(raw string) string {
	p := urlPath(raw)
	if p == "" {
		return ""
	}
	name, err := url.PathUnescape(path.Base(p))
	if err != nil {
		name = path.Base(p)
	}
	return name
}

func urlPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(u.Path, "/")
}

// --- Filename sanitisation ---
//
// Same rules as the zohodesk adapter's: an attachment name comes from a
// remote system, so it never gets to choose where the file lands.

// sanitizeName turns a name taken from a URL into a safe filename component:
// it strips any directory portion (so "../../evil.txt" cannot write outside
// the destination dir), drops path separators and control characters, falls
// back to "attachment" for an empty/"."/".." result, and caps the result at
// 120 bytes while preserving the extension.
func sanitizeName(name string) string {
	base := path.Base(strings.ReplaceAll(name, `\`, "/"))

	var b strings.Builder
	for _, r := range base {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(b.String())
	if clean == "" || clean == "." || clean == ".." {
		clean = "attachment"
	}
	return capBytes(clean, 120)
}

// capBytes truncates name to at most max bytes, preserving its extension
// where possible and never splitting a multi-byte UTF-8 rune.
func capBytes(name string, max int) string {
	if len(name) <= max {
		return name
	}
	ext := path.Ext(name)
	if len(ext) >= max {
		return truncateValidUTF8(name, max)
	}
	return truncateValidUTF8(name[:len(name)-len(ext)], max-len(ext)) + ext
}

func truncateValidUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// uuidRe matches the canonical 8-4-4-4-12 form Linear uses for entity ids, so
// a caller-supplied parent can be told apart from a human identifier.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(s) }

func indexedName(i int, name string) string { return fmt.Sprintf("%d-%s", i, name) }
