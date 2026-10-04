package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
)

// TestUserServersPerProvider: claude carries every opted server, codex the
// stdio and header-auth ones with a notice naming the rest, and any other
// provider none, with a notice.
func TestUserServersPerProvider(t *testing.T) {
	cli := t.TempDir()
	if err := os.WriteFile(filepath.Join(cli, ".claude.json"), []byte(`{"mcpServers":{
		"slack":{"type":"http","url":"https://mcp.example/mcp","oauth":{"clientId":"c"}},
		"janus":{"type":"http","url":"https://janus.example/mcp"},
		"zoho-desk":{"type":"stdio","command":"node"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Root: t.TempDir(), Billing: "subscription"}
	cfg.MCP.UserServers = []string{"slack", "zoho-desk", "janus"}

	for _, tc := range []struct {
		provider string
		servers  string
		notice   string
	}{
		{"claude", "slack,zoho-desk,janus", ""},
		{"codex", "zoho-desk", "codex runs without slack, janus"},
		{"qwen", "", "ignored by the qwen provider (slack, zoho-desk, janus)"},
	} {
		r := &Runner{Deps: Deps{Config: cfg, Provider: &stubProvider{name: tc.provider}, Env: []string{"CLAUDE_CONFIG_DIR=" + cli}}}
		p := &prepared{}
		if err := r.userServers(p); err != nil {
			t.Fatalf("%s: %v", tc.provider, err)
		}
		var names []string
		for _, s := range p.userMCP {
			names = append(names, s.Name)
		}
		if got := strings.Join(names, ","); got != tc.servers {
			t.Errorf("%s: servers %q, want %q", tc.provider, got, tc.servers)
		}
		if !strings.Contains(p.userMCPNotice, tc.notice) || (tc.notice == "") != (p.userMCPNotice == "") {
			t.Errorf("%s: notice %q, want it to contain %q", tc.provider, p.userMCPNotice, tc.notice)
		}
		if spec := r.sessionSpec(p, ""); len(spec.UserMCPServers) != len(names) {
			t.Errorf("%s: the session spec does not carry the servers", tc.provider)
		}
	}

	// A server removed from the CLI since the config was loaded fails the
	// run rather than starting it without the connector.
	cfg.MCP.UserServers = []string{"gone"}
	r := &Runner{Deps: Deps{Config: cfg, Provider: &stubProvider{}, Env: []string{"CLAUDE_CONFIG_DIR=" + cli}}}
	if err := r.userServers(&prepared{}); err == nil {
		t.Fatal("a name the CLI no longer has must fail the run")
	}
}
