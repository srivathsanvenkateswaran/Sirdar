package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/gather"
)

// TestPlaybooksCheckRedOnBadFrontmatter: a playbook whose frontmatter does
// not parse still loads with the block ignored (spec §8), which is exactly
// the case worth a red row — a misspelt `when` makes a scoped playbook
// always-on with nobody told.
func TestPlaybooksCheckRedOnBadFrontmatter(t *testing.T) {
	root := newWorkspace(t)
	bad := "---\nkinds: [not-a-kind]\n---\n# Bad\nBody.\n"
	if err := os.WriteFile(filepath.Join(root, ".sirdar", "playbooks", "01-bad.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	check := playbooksCheck(cfg)
	if check.OK {
		t.Fatalf("playbooksCheck = %+v, want a failing row", check)
	}
	if !strings.Contains(check.Detail, "01-bad: frontmatter ignored:") {
		t.Errorf("Detail = %q, want it to name the playbook and the reason", check.Detail)
	}
}

func TestPlaybooksCheckOKWithBudget(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	check := playbooksCheck(cfg)
	if !check.OK {
		t.Fatalf("playbooksCheck = %+v, want OK", check)
	}
	if !strings.Contains(check.Detail, "1 playbooks") || !strings.Contains(check.Detail, "budget 16384 bytes") {
		t.Errorf("Detail = %q", check.Detail)
	}
}

// TestGatherCheckWarnsOnDeniedStep: a configured step whose command is not
// on the allow-list is denied the same way a run's own attempt would be;
// the doctor row says so by name rather than leaving it for the first run
// that hits it.
func TestGatherCheckWarnsOnDeniedStep(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Gather.Steps = []config.GatherStep{
		{Name: "company", Command: []string{"company-lookup", "{{company}}"}},
	}

	check := gatherCheck(cfg)
	if check.Level != "warn" {
		t.Fatalf("gatherCheck = %+v, want a warning", check)
	}
	if !strings.Contains(check.Detail, "step company: denied:") {
		t.Errorf("Detail = %q, want it to name the denied step", check.Detail)
	}
}

// TestGatherCheckWarnsOnProgramNotOnPath pins the S4 review finding:
// Preview marks a step whose program is not on PATH "allowed", with the
// fact only in Reason. The doctor row must not let that read as "will
// work" — it warns, naming the step and the reason gather.Preview gave.
func TestGatherCheckWarnsOnProgramNotOnPath(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Permissions.Bash = []string{"nonexistent-tool-xyz *"}
	cfg.Gather.Steps = []config.GatherStep{
		{Name: "lookup", Command: []string{"nonexistent-tool-xyz", "{{company}}"}},
	}
	// An empty PATH guarantees the program cannot be found, on any
	// machine this runs on.
	t.Setenv("PATH", "")

	check := gatherCheck(cfg)
	if check.Level != "warn" {
		t.Fatalf("gatherCheck = %+v, want a warning", check)
	}
	if !strings.Contains(check.Detail, "step lookup:") || !strings.Contains(check.Detail, "is not on PATH") {
		t.Errorf("Detail = %q, want it to name the step and say the program is not on PATH", check.Detail)
	}
}

func TestGatherCheckOKByDefault(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	// gh is not relied on here: the built-in pull-request gatherer is
	// turned off, so its absence from PATH is not this row's business.
	off := false
	cfg.Gather.PR.Enabled = &off

	check := gatherCheck(cfg)
	if !check.OK || check.Level == "warn" {
		t.Fatalf("gatherCheck = %+v, want OK and no warning", check)
	}
	if !strings.Contains(check.Detail, "0 steps allowed") {
		t.Errorf("Detail = %q", check.Detail)
	}
}

// claudeTempHome isolates HOME (and unsets CLAUDE_CONFIG_DIR unless a test
// sets it itself) so a row under test never reads the developer's own
// ~/.claude/settings.json.
func claudeTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

// reproWorkspace returns a workspace configured for staging reproduction,
// with a workspace .mcp.json declaring the repro server. args is the
// server's own args, so a test can include or omit --allowed-origins.
func reproWorkspace(t *testing.T, args []string) *config.Config {
	t.Helper()
	root := newWorkspace(t)
	mcpJSON, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"playwright": map[string]any{"command": "npx", "args": args},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), mcpJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Repro.Hosts = []string{"staging.oxo.example"}
	return cfg
}

// TestReproCheckWarnsOnClaudeAllowRule is the spec §9 residual: an allow
// rule for the repro server's tools in the operator's own Claude settings
// approves the call inside the CLI, so can_use_tool never reaches Sirdar's
// repro guard — worth a warning the operator can act on.
func TestReproCheckWarnsOnClaudeAllowRule(t *testing.T) {
	home := claudeTempHome(t)
	cfg := reproWorkspace(t, []string{"--allowed-origins", "https://staging.oxo.example"})

	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"permissions":{"allow":["mcp__playwright__browser_click"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	check := reproCheck(cfg)
	if check.Level != "warn" {
		t.Fatalf("reproCheck = %+v, want a warning", check)
	}
	if !strings.Contains(check.Detail, settings) || !strings.Contains(check.Detail, "mcp__playwright__browser_click") ||
		!strings.Contains(check.Detail, "never reach Sirdar's repro guard") {
		t.Errorf("Detail = %q", check.Detail)
	}
}

// TestReproCheckWarnsWithoutAllowedOrigins: without --allowed-origins the
// repro guard never auto-allows browser_evaluate, which is worth saying
// before an operator wonders why it keeps asking.
func TestReproCheckWarnsWithoutAllowedOrigins(t *testing.T) {
	claudeTempHome(t)
	cfg := reproWorkspace(t, []string{})

	check := reproCheck(cfg)
	if check.Level != "warn" {
		t.Fatalf("reproCheck = %+v, want a warning", check)
	}
	if !strings.Contains(check.Detail, "browser_evaluate stays an operator question") {
		t.Errorf("Detail = %q", check.Detail)
	}
}

// TestReproCheckFailsWhenServerAbsent: repro.hosts is configured but the
// workspace never added the server to its .mcp.json, so a run would start
// with no browser at all for reproduction to use.
func TestReproCheckFailsWhenServerAbsent(t *testing.T) {
	claudeTempHome(t)
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Repro.Hosts = []string{"staging.oxo.example"}

	check := reproCheck(cfg)
	if check.OK {
		t.Fatalf("reproCheck = %+v, want a failing row", check)
	}
	if !strings.Contains(check.Detail, "mcp server playwright is not configured") {
		t.Errorf("Detail = %q", check.Detail)
	}
}

// TestReproCheckOKWithAllowedOriginsAndNoAllowRule: the clean case — the
// server is configured, confined with --allowed-origins, and nothing in
// the operator's Claude settings pre-approves its tools.
func TestReproCheckOKWithAllowedOriginsAndNoAllowRule(t *testing.T) {
	claudeTempHome(t)
	cfg := reproWorkspace(t, []string{"--allowed-origins", "https://staging.oxo.example"})

	check := reproCheck(cfg)
	if !check.OK || check.Level == "warn" {
		t.Fatalf("reproCheck = %+v, want OK with no warning", check)
	}
	if !strings.Contains(check.Detail, "playwright") || !strings.Contains(check.Detail, "staging.oxo.example") {
		t.Errorf("Detail = %q", check.Detail)
	}
}

// TestReproCheckIgnoresRuleForALongerServerName pins the fix for a prefix
// match that was too loose: strings.HasPrefix(rule, "mcp__"+server) also
// matched a rule for a different server whose name merely starts with the
// repro server's own, e.g. "playwright" against "mcp__playwright2__…". That
// rule approves calls to playwright2, not to the repro server, so it must
// not be reported here.
func TestReproCheckIgnoresRuleForALongerServerName(t *testing.T) {
	home := claudeTempHome(t)
	cfg := reproWorkspace(t, []string{"--allowed-origins", "https://staging.oxo.example"})

	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"permissions":{"allow":["mcp__playwright2__browser_click"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	check := reproCheck(cfg)
	if !check.OK || check.Level == "warn" {
		t.Fatalf("reproCheck = %+v, want OK: the rule names mcp__playwright2__…, not this repro server", check)
	}
}

// TestClaudeSettingsPathsDedupesCLAUDE_CONFIG_DIR: when CLAUDE_CONFIG_DIR
// points at the same directory os.UserHomeDir()'s own ".claude" already
// resolves to, claudeSettingsPaths must list that settings.json once, not
// twice — otherwise every rule in it is read, and warned about, twice.
func TestClaudeSettingsPathsDedupesCLAUDE_CONFIG_DIR(t *testing.T) {
	home := claudeTempHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	paths := claudeSettingsPaths(cfg)
	seen := map[string]int{}
	for _, p := range paths {
		seen[p]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("claudeSettingsPaths lists %s %d times, want once", p, n)
		}
	}
}

// TestReproCheckWarnsOnceWithCLAUDE_CONFIG_DIRPointingAtHome: the end-to-end
// shape of the same bug — a rule in ~/.claude/settings.json produces one
// warning line for that path, not one per alias the settings directory is
// reachable under.
func TestReproCheckWarnsOnceWithCLAUDE_CONFIG_DIRPointingAtHome(t *testing.T) {
	home := claudeTempHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	cfg := reproWorkspace(t, []string{"--allowed-origins", "https://staging.oxo.example"})

	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"permissions":{"allow":["mcp__playwright__browser_click"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	check := reproCheck(cfg)
	if n := strings.Count(check.Detail, "mcp__playwright__browser_click"); n != 1 {
		t.Errorf("Detail = %q names the rule %d times, want once", check.Detail, n)
	}
}

// TestReproCheckOKDetailSaysWhatWasChecked: the OK row only confirms the
// server is started with --allowed-origins, not that it "confines the
// browser" — Sirdar never inspects what the flag actually restricts — so
// the detail says what was checked instead of a result it cannot see.
func TestReproCheckOKDetailSaysWhatWasChecked(t *testing.T) {
	claudeTempHome(t)
	cfg := reproWorkspace(t, []string{"--allowed-origins", "https://staging.oxo.example"})

	check := reproCheck(cfg)
	want := "playwright is started with --allowed-origins https://staging.oxo.example; repro hosts: staging.oxo.example"
	if check.Detail != want {
		t.Errorf("Detail = %q, want %q", check.Detail, want)
	}
}

// TestRunDoctorDefaultRows: a default workspace has gather on and repro
// unconfigured, so the report carries a gather row and a playbooks row,
// both OK, and no repro row at all.
func TestRunDoctorDefaultRows(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	// The built-in pull-request gatherer's gh dependency is not this
	// test's business; turning it off keeps the row's OK-ness independent
	// of whether the machine running the suite has gh installed.
	off := false
	cfg.Gather.PR.Enabled = &off

	rows := RunDoctor(context.Background(), cfg)
	var gather, playbooks *Check
	var sawRepro bool
	for i := range rows {
		switch rows[i].Name {
		case "gather":
			gather = &rows[i]
		case "playbooks":
			playbooks = &rows[i]
		case "repro":
			sawRepro = true
		}
	}
	if gather == nil || !gather.OK {
		t.Fatalf("gather row = %+v, want present and OK", gather)
	}
	if playbooks == nil || !playbooks.OK {
		t.Fatalf("playbooks row = %+v, want present and OK", playbooks)
	}
	if sawRepro {
		t.Error("a workspace with no repro.hosts should carry no repro row")
	}
}

// writeUserScopePlaywright writes a Claude CLI user scope holding a
// Playwright server started with args, and points CLAUDE_CONFIG_DIR at it.
func writeUserScopePlaywright(t *testing.T, args []string) {
	t.Helper()
	dir := t.TempDir()
	body, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"playwright": map[string]any{"type": "stdio", "command": "npx", "args": args},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
}

// TestReproCheckSeesUserServer pins the final-review finding: a run finds
// the repro browser among mcp.userServers as well as .mcp.json, so a
// workspace that opts its Playwright in from the Claude CLI's user scope
// reproduces — and the doctor must not say the server is not configured.
func TestReproCheckSeesUserServer(t *testing.T) {
	claudeTempHome(t)
	writeUserScopePlaywright(t, []string{"--allowed-origins", "https://staging.oxo.example"})
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Repro.Hosts = []string{"staging.oxo.example"}
	cfg.MCP.UserServers = []string{"playwright"}

	check := reproCheck(cfg)
	if !check.OK || check.Level == "warn" {
		t.Fatalf("reproCheck = %+v, want OK: the server comes from mcp.userServers", check)
	}
	if !strings.Contains(check.Detail, "--allowed-origins https://staging.oxo.example") {
		t.Errorf("Detail = %q, want the configured origins", check.Detail)
	}
}

// TestReproCheckUserServerIgnoredByProvider: a provider that runs without
// the Claude CLI's user servers never gets the browser from there, so the
// doctor keeps saying it is not configured for that provider.
func TestReproCheckUserServerIgnoredByProvider(t *testing.T) {
	claudeTempHome(t)
	writeUserScopePlaywright(t, []string{"--allowed-origins", "https://staging.oxo.example"})
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Repro.Hosts = []string{"staging.oxo.example"}
	cfg.MCP.UserServers = []string{"playwright"}
	cfg.Provider = "qwen"

	check := reproCheck(cfg)
	if check.OK || !strings.Contains(check.Detail, "mcp server playwright is not configured") {
		t.Fatalf("reproCheck = %+v, want the not-configured row", check)
	}
}

// TestReproCheckShowsWildcardOrigin: the mere presence of --allowed-origins
// is what lets browser_evaluate run unasked, so a "*" or a production
// origin there counts the same as the staging one. The row shows the value
// so an operator can see which it is, on the warning row as well as the OK
// one.
func TestReproCheckShowsWildcardOrigin(t *testing.T) {
	home := claudeTempHome(t)
	cfg := reproWorkspace(t, []string{"--allowed-origins=*"})

	check := reproCheck(cfg)
	want := "playwright is started with --allowed-origins *; repro hosts: staging.oxo.example"
	if check.Detail != want {
		t.Errorf("Detail = %q, want %q", check.Detail, want)
	}

	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"allow":["mcp__playwright__browser_click"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	check = reproCheck(cfg)
	if check.Level != "warn" || !strings.Contains(check.Detail, "--allowed-origins *; repro hosts: staging.oxo.example") {
		t.Errorf("warning Detail = %q, want the origins beside the repro hosts", check.Detail)
	}
}

// TestGatherCheckResolvesWithChildEnv: a run resolves gather programs and
// MCP servers in the session's child environment, with the workspace's
// credentials stripped, so the doctor previews in that same environment
// rather than this process's whole one.
func TestGatherCheckResolvesWithChildEnv(t *testing.T) {
	root := newWorkspace(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Sources.Helpdesk = &config.SourceConfig{Token: "env:SIRDAR_DOCTOR_TEST_TOKEN"}
	t.Setenv("SIRDAR_DOCTOR_TEST_TOKEN", "secret-value")

	var got []string
	orig := gatherPreview
	gatherPreview = func(c *config.Config, env []string) ([]gather.PreviewRow, string) {
		got = env
		return orig(c, env)
	}
	t.Cleanup(func() { gatherPreview = orig })

	gatherCheck(cfg)
	if got == nil {
		t.Fatal("gather preview was not called")
	}
	for _, e := range got {
		if strings.HasPrefix(e, "SIRDAR_DOCTOR_TEST_TOKEN=") {
			t.Fatalf("preview env carries the helpdesk credential: %q", e)
		}
	}
	if !slices.Contains(got, "SIRDAR_BILLING="+cfg.Billing) {
		t.Errorf("preview env lacks SIRDAR_BILLING, so it is not the child environment a run uses")
	}
}
