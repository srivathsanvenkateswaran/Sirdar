package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sync"

	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
)

// The ids the fake service knows. Anything else is a 404, which is how the
// unknown-id cases below are driven.
const (
	knownWS        = "ws1"
	knownRun       = "20260910T120000Z-ab12"
	knownJob       = "job-1"
	knownMCPServer = "fake"
)

// mcpCall records what the call route asked the service.
type mcpCall struct{ Server, Tool, Args string }

// dropCall is what the drop route passed the service.
type dropCall struct {
	Path string
	Hunk int
	ETag string
}

// fake is a Service that answers from canned data and records what it was
// asked, so the handlers can be tested without internal/app.
type fake struct {
	mu sync.Mutex

	workspaces []Workspace
	tickets    []Ticket
	runs       []RunSummary
	detail     RunDetail
	events     []RunEvent
	next       int
	note       string
	prompt     string
	register   []RegisterRow
	checks     []Check
	quotas     []Quota

	golden  []GoldenEntry
	reports []EvalReport
	summary ConfigSummary
	diff    RunDiff

	inventory MCPInventory
	toolList  MCPToolList

	// The playbooks the fake holds, by name, in the order Playbooks
	// answers with; playbookRows says what the list route returns.
	playbooks    map[string]string
	playbookRows []PlaybookSummary
	// What the playbook handlers passed in, and the failures to inject.
	gotPlaybookSave   struct{ Name, Body string }
	gotPlaybookAdd    struct{ Name, Body string }
	gotPlaybookDelete string
	gotPlaybookOpen   string
	scaffolded        bool
	playbookErr       error

	// Search answers with these, and records the query.
	hits      []SearchHit
	gotSearch string

	// Failures to inject.
	queueUnsupported bool
	deleteErr        error
	queueErr         error
	noteMissing      bool
	addErr           error
	diffErr          error
	dropErr          error

	// What the handlers passed in.
	gotRoot      string
	gotRemoved   string
	gotDeleted   string
	gotFilter    QueueFilter
	gotHelpdesk  string
	helpdeskLink HelpdeskLink
	gotCompose   string
	composed     ComposedIntent
	gotKey       string
	gotAfter     int
	gotNoteKind  string
	gotKeys      []string
	gotTriage    TriageOptions
	gotRCAKey    string
	gotRCA       RCAOptions
	gotFixKey    string
	gotFix       FixOptions
	gotEvalKeys  []string
	gotEval      EvalOptions
	retro        *RetroReport
	gotGolden    struct{ Key, RunID string }
	gotAnswer    string
	gotSteer     string
	gotModel     string
	steerErr     error // when set, Steer refuses with it
	gotCancelled JobID
	gotConnect   bool
	gotCall      mcpCall
	gotDrop      dropCall

	// Webhook plumbing: the reason TriageIfIdle gives for starting
	// nothing, the error it fails with, and what the hook route asked it
	// and reported.
	idleReason string
	idleErr    error
	gotIdle    []string
	gotHooks   []hookCall

	// Subscribe plumbing for the SSE tests.
	ch          chan Event
	unsubscribe chan struct{}
}

