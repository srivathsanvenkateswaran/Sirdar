package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

const slackKey = "SLACK-D0FAKEDM01-1791100254"

// slackOnly is an invented report built the way slack.Thread.Bundle builds
// one: no tracker, no helpdesk, a PR link into another repository.
func slackOnly() *ReportedBundle {
	return &ReportedBundle{
		Bundle: ticket.Bundle{
			Reported: &ticket.ReportedTicket{
				Source: "slack", Key: slackKey, Title: "Coupon totals are wrong on the receipt",
				Description: "Coupon totals are wrong on the receipt\nCompanyID: 4417\nPR: https://github.com/acme-co/Billing.Service/pull/412",
				Author:      "Rana Example", URL: "https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059",
				At:     time.Date(2026, 10, 4, 7, 50, 54, 0, time.UTC),
				Fields: []ticket.Field{{Name: "CompanyID", Value: "4417"}},
				Links:  []string{"https://github.com/acme-co/Billing.Service/pull/412"},
			},
			Thread: ticket.Thread{
				{At: time.Date(2026, 10, 4, 7, 50, 54, 0, time.UTC), Author: "Rana Example", Role: ticket.RoleCustomer, Text: "Coupon totals are wrong on the receipt"},
				{At: time.Date(2026, 10, 4, 7, 51, 40, 0, time.UTC), Author: "Sam Engineer", Role: ticket.RoleAgent, Text: "looking"},
			},
		},
		Files:        []ReportedFile{{Name: "receipt.png", URL: "https://files.slack.com/files-pri/T0-F0/receipt.png"}},
		UnreadReason: "read through the Slack MCP, which gives no file access",
	}
}

func gitOrigin(t *testing.T, root, origin string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", origin}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

// TestSlackOnlyTriage: a thread with no ticket is triaged from its own
// bundle — no tracker or helpdesk call, the run and the note filed under the
// synthetic key, the files named as unread, and the prompt saying who
// reported it and that the PR's repository is not this one.
func TestSlackOnlyTriage(t *testing.T) {
	cfg := newWorkspace(t)
	gitOrigin(t, cfg.Root, "git@github.com:acme-co/web-app.git")
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, nil, nil)

	outs, err := r.Triage(context.Background(), []string{slackKey}, Options{DryRun: true, Reported: slackOnly()})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].Key != slackKey || outs[0].State.Status == store.StatusFailed {
		t.Fatalf("outcome %+v", outs[0])
	}
	runs, _ := filepath.Glob(filepath.Join(cfg.Root, ".sirdar", "runs", slackKey, "*"))
	if len(runs) != 1 {
		t.Fatalf("runs %v", runs)
	}
	dir := runs[0]
	var b ticket.Bundle
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "bundle", "ticket.json"))), &b); err != nil {
		t.Fatal(err)
	}
	if b.Tracker != nil || b.Helpdesk != nil || b.Reported == nil || b.Key() != slackKey {
		t.Fatalf("bundle %+v", b)
	}
	if len(b.SkippedAttachments) != 1 || b.SkippedAttachments[0].Name != "receipt.png" || !strings.Contains(b.SkippedAttachments[0].Reason, "Slack MCP") {
		t.Errorf("skipped %+v", b.SkippedAttachments)
	}
	if m := readFile(t, filepath.Join(dir, "bundle", "manifest.json")); !strings.Contains(m, `"tracker": "absent"`) {
		t.Errorf("manifest %s", m)
	}
	prompt := readFile(t, filepath.Join(dir, "prompt.md"))
	for _, want := range []string{
		"This was reported in Slack by Rana Example",
		"Key: " + slackKey,
		"## Code in another repository",
		"The ticket names acme-co/Billing.Service, and this workspace is acme-co/web-app.",
		"Sam Engineer",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if !strings.Contains(strings.Join(outs[0].State.Warnings, "\n"), "reported in Slack and has none yet") {
		t.Errorf("warnings %v", outs[0].State.Warnings)
	}
}

// TestSlackOnlyTriageFilesTheNote: the whole run, with the note filed in
// the notes directory under the synthetic key.
func TestSlackOnlyTriageFilesTheNote(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, nil, nil)
	outs, err := r.Triage(context.Background(), []string{slackKey}, Options{Reported: slackOnly()})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %s: %s", outs[0].State.Status, outs[0].State.Reason)
	}
	notes, _ := filepath.Glob(filepath.Join(cfg.ExpandPath(cfg.Notes.Dir), slackKey+"*.md"))
	if len(notes) != 1 {
		t.Fatalf("notes %v", notes)
	}
}

func TestSlackOnlyDownloadsWithAToken(t *testing.T) {
	cfg := newWorkspace(t)
	rb := slackOnly()
	var asked string
	rb.Download = func(ctx context.Context, url, dest string, max int64) error {
		asked = url
		return os.WriteFile(dest, []byte("\x89PNG"), 0o644)
	}
	r := newRunner(cfg, &stubProvider{script: replay(finalEvent(triageDoc))}, nil, nil)
	if _, err := r.Triage(context.Background(), []string{slackKey}, Options{DryRun: true, Reported: rb}); err != nil {
		t.Fatal(err)
	}
	runs, _ := filepath.Glob(filepath.Join(cfg.Root, ".sirdar", "runs", slackKey, "*"))
	var b ticket.Bundle
	_ = json.Unmarshal([]byte(readFile(t, filepath.Join(runs[0], "bundle", "ticket.json"))), &b)
	if asked != rb.Files[0].URL || len(b.Attachments) != 1 || b.Attachments[0].Path != filepath.Join("attachments", "receipt.png") || len(b.SkippedAttachments) != 0 {
		t.Fatalf("asked %q, attachments %+v, skipped %+v", asked, b.Attachments, b.SkippedAttachments)
	}

	// A download that fails names the file as unread with the reason.
	rb = slackOnly()
	rb.Download = func(ctx context.Context, url, dest string, max int64) error { return errors.New("status 403") }
	cfg = newWorkspace(t)
	r = newRunner(cfg, &stubProvider{script: replay(finalEvent(triageDoc))}, nil, nil)
	if _, err := r.Triage(context.Background(), []string{slackKey}, Options{DryRun: true, Reported: rb}); err != nil {
		t.Fatal(err)
	}
	runs, _ = filepath.Glob(filepath.Join(cfg.Root, ".sirdar", "runs", slackKey, "*"))
	b = ticket.Bundle{}
	_ = json.Unmarshal([]byte(readFile(t, filepath.Join(runs[0], "bundle", "ticket.json"))), &b)
	if len(b.SkippedAttachments) != 1 || !strings.Contains(b.SkippedAttachments[0].Reason, "status 403") {
		t.Fatalf("skipped %+v", b.SkippedAttachments)
	}
}

func TestSlackOnlyTakesOneKey(t *testing.T) {
	r := newRunner(newWorkspace(t), &stubProvider{}, nil, nil)
	if _, err := r.Triage(context.Background(), []string{slackKey, "OMNI-1"}, Options{Reported: slackOnly()}); err == nil {
		t.Fatal("a reported bundle beside another key was accepted")
	}
}
