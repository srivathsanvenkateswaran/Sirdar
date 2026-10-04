package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// slackOnlyDoc is what the fake Claude answers the Slack MCP reading with:
// an invented DM support request with no tracker key or helpdesk number.
const slackOnlyDoc = `{"messages":[
 {"author":"Rana Example <rana@example.test> (U0FAKE001)","time":"2026-10-04T07:50:54Z","text":"*Coupon totals are wrong on the receipt*\n*CompanyID:* 4417\n*Domain:* shop.example.test\nPR: <https://github.com/acme-co/Billing.Service/pull/412|#412>","files":[{"name":"receipt.png","url":""}]},
 {"author":"Sam Engineer","time":"2026-10-04T07:51:40Z","text":"looking"}],
 "refs":[]}`

// TestTriageASlackThreadWithNoTicket is `sirdar triage <permalink>` on a
// thread that names no ticket, read through the operator's Slack MCP
// server: the chip line is printed, and the run is filed under the
// synthetic key with a bundle built from the thread.
func TestTriageASlackThreadWithNoTicket(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:acme-co/web-app.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	body := fmt.Sprintf(`workspace: sirdar-test
provider: claude
mcp:
  userServers: [slack]
notes:
  dir: %s
playbooks: .sirdar/playbooks
providers:
  claude:
    path: %s
`, filepath.Join(t.TempDir(), "notes"), fakeClaude)
	if err := os.MkdirAll(filepath.Join(root, ".sirdar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prompt.ScaffoldPlaybooks(filepath.Join(root, ".sirdar", "playbooks")); err != nil {
		t.Fatal(err)
	}
	cli := t.TempDir()
	if err := os.WriteFile(filepath.Join(cli, ".claude.json"), []byte(`{"mcpServers":{"slack":{"type":"http","url":"https://mcp.example/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", cli)
	t.Setenv("HOME", t.TempDir()) // the reading's transcript goes under the user dir
	doc := filepath.Join(t.TempDir(), "slack.json")
	if err := os.WriteFile(doc, []byte(slackOnlyDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIRDAR_FAKE_DOC", doc)
	chdir(t, root)

	key := "SLACK-D0FAKEDM01-1791100254"
	_, stderr := mustRun(t, 0, "triage", "--dry-run", "https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059")
	if !strings.Contains(stderr, "Slack thread · no ticket yet · will triage the thread · mentions Billing.Service (not configured — add it under repos:)") {
		t.Errorf("the chip line was not printed:\n%s", stderr)
	}
	runs, _ := filepath.Glob(filepath.Join(root, ".sirdar", "runs", key, "*"))
	if len(runs) != 1 {
		t.Fatalf("want one %s run, got %v\n%s", key, runs, stderr)
	}
	var b ticket.Bundle
	raw, err := os.ReadFile(filepath.Join(runs[0], "bundle", "ticket.json"))
	if err != nil || json.Unmarshal(raw, &b) != nil {
		t.Fatalf("bundle: %v", err)
	}
	if b.Reported == nil || b.Tracker != nil || b.Reported.Author != "Rana Example" || b.Reported.Title != "Coupon totals are wrong on the receipt" {
		t.Fatalf("reported %+v", b.Reported)
	}
	if len(b.SkippedAttachments) != 1 || b.SkippedAttachments[0].Name != "receipt.png" {
		t.Errorf("the file is named as unread: %+v", b.SkippedAttachments)
	}
	p, _ := os.ReadFile(filepath.Join(runs[0], "prompt.md"))
	if !strings.Contains(string(p), "This was reported in Slack by Rana Example") || !strings.Contains(string(p), "## Code in another repository") {
		t.Errorf("prompt:\n%s", p)
	}
	if strings.Contains(string(p), "rana@example.test") {
		t.Error("the author's address reached the prompt")
	}

	// Two tickets beside a Slack-only thread are refused.
	_, stderr = mustRun(t, 1, "triage", "--dry-run", "SBX-1", "https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059")
	if !strings.Contains(stderr, "triaged on its own") {
		t.Errorf("stderr:\n%s", stderr)
	}
}
