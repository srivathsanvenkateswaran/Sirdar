package cursor

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// streamLine is the union of every stdout line shape `cursor-agent -p
// --output-format stream-json` emits. Shapes are taken from
// docs/research/11-cursor-wire-formats.md.
type streamLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`

	// init
	APIKeySource   string `json:"apiKeySource"`
	Model          string `json:"model"`
	PermissionMode string `json:"permissionMode"`
	Cwd            string `json:"cwd"`

	// assistant / user
	Message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	// Under --stream-partial-output an assistant line per token arrives in
	// the same shape as the complete one; the only difference is that a
	// partial carries timestamp_ms and no model_call_id. Sirdar never
	// passes the flag, so partials should not arrive — both fields are
	// read so that a session started with it by hand is not counted twice
	// over. See isRoundTrip.
	TimestampMS int64  `json:"timestamp_ms"`
	ModelCallID string `json:"model_call_id"`

	// tool_call
	CallID   string          `json:"call_id"`
	ToolCall json.RawMessage `json:"tool_call"`

	// result
	Result    string     `json:"result"`
	IsError   bool       `json:"is_error"`
	RequestID string     `json:"request_id"`
	Usage     tokenUsage `json:"usage"`
}

// tokenUsage is the result line's usage object. Input tokens arrive in
// three counters and reporting only the uncached one understates a cached
// session badly — a one-word answer billed 13 112 fresh input tokens
// against 7 808 cached ones — so Input adds them up.
type tokenUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

// Input is every token the request was billed for on the way in, cached or
// not.
func (u tokenUsage) Input() int64 {
	return u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// contentBlock is one entry of a message's content array.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// decode turns one stdout line into zero or more events. A line that is not
// valid JSON, or that carries no "type", becomes a single EvError so the
// runner can count malformed output.
func decode(raw []byte) []provider.Event {
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil || l.Type == "" {
		ev := newEvent(provider.EvError, raw)
		ev.Text = "malformed line"
		return []provider.Event{ev}
	}

	switch l.Type {
	case "assistant":
		text := messageText(l.Message.Content)
		if strings.TrimSpace(text) == "" {
			return nil
		}
		ev := newEvent(provider.EvAssistantText, raw)
		ev.Text = text
		return []provider.Event{ev}

	case "user":
		// The echoed prompt. It is kept as a system line so the raw event
		// log is complete, but it is not assistant output.
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = "prompt"
		return []provider.Event{ev}

	case "thinking":
		// Reasoning arrives one delta per token whether or not
		// --stream-partial-output was passed. It is not the answer and
		// putting it on the event stream would bury everything else.
		return nil

	case "tool_call":
		return toolCallEvents(l, raw)

	case "result":
		return resultEvents(l, raw)

	case "system":
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = initNote(l)
		return []provider.Event{ev}

	default:
		ev := newEvent(provider.EvSystem, raw)
		ev.Text = l.Subtype
		if ev.Text == "" {
			ev.Text = l.Type
		}
		return []provider.Event{ev}
	}
}

// initNote renders the init line as the operator would want to read it:
// which model actually answered, and which credential paid for it.
func initNote(l streamLine) string {
	if l.Subtype != "init" {
		if l.Subtype != "" {
			return l.Subtype
		}
		return "system"
	}
	parts := []string{"init"}
	if l.Model != "" {
		parts = append(parts, "model "+l.Model)
	}
	if l.APIKeySource != "" {
		parts = append(parts, "auth "+l.APIKeySource)
	}
	if l.PermissionMode != "" {
		parts = append(parts, "permissions "+l.PermissionMode)
	}
	return strings.Join(parts, ", ")
}

// resultEvents turns the terminal line into the usage event and the final
// answer. The CLI reports no cost and no turn count, so CostUSD stays zero
// and the turn total is whatever the session counted for itself.
func resultEvents(l streamLine, raw []byte) []provider.Event {
	usage := newEvent(provider.EvUsage, raw)
	usage.InputTok = l.Usage.Input()
	usage.OutputTok = l.Usage.OutputTokens

	final := newEvent(provider.EvFinal, raw)
	final.InputTok = l.Usage.Input()
	final.OutputTok = l.Usage.OutputTokens
	final.Text = l.Result
	if doc := extractJSON(l.Result); len(doc) > 0 {
		final.Final = doc
	}

	events := []provider.Event{usage, final}
	if l.IsError {
		ev := newEvent(provider.EvError, raw)
		ev.Text = "session ended with is_error: " + firstNonEmpty(l.Subtype, "error")
		events = append(events, ev)
	}
	return events
}

// toolCallEvents renders one tool_call line. The tool's identity is the
// single key of the tool_call object — "editToolCall", "shellToolCall" —
// rather than a name field, so the key is what names the event.
func toolCallEvents(l streamLine, raw []byte) []provider.Event {
	name, body := toolOf(l.ToolCall)
	switch l.Subtype {
	case "started":
		ev := newEvent(provider.EvToolStarted, raw)
		ev.Tool = name
		ev.Input = body.Args
		return []provider.Event{ev}
	case "completed":
		ev := newEvent(provider.EvToolFinished, raw)
		ev.Tool = name
		ev.Input = body.Result
		ev.Text = resultSummary(body.Result)
		return []provider.Event{ev}
	default:
		ev := newEvent(provider.EvSystem, raw)
		ev.Tool = name
		ev.Text = "tool_call " + l.Subtype
		return []provider.Event{ev}
	}
}

// toolBody is the part of a tool_call's payload the adapter reads.
type toolBody struct {
	Args   json.RawMessage `json:"args"`
	Result json.RawMessage `json:"result"`
}

// toolOf finds the one "<name>ToolCall" key in a tool_call object and
// returns a display name for it plus its body. An object carrying no such
// key — a shape a later CLI adds — yields the name "tool" rather than an
// error, because a tool Sirdar cannot name is still a tool it must report.
func toolOf(rawCall json.RawMessage) (string, toolBody) {
	if len(rawCall) == 0 {
		return "tool", toolBody{}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawCall, &fields); err != nil {
		return "tool", toolBody{}
	}
	for key, value := range fields {
		if !strings.HasSuffix(key, "ToolCall") {
			continue
		}
		var body toolBody
		_ = json.Unmarshal(value, &body)
		return displayName(strings.TrimSuffix(key, "ToolCall")), body
	}
	return "tool", toolBody{}
}

// displayName turns "edit" into "Edit" and "webFetch" into "WebFetch", so a
// tool reads the way the other providers' tools do in a run's event log.
func displayName(name string) string {
	if name == "" {
		return "tool"
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// resultSummary says how a finished tool call ended. A refused call is the
// one outcome worth a sentence: the reason the CLI reports is the reason
// the model was given, so it is what an operator reading the log needs.
func resultSummary(rawResult json.RawMessage) string {
	if len(rawResult) == 0 {
		return ""
	}
	var outcome struct {
		Rejected *struct {
			Reason  string `json:"reason"`
			Command string `json:"command"`
			Path    string `json:"path"`
		} `json:"rejected"`
		Error *struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		} `json:"error"`
		Success json.RawMessage `json:"success"`
	}
	if err := json.Unmarshal(rawResult, &outcome); err != nil {
		return ""
	}
	switch {
	case outcome.Rejected != nil:
		reason := strings.TrimSpace(firstLine([]byte(outcome.Rejected.Reason)))
		if reason == "" {
			reason = "refused"
		}
		return "rejected: " + reason
	case outcome.Error != nil:
		return "error: " + firstNonEmpty(outcome.Error.Message, outcome.Error.Error)
	case len(outcome.Success) > 0:
		return "ok"
	default:
		return ""
	}
}

// messageText renders a message's content array. A plain string content is
// returned as it is.
func messageText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	var out strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			out.WriteString(b.Text)
		}
	}
	return out.String()
}

// extractJSON pulls the structured answer out of the result line's text.
//
// The CLI has no --json-schema flag and no structured-output field: the
// schema goes in the prompt and the answer comes back as prose-or-JSON. So
// the answer is read leniently — a ```json fence is stripped, and the
// outermost balanced {...} is taken out of whatever surrounds it — and a
// text that carries no JSON object yields nothing, which is what makes the
// runner's schema retry fire.
func extractJSON(text string) json.RawMessage {
	s := strings.TrimSpace(stripFence(text))
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return nil
	}
	end := matchingBrace(s, start)
	if end < 0 {
		return nil
	}
	candidate := s[start : end+1]
	if !json.Valid([]byte(candidate)) {
		return nil
	}
	return json.RawMessage(candidate)
}

