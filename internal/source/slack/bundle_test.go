package slack

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// fixtureThread is an invented support request posted as a DM: no tracker
// key and no helpdesk number, a PR link into another repository, and a file.
func fixtureThread() Thread {
	l, _ := FindLink("https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059")
	return Thread{
		Link:     l,
		IsThread: true,
		Messages: []Message{
			{
				TS: "1791100254.656059", At: time.Date(2026, 10, 4, 7, 50, 54, 0, time.UTC), Author: "Rana Example",
				Text: "*Coupon totals are wrong on the receipt for one merchant after the last release*\n" +
					"*CompanyID:* 4417\n*Domain:* shop.example.test\n" +
					"*Issue:* the receipt shows the coupon twice\n*Expected:* one discount line\n*Actual:* two discount lines\n" +
					"PR: <https://github.com/acme-co/Billing.Service/pull/412|Billing.Service#412>",
				Files: []File{{Name: "receipt.png", URL: "https://files.slack.com/files-pri/T0FAKE-F0FAKE/receipt.png"}},
			},
			{TS: "1791100300.000100", At: time.Date(2026, 10, 4, 7, 51, 40, 0, time.UTC), Author: "Sam Engineer", Text: "looking, thanks"},
		},
	}
}

func TestThreadBundle(t *testing.T) {
	th := fixtureThread()
	key := KeyFor(th.Link)
	if key != "SLACK-D0FAKEDM01-1791100254" || !IsKey(key) {
		t.Fatalf("key %q", key)
	}
	b := th.Bundle(key)
	if b.Tracker != nil || b.Helpdesk != nil || b.Reported == nil {
		t.Fatalf("a Slack-only bundle has no tracker or helpdesk: %+v", b)
	}
	r := b.Reported
	if b.Key() != key || r.Source != "slack" || r.Author != "Rana Example" || r.URL != th.Link.URL {
		t.Errorf("reported %+v", r)
	}
	if r.Title != "Coupon totals are wrong on the receipt for one merchant after the last release" {
		t.Errorf("title %q", r.Title)
	}
	if r.Description != strings.TrimSpace(th.Messages[0].Text) {
		t.Errorf("description %q", r.Description)
	}
	want := []ticket.Field{
		{Name: "CompanyID", Value: "4417"}, {Name: "Domain", Value: "shop.example.test"},
		{Name: "Issue", Value: "the receipt shows the coupon twice"}, {Name: "Expected", Value: "one discount line"},
		{Name: "Actual", Value: "two discount lines"}, {Name: "PR", Value: "https://github.com/acme-co/Billing.Service/pull/412"},
	}
	if !reflect.DeepEqual(r.Fields, want) {
		t.Errorf("fields\n got %+v\nwant %+v", r.Fields, want)
	}
	if !reflect.DeepEqual(r.Links, []string{"https://github.com/acme-co/Billing.Service/pull/412"}) {
		t.Errorf("links %v", r.Links)
	}
	if len(b.Thread) != 2 || b.Thread[0].Role != ticket.RoleCustomer || b.Thread[1].Role != ticket.RoleAgent || b.Thread[1].Author != "Sam Engineer" {
		t.Errorf("thread %+v", b.Thread)
	}
	if files := th.Files(); len(files) != 1 || files[0].Name != "receipt.png" {
		t.Errorf("files %+v", files)
	}

	// The manifest says where the bundle came from and that both sources
	// are absent.
	dir := t.TempDir()
	if err := ticket.WriteBundle(dir, b); err != nil {
		t.Fatal(err)
	}
	m, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || !strings.Contains(string(m), `"source": "slack"`) || !strings.Contains(string(m), `"tracker": "absent"`) || !strings.Contains(string(m), `"helpdesk": "absent"`) {
		t.Fatalf("manifest %s %v", m, err)
	}
}

func TestKeyForAReplyUsesTheThread(t *testing.T) {
	l, _ := FindLink("https://acme.slack.com/archives/C0FAKE0001/p1791100299000100?thread_ts=1791100254.656059&cid=C0FAKE0001")
	if got := KeyFor(l); got != "SLACK-C0FAKE0001-1791100254" {
		t.Fatalf("KeyFor = %q", got)
	}
	if IsKey("SBX-1") || IsKey("SLACK-x-1") {
		t.Error("only the synthetic shape is a Slack key")
	}
}

func TestTitle(t *testing.T) {
	long := strings.Repeat("ع", 120)
	if got := Title("\n\n  " + long + "\nsecond"); len([]rune(got)) != 80 || !strings.HasSuffix(got, "…") {
		t.Errorf("an Arabic title is cut to 80 characters on a rune boundary: %q (%d)", got, len([]rune(got)))
	}
	if got := Title("_*hello*_ world"); got != "hello world" {
		t.Errorf("markup: %q", got)
	}
}

func TestFieldsSkipsURLsAndBlankValues(t *testing.T) {
	got := Fields("https://example.test/a\nNote:\n• Merchant: Café Ünïcode\n10:30 the job ran")
	want := []ticket.Field{{Name: "Merchant", Value: "Café Ünïcode"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields = %+v", got)
	}
}

func TestDownloadSendsTheTokenToSlackFilesOnly(t *testing.T) {
	c, seen := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG fake"))
	})
	dest := filepath.Join(t.TempDir(), "receipt.png")
	if err := c.Download(context.Background(), "https://files.slack.com/files-pri/T0FAKE-F0FAKE/receipt.png", dest, 1<<20); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); !strings.HasPrefix(string(data), "\x89PNG") {
		t.Fatalf("saved %q", data)
	}
	if len(seen.reqs) != 1 || seen.reqs[0].Header.Get("Authorization") != "Bearer xoxp-test-token" {
		t.Fatalf("requests %v", seen.reqs)
	}
	if err := c.Download(context.Background(), "https://evil.example/receipt.png", dest, 1<<20); err == nil {
		t.Fatal("a file URL off files.slack.com was downloaded")
	}
	if len(seen.reqs) != 1 {
		t.Fatal("the refused download made a request")
	}
}
