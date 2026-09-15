package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- fixtures ---------------------------------------------------------

const playbookMarker = "PLAYBOOK-MARKER: always check the export path."

// sampleBundle is the Task 3 sample bundle, copied here so the run tests
// do not depend on another package's test fixtures.
func sampleBundle() ticket.Bundle {
	t0 := time.Date(2026, 9, 10, 8, 30, 0, 0, time.FixedZone("KSA", 3*3600))
	return ticket.Bundle{
		Tracker: &ticket.TrackerTicket{
			Key: "OMNI-1", Title: "Export fails", HelpdeskRef: "555",
			URL: "https://t/OMNI-1", Priority: "high", CreatedAt: t0,
		},
		Helpdesk: &ticket.HelpdeskTicket{
			ID: "555", Subject: "تصدير", Customer: "شركة", CustomerID: "4561",
			URL: "https://h/555", Priority: "high", CreatedAt: t0,
		},
		Thread: ticket.Thread{
			{At: t0, Author: "Customer", Role: ticket.RoleCustomer, Text: "لا يعمل التصدير", AttachmentIDs: []string{"a1"}},
			{At: t0.Add(time.Hour), Author: "L1", Role: ticket.RoleAgent, Text: "سنتحقق"},
		},
		Attachments: []ticket.Attachment{{ID: "a1", Name: "shot.png", MIME: "image/png", Path: "attachments/1-shot.png"}},
	}
}

const triageDoc = `{
  "ticket": {"key":"OMNI-1","title":"Export fails","trackerUrl":"https://t/OMNI-1","helpdeskId":"555","helpdeskUrl":"https://h/555","priority":"high","service":"omni","customer":"شركة","customerId":"4561","customerIds":null},
  "title": "Export fails for large orders",
  "complaint": "The export fails for large orders. It has happened every day this week.",
  "complaintOriginal": null,
  "customerReplyDraft": null,
  "timeline": [{"at":"2026-09-10T08:30:00+03:00","role":"customer","summary":"Reported the export failing."}],
  "reproSteps": ["Request a CSV export for a 600-line order."],
  "rootCause": {"hypothesis":"The export job times out.","confidence":"medium","evidence":[{"source":"logs","query":"service:export level:error","finding":"Timeout after 30s."}],"codeRefs":["internal/export/csv.go:42"]},
  "blastRadius": "Any order above 500 line items.",
  "classification": "code",
  "proposedFix": {"description":"Stream the export.","files":["internal/export/csv.go"],"remediationSql":"","risks":"none"},
  "openQuestions": []
}`

const rcaDoc = `{
  "rca": {
    "title": "Export times out on large orders",
    "summary": "The export buffered every row before writing. Large orders exceeded the request timeout. Streaming the rows fixes it.",
    "customerSummary": null,
    "impact": {"customersAffected":"1","recordsAffected":"n/a","financialImpact":"none","firstOccurrence":"2026-06-01","detection":"customer report","timeToDetect":"months"},
    "timeline": [{"at":"2026-09-10T08:30:00+03:00","event":"Customer reported the failure.","evidence":"ticket 555"}],
    "rootCause": {"description":"The handler buffers all rows.","codeRefs":["internal/export/csv.go:42"],"offendingCode":"rows := make([][]string, 0)","mechanism":"Nothing is flushed until encoding finishes."},
    "contributingFactors": ["No test above 100 line items."],
    "evidence": {"database":[],"logs":[],"apm":[],"code":[],"attachments":[]},
    "blastRadius": {"query":"select count(*) ...","count":"37","scope":"systemic","reasoning":"Same code path for every large order."},
    "whyNotCaughtEarlier": "Load tests used small fixtures.",
    "prevention": [{"action":"Add a 1000-line load test.","type":"test","owner":"platform","ticket":"OMNI-2"}],
    "openQuestions": [],
    "classification": "code",
    "severity": "medium",
    "confidence": "high",
    "origin": "omni",
    "triageReview": {"verdict":"confirmed","gotRight":"The timeout and the file.","missed":"The buffering mechanism.","whyMissed":"No profiling data."},
    "lessons": ["Stream large responses."],
    "playbookSuggestions": [{"playbook":"exports","addition":"Check the row count first.","reason":"It is the fastest signal."}]
  },
  "resolution": {
    "title": "Stream the CSV export",
    "resolutionType": "code-fix",
    "whatWasWrong": "The export buffered every row in memory.",
    "whatWeChanged": "The handler now streams rows to the response.",
    "codeChange": null,
    "dataChange": null,
    "verification": [],
    "customerOutcome": {"told":"Fix deployed.","confirmedFixed":"pending","helpdeskClosed":"no","trackerStatus":"in review"},
    "residualRisk": [],
    "lessons": "Stream anything unbounded."
  }
}`

const configYAML = `workspace: test
provider: claude
billing: subscription
notes:
  dir: notes
budget:
  maxTurns: 60
  maxMinutes: 25
  maxUsd: 5
concurrency: 1
permissions:
  bash:
    - "git log*"
    - "rg *"
playbooks: .sirdar/playbooks
`

// configWithCredential is the same workspace with a helpdesk source whose
// token is an env: reference, so the run has a credential to keep away from
// the agent.
const configWithCredential = configYAML + `sources:
  helpdesk:
    adapter: zohodesk
    orgId: "1"
    baseUrl: https://desk.example
    token: env:ZOHO_TOKEN
`

// configWithOAuth is the same workspace with a helpdesk whose credentials
// are an OAuth grant rather than one access token, so the run has three
// env: references to keep away from the agent instead of one.
const configWithOAuth = configYAML + `sources:
  helpdesk:
    adapter: zohodesk
    orgId: "1"
    baseUrl: https://desk.zoho.in
    auth:
      clientId: env:ZOHO_CLIENT_ID
      clientSecret: env:ZOHO_CLIENT_SECRET
      refreshToken: env:ZOHO_REFRESH_TOKEN
`

// newWorkspace writes a real .sirdar workspace into a temp dir and loads
// it through config.Load, so Config.Root and every default is set the way
// a real run sees them.
func newWorkspace(t *testing.T) *config.Config {
	t.Helper()
	return newWorkspaceWith(t, configYAML)
}

func newWorkspaceWith(t *testing.T, body string) *config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sirdar", "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	playbook := "# Exports\n\n" + playbookMarker + "\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "playbooks", "00-test.md"), []byte(playbook), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// --- stub sources -----------------------------------------------------

type stubTracker struct {
	gate func(key string)
	err  error
	// warningsFor stands in for a tracker that degraded without failing —
	// a comment page it could not read, an attachment it skipped — keyed
	// by the ticket key the warning belongs to.
	warningsFor map[string][]string
}

// WarningsFor implements source.Warner. The tracker side of a bundle
// degrades exactly as the helpdesk side does, so prepare has to drain both.
func (s stubTracker) WarningsFor(key string) []string {
	return s.warningsFor[key]
}

func (s stubTracker) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	if s.gate != nil {
		s.gate(key)
	}
	if s.err != nil {
		return ticket.TrackerTicket{}, s.err
	}
	tt := *sampleBundle().Tracker
	tt.Key = key
	return tt, nil
}

func (s stubTracker) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	return nil, nil
}

type stubHelpdesk struct {
	getErr    error
	attachErr error
	// warnings is what the stub reports through source.Warner, standing in
	// for a helpdesk that downloaded some attachments and skipped others.
	// warningsFor, when set, overrides warnings with a per-ticket-id
	// mapping, for tests that need two tickets to see different warnings.
	warnings    []string
	warningsFor map[string][]string
	// files, when set, replaces the single sample attachment: each entry
	// is written to the bundle at the given size, which is what the
	// runner's size cap and MIME allow-list are applied to.
	files []stubAttachment
	// helpdesk, when set, replaces the sample helpdesk ticket Get returns —
	// for tests that need a Customer/CustomerID other than the fixture's.
	helpdesk *ticket.HelpdeskTicket
}

// stubAttachment is one downloaded attachment and the number of bytes it
// arrived with.
type stubAttachment struct {
	ticket.Attachment
	bytes int
}

func (s stubHelpdesk) WarningsFor(id string) []string {
	if s.warningsFor != nil {
		return s.warningsFor[id]
	}
	return s.warnings
}

func (s stubHelpdesk) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	if s.getErr != nil {
		return ticket.HelpdeskTicket{}, s.getErr
	}
	if s.helpdesk != nil {
		return *s.helpdesk, nil
	}
	return *sampleBundle().Helpdesk, nil
}

func (s stubHelpdesk) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	return sampleBundle().Thread, nil
}

func (s stubHelpdesk) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	if s.attachErr != nil {
		return nil, s.attachErr
	}
	if len(s.files) > 0 {
		var out []ticket.Attachment
		for _, f := range s.files {
			path := filepath.Join(dir, filepath.Base(f.Path))
			if err := os.WriteFile(path, make([]byte, f.bytes), 0o644); err != nil {
				return nil, err
			}
			out = append(out, f.Attachment)
		}
		return out, nil
	}
	if err := os.WriteFile(filepath.Join(dir, "1-shot.png"), []byte("png"), 0o644); err != nil {
		return nil, err
	}
	return sampleBundle().Attachments, nil
}

// --- stub provider ----------------------------------------------------

type stubSession struct {
	events    chan provider.Event
	cancelled chan struct{}
	sendCh    chan string
	handle    string
	result    provider.Result
	sendErr   error // when set, Send fails the way a finished CLI does

	mu         sync.Mutex
	sends      []string
	cancels    int
	inputClose int
	cancelOnce sync.Once
	finishOnce sync.Once
}

// CloseInput records that the runner said no further message is coming.
// The stub does not end its stream on it: a provider whose process stays
// alive after its answer is exactly the case the runner has to survive.
func (s *stubSession) CloseInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputClose++
	return nil
}

func (s *stubSession) inputCloseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputClose
}

func (s *stubSession) Events() <-chan provider.Event { return s.events }

func (s *stubSession) Send(ctx context.Context, userText string) error {
	s.mu.Lock()
	s.sends = append(s.sends, userText)
	err := s.sendErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.sendCh <- userText
	return nil
}

func (s *stubSession) Wait() (provider.Result, error) { return s.result, nil }
func (s *stubSession) Handle() string                 { return s.handle }

func (s *stubSession) Cancel() {
	s.mu.Lock()
	s.cancels++
	s.mu.Unlock()
	s.cancelOnce.Do(func() { close(s.cancelled) })
}

// emit delivers one event unless the runner has already cancelled the
// session; it reports whether the script should keep going.
func (s *stubSession) emit(ev provider.Event) bool {
	select {
	case s.events <- ev:
		return true
	case <-s.cancelled:
		return false
	}
}

func (s *stubSession) finish() { s.finishOnce.Do(func() { close(s.events) }) }

func (s *stubSession) sentTexts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sends...)
}

func (s *stubSession) cancelCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancels
}

type stubProvider struct {
	name       string
	sendErr    error    // handed to every session this provider starts
	stderrTail []string // reported in every session's Result
	script     func(spec provider.SessionSpec, s *stubSession)
	// resumeDelay, when set, is how long Start blocks before returning for
	// a resumed session (one whose spec carries a Resume handle) — standing
	// in for how long spawning a fresh agent process can actually take, so
	// a test can put that delay on the far side of a short stall window.
	resumeDelay time.Duration

	mu       sync.Mutex
	specs    []provider.SessionSpec
	starts   []time.Time
	sessions []*stubSession
}

func (p *stubProvider) Name() string {
	if p.name == "" {
		return "claude"
	}
	return p.name
}

func (p *stubProvider) Doctor(ctx context.Context, binary string) []provider.Check { return nil }

func (p *stubProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	if spec.Resume != "" && p.resumeDelay > 0 {
		time.Sleep(p.resumeDelay)
	}
	s := &stubSession{
		events:    make(chan provider.Event),
		cancelled: make(chan struct{}),
		sendCh:    make(chan string, 4),
		handle:    "handle-abc",
		sendErr:   p.sendErr,
	}
	s.result.Handle = s.handle
	s.result.StderrTail = p.stderrTail
	p.mu.Lock()
	p.specs = append(p.specs, spec)
	p.starts = append(p.starts, time.Now())
	p.sessions = append(p.sessions, s)
	p.mu.Unlock()

	go p.script(spec, s)
	return s, nil
}

func (p *stubProvider) startCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.specs)
}

func (p *stubProvider) spec(i int) provider.SessionSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.specs[i]
}

func (p *stubProvider) session(i int) *stubSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessions[i]
}

// startOf returns when the session whose prompt mentions key was started.
func (p *stubProvider) startOf(key string) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, spec := range p.specs {
		if strings.Contains(spec.Prompt, "Key: "+key+"\n") {
			return p.starts[i], true
		}
	}
	return time.Time{}, false
}

// replay returns a script that emits events in order and then ends the
// session.
func replay(events ...provider.Event) func(provider.SessionSpec, *stubSession) {
	return func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		for _, ev := range events {
			if !s.emit(ev) {
				return
			}
		}
	}
}

func finalEvent(doc string) provider.Event {
	return provider.Event{Kind: provider.EvFinal, Final: json.RawMessage(doc), Raw: json.RawMessage(`{"type":"result"}`)}
}

