package app

import (
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/slack"
)

// TestIntakeNamesTheRepositoriesAThreadMentions pins the chip's repository
// words: a thread naming a companion's pull request reads "mentions
// Acme.Web (companion repo)", one naming a repository nobody configured
// says where to add it, and the workspace's own repository is not worth a
// word.
func TestIntakeNamesTheRepositoriesAThreadMentions(t *testing.T) {
	fs := &fakeSlack{thread: slack.Thread{IsThread: true, Messages: []slack.Message{
		{TS: "1712345678.901234", Author: "sam", Text: "SBX-1 broke after https://github.com/acme/Acme.Web/pull/828"},
		{TS: "1712345690.000001", Author: "lee", Text: "or other/Billing.Service#12, or acme/acme-api#3"},
	}}}
	r := newTestResolver(t, startIntakeAdapter(t, "ignore"), nil)
	r.slack = fs
	r.repos = []repos.Repo{
		{Name: "acme-api", Path: "/src/acme-api", Origin: "git@github.com:acme/acme-api.git", Workspace: true},
		{Name: "Acme.Web", Path: "/src/Acme.Web", Origin: "git@github.com:acme/Acme.Web.git"},
	}
	in := resolveOK(t, r, "https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	if in.Key != "SBX-1" {
		t.Fatalf("intake %+v", in)
	}
	want := "Slack thread → SBX-1 · #28310 · mentions Acme.Web (companion repo) · mentions Billing.Service (not configured — add it under repos:)"
	if in.Summary != want {
		t.Fatalf("summary\n got  %q\n want %q", in.Summary, want)
	}
	if len(in.Repos) != 2 || in.Repos[0].Status != repos.StatusCompanion || in.Repos[1].Slug != "other/Billing.Service" {
		t.Fatalf("repos %+v", in.Repos)
	}

	// A key typed outright with nothing mentioned says what it said before.
	r.slack = nil
	if in := resolveOK(t, r, "SBX-1"); in.Summary != "SBX-1 · #28310" || len(in.Repos) != 0 {
		t.Fatalf("plain key %+v", in)
	}
}
