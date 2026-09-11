package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/zohodesk"
)

// doctorTimeout caps each source check, so one unreachable adapter or API
// cannot hang the whole report.
const doctorTimeout = 30 * time.Second

// RunDoctor checks everything a run in this workspace depends on: the
// configuration, the agent binary, every configured source, which MCP
// servers a session will see, the notes directory and the note templates.
// Both the `sirdar doctor` command and the desktop Settings screen read the
// same list.
func RunDoctor(ctx context.Context, cfg *config.Config) []Check {
	checks := []Check{{Name: "config", OK: true, Detail: filepath.Join(cfg.Root, ".sirdar", "config.yaml")}}
	checks = append(checks, providerChecks(ctx, cfg)...)
	checks = append(checks, sourceChecks(ctx, cfg)...)
	checks = append(checks, mcpCheck(cfg), fetchCheck(cfg))
	checks = append(checks, notesCheck(cfg), templatesCheck(cfg))
	return checks
}

// checkOf converts a provider diagnostic into the wire shape.
func checkOf(c provider.Check) Check {
	return Check{Name: c.Name, OK: c.OK, Detail: c.Detail}
}

func providerChecks(ctx context.Context, cfg *config.Config) []Check {
	p, err := ProviderFor(cfg, config.Resolver{Keychain: KeychainFor()})
	if err != nil {
		return []Check{{Name: "provider", Detail: err.Error()}}
	}
	// provider: openai has no binary to find — the loop runs in this
	// process — so its Doctor ignores the argument and probes the endpoint
	// instead.
	binary := cfg.Providers.Claude.Path
	switch cfg.Provider {
	case "codex":
		binary = cfg.Providers.Codex.Path
	case "openai", "acp":
		// provider: acp has no path setting either: the agent's launch
		// command is acp.command, which the provider already holds.
		binary = ""
	case "qwen":
		binary = ""
		if cfg.Qwen != nil {
			binary = cfg.Qwen.Path
		}
	}
	if binary != "" {
		binary = cfg.ExpandPath(binary)
	}
	raw := doctorChecks(ctx, p, binary, cfg)
	out := make([]Check, 0, len(raw)+1)
	for _, c := range raw {
		out = append(out, checkOf(c))
	}
	if cfg.Provider == "claude" {
		out = append(out, claudeEnvironmentCheck(cfg))
	}
	return out
}

// doctorChecks runs a provider's diagnostics, handing it the workspace
// when it is the kind of provider that depends on one.
//
// Codex is: which MCP servers a session will see depends on the
// workspace's .mcp.json and its mcp.workspaceOnly setting, neither of
// which reaches Doctor(ctx, binary). Left to infer them it used the
// process working directory and an environment variable, which is right
// for `sirdar doctor` run inside a workspace and wrong for the desktop
// app and `sirdar serve`, whose working directory is wherever they were
// launched from — the row read "none" in both.
func doctorChecks(ctx context.Context, p provider.Provider, binary string, cfg *config.Config) []provider.Check {
	if cd, ok := p.(provider.ConfigDoctor); ok {
		return cd.DoctorWithConfig(ctx, binary, provider.DoctorConfig{
			Root:             cfg.Root,
			MCPWorkspaceOnly: cfg.WorkspaceOnlyMCP(),
		})
	}
	return p.Doctor(ctx, binary)
}

// claudeGatewayEnvVars mirrors the list internal/provider/claude's childEnv
// strips from the agent's environment under subscription billing. Doctor
// checks the operator's own process environment for the same names, since
// a run that sets no spec.Env inherits os.Environ() verbatim.
var claudeGatewayEnvVars = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_CUSTOM_HEADERS",
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY",
}

