package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

func writeCLIUserScope(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := `{"mcpServers":{
		"slack":{"type":"http","url":"https://mcp.example/mcp","oauth":{"clientId":"c"}},
		"janus":{"type":"http","url":"https://janus.example/mcp"},
		"zoho-desk":{"type":"stdio","command":"node","env":{"TOKEN":"secret-value"}}}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDoctorUserServersRow(t *testing.T) {
	env := []string{"CLAUDE_CONFIG_DIR=" + writeCLIUserScope(t)}
	cfg := &config.Config{Provider: "claude"}
	if _, ok := userServersCheck(cfg, env); ok {
		t.Fatal("no row when the workspace opts no server in")
	}

	cfg.MCP.UserServers = []string{"slack", "zoho-desk", "janus"}
	c, ok := userServersCheck(cfg, env)
	if !ok || !c.OK || c.Detail != "slack (http, oauth) · zoho-desk (stdio) · janus (http)" {
		t.Fatalf("row = %+v", c)
	}
	if strings.Contains(c.Detail, "secret-value") || strings.Contains(c.Detail, "clientId") {
		t.Fatal("the row printed an entry's values")
	}

	cfg.MCP.UserServers = []string{"slack", "jira"}
	c, _ = userServersCheck(cfg, env)
	if c.OK || !strings.Contains(c.Detail, "jira (not in the Claude CLI)") || !strings.Contains(c.Detail, "janus, slack, zoho-desk") {
		t.Fatalf("missing name: %+v", c)
	}

	cfg.MCP.UserServers = []string{"slack", "zoho-desk"}
	cfg.Provider = "codex"
	if c, _ := userServersCheck(cfg, env); !c.OK || c.Level != "warn" || !strings.Contains(c.Detail, "codex runs without slack") {
		t.Fatalf("codex: %+v", c)
	}
	cfg.Provider = "qwen"
	if c, _ := userServersCheck(cfg, env); c.Level != "warn" || !strings.Contains(c.Detail, "ignored by the qwen provider") {
		t.Fatalf("qwen: %+v", c)
	}
}
