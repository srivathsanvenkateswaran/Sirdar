package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeServer is the stdio MCP server these tests drive, installed by
// TestMain. It used to be testdata/fakemcp.sh, which internal/app and
// cmd/sirdar pointed at by relative paths of their own; the one fixture is
// testbin.FakeMCP now, because a #!/bin/sh script is not an executable on
// Windows and every layer that started one failed there.
func fakeServer(t *testing.T) string {
	t.Helper()
	if fakeMCPBin == "" {
		t.Fatal("TestMain did not install the fake MCP server")
	}
	return fakeMCPBin
}

// writeMCP writes a .mcp.json into dir and returns the directory.
func writeMCP(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInventoryDescribesEveryTransportAndNamesNoValue(t *testing.T) {
	root := writeMCP(t, t.TempDir(), `{"mcpServers":{
		"db":      {"command":"/bin/sh","args":["-c","serve --token=$DB_TOKEN"],"env":{"DB_TOKEN":"$DB_TOKEN","DB_HOST":"db.example"}},
		"remote":  {"type":"http","url":"https://mcp.example/v1","headers":{"Authorization":"Bearer $REMOTE_TOKEN"}},
		"legacy":  {"type":"sse","url":"https://mcp.example/sse"},
		"broken":  {"env":{"A":"b"}},
		"bad__name": {"command":"/bin/true"}
	}}`)

	env := []string{"DB_TOKEN=s3cr3t-db", "REMOTE_TOKEN=s3cr3t-remote"}
	entries, warnings, err := Inventory(root, env, false)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	byName := map[string]Entry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if len(entries) != 5 {
		t.Fatalf("got %d entries, want 5: %+v", len(entries), entries)
	}

	db := byName["db"]
	switch {
	case db.Transport != TransportStdio:
		t.Errorf("db transport = %q, want stdio", db.Transport)
	case db.Scope != ScopeWorkspace:
		t.Errorf("db scope = %q, want workspace", db.Scope)
	case !db.Usable():
		t.Errorf("db should be usable: %s", db.Note)
	case strings.Join(db.EnvKeys, ",") != "DB_HOST,DB_TOKEN":
		t.Errorf("db env keys = %v, want the two names sorted", db.EnvKeys)
	}

	remote := byName["remote"]
	if remote.Transport != TransportHTTP || remote.URL != "https://mcp.example/v1" {
		t.Errorf("remote = %q %q, want an http entry with its url", remote.Transport, remote.URL)
	}
	if strings.Join(remote.HeaderKeys, ",") != "Authorization" {
		t.Errorf("remote header keys = %v, want Authorization alone", remote.HeaderKeys)
	}
	if !remote.Usable() {
		t.Errorf("an http entry should be connectable: %s", remote.Note)
	}

	for _, name := range []string{"legacy", "broken", "bad__name"} {
		e := byName[name]
		if e.Usable() {
			t.Errorf("%s should not be usable", name)
		}
		if e.Note == "" {
			t.Errorf("%s should say why it is unusable", name)
		}
	}

	// Nothing an operator is shown may carry a value: not the entries,
	// not the warnings.
	shown, err := json.Marshal(struct {
		Entries  []Entry
		Warnings []string
	}{entries, warnings})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"s3cr3t-db", "s3cr3t-remote"} {
		if strings.Contains(string(shown), secret) {
			t.Errorf("the inventory carries the credential %q: %s", secret, shown)
		}
	}
}

