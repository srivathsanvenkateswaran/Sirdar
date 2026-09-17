package ticket

import "strings"

// CanonicalType folds a tracker's own name for an issue type onto the
// vocabulary TrackerTicket.Type is written in: lower case, single spaces,
// and one spelling for the handful of types every tracker has under a
// different name. A name that matches no alias comes back lower-cased and
// otherwise untouched — a workspace with a custom "Change Request" type
// keeps it, and can filter the queue on it by writing it out.
//
// The folding is deliberately small. It exists so `queue.types: [bug]`
// means the same thing to a Jira "Bug", a Rally "Defect" and an Azure
// DevOps "Bug", not so that every tracker's taxonomy is rewritten into
// one; anything it cannot place it leaves alone rather than guessing.
func CanonicalType(name string) string {
	norm := normalizeType(name)
	if norm == "" {
		return ""
	}
	if canonical, ok := typeAliases[norm]; ok {
		return canonical
	}
	return norm
}

// typeAliases maps a tracker's own spelling onto the canonical one. Keys
// are already normalized by normalizeType, so "Sub-task", "sub_task" and
// "SUB TASK" all arrive here as "sub task".
var typeAliases = map[string]string{
	"defect":                   "bug",
	"bug report":               "bug",
	"sub task":                 "subtask",
	"subtask":                  "subtask",
	"user story":               "story",
	"hierarchicalrequirement":  "story",
	"hierarchical requirement": "story",
	"product backlog item":     "story",
	"portfolioitem/feature":    "feature",
	"portfolio item/feature":   "feature",
}

// normalizeType lower-cases a type name and collapses the separators
// trackers disagree about, so "Sub-Task" and "sub_task" are one name.
func normalizeType(name string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch r {
		case '_', '-', ' ', '\t', '\n':
			space = b.Len() > 0
		default:
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// typeFieldKeys are the Fields entries an adapter that predates
// TrackerTicket.Type writes its issue type into, newest spelling first.
// The private Janus exec adapter writes ticket_type; the protocol's own
// examples use issuetype, after Jira's field name; type is what the
// generic adapters settled on.
var typeFieldKeys = []string{"ticket_type", "issuetype", "type"}

// parentFieldKeys are the Fields entries the same adapters write a parent
// key into.
var parentFieldKeys = []string{"parent_key", "parent"}

// DeriveTypeAndParent fills Type and ParentKey from the adapter-specific
// Fields entries when the adapter itself set neither, and canonicalises
// Type either way.
//
// It is what keeps an out-of-process adapter written against the older
// protocol working unchanged: the type it already reports as a field is
// read as the type, rather than the ticket arriving with no type at all
// and being filtered out of the queue for it. Fields is left as it was —
// the entries are part of what a note renders, and an adapter that stops
// writing them would change what a reader sees.
func (t *TrackerTicket) DeriveTypeAndParent() {
	if strings.TrimSpace(t.Type) == "" {
		for _, key := range typeFieldKeys {
			if v := strings.TrimSpace(t.Fields[key]); v != "" {
				t.Type = v
				break
			}
		}
	}
	t.Type = CanonicalType(t.Type)

	if strings.TrimSpace(t.ParentKey) == "" {
		for _, key := range parentFieldKeys {
			if v := strings.TrimSpace(t.Fields[key]); v != "" {
				t.ParentKey = v
				break
			}
		}
	}
	t.ParentKey = strings.TrimSpace(t.ParentKey)
}
