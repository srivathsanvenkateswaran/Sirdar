package run

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
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

// newWorkspace writes a real .sirdar workspace into a temp dir and loads
// it through config.Load, so Config.Root and every default is set the way
// a real run sees them.
func newWorkspace(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sirdar", "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "# Exports\n\n" + playbookMarker + "\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "playbooks", "00-test.md"), []byte(body), 0o644); err != nil {
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

	mu         sync.Mutex
	sends      []string
	cancels    int
	cancelOnce sync.Once
	finishOnce sync.Once
}

func (s *stubSession) Events() <-chan provider.Event { return s.events }

func (s *stubSession) Send(ctx context.Context, userText string) error {
	s.mu.Lock()
	s.sends = append(s.sends, userText)
	s.mu.Unlock()
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
	name   string
	script func(spec provider.SessionSpec, s *stubSession)

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
	}
	s.result.Handle = s.handle
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
	return &Runner{Deps{
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
	p := &stubProvider{script: replay(events...)}
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
	if len(spec.Images) != 0 {
		t.Fatalf("claude sessions take no images: %v", spec.Images)
	}
	if out.Digest.Confidence != "medium" || out.Digest.Classification != "code" || out.Digest.Issue == "" {
		t.Fatalf("digest: %+v", out.Digest)
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
	pauseObserver = func(time.Time) { once.Do(func() { close(gate) }) }
	t.Cleanup(func() { pauseObserver = nil })

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

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
