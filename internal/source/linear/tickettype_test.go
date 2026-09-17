package linear

import (
	"context"
	"testing"
)

func TestIssueTypeReadsTheTypeLabel(t *testing.T) {
	label := func(names ...string) labelConn {
		var l labelConn
		for _, n := range names {
			l.Nodes = append(l.Nodes, struct {
				Name string `json:"name"`
			}{Name: n})
		}
		return l
	}
	for _, tc := range []struct {
		name string
		in   labelConn
		want string
	}{
		{"no labels", label(), ""},
		{"a bug label", label("Bug"), "bug"},
		{"a component label alone", label("payments"), ""},
		{"the type label after a component one", label("payments", "Bug"), "bug"},
		{"a defect label folds onto bug", label("Defect"), "bug"},
		{"feature", label("Feature"), "feature"},
		{"the first type label wins", label("Feature", "Bug"), "feature"},
	} {
		if got := issueType(tc.in); got != tc.want {
			t.Errorf("%s: issueType = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGetCarriesTypeAndParentKey(t *testing.T) {
	f := newFakeLinear(t, serveFixtures(t, map[string]string{"Issue": "issue.json"}))
	c := newTestClient(t, f, "")

	got, err := c.Get(context.Background(), "ENG-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The fixture carries labels "bug" and "payments".
	if got.Type != "bug" {
		t.Errorf("Type = %q, want bug", got.Type)
	}
	if got.Fields["labels"] != "bug, payments" {
		t.Errorf("Fields[labels] = %q, want the labels untouched", got.Fields["labels"])
	}
}