func newFake() *fake {
	return &fake{
		workspaces: []Workspace{{
			ID: knownWS, Name: "oxo-apis", Root: "/repos/oxo-apis",
			Provider: "claude", Model: "sonnet", NotesDir: "/notes", Billing: "subscription",
		}},
		tickets: []Ticket{{
			Key: "OMNI-2510", Title: "Payment stuck", Priority: "P1", Status: "Open",
			Assignee: "sri", URL: "https://tracker/OMNI-2510", HelpdeskRef: "ZD-77",
			UpdatedAt: "2026-09-10T09:00:00Z",
		}},
		runs: []RunSummary{{
			RunID: knownRun, Key: "OMNI-2510", Kind: "triage", Status: "completed",
			Provider: "claude", Model: "sonnet",
			StartedAt: "2026-09-10T12:00:00Z", UpdatedAt: "2026-09-10T12:04:00Z",
			Usage: Usage{Turns: 7, InputTokens: 1200, OutputTokens: 900, CostUSD: 0.42},
			Notes: []string{"/notes/OMNI-2510-triage.md"},
		}},
		events: []RunEvent{
			{T: "2026-09-10T12:00:01Z", Kind: "tool", Payload: EventPayload{Tool: "grep"}},
			{T: "2026-09-10T12:00:09Z", Kind: "usage", Payload: EventPayload{Turns: 2, CostUSD: 0.1}},
		},
		next:     2,
		note:     "# Triage OMNI-2510\n\nThe adapter times out.\n",
		prompt:   "# Prompt\n\nYou are triaging OMNI-2510.\n",
		register: []RegisterRow{{Key: "OMNI-2510", Kind: "triage", RunID: knownRun, Date: "2026-09-10", Provider: "claude", Model: "sonnet", Service: "payments", Classification: "bug", Confidence: "high", Severity: "P1", Turns: 7, CostUSD: 0.42, TriageVerdict: "held", NotePath: "/notes/OMNI-2510-triage.md"}},
		checks:   []Check{{Name: "claude cli", OK: true, Detail: "1.2.3"}},
		golden:   []GoldenEntry{{Key: "OMNI-2510", Dir: "/golden/OMNI-2510", BundleDir: "/golden/OMNI-2510/bundle", Assertions: 3, HasExpectedNote: true}},
		quotas:   []Quota{{Provider: "claude", ObservedAt: "2026-09-10T12:00:00Z", FiveHour: &QuotaWindow{Utilization: 0.31, ResetsAt: "2026-09-10T15:00:00Z"}}},
		inventory: MCPInventory{
			Servers: []MCPServer{{
				Entry: mcpclient.Entry{
					Name: knownMCPServer, Scope: "workspace", Transport: "stdio",
					Command: "/usr/local/bin/oxo-mcp", EnvKeys: []string{"OXO_TOKEN"},
					Source: "/repos/oxo-apis/.mcp.json",
				},
				Connected: true, Tools: 2, TookMs: 41,
			}},
			Warnings:      []string{},
			WorkspaceOnly: true,
			Permissions:   []string{},
		},
		playbooks: map[string]string{
			"10-helpdesk.md": "# Helpdesk\n\nRead the whole thread first.\n",
		},
		playbookRows: []PlaybookSummary{{
			Name: "10-helpdesk.md", File: ".sirdar/playbooks/10-helpdesk.md",
			Title: "Helpdesk", Lede: "Read the whole thread first.", Order: "10", Bytes: 44,
		}},
		toolList: MCPToolList{
			Server: knownMCPServer,
			Tools: []MCPTool{
				{Name: "list_rows", FullName: "mcp__fake__list_rows", Verdict: "allowed",
					Rule: "read-word", Reason: `read word "list", with no write word beside it`},
				{Name: "delete_rows", FullName: "mcp__fake__delete_rows", Verdict: "denied",
					Rule: "write-word", Reason: `write word "delete" in the name`},
			},
			TookMs:      41,
			Permissions: []string{},
		},
	}
}

func (f *fake) detailValue() RunDetail {
	if f.detail.RunID != "" {
		return f.detail
	}
	return RunDetail{
		RunSummary: f.runs[0],
		PromptPath: "/repos/oxo-apis/.sirdar/runs/OMNI-2510/" + knownRun + "/prompt.md",
		BundleDir:  "/repos/oxo-apis/.sirdar/runs/OMNI-2510/" + knownRun + "/bundle",
		Warnings:   []string{},
		Handle:     "sess-9",
		Budget:     Budget{MaxTurns: 40, MaxMinutes: 20, MaxUSD: 5},
	}
}

func unknownWS(id string) error {
	return fmt.Errorf("workspace %q: %w", id, ErrNotFound)
}

func (f *fake) checkWS(id string) error {
	if id != knownWS {
		return unknownWS(id)
	}
	return nil
}

func (f *fake) checkRun(wsID, runID string) error {
	if err := f.checkWS(wsID); err != nil {
		return err
	}
	if runID != knownRun {
		return fmt.Errorf("run %q: %w", runID, ErrNotFound)
	}
	return nil
}

func (f *fake) Workspaces() ([]Workspace, error) { return f.workspaces, nil }

func (f *fake) AddWorkspace(root string) (Workspace, error) {
	f.mu.Lock()
	f.gotRoot = root
	f.mu.Unlock()
	if f.addErr != nil {
		return Workspace{}, f.addErr
	}
	return Workspace{ID: "ws2", Name: "new", Root: root, Provider: "claude", Model: "sonnet", NotesDir: "/notes", Billing: "subscription"}, nil
}

