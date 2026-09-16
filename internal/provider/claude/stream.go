package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// statusAllowed is the prefix of every rate_limit_info.status the CLI
// reports while the window still has room: "allowed" outright, and
// "allowed_warning" once utilization is high enough to mention. Neither
// refuses work, so neither is a rate limit.
const statusAllowed = "allowed"

// streamLine is the union of every stdout line shape Claude Code emits in
// stream-json mode. Shapes are taken from docs/research/06-wire-formats.md.
type streamLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`

	// Model is on the system/init line, and is the model that actually
	// answers — the dated id an alias like `sonnet` resolved to, or the
	// CLI's own default when the run configured none.
	Model string `json:"model"`

	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Usage   *tokenUsage     `json:"usage"`
	} `json:"message"`

	RateLimitInfo struct {
		Status         string  `json:"status"`
		ResetsAt       int64   `json:"resetsAt"`
		RateLimitType  string  `json:"rateLimitType"`
		Utilization    float64 `json:"utilization"`
		IsUsingOverage bool    `json:"isUsingOverage"`
	} `json:"rate_limit_info"`

	// Event is the partial-message envelope on a `stream_event` line,
	// which --include-partial-messages turns on. Only its text deltas are
	// read; everything else it carries — the thinking deltas, the
	// tool-input deltas, the block and message boundaries — stays an
	// informational line.
	Event struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`

	// result line
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	NumTurns         int             `json:"num_turns"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	Usage            tokenUsage      `json:"usage"`
}

// tokenUsage is the CLI's usage object. Input tokens arrive in three
// separate counters and reporting only the uncached one understates a
// cached session by three orders of magnitude, so Input adds them up.
type tokenUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

// Input is every token the request was billed for on the way in, cached or
// not.
func (u tokenUsage) Input() int64 {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// contentBlock is one entry of an assistant or user message's content array.
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// controlRequest is a `control_request` line; only can_use_tool is acted on.
type controlRequest struct {
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype   string          `json:"subtype"`
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	} `json:"request"`
}

// decode turns one stdout line into zero or more events. A line that is not
// valid JSON, or that carries no "type", becomes a single EvError so the
// runner can count malformed output. control_request lines are handled by the
// session (they need a reply) and reach decode only as a fallback.
func decode(raw []byte) []provider.Event {
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil || l.Type == "" {
		ev := newEvent(provider.EvError, raw)
		ev.Text = "malformed line"
		return []provider.Event{ev}
	}

	switch l.Type {
	case "assistant":
		return assistantEvents(l, raw)
	case "user":
		return userEvents(l, raw)
	case "rate_limit_event":
		// The CLI reports the window's state on every turn, and status
		// "allowed" means nothing is being limited. Only a status that
		// refuses work is a rate limit; treating the routine line as one
		// parks the whole pool until resetsAt, hours away.
		info := l.RateLimitInfo
		if strings.HasPrefix(info.Status, statusAllowed) {
			ev := newEvent(provider.EvSystem, raw)
			ev.Text = rateLimitNote(info.RateLimitType, info.Utilization, info.IsUsingOverage)
			return []provider.Event{ev}
		}
		ev := newEvent(provider.EvRateLimited, raw)
		ev.Text = info.RateLimitType
		if info.ResetsAt > 0 {
			ev.ResetsAt = time.Unix(info.ResetsAt, 0)
		}
		return []provider.Event{ev}
	case "stream_event":
		return streamEvents(l, raw)
	case "result":
		usage := newEvent(provider.EvUsage, raw)
		usage.Turns = l.NumTurns
		usage.InputTok = l.Usage.Input()
		usage.OutputTok = l.Usage.OutputTokens
		usage.CostUSD = l.TotalCostUSD

		final := newEvent(provider.EvFinal, raw)
		final.Turns = l.NumTurns
		final.InputTok = l.Usage.Input()
		final.OutputTok = l.Usage.OutputTokens
		final.CostUSD = l.TotalCostUSD
		final.Text = l.Result
		if len(l.StructuredOutput) > 0 && string(l.StructuredOutput) != "null" {
			final.Final = l.StructuredOutput
		}
		return []provider.Event{usage, final}
	default:
		// system/* (init, status, hooks) and anything new.
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = l.Subtype
		if ev.Text == "" {
			ev.Text = l.Type
		}
		if l.Type == "system" && l.Subtype == "init" {
			ev.Model = l.Model
		}
		return []provider.Event{ev}
	}
}

// rateLimitNote renders an informational rate_limit_event as the operator
// would want to read it on a run that costs real money: which window, and
// how much of it is gone. It never says "blocked", because nothing was.
func rateLimitNote(window string, utilization float64, overage bool) string {
	note := "rate limit"
	if window != "" {
		note += " " + window
	}
	if utilization > 0 {
		note += fmt.Sprintf(" at %.0f%% of the window", utilization*100)
	}
	if overage {
		note += ", using overage"
	}
	return note
}

// isRoundTrip reports whether an assistant line completes one model
// round-trip, which is the unit the CLI counts in the result line's
// num_turns.
//
// Claude Code emits one stdout line per content block, so a single model
// response arrives as a thinking line, then a tool_use line, and a
// response that calls three tools in parallel arrives as three tool_use
// lines. Counting every assistant line ran about 1.5x ahead of num_turns
// — 61 against the CLI's 41 on a real run — and cancelled a session
// mid-tool on a budget it had not spent. A line carrying a tool_use block
// or a non-empty text answer is one round-trip; a thinking-only line is
// part of the round-trip that follows it.
func isRoundTrip(raw []byte) bool {
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil || l.Type != "assistant" {
		return false
	}
	for _, b := range blocksOf(l.Message.Content) {
		switch b.Type {
		case "tool_use":
			return true
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				return true
			}
		}
	}
	return false
}

func assistantEvents(l streamLine, raw []byte) []provider.Event {
	var events []provider.Event
	if u := l.Message.Usage; u != nil && (u.Input() > 0 || u.OutputTokens > 0) {
		// One turn's tokens. The session accumulates these into running
		// totals, so state.json shows spend as it happens rather than
		// zeros until the result line lands.
		ev := newEvent(provider.EvUsage, raw)
		ev.InputTok = u.Input()
		ev.OutputTok = u.OutputTokens
		events = append(events, ev)
	}
	for _, b := range blocksOf(l.Message.Content) {
		switch b.Type {
		case "tool_use":
			ev := newEvent(provider.EvToolStarted, raw)
			ev.Tool = b.Name
			ev.Input = b.Input
			events = append(events, ev)
		case "text":
			if b.Text == "" {
				continue
			}
			// The finished block, which stands in for the deltas that
			// streamed it rather than following them: see
			// provider.Event.Replace. Sirdar always runs the CLI with
			// --include-partial-messages, so every word here has already
			// been seen once; a reader that ignores Replace and appends
			// would print the message twice.
			ev := newEvent(provider.EvAssistantText, raw)
			ev.Text = b.Text
			ev.Replace = true
			events = append(events, ev)
		}
	}
	return events
}

// streamEvents reads a `stream_event` line, the partial-message envelope
// --include-partial-messages turns on.
//
// A text delta is the model's prose arriving as it is written, and it
// becomes an EvAssistantText carrying that fragment. It is not also kept as
// an informational line: the same words reach the transcript twice already
// (once per delta, once in the turn's `assistant` line), and a third copy
// under a kind nothing renders is noise — on a real run these lines are
// most of events.jsonl.
//
// Every other stream event stays informational, tool-input deltas included:
// those are the only account of what a tool call was being handed while it
// was being written, and nothing else republishes them.
func streamEvents(l streamLine, raw []byte) []provider.Event {
	if l.Event.Type == "content_block_delta" && l.Event.Delta.Type == "text_delta" {
		if l.Event.Delta.Text == "" {
			return nil
		}
		ev := newEvent(provider.EvAssistantText, raw)
		ev.Text = l.Event.Delta.Text
		ev.Delta = true
		return []provider.Event{ev}
	}
	ev := newEvent(provider.EvSystem, raw)
	ev.Text = l.Type
	if l.Subtype != "" {
		ev.Text = l.Subtype
	}
	return []provider.Event{ev}
}

func userEvents(l streamLine, raw []byte) []provider.Event {
	var events []provider.Event
	for _, b := range blocksOf(l.Message.Content) {
		if b.Type != "tool_result" {
			continue
		}
		ev := newEvent(provider.EvToolFinished, raw)
		ev.Text = blockText(b.Content)
		ev.Input = b.Content
		events = append(events, ev)
	}
	return events
}

// blocksOf parses a message's content array; a plain string content (as in an
// echoed user prompt) has no blocks.
func blocksOf(content json.RawMessage) []contentBlock {
	if len(content) == 0 {
		return nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	return blocks
}

// blockText renders a tool_result's content, which is either a JSON string or
// a structured array of blocks.
func blockText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err == nil {
		var out string
		for _, b := range blocks {
			if b.Type == "text" {
				out += b.Text
			}
		}
		if out != "" {
			return out
		}
	}
	return string(content)
}

func newEvent(kind provider.EventKind, raw []byte) provider.Event {
	return provider.Event{Kind: kind, At: time.Now(), Raw: json.RawMessage(raw)}
}
