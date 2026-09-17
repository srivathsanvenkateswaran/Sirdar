// Package provider defines the contract between Sirdar's triage loop and a
// coding-agent CLI (Claude Code, Codex, etc.), plus the permission policy
// that decides which tools an agent session may use.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
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

	// RunDir is this run's own directory (.sirdar/runs/<id>), where a
	// provider may keep whatever it needs to resume the session later.
	// Sirdar's own loop writes its message transcript there, which is the
	// only resume handle it can have: unlike a CLI session there is no
	// server-side thread to name. Empty means the caller keeps no run
	// directory — an eval, a test — and a provider that would have
	// written one does not, and reports no handle.
	RunDir string

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

	// EvModelLimit says the login has no room left for one model in
	// particular, while the rest of it still works. It is not a rate
	// limit: nothing resets at a time the provider will name, and every
	// other model on the same account is still answering, so parking the
	// pool until a reset would wait for something that is not coming.
	//
	// It is not an answer either, which is the whole reason it exists.
	// Claude Code reports the refusal as an ordinary assistant message —
	// "You've reached your Fable limit. Switch to another model…" — and
	// a reader that takes that for prose sees a turn that ended without a
	// document and asks the same model again, three times, before failing
	// the run. Naming it here is what lets the run layer hold the session
	// instead, with a handle to continue from once a model is chosen.
	//
	// Model is the model family the provider named, as it named it
	// ("Fable"), and Text is the sentence it said. Only the claude
	// adapter reports it today: Codex's rate-limit notification is about
	// the account's window rather than one model, and ACP carries no
	// equivalent at all, so both stay as they were.
	EvModelLimit EventKind = "model_limit"
	EvFinal         EventKind = "final"
	EvSystem        EventKind = "system" // init, status, anything informational
	EvError         EventKind = "error"

	// EvBreach says a read-only session did something a read-only session
	// cannot do: it finished a write or ran a command. It is separate from
	// EvError because the two mean opposite things to the run layer. An
	// error is a line the run survives — the malformed-line counter exists
	// precisely so that a few of them do not end a run — whereas a breach
	// is the guarantee the run was started under failing, which no amount
	// of surviving makes better. A run that sees one ends failed and files
	// nothing.
	//
	// Only a provider that cannot mediate a tool call before it runs has
	// any use for this: provider agy, where the CLI decides permissions
	// out of files Sirdar does not own and the first Sirdar hears of a
	// write is the line saying it finished. Everywhere else a write is
	// refused at the permission callback and never happens.
	//
	// Text is the operator-facing reason, whose first line is the run's
	// terminal reason and reads "read-only breach: <tool> <path|command>".
	EvBreach EventKind = "breach"

	// EvBlind says the session produced an answer without having read
	// anything: every read tool it called was refused, or it called none
	// at all.
	//
	// A triage note is a claim about a codebase, and the run layer files
	// it, adds a register row and prints it in the digest at whatever
	// confidence the agent asserted. A session that read nothing wrote
	// that claim out of the ticket text alone, and nothing downstream can
	// tell the difference: the note looks exactly like one built on
	// evidence. So the run fails instead of filing, with a reason naming
	// what was refused.
	//
	// Like EvBreach this exists for the provider whose permissions are
	// decided by files Sirdar does not own, where a refused read is
	// reported after the fact rather than negotiated. On a provider Sirdar
	// mediates, a read is allowed by its own policy or the run never had
	// the permission to begin with.
	//
	// Text is the operator-facing reason, whose first line becomes the
	// run's terminal reason.
	EvBlind EventKind = "blind"
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

	// Model is the model id the provider says is answering, carried by the
	// EvSystem event its init line becomes. A workspace that configured no
	// model, or configured an alias the CLI resolves, has no other way of
	// learning which model a run actually used: SessionSpec.Model is what
	// was asked for, and this is the answer. Empty on every other event,
	// and on an agent that reports none.
	Model string

	// Delta and Replace say how an EvAssistantText joins the text around
	// it. A provider that streams a message token by token emits each
	// fragment with Delta set, and the reader grows one block by
	// concatenating them; the whole block, when it arrives, is emitted
	// with Replace set and stands in for every fragment that preceded it.
	//
	// Both are false on a provider that reports a message once, which is
	// every provider but Claude Code: those events concatenate the way
	// they always have, one paragraph per event.
	//
	// The pair exists because Claude Code says the same words twice. With
	// --include-partial-messages on — Sirdar always passes it — the CLI
	// streams `stream_event` text deltas while the model writes, and then
	// repeats the finished text in the turn's `assistant` line. Appending
	// both would print the message twice, and dropping either would lose
	// the live growth or the authoritative text. So the deltas grow the
	// block and the final line replaces it.
	Delta   bool
	Replace bool

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

// Level is how serious a Doctor diagnostic is.
//
// LevelWarn is the middle state the bool alone could not express: the
// check found something the operator should know about — every user-level
// MCP server visible to the agent, a Codex session that will see no MCP
// servers at all, a custom Anthropic base URL that makes budget.maxUsd
// meaningless — but nothing that stops a run. Only LevelFail exits
// non-zero.
type Level string

const (
	LevelOK   Level = "ok"
	LevelWarn Level = "warn"
	LevelFail Level = "fail"
)

// Check is one Doctor diagnostic result.
//
// OK stays the field every caller already reads, and it means "this is not
// a failure": a warning has OK true. Level carries the third state; an
// empty Level is read off OK, so a provider that has not been taught about
// warnings still reports correctly.
type Check struct {
	Name   string
	OK     bool
	Level  Level
	Detail string
}

// Severity is the check's level, derived from OK when the check set none.
func (c Check) Severity() Level {
	switch {
	case c.Level != "":
		return c.Level
	case c.OK:
		return LevelOK
	default:
		return LevelFail
	}
}

// Warn builds a warning check: something worth reporting that is not a
// failure, so it never changes an exit code.
func Warn(name, detail string) Check {
	return Check{Name: name, OK: true, Level: LevelWarn, Detail: detail}
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

// FixSupport is the optional half of the Provider contract for a provider
// that cannot run `sirdar fix` at all.
//
// Start refusing a fix spec is too late to be the only refusal: by the
// time the runner starts a session, `sirdar fix` has already fetched the
// default branch, cut a branch from it and checked that branch out into a
// linked worktree. A provider that was never going to run leaves all of
// that behind for the operator to clean up. SupportsFix is asked before
// any of it, and FixRefusal is the same error Start would have returned,
// so the early refusal and the late one read alike.
type FixSupport interface {
	// SupportsFix reports whether a fix session can run on this provider.
	SupportsFix() bool
	// FixRefusal is why not, in the words the operator should read.
	FixRefusal() error
}

// RefuseFix returns the reason p cannot run a fix session, or nil when it
// can. A provider that says nothing about fix support can run one: that is
// every provider Sirdar can mediate a tool call for.
func RefuseFix(p Provider) error {
	fs, ok := p.(FixSupport)
	if !ok || fs.SupportsFix() {
		return nil
	}
	if err := fs.FixRefusal(); err != nil {
		return err
	}
	return fmt.Errorf("provider %s: `sirdar fix` is not supported", p.Name())
}
