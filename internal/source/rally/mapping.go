package rally

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source/htmltext"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// rallyRef is a reference to another object. WSAPI carries a display name
// alongside every reference, and hydrates the referenced object with any
// attribute that appears in the request's fetch list — which is why
// FormattedID is readable on Parent and Feature here.
type rallyRef struct {
	Ref         string      `json:"_ref"`
	RefName     string      `json:"_refObjectName"`
	Type        string      `json:"_type"`
	FormattedID string      `json:"FormattedID"`
	Name        string      `json:"Name"`
	UserName    string      `json:"UserName"`
	ObjectID    json.Number `json:"ObjectID"`
}

func (r *rallyRef) name() string {
	if r == nil {
		return ""
	}
	if r.RefName != "" {
		return r.RefName
	}
	return r.Name
}

// key returns the reference's human key, falling back to its display name
// when FormattedID was not hydrated.
func (r *rallyRef) key() string {
	if r == nil {
		return ""
	}
	if r.FormattedID != "" {
		return r.FormattedID
	}
	return r.name()
}

// tagCollection is the shape a fetched Tags collection comes back in.
// Rally puts the names in _tagsNameArray; a plain collection fetch uses
// Results instead, so both are read.
type tagCollection struct {
	Count   int         `json:"Count"`
	NameArr []*rallyRef `json:"_tagsNameArray"`
	Results []*rallyRef `json:"Results"`
}

func (t *tagCollection) names() []string {
	if t == nil {
		return nil
	}
	src := t.NameArr
	if len(src) == 0 {
		src = t.Results
	}
	var out []string
	for _, r := range src {
		if n := r.name(); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// artifact is the subset of a Rally artifact the adapter maps. Fields the
// subscription does not have (Severity on a story, ScheduleState on a
// defect) simply decode to their zero value.
type artifact struct {
	Ref            string         `json:"_ref"`
	Type           string         `json:"_type"`
	ObjectID       json.Number    `json:"ObjectID"`
	FormattedID    string         `json:"FormattedID"`
	Name           string         `json:"Name"`
	Description    string         `json:"Description"`
	Notes          string         `json:"Notes"`
	Priority       string         `json:"Priority"`
	Severity       string         `json:"Severity"`
	State          string         `json:"State"`
	ScheduleState  string         `json:"ScheduleState"`
	Owner          *rallyRef      `json:"Owner"`
	Project        *rallyRef      `json:"Project"`
	Iteration      *rallyRef      `json:"Iteration"`
	Release        *rallyRef      `json:"Release"`
	Tags           *tagCollection `json:"Tags"`
	Parent         *rallyRef      `json:"Parent"`
	Feature        *rallyRef      `json:"Feature"`
	CreationDate   string         `json:"CreationDate"`
	LastUpdateDate string         `json:"LastUpdateDate"`
}

// status is the artifact's workflow state: Defect-shaped types carry State,
// everything else ScheduleState. Whichever field the type does not use is
// consulted as a fallback so a subscription that populates both, or a
// custom type, still reports something.
func (f found) status() string {
	primary, secondary := f.a.ScheduleState, f.a.State
	if statusField(f.typ) == "State" {
		primary, secondary = f.a.State, f.a.ScheduleState
	}
	if primary != "" {
		return primary
	}
	return secondary
}

// parentKey is the artifact's parent as a human key: the portfolio Feature
// when one is set, otherwise Parent.
func (f found) parentKey() string {
	if k := f.a.Parent.key(); k != "" {
		return k
	}
	return f.a.Feature.key()
}

// objectID is the artifact's numeric internal id, read from ObjectID or,
// when that was not fetched, from the trailing segment of _ref.
func (f found) objectID() string {
	if s := f.a.ObjectID.String(); s != "" && s != "0" {
		return s
	}
	if i := strings.LastIndexByte(f.a.Ref, '/'); i >= 0 {
		last := f.a.Ref[i+1:]
		if _, err := strconv.ParseInt(last, 10, 64); err == nil {
			return last
		}
	}
	return ""
}

// artifactURL is the browser URL for an artifact.
//
// Assumption: Rally's single-page UI addresses a record as
// <base>/#/detail/<collection path>/<ObjectID> — e.g.
// https://rally1.rallydev.com/#/detail/defect/12345. This is the form
// Rally's own deep links and the community toolkits use, but it is not
// stated in the WSAPI reference, and a subscription whose UI is served
// from a different host than its API will need the base overridden. When
// the ObjectID is unknown the URL is left empty rather than guessed.
func (c *Client) artifactURL(f found) string {
	oid := f.objectID()
	if oid == "" {
		return ""
	}
	return c.cfg.BaseURL + "/#/detail/" + f.typ.Path + "/" + oid
}

// description renders Description, and Notes when present, as Markdown,
// returning the inline image sources referenced by either.
func (f found) description() (string, []string) {
	body, images := htmltext.ToMarkdown(f.a.Description)
	if strings.TrimSpace(f.a.Notes) != "" {
		notes, notesImages := htmltext.ToMarkdown(f.a.Notes)
		if strings.TrimSpace(notes) != "" {
			if strings.TrimSpace(body) != "" {
				body += "\n\n"
			}
			body += "## Notes\n\n" + notes
		}
		images = append(images, notesImages...)
	}
	return body, images
}

// helpdeskRef reads the configured custom field off the raw artifact JSON.
// The field name is operator-configured (Rally exposes custom fields with a
// c_ prefix) so it cannot be a struct tag; a reference-shaped value yields
// its display name, a scalar its string form.
func (f found) helpdeskRef(field string) string {
	field = strings.TrimSpace(field)
	if field == "" {
		return ""
	}
	raw, ok := f.raw[field]
	if !ok {
		return ""
	}
	return rawToString(raw)
}

func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return strconv.FormatBool(b)
	}
	var ref rallyRef
	if err := json.Unmarshal(raw, &ref); err == nil {
		if n := ref.name(); n != "" {
			return n
		}
		return ref.Ref
	}
	return ""
}

