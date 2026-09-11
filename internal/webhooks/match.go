package webhooks

import (
	"fmt"
	"sort"
	"strings"
)

// Match is the filter a delivery has to pass before it starts a run. A
// zero Match accepts everything, which is what a workspace that names no
// webhooks.match gets.
type Match struct {
	// Assignee is the one person whose tickets are worth a run, as the
	// payloads name them: an email for most sources, an account id for a
	// tracker that sends no email. When it is set, a trigger carrying no
	// assignee at all is rejected — "only what is assigned to me" cannot
	// be satisfied by a payload that does not say.
	Assignee string
	// Statuses and Labels are accepted values, matched case-insensitively
	// against whatever the payload carries. Both are best-effort: a
	// payload that names no status or no labels passes the corresponding
	// filter rather than being dropped, because half the sources here can
	// be configured to send a body with neither in it.
	Statuses []string
	Labels   []string
}

// Allows reports whether t passes, and when it does not, the reason to
// report back to the sender.
func (m Match) Allows(t Trigger) (bool, string) {
	if m.Assignee != "" {
		got := strings.TrimSpace(t.Assignee)
		if got == "" {
			return false, "the payload names no assignee and webhooks.match.assignee is set"
		}
		if !strings.EqualFold(got, m.Assignee) {
			return false, fmt.Sprintf("assigned to %q, not %q", got, m.Assignee)
		}
	}
	if len(m.Statuses) > 0 {
		if got := harvest(t.Raw, statusFields); len(got) > 0 && !anyFold(got, m.Statuses) {
			return false, fmt.Sprintf("status %s is not in webhooks.match.statuses", quoteAll(got))
		}
	}
	if len(m.Labels) > 0 {
		if got := harvest(t.Raw, labelFields); len(got) > 0 && !anyFold(got, m.Labels) {
			return false, fmt.Sprintf("labels %s are not in webhooks.match.labels", quoteAll(got))
		}
	}
	return true, ""
}

// fieldset names the JSON fields a harvest reads one kind of value out of,
// lowercased. Object holds the fields whose value may be a string, a list
// or a wrapper object; Scalar holds the ones only counted when their value
// is a plain string.
//
// The split exists for one field: "state". Intercom and a hand-written
// Zendesk body put the status there as a string, while Rally names the
// whole work-item snapshot message.state — harvesting that object would
// read the ticket's title as its status.
type fieldset struct {
	Object map[string]bool
	Scalar map[string]bool
}

// statusFields and labelFields cover what the nine sources send: Jira's
// fields.status.name, Azure DevOps's System.State and System.Tags, Rally's
// ScheduleState and Tags, Zendesk's status and tags, Linear's labels.
//
// Linear's workflow state is not among them. It arrives as data.state, an
// object, and that is the collision above; a Linear workspace filters on
// labels instead.
var (
	statusFields = fieldset{
		Object: map[string]bool{"status": true, "schedulestate": true, "system.state": true},
		Scalar: map[string]bool{"state": true, "ticket_status": true},
	}
	labelFields = fieldset{
		Object: map[string]bool{"labels": true, "tags": true, "labelids": true, "system.tags": true},
	}
)

// harvestLimits bound the walk over a body that may be a megabyte of
// nested JSON: a payload deeper or wider than this is not one whose labels
// are worth finding.
const (
	maxHarvestDepth  = 12
	maxHarvestValues = 64
)

// harvest pulls every plausible value for the given field names out of a
// JSON body. It walks the whole document rather than looking at fixed
// paths, because the same field sits in a different place in each vendor's
// payload and, for the three sources whose body the operator writes
// themselves, wherever they put it.
func harvest(raw []byte, fields fieldset) []string {
	if len(raw) == 0 {
		return nil
	}
	v, err := decode(raw)
	if err != nil {
		return nil
	}
	var out []string
	walk(v, fields, 0, &out)
	sort.Strings(out)
	return out
}

func walk(v any, fields fieldset, depth int, out *[]string) {
	if depth > maxHarvestDepth || len(*out) >= maxHarvestValues {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			switch lower := strings.ToLower(k); {
			case fields.Object[lower]:
				take(child, depth, out)
			case fields.Scalar[lower]:
				if s := text(child); s != "" {
					*out = append(*out, s)
				}
			}
			walk(child, fields, depth+1, out)
		}
	case []any:
		for _, child := range t {
			walk(child, fields, depth+1, out)
		}
	}
}

// take reads the values out of one matched field: a plain string, a list
// of them, or the object a vendor wraps a status in.
func take(v any, depth int, out *[]string) {
	if depth > maxHarvestDepth || len(*out) >= maxHarvestValues {
		return
	}
	switch t := v.(type) {
	case []any:
		for _, child := range t {
			take(child, depth+1, out)
		}
	case map[string]any:
		// A status Jira sends as {"name":"In Progress"}, Azure DevOps as
		// {"oldValue":"New","newValue":"Active"}, Rally as {"Name":"..."}
		// — hence the case-insensitive lookup.
		lower := make(map[string]any, len(t))
		for k, v := range t {
			lower[strings.ToLower(k)] = v
		}
		for _, k := range []string{"name", "value", "newvalue", "displayname"} {
			if s := text(lower[k]); s != "" {
				*out = append(*out, s)
				return
			}
		}
	default:
		if s := text(v); s != "" {
			// Azure DevOps keeps tags in one semicolon-separated string.
			for _, part := range strings.Split(s, ";") {
				if part = strings.TrimSpace(part); part != "" {
					*out = append(*out, part)
				}
			}
		}
	}
}

func anyFold(got, want []string) bool {
	for _, g := range got {
		for _, w := range want {
			if strings.EqualFold(g, w) {
				return true
			}
		}
	}
	return false
}

func quoteAll(in []string) string {
	parts := make([]string, 0, len(in))
	for _, s := range in {
		parts = append(parts, fmt.Sprintf("%q", s))
	}
	return strings.Join(parts, ", ")
}
