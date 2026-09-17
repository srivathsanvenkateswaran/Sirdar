package jira

import (
	"context"
	"net/http"
	"testing"
)

func TestIssueTypeFoldsTheNameAndObeysTheSubtaskFlag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   *jiraIssueType
		want string
	}{
		{"no issuetype at all", nil, ""},
		{"bug", &jiraIssueType{Name: "Bug"}, "bug"},
		{"story", &jiraIssueType{Name: "Story"}, "story"},
		{"a named sub-task", &jiraIssueType{Name: "Sub-task", Subtask: true}, "subtask"},
		// The flag is what a project that renamed or localised its
		// sub-task type still reports, and it is the answer a bug queue
		// depends on.
		{"a renamed sub-task", &jiraIssueType{Name: "Technical Sub-task", Subtask: true}, "subtask"},
		{"an instance's own type", &jiraIssueType{Name: "Support"}, "support"},
	} {
		if got := issueType(tc.in); got != tc.want {
			t.Errorf("%s: issueType = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGetCarriesTypeAndParentKey(t *testing.T) {
	t.Parallel()
	ts, mux := startServer(t)
	mux.HandleFunc("/rest/api/2/issue/SUP-42", func(w http.ResponseWriter, r *http.Request) {
		ts.writeFixture(w, "issue_cloud.json")
	})

	c := newClient(t, ts, cloudConfig())
	got, err := c.Get(context.Background(), "SUP-42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Type != "support" {
		t.Errorf("Type = %q, want support", got.Type)
	}
	if got.ParentKey != "SUP-7" {
		t.Errorf("ParentKey = %q, want SUP-7", got.ParentKey)
	}
	// The field entry stays: a note renders it, and an adapter reader
	// looking for issuetype should still find it.
	if got.Fields["issuetype"] != "Support" {
		t.Errorf("Fields[issuetype] = %q, want Support", got.Fields["issuetype"])
	}
}
