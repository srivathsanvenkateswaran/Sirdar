package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/config"
	"github.com/srivathsanvenkateswaran/sirdar/internal/note"
	"github.com/srivathsanvenkateswaran/sirdar/internal/provider"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/plugin"
	"github.com/srivathsanvenkateswaran/sirdar/internal/source/zohodesk"
)

func init() { commands["doctor"] = cmdDoctor }

// doctorTimeout caps each source check, so one unreachable adapter or API
// cannot hang the whole report.
const doctorTimeout = 30 * time.Second

// cmdDoctor checks everything a run depends on and prints one line per
// check. It exits 1 when any check failed, so it can gate a CI job.
func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor", stderr, "usage: sirdar doctor")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}

	cfg, ok := loadWorkspace(stderr)
	if !ok {
		return exitUsage
	}
	stderr = synced(stderr)

	ctx := context.Background()
	checks := []provider.Check{{Name: "config", OK: true, Detail: filepath.Join(cfg.Root, ".sirdar", "config.yaml")}}
	checks = append(checks, providerChecks(ctx, cfg)...)
	checks = append(checks, sourceChecks(ctx, cfg, stderr)...)
	checks = append(checks, mcpCheck(cfg))
	checks = append(checks, notesCheck(cfg), templatesCheck(cfg))

	failed := 0
	for _, c := range checks {
		mark := "[OK]"
		if !c.OK {
			mark = "[!!]"
			failed++
		}
		if c.Detail == "" {
			fmt.Fprintf(stdout, "%s %s\n", mark, c.Name)
			continue
		}
		fmt.Fprintf(stdout, "%s %s — %s\n", mark, c.Name, c.Detail)
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "\n%d of %d checks failed\n", failed, len(checks))
		return 1
	}
	return 0
}

func providerChecks(ctx context.Context, cfg *config.Config) []provider.Check {
	p, err := providerFor(cfg.Provider)
	if err != nil {
		return []provider.Check{{Name: "provider", Detail: err.Error()}}
	}
	binary := cfg.Providers.Claude.Path
	if cfg.Provider == "codex" {
		binary = cfg.Providers.Codex.Path
	}
	if binary != "" {
		binary = cfg.ExpandPath(binary)
	}
	return p.Doctor(ctx, binary)
}

// sourceChecks reaches each configured source the cheapest way that proves
// the credentials and the transport work: describe for an adapter, and a
// lookup of an id that cannot exist for Zoho Desk, where "not found" is the
// answer that proves the token is good.
func sourceChecks(ctx context.Context, cfg *config.Config, stderr io.Writer) []provider.Check {
	var checks []provider.Check
	for _, s := range []struct {
		role string
		sc   *config.SourceConfig
	}{{"tracker", cfg.Sources.Tracker}, {"helpdesk", cfg.Sources.Helpdesk}} {
		if s.sc == nil {
			checks = append(checks, provider.Check{Name: "sources." + s.role, OK: true, Detail: "not configured"})
			continue
		}
		name := fmt.Sprintf("sources.%s (%s)", s.role, s.sc.Adapter)
		checks = append(checks, checkSource(ctx, cfg, name, s.sc, stderr)...)
	}
	return checks
}

