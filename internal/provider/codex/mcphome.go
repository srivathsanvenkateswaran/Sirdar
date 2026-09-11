package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
)

const (
	// envCodexHome is the variable Codex reads its own directory from:
	// config.toml, auth.json, sessions, caches, skills and plugins.
	envCodexHome = "CODEX_HOME"
	// envKeepHome keeps a session's generated home on disk for debugging.
	envKeepHome = "SIRDAR_KEEP_CODEX_HOME"
	// envWorkspaceOnly tells Doctor which mcp.workspaceOnly setting to
	// report under. Provider.Doctor is handed a binary and nothing else,
	// so the setting cannot reach it through the SessionSpec the way it
	// reaches Start; unset means the config default, which is on.
	envWorkspaceOnly = "SIRDAR_MCP_WORKSPACE_ONLY"
	// defaultCodexDir is the home Codex uses when CODEX_HOME is unset.
	defaultCodexDir = ".codex"
)

// scratchHome is a generated CODEX_HOME for one session: the user's own
// config.toml with its [mcp_servers] tables replaced by the workspace's,
// their auth.json copied in so the session still bills against their
// login, and every other entry of their real home symlinked so history,
// sessions, skills and caches behave as they always did.
//
// It exists because Codex has no equivalent of Claude Code's
// --strict-mcp-config: MCP servers come from config.toml, and both the
// `-c key=value` overrides and thread/start's `config` object merge into
// that table instead of replacing it (verified against codex-cli 0.154.0;
// see docs/research/06-wire-formats.md). Pointing the session at a home
// whose config.toml names only the workspace's servers is the only
// mechanism that can subtract the operator's own.
type scratchHome struct {
	dir     string
	servers []string
	keep    bool
	once    sync.Once
}

// newScratchHome writes a home for the stdio servers declared in
// <root>/.mcp.json. A root with no .mcp.json yields a home that declares
// no servers at all, which is what mcp.workspaceOnly asks for in a
// workspace that has written none. The warnings are LoadWorkspaceServers'
// own, one per skipped entry.
func newScratchHome(root string, env []string) (*scratchHome, []string, error) {
	servers, warnings, err := mcpclient.LoadWorkspaceServers(root)
	if err != nil {
		return nil, nil, err
	}

	dir, err := os.MkdirTemp("", "sirdar-codex-home-")
	if err != nil {
		return nil, nil, err
	}
	h := &scratchHome{dir: dir, keep: envValue(env, envKeepHome) == "1"}
	for _, s := range servers {
		h.servers = append(h.servers, s.Name)
	}

	real := realCodexHome(env)
	if err := h.inherit(real); err != nil {
		h.forceRemove()
		return nil, nil, err
	}
	body := stripMCPServers(readConfig(real)) + renderServers(servers)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		h.forceRemove()
		return nil, nil, err
	}
	return h, warnings, nil
}

// inherit copies the login and links everything else. auth.json is copied
// rather than linked because Codex rewrites it when it refreshes the
// ChatGPT token, and a triage run must not be able to damage the file the
// operator's own `codex` sessions depend on; the copy is the session's to
// refresh. Everything else — sessions, history, state, skills, plugins,
// caches — is symlinked to the real home, so a thread started here is
// still on disk for the resume that a schema retry needs, and so a
// workspace-only run differs from an ordinary one in its MCP servers and
// nothing else.
func (h *scratchHome) inherit(real string) error {
	if real == "" {
		return nil
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		// No home of their own is not an error: Codex creates one.
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		if name == "config.toml" {
			continue
		}
		if name == "auth.json" {
			if err := copyFile(filepath.Join(real, name), filepath.Join(h.dir, name)); err != nil {
				return fmt.Errorf("copy %s: %w", filepath.Join(real, name), err)
			}
			continue
		}
		if err := os.Symlink(filepath.Join(real, name), filepath.Join(h.dir, name)); err != nil {
			return fmt.Errorf("link %s: %w", name, err)
		}
	}
	return nil
}

// apply returns base with CODEX_HOME pointing at the generated home.
func (h *scratchHome) apply(base []string) []string {
	out := make([]string, 0, len(base)+1)
	for _, e := range base {
		if strings.HasPrefix(e, envCodexHome+"=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, envCodexHome+"="+h.dir)
}

// remove deletes the generated home once the session that used it has
// exited. SIRDAR_KEEP_CODEX_HOME=1 keeps it for inspection. A nil home —
// mcp.workspaceOnly off — has nothing to remove.
func (h *scratchHome) remove() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		if h.keep {
			return
		}
		_ = os.RemoveAll(h.dir)
	})
}

