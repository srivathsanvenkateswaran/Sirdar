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
	warnings []string
}

func (s stubHelpdesk) Warnings() []string { return s.warnings }

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
	sendErr   error // when set, Send fails the way a finished CLI does

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
