package mcpclient

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

// ServerConfig describes one stdio MCP server. Root is the workspace
// directory: the server's working directory, and the root reported to
// its roots/list requests.
type ServerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Root    string

	// BaseEnv is the environment the core variables (PATH, HOME, LANG)
	// are taken from when the server is started. It is the session's own
	// child environment, so a run that strips a credential strips it
	// here too; nil falls back to this process's environment.
	BaseEnv []string
}

// mcpFile is the shape of <root>/.mcp.json.
type mcpFile struct {
	MCPServers map[string]mcpEntry `json:"mcpServers"`
}

// mcpEntry is one server in .mcp.json. Type is "stdio" or absent for the
// servers Sirdar can run; URL is present only on the remote kinds, which
// is what makes an entry recognisable as remote even when Type is
// missing.
type mcpEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// LoadWorkspaceServers reads <root>/.mcp.json against this process's own
// environment. Prefer LoadWorkspaceServersEnv wherever the session's
// environment is in hand: a run's child environment has had its
// credentials stripped, and expanding from os.Environ puts back exactly
// the variables that were taken out.
func LoadWorkspaceServers(root string) ([]ServerConfig, []string, error) {
	return LoadWorkspaceServersEnv(root, os.Environ())
}

// LoadWorkspaceServersEnv reads <root>/.mcp.json and returns the stdio
// servers it declares, in name order, alongside a warning for every
// entry that was skipped. A missing file is not an error — most
// workspaces have no MCP servers — and yields no servers and no
// warnings. A malformed file is an error, because silently running with
// no tools when the user configured some is worse than failing loudly.
//
// ${VAR} and $VAR in args and env values are expanded from env — the
// environment the session itself runs with — rather than from this
// process's. The two differ by exactly the variables internal/run strips
// as credentials, and a `.mcp.json` the workspace does not control must
// not be able to read one back out. On the Codex path that expansion is
// written to a config.toml on disk, which makes the distinction the
// difference between a secret staying in memory and a secret in a file.
//
// An undefined variable expands to the empty string, as a shell would,
// and earns a warning naming the variable. Warnings name variables and
// never values: the ones worth warning about are the secret ones.
func LoadWorkspaceServersEnv(root string, env []string) ([]ServerConfig, []string, error) {
	lookup := envLookup(env)
	path := filepath.Join(root, ".mcp.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}

	var f mcpFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}

	names := make([]string, 0, len(f.MCPServers))
	for name := range f.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	var servers []ServerConfig
	var warnings []string
	for _, name := range names {
		e := f.MCPServers[name]
		kind := strings.ToLower(strings.TrimSpace(e.Type))
		switch {
		case !validServerName(name):
			warnings = append(warnings, fmt.Sprintf("mcp server %q: name must be letters, digits, - or _ (and no __), skipping", name))
			continue
		case kind != "" && kind != "stdio":
			warnings = append(warnings, fmt.Sprintf("mcp server %q: transport %q is not supported (stdio only), skipping", name, kind))
			continue
		case e.URL != "":
			warnings = append(warnings, fmt.Sprintf("mcp server %q: remote servers (url) are not supported (stdio only), skipping", name))
			continue
		case strings.TrimSpace(e.Command) == "":
			warnings = append(warnings, fmt.Sprintf("mcp server %q: no command, skipping", name))
			continue
		}

		cfg := ServerConfig{Name: name, Command: e.Command, Root: root, BaseEnv: env}
		var missing []string
		expand := func(s string) string { return expandFrom(s, lookup, &missing) }
		for _, a := range e.Args {
			cfg.Args = append(cfg.Args, expand(a))
		}
		if len(e.Env) > 0 {
			cfg.Env = make(map[string]string, len(e.Env))
			keys := make([]string, 0, len(e.Env))
			for k := range e.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				cfg.Env[k] = expand(e.Env[k])
			}
		}
		for _, v := range dedupe(missing) {
			warnings = append(warnings, fmt.Sprintf("mcp server %q: %s is not set in the session environment, expanded to the empty string", name, v))
		}
		servers = append(servers, cfg)
	}
	return servers, warnings, nil
}

// envLookup turns a child environment slice into the lookup os.Expand
// wants. A later entry wins, which is how exec.Cmd reads a duplicated
// variable, so a caller that appends an override gets the override.
func envLookup(env []string) func(string) (string, bool) {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok {
			values[k] = v
		}
	}
	return func(k string) (string, bool) {
		v, ok := values[k]
		return v, ok
	}
}

// expandFrom substitutes ${VAR} and $VAR from lookup, appending the name
// of every variable that was not set to missing.
func expandFrom(s string, lookup func(string) (string, bool), missing *[]string) string {
	if !strings.ContainsRune(s, '$') {
		return s
	}
	return os.Expand(s, func(name string) string {
		v, ok := lookup(name)
		if !ok {
			*missing = append(*missing, name)
			return ""
		}
		return v
	})
}

// dedupe returns names in first-seen order with repeats dropped, so a
// variable used in three of a server's arguments is warned about once.
func dedupe(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// validServerName reports whether a name from .mcp.json is one Sirdar can
// namespace safely. Tool names reach the model as mcp__<server>__<tool>
// and are split back on the first "__" after the prefix, so a name
// carrying "__" — or a space, a colon, anything else — would route a call
// to the wrong server, or to none. Such an entry is skipped with a
// warning rather than renamed: the workspace's permission rules are
// written against the name as configured.
func validServerName(name string) bool {
	if name == "" || strings.Contains(name, nameSep) {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// namePrefix and nameSep bracket the server name in a namespaced tool
// name. The shape matches Claude Code's, so a workspace's existing
// mcp__ permission rules keep meaning what they meant.
const (
	namePrefix = "mcp__"
	nameSep    = "__"
)

// ToolName namespaces a server's tool for the model: mcp__<server>__<tool>.
func ToolName(server, tool string) string {
	return namePrefix + server + nameSep + tool
}

// SplitToolName reverses ToolName, reporting false for anything that is
// not a namespaced MCP tool name (a local tool such as read_file, say).
// The split is at the first separator after the prefix, so a server name
// containing "__" does not round-trip; tool names may contain it freely.
func SplitToolName(s string) (server, tool string, ok bool) {
	rest, found := strings.CutPrefix(s, namePrefix)
	if !found {
		return "", "", false
	}
	i := strings.Index(rest, nameSep)
	if i <= 0 {
		return "", "", false
	}
	server, tool = rest[:i], rest[i+len(nameSep):]
	if tool == "" {
		return "", "", false
	}
	return server, tool, true
}