// fields collects the adapter-specific extras. Empty values are omitted so
// the map never carries a key that says nothing.
func (f found) fields(helpdeskField string) map[string]string {
	out := map[string]string{}
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			out[k] = v
		}
	}
	set("type", f.typ.Name)
	set("project", f.a.Project.name())
	set("iteration", f.a.Iteration.name())
	set("release", f.a.Release.name())
	set("tags", strings.Join(f.a.Tags.names(), ", "))
	set("parent", f.parentKey())
	set("severity", f.a.Severity)
	set("objectId", f.objectID())
	set("_ref", f.a.Ref)
	if len(out) == 0 {
		return nil
	}
	return out
}

// toTracker maps a decoded artifact to Sirdar's tracker ticket.
func (c *Client) toTracker(f found) ticket.TrackerTicket {
	body, images := f.description()
	fields := f.fields(c.cfg.HelpdeskField)
	if len(images) > 0 {
		if fields == nil {
			fields = map[string]string{}
		}
		// The bytes for these live in the artifact's Attachment
		// collection and are fetched by Attachments; recording the
		// sources keeps an image referenced only from the HTML from
		// disappearing silently.
		fields["inlineImages"] = strings.Join(images, ", ")
	}
	return ticket.TrackerTicket{
		Key:         f.a.FormattedID,
		Title:       f.a.Name,
		Description: body,
		Priority:    f.a.Priority,
		Status:      f.status(),
		Assignee:    f.a.Owner.name(),
		URL:         c.artifactURL(f),
		HelpdeskRef: f.helpdeskRef(c.cfg.HelpdeskField),
		CreatedAt:   parseRallyTime(f.a.CreationDate),
		UpdatedAt:   parseRallyTime(f.a.LastUpdateDate),
		Fields:      fields,
	}
}

// parseRallyTime parses a WSAPI timestamp, which is RFC3339 with
// fractional seconds (e.g. "2026-09-08T10:12:00.000Z"). An empty or
// unparseable value yields the zero time rather than an error: a missing
// date is not a reason to drop a ticket.
func parseRallyTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z0700"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
