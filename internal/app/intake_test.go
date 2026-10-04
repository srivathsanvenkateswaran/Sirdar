package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/repos"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/slack"
	"github.com/srivathsanvenkateswaran/sirdar/internal/testbin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

func TestRecognise(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		kind, val  string
	}{
		{"a bare key", "SBX-1", InputKey, "SBX-1"},
		{"a lower-case lone key", "sbx-1", InputKey, "SBX-1"},
		{"a key in a sentence", "triage SBX-1 please, the export is empty", InputKey, "SBX-1"},
		{"a key with Arabic around it", "العميل يقول SBX-1 لا يعمل", InputKey, "SBX-1"},
		{"a key touching Arabic", "التذكرةSBX-1،", InputKey, "SBX-1"},
		{"a tracker URL", "https://tracker.example.com/browse/SBX-1", InputTrackerURL, "SBX-1"},
		{"a helpdesk number", "look at #28310 when you can", InputHelpdeskNumber, "28310"},
		{"a helpdesk number in Arabic", "رقم التذكرة #28310 من فضلك", InputHelpdeskNumber, "28310"},
		{"a Zoho agent URL", "https://desk.zoho.com/agent/acme/support/tickets/details/123400000456789", InputHelpdeskURL, "123400000456789"},
		{"a Zoho classic URL", "https://desk.zoho.eu/support/acme/ShowHomePage.do#Cases/dv/123400000456789", InputHelpdeskURL, "123400000456789"},
		{"a Zoho URL on a regional host", "https://desk.zoho.com.au/agent/acme/tickets/123400000456789", InputHelpdeskURL, "123400000456789"},
		{"a Slack permalink", "https://acme.slack.com/archives/C0123ABCD/p1712345678901234", InputSlack, "https://acme.slack.com/archives/C0123ABCD/p1712345678901234"},
		{"a Slack permalink in a thread", "see https://acme.slack.com/archives/C0123ABCD/p1712345699000100?thread_ts=1712345678.901234&cid=C0123ABCD", InputSlack, "https://acme.slack.com/archives/C0123ABCD/p1712345699000100?thread_ts=1712345678.901234&cid=C0123ABCD"},
		{"a key wins over a number", "#28310 is SBX-1", InputKey, "SBX-1"},
		{"a number wins over a Slack link", "https://acme.slack.com/archives/C0123ABCD/p1712345678901234 #28310", InputHelpdeskNumber, "28310"},
		{"a look-alike Zoho host is a URL and nothing more", "https://desk.zoho.com.evil.example/agent/x/tickets/details/123400000456789", "", ""},
		{"a UUID is not a key", "ABCDEF12-3456-7890-ABCD-EF1234567890", "", ""},
		{"a short number is not a helpdesk number", "#12 is the room", "", ""},
		{"a GitHub reference is not a helpdesk number", "broke after acme/Acme.Web#12345", "", ""},
		{"prose", "the customer says the export is empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs := recognise(tc.text)
			if tc.kind == "" {
				if len(refs) != 0 {
					t.Fatalf("recognise = %+v, want nothing", refs)
				}
				return
			}
			if len(refs) == 0 || refs[0].kind != tc.kind || refs[0].value != tc.val {
				t.Fatalf("recognise = %+v, want %s %q first", refs, tc.kind, tc.val)
			}
		})
	}
}

// --- the fake exec adapter ------------------------------------------------

const intakeUUID = "5f3c9a1e-2b4d-4c6e-8f10-123456789abc"

// intakeTickets are the tracker the fake adapter serves: two tickets synced
// from the helpdesk, titled with its number the way the sync titles them,
// one unrelated, and an older duplicate title the newest-first match must
// not pick.
func intakeTickets() []ticket.TrackerTicket {
	day := func(d int) time.Time { return time.Date(2026, 9, d, 8, 0, 0, 0, time.UTC) }
	return []ticket.TrackerTicket{
		{Key: "SBX-4", Title: "#28310 an older ticket with the same number", CreatedAt: time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)},
		{Key: "SBX-1", Title: "#28310 Export is empty", HelpdeskRef: "900001", CreatedAt: day(1)},
		{Key: "SBX-2", Title: "#28311 Login fails", CreatedAt: day(2)},
		{Key: "SBX-3", Title: "Unrelated chore", CreatedAt: day(3)},
	}
}

