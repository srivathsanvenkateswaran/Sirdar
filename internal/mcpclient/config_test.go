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

	// Three skipped entries, and the one variable that was not set: a
	// ${VAR} expanding to nothing is how a server ends up started with an
	// empty token and failing for a reason nobody can see from outside.
	if len(warnings) != 4 {
		t.Fatalf("warnings = %+v", warnings)
	}
	joined := strings.Join(warnings, "\n")
	if !containsAll(joined, `"remote"`, `"http"`, `"broken"`, "stdio only", "no command", "SIRDAR_TEST_ABSENT") {
		t.Fatalf("warnings = %q", joined)
	}
	if strings.Contains(joined, "s3cret") {
		t.Fatalf("a warning carried a value rather than only a name: %q", joined)
	}
	if strings.Contains(joined, `"alpha"`) {
		t.Fatalf("warned about a usable server: %q", joined)
	}
}

// TestLoadWorkspaceServersEnv is the point of the env-aware loader: a
// ${VAR} is expanded from the environment the session will run with, not
// from this process's, so a credential internal/run strips cannot be read
// back out through a workspace's .mcp.json.
func TestLoadWorkspaceServersEnv(t *testing.T) {
	t.Setenv("SIRDAR_TEST_STRIPPED", "the-real-secret")

	root := writeMCPJSON(t, `{"mcpServers":{
		"zeta": {"command":"zeta-server","args":["--tok","${SIRDAR_TEST_STRIPPED}"],"env":{"TOKEN":"${SIRDAR_TEST_STRIPPED}"}}
	}}`)

	// The session's environment, with the credential taken out of it.
	servers, warnings, err := LoadWorkspaceServersEnv(root, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %+v", servers)
	}
	if got := strings.Join(servers[0].Args, " "); got != "--tok " {
		t.Fatalf("args = %q; the stripped credential came back", got)
	}
	if servers[0].Env["TOKEN"] != "" {
		t.Fatalf("env = %+v; the stripped credential came back", servers[0].Env)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "SIRDAR_TEST_STRIPPED") {
		t.Fatalf("no warning named the unset variable: %q", joined)
	}
	if strings.Contains(joined, "the-real-secret") {
		t.Fatalf("the warning carried the value: %q", joined)
	}

	// The same file, against an environment that does have it.
	servers, warnings, err = LoadWorkspaceServersEnv(root, []string{"SIRDAR_TEST_STRIPPED=in-session"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(servers[0].Args, " "); got != "--tok in-session" {
		t.Fatalf("args = %q", got)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %+v", warnings)
	}
	if got := strings.Join(servers[0].BaseEnv, " "); got != "SIRDAR_TEST_STRIPPED=in-session" {
		t.Fatalf("BaseEnv = %q; the server would be started from the wrong environment", got)
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

// TestLoadWorkspaceServersSkipsUnnamableServers covers a server whose name
// cannot survive the mcp__<server>__<tool> round trip: a call would be
// routed to the wrong server, or to none, and the workspace's mcp__
// permission rules would not mean what they say.
func TestLoadWorkspaceServersSkipsUnnamableServers(t *testing.T) {
	root := t.TempDir()
	body := `{"mcpServers":{
		"good": {"command":"/bin/echo"},
		"two__parts": {"command":"/bin/echo"},
		"has space": {"command":"/bin/echo"},
		"colon:name": {"command":"/bin/echo"}
	}}`
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	servers, warnings, err := LoadWorkspaceServers(root)
	if err != nil {
		t.Fatalf("LoadWorkspaceServers: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "good" {
		t.Fatalf("servers = %+v, want only good", servers)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %q, want one per skipped name", warnings)
	}
	for _, name := range []string{"two__parts", "has space", "colon:name"} {
		found := false
		for _, w := range warnings {
			if strings.Contains(w, name) && strings.Contains(w, "name must be") {
				found = true
			}
		}
		if !found {
			t.Errorf("no warning names %q: %q", name, warnings)
		}
	}
}
