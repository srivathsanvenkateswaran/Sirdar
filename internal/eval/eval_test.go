package eval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	runner "github.com/srivathsanvenkateswaran/sirdar/internal/run"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/ticket"
)

// --- fixtures ---------------------------------------------------------

const triageDoc = `{
  "ticket": {"key":"OMNI-1","title":"Export fails","trackerUrl":"https://t/OMNI-1","helpdeskId":"555","helpdeskUrl":"https://h/555","priority":"high","service":"omni","customer":"Acme","customerId":"4561"},
  "title": "Export fails for large orders",
  "complaint": "The export fails for large orders.",
  "timeline": [{"at":"2026-09-10T08:30:00+03:00","role":"customer","summary":"Reported the export failing."}],
  "reproSteps": ["Request a CSV export for a 600-line order."],
  "rootCause": {"hypothesis":"The export job times out.","confidence":"medium","evidence":[{"source":"logs","query":"service:export level:error","finding":"Timeout after 30s."}],"codeRefs":["Domain/Inventory.API/Export/Csv.cs:42"]},
  "blastRadius": "Any order above 500 line items.",
  "classification": "code",
  "proposedFix": {"description":"Stream the export.","files":["Domain/Inventory.API/Export/Csv.cs"],"remediationSql":"","risks":"none"},
  "openQuestions": ["Which tenant sizes are affected?","Is the timeout configurable?","Has it ever worked?"]
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
playbooks: .sirdar/playbooks
`

func sampleBundle() ticket.Bundle {
	t0 := time.Date(2026, 9, 10, 8, 30, 0, 0, time.UTC)
	return ticket.Bundle{
		Tracker: &ticket.TrackerTicket{
			Key: "OMNI-1", Title: "Export fails", HelpdeskRef: "555",
			URL: "https://t/OMNI-1", Priority: "high", CreatedAt: t0,
		},
		Helpdesk: &ticket.HelpdeskTicket{
			ID: "555", Subject: "Export", Customer: "Acme", CustomerID: "4561",
			URL: "https://h/555", Priority: "high", CreatedAt: t0,
		},
		Thread: ticket.Thread{
			{At: t0, Author: "Customer", Role: ticket.RoleCustomer, Text: "The export does not work"},
		},
	}
}

