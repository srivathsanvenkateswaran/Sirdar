package rally

import (
	"context"
	"testing"
)

func TestGetCarriesTypeAndParentKey(t *testing.T) {
	fs := newServer(t, artifactHandler)
	c := newClient(t, fs, Config{Workspace: "/workspace/111", HelpdeskField: "c_ZendeskTicketID"})

	defect, err := c.Get(context.Background(), "DE1234")
	if err != nil {
		t.Fatalf("Get DE1234: %v", err)
	}
	// Rally calls a bug a Defect, which is the whole reason the canonical
	// vocabulary exists.
	if defect.Type != "bug" {
		t.Errorf("Type = %q, want bug", defect.Type)
	}
	if defect.ParentKey != "F42" {
		t.Errorf("ParentKey = %q, want the portfolio Feature F42", defect.ParentKey)
	}
	if defect.Fields["type"] != "Defect" {
		t.Errorf("Fields[type] = %q, want Rally's own name", defect.Fields["type"])
	}

	story, err := c.Get(context.Background(), "US777")
	if err != nil {
		t.Fatalf("Get US777: %v", err)
	}
	if story.Type != "story" {
		t.Errorf("Type = %q, want story (HierarchicalRequirement)", story.Type)
	}
}
