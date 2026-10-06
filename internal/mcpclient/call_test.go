package mcpclient

import (
	"context"
	"encoding/json"
	"testing"
)

// TestCallOnce drives the connect, call and close that both `sirdar mcp
// call` and a gather step go through, against the real fake server.
func TestCallOnce(t *testing.T) {
	root := writeMCP(t, t.TempDir(), `{"mcpServers":{
		"fake":   {"command":`+jsonString(fakeServer(t))+`},
		"broken": {"env":{"A":"b"}}
	}}`)
	entries, _, err := Inventory(root, nil, false)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	byName := map[string]Entry{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	text, isErr, err := CallOnce(context.Background(), byName["fake"], "list_rows", json.RawMessage(`{"table":"t"}`))
	if err != nil {
		t.Fatalf("CallOnce: %v", err)
	}
	if isErr {
		t.Fatal("CallOnce: isErr is true for a tool that answered")
	}
	if text != "rows of t" {
		t.Fatalf("CallOnce = %q, want %q", text, "rows of t")
	}

	if _, _, err := CallOnce(context.Background(), byName["broken"], "list_rows", json.RawMessage(`{}`)); err == nil {
		t.Fatal("CallOnce on an unusable entry: want an error, got nil")
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
