package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const userScope = `{
  "numStartups": 3,
  "mcpServers": {
    "slack": {"type": "http", "url": "https://mcp.slack.example/mcp", "oauth": {"clientId": "c", "callbackPort": 1}},
    "janus": {"type": "http", "url": "https://janus.example/mcp"},
    "zoho-desk": {"type": "stdio", "command": "node", "args": ["index.js"], "env": {"TOKEN": "t"}},
    "hdr": {"type": "http", "url": "https://h.example/mcp", "headers": {"Authorization": "Bearer x"}}
  }
}`

func writeUserScope(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(userScope), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeUserConfigPath(t *testing.T) {
	if got := ClaudeUserConfigPath([]string{"HOME=/h"}); got != filepath.Join("/h", ".claude.json") {
		t.Errorf("HOME only: %q", got)
	}
	if got := ClaudeUserConfigPath([]string{"HOME=/h", "CLAUDE_CONFIG_DIR=/c"}); got != filepath.Join("/c", ".claude.json") {
		t.Errorf("CLAUDE_CONFIG_DIR wins: %q", got)
	}
}

func TestLoadClaudeUserServers(t *testing.T) {
	dir := t.TempDir()
	writeUserScope(t, dir)
	all, err := LoadClaudeUserServers([]string{"CLAUDE_CONFIG_DIR=" + dir})
	if err != nil {
		t.Fatal(err)
	}
	got, err := PickUserServers(all, []string{"slack", "zoho-desk", "janus", "hdr"})
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, s := range got {
		labels = append(labels, s.Label())
	}
	if want := "slack (http, oauth)|zoho-desk (stdio)|janus (http)|hdr (http)"; strings.Join(labels, "|") != want {
		t.Errorf("labels = %q", strings.Join(labels, "|"))
	}
	if !got[3].Headers || got[2].Headers {
		t.Errorf("headers flags wrong: %+v", got)
	}
	if !strings.Contains(string(got[1].Entry), `"TOKEN"`) {
		t.Errorf("entry is not verbatim: %s", got[1].Entry)
	}
}

func TestPickUserServersUnknownName(t *testing.T) {
	dir := t.TempDir()
	writeUserScope(t, dir)
	_, err := UserServersFor([]string{"CLAUDE_CONFIG_DIR=" + dir}, []string{"slack", "jira"})
	if err == nil {
		t.Fatal("an unknown name must fail")
	}
	if !strings.Contains(err.Error(), `"jira"`) || !strings.Contains(err.Error(), "hdr, janus, slack, zoho-desk") {
		t.Errorf("error should name the missing one and the ones the CLI has: %v", err)
	}
}

func TestLoadClaudeUserServersMissingFile(t *testing.T) {
	all, err := LoadClaudeUserServers([]string{"CLAUDE_CONFIG_DIR=" + t.TempDir()})
	if err != nil || len(all) != 0 {
		t.Fatalf("a CLI with no user file has no servers: %v %v", all, err)
	}
}

func TestWriteMCPConfigWithoutWorkspaceFile(t *testing.T) {
	dir := t.TempDir()
	writeUserScope(t, dir)
	servers, err := UserServersFor([]string{"CLAUDE_CONFIG_DIR=" + dir}, []string{"janus"})
	if err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := WriteMCPConfig(filepath.Join(t.TempDir(), ".mcp.json"), servers)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("generated config mode %v, want 0600", info.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"janus"`) {
		t.Errorf("config lacks janus: %s", b)
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Errorf("cleanup left %s behind", filepath.Dir(path))
	}
}
