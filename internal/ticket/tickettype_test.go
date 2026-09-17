package ticket

import "testing"

func TestCanonicalType(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"  ", ""},
		{"Bug", "bug"},
		{"BUG", "bug"},
		{"Defect", "bug"},
		{"Sub-task", "subtask"},
		{"sub_task", "subtask"},
		{"Sub Task", "subtask"},
		{"Story", "story"},
		{"User Story", "story"},
		{"HierarchicalRequirement", "story"},
		{"Product Backlog Item", "story"},
		{"PortfolioItem/Feature", "feature"},
		{"Epic", "epic"},
		{"Incident", "incident"},
		{"Task", "task"},
		// No alias: the tracker's own name, lower-cased and no more.
		{"Change Request", "change request"},
		{"  Support   Request ", "support request"},
	} {
		if got := CanonicalType(tc.in); got != tc.want {
			t.Errorf("CanonicalType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDeriveTypeAndParentFromFields(t *testing.T) {
	tests := []struct {
		name               string
		in                 TrackerTicket
		wantType, wantPare string
	}{
		{
			name:     "explicit fields win over the map",
			in:       TrackerTicket{Type: "Bug", ParentKey: "OMNI-1", Fields: map[string]string{"ticket_type": "Sub-task", "parent_key": "OMNI-9"}},
			wantType: "bug", wantPare: "OMNI-1",
		},
		{
			name:     "ticket_type is the private adapter's spelling",
			in:       TrackerTicket{Fields: map[string]string{"ticket_type": "Bug", "parent_key": "OMNI-2", "parent_type": "Story"}},
			wantType: "bug", wantPare: "OMNI-2",
		},
		{
			name:     "issuetype is the jira spelling",
			in:       TrackerTicket{Fields: map[string]string{"issuetype": "Sub-task", "parent": "OMNI-3"}},
			wantType: "subtask", wantPare: "OMNI-3",
		},
		{
			name:     "type is the generic spelling",
			in:       TrackerTicket{Fields: map[string]string{"type": "Defect"}},
			wantType: "bug", wantPare: "",
		},
		{
			name:     "ticket_type is preferred over the others",
			in:       TrackerTicket{Fields: map[string]string{"ticket_type": "Bug", "issuetype": "Story", "type": "Epic"}},
			wantType: "bug", wantPare: "",
		},
		{
			name:     "no type anywhere stays empty",
			in:       TrackerTicket{Fields: map[string]string{"project": "OMNI"}},
			wantType: "", wantPare: "",
		},
		{
			name:     "a nil Fields map is not a panic",
			in:       TrackerTicket{Type: "Story"},
			wantType: "story", wantPare: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in
			got.DeriveTypeAndParent()
			if got.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tc.wantType)
			}
			if got.ParentKey != tc.wantPare {
				t.Errorf("ParentKey = %q, want %q", got.ParentKey, tc.wantPare)
			}
			// The Fields entries stay: a note renders them.
			for k, v := range tc.in.Fields {
				if got.Fields[k] != v {
					t.Errorf("Fields[%q] = %q, want %q", k, got.Fields[k], v)
				}
			}
		})
	}
}
