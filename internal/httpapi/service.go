package httpapi

import (
	"context"
	"errors"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

// Service is what the HTTP layer needs from the application service: the
// method set the design spec gives for *app.Service, which satisfies it.
// The interface is here so the handlers can be tested against a fake, and
// so the Wails shell and this one cannot drift apart silently.
type Service interface {
	Workspaces() ([]Workspace, error)
	AddWorkspace(root string) (Workspace, error)
	RemoveWorkspace(id string) error
	Queue(ctx context.Context, wsID string, f QueueFilter) ([]Ticket, error)
	Runs(wsID, key string) ([]RunSummary, error)
	Run(wsID, runID string) (RunDetail, error)
	Events(wsID, runID string, after int) ([]RunEvent, int, error)
	Note(wsID, runID string, kind string) (string, error)
	Prompt(wsID, runID string) (string, error)
	StartTriage(ctx context.Context, wsID string, keys []string, o TriageOptions) (JobID, error)
	StartRCA(ctx context.Context, wsID, key string, o RCAOptions) (JobID, error)
	Resume(ctx context.Context, wsID, runID, answer string) (JobID, error)
	Cancel(jobID JobID) error
	Register(wsID string) ([]RegisterRow, error)
	Doctor(ctx context.Context, wsID string) ([]Check, error)
	Quota() []Quota
	Subscribe() (<-chan Event, func())
}

// The real service is the one this package is written for: if a signature
// there changes, this line fails the build rather than the wiring.
var _ Service = (*app.Service)(nil)

// The two error classes the HTTP layer answers with something other than
// 500, aliased from internal/app so a handler test can raise them.
var (
	// ErrUnsupported means the workspace cannot answer this at all — no
	// tracker configured, say. It becomes 501.
	ErrUnsupported = app.ErrUnsupported
	// ErrNotFound stands for every id nobody knows. internal/app reports a
	// separate sentinel per kind of id; they all become 404.
	ErrNotFound = app.ErrNoSuchWorkspace
)

// classify maps a Service error onto an HTTP status and an error code.
// Only an id nobody knows is a 404: a file error from somewhere deeper —
// an adapter binary that is not installed, say — is this server's problem
// to report as one, not the client's to read as a missing resource.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, ErrUnsupported):
		return 501, "unsupported"
	case errors.Is(err, app.ErrNoSuchWorkspace),
		errors.Is(err, app.ErrNoSuchRun),
		errors.Is(err, app.ErrNoSuchJob):
		return 404, "not_found"
	default:
		return 500, "internal"
	}
}
