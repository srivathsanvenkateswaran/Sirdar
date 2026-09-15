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
		if l.Subtype == "init" {
			// What the CLI says answered, which on a session started
			// without a model is the account's own choice — "Auto", or
			// whichever id the picker last landed on.
			ev.Model = l.Model
		}
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
//
// A completed line for an excluded tool is also where a read-only breach
// is noticed; see breachOf.
func toolCallEvents(l streamLine, raw []byte) []provider.Event {
	name, key, body := toolOf(l.ToolCall)
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
		events := []provider.Event{ev}
		if breach := breachOf(name, key, body, raw); breach != nil {
			events = append(events, *breach)
		}
		return events
	default:
		ev := newEvent(provider.EvSystem, raw)
		ev.Tool = name
		ev.Text = "tool_call " + l.Subtype
		return []provider.Event{ev}
	}
}

// excludedToolCalls is the set of tool-call names a read-only session
// passed to --exclude-tools, keyed the way the wire spells them once
// normalised. It is derived from writeTools rather than restated, so the
// tools the session refuses and the tools whose completion is a breach
// cannot drift apart.
var excludedToolCalls = func() map[string]bool {
	m := make(map[string]bool, len(writeTools))
	for _, name := range writeTools {
		m[name] = true
	}
	return m
}()

// breachOf is this adapter's substitute for a permission policy, and the
// counterpart of what the agy adapter does with a completed write.
//
// Sirdar cannot judge a Cursor tool call: print mode is its own approver,
// there is no control channel to answer, and the first Sirdar hears of an
// edit or a shell command is the line saying it finished. Every one of
// these tools was named on --exclude-tools and the session was started in
// a read-only --mode, so a completed one means both of those failed —
// which is the read-only guarantee the run was started under, gone. That
// is not something a run can note and carry on from, so it becomes an
// EvBreach: the run layer cancels the session, kills the process group,
// and fails the run without filing a note or a register row.
//
// A rejected result is the opposite outcome and the ordinary one: the tool
// call was refused, nothing happened, and the session goes on. An errored
// or successful one both mean the call reached the tool.
func breachOf(name, key string, body toolBody, raw []byte) *provider.Event {
	if !excludedToolCalls[toolCallName(key)] {
		return nil
	}
	outcome, ok := parseOutcome(body.Result)
	if ok && outcome.Rejected != nil {
		return nil
	}

	headline := "read-only breach: " + name
	if subject := subjectOf(body); subject != "" {
		headline += " " + oneLine(subject)
	}
	ev := newEvent(provider.EvBreach, raw)
	ev.Tool = name
	ev.Input = body.Result
	ev.Text = headline + "\na triage session completed " + name + ", which this session excluded: " +
		toolCallName(key) + " is passed to --exclude-tools on every read-only run and --mode " +
		"ask|plan is supposed to decline it as well. Sirdar cannot mediate a Cursor tool call — " +
		"`cursor-agent -p` approves its own — so this run is ended rather than filed. Check " +
		"whether the account's plan honours --exclude-tools, and whether ~/.cursor holds a rule " +
		"that widened the session; Sirdar can neither see nor override that file"
	return &ev
}

// toolCallName turns a wire key into the --exclude-tools spelling of the
// same tool: "editToolCall" becomes "edit_tool_call", "switchModeToolCall"
// becomes "switch_mode_tool_call". A key already written in snake case is
// left as it is, so both spellings of switch_mode land on one name.
func toolCallName(key string) string {
	base := strings.TrimSuffix(key, "ToolCall")
	if base == "" || base == key {
		return ""
	}
	var out strings.Builder
	for i, r := range base {
		if unicode.IsUpper(r) {
			if i > 0 && out.Len() > 0 && !strings.HasSuffix(out.String(), "_") {
				out.WriteByte('_')
			}
			out.WriteRune(unicode.ToLower(r))
			continue
		}
		out.WriteRune(r)
	}
	return out.String() + "_tool_call"
}

// subjectOf is what the tool call acted on, for the breach to name: the
// path an edit wrote or the command a shell ran. The arguments carry it on
// the started line and usually on the completed one too, but a completed
// line can elide them (the capture in
// docs/research/11-cursor-wire-formats.md shows a rejected shell call
// carrying only its result), so the result is read as well.
func subjectOf(body toolBody) string {
	if s := argsSubject(body.Args); s != "" {
		return s
	}
	outcome, ok := parseOutcome(body.Result)
	if !ok {
		return ""
	}
	if outcome.Rejected != nil {
		return firstNonEmpty(outcome.Rejected.Path, outcome.Rejected.Command)
	}
	return argsSubject(outcome.Success)
}