func checkSource(ctx context.Context, cfg *config.Config, name string, sc *config.SourceConfig, stderr io.Writer) []provider.Check {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()

	switch sc.Adapter {
	case "exec":
		command := expandCommand(cfg, sc.Command)
		client, err := plugin.Start(ctx, command, stderr)
		if err != nil {
			return []provider.Check{{Name: name, Detail: err.Error()}}
		}
		defer client.Close()
		d, err := client.Describe(ctx)
		if err != nil {
			return []provider.Check{{Name: name, Detail: err.Error()}}
		}
		detail := fmt.Sprintf("%s v%s roles=%v", d.Name, d.Version, d.Roles)
		if probeErr := trackerProbe(ctx, client, d); probeErr != "" {
			return []provider.Check{{Name: name, Detail: detail + " — " + probeErr}}
		}
		return []provider.Check{{Name: name, OK: true, Detail: detail}}

	case "zohodesk":
		ts, err := zohoTokenSource(sc, config.Resolver{Keychain: keychainFor()})
		if err != nil {
			return []provider.Check{{Name: name, Detail: err.Error()}}
		}

		var checks []provider.Check
		if rt, ok := ts.(*zohodesk.RefreshingToken); ok {
			// The refresh grant gets its own line: it is the part an
			// unattended run depends on, and a Desk call that happens to
			// succeed does not tell the operator the grant is still good
			// for the next one.
			checks = append(checks, oauthCheck(ctx, rt))
		}
		return append(checks, deskProbe(ctx, name, sc, ts))

	default:
		return []provider.Check{{Name: name, Detail: fmt.Sprintf("unknown adapter %q", sc.Adapter)}}
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

// oauthCheck performs one refresh and reports the access token it got and
// how long that token is good for. The token itself is never printed.
func oauthCheck(ctx context.Context, rt *zohodesk.RefreshingToken) provider.Check {
	_, ttl, err := rt.TokenWithExpiry(ctx)
	if err != nil {
		return provider.Check{Name: "zoho oauth", Detail: err.Error()}
	}
	return provider.Check{
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
func deskProbe(ctx context.Context, name string, sc *config.SourceConfig, ts zohodesk.TokenSource) provider.Check {
	_, err := zohodesk.New(sc.BaseURL, sc.OrgID, ts).Get(ctx, "0")
	var serr *source.Error
	switch {
	case err == nil:
		return provider.Check{Name: name, OK: true, Detail: sc.BaseURL}
	case errors.As(err, &serr) && serr.Code == source.NotFound:
		return provider.Check{Name: name, OK: true, Detail: sc.BaseURL}
	default:
		return provider.Check{Name: name, Detail: err.Error()}
	}
}

// mcpCheck reports which MCP servers a session will be able to reach. A
// workspace .mcp.json is the good case: the run sees those servers and no
// others. Without one there is nothing to restrict the session to, so it
// inherits every server the operator has configured for themselves —
// including whatever write tools those carry — and that is worth saying
// out loud rather than discovering in an events log.
func mcpCheck(cfg *config.Config) provider.Check {
	check := provider.Check{Name: "mcp", OK: true}
	path := filepath.Join(cfg.Root, ".mcp.json")
	switch {
	case !cfg.WorkspaceOnlyMCP():
		check.Detail = "mcp.workspaceOnly is off: every user-level MCP server is visible to the agent"
	case cfg.MCPConfigPath() != "":
		check.Detail = path + " — the session sees these servers only"
	default:
		check.Detail = "warning: no " + path + ", so every user-level MCP server is visible to the agent"
	}
	if len(cfg.Permissions.MCP) > 0 {
		check.Detail += fmt.Sprintf("; permissions.mcp allows %d pattern(s)", len(cfg.Permissions.MCP))
	} else {
		check.Detail += "; permissions.mcp is empty, so write-shaped MCP tools are denied by name"
	}
	return check
}

// notesCheck proves the notes directory exists and takes writes, since a
// run that discovers otherwise has already spent an agent session.
func notesCheck(cfg *config.Config) provider.Check {
	dir := cfg.ExpandPath(cfg.Notes.Dir)
	check := provider.Check{Name: "notes.dir", Detail: dir}
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
func templatesCheck(cfg *config.Config) provider.Check {
	dir := ""
	detail := "embedded defaults"
	if cfg.Notes.Templates != "" {
		dir = cfg.ExpandPath(cfg.Notes.Templates)
		detail = dir
	}
	if err := (note.Renderer{TemplatesDir: dir}).Check(); err != nil {
		return provider.Check{Name: "notes.templates", Detail: err.Error()}
	}
	return provider.Check{Name: "notes.templates", OK: true, Detail: detail}
}
