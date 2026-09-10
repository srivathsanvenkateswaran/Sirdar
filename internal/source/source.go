// Package source defines the contract between Sirdar's triage loop and the
// tracker/helpdesk systems it reads tickets from. Implementations live
// out-of-process, behind the stdio adapter protocol in the plugin
// subpackage, so this package stays free of any particular tracker or
// helpdesk vendor.
package source

import (
	"context"

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
type Warner interface {
	// Warnings returns the problems recorded by the most recent call, or
	// nothing when there were none.
	Warnings() []string
}
