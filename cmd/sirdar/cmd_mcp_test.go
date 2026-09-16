package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcpWorkspace is a workspace whose .mcp.json declares the fake stdio
// server internal/mcpclient keeps for its own tests, handed a token in
// secret so the output can be checked for it.
func mcpWorkspace(t *testing.T, mcpJSON string) string {
	t.Helper()
	root, _ := newWorkspace(t, fakeClaude)
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIRDAR_TEST_MCP_TOKEN", "hunter2-token")
	chdir(t, root)
	return root
}

// fakeMCPCommand is the stdio server stand-in, as a JSON string literal:
// the path it quotes is a Windows path half the time, and a raw
// "C:\Users\..." inside a JSON document is a string of invalid escapes.
func fakeMCPCommand(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(fakeMCP); err != nil {
		t.Fatal(err)
	}
	quoted, err := json.Marshal(fakeMCP)
	if err != nil {
		t.Fatal(err)
	}
	return string(quoted)
}

func oneServerJSON(t *testing.T) string {
	t.Helper()
	return `{"mcpServers":{"fake":{"command":` + fakeMCPCommand(t) +
		`,"env":{"FAKE_MCP_TOKEN":"$SIRDAR_TEST_MCP_TOKEN"}},` +
		`"remote":{"type":"http","url":"https://mcp.example/v1","headers":{"Authorization":"Bearer $SIRDAR_TEST_MCP_TOKEN"}}}}`
}

func TestMCPListNamesEveryServerAndNoValue(t *testing.T) {
	mcpWorkspace(t, oneServerJSON(t))

	stdout, _ := mustRun(t, 0, "mcp", "list")
	for _, want := range []string{"fake", "stdio", "remote", "http", "https://mcp.example/v1", "FAKE_MCP_TOKEN", "Authorization"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("`mcp list` did not mention %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "hunter2-token") {
		t.Fatalf("`mcp list` printed the credential:\n%s", stdout)
	}
	if !strings.Contains(stdout, "permissions.mcp is empty") {
		t.Errorf("`mcp list` should say which rule judges the tools:\n%s", stdout)
	}
}

func TestMCPListConnectCountsTools(t *testing.T) {
	mcpWorkspace(t, `{"mcpServers":{"fake":{"command":`+fakeMCPCommand(t)+
		`,"env":{"FAKE_MCP_TOKEN":"$SIRDAR_TEST_MCP_TOKEN"}}}}`)

	stdout, _ := mustRun(t, 0, "mcp", "list", "--connect")
	if !strings.Contains(stdout, "4 tool(s) in") {
		t.Errorf("--connect should count the fake's tools:\n%s", stdout)
	}
	if strings.Contains(stdout, "hunter2-token") {
		t.Fatalf("--connect printed the credential:\n%s", stdout)
	}
}

func TestMCPToolsPrintsTheVerdictARunWouldGet(t *testing.T) {
	mcpWorkspace(t, `{"mcpServers":{"fake":{"command":`+fakeMCPCommand(t)+`}}}`)

	stdout, _ := mustRun(t, 0, "mcp", "tools", "fake")
	want := []string{
		"mcp__fake__list_rows\tallowed\tread word",
		"mcp__fake__delete_rows\tdenied\twrite word",
		"mcp__fake__api_request\tdenied\tgeneric passthrough",
		"mcp__fake__echo_token\tdenied\tno read word",
	}
	for _, line := range want {
		if !strings.Contains(stdout, line) {
			t.Errorf("`mcp tools` is missing %q:\n%s", line, stdout)
		}
	}
}

func TestMCPCallRunsAnAllowedToolAndRefusesADeniedOne(t *testing.T) {
	mcpWorkspace(t, `{"mcpServers":{"fake":{"command":`+fakeMCPCommand(t)+
		`,"env":{"FAKE_MCP_TOKEN":"$SIRDAR_TEST_MCP_TOKEN"}}}}`)

	stdout, _ := mustRun(t, 0, "mcp", "call", "fake", "list_rows", "--args", `{"table":"orders"}`)
	if !strings.Contains(stdout, "rows of orders") {
		t.Errorf("want the tool's own output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "allowed in") {
		t.Errorf("want the verdict and the timing:\n%s", stdout)
	}

	// A denied tool is refused with the same reason `mcp tools` gives,
	// and exit 2.
	_, stderr := mustRun(t, 2, "mcp", "call", "fake", "delete_rows")
	if !strings.Contains(stderr, "refused") || !strings.Contains(stderr, "write word") {
		t.Errorf("want the refusal and its reason:\n%s", stderr)
	}

	// The credential never reaches the terminal, even from a tool that
	// hands it straight back.
	stdout, stderr = mustRun(t, 0, "mcp", "call", "fake", "list_rows")
	if strings.Contains(stdout+stderr, "hunter2-token") {
		t.Fatalf("the credential was printed:\n%s\n%s", stdout, stderr)
	}
}

func TestMCPCallRejectsArgumentsThatAreNotAJSONObject(t *testing.T) {
	mcpWorkspace(t, `{"mcpServers":{"fake":{"command":`+fakeMCPCommand(t)+`}}}`)

	_, stderr := mustRun(t, 2, "mcp", "call", "fake", "list_rows", "--args", "not json")
	if !strings.Contains(stderr, "--args must be a JSON object") {
		t.Errorf("want the argument complaint:\n%s", stderr)
	}
}

func TestMCPOnAWorkspaceWithNoServersSaysSo(t *testing.T) {
	root, _ := newWorkspace(t, fakeClaude)
	chdir(t, root)

	stdout, _ := mustRun(t, 0, "mcp", "list")
	if !strings.Contains(stdout, "no MCP servers") {
		t.Errorf("want the empty case said plainly:\n%s", stdout)
	}
	if _, stderr := mustRun(t, 1, "mcp", "tools", "nope"); !strings.Contains(stderr, "no such mcp server") {
		t.Errorf("want an unknown server reported:\n%s", stderr)
	}
}