func newRunner(cfg *config.Config, p provider.Provider, tr source.Tracker, hd source.Helpdesk) *Runner {
	return &Runner{Deps: Deps{
		Config:   cfg,
		Tracker:  tr,
		Helpdesk: hd,
		Provider: p,
		Now:      func() time.Time { return time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC) },
		Stderr:   io.Discard,
		Env:      []string{"PATH=/usr/bin"},
	}}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runDir(t *testing.T, cfg *config.Config, out Outcome) string {
	t.Helper()
	return filepath.Join(cfg.Root, ".sirdar", "runs", out.State.Key, out.State.RunID)
}

// --- tests ------------------------------------------------------------

// TestFrontmatterPrefersTheDocumentsCustomer is N2 of the second dogfood:
// the frontmatter took customer and customer_id from the helpdesk bundle
// alone, so a run whose agent resolved the real company from logs filed
// the note under the wrong one with an empty id. The document's values
// win; the bundle's disagreement is kept in the run's warnings.
func TestFrontmatterPrefersTheDocumentsCustomer(t *testing.T) {
	cfg := newWorkspace(t)
	doc := strings.Replace(triageDoc,
		`"customer":"شركة","customerId":"4561"`,
		`"customer":"مؤسسة شيك الراقي","customerId":"5598"`, 1)
	if doc == triageDoc {
		t.Fatal("the fixture's customer fields did not change")
	}
	p := &stubProvider{script: replay(finalEvent(doc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}

	note := readFile(t, filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md"))
	if !strings.Contains(note, `customer: "مؤسسة شيك الراقي"`) || !strings.Contains(note, `customer_id: "5598"`) {
		t.Fatalf("frontmatter kept the bundle's customer:\n%s", note)
	}

	// The helpdesk's own answer is not thrown away: it is in state.json,
	// so the two can be compared after the fact.
	warnings := strings.Join(out.State.Warnings, "\n")
	for _, want := range []string{"frontmatter customer", "شركة", "frontmatter customer_id", "4561"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("warning %q missing from %q", want, warnings)
		}
	}
}

// A document that leaves the customer blank keeps the bundle's, which is
// the only value either note ever had before.
func TestFrontmatterFallsBackToTheBundlesCustomer(t *testing.T) {
	cfg := newWorkspace(t)
	doc := strings.Replace(triageDoc,
		`"customer":"شركة","customerId":"4561"`,
		`"customer":"","customerId":""`, 1)
	p := &stubProvider{script: replay(finalEvent(doc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	note := readFile(t, filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md"))
	if !strings.Contains(note, `customer: "شركة"`) || !strings.Contains(note, `customer_id: "4561"`) {
		t.Fatalf("frontmatter lost the bundle's customer:\n%s", note)
	}
	if w := strings.Join(outs[0].State.Warnings, "\n"); strings.Contains(w, "frontmatter") {
		t.Fatalf("nothing disagreed, so nothing should be warned about: %q", w)
	}
}

// TestFrontmatterCustomerSuffixDoesNotWarn is the first live triage's N1:
// every note tripped "frontmatter customer: the note says ... the helpdesk
// bundle says ..." because the agent (or an older bundle field) appended a
// domain, CompanyID or company code to the customer name in a different
// shape than the bundle's own field carried it — "Acme Corp (domain 3521)"
// against "Acme Corp - 3521". The two names agree once that suffix is
// stripped, so this must not warn, even though the two raw strings differ.
func TestFrontmatterCustomerSuffixDoesNotWarn(t *testing.T) {
	cfg := newWorkspace(t)
	hd := stubHelpdesk{helpdesk: &ticket.HelpdeskTicket{
		ID: "555", Subject: "تصدير", Customer: "Acme Corp - 3521", CustomerID: "4561",
		URL: "https://h/555", Priority: "high",
	}}
	doc := strings.Replace(triageDoc,
		`"customer":"شركة","customerId":"4561"`,
		`"customer":"Acme Corp (domain 3521)","customerId":"4561"`, 1)
	if doc == triageDoc {
		t.Fatal("the fixture's customer field did not change")
	}
	p := &stubProvider{script: replay(finalEvent(doc))}
	r := newRunner(cfg, p, stubTracker{}, hd)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}

	note := readFile(t, filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md"))
	if !strings.Contains(note, `customer: "Acme Corp (domain 3521)"`) {
		t.Fatalf("frontmatter did not keep the note's customer:\n%s", note)
	}
	if w := strings.Join(out.State.Warnings, "\n"); strings.Contains(w, "frontmatter customer:") {
		t.Fatalf("names agreeing once the suffix is stripped should not warn: %q", w)
	}
}

// A genuine disagreement between two different company names still warns
// even after normalization: TestFrontmatterCustomerSuffixDoesNotWarn must
// not have made the check toothless.
func TestFrontmatterCustomerMismatchStillWarns(t *testing.T) {
	cfg := newWorkspace(t)
	hd := stubHelpdesk{helpdesk: &ticket.HelpdeskTicket{
		ID: "555", Subject: "تصدير", Customer: "Acme Corp - 3521", CustomerID: "4561",
		URL: "https://h/555", Priority: "high",
	}}
	doc := strings.Replace(triageDoc,
		`"customer":"شركة","customerId":"4561"`,
		`"customer":"Widgets Inc (domain 9001)","customerId":"4561"`, 1)
	if doc == triageDoc {
		t.Fatal("the fixture's customer field did not change")
	}
	p := &stubProvider{script: replay(finalEvent(doc))}
	r := newRunner(cfg, p, stubTracker{}, hd)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	warnings := strings.Join(out.State.Warnings, "\n")
	for _, want := range []string{"frontmatter customer", "Widgets Inc (domain 9001)", "Acme Corp - 3521"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("warning %q missing from %q", want, warnings)
		}
	}
}

// customerIds keeps any extra identifier the agent found out of the
// customer field it flows alongside, and out of the mismatch warning: it
// has no bundle counterpart to disagree with.
func TestFrontmatterCustomerIDsRendered(t *testing.T) {
	cfg := newWorkspace(t)
	doc := strings.Replace(triageDoc,
		`"customerId":"4561","customerIds":null`,
		`"customerId":"4561","customerIds":["domain-42","code-7"]`, 1)
	if doc == triageDoc {
		t.Fatal("the fixture's ticket field did not change")
	}
	p := &stubProvider{script: replay(finalEvent(doc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	note := readFile(t, filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md"))
	if !strings.Contains(note, `customer_ids: "domain-42, code-7"`) {
		t.Fatalf("frontmatter did not carry customer_ids:\n%s", note)
	}
	if strings.Contains(note, `customer: "شركة (domain-42`) {
		t.Fatalf("the identifiers leaked into the customer field:\n%s", note)
	}
}

func TestTriageHappyPath(t *testing.T) {
	cfg := newWorkspace(t)
	events := []provider.Event{
		{Kind: provider.EvSystem, Text: "init"},
		{Kind: provider.EvToolStarted, Tool: "Bash", Input: json.RawMessage(`{"command":"git log -1"}`)},
		{Kind: provider.EvToolFinished, Tool: "Bash"},
		{Kind: provider.EvPermission, Tool: "Write", Decision: "deny"},
		{Kind: provider.EvUsage, Turns: 3, InputTok: 100, OutputTok: 20, CostUSD: 0.42},
		finalEvent(triageDoc),
	}
	p := &stubProvider{script: replay(events...), stderrTail: []string{"warning: mcp server slow", "done"}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 {
		t.Fatalf("outcomes: %d", len(outs))
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if ExitCode(outs) != 0 {
		t.Fatalf("exit code %d", ExitCode(outs))
	}

	dir := runDir(t, cfg, out)
	if _, err := os.Stat(filepath.Join(dir, "note.md")); err != nil {
		t.Fatal(err)
	}
	notePath := filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md")
	note := readFile(t, notePath)
	if !strings.Contains(note, `tracker_key: "OMNI-1"`) || !strings.Contains(note, `status: "triaged"`) {
		t.Fatalf("note frontmatter:\n%s", note)
	}

	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("register rows: %d", len(rows))
	}
	row := rows[0]
	if row.Key != "OMNI-1" || row.Kind != "triage" || row.Classification != "code" ||
		row.Confidence != "medium" || row.Service != "omni" || row.Turns != 3 || row.CostUSD != 0.42 {
		t.Fatalf("register row: %+v", row)
	}
	if row.NotePath != notePath {
		t.Fatalf("register note path %q want %q", row.NotePath, notePath)
	}

	logged := strings.Count(strings.TrimRight(readFile(t, filepath.Join(dir, "events.jsonl")), "\n"), "\n") + 1
	if logged != len(events) {
		t.Fatalf("events.jsonl lines %d want %d", logged, len(events))
	}

	promptText := readFile(t, filepath.Join(dir, "prompt.md"))
	if !strings.Contains(promptText, playbookMarker) {
		t.Fatalf("prompt is missing the playbook text:\n%s", promptText)
	}

	spec := p.spec(0)
	if !contains(spec.Env, "SIRDAR_BILLING=subscription") {
		t.Fatalf("env: %v", spec.Env)
	}
	if spec.Policy == nil || strings.Join(spec.Policy.BashAllow, "|") != strings.Join(cfg.Permissions.Bash, "|") {
		t.Fatalf("policy: %+v", spec.Policy)
	}
	if spec.Cwd != cfg.Root {
		t.Fatalf("cwd %q want %q", spec.Cwd, cfg.Root)
	}
	if spec.Policy.Root != cfg.Root {
		t.Fatalf("policy root %q want %q; without it a shell command is not held to the workspace", spec.Policy.Root, cfg.Root)
	}
	// The read scope: the workspace, plus this run's own directory and
	// the bundle staged inside it. Without the run directory the session
	// cannot read the attachments the prompt points it at.
	if d := spec.Policy.Decide("Read", json.RawMessage(`{"file_path":"/etc/passwd"}`)); d.Allow {
		t.Fatal("the session could read outside the workspace")
	}
	bundleRead := `{"file_path":"` + filepath.Join(dir, "bundle", "thread.md") + `"}`
	if d := spec.Policy.Decide("Read", json.RawMessage(bundleRead)); !d.Allow {
		t.Fatalf("the session could not read its own bundle: %s", d.Message)
	}
	if len(spec.Images) != 0 {
		t.Fatalf("claude sessions take no images: %v", spec.Images)
	}
	if out.Digest.Confidence != "medium" || out.Digest.Classification != "code" || out.Digest.Issue == "" {
		t.Fatalf("digest: %+v", out.Digest)
	}
	if strings.Join(out.State.StderrTail, "|") != "warning: mcp server slow|done" {
		t.Fatalf("state stderr tail: %v", out.State.StderrTail)
	}
	var persisted store.State
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "state.json"))), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.StderrTail) != 2 {
		t.Fatalf("state.json stderr tail: %v", persisted.StderrTail)
	}
}

func TestSchemaRetryThenFail(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(provider.Event{Kind: provider.EvFinal, Text: `{"title":"nope"}`}) {
			return
		}
		select {
		case <-s.sendCh:
		case <-s.cancelled:
			return
		}
		s.emit(provider.Event{Kind: provider.EvFinal, Text: `{"title":"still nope"}`})
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q", out.State.Status)
	}
	if !strings.Contains(out.State.Reason, "schema validation failed twice") {
		t.Fatalf("reason %q", out.State.Reason)
	}
	sends := p.session(0).sentTexts()
	if len(sends) != 1 || !strings.Contains(sends[0], "did not match the schema") {
		t.Fatalf("sends: %v", sends)
	}
	dir := runDir(t, cfg, out)
	if _, err := os.Stat(filepath.Join(dir, "result.raw.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); !os.IsNotExist(err) {
		t.Fatalf("note.md should not exist: %v", err)
	}
	if ExitCode(outs) != 1 {
		t.Fatalf("exit code %d", ExitCode(outs))
	}
}

// TestSchemaRetryResumesAfterSendFails covers the retry turn arriving when
// the agent process has already exited: Send fails, and the run continues in
// a fresh session resumed from the same handle rather than failing outright.
func TestSchemaRetryResumesAfterSendFails(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{sendErr: errors.New("claude session has exited")}
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if spec.Resume == "" {
			s.emit(provider.Event{Kind: provider.EvFinal, Text: `{"title":"nope"}`})
			return
		}
		s.emit(finalEvent(triageDoc))
	}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if p.startCount() != 2 {
		t.Fatalf("sessions started: %d, want the original plus the resumed retry", p.startCount())
	}
	retry := p.spec(1)
	if retry.Resume != "handle-abc" {
		t.Fatalf("retry session resume %q, want the first session's handle", retry.Resume)
	}
	if !strings.Contains(retry.Prompt, "did not match the schema") {
		t.Fatalf("retry session prompt %q", retry.Prompt)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "note.md")); err != nil {
		t.Fatal(err)
	}
	if ExitCode(outs) != 0 {
		t.Fatalf("exit code %d", ExitCode(outs))
	}
}

// TestStallGuardIsHeldWhileTheSchemaRetrySessionStarts: resumeForRetry's
// Provider.Start blocks on starting a fresh agent process, which is not
// silence from a live session. Make that start slower than the stall
// window and, without the guard held across it, the timer fires on its own
// goroutine while nothing is there to reset it — misreporting a run that
// is about to finish normally as stalled: a warning on the state, and a
// stray error line in events.jsonl beside the true account of what
// happened. Held and rearmed around the call, the guard never gets the
// chance.
func TestStallGuardIsHeldWhileTheSchemaRetrySessionStarts(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{
		sendErr:     errors.New("claude session has exited"),
		resumeDelay: 150 * time.Millisecond,
	}
	p.script = func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if spec.Resume == "" {
			s.emit(provider.Event{Kind: provider.EvFinal, Text: `{"title":"nope"}`})
			return
		}
		s.emit(finalEvent(triageDoc))
	}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.StallTimeout = 60 * time.Millisecond // well inside resumeDelay

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q; a slow retry start should not have been read as a stall", out.State.Status, out.State.Reason)
	}
	if p.startCount() != 2 {
		t.Fatalf("sessions started: %d, want the original plus the resumed retry", p.startCount())
	}
	for _, w := range out.State.Warnings {
		if strings.Contains(w, "stalled") {
			t.Errorf("a slow retry start left a stalled warning on a run that finished normally: %v", out.State.Warnings)
		}
	}
	for _, line := range eventLogLines(t, runDir(t, cfg, out)) {
		if strings.Contains(line, `"kind":"error"`) && strings.Contains(line, "stalled") {
			t.Errorf("events.jsonl carries a misleading stalled error:\n%s", line)
		}
	}
}

// TestCredentialEnvIsStrippedFromTheAgent covers a token the workspace
// resolves for itself ending up in the agent's environment, where a session
// that can run shell commands could read it straight out.
func TestCredentialEnvIsStrippedFromTheAgent(t *testing.T) {
	cfg := newWorkspaceWith(t, configWithCredential)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.Env = []string{"PATH=/usr/bin", "ZOHO_TOKEN=secret", "HOME=/home/tester"}

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	env := p.spec(0).Env
	for _, entry := range env {
		if strings.HasPrefix(entry, "ZOHO_TOKEN=") {
			t.Fatalf("the helpdesk credential reached the agent: %v", env)
		}
	}
	if !contains(env, "PATH=/usr/bin") || !contains(env, "HOME=/home/tester") {
		t.Fatalf("child env dropped entries that are not credentials: %v", env)
	}
	if !contains(env, "SIRDAR_BILLING=subscription") {
		t.Fatalf("child env is missing the billing mode: %v", env)
	}
}

// TestOAuthCredentialsAreStrippedFromTheAgent covers the refresh grant
// reaching the agent's environment. It matters more than the access token
// did: an access token expires in an hour, while a refresh token keeps
// minting them until somebody revokes it at the Zoho console.
func TestOAuthCredentialsAreStrippedFromTheAgent(t *testing.T) {
	cfg := newWorkspaceWith(t, configWithOAuth)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.Env = []string{
		"PATH=/usr/bin",
		"ZOHO_CLIENT_ID=1000.clientid",
		"ZOHO_CLIENT_SECRET=shhh",
		"ZOHO_REFRESH_TOKEN=1000.refresh",
		"HOME=/home/tester",
	}

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	env := p.spec(0).Env
	for _, entry := range env {
		for _, name := range []string{"ZOHO_CLIENT_ID=", "ZOHO_CLIENT_SECRET=", "ZOHO_REFRESH_TOKEN="} {
			if strings.HasPrefix(entry, name) {
				t.Fatalf("an OAuth credential reached the agent: %s", name)
			}
		}
	}
	if !contains(env, "PATH=/usr/bin") || !contains(env, "HOME=/home/tester") {
		t.Fatalf("child env dropped entries that are not credentials: %v", env)
	}
}

func TestOverBudgetUSD(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(
		provider.Event{Kind: provider.EvUsage, Turns: 2, CostUSD: 6},
		finalEvent(triageDoc),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusOverBudget {
		t.Fatalf("status %q", out.State.Status)
	}
	if p.session(0).cancelCount() == 0 {
		t.Fatal("session was not cancelled")
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "note.md")); !os.IsNotExist(err) {
		t.Fatalf("note.md should not exist: %v", err)
	}
}

// TestOverBudgetUSDBeatsAFinalOnTheSameLine pins the ordering the CLI
// actually produces. One `result` line becomes a usage event and then the
// final answer, back to back, so the note is already in flight by the time
// the cost budget cancels the session. The budget was decided first and has
// to hold, whichever of the two the runner happens to read next.
func TestOverBudgetUSDBeatsAFinalOnTheSameLine(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		// Sent without consulting s.cancelled, unlike replay: the
		// provider wrote both events before the runner could cancel, so
		// the final is delivered whatever the budget decided.
		s.events <- provider.Event{Kind: provider.EvUsage, Turns: 2, CostUSD: 6}
		s.events <- finalEvent(triageDoc)
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusOverBudget {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "note.md")); !os.IsNotExist(err) {
		t.Fatalf("note.md should not exist: %v", err)
	}
}

func TestBlockedOnQuestion(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(
		provider.Event{Kind: provider.EvQuestion, Text: "Which database should I query?"},
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusBlocked {
		t.Fatalf("status %q", out.State.Status)
	}
	if out.State.Handle != "handle-abc" {
		t.Fatalf("handle %q", out.State.Handle)
	}
	if !strings.Contains(out.State.Reason, "agent asked: Which database") {
		t.Fatalf("reason %q", out.State.Reason)
	}

	p.script = replay(finalEvent(triageDoc))
	r.Stdin = strings.NewReader("answer\n")
	resumed, err := r.Resume(context.Background(), out.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State.Status != store.StatusCompleted {
		t.Fatalf("resumed status %q reason %q", resumed.State.Status, resumed.State.Reason)
	}
	spec := p.spec(1)
	if spec.Resume != "handle-abc" {
		t.Fatalf("resume %q", spec.Resume)
	}
	if spec.Prompt != "answer" {
		t.Fatalf("prompt %q", spec.Prompt)
	}
}

// TestInvalidKeyFailsBeforeAnyWrite covers `sirdar triage ../x`: the key is
// joined into the runs directory and the note filename, so it is refused
// before a directory exists.
func TestInvalidKeyFailsBeforeAnyWrite(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"../escape"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q", out.State.Status)
	}
	if !strings.Contains(out.State.Reason, "not a usable ticket key") {
		t.Fatalf("reason %q", out.State.Reason)
	}
	if p.startCount() != 0 {
		t.Fatal("the provider was started for an unusable key")
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, ".sirdar", "runs")); !os.IsNotExist(err) {
		t.Fatalf("an unusable key created run directories: %v", err)
	}
	if ExitCode(outs) != 1 {
		t.Fatalf("exit code %d", ExitCode(outs))
	}
}

func TestPrepareFailsFast(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	hd := stubHelpdesk{getErr: &source.Error{Code: source.Auth, Message: "token expired"}}
	r := newRunner(cfg, p, stubTracker{}, hd)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q", out.State.Status)
	}
	if !strings.Contains(out.State.Reason, "token expired") {
		t.Fatalf("reason %q", out.State.Reason)
	}
	if p.startCount() != 0 {
		t.Fatal("provider was started despite a prepare failure")
	}
}

