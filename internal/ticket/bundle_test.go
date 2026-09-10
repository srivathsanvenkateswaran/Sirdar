package ticket

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sample() Bundle {
	t0 := time.Date(2026, 9, 10, 8, 30, 0, 0, time.FixedZone("KSA", 3*3600))
	return Bundle{
		Tracker:  &TrackerTicket{Key: "OMNI-1", Title: "Export fails", HelpdeskRef: "555", URL: "https://t/OMNI-1"},
		Helpdesk: &HelpdeskTicket{ID: "555", Subject: "تصدير", Customer: "شركة", CustomerID: "4561", URL: "https://h/555"},
		Thread: Thread{
			{At: t0, Author: "Customer", Role: "customer", Text: "لا يعمل التصدير", AttachmentIDs: []string{"a1"}},
			{At: t0.Add(time.Hour), Author: "L1", Role: "agent", Text: "سنتحقق"},
		},
		Attachments: []Attachment{{ID: "a1", Name: "shot.png", MIME: "image/png", Path: "attachments/1-shot.png"}},
	}
}

func TestThreadMarkdownGolden(t *testing.T) {
	got := ThreadMarkdown(sample().Thread, sample().Attachments)
	want, _ := os.ReadFile("testdata/thread.golden.md")
	if os.Getenv("UPDATE_GOLDEN") != "" { os.WriteFile("testdata/thread.golden.md", []byte(got), 0o644); return }
	if got != string(want) { t.Fatalf("golden mismatch:\n%s", got) }
}

func TestWriteBundle(t *testing.T) {
	dir := t.TempDir()
	if err := WriteBundle(dir, sample()); err != nil { t.Fatal(err) }
	raw, err := os.ReadFile(filepath.Join(dir, "ticket.json"))
	if err != nil { t.Fatal(err) }
	var back Bundle
	if err := json.Unmarshal(raw, &back); err != nil { t.Fatal(err) }
	if back.Tracker.Key != "OMNI-1" || back.Helpdesk.CustomerID != "4561" { t.Fatalf("%+v", back) }
	if _, err := os.Stat(filepath.Join(dir, "thread.md")); err != nil { t.Fatal(err) }
	if sample().Key() != "OMNI-1" { t.Fatal("key") }
	if (Bundle{Helpdesk: &HelpdeskTicket{ID: "9"}}).Key() != "9" { t.Fatal("helpdesk key fallback") }
}
