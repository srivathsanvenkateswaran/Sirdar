package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// ComposedIntent is what one ambiguous composer line was read as: the
// ticket the session should run on, what kind of session it should be, and
// what the person actually asked for, with the model's own confidence in
// the reading.
//
// It is a suggestion and nothing more. The composer draws it as the same
// chips its own parser draws and waits for a second Enter before starting
// anything, because a model that has misread which of two keys was meant
// would otherwise start a session on the wrong ticket.
type ComposedIntent struct {
	Key         string  `json:"key"`
	Mode        string  `json:"mode"`
	Instruction string  `json:"instruction"`
	Confidence  float64 `json:"confidence"`
}

// composeIntentSchema is the answer's shape, handed over as structured
// output so the reading does not have to be scraped out of prose.
var composeIntentSchema = []byte(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["key", "mode", "instruction", "confidence"],
  "properties": {
    "key": {"type": "string", "description": "The tracker key the person meant, upper-case, or \"\" when the text names none."},
    "mode": {"type": "string", "enum": ["triage", "rca", "fix", ""], "description": "The kind of session asked for, or \"\" when the text does not say."},
    "instruction": {"type": "string", "description": "What the person asked for, in their own words, with the key and the mode word taken out."},
    "confidence": {"type": "number", "description": "0 to 1: how sure you are of the key and the mode."}
  }
}`)

// composeIntentModes are the three a session can be, and the only values a
// reading may carry besides the empty one.
var composeIntentModes = map[string]bool{"triage": true, "rca": true, "fix": true}

// ComposeIntentMax is the most text this call will read. The composer's box
// is a prompt, so a person can paste a whole ticket into it; a thousand
// characters is more than enough to tell which key and which mode were
// meant, and it keeps one keystroke's fallback from costing what a run
// costs.
const ComposeIntentMax = 2000

// composeIntentGrace is how long a session that has answered is given to
// exit on its own before it is cancelled.
const composeIntentGrace = 10 * time.Second

// ComposeIntentPrompt is the question the model is asked. It is one turn
// with no tools: everything it needs is in the text it is given, and a
// model that goes reading the repository to settle a composer line has
// already cost more than the reading is worth.
func ComposeIntentPrompt(text string) string {
	var b strings.Builder
	b.WriteString("You are reading one line a support engineer typed into a box that starts a session ")
	b.WriteString("on a support ticket. Work out what they meant. Answer only with the JSON object the schema describes.\n\n")
	b.WriteString("- key: the tracker key they meant, like OMNI-2510 — upper-case letters or digits, a dash, digits. ")
	b.WriteString("When the line names two, pick the one the sentence is about. When it names none, answer \"\".\n")
	b.WriteString("- mode: \"triage\" to read the ticket and write a triage note, \"rca\" for a root-cause note, ")
	b.WriteString("\"fix\" to make the change on a branch. Answer \"\" when the line does not say which.\n")
	b.WriteString("- instruction: what they asked for, in their own words, with the key and the mode word taken out. ")
	b.WriteString("Do not invent, summarise or translate it; \"\" when they asked for nothing in particular.\n")
	b.WriteString("- confidence: 0 to 1, how sure you are of the key and the mode together.\n\n")
	b.WriteString("Do not run any tool. Read the line and answer.\n\n")
	b.WriteString("## The line\n\n")
	b.WriteString(fenceBlock(text))
	return b.String()
}

// fenceBlock puts the person's own text in a fence, so a line that itself
// contains a heading or a list cannot be read as part of the question.
func fenceBlock(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + "\n" + text + "\n" + fence + "\n"
}

// ComposeIntent reads one ambiguous composer line with a single short
// provider call and answers what it was understood as.
//
// It starts no run, writes nothing, and reads nothing but the text it was
// given: one session, no MCP servers, no tools worth using, a two-turn
// ceiling. The composer calls it only when its own parser cannot settle the
// line — text with no key in it, two keys, two mode words — and never on an
// empty box or on a line the parser read outright, which is what keeps a
// keystroke from being a provider call.
func (s *Service) ComposeIntent(ctx context.Context, wsID, text string) (ComposedIntent, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return ComposedIntent{}, fmt.Errorf("%w: there is nothing to read", ErrInvalidArgument)
	}
	if len(text) > ComposeIntentMax {
		text = text[:ComposeIntentMax]
	}
	_, cfg, err := s.load(wsID)
	if err != nil {
		return ComposedIntent{}, err
	}
	deps, cleanup, err := s.build(cfg, "", "", s.stderr())
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return ComposedIntent{}, err
	}
	if deps.Provider == nil {
		return ComposedIntent{}, ErrUnsupported
	}

	sess, err := deps.Provider.Start(ctx, provider.SessionSpec{
		Cwd:          cfg.Root,
		Prompt:       ComposeIntentPrompt(text),
		Model:        cfg.Model,
		OutputSchema: composeIntentSchema,
		Policy:       &provider.PermissionPolicy{Root: cfg.Root, Mode: provider.ModeTriage},
		Mode:         provider.ModeTriage,
		// One answer, and a ceiling low enough that a model that starts
		// reading the repository stops long before it costs anything.
		Budget: provider.Budget{MaxTurns: 2, MaxMinutes: 2},
		// No MCP servers: everything the question needs is in it.
		MCPStrict: true,
	})
	if err != nil {
		return ComposedIntent{}, fmt.Errorf("compose: start the reading session: %w", err)
	}
	// Nothing further is coming, and a CLI reading stream-json holds its
	// stdout open until it is told so.
	_ = sess.CloseInput()
	defer time.AfterFunc(composeIntentGrace, sess.Cancel).Stop()

	var final json.RawMessage
	for ev := range sess.Events() {
		if ev.Kind == provider.EvFinal && ev.Final != nil {
			final = ev.Final
		}
	}
	res, waitErr := sess.Wait()
	if final == nil {
		final = res.Final
	}
	if final == nil && strings.TrimSpace(res.Text) != "" {
		final = json.RawMessage(objectIn(res.Text))
	}
	if final == nil {
		if waitErr != nil {
			return ComposedIntent{}, fmt.Errorf("compose: the reading gave no answer: %w", waitErr)
		}
		return ComposedIntent{}, fmt.Errorf("compose: the reading gave no answer")
	}
	return composedIntentFromJSON(final)
}

// composedIntentFromJSON parses and bounds a reading: an unknown mode
// becomes none rather than a word the composer cannot draw, a key is
// upper-cased the way the store files one, and the confidence is held to
// the range it claims to be in.
func composedIntentFromJSON(data []byte) (ComposedIntent, error) {
	var out ComposedIntent
	if err := json.Unmarshal(data, &out); err != nil {
		return ComposedIntent{}, fmt.Errorf("compose: the reading is not valid JSON: %w", err)
	}
	out.Key = strings.ToUpper(strings.TrimSpace(out.Key))
	out.Mode = strings.ToLower(strings.TrimSpace(out.Mode))
	if !composeIntentModes[out.Mode] {
		out.Mode = ""
	}
	out.Instruction = strings.TrimSpace(out.Instruction)
	switch {
	case out.Confidence < 0:
		out.Confidence = 0
	case out.Confidence > 1:
		out.Confidence = 1
	}
	return out, nil
}

// objectIn pulls the object out of a final message that came back as prose
// with a fenced block in it, which is what a provider without structured
// output returns.
func objectIn(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return text
	}
	return text[start : end+1]
}