func TestAttachmentWarning(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	hd := stubHelpdesk{attachErr: &source.Error{Code: source.Internal, Message: "download failed"}}
	r := newRunner(cfg, p, stubTracker{}, hd)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if len(out.State.Warnings) == 0 || !strings.Contains(out.State.Warnings[0], "download failed") {
		t.Fatalf("warnings: %v", out.State.Warnings)
	}
	promptText := readFile(t, filepath.Join(runDir(t, cfg, out), "prompt.md"))
	if !strings.Contains(promptText, "download failed") {
		t.Fatalf("prompt is missing the warning:\n%s", promptText)
	}
}

// TestHelpdeskWarningsReachThePrompt covers a helpdesk that returns
// attachments and, separately, reports that it skipped some. Nothing fails,
// so the only way the agent learns an attachment is missing is the warning.
func TestHelpdeskWarningsReachThePrompt(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	hd := stubHelpdesk{warnings: []string{"zoho desk: download attachment a2: status 404"}}
	r := newRunner(cfg, p, stubTracker{}, hd)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	promptText := readFile(t, filepath.Join(runDir(t, cfg, out), "prompt.md"))
	if !strings.Contains(promptText, "download attachment a2") {
		t.Fatalf("prompt is missing the helpdesk warning:\n%s", promptText)
	}
	found := false
	for _, w := range out.State.Warnings {
		if strings.Contains(w, "download attachment a2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("run state warnings: %v", out.State.Warnings)
	}
}

// TestTrackerWarningsReachThePrompt covers a tracker that answered with an
// issue and, separately, reported what it could not read. Nothing failed,
// so the warning is the only thing telling the agent the record in front of
// it is incomplete — and it is keyed by the tracker key, not the helpdesk
// id.
func TestTrackerWarningsReachThePrompt(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	tr := stubTracker{warningsFor: map[string][]string{
		"OMNI-1": {"jira: comment pagination stopped after 100 pages"},
		"OMNI-9": {"jira: a warning for another ticket entirely"},
	}}
	r := newRunner(cfg, p, tr, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	promptText := readFile(t, filepath.Join(runDir(t, cfg, out), "prompt.md"))
	if !strings.Contains(promptText, "stopped after 100 pages") {
		t.Fatalf("prompt is missing the tracker warning:\n%s", promptText)
	}
	if strings.Contains(promptText, "another ticket entirely") {
		t.Fatalf("prompt carries another ticket's tracker warning:\n%s", promptText)
	}
	found := false
	for _, w := range out.State.Warnings {
		if strings.Contains(w, "stopped after 100 pages") {
			found = true
		}
	}
	if !found {
		t.Fatalf("run state warnings: %v", out.State.Warnings)
	}
}

// TestHelpdeskWarningsStayWithTheirTicket covers a helpdesk that answers
// two tickets with different per-id warnings: prepare must call
// WarningsFor(helpdeskID) rather than an argument-less Warnings(), or one
// ticket's prompt.md ends up carrying the other's missing-attachment
// warning.
func TestHelpdeskWarningsStayWithTheirTicket(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	hd := stubHelpdesk{warningsFor: map[string][]string{
		"OMNI-1": {"zoho desk: download attachment a1: status 404"},
		"OMNI-2": {"zoho desk: download attachment b2: status 500"},
	}}
	// No tracker: the helpdesk id is the ticket key itself, so the two
	// tickets go to the helpdesk stub as distinct ids.
	r := newRunner(cfg, p, nil, hd)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1", "OMNI-2"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 2 {
		t.Fatalf("outs = %d, want 2", len(outs))
	}

	for _, out := range outs {
		if out.State.Status != store.StatusCompleted {
			t.Fatalf("%s status %q reason %q", out.State.Key, out.State.Status, out.State.Reason)
		}
		promptText := readFile(t, filepath.Join(runDir(t, cfg, out), "prompt.md"))
		own, other := "attachment a1", "attachment b2"
		if out.State.Key == "OMNI-2" {
			own, other = "attachment b2", "attachment a1"
		}
		if !strings.Contains(promptText, own) {
			t.Fatalf("%s prompt is missing its own warning:\n%s", out.State.Key, promptText)
		}
		if strings.Contains(promptText, other) {
			t.Fatalf("%s prompt carries the other ticket's warning:\n%s", out.State.Key, promptText)
		}
	}
}

func TestRCARequiresTriageNote(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(rcaDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	if _, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{}); err == nil ||
		!strings.Contains(err.Error(), "run triage first") {
		t.Fatalf("error %v", err)
	}
	if p.startCount() != 0 {
		t.Fatal("provider was started without a triage note")
	}

	// A triage run first, so the rca run has a note to review.
	p.script = replay(finalEvent(triageDoc))
	triageOuts, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if triageOuts[0].State.Status != store.StatusCompleted {
		t.Fatalf("triage status %q", triageOuts[0].State.Status)
	}

	p.script = replay(finalEvent(rcaDoc))
	out, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{Resolution: "Streamed the export."})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("rca status %q reason %q", out.State.Status, out.State.Reason)
	}

	dir := runDir(t, cfg, out)
	promptText := readFile(t, filepath.Join(dir, "prompt.md"))
	if !strings.Contains(promptText, "Export fails for large orders") {
		t.Fatalf("rca prompt is missing the triage note:\n%s", promptText)
	}
	if !strings.Contains(promptText, "Streamed the export.") {
		t.Fatalf("rca prompt is missing the resolution:\n%s", promptText)
	}
	for _, name := range []string{"note.md", "note-resolution.md", "playbook-suggestions.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "notes", "OMNI-1 RCA export-times-out-on-large-orders.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "notes", "OMNI-1 RES stream-the-csv-export.md")); err != nil {
		t.Fatal(err)
	}

	triageNote := readFile(t, filepath.Join(runDir(t, cfg, triageOuts[0]), "note.md"))
	if !strings.Contains(triageNote, "status: resolved") {
		t.Fatalf("triage note was not updated:\n%s", triageNote)
	}

	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("register rows: %d", len(rows))
	}
	if rows[1].Kind != "rca" || rows[1].Severity != "medium" || rows[1].TriageVerdict != "confirmed" {
		t.Fatalf("rca row: %+v", rows[1])
	}
	if rows[2].Kind != "resolution" || rows[2].Classification != "code-fix" {
		t.Fatalf("resolution row: %+v", rows[2])
	}
}