func (f *fake) RemoveWorkspace(id string) error {
	if err := f.checkWS(id); err != nil {
		return err
	}
	f.mu.Lock()
	f.gotRemoved = id
	f.mu.Unlock()
	return nil
}

func (f *fake) ResolveHelpdesk(_ context.Context, wsID, number string) (HelpdeskLink, error) {
	if err := f.checkWS(wsID); err != nil {
		return HelpdeskLink{}, err
	}
	f.mu.Lock()
	f.gotHelpdesk = number
	link := f.helpdeskLink
	f.mu.Unlock()
	link.Number = number
	return link, nil
}

func (f *fake) ComposeIntent(_ context.Context, wsID, text string) (ComposedIntent, error) {
	if err := f.checkWS(wsID); err != nil {
		return ComposedIntent{}, err
	}
	f.mu.Lock()
	f.gotCompose = text
	out := f.composed
	f.mu.Unlock()
	return out, nil
}

func (f *fake) Queue(_ context.Context, wsID string, filter QueueFilter) ([]Ticket, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	if f.queueUnsupported {
		return nil, fmt.Errorf("workspace has no tracker: %w", ErrUnsupported)
	}
	if f.queueErr != nil {
		return nil, f.queueErr
	}
	f.mu.Lock()
	f.gotFilter = filter
	f.mu.Unlock()
	return f.tickets, nil
}

func (f *fake) Runs(wsID, key string) ([]RunSummary, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.gotKey = key
	f.mu.Unlock()
	return f.runs, nil
}

func (f *fake) Run(wsID, runID string) (RunDetail, error) {
	if err := f.checkRun(wsID, runID); err != nil {
		return RunDetail{}, err
	}
	return f.detailValue(), nil
}

func (f *fake) DeleteRun(wsID, runID string) error {
	if err := f.checkRun(wsID, runID); err != nil {
		return err
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	f.gotDeleted = runID
	f.mu.Unlock()
	return nil
}

func (f *fake) Search(wsID, q string) ([]SearchHit, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.gotSearch = q
	f.mu.Unlock()
	return f.hits, nil
}

func (f *fake) Events(wsID, runID string, after int) ([]RunEvent, int, error) {
	if err := f.checkRun(wsID, runID); err != nil {
		return nil, 0, err
	}
	f.mu.Lock()
	f.gotAfter = after
	f.mu.Unlock()
	if after >= len(f.events) {
		return nil, after, nil
	}
	return f.events[after:], f.next, nil
}

func (f *fake) Note(wsID, runID, kind string) (string, error) {
	if err := f.checkRun(wsID, runID); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.gotNoteKind = kind
	f.mu.Unlock()
	if f.noteMissing {
		// The service reads notes off the disk, so a note that was never
		// written arrives as a plain file error.
		return "", fmt.Errorf("open note: %w", fs.ErrNotExist)
	}
	return f.note, nil
}

func (f *fake) Prompt(wsID, runID string) (string, error) {
	if err := f.checkRun(wsID, runID); err != nil {
		return "", err
	}
	return f.prompt, nil
}

func (f *fake) RunDiff(wsID, runID string) (RunDiff, error) {
	if err := f.checkRun(wsID, runID); err != nil {
		return RunDiff{}, err
	}
	if f.diffErr != nil {
		return RunDiff{}, f.diffErr
	}
	return f.diff, nil
}

func (f *fake) DropHunk(_ context.Context, wsID, runID, path string, hunk int, etag string) (RunDiff, error) {
	if err := f.checkRun(wsID, runID); err != nil {
		return RunDiff{}, err
	}
	f.mu.Lock()
	f.gotDrop = dropCall{Path: path, Hunk: hunk, ETag: etag}
	f.mu.Unlock()
	if f.dropErr != nil {
		return RunDiff{}, f.dropErr
	}
	return f.diff, nil
}

func (f *fake) StartTriage(_ context.Context, wsID string, keys []string, o TriageOptions) (JobID, error) {
	if err := f.checkWS(wsID); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.gotKeys, f.gotTriage = keys, o
	f.mu.Unlock()
	return knownJob, nil
}

// hookCall is one hook.received event the route published.
type hookCall struct{ Source, Key, Outcome string }

func (f *fake) TriageIfIdle(_ context.Context, wsID, key string, _ TriageOptions) (JobID, string, error) {
	if err := f.checkWS(wsID); err != nil {
		return "", "", err
	}
	f.mu.Lock()
	f.gotIdle = append(f.gotIdle, key)
	f.mu.Unlock()
	if f.idleErr != nil {
		return "", "", f.idleErr
	}
	if f.idleReason != "" {
		return "", f.idleReason, nil
	}
	return knownJob, "", nil
}

func (f *fake) HookReceived(source, key, outcome string) {
	f.mu.Lock()
	f.gotHooks = append(f.gotHooks, hookCall{source, key, outcome})
	f.mu.Unlock()
}

func (f *fake) hooks() []hookCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]hookCall(nil), f.gotHooks...)
}