// intakeAdapterMain is a tracker adapter in one of two modes. "honour"
// answers tracker.list's query with only the matches (and an unfiltered
// list with nothing, so a match proves the query reached it) and answers
// tracker.get by id. "ignore" is an adapter written before either existed:
// every list is the whole tracker, and tracker.get without a key is refused.
func intakeAdapterMain() int {
	args := testbin.Args()
	if len(args) != 1 {
		return testbin.Fail("intake adapter: want honour|ignore, got %q", args)
	}
	honour := args[0] == "honour"
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	reply := func(id int, v any) {
		b, _ := json.Marshal(v)
		fmt.Printf(`{"id":%d,"result":%s}`+"\n", id, b)
	}
	fail := func(id int, code, msg string) {
		fmt.Printf(`{"id":%d,"error":{"code":%q,"message":%q}}`+"\n", id, code, msg)
	}
	for in.Scan() {
		var req plugin.Request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			continue
		}
		var p struct {
			Key, ID, Query string
			Limit          int
		}
		_ = json.Unmarshal(req.Params, &p)
		switch req.Method {
		case "describe":
			reply(req.ID, plugin.Describe{Name: "intake", Roles: []string{"tracker"}, Version: "1"})
		case "tracker.get":
			if p.Key == "" && (!honour || p.ID == "") {
				fail(req.ID, "invalid_request", "key is required")
				continue
			}
			found := false
			for _, tt := range intakeTickets() {
				if tt.Key == p.Key || (honour && p.ID == intakeUUID && tt.Key == "SBX-2") {
					reply(req.ID, tt)
					found = true
					break
				}
			}
			if !found {
				fail(req.ID, "not_found", "no such ticket")
			}
		case "tracker.list":
			var out []ticket.TrackerTicket
			for _, tt := range intakeTickets() {
				switch {
				case !honour:
					out = append(out, tt)
				case p.Query != "" && (strings.Contains(tt.Title, p.Query) || tt.HelpdeskRef == p.Query):
					out = append(out, tt)
				}
			}
			if out == nil {
				out = []ticket.TrackerTicket{}
			}
			reply(req.ID, out)
		case "shutdown":
			return 0
		default:
			fail(req.ID, "unsupported", "no")
		}
	}
	return 0
}

