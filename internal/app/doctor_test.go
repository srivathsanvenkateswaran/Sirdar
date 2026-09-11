package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider/codex"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
)

// probeAdapter writes a fake exec adapter that answers describe with the
// roles it is given and answers tracker.get with getCode — the error code
// the probe is meant to react to. An empty getCode means the adapter
// answers "not found", which is what a healthy tracker does for a key that
// cannot exist.
func probeAdapter(t *testing.T, roles, getCode string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe.sh")
	script := "#!/bin/sh\n" +
		"while IFS= read -r line; do\n" +
		`  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')` + "\n" +
		`  case "$line" in` + "\n" +
		`    *'"describe"'*) printf '{"id":%s,"result":{"name":"probe","version":"1","roles":[%s]}}\n' "$id" '` + roles + `' ;;` + "\n" +
		`    *'"tracker.get"'*) printf '{"id":%s,"error":{"code":"` + getCode + `","message":"probe said so"}}\n' "$id" ;;` + "\n" +
		`    *'"shutdown"'*) exit 0 ;;` + "\n" +
		`    *) printf '{"id":%s,"error":{"code":"unsupported","message":"no"}}\n' "$id" ;;` + "\n" +
		"  esac\n" +
		"done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func startProbeAdapter(t *testing.T, roles, getCode string) (*plugin.Client, plugin.Describe) {
	t.Helper()
	c, err := plugin.Start(context.Background(), probeAdapter(t, roles, getCode), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	d, err := c.Describe(context.Background())
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	return c, d
}

// TestTrackerProbeReportsABrokenCredential covers the dogfood finding the
// probe exists for: an adapter whose token command is broken answers
// describe perfectly and fails on the first real fetch.
func TestTrackerProbeReportsABrokenCredential(t *testing.T) {
	for _, code := range []string{"auth", "internal"} {
		c, d := startProbeAdapter(t, `"tracker"`, code)
		got := trackerProbe(context.Background(), c, d)
		if !strings.Contains(got, "tracker.get probe failed with "+code) {
			t.Errorf("%s: trackerProbe = %q", code, got)
		}
		if !strings.Contains(got, "probe said so") {
			t.Errorf("%s: probe dropped the adapter's message: %q", code, got)
		}
	}
}

func TestTrackerProbeStaysQuietOnAHealthyAdapter(t *testing.T) {
	c, d := startProbeAdapter(t, `"tracker"`, "not_found")
	if got := trackerProbe(context.Background(), c, d); got != "" {
		t.Errorf("trackerProbe = %q, want empty", got)
	}
}

// A helpdesk-only adapter has no tracker.get to probe, so the probe must
// not invent a failure for it.
func TestTrackerProbeSkipsANonTracker(t *testing.T) {
	c, d := startProbeAdapter(t, `"helpdesk"`, "auth")
	if got := trackerProbe(context.Background(), c, d); got != "" {
		t.Errorf("trackerProbe = %q, want empty", got)
	}
}

// Without a workspace .mcp.json the session is started against an empty
// MCP config, so the row's job is to say the agent has no MCP tools and
// where to put the servers the playbooks need — not, as it once did, that
// every user-level server is exposed.
func TestMCPCheckWarnsWithoutAWorkspaceConfig(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	check := mcpCheck(cfg)
	if !check.OK {
		t.Errorf("mcp check should warn, not fail: %+v", check)
	}
	if !strings.Contains(check.Detail, "no workspace .mcp.json") ||
		!strings.Contains(check.Detail, "no MCP tools") ||
		!strings.Contains(check.Detail, filepath.Join(root, ".mcp.json")) {
		t.Errorf("the row must say the agent has no MCP tools and where to add them: %q", check.Detail)
	}
	if !strings.Contains(check.Detail, "permissions.mcp is empty") {
		t.Errorf("no permissions note in %q", check.Detail)
	}

	// With a workspace .mcp.json the session is restricted to those
	// servers, and the row says so instead of warning.
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	check = mcpCheck(cfg)
	if strings.Contains(check.Detail, "warning") {
		t.Errorf("still warning with a workspace .mcp.json: %q", check.Detail)
	}
	if !strings.Contains(check.Detail, "sees these servers only") {
		t.Errorf("unexpected detail %q", check.Detail)
	}
}

// TestDoctorReportsTheMCPRow proves the row reaches the report both shells
// print, not just the helper.
func TestDoctorReportsTheMCPRow(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range RunDoctor(context.Background(), cfg) {
		if c.Name == "mcp" {
			return
		}
	}
	t.Fatal("no mcp check in the doctor report")
}

// ---------------------------------------------------------- doctor dispatch

// plainProvider implements only the base Doctor contract.
type plainProvider struct{ binary string }

func (p *plainProvider) Name() string { return "plain" }
func (p *plainProvider) Start(context.Context, provider.SessionSpec) (provider.Session, error) {
	return nil, errors.New("not used")
}
func (p *plainProvider) Doctor(_ context.Context, binary string) []provider.Check {
	p.binary = binary
	return []provider.Check{{Name: "plain", OK: true, Detail: "no workspace was offered"}}
}

// configProvider implements the optional half as well, and records what it
// was told about the workspace.
type configProvider struct {
	plainProvider
	got    provider.DoctorConfig
	called bool
}

func (p *configProvider) DoctorWithConfig(_ context.Context, _ string, cfg provider.DoctorConfig) []provider.Check {
	p.got, p.called = cfg, true
	return []provider.Check{{Name: "config", OK: true, Detail: cfg.Root}}
}

// TestDoctorChecksHandOverTheWorkspace is the fix for a Doctor row that
// guessed. A provider that can use the workspace configuration is given
// it; one that cannot is called as before.
func TestDoctorChecksHandOverTheWorkspace(t *testing.T) {
	cfg := &config.Config{Root: "/w"}
	cfg.MCP.WorkspaceOnly = nil // unset, so the config default (on) applies

	cp := &configProvider{}
	checks := doctorChecks(context.Background(), cp, "/bin/codex", cfg)
	if !cp.called {
		t.Fatal("DoctorWithConfig was not preferred over Doctor")
	}
	if cp.got.Root != "/w" || !cp.got.MCPWorkspaceOnly {
		t.Errorf("DoctorConfig = %+v, want root /w with workspaceOnly on", cp.got)
	}
	if len(checks) != 1 || checks[0].Detail != "/w" {
		t.Errorf("checks = %+v", checks)
	}

	off := false
	cfg.MCP.WorkspaceOnly = &off
	_ = doctorChecks(context.Background(), cp, "/bin/codex", cfg)
	if cp.got.MCPWorkspaceOnly {
		t.Error("the workspaceOnly setting did not reach the provider")
	}

	pp := &plainProvider{}
	checks = doctorChecks(context.Background(), pp, "/bin/plain", cfg)
	if pp.binary != "/bin/plain" {
		t.Errorf("Doctor binary = %q", pp.binary)
	}
	if len(checks) != 1 || checks[0].Name != "plain" {
		t.Errorf("checks = %+v", checks)
	}
}

// TestCodexProviderOffersTheConfigDoctor keeps the two halves connected:
// the wiring above only helps if the Codex adapter implements it.
func TestCodexProviderOffersTheConfigDoctor(t *testing.T) {
	if _, ok := codex.New().(provider.ConfigDoctor); !ok {
		t.Error("the codex provider no longer implements provider.ConfigDoctor, so its doctor row is guessing again")
	}
}