// newWorkspace writes a real .sirdar workspace and loads it the way a run
// sees it.
func newWorkspace(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sirdar", "playbooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// newGolden writes one golden entry: a real bundle, plus whatever expected
// files the test asks for.
func newGolden(t *testing.T, key string, expectedJSON, expectedMD string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, key)
	if err := ticket.WriteBundle(filepath.Join(dir, "bundle"), sampleBundle()); err != nil {
		t.Fatal(err)
	}
	if expectedJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "expected.json"), []byte(expectedJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if expectedMD != "" {
		if err := os.WriteFile(filepath.Join(dir, "expected.md"), []byte(expectedMD), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// --- stub provider ----------------------------------------------------

type stubSession struct {
	events chan provider.Event
	done   chan struct{}
}

func (s *stubSession) Events() <-chan provider.Event               { return s.events }
func (s *stubSession) Send(ctx context.Context, text string) error { return nil }
func (s *stubSession) CloseInput() error                           { return nil }
func (s *stubSession) Handle() string                              { return "h1" }
func (s *stubSession) Cancel()                                     {}
func (s *stubSession) Wait() (provider.Result, error)              { return provider.Result{Handle: "h1"}, nil }
func (s *stubSession) emit(ev provider.Event)                      { s.events <- ev }

type stubProvider struct {
	doc   string
	specs chan provider.SessionSpec
}

func (p *stubProvider) Name() string                                               { return "claude" }
func (p *stubProvider) Doctor(ctx context.Context, binary string) []provider.Check { return nil }

func (p *stubProvider) Start(ctx context.Context, spec provider.SessionSpec) (provider.Session, error) {
	if p.specs != nil {
		select {
		case p.specs <- spec:
		default:
		}
	}
	s := &stubSession{events: make(chan provider.Event, 4), done: make(chan struct{})}
	go func() {
		s.emit(provider.Event{Kind: provider.EvUsage, Turns: 7, CostUSD: 0.25})
		s.emit(provider.Event{Kind: provider.EvFinal, Final: json.RawMessage(p.doc)})
		close(s.events)
	}()
	return s, nil
}

// refusingSource fails the test the moment anything asks it for a ticket:
// an eval replays a stored bundle, and a run that reached the helpdesk
// would be scoring a different ticket than the golden one.
type refusingSource struct{ t *testing.T }

func (r refusingSource) Get(ctx context.Context, key string) (ticket.TrackerTicket, error) {
	r.t.Errorf("the tracker was called for %s during an eval run", key)
	return ticket.TrackerTicket{}, nil
}

func (r refusingSource) List(ctx context.Context, f source.ListFilter) ([]ticket.TrackerTicket, error) {
	r.t.Error("the tracker was listed during an eval run")
	return nil, nil
}

type refusingHelpdesk struct{ t *testing.T }

func (r refusingHelpdesk) Get(ctx context.Context, id string) (ticket.HelpdeskTicket, error) {
	r.t.Errorf("the helpdesk was called for %s during an eval run", id)
	return ticket.HelpdeskTicket{}, nil
}

func (r refusingHelpdesk) Threads(ctx context.Context, id string) (ticket.Thread, error) {
	r.t.Error("the helpdesk thread was read during an eval run")
	return nil, nil
}

func (r refusingHelpdesk) Attachments(ctx context.Context, id, dir string) ([]ticket.Attachment, error) {
	r.t.Error("helpdesk attachments were downloaded during an eval run")
	return nil, nil
}

func newDeps(t *testing.T, cfg *config.Config, p provider.Provider) runner.Deps {
	t.Helper()
	return runner.Deps{
		Config:   cfg,
		Tracker:  refusingSource{t},
		Helpdesk: refusingHelpdesk{t},
		Provider: p,
		Env:      []string{"PATH=/usr/bin"},
	}
}

// --- tests ------------------------------------------------------------

// TestRunReplaysTheBundleAndScoresIt is the whole command in one case: the
// golden bundle is what the session sees, no source is called, every
// assertion is evaluated, and the report lands on disk.
func TestRunReplaysTheBundleAndScoresIt(t *testing.T) {
	cfg := newWorkspace(t)
	golden := newGolden(t, "OMNI-1", `{
  "rootCause.confidence": "medium",
  "classification": "code",
  "rootCause.codeRefs_contains": ["Domain/Inventory.API/"],
  "openQuestions_min": 3
}`, "")

	specs := make(chan provider.SessionSpec, 1)
	deps := newDeps(t, cfg, &stubProvider{doc: triageDoc, specs: specs})

	report, err := Run(t.Context(), deps, nil, Options{GoldenDir: golden})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("%d results, want 1", len(report.Results))
	}
	res := report.Results[0]
	if res.Key != "OMNI-1" || res.State != "completed" {
		t.Fatalf("result %+v", res)
	}
	if !res.SchemaValid {
		t.Error("the note did not validate")
	}
	if res.Passed != 4 || res.Total != 4 {
		t.Fatalf("assertions %d/%d: %+v", res.Passed, res.Total, res.Checks)
	}
	if res.Turns != 7 || res.CostUSD != 0.25 {
		t.Errorf("usage turns=%d cost=%v", res.Turns, res.CostUSD)
	}
	if res.Overlap != nil {
		t.Error("an overlap was scored with no expected.md")
	}

	// The bundle the session saw is the golden one, copied into the run.
	runBundle := filepath.Join(cfg.Root, ".sirdar", "runs", "OMNI-1", res.RunID, "bundle")
	for _, name := range []string{"ticket.json", "thread.md"} {
		if _, err := os.Stat(filepath.Join(runBundle, name)); err != nil {
			t.Errorf("the golden bundle's %s was not copied into the run: %v", name, err)
		}
	}
	select {
	case spec := <-specs:
		if !strings.Contains(spec.Prompt, "Export fails") {
			t.Error("the prompt was not assembled from the golden bundle")
		}
		if spec.Mode.IsFix() {
			t.Error("an eval replay started a fix session")
		}
	default:
		t.Fatal("no session was started")
	}

	// The report is on disk and says the same thing the table does.
	path, err := report.Write(cfg.Root)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var reread Report
	if err := json.Unmarshal(data, &reread); err != nil {
		t.Fatalf("the report is not valid JSON: %v", err)
	}
	if len(reread.Results) != 1 || reread.Results[0].Passed != 4 {
		t.Fatalf("the report on disk says %+v", reread.Results)
	}
	if dir := filepath.Dir(path); dir != filepath.Join(cfg.Root, ".sirdar", "eval") {
		t.Errorf("the report went to %s", dir)
	}

	table := report.Table()
	for _, want := range []string{"KEY", "OMNI-1", "completed", "4/4", "yes"} {
		if !strings.Contains(table, want) {
			t.Errorf("the table does not mention %q:\n%s", want, table)
		}
	}
	if ExitCode(report) != 0 {
		t.Error("a fully passing eval exited non-zero")
	}
}

// TestRunReportsFailedAssertions: a run that produced a valid note but the
// wrong answer is the case the command exists for, so the failure has to
// reach the table with the value that was actually there.
func TestRunReportsFailedAssertions(t *testing.T) {
	cfg := newWorkspace(t)
	golden := newGolden(t, "OMNI-1", `{
  "rootCause.confidence": "high",
  "classification": "data",
  "openQuestions_min": 9
}`, "")
	deps := newDeps(t, cfg, &stubProvider{doc: triageDoc})

	report, err := Run(t.Context(), deps, []string{"OMNI-1"}, Options{GoldenDir: golden})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := report.Results[0]
	if res.Passed != 0 || res.Total != 3 {
		t.Fatalf("assertions %d/%d: %+v", res.Passed, res.Total, res.Checks)
	}
	table := report.Table()
	for _, want := range []string{`want "high", got "medium"`, `want "data", got "code"`, "want at least 9, got 3"} {
		if !strings.Contains(table, want) {
			t.Errorf("the table does not explain %q:\n%s", want, table)
		}
	}
	if ExitCode(report) != 1 {
		t.Error("an eval with failed assertions exited zero")
	}
}

// TestRunScoresAgainstAHumanNote checks the coarse overlap: what the human
// cited and structured, and what came back.
func TestRunScoresAgainstAHumanNote(t *testing.T) {
	cfg := newWorkspace(t)
	expectedMD := `# Export fails

## Customer Complaint (translated)

It times out.

## Root Cause Hypothesis

See Domain/Inventory.API/Export/Csv.cs:42 and Domain/Inventory.API/Export/Writer.cs:88.

## Blast Radius Nobody Wrote About
`
	golden := newGolden(t, "OMNI-1", "", expectedMD)
	deps := newDeps(t, cfg, &stubProvider{doc: triageDoc})

	report, err := Run(t.Context(), deps, nil, Options{GoldenDir: golden})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := report.Results[0]
	if res.Overlap == nil {
		t.Fatal("no overlap was scored against expected.md")
	}
	if res.Overlap.Refs.Matched != 1 || res.Overlap.Refs.Total != 2 {
		t.Errorf("refs %+v, want 1 of 2", res.Overlap.Refs)
	}
	if len(res.Overlap.MissingRefs) != 1 || !strings.Contains(res.Overlap.MissingRefs[0], "writer.cs:88") {
		t.Errorf("missing refs %v", res.Overlap.MissingRefs)
	}
	if res.Overlap.Headings.Total != 3 || res.Overlap.Headings.Matched != 2 {
		t.Errorf("headings %+v, want 2 of 3", res.Overlap.Headings)
	}
	if !strings.Contains(report.Table(), "1/2 50%") {
		t.Errorf("the table does not carry the ref overlap:\n%s", report.Table())
	}
}

func TestRunWithNoGoldenBundles(t *testing.T) {
	cfg := newWorkspace(t)
	deps := newDeps(t, cfg, &stubProvider{doc: triageDoc})
	if _, err := Run(t.Context(), deps, nil, Options{GoldenDir: t.TempDir()}); err == nil {
		t.Fatal("an empty golden set did not report itself")
	}
}

// TestRunKeepsGoingAfterAMissingKey: one bad entry must not cost the rest
// of the set its scores.
func TestRunKeepsGoingAfterAMissingKey(t *testing.T) {
	cfg := newWorkspace(t)
	golden := newGolden(t, "OMNI-1", `{"classification": "code"}`, "")
	deps := newDeps(t, cfg, &stubProvider{doc: triageDoc})

	report, err := Run(t.Context(), deps, []string{"OMNI-404", "OMNI-1"}, Options{GoldenDir: golden})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("%d results", len(report.Results))
	}
	if report.Results[0].State != "failed" || !strings.Contains(report.Results[0].Reason, "no bundle") {
		t.Errorf("missing key result %+v", report.Results[0])
	}
	if report.Results[1].Passed != 1 {
		t.Errorf("the second key was not scored: %+v", report.Results[1])
	}
}