func startIntakeAdapter(t *testing.T, mode string) *plugin.Client {
	t.Helper()
	bin := testbin.Install(t, t.TempDir(), "intakeadapter", "intakeadapter")
	c, err := plugin.Start(context.Background(), `"`+bin+`" `+mode, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// recordHelpdesk answers Get with the record whose id or ticket number was
// asked for, and NotFound for anything else.
type recordHelpdesk struct{ records []ticket.HelpdeskTicket }

func (h recordHelpdesk) Get(_ context.Context, id string) (ticket.HelpdeskTicket, error) {
	for _, r := range h.records {
		if r.ID == id || r.Fields["ticketNumber"] == id {
			return r, nil
		}
	}
	return ticket.HelpdeskTicket{}, &source.Error{Code: source.NotFound, Message: "no ticket " + id}
}

func (recordHelpdesk) Threads(context.Context, string) (ticket.Thread, error) { return nil, nil }

func (recordHelpdesk) Attachments(context.Context, string, string) ([]ticket.Attachment, error) {
	return nil, nil
}

func newTestResolver(t *testing.T, tracker source.Tracker, helpdesk source.Helpdesk) *intakeResolver {
	t.Helper()
	return &intakeResolver{
		tracker:      tracker,
		helpdesk:     helpdesk,
		helpdeskName: "Zoho",
		trackerField: config.DefaultZohoTrackerField,
		cache:        newIntakeCache(t.TempDir(), nil),
	}
}

func resolveOK(t *testing.T, r *intakeResolver, text string) Intake {
	t.Helper()
	in, err := r.resolve(context.Background(), text)
	if err != nil {
		t.Fatalf("resolve %q: %v", text, err)
	}
	return in
}

// --- helpdesk → tracker ---------------------------------------------------

func TestHelpdeskToTrackerWhenTheAdapterIgnoresQuery(t *testing.T) {
	r := newTestResolver(t, startIntakeAdapter(t, "ignore"), nil)
	in := resolveOK(t, r, "#28311")
	if in.Key != "SBX-2" || in.Summary != "#28311 → SBX-2 · matched by title" {
		t.Fatalf("intake %+v", in)
	}
	// The older ticket with the same title prefix loses to the newer one.
	in = resolveOK(t, r, "what about #28310?")
	if in.Key != "SBX-1" {
		t.Fatalf("intake %+v, want the newest #28310", in)
	}
}

func TestHelpdeskToTrackerWhenTheAdapterHonoursQuery(t *testing.T) {
	r := newTestResolver(t, startIntakeAdapter(t, "honour"), nil)
	in := resolveOK(t, r, "#28311")
	if in.Key != "SBX-2" || in.Via[0].Source != "tracker search" {
		t.Fatalf("intake %+v, want SBX-2 from the search", in)
	}
}

func TestHelpdeskURLMatchesByHelpdeskLink(t *testing.T) {
	hd := recordHelpdesk{records: []ticket.HelpdeskTicket{{ID: "900001", Subject: "Export is empty", Fields: map[string]string{"ticketNumber": "28310"}}}}
	r := newTestResolver(t, startIntakeAdapter(t, "ignore"), hd)
	in := resolveOK(t, r, "https://desk.zoho.com/agent/acme/support/tickets/details/900001")
	if in.Key != "SBX-1" || in.HelpdeskNumber != "28310" || in.HelpdeskID != "900001" {
		t.Fatalf("intake %+v", in)
	}
	if in.Summary != "#28310 → SBX-1 · matched by helpdesk link" {
		t.Fatalf("summary %q", in.Summary)
	}
}

// TestHelpdeskFieldHoldsAKey is the first step: a record whose tracker
// field names the key outright needs no tracker search at all.
func TestHelpdeskFieldHoldsAKey(t *testing.T) {
	hd := recordHelpdesk{records: []ticket.HelpdeskTicket{{ID: "900001", Fields: map[string]string{"ticketNumber": "28310", "cf.cf_jira_ticket_id": "SBX-3"}}}}
	r := newTestResolver(t, nil, hd)
	in := resolveOK(t, r, "#28310")
	if in.Key != "SBX-3" || in.Summary != "#28310 → SBX-3 · from Zoho field cf_jira_ticket_id" {
		t.Fatalf("intake %+v", in)
	}
}

// TestHelpdeskFieldHoldsAUUID covers a record synced recently, whose field
// holds the tracker's own id: a tracker that answers tracker.get by id
// resolves it, and one that does not falls back to the number.
func TestHelpdeskFieldHoldsAUUID(t *testing.T) {
	hd := recordHelpdesk{records: []ticket.HelpdeskTicket{{ID: "900002", Fields: map[string]string{"ticketNumber": "28311", "cf.cf_jira_ticket_id": intakeUUID}}}}

	r := newTestResolver(t, startIntakeAdapter(t, "honour"), hd)
	in := resolveOK(t, r, "#28311")
	if in.Key != "SBX-2" || in.Summary != "#28311 → SBX-2 · from Zoho field cf_jira_ticket_id, by tracker id" {
		t.Fatalf("by id: intake %+v", in)
	}

	r = newTestResolver(t, startIntakeAdapter(t, "ignore"), hd)
	in = resolveOK(t, r, "#28311")
	if in.Key != "SBX-2" || in.Summary != "#28311 → SBX-2 · matched by title" {
		t.Fatalf("fallback: intake %+v", in)
	}
}

func TestHelpdeskWithNoLinkAnywhere(t *testing.T) {
	hd := recordHelpdesk{records: []ticket.HelpdeskTicket{{ID: "900009", Subject: "refund is late", Fields: map[string]string{"ticketNumber": "99999"}}}}
	r := newTestResolver(t, startIntakeAdapter(t, "ignore"), hd)
	in := resolveOK(t, r, "#99999")
	if in.Key != "" || !strings.Contains(in.Reason, "no tracker issue names #99999") {
		t.Fatalf("intake %+v", in)
	}
}

func TestResolvedPairsAreRemembered(t *testing.T) {
	cache := newIntakeCache(t.TempDir(), nil)
	r := newTestResolver(t, startIntakeAdapter(t, "ignore"), nil)
	r.cache = cache
	if in := resolveOK(t, r, "#28311"); in.Key != "SBX-2" {
		t.Fatalf("first: %+v", in)
	}
	// The second lookup reads a tracker that fails every list: only the
	// cache can answer.
	r2 := newTestResolver(t, stubTracker{err: errors.New("tracker is down")}, nil)
	r2.cache = cache
	in := resolveOK(t, r2, "#28311")
	if in.Key != "SBX-2" || in.Via[0].Source != "remembered" || in.Summary != "#28311 → SBX-2 · matched by title" {
		t.Fatalf("remembered: %+v", in)
	}

	// A day later it is forgotten.
	cache.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if in := resolveOK(t, r2, "#28311"); in.Key != "" {
		t.Fatalf("a day-old pair should be forgotten: %+v", in)
	}
}

// --- tracker → helpdesk ---------------------------------------------------

func TestKeyCarriesItsHelpdeskTicket(t *testing.T) {
	r := newTestResolver(t, startIntakeAdapter(t, "ignore"), nil)
	in := resolveOK(t, r, "triage SBX-1, the export is empty")
	if in.Key != "SBX-1" || in.HelpdeskID != "900001" || in.HelpdeskNumber != "28310" || in.Summary != "SBX-1 · #28310" {
		t.Fatalf("intake %+v", in)
	}
	in = resolveOK(t, r, "https://tracker.example.com/browse/SBX-3")
	if in.Key != "SBX-3" || in.Input != InputTrackerURL || in.Summary != "SBX-3" {
		t.Fatalf("intake %+v", in)
	}
}

// --- Slack ------------------------------------------------------------------

type fakeSlack struct {
	thread slack.Thread
	err    error
	got    []slack.Link
}

func (f *fakeSlack) Read(_ context.Context, l slack.Link) (slack.Thread, error) {
	f.got = append(f.got, l)
	th := f.thread
	th.Link = l
	return th, f.err
}

func TestSlackThreadResolvesOnward(t *testing.T) {
	fs := &fakeSlack{thread: slack.Thread{IsThread: true, Messages: []slack.Message{
		{TS: "1712345678.901234", Author: "سارة", Text: "العميل يشتكي، التذكرة #28311", At: time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)},
		{TS: "1712345690.000001", Author: "sam", Text: "looking"},
	}}}
	r := newTestResolver(t, startIntakeAdapter(t, "ignore"), nil)
	r.slack = fs
	in := resolveOK(t, r, "https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	if in.Key != "SBX-2" || in.Input != InputSlack {
		t.Fatalf("intake %+v", in)
	}
	if in.Summary != "Slack thread → #28311 → SBX-2 · matched by title" {
		t.Fatalf("summary %q", in.Summary)
	}
	if in.Slack == nil || in.Slack.Messages != 2 || !in.Slack.Thread {
		t.Fatalf("slack %+v", in.Slack)
	}
	md := in.SlackMarkdown()
	if !strings.Contains(md, "## 2026-09-02T09:00:00Z · slack · سارة") || !strings.Contains(md, "العميل يشتكي") {
		t.Fatalf("slack.md:\n%s", md)
	}
	if fs.got[0].Channel != "C0123ABCD" || fs.got[0].TS != "1712345678.901234" {
		t.Fatalf("read %+v", fs.got)
	}
}

func TestSlackMessageNamingAKey(t *testing.T) {
	fs := &fakeSlack{thread: slack.Thread{Messages: []slack.Message{{TS: "1712345678.901234", Author: "sam", Text: "can someone look at SBX-1"}}}}
	r := newTestResolver(t, startIntakeAdapter(t, "ignore"), nil)
	r.slack = fs
	in := resolveOK(t, r, "https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	if in.Key != "SBX-1" || in.Summary != "Slack message → SBX-1 · #28310" {
		t.Fatalf("intake %+v", in)
	}
}

func TestSlackNotConfiguredSaysWhatToSet(t *testing.T) {
	r := newTestResolver(t, nil, nil)
	in := resolveOK(t, r, "https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	if in.Key != "" || in.Reason != SlackNotConfigured {
		t.Fatalf("intake %+v", in)
	}
}

func TestSlackThreadWithNoReference(t *testing.T) {
	r := newTestResolver(t, nil, nil)
	r.slack = &fakeSlack{thread: slack.Thread{Messages: []slack.Message{{TS: "1712345678.901234", Text: "anyone around?"}}}}
	in := resolveOK(t, r, "https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	if in.Key != "SLACK-C0123ABCD-1712345678" || !in.SlackOnly || in.Reason != "" || in.Summary != "Slack message · no ticket yet · will triage the message" {
		t.Fatalf("intake %+v", in)
	}
}

// TestSlackThreadWithNoTicketTriagesTheThread is the DM support request: a
// thread with a CompanyID, a domain and a PR link but no tracker key or
// helpdesk number. The chip says the thread will be triaged, and that the
// PR's repository is not one the workspace has configured.
func TestSlackThreadWithNoTicketTriagesTheThread(t *testing.T) {
	r := newTestResolver(t, nil, nil)
	r.repos = []repos.Repo{{Name: "web-app", Path: "/src/web-app", Origin: "git@github.com:acme-co/web-app.git", Workspace: true}}
	r.slack = &fakeSlack{thread: slack.Thread{IsThread: true, Messages: []slack.Message{
		{TS: "1791100254.656059", Author: "Rana Example", Text: "*Coupon totals are wrong on the receipt*\n*CompanyID:* 4417\n*Domain:* shop.example.test\nPR: <https://github.com/acme-co/Billing.Service/pull/412|#412>",
			Files: []slack.File{{Name: "receipt.png"}}},
		{TS: "1791100300.000100", Author: "Sam Engineer", Text: "looking"},
	}}}
	in := resolveOK(t, r, "https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059")
	want := "Slack thread · no ticket yet · will triage the thread · mentions Billing.Service (not configured — add it under repos:)"
	if in.Key != "SLACK-D0FAKEDM01-1791100254" || !in.SlackOnly || in.Summary != want || in.Subject != "Coupon totals are wrong on the receipt" {
		t.Fatalf("intake %+v", in)
	}
	if in.SlackMarkdown() != "" {
		t.Error("a Slack-only intake's thread is its conversation, not an extra slack.md")
	}
	rb := in.Reported()
	if rb == nil || rb.Bundle.Key() != in.Key || rb.Bundle.Tracker != nil || len(rb.Files) != 1 || rb.Download != nil || rb.UnreadReason == "" {
		t.Fatalf("reported %+v", rb)
	}
}

// TestRepoMismatchOnATicket: a tracker record naming another repository
// says so in the chip too, and the workspace's own is not worth a word.
func TestRepoMismatchOnATicket(t *testing.T) {
	r := newTestResolver(t, nil, nil)
	r.repos = []repos.Repo{{Name: "web-app", Path: "/src/web-app", Origin: "git@github.com:acme-co/web-app.git", Workspace: true}}
	got := r.mentions([]string{"see https://github.com/acme-co/web-app/pull/3 and https://github.com/acme-co/Billing.Service/pull/412"})
	if len(got) != 1 || got[0].Slug != "acme-co/Billing.Service" || got[0].Status != repos.StatusUnknown {
		t.Fatalf("mentions = %+v", got)
	}
	in := Intake{Key: "SBX-1", Repos: got}
	if s := intakeSummary(in); s != "SBX-1 · mentions Billing.Service (not configured — add it under repos:)" {
		t.Fatalf("summary %q", s)
	}
}

// TestSlackOnlyKeyTypedOutright is the key of a Slack-only run, which holds
// a tracker-key-shaped piece (D0FAKEDM01-1791100254) that must not be
// resolved on its own.
func TestSlackOnlyKeyTypedOutright(t *testing.T) {
	r := newTestResolver(t, nil, nil)
	in := resolveOK(t, r, "SLACK-D0FAKEDM01-1791100254")
	if in.Key != "SLACK-D0FAKEDM01-1791100254" || in.Input != InputKey {
		t.Fatalf("intake %+v", in)
	}
}

func TestCheckSlackOnly(t *testing.T) {
	link := "https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059"
	for _, c := range []struct {
		keys []string
		link string
		ok   bool
	}{
		{[]string{"SLACK-D0FAKEDM01-1791100254"}, link, true},
		{[]string{"SBX-1"}, "", true},
		{[]string{"SLACK-D0FAKEDM01-1791100254"}, "", false},
		{[]string{"SLACK-D0FAKEDM01-1791100999"}, link, false},
		{[]string{"SLACK-D0FAKEDM01-1791100254", "SBX-1"}, link, false},
	} {
		if err := checkSlackOnly(c.keys, c.link); (err == nil) != c.ok {
			t.Errorf("checkSlackOnly(%v, %q) = %v", c.keys, c.link, err)
		}
	}
}

func TestSlackReadFailureIsAReason(t *testing.T) {
	r := newTestResolver(t, nil, nil)
	r.slack = &fakeSlack{err: &source.Error{Code: source.Auth, Message: "slack: conversations.history: missing_scope"}}
	in := resolveOK(t, r, "https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	if in.Key != "" || !strings.Contains(in.Reason, "missing_scope") {
		t.Fatalf("intake %+v", in)
	}
}

// --- plain text -------------------------------------------------------------

func TestPlainTextFallsBackToTheModel(t *testing.T) {
	r := newTestResolver(t, nil, nil)
	in := resolveOK(t, r, "the customer says the export is empty")
	if in.Key != "" || in.Reason == "" {
		t.Fatalf("with no fallback: %+v", in)
	}
	r.fallback = func(context.Context, string) (ComposedIntent, error) {
		return ComposedIntent{Key: "SBX-1", Mode: "triage", Instruction: "the export", Confidence: 0.8}, nil
	}
	in = resolveOK(t, r, "the customer says the export is empty")
	if in.Key != "SBX-1" || in.Mode != "triage" || in.Summary != "your words → SBX-1 · read by the model" {
		t.Fatalf("with the model: %+v", in)
	}
}

// --- the service route ------------------------------------------------------

func TestServiceResolveUsesTheWorkspaceSources(t *testing.T) {
	root := newWorkspace(t)
	wsID := WorkspaceID(root)
	fs := &fakeSlack{thread: slack.Thread{Messages: []slack.Message{{TS: "1712345678.901234", Text: "SBX-7 again"}}}}
	svc := New(newRegistry(t, root), stubBuilder(&stubProvider{}, stubTracker{}, nil), Options{
		Interval: 20 * time.Millisecond,
		Slack:    func(*config.Config) (SlackReader, error) { return fs, nil },
	})
	in, err := svc.Resolve(context.Background(), wsID, "https://acme.slack.com/archives/C0123ABCD/p1712345678901234")
	if err != nil {
		t.Fatal(err)
	}
	if in.Key != "SBX-7" || in.Input != InputSlack {
		t.Fatalf("intake %+v", in)
	}
	if _, err := svc.Resolve(context.Background(), wsID, "   "); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty text: %v", err)
	}
}

