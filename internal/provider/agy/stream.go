package agy

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// streamLine is the union of every stdout line shape `agy` emits under
// --output-format stream-json. Shapes are taken from
// docs/research/10-antigravity-wire-formats.md, where each one is quoted
// off a live run.
//
// Unlike Claude Code's stream-json, the discriminator is "event" and the
// payload sits in a field named after it, so the three payloads are
// separate structs rather than one flat line.
type streamLine struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id"`

	Init       *initPayload `json:"init"`
	StepUpdate *stepUpdate  `json:"step_update"`
	Result     *resultLine  `json:"result"`
}

// initPayload is the first line of every process.
type initPayload struct {
	Model          string          `json:"model"`
	Cwd            string          `json:"cwd"`
	Tools          []string        `json:"tools"`
	PermissionMode string          `json:"permission_mode"`
	JSONSchema     json.RawMessage `json:"json_schema"`
}

// stepUpdate is one step's state transition. A step goes ACTIVE then DONE,
// or ACTIVE then ERROR; text arrives as deltas on the ACTIVE lines and the
// last fragment on the DONE one.
type stepUpdate struct {
	ConversationID string      `json:"conversation_id"`
	StepIndex      int         `json:"step_index"`
	State          string      `json:"state"`
	StepType       string      `json:"step_type"`
	TextDelta      string      `json:"text_delta"`
	Duration       float64     `json:"duration_seconds"`
	ToolName       string      `json:"tool_name"`
	ToolInfo       *toolInfo   `json:"tool_info"`
	Usage          *tokenUsage `json:"usage"`
}

// toolInfo carries the tool call's own arguments, and the error when the
// step ended in ERROR. Argument keys are Cascade's PascalCase
// (TargetFile, CommandLine), not the snake_case the other adapters see.
type toolInfo struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
	Error      *toolError      `json:"error"`
}

