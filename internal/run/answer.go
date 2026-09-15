package run

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// answerDoc picks the JSON document a final event is offering, and returns
// whatever prose came instead when it is offering none.
//
// A provider that produced structured output hands it over in Final and
// there is nothing to decide. The interesting case is the one where Final
// is empty and Text is not, which is two different things wearing the same
// shape:
//
//   - the agent wrote the answer as plain text, because nothing on the
//     wire made it call the structured-output tool. The document is in
//     there, often behind a code fence or a sentence of preamble, and it
//     is the run's answer.
//   - the CLI is narrating its own failure. Qwen Code 0.23.3 fails a
//     --json-schema run whose model answered in prose and puts an English
//     sentence under error.message; the adapter keeps that sentence as the
//     final event's text, because it is the only account of what happened.
//
// Handing the second to note.Validate is what ended the first live rca run
// with `parse document: invalid character 'M' looking for beginning of
// value` — the 'M' of "Model produced plain text instead of calling the
// structured_output tool" — and what made the schema retry quote that back
// at the agent as though it were the agent's mistake. So the text is a
// candidate answer only when it actually carries a JSON object; otherwise
// it is narration, the run has no answer, and the narration is what the
// operator should be told.
//
// This is deliberately at the run layer rather than in one adapter: it
// applies to every provider, and to fix and rca runs as much as to triage.
// A provider-side recovery (qwen's recoverFinal) can only reach the text
// blocks that session saw, and it fills Final when it finds a document
// there; this is the same judgement made once more where the answer is
// finally read, and it is the one that keeps a CLI's error sentence out of
// the validator whatever the provider did.
func answerDoc(ev provider.Event) (doc []byte, narration string) {
	if len(bytes.TrimSpace(ev.Final)) > 0 {
		return []byte(ev.Final), ""
	}
	text := strings.TrimSpace(ev.Text)
	if text == "" {
		return nil, ""
	}
	if obj := jsonObject(text); len(obj) > 0 {
		return obj, ""
	}
	return nil, text
}

// jsonObject returns the last balanced top-level JSON object in text, or
// nil when there is none.
//
// The last rather than the first: an agent that quotes an example object
// and then answers is answering with the second, and a leading sentence or
// a code fence before the real document is common enough that throwing the
// whole answer away over it would be the wrong trade. Braces and quotes
// inside string literals are skipped, because a note's own fields carry
// both.
//
// Only an object counts. Every note schema is an object, so a bare string
// or array here could never validate.
func jsonObject(text string) json.RawMessage {
	var (
		depth int
		start = -1
		inStr bool
		esc   bool
		best  string
	)
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				if candidate := text[start : i+1]; json.Valid([]byte(candidate)) {
					best = candidate
				}
				start = -1
			}
		}
	}
	if best == "" {
		return nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(best)); err != nil {
		return json.RawMessage(best)
	}
	return json.RawMessage(compact.Bytes())
}
