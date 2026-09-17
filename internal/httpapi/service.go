package httpapi

import (
	"context"
	"encoding/json"
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
	ResolveHelpdesk(ctx context.Context, wsID, number string) (HelpdeskLink, error)
	ComposeIntent(ctx context.Context, wsID, text string) (ComposedIntent, error)
	Runs(wsID, key string) ([]RunSummary, error)
	Run(wsID, runID string) (RunDetail, error)
	DeleteRun(wsID, runID string) error
	Search(wsID, q string) ([]SearchHit, error)
	Events(wsID, runID string, after int) ([]RunEvent, int, error)
	Note(wsID, runID string, kind string) (string, error)
	Prompt(wsID, runID string) (string, error)
	RunDiff(wsID, runID string) (RunDiff, error)
	DropHunk(ctx context.Context, wsID, runID, path string, hunk int, etag string) (RunDiff, error)
	StartTriage(ctx context.Context, wsID string, keys []string, o TriageOptions) (JobID, error)
	TriageIfIdle(ctx context.Context, wsID, key string, o TriageOptions) (JobID, string, error)
	HookReceived(source, key, outcome string)
	StartRCA(ctx context.Context, wsID, key string, o RCAOptions) (JobID, error)
	StartFix(ctx context.Context, wsID, key string, o FixOptions) (JobID, error)
	StartEval(ctx context.Context, wsID string, keys []string, o EvalOptions) (JobID, error)
	EvalReports(wsID string) ([]EvalReport, error)
	LatestRetro(wsID string) (*RetroReport, error)
	Golden(wsID string) ([]GoldenEntry, error)
	AddGolden(wsID, key, runID string) (GoldenEntry, error)
	ConfigSummary(wsID string) (ConfigSummary, error)
	MCPServers(ctx context.Context, wsID string, connect bool) (MCPInventory, error)
	MCPTools(ctx context.Context, wsID, server string) (MCPToolList, error)
	MCPCall(ctx context.Context, wsID, server, tool string, args json.RawMessage) (MCPCallResult, error)
	Resume(ctx context.Context, wsID, runID, answer string) (JobID, error)
	Steer(ctx context.Context, wsID, runID, text string) (JobID, error)
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
	// ErrMCPDenied is a tool the workspace's own permissions refuse. It
	// becomes 403, with the reason in the body the handler already has.
	ErrMCPDenied = app.ErrMCPDenied
	// ErrNoSuchMCPServer is a server name the workspace does not
	// configure. It becomes 404.
	ErrNoSuchMCPServer = app.ErrNoSuchMCPServer
	// ErrNoDiff is a run with no change to review. It becomes 404 with the
	// reason, which is what the screen shows instead of a diff.
	ErrNoDiff = app.ErrNoDiff
	// ErrRefused is a change that exists and must not be edited right now.
	// It becomes 409: the caller can read the diff again and try again.
	ErrRefused = app.ErrRefused
	// ErrRunLive is a run whose runner is still writing, asked to be
	// deleted. It becomes 409: cancel the job, or wait, and ask again.
	ErrRunLive = app.ErrRunLive
)

// classify maps a Service error onto an HTTP status and an error code.
// Only an id nobody knows is a 404: a file error from somewhere deeper —
// an adapter binary that is not installed, say — is this server's problem
// to report as one, not the client's to read as a missing resource.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, ErrUnsupported):
		return 501, "unsupported"
	case errors.Is(err, app.ErrSteerRefused):
		// The run's own state refuses the steer — it is live, or over a
		// budget. Nothing is wrong with the server or the id; the caller
		// can wait, or cannot have this at all, and 409 says which
		// through the message.
		return 409, "conflict"
	case errors.Is(err, app.ErrMCPDenied):
		return 403, "forbidden"
	case errors.Is(err, ErrRefused), errors.Is(err, ErrRunLive):
		return 409, "conflict"
	case errors.Is(err, ErrNoDiff):
		return 404, "no_diff"
	case errors.Is(err, app.ErrNoSuchWorkspace),
		errors.Is(err, app.ErrNoSuchRun),
		errors.Is(err, app.ErrNoSuchJob),
		errors.Is(err, app.ErrNoSuchMCPServer):
		return 404, "not_found"
	default:
		return 500, "internal"
	}
}
