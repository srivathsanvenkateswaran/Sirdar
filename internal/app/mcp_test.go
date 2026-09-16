package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/testbin"
)

// fakeMCPServer installs the stdio server stand-in from internal/testbin
// and returns its path. It used to be a #!/bin/sh script that every layer
// pointed at by a relative path; a script is not executable on Windows, so
// the one fixture is a Go function now (testbin.FakeMCP, registered by
// TestMain) and each caller installs its own copy of this test binary
// under that name.
func fakeMCPServer(t *testing.T) string {
	t.Helper()
	return testbin.Install(t, t.TempDir(), "fakemcp", "fakemcp")
}

// mcpWorkspace is a workspace whose .mcp.json declares the fake server
// under the name "fake", handing it the token in secret.
func mcpWorkspace(t *testing.T, allow ...string) *config.Config {
	t.Helper()
	root := newWorkspace(t)
	// The command has to be quoted by encoding/json, not by hand: half
	// the time it is a Windows path, and a raw "C:\Users\..." inside a
	// JSON document is a string of invalid escapes — "\U" is not one, and
	// the parser refuses the whole file.
	command, err := json.Marshal(fakeMCPServer(t))
	if err != nil {
		t.Fatal(err)
	}
	body := `{"mcpServers":{"fake":{"command":` + string(command) + `,"env":{"FAKE_MCP_TOKEN":"$SIRDAR_TEST_MCP_TOKEN"}}}}`
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIRDAR_TEST_MCP_TOKEN", "hunter2-token")
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Permissions.MCP = allow
	return cfg
}

func TestMCPServersForListsAndConnects(t *testing.T) {
	cfg := mcpWorkspace(t)

	inv, err := MCPServersFor(context.Background(), cfg, false)
	if err != nil {
		t.Fatalf("MCPServersFor: %v", err)
	}
	if len(inv.Servers) != 1 || inv.Servers[0].Name != "fake" {
		t.Fatalf("got %+v, want the one workspace server", inv.Servers)
	}
	row := inv.Servers[0]
	switch {
	case row.Transport != "stdio":
		t.Errorf("transport = %q", row.Transport)
	case row.Connected:
		t.Error("nothing should have been connected without --connect")
	case strings.Join(row.EnvKeys, ",") != "FAKE_MCP_TOKEN":
		t.Errorf("env keys = %v, want the name alone", row.EnvKeys)
	case !inv.WorkspaceOnly:
		t.Error("the default workspace is workspaceOnly")
	}

	connected, err := MCPServersFor(context.Background(), cfg, true)
	if err != nil {
		t.Fatalf("MCPServersFor --connect: %v", err)
	}
	got := connected.Servers[0]
	if !got.Connected || got.Error != "" {
		t.Fatalf("want a connection, got connected=%v err=%q", got.Connected, got.Error)
	}
	if got.Tools != 4 {
		t.Errorf("tools = %d, want the fake's 4", got.Tools)
	}
}

func TestMCPToolsForGivesTheVerdictARunWouldGet(t *testing.T) {
	cfg := mcpWorkspace(t)

	list, err := MCPToolsFor(context.Background(), cfg, "fake")
	if err != nil {
		t.Fatalf("MCPToolsFor: %v", err)
	}
	byName := map[string]MCPTool{}
	for _, tool := range list.Tools {
		byName[tool.Name] = tool
	}

	cases := []struct {
		tool, verdict, rule string
	}{
		{"list_rows", MCPAllowed, "read-word"},
		{"delete_rows", MCPDenied, "write-word"},
		{"api_request", MCPDenied, "passthrough"},
		{"echo_token", MCPDenied, "unrecognised"},
	}
	for _, c := range cases {
		got, ok := byName[c.tool]
		if !ok {
			t.Fatalf("the server did not list %s", c.tool)
		}
		if got.Verdict != c.verdict || got.Rule != c.rule {
			t.Errorf("%s = %s/%s, want %s/%s (%s)", c.tool, got.Verdict, got.Rule, c.verdict, c.rule, got.Reason)
		}
		if got.FullName != "mcp__fake__"+c.tool {
			t.Errorf("%s full name = %q", c.tool, got.FullName)
		}
		if got.Reason == "" {
			t.Errorf("%s has no reason", c.tool)
		}
	}

	// permissions.mcp, once set, is the whole rule — and it is the rule
	// reported, not the heuristic's.
	allowed := mcpWorkspace(t, "mcp__fake__echo_*")
	list, err = MCPToolsFor(context.Background(), allowed, "fake")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		want, rule := MCPDenied, "not-listed"
		if tool.Name == "echo_token" {
			want, rule = MCPAllowed, "pattern"
		}
		if tool.Verdict != want || tool.Rule != rule {
			t.Errorf("%s = %s/%s, want %s/%s", tool.Name, tool.Verdict, tool.Rule, want, rule)
		}
	}
}

