// Package source defines the contract between Sirdar's triage loop and the
// tracker/helpdesk systems it reads tickets from. Implementations live
// out-of-process, behind the stdio adapter protocol in the plugin
// subpackage, so this package stays free of any particular tracker or
// helpdesk vendor.
package source

import (
	"context"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// Code classifies a source error so callers can decide how to react
// (retry, skip, surface to the user) without parsing Message.
type Code string

const (
	NotFound    Code = "not_found"
	Auth        Code = "auth"
	Unsupported Code = "unsupported"
	RateLimited Code = "rate_limited"
	Internal    Code = "internal"
)

// Error is the error type returned by Tracker and Helpdesk implementations.
// It is also the wire format for an adapter's error responses, so its
// fields carry explicit lowercase JSON tags.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// ListFilter narrows Tracker.List. Fields left zero are unfiltered; Limit
// zero means no limit.
type ListFilter struct {
	Assignee, Status, Parent string
	Limit                    int
}

// Tracker reads issue-tracker records (e.g. Jira).
type Tracker interface {
	Get(ctx context.Context, key string) (ticket.TrackerTicket, error)
	List(ctx context.Context, f ListFilter) ([]ticket.TrackerTicket, error)
}

// Helpdesk reads customer-support records and their conversation (e.g.
// Zoho Desk).
type Helpdesk interface {
	Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error)
	Threads(ctx context.Context, id string) (ticket.Thread, error)
	Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error)
}

// Closer is implemented by sources that hold a resource (e.g. a subprocess)
// which must be released when the source is no longer needed.
type Closer interface{ Close() error }

// Warner is implemented by sources that record non-fatal problems while
// serving a call — an attachment that would not download, say, when the
// rest of them did. The caller reads the warnings after the call it made
// and puts them in front of the agent and the operator, because a partial
// result that looks complete is how evidence goes missing unnoticed.
//
// It is keyed by ticket id, not the most recent call, because one source
// can serve several tickets finishing at the same time: an argument-less
// Warnings() would let two tickets in flight swap each other's warnings.
type Warner interface {
	// WarningsFor returns the problems recorded by the call made for
	// ticket id, or nothing when there were none.
	WarningsFor(id string) []string
}

// Transition is one status change on a tracker record: when it happened and
// what the record moved between. From is empty for a record's first
// transition when the adapter does not report the state it left.
type Transition struct {
	At   time.Time `json:"at"`
	From string    `json:"from"`
	To   string    `json:"to"`
}

// Transitioner is implemented by trackers that can report a record's status
// history. It is optional: an adapter that cannot read a changelog simply
// does not implement it, and the caller falls back to whatever other
// evidence it has.
//
// The one caller today is the retrospective golden builder, which wants the
// moment an engineer picked the ticket up — the first move into an
// in-progress status — as the cutoff for a bundle that must not contain the
// fix. Transitions are returned oldest first.
type Transitioner interface {
	Transitions(ctx context.Context, key string) ([]Transition, error)
}

// SelfAssignee is the value ListFilter.Assignee takes to mean "whoever
// this workspace's credentials belong to".
const SelfAssignee = "me"

// resolvesSelf names the built-in adapters that resolve SelfAssignee
// against their own credentials: Jira with currentUser(), Linear against
// the token's own user, Azure DevOps with @Me, Rally with the logged-in
// user's _ref, ServiceNow with gs.getUserID(). Each one knows which account
// it is authenticated as, which no caller of theirs does.
var resolvesSelf = map[string]bool{
	"jira":       true,
	"linear":     true,
	"azdo":       true,
	"rally":      true,
	"servicenow": true,
}

// ResolvesSelf reports whether the adapter named turns an Assignee of "me"
// into its own account by itself.
//
// It is false for `exec`, and it has to be: an external adapter is handed
// the filter verbatim over stdio, and the protocol says an assignee it
// cannot resolve must be an error rather than an empty list. So a caller
// asking one of those for "my tickets" writes out the address instead of
// hoping the other side knows who "me" is.
func ResolvesSelf(adapter string) bool { return resolvesSelf[strings.TrimSpace(adapter)] }

// IsSelf reports whether an assignee filter is the literal "me".
func IsSelf(assignee string) bool {
	return strings.EqualFold(strings.TrimSpace(assignee), SelfAssignee)
}

// InProgress reports whether a status name means work had started, folding
// case and dropping the separators trackers disagree about: "In Progress",
// "in_progress" and "inprogress" are one status.
func InProgress(status string) bool {
	var b strings.Builder
	for _, r := range strings.ToLower(status) {
		switch r {
		case '_', '-', ' ', '.':
		default:
			b.WriteRune(r)
		}
	}
	return b.String() == "inprogress"
}
