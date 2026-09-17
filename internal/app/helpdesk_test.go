package app

import (
	"context"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// linkedHelpdesk is a helpdesk whose records carry the tracker key the way
// the adapters that know the link do: in the record's own fields.
type linkedHelpdesk struct{ hd ticket.HelpdeskTicket }

func (h linkedHelpdesk) Get(context.Context, string) (ticket.HelpdeskTicket, error) {
	return h.hd, nil
}

func (linkedHelpdesk) Threads(context.Context, string) (ticket.Thread, error) { return nil, nil }

func (linkedHelpdesk) Attachments(context.Context, string, string) ([]ticket.Attachment, error) {
	return nil, nil
}

func TestTrackerKeyOfReadsTheFieldsThenTheSubject(t *testing.T) {
	for _, tc := range []struct {
		name string
		hd   ticket.HelpdeskTicket
		want string
	}{
		{"a field carrying the link", ticket.HelpdeskTicket{Fields: map[string]string{"ticketIds": "OMNI-3233"}}, "OMNI-3233"},
		{"the subject when no field has one", ticket.HelpdeskTicket{Subject: "[SBX-1] export is empty"}, "SBX-1"},
		{"a field wins over the subject", ticket.HelpdeskTicket{
			Subject: "[SBX-1] export is empty",
			Fields:  map[string]string{"ticketIds": "OMNI-3233"},
		}, "OMNI-3233"},
		{"the first field by name, so the answer does not move", ticket.HelpdeskTicket{
			Fields: map[string]string{"zzz": "OMNI-9", "aaa": "OMNI-1"},
		}, "OMNI-1"},
		{"nothing that is a key", ticket.HelpdeskTicket{
			Subject: "refund is late",
			Fields:  map[string]string{"tags": "billing, urgent", "via": "email"},
		}, ""},
		{"a lower-case word with a number is not a key", ticket.HelpdeskTicket{Subject: "order-3 never shipped"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trackerKeyOf(tc.hd); got != tc.want {
				t.Errorf("trackerKeyOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveHelpdeskAnswersTheKeyOrTheReason(t *testing.T) {
	root := newWorkspace(t)
	wsID := WorkspaceID(root)

	linked := linkedHelpdesk{hd: ticket.HelpdeskTicket{
		ID: "25312", Subject: "Invoice total is off by one fils",
		Fields: map[string]string{"ticketIds": "OMNI-3233"},
	}}
	svc := newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, linked))
	got, err := svc.ResolveHelpdesk(context.Background(), wsID, "#25312")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "OMNI-3233" || got.Number != "25312" || got.Subject != linked.hd.Subject {
		t.Fatalf("link %+v", got)
	}

	// A record that names no tracker issue is an answer with a reason on
	// it, not an error: the composer prints the reason under the box.
	bare := linkedHelpdesk{hd: ticket.HelpdeskTicket{ID: "25312", Subject: "refund is late"}}
	svc = newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, bare))
	got, err = svc.ResolveHelpdesk(context.Background(), wsID, "25312")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "" || got.Reason == "" {
		t.Fatalf("link %+v, want no key and a reason", got)
	}

	// A workspace with no helpdesk says so rather than failing.
	svc = newService(t, root, stubBuilder(&stubProvider{}, stubTracker{}, nil))
	got, err = svc.ResolveHelpdesk(context.Background(), wsID, "25312")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "" || got.Reason == "" {
		t.Fatalf("link %+v, want the no-helpdesk reason", got)
	}

	if _, err := svc.ResolveHelpdesk(context.Background(), wsID, "not-a-number"); err == nil {
		t.Error("a number that is not one should be refused")
	}
}
