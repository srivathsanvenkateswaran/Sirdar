package prompt

import (
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

func slackOnlyBundle() ticket.Bundle {
	return ticket.Bundle{
		Reported: &ticket.ReportedTicket{
			Source: "slack", Key: "SLACK-D0FAKEDM01-1791100254", Title: "Coupon totals are wrong on the receipt",
			Description: "Coupon totals are wrong on the receipt\nCompanyID: 4417", Author: "Rana Example",
			URL: "https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059", At: time.Date(2026, 10, 4, 7, 50, 54, 0, time.UTC),
			Fields: []ticket.Field{{Name: "CompanyID", Value: "4417"}, {Name: "Domain", Value: "shop.example.test"}},
			Links:  []string{"https://github.com/acme-co/Billing.Service/pull/412"},
		},
		Thread:             ticket.Thread{{Author: "Rana Example", Role: ticket.RoleCustomer, Text: "Coupon totals are wrong"}},
		SkippedAttachments: []ticket.SkippedAttachment{{Name: "receipt.png", Reason: "read through the Slack MCP, which gives no file access"}},
	}
}

func TestTriagePromptForASlackOnlyTicket(t *testing.T) {
	p := Triage(TriageInput{Bundle: slackOnlyBundle(), BundleDir: "/b", ThreadHead: "x"})
	for _, want := range []string{
		"This was reported in Slack by Rana Example. There is no tracker or helpdesk ticket for it yet",
		"Key: SLACK-D0FAKEDM01-1791100254",
		"Title: Coupon totals are wrong on the receipt",
		"Reported: in Slack by Rana Example at 2026-10-04T07:50:54Z",
		"Link: https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059",
		"Tracker URL: (none)",
		"- CompanyID: 4417\n- Domain: shop.example.test",
		"Links in the thread:\n- https://github.com/acme-co/Billing.Service/pull/412",
		"- receipt.png (not in the bundle: read through the Slack MCP, which gives no file access)",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "## Code in another repository") {
		t.Error("no other repos were named, so there is no section")
	}
}

func TestPromptNamesTheOtherRepository(t *testing.T) {
	in := TriageInput{Bundle: slackOnlyBundle(), BundleDir: "/b", ThreadHead: "x", OtherRepos: []string{"acme-co/Billing.Service"}, Origin: "acme-co/web-app"}
	for name, p := range map[string]string{"triage": Triage(in), "rca": RCA(RCAInput{TriageInput: in})} {
		if !strings.Contains(p, "## Code in another repository") || !strings.Contains(p, "The ticket names acme-co/Billing.Service, and this workspace is acme-co/web-app.") ||
			!strings.Contains(p, "may live in that repository") {
			t.Errorf("%s prompt:\n%s", name, p)
		}
	}
}
