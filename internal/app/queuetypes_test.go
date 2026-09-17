package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// queueFixture is one of each kind a tracker might hand back, including the
// untyped ticket an adapter that names no type produces.
var queueFixture = []ticket.TrackerTicket{
	{Key: "OMNI-1", Title: "Export fails", Type: "bug", Status: "open"},
	{Key: "OMNI-2", Title: "Write the migration", Type: "subtask", Status: "open"},
	{Key: "OMNI-3", Title: "Checkout revamp", Type: "story", Status: "open"},
	{Key: "OMNI-4", Title: "Site down", Type: "incident", Status: "open"},
	{Key: "OMNI-5", Title: "From an adapter that says nothing", Status: "open"},
}

// withQueueTypes rewrites the workspace's config so its tracker carries the
// given queue block, e.g. "\n    queue:\n      types: [bug]".
func withQueueTypes(t *testing.T, root, block string) {
	t.Helper()
	body := configYAML + `sources:
  tracker:
    adapter: exec
    command: /bin/true` + block + "\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func queueKeys(t *testing.T, root string, f QueueFilter) []string {
	t.Helper()
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{list: queueFixture}, stubHelpdesk{}))
	rows, err := svc.Queue(context.Background(), WorkspaceID(root), f)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.Key)
	}
	return keys
}

func TestQueueFiltersByType(t *testing.T) {
	for _, tc := range []struct {
		name   string
		block  string
		filter QueueFilter
		want   []string
	}{
		{
			name: "the default is bugs alone, and drops the untyped ticket",
			want: []string{"OMNI-1"},
		},
		{
			name:  "an explicit list",
			block: "\n    queue:\n      types: [bug, incident]",
			want:  []string{"OMNI-1", "OMNI-4"},
		},
		{
			name:  "a list written in the tracker's own words",
			block: "\n    queue:\n      types: [Defect, Sub-task]",
			want:  []string{"OMNI-1", "OMNI-2"},
		},
		{
			// The untyped ticket comes back too: an operator who turned
			// the filter off must not still be missing tickets.
			name:  "the wildcard shows everything",
			block: "\n    queue:\n      types: [\"*\"]",
			want:  []string{"OMNI-1", "OMNI-2", "OMNI-3", "OMNI-4", "OMNI-5"},
		},
		{
			name:  "an explicitly empty list shows everything too",
			block: "\n    queue:\n      types: []",
			want:  []string{"OMNI-1", "OMNI-2", "OMNI-3", "OMNI-4", "OMNI-5"},
		},
		{
			name:   "a caller's list overrides the configured one",
			block:  "\n    queue:\n      types: [bug]",
			filter: QueueFilter{Types: []string{"story"}},
			want:   []string{"OMNI-3"},
		},
		{
			name:   "a caller's empty list is the wildcard, not the default",
			block:  "\n    queue:\n      types: [bug]",
			filter: QueueFilter{Types: []string{}},
			want:   []string{"OMNI-1", "OMNI-2", "OMNI-3", "OMNI-4", "OMNI-5"},
		},
		{
			name:   "a nil list from the caller means the configured default",
			block:  "\n    queue:\n      types: [story]",
			filter: QueueFilter{},
			want:   []string{"OMNI-3"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newWorkspace(t)
			if tc.block != "" {
				withQueueTypes(t, root, tc.block)
			}
			got := queueKeys(t, root, tc.filter)
			if len(got) != len(tc.want) {
				t.Fatalf("keys = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("keys = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestDoctorWarnsWhenNoTicketCarriesAType: the filter is right to drop an
// untyped ticket, but an operator staring at an empty board needs to be
// told that is what happened and what to do about it.
func TestDoctorWarnsWhenNoTicketCarriesAType(t *testing.T) {
	const name, shown = "sources.tracker queue", "types=bug"
	untyped := []ticket.TrackerTicket{{Key: "OMNI-1"}, {Key: "OMNI-2"}}

	got := queueTypesVerdict(name, shown, untyped)
	if got.OK != true || got.Level != "warn" {
		t.Fatalf("check = %+v, want an advisory warning", got)
	}
	for _, want := range []string{shown, "2 tickets", `queue.types: ["*"]`} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail %q does not mention %q", got.Detail, want)
		}
	}

	// One typed ticket is enough to say the tracker types its records.
	mixed := append([]ticket.TrackerTicket{{Key: "OMNI-3", Type: "story"}}, untyped...)
	if got := queueTypesVerdict(name, shown, mixed); got.Level == "warn" {
		t.Errorf("check = %+v, want no warning when a ticket carries a type", got)
	}
	// Nothing assigned to the reader says nothing about the tracker.
	if got := queueTypesVerdict(name, shown, nil); got.Level == "warn" {
		t.Errorf("check = %+v, want no warning on an empty list", got)
	}
}

// TestQueueRowCarriesTheCanonicalType: the lane's chip reads off this, so
// it has to be the folded name and not whatever the tracker wrote.
func TestQueueRowCarriesTheCanonicalType(t *testing.T) {
	root := newWorkspace(t)
	withQueueTypes(t, root, "\n    queue:\n      types: [\"*\"]")
	svc := newService(t, root, stubBuilder(&stubProvider{script: replay()}, stubTracker{list: []ticket.TrackerTicket{
		{Key: "OMNI-1", Title: "Export fails", Type: "Defect"},
		{Key: "OMNI-5", Title: "Untyped"},
	}}, stubHelpdesk{}))
	rows, err := svc.Queue(context.Background(), WorkspaceID(root), QueueFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Type != "bug" {
		t.Errorf("Type = %q, want bug", rows[0].Type)
	}
	if rows[1].Type != "" {
		t.Errorf("an untyped ticket reported type %q, want empty", rows[1].Type)
	}
}
