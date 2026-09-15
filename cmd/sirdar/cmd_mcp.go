package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

// exitRefused is the status `sirdar mcp call` exits with when the
// workspace's own permissions refuse the tool. It is deliberately the same
// number as a usage error: both mean "this invocation did not happen", and
// a caller scripting the command only needs non-zero to tell that apart
// from a call that ran.
const exitRefused = 2

const mcpUsage = "usage: sirdar mcp list [--connect]\n" +
	"       sirdar mcp tools SERVER\n" +
	"       sirdar mcp call SERVER TOOL [--args '<json>']"

func init() { commands["mcp"] = cmdMCP }

// cmdMCP shows what a run's MCP access would be, without starting a run:
// which servers the workspace declares, which of their tools the
// workspace's permissions allow, and — for an allowed one — what the tool
// actually answers.
func cmdMCP(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, mcpUsage)
		return exitUsage
	}
	switch args[0] {
	case "list":
		return cmdMCPList(args[1:], stdout, stderr)
	case "tools":
		return cmdMCPTools(args[1:], stdout, stderr)
	case "call":
		return cmdMCPCall(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "sirdar mcp: unknown subcommand %q; use list, tools, or call\n", args[0])
		return exitUsage
	}
}

func cmdMCPList(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("mcp list", stderr, "usage: sirdar mcp list [--connect]")
	connect := fs.Bool("connect", false, "start each server, initialize, and count its tools")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}
	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	ctx, stop := interruptible()
	defer stop()

	inv, err := app.MCPServersFor(ctx, cfg, *connect)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	for _, w := range inv.Warnings {
		fmt.Fprintf(stderr, "sirdar: %s\n", w)
	}
	if len(inv.Servers) == 0 {
		fmt.Fprintf(stdout, "no MCP servers: %s declares none\n", cfg.Root+"/.mcp.json")
		return 0
	}

	for _, s := range inv.Servers {
		where := s.URL
		if where == "" {
			where = strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", s.Name, s.Scope, s.Transport, where)
		if len(s.EnvKeys) > 0 {
			fmt.Fprintf(stdout, "\tenv: %s\n", strings.Join(s.EnvKeys, ", "))
		}
		if len(s.HeaderKeys) > 0 {
			fmt.Fprintf(stdout, "\theaders: %s\n", strings.Join(s.HeaderKeys, ", "))
		}
		if s.Note != "" {
			fmt.Fprintf(stdout, "\tnote: %s\n", s.Note)
		}
		switch {
		case !*connect:
		case s.Error != "":
			fmt.Fprintf(stdout, "\tfailed after %dms: %s\n", s.TookMs, s.Error)
		case s.Connected:
			fmt.Fprintf(stdout, "\t%d tool(s) in %dms\n", s.Tools, s.TookMs)
		}
	}
	fmt.Fprintln(stdout, mcpRuleLine(inv.Permissions))
	if !inv.WorkspaceOnly {
		fmt.Fprintln(stdout, "mcp.workspaceOnly is off, so a run sees the global servers above too")
	}
	return 0
}

func cmdMCPTools(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("mcp tools", stderr, "usage: sirdar mcp tools SERVER")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	ctx, stop := interruptible()
	defer stop()

	list, err := app.MCPToolsFor(ctx, cfg, positional[0])
	if err != nil {
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	for _, t := range list.Tools {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", t.FullName, t.Verdict, t.Reason)
	}
	fmt.Fprintf(stdout, "%d tool(s) in %dms; %s\n", len(list.Tools), list.TookMs, mcpRuleLine(list.Permissions))
	return 0
}

func cmdMCPCall(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("mcp call", stderr, "usage: sirdar mcp call SERVER TOOL [--args '<json>']")
	argsJSON := fs.String("args", "", "the tool's arguments, as a JSON object (default {})")
	positional, ok := parseFlags(fs, args, 2, 2, stderr)
	if !ok {
		return exitUsage
	}
	var raw json.RawMessage
	if strings.TrimSpace(*argsJSON) != "" {
		var probe map[string]any
		if err := json.Unmarshal([]byte(*argsJSON), &probe); err != nil {
			fmt.Fprintf(stderr, "sirdar mcp call: --args must be a JSON object: %v\n", err)
			return exitUsage
		}
		raw = json.RawMessage(*argsJSON)
	}

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	ctx, stop := interruptible()
	defer stop()

	res, err := app.MCPCallFor(ctx, cfg, positional[0], positional[1], raw)
	switch {
	case errors.Is(err, app.ErrMCPDenied):
		fmt.Fprintf(stderr, "sirdar: refused: %s is %s: %s\n",
			positional[1], res.Verdict, res.Reason)
		return exitRefused
	case err != nil:
		fmt.Fprintf(stderr, "sirdar: %v\n", err)
		return 1
	}
	if res.Error != "" {
		fmt.Fprintf(stderr, "sirdar: %s failed after %dms: %s\n", positional[1], res.TookMs, res.Error)
		return 1
	}
	if res.Result != "" {
		fmt.Fprintln(stdout, res.Result)
	}
	if res.Truncated {
		fmt.Fprintf(stdout, "truncated at %d bytes\n", app.MCPResultLimit)
	}
	if res.IsError {
		fmt.Fprintf(stderr, "sirdar: the tool reported a failure of its own (%dms)\n", res.TookMs)
		return 1
	}
	fmt.Fprintf(stdout, "%s in %dms (%s)\n", res.Verdict, res.TookMs, res.Reason)
	return 0
}

// mcpRuleLine says which rule the verdicts on this workspace came from.
func mcpRuleLine(patterns []string) string {
	if len(patterns) == 0 {
		return "permissions.mcp is empty, so each tool is judged by its name"
	}
	return fmt.Sprintf("permissions.mcp: %s", strings.Join(patterns, ", "))
}
