package claude

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// statusAllowed is the rate_limit_info.status the CLI reports while the
// window still has room; every other status refuses work.
const statusAllowed = "allowed"

// streamLine is the union of every stdout line shape Claude Code emits in
// stream-json mode. Shapes are taken from docs/research/06-wire-formats.md.
type streamLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`

	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	RateLimitInfo struct {
		Status        string `json:"status"`
		ResetsAt      int64  `json:"resetsAt"`
		RateLimitType string `json:"rateLimitType"`
	} `json:"rate_limit_info"`

	// result line
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	NumTurns         int             `json:"num_turns"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	Usage            struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
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
		if info.Status == statusAllowed {
			ev := newEvent(provider.EvSystem, raw)
			ev.Text = strings.TrimSpace("rate limit " + info.Status + " " + info.RateLimitType)
			return []provider.Event{ev}
		}
		ev := newEvent(provider.EvRateLimited, raw)
		ev.Text = info.RateLimitType
		if info.ResetsAt > 0 {
			ev.ResetsAt = time.Unix(info.ResetsAt, 0)
		}
		return []provider.Event{ev}
	case "result":
		usage := newEvent(provider.EvUsage, raw)
		usage.Turns = l.NumTurns
		usage.InputTok = l.Usage.InputTokens
		usage.OutputTok = l.Usage.OutputTokens
		usage.CostUSD = l.TotalCostUSD

		final := newEvent(provider.EvFinal, raw)
		final.Turns = l.NumTurns
		final.InputTok = l.Usage.InputTokens
		final.OutputTok = l.Usage.OutputTokens
		final.CostUSD = l.TotalCostUSD
		final.Text = l.Result
		if len(l.StructuredOutput) > 0 && string(l.StructuredOutput) != "null" {
			final.Final = l.StructuredOutput
		}
		return []provider.Event{usage, final}
	default:
		// system/* (init, status, hooks), stream_event and anything new.
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = l.Subtype
		if ev.Text == "" {
			ev.Text = l.Type
		}
		return []provider.Event{ev}
	}
}

func assistantEvents(l streamLine, raw []byte) []provider.Event {
	var events []provider.Event
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
			ev := newEvent(provider.EvAssistantText, raw)
			ev.Text = b.Text
			events = append(events, ev)
		}
	}
	return events
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