type toolError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// resultLine ends a turn. There is one per turn, not one per process:
// driving two stdin messages produces two of these on the same
// conversation, and NumTurns and Usage are cumulative across them.
//
// There is no cost field of any kind on this wire — see
// docs/research/10-antigravity-wire-formats.md — so CostUSD is never set
// and budget.maxUsd cannot bite on this provider.
type resultLine struct {
	ConversationID   string          `json:"conversation_id"`
	Status           string          `json:"status"`
	Response         string          `json:"response"`
	Error            string          `json:"error"`
	Duration         float64         `json:"duration_seconds"`
	NumTurns         int             `json:"num_turns"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Usage            tokenUsage      `json:"usage"`
	DeniedActions    []deniedAction  `json:"denied_actions"`
}

// deniedAction names a permission the turn asked for and did not get.
// Headless `agy` cannot prompt, so anything needing approval is
// auto-denied and reported here; "command" and "write_file" were both
// observed.
type deniedAction struct {
	Action      string `json:"action"`
	DisplayName string `json:"display_name"`
}

// tokenUsage is the CLI's usage object. Input tokens arrive split between
// the billed prompt and the cached read, and reporting only the first
// understates a cached turn badly, so Input adds them.
type tokenUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ThinkingTokens  int64 `json:"thinking_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

// Input is every token the request was billed for on the way in, cached or
// not.
func (u tokenUsage) Input() int64 { return u.InputTokens + u.CacheReadTokens }

// Output counts the thinking tokens as output, because they are generated
// and billed as such and reporting them nowhere would make a reasoning
// model look free.
func (u tokenUsage) Output() int64 { return u.OutputTokens + u.ThinkingTokens }

// stateError is the step state a refused or failed tool call ends in.
const stateError = "ERROR"

// decode turns one stdout line into zero or more events. A line that is
// not valid JSON, or that carries no "event", becomes a single EvError so
// the runner can count malformed output.
func decode(raw []byte) []provider.Event {
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil || l.Event == "" {
		ev := newEvent(provider.EvError, raw)
		ev.Text = "malformed line"
		return []provider.Event{ev}
	}

	switch l.Event {
	case "init":
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = "init"
		if l.Init != nil {
			ev.Text = "init: model " + l.Init.Model + ", permission mode " + l.Init.PermissionMode
		}
		return []provider.Event{ev}
	case "step_update":
		if l.StepUpdate == nil {
			return []provider.Event{malformed(raw, "step_update with no payload")}
		}
		return stepEvents(*l.StepUpdate, raw)
	case "result":
		if l.Result == nil {
			return []provider.Event{malformed(raw, "result with no payload")}
		}
		return resultEvents(*l.Result, raw)
	default:
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = l.Event
		return []provider.Event{ev}
	}
}

func malformed(raw []byte, text string) provider.Event {
	ev := newEvent(provider.EvError, raw)
	ev.Text = text
	return ev
}

// stepEvents turns one step_update into the events it stands for.
//
// Only the terminal line of a step (DONE or ERROR) carries usage, so that
// is where the usage event comes from; the ACTIVE lines carry text deltas
// and the tool's arguments.
func stepEvents(s stepUpdate, raw []byte) []provider.Event {
	var events []provider.Event
	if u := s.Usage; u != nil && (u.Input() > 0 || u.Output() > 0) {
		ev := newEvent(provider.EvUsage, raw)
		ev.InputTok = u.Input()
		ev.OutputTok = u.Output()
		events = append(events, ev)
	}

	switch s.StepType {
	case "tool":
		events = append(events, toolEvents(s, raw)...)
	case "agent_response":
		// text_delta is a fragment, not the whole step's text, so each
		// one is its own assistant-text event and the runner's
		// transcript concatenates them the way it does for every other
		// streaming provider.
		if s.TextDelta != "" {
			ev := newEvent(provider.EvAssistantText, raw)
			ev.Text = s.TextDelta
			events = append(events, ev)
		}
	default:
		if s.State == stateError {
			ev := newEvent(provider.EvError, raw)
			ev.Text = s.StepType + " step failed"
			events = append(events, ev)
		}
	}
	if len(events) == 0 {
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = s.StepType + " " + strings.ToLower(s.State)
		events = append(events, ev)
	}
	return events
}

// toolEvents renders a tool step. The ACTIVE line is the call starting,
// the DONE line is it finishing, and the ERROR line is it being refused or
// failing — and a refusal is the only permission signal this CLI ever
// emits, since there is no request to answer (see
// docs/research/10-antigravity-wire-formats.md, "Permissions: there is no
// mediation channel").
func toolEvents(s stepUpdate, raw []byte) []provider.Event {
	name := s.ToolName
	var args json.RawMessage
	var toolErr *toolError
	if s.ToolInfo != nil {
		if name == "" {
			name = s.ToolInfo.Name
		}
		args = s.ToolInfo.Parameters
		toolErr = s.ToolInfo.Error
	}

	switch s.State {
	case stateError:
		message := ""
		if toolErr != nil {
			message = toolErr.Message
		}
		if isDenial(message) {
			// Reported as a permission event with a deny decision, so a
			// run's event log reads the same way it does for a provider
			// Sirdar actually mediates. Sirdar did not make this
			// decision; the CLI did, because headless mode cannot
			// prompt. Nothing here can turn it into an allow.
			ev := newEvent(provider.EvPermission, raw)
			ev.Tool = name
			ev.Input = args
			ev.Decision = "deny"
			ev.Text = message
			return []provider.Event{ev}
		}
		ev := newEvent(provider.EvToolFinished, raw)
		ev.Tool = name
		ev.Input = args
		ev.Text = message
		return []provider.Event{ev}
	case "ACTIVE":
		ev := newEvent(provider.EvToolStarted, raw)
		ev.Tool = name
		ev.Input = args
		return []provider.Event{ev}
	default:
		ev := newEvent(provider.EvToolFinished, raw)
		ev.Tool = name
		ev.Input = args
		return []provider.Event{ev}
	}
}

// denialMarkers are the phrases the CLI's own refusal carries. Both halves
// were captured live: "permission check failed for command ..." when the
// headless run could not prompt, and "user denied permission for
// write_file(...)" in plan mode.
var denialMarkers = []string{
	"permission check failed",
	"denied permission",
	"auto-denied",
}

func isDenial(message string) bool {
	m := strings.ToLower(message)
	for _, marker := range denialMarkers {
		if strings.Contains(m, marker) {
			return true
		}
	}
	return false
}

// resultEvents turns a result line into the usage and final events. The
// usage figures on it are the conversation's own totals, not the turn's,
// so the session replaces its running count with them rather than adding.
func resultEvents(r resultLine, raw []byte) []provider.Event {
	usage := newEvent(provider.EvUsage, raw)
	usage.Turns = r.NumTurns
	usage.InputTok = r.Usage.Input()
	usage.OutputTok = r.Usage.Output()

	events := []provider.Event{usage}
	for _, d := range r.DeniedActions {
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = "agy refused the " + d.Action + " permission this turn: headless runs cannot prompt, so it was auto-denied"
		ev.Tool = d.DisplayName
		events = append(events, ev)
	}

	if r.Status == "ERROR" {
		ev := newEvent(provider.EvError, raw)
		ev.Text = r.Error
		if ev.Text == "" {
			ev.Text = "agy reported an error with no message"
		}
		return append(events, ev)
	}

	final := newEvent(provider.EvFinal, raw)
	final.Turns = r.NumTurns
	final.InputTok = r.Usage.Input()
	final.OutputTok = r.Usage.Output()
	final.Text = r.Response
	if len(r.StructuredOutput) > 0 && string(r.StructuredOutput) != "null" {
		final.Final = r.StructuredOutput
	}
	return append(events, final)
}

// isResultLine reports whether raw is a turn's terminal result line, the
// one carrying the conversation's own totals.
func isResultLine(raw []byte) bool {
	var probe struct {
		Event string `json:"event"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.Event == "result"
}

// stepIndex is the step a line belongs to, and whether it names one at
// all. A step's ACTIVE and DONE lines share it, which is what lets the
// session pair a tool call's arguments with its completion.
func stepIndex(raw []byte) (int, bool) {
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil || l.StepUpdate == nil {
		return 0, false
	}
	return l.StepUpdate.StepIndex, true
}

// conversationID pulls the resume handle off any line that carries one.
// init names it at the top level; step_update and result repeat it inside
// their payloads.
func conversationID(raw []byte) string {
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return ""
	}
	switch {
	case l.ConversationID != "":
		return l.ConversationID
	case l.StepUpdate != nil && l.StepUpdate.ConversationID != "":
		return l.StepUpdate.ConversationID
	case l.Result != nil && l.Result.ConversationID != "":
		return l.Result.ConversationID
	}
	return ""
}

func newEvent(kind provider.EventKind, raw []byte) provider.Event {
	return provider.Event{Kind: kind, At: time.Now(), Raw: json.RawMessage(raw)}
}