func TestMCPCallForRunsAnAllowedToolAndRefusesADeniedOne(t *testing.T) {
	cfg := mcpWorkspace(t)

	res, err := MCPCallFor(context.Background(), cfg, "fake", "list_rows", json.RawMessage(`{"table":"orders"}`))
	if err != nil {
		t.Fatalf("MCPCallFor: %v", err)
	}
	if res.Verdict != MCPAllowed || res.Error != "" {
		t.Fatalf("got %+v, want an allowed call", res)
	}
	if res.Result != "rows of orders" {
		t.Errorf("result = %q, want the tool's own output", res.Result)
	}

	denied, err := MCPCallFor(context.Background(), cfg, "fake", "delete_rows", nil)
	if !errors.Is(err, ErrMCPDenied) {
		t.Fatalf("want ErrMCPDenied, got %v", err)
	}
	if denied.Verdict != MCPDenied || !strings.Contains(denied.Reason, "write word") {
		t.Errorf("got %+v, want the write-word reason", denied)
	}
	if denied.Result != "" || denied.TookMs != 0 {
		t.Errorf("a denied tool must not be run at all: %+v", denied)
	}
}

// Whatever the server says, the credential the workspace handed it does
// not come back out — not through a tool's output, not through an error.
func TestMCPCallForNeverEchoesTheCredential(t *testing.T) {
	cfg := mcpWorkspace(t, "mcp__fake__*")

	res, err := MCPCallFor(context.Background(), cfg, "fake", "echo_token", nil)
	if err != nil {
		t.Fatalf("MCPCallFor: %v", err)
	}
	if strings.Contains(res.Result, "hunter2-token") {
		t.Fatalf("the token reached the result: %q", res.Result)
	}
	if !strings.Contains(res.Result, "[redacted]") {
		t.Errorf("want the token replaced, got %q", res.Result)
	}

	inv, err := MCPServersFor(context.Background(), cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	shown, err := json.Marshal(struct {
		Inventory MCPInventory
		Call      MCPCallResult
	}{inv, res})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shown), "hunter2-token") {
		t.Errorf("the token is in what the API returns: %s", shown)
	}
}

func TestMCPToolsForUnknownServerIsNotFound(t *testing.T) {
	cfg := mcpWorkspace(t)
	if _, err := MCPToolsFor(context.Background(), cfg, "nope"); !errors.Is(err, ErrNoSuchMCPServer) {
		t.Fatalf("want ErrNoSuchMCPServer, got %v", err)
	}
}

func TestMCPResultIsCappedWithATruncatedFlag(t *testing.T) {
	long := strings.Repeat("x", MCPResultLimit+100)
	out, truncated := capResult(long)
	if !truncated || len(out) != MCPResultLimit {
		t.Fatalf("got %d bytes, truncated=%v", len(out), truncated)
	}
	short, truncated := capResult("small")
	if truncated || short != "small" {
		t.Errorf("a short result should pass through: %q %v", short, truncated)
	}
}
