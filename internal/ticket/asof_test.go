package ticket

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// retroSample is one closed ticket whose fix has already merged: the
// engineer picked it up at 10:00, and everything after that — the PR link,
// the "fixed in" comment, the screenshot of the green build — is what a
// retrospective bundle must not contain.
func retroSample() (Bundle, time.Time) {
	t0 := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	pickup := t0.Add(time.Hour)
	return Bundle{
		Tracker: &TrackerTicket{
			Key:         "OMNI-7",
			Title:       "Export times out",
			Description: "CSV export times out over 500 rows.\nFixed by https://github.com/acme/omni/pull/482.",
			Fields: map[string]string{
				"prs":     "https://github.com/acme/omni/pull/482, https://github.com/acme/omni/pull/489",
				"epic":    "OMNI-100",
				"release": "see MR !17 for the backport",
			},
		},
		Helpdesk: &HelpdeskTicket{
			ID:     "555",
			Fields: map[string]string{"pr_url": "https://gitlab.acme.dev/omni/api/-/merge_requests/31"},
		},
		Thread: Thread{
			{At: t0, Author: "Customer", Role: RoleCustomer, Text: "Export never finishes.", AttachmentIDs: []string{"a1"}},
			{At: pickup, Author: "L2", Role: RoleAgent, Text: "Taking a look."},
			{At: pickup.Add(time.Hour), Author: "L2", Role: RoleAgent, Text: "Fix is up: PR #482 and https://github.com/acme/omni/pull/489", AttachmentIDs: []string{"a2"}},
			{At: pickup.Add(2 * time.Hour), Author: "Customer", Role: RoleCustomer, Text: "Confirmed fixed."},
		},
		Attachments: []Attachment{
			{ID: "a1", Name: "error.png", MIME: "image/png", Path: "attachments/1-error.png"},
			{ID: "a2", Name: "green-build.png", MIME: "image/png", Path: "attachments/2-green-build.png"},
			{ID: "a3", Name: "orphan.txt", MIME: "text/plain", Path: "attachments/3-orphan.txt"},
		},
	}, pickup
}

func TestApplyAsOfDropsLaterMessagesAndTheirAttachments(t *testing.T) {
	b, asOf := retroSample()
	c, dropped := ApplyAsOf(&b, asOf)

	if c.CommentsDropped != 2 {
		t.Errorf("CommentsDropped = %d, want 2", c.CommentsDropped)
	}
	if len(b.Thread) != 2 {
		t.Fatalf("thread kept %d messages, want 2: %+v", len(b.Thread), b.Thread)
	}
	if b.Thread[1].Text != "Taking a look." {
		t.Errorf("last kept message is %q", b.Thread[1].Text)
	}

	// a2 only ever appeared on a dropped message, so it goes; a1 is on a
	// kept one and a3 is on none at all, so neither is datable as later.
	if c.AttachmentsDropped != 1 || len(dropped) != 1 || dropped[0].ID != "a2" {
		t.Fatalf("dropped %d attachments (%+v), want only a2", c.AttachmentsDropped, dropped)
	}
	var ids []string
	for _, a := range b.Attachments {
		ids = append(ids, a.ID)
	}
	if strings.Join(ids, ",") != "a1,a3" {
		t.Errorf("attachments kept = %v, want a1 and a3", ids)
	}
}

