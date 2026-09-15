package cursor

import "testing"

// TestExtractJSON covers the answer shapes a prompt-carried schema
// actually comes back in. There is no --json-schema flag and no
// structured_output field, so this function is the whole of Sirdar's
// structured-output handling on this provider, and what it refuses is what
// makes the runner's schema retry fire.
func TestExtractJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"bare object", `{"a":1}`, `{"a":1}`},
		{"json fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"bare fence", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"prose around it", "Here you go:\n{\"a\":1}\nHope that helps.", `{"a":1}`},
		{"nested braces", `{"a":{"b":2},"c":3}`, `{"a":{"b":2},"c":3}`},
		{"brace inside a string", `{"a":"}"}`, `{"a":"}"}`},
		{"escaped quote before a brace", `{"a":"x\"}","b":1}`, `{"a":"x\"}","b":1}`},
		{"prose only", "I could not determine the root cause.", ""},
		{"unbalanced", `{"a":1`, ""},
		{"invalid json", `{"a": undefined}`, ""},
		{"empty", "", ""},

		// A model answering a prompt-carried schema restates the schema,
		// or works an example, and then answers. The answer is the last
		// thing it says, so the last candidate is the one taken.
		{
			"two fences, the answer last",
			"Here is the schema:\n```json\n{\"type\":\"object\"}\n```\nAnd my answer:\n```json\n{\"summary\":\"x\"}\n```",
			`{"summary":"x"}`,
		},
		{
			"two bare objects, the answer last",
			"Example: {\"summary\":\"like this\"}\nActual: {\"summary\":\"the real one\"}",
			`{"summary":"the real one"}`,
		},
		{
			"a fence beats prose that follows it",
			"```json\n{\"summary\":\"x\"}\n```\nLet me know if {\"this\":\"helps\"}.",
			`{"summary":"x"}`,
		},

		// The schema's own header quoted back is not an answer. Refusing
		// it is what makes the runner's schema retry fire instead of a
		// note being filed over an empty document.
		{"schema echo", `{"$schema":"https://json-schema.org/draft/2020-12/schema","title":"TriageNote"}`, ""},
		{"schema echo, $schema alone", `{"$schema":"https://json-schema.org/draft/2020-12/schema"}`, ""},
		{
			"schema echo then the answer",
			"```json\n{\"$schema\":\"x\",\"title\":\"TriageNote\"}\n```\n```json\n{\"summary\":\"x\"}\n```",
			`{"summary":"x"}`,
		},
		// Not an echo: a real answer may carry a title of its own, and
		// only the degenerate all-header object is refused.
		{"title with real fields", `{"title":"TriageNote","summary":"x"}`, `{"title":"TriageNote","summary":"x"}`},
		// Nothing but the echo leaves the runner with no document, which
		// is the retry, not a filed note.
		{"schema echo only, in a fence", "```json\n{\"title\":\"TriageNote\"}\n```", ""},
		// An invalid last candidate falls back to the valid earlier one
		// rather than throwing the answer away.
		{"invalid last, valid first", "{\"summary\":\"x\"}\nthen {\"a\": undefined}", `{"summary":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(extractJSON(tc.in))
			if got != tc.want {
				t.Errorf("extractJSON(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestToolOfNamesTheToolByItsKey pins the one thing that identifies a
// Cursor tool call: the key of the tool_call object, since there is no
// name field.
func TestToolOfNamesTheToolByItsKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"editToolCall":{"args":{}}}`, "Edit"},
		{`{"shellToolCall":{"args":{}}}`, "Shell"},
		{`{"webFetchToolCall":{"args":{}}}`, "WebFetch"},
		{`{"mcpToolCall":{"args":{}}}`, "Mcp"},
		{`{"toolCallId":"x","startedAtMs":"1"}`, "tool"},
		{`not json`, "tool"},
	} {
		if got, _, _ := toolOf([]byte(tc.in)); got != tc.want {
			t.Errorf("toolOf(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
