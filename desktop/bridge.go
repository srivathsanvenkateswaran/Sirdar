package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

// callTimeout caps the Bridge methods that reach outside the process: Queue
// talks to the workspace's tracker adapter, Doctor shells out to the provider
// CLI, and the three MCP methods start or connect to a server. Everything
// else only touches the run directory.
const callTimeout = 60 * time.Second

// EventsPage is what Events returns. internal/app returns the page and the
// next index as two values, which Wails cannot bind, so the pair is carried
// as one struct — the same shape the HTTP endpoint answers with.
type EventsPage struct {
	Events []app.RunEvent `json:"events"`
	Next   int            `json:"next"`
}

// Bridge is the struct Wails binds to the frontend. Every method is a thin
// pass-through to app.Service with a signature Wails can generate bindings
// for: plain structs and slices in, a value and an error out. Bound methods
// take no context, so the ones that need one make their own.
type Bridge struct {
	svc *app.Service
}

// NewBridge returns the Bridge over svc that main passes to wails.Run.
func NewBridge(svc *app.Service) *Bridge { return &Bridge{svc: svc} }

// Version returns the desktop build's version, as stamped by
// `wails build -ldflags "-X main.version=..."`. It has no app.Service
// counterpart, so it is not in bridgeMethods: there is nothing to forward to.
func (b *Bridge) Version() string { return version }

// --- workspaces -------------------------------------------------------

// Workspaces lists every registered workspace.
func (b *Bridge) Workspaces() ([]app.Workspace, error) { return b.svc.Workspaces() }

// AddWorkspace registers a workspace root.
func (b *Bridge) AddWorkspace(root string) (app.Workspace, error) {
	return b.svc.AddWorkspace(root)
}

// RemoveWorkspace drops a workspace from the registry.
func (b *Bridge) RemoveWorkspace(id string) error { return b.svc.RemoveWorkspace(id) }

// --- reading ----------------------------------------------------------

// Queue lists the workspace's tracker tickets with their newest run.
func (b *Bridge) Queue(ws string, f app.QueueFilter) ([]app.Ticket, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return b.svc.Queue(ctx, ws, f)
}

// ResolveHelpdesk answers which tracker issue a helpdesk number belongs
// to. It reads one helpdesk record and starts nothing; a number with no
// tracker issue behind it comes back with the reason on it rather than as
// an error, because the composer asks this while somebody is still typing.
func (b *Bridge) ResolveHelpdesk(ws, number string) (app.HelpdeskLink, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return b.svc.ResolveHelpdesk(ctx, ws, number)
}

// ComposeIntent reads one ambiguous composer line with a single short
// provider call and answers what it was understood as. It starts nothing:
// the composer draws the reading as chips and waits to be told to go.
func (b *Bridge) ComposeIntent(ws, text string) (app.ComposedIntent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return b.svc.ComposeIntent(ctx, ws, text)
}

// Runs lists a workspace's runs, newest first. An empty key lists all.
func (b *Bridge) Runs(ws, key string) ([]app.RunSummary, error) { return b.svc.Runs(ws, key) }

// Run returns one run's full detail.
func (b *Bridge) Run(ws, runId string) (app.RunDetail, error) { return b.svc.Run(ws, runId) }

// DeleteRun removes one run's directory under .sirdar/runs. A run whose
// runner is still writing is refused; the register row and any filed note
// stay. The run.removed event reaches the frontend through forward().
func (b *Bridge) DeleteRun(ws, runId string) error { return b.svc.DeleteRun(ws, runId) }

// Search finds q, case folded, in every run's answer JSON and note text,
// capped at app.SearchLimit hits with a line's worth of excerpt each.
func (b *Bridge) Search(ws, q string) ([]app.SearchHit, error) {
	hits, err := b.svc.Search(ws, q)
	if err != nil {
		return nil, err
	}
	if hits == nil {
		hits = []app.SearchHit{}
	}
	return hits, nil
}

// Events returns the run's events after index `after`, and the index the
// next poll should pass.
func (b *Bridge) Events(ws, runId string, after int) (EventsPage, error) {
	events, next, err := b.svc.Events(ws, runId, after)
	if err != nil {
		return EventsPage{Events: []app.RunEvent{}, Next: after}, err
	}
	if events == nil {
		events = []app.RunEvent{}
	}
	return EventsPage{Events: events, Next: next}, nil
}

// Note returns the markdown of one of a run's notes.
func (b *Bridge) Note(ws, runId, kind string) (string, error) { return b.svc.Note(ws, runId, kind) }

// Prompt returns the prompt the run sent the agent.
func (b *Bridge) Prompt(ws, runId string) (string, error) { return b.svc.Prompt(ws, runId) }

// RunDiff returns a fix run's change: the file list and the unified patch,
// read out of the run's own worktree or, once that is gone, out of the
// repository. It starts nothing.
func (b *Bridge) RunDiff(ws, runId string) (app.RunDiff, error) { return b.svc.RunDiff(ws, runId) }

// DropHunk reverts one hunk out of a fix run's commit and amends it, then
// answers with the change as it stands afterwards. etag is the etag of the
// diff the hunk index was read from; a mismatch is refused rather than
// applied to whatever patch is there now.
//
// The loopback gate `sirdar serve` puts on this is not needed here: the
// desktop shell calls the service in process, so there is no listener for
// anybody else to reach it through.
func (b *Bridge) DropHunk(ws, runId, path string, hunk int, etag string) (app.RunDiff, error) {
	return b.svc.DropHunk(context.Background(), ws, runId, path, hunk, etag)
}

