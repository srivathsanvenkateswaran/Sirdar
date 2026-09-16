package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider/codex"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/testbin"
)

// probeAdapterMain is the adapter half of the tracker-probe tests: it
// answers describe with the roles named in its first argument (a
// comma-separated list) and answers tracker.get with the error code named
// in its second — the code the probe is meant to react to. "not_found" is
// what a healthy tracker answers for a key that cannot exist.
//
// It runs inside a copy of this test binary, dispatched by TestMain on the
// name probeAdapter installed it under. The roles and the code travel as
// arguments rather than environment variables because the thing under test
// decides what environment the adapter gets.
func probeAdapterMain() int {
	args := testbin.Args()
	if len(args) != 2 {
		return testbin.Fail("probe adapter: want <roles> <tracker.get code>, got %q", args)
	}
	roles, err := json.Marshal(strings.Split(args[0], ","))
	if err != nil {
		return testbin.Fail("probe adapter: %v", err)
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for in.Scan() {
		var req plugin.Request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			continue // tolerate noise, the way a real adapter must
		}
		switch req.Method {
		case "describe":
			fmt.Printf(`{"id":%d,"result":{"name":"probe","version":"1","roles":%s}}`+"\n", req.ID, roles)
		case "tracker.get":
			fmt.Printf(`{"id":%d,"error":{"code":%q,"message":"probe said so"}}`+"\n", req.ID, args[1])
		case "shutdown":
			return 0
		default:
			fmt.Printf(`{"id":%d,"error":{"code":"unsupported","message":"no"}}`+"\n", req.ID)
		}
	}
	return 0
}

// probeAdapter installs probeAdapterMain as a real executable and returns
// the command that starts it with the roles and the tracker.get code it
// should answer with. The path is quoted because plugin.Start splits its
// command on spaces and a temp directory can contain one.
func probeAdapter(t *testing.T, roles, getCode string) string {
	t.Helper()
	bin := testbin.Install(t, t.TempDir(), "probeadapter", "probeadapter")
	return `"` + bin + `" ` + roles + " " + getCode
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
		c, d := startProbeAdapter(t, "tracker", code)
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
	c, d := startProbeAdapter(t, "tracker", "not_found")
	if got := trackerProbe(context.Background(), c, d); got != "" {
		t.Errorf("trackerProbe = %q, want empty", got)
	}
}

