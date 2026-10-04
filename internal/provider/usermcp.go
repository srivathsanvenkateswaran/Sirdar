package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// UserMCPServer is one MCP server the operator configured for the Claude
// CLI itself, at user scope (the top-level mcpServers of ~/.claude.json),
// that a workspace opted into its runs with mcp.userServers.
//
// Entry is the CLI's own JSON for the server, verbatim: type, url, headers,
// an oauth block, command, args, env. It can carry credentials, so it is
// never logged and is written to one place only — the per-session MCP
// configuration a provider generates and removes when the session ends.
// Transport, OAuth and Headers are read off it for the decisions a
// provider makes without looking inside: Codex cannot use an OAuth grant
// the Claude CLI holds, and the doctor row names the transport.
type UserMCPServer struct {
	Name      string
	Transport string // "stdio", "http" or "sse"
	OAuth     bool   // the entry carries an oauth block
	Headers   bool   // the entry carries headers (an http server's own auth)
	Entry     json.RawMessage
}

// Label is how the doctor row and a run's notice name the server:
// "slack (http, oauth)", "zoho-desk (stdio)".
func (s UserMCPServer) Label() string {
	t := s.Transport
	if s.OAuth {
		t += ", oauth"
	}
	return s.Name + " (" + t + ")"
}

// ClaudeUserConfigPath is the file the Claude CLI keeps its user-scope
// settings in: $CLAUDE_CONFIG_DIR/.claude.json when that variable is set,
// ~/.claude.json otherwise. env is the environment the CLI will run with; a
// nil env reads this process's.
func ClaudeUserConfigPath(env []string) string {
	if env == nil {
		env = os.Environ()
	}
	if dir := lastEnv(env, "CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	home := lastEnv(env, "HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// LoadClaudeUserServers reads the CLI's user-scope MCP servers, by name. A
// missing file is no servers, not an error: the CLI has simply never had
// one added. A file that does not parse is an error.
func LoadClaudeUserServers(env []string) (map[string]UserMCPServer, error) {
	path := ClaudeUserConfigPath(env)
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make(map[string]UserMCPServer, len(f.MCPServers))
	for name, raw := range f.MCPServers {
		out[name] = describeUserServer(name, raw)
	}
	return out, nil
}

// describeUserServer reads the shape of one entry without keeping any of
// its values but the verbatim bytes.
func describeUserServer(name string, raw json.RawMessage) UserMCPServer {
	var e struct {
		Type    string          `json:"type"`
		URL     string          `json:"url"`
		OAuth   json.RawMessage `json:"oauth"`
		Headers map[string]any  `json:"headers"`
	}
	_ = json.Unmarshal(raw, &e)
	t := strings.ToLower(strings.TrimSpace(e.Type))
	if t == "" {
		t = "stdio"
		if e.URL != "" {
			t = "http"
		}
	}
	oauth := len(e.OAuth) > 0 && string(e.OAuth) != "null"
	return UserMCPServer{Name: name, Transport: t, OAuth: oauth, Headers: len(e.Headers) > 0, Entry: raw}
}

// PickUserServers returns the named servers in the order they were named,
// or an error naming the ones the CLI does not have and the ones it does.
func PickUserServers(all map[string]UserMCPServer, names []string) ([]UserMCPServer, error) {
	var out []UserMCPServer
	var missing []string
	for _, n := range names {
		s, ok := all[n]
		if !ok {
			missing = append(missing, n)
			continue
		}
		out = append(out, s)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s not among the Claude CLI's user-scope MCP servers (it has: %s)",
			strings.Join(quoteAll(missing), ", "), userServerNames(all))
	}
	return out, nil
}

// UserServersFor reads the CLI's user scope from env and picks names out
// of it. No names reads nothing.
func UserServersFor(env []string, names []string) ([]UserMCPServer, error) {
	if len(names) == 0 {
		return nil, nil
	}
	all, err := LoadClaudeUserServers(env)
	if err != nil {
		return nil, err
	}
	return PickUserServers(all, names)
}

func userServerNames(all map[string]UserMCPServer) string {
	if len(all) == 0 {
		return "none"
	}
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

func lastEnv(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], prefix); ok {
			return v
		}
	}
	return ""
}

// WriteMCPConfig writes the MCP configuration a strict session runs
// against: the servers of workspaceFile (a .mcp.json, or "" for none) and
// the user servers, verbatim, under one mcpServers object. It goes in a new
// private temporary directory, which the returned cleanup removes; the file
// is owner-only because a user entry can carry an env token or a header.
// A user server with the same name as a workspace server is an error
// rather than a silent override of either.
func WriteMCPConfig(workspaceFile string, user []UserMCPServer) (path string, cleanup func(), err error) {
	servers := map[string]json.RawMessage{}
	if workspaceFile != "" {
		b, err := os.ReadFile(workspaceFile)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", nil, fmt.Errorf("read %s: %w", workspaceFile, err)
		}
		if err == nil {
			var f struct {
				MCPServers map[string]json.RawMessage `json:"mcpServers"`
			}
			if err := json.Unmarshal(b, &f); err != nil {
				return "", nil, fmt.Errorf("parse %s: %w", workspaceFile, err)
			}
			for k, v := range f.MCPServers {
				servers[k] = v
			}
		}
	}
	for _, s := range user {
		if _, dup := servers[s.Name]; dup {
			return "", nil, fmt.Errorf("mcp.userServers: %q is also declared in the workspace's .mcp.json; rename one", s.Name)
		}
		servers[s.Name] = s.Entry
	}
	body, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "sirdar-mcp-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	if err := os.Chmod(dir, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	path = filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}