func TestInventoryReadsGlobalServersOnlyWhenAsked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	if err := os.WriteFile(filepath.Join(home, ".mcp.json"),
		[]byte(`{"mcpServers":{"global-one":{"command":"/bin/true"},"db":{"command":"/bin/true"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	root := writeMCP(t, t.TempDir(), `{"mcpServers":{"db":{"command":"/bin/true"}}}`)

	only, _, err := Inventory(root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Name != "db" {
		t.Fatalf("workspaceOnly should list the workspace's server alone, got %+v", only)
	}

	all, warnings, err := Inventory(root, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Name != "db" || all[1].Name != "global-one" {
		t.Fatalf("want the workspace server then the global one, got %+v", all)
	}
	if all[1].Scope != ScopeGlobal {
		t.Errorf("global-one scope = %q, want global", all[1].Scope)
	}
	// The workspace's own db shadows the global one of the same name.
	if !strings.Contains(strings.Join(warnings, "\n"), "a workspace server of the same name") {
		t.Errorf("want a warning about the shadowed name, got %v", warnings)
	}
}

func TestInventoryOnAMissingFileIsEmptyAndOnABrokenOneIsAnError(t *testing.T) {
	entries, _, err := Inventory(t.TempDir(), nil, false)
	if err != nil || len(entries) != 0 {
		t.Fatalf("a workspace with no .mcp.json: got %v, %v", entries, err)
	}
	root := writeMCP(t, t.TempDir(), `{"mcpServers":`)
	if _, _, err := Inventory(root, nil, false); err == nil {
		t.Fatal("a malformed .mcp.json should be an error")
	}
}

func TestConnectStdioListsToolsAndRedactsTheToken(t *testing.T) {
	root := t.TempDir()
	// encoding/json quotes the command, rather than a pair of literal
	// double quotes doing it: on Windows the path is "C:\Users\..." and a
	// raw backslash run inside a JSON document is a string of invalid
	// escapes — "\U" is not one, and the parse fails before the server is
	// ever reached.
	command, err := json.Marshal(fakeServer(t))
	if err != nil {
		t.Fatal(err)
	}
	writeMCP(t, root, `{"mcpServers":{"fake":{"command":`+string(command)+`,"env":{"FAKE_MCP_TOKEN":"$FAKE_MCP_TOKEN"}}}}`)

	entries, _, err := Inventory(root, []string{"FAKE_MCP_TOKEN=hunter2-token", "PATH=" + os.Getenv("PATH")}, false)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := Connect(context.Background(), entries[0], nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	if info := conn.ServerInfo(); info.Name != "fake-mcp" {
		t.Errorf("server info = %+v, want fake-mcp", info)
	}
	tools, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 4 || tools[0].Name != "list_rows" {
		t.Fatalf("got %+v, want the fake's four tools", tools)
	}

	out, isErr, err := conn.CallTool(context.Background(), "echo_token", nil)
	if err != nil || isErr {
		t.Fatalf("CallTool: %v, isError=%v", err, isErr)
	}
	if strings.Contains(out, "hunter2-token") {
		t.Errorf("the token came back out of the server: %q", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Errorf("want the token replaced, got %q", out)
	}
}

func TestConnectHTTPSaysWhatA401Means(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good-token" {
			w.WriteHeader(http.StatusUnauthorized)
			// A server that quotes the token back must not put it on a
			// terminal, whatever the status.
			_, _ = w.Write([]byte(`{"error":"bad token good-token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"remote","version":"2"}}}`))
	}))
	defer srv.Close()

	root := t.TempDir()
	writeMCP(t, root, `{"mcpServers":{"remote":{"type":"http","url":"`+srv.URL+`","headers":{"Authorization":"Bearer $TOK"}}}}`)
	entries, _, err := Inventory(root, []string{"TOK=bad-token"}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Connect(context.Background(), entries[0], nil)
	if err == nil {
		t.Fatal("want an error from a 401")
	}
	if !strings.Contains(err.Error(), "401 from the token, check its scope") {
		t.Errorf("want the 401 sentence, got %v", err)
	}
	if strings.Contains(err.Error(), "bad-token") {
		t.Errorf("the error carries the token: %v", err)
	}
}

func TestConnectHTTPSpeaksSSEAndJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			// Close ends the session; there is no body to read.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var m message
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			t.Errorf("decode request: %v", err)
		}
		id := string(m.ID)
		switch m.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "sess-1")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + id + `,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"remote","version":"2"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "sess-1" {
				t.Errorf("the session id was not carried back: %q", r.Header.Get("Mcp-Session-Id"))
			}
			// The SSE shape, with a notification ahead of the answer.
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n"))
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":" + id + ",\"result\":{\"tools\":[{\"name\":\"get_thing\",\"description\":\"reads\"}]}}\n\n"))
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + id + `,"result":{"content":[{"type":"text","text":"remote said hello"}]}}`))
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	writeMCP(t, root, `{"mcpServers":{"remote":{"type":"http","url":"`+srv.URL+`"}}}`)
	entries, _, err := Inventory(root, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := Connect(context.Background(), entries[0], nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	tools, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "get_thing" {
		t.Fatalf("got %+v, want get_thing", tools)
	}
	out, _, err := conn.CallTool(context.Background(), "get_thing", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out != "remote said hello" {
		t.Errorf("got %q", out)
	}
}
