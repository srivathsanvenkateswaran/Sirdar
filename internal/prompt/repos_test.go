package prompt

import (
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
)

func TestRepositoriesSection(t *testing.T) {
	list := []repos.Repo{
		{Name: "acme-api", Path: "/src/acme-api", Origin: "git@github.com:acme/acme-api.git", Workspace: true},
		{Name: "Acme.Web", Path: "/src/Acme.Web", Origin: "https://github.com/acme/Acme.Web.git", About: "web frontend"},
	}
	in := TriageInput{Repositories: Repositories{
		List: list,
		Mentions: []repos.Mention{
			{Name: "acme-api", Ref: "acme/acme-api#1", Status: repos.StatusWorkspace},
			{Name: "Acme.Web", Slug: "acme/Acme.Web", Ref: "acme/Acme.Web#828", Status: repos.StatusCompanion},
			{Name: "Billing.Service", Slug: "other/Billing.Service", Ref: "other/Billing.Service#2", Status: repos.StatusUnknown},
		},
		Asks: []repos.Ask{
			{Phrase: "web", Name: "Acme.Web", Status: repos.StatusCompanion},
			{Phrase: "Ledger.Core", Status: repos.StatusUnknown},
		},
	}}
	got := Triage(in)
	for _, want := range []string{
		"# Repositories\n\nYou are running in the workspace repository, acme-api at /src/acme-api (origin git@github.com:acme/acme-api.git). It is the only repository a fix is ever made in.",
		"- Acme.Web — /src/Acme.Web (origin https://github.com/acme/Acme.Web.git): web frontend",
		"write proposedFix.files as `<repository name>/<path>` (for example `Acme.Web/src/…`)",
		"- mentions Acme.Web (companion repo) — named by acme/Acme.Web#828",
		"- mentions Billing.Service (not configured — add it under repos:) — named by other/Billing.Service#2",
		"a repository this workspace has no clone of",
		"The operator asked you to look in Acme.Web.",
		"The operator asked you to look in Ledger.Core, which is not a configured repository",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(got, "mentions acme-api") {
		t.Error("the workspace's own repository is listed as a mention")
	}
	// The section sits ahead of the playbooks, so the Bundle pane's
	// playbook reader never runs into it.
	if strings.Index(got, "# Repositories") > strings.Index(got, "# Playbooks") {
		t.Error("# Repositories comes after # Playbooks")
	}

	// A workspace with no companions and nothing mentioned reads as before.
	plain := Triage(TriageInput{Repositories: Repositories{List: list[:1]}})
	if strings.Contains(plain, "# Repositories") {
		t.Error("a section with nothing to say")
	}
}
