package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope says where a server entry was configured. A run governed by
// mcp.workspaceOnly (the default) sees the workspace scope alone; with it
// off, the operator's own global servers are in the session too, which is
// why they are worth listing.
const (
	ScopeWorkspace = "workspace"
	ScopeGlobal    = "global"
)

// The transports an entry can declare. Sirdar starts stdio servers itself
// and speaks streamable HTTP to an http one; sse is recognised only so an
// entry using it is reported rather than silently missing.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"
)

// Entry is one server as configured, described for a person to read.
//
// Command, Args and URL are carried verbatim from the file, never
// expanded: a ${TOKEN} in an argument expands to the token itself, and
// this struct is what `sirdar mcp list` and the API print. Env and
// Headers are reduced to their key names for the same reason — the value
// is the secret, and the name is what an operator needs to check. The
// expanded forms live in the unexported fields, which only Connect reads.
type Entry struct {
	Name      string   `json:"name"`
	Scope     string   `json:"scope"`
	Transport string   `json:"transport"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	URL       string   `json:"url,omitempty"`
	// EnvKeys names the environment variables the entry sets for the
	// server, in sorted order. Names only.
	EnvKeys []string `json:"envKeys,omitempty"`
	// HeaderKeys names the HTTP headers an http entry sends, sorted.
	// Names only: an Authorization header's value is the credential.
	HeaderKeys []string `json:"headerKeys,omitempty"`
	// Source is the file this entry was read from.
	Source string `json:"source"`
	// Note is what an operator should know about this entry before
	// trusting the row: an unsupported transport, a missing command, a
	// name no run can namespace. Empty when there is nothing to say.
	Note string `json:"note,omitempty"`

	stdio    ServerConfig
	http     httpConfig
	usable   bool
	secretsV []string
}

// Usable reports whether Connect can reach this entry at all.
func (e Entry) Usable() bool { return e.usable }

// secrets returns the values this entry hands the server — expanded env
// values and header values — so anything the server says back can have
// them taken out of it before it reaches a terminal or a log.
func (e Entry) secrets() []string { return e.secretsV }

// Inventory describes every MCP server a run in root would be offered,
// in workspace-then-global order and by name within each. Warnings name
// the entries that were read but cannot be used, and a file that could
// not be parsed at all.
//
// includeGlobal is the workspace's mcp.workspaceOnly, inverted: with
// workspaceOnly on (the default) a session sees the workspace's servers
// and no others, so the global files are not read.
//
// env is the environment ${VAR} in the file expands from — a run's own
// child environment, which has had the workspace's credentials stripped
// out of it. A missing workspace .mcp.json is not an error; a malformed
// one is.
func Inventory(root string, env []string, includeGlobal bool) ([]Entry, []string, error) {
	entries, warnings, err := entriesFrom(filepath.Join(root, ".mcp.json"), ScopeWorkspace, root, env)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	if !includeGlobal {
		return entries, warnings, nil
	}

	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		seen[e.Name] = true
	}
	for _, path := range GlobalConfigPaths() {
		global, warn, err := entriesFrom(path, ScopeGlobal, root, env)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			// An operator's own file that Sirdar cannot read is a
			// warning, never a refusal: the workspace's own servers are
			// still worth listing.
			warnings = append(warnings, err.Error())
			continue
		}
		warnings = append(warnings, warn...)
		for _, e := range global {
			if seen[e.Name] {
				warnings = append(warnings, fmt.Sprintf("mcp server %q in %s: a workspace server of the same name is listed instead", e.Name, path))
				continue
			}
			seen[e.Name] = true
			entries = append(entries, e)
		}
	}
	return entries, warnings, nil
}

// GlobalConfigPaths are the user-level files Sirdar reads an operator's
// own MCP servers from, in the order they are consulted. They are the
// JSON `mcpServers` files the agent CLIs Sirdar drives keep in the home
// directory; Codex's TOML config is not among them, and a workspace on
// provider codex with mcp.workspaceOnly off will not see its servers
// listed here.
func GlobalConfigPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude.json"),                         // Claude Code, user scope
		filepath.Join(home, ".mcp.json"),                            // a home-directory .mcp.json
		filepath.Join(home, ".gemini", "config", "mcp_config.json"), // Antigravity / Gemini CLI
	}
}

// entriesFrom reads one mcpServers file. root is the workspace directory
// a stdio server is started in, whichever file declared it.
func entriesFrom(path, scope, root string, env []string) ([]Entry, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
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

	lookup := envLookup(env)
	var entries []Entry
	var warnings []string
	for _, name := range names {
		e, warn := describe(name, f.MCPServers[name], scope, path, root, env, lookup)
		entries = append(entries, e)
		warnings = append(warnings, warn...)
	}
	return entries, warnings, nil
}

// describe turns one .mcp.json entry into the row an operator reads and,
// when Sirdar can reach it, the connection details Connect needs.
func describe(name string, m mcpEntry, scope, source, root string, env []string, lookup func(string) (string, bool)) (Entry, []string) {
	e := Entry{
		Name:       name,
		Scope:      scope,
		Command:    m.Command,
		Args:       append([]string(nil), m.Args...),
		URL:        m.URL,
		EnvKeys:    sortedKeys(m.Env),
		HeaderKeys: sortedKeys(m.Headers),
		Source:     source,
	}
	e.Transport = transportOf(m)

	var warnings []string
	warn := func(note string) {
		e.Note = note
		warnings = append(warnings, fmt.Sprintf("mcp server %q in %s: %s", name, source, note))
	}
	switch {
	case !validServerName(name):
		warn("the name must be letters, digits, - or _ (and no __), so no run can namespace its tools")
		return e, warnings
	case e.Transport != TransportStdio && e.Transport != TransportHTTP:
		warn("the " + e.Transport + " transport is not supported (stdio and http only)")
		return e, warnings
	case e.Transport == TransportHTTP && strings.TrimSpace(m.URL) == "":
		warn("no url")
		return e, warnings
	case e.Transport == TransportStdio && strings.TrimSpace(m.Command) == "":
		warn("no command")
		return e, warnings
	}

	var missing []string
	expand := func(s string) string { return expandFrom(s, lookup, &missing) }
	if e.Transport == TransportHTTP {
		e.http = httpConfig{URL: expand(m.URL), Headers: expandMap(m.Headers, expand)}
		e.secretsV = values(e.http.Headers)
		e.Note = "http transport: Sirdar's own agent loop (provider: openai) starts stdio servers only, " +
			"so a run on that provider will not have this server's tools"
	} else {
		cfg := ServerConfig{Name: name, Command: m.Command, Root: root, BaseEnv: env}
		for _, a := range m.Args {
			cfg.Args = append(cfg.Args, expand(a))
		}
		cfg.Env = expandMap(m.Env, expand)
		e.stdio = cfg
		e.secretsV = values(cfg.Env)
	}
	e.usable = true
	for _, v := range dedupe(missing) {
		warnings = append(warnings, fmt.Sprintf("mcp server %q in %s: %s is not set in the session environment, expanded to the empty string", name, source, v))
	}
	return e, warnings
}

// transportOf reads the entry's declared transport, falling back to the
// shape of the entry itself: a url and no command is a remote server even
// when the file names no type, which is how .mcp.json is usually written.
func transportOf(m mcpEntry) string {
	switch kind := strings.ToLower(strings.TrimSpace(m.Type)); kind {
	case TransportStdio, TransportHTTP, TransportSSE:
		return kind
	case "streamable-http", "streamablehttp":
		return TransportHTTP
	case "":
		if strings.TrimSpace(m.URL) != "" {
			return TransportHTTP
		}
		return TransportStdio
	default:
		return kind
	}
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func expandMap(m map[string]string, expand func(string) string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for _, k := range sortedKeys(m) {
		out[k] = expand(m[k])
	}
	return out
}

// values returns the non-empty values of m, which are the secrets an
// entry hands its server.
func values(m map[string]string) []string {
	var out []string
	for _, k := range sortedKeys(m) {
		if v := m[k]; v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Conn is a live connection to one MCP server: what `sirdar mcp` needs of
// it, and what both transports provide.
type Conn interface {
	ServerInfo() ServerInfo
	ListTools(ctx context.Context) ([]ToolInfo, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error)
	Close() error
}

var (
	_ Conn = (*Client)(nil)
	_ Conn = (*httpClient)(nil)
)

// Connect opens a connection to one entry and performs the initialize
// handshake: a subprocess for a stdio entry, a streamable-HTTP session for
// an http one. stderr, which may be nil, receives a stdio server's own
// stderr.
//
// Everything the server says back — tool output, and the text of any error
// — has the entry's own credentials taken out of it first, so a server
// that echoes its token cannot put it on an operator's terminal.
func Connect(ctx context.Context, e Entry, stderr io.Writer) (Conn, error) {
	if !e.usable {
		note := e.Note
		if note == "" {
			note = "this entry cannot be connected to"
		}
		return nil, fmt.Errorf("mcp server %q: %s", e.Name, note)
	}
	var (
		conn Conn
		err  error
	)
	if e.Transport == TransportHTTP {
		conn, err = startHTTP(ctx, e.Name, e.http)
	} else {
		conn, err = Start(ctx, e.stdio, stderr)
	}
	if err != nil {
		return nil, errors.New(Redact(err.Error(), e.secrets()))
	}
	return &redacting{Conn: conn, secrets: e.secrets()}, nil
}

// Redact replaces every occurrence of each secret with "[redacted]".
// Empty and one-character secrets are left alone: they would match
// everywhere and say nothing.
func Redact(s string, secrets []string) string {
	for _, secret := range secrets {
		if len(secret) < 2 {
			continue
		}
		s = strings.ReplaceAll(s, secret, "[redacted]")
	}
	return s
}

// redacting wraps a connection so no credential the entry handed the
// server can come back out of it.
type redacting struct {
	Conn
	secrets []string
}

func (r *redacting) ListTools(ctx context.Context) ([]ToolInfo, error) {
	tools, err := r.Conn.ListTools(ctx)
	if err != nil {
		return nil, errors.New(Redact(err.Error(), r.secrets))
	}
	for i := range tools {
		tools[i].Description = Redact(tools[i].Description, r.secrets)
	}
	return tools, nil
}

func (r *redacting) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	out, isErr, err := r.Conn.CallTool(ctx, name, args)
	if err != nil {
		return "", false, errors.New(Redact(err.Error(), r.secrets))
	}
	return Redact(out, r.secrets), isErr, nil
}