// claudeEnvironmentCheck flags the credential-leak configuration in
// docs/research/providers/spike-anthropic-compatible.md: subscription
// billing with a gateway variable set in the process environment sends the
// operator's claude.ai OAuth material to whatever host it names. Under api
// billing the same variables are the supported way to reach an
// Anthropic-compatible endpoint, so the check only notes that
// budget.maxUsd cannot be trusted behind a custom base URL.
func claudeEnvironmentCheck(cfg *config.Config) Check {
	check := Check{Name: "claude environment"}
	if cfg.Billing == "api" {
		check.OK = true
		if base := os.Getenv("ANTHROPIC_BASE_URL"); base != "" {
			check.Detail = "custom base URL: " + hostOnly(base) + "; budget.maxUsd cannot be trusted"
		}
		return check
	}

	var set []string
	for _, name := range claudeGatewayEnvVars {
		if os.Getenv(name) != "" {
			set = append(set, name)
		}
	}
	if len(set) == 0 {
		check.OK = true
		return check
	}
	check.Detail = strings.Join(set, ", ") + " set with billing: subscription; Sirdar will strip these from the agent's environment"
	return check
}

// hostOnly returns just the host portion of a URL, so a doctor report never
// repeats a full base URL that might carry embedded credentials or a path
// meant to stay private.
func hostOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// sourceChecks reaches each configured source the cheapest way that proves
// the credentials and the transport work: describe for an adapter, and a
// lookup of an id that cannot exist for Zoho Desk, where "not found" is the
// answer that proves the token is good.
func sourceChecks(ctx context.Context, cfg *config.Config) []Check {
	var checks []Check
	for _, s := range []struct {
		role string
		sc   *config.SourceConfig
	}{{"tracker", cfg.Sources.Tracker}, {"helpdesk", cfg.Sources.Helpdesk}} {
		if s.sc == nil {
			checks = append(checks, Check{Name: "sources." + s.role, OK: true, Detail: "not configured"})
			continue
		}
		name := fmt.Sprintf("sources.%s (%s)", s.role, s.sc.Adapter)
		checks = append(checks, checkSource(ctx, cfg, name, s.sc)...)
	}
	return checks
}

// checkSource returns every check one configured source is worth. Most
// sources are one line; an OAuth-authenticated Zoho Desk is two, because
// the grant it refreshes with is worth reporting on its own.
func checkSource(ctx context.Context, cfg *config.Config, name string, sc *config.SourceConfig) []Check {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()

	switch sc.Adapter {
	case "exec":
		command := ExpandCommand(cfg, sc.Command)
		client, err := plugin.Start(ctx, command, io.Discard)
		if err != nil {
			return []Check{{Name: name, Detail: err.Error()}}
		}
		defer client.Close()
		d, err := client.Describe(ctx)
		if err != nil {
			return []Check{{Name: name, Detail: err.Error()}}
		}
		detail := fmt.Sprintf("%s v%s roles=%v", d.Name, d.Version, d.Roles)
		if probeErr := trackerProbe(ctx, client, d); probeErr != "" {
			return []Check{{Name: name, Detail: detail + " — " + probeErr}}
		}
		return []Check{{Name: name, OK: true, Detail: detail}}

	case "zohodesk":
		ts, err := ZohoTokenSource(sc, config.Resolver{Keychain: KeychainFor()})
		if err != nil {
			return []Check{{Name: name, Detail: err.Error()}}
		}

		var checks []Check
		if rt, ok := ts.(*zohodesk.RefreshingToken); ok {
			// The refresh grant gets its own line: it is the part an
			// unattended run depends on, and a Desk call that happens to
			// succeed does not tell the operator the grant is still good
			// for the next one.
			checks = append(checks, oauthCheck(ctx, rt))
		}
		return append(checks, deskProbe(ctx, name, sc, ts))

	case "zendesk", "freshdesk", "helpscout", "intercom", "hubspot":
		return []Check{builtinHelpdeskProbe(ctx, name, sc)}

	case "jira", "linear", "azdo", "rally":
		return []Check{builtinProbe(ctx, name, sc)}

	default:
		return []Check{{Name: name, Detail: fmt.Sprintf("unknown adapter %q", sc.Adapter)}}
	}
}