// Register returns the workspace's run register.
func (b *Bridge) Register(ws string) ([]app.RegisterRow, error) { return b.svc.Register(ws) }

// Doctor runs the workspace's health checks.
func (b *Bridge) Doctor(ws string) ([]app.Check, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return b.svc.Doctor(ctx, ws)
}

// Quota returns the newest rate-limit reading per provider.
func (b *Bridge) Quota() []app.Quota { return b.svc.Quota() }

// --- MCP inspection ---------------------------------------------------

// MCPServers lists the MCP servers a run in this workspace would be
// offered. With connect set it reaches each one, counts its tools and
// reports how long the handshake took, the way `sirdar mcp list --connect`
// does; the settings page's Test button is that flag for one server at a
// time. Nothing here carries a credential value: env and headers cross as
// key names only.
func (b *Bridge) MCPServers(ws string, connect bool) (app.MCPInventory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	inv, err := b.svc.MCPServers(ctx, ws, connect)
	if err != nil {
		return app.MCPInventory{}, err
	}
	if inv.Servers == nil {
		inv.Servers = []app.MCPServer{}
	}
	if inv.Warnings == nil {
		inv.Warnings = []string{}
	}
	if inv.Permissions == nil {
		inv.Permissions = []string{}
	}
	return inv, nil
}

// MCPTools lists every tool one server offers, with the verdict a run
// would get for it and the rule that settled it.
func (b *Bridge) MCPTools(ws, server string) (app.MCPToolList, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	list, err := b.svc.MCPTools(ctx, ws, server)
	if err != nil {
		return app.MCPToolList{}, err
	}
	if list.Tools == nil {
		list.Tools = []app.MCPTool{}
	}
	if list.Permissions == nil {
		list.Permissions = []string{}
	}
	return list, nil
}

// MCPCall runs one tool by hand. The arguments arrive as a JSON object
// rather than raw bytes because that is what Wails can carry across the
// bridge; they are re-encoded for the service. A tool the workspace's
// permissions would refuse a run is refused here too, and that refusal is
// an answer rather than an error: the result comes back with its verdict
// and reason filled in and nothing started, which is the same body the
// HTTP route answers 403 with. Only a call that could not be made at all
// is an error.
func (b *Bridge) MCPCall(ws, server, tool string, args map[string]any) (app.MCPCallResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return app.MCPCallResult{}, fmt.Errorf("mcp call arguments: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	res, err := b.svc.MCPCall(ctx, ws, server, tool, json.RawMessage(raw))
	if err != nil && !errors.Is(err, app.ErrMCPDenied) {
		return app.MCPCallResult{}, err
	}
	return res, nil
}

// Golden lists the keys in the golden set the eval runs replay.
func (b *Bridge) Golden(ws string) ([]app.GoldenEntry, error) { return b.svc.Golden(ws) }

// AddGolden copies a completed run's bundle into the golden set. An empty
// key is taken from the run the id names.
func (b *Bridge) AddGolden(ws, key, runId string) (app.GoldenEntry, error) {
	return b.svc.AddGolden(ws, key, runId)
}

// EvalReports returns the workspace's recorded eval reports, newest first.
func (b *Bridge) EvalReports(ws string) ([]app.EvalReport, error) { return b.svc.EvalReports(ws) }

// LatestRetro returns the newest retro report recorded for the workspace,
// or nil when it has run none.
func (b *Bridge) LatestRetro(ws string) (*app.RetroReport, error) { return b.svc.LatestRetro(ws) }

// ConfigSummary reports the workspace's notify and webhooks configuration
// with every credential reference cut back to its scheme.
func (b *Bridge) ConfigSummary(ws string) (app.ConfigSummary, error) {
	return b.svc.ConfigSummary(ws)
}

// --- jobs -------------------------------------------------------------

// StartTriage triages the keys in the background and returns the job id.
// The job outlives this call: its context is detached inside the service,
// so the background context handed in here only carries cancellation the
// service already declines to propagate.
func (b *Bridge) StartTriage(ws string, keys []string, o app.TriageOptions) (string, error) {
	id, err := b.svc.StartTriage(context.Background(), ws, keys, o)
	return string(id), err
}

// StartRCA produces the RCA note and resolution draft for one key.
func (b *Bridge) StartRCA(ws, key string, o app.RCAOptions) (string, error) {
	id, err := b.svc.StartRCA(context.Background(), ws, key, o)
	return string(id), err
}

// StartFix runs the confined fix flow for one key: the branch, the agent
// session, the commit, and — unless the agent reported deviating from the
// note — the push and the pull request.
func (b *Bridge) StartFix(ws, key string, o app.FixOptions) (string, error) {
	id, err := b.svc.StartFix(context.Background(), ws, key, o)
	return string(id), err
}

// StartEval replays the golden set and scores it. An empty keys list means
// every key in the set.
func (b *Bridge) StartEval(ws string, keys []string, o app.EvalOptions) (string, error) {
	id, err := b.svc.StartEval(context.Background(), ws, keys, o)
	return string(id), err
}

// Resume continues a blocked run, answering the agent's question.
func (b *Bridge) Resume(ws, runId, answer string) (string, error) {
	id, err := b.svc.Resume(context.Background(), ws, runId, answer)
	return string(id), err
}

// Steer continues a finished run with a follow-up instruction, on the
// same run.
func (b *Bridge) Steer(ws, runId, text string) (string, error) {
	id, err := b.svc.Steer(context.Background(), ws, runId, text)
	return string(id), err
}

// Cancel stops a job this process started.
func (b *Bridge) Cancel(jobId string) error { return b.svc.Cancel(app.JobID(jobId)) }