func TestApplyAsOfRedactsEveryPullRequestReference(t *testing.T) {
	b, asOf := retroSample()
	c, _ := ApplyAsOf(&b, asOf)

	rendered := b.Tracker.Title + "\n" + b.Tracker.Description + "\n" +
		b.Helpdesk.Fields["pr_url"] + "\n" + b.Tracker.Fields["prs"] + "\n" +
		b.Tracker.Fields["release"] + "\n" + ThreadMarkdown(b.Thread, b.Attachments)

	for _, leak := range []string{"pull/482", "pull/489", "merge_requests/31", "PR #482", "MR !17", "github.com"} {
		if strings.Contains(rendered, leak) {
			t.Errorf("the as-of bundle still names %q:\n%s", leak, rendered)
		}
	}
	if !strings.Contains(b.Tracker.Description, RedactionMarker) {
		t.Errorf("description was not marked: %q", b.Tracker.Description)
	}
	// The whole of a field that holds nothing but pull requests goes, not
	// just the URLs inside it.
	if b.Tracker.Fields["prs"] != RedactionMarker {
		t.Errorf("prs field = %q, want it replaced whole", b.Tracker.Fields["prs"])
	}
	if b.Helpdesk.Fields["pr_url"] != RedactionMarker {
		t.Errorf("pr_url field = %q, want it replaced whole", b.Helpdesk.Fields["pr_url"])
	}
	// A field that is not a pull-request field keeps what is not one.
	if b.Tracker.Fields["epic"] != "OMNI-100" {
		t.Errorf("epic = %q, want it untouched", b.Tracker.Fields["epic"])
	}
	if !strings.HasPrefix(b.Tracker.Fields["release"], "see ") {
		t.Errorf("release = %q, want only the reference redacted", b.Tracker.Fields["release"])
	}
	// description 1 + prs 1 + release 1 + pr_url 1; the thread's two are
	// on messages that were dropped before they could be scanned.
	if c.PRLinks != 4 {
		t.Errorf("PRLinks = %d, want 4", c.PRLinks)
	}
}

// A reference on a message that survives the cutoff is redacted in place:
// an engineer who linked a PR before picking the ticket up (a duplicate, a
// revert) still gives the answer away.
func TestApplyAsOfRedactsSurvivingMessages(t *testing.T) {
	at := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	b := Bundle{Thread: Thread{{At: at, Text: "Looks like the same thing as PR#77, see https://github.com/acme/omni/pull/77"}}}
	c, _ := ApplyAsOf(&b, at.Add(time.Hour))

	if strings.Contains(b.Thread[0].Text, "77") {
		t.Errorf("message still names the pull request: %q", b.Thread[0].Text)
	}
	if c.PRLinks != 2 {
		t.Errorf("PRLinks = %d, want 2", c.PRLinks)
	}
}

// A message an adapter could not date is kept. A missing timestamp is a gap
// in the source, and treating it as "after the cutoff" would empty the
// bundle for whichever helpdesk reports one.
func TestApplyAsOfKeepsUndatedMessages(t *testing.T) {
	b := Bundle{Thread: Thread{{Text: "no timestamp"}}}
	c, _ := ApplyAsOf(&b, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	if c.CommentsDropped != 0 || len(b.Thread) != 1 {
		t.Fatalf("dropped an undated message: %+v", b.Thread)
	}
}

// Ordinary prose that happens to carry a "#" or a slash is not a pull
// request, and over-redaction costs the session the evidence it needs.
func TestApplyAsOfLeavesOrdinaryTextAlone(t *testing.T) {
	at := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	const text = "Order #90210 failed in internal/export/csv.go:412; ticket OMNI-3217 covers the retry."
	b := Bundle{Thread: Thread{{At: at, Text: text}}}
	c, _ := ApplyAsOf(&b, at)
	if b.Thread[0].Text != text || c.PRLinks != 0 {
		t.Errorf("redacted ordinary prose: %q (%d links)", b.Thread[0].Text, c.PRLinks)
	}
}

// The cutoff is written into the bundle directory, so a reader who opens a
// golden entry can see what was taken out without reading the run state.
func TestWriteBundleWritesTheCutoffManifest(t *testing.T) {
	dir := t.TempDir()
	b, asOf := retroSample()
	ApplyAsOf(&b, asOf)
	if err := WriteBundle(dir, b); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Cutoff
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !got.AsOf.Equal(asOf) || got.CommentsDropped != 2 || got.AttachmentsDropped != 1 || got.PRLinks != 4 {
		t.Fatalf("manifest = %+v", got)
	}

	// A live bundle has no cutoff and gets no manifest.
	plain := t.TempDir()
	if err := WriteBundle(plain, sample()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plain, "manifest.json")); !os.IsNotExist(err) {
		t.Errorf("a bundle with no cutoff wrote a manifest: %v", err)
	}
}