func TestDryRun(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted || out.State.Reason != "dry-run" {
		t.Fatalf("state: %+v", out.State)
	}
	if p.startCount() != 0 {
		t.Fatal("provider was started on a dry run")
	}
	dir := runDir(t, cfg, out)
	for _, name := range []string{"prompt.md", filepath.Join("bundle", "ticket.json"), filepath.Join("bundle", "thread.md")} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPoolRateLimitPause(t *testing.T) {
	cfg := newWorkspace(t)
	resets := time.Now().Add(80 * time.Millisecond)

	gate := make(chan struct{})
	var once sync.Once

	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if strings.Contains(spec.Prompt, "Key: OMNI-1\n") {
			s.emit(provider.Event{Kind: provider.EvRateLimited, Text: "slow down", ResetsAt: resets})
			return
		}
		s.emit(finalEvent(triageDoc))
	}}
	tr := stubTracker{gate: func(key string) {
		if key == "OMNI-2" {
			<-gate
		}
	}}
	r := newRunner(cfg, p, tr, stubHelpdesk{})
	r.onPause = func(time.Time) { once.Do(func() { close(gate) }) }

	outs, err := r.Triage(context.Background(), []string{"OMNI-1", "OMNI-2"}, Options{Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusBlocked {
		t.Fatalf("first run status %q", outs[0].State.Status)
	}
	started, ok := p.startOf("OMNI-2")
	if !ok {
		t.Fatal("the second run never started a session")
	}
	if started.Before(resets) {
		t.Fatalf("second session started %v before the rate limit reset", resets.Sub(started))
	}
}

func TestInterruptSkipsUnstartedKeys(t *testing.T) {
	cfg := newWorkspace(t)
	started := make(chan struct{})
	var once sync.Once
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		once.Do(func() { close(started) })
		<-s.cancelled
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	defer cancel()

	outs, err := r.Triage(ctx, []string{"OMNI-1", "OMNI-2"}, Options{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusBlocked || outs[0].State.Reason != "interrupted" {
		t.Fatalf("first run: %+v", outs[0].State)
	}
	if outs[1].State.Status != store.StatusBlocked || !strings.Contains(outs[1].State.Reason, "skipped") {
		t.Fatalf("second run: %+v", outs[1].State)
	}
	if outs[1].Digest.Reason != outs[1].State.Reason || outs[1].Digest.State != string(store.StatusBlocked) {
		t.Fatalf("second digest: %+v", outs[1].Digest)
	}
	dirs, err := filepath.Glob(filepath.Join(cfg.Root, ".sirdar", "runs", "OMNI-2", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 0 {
		t.Fatalf("the skipped key got run directories: %v", dirs)
	}
	if p.startCount() != 1 {
		t.Fatalf("sessions started: %d", p.startCount())
	}
	if ExitCode(outs) != 0 {
		t.Fatalf("exit code %d", ExitCode(outs))
	}
}

func TestInterruptAfterFinalKeepsNote(t *testing.T) {
	cfg := newWorkspace(t)
	emitted := make(chan struct{})
	var once sync.Once
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(finalEvent(triageDoc)) {
			return
		}
		once.Do(func() { close(emitted) })
		// Hold the stream open until the runner reacts to the interrupt.
		<-s.cancelled
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-emitted
		cancel()
	}()
	defer cancel()

	outs, err := r.Triage(ctx, []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "note.md")); err != nil {
		t.Fatal(err)
	}
}

func TestRetriageOverwritesTheKeysNote(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	notesDir := filepath.Join(cfg.Root, "notes")
	notePath := filepath.Join(notesDir, "OMNI-1 export-fails-for-large-orders.md")

	if outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	} else if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("first triage: %+v", outs[0].State)
	}

	// A re-triage that retitles the issue updates the note it already
	// filed rather than adding a second one.
	retitled := strings.Replace(triageDoc, `"title": "Export fails for large orders"`, `"title": "Export still fails"`, 1)
	if retitled == triageDoc {
		t.Fatal("the retitled document is identical to the original")
	}
	p.script = replay(finalEvent(retitled))
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("second triage: %+v", outs[0].State)
	}
	entries, err := os.ReadDir(notesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(notePath) {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("notes directory holds %v", names)
	}
	if body := readFile(t, notePath); !strings.Contains(body, "# Export still fails") {
		t.Fatalf("the note was not rewritten:\n%s", body)
	}

	// Once a human has moved the note on, a later run leaves it alone.
	if err := note.UpdateTriageStatus(notePath, "resolved", nil); err != nil {
		t.Fatal(err)
	}
	third := strings.Replace(triageDoc, `"title": "Export fails for large orders"`, `"title": "Export fails a third time"`, 1)
	p.script = replay(finalEvent(third))
	outs, err = r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("third triage: %+v", out.State)
	}
	if body := readFile(t, notePath); !strings.Contains(body, "# Export still fails") {
		t.Fatalf("the resolved note was overwritten:\n%s", body)
	}
	if entries, err := os.ReadDir(notesDir); err != nil {
		t.Fatal(err)
	} else if len(entries) != 1 {
		t.Fatalf("notes directory grew to %d files", len(entries))
	}
	warned := false
	for _, w := range out.State.Warnings {
		if strings.Contains(w, "left unchanged") && strings.Contains(w, `"resolved"`) {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("warnings: %v", out.State.Warnings)
	}
	// With nothing filed, the run keeps its own copy of the note.
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "note.md")); err != nil {
		t.Fatal(err)
	}
}

// TestArchivedNoteDoesNotBlockFiling: a vault keeps old notes, and the
// place it keeps them is a folder inside the notes directory. Walking that
// directory recursively found `notes/previous/OMNI-1 ….md`, read its
// `resolved` status and refused to file this run's note at all — the run
// passed, the note landed only in the run directory, and the operator got
// one warning buried in the state file. A note lives at the configured
// filename pattern, so that is the only directory the lookup reads.
func TestArchivedNoteDoesNotBlockFiling(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	notesDir := filepath.Join(cfg.Root, "notes")
	archive := filepath.Join(notesDir, "previous")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(archive, "OMNI-1 export-fails-for-large-orders.md")
	if err := os.WriteFile(archived, []byte("---\ntags: [triage]\nstatus: resolved\n---\n\n# Last quarter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}

	notePath := filepath.Join(notesDir, "OMNI-1 export-fails-for-large-orders.md")
	if _, err := os.Stat(notePath); err != nil {
		t.Fatalf("the note was not filed beside the archive: %v", err)
	}
	if body := readFile(t, archived); !strings.Contains(body, "# Last quarter") {
		t.Fatalf("the archived note was rewritten:\n%s", body)
	}
	for _, w := range out.State.Warnings {
		if strings.Contains(w, "previous") {
			t.Errorf("the archived note was treated as this key's note: %q", w)
		}
	}
}

// TestRefusedFilingIsSaidAtTheEndOfTheRun: the one warning that decides
// where a run's note is readable has to reach the operator's terminal, not
// only the state file. It names the note and the status that stopped it.
func TestRefusedFilingIsSaidAtTheEndOfTheRun(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	var progress bytes.Buffer
	r.Stderr = &progress

	notePath := filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notePath, []byte("---\ntags: [triage]\nstatus: resolved\n---\n\n# Human's own note\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if body := readFile(t, notePath); !strings.Contains(body, "# Human's own note") {
		t.Fatalf("the resolved note was overwritten:\n%s", body)
	}
	said := progress.String()
	if !strings.Contains(said, notePath) || !strings.Contains(said, `"resolved"`) {
		t.Errorf("the run said %q, want a line naming %s and its status", said, notePath)
	}
	if !strings.Contains(said, "warning") {
		t.Errorf("the line does not read as a warning: %q", said)
	}
	warned := false
	for _, w := range out.State.Warnings {
		if strings.Contains(w, notePath) && strings.Contains(w, `"resolved"`) {
			warned = true
		}
	}
	if !warned {
		t.Errorf("the refusal was not recorded on the run: %v", out.State.Warnings)
	}
}

// subdirConfigYAML files notes the way an Obsidian vault with per-kind
// folders expects: a triage note under Triage/, an RCA note under RCA/, and
// a resolution note under Resolutions/.
const subdirConfigYAML = `workspace: test
provider: claude
billing: subscription
notes:
  dir: notes
  filenames:
    triage: "Triage/{key} {slug}.md"
    rca: "RCA/{key} RCA {slug}.md"
    resolution: "Resolutions/{key} RES {slug}.md"
budget:
  maxTurns: 60
  maxMinutes: 25
  maxUsd: 5
concurrency: 1
permissions:
  bash:
    - "git log*"
    - "rg *"
playbooks: .sirdar/playbooks
`

func TestTriageFilesNoteUnderPatternSubdirectory(t *testing.T) {
	cfg := newWorkspaceWith(t, subdirConfigYAML)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}

	notePath := filepath.Join(cfg.Root, "notes", "Triage", "OMNI-1 export-fails-for-large-orders.md")
	if _, err := os.Stat(notePath); err != nil {
		t.Fatalf("note was not filed under Triage/: %v", err)
	}

	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].NotePath != notePath {
		t.Fatalf("register row NotePath = %q, want %q", rows[0].NotePath, notePath)
	}
}

func TestRetriageWithSubdirectoryPatternOverwritesInPlace(t *testing.T) {
	cfg := newWorkspaceWith(t, subdirConfigYAML)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	notesDir := filepath.Join(cfg.Root, "notes")
	notePath := filepath.Join(notesDir, "Triage", "OMNI-1 export-fails-for-large-orders.md")

	if outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	} else if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("first triage: %+v", outs[0].State)
	}

	retitled := strings.Replace(triageDoc, `"title": "Export fails for large orders"`, `"title": "Export still fails"`, 1)
	p.script = replay(finalEvent(retitled))
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("second triage: %+v", outs[0].State)
	}

	entries, err := os.ReadDir(filepath.Join(notesDir, "Triage"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(notePath) {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("Triage/ holds %v", names)
	}
	if body := readFile(t, notePath); !strings.Contains(body, "# Export still fails") {
		t.Fatalf("the note inside Triage/ was not rewritten:\n%s", body)
	}
}

func TestRCAWithSubdirectoryPatternsFilesBothNotesAndUpdatesTriage(t *testing.T) {
	cfg := newWorkspaceWith(t, subdirConfigYAML)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	triageOuts, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if triageOuts[0].State.Status != store.StatusCompleted {
		t.Fatalf("triage status %q", triageOuts[0].State.Status)
	}

	p.script = replay(finalEvent(rcaDoc))
	out, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{Resolution: "Streamed the export."})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("rca status %q reason %q", out.State.Status, out.State.Reason)
	}

	rcaPath := filepath.Join(cfg.Root, "notes", "RCA", "OMNI-1 RCA export-times-out-on-large-orders.md")
	resPath := filepath.Join(cfg.Root, "notes", "Resolutions", "OMNI-1 RES stream-the-csv-export.md")
	if _, err := os.Stat(rcaPath); err != nil {
		t.Fatalf("rca note was not filed under RCA/: %v", err)
	}
	if _, err := os.Stat(resPath); err != nil {
		t.Fatalf("resolution note was not filed under Resolutions/: %v", err)
	}

	triagePath := filepath.Join(cfg.Root, "notes", "Triage", "OMNI-1 export-fails-for-large-orders.md")
	triageNote := readFile(t, triagePath)
	if !strings.Contains(triageNote, "status: resolved") {
		t.Fatalf("triage note inside Triage/ was not updated:\n%s", triageNote)
	}
	// The body's links were written at triage time from the triage
	// title's slug; the rca retitled the issue and filed under its own.
	// Both halves of the note have to point at the notes that exist.
	for _, want := range []string{
		"RCA/OMNI-1 RCA export-times-out-on-large-orders",
		"Resolutions/OMNI-1 RES stream-the-csv-export",
	} {
		if strings.Count(triageNote, want) < 2 {
			t.Errorf("triage note does not carry %q in both its frontmatter and its body:\n%s", want, triageNote)
		}
	}
	if strings.Contains(triageNote, "RCA export-fails-for-large-orders") {
		t.Errorf("a link predicted at triage time survived in the body:\n%s", triageNote)
	}

	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("register rows: %d", len(rows))
	}
	if rows[1].NotePath != rcaPath {
		t.Fatalf("rca register NotePath = %q, want %q", rows[1].NotePath, rcaPath)
	}
	if rows[2].NotePath != resPath {
		t.Fatalf("resolution register NotePath = %q, want %q", rows[2].NotePath, resPath)
	}
}