// forceRemove discards a half-built home, keep flag or not.
func (h *scratchHome) forceRemove() {
	h.once.Do(func() { _ = os.RemoveAll(h.dir) })
}

// realCodexHome is the home the operator's own codex sessions use.
func realCodexHome(env []string) string {
	if dir := envValue(env, envCodexHome); dir != "" {
		return dir
	}
	if home := envValue(env, "HOME"); home != "" {
		return filepath.Join(home, defaultCodexDir)
	}
	return ""
}

// envValue reads a variable out of a child environment slice.
func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], prefix); ok {
			return v
		}
	}
	return ""
}

// readConfig returns the operator's config.toml, or "" when they have none.
func readConfig(real string) string {
	if real == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(real, "config.toml"))
	if err != nil {
		return ""
	}
	return string(b)
}

// stripMCPServers removes every mcp_servers declaration from a config.toml
// and leaves the rest — model, reasoning effort, project trust, profiles —
// byte for byte, so the generated home changes which MCP servers the
// session sees and nothing else.
func stripMCPServers(cfg string) string {
	if cfg == "" {
		return ""
	}
	var out []string
	skip := false
	for _, line := range strings.Split(cfg, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			skip = isMCPTable(trimmed)
		}
		if skip || isMCPAssignment(trimmed) {
			continue
		}
		out = append(out, line)
	}
	body := strings.Join(out, "\n")
	return strings.TrimRight(body, "\n") + "\n"
}

// isMCPTable reports whether a table header opens an mcp_servers section:
// [mcp_servers], [mcp_servers.x], [mcp_servers.x.env], [[mcp_servers.x]].
func isMCPTable(header string) bool {
	name := strings.TrimLeft(header, "[")
	name = strings.TrimSpace(name)
	return name == "mcp_servers]" || strings.HasPrefix(name, "mcp_servers.") ||
		strings.HasPrefix(name, "mcp_servers ")
}

// isMCPAssignment reports whether a top-level line assigns mcp_servers,
// either whole (mcp_servers = {...}) or by dotted key.
func isMCPAssignment(line string) bool {
	rest, ok := strings.CutPrefix(line, "mcp_servers")
	if !ok {
		return false
	}
	rest = strings.TrimLeft(rest, " \t")
	return strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ".")
}

// renderServers writes one [mcp_servers.<name>] table per workspace server.
// LoadWorkspaceServers has already expanded ${VAR} and $VAR in args and env
// values from Sirdar's own environment, so what lands in the TOML is the
// literal the server will be started with.
func renderServers(servers []mcpclient.ServerConfig) string {
	var b strings.Builder
	for _, s := range servers {
		fmt.Fprintf(&b, "\n[mcp_servers.%s]\n", s.Name)
		fmt.Fprintf(&b, "command = %s\n", tomlString(s.Command))
		b.WriteString("args = [")
		for i, a := range s.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(tomlString(a))
		}
		b.WriteString("]\n")
		if len(s.Env) == 0 {
			continue
		}
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", s.Name)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %s\n", tomlString(k), tomlString(s.Env[k]))
		}
	}
	return b.String()
}

// tomlString quotes a value as a TOML basic string. Environment values
// carry tokens and paths, so the escaping has to be exact rather than
// hopeful: a stray quote or backslash would make the whole config
// unparseable and the session would start with no servers at all.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// copyFile copies src to dst with owner-only permissions.
func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

// specRoot is the workspace whose .mcp.json governs the session. The
// runner sets Cwd to the workspace root and MCPConfig to the .mcp.json in
// it; the config path wins when both are set, so a workspace that moves
// its file does not silently lose its servers.
func specRoot(cwd, mcpConfig string) string {
	if mcpConfig != "" {
		return filepath.Dir(mcpConfig)
	}
	return cwd
}

// workspaceOnly reports whether Doctor should describe a workspace-only
// session. The setting lives in the workspace config, which Provider.Doctor
// never sees, so the environment is the only channel; its default matches
// the config's, which is on.
func workspaceOnly(env []string) bool {
	switch strings.ToLower(envValue(env, envWorkspaceOnly)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// doctorRoot finds the workspace Doctor is being run against by walking up
// from dir for the .sirdar directory that marks one. It falls back to dir
// itself, which is what `sirdar doctor` run from the workspace root gives.
func doctorRoot(dir string) string {
	for {
		if st, err := os.Stat(filepath.Join(dir, ".sirdar")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
