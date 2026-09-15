package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// ErrNoSuchMCPServer is returned for a server name the workspace does not
// configure. The HTTP layer answers 404.
var ErrNoSuchMCPServer = errors.New("app: no such mcp server")

// ErrMCPDenied is returned by MCPCall for a tool the workspace's own
// permissions would refuse a run. The HTTP layer answers 403, and the CLI
// exits 2; the reason travels with it in the result.
var ErrMCPDenied = errors.New("app: mcp tool denied")

// MCPResultLimit caps how much of a tool's output is returned, in bytes.
// A tool that answers with a whole table is the reason there is a cap at
// all; the result says when it bit.
const MCPResultLimit = 64 << 10

// mcpConnectTimeout bounds one server's handshake and tools/list when a
// caller asked for them. A server that has not answered by then is
// reported as an error rather than left holding the request open.
const mcpConnectTimeout = 30 * time.Second

// MCPServer is one configured server as an operator reads it. The
// configuration half is mcpclient's, which carries the rule that key names
// cross and values never do; the rest is what connecting to it found out.
type MCPServer struct {
	mcpclient.Entry
	// Connected is true when --connect reached the server. It and the
	// three fields below are zero when no connection was attempted.
	Connected bool `json:"connected,omitempty"`
	// Tools is how many tools the server listed.
	Tools int `json:"tools,omitempty"`
	// TookMs is how long the handshake and the listing took together.
	TookMs int64 `json:"tookMs,omitempty"`
	// Error is why the connection failed, with the entry's own
	// credentials taken out of it.
	Error string `json:"error,omitempty"`
}

// MCPInventory is the answer to "which MCP servers would a run here get",
// with the permission rule those servers' tools will be judged by.
type MCPInventory struct {
	Servers  []MCPServer `json:"servers"`
	Warnings []string    `json:"warnings"`
	// WorkspaceOnly is mcp.workspaceOnly. With it off, the operator's own
	// global servers are in the list too, scoped "global".
	WorkspaceOnly bool `json:"workspaceOnly"`
	// Permissions is permissions.mcp as configured. Empty means the name
	// heuristic decides.
	Permissions []string `json:"permissions"`
}

// MCPTool is one of a server's tools with the verdict a run would get for
// it. Name is the tool's own; FullName is what a session sees and what
// permissions.mcp patterns are written against.
type MCPTool struct {
	Name        string `json:"name"`
	FullName    string `json:"fullName"`
	Description string `json:"description,omitempty"`
	// Verdict is "allowed" or "denied".
	Verdict string `json:"verdict"`
	// Rule names which rule settled it (see provider.MCPRule).
	Rule string `json:"rule"`
	// Reason is that rule in a sentence.
	Reason string `json:"reason"`
}

// MCPToolList is every tool on one server, judged.
type MCPToolList struct {
	Server      string    `json:"server"`
	Tools       []MCPTool `json:"tools"`
	TookMs      int64     `json:"tookMs"`
	Permissions []string  `json:"permissions"`
}