func (f *fake) StartFix(_ context.Context, wsID, key string, o FixOptions) (JobID, error) {
	if err := f.checkWS(wsID); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.gotFixKey, f.gotFix = key, o
	f.mu.Unlock()
	return knownJob, nil
}

func (f *fake) StartEval(_ context.Context, wsID string, keys []string, o EvalOptions) (JobID, error) {
	if err := f.checkWS(wsID); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.gotEvalKeys, f.gotEval = keys, o
	f.mu.Unlock()
	return knownJob, nil
}

func (f *fake) LatestRetro(wsID string) (*RetroReport, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	return f.retro, nil
}

func (f *fake) EvalReports(wsID string) ([]EvalReport, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	return f.reports, nil
}

func (f *fake) Golden(wsID string) ([]GoldenEntry, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	return f.golden, nil
}

func (f *fake) AddGolden(wsID, key, runID string) (GoldenEntry, error) {
	if err := f.checkWS(wsID); err != nil {
		return GoldenEntry{}, err
	}
	f.mu.Lock()
	f.gotGolden.Key, f.gotGolden.RunID = key, runID
	f.mu.Unlock()
	return f.golden[0], nil
}

func (f *fake) ConfigSummary(wsID string) (ConfigSummary, error) {
	if err := f.checkWS(wsID); err != nil {
		return ConfigSummary{}, err
	}
	return f.summary, nil
}

// --- playbooks ---

// checkPlaybook is the service's own guard as the fake spells it: an
// unknown workspace, an injected failure, then a name nothing is filed
// under.
func (f *fake) checkPlaybook(wsID, name string) error {
	if err := f.checkWS(wsID); err != nil {
		return err
	}
	if f.playbookErr != nil {
		return f.playbookErr
	}
	if _, ok := f.playbooks[name]; !ok {
		return fmt.Errorf("playbook %q: %w", name, ErrNoSuchPlaybook)
	}
	return nil
}

func (f *fake) Playbooks(wsID string) ([]PlaybookSummary, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	if f.playbookErr != nil {
		return nil, f.playbookErr
	}
	return f.playbookRows, nil
}

func (f *fake) Playbook(wsID, name string) (string, error) {
	if err := f.checkPlaybook(wsID, name); err != nil {
		return "", err
	}
	return f.playbooks[name], nil
}

func (f *fake) SavePlaybook(wsID, name, body string) (PlaybookSummary, error) {
	if err := f.checkPlaybook(wsID, name); err != nil {
		return PlaybookSummary{}, err
	}
	f.gotPlaybookSave = struct{ Name, Body string }{name, body}
	f.playbooks[name] = body
	return PlaybookSummary{Name: name, File: ".sirdar/playbooks/" + name, Title: "Helpdesk", Bytes: int64(len(body))}, nil
}

func (f *fake) AddPlaybook(wsID, name, body string) (PlaybookSummary, error) {
	if err := f.checkWS(wsID); err != nil {
		return PlaybookSummary{}, err
	}
	if f.playbookErr != nil {
		return PlaybookSummary{}, f.playbookErr
	}
	if _, taken := f.playbooks[name]; taken {
		return PlaybookSummary{}, fmt.Errorf("playbook %q: %w", name, ErrPlaybookExists)
	}
	f.gotPlaybookAdd = struct{ Name, Body string }{name, body}
	f.playbooks[name] = body
	return PlaybookSummary{Name: name, File: ".sirdar/playbooks/" + name, Title: name, Bytes: int64(len(body))}, nil
}

func (f *fake) DeletePlaybook(wsID, name string) error {
	if err := f.checkPlaybook(wsID, name); err != nil {
		return err
	}
	f.gotPlaybookDelete = name
	delete(f.playbooks, name)
	return nil
}

