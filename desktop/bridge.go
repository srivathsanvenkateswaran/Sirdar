package main

import (
	"context"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

// callTimeout caps the two Bridge methods that reach outside the process:
// Queue talks to the workspace's tracker adapter, Doctor shells out to the
// provider CLI. Everything else only touches the run directory.
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

// Runs lists a workspace's runs, newest first. An empty key lists all.
func (b *Bridge) Runs(ws, key string) ([]app.RunSummary, error) { return b.svc.Runs(ws, key) }

// Run returns one run's full detail.
func (b *Bridge) Run(ws, runId string) (app.RunDetail, error) { return b.svc.Run(ws, runId) }

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