// builtinProbe builds a built-in tracker adapter with the credentials the
// config names and makes it call Ping: one authenticated round trip against
// the cheapest endpoint the API has, which is what proves the base URL, the
// credential and the network all work before a run spends an agent session
// finding out otherwise.
func builtinProbe(ctx context.Context, name string, sc *config.SourceConfig) Check {
	tracker, _, err := newBuiltinTracker(sc, config.Resolver{Keychain: KeychainFor()})
	if err != nil {
		return Check{Name: name, Detail: err.Error()}
	}
	p, ok := tracker.(pinger)
	if !ok {
		return Check{Name: name, OK: true, Detail: "configured (" + builtinEndpoint(sc) + ")"}
	}
	if err := p.Ping(ctx); err != nil {
		return Check{Name: name, Detail: err.Error()}
	}
	return Check{Name: name, OK: true, Detail: "reachable (" + builtinEndpoint(sc) + ")"}
}

// builtinHelpdeskProbe builds a built-in helpdesk adapter (zendesk,
// freshdesk, helpscout, intercom, hubspot) with the credentials the config
// names and calls its Ping: one authenticated round trip proving the base
// URL/domain, the credential and the network all work. The detail names
// who the connection authenticates as — an email for Zendesk basic auth,
// "oauth" for a bearer token, the account domain for Freshdesk, the kind
// of grant for the three fixed-host vendors — never the secret itself.
func builtinHelpdeskProbe(ctx context.Context, name string, sc *config.SourceConfig) Check {
	hd, err := newBuiltinHelpdesk(sc, config.Resolver{Keychain: KeychainFor()})
	if err != nil {
		return Check{Name: name, Detail: err.Error()}
	}
	p, ok := hd.(pinger)
	if !ok {
		return Check{Name: name, OK: true, Detail: "configured"}
	}
	if err := p.Ping(ctx); err != nil {
		return Check{Name: name, Detail: err.Error()}
	}
	return Check{Name: name, OK: true, Detail: "reachable as " + helpdeskAuthWho(sc)}
}

// helpdeskAuthWho names who a built-in helpdesk connection authenticates
// as, for the doctor report. It never returns any part of the credential.
func helpdeskAuthWho(sc *config.SourceConfig) string {
	switch sc.Adapter {
	case "zendesk":
		if sc.Email != "" {
			return sc.Email
		}
		return "oauth"
	case "freshdesk":
		return sc.Domain
	case "helpscout":
		// Help Scout's client-credentials app has no account identifier to
		// show: the reachable row itself is the proof the pair minted a
		// token and the token was accepted.
		return "the Help Scout app"
	case "intercom":
		return "the workspace access token"
	case "hubspot":
		return "the private app token"
	default:
		return "configured"
	}
}

// probeKey is a tracker key no tracker has. Looking it up is the cheapest
// call that needs a credential, which is the point: describe does not, so
// an adapter whose token command is broken answers describe perfectly
// while being unable to list a single ticket.
const probeKey = "SIRDAR-DOCTOR-PROBE-0"

// probeTimeout caps the probe on its own, inside the check's budget, so a
// slow tracker is reported as slow rather than as the whole check running
// out of time.
const probeTimeout = 20 * time.Second

