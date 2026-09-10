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

// LoadWorkspaceServers reads <root>/.mcp.json and returns the stdio
// servers it declares, in name order, alongside a warning for every
// entry that was skipped. A missing file is not an error — most
// workspaces have no MCP servers — and yields no servers and no
// warnings. A malformed file is an error, because silently running with
// no tools when the user configured some is worse than failing loudly.
//
// ${VAR} and $VAR in args and env values are expanded from Sirdar's own
// environment; an undefined variable expands to the empty string, the
// same as a shell would.
func LoadWorkspaceServers(root string) ([]ServerConfig, []string, error) {
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

		cfg := ServerConfig{Name: name, Command: e.Command, Root: root}
		for _, a := range e.Args {
			cfg.Args = append(cfg.Args, expand(a))
		}
		if len(e.Env) > 0 {
			cfg.Env = make(map[string]string, len(e.Env))
			for k, v := range e.Env {
				cfg.Env[k] = expand(v)
			}
		}
		servers = append(servers, cfg)
	}
	return servers, warnings, nil
}

// expand substitutes ${VAR} and $VAR from the process environment.
func expand(s string) string {
	if !strings.ContainsRune(s, '$') {
		return s
	}
	return os.Expand(s, os.Getenv)
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
