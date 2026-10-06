package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
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
	want := "playwright is started with --allowed-origins; repro hosts: staging.oxo.example"
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