func TestInterruptDuringRateLimitPauseSkipsKey(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(spec provider.SessionSpec, s *stubSession) {
		defer s.finish()
		s.emit(provider.Event{Kind: provider.EvRateLimited, Text: "slow down", ResetsAt: time.Now().Add(2 * time.Second)})
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	paused := make(chan struct{})
	var once sync.Once
	r.onPause = func(time.Time) { once.Do(func() { close(paused) }) }
	go func() {
		<-paused
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	outs, err := r.Triage(ctx, []string{"OMNI-1", "OMNI-2"}, Options{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the interrupt did not cut the pause short: waited %v", elapsed)
	}
	if outs[0].State.Status != store.StatusBlocked || !strings.Contains(outs[0].State.Reason, "rate limited") {
		t.Fatalf("first run: %+v", outs[0].State)
	}
	if outs[1].State.Status != store.StatusBlocked || !strings.Contains(outs[1].State.Reason, "skipped") {
		t.Fatalf("second run: %+v", outs[1].State)
	}
	dirs, err := filepath.Glob(filepath.Join(cfg.Root, ".sirdar", "runs", "OMNI-2", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 0 {
		t.Fatalf("the skipped key got run directories: %v", dirs)
	}
	if p.startCount() != 1 {
		t.Fatalf("sessions started: %d", p.startCount())
	}
	if ExitCode(outs) != 0 {
		t.Fatalf("exit code %d", ExitCode(outs))
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// --- helpdeskRef fallback ---------------------------------------------

// zohoURLRule is the rule that used to be hardcoded: it finds the Zoho Desk
// ticket URL a triager pasted into the tracker description, then narrows it
// to the ticket number the Desk API answers to.
func zohoURLRule() *config.SourceConfig {
	return &config.SourceConfig{
		Adapter: "jira",
		BaseURL: "https://acme.atlassian.net",
		PAT:     "env:JIRA_PAT",
		HelpdeskRef: &config.HelpdeskRefConfig{
			Pattern:   `Zoho Ticket URL:\s*(\S+)`,
			IDPattern: `(\d+)$`,
		},
	}
}

func TestHelpdeskRefFallback(t *testing.T) {
	cases := []struct {
		name        string
		description string
		want        string
		wantWarning bool
	}{
		{
			name:        "the agent-console URL shape",
			description: "Customer cannot export.\n\nZoho Ticket URL: https://desk.zoho.com/agent/acme/support/tickets/details/1234567890123456789\n",
			want:        "1234567890123456789",
		},
		{
			name:        "the ShowHomePage URL shape",
			description: "Zoho Ticket URL: https://desk.zoho.com/support/acme/ShowHomePage.do#Cases/dv/987654321\nfiled by L1.",
			want:        "987654321",
		},
		{
			name:        "a description with no Zoho URL at all",
			description: "Customer cannot export. Reported over the phone.",
			want:        "",
		},
		{
			name:        "a match the idPattern cannot narrow",
			description: "Zoho Ticket URL: https://desk.zoho.com/agent/acme/support/tickets/details/none",
			want:        "",
			wantWarning: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Sources.Tracker = zohoURLRule()
			f := &Fetcher{Config: cfg}

			tt := ticket.TrackerTicket{Key: "OMNI-1", Description: tc.description}
			var b ticket.Bundle
			f.applyHelpdeskRefFallback(&b, &tt)

			if tt.HelpdeskRef != tc.want {
				t.Errorf("HelpdeskRef = %q, want %q", tt.HelpdeskRef, tc.want)
			}
			if got := len(b.Warnings) > 0; got != tc.wantWarning {
				t.Errorf("warnings = %v, want a warning: %v", b.Warnings, tc.wantWarning)
			}
			if len(b.Warnings) != len(f.warnings) {
				t.Errorf("the run state and the prompt disagree: %v vs %v", f.warnings, b.Warnings)
			}
		})
	}
}

// The value quoted in the idPattern warning comes out of a ticket
// description, so its length is whoever wrote that description's choice. A
// warning line goes into prompt.md and the run state, and neither wants a
// paragraph of prose.
func TestHelpdeskRefWarningTruncatesTheCapturedValue(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sources.Tracker = &config.SourceConfig{
		Adapter: "jira",
		BaseURL: "https://acme.atlassian.net",
		PAT:     "env:JIRA_PAT",
		HelpdeskRef: &config.HelpdeskRefConfig{
			Pattern:   `Zoho Ticket URL:\s*(\S+)`,
			IDPattern: `(\d+)$`,
		},
	}
	f := &Fetcher{Config: cfg}

	long := strings.Repeat("x", 500)
	tt := ticket.TrackerTicket{Key: "OMNI-1", Description: "Zoho Ticket URL: " + long}
	var b ticket.Bundle
	f.applyHelpdeskRefFallback(&b, &tt)

	if len(b.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", b.Warnings)
	}
	if n := len(b.Warnings[0]); n > 250 {
		t.Errorf("warning is %d bytes long; the captured value was not truncated: %q", n, b.Warnings[0])
	}
	if !strings.Contains(b.Warnings[0], "…") {
		t.Errorf("warning does not mark the value as truncated: %q", b.Warnings[0])
	}
	if strings.Contains(b.Warnings[0], long) {
		t.Errorf("warning still carries the whole captured value: %q", b.Warnings[0])
	}
}

// TestHelpdeskRefFallbackWithoutARule leaves the reference alone when the
// workspace configured no rule at all.
func TestHelpdeskRefFallbackWithoutARule(t *testing.T) {
	cfg := &config.Config{}
	cfg.Sources.Tracker = &config.SourceConfig{Adapter: "linear", APIKey: "env:LINEAR_KEY"}
	f := &Fetcher{Config: cfg}

	tt := ticket.TrackerTicket{Description: "Zoho Ticket URL: https://desk.zoho.com/agent/a/support/tickets/details/42"}
	var b ticket.Bundle
	f.applyHelpdeskRefFallback(&b, &tt)
	if tt.HelpdeskRef != "" {
		t.Fatalf("HelpdeskRef = %q, want it left empty", tt.HelpdeskRef)
	}
}

// descTracker is a tracker whose issue carries a helpdesk URL in its
// description and a native HelpdeskRef only when one is set.
type descTracker struct {
	description string
	helpdeskRef string
}

func (d descTracker) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	return ticket.TrackerTicket{Key: key, Title: "Export fails", Description: d.description, HelpdeskRef: d.helpdeskRef}, nil
}

func (d descTracker) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	return nil, nil
}

// TestFetchBundleAppliesAndDefersToTheAdapter: the fallback fills in a
// missing reference and never overrides one the adapter found itself.
func TestFetchBundleAppliesAndDefersToTheAdapter(t *testing.T) {
	const desc = "Zoho Ticket URL: https://desk.zoho.com/agent/acme/support/tickets/details/1234567890123456789"

	for _, tc := range []struct {
		name   string
		native string
		want   string
	}{
		{name: "the adapter found nothing", native: "", want: "1234567890123456789"},
		{name: "the adapter found its own reference", native: "555", want: "555"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Sources.Tracker = zohoURLRule()
			f := &Fetcher{Config: cfg, Tracker: descTracker{description: desc, helpdeskRef: tc.native}}

			b, _, err := f.Fetch(context.Background(), "OMNI-1", t.TempDir())
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if b.Tracker.HelpdeskRef != tc.want {
				t.Fatalf("HelpdeskRef = %q, want %q", b.Tracker.HelpdeskRef, tc.want)
			}
		})
	}
}

// TestCredentialEnvNamesCoversBuiltinTrackers: every env: ref a built-in
// tracker names is stripped from the agent's environment, for the same
// reason the Zoho token is — an agent that runs shell commands must not be
// able to read the tracker's credentials back out.
func TestCredentialEnvNamesCoversBuiltinTrackers(t *testing.T) {
	cfg := &config.Config{Billing: "subscription"}
	cfg.Sources.Tracker = &config.SourceConfig{
		Adapter:  "jira",
		BaseURL:  "https://acme.atlassian.net",
		Email:    "you@acme.com",
		APIToken: "env:JIRA_TOKEN",
		PAT:      "env:JIRA_PAT",
		APIKey:   "env:LINEAR_KEY",
	}
	cfg.Sources.Helpdesk = &config.SourceConfig{Adapter: "zohodesk", Token: "env:ZOHO_TOKEN"}

	names := credentialEnvNames(cfg)
	for _, want := range []string{"JIRA_TOKEN", "JIRA_PAT", "LINEAR_KEY", "ZOHO_TOKEN"} {
		if !names[want] {
			t.Errorf("%s is not treated as a credential", want)
		}
	}

	d := Deps{Config: cfg, Env: []string{
		"PATH=/usr/bin", "JIRA_TOKEN=x", "JIRA_PAT=y", "LINEAR_KEY=z", "ZOHO_TOKEN=w", "HOME=/home/me",
	}}
	got := strings.Join(d.childEnv(), " ")
	for _, gone := range []string{"JIRA_TOKEN=", "JIRA_PAT=", "LINEAR_KEY=", "ZOHO_TOKEN="} {
		if strings.Contains(got, gone) {
			t.Errorf("%s survived into the agent environment: %s", gone, got)
		}
	}
	if !strings.Contains(got, "PATH=/usr/bin") || !strings.Contains(got, "HOME=/home/me") {
		t.Errorf("childEnv dropped a variable that is not a credential: %s", got)
	}
}

// TestCredentialEnvNamesCoversZendeskAndFreshdesk: a helpdesk configured as
// zendesk or freshdesk strips its apiToken/oauthToken/apiKey refs from the
// agent's environment the same way every other built-in source does.
func TestCredentialEnvNamesCoversZendeskAndFreshdesk(t *testing.T) {
	cfg := &config.Config{Billing: "subscription"}
	cfg.Sources.Helpdesk = &config.SourceConfig{
		Adapter:    "zendesk",
		Subdomain:  "acme",
		Email:      "agent@acme.com",
		APIToken:   "env:ZENDESK_TOKEN",
		OAuthToken: "env:ZENDESK_OAUTH",
	}

	names := credentialEnvNames(cfg)
	for _, want := range []string{"ZENDESK_TOKEN", "ZENDESK_OAUTH"} {
		if !names[want] {
			t.Errorf("%s is not treated as a credential", want)
		}
	}

	d := Deps{Config: cfg, Env: []string{
		"PATH=/usr/bin", "ZENDESK_TOKEN=x", "ZENDESK_OAUTH=y", "HOME=/home/me",
	}}
	got := strings.Join(d.childEnv(), " ")
	for _, gone := range []string{"ZENDESK_TOKEN=", "ZENDESK_OAUTH="} {
		if strings.Contains(got, gone) {
			t.Errorf("%s survived into the agent environment: %s", gone, got)
		}
	}
	if !strings.Contains(got, "PATH=/usr/bin") || !strings.Contains(got, "HOME=/home/me") {
		t.Errorf("childEnv dropped a variable that is not a credential: %s", got)
	}

	cfg2 := &config.Config{Billing: "subscription"}
	cfg2.Sources.Helpdesk = &config.SourceConfig{
		Adapter: "freshdesk",
		Domain:  "acme.freshdesk.com",
		APIKey:  "env:FRESHDESK_KEY",
	}
	if names2 := credentialEnvNames(cfg2); !names2["FRESHDESK_KEY"] {
		t.Error("FRESHDESK_KEY is not treated as a credential")
	}

	// Gorgias authenticates with the login email and an API key. The email
	// is an identifier written literally in the config and stays where it
	// is; the key is the password and is stripped like every other one.
	cfg3 := &config.Config{Billing: "subscription"}
	cfg3.Sources.Helpdesk = &config.SourceConfig{
		Adapter: "gorgias",
		Account: "acme",
		Email:   "ops@acme.com",
		APIKey:  "env:GORGIAS_KEY",
	}
	if names3 := credentialEnvNames(cfg3); !names3["GORGIAS_KEY"] {
		t.Error("GORGIAS_KEY is not treated as a credential")
	}
}

// TestCredentialEnvNamesCoversServiceNowPassword is the round-1 regression:
// credentialEnvNames listed every SourceConfig credential field except
// Password, so a ServiceNow helpdesk configured with basic auth
// (password: env:SERVICENOW_PASSWORD) left that variable readable inside
// the agent's environment. Username is a literal login name, not a
// credential ref, and must survive.
func TestCredentialEnvNamesCoversServiceNowPassword(t *testing.T) {
	cfg := &config.Config{Billing: "subscription"}
	cfg.Sources.Helpdesk = &config.SourceConfig{
		Adapter:  "servicenow",
		Instance: "acme",
		Username: "agent",
		Password: "env:SERVICENOW_PASSWORD",
	}

	names := credentialEnvNames(cfg)
	if !names["SERVICENOW_PASSWORD"] {
		t.Error("SERVICENOW_PASSWORD is not treated as a credential")
	}

	d := Deps{Config: cfg, Env: []string{
		"PATH=/usr/bin", "SERVICENOW_PASSWORD=hunter2", "HOME=/home/me",
	}}
	got := strings.Join(d.childEnv(), " ")
	if strings.Contains(got, "SERVICENOW_PASSWORD=") {
		t.Errorf("SERVICENOW_PASSWORD survived into the agent environment: %s", got)
	}
	if !strings.Contains(got, "PATH=/usr/bin") || !strings.Contains(got, "HOME=/home/me") {
		t.Errorf("childEnv dropped a variable that is not a credential: %s", got)
	}

	// The OAuth alternative for ServiceNow basic auth: the oauthToken ref
	// was already covered before this fix, guard it stays that way.
	cfg2 := &config.Config{Billing: "subscription"}
	cfg2.Sources.Helpdesk = &config.SourceConfig{
		Adapter:    "servicenow",
		Instance:   "acme",
		OAuthToken: "env:SERVICENOW_OAUTH",
	}
	if names2 := credentialEnvNames(cfg2); !names2["SERVICENOW_OAUTH"] {
		t.Error("SERVICENOW_OAUTH is not treated as a credential")
	}
}

// TestFinalEndsTheSessionDeterministically is the D1 regression: the first
// real run produced a schema-valid note at 9 minutes and then sat for
// another 15 until the wall-clock budget killed it, because nothing closed
// the CLI's stdin and the CLI does not exit while it might still be sent
// another message. The stub reproduces that provider exactly — it holds
// its event stream open until it is cancelled — so the run can only finish
// if the runner ends the session itself.
func TestFinalEndsTheSessionDeterministically(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(finalEvent(triageDoc)) {
			return
		}
		<-s.cancelled // the CLI stays alive after its result line
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	start := time.Now()
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the run took %s; a session that will not exit must be cancelled %s after the note", elapsed, r.CloseGrace)
	}
	if got := p.session(0).inputCloseCount(); got == 0 {
		t.Fatal("the session's input was never closed")
	}
	if got := p.session(0).cancelCount(); got == 0 {
		t.Fatal("the session was never cancelled after the grace period")
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "result.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md")); err != nil {
		t.Fatal(err)
	}
}

// TestNoteIsWrittenBeforeTheSessionEnds pins the ordering D1 turned on: the
// note, the register row and the state are on disk while the session is
// still running, not after it has been reaped.
func TestNoteIsWrittenBeforeTheSessionEnds(t *testing.T) {
	cfg := newWorkspace(t)
	seen := make(chan []string, 1)
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(finalEvent(triageDoc)) {
			return
		}
		// The runner is past handleFinal by the time CloseInput lands.
		for s.inputCloseCount() == 0 {
			time.Sleep(time.Millisecond)
		}
		var found []string
		for _, name := range []string{"result.json", "note.md"} {
			if _, err := os.Stat(filepath.Join(runDirFor(cfg, "OMNI-1"), name)); err == nil {
				found = append(found, name)
			}
		}
		seen <- found
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	got := <-seen
	if len(got) != 2 {
		t.Fatalf("while the session was still running, only %v were on disk", got)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, ".sirdar", "register.jsonl")); err != nil {
		t.Fatalf("register row: %v", err)
	}
}