// trackerProbe fetches a key that cannot exist and reports the problem
// when the adapter answers with anything other than "not found" — an auth
// error means the credential is wrong, and an internal error means the
// token command failed. It returns "" when the adapter is healthy, and
// says nothing about an adapter that serves no tracker role.
func trackerProbe(ctx context.Context, client *plugin.Client, d plugin.Describe) string {
	tracker := false
	for _, role := range d.Roles {
		if role == "tracker" {
			tracker = true
		}
	}
	if !tracker {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	_, err := client.Get(ctx, probeKey)
	var serr *source.Error
	if !errors.As(err, &serr) {
		return ""
	}
	if ctx.Err() != nil {
		return fmt.Sprintf("a tracker.get probe did not answer within %s, so a run's first fetch may not either", probeTimeout)
	}
	switch serr.Code {
	case source.Auth, source.Internal:
		return "a tracker.get probe failed with " + string(serr.Code) + ": " + serr.Message
	default:
		return ""
	}
}

// mcpCheck reports which MCP servers a session will be able to reach. A
// workspace .mcp.json is the good case: the run sees those servers and no
// others. With mcp.workspaceOnly set and no such file the session is
// started against an empty config, so it has no MCP tools at all — safe,
// but it silently costs the run every datasource the playbooks name, which
// is worth saying here rather than leaving to an events log.
func mcpCheck(cfg *config.Config) Check {
	check := Check{Name: "mcp", OK: true}
	path := filepath.Join(cfg.Root, ".mcp.json")
	switch {
	case !cfg.WorkspaceOnlyMCP():
		check.Detail = "mcp.workspaceOnly is off: every user-level MCP server is visible to the agent"
	case cfg.MCPConfigPath() != "":
		check.Detail = path + " — the session sees these servers only"
	default:
		check.Detail = "no workspace .mcp.json: the agent will have no MCP tools; add the servers the playbooks need to " + path
	}
	if len(cfg.Permissions.MCP) > 0 {
		check.Detail += fmt.Sprintf("; permissions.mcp allows %d pattern(s)", len(cfg.Permissions.MCP))
	} else {
		check.Detail += "; permissions.mcp is empty, so write-shaped MCP tools are denied by name"
	}
	return check
}

// fetchCheck reports where a session may fetch a URL from. An empty list
// is the default and is safe — nothing is fetchable — but a run that then
// refuses every documentation page the playbooks point at is worth
// explaining here rather than one denial at a time in an events log.
func fetchCheck(cfg *config.Config) Check {
	check := Check{Name: "fetch", OK: true}
	if len(cfg.Permissions.Fetch) == 0 {
		check.Detail = "permissions.fetch is empty: no web fetch is allowed, on any provider"
		return check
	}
	check.Detail = "permissions.fetch allows " + strings.Join(cfg.Permissions.Fetch, ", ")
	return check
}

// oauthCheck performs one refresh and reports the access token it got and
// how long that token is good for. The token itself is never printed.
func oauthCheck(ctx context.Context, rt *zohodesk.RefreshingToken) Check {
	_, ttl, err := rt.TokenWithExpiry(ctx)
	if err != nil {
		return Check{Name: "zoho oauth", Detail: err.Error()}
	}
	return Check{
		Name: "zoho oauth",
		OK:   true,
		// Rounded: the sub-second drift between minting the token and
		// measuring it is not something to report to three decimals.
		Detail: fmt.Sprintf("access token obtained, expires in %ds", int(ttl.Round(time.Second).Seconds())),
	}
}

// deskProbe looks up a ticket id that cannot exist. "Not found" is the
// answer that proves the transport and the credentials work, since the API
// had to authenticate the request before it could reject the id.
func deskProbe(ctx context.Context, name string, sc *config.SourceConfig, ts zohodesk.TokenSource) Check {
	_, err := zohodesk.New(sc.BaseURL, sc.OrgID, ts).Get(ctx, "0")
	var serr *source.Error
	switch {
	case err == nil:
		return Check{Name: name, OK: true, Detail: sc.BaseURL}
	case errors.As(err, &serr) && serr.Code == source.NotFound:
		return Check{Name: name, OK: true, Detail: sc.BaseURL}
	default:
		return Check{Name: name, Detail: err.Error()}
	}
}

// notesCheck proves the notes directory exists and takes writes, since a
// run that discovers otherwise has already spent an agent session.
func notesCheck(cfg *config.Config) Check {
	dir := cfg.ExpandPath(cfg.Notes.Dir)
	check := Check{Name: "notes.dir", Detail: dir}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		check.Detail = err.Error()
		return check
	}
	f, err := os.CreateTemp(dir, ".sirdar-doctor-*")
	if err != nil {
		check.Detail = err.Error()
		return check
	}
	name := f.Name()
	f.Close()
	if err := os.Remove(name); err != nil {
		check.Detail = err.Error()
		return check
	}
	check.OK = true
	return check
}

// templatesCheck renders every note template against the built-in sample,
// so a broken override is caught here rather than after a run.
func templatesCheck(cfg *config.Config) Check {
	dir := ""
	detail := "embedded defaults"
	if cfg.Notes.Templates != "" {
		dir = cfg.ExpandPath(cfg.Notes.Templates)
		detail = dir
	}
	if err := (note.Renderer{TemplatesDir: dir}).Check(); err != nil {
		return Check{Name: "notes.templates", Detail: err.Error()}
	}
	return Check{Name: "notes.templates", OK: true, Detail: detail}
}