// A helpdesk-only adapter has no tracker.get to probe, so the probe must
// not invent a failure for it.
func TestTrackerProbeSkipsANonTracker(t *testing.T) {
	c, d := startProbeAdapter(t, "helpdesk", "auth")
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

// TestClaudeEnvironmentCheckFlagsGatewayVarsUnderSubscription covers the
// credential-leak fix: subscription billing (the default) with a gateway
// variable set in the process environment must fail the check and name
// every such variable, since Sirdar is about to strip them from the
// agent's environment.
func TestClaudeEnvironmentCheckFlagsGatewayVarsUnderSubscription(t *testing.T) {
	for _, name := range claudeGatewayEnvVars {
		clearClaudeGatewayEnvVars(t)
		t.Setenv(name, "set")
		cfg := &config.Config{Billing: "subscription"}
		check := claudeEnvironmentCheck(cfg)
		if check.OK {
			t.Fatalf("%s: check should fail with billing: subscription, got %+v", name, check)
		}
		if !strings.Contains(check.Detail, name) {
			t.Errorf("%s: detail does not name the variable: %q", name, check.Detail)
		}
		if !strings.Contains(check.Detail, "strip") {
			t.Errorf("%s: detail does not say Sirdar will strip it: %q", name, check.Detail)
		}
	}
}

// TestClaudeEnvironmentCheckOKWhenClean covers the quiet cases: no gateway
// variable set under subscription billing, and api billing with no base
// URL, both pass with no alarming detail.
func TestClaudeEnvironmentCheckOKWhenClean(t *testing.T) {
	clearClaudeGatewayEnvVars(t)
	if check := claudeEnvironmentCheck(&config.Config{Billing: "subscription"}); !check.OK {
		t.Errorf("clean subscription env should pass: %+v", check)
	}
	if check := claudeEnvironmentCheck(&config.Config{Billing: "api"}); !check.OK {
		t.Errorf("api billing with no base URL should pass: %+v", check)
	}
}

// TestClaudeEnvironmentCheckAPIBillingNotesUnreliableCost covers the other
// half: under api billing a custom ANTHROPIC_BASE_URL is the supported
// configuration, so the check passes but says budget.maxUsd cannot be
// trusted, naming only the host.
func TestClaudeEnvironmentCheckAPIBillingNotesUnreliableCost(t *testing.T) {
	clearClaudeGatewayEnvVars(t)
	t.Setenv("ANTHROPIC_BASE_URL", "http://localhost:11434/some/path?secret=1")
	check := claudeEnvironmentCheck(&config.Config{Billing: "api"})
	if !check.OK {
		t.Fatalf("api billing should pass even with a custom base URL: %+v", check)
	}
	if !strings.Contains(check.Detail, "localhost:11434") {
		t.Errorf("detail should name the host: %q", check.Detail)
	}
	if strings.Contains(check.Detail, "secret=1") {
		t.Errorf("detail leaked the full URL, not just the host: %q", check.Detail)
	}
	if !strings.Contains(check.Detail, "budget.maxUsd") {
		t.Errorf("detail should say budget.maxUsd cannot be trusted: %q", check.Detail)
	}
}

// clearClaudeGatewayEnvVars scrubs every gateway variable via t.Setenv (to
// "", which os.Getenv reports the same as unset), so the check's tests are
// isolated from whatever the test runner's own shell happens to export,
// and are restored automatically once the test ends.
func clearClaudeGatewayEnvVars(t *testing.T) {
	t.Helper()
	for _, name := range claudeGatewayEnvVars {
		t.Setenv(name, "")
	}
}

// TestAdvisoryChecksWarnRatherThanFail is the tri-state itself: a row that
// is worth reading and is not a broken workspace reports "warn", which
// leaves OK true so no exit code moves. The mcp row of a default workspace
// (workspaceOnly on, no .mcp.json) and the api-billing base URL note are
// the two the dogfood run asked for.
func TestAdvisoryChecksWarnRatherThanFail(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	mcp := mcpCheck(cfg)
	if mcp.Level != string(provider.LevelWarn) || !mcp.OK {
		t.Errorf("mcp row = %+v, want a warning that is still OK", mcp)
	}

	cfg.MCP.WorkspaceOnly = new(bool) // off: the agent sees every user-level server
	if off := mcpCheck(cfg); off.Level != string(provider.LevelWarn) || !off.OK {
		t.Errorf("mcp row with workspaceOnly off = %+v, want a warning that is still OK", off)
	}

	clearClaudeGatewayEnvVars(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.example.com")
	env := claudeEnvironmentCheck(&config.Config{Billing: "api"})
	if env.Level != string(provider.LevelWarn) || !env.OK {
		t.Errorf("claude environment row = %+v, want a warning that is still OK", env)
	}
}

// A check that named no level is levelled off its bool, so every row of
// the report carries one and no caller has to derive it twice.
func TestEveryDoctorRowCarriesALevel(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range RunDoctor(context.Background(), cfg) {
		switch c.Level {
		case string(provider.LevelOK), string(provider.LevelWarn), string(provider.LevelFail):
		default:
			t.Errorf("%s: level %q", c.Name, c.Level)
		}
		if c.OK != (c.Level != string(provider.LevelFail)) {
			t.Errorf("%s: ok=%v disagrees with level %q", c.Name, c.OK, c.Level)
		}
	}
}

// TestDoctorReportsTheDisabledAgyProvider covers the report an operator
// whose workspace still says `provider: agy` runs to find out what is
// wrong: one row naming the provider and the reason, in place of the
// binary, model and permission rows of a provider no run will reach.
func TestDoctorReportsTheDisabledAgyProvider(t *testing.T) {
	cfg := &config.Config{Root: t.TempDir(), Provider: "agy"}
	rows := providerChecks(context.Background(), cfg)
	if len(rows) != 1 {
		t.Fatalf("provider rows = %+v, want exactly one", rows)
	}
	if rows[0].Name != "agy" || rows[0].Detail != "disabled (Antigravity terms)" {
		t.Fatalf("row = %+v, want agy — disabled (Antigravity terms)", rows[0])
	}
	if rows[0].OK {
		t.Fatal("the disabled row is OK; a workspace naming it cannot run")
	}

	// The acknowledgement brings the ordinary report back, which is what
	// makes the row a statement about the refusal and not about the
	// adapter, still in the tree and still working.
	cfg.Agy = &config.AgyConfig{AcknowledgeTerms: true}
	acked := providerChecks(context.Background(), cfg)
	if len(acked) == 1 && acked[0].Detail == "disabled (Antigravity terms)" {
		t.Fatal("the acknowledged workspace still reports the provider disabled")
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

// fakeWhisperMain stands in for a transcription command the doctor row is
// meant to find. The row never runs it — it only looks it up on PATH — so
// the fake has nothing to do but succeed.
func fakeWhisperMain() int { return 0 }

// TestTranscribeCheckReportsWhatWillHappenToAudio covers the three
// answers the row can give: no transcription configured, a command that
// cannot be found, and one that can.
func TestTranscribeCheckReportsWhatWillHappenToAudio(t *testing.T) {
	cfg := &config.Config{}
	c := transcribeCheck(cfg)
	if !c.OK || !strings.Contains(c.Detail, "unset") {
		t.Fatalf("unconfigured: %+v", c)
	}

	cfg.Attachments.Transcribe = &config.TranscribeConfig{
		Command:    "sirdar-no-such-transcriber -l ar {in}",
		MaxSeconds: 300,
		MaxFiles:   30,
		Formats:    []string{"ogg", "mp3"},
	}
	c = transcribeCheck(cfg)
	if c.Level != string(provider.LevelWarn) || !strings.Contains(c.Detail, "not on PATH") {
		t.Fatalf("missing binary: %+v", c)
	}
	if !c.OK {
		t.Error("a missing transcriber is not a reason for doctor to exit non-zero")
	}

	// A real executable, not a #!/bin/sh file: the row's verdict is
	// exec.LookPath's, and on Windows LookPath does not consider an
	// extensionless file executable at all. testbin.Install appends the
	// ".exe" the platform needs, so bin is the name the config command
	// carries and the name the row must quote back.
	bin := testbin.Install(t, t.TempDir(), "fake-whisper", "fake-whisper")
	cfg.Attachments.Transcribe.Command = `"` + bin + `" {in}`
	c = transcribeCheck(cfg)
	if !c.OK || !strings.Contains(c.Detail, bin) {
		t.Fatalf("configured: %+v", c)
	}
	for _, want := range []string{"30 files", "300s", "ogg, mp3"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("the row does not state %q: %s", want, c.Detail)
		}
	}
}

// TestTranscribeCheckRefusesAnUnrunnableCommand: config validation
// already refuses this, so doctor is where an operator editing the file by
// hand sees it.
func TestTranscribeCheckRefusesAnUnrunnableCommand(t *testing.T) {
	cfg := &config.Config{}
	cfg.Attachments.Transcribe = &config.TranscribeConfig{Command: "whisper {in} | tee out"}
	if c := transcribeCheck(cfg); c.OK {
		t.Fatalf("a pipeline should fail the row: %+v", c)
	}
}