// TestStartTriageFromASlackOnlyThread: Enter on a "no ticket yet" chip
// starts a triage under the synthetic key, built from the thread with no
// tracker or helpdesk call, and the run's card says it came from Slack.
func TestStartTriageFromASlackOnlyThread(t *testing.T) {
	root := newWorkspace(t)
	wsID := WorkspaceID(root)
	link := "https://acme.slack.com/archives/D0FAKEDM01/p1791100254656059"
	key := "SLACK-D0FAKEDM01-1791100254"
	fs := &fakeSlack{thread: slack.Thread{IsThread: true, Messages: []slack.Message{
		{TS: "1791100254.656059", Author: "Rana Example", Text: "Coupon totals are wrong on the receipt\nCompanyID: 4417"},
		{TS: "1791100300.000100", Author: "Sam Engineer", Text: "looking"},
	}}}
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	tracker := &countingTracker{}
	svc := New(newRegistry(t, root), stubBuilder(p, tracker, nil), Options{
		Interval: 20 * time.Millisecond,
		Slack:    func(*config.Config) (SlackReader, error) { return fs, nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	t.Cleanup(svc.Stop)
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	if _, err := svc.StartTriage(context.Background(), wsID, []string{key}, TriageOptions{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("a Slack-only key with no link: %v", err)
	}
	if _, err := svc.StartTriage(context.Background(), wsID, []string{key}, TriageOptions{Slack: link}); err != nil {
		t.Fatal(err)
	}
	done := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
	if len(done.Outcomes) != 1 || done.Outcomes[0].RunID == "" || done.Outcomes[0].Key != key {
		t.Fatalf("outcomes %+v", done.Outcomes)
	}
	if tracker.calls() != 0 {
		t.Errorf("the tracker was called %d times for a Slack-only run", tracker.calls())
	}
	detail, err := svc.Run(wsID, done.Outcomes[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Source != "slack" || detail.Title != "Coupon totals are wrong on the receipt" {
		t.Fatalf("summary %+v", detail.RunSummary)
	}
	if _, err := os.Stat(detail.BundleDir + "/slack.md"); !os.IsNotExist(err) {
		t.Error("the thread is the conversation; no slack.md beside it")
	}
}

// countingTracker fails every call and counts them.
type countingTracker struct {
	mu sync.Mutex
	n  int
}

func (c *countingTracker) calls() int { c.mu.Lock(); defer c.mu.Unlock(); return c.n }
func (c *countingTracker) bump()      { c.mu.Lock(); c.n++; c.mu.Unlock() }
func (c *countingTracker) Get(context.Context, string) (ticket.TrackerTicket, error) {
	c.bump()
	return ticket.TrackerTicket{}, errors.New("no tracker here")
}
func (c *countingTracker) List(context.Context, source.ListFilter) ([]ticket.TrackerTicket, error) {
	c.bump()
	return nil, errors.New("no tracker here")
}

// TestStartTriageCarriesTheSlackThread is the start half: a triage started
// from a Slack link reads the thread again and files it as bundle/slack.md.
func TestStartTriageCarriesTheSlackThread(t *testing.T) {
	root := newWorkspace(t)
	wsID := WorkspaceID(root)
	fs := &fakeSlack{thread: slack.Thread{Messages: []slack.Message{{TS: "1712345678.901234", Author: "sam", Text: "OMNI-1 export is empty"}}}}
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	svc := New(newRegistry(t, root), stubBuilder(p, stubTracker{}, stubHelpdesk{}), Options{
		Interval: 20 * time.Millisecond,
		Slack:    func(*config.Config) (SlackReader, error) { return fs, nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	t.Cleanup(svc.Stop)
	events, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	if _, err := svc.StartTriage(context.Background(), wsID, []string{"OMNI-1"}, TriageOptions{Slack: "https://example.org/not-slack"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("a link that is not Slack's: %v", err)
	}
	if _, err := svc.StartTriage(context.Background(), wsID, []string{"OMNI-1"}, TriageOptions{Slack: "https://acme.slack.com/archives/C0123ABCD/p1712345678901234"}); err != nil {
		t.Fatal(err)
	}
	done := waitFor(t, events, "job.finished", func(e Event) bool { return e.Kind == KindJobFinished })
	if len(done.Outcomes) != 1 || done.Outcomes[0].RunID == "" {
		t.Fatalf("outcomes %+v", done.Outcomes)
	}
	detail, err := svc.Run(wsID, done.Outcomes[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(detail.BundleDir + "/slack.md")
	if err != nil || !strings.Contains(string(data), "OMNI-1 export is empty") {
		t.Fatalf("slack.md: %q %v", data, err)
	}
}
