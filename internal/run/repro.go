package run

import (
	"encoding/json"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/mcpclient"
	"github.com/srivathsanvenkateswaran/sirdar/internal/store"
)

// reproPlan is what a run that reproduces on staging hands each session's
// guard: the browser server, the hosts it may navigate to, and whether
// browser_evaluate may run unasked while the browser is on one of them.
type reproPlan struct {
	Server   string
	Hosts    []string
	Evaluate bool
}

// reproProviders are the providers whose permission requests carry the
// tool's arguments, so a guard can read the URL a navigate is going to.
// Codex asks through an MCP elicitation that names the tool and nothing
// else, and cursor and agy put no browser call to Sirdar at all; a guard on
// any of them would be judging calls it cannot see.
var reproProviders = map[string]bool{"claude": true, "openai": true, "qwen": true, "acp": true}

// reproFor settles once per prepared run whether staging reproduction is
// active, and why not. A nil plan with an empty reason is a workspace that
// configured no repro hosts, which says nothing. Steers and resumes build
// their prepared without prepare, so the first session spec settles it for
// them.
func (r *Runner) reproFor(p *prepared) (guard *reproPlan, reason string) {
	if p.reproRead {
		return p.repro, p.reproReason
	}
	p.reproRead = true
	p.repro, p.reproReason = r.settleRepro(p)
	return p.repro, p.reproReason
}

// settleRepro checks spec §4.1's conditions in order, so the reason a
// run gives is the first one that failed.
func (r *Runner) settleRepro(p *prepared) (*reproPlan, string) {
	cfg := r.Config
	if !cfg.ReproConfigured() {
		return nil, ""
	}
	if !kindListed(cfg.Repro.Kinds, p.kind) {
		return nil, "kind " + string(p.kind) + " is not in repro.kinds"
	}
	if p.state.Eval {
		return nil, "eval runs do not reproduce"
	}
	args, ok := r.sessionServerArgs(p, cfg.Repro.Server)
	if !ok {
		return nil, "mcp server " + cfg.Repro.Server + " is not configured for this session"
	}
	if name := r.providerName(); !reproProviders[name] {
		return nil, "provider " + name + " does not hand Sirdar the call's arguments"
	}
	return &reproPlan{
		Server:   cfg.Repro.Server,
		Hosts:    append([]string(nil), cfg.Repro.Hosts...),
		Evaluate: allowedOrigins(args),
	}, ""
}

func kindListed(kinds []string, k store.Kind) bool {
	for _, want := range kinds {
		if want == string(k) {
			return true
		}
	}
	return false
}

// sessionServerArgs finds the named server among the ones this run's
// session is offered — the workspace's .mcp.json, the operator's global
// files when mcp.workspaceOnly is off, and the Claude CLI servers
// mcp.userServers carries — and returns the args it is started with. A
// name declared in both is refused when the session starts, so the order
// they are looked in does not decide anything. A .mcp.json that does not
// parse offers no server at all.
func (r *Runner) sessionServerArgs(p *prepared, server string) ([]string, bool) {
	cfg := r.Config
	entries, _, err := mcpclient.Inventory(cfg.Root, r.childEnv(), !cfg.WorkspaceOnlyMCP())
	if err == nil {
		for _, e := range entries {
			if e.Name == server {
				return e.Args, true
			}
		}
	}
	// A user server that cannot be read fails the run when execute asks
	// for it; here it only means the browser is not among them.
	_ = r.userServers(p)
	for _, s := range p.userMCP {
		if s.Name != server {
			continue
		}
		var entry struct {
			Args []string `json:"args"`
		}
		_ = json.Unmarshal(s.Entry, &entry)
		return entry.Args, true
	}
	return nil, false
}

// allowedOrigins reports whether a Playwright server is started with
// --allowed-origins, in either the two-element or the = form. With it the
// browser itself refuses other origins, which is what makes running page
// script unasked tolerable (spec §9); without it browser_evaluate is put to
// the operator like any other call.
func allowedOrigins(args []string) bool {
	for _, a := range args {
		if a == "--allowed-origins" || strings.HasPrefix(a, "--allowed-origins=") {
			return true
		}
	}
	return false
}
