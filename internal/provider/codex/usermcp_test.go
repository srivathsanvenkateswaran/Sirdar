package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// TestScratchHomeCarriesUserServers: mcp.userServers' stdio and header-auth
// http entries land in the generated config.toml beside the workspace's,
// and the directory holding them goes with the home.
func TestScratchHomeCarriesUserServers(t *testing.T) {
	real := writeRealHome(t)
	user := []provider.UserMCPServer{
		{Name: "zoho-desk", Transport: "stdio", Entry: json.RawMessage(`{"type":"stdio","command":"node","args":["dist/index.js"],"env":{"ZOHO_TOKEN":"tok"}}`)},
		{Name: "hdr", Transport: "http", Headers: true, Entry: json.RawMessage(`{"type":"http","url":"https://h.example/mcp","headers":{"Authorization":"Bearer x"}}`)},
	}
	home, _, err := newScratchHome(t.TempDir(), []string{"CODEX_HOME=" + real}, user)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home.dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		`[mcp_servers."zoho-desk"]`, `command = "node"`, `args = ["dist/index.js"]`,
		`[mcp_servers."zoho-desk".env]`, `"ZOHO_TOKEN" = "tok"`,
		`[mcp_servers."hdr"]`, `url = "https://h.example/mcp"`,
		`[mcp_servers."hdr".http_headers]`, `"Authorization" = "Bearer x"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("config.toml lacks %s:\n%s", want, got)
		}
	}
	if s := strings.Join(home.servers, ","); s != "zoho-desk,hdr" {
		t.Errorf("servers = %q", s)
	}
	home.remove()
	if _, err := os.Stat(home.dir); !os.IsNotExist(err) {
		t.Errorf("the home holding the user servers' env is still on disk: %v", err)
	}
}

// TestScratchHomeUserServerClash refuses a user server named like a
// workspace one.
func TestScratchHomeUserServerClash(t *testing.T) {
	real := writeRealHome(t)
	root := writeWorkspace(t, "/opt/bin/mcp")
	user := []provider.UserMCPServer{{Name: "notes", Transport: "stdio", Entry: json.RawMessage(`{"command":"x"}`)}}
	if _, _, err := newScratchHome(root, []string{"CODEX_HOME=" + real}, user); err == nil || !strings.Contains(err.Error(), "also declared") {
		t.Fatalf("want a clash error, got %v", err)
	}
}
