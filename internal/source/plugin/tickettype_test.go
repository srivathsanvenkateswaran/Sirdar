package plugin

import (
	"bytes"
	"context"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// TestTypeFallsBackToTheAdaptersFields covers the compatibility the
// protocol change rests on: an adapter written before TrackerTicket had a
// Type reports its type as a field, and its tickets must still carry one.
func TestTypeFallsBackToTheAdaptersFields(t *testing.T) {
	bin := buildFileAdapter(t)
	c, err := Start(context.Background(), quoted(bin)+` -file "testdata/tickets.json"`, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	list, err := c.List(context.Background(), source.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byKey := map[string]ticket.TrackerTicket{}
	for _, tt := range list {
		byKey[tt.Key] = tt
	}

	for _, tc := range []struct{ key, wantType, wantParent string }{
		// No type anywhere: the ticket keeps an empty one rather than
		// being given a guess.
		{"OMNI-1", "", ""},
		// The private-adapter spelling, read off Fields and folded.
		{"OMNI-2", "bug", "OMNI-9"},
		// The adapter sent the fields outright; they are only folded.
		{"OMNI-3", "subtask", "OMNI-9"},
	} {
		got, ok := byKey[tc.key]
		if !ok {
			t.Fatalf("%s missing from the list", tc.key)
		}
		if got.Type != tc.wantType {
			t.Errorf("%s: Type = %q, want %q", tc.key, got.Type, tc.wantType)
		}
		if got.ParentKey != tc.wantParent {
			t.Errorf("%s: ParentKey = %q, want %q", tc.key, got.ParentKey, tc.wantParent)
		}
	}
	// The fields the adapter sent are still there for a note to render.
	if got := byKey["OMNI-2"].Fields["ticket_type"]; got != "Bug" {
		t.Errorf("Fields[ticket_type] = %q, want Bug", got)
	}

	// Get takes the same path.
	one, err := c.Get(context.Background(), "OMNI-2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if one.Type != "bug" || one.ParentKey != "OMNI-9" {
		t.Errorf("Get: Type/ParentKey = %q/%q, want bug/OMNI-9", one.Type, one.ParentKey)
	}
}
