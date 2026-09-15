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
		if got, _ := toolOf([]byte(tc.in)); got != tc.want {
			t.Errorf("toolOf(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