// runDirFor finds the one run directory for a key, for a test that has to
// look at the run's files while the run is still going.
func runDirFor(cfg *config.Config, key string) string {
	entries, err := os.ReadDir(filepath.Join(cfg.Root, ".sirdar", "runs", key))
	if err != nil || len(entries) == 0 {
		return ""
	}
	return filepath.Join(cfg.Root, ".sirdar", "runs", key, entries[len(entries)-1].Name())
}

// TestBudgetAfterTheNoteDoesNotLoseIt is the other half of D1: a run that
// blew its budget while the session was being reaped has still produced a
// note, and reporting it as over_budget sends the operator to their budget
// settings instead of to the note that is sitting on disk.
func TestBudgetAfterTheNoteDoesNotLoseIt(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(
		finalEvent(triageDoc),
		provider.Event{Kind: provider.EvUsage, Turns: 2, CostUSD: 6},
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !hasWarningContaining(out.State.Warnings, "after the note was written") {
		t.Fatalf("warnings %v", out.State.Warnings)
	}
	if _, err := os.Stat(filepath.Join(runDir(t, cfg, out), "note.md")); err != nil {
		t.Fatal(err)
	}
}

func hasWarningContaining(warnings []string, want string) bool {
	for _, w := range warnings {
		if strings.Contains(w, want) {
			return true
		}
	}
	return false
}

// TestOversizeAndUnreadableAttachmentsAreDropped is D7: the first real run
// put a 17 MB mp4 and two voice notes in the bundle, 99% of it by bytes,
// none of it openable by the session, which then spent a denied turn
// looking for a transcoder.
func TestOversizeAndUnreadableAttachmentsAreDropped(t *testing.T) {
	cfg := newWorkspace(t)
	cfg.Attachments.MaxBytes = 1024
	hd := stubHelpdesk{files: []stubAttachment{
		{ticket.Attachment{ID: "a1", Name: "shot.png", MIME: "image/png", Path: "attachments/1-shot.png"}, 10},
		{ticket.Attachment{ID: "a2", Name: "screen.mp4", MIME: "video/mp4", Path: "attachments/2-screen.mp4"}, 20},
		{ticket.Attachment{ID: "a3", Name: "voice.ogg", MIME: "audio/ogg", Path: "attachments/3-voice.ogg"}, 30},
		{ticket.Attachment{ID: "a4", Name: "dump.txt", MIME: "text/plain", Path: "attachments/4-dump.txt"}, 4096},
	}}
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, hd)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]

	dir := filepath.Join(runDir(t, cfg, out), "bundle", "attachments")
	for name, wantKept := range map[string]bool{
		"1-shot.png": true, "2-screen.mp4": false, "3-voice.ogg": false, "4-dump.txt": false,
	} {
		_, err := os.Stat(filepath.Join(dir, name))
		if wantKept && err != nil {
			t.Errorf("%s should have been kept: %v", name, err)
		}
		if !wantKept && err == nil {
			t.Errorf("%s should have been deleted from the bundle", name)
		}
	}
	for _, want := range []string{"screen.mp4", "voice.ogg", "dump.txt"} {
		if !hasWarningContaining(out.State.Warnings, want) {
			t.Errorf("no warning names %s: %v", want, out.State.Warnings)
		}
	}
	if !hasWarningContaining(out.State.Warnings, "4.0 KiB") {
		t.Errorf("the oversize warning does not give the size: %v", out.State.Warnings)
	}
	prompt := readFile(t, filepath.Join(runDir(t, cfg, out), "prompt.md"))
	if !strings.Contains(prompt, "screen.mp4") {
		t.Error("the prompt does not tell the agent the video was not kept")
	}
	if strings.Contains(prompt, "attachments/2-screen.mp4") {
		t.Error("the prompt still lists the dropped video as a file to read")
	}
}

// TestNoteListsAttachmentsNotReviewed covers the second finding from the
// first live triage and eval runs: an attachment the run could not view —
// too large, or a type it cannot open — was only ever recorded as a run
// warning. A reader of the note itself had no way to learn that evidence
// went unread. The note now carries an "Attachments not reviewed" section,
// built from the bundle's own record of what was dropped rather than from
// anything the agent said, so it is there even when the agent never
// mentions it.
func TestNoteListsAttachmentsNotReviewed(t *testing.T) {
	cfg := newWorkspace(t)
	cfg.Attachments.MaxBytes = 1024
	hd := stubHelpdesk{files: []stubAttachment{
		{ticket.Attachment{ID: "a1", Name: "shot.png", MIME: "image/png", Path: "attachments/1-shot.png"}, 10},
		{ticket.Attachment{ID: "a2", Name: "screen.mp4", MIME: "video/mp4", Path: "attachments/2-screen.mp4"}, 4096},
		{ticket.Attachment{ID: "a3", Name: "voice.ogg", MIME: "audio/ogg", Path: "attachments/3-voice.ogg"}, 30},
	}}
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, hd)

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}

	got := readFile(t, filepath.Join(cfg.Root, "notes", "OMNI-1 export-fails-for-large-orders.md"))
	if !strings.Contains(got, "## Attachments not reviewed") {
		t.Fatalf("no \"Attachments not reviewed\" section:\n%s", got)
	}
	for _, want := range []string{
		"screen.mp4", "video/mp4", "4.0 KiB", "over the 1.0 KiB limit",
		"voice.ogg", "audio/ogg", "30 B", "cannot be opened in this session",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the attachments-not-reviewed section is missing %q:\n%s", want, got)
		}
	}
	// The one attachment the session could read is not listed as unread.
	if strings.Contains(got, "shot.png") {
		t.Errorf("the kept attachment should not appear as not reviewed:\n%s", got)
	}
}

