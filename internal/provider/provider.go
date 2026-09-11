// Package provider defines the contract between Sirdar's triage loop and a
// coding-agent CLI (Claude Code, Codex, etc.), plus the permission policy
// that decides which tools an agent session may use.
package provider

import (
	"context"
	"encoding/json"
	"time"
)

// Budget caps a session's turns, wall-clock time, and spend. A provider
// enforces these locally where it can and reports usage via Event/Result
// so the caller can stop a session that runs over.
type Budget struct {
	MaxTurns   int
	MaxMinutes int
	MaxUSD     float64
}

// Mode says what kind of run a session is. A triage or rca session is
// read-only: it reads the workspace and writes nothing but its own JSON
// answer. A fix session implements the note's proposed fix, so it needs the
// editing tools a read-only session is refused. Providers read it to decide
// which tool set and which sandbox to start with; the zero value is
// ModeTriage, so a caller that never sets it gets the read-only session it
// used to get.
type Mode string

const (
	ModeTriage Mode = "triage"
	ModeFix    Mode = "fix"
)

// IsFix reports whether m is the write-enabled fix mode.
func (m Mode) IsFix() bool { return m == ModeFix }

// SessionSpec configures a single agent session.
type SessionSpec struct {
	Cwd          string
	Prompt       string
	Model        string
	OutputSchema []byte
	Policy       *PermissionPolicy
	Budget       Budget
	Resume       string
	Images       []string
	Env          []string // full child env; provider may strip keys
	Binary       string   // path override; "" = look up on PATH

	// Mode selects the read-only triage session or the write-enabled fix
	// session. Empty means ModeTriage.
	Mode Mode

	// MCPConfig is the path to the only MCP server file the session may
	// load. Empty means there is no such file.
	MCPConfig string

	// MCPStrict says the session must see the servers in MCPConfig and no
	// others. With MCPStrict set and MCPConfig empty the session gets no
	// MCP servers at all, which is what mcp.workspaceOnly asks for in a
	// workspace that has written no .mcp.json. With MCPStrict false the
	// session inherits whatever the operator has configured globally.
	MCPStrict bool
}

// EventKind identifies the kind of a Session event.
type EventKind string

const (
	EvAssistantText EventKind = "assistant_text"
	EvToolStarted   EventKind = "tool_started"
	EvToolFinished  EventKind = "tool_finished"
	EvPermission    EventKind = "permission"
	EvUsage         EventKind = "usage"
	EvRateLimited   EventKind = "rate_limited"
	EvQuestion      EventKind = "question"
	EvFinal         EventKind = "final"
	EvSystem        EventKind = "system" // init, status, anything informational
	EvError         EventKind = "error"
)

// Event is one line of a Session's activity stream.
type Event struct {
	Kind     EventKind
	At       time.Time
	Text     string          // assistant text / question / error message
	Tool     string          // tool name for tool_* and permission
	Input    json.RawMessage // tool input
	Decision string          // "allow" | "deny" for permission

	Turns               int
	InputTok, OutputTok int64
	CostUSD             float64

	ResetsAt time.Time // rate limit

	Final json.RawMessage // structured output, nil if absent
	Raw   json.RawMessage // original provider line, always set
}

// Result is a session's terminal outcome, available after Wait returns.
type Result struct {
	Final json.RawMessage
	Text  string // final text when no structured output
	Usage struct {
		Turns               int
		InputTok, OutputTok int64
		CostUSD             float64
	}
	Handle  string
	ExitErr error // non-nil if the process failed

	// StderrTail is the last lines the agent process wrote to stderr. It
	// is the only account of why a session died badly, so it is recorded
	// whether or not the session failed.
	StderrTail []string
}

// Session represents one running (or completed) agent process.
type Session interface {
	// Events returns the channel of activity events. It closes when the
	// session ends, whether that is success, failure, or cancellation.
	Events() <-chan Event

	// Send delivers a follow-up user message to a running session. It is
	// used for the schema-retry turn, when the agent's structured output
	// failed validation and needs another attempt.
	Send(ctx context.Context, userText string) error

	// CloseInput tells the session no further user message is coming. A
	// CLI reading stream-json on stdin keeps its own stdout open waiting
	// for the next one, so a session that has produced its answer only
	// ends once its input is closed. It is safe to call more than once,
	// and a provider that has no input to close does nothing.
	CloseInput() error

	// Wait blocks until the underlying process exits and returns its
	// final Result.
	Wait() (Result, error)

	// Handle returns the provider's resume token for this session, so a
	// later SessionSpec.Resume can continue it.
	Handle() string

	Cancel()
}

// Check is one Doctor diagnostic result.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// DoctorConfig carries the parts of the workspace configuration a
// diagnostic needs in order to report on what a run would actually do.
// Doctor is handed a binary and nothing else, which left the Codex MCP row
// guessing: the workspace from the process working directory, the setting
// from an environment variable. That is accurate for `sirdar doctor` run
// inside a workspace and wrong from the desktop app, whose working
// directory has nothing to do with the workspace being reported on.
type DoctorConfig struct {
	// Root is the workspace directory, where .mcp.json is looked for.
	Root string
	// MCPWorkspaceOnly is the workspace's mcp.workspaceOnly setting.
	MCPWorkspaceOnly bool
}

// ConfigDoctor is the optional half of the Doctor contract, for a provider
// whose diagnostics depend on the workspace and not on the binary alone. A
// caller holding the configuration should prefer it; one that does not
// calls Doctor, and the provider falls back to what it can infer.
type ConfigDoctor interface {
	DoctorWithConfig(ctx context.Context, binary string, cfg DoctorConfig) []Check
}

// Provider adapts a specific agent CLI to the Session contract.
type Provider interface {
	Name() string
	Start(ctx context.Context, spec SessionSpec) (Session, error)
	Doctor(ctx context.Context, binary string) []Check
}