// stripFence removes a leading ```/```json fence and its closing partner.
func stripFence(text string) string {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	} else {
		return s
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// matchingBrace returns the index of the '}' closing the '{' at start,
// skipping braces inside string literals, or -1.
func matchingBrace(s string, start int) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// isRoundTrip reports whether a line completes one model round-trip, which
// is what the turn budget counts.
//
// There is no num_turns on the wire — the result line carries tokens and a
// duration and nothing else — so the count is Sirdar's own: an assistant
// line carrying a non-empty answer is one round-trip, and so is the first
// tool_call of a model_call_id that has not been seen. Counting every
// tool_call would run several times ahead of the model's turns on a
// response that calls three tools at once.
func isRoundTrip(raw []byte, seen map[string]bool) bool {
	var l streamLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return false
	}
	switch l.Type {
	case "assistant":
		// A partial line repeats text the complete line carries too, and
		// the only thing telling them apart is that a partial has
		// timestamp_ms and no model_call_id — the complete line has
		// either both or neither. Sirdar never asks for partials, so this
		// is a safety net for a session started with the flag by hand.
		if l.TimestampMS > 0 && l.ModelCallID == "" {
			return false
		}
		return strings.TrimSpace(messageText(l.Message.Content)) != ""
	case "tool_call":
		if l.Subtype != "started" || l.ModelCallID == "" || seen[l.ModelCallID] {
			return false
		}
		seen[l.ModelCallID] = true
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func newEvent(kind provider.EventKind, raw []byte) provider.Event {
	return provider.Event{Kind: kind, At: time.Now(), Raw: json.RawMessage(raw)}
}