// TestSessionSpecCarriesMCPPolicy checks the wiring D2 needs at the run
// level: the workspace's MCP allow-list reaches the policy, and a
// workspace .mcp.json reaches the provider as the only server file the
// session may load.
func TestSessionSpecCarriesMCPPolicy(t *testing.T) {
	cfg := newWorkspaceWith(t, strings.Replace(configYAML,
		"    - \"rg *\"\n",
		"    - \"rg *\"\n  mcp:\n    - \"mcp__grafana__query_*\"\n", 1))
	if err := os.WriteFile(filepath.Join(cfg.Root, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	spec := p.spec(0)
	if spec.MCPConfig != filepath.Join(cfg.Root, ".mcp.json") {
		t.Errorf("MCPConfig %q", spec.MCPConfig)
	}
	if len(spec.Policy.MCPAllow) != 1 || spec.Policy.MCPAllow[0] != "mcp__grafana__query_*" {
		t.Errorf("MCPAllow %v", spec.Policy.MCPAllow)
	}
	if d := spec.Policy.Decide("mcp__grafana__create_incident", nil); d.Allow {
		t.Error("a write-shaped MCP tool reached the session as allowed")
	}
	if !spec.MCPStrict {
		t.Error("mcp.workspaceOnly must reach the provider as MCPStrict")
	}
}

// TestSessionSpecIsStrictWithoutAWorkspaceMCPConfig is N3 of the second
// dogfood: with mcp.workspaceOnly on and no .mcp.json the run passed the
// provider nothing at all, and the session quietly loaded every user-level
// server. Strict travels with the setting, not with the file.
func TestSessionSpecIsStrictWithoutAWorkspaceMCPConfig(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}
	spec := p.spec(0)
	if spec.MCPConfig != "" {
		t.Errorf("there is no workspace .mcp.json to name, got %q", spec.MCPConfig)
	}
	if !spec.MCPStrict {
		t.Error("the session must still be restricted, or workspaceOnly means nothing here")
	}
}

// TestASecondFinalIsIgnored is R5: a provider that repeats its result line
// — a resumed session replaying it, a CLI that says goodbye twice — filed
// the note a second time and left the register with a duplicate row.
func TestASecondFinalIsIgnored(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(
		finalEvent(triageDoc),
		finalEvent(triageDoc),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("register rows: %d, want the note filed once", len(rows))
	}
}

// TestUsageKeepsTheHighestReport is R6: usage was assigned from whichever
// event arrived last, so a schema retry in a fresh session — whose turn
// and cost counters start again at zero — handed the run back a budget it
// had already spent.
func TestUsageKeepsTheHighestReport(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(
		provider.Event{Kind: provider.EvUsage, Turns: 5, InputTok: 900, OutputTok: 300, CostUSD: 0.4},
		provider.Event{Kind: provider.EvUsage, Turns: 1, InputTok: 20, OutputTok: 5, CostUSD: 0.05},
		finalEvent(triageDoc),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	u := outs[0].State.Usage
	if u.Turns != 5 || u.InputTokens != 900 || u.OutputTokens != 300 || u.CostUSD != 0.4 {
		t.Fatalf("usage %+v, want the highest figure each counter reached", u)
	}
}

// TestFailureAfterTheNoteIsAWarning is R7: a provider that fell apart once
// the note was on disk left nothing in the run's state to say so, because
// the completed branch read only completeErr.
func TestFailureAfterTheNoteIsAWarning(t *testing.T) {
	cfg := newWorkspace(t)
	events := []provider.Event{finalEvent(triageDoc)}
	for i := 0; i < 10; i++ {
		events = append(events, provider.Event{Kind: provider.EvError, Text: "not json"})
	}
	p := &stubProvider{script: replay(events...)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !hasWarningContaining(out.State.Warnings, "malformed provider lines") {
		t.Fatalf("warnings %v, want the provider failure reported as one", out.State.Warnings)
	}
}

// TestTriageReplaysABundleInsteadOfFetching is what makes an evaluation
// run a real run: the session is prepared from a bundle on disk and no
// ticket source is touched, but everything after that — prompt, session,
// validation, note, register row — is the ordinary path.
func TestTriageReplaysABundleInsteadOfFetching(t *testing.T) {
	cfg := newWorkspace(t)
	golden := t.TempDir()
	if err := ticket.WriteBundle(golden, sampleBundle()); err != nil {
		t.Fatal(err)
	}

	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	refuse := errors.New("the source must not be called during a replay")
	r := newRunner(cfg, p, stubTracker{err: refuse}, stubHelpdesk{getErr: refuse})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{BundleDir: golden})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}

	dir := runDir(t, cfg, out)
	for _, name := range []string{"ticket.json", "thread.md"} {
		if _, err := os.Stat(filepath.Join(dir, "bundle", name)); err != nil {
			t.Errorf("the golden bundle's %s was not copied in: %v", name, err)
		}
	}
	if prompt := readFile(t, filepath.Join(dir, "prompt.md")); !strings.Contains(prompt, "Export fails") {
		t.Error("the prompt was not assembled from the replayed bundle")
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); err != nil {
		t.Errorf("a replayed run wrote no note: %v", err)
	}

	var said bool
	for _, w := range out.State.Warnings {
		if strings.Contains(w, "bundle replayed from") {
			said = true
		}
	}
	if !said {
		t.Errorf("the run state does not say the bundle was replayed: %v", out.State.Warnings)
	}
}

// TestFixRunRecordsItsKindAndPrompt: a fix run has no bundle and no note,
// so what it leaves on disk is the prompt, the report and a state that says
// which branch the work went to.
func TestFixRunRecordsItsKindAndPrompt(t *testing.T) {
	cfg := newWorkspace(t)
	const report = `{"summary":"Stream the export","filesChanged":["export/csv.go"],"testsRun":[],"risks":"none","deviationFromNote":""}`
	p := &stubProvider{script: replay(finalEvent(report))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out, err := r.Fix(context.Background(), "OMNI-1", FixOptions{
		Prompt: "implement the fix",
		Branch: "fix-omni-1-export",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if out.State.Kind != store.KindFix {
		t.Fatalf("kind %q", out.State.Kind)
	}

	dir := runDir(t, cfg, out)
	if got := readFile(t, filepath.Join(dir, "prompt.md")); got != "implement the fix" {
		t.Errorf("prompt %q", got)
	}
	doc, err := FixReport(cfg.Root, "OMNI-1", out.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "Stream the export") {
		t.Errorf("the report was not filed: %s", doc)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.md")); err == nil {
		t.Error("a fix run rendered a note")
	}
	rows, err := store.ReadRegister(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("a fix run wrote a register row before anything was pushed: %+v", rows)
	}

	spec := p.spec(0)
	if !spec.Mode.IsFix() || !spec.Policy.IsFix() {
		t.Errorf("the fix run started a triage session: mode=%q", spec.Mode)
	}
	if len(spec.Policy.BashAllow) == 0 || spec.Policy.BashAllow[0] != cfg.Permissions.FixBash[0] {
		t.Errorf("the fix session's bash list is %v, not permissions.fixBash %v",
			spec.Policy.BashAllow, cfg.Permissions.FixBash)
	}
}

// TestFixRunNeedsAPrompt guards the one way to call Fix wrongly.
func TestFixRunNeedsAPrompt(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay()}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	if _, err := r.Fix(context.Background(), "OMNI-1", FixOptions{}); err == nil {
		t.Fatal("a fix run with no prompt was accepted")
	}
}

// TestSchemaEchoIsStrippedWithoutARetry replays the exact shape a
// provider: acp fix session answered with in the sandbox evidence
// (SBX-1/20260915T102406Z-77e5): a complete report that also carries the
// fix schema's own "$schema" and "title" root keys. The run must recover
// it on the first turn, with no retry spent and no trace of the echoed
// keys in the filed report.
func TestSchemaEchoIsStrippedWithoutARetry(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "acp", script: replay(finalEvent(realEvidenceFixAnswer))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out, err := r.Fix(context.Background(), "OMNI-1", FixOptions{Prompt: "implement the fix"})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if sends := p.session(0).sentTexts(); len(sends) != 0 {
		t.Errorf("sent %v, want no retry", sends)
	}
	doc, err := FixReport(cfg.Root, "OMNI-1", out.State.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "$schema") || strings.Contains(string(doc), `"title"`) {
		t.Errorf("filed report still carries a schema-metadata key: %s", doc)
	}
	if !strings.Contains(string(doc), "double-counted") {
		t.Errorf("filed report lost the answer's own content: %s", doc)
	}
}

// TestSchemaRetrySharpensForProvidersWithoutWireEnforcement covers a first
// answer the echo strip cannot fully recover (it is short two required
// fields as well as carrying $schema/title): provider: acp gets the
// sharpened retry wording, since ACP has no --json-schema equivalent to
// fall back on, and a provider that does gets the plain one.
func TestSchemaRetrySharpensForProvidersWithoutWireEnforcement(t *testing.T) {
	const incomplete = `{"$schema":"http://json-schema.org/draft-07/schema#","title":"Sirdar Fix Report","summary":"x","filesChanged":[]}`
	const complete = `{"summary":"x","filesChanged":[],"testsRun":[],"risks":"none","deviationFromNote":""}`

	for _, tc := range []struct {
		provider  string
		sharpened bool
	}{
		{"acp", true},
		{"claude", false},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			cfg := newWorkspace(t)
			p := &stubProvider{name: tc.provider, script: func(_ provider.SessionSpec, s *stubSession) {
				defer s.finish()
				if !s.emit(finalEvent(incomplete)) {
					return
				}
				select {
				case <-s.sendCh:
				case <-s.cancelled:
					return
				}
				s.emit(finalEvent(complete))
			}}
			r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

			out, err := r.Fix(context.Background(), "OMNI-1", FixOptions{Prompt: "implement the fix"})
			if err != nil {
				t.Fatal(err)
			}
			if out.State.Status != store.StatusCompleted {
				t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
			}
			sends := p.session(0).sentTexts()
			if len(sends) != 1 {
				t.Fatalf("sends: %v", sends)
			}
			const wantSharp = "Reply with the JSON object only: no `$schema`, no `title`, no surrounding text or code fence."
			if strings.Contains(sends[0], wantSharp) != tc.sharpened {
				t.Errorf("send = %q, want sharpened=%v", sends[0], tc.sharpened)
			}
		})
	}
}

// TestSchemaRetryNamesTheRequiredTopLevelKeys covers the failure the live
// OpenCode rca run hit: the agent wrote a perfectly good root-cause
// analysis with the rca object's own fields at the root, so the validator
// said "(root): missing properties 'rca', 'resolution'" and the retry
// repeated the same shape. A model that has just written an rca does not
// read that message as being about itself, so the retry names the keys —
// off the schema's own root `required` list, so it cannot drift from the
// schema — and says the substance belongs inside them.
func TestSchemaRetryNamesTheRequiredTopLevelKeys(t *testing.T) {
	cfg := newWorkspace(t)

	// The triage note the rca run reviews.
	p := &stubProvider{name: "acp", script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}

	// The rca answer with the rca object's fields flattened to the root,
	// exactly as the live run produced it.
	var full map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rcaDoc), &full); err != nil {
		t.Fatal(err)
	}
	flattened, err := json.Marshal(json.RawMessage(full["rca"]))
	if err != nil {
		t.Fatal(err)
	}

	p.script = func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(finalEvent(string(flattened))) {
			return
		}
		select {
		case <-s.sendCh:
		case <-s.cancelled:
			return
		}
		s.emit(finalEvent(rcaDoc))
	}

	out, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{Resolution: "Streamed the export."})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}

	sends := p.session(1).sentTexts()
	if len(sends) != 1 {
		t.Fatalf("sends: %v", sends)
	}
	retry := sends[0]
	for _, want := range []string{
		// The validator's own message, so the retry says what failed.
		"missing properties 'rca', 'resolution'",
		// The keys the answer has to carry, named.
		"The object must have these top-level keys: rca, resolution.",
		"Everything else belongs inside them, not at the root.",
	} {
		if !strings.Contains(retry, want) {
			t.Errorf("the retry message does not carry %q:\n%s", want, retry)
		}
	}
}

// TestSchemaItselfFailsWithAClearerReason covers a session that answers
// with its own JSON Schema — a root "properties" object — rather than a
// document shaped by it. There is nothing to reconstruct there, so the run
// must fail with a reason that says so plainly instead of the raw
// additionalProperties dump every schema keyword would otherwise produce.
func TestSchemaItselfFailsWithAClearerReason(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "acp", script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(finalEvent(string(prompt.FixSchema))) {
			return
		}
		select {
		case <-s.sendCh:
		case <-s.cancelled:
			return
		}
		s.emit(finalEvent(string(prompt.FixSchema)))
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out, err := r.Fix(context.Background(), "OMNI-1", FixOptions{Prompt: "implement the fix"})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q", out.State.Status)
	}
	if !strings.Contains(out.State.Reason, "the agent's answer is the JSON Schema itself") {
		t.Fatalf("reason %q", out.State.Reason)
	}
}

// --- language ---

// TestWorkspaceLanguagesReachThePrompt: the session is the only thing that
// translates, so it is the one that has to be told which language the note
// is in and which language the customer reads.
func TestWorkspaceLanguagesReachThePrompt(t *testing.T) {
	cfg := newWorkspaceWith(t, configYAML+"language:\n  notes: en\n  customer: ar\n")
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	promptText := readFile(t, filepath.Join(runDir(t, cfg, outs[0]), "prompt.md"))
	for _, want := range []string{
		"# Language",
		"Write the note in en (language.notes: en)",
		"Write customer-facing text in ar (language.customer: ar)",
	} {
		if !strings.Contains(promptText, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, promptText)
		}
	}
}

// The default workspace names no language at all, and the prompt still has
// to say what to do: English note, customer's own language for the reply.
func TestDefaultWorkspaceStillStatesBothLanguages(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	promptText := readFile(t, filepath.Join(runDir(t, cfg, outs[0]), "prompt.md"))
	if !strings.Contains(promptText, "language.notes: en") || !strings.Contains(promptText, "language.customer: auto") {
		t.Fatalf("default prompt does not state both languages:\n%s", promptText)
	}
}

// --- stall detection --------------------------------------------------

