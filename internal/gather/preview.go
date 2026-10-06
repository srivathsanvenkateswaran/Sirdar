package gather

import (
	"runtime"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
)

// PreviewRow is one configured step as `sirdar doctor` reports it: what it
// would run with the sample value, and whether a run would let it.
type PreviewRow struct {
	Step, Call, Verdict, Reason string // Verdict: "allowed" | "denied"
	Server                      string
	ServerPresent               bool
}

// The verdict words a preview row carries.
const (
	PreviewAllowed = "allowed"
	PreviewDenied  = "denied"
)

// previewValue stands in for every variable, so a step renders whatever
// the ticket would have said.
const previewValue = "x"

// Preview judges every configured step exactly as Run would — the same
// allow-list, the same MCP verdict, the same server lookup — with every
// variable set to "x", and starts nothing. ghPath is where gh resolves on
// env's PATH, "" when it does not, so a missing gh shows up before a run
// needs it.
func Preview(cfg *config.Config, env []string) (rows []PreviewRow, ghPath string) {
	ghPath, _ = lookPath("gh", env, runtime.GOOS, cfg.Root)
	g := &gatherer{in: Input{Config: cfg, Env: env, GOOS: runtime.GOOS}, cfg: cfg}

	for _, s := range cfg.Gather.Steps {
		values := map[string]string{}
		for _, r := range stepRefs(s) {
			values[r] = previewValue
		}
		row := PreviewRow{Step: s.Name, Verdict: PreviewAllowed}
		deny := func(reason string) {
			row.Verdict, row.Reason = PreviewDenied, reason
		}

		if s.MCP != nil {
			row.Server = s.MCP.Server
			args, err := RenderArgs(s.MCP.Args, values)
			if err != nil {
				deny(err.Error())
				rows = append(rows, row)
				continue
			}
			row.Call = s.MCP.Server + "." + s.MCP.Tool + "(" + string(args) + ")"
			v := provider.DecideMCPTool(mcpclient.ToolName(s.MCP.Server, s.MCP.Tool), cfg.Permissions.MCP)
			if !v.Allow {
				deny(v.Reason())
			} else {
				row.Reason = v.Reason()
			}
			g.invOnce.Do(func() {
				g.inv, _, g.invErr = mcpclient.Inventory(cfg.Root, env, !cfg.WorkspaceOnlyMCP())
			})
			for _, e := range g.inv {
				if e.Name == s.MCP.Server && e.Usable() {
					row.ServerPresent = true
					break
				}
			}
			rows = append(rows, row)
			continue
		}

		argv, err := Render(s.Command, values, cfg.ExpandPath)
		if err != nil {
			deny(err.Error())
			rows = append(rows, row)
			continue
		}
		row.Call = DisplayLine(argv)
		if _, status, reason := g.judgeCommand(stepPolicy(cfg, "", ""), argv); status != "" {
			if status == StatusDenied {
				deny(reason)
			} else {
				row.Reason = reason
			}
		}
		rows = append(rows, row)
	}
	return rows, ghPath
}
