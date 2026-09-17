package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// HelpdeskLink is what one helpdesk number resolves to: the tracker key the
// helpdesk record points at, or nothing with the reason it could not be
// found.
//
// Key empty is an ordinary answer and not an error. A person typing a
// helpdesk number into the composer needs to be told which of the three it
// was — the workspace reads no helpdesk, the record does not exist, or the
// record exists and names no tracker issue — and each of those is a
// different thing to do next.
type HelpdeskLink struct {
	Number string `json:"number"`
	Key    string `json:"key"`
	// Subject is the helpdesk record's own subject when it was read, so a
	// status line can show what was found even with no key on it.
	Subject string `json:"subject,omitempty"`
	// Reason says why Key is empty, in words a person reads. Empty when
	// there is a key.
	Reason string `json:"reason,omitempty"`
}

// helpdeskNumber is what this route accepts: digits, as a helpdesk writes a
// ticket number. It goes no further than the adapter, but it is checked
// here for the same reason every other identifier is.
var helpdeskNumber = regexp.MustCompile(`^[0-9]{1,32}$`)

// trackerKeyIn finds a tracker key in arbitrary text: two or more
// upper-case letters or digits starting with a letter, a dash, and digits.
// It is deliberately the same shape the composer's own parser uses, so a
// key the person could have typed is a key the resolver can find.
var trackerKeyIn = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+\b`)

// ResolveHelpdesk answers which tracker issue a helpdesk number belongs to.
//
// It is read-only: one Get against the workspace's helpdesk adapter, and no
// run, no job and no write of any kind. It exists because the composer lets
// a person paste the number they have — support engineers are handed
// helpdesk numbers, not tracker keys — and everything Sirdar files is filed
// under the tracker key.
//
// How the link is found is the honest part. ticket.HelpdeskTicket carries
// no tracker reference of its own: the link is modelled in the other
// direction, as TrackerTicket.HelpdeskRef, because that is the direction a
// triage run reads it in. So the key is looked for where the adapters
// actually put it — the record's own Fields, which is where an adapter that
// knows the link records it (Front's `ticketIds`, for one), and failing
// that the subject line, where a support process that types the key into
// the subject puts it. When neither has one, the answer says so rather than
// guessing, and the composer tells the person to type the key instead.
func (s *Service) ResolveHelpdesk(ctx context.Context, wsID, number string) (HelpdeskLink, error) {
	number = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(number), "#"))
	if !helpdeskNumber.MatchString(number) {
		return HelpdeskLink{}, fmt.Errorf("%w: %q is not a helpdesk number", ErrInvalidArgument, number)
	}
	_, cfg, err := s.load(wsID)
	if err != nil {
		return HelpdeskLink{}, err
	}
	deps, cleanup, err := s.build(cfg, "", "", s.stderr())
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return HelpdeskLink{}, err
	}
	if deps.Helpdesk == nil {
		return HelpdeskLink{Number: number, Reason: "this workspace reads no helpdesk, so a helpdesk number cannot be looked up"}, nil
	}

	hd, err := deps.Helpdesk.Get(ctx, number)
	if err != nil {
		var serr *source.Error
		if errors.As(err, &serr) {
			switch serr.Code {
			case source.NotFound:
				return HelpdeskLink{Number: number, Reason: "the helpdesk has no ticket " + number}, nil
			case source.Unsupported:
				return HelpdeskLink{}, ErrUnsupported
			}
		}
		return HelpdeskLink{}, err
	}

	link := HelpdeskLink{Number: number, Subject: hd.Subject}
	if key := trackerKeyOf(hd); key != "" {
		link.Key = key
		return link, nil
	}
	link.Reason = "the helpdesk record for " + number + " names no tracker issue"
	return link, nil
}

// trackerKeyOf is the first tracker key the helpdesk record carries: its
// fields first, read in name order so the answer does not depend on map
// iteration, then the subject. Empty when it carries none.
func trackerKeyOf(hd ticket.HelpdeskTicket) string {
	names := make([]string, 0, len(hd.Fields))
	for name := range hd.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if key := trackerKeyIn.FindString(hd.Fields[name]); key != "" {
			return key
		}
	}
	return trackerKeyIn.FindString(hd.Subject)
}
