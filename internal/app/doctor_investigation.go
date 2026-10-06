package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/gather"
	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
	"github.com/srivathsanvenkateswaran/sirdar/internal/prompt"
)

// playbooksCheck reports what the "# Playbooks" section of a prompt will
// carry: how many playbooks load, how many of them scope themselves to a
// kind or a `when` keyword, and the byte budget that caps the section. A
// playbook whose frontmatter did not parse is not silently dropped — it
// still loads, with the block ignored (spec §8) — so the row fails rather
// than warns: a misspelt `when` makes a scoped playbook always-on with
// nobody told, which is worth a red row rather than one more line to miss.
func playbooksCheck(cfg *config.Config) Check {
	all, err := prompt.LoadPlaybooks(cfg.ExpandPath(cfg.Playbooks))
	if err != nil {
		return Check{Name: "playbooks", Detail: err.Error()}
	}
	var bad []string
	scoped := 0
	for _, p := range all {
		if p.FrontmatterErr != "" {
			bad = append(bad, p.Name+": frontmatter ignored: "+p.FrontmatterErr)
			continue
		}
		if len(p.Kinds) > 0 || len(p.When) > 0 {
			scoped++
		}
	}
	if len(bad) > 0 {
		return Check{Name: "playbooks", OK: false, Detail: strings.Join(bad, "; ")}
	}
	budget := "no byte budget"
	if b := cfg.PlaybooksBudget(); b > 0 {
		budget = fmt.Sprintf("budget %d bytes", b)
	}
	return Check{Name: "playbooks", OK: true,
		Detail: fmt.Sprintf("%d playbooks, %d scoped by frontmatter, %s", len(all), scoped, budget)}
}

// gatherCheck reports what gather.Preview finds: a step the workspace's own
// permissions would refuse, an MCP step whose server is not configured, or
// gh missing from PATH while the built-in pull-request gatherer is on —
// all three are things a run would hit silently otherwise, so they are
// warnings here rather than a line in an events log nobody is watching.
// Only called when gather is enabled at all.
func gatherCheck(cfg *config.Config) Check {
	rows, ghPath := gather.Preview(cfg, os.Environ())
	var warnings []string
	for _, r := range rows {
		switch {
		case r.Verdict == gather.PreviewDenied:
			warnings = append(warnings, fmt.Sprintf("step %s: denied: %s", r.Step, r.Reason))
		case r.Server != "" && !r.ServerPresent:
			warnings = append(warnings, fmt.Sprintf("step %s: mcp server %s is not configured", r.Step, r.Server))
		case r.Reason != "" && strings.Contains(r.Reason, "is not on PATH"):
			// Preview marks a step whose program is missing "allowed",
			// because the allow-list has no opinion on binaries that do
			// not exist — but a run that reaches it will only fail, so
			// the doctor row says so plainly rather than letting
			// "allowed" read as "will work".
			warnings = append(warnings, fmt.Sprintf("step %s: %s", r.Step, r.Reason))
		}
	}
	if cfg.GatherPREnabled() && ghPath == "" {
		warnings = append(warnings, "gh is not on PATH; pull-request lookups will fail")
	}
	if len(warnings) > 0 {
		return warn("gather", strings.Join(warnings, "; "))
	}
	detail := fmt.Sprintf("%d steps allowed", len(rows))
	if ghPath != "" {
		detail += "; gh at " + ghPath
	}
	return Check{Name: "gather", OK: true, Detail: detail}
}

// hasAllowedOrigins reports whether a server's configured args carry
// `--allowed-origins`, the flag that makes the repro guard auto-allow
// browser_evaluate (spec §4.2) — the one browser tool that can issue
// arbitrary requests from page script, so it is trusted only when
// Playwright itself is confined to the repro origins.
func hasAllowedOrigins(args []string) bool {
	for _, a := range args {
		if a == "--allowed-origins" || strings.HasPrefix(a, "--allowed-origins=") {
			return true
		}
	}
	return false
}

// claudeSettingsPaths is every file the Claude CLI reads permissions.allow
// from, in the order a rule there would be found: the user's own settings,
// then the workspace's project and local settings, then $CLAUDE_CONFIG_DIR's
// settings when that variable is set (the same override the user-scope MCP
// lookup honours).
func claudeSettingsPaths(cfg *config.Config) []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".claude", "settings.json"))
	}
	paths = append(paths,
		filepath.Join(cfg.Root, ".claude", "settings.json"),
		filepath.Join(cfg.Root, ".claude", "settings.local.json"),
	)
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		paths = append(paths, filepath.Join(dir, "settings.json"))
	}
	return paths
}

// claudeSettingsAllowWarnings is the spec §9 residual: an allow rule for
// `mcp__<server>__…` in the operator's own Claude settings approves the
// call inside the CLI, so `can_use_tool` never reaches Sirdar's repro
// guard at all. A file that does not exist or will not parse is silently
// skipped — this is an operator's own machine, not Sirdar's to validate.
func claudeSettingsAllowWarnings(cfg *config.Config, server string) []string {
	prefix := "mcp__" + server
	var out []string
	for _, path := range claudeSettingsPaths(cfg) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Permissions struct {
				Allow []string `json:"allow"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue
		}
		for _, rule := range doc.Permissions.Allow {
			if strings.HasPrefix(rule, prefix) {
				out = append(out, fmt.Sprintf("Claude settings %s allow %s; those calls never reach Sirdar's repro guard", path, rule))
			}
		}
	}
	return out
}

// reproCheck reports whether staging reproduction is ready to run: the
// repro server is among the workspace's MCP servers, it is started with
// `--allowed-origins` (otherwise browser_evaluate stays an operator
// question rather than auto-allowed on a repro host), and no Claude
// settings rule pre-approves the server's tools behind the repro guard's
// back. Only called when repro.hosts is configured at all.
func reproCheck(cfg *config.Config) Check {
	entries, _, err := mcpclient.Inventory(cfg.Root, os.Environ(), !cfg.WorkspaceOnlyMCP())
	if err != nil {
		return Check{Name: "repro", Detail: err.Error()}
	}
	var entry *mcpclient.Entry
	for i := range entries {
		if entries[i].Name == cfg.Repro.Server && entries[i].Usable() {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return Check{Name: "repro", OK: false, Detail: fmt.Sprintf("mcp server %s is not configured", cfg.Repro.Server)}
	}
	var warnings []string
	if !hasAllowedOrigins(entry.Args) {
		warnings = append(warnings, "browser_evaluate stays an operator question")
	}
	warnings = append(warnings, claudeSettingsAllowWarnings(cfg, cfg.Repro.Server)...)
	if len(warnings) > 0 {
		return warn("repro", strings.Join(warnings, "; "))
	}
	return Check{Name: "repro", OK: true,
		Detail: fmt.Sprintf("%s confines the browser to %s", cfg.Repro.Server, strings.Join(cfg.Repro.Hosts, ", "))}
}
