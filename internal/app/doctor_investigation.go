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
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
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

// gatherPreview is gather.Preview, held in a variable so a test can see
// the environment the doctor previews in.
var gatherPreview = gather.Preview

// gatherCheck reports what gather.Preview finds: a step the workspace's own
// permissions would refuse, an MCP step whose server is not configured, or
// gh missing from PATH while the built-in pull-request gatherer is on —
// all three are things a run would hit silently otherwise, so they are
// warnings here rather than a line in an events log nobody is watching.
// Only called when gather is enabled at all. The preview runs in the child
// environment a run gives its gather steps, credentials stripped, so a
// program or server that resolves here resolves the same way in a run.
func gatherCheck(cfg *config.Config) Check {
	rows, ghPath := gatherPreview(cfg, cfg.ChildEnv(os.Environ()))
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

// allowedOriginsValue returns what a server's configured args pass to
// `--allowed-origins`, in either the two-element or the = form, and whether
// the flag is there at all. The flag's presence is what makes the repro
// guard auto-allow browser_evaluate (spec §4.2), the one browser tool that
// can issue arbitrary requests from page script; its value is not checked
// by anything, so the doctor shows it and a "*" or a production origin is
// in front of the operator rather than counted as confinement unseen.
// Several occurrences are all shown, in order.
func allowedOriginsValue(args []string) (string, bool) {
	var values []string
	found := false
	for i, a := range args {
		switch {
		case a == "--allowed-origins":
			found = true
			if i+1 < len(args) {
				values = append(values, args[i+1])
			}
		case strings.HasPrefix(a, "--allowed-origins="):
			found = true
			values = append(values, strings.TrimPrefix(a, "--allowed-origins="))
		}
	}
	return strings.Join(values, " "), found
}

// claudeSettingsPaths is every file the Claude CLI reads permissions.allow
// from, in the order a rule there would be found: the user's own settings,
// then the workspace's project and local settings, then $CLAUDE_CONFIG_DIR's
// settings when that variable is set (the same override the user-scope MCP
// lookup honours). CLAUDE_CONFIG_DIR commonly points straight at ~/.claude,
// which is the same file as the first entry under a different spelling; the
// paths are cleaned and deduplicated so that case does not read the same
// settings.json twice and warn about every rule in it twice.
func claudeSettingsPaths(cfg *config.Config) []string {
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".claude", "settings.json"))
	}
	candidates = append(candidates,
		filepath.Join(cfg.Root, ".claude", "settings.json"),
		filepath.Join(cfg.Root, ".claude", "settings.local.json"),
	)
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		candidates = append(candidates, filepath.Join(dir, "settings.json"))
	}
	seen := make(map[string]bool, len(candidates))
	paths := make([]string, 0, len(candidates))
	for _, p := range candidates {
		clean := filepath.Clean(p)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		paths = append(paths, clean)
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
			// A plain HasPrefix(rule, prefix) also matches a rule for a
			// different server whose name merely starts with this one's,
			// e.g. "mcp__playwright2__…" against the "playwright" repro
			// server — so the rule must name this server exactly, or one
			// of its tools with the "__" separator Claude settings use.
			if rule == prefix || strings.HasPrefix(rule, prefix+"__") {
				out = append(out, fmt.Sprintf("Claude settings %s allow %s; those calls never reach Sirdar's repro guard", path, rule))
			}
		}
	}
	return out
}

// reproCheck reports whether staging reproduction is ready to run: the
// repro server is among the servers a run's session is offered, it is
// started with `--allowed-origins` (otherwise browser_evaluate stays an
// operator question rather than auto-allowed on a repro host), and no
// Claude settings rule pre-approves the server's tools behind the repro
// guard's back. Every row names the configured origins beside repro.hosts.
// Only called when repro.hosts is configured at all.
func reproCheck(cfg *config.Config) Check {
	args, ok, err := reproServerArgs(cfg, cfg.ChildEnv(os.Environ()))
	if err != nil {
		return Check{Name: "repro", Detail: err.Error()}
	}
	if !ok {
		return Check{Name: "repro", OK: false, Detail: fmt.Sprintf("mcp server %s is not configured", cfg.Repro.Server)}
	}
	hosts := strings.Join(cfg.Repro.Hosts, ", ")
	origins, confined := allowedOriginsValue(args)
	summary := fmt.Sprintf("%s is started without --allowed-origins; repro hosts: %s", cfg.Repro.Server, hosts)
	if confined {
		summary = fmt.Sprintf("%s is started with --allowed-origins %s; repro hosts: %s", cfg.Repro.Server, origins, hosts)
	}
	var warnings []string
	if !confined {
		warnings = append(warnings, "browser_evaluate stays an operator question")
	}
	warnings = append(warnings, claudeSettingsAllowWarnings(cfg, cfg.Repro.Server)...)
	if len(warnings) > 0 {
		return warn("repro", strings.Join(append(warnings, summary), "; "))
	}
	return Check{Name: "repro", OK: true, Detail: summary}
}

// reproServerArgs finds the repro server where a run looks for it
// (internal/run's sessionServerArgs): the workspace's .mcp.json, the
// operator's global files when mcp.workspaceOnly is off, and then the
// Claude CLI servers mcp.userServers opts in — of those, only the ones the
// configured provider carries into a session, since a run on any other
// provider never sees them. env is the run's child environment, which is
// what both lookups expand through in a run.
func reproServerArgs(cfg *config.Config, env []string) ([]string, bool, error) {
	server := cfg.Repro.Server
	entries, _, err := mcpclient.Inventory(cfg.Root, env, !cfg.WorkspaceOnlyMCP())
	if err != nil {
		return nil, false, err
	}
	for _, e := range entries {
		if e.Name == server && e.Usable() {
			return e.Args, true, nil
		}
	}
	users, err := cfg.UserMCPServers(env)
	if err != nil {
		return nil, false, err
	}
	for _, s := range users {
		if s.Name != server || !providerCarriesUserServer(string(cfg.Provider), s) {
			continue
		}
		var entry struct {
			Args []string `json:"args"`
		}
		_ = json.Unmarshal(s.Entry, &entry)
		return entry.Args, true, nil
	}
	return nil, false, nil
}

// providerCarriesUserServer is internal/run's rule for which of the
// mcp.userServers a provider takes into its session: Claude Code takes
// every one; Codex takes stdio servers and http servers with headers of
// their own, because an OAuth grant held by the Claude CLI is not one
// Codex can present; every other provider runs without them.
func providerCarriesUserServer(name string, s provider.UserMCPServer) bool {
	switch name {
	case "claude":
		return true
	case "codex":
		return s.Transport == "stdio" || (s.Headers && !s.OAuth)
	}
	return false
}
