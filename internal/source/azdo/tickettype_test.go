package azdo

import (
	"context"
	"testing"
)

func TestGetCarriesTypeAndParentKey(t *testing.T) {
	_, c := newFixtureServer(t)

	tk, err := c.Get(context.Background(), "4242")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// System.WorkItemType is "Bug" and System.Parent 4000 on the fixture.
	if tk.Type != "bug" {
		t.Errorf("Type = %q, want bug", tk.Type)
	}
	if tk.ParentKey != "4000" {
		t.Errorf("ParentKey = %q, want 4000", tk.ParentKey)
	}
	if tk.Fields["workItemType"] != "Bug" {
		t.Errorf("Fields[workItemType] = %q, want the process template's own name", tk.Fields["workItemType"])
	}
}