// eventLogLines returns every non-empty line of a run's events.jsonl, so a
// test can assert what the run recorded rather than only what it returned.
func eventLogLines(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// TestInterruptStopsTheStallGuardBeforeTheStreamDrains: an interrupt
// cancels the session and then waits for its stream to drain, which can
// take a while — a real process is not required to exit the instant it is
// asked to. The stall guard is stopped the moment the interrupt is seen,
// not only once the drain finishes, so a slow-to-exit process cancelled by
// an interrupt does not also get logged as though it had gone silent.
func TestInterruptStopsTheStallGuardBeforeTheStreamDrains(t *testing.T) {
	cfg := newWorkspace(t)
	started := make(chan struct{})
	var once sync.Once
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(provider.Event{Kind: provider.EvToolStarted, Tool: "Bash"}) {
			return
		}
		once.Do(func() { close(started) })
		// The stream takes several stall windows to actually drain after
		// the interrupt cancels it — the case that used to let the stall
		// timer win the race and misname the reason.
		<-s.cancelled
		time.Sleep(150 * time.Millisecond)
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.StallTimeout = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	defer cancel()

	outs, err := r.Triage(ctx, []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusBlocked || out.State.Reason != "interrupted" {
		t.Fatalf("status %q reason %q, want blocked/interrupted", out.State.Status, out.State.Reason)
	}
	for _, line := range eventLogLines(t, runDir(t, cfg, out)) {
		if strings.Contains(line, `"kind":"error"`) && strings.Contains(line, "stalled") {
			t.Errorf("events.jsonl carries a misleading stalled error beside the interrupt:\n%s", line)
		}
	}
}

// TestOverBudgetStopsTheStallGuardBeforeTheStreamDrains is the same race
// on the other cancel that can lose it: the session is cancelled for
// having gone over budget, but is slow to actually exit, and that must not
// read as a stall either.
func TestOverBudgetStopsTheStallGuardBeforeTheStreamDrains(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(provider.Event{Kind: provider.EvUsage, Turns: 2, CostUSD: 6}) {
			return
		}
		<-s.cancelled
		time.Sleep(150 * time.Millisecond)
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.StallTimeout = 20 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusOverBudget {
		t.Fatalf("status %q reason %q, want over_budget", out.State.Status, out.State.Reason)
	}
	for _, line := range eventLogLines(t, runDir(t, cfg, out)) {
		if strings.Contains(line, `"kind":"error"`) && strings.Contains(line, "stalled") {
			t.Errorf("events.jsonl carries a misleading stalled error beside the budget cancellation:\n%s", line)
		}
	}
}

// TestSilentProviderIsCancelledAsStalled: a provider that opens its stream
// and then says nothing at all used to hold the run until the wall-clock
// budget expired — 25 minutes of a session that had already died. The
// stall watch ends it in stallMinutes and says so.
func TestSilentProviderIsCancelledAsStalled(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(provider.Event{Kind: provider.EvToolStarted, Tool: "Bash"}) {
			return
		}
		<-s.cancelled // and then nothing, ever
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.StallTimeout = 80 * time.Millisecond

	start := time.Now()
	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	out := outs[0]

	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !strings.HasPrefix(out.State.Reason, "stalled: no activity for ") {
		t.Fatalf("reason %q", out.State.Reason)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the run took %s; a silent provider must be cancelled after %s", elapsed, r.StallTimeout)
	}
	if p.session(0).cancelCount() == 0 {
		t.Error("the stalled session was not cancelled")
	}

	// The cancellation is in the run's own event log as an error, which
	// is the only account of it for anyone reading the run afterwards.
	var found bool
	for _, line := range eventLogLines(t, runDir(t, cfg, out)) {
		if strings.Contains(line, `"kind":"error"`) && strings.Contains(line, "stalled: no activity for") {
			found = true
		}
	}
	if !found {
		t.Errorf("no error event recorded the stall:\n%s", strings.Join(eventLogLines(t, runDir(t, cfg, out)), "\n"))
	}
}

// TestEveryEventResetsTheStallTimer: the timer measures silence, not
// elapsed time. A session that keeps talking runs for as long as the
// wall-clock budget allows, however far past stallMinutes that is.
func TestEveryEventResetsTheStallTimer(t *testing.T) {
	cfg := newWorkspace(t)
	// Eight steps of chatter, each well inside a full-second stall window:
	// the gap the timer actually has to judge is one step, ~120ms, against
	// a full second of slack, so ordinary CI scheduling jitter between two
	// sleeps cannot make an on-time event look like a stall. The old
	// 20ms-step-against-100ms-window version left next to no margin, which
	// is what made this test flake — never as a false pass, always as a
	// good run failing.
	const step = 120 * time.Millisecond
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		for i := 0; i < 8; i++ {
			time.Sleep(step)
			if !s.emit(provider.Event{Kind: provider.EvToolStarted, Tool: "Bash"}) {
				return
			}
		}
		s.emit(finalEvent(triageDoc))
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.StallTimeout = time.Second
	r.CloseGrace = 200 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
}

// TestAQuestionIsNotAStall: a run blocked on a question is waiting on the
// operator, who may be at lunch. Counting that silence would turn every
// question into a failed run and throw away the handle `sirdar resume`
// needs.
func TestAQuestionIsNotAStall(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(provider.Event{Kind: provider.EvQuestion, Text: "Which database should I query?"}) {
			return
		}
		// Silence for well past the stall window, as a session waiting
		// on an answer is.
		select {
		case <-s.cancelled:
		case <-time.After(300 * time.Millisecond):
		}
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.StallTimeout = 40 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusBlocked {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if !strings.Contains(out.State.Reason, "agent asked: Which database") {
		t.Errorf("reason %q", out.State.Reason)
	}
	if out.State.Handle != "handle-abc" {
		t.Errorf("the resume handle was lost: %q", out.State.Handle)
	}
}

// TestARateLimitIsNotAStall: the same rule for the other way a run parks.
func TestARateLimitIsNotAStall(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		if !s.emit(provider.Event{Kind: provider.EvRateLimited}) {
			return
		}
		select {
		case <-s.cancelled:
		case <-time.After(300 * time.Millisecond):
		}
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.StallTimeout = 40 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusBlocked {
		t.Fatalf("status %q reason %q", outs[0].State.Status, outs[0].State.Reason)
	}
}

// TestStallMinutesZeroTurnsTheCheckOff: the setting is a pointer because 0
// means "off" and an absent key means the default.
func TestStallMinutesZeroTurnsTheCheckOff(t *testing.T) {
	off := newWorkspaceWith(t, strings.Replace(configYAML, "  maxUsd: 5\n", "  maxUsd: 5\n  stallMinutes: 0\n", 1))
	r := &Runner{Deps: Deps{Config: off}}
	if got := r.stallTimeout(); got != 0 {
		t.Errorf("stallMinutes: 0 left a timeout of %s", got)
	}

	def := newWorkspace(t)
	if got := (&Runner{Deps: Deps{Config: def}}).stallTimeout(); got != 6*time.Minute {
		t.Errorf("the default stall timeout is %s, want 6m", got)
	}
}

// TestStallReasonNamesTheWindowInMinutes pins the text a channel and a
// state file carry: budget.stallMinutes is in minutes, so the reason says
// minutes.
func TestStallReasonNamesTheWindowInMinutes(t *testing.T) {
	if got := stallReason(6 * time.Minute); got != "stalled: no activity for 6m" {
		t.Errorf("stallReason(6m) = %q", got)
	}
	if got := stallReason(90 * time.Second); got != "stalled: no activity for 1m30s" {
		t.Errorf("stallReason(90s) = %q", got)
	}
}

// --- read-only breaches -----------------------------------------------

// breachEvent is what provider agy raises when a triage session finished a
// write: the first line is the run's terminal reason, the rest is the
// explanation the event log keeps.
func breachEvent(reason string) provider.Event {
	return provider.Event{
		Kind: provider.EvBreach,
		Text: reason + "\na triage session completed a write agy should have refused",
		Tool: "write_to_file",
		Raw:  json.RawMessage(`{"event":"step_update"}`),
	}
}

// TestBreachEndsTheRunAndFilesNothing is what makes the read-only claim
// honest. A breach used to arrive as one more EvError, which the run layer
// counts towards its malformed-line threshold and otherwise ignores — so a
// session that had watched a write complete went on to file its note and
// its register row, both asserting a run that wrote nothing.
func TestBreachEndsTheRunAndFilesNothing(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "agy", script: replay(
		breachEvent("read-only breach: write_to_file /work/src/a.go"),
		finalEvent(triageDoc),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q reason %q, want failed", out.State.Status, out.State.Reason)
	}
	if out.State.Reason != "read-only breach: write_to_file /work/src/a.go" {
		t.Errorf("reason %q: the breach's first line is the run's reason", out.State.Reason)
	}

	// No note, anywhere: not in the notes directory, not in the register.
	if entries, err := os.ReadDir(filepath.Join(cfg.Root, "notes")); err == nil && len(entries) > 0 {
		t.Errorf("a breached run filed %d note(s)", len(entries))
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, ".sirdar", "register.jsonl")); !os.IsNotExist(err) {
		rows, _ := store.ReadRegister(cfg.Root)
		t.Errorf("a breached run wrote %d register row(s)", len(rows))
	}

	// And the session was stopped rather than left to finish its turn.
	if p.session(0).cancelCount() == 0 {
		t.Error("the session was not cancelled on the breach")
	}
}

// TestBreachIsNotCountedAsAMalformedLine: the malformed-line counter
// exists so a run survives a few bad lines. A breach is the opposite kind
// of event and must not need ten of itself to be believed.
func TestBreachIsNotCountedAsAMalformedLine(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "agy", script: replay(
		breachEvent("read-only breach: run_command touch x"),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := outs[0].State.Reason; got != "read-only breach: run_command touch x" {
		t.Fatalf("reason %q, want the breach and not a malformed-line count", got)
	}
}

// TestBreachAfterTheNoteStillFailsTheRun: a note that landed first is not
// a reprieve. The guarantee it was written under did not hold, so the run
// is failed rather than completed with a warning, which is how every other
// late arrival — a bad exit, an expired budget — is treated.
func TestBreachAfterTheNoteStillFailsTheRun(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "agy", script: replay(
		finalEvent(triageDoc),
		breachEvent("read-only breach: write_to_file /work/src/a.go"),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if outs[0].State.Status != store.StatusFailed {
		t.Fatalf("status %q reason %q, want failed", outs[0].State.Status, outs[0].State.Reason)
	}
}

// --- sessions that read nothing ---------------------------------------

// blindEvent is what provider agy raises when a session produced an answer
// without completing a single read: the first line is the run's terminal
// reason, the rest is the explanation the event log keeps.
func blindEvent(reason string) provider.Event {
	return provider.Event{
		Kind: provider.EvBlind,
		Text: reason + "\nthis session completed no read of a file",
		Raw:  json.RawMessage(`{"event":"result"}`),
	}
}

// TestBlindSessionFailsInsteadOfFilingANote is round 1 of provider agy, as
// a run-layer test. Every read that session tried was auto-denied, the
// agent answered out of the ticket text, and the note that reached the
// register claimed high confidence about code nobody had opened. A note
// like that is indistinguishable downstream from one built on evidence, so
// the run fails and files nothing.
func TestBlindSessionFailsInsteadOfFilingANote(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "agy", script: replay(
		blindEvent("the agent could read nothing (2 reads denied)"),
		finalEvent(triageDoc),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := outs[0]
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q reason %q, want failed", out.State.Status, out.State.Reason)
	}
	if out.State.Reason != "the agent could read nothing (2 reads denied)" {
		t.Errorf("reason %q: the blind verdict's first line is the run's reason", out.State.Reason)
	}
	if entries, err := os.ReadDir(filepath.Join(cfg.Root, "notes")); err == nil && len(entries) > 0 {
		t.Errorf("a blind run filed %d note(s)", len(entries))
	}
	if rows, _ := store.ReadRegister(cfg.Root); len(rows) > 0 {
		t.Errorf("a blind run wrote %d register row(s)", len(rows))
	}
	// The answer is kept where it can be read without being believed.
	runs, _ := filepath.Glob(filepath.Join(cfg.Root, ".sirdar", "runs", "OMNI-1", "*", "result.raw.txt"))
	if len(runs) != 1 {
		t.Errorf("result.raw.txt files %v, want the one unfiled answer", runs)
	}
}

// TestBreachOutranksBlind: a session that both read nothing and completed a
// write is reported as the breach. They are different facts about the same
// run and the breach is the worse one.
func TestBreachOutranksBlind(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "agy", script: replay(
		blindEvent("the agent could read nothing (1 reads denied)"),
		breachEvent("read-only breach: write_to_file /work/src/a.go"),
		finalEvent(triageDoc),
	)}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	r.CloseGrace = 50 * time.Millisecond

	outs, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := outs[0].State.Reason; got != "read-only breach: write_to_file /work/src/a.go" {
		t.Errorf("reason %q, want the breach", got)
	}
}

// TestEmptyFinalFailsWithoutCallingItASchemaError covers a fix session that
// ends its turn with nothing in it — the ACP failure the Copilot sandbox
// run hit. Nothing was ever validated, so neither the retry nor the run's
// reason may talk about the schema; and the retry restates the report shape
// the session was asked for several turns earlier.
func TestEmptyFinalFailsWithoutCallingItASchemaError(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "acp", script: func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		// One more empty turn than the run tolerates.
		for i := 0; i <= maxEmptyTurns; i++ {
			if i > 0 {
				select {
				case <-s.sendCh:
				case <-s.cancelled:
					return
				}
			}
			if !s.emit(provider.Event{Kind: provider.EvFinal, Text: "   "}) {
				return
			}
		}
	}}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})

	out, err := r.Fix(context.Background(), "OMNI-1", FixOptions{Prompt: "implement the fix"})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q", out.State.Status)
	}
	if out.State.Reason != "the agent ended the turn without an answer" {
		t.Fatalf("reason %q", out.State.Reason)
	}
	sends := p.session(0).sentTexts()
	if len(sends) != maxEmptyTurns {
		t.Fatalf("sends: %v", sends)
	}
	if !strings.Contains(sends[0], "ended without an answer") {
		t.Errorf("the nudge does not say what went wrong: %q", sends[0])
	}
	if !strings.Contains(sends[0], "deviationFromNote") {
		t.Errorf("the nudge does not restate the fix report shape: %q", sends[0])
	}
}

// qwenPlainTextFailure is the error sentence Qwen Code 0.23.3 puts on its
// result line when it fails a --json-schema run whose model answered in
// prose. It is copied from the first live `provider: qwen` rca run
// (SBX-1, run 20260915T110536Z-3c18), whose result line carried
// subtype "error_during_execution", is_error true, no result string, no
// structured_result, and this under error.message.
const qwenPlainTextFailure = "Model produced plain text instead of calling the structured_output " +
	"tool as required by --json-schema after 1 turn(s)."

// TestProviderNarrationIsNotTheAnswer is that run. The adapter keeps the
// CLI's sentence as the final event's text, because it is the only account
// of how the session ended — and the run used to hand it to the note
// validator, which reported `parse document: invalid character 'M'` (the
// 'M' of "Model") and then quoted that back at the agent as its own
// mistake. The text is a candidate answer only when it carries a JSON
// document; otherwise it is the reason the run failed.
func TestProviderNarrationIsNotTheAnswer(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "qwen", script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}

	p.script = func(_ provider.SessionSpec, s *stubSession) {
		defer s.finish()
		for i := 0; i <= maxEmptyTurns; i++ {
			if i > 0 {
				select {
				case <-s.sendCh:
				case <-s.cancelled:
					return
				}
			}
			if !s.emit(provider.Event{
				Kind: provider.EvFinal,
				Text: qwenPlainTextFailure,
				Raw:  json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true}`),
			}) {
				return
			}
		}
	}
	out, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{Resolution: "Streamed the export."})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusFailed {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if strings.Contains(out.State.Reason, "invalid character 'M'") {
		t.Fatalf("the CLI's error sentence reached the validator: %q", out.State.Reason)
	}
	if !strings.Contains(out.State.Reason, "Model produced plain text") {
		t.Fatalf("the reason does not say what the CLI reported: %q", out.State.Reason)
	}
	// And the retry says what the agent got wrong, not what the CLI said
	// about itself.
	sends := p.session(1).sentTexts()
	if len(sends) == 0 {
		t.Fatal("no retry was sent")
	}
	if strings.Contains(sends[0], "invalid character 'M'") {
		t.Fatalf("the retry quoted the CLI's own error back at the agent: %q", sends[0])
	}
}

// TestPlainTextAnswerOnTheResultLineIsRead is the other side of the same
// judgement, and the case the recovery exists for: the model wrote the
// note as prose because nothing on the wire made it call the
// structured-output tool. The document is in the text, behind a sentence
// and a code fence, and this is an rca run — the recovery is not a triage
// feature.
func TestPlainTextAnswerOnTheResultLineIsRead(t *testing.T) {
	cfg := newWorkspace(t)
	p := &stubProvider{name: "qwen", script: replay(finalEvent(triageDoc))}
	r := newRunner(cfg, p, stubTracker{}, stubHelpdesk{})
	if _, err := r.Triage(context.Background(), []string{"OMNI-1"}, Options{}); err != nil {
		t.Fatal(err)
	}

	prose := "Here is the RCA and resolution note:\n\n```json\n" + rcaDoc + "\n```\n"
	p.script = replay(provider.Event{
		Kind: provider.EvFinal,
		Text: prose,
		Raw:  json.RawMessage(`{"type":"result","subtype":"success"}`),
	})
	out, err := r.RCA(context.Background(), "OMNI-1", RCAOptions{Resolution: "Streamed the export."})
	if err != nil {
		t.Fatal(err)
	}
	if out.State.Status != store.StatusCompleted {
		t.Fatalf("status %q reason %q", out.State.Status, out.State.Reason)
	}
	if sends := p.session(1).sentTexts(); len(sends) != 0 {
		t.Fatalf("the answer was there; no retry should have been spent: %v", sends)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "notes", "OMNI-1 RCA export-times-out-on-large-orders.md")); err != nil {
		t.Fatal(err)
	}
}
