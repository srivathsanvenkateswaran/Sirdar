package mcpclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMCPJSON(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadWorkspaceServers(t *testing.T) {
	t.Setenv("SIRDAR_TEST_TOKEN", "s3cret")

	root := writeMCPJSON(t, `{"mcpServers":{
		"zeta":   {"type":"stdio","command":"zeta-server","args":["--tok","${SIRDAR_TEST_TOKEN}","--plain"],"env":{"TOKEN":"${SIRDAR_TEST_TOKEN}","MIX":"pre-$SIRDAR_TEST_TOKEN","MISSING":"[${SIRDAR_TEST_ABSENT}]"}},
		"alpha":  {"command":"alpha-server"},
		"remote": {"type":"sse","url":"https://example.test/sse"},
		"http":   {"url":"https://example.test/mcp"},
		"broken": {"type":"stdio"}
	}}`)

	servers, warnings, err := LoadWorkspaceServers(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("servers = %+v", servers)
	}
	if servers[0].Name != "alpha" || servers[1].Name != "zeta" {
		t.Fatalf("not sorted by name: %+v", servers)
	}
	if servers[0].Root != root || servers[1].Root != root {
		t.Fatalf("root not propagated: %+v", servers)
	}

	z := servers[1]
	if z.Command != "zeta-server" {
		t.Fatalf("command = %q", z.Command)
	}
	if got := strings.Join(z.Args, " "); got != "--tok s3cret --plain" {
		t.Fatalf("args = %q", got)
	}
	if z.Env["TOKEN"] != "s3cret" || z.Env["MIX"] != "pre-s3cret" || z.Env["MISSING"] != "[]" {
		t.Fatalf("env = %+v", z.Env)
	}

	if len(warnings) != 3 {
		t.Fatalf("warnings = %+v", warnings)
	}
	joined := strings.Join(warnings, "\n")
	if !containsAll(joined, `"remote"`, `"http"`, `"broken"`, "stdio only", "no command") {
		t.Fatalf("warnings = %q", joined)
	}
	if strings.Contains(joined, `"zeta"`) || strings.Contains(joined, `"alpha"`) {
		t.Fatalf("warned about a usable server: %q", joined)
	}
}

func TestLoadWorkspaceServersMissingFile(t *testing.T) {
	servers, warnings, err := LoadWorkspaceServers(t.TempDir())
	if err != nil || servers != nil || warnings != nil {
		t.Fatalf("servers=%+v warnings=%+v err=%v", servers, warnings, err)
	}
}

func TestLoadWorkspaceServersMalformed(t *testing.T) {
	root := writeMCPJSON(t, `{"mcpServers":`)
	if _, _, err := LoadWorkspaceServers(root); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestToolNameRoundTrip(t *testing.T) {
	for _, tc := range []struct{ server, tool string }{
		{"github", "create_issue"},
		{"fs", "read"},
		{"linear", "list__issues"}, // separators inside a tool name survive
	} {
		name := ToolName(tc.server, tc.tool)
		if !strings.HasPrefix(name, "mcp__") {
			t.Fatalf("ToolName = %q", name)
		}
		server, tool, ok := SplitToolName(name)
		if !ok || server != tc.server || tool != tc.tool {
			t.Fatalf("SplitToolName(%q) = %q, %q, %v", name, server, tool, ok)
		}
	}

	for _, bad := range []string{"read_file", "mcp__", "mcp__github", "mcp____tool", "mcp__github__", ""} {
		if _, _, ok := SplitToolName(bad); ok {
			t.Fatalf("SplitToolName(%q) reported ok", bad)
		}
	}
}
