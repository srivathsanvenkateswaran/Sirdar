package qwen

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// streamLine is the union of every stdout line shape Qwen Code emits in
// stream-json mode. Shapes are taken from
// docs/research/09-qwen-wire-formats.md.
type streamLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`

	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Usage   *tokenUsage     `json:"usage"`
	} `json:"message"`

	// result line
	Result           string          `json:"result"`
	StructuredResult json.RawMessage `json:"structured_result"`
	NumTurns         int             `json:"num_turns"`
	IsError          bool            `json:"is_error"`
	Usage            tokenUsage      `json:"usage"`
	Error            struct {
		Message string `json:"message"`
	} `json:"error"`
}

// tokenUsage is Qwen Code's usage object. Unlike Claude Code's, the cache
// counter is not a separate bucket: input_tokens is the whole prompt and
// cache_read_input_tokens is the part of it that was served from cache,
// which the wire-format capture confirmed by feeding the CLI a stub that
// reported prompt_tokens 1200 with cached_tokens 800 (Qwen emitted
// input_tokens 1200, cache_read_input_tokens 800, total_tokens 1240).
// Adding them the way the Claude adapter has to would count a cached turn
// twice.
type tokenUsage struct {
	InputTokens          int64 `json:"input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
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

// decode turns one stdout line into zero or more events. A line that is
// not valid JSON, or that carries no "type", becomes a single EvError so
// the runner can count malformed output.
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
	case "result":
		usage := newEvent(provider.EvUsage, raw)
		usage.Turns = l.NumTurns
		usage.InputTok = l.Usage.InputTokens
		usage.OutputTok = l.Usage.OutputTokens

		final := newEvent(provider.EvFinal, raw)
		final.Turns = l.NumTurns
		final.InputTok = l.Usage.InputTokens
		final.OutputTok = l.Usage.OutputTokens
		final.Text = l.Result
		if len(l.StructuredResult) > 0 && string(l.StructuredResult) != "null" {
			final.Final = l.StructuredResult
		}
		if l.IsError {
			// A failed result still carries the session's totals, so it
			// is reported as the final event; the reason goes with it.
			final.Text = resultError(l)
		}
		return []provider.Event{usage, final}
	default:
		// system/init, stream_event, and anything new.
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = l.Subtype
		if ev.Text == "" {
			ev.Text = l.Type
		}
		return []provider.Event{ev}
	}
}

// resultError renders the reason a result line reports a failure. Qwen
// Code puts a sentence under "error" for the cases it can name — a loop
// detector trip, a budget abort — and nothing at all when the model simply
// answered in prose instead of calling structured_output, which is the
// case the subtype has to speak for.
func resultError(l streamLine) string {
	if msg := strings.TrimSpace(l.Error.Message); msg != "" {
		return msg
	}
	if l.Subtype != "" {
		return l.Subtype
	}
	return "the session ended in an error"
}

// isResultLine reports whether raw is the CLI's terminal "result" line,
// the one that carries the session's own totals.
func isResultLine(raw []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(raw, &probe) == nil && probe.Type == "result"
}

// isRoundTrip reports whether an assistant line completes one model
// round-trip, which is the unit the result line counts in num_turns.
//
// Qwen Code emits one stdout line per content block, so a single model
// response arrives as a text line and then a tool_use line, and counting
// every assistant line runs ahead of num_turns the same way it did for
// Claude Code. The distinguishing mark here is simpler than Claude's: the
// CLI hangs the API response's usage on the last line of the turn and
// leaves every earlier line at zero, so a line carrying tokens is one
// round-trip.
func isRoundTrip(raw []byte) bool {
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil || l.Type != "assistant" {
		return false
	}
	u := l.Message.Usage
	return u != nil && (u.InputTokens > 0 || u.OutputTokens > 0)
}

func assistantEvents(l streamLine, raw []byte) []provider.Event {
	var events []provider.Event
	if u := l.Message.Usage; u != nil && (u.InputTokens > 0 || u.OutputTokens > 0) {
		// One turn's tokens. The session accumulates these into running
		// totals, so state.json shows spend as it happens rather than
		// zeros until the result line lands.
		ev := newEvent(provider.EvUsage, raw)
		ev.InputTok = u.InputTokens
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

// blocksOf parses a message's content array; a plain string content (as in
// an echoed user prompt) has no blocks.
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

// blockText renders a tool_result's content, which is either a JSON string
// or a structured array of blocks.
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

// policyNames maps Qwen Code's runtime tool ids onto the names
// provider.PermissionPolicy judges, which are Claude Code's. Only the
// tools whose Sirdar-side verdict is already settled are listed: the read
// tools that AlwaysAllowed covers, the write tools AlwaysDenied covers,
// and the shell, whose command the policy inspects.
//
// A name that is not in the table is passed to Decide as it is. For an
// MCP tool (mcp__server__tool) that is exactly right — the policy parses
// that form itself. For everything else — Qwen's own cron_create,
// send_message, enter_worktree, record_artifact and the rest — the policy
// answers "not permitted", which is the safe default for a tool nobody
// has judged.
var policyNames = map[string]string{
	"run_shell_command":   "Bash",
	"read_file":           "Read",
	"read_many_files":     "Read",
	"read_mcp_resource":   "Read",
	"grep_search":         "Grep",
	"search_file_content": "Grep",
	"glob":                "Glob",
	"list_directory":      "LS",
	"web_fetch":           "WebFetch",
	"web_search":          "WebSearch",
	"structured_output":   "StructuredOutput",
	"todo_write":          "TodoWrite",
	"tool_search":         "Task",
	"agent":               "Task",
	"task":                "Task",
	"skill":               "Task",
	"write_file":          "Write",
	"edit":                "Edit",
	"replace":             "Edit",
	"notebook_edit":       "NotebookEdit",
}

// policyName returns the name to judge a Qwen tool call under.
func policyName(tool string) string {
	if name, ok := policyNames[tool]; ok {
		return name
	}
	return tool
}

func newEvent(kind provider.EventKind, raw []byte) provider.Event {
	return provider.Event{Kind: kind, At: time.Now(), Raw: json.RawMessage(raw)}
}