// argsSubject reads a path or a command out of a tool call's arguments.
func argsSubject(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var args struct {
		Path       string `json:"path"`
		TargetFile string `json:"targetFile"`
		Command    string `json:"command"`
		ToModeID   string `json:"toModeId"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	return firstNonEmpty(args.Path, args.TargetFile, args.Command, args.ToModeID)
}

// oneLine flattens a multi-line command into something a run's terminal
// reason can carry on one line.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 120
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// toolBody is the part of a tool_call's payload the adapter reads.
type toolBody struct {
	Args   json.RawMessage `json:"args"`
	Result json.RawMessage `json:"result"`
}

// toolOf finds the one "<name>ToolCall" key in a tool_call object and
// returns a display name for it, the key itself, and its body. An object
// carrying no such key — a shape a later CLI adds — yields the name "tool"
// rather than an error, because a tool Sirdar cannot name is still a tool
// it must report. The raw key comes back too because the breach check
// matches on the --exclude-tools spelling, not on the display name.
func toolOf(rawCall json.RawMessage) (string, string, toolBody) {
	if len(rawCall) == 0 {
		return "tool", "", toolBody{}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawCall, &fields); err != nil {
		return "tool", "", toolBody{}
	}
	for key, value := range fields {
		if !strings.HasSuffix(key, "ToolCall") {
			continue
		}
		var body toolBody
		_ = json.Unmarshal(value, &body)
		return displayName(strings.TrimSuffix(key, "ToolCall")), key, body
	}
	return "tool", "", toolBody{}
}

// displayName turns "edit" into "Edit" and "webFetch" into "WebFetch", so a
// tool reads the way the other providers' tools do in a run's event log.
// Snake-cased keys are folded the same way — "switch_mode" reads
// "SwitchMode" — so one tool does not appear under two names depending on
// how the CLI spelled it.
func displayName(name string) string {
	if name == "" {
		return "tool"
	}
	var out strings.Builder
	for _, part := range strings.Split(name, "_") {
		if part == "" {
			continue
		}
		r := []rune(part)
		r[0] = unicode.ToUpper(r[0])
		out.WriteString(string(r))
	}
	if out.Len() == 0 {
		return "tool"
	}
	return out.String()
}

// toolOutcome is how a finished tool call ended: refused before it ran,
// attempted and failed, or done.
type toolOutcome struct {
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

// parseOutcome reads a completed tool call's result. The second return is
// false when there is nothing to read, which the breach check treats as
// "not a refusal" rather than as "refused".
func parseOutcome(rawResult json.RawMessage) (toolOutcome, bool) {
	var outcome toolOutcome
	if len(rawResult) == 0 {
		return outcome, false
	}
	if err := json.Unmarshal(rawResult, &outcome); err != nil {
		return toolOutcome{}, false
	}
	return outcome, true
}

// resultSummary says how a finished tool call ended. A refused call is the
// one outcome worth a sentence: the reason the CLI reports is the reason
// the model was given, so it is what an operator reading the log needs.
func resultSummary(rawResult json.RawMessage) string {
	outcome, ok := parseOutcome(rawResult)
	if !ok {
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
// the answer is read leniently, and a text that carries no JSON object
// yields nothing, which is what makes the runner's schema retry fire.
//
// Which object, when a text carries several, is decided by what a model
// answering a prompt-carried schema actually does. It restates the schema,
// or shows a worked example, and then gives the answer — so the LAST
// candidate is the answer and the earlier ones are working. Fenced blocks
// are searched before bare text for the same reason: a model that fences
// anything fences its answer.
//
// A candidate whose only top-level keys are $schema and title is the
// schema's own header quoted back rather than an answer, and is refused so
// the retry fires instead of a run filing a note that says nothing.
func extractJSON(text string) json.RawMessage {
	for _, candidate := range jsonCandidates(text) {
		if !json.Valid(candidate) || isSchemaEcho(candidate) {
			continue
		}
		return json.RawMessage(candidate)
	}
	return nil
}

// jsonCandidates lists the objects a text offers, best first: every
// top-level object inside a fenced block, last fence first and last object
// within it first, and then every top-level object in the text as a whole,
// last first.
func jsonCandidates(text string) [][]byte {
	var out [][]byte
	blocks := fencedBlocks(text)
	for i := len(blocks) - 1; i >= 0; i-- {
		out = append(out, topLevelObjects(blocks[i])...)
	}
	return append(out, topLevelObjects(text)...)
}

// topLevelObjects returns the balanced {...} runs of s that are not nested
// inside another, last first.
func topLevelObjects(s string) [][]byte {
	var found [][]byte
	for i := 0; i < len(s); {
		start := strings.IndexByte(s[i:], '{')
		if start < 0 {
			break
		}
		start += i
		end := matchingBrace(s, start)
		if end < 0 {
			break
		}
		found = append(found, []byte(s[start:end+1]))
		i = end + 1
	}
	for l, r := 0, len(found)-1; l < r; l, r = l+1, r-1 {
		found[l], found[r] = found[r], found[l]
	}
	return found
}

// fencedBlocks returns the bodies of the ```…``` blocks in text, in the
// order they appear. An unterminated fence yields the rest of the text,
// because a truncated answer is still worth reading.
func fencedBlocks(text string) []string {
	var out []string
	rest := text
	for {
		open := strings.Index(rest, "```")
		if open < 0 {
			return out
		}
		rest = rest[open+3:]
		// The rest of the opening line is the info string ("json"), not
		// content.
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			return append(out, rest)
		}
		rest = rest[nl+1:]
		shut := strings.Index(rest, "```")
		if shut < 0 {
			return append(out, rest)
		}
		out = append(out, rest[:shut])
		rest = rest[shut+3:]
	}
}

// isSchemaEcho reports whether doc is the prompt's own JSON Schema header
// quoted back instead of an answer. Only the degenerate case is refused —
// an object whose entire top level is $schema and/or title — because
// anything richer may be a real answer whose fields happen to include one
// of those names.
func isSchemaEcho(doc []byte) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(doc, &fields); err != nil || len(fields) == 0 {
		return false
	}
	for key := range fields {
		if key != "$schema" && key != "title" {
			return false
		}
	}
	return true
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
