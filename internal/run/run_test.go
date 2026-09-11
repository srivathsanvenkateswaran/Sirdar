package run

import (
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
  "ticket": {"key":"OMNI-1","title":"Export fails","trackerUrl":"https://t/OMNI-1","helpdeskId":"555","helpdeskUrl":"https://h/555","priority":"high","service":"omni","customer":"شركة","customerId":"4561"},
  "title": "Export fails for large orders",
  "complaint": "The export fails for large orders. It has happened every day this week.",
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
			r := &Runner{Deps: Deps{Config: cfg}}

			tt := ticket.TrackerTicket{Key: "OMNI-1", Description: tc.description}
			p := &prepared{}
			var b ticket.Bundle
			r.applyHelpdeskRefFallback(p, &b, &tt)

			if tt.HelpdeskRef != tc.want {
				t.Errorf("HelpdeskRef = %q, want %q", tt.HelpdeskRef, tc.want)
			}
			if got := len(b.Warnings) > 0; got != tc.wantWarning {
				t.Errorf("warnings = %v, want a warning: %v", b.Warnings, tc.wantWarning)
			}
			if len(b.Warnings) != len(p.state.Warnings) {
				t.Errorf("the run state and the prompt disagree: %v vs %v", p.state.Warnings, b.Warnings)
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
	r := &Runner{Deps: Deps{Config: cfg}}

	long := strings.Repeat("x", 500)
	tt := ticket.TrackerTicket{Key: "OMNI-1", Description: "Zoho Ticket URL: " + long}
	var b ticket.Bundle
	r.applyHelpdeskRefFallback(&prepared{}, &b, &tt)

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
	r := &Runner{Deps: Deps{Config: cfg}}

	tt := ticket.TrackerTicket{Description: "Zoho Ticket URL: https://desk.zoho.com/agent/a/support/tickets/details/42"}
	var b ticket.Bundle
	r.applyHelpdeskRefFallback(&prepared{}, &b, &tt)
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
			r := &Runner{Deps: Deps{Config: cfg, Tracker: descTracker{description: desc, helpdeskRef: tc.native}}}

			b, err := r.fetchBundle(context.Background(), "OMNI-1", &prepared{})
			if err != nil {
				t.Fatalf("fetchBundle: %v", err)
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