func (f *fake) ScaffoldPlaybooks(wsID string) ([]PlaybookSummary, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	if f.playbookErr != nil {
		return nil, f.playbookErr
	}
	f.scaffolded = true
	return f.playbookRows, nil
}

func (f *fake) OpenPlaybook(wsID, name string) error {
	if err := f.checkPlaybook(wsID, name); err != nil {
		return err
	}
	f.gotPlaybookOpen = name
	return nil
}

func (f *fake) MCPServers(_ context.Context, wsID string, connect bool) (MCPInventory, error) {
	if err := f.checkWS(wsID); err != nil {
		return MCPInventory{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotConnect = connect
	inv := f.inventory
	if !connect {
		// The list route never starts anything, so the connected half of
		// each row is not there to read.
		for i := range inv.Servers {
			inv.Servers[i].Connected = false
			inv.Servers[i].Tools = 0
			inv.Servers[i].TookMs = 0
		}
	}
	return inv, nil
}

func (f *fake) MCPTools(_ context.Context, wsID, server string) (MCPToolList, error) {
	if err := f.checkWS(wsID); err != nil {
		return MCPToolList{}, err
	}
	if server != knownMCPServer {
		return MCPToolList{}, fmt.Errorf("%w: %s", ErrNoSuchMCPServer, server)
	}
	return f.toolList, nil
}

func (f *fake) MCPCall(_ context.Context, wsID, server, tool string, args json.RawMessage) (MCPCallResult, error) {
	if err := f.checkWS(wsID); err != nil {
		return MCPCallResult{}, err
	}
	if server != knownMCPServer {
		return MCPCallResult{}, fmt.Errorf("%w: %s", ErrNoSuchMCPServer, server)
	}
	f.mu.Lock()
	f.gotCall = mcpCall{Server: server, Tool: tool, Args: string(args)}
	f.mu.Unlock()

	for _, t := range f.toolList.Tools {
		if t.Name != tool {
			continue
		}
		res := MCPCallResult{Server: server, Tool: tool, Verdict: t.Verdict, Reason: t.Reason, TookMs: 7}
		if t.Verdict != "allowed" {
			res.TookMs = 0
			return res, fmt.Errorf("%w: %s: %s", ErrMCPDenied, t.FullName, t.Reason)
		}
		res.Result = "rows of orders"
		return res, nil
	}
	return MCPCallResult{}, fmt.Errorf("%w: %s", ErrNoSuchMCPServer, server)
}

func (f *fake) StartRCA(_ context.Context, wsID, key string, o RCAOptions) (JobID, error) {
	if err := f.checkWS(wsID); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.gotRCAKey, f.gotRCA = key, o
	f.mu.Unlock()
	return knownJob, nil
}

func (f *fake) Resume(_ context.Context, wsID, runID, answer, model string) (JobID, error) {
	if err := f.checkRun(wsID, runID); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.gotAnswer, f.gotModel = answer, model
	f.mu.Unlock()
	return knownJob, nil
}

func (f *fake) Steer(_ context.Context, wsID, runID, text, model string) (JobID, error) {
	if err := f.checkRun(wsID, runID); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.steerErr != nil {
		return "", f.steerErr
	}
	f.gotSteer, f.gotModel = text, model
	return knownJob, nil
}

func (f *fake) Cancel(id JobID) error {
	if string(id) != knownJob {
		return fmt.Errorf("job %q: %w", id, ErrNotFound)
	}
	f.mu.Lock()
	f.gotCancelled = id
	f.mu.Unlock()
	return nil
}

func (f *fake) Register(wsID string) ([]RegisterRow, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	return f.register, nil
}

func (f *fake) Doctor(_ context.Context, wsID string) ([]Check, error) {
	if err := f.checkWS(wsID); err != nil {
		return nil, err
	}
	return f.checks, nil
}

func (f *fake) Quota() []Quota { return f.quotas }

func (f *fake) Subscribe() (<-chan Event, func()) {
	f.mu.Lock()
	if f.ch == nil {
		f.ch = make(chan Event, 8)
	}
	if f.unsubscribe == nil {
		f.unsubscribe = make(chan struct{})
	}
	ch, done := f.ch, f.unsubscribe
	f.mu.Unlock()
	var once sync.Once
	return ch, func() { once.Do(func() { close(done) }) }
}
