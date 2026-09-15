package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// retroPickup is when the engineer took the synthetic ticket: everything
// after it is the fix, and a bundle built as of it must hold none of it.
var retroPickup = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

// retroTracker is a tracker whose issue carries the merged pull request in
// its description and in a `prs` field, the way a closed ticket does.
type retroTracker struct{}

func (retroTracker) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	return ticket.TrackerTicket{
		Key:         key,
		Title:       "Export times out",
		Description: "CSV export times out over 500 rows. Fixed by https://github.com/acme/omni/pull/482.",
		HelpdeskRef: "555",
		Fields:      map[string]string{"prs": "https://github.com/acme/omni/pull/482", "epic": "OMNI-100"},
	}, nil
}

func (retroTracker) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	return nil, nil
}

// retroHelpdesk serves the conversation either side of the pickup, with one
// screenshot from before it and one from after.
type retroHelpdesk struct{}

func (retroHelpdesk) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	return ticket.HelpdeskTicket{ID: id, Subject: "Export never finishes"}, nil
}

func (retroHelpdesk) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	return ticket.Thread{
		{At: retroPickup.Add(-time.Hour), Author: "Customer", Role: ticket.RoleCustomer, Text: "Export never finishes.", AttachmentIDs: []string{"a1"}},
		{At: retroPickup.Add(time.Hour), Author: "L2", Role: ticket.RoleAgent, Text: "Shipped in PR #482.", AttachmentIDs: []string{"a2"}},
		{At: retroPickup.Add(2 * time.Hour), Author: "Customer", Role: ticket.RoleCustomer, Text: "Confirmed."},
	}, nil
}

func (retroHelpdesk) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	for _, name := range []string{"1-error.png", "2-green-build.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("png"), 0o644); err != nil {
			return nil, err
		}
	}
	return []ticket.Attachment{
		{ID: "a1", Name: "error.png", MIME: "image/png", Path: "attachments/1-error.png"},
		{ID: "a2", Name: "green-build.png", MIME: "image/png", Path: "attachments/2-green-build.png"},
	}, nil
}

// A fetch with AsOf set produces the ticket as the engineer found it: the
// later conversation, the screenshot only that conversation pointed at, and
// every pull-request reference are gone, and the file behind the dropped
// attachment is deleted rather than left in the directory for a session to
// open anyway.
func TestFetchAsOfCutsTheBundleBackToThePickup(t *testing.T) {
	dir := t.TempDir()
	f := &Fetcher{Config: &config.Config{}, Tracker: retroTracker{}, Helpdesk: retroHelpdesk{}, AsOf: retroPickup}

	b, warnings, err := f.Fetch(context.Background(), "OMNI-7", dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(b.Thread) != 1 {
		t.Fatalf("thread kept %d messages, want 1: %+v", len(b.Thread), b.Thread)
	}
	if len(b.Attachments) != 1 || b.Attachments[0].ID != "a1" {
		t.Fatalf("attachments = %+v, want only a1", b.Attachments)
	}
	if _, err := os.Stat(filepath.Join(dir, "attachments", "2-green-build.png")); !os.IsNotExist(err) {
		t.Errorf("the dropped attachment's file is still on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "attachments", "1-error.png")); err != nil {
		t.Errorf("the kept attachment's file was deleted: %v", err)
	}

	if b.Cutoff == nil {
		t.Fatal("the bundle carries no cutoff")
	}
	if b.Cutoff.CommentsDropped != 2 || b.Cutoff.AttachmentsDropped != 1 || b.Cutoff.PRLinks != 2 {
		t.Errorf("cutoff = %+v", *b.Cutoff)
	}

	if strings.Contains(b.Tracker.Description, "pull/482") || b.Tracker.Fields["prs"] != ticket.RedactionMarker {
		t.Errorf("the tracker record still names the pull request: %+v", *b.Tracker)
	}
	if b.Tracker.Fields["epic"] != "OMNI-100" {
		t.Errorf("an unrelated field was redacted: %q", b.Tracker.Fields["epic"])
	}

	// The counts go to the operator, not to the agent: the prompt quotes
	// the bundle's own warnings, and telling a session being measured on
	// this ticket that two later comments exist is telling it the answer
	// arrived later.
	if len(b.Warnings) != 0 {
		t.Errorf("the cutoff leaked into the prompt's warnings: %v", b.Warnings)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "as of 2026-03-02T10:00:00Z") {
		t.Fatalf("operator warnings = %v", warnings)
	}

	// Written out, the bundle carries the manifest a reader opens.
	if err := ticket.WriteBundle(dir, b); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest ticket.Cutoff
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.AsOf.Equal(retroPickup) || manifest.PRLinks != 2 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if md, err := os.ReadFile(filepath.Join(dir, "thread.md")); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(md), "482") || strings.Contains(string(md), "Confirmed") {
		t.Errorf("thread.md still carries the fix:\n%s", md)
	}
}

// Without AsOf nothing is cut, and the bundle has no manifest at all.
func TestFetchWithoutAsOfIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	f := &Fetcher{Config: &config.Config{}, Tracker: retroTracker{}, Helpdesk: retroHelpdesk{}}

	b, _, err := f.Fetch(context.Background(), "OMNI-7", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Thread) != 3 || len(b.Attachments) != 2 || b.Cutoff != nil {
		t.Fatalf("a live fetch was cut: %d messages, %d attachments, cutoff %+v", len(b.Thread), len(b.Attachments), b.Cutoff)
	}
	if !strings.Contains(b.Tracker.Description, "pull/482") {
		t.Errorf("a live fetch redacted the pull request: %q", b.Tracker.Description)
	}
}