// MCPCallResult is one hand-run tool call.
type MCPCallResult struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	// Verdict is "allowed" or "denied", and Reason is why.
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	// Result is the tool's output, flattened to text and capped at
	// MCPResultLimit. Truncated says whether the cap bit.
	Result    string `json:"result,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	// IsError is the server's own flag: the tool ran and reported a
	// failure of its own, which is not the same as the call failing.
	IsError bool `json:"isError,omitempty"`
	// Error is a call that did not happen: a transport failure, a
	// timeout, a JSON-RPC error.
	Error  string `json:"error,omitempty"`
	TookMs int64  `json:"tookMs"`
}

// The two verdict words, which the CLI prints and the API returns.
const (
	MCPAllowed = "allowed"
	MCPDenied  = "denied"
)

// MCPServers lists the MCP servers a run in this workspace would be
// offered, optionally connecting to each.
func (s *Service) MCPServers(ctx context.Context, wsID string, connect bool) (MCPInventory, error) {
	_, cfg, err := s.load(wsID)
	if err != nil {
		return MCPInventory{}, err
	}
	return MCPServersFor(ctx, cfg, connect)
}

// MCPTools lists one server's tools with the verdict a run would get.
func (s *Service) MCPTools(ctx context.Context, wsID, server string) (MCPToolList, error) {
	if err := checkID(ErrNoSuchMCPServer, "mcp server", server); err != nil {
		return MCPToolList{}, err
	}
	_, cfg, err := s.load(wsID)
	if err != nil {
		return MCPToolList{}, err
	}
	return MCPToolsFor(ctx, cfg, server)
}

// MCPCall runs one tool by hand, refusing it exactly as a run would.
func (s *Service) MCPCall(ctx context.Context, wsID, server, tool string, args json.RawMessage) (MCPCallResult, error) {
	if err := checkID(ErrNoSuchMCPServer, "mcp server", server); err != nil {
		return MCPCallResult{}, err
	}
	_, cfg, err := s.load(wsID)
	if err != nil {
		return MCPCallResult{}, err
	}
	return MCPCallFor(ctx, cfg, server, tool, args)
}

// MCPServersFor is MCPServers against a loaded configuration, which is how
// the CLI reaches it: `sirdar mcp` works in the workspace the operator is
// standing in and never touches the workspace registry.
func MCPServersFor(ctx context.Context, cfg *config.Config, connect bool) (MCPInventory, error) {
	entries, warnings, err := mcpEntries(cfg)
	if err != nil {
		return MCPInventory{}, err
	}
	inv := MCPInventory{
		Servers:       make([]MCPServer, 0, len(entries)),
		Warnings:      warnings,
		WorkspaceOnly: cfg.WorkspaceOnlyMCP(),
		Permissions:   cfg.Permissions.MCP,
	}
	for _, e := range entries {
		row := MCPServer{Entry: e}
		if connect && e.Usable() {
			started := time.Now()
			conn, tools, err := connectAndList(ctx, e)
			row.TookMs = time.Since(started).Milliseconds()
			if conn != nil {
				_ = conn.Close()
			}
			if err != nil {
				row.Error = err.Error()
			} else {
				row.Connected = true
				row.Tools = len(tools)
			}
		}
		inv.Servers = append(inv.Servers, row)
	}
	return inv, nil
}

// MCPToolsFor is MCPTools against a loaded configuration.
func MCPToolsFor(ctx context.Context, cfg *config.Config, server string) (MCPToolList, error) {
	entry, err := findMCPServer(cfg, server)
	if err != nil {
		return MCPToolList{}, err
	}
	started := time.Now()
	conn, tools, err := connectAndList(ctx, entry)
	if conn != nil {
		defer conn.Close()
	}
	if err != nil {
		return MCPToolList{}, err
	}

	out := MCPToolList{
		Server:      server,
		Tools:       make([]MCPTool, 0, len(tools)),
		Permissions: cfg.Permissions.MCP,
	}
	for _, t := range tools {
		full := mcpclient.ToolName(server, t.Name)
		v := provider.DecideMCPTool(full, cfg.Permissions.MCP)
		out.Tools = append(out.Tools, MCPTool{
			Name:        t.Name,
			FullName:    full,
			Description: t.Description,
			Verdict:     verdictWord(v.Allow),
			Rule:        string(v.Rule),
			Reason:      v.Reason(),
		})
	}
	out.TookMs = time.Since(started).Milliseconds()
	return out, nil
}

// MCPCallFor is MCPCall against a loaded configuration.
//
// The verdict is taken before anything is started: a denied tool never has
// its server spawned, let alone its call sent. The error returned for one
// is ErrMCPDenied, and the result beside it carries the same reason
// `sirdar mcp tools` would have printed.
func MCPCallFor(ctx context.Context, cfg *config.Config, server, tool string, args json.RawMessage) (MCPCallResult, error) {
	entry, err := findMCPServer(cfg, server)
	if err != nil {
		return MCPCallResult{}, err
	}
	full := mcpclient.ToolName(server, tool)
	v := provider.DecideMCPTool(full, cfg.Permissions.MCP)
	res := MCPCallResult{Server: server, Tool: tool, Verdict: verdictWord(v.Allow), Reason: v.Reason()}
	if !v.Allow {
		return res, fmt.Errorf("%w: %s: %s", ErrMCPDenied, full, v.Reason())
	}

	started := time.Now()
	cctx, cancel := context.WithTimeout(ctx, mcpConnectTimeout)
	conn, err := mcpclient.Connect(cctx, entry, nil)
	cancel()
	if err != nil {
		res.TookMs = time.Since(started).Milliseconds()
		res.Error = err.Error()
		return res, nil
	}
	defer conn.Close()

	out, isErr, err := conn.CallTool(ctx, tool, args)
	res.TookMs = time.Since(started).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	res.IsError = isErr
	res.Result, res.Truncated = capResult(out)
	return res, nil
}

// capResult cuts output to MCPResultLimit bytes, on a rune boundary so the
// tail is not half a character.
func capResult(s string) (string, bool) {
	if len(s) <= MCPResultLimit {
		return s, false
	}
	cut := MCPResultLimit
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// utf8Start reports whether b begins a UTF-8 encoded rune.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func verdictWord(allow bool) string {
	if allow {
		return MCPAllowed
	}
	return MCPDenied
}

// mcpEntries reads the workspace's servers, and the operator's global ones
// when mcp.workspaceOnly is off — which is exactly what a session on this
// workspace would see.
//
// The environment ${VAR} expands from is this process's, not a run's: a
// run's child environment has had the workspace's own credentials stripped
// out of it, so a server whose command line names one may expand
// differently here than it would in a session. That is the direction that
// fails safe — a hand-run call sees at most what the operator's own shell
// can — and the docs say so.
func mcpEntries(cfg *config.Config) ([]mcpclient.Entry, []string, error) {
	return mcpclient.Inventory(cfg.Root, os.Environ(), !cfg.WorkspaceOnlyMCP())
}

// findMCPServer resolves one server name against the workspace's
// configuration, reporting ErrNoSuchMCPServer for a name that is not there
// and a plain error for one that is configured but cannot be connected to.
func findMCPServer(cfg *config.Config, server string) (mcpclient.Entry, error) {
	entries, _, err := mcpEntries(cfg)
	if err != nil {
		return mcpclient.Entry{}, err
	}
	for _, e := range entries {
		if e.Name != server {
			continue
		}
		if !e.Usable() {
			return mcpclient.Entry{}, fmt.Errorf("mcp server %q: %s", server, e.Note)
		}
		return e, nil
	}
	return mcpclient.Entry{}, fmt.Errorf("%w: %s", ErrNoSuchMCPServer, server)
}

// connectAndList starts one server and lists its tools under one timeout.
// The connection is returned even when listing failed, so the caller can
// close it.
func connectAndList(ctx context.Context, e mcpclient.Entry) (mcpclient.Conn, []mcpclient.ToolInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, mcpConnectTimeout)
	defer cancel()

	// A server's own stderr is discarded rather than forwarded: it is the
	// one stream Sirdar does not control the content of, and a server that
	// logs its configuration would put a credential on the terminal.
	conn, err := mcpclient.Connect(ctx, e, nil)
	if err != nil {
		return nil, nil, err
	}
	tools, err := conn.ListTools(ctx)
	if err != nil {
		return conn, nil, err
	}
	return conn, tools, nil
}
